// load/traffic.js — the shop's traffic: Kazu's k6 template (DESIGN.md §4.3),
// edited the way a customer would. It sends load and never judges: there are
// no thresholds, so k6 exits 0 whatever the responses were.
//
// Env vars:
//   KAZU_SEED       set by Kazu; keys and amounts derive from it, so base and head send the same sequence
//   KAZU_DURATION   set by Kazu; how long to send
//   FLOW            weighted mix of flows, e.g. "checkout:7,browse:3"
//   RATE            iterations per second (default 100)
//   DUPLICATE_RATE  fraction of orders submitted twice at once with the same key, like a double-click
//   CHECKOUT_URL    default http://checkout:8080
import http from "k6/http";
import exec from "k6/execution";

const SEED = __ENV.KAZU_SEED || "0";
const RATE = Number(__ENV.RATE || 100);
const DUPLICATE_RATE = Number(__ENV.DUPLICATE_RATE || 0);
const CHECKOUT = __ENV.CHECKOUT_URL || "http://checkout:8080";

export const options = {
  scenarios: {
    traffic: {
      executor: "constant-arrival-rate", // keeps sending when the system slows
      rate: RATE,
      timeUnit: "1s",
      duration: __ENV.KAZU_DURATION || "60s",
      preAllocatedVUs: RATE * 2,
      maxVUs: RATE * 20,
    },
  },
};

// Real clients give up eventually; k6's default of 60 s would hold VUs far longer.
const PARAMS = { timeout: "10s", headers: { "Content-Type": "application/json" } };

// A number in [0, 1) that depends only on the seed, the iteration and a label.
function unit(n, label) {
  let h = 0x811c9dc5; // FNV-1a
  for (const c of `${SEED}:${n}:${label}`) {
    h ^= c.charCodeAt(0);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h / 2 ** 32;
}

// About 70% of orders under 1,000 cents, 30% at or above, 1% over the fraud limit.
function amount(n) {
  const u = unit(n, "amount");
  const v = unit(n, "value");
  if (u < 0.01) return 100001 + Math.floor(v * 100000);
  if (u < 0.3) return 1000 + Math.floor(v * 99000);
  return 100 + Math.floor(v * 900);
}

const FLOWS = {
  checkout(n) {
    const key = `${SEED}-${n}`;
    const req = ["POST", `${CHECKOUT}/orders`, JSON.stringify({ amount_cents: amount(n) }),
      { ...PARAMS, headers: { ...PARAMS.headers, "Idempotency-Key": key } }];
    const res = unit(n, "duplicate") < DUPLICATE_RATE ? http.batch([req, req])[0] : http.request(...req);
    const id = res.status && res.status < 300 ? res.json("id") : null;
    if (id) http.get(`${CHECKOUT}/orders/${id}`, PARAMS);
  },
  browse() {
    http.get(`${CHECKOUT}/orders?status=paid`, PARAMS);
  },
};

// "checkout:7,browse:3" → cumulative weights.
const MIX = (() => {
  const parts = (__ENV.FLOW || "checkout:7,browse:3").split(",").map((p) => {
    const [name, w] = p.split(":");
    if (!FLOWS[name]) throw new Error(`unknown flow ${name}`);
    return [name, Number(w || 1)];
  });
  const total = parts.reduce((s, [, w]) => s + w, 0);
  let acc = 0;
  return parts.map(([name, w]) => [name, (acc += w) / total]);
})();

export default function () {
  const n = exec.scenario.iterationInTest;
  const u = unit(n, "flow");
  const [name] = MIX.find(([, cum]) => u < cum) || MIX[MIX.length - 1];
  FLOWS[name](n);
}

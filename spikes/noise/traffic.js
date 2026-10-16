// Noise-spike load: constant arrival rate, weighted checkout/browse mix,
// seeded so every run sends the same sequence. Never judges responses.
import http from 'k6/http';
import exec from 'k6/execution';

const BASE = __ENV.CHECKOUT_URL || 'http://checkout:8080';
const SEED = parseInt(__ENV.SEED || '1', 10);
const params = { timeout: '10s' };

export const options = {
  summaryTrendStats: ['avg', 'min', 'max', 'p(50)', 'p(95)', 'p(99)'],
  scenarios: {
    load: {
      executor: 'constant-arrival-rate',
      rate: parseInt(__ENV.RATE || '50', 10),
      timeUnit: '1s',
      duration: __ENV.DURATION || '60s',
      preAllocatedVUs: 50,
      maxVUs: 500,
    },
  },
};

// mulberry32: a small deterministic PRNG, one stream per iteration.
function rng(seed) {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// ~70% under 1,000 cents, ~29% from 1,000 up to the fraud limit, ~1% above it.
function amount(r) {
  const u = r();
  if (u < 0.7) return 100 + Math.floor(r() * 900);
  if (u < 0.99) return 1000 + Math.floor(r() * 99001);
  return 100001 + Math.floor(r() * 100000);
}

export default function () {
  const i = exec.scenario.iterationInTest;
  const r = rng(SEED * 1000003 + i);
  if (r() < 0.7) {
    const res = http.post(`${BASE}/orders`, JSON.stringify({ amount_cents: amount(r) }), {
      ...params,
      headers: { 'Idempotency-Key': `noise-${SEED}-${i}`, 'Content-Type': 'application/json' },
      tags: { name: 'POST /orders' },
    });
    const id = res.status >= 200 && res.status < 300 && res.json('id');
    if (id) http.get(`${BASE}/orders/${id}`, { ...params, tags: { name: 'GET /orders/{id}' } });
  } else {
    http.get(`${BASE}/orders?status=paid`, { ...params, tags: { name: 'GET /orders?status=paid' } });
  }
}

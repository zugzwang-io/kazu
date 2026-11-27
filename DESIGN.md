# Kazu — design

**Kazu, by Zugzwang.** Resilience regression testing for every release.

On every PR or release candidate, Kazu answers one question: *did this change make us more fragile than the last release?* It injects seeded faults into your system, checks invariants — built-in ones, and ones you write over your data, your latency and your own telemetry — and compares the result against the base branch.
 
---

## 1. Identity

| Thing | Value | Status |
|---|---|---|
| Umbrella | **Zugzwang** — parent brand for all projects (Kazu, Tessera canvas, Latch, ...) | Decided |
| Product | **Kazu** | Decided |
| CLI | `kazu` | Decided |
| GitHub | `github.com/zugzwang-io/kazu` (existing org, home for every project) | Decided |
| Packages | CLI: `kazu-cli` on npm and PyPI · SDK: `kazu-sdk` on npm, PyPI and crates.io. Bare `kazu` is taken on PyPI (unrelated biomedical library), so the suffixed names are the only fully consistent option | Claim now |
| Install | `brew install kazu` · `curl \| sh` · `npm i -g kazu-cli` · `pip install kazu-cli` → all install the `kazu` command. No `cargo install`: crates.io builds from source and the CLI is a Go binary | Decided |
| SDK imports | Python `kazu_sdk` · TypeScript `kazu-sdk` · Rust `kazu_sdk` · Go `github.com/zugzwang-io/kazu/sdk/go` | — |
| Defensive claims | Publish placeholder `kazu` on npm and `kazu` + `kazu-cli` on crates.io that point to the install docs, so nobody squats the names | Claim now |
| npm scope | `@zugzwang-io` is free if a scope is ever wanted | — |
| Docs | `zugzwang-io.github.io/kazu` (free); a custom domain later, if ever | — |
| Config file | `kazu.yaml` | Decided |
| Env vars | `KAZU_SEED` (set on the traffic container) | |

Package names differing from the command is normal (`awscli` → `aws`, `azure-cli` → `az`, `@angular/cli` → `ng`); people remember the brand and the command.

**Before the open-source launch:** run a USPTO trademark search for "Kazu" (classes 9 and 42) and check `kazu.dev` or a Zugzwang domain at a registrar. Keep the product name out of the run-record schema and the wire protocol, so a rename only touches the surface (and the config filename, which can be migrated by reading both names for a release).
 
---

## 2. Goal and principles

**Goal:** catch resilience regressions before release, in CI, for application teams, with a tool good enough that its author uses it during release windows.

**Why not unit or integration tests?** They check one component's logic. The failures Kazu targets only exist when the whole system runs under a fault: a retry that doubles a charge, retries that multiply load on a struggling dependency, a connection pool that drains on an error path, a service that crashes on an unexpected error, an order stuck after a crash. Kazu checks them four ways — built-in checks that need no configuration, invariants over the system's data, latency measured at its proxies, and the system's own OpenTelemetry metrics — and turns "watch the dashboard after deploy" into a gate before merge.

**Principles**
1. **A declarative fault-injection and assertion runtime, nothing more.** No state-reset semantics, no knowledge of the customer's domain, no claims about production health.
2. **The customer provides a complete, isolated system; Kazu owns the wires and the checks.** The system is a compose file or rendered Kubernetes manifests, with no outbound internet, and services reach each other by name. The same packaging contract as Antithesis.
3. **Fresh environment per trial.** Clean state comes from the customer's own images and seed scripts, never from Kazu.
4. **Follow Antithesis by default.** Where they have made a design decision for the same problem, we take it. Deviations are deliberate, listed in §6.7, and flagged when they are proposed.
5. **Fail only on facts; every failure has a way forward in the user's config.** Heuristics may warn. A check passes if Kazu can verify that head is no worse than base, and otherwise fails; a scenario passes if and only if it provably ran and every check passed (§4.6). "No worse" has three proofs: head passed the check, base failed it, or both sides were measured and the comparison puts head within tolerance. An unknown is always resolved against head. So a missing signal (a fault that never landed, a metric with no data, a check whose `requires:` was never met, a comparison too noisy to decide) is never a pass, and a comparison too noisy to decide is reported as underpowered, never guessed to be a regression. Every failure has a way forward in the user's config: a severity switch, `blocking: false` on the scenario, or an edit to the check in head's `kazu.yaml` (fix it, remove it, or `defaults.off`). Results that say nothing about the system (*trial incomplete*) are fixed by running again. Nothing in Kazu can leave a customer blocked with no way forward.
6. **Never gate correctness; never cap the customer's own compute.** The free tier is complete, correct and as fast as their hardware allows.
7. **Charge for what costs us money or what an organization needs:** hosting, AI features, cross-team coordination and governance.
8. **Once free, always free.** No relicensing, no moving features behind a paywall later.
9. **Small to start.** A system file, a traffic image and one scenario are enough: the default checks give a real verdict without a single user-written check.
10. **Every failure prints the command that reproduces it.**
---

## 3. User journey (reference persona)

Priya, a backend developer at a 40-person fintech. She owns checkout, uses GitHub Actions and docker-compose. Payments calls Stripe; checkout calls an internal auth service. In her compose file both are already mocks (stripe-mock and a small auth stub), because local development needed them anyway. Her services already emit OpenTelemetry metrics to a collector in the compose file.

| Moment | Need | Kazu |
|---|---|---|
| Day 0, 5 min | Try it with no commitment | `brew install kazu && kazu init` reads compose and drafts `kazu.yaml`, with candidate edges from service names it finds in env values and the collector it finds, for her to review |
| Day 0, 10 min | Know what Kazu can do to each edge | `kazu doctor` lists per declared edge which faults apply (TLS edges: network faults only, unless a test CA is configured), which services aren't sealed (outbound internet), and which services sent telemetry |
| Day 0, 15 min | Proof | `kazu run db-blip` gives a real verdict locally from the default checks alone: crashes, exits, user-facing errors, latency and recovery |
| Day 1 | Domain checks | Check templates (idempotency, no double effect, eventual completion) for the SDK image in her language, plus one-line metric checks over her existing telemetry (retries per request, pool wait) |
| Day 1 | Every PR | `kazu init --ci github` writes the workflow; runs on their runners |
| Week 2 | Trust | A PR regresses, the comment names the cause, `kazu replay` reproduces it |
| Month 1+ | Org adoption | Platform lead wants standard suites, cross-team contracts, policy → paid |
 
---

## 4. Interface

### 4.1 Components and the trial lifecycle

Four components, each with one job, one entry point and one exit signal. **Everything Kazu runs is an image**, so a runner needs only Docker and the `kazu` binary.

| Component | Lives in | Owns | Entry | Exit |
|---|---|---|---|---|
| **System** | `docker-compose.yml` (or rendered manifests) | Services, mocks, schema, seed data. All setup, as one-shot jobs. Nothing Kazu-specific, and no load | Kazu starts it, fresh for every trial | **Ready:** every healthcheck or readiness probe passes and every one-shot job has exited 0. After the trial Kazu deletes it; there is no cleanup step |
| **Traffic** | A compose-style service definition (`image`, `command`, `volumes`, `environment`) in `kazu.yaml`; a scenario can override it | What requests to send, how fast, for how long. It generates load and never judges | Kazu starts it once the system is ready, on the system's network, with `KAZU_SEED` and the scenario's env vars | **The process exits.** Exit 0 means the load ran (a default check). It decides how long it runs (the k6 template: `DURATION`, 120 s by default); Kazu stops a trial still running after 30 min. If it stops before every fault fires and recovery is seen, the scenario is *not exercised* |
| **Checks** | The `checks:` list in `kazu.yaml`: one-line checks, plus code checks in a compose-style service using a Kazu SDK image | Judging, and nothing else | Kazu starts it per trial, outside the system, and talks to it over stdin/stdout; it calls each check at its point in the lifecycle | **Pass or fail, with a message**, per check |
| **`kazu.yaml`** | The repo | Declarations only: system, traffic, edges, connections, scenarios, checks, suites, gate settings | — | — |

`kazu.yaml` has no lifecycle, hooks or scripts. The lifecycle belongs to Kazu, is the same for every trial, and is not configurable:

```
up system ─▶ ready ─▶ start traffic (t=0) ─▶ faults fire on schedule ─▶ traffic exits ─▶ @after checks ─▶ @eventually checks (retry until pass or timeout) ─▶ delete system
```

Fault triggers count from traffic start, so the time before the first fault is the pre-fault period that the recovery check compares against.

**No run tagging.** Every trial is a fresh, sealed system, so all of its data belongs to that trial and checks need no run-id filters.

**Base and head.** One `kazu.yaml` describes both. They differ only in the variables the system file interpolates, usually image tags:

```
kazu run --suite pr --base TAG=main-9c41e2 --head TAG=pr-a1f3c9
```

The variables are usually image tags, so base and head are **images already published by CI**: Kazu pulls them and never builds. If an image doesn't exist (main's build hasn't published yet, say), the run stops with an error naming the missing image rather than building it.

Kazu takes `kazu.yaml`, traffic and checks from the head commit and pins every image by digest once per run: traffic, checks, and each system service on both base and head. Only the system differs between base and head, so a changed load script can't masquerade as a regression, and a PR can't weaken a check to hide its own regression: a deleted check is gone from both sides. The pins and the resolved base and head variables go in the run record, which `kazu replay` reruns from.

The cost: a PR that changes the system's schema and updates a check to match runs the new check against the old schema on base. A check that **errors** on base (rather than failing an assertion), for example on a missing column, is reported as **incompatible with base** and judged on head: a pass/fail check that head passed passes, still labelled incompatible with base, and one that head didn't pass fails (§4.6).

### 4.2 Walkthrough

Priya's checkout service, end to end.

**`docker-compose.yml` — the system.** Hers already; nothing Kazu-specific.

```yaml
services:
  postgres:
    image: postgres:16
    environment: { POSTGRES_PASSWORD: dev }
    healthcheck: { test: pg_isready -U postgres, interval: 1s }
 
  migrate:                                   # setup: a one-shot job
    image: checkout:${TAG:-latest}
    command: ["./checkout", "migrate"]
    depends_on: { postgres: { condition: service_healthy } }
 
  stripe-mock:                               # external API, mocked
    image: stripe/stripe-mock
 
  payments:
    image: payments:${TAG:-latest}
    environment: { STRIPE_URL: http://stripe-mock:12111 }
 
  checkout:
    image: checkout:${TAG:-latest}
    environment:
      DATABASE_URL: postgres://postgres:dev@postgres/checkout
      PAYMENTS_URL: http://payments:8081
      OTEL_EXPORTER_OTLP_ENDPOINT: http://otel-collector:4317
    depends_on: { migrate: { condition: service_completed_successfully } }
    healthcheck: { test: curl -f localhost:8080/healthz, interval: 1s }
 
  otel-collector:
    image: otel/opentelemetry-collector
```

**`load/checkout.js` — traffic.** Sends requests, never judges. She started from the k6 template `kazu init` wrote (§4.3) and edited it.

```js
import http from "k6/http";
import exec from "k6/execution";
 
export const options = {
  scenarios: { orders: {
    executor: "constant-arrival-rate",       // keeps sending even when checkout slows
    rate: 10, timeUnit: "1s", preAllocatedVUs: 50,
    duration: __ENV.DURATION || "120s",     // a scenario changes it through env
  }},
};
 
export default function () {
  const n = exec.scenario.iterationInTest;
  const key = `${__ENV.KAZU_SEED}-${n}`;     // same request sequence in base and head
  const send = () => http.post("http://checkout:8080/orders",
    JSON.stringify({ amount_cents: 1000 + (n % 50) * 100 }),
    { headers: { "Idempotency-Key": key, "Content-Type": "application/json" } });
 
  send();
  if (__ENV.DOUBLE_SUBMIT === "1") send();   // her own knob; Kazu attaches no meaning to it
}
```

**`checks/checkout.py` — checks.** Judges, never sends load.

```python
from kazu_sdk import after, eventually, run
 
@after
def no_double_charge():
    dupes = run.sql("postgres", "select order_id from charges group by order_id having count(*) > 1")
    assert not dupes, f"{len(dupes)} orders charged twice, e.g. {dupes[:3]}"
 
@eventually(within="60s")
def orders_settle():
    pending = run.sql_value("postgres", "select count(*) from orders where status = 'pending'")
    assert pending == 0, f"{pending} orders still pending"
```

**`kazu.yaml` — declarations.** What to break and what to judge.

```yaml
system: docker-compose.yml
 
traffic:                                    # a compose-style service
  image: grafana/k6:1.0.0
  command: [run, /load/checkout.js]
  volumes: [./load:/load:ro]                # her script, in the stock k6 image
 
edges:                                      # the only places faults can go
  traffic -> checkout: http                 # user-facing: the default checks read this
  checkout -> payments: http
  checkout -> postgres: tcp
  payments -> stripe-mock: http
 
connections:                                # what run.sql("postgres") connects to
  postgres: postgres://postgres:dev@postgres/checkout
 
telemetry: { collector: otel-collector }
 
scenarios:
  slow-payments:
    faults:
      - { edge: checkout -> payments, fault: latency, delay: 1500ms, jitter: 200ms, after: 20s }
  db-blip:
    faults:
      - { service: postgres, fault: down, after: 20s, for: 15s }
  stripe-errors:
    faults:
      - { edge: payments -> stripe-mock, fault: errors, rate: 10%, code: 503, after: 20s }
  double-click:
    env: { traffic: { DOUBLE_SUBMIT: "1" } }
 
checks:
  - image: ghcr.io/zugzwang-io/kazu-python:1   # her code checks
    volumes: [./checks:/checks:ro]
  - metric:                                    # a one-line check
      query: sum(increase(payment_attempts_total[5m])) / sum(increase(orders_created_total[5m]))
      max: 3
 
suites:
  pr: [slow-payments, db-blip, double-click]
```

**What `kazu run --suite pr` does**, for each scenario, base and head back to back on the same runner:
1. Starts the system with three edits: a proxy per declared edge, a hosts-file alias on each caller (§6.6), and `otel-collector` aliased to Kazu's own receiver (§6.8).
2. Waits for ready.
3. Starts traffic with `KAZU_SEED` and the scenario's env vars. The fault clock starts.
4. Fires faults on schedule. Proxies count traffic and latency; telemetry streams in.
5. Traffic exits.
6. Runs the `@after` checks, then retries the `@eventually` checks for up to 60 s; runs the default checks and the one-line checks.
7. Deletes the system.
   Then it compares base and head per scenario and prints the verdict (§4.8).

**In CI**, one step in the workflow `kazu init --ci github` writes:

```yaml
- run: kazu run --suite pr --base TAG=${{ github.event.pull_request.base.sha }} --head TAG=${{ github.sha }} --junit reports/kazu.xml --markdown $GITHUB_STEP_SUMMARY
```

Her compose file never mentions Kazu, and her load script reads one variable Kazu sets (`KAZU_SEED`) and one knob of its own, `DURATION`: 120 s unless a scenario's `env` changes it. `db-blip`'s outage ends at 35 s, leaving 85 s to watch recovery.

### 4.3 Traffic

Traffic can be any image. **k6** is the one supported tool: Kazu ships a template for it, documents it and uses it in every example and CI template.

**Why k6.** Its `constant-arrival-rate` executor keeps sending at the configured rate when the system slows down; tools that wait for each response before sending the next (Locust's default, most hand-written loops) send less during an outage and make it look milder than it is. It scripts multi-step flows in JavaScript, has HTTP, gRPC and WebSockets built in, reads env vars for the seed and scenario knobs, runs from one official image (`grafana/k6`), and is the most widely used open-source load tool, so many teams already have scripts. Gatling has similar open-model support but is a JVM tool with a Scala/Java DSL; Vegeta and wrk2 send at a constant rate but can't pass a value from one response into the next request.

**Licence.** k6 is AGPL-3.0. Kazu never embeds or links it; it only runs the `grafana/k6` image as a separate container, so Kazu stays Apache 2.0.

**The template.** `kazu init` writes `load/traffic.js`, a complete k6 script (kept in the repo at `templates/k6/traffic.js`). Simple cases edit the list of requests at the top; anything else edits the JavaScript below it. There is no separate Kazu requests format.

```js
// load/traffic.js — written by `kazu init`; edit freely.
// Each iteration runs the selected flow once, in order. Placeholders in strings:
//   {{seed}}  KAZU_SEED, the same in base and head
//   {{n}}     the iteration number
//   {{vars.NAME}}  a value saved from an earlier response in the same iteration
const FLOWS = {
  checkout: [
    { method: "POST", url: "http://checkout:8080/orders",
      headers: { "Idempotency-Key": "{{seed}}-{{n}}" },
      body: { amount_cents: 1500 },
      save: { order_id: "id" } },                     // response JSON field → vars.order_id
    { method: "GET", url: "http://checkout:8080/orders/{{vars.order_id}}" },
  ],
  browse: [
    { method: "GET", url: "http://checkout:8080/products?page={{n}}" },
  ],
};
// FLOW picks one flow ("browse") or a weighted mix ("checkout:7,browse:3"); a scenario sets it with env.
const RATE = Number(__ENV.RATE || 10);               // iterations per second
 
export const options = {
  scenarios: { traffic: {
    executor: "constant-arrival-rate",              // keeps sending when the system slows
    rate: RATE, timeUnit: "1s", preAllocatedVUs: RATE * 5,
    duration: __ENV.DURATION || "120s",              // a scenario changes it through env
  }},
  // No thresholds: k6 exits non-zero when a threshold fails, and traffic never judges.
};
 
// ...below: placeholder filling, `save` handling, the default function (about 30 lines)
```

**Env vars Kazu sets on traffic**

| Variable | Meaning |
|---|---|
| `KAZU_SEED` | The run's seed. Derive keys and IDs from it so base and head send the same sequence |

Plus whatever the scenario's `env:` sets for `traffic`.

**How long traffic runs is the script's business** ([ADR-0006](docs/adr/0006-traffic-decides-its-length.md)): the template reads `DURATION` (120 s by default) and a scenario changes it through `env`. Kazu stops a trial whose traffic is still running after 30 minutes. A fault that hasn't fired by the time traffic exits, or ended too late to watch recovery, makes the scenario *not exercised*; a longer `DURATION` fixes it.

**Different traffic per scenario.** Base and head always get the same traffic for a given scenario; scenarios can differ from each other in two ways:
- **Knobs, through `env`.** The scenario sets variables the script reads: `env: { traffic: { FLOW: browse, RATE: "50" } }`. The template's named flows make switching flow one variable.
- **A replacement, through `traffic:`.** A scenario's own `traffic:` block is merged over the default definition, so a soak can run a different script, or another tool's image entirely:
  ```yaml
  soak:
    traffic: { command: [run, /load/soak.js] }
    faults:
      - { service: payments, fault: crash, after_requests: 500, for: 5s }
  ```

### 4.4 `kazu.yaml` reference

```yaml
system: docker-compose.yml            # or: { manifests: ./k8s/rendered }  (single-node K3s)
 
traffic:                              # a compose-style service; Kazu starts it after ready (k6 supported, §4.3)
  image: grafana/k6:1.0.0
  command: [run, /load/traffic.js]
  volumes: [./load:/load:ro]          # optional; your scripts in a stock image
  environment: { FLOW: checkout }     # optional; defaults for every scenario
 
edges:                                # faults apply only to declared edges
  traffic -> checkout: http           # from traffic: "user-facing"
  checkout -> payments: http
  checkout -> postgres: tcp
  payments -> fraud: grpc
  checkout -> auth:
    protocol: http
    tls: { ca_cert: ./certs/test-ca.pem, ca_key: "${env:KAZU_TEST_CA_KEY}" }
 
connections:                          # what run.sql / run.http reach, by name
  postgres: postgres://postgres:dev@postgres/checkout
 
telemetry:                            # not in the schema yet (joins with Phase 6); default: set OTEL_EXPORTER_OTLP_ENDPOINT on every service
  collector: otel-collector           # or alias an existing collector service to Kazu's receiver
 
auto_scenarios: true                  # not in the schema yet (joins with Phase 6); one generated scenario per service and per declared edge (§4.6)
 
scenarios:
  slow-payments:
    faults:
      - { edge: checkout -> payments, fault: latency, delay: 300ms, jitter: 50ms, after: 20s }
  db-blip:
    underpowered: warn                # not in the schema yet (joins with Phase 5); this scenario's latency is too noisy on our runners to gate on
    faults:
      - { service: postgres, fault: down, after: 20s, for: 15s }   # every declared edge into postgres
    checks:                           # this scenario only, on top of the top-level checks
      - recovers: { service: checkout, within: 30s }
  cascading:
    faults:
      - { name: slow-pay, edge: checkout -> payments, fault: latency, delay: 300ms, after: 20s, for: 30s }
      - { edge: checkout -> postgres, fault: down, from: slow-pay, after: 5s, for: 10s }           # 5s after slow-pay ends
      - { edge: checkout -> payments, fault: down, from: { start: slow-pay }, after: 10s, for: 5s } # overlaps it
  payments-dies-midflight:
    faults:
      - { service: payments, fault: crash, after_requests: 200, for: 5s }
  double-click:
    blocking: false                   # not in the schema yet (joins with Phase 5); the race in payments is on main already; remove this line when it is fixed
    env: { traffic: { DOUBLE_SUBMIT: "1" } }   # env vars per service, passed through untouched
    checks:
      - name: charged_once
        sql: { on: postgres, query: "select order_id from charges group by order_id having count(*) > 1" }
        requires:                       # all must hold in a trial for the check to be eligible in it (§4.6)
          - sql: { on: postgres, query: "select 1 from charges limit 1" }
          - requests: { edge: checkout -> payments, min: 50 }
      - name: payments_p99
        no_regression: { service: payments, measure: p99, within: 10% }
        requires:
          - requests: { service: payments, min: 200 }
  browse-under-outage:
    env: { traffic: { FLOW: browse, RATE: "50" } }
    faults:
      - { edge: checkout -> payments, fault: down, after: 20s, for: 20s }
  soak:
    traffic: { command: [run, /load/soak.js] }  # merged over the default traffic definition
    env: { traffic: { DURATION: 180s } }        # the script decides how long it runs
    faults:
      - { service: payments, fault: crash, after_requests: 500, for: 5s }
  rare-errors:
    not_exercised: warn               # a 1% fault may not fire on light traffic
    faults:
      - { edge: payments -> fraud, fault: errors, rate: 1%, code: UNAVAILABLE, after: 20s }
 
defaults:                             # not in the schema yet (joins with Phase 5); built-in checks (§4.6), all on unless tuned or switched off
  latency_within: 20%                 # user-facing p99, head vs base
  error_rate_within: 0.5pp            # user-facing error rate, head vs base
  recovers_within: 60s                # user-facing error rate back to pre-fault level after each fault ends
  languages: [go]                     # opt-in log checks: go, java, rust
  off: []                             # e.g. [latency] to drop one entirely
 
checks:
  - image: ghcr.io/zugzwang-io/kazu-python:1     # code checks (§4.6); a compose-style service
    volumes: [./checks:/checks:ro]
  - recovers: { service: payments, within: 30s }
  - slo: { service: checkout, measure: error_rate, max: 1% }    # absolute; fine for rates, risky for latency
  - metric:
      query: histogram_quantile(0.99, sum by (le) (rate(db_pool_acquire_seconds_bucket[5m])))
      within: 50%                     # relative to base; or max/min for an absolute bound
 
suites:                               # named sets of scenarios; `kazu run --suite pr`
  pr: [slow-payments, db-blip, double-click]
  release: all
 
gate:                                 # not in the schema yet (joins with Phase 5)
  fail_on: regression                 # or: violation
  trials: auto                        # adaptive; or a fixed number
  not_exercised: fail                 # a fault that never landed, or a check whose requires: was never met, is not a pass
  no_data: fail                       # a metric with no samples is not a pass
  underpowered: fail                  # a comparison too noisy to decide is not a pass; a scenario can set underpowered: warn
```

**What the schema carries today** ([ADR-0002](docs/adr/0002-kazu-yaml-schema-shape.md)). The published schema holds the top-level keys Kazu builds next; `telemetry`, `auto_scenarios`, `defaults` and `gate` join it when their phase is built. Inside each key the shape is a good-faith estimate of where the interface lands, and it can change until the first public release, breaking changes included. Before that release the schema is trimmed to what Kazu implements.

**Shapes the reference doesn't show.** `command` takes a list only (compose's string form would need its shell-splitting rules); `environment` takes compose's map or `NAME=value` list. The `http` check is `{ on, path, method, expect: { status, body } }`, `method` defaulting to `GET` and `body` a substring. `slo.max` is a duration for latency measures and a percentage for `error_rate`. `metric` takes `max`/`min` or a relative `within`, not both, plus `no_data: warn`. Every check entry takes `not_exercised: warn`. Measures are `p50`, `p90`, `p95`, `p99`, `error_rate`. A scenario can be empty (`name: {}`): no faults. Names are letters, digits, `.`, `_` and `-`, starting with a letter or digit. A bare number where a duration or percentage goes is an error. The schema's `$id` is `https://zugzwang-io.github.io/kazu/kazu.schema.json`.

**Edges.** Declared, never inferred. Protocol is `tcp` (network faults and connection-level measurement only), `http` or `grpc` (adds request-level faults, request-count triggers and per-request latency and errors). Kazu does not sniff or verify protocols; if an edge is declared `http` but carries something else, HTTP faults on it simply don't land and the landed check reports it. Edges from `traffic` are **user-facing**: the default checks read them.

**Scenario `env`.** Sets env vars on any service, including `traffic`, for that scenario only. Kazu passes them through untouched and attaches no meaning to them; which knobs a team's code exposes is the team's business.

**Compose-style blocks.** `traffic`, a scenario's `traffic` override and the code-checks entry in `checks:` take the compose service keys `image`, `command`, `volumes` and `environment`, with the same meaning, so there is no Kazu-specific way to mount a script.

**Every key is structured.** Faults and checks are plain YAML maps validated by a published JSON schema, which gives editors autocomplete and catches mistakes before a run. There is no free-text mini-language to learn; the only parsed strings are edge names (`a -> b`), durations and percentages.

**One-shot jobs** are recognised from the system file itself (anything another service waits on with `service_completed_successfully`, or a Kubernetes Job), so their exit is expected, not a crash.

**Secrets** are references (`${env:NAME}` or a file path), resolved at run time, never written to run records or logs. In CI they come from the CI's own secret store. The proxy needs secrets only to terminate TLS for request-level faults (a test CA) or to present a client certificate to an mTLS upstream; network faults never need any.

### 4.5 Faults

A scenario's `faults:` is a list. Each fault is a map:

```yaml
- edge: checkout -> payments    # or service: payments (exactly one of the two)
  fault: latency                # the kind, from the table below
  delay: 300ms                  # fields for that kind
  jitter: 50ms
  after: 20s                    # when it starts: seconds from traffic start, or after_requests: N
  for: 15s                      # optional: how long it lasts; default until traffic ends
  name: slow-pay                # optional: lets other faults count from this one
  from: other-fault             # optional: after counts from that fault's end instead of traffic start
```

| `fault` | Layer | Target | Fields | Example |
|---|---|---|---|---|
| `latency` | network | edge or service | `delay`, `jitter`, `direction` (`request` or `response`; default `response`) | `{ fault: latency, delay: 300ms, jitter: 50ms }` |
| `down` | network (refuse new connections and cut existing ones) | edge or service | — | `{ fault: down, after: 30s, for: 15s }` |
| `partition` | network (hold both ways, deliver on heal) | edge or service | — | `{ fault: partition, for: 20s }` |
| `reset` | network | edge or service | `rate`, `direction` (`request` or `response`; default `request`) | `{ fault: reset, rate: 2% }` |
| `bandwidth` | network | edge or service | `limit`, `direction` (`request`, `response` or `both`; default `both`, each direction with its own `limit`) | `{ fault: bandwidth, limit: 64kbps }` |
| `errors` | HTTP/gRPC | http/grpc edge | `rate`, `code` | `{ fault: errors, rate: 10%, code: 503 }` |
| `crash` | process | service | — (`for` is how long it stays down before Kazu restarts it; without `for`, until traffic exits) | `{ fault: crash, after_requests: 200, for: 5s }` |
| `pause` | process | service | — | `{ fault: pause, for: 10s }` |
| `cpu`, `memory` | resource | service | `load` | `{ fault: cpu, load: 90% }` |
| `disk_full` | resource | service | — | `{ fault: disk_full, after: 30s }` |

- `edge: a -> b` means one declared edge. `service: b` means **every declared inbound edge** (network faults) or the process (process and resource faults). Callers whose edges aren't declared are untouched; `kazu doctor` lists them.
- `after` counts from traffic start; `after_requests` counts requests through the proxy.
- **Relative triggers** ([ADR-0004](docs/adr/0004-relative-fault-triggers.md)). `from:` makes `after` count from another fault in the same scenario: its name for its end, `{ start: name }` for its start, or a list for the latest of several (a join). Without `from`, faults are independent and overlap by their windows. Anchors are *planned* times: every wall-clock fault's start and end resolve when the plan is built, so base and head get the same schedule; an anchor on a request-count fault resolves at run time, through the controller (ADR-0006). Using a fault's end needs that fault to have a `for`; loops are rejected. There are no triggers on system events ("once payments is healthy"): the schedule would then depend on the system, so base and head could get different timelines.
- **`direction`** ([ADR-0008](docs/adr/0008-network-faults-have-a-direction.md)). `request` is the bytes the caller sends toward the callee, following the edge's `a -> b` arrow; `response` is the bytes the callee sends back. The names follow HTTP's meaning and work the same on a `tcp` edge; on a `service: b` target, `direction` applies to every declared inbound edge of `b`. `latency` adds `delay` once per call, in the one chosen direction: each chunk is due at the later of its read time plus `delay` plus jitter and the previous chunk's due time, so chunks never reorder and a multi-chunk answer is late by about `delay`. The default `response` means the callee acts on time and the answer is late; `request` delivers the request late, so the callee may act after the caller has given up (the caller's FIN follows the delayed bytes, and a server that cancels work when its client disconnects may cancel that request). There is no `both` on `latency` (slowing both streams is `bandwidth`) or on `reset` (it would differ from `request` only on a connection that already had a request in flight when the fault started). The schema rejects `direction` on `partition` (both ways by definition; a one-way hold is `latency` with a long delay), `down` (a state of the connection, and refused has to keep meaning "definitely not processed"), `errors` (never forwards anything) and the process and resource faults (no stream). Response loss has exactly two forms: silent (`latency` past the caller's timeout) and with an error (`reset` with `direction: response`).
- **A network fault applies to live connections from the moment it starts**, or a pooled keep-alive connection rides through it and the scenario tests nothing. `down` refuses new connections and resets live ones.
- **`partition` stops reading, in both directions**, and builds no queue of its own. On a live connection it holds the chunk its copy loop was already reading, if any, plus whatever later stages already had (chunks waiting out a `latency` delay, writes queued by `bandwidth`, an `errors` answer not yet written), then stops reading, which leaves the rest in the operating system's TCP buffers: a small amount waits there, a large amount fills them and the sender's writes block, as they do while TCP retransmits across a real split. A connection accepted during the window reads nothing and the callee isn't dialled until the window ends. On heal the proxy forwards what it held and reads again, so everything is delivered late and nothing is lost, which is how a real partition behaves for any split shorter than TCP's give-up time. A caller that gave up during the window still has its request delivered on heal, followed by its FIN, so several same-key retries sent during a partition arrive together: a direct test of the callee's deduplication. Two known differences from a real split: delivery is immediate on heal rather than on TCP's next retransmit, and a new connection completes its handshake before hanging, because connect-time faults need a packet-filter rule in the proxy's network namespace (backlog).
- **Overlapping faults on one edge** compose through one pipeline per direction: `down` → `partition` (read gate) → `reset` → `errors` → `latency` → `bandwidth` → `partition` (write gate) → write. `down` gates the connection itself, and anything a partition held goes with it. The partition's write gate holds everything the proxy would otherwise write in that direction, bytes already inside `latency` or `bandwidth` and the proxy's own `errors` answers included: nothing crosses a partition. `reset` comes before `errors`, so a request drawn by both is cut, not answered (`ECONNRESET`, so the shop leaves the order `pending`); a `response` reset cuts before anything in the response direction is written, the proxy's own answers included. `errors` answers a drawn request itself and never forwards it; its answer enters the response direction at the `latency` stage, so `latency` (`response`) plus `errors` gives a slow error, which is how a struggling dependency usually fails. A per-scenario override of the stage order is in the post-v1 backlog.
- **Two faults of the same kind on the same target are rejected when the config loads, unless they provably can't overlap.** For network and HTTP/gRPC faults the target is an edge (`service: b` counts as each of `b`'s inbound edges), the same kind in different directions included (or `bandwidth` with `request` plus `bandwidth` with `response` would be a second way to write `both`); for process and resource faults (`crash`, `pause`, `cpu`, `memory`, `disk_full`) it is the service. Two latencies or two error rates have no single obvious meaning, so the user picks one or sequences them. Two faults whose windows both resolve to planned wall-clock times are compared as half-open intervals; a fault with no `for` runs until traffic exits. A fault that depends on a request count, directly or through `from:`, is treated as running forever, so it overlaps, unless one fault is sequenced after the other's end through `from:` (chains and joins count; a link through `{ start: X }` counts only if X is itself sequenced after the first fault's end). A `crash` with `for` is down for that long, then restarted; its window covers Kazu's own action, not the service's recovery.
- `crash` kills the container in place and lets it restart in the same container (compose) or the same pod (Kubernetes). Deleting a pod would change IPs and filesystems and make base and head behave differently.
- Request-count triggers land at the same point in every run; wall-clock triggers drift more.
- **Request-count triggers need an `http` or `grpc` edge to count on.** On a `tcp` edge the proxy sees only bytes, so there are no requests to count; `after_requests` there is rejected when the config is loaded. On a `service` target it counts requests on the service's declared HTTP/gRPC inbound edges, and is rejected if it has none.
- Probabilities use a PRNG seeded from `(run seed, edge, request number)`; `reset` draws per request on `http` and `grpc` edges and cuts a drawn request's connection before it is forwarded (`request`) or when the callee starts answering it (`response`), whatever the client's pooling. The one exception is `reset` on a `tcp` edge, where there are no requests: it draws each connection once from `(run seed, edge, connection number)` and cuts it on its next chunk in the selected direction. Connection numbers follow accept order, which depends on the client's pooling, so that draw is deterministic only while base and head open connections the same way.
- HTTP/gRPC faults on a TLS edge require a stub or a test CA. `kazu doctor` reports this per edge.
  **Fault layer priority** (application teams first): network proxy → HTTP/gRPC proxy → process/resource → in-code SDK fault points → kernel-level (partitions via netem) last.

### 4.6 Checks

Kazu checks four kinds of evidence:
- **Built-in default checks**, with no configuration (below).
- **The system's data**, through SQL and HTTP (did anything get charged twice, does every order settle).
- **Latency and errors measured by Kazu's proxies** on declared HTTP/gRPC edges (§6.8).
- **The system's own OpenTelemetry metrics**, received by Kazu during the trial (§6.8).
#### Default checks

Modelled on Antithesis's default properties, which check setup and correctness with no user assertions. Kazu's defaults know nothing about the customer's domain, so they hold for any system.

| Group | Check | Kind | Origin |
|---|---|---|---|
| Setup | The system became ready | fact | Antithesis |
| Setup | Every configured fault landed (§6.6) | fact | Antithesis ("fault injector was started"), made per fault |
| Setup | Traffic outlasted the last fault plus the recovery window | fact | Kazu |
| Setup | Telemetry arrived, when metric checks use it (§6.8) | fact | Kazu |
| Correctness | No unexpected crashes: no process died from a program-error signal that Kazu didn't send | fact | Antithesis |
| Correctness | No unexpected container exits: no out-of-memory kills, non-zero exits or crash loops, other than Kazu's own `crash` faults and one-shot jobs | fact | Antithesis |
| Correctness | Traffic exited 0 | fact | Antithesis (test commands finish with zero exit code) |
| Regression | User-facing error rate doesn't regress (`error_rate`) | statistical | Kazu (deviation: base vs head) |
| Regression | User-facing p99 doesn't regress (`latency`) | statistical | Kazu (deviation: base vs head) |
| Regression | User-facing error rate returns to its pre-fault level within `recovers` after each fault ends | statistical | Kazu (deviation: base vs head) |
| Language (opt-in) | No Go panics, data races or fatal errors; no Java uncaught exceptions; no Rust panics, from logs | fact | Antithesis |

"User-facing" means the declared edges from `traffic`. Without any, the regression defaults have nothing to read and say so in `kazu doctor`. Every default can be tuned or switched off in `defaults:`. The default tolerances are policy — the regression accepted when the user hasn't said — not noise estimates.

#### Auto scenarios

With `auto_scenarios: true`, Kazu generates scenarios as well as checks:
- `auto-crash-<service>` for every service (except one-shot jobs and traffic): `{ fault: crash, after: 20s, for: 5s }`;
- `auto-down-<caller>-<callee>` for every declared edge (except edges from traffic): `{ fault: down, after: 20s, for: 15s }`.
  They can be listed in suites by name, and `auto` in a suite means all of them.

They assert nothing new: the default checks above are the assertions (no crash beyond the injected one, traffic exits 0, user-facing errors recover, user-facing error rate and p99 no worse than base). Together they answer "does this change make us worse at surviving any single crash or dependency outage?" with no scenarios written. Off by default, because they multiply trials: six services and six edges are twelve scenarios.

#### `no_regression`

`no_regression: { service, measure, within: N% }` compares head against base instead of against a fixed number: across the scenario's trial pairs, head's value must not be worse than base's by more than N%. The user chooses N; it is the regression they are willing to accept.

It exists because a regression gate handles pass/fail checks naturally (passed on base, failed on head), but a measurement like p99 has no pass/fail on its own, and absolute thresholds flap on noisy runners. Using base as the yardstick turns a number into a verdict.

**Noise is measured in every run, never assumed.** Different services, endpoints, commits and runners flap differently, so there is no global noise baseline. For each scenario and each measurement, the spread of the head−base differences across this run's trial pairs is the noise estimate. Kazu decides whether head is worse than base by more than N% given that estimate, adding pairs while it is undecided. No extra base-vs-base runs are needed; the paired differences carry the noise. If this run's own noise is too large to resolve N% within the trial budget, the result is **underpowered**: Kazu reports the noise it measured and what would resolve it (a wider tolerance, more trials, a quieter runner), and never guesses. Underpowered fails by default (`gate.underpowered`); a scenario can downgrade it with `underpowered: warn`.

Noise seen in earlier runs of the same scenario may choose a *starting* trial count, so a known-noisy scenario starts with more pairs. It never decides a verdict.

It works on any proxy measurement (`p99`, `error_rate`) and, as `within:`, on any `metric:` query.

**Latency: relative by default.** Shared CI runners are noisy, so absolute latency thresholds flap. Each base and head trial runs back to back on the same machine, which makes relative comparisons stable. Docs, templates and the defaults lead with the relative form; absolute `slo:` remains for rates and for teams with dedicated runners.

#### The `checks:` list

One list holds every user check, and runs in every scenario. A scenario can add its own `checks:` (same entries), run on top of the top-level list in that scenario only, for checks that mean something only under its faults; names can't repeat a top-level check's. Each entry may have a `name:` (used in output; one-line checks are otherwise named after their kind and position for now; stable ids for history are decided with run records), may carry `requires:` (below), and is one of:
- **`image:`** — code checks, as a compose-style service using a Kazu SDK image (below);
- **`sql:`** — a query that selects violations; it passes when it returns no rows. Kazu reads only the first few rows (for the failure message) under a statement timeout. Exact-row comparisons belong in a code check;
- **`http:`** — a request and the expected status or body;
- **`recovers:`** — a service's error rate back to its pre-fault level `within` a duration;
- **`no_regression:`** — a proxy measurement no worse than base `within` a tolerance;
- **`slo:`** — a proxy measurement under an absolute `max`;
- **`metric:`** — a PromQL query over the trial's proxy and OTLP metrics, with `max`/`min` or a relative `within`.

**`requires:`.** Every check has two parts: did enough happen for its verdict to mean anything (eligibility), and given that it did, did it pass (the decision). A `sql:` check that selects violations passes on no rows, so "no order charged twice" passes when nothing was charged at all. `requires:` states eligibility on the check it applies to: a list of conditions that all must hold in a trial for the check to be eligible there. Each condition is one of:
- **`sql: { on, query }`** holds when the query returns at least one row. Kazu stops reading after the first row, under the same statement timeout as a `sql:` check; `on` names a postgres connection.
- **`requests: { edge | service, min }`** holds when the proxy received at least `min` requests between traffic start and traffic exit. It reads the counter `after_requests` uses: every request the proxy received counts, forwarded or answered by a fault; a request is one HTTP request or one gRPC call (a stream counts once); a connection refused by `down` carries no request. `edge: a -> b` counts that one edge, which must be `http` or `grpc` (a `tcp` proxy sees bytes); `service: b` sums `b`'s `http`/`grpc` inbound edges, ignores its `tcp` ones, and is rejected when the config is loaded if it has none. `min` is an integer of at least 1. Counts are per trial, never summed across trials.

Conditions are evaluated once per trial, after every check in the trial has finished, `@eventually` retries included. A `sql` condition therefore sees the trial's final state, which may be up to a minute after the check decided; that is intended, so write conditions about things that persist, not about a transient state such as a pending order. A `requests` condition reads the counts snapshotted at traffic exit. On an `image:` entry, `requires:` applies to every code check in the image and is evaluated once. In an ineligible trial the check's decision still runs and is reported for information, but none of that check's results from that trial count: not its decision, its no data, or its cut-short window. A condition that can't be evaluated (a syntax error, a statement timeout, a refused connection) is a check error, handled like an error in the check itself, never "no rows"; otherwise a broken query would quietly become not exercised, or a pass under `warn`. There are no defaults: a check without `requires:` is always eligible, and the other missing-signal results (a fault that never landed, a `recovers:` window cut short by the end of traffic, no data on a `metric:`, underpowered) keep their rules inside an eligible trial.

**Verdict.** A check passes if Kazu can verify that head is no worse than base; otherwise it fails. A scenario passes if and only if it provably ran and every check passes ([ADR-0013](docs/adr/0013-a-check-passes-only-if-head-is-verifiably-no-worse-than-base.md)). An unknown is always resolved against head: head is what is under test, so wherever Kazu lacks a fact it takes the reading that is worse for head. The rule applies from the one-pair verdict onward. "No worse" can be shown in exactly three ways:
1. head passed the check, whatever base did;
2. base failed the check, whatever head did;
3. both sides were measured, and the comparison says head is within tolerance. This is how the relative checks work: `no_regression`, a `metric` with `within:`, and the regression defaults.

If none can be shown, the check fails. A pass/fail check offers (1) and (2) for its decision; a relative check offers only (3).

**A pass/fail check that fails in any head trial fails.** With several trial pairs, (1) needs head to pass in every trial where the check was eligible, and (2) needs base to fail in every trial where it was eligible. Kazu doesn't compare failure rates, so a pass/fail check is never undecided, and a failure on head is a plain failure unless base failed in every trial. The result name says which case it is:
- **REGRESSED**: base had no failing trial among those that could be judged, and head failed in at least one. The counts are shown (`0/5 → 4/5 trials failing`). A base trial that errored is unknown, so it is read as a pass;
- ***fails intermittently on head***: base failed in some trials but not all, and head is mixed too;
- ***fails on head***: base failed in some trials but not all, and head failed in every one.

No per-result switch governs these. The way forward, finest first: fix the flake; or remove the check from head's `kazu.yaml`, or switch a built-in off with `defaults.off`; and only then waive the scenario with `blocking: false` (below). The order matters because a top-level check or a correctness default runs in every scenario, and waiving every scenario turns the gate off.

**Eligibility and decision both have to hold.** Base and head trials are paired, as for every check. A check passes only if its eligibility and its decision are both shown no worse; the two comparisons are independent, and either can regress:
- never eligible in any trial, on either side: **not exercised**, naming the condition that was never met, decided per scenario;
- eligible in fewer head trials than base trials, including none: a candidate regression (head stopped doing something the check depends on), judged by the base-vs-head comparison on the eligible-trial counts (proof 3), whatever the decision says; an undecided comparison is **underpowered**; no fixed rule decides it. Base failing in every trial doesn't rescue a head that was never eligible;
- the decision is judged by the three proofs, over the trials where the check was eligible. A relative check's decision is compared over the pairs in which both sides were eligible; too few such pairs to decide is **underpowered**. A pass/fail check's decision is never underpowered;
- eligible on head but never on base (head started doing it) is not a regression on eligibility, and there is nothing on base to compare with. A pass/fail check that head passed passes by (1). One that head failed fails as ***new check failing***, not REGRESSED, with no switch of its own; the way forward is the code or the check, both in the PR. A relative check has no proof left and fails as ***no baseline***, whose severity comes from the `underpowered` switches;
- a trial in which the check errored, or its metric had no data, is unverifiable. On head it blocks (1): the check fails as **no data** (`gate.no_data`) or **check error**, unless it is a pass/fail check that base failed in every trial. On base it blocks (2): a pass/fail check that head didn't pass fails as **incompatible with base** when base errored in every trial (§4.1), or as no data when base had no samples; in a relative check a pair whose base measurement is missing is left out, which leaves fewer pairs to decide with, and no data on base in every trial is no data. A check error has no switch: the checks come from head's `kazu.yaml`, so the way forward is the check itself;
- with one base and one head trial per scenario (Phase 3, before the statistical verdict): eligible on base but not on head is REGRESSED; eligible on head but not on base is judged by the rule (head passing passes, head failing fails); eligible on neither is not exercised; eligible on both compares the decision as for any check. The statistical rules above replace this once the paired comparison lands;
- under `gate.fail_on: violation` head must pass every pass/fail check outright: the decision is judged in every head trial where the check is eligible, whatever base did, so proof (2) is dropped. A relative check has no outright pass, so it stays on proof (3). Eligibility is still compared as above: losing it is a regression, not a violation, and never having it is still not exercised;
- under adaptive trials, a check not yet eligible on either side leaves its scenario undecided, so pairs are added while the budget lasts; it is not exercised only once the budget is spent. More trials can't fix *no baseline* or *new check failing*, so pairs stop being added for that check once the minimum number of pairs shows it;
- the landed check (§6.6) and eligibility are independent: a trial whose fault didn't land still counts toward a check's eligibility, and the scenario is not exercised for the fault either way.

**Placement.** A top-level check runs in every scenario, so a top-level check with a `requires:` that some scenario's traffic can't meet is not exercised there by design. Put such checks in the `checks:` of the scenarios whose traffic meets them, and at the top level only when every scenario's traffic does. Downgrading a whole scenario to `warn` instead would also downgrade its landed check.

**Switches.** `not_exercised: warn` exists at three levels, the most specific winning: on the check entry (any kind), then the scenario, then `gate.not_exercised`. The check-level switch covers every not-exercised result of that check, a `recovers:` window cut short included; a candidate regression on eligibility counts is a regression, so `gate.fail_on` governs it like any other. `underpowered: warn` exists at two levels, the more specific winning: the scenario, then `gate.underpowered` (default `fail`); there is no per-check switch. It covers underpowered and no-baseline results, for the decision and the eligibility comparison alike.

**Roll-up.** A scenario provably ran when every configured fault landed and its trial pairs are complete: each pair has a base trial and a head trial that Kazu ran to the end. A trial Kazu itself failed to run (the driver errored, an image pull failed midway, the runner was lost) fails the scenario as ***trial incomplete***; it says nothing about the system, has no switch of its own, and the way forward is to run again. A scenario fails if any result is at `fail` severity: REGRESSED, fails on head, fails intermittently on head, new check failing, not exercised, no data, underpowered, no baseline, a check error, incompatible with base where head didn't pass, or trial incomplete. Every such result is listed. A result at `fail` severity is printed on a `✗` line; one downgraded to `warn` is printed on a `⚠` line and doesn't fail the scenario. No third outcome leaves a scenario green by default.

**`blocking: false`.** A scenario may carry `blocking: false` (the default is `true`). It still runs, and every result is computed and reported as usual, but it never fails the gate: the run's exit status and the status check ignore it. A run fails if a blocking scenario fails, or if the run itself failed (a config error, a planner rejection, a missing shard); a run with no blocking scenario says so in its output. Every output marks a non-blocking scenario (§4.8). It is the way forward when a scenario fails for a reason the PR can't fix, mainly flakiness already on main, and the one switch for results with none of their own: a mixed result on head, new check failing, trial incomplete and a check error. It waives the whole scenario, whatever the severities of its results; the scenario's `not_exercised: warn` and `underpowered: warn` stay as the finer switches, each downgrading one kind of result while a regression in the same scenario still blocks. Leaving a scenario out of a run altogether stays a `suites:` choice.
#### The checks image

For anything that needs code. Two decorators, both relative to traffic exiting:
- **`@after`** runs once, right after traffic exits.
- **`@eventually(within=...)`** is retried after traffic exits until it passes or the timeout expires. It runs where Antithesis's `finally_` does, after traffic has finished on its own, with Kazu doing the retrying.
```python
from kazu_sdk import after, eventually, run
 
@after
def retries_bounded():
    per_order = run.metrics("sum(increase(payment_attempts_total[5m])) / sum(increase(orders_created_total[5m]))")
    assert per_order <= 3, f"{per_order:.1f} payment attempts per order"
 
@eventually(within="60s")
def orders_settle():
    assert run.sql_value("postgres", "select count(*) from orders where status = 'pending'") == 0
```

There are no checks during the fault window (no `@during`): every failure the shop models is visible afterwards. It can be added if a real case needs it.

**SDK images.** Kazu publishes one image per SDK language with the SDK preinstalled (`kazu-python`, `kazu-node`, `kazu-go`). The customer's files are mounted in with `volumes:`, so the simple case needs no build; checks that need extra libraries use an image built `FROM` Kazu's.

**How SDKs stay thin:** Kazu starts the checks container with stdin and stdout attached and speaks JSON-RPC over them.
- Host → worker: `initialize`, `list` (names and timings), `check {name}`.
- Worker → host: `result {pass, message}`.
- Worker → host callbacks: `sql`, `http`, `metrics` (PromQL), `log`. **Kazu does the real work**, using `connections:` and its own metric store, so the checks container needs no database drivers, no HTTP clients, no credentials and no access to the system's network.
- **One Protobuf schema is the source of truth for the protocol.** `buf generate` produces the message types for every SDK language; messages travel as Protobuf's standard JSON encoding. Only the decorators and the stdio loop are hand-written per language, roughly 200–400 lines per SDK.
- Check files are found by extension in the mounted folders.
- SDK order: Python, TypeScript, Go → Java/Kotlin, Ruby → community.
### 4.7 CLI

```
kazu init [--ci github|gitlab|circleci|buildkite|jenkins]   # drafts kazu.yaml and load/traffic.js (k6); one-time, reviewed by a human
kazu doctor                      # per declared edge: which faults apply; undeclared callers; unsealed services;
                                 # which services sent telemetry; whether user-facing edges exist
kazu run [scenario] [--suite pr] --base VAR=value... --head VAR=value... [--seed N]
         [--shard i/N] [--coordinator addr] [--hosted]
         [--junit path] [--markdown path] [--json path] [--otlp endpoint] [--s3 bucket/prefix] [--webhook url] [--cloud]
kazu merge <results...>          # combine shard results into one verdict
kazu worker --coordinator <addr> # dynamic runner
kazu coordinator                 # self-hosted coordinator
kazu replay <run-id> [--trial N]     # run-id: the handle printed by every run, for its stored record
kazu diff <run-a> <run-b>
kazu explain <run-id>
```

Runners and output sinks are flags, not config: the CI templates written by `kazu init --ci` set them (shards from the CI's native parallelism, JUnit and Markdown to the CI's report locations). `--otlp` exports Kazu's own results and the trial's collected metrics to the customer's observability stack after the run; it is separate from the receiver that collects the system's telemetry during a trial.

**Distribution:** one static Go binary per platform, released with GoReleaser: GitHub releases, a Homebrew tap, a Docker image, a `curl | sh` installer and a GitHub Action. Thin `kazu-cli` wrappers carry the same binary to npm (per-platform `optionalDependencies` packages) and PyPI (per-platform wheels). crates.io holds name claims only. SDK images are published to GHCR.

### 4.8 What users see

```
kazu run  ·  suite pr  ·  base main@9c41e2 vs head a1f3c9  ·  adaptive trials
 
  slow-payments              ✓ pass       p99 612ms → 640ms (+4%, within 20%)  ·  proxy forwarding p99: base 40µs, head 42µs
  stripe-errors              ✗ REGRESSED  no_double_charge  0/5 → 4/5 trials failing  ·  proxy forwarding p99: base 38µs, head 2.1ms ▲
  db-blip                    ✓ pass       recovers in 11s → 9s  ·  proxy forwarding p99: base 41µs, head 39µs
  double-click               ✗ REGRESSED  default: unexpected crash  payments exited with SIGSEGV in 3/5 trials  ·  proxy forwarding p99: base 37µs, head 40µs
 
  ✗ stripe-errors: 3 orders charged more than once, e.g. [o_81, o_93, o_97]
    changed since base: payments (a1f3c9 "retry stripe call on 503")
    reproduce: kazu replay r_2fj9 --trial 2
```

Every scenario line shows **proxy forwarding p99** for base and for head ([ADR-0012](docs/adr/0012-build-our-own-proxy-and-measure-its-overhead.md); §6.8): the largest forwarding-overhead p99 among the scenario's edges and trials on that side, over chunks, not requests. With no samples it reads "no data", never 0. It is a maximum over trials, so it grows with the trial count and is not comparable between runs with different trial counts. A `▲` after a number marks a figure above the display threshold (1 ms p99 is ADR-0012's recommendation, for forwarding overhead and accept lag alike); when accept lag is the one over the threshold, the line names it. The marker is an informational heuristic: it never starts a line, is not a result, and changes no result and no exit code.

Every result that fails a scenario is listed on a `✗` line. A fault that never landed, a check whose `requires:` was never met, a metric with no data, a comparison too noisy to decide, a relative check with no baseline, a new check failing, or a check that fails on head while base is mixed looks like this:

```
  ✗ slow-payments: fault on checkout -> payments was not exercised
    no traffic reached the proxy; checkout may use an IP address or bypass the system resolver
  ✗ double-click: check "charged_once" was not exercised
    requires: "select 1 from charges limit 1" on postgres returned no rows in any of 5 base and 5 head trials; check that traffic creates charges
  ✗ db-blip: metric "payment attempts per order" has no data
    no samples for payment_attempts_total; check the service's OTLP exporter (kazu doctor)
  ✗ db-blip: latency within 1% is underpowered
    this run's noise on checkout p99 is ±14% after 12 trial pairs; widen the tolerance, allow more trials or use a quieter runner
    to stop this blocking: underpowered: warn on the scenario
  ✗ new-export: payments_p99 within 10% has no baseline on base
    this path is new in head; use an absolute check (slo:, sql:, http:) until it's on main
  ✗ new-export: new check failing: export_complete fails on head (5/5 trials) and was never eligible on base
    nothing on base to compare with; fix the code or the check, both are in this PR
  ✗ double-click: no_double_charge fails intermittently on head (2/10 trials; base 1/10)
    to stop this blocking: fix the flake, remove the check from kazu.yaml (or defaults.off for a built-in), or set blocking: false on the scenario
  ✗ soak: orders_settle fails on head (10/10 trials; base 3/10)
    to stop this blocking: fix the flake, remove the check from kazu.yaml (or defaults.off for a built-in), or set blocking: false on the scenario
```

A result downgraded to `warn` by its switch is printed on a `⚠` line and doesn't fail the scenario. So underpowered is `✗` by default and `⚠` only where a switch downgraded it:

```
  ⚠ rare-errors: fault on payments -> fraud was not exercised (not_exercised: warn)
    the fault drew no request; 1% errors on 240 requests
```

A scenario with `blocking: false` is marked on its line and never fails the gate (§4.6); a run with no blocking scenario says so:

```
  double-click  (non-blocking)   ✗ no_double_charge fails intermittently on head (2/10 trials; base 1/10)

  no scenario in this run is blocking; it cannot fail the gate
```

PR comment: same content, condensed, with a pass/fail status check. Every other output (`--json` and the run record, JUnit, Markdown, the PR comment and the status check) carries the non-blocking marker and the result names above.
 
---

## 5. Contract boundary

| Customer provides | Kazu provides |
|---|---|
| A complete system: a compose file, or rendered Kubernetes manifests for a single-node K3s cluster, with all setup as one-shot jobs | Starting it fresh per trial (compose, or K3s on the runner) and deleting it afterwards |
| Isolation: no outbound internet; external services as mocks inside the system (stripe-mock, LocalStack, stubs) | A proxy per declared edge, wired in by a per-caller hosts-file alias |
| Services that reach each other by name | Seeded fault scheduling and the landed check |
| Readiness: compose healthchecks or k8s readiness probes | The fixed trial lifecycle |
| A traffic image: the k6 template, an existing k6 script, or any other load tool | Default checks; per-edge latency and error metrics from the proxies |
| Checks: one-liners in `kazu.yaml`, and optionally files for a Kazu SDK image | SDK images, the check host and its `sql`/`http`/`metrics` callbacks |
| Optionally, OpenTelemetry metrics over OTLP (the standard SDK env var, or a collector service Kazu can alias) | An OTLP receiver and per-trial metric store with PromQL |
| Images for base and head, selected by variables the system file interpolates | Base-vs-head comparison, verdict, reports |
| A container engine (Docker Engine, Podman, OrbStack, Colima, or Docker Desktop under their own licence) | — |

This is Antithesis's packaging contract, so a system already packaged for Antithesis needs only a `kazu.yaml` (its client container can serve as the traffic image). Kazu never holds customer secrets beyond the references it resolves at run time, needs no access to private services, and doesn't know the data model.

Systems that are not containerized, or that depend on externally managed state (a shared database, a live cloud service), are out of scope: there is nothing fresh per trial and no single place to put a proxy.
 
---

## 6. Architecture

```
┌──────────────────────── Any runner: laptop, CI job, k8s pod, VM, hosted microVM ────────────────────────┐
│ kazu (single Go binary)                                                                                │
│ ├─ Config compiler ─ kazu.yaml + seed → pinned plan (scenarios × trials × {base, head})                 │
│ ├─ Env driver ────── compose, or K3s + rendered manifests; inserts proxies, per-caller aliases and      │
│ │                    the OTLP endpoint; waits for readiness; starts traffic; watches events and logs    │
│ ├─ Controller ────── pushes fault programs to proxies; process faults via Docker API / kubelet;          │
│ │                    event log; landed check                                                            │
│ ├─ Telemetry ─────── OTLP receiver + proxy counters → in-memory metric store per trial; PromQL engine   │
│ ├─ Check host ────── default checks; checks container over stdio JSON-RPC; sql/http/metrics callbacks  │
│ ├─ Verdict engine ── per-scenario statistical base-vs-head comparison with per-run noise estimates;     │
│ │                    underpowered results                                                               │
│ └─ Output sinks ──── stdout, JUnit, Markdown, JSON run record, OTLP, S3, webhook, cloud                  │
│        │ gRPC                    │ Docker API / Kubernetes API         ▲ OTLP                           │
│ ┌──────▼─────────── system under test (fresh per trial, sealed) ──────┼──────┐                           │
│ │ traffic ──(hosts: checkout→proxy)─▶ kazu-proxy ─▶ checkout           │      │  kazu-proxy: one per      │
│ │ checkout ─(hosts: payments→proxy)─▶ kazu-proxy ─▶ payments           │      │  declared edge; network + │
│ │ checkout ─(hosts: postgres→proxy)─▶ kazu-proxy ─▶ postgres           │      │  HTTP/gRPC modes; counts  │
│ │ worker ──────────────────────────────────────────▶ postgres (direct) │      │  traffic, latency, errors │
│ │ every service ─(OTLP; hosts: otel-collector→kazu)───────────────────┘      │                           │
│ └──────────────────────────────────────────────────────────────────────────────┘                           │
└───────────────────────────────┬─────────────────────────────────────────────────────────────────────────┘
                                │ outbound HTTPS only (optional) · Connect API
┌───────────────────────────────▼──────────────── Control plane (paid, Go) ───────────────────────────────┐
│ Control-plane API: before a run → policies, consumer invariants, trial-pair leases; after → results     │
│ Postgres (orgs, repos, runs, baselines, registry, policies, waivers, audit) · object storage (TTL)       │
│ Org-wide GitHub App (checks, comments, cross-repo triggers) · web UI · billing · hosted-lane scheduler   │
└──────────────────────────────────────────────────────────────────────────────────────────────────────────┘
```

### 6.1 Key decisions
- **Proxies evaluate their own fault programs.** The controller compiles a program per proxy and pushes it before traffic starts, so request-count triggers never race a network round trip. Cross-component faults (crash after N requests on an edge) go through the controller, which records the request number where the fault actually landed.
- **Fresh environment per trial.** No reset logic. Startup cost is paid with parallelism, not reuse.
- **Trial pairs are the unit of work.** One base trial and one head trial of the same scenario and seed, back to back on the same machine, so runner noise hits both equally. Workers lease pairs; an expired lease puts the pair back in the queue. The spread of differences across pairs is also each run's own noise estimate.
- **Every image is pinned once per run, for replay.** Before the first trial, every image is resolved to a digest (traffic, checks, and each system service on both base and head) and the mounted files are hashed. The same traffic and checks pins serve base and head, so only the system differs between the two. The run record is the plan (with those pins and the resolved base and head variables) plus the results (including each trial's metrics), which is exactly what replay needs. Every selected scenario runs on every run, base and head; Kazu never skips one as unchanged ([ADR-0010](docs/adr/0010-every-scenario-runs-on-every-run.md)).
- **Kazu knows which crashes are its own.** The controller records every process fault it injects, so the default crash and exit checks can tell an injected `crash` from a real one.
- **A fault that didn't land, a metric with no data and a comparison too noisy to decide each have a verdict: they fail by default** (§4.6, §6.6, §6.8).
- **The CLI is complete without the backend.** Baselines can live in CI artifacts. If the control plane is down, runs fall back to the customer's runners and still gate.
### 6.2 Runners (bring your own)
Any machine that can run `kazu worker` is a runner.
- **Static shards (any CI, nothing to integrate):** `kazu run --shard i/N` per job, `kazu merge` at the end. Templates for GitHub, GitLab, CircleCI, Buildkite, Jenkins.
- **Dynamic leasing:** workers pull the next trial pair from a coordinator: `kazu coordinator` self-hosted (free), or the control plane. Workers can be CI jobs, Kubernetes pods (Helm chart / Job template), VMs, laptops.
- **Hosted lanes (paid):** our workers in rented microVMs. Each environment boots once, is snapshotted after the ready-check, and every trial forks from the snapshot.
  The runtime is identical on every runner.

### 6.3 Determinism and the verdict
- Seeded fault schedules; logical (request-count) triggers preferred; per-edge seeded PRNG.
- The seed fixes the fault plan, not system behaviour. Remaining nondeterminism (scheduling, timing, noise) is handled with repeated trials and statistics.
- **Noise is estimated per run, per scenario, per measurement**, from the head−base differences across that run's trial pairs. There is no global noise baseline; services, endpoints, commits and runners each flap differently.
- Adaptive trial counts: stop early when clearly passing or clearly regressed; add pairs while undecided, up to a budget. A pass/fail check is never undecided, so trials stop at the minimum number of pairs unless a relative measure or an eligibility count in the scenario is still undecided. History from earlier runs of the same scenario may set the starting count, never the verdict.
- Per-scenario comparison of latency distributions and metric values, with false-discovery control across many scenarios and checks.
- If the budget runs out before the test can decide, the result is **underpowered**, with this run's measured noise and what would resolve it. It fails by default (§4.6).
- A pass/fail check that fails in any head trial fails, unless base failed in every trial. Kazu doesn't compare failure rates and doesn't accommodate flake (§4.6).
### 6.4 Scale

**CI wall time is the constraint** (target: PR gate in about 10 minutes). With fresh environments at roughly 100 s per trial run:

| Setup | 5 scenarios × 5 trials × 2 variants | Wall time |
|---|---|---|
| `local`, serial | 50 runs × 100 s | ~85 min |
| Static shards ×10 | | ~10–15 min |
| + adaptive trials | most scenarios need 3 trials | ~6–9 min |
| Hosted lanes ×25 with snapshot boot | boot ≈ 0 s after first | ~2–4 min |

On K3s, one cluster per runner is reused across trials with a fresh namespace per trial, so cluster boot is paid once per runner, not per trial.

Other limits: the system under test must fit on one runner (bigger systems: stubs or bigger self-hosted runners). Proxy overhead is measured and reported on every run ([ADR-0012](docs/adr/0012-build-our-own-proxy-and-measure-its-overhead.md)): the proxy's own processing, as forwarding overhead per chunk and accept lag per connection (§6.8). Not measured: anything before a read returns, the wait in the kernel's accept queue, the upstream dial, the extra hop itself (paid alike by base and head, and tracked by Kazu's own bench job), and a proxy waiting for a CPU. Per-trial metric stores are small (one trial's series for minutes) and discarded after the run record is written. The backend stores KB–MB per run and is not a scaling risk.

### 6.5 Tech stack

**Go everywhere we write code.** One language for a solo builder; the control plane imports the same packages as the binary (trial-pair leasing, run records) instead of reimplementing them.

| Part | Choice |
|---|---|
| Binary (CLI, proxy, worker, coordinator) | Go, single static binary. compose-go (the library Docker Compose uses), Docker Engine API client, client-go for Kubernetes, grpc-go |
| Container engine | Docker Engine API only, so Docker Engine, Podman, OrbStack and Colima all work. Kazu needs no Docker licence. Container events and logs feed the default crash, exit and language checks |
| SDK images | `kazu-python`, `kazu-node`, `kazu-go` on GHCR, built and released with the binary; the check host talks to them over the container's stdin/stdout |
| Kubernetes target | Single-node K3s run as a container on the runner (the `rancher/k3s` image), same cluster shape Antithesis uses |
| Telemetry | OTLP receiver (gRPC and HTTP) on the OpenTelemetry Collector's `pdata` packages; OTLP metrics converted to Prometheus naming; Prometheus's own Go `promql` engine and in-memory TSDB per trial, so queries are standard PromQL |
| Protocols | One Protobuf schema, generated with `buf` for every language. SDK ↔ host: JSON over stdio. Controller ↔ proxy: gRPC. CLI ↔ control plane: Connect over HTTPS (JSON), validated with protovalidate |
| Release | GoReleaser (binaries, Homebrew tap, Docker image, GitHub releases, install script); npm per-platform packages; PyPI per-platform wheels; crates.io name claims only |
| Control plane: HTTP and API | Standard library `net/http` routing; Connect handlers |
| Control plane: data | Postgres with pgx and sqlc (plain SQL → typed Go); goose migrations; River for background jobs (stored in Postgres, committed in the same transaction as the data) |
| Control plane: background jobs | GitHub webhook processing, PR comments and checks with retries, Stripe usage reporting, AI calls, sweeps (expired leases, waivers, old runs) |
| Web UI | Server-rendered: templ + htmx, ECharts for charts. React + TypeScript (Vite, TanStack Query/Router, no Next.js, embedded with `go:embed`) only when a view needs it, likely the coverage map or run explorer |
| Integrations | stripe-go, go-github, WorkOS for SSO/SCIM when enterprise needs it |
| Hosting | Managed platform, one region; managed Postgres; S3 or Cloudflare R2 with lifecycle TTLs; Cloudflare for static assets |
| Hosted lanes | Firecracker microVMs on bare-metal KVM (Hetzner dedicated or AWS `.metal`); Go host agent via the Firecracker Go SDK; each lane runs `kazu worker` |

### 6.6 Routing and the landed check

**One routing mechanism: a hosts-file alias per caller.** For each declared edge `a -> b`, Kazu starts one proxy that forwards to the real `b`, and adds a hosts-file entry to **`a`'s spec only**, mapping `b`'s name to that proxy:
- compose: `extra_hosts:` on service `a`
- Kubernetes: `hostAliases:` on `a`'s pod spec, covering the short name and the cluster forms (`b.<namespace>`, `b.<namespace>.svc.cluster.local`)
  Every standard resolver (glibc, musl, Go, the JVM, Node's default `dns.lookup`, .NET, Rust) reads the hosts file before DNS, so this works in any language, wherever the address is configured (env var, config file baked into the image, default in code), as long as it is a name.

It is not guaranteed: a few client libraries do their own DNS and skip the hosts file (Node's `dns.resolve`, gRPC's C-core resolver in some versions, SRV lookups), and hard-coded IPs bypass names entirely. The landed check turns each of these into a clear *not exercised* error rather than a false pass, and the docs list known cases with their fixes (for example `GRPC_DNS_RESOLVER=native`). Only the caller's spec changes, so faults are per-edge by construction. No custom DNS server, no discovery, no privileges.

Servers that hand out their own addresses after the first connection (Kafka brokers, Redis Cluster, MongoDB replica sets) work, with the clients tested, once configured to advertise their service names. A server that can only hand out IPs (multi-node Cassandra) sends later connections around the proxy even though the first went through it. The landed check as described below can't see that, because the fault did apply to the first connection. This is an open gap ([spike](docs/spikes/advertised-addresses/)).

**The landed check.** Each proxy counts traffic and applied faults. After every trial, each configured fault must have applied at least once; otherwise the scenario's result is **not exercised**, with the reason:
- no traffic reached the proxy (the caller uses an IP or bypasses the system resolver);
- the trigger was never reached (`crash after 200 requests` with 150 sent);
- a request-level fault on traffic the proxy can't parse as HTTP/gRPC;
- the fault found nothing to change (a `response` reset against a callee that never answers, a 5% `reset` that drew nothing).
  By default, not exercised fails the scenario, because passing would be a lie. It is a fact, not a heuristic, and it can be downgraded to a warning globally (`gate.not_exercised: warn`) or per scenario, for cases like a 1% fault on light traffic.

**Applied counts an effect, not an attempt.** The counters are per fault and per direction, and a fault is applied only when it changed what a side saw; forwarding a request or accepting a connection never counts. `latency`: a chunk in its direction was held for the delay. `partition`: it held at least one byte (on `grpc` and HTTP/2 edges only HEADERS and DATA frames count; pings, SETTINGS and WINDOW_UPDATE frames don't). `reset`: a drawn request's connection (on `tcp` edges, a drawn connection) was cut, and under `response` the callee had started answering. `bandwidth`: the limiter made at least one write in a selected direction wait. `down`: it refused a connection or reset a live one. `errors`: it answered a request with the configured error. For a `requests:` condition (§4.6), a request the proxy held or cut still counts as received.

*Not exercised* also comes from a check whose `requires:` was never met (§4.6).

There is no discovery and no protocol sniffing. `kazu init` drafts edges once from service names it finds in env values, as a convenience a human reviews; nothing infers anything during a run.

### 6.7 Antithesis alignment

We follow Antithesis's decisions by default. Same as Antithesis: the packaging contract (complete system as compose or raw manifests for single-node K3s; no internet; mocks inside the system; services by name), a fresh, sealed environment per test, default checks for setup and correctness (no unexpected crashes or exits, opt-in language checks), and the terminal check timing: checks run once traffic has exited on its own and faults are over, which is Antithesis's `finally_`. Both `@after` and `@eventually` sit there; Kazu does the retrying that Antithesis leaves to the command.

Deliberate deviations:

| Deviation | Why |
|---|---|
| Proxies per declared edge, instead of owning the whole environment from a hypervisor | We run on customer CI runners, not our own hypervisor |
| User-declared scenarios compared base vs head, instead of autonomous fault exploration (Antithesis enables all network faults by default) | Kazu's question is "did this change regress?", which needs the same faults on both sides |
| Invariants external to the code (SDK workers, SQL, HTTP), instead of assertions inside the application | No code changes needed to start; checks run against the system from outside |
| Assertions over proxy-measured latency and the system's own OpenTelemetry metrics | Reuses telemetry teams already emit; catches failures that leave the data correct (retry amplification, pool exhaustion, latency regressions). Widens the previous row |
| Regression defaults (user-facing error rate, p99, recovery) compared head vs base within one run; Antithesis diffs an ephemeral branch run against the last main run (New/Resolved findings) | Pairing base and head trials on the same machine gives each run its own noise estimate (§6.3); these defaults make a minimal config give a meaningful regression verdict |
| Runs on the customer's CI runners (hosted lanes optional), instead of only in a vendor cloud | Free tier runs on the customer's compute |
| A supported load tool (k6) with a template | Antithesis leaves workloads entirely to the customer; writing load is the biggest effort after packaging, so we cut it to editing a list |
| Traffic declared in `kazu.yaml` as an image, instead of a client container inside the compose file | Keeps the system file "just the system", with no Kazu-specific content. An Antithesis client image can still be used as the traffic image |
| A fixed lifecycle with two check timings (`after`, `eventually`), instead of the test-command layout (`first_`, `parallel_driver_`, `anytime_`, `eventually_`, ...) | Our scenarios are fixed rather than explored, so driver kinds, in-flight checks and `eventually_` (which kills running drivers) have no current use; setup lives in the system as one-shot jobs |
| On compose, readiness from healthchecks and one-shot jobs exiting 0, with no `setup_complete` signal from inside the system; Antithesis requires that signal there (SDK or JSONL). On Kubernetes Antithesis recommends orchestration completion with readiness probes, which is what Kazu does | Kazu reads what the system file already declares, so nothing inside the system knows about Kazu |
| `crash` restarts the same container or pod in place, keeping its filesystem and, in practice, its IP; an Antithesis restart may change the IP and keep or lose filesystem changes | Base and head must get the same timeline; a new IP or an emptied filesystem would vary between the pair (§4.5) |
| The exit check exempts Kazu's own kills and one-shot jobs rather than exit codes, so OOM kills (137), crash loops and every other non-zero exit count; Antithesis exempts 0, 137 and 143 | Kazu records its own process faults (§6.1), so it needn't exempt by code; exempting 137 would hide out-of-memory kills |
| A check without `requires:` stays eligible when never reached (a `sql:` check passes on no rows); Antithesis's `Always` fails if it is never reached | Kazu checks from outside, so it can't see that a check was reached: a violation-selecting query returns no rows both for "no violations" and for "nothing happened". Only the user knows what has to happen first, and states it with `requires:` (§4.6) |
| Reachability is a precondition attached to the check it guards (`requires:`), not a standalone `Sometimes` assertion; Antithesis has no counterpart to `requires:` | Which assertion a `Sometimes` vouches for is implicit, left for the reader to work out; the condition on the check it applies to makes it explicit |
| `delay` is added once per call, in one direction; Antithesis's Slowed delays packets, so a round trip pays it in each direction | `delay` then means the time added to a call, which is what users mean by "payments is slow" (§4.5) |
| Overlapping faults compose through a fixed pipeline per direction; Antithesis lets the more aggressive fault win for the overlap | The pipeline is defined for every pair, and gives slow errors (§4.5) |
| Two faults of the same kind can't overlap on one target (an edge, or a service for process and resource faults); Antithesis lets network faults overlap on a link and silently skips a second node fault on a container | Two latencies or two error rates have no single obvious meaning, so the user picks one or sequences them (§4.5) |

### 6.8 Telemetry

Two sources, one store, one query language.

**Proxy metrics (no setup).** Every proxy records, per declared edge: requests, errors (HTTP status classes, gRPC codes), latency histograms for `http`/`grpc` edges, and connections and bytes for `tcp` edges. Declaring an edge from traffic (`traffic -> checkout: http`) without faulting it gives user-facing latency and error rate, measured identically in base and head; the default regression checks read these edges. `slo:`, `no_regression:` and `recovers:` on a service read the service's declared inbound edges.

Every proxy also records two measures of what it added itself, per edge and per trial, on base and on head, each as a histogram with a count (p50, p99, max). **Forwarding overhead**, per chunk and per direction: the time from the chunk being free to go (the latest of its read returning, the time the fault program meant to release it, and the previous write in that direction returning) to the proxy issuing its write. **Accept lag**, per connection: from `Accept` returning to the connection being admitted to the fault program. They go into the per-trial store with the other proxy counters and are reported on every run (§4.8, `--json` and the run record). They change no result and no default check reads them; a user's `metric:` check may.

**The system's own OpenTelemetry metrics.** Kazu runs an OTLP receiver (gRPC and HTTP) inside the sealed system and points services at it, using the same spec-editing approach as routing:
- by default, it sets the standard `OTEL_EXPORTER_OTLP_ENDPOINT` env var on every service, which every OpenTelemetry SDK honours;
- if the system already has a collector service, `telemetry.collector` names it and Kazu aliases that name to its receiver with the same hosts-file mechanism.
  Services already instrumented need no changes. Metrics are converted to Prometheus naming and stored per trial; resource and run attributes are kept as labels.

**Queries.** Both sources land in one in-memory store per trial, queried with standard PromQL (Prometheus's own Go engine), from `metric:` invariants and the SDK's `run.metrics()` callback. Results are compared base vs head like any other check.

**No data is never a pass.** A metric invariant whose query returns no samples in a trial (exporter disabled, wrong metric name, service never started) is reported as **no data**, with a pointer to `kazu doctor`, which lists the services that sent telemetry and the metric names received. Like the landed check, it fails by default and can be downgraded with `gate.no_data: warn` or per invariant.

Traces come later: the same receiver can accept spans, enabling checks like "each order calls charge at most once" from trace structure.
 
---

## 7. Tiers and business model

| | Free (Apache 2.0) | Pro (~$1K/yr/org, card, no sales call) | Enterprise |
|---|---|---|---|
| Runtime, all fault types, unlimited assertions | ✅ | ✅ | ✅ |
| Default checks; invariants in any language; proxy and OpenTelemetry metric checks | ✅ | ✅ | ✅ |
| GitHub Action / any-CI templates, pass/fail, summaries, all output sinks | ✅ | ✅ | ✅ |
| Smart parallelism on your runners (shards, dynamic leasing, adaptive trials) | ✅ | ✅ | ✅ |
| AI: incident → scenario, PR coverage review, suggested invariants | | ✅ metered | ✅ |
| **Consumer resilience contracts** (provider PRs run consumers' invariants) | | ✅ | ✅ |
| Org registry of standard suites | | basic | full |
| Policy-as-code for gates | | basic | full |
| Ownership routing, coverage map | | ✅ | ✅ |
| Hosted lanes with snapshot boot | | metered add-on | committed spend |
| SSO/SCIM, RBAC, audit export, self-hosted control plane, SLA | | | ✅ |

**Headline paid feature: consumer resilience contracts.** When payments changes, checkout's invariants run against the new payments image and block the merge if a consumer guarantee breaks. It needs shared, cross-repo state (who consumes whom, current image versions, whose invariants apply), so it is naturally a hosted product. Analog: Pact (free library) and PactFlow (paid broker), applied to failure behaviour.

**Who pays:** the platform or infrastructure lead at a 50+ engineer org (monorepo or many repos, ample CI compute). Triggers: a cross-team incident, a mandate to standardize reliability practice, compliance pressure.

**Metering:** hosted trial-minutes and lanes; AI calls. Never assertions, never the customer's own compute.

**Licensing:** runtime Apache 2.0, permanently (`zugzwang-io/kazu`, public). Control-plane code closed, in a separate private repo that imports the shared Go packages.
 
---

## 8. Build order

The target system for every step is `examples/shop` in the public repo (spec: `examples/shop/SPEC.md`). Each step lands the shop features and ground-truth rows it needs.

1. **Walking skeleton:** network proxy (`latency`, `down` that cuts live connections) with traffic counters, compose driver with per-caller `extra_hosts` aliases, declared edges, landed check, the fixed lifecycle with traffic and checks images, the k6 template, `--base`/`--head` variables, `@after` via the Python SDK image, terminal verdict and a minimal `--json` result, config validation against the JSON schema. Target: shop's checkout → payments → postgres, including the IP trap. **The e2e gate starts here** (below), with rows V0, S1–S3 and S14.
2. **Trustworthy:** seeds, HTTP/gRPC proxy with request-count triggers and per-edge latency/error metrics, default setup and correctness checks (ready, crashes, exits, traffic exit code) from container events, `@eventually`, run records, `kazu replay`.
3. **Gate + open-source launch:** trial pairs, statistical verdict with per-run noise estimates, `no_regression`, regression defaults and underpowered results, static shards + `merge`, GitHub Action, JUnit/Markdown/JSON outputs, PR comment; the e2e gate switches to the GitHub Action, exactly as customers use it.
4. **Easy and everywhere:** K3s driver (rendered manifests, `hostAliases`, crash in place; moved to v1.1 by ADR-0003), OTLP receiver with PromQL `metric:` checks and the no-data check, opt-in language checks, `init`, `doctor`, process faults (built earlier, in PLAN.md's Phase 4), one-line checks (the `sql` one from step 1), TypeScript + Go SDK images, CI templates beyond GitHub, OTLP/S3/webhook sinks.
5. **Smart parallelism:** coordinator + dynamic workers, adaptive trials, base-image cache, Kubernetes runner.
6. **Paid v1:** control plane, org-wide GitHub App, consumer contracts, incident → scenario. First web UI (server-rendered).
7. **Later:** traces as evidence, registry and policy, waivers, coverage map, hosted lanes with snapshot boot, enterprise features.
   **Testing Kazu: the e2e gate**

Kazu is a CI gate, so it is gated the same way, on every Kazu PR, by running as a customer would against `examples/shop` and asserting its outputs. Nothing waits for a nightly run.

1. The PR builds the `kazu` binary and the SDK images.
2. A GitHub Actions workflow identical to the one `kazu init --ci github` writes for customers runs those builds against the shop: same commands (the Action from step 3), same `kazu.yaml`, same JUnit and Markdown outputs.
3. A small Go asserter, deliberately not Kazu, compares each row's JSON result with the shop's `expectations.yaml`: verdict, which check caught it, *not exercised*, *no data*, *incompatible with base*, *underpowered*, *no baseline*, *new check failing*, *fails on head*, *fails intermittently on head*, *trial incomplete*, and the non-blocking marker. Any mismatch blocks the merge.
   **Shop rules.**
- Every bug flag must pass the shop's own test suite, so it is a bug ordinary tests miss.
- Predicted misses are recorded and asserted like any other outcome; a miss is fixed only by a generic Kazu improvement, never by a shop-specific check.
  The shop's checks are written by people who know its bugs, so the shop tests Kazu's mechanics; its catch rate is not evidence of what Kazu finds on real systems. That evidence comes from dogfooding.

The shop's bug flags are known, planted bugs, so this tests Kazu in both directions: a change that lets everything pass breaks the rows that expect `REGRESSED`, and one that fails everything breaks row V0. Beneath it, three faster layers also gate every PR: Go unit tests (config, schema, statistics on synthetic data with known answers); proxy tests against a small echo server (added latency, `down` cutting live connections, counters, the landed check); and the shop's own Go tests proving each bug flag really produces its bug, so a failing row says whether the shop or Kazu broke.

Keeping it fast and reliable:
- **Sharded** across about 10 jobs with Kazu's own static shards, targeting the same ~10-minute budget as a customer's PR gate.
- **No flaky rows.** Statistical rows are made decisive for gating: a race probability at which the row's expected result is near certain, so that a false failure is rarer than about 1 in 1000. The 20-repeat false-positive row runs as 20 shards.
- **K3s rows** gate PRs if the K3s spike shows GitHub-hosted runners can run K3s; otherwise they need a self-hosted runner, a cost to decide then.
  **Spikes**
- Before step 3: validate the verdict *method*, not a noise number. On the shop's race flag and on repeated unchanged baselines across different services and runners, choose the statistical test (for example a paired bootstrap on the ratio of quantiles), the minimum number of trial pairs and the trial budget, and confirm the per-run noise estimate keeps false failures at the target rate: underpowered results on unchanged baselines count, per measure, not only a false REGRESSED. Two inputs: a pass/fail check is never undecided, so trials stop at the minimum unless a relative measure or an eligibility count is still undecided; and the trial count pulls two ways, since more trials catch a rare regression more often and make relative checks underpowered less often, but also make a flake already on main block more often. Set the default checks' tolerances as policy.
- Before step 4: K3s on a hosted CI runner (boot time, memory, whether the runner allows the privileges K3s-in-Docker needs).
- Before step 7: VM snapshot/restore pitfalls (clocks, randomness, open connections).
  **Size guess** (pre-repo, assuming a human writing the code and learning Go): 10–14 weekends for steps 1–3, a similar amount for 4–5, and 4–6 for step 6. Steps 3 and 5 are where estimates slip.

---

## 9. Risks

1. **Packaging the customer's system** — the biggest adoption barrier. Requiring a complete, sealed system is the same bar Antithesis sets; mitigations are `doctor`, mocks, templates, and the fact that most compose files used for local development are already close.
2. **Flaky verdicts** — if the gate cries wolf, it gets disabled. Latency checks are the most exposed, because a comparison too noisy to decide fails by default; flakiness already on main fails too, on every PR whose head run hits it. Mitigations are relative comparisons on trial pairs, per-run noise estimates, adaptive trials, the scenario overrides (`underpowered: warn`, `blocking: false`), a message that names the fix, and validating the method in the step-3 spike.
3. **Faults that don't land, metrics that don't arrive** — a caller using an IP bypasses the alias; a disabled exporter sends nothing. Mitigation: the landed and no-data checks turn both into clear errors instead of false passes.
4. **Check authoring** — contracts only work if teams write checks; default checks, templates, metric checks over telemetry they already have, and AI suggestions matter.
5. **Org purchase cycles** — paid value depends on cross-team adoption, which is slow for a solo vendor.
6. **HTTP/2, gRPC streaming and long-lived connections** in the proxy. The proxy is a separate process behind a gRPC interface, so it can be rewritten in Rust later if this becomes a bottleneck, or Envoy reconsidered for the gRPC mode if `x/net/http2` doesn't cover its edge cases (ADR-0012).
7. **Incumbents** (Gremlin into CI gating, Antithesis downmarket) — possible, not imminent.
   **Leading indicators:** dogfooding finds a real bug; three outside teams reach a verdict without help; someone asks for cross-team features unprompted.

---

## 10. Open questions

- The exact statistical test, minimum trial pairs and trial budget (spike before step 3); default tolerances for the default checks (policy).
- Whether hosted CI runners can run K3s inside Docker (spike before step 4).
- OTLP → Prometheus name mapping details (units, dots, delta vs cumulative counters). Deferred to step 4.
- Generic improvements suggested by the shop's predicted misses: realistic client timeouts in the k6 template or as a default scenario (abandoned requests); memory growth relative to base as a default check, or memory limits on sealed systems; a small share of malformed requests in the template (poison messages).
---

## 11. Decision log

Pre-repo log, frozen as of 2026-10-14. New decisions are recorded as ADRs in `docs/adr/` (see ADR-0001); entries here are not relitigated without new evidence.

| Decision | Why |
|---|---|
| Resilience regression gating instead of finance audit reproducibility | Audit pain was annual and unpriced; release regressions recur every week |
| No state-reset logic; fresh environment per trial | Keeps Kazu out of the customer's domain |
| Follow Antithesis's design decisions by default; flag deviations when proposed | They have solved the same packaging and isolation problems at scale; deviations are listed in §6.7 |
| Targets: compose, then single-node K3s from rendered manifests. No bring-your-own environments | Same contract as Antithesis. Externally managed state is neither fresh per trial nor routable through one place, and supporting it multiplies footguns |
| All runs are sealed: no outbound internet, external services as mocks | Antithesis's rule; real sandboxes add nondeterminism that base vs head comparison can't separate from regressions |
| CI only: sealed systems on CI runners (or a laptop), as compose or local K3s. No staging, preview or production targets | The persona's gate is CI. Shared environments break fresh-per-trial, sealing and base-vs-head pairing |
| Routing by a per-caller hosts-file alias (`extra_hosts` / `hostAliases`) | One canonical mechanism for both targets; works in every language and wherever the address is configured; per-edge by construction; no DNS server or privileges |
| Edges are declared and opt-in; no discovery | Inference is the source of flakiness and false confidence. Declared edges plus the landed check give the same safety deterministically |
| No protocol sniffing | Sniffing misreads TLS, SSL-request openings, PROXY headers and server-speaks-first protocols; a false error would block customers. Wrong protocols surface through the landed check instead |
| A fault that never landed fails the scenario by default, with a config override | A silent false pass is the worst outcome for a gate; the override keeps anyone from being blocked |
| Default checks modelled on Antithesis's default properties, plus regression defaults on user-facing edges | Teams get a verdict before writing any invariant; crashes and unexpected exits are facts any system should fail on; the regression defaults are where Kazu's base vs head adds value |
| `no_regression`: user-chosen tolerance; noise estimated in every run, per scenario and per measurement, from the trial pairs; too noisy to decide is "underpowered", a warning | Flapping differs by service, endpoint, commit and runner, so a global noise baseline is meaningless. The tolerance is the user's; whether this run can resolve it is a measurement. Guessing either way would break "fail only on facts". *Superseded in part by ADR-0013: underpowered fails by default, with a scenario override* |
| Telemetry as evidence: proxy metrics plus the system's own OpenTelemetry metrics, one per-trial store, PromQL | Answers "why not unit or integration tests?": catches system-level failures that leave data correct. Reuses telemetry teams already emit and a query language they already know; Prometheus's engine is an embeddable Go library |
| OTLP wired in with the standard env var or a collector alias | No code changes for instrumented services; same spec-editing approach as routing |
| Latency checks relative to base by default | Shared CI runners are noisy; base and head trial pairs on the same machine make relative checks stable where absolute thresholds flap |
| A metric with no data fails by default, with a config override | Same rule as the landed check: a missing signal is never a pass |
| Config file `kazu.yaml`, describing the system and checks only; runners and outputs are CLI flags | Clear ownership; what varies between laptop and CI lives in the CI template. The rename risk is a one-release migration |
| Secrets by reference only (`${env:...}`, files) | Kazu never stores secrets; CI secret stores already exist |
| Fault layers: network → HTTP/gRPC → process, kernel last | Application teams break on dependencies and crashes, not packet reordering |
| Four components with one job each (system, traffic, checks, `kazu.yaml`) and a fixed, hook-free trial lifecycle | Sharp entry and exit points; setup lives in the system, cleanup is deleting it, load never judges, checks never send load |
| Everything Kazu runs is an image: system, traffic and checks; SDK images provided | The runner needs only Docker; checks are hermetic like everything else; files can be mounted so the simple case needs no build |
| Traffic and checks pinned by digest once per run and taken from head | Only the system differs between base and head, so a changed load script or check can't look like a regression. *Extended by ADR-0010: system images are pinned too, for replay* |
| Base and head selected by variables the system file interpolates (`--base TAG=...`, `--head TAG=...`) | One config describes both; works with any image-tagging scheme; the shop's bug flags use the same mechanism |
| Two check timings, `@after` and `@eventually`; no `@during`, no executable-check tier | YAGNI: every modelled failure is visible after traffic exits; SDK images and one-line checks cover every language |
| k6 is the one supported traffic tool; the template is a k6 script, not a Kazu requests format | Constant arrival rate built in, scriptable multi-step flows, one official image, the largest user base. A template avoids designing and maintaining a second format. Any image still works |
| Kazu never embeds k6 | k6 is AGPL-3.0; running its image as a separate container keeps Kazu Apache 2.0 |
| Kazu computes `KAZU_DURATION` per scenario | Nobody hand-calculates fault plus recovery time; a too-short load was the most likely setup mistake. *Superseded by ADR-0006: the traffic decides how long it runs (the template defaults to 120 s), Kazu caps a trial at 30 min, and too short is reported as not exercised* |
| No traffic recording and no observability-driven traffic | YAGNI: the template covers simple cases and existing k6 scripts cover the rest; observability data has the shape of traffic but not the payloads, and would pull production data into CI |
| Base and head chosen by variables the system file interpolates; images pulled, never built | Works with any tagging scheme and keeps builds out of Kazu; CI already publishes images per commit. Git refs (Kazu builds both) and per-service image maps were the alternatives |
| Head's traffic and checks run against both base and head | A PR can't weaken a check to hide its own regression, and load changes can't look like regressions. A check that errors on base is reported as incompatible, not as a verdict. *Superseded in part by ADR-0013: it is judged on head* |
| No executable-check tier | Checks are an image; a shell script would need its own image anyway. SDK images and one-line checks cover it |
| Request-count triggers only on HTTP/gRPC edges | A TCP proxy sees bytes, not requests; counting connections is meaningless with pooling, and parsing database protocols isn't worth it yet |
| Auto scenarios (crash each service, take each edge down), opt-in | A zero-effort regression question using only default checks; opt-in because they multiply CI time. A small step towards Antithesis's autonomous faulting |
| Faults and checks are structured YAML validated by a JSON schema, not a free-text grammar | No parser to write, editor autocomplete, nothing that reads as English but isn't. Only edge names, durations and percentages are parsed strings. *Schema scope and compatibility: ADR-0002* |
| One `checks:` list holding one-line checks and the code-checks image | One place for every user check; the stale `invariants:` key is gone. *Extended by ADR-0002 (decision 13): scenarios can add their own checks* |
| Traffic and the code-checks entry use compose service keys (`image`, `command`, `volumes`, `environment`) | No Kazu-specific way to mount files; every compose user already knows the shape |
| Scenarios vary traffic with `env` knobs or a merged `traffic:` override; the k6 template has named flows | Different scenarios need different load (write-heavy, bursts, soaks) without one script full of branches; base and head still get identical traffic per scenario |
| `suites:` kept | Teams want a fast PR set and a fuller release set; Kazu still needs no knowledge of where it runs, because CI passes `--suite` |
| Kazu's own PRs are gated by the customer CI workflow run against `examples/shop`, asserted against `expectations.yaml` by a separate asserter | A gate must not merge its own regressions; nightly is too late. Separate from Kazu so the checker isn't checking itself |
| Shop bugs must pass the shop's own test suite; misses are recorded, not patched; no blind authoring (dropped 2026-10-10) | Keeps the bugs realistic and Kazu honest about its misses. Blind authoring cost a lot for little: the shop gates Kazu's mechanics, and evidence of value comes from dogfooding, not planted bugs |
| SDK exposes a module-level `run` (`from kazu_sdk import after, eventually, run`) | Reads naturally and needs no argument plumbing |
| Per-scenario `env` on any service, passed through untouched | Which knobs a team's code exposes is their business, not Kazu's |
| No run tagging | Every trial is a fresh, sealed system, so all its data belongs to that trial |
| Invariants via stdio JSON-RPC with host callbacks | Every language with minimal per-language code |
| Any machine running `kazu worker` is a runner | GitHub is one case; no CI lock-in |
| Smart parallelism free | Withholding features that run on the customer's own compute feels artificial |
| No licence threshold on the runtime | Solo open-core evidence (Sidekiq) favours paid features over licence enforcement; relicensing backlash is real |
| Paid = hosting, AI, org coordination | Costs us money or is bought by the organization, not the individual |
| Umbrella: Zugzwang; product: Kazu | Zugzwang matches the existing GitHub org (zugzwang-io) and can hold unrelated projects; Kazu is short, snappy and free on npm and crates.io. tesseracanvas.com stays with the canvas project |
| Go for the binary, not Rust | Learnable in weeks rather than months for a newcomer to both; compose-go and the official Docker/Kubernetes clients address the top adoption risk; Rust's speed edge cancels out because base and head pay the same proxy overhead. *Assumption replaced by a per-run measurement: ADR-0012* |
| Go for the control plane, not Kotlin (Spring or Ktor) | Shares trial-pair leasing, run records and plan hashes with the binary instead of reimplementing them; one toolchain for a solo builder. Spring rejected outright (annotation/DI magic, enterprise strengths we buy from WorkOS). *Plan hashes dropped by ADR-0010* |
| One Protobuf schema for every protocol | Types generated for every SDK language and for Go/TypeScript clients; no hand-maintained duplicate types |
| Docker Engine API, not Docker Desktop | Engine, CLI and Compose are Apache 2.0; Desktop's paid terms are the customer's concern, and the API also covers Podman, OrbStack and Colima |
| No `cargo install` for the CLI | crates.io builds from source and the CLI is a Go binary; Rust users install via brew or the script. Names are still claimed |
| Server-rendered UI first (templ + htmx); React later | YAGNI: no UI before step 6, and the first pages are simple. Splitting front end and back end doesn't improve E2E testing and doubles toolchains for one person. The Connect API exists for the CLI anyway, so moving to React later rewrites pages, not the backend |
| Control plane in a separate private repo (e.g. `zugzwang-io/kazu-cloud`), not an `ee/` directory in the public repo | A public repo can't hold closed code. The control plane imports the shared packages (leasing, run records, plan hashes) from `github.com/zugzwang-io/kazu` as a normal Go module, so those must live outside `internal/`. Keeps the public repo all-Apache and easy to explain at launch; a private repo can be opened later, but published code can't be taken back. *Plan hashes dropped by ADR-0010* |
| Stack picks beyond the standard library (sqlc, templ, Connect, River) | Each removes hand-maintained glue: query mapping, untyped templates, duplicate API types, a homemade job queue. templ is the least essential and the first to drop if we want maximum boring |
| One toy system (`examples/shop`) is the target, the demo and the test oracle | Ground truth for Kazu's own correctness; bugs and traps are env flags so base and head differ without new commits |
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
5. **Fail only on facts; every failure has a switch in the user's config.** Heuristics may warn. A missing signal (a fault that never landed, a metric with no data) is never a pass, and a comparison too noisy to decide is reported as such, never guessed. Nothing in Kazu can leave a customer blocked with no way forward.
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
| **Traffic** | A compose-style service definition (`image`, `command`, `volumes`, `environment`) in `kazu.yaml`; a scenario can override it | What requests to send, how fast, for how long. It generates load and never judges | Kazu starts it once the system is ready, on the system's network, with `KAZU_SEED`, `KAZU_DURATION` and the scenario's env vars | **The process exits.** Exit 0 means the load ran (a default check). It decides how long it runs (the k6 template: `DURATION`, 120 s by default); Kazu stops a trial still running after 30 min. If it stops before every fault fires and recovery is seen, the scenario is *not exercised* |
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

Kazu takes `kazu.yaml`, traffic and checks from the head commit and pins the traffic and checks images by digest once per run. Only the system differs between base and head, so a changed load script can't masquerade as a regression, and a PR can't weaken a check to hide its own regression: a deleted check is gone from both sides.

The cost: a PR that changes the system's schema and updates a check to match runs the new check against the old schema on base. A check that **errors** on base (rather than failing an assertion), for example on a missing column, is reported as **incompatible with base**, not as a verdict.

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

**How long traffic runs is the script's business** ([ADR-0006](docs/adr/0006-fixed-traffic-duration.md)): the template reads `DURATION` (120 s by default) and a scenario changes it through `env`. Kazu stops a trial whose traffic is still running after 30 minutes. A fault that hasn't fired by the time traffic exits, or ended too late to watch recovery, makes the scenario *not exercised*; a longer `DURATION` fixes it.

**Different traffic per scenario.** Base and head always get the same traffic for a given scenario; scenarios can differ from each other in two ways:
- **Knobs, through `env`.** The scenario sets variables the script reads: `env: { traffic: { FLOW: browse, RATE: "50" } }`. The template's named flows make switching flow one variable.
- **A replacement, through `traffic:`.** A scenario's own `traffic:` block is merged over the default definition, so a soak can run a different script, or another tool's image entirely:
  ```yaml
  soak:
    traffic: { command: [run, /load/soak.js] }
    faults:
      - { service: payments, fault: crash, after_requests: 500, restart_after: 5s }
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
      - { service: payments, fault: crash, after_requests: 200, restart_after: 5s }
  double-click:
    env: { traffic: { DOUBLE_SUBMIT: "1" } }   # env vars per service, passed through untouched
  browse-under-outage:
    env: { traffic: { FLOW: browse, RATE: "50" } }
    faults:
      - { edge: checkout -> payments, fault: down, after: 20s, for: 20s }
  soak:
    traffic: { command: [run, /load/soak.js] }  # merged over the default traffic definition
    env: { traffic: { DURATION: 180s } }        # the script decides how long it runs
    faults:
      - { service: payments, fault: crash, after_requests: 500, restart_after: 5s }
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
  - sql: { on: postgres, query: "select order_id from charges group by order_id having count(*) > 1" }   # passes on no rows
  - recovers: { service: payments, within: 30s }
  - no_regression: { service: payments, measure: p99, within: 10% }
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
  flaky: warn
  not_exercised: fail                 # a fault that never landed is not a pass
  no_data: fail                       # a metric with no samples is not a pass
  underpowered: warn                  # a comparison too noisy to decide is reported, not guessed
```

**What the schema carries today** ([ADR-0002](docs/adr/0002-kazu-yaml-schema-shape.md)). The published schema holds the top-level keys Kazu builds next; `telemetry`, `auto_scenarios`, `defaults` and `gate` join it when their phase is built. Inside each key the shape is a good-faith estimate of where the interface lands, and it can change until the first public release, breaking changes included. Before that release the schema is trimmed to what Kazu implements.

**Shapes the reference doesn't show.** `command` takes a list only (compose's string form would need its shell-splitting rules); `environment` takes compose's map or `NAME=value` list. The `http` check is `{ on, path, method, expect: { status, body } }`, `method` defaulting to `GET` and `body` a substring. `slo.max` is a duration for latency measures and a percentage for `error_rate`. `metric` takes `max`/`min` or a relative `within`, not both, plus `no_data: warn`. Measures are `p50`, `p90`, `p95`, `p99`, `error_rate`. A scenario can be empty (`name: {}`): no faults. Names are letters, digits, `.`, `_` and `-`, starting with a letter or digit. A bare number where a duration or percentage goes is an error. The schema's `$id` is `https://zugzwang-io.github.io/kazu/kazu.schema.json`.

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
| `latency` | network | edge or service | `delay`, `jitter` | `{ fault: latency, delay: 300ms, jitter: 50ms }` |
| `down` | network (refuse new connections and cut existing ones) | edge or service | — | `{ fault: down, after: 30s, for: 15s }` |
| `blackhole` | network (accept, never answer) | edge or service | — | `{ fault: blackhole, for: 20s }` |
| `reset` | network | edge or service | `rate` | `{ fault: reset, rate: 2% }` |
| `bandwidth` | network | edge or service | `limit` | `{ fault: bandwidth, limit: 64kbps }` |
| `errors` | HTTP/gRPC | http/grpc edge | `rate`, `code` | `{ fault: errors, rate: 10%, code: 503 }` |
| `timeout` | HTTP/gRPC | http/grpc edge | `rate` | `{ fault: timeout, rate: 5%, after: 1m }` |
| `crash` | process | service | `restart_after` | `{ fault: crash, after_requests: 200, restart_after: 5s }` |
| `pause` | process | service | — | `{ fault: pause, for: 10s }` |
| `cpu`, `memory` | resource | service | `load` | `{ fault: cpu, load: 90% }` |
| `disk_full` | resource | service | — | `{ fault: disk_full, after: 30s }` |

- `edge: a -> b` means one declared edge. `service: b` means **every declared inbound edge** (network faults) or the process (process and resource faults). Callers whose edges aren't declared are untouched; `kazu doctor` lists them.
- `after` counts from traffic start; `after_requests` counts requests through the proxy.
- **Relative triggers** ([ADR-0004](docs/adr/0004-relative-fault-triggers.md)). `from:` makes `after` count from another fault in the same scenario: its name for its end, `{ start: name }` for its start, or a list for the latest of several (a join). Without `from`, faults are independent and overlap by their windows. Anchors are *planned* times: every wall-clock fault's start and end resolve when the plan is built, so base and head get the same schedule; an anchor on a request-count fault resolves at run time, through the controller (ADR-0006). Using a fault's end needs that fault to have a `for` (a crash has none); loops are rejected. There are no triggers on system events ("once payments is healthy"): the schedule would then depend on the system, so base and head could get different timelines.
- `down` must cut live connections, or pooled connections ride through the outage and the scenario tests nothing.
- `crash` kills the container in place and lets it restart in the same container (compose) or the same pod (Kubernetes). Deleting a pod would change IPs and filesystems and make base and head behave differently.
- Request-count triggers land at the same point in every run; wall-clock triggers drift more.
- **Request-count triggers need an `http` or `grpc` edge to count on.** On a `tcp` edge the proxy sees only bytes, so there are no requests to count; `after_requests` there is rejected when the config is loaded. On a `service` target it counts requests on the service's declared HTTP/gRPC inbound edges, and is rejected if it has none.
- Probabilities use a PRNG seeded from `(run seed, edge, request number)`.
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
- `auto-crash-<service>` for every service (except one-shot jobs and traffic): `{ fault: crash, after: 20s, restart_after: 5s }`;
- `auto-down-<caller>-<callee>` for every declared edge (except edges from traffic): `{ fault: down, after: 20s, for: 15s }`.
  They can be listed in suites by name, and `auto` in a suite means all of them.

They assert nothing new: the default checks above are the assertions (no crash beyond the injected one, traffic exits 0, user-facing errors recover, user-facing error rate and p99 no worse than base). Together they answer "does this change make us worse at surviving any single crash or dependency outage?" with no scenarios written. Off by default, because they multiply trials: six services and six edges are twelve scenarios.

#### `no_regression`

`no_regression: { service, measure, within: N% }` compares head against base instead of against a fixed number: across the scenario's trial pairs, head's value must not be worse than base's by more than N%. The user chooses N; it is the regression they are willing to accept.

It exists because a regression gate handles pass/fail checks naturally (passed on base, failed on head), but a measurement like p99 has no pass/fail on its own, and absolute thresholds flap on noisy runners. Using base as the yardstick turns a number into a verdict.

**Noise is measured in every run, never assumed.** Different services, endpoints, commits and runners flap differently, so there is no global noise baseline. For each scenario and each measurement, the spread of the head−base differences across this run's trial pairs is the noise estimate. Kazu decides whether head is worse than base by more than N% given that estimate, adding pairs while it is undecided. No extra base-vs-base runs are needed; the paired differences carry the noise. If this run's own noise is too large to resolve N% within the trial budget, the result is **underpowered**: Kazu reports the noise it measured and what would resolve it (a wider tolerance, more trials, a quieter runner), and never guesses. Underpowered is a warning by default (`gate.underpowered`).

Noise seen in earlier runs of the same scenario may choose a *starting* trial count, so a known-noisy scenario starts with more pairs. It never decides a verdict.

It works on any proxy measurement (`p99`, `error_rate`) and, as `within:`, on any `metric:` query.

**Latency: relative by default.** Shared CI runners are noisy, so absolute latency thresholds flap. Each base and head trial runs back to back on the same machine, which makes relative comparisons stable. Docs, templates and the defaults lead with the relative form; absolute `slo:` remains for rates and for teams with dedicated runners.

#### The `checks:` list

One list holds every user check, and runs in every scenario. A scenario can add its own `checks:` (same entries), run on top of the top-level list in that scenario only, for checks that mean something only under its faults; names can't repeat a top-level check's. Each entry may have a `name:` (used in output; one-line checks are otherwise named after their kind and position for now; stable ids for history are decided with run records) and is one of:
- **`image:`** — code checks, as a compose-style service using a Kazu SDK image (below);
- **`sql:`** — a query that selects violations; it passes when it returns no rows. Kazu reads only the first few rows (for the failure message) under a statement timeout. Exact-row comparisons belong in a code check;
- **`http:`** — a request and the expected status or body;
- **`recovers:`** — a service's error rate back to its pre-fault level `within` a duration;
- **`no_regression:`** — a proxy measurement no worse than base `within` a tolerance;
- **`slo:`** — a proxy measurement under an absolute `max`;
- **`metric:`** — a PromQL query over the trial's proxy and OTLP metrics, with `max`/`min` or a relative `within`.
#### The checks image

For anything that needs code. Two decorators, both relative to traffic exiting:
- **`@after`** runs once, right after traffic exits.
- **`@eventually(within=...)`** is retried after traffic exits until it passes or the timeout expires. It is Antithesis's `eventually_` under the same name.
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
 
  slow-payments              ✓ pass       p99 612ms → 640ms (+4%, within 20%)
  stripe-errors              ✗ REGRESSED  no_double_charge  0/5 → 4/5 trials failing
  db-blip                    ✓ pass       recovers in 11s → 9s
  double-click               ✗ REGRESSED  default: unexpected crash  payments exited with SIGSEGV in 3/5 trials
 
  ✗ stripe-errors: 3 orders charged more than once, e.g. [o_81, o_93, o_97]
    changed since base: payments (a1f3c9 "retry stripe call on 503")
    reproduce: kazu replay r_2fj9 --trial 2
```

A fault that never landed, a metric with no data, or a comparison too noisy to decide looks like this:

```
  ✗ slow-payments: fault on checkout -> payments was not exercised
    no traffic reached the proxy; checkout may use an IP address or bypass the system resolver
  ✗ db-blip: metric "payment attempts per order" has no data
    no samples for payment_attempts_total; check the service's OTLP exporter (kazu doctor)
  ⚠ db-blip: latency within 1% is underpowered
    this run's noise on checkout p99 is ±14% after 12 trial pairs; widen the tolerance or use a quieter runner
```

PR comment: same content, condensed, with a pass/fail status check.
 
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
│ ├─ Config compiler ─ kazu.yaml + seed → content-hashed plan (scenarios × trials × {base, head})         │
│ ├─ Env driver ────── compose, or K3s + rendered manifests; inserts proxies, per-caller aliases and      │
│ │                    the OTLP endpoint; waits for readiness; starts traffic; watches events and logs    │
│ ├─ Controller ────── pushes fault programs to proxies; process faults via Docker API / kubelet;          │
│ │                    event log; landed check                                                            │
│ ├─ Telemetry ─────── OTLP receiver + proxy counters → in-memory metric store per trial; PromQL engine   │
│ ├─ Check host ────── default checks; checks container over stdio JSON-RPC; sql/http/metrics callbacks  │
│ ├─ Verdict engine ── per-scenario statistical base-vs-head comparison with per-run noise estimates;     │
│ │                    flake and underpowered results                                                     │
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
- **Content addressing.** `plan_hash = H(scenario, seed, check and traffic image digests and mounted files, system image digests on the scenario's path)`. Identical base/head hashes skip the scenario. The run record is plan + results (including the trial's metrics), which is exactly what replay needs.
- **Traffic and checks are pinned once per run.** Their images are resolved to digests and their mounted files hashed before the first trial, and the same pins serve base and head, so only the system differs between the two.
- **Kazu knows which crashes are its own.** The controller records every process fault it injects, so the default crash and exit checks can tell an injected `crash` from a real one.
- **No verdict on a fault that didn't land, on a metric with no data, or on a comparison too noisy to decide** (§4.6, §6.6, §6.8).
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
- Adaptive trial counts: stop early when clearly passing or clearly regressed; add pairs while undecided, up to a budget. History from earlier runs of the same scenario may set the starting count, never the verdict.
- Per-scenario comparison of failure counts, latency distributions and metric values, with false-discovery control across many scenarios and checks.
- If the budget runs out before the test can decide, the result is **underpowered**, with this run's measured noise and what would resolve it.
- Flaky scenarios are classified and reported as flaky, not as regressions.
### 6.4 Scale

**CI wall time is the constraint** (target: PR gate in about 10 minutes). With fresh environments at roughly 100 s per trial run:

| Setup | 5 scenarios × 5 trials × 2 variants | Wall time |
|---|---|---|
| `local`, serial | 50 runs × 100 s | ~85 min |
| Static shards ×10 | | ~10–15 min |
| + adaptive trials, skip unchanged | most scenarios need 3 trials; unchanged ones skip | ~5–8 min |
| Hosted lanes ×25 with snapshot boot | boot ≈ 0 s after first | ~2–4 min |

On K3s, one cluster per runner is reused across trials with a fresh namespace per trial, so cluster boot is paid once per runner, not per trial.

Other limits: the system under test must fit on one runner (bigger systems: stubs or bigger self-hosted runners). Proxy overhead is sub-millisecond and paid equally by base and head. Per-trial metric stores are small (one trial's series for minutes) and discarded after the run record is written. The backend stores KB–MB per run and is not a scaling risk.

### 6.5 Tech stack

**Go everywhere we write code.** One language for a solo builder; the control plane imports the same packages as the binary (trial-pair leasing, run records, plan hashes) instead of reimplementing them.

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

**The landed check.** Each proxy counts traffic and applied faults. After every trial, each configured fault must have applied at least once; otherwise the scenario's result is **not exercised**, with the reason:
- no traffic reached the proxy (the caller uses an IP or bypasses the system resolver);
- the trigger was never reached (`crash after 200 requests` with 150 sent);
- a request-level fault on traffic the proxy can't parse as HTTP/gRPC.
  By default, not exercised fails the scenario, because passing would be a lie. It is a fact, not a heuristic, and it can be downgraded to a warning globally (`gate.not_exercised: warn`) or per scenario, for cases like a 1% fault on light traffic.

There is no discovery and no protocol sniffing. `kazu init` drafts edges once from service names it finds in env values, as a convenience a human reviews; nothing infers anything during a run.

### 6.7 Antithesis alignment

We follow Antithesis's decisions by default. Same as Antithesis: the packaging contract (complete system as compose or raw manifests for single-node K3s; no internet; mocks inside the system; services by name; readiness from healthchecks or probes), a fresh, sealed environment per test, default checks for setup and correctness (no unexpected crashes or exits, opt-in language checks), and the `eventually` check timing.

Deliberate deviations:

| Deviation | Why |
|---|---|
| Proxies per declared edge, instead of owning the whole environment from a hypervisor | We run on customer CI runners, not our own hypervisor |
| User-declared scenarios compared base vs head, instead of autonomous fault exploration | Kazu's question is "did this change regress?", which needs the same faults on both sides |
| Invariants external to the code (SDK workers, SQL, HTTP), instead of assertions inside the application | No code changes needed to start; checks run against the system from outside |
| Assertions over proxy-measured latency and the system's own OpenTelemetry metrics | Reuses telemetry teams already emit; catches failures that leave the data correct (retry amplification, pool exhaustion, latency regressions). Widens the previous row |
| Regression defaults (user-facing error rate, p99, recovery, head vs base) alongside Antithesis-style defaults | Antithesis has no base vs head; these defaults make a minimal config give a meaningful regression verdict |
| Runs on the customer's CI runners (hosted lanes optional), instead of only in a vendor cloud | Free tier runs on the customer's compute |
| A supported load tool (k6) with a template | Antithesis leaves workloads entirely to the customer; writing load is the biggest effort after packaging, so we cut it to editing a list |
| Traffic declared in `kazu.yaml` as an image, instead of a client container inside the compose file | Keeps the system file "just the system", with no Kazu-specific content. An Antithesis client image can still be used as the traffic image |
| A fixed lifecycle with two check timings (`after`, `eventually`), instead of the test-command layout (`first_`, `parallel_driver_`, `anytime_`, `finally_`, ...) | Our scenarios are fixed rather than explored, so driver kinds and in-flight checks have no current use; setup lives in the system as one-shot jobs |

### 6.8 Telemetry

Two sources, one store, one query language.

**Proxy metrics (no setup).** Every proxy records, per declared edge: requests, errors (HTTP status classes, gRPC codes), latency histograms for `http`/`grpc` edges, and connections and bytes for `tcp` edges. Declaring an edge from traffic (`traffic -> checkout: http`) without faulting it gives user-facing latency and error rate, measured identically in base and head; the default regression checks read these edges. `slo:`, `no_regression:` and `recovers:` on a service read the service's declared inbound edges.

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
| Smart parallelism on your runners (shards, dynamic leasing, adaptive trials, skip unchanged) | ✅ | ✅ | ✅ |
| AI: incident → scenario, PR coverage review, suggested invariants | | ✅ metered | ✅ |
| **Consumer resilience contracts** (provider PRs run consumers' invariants) | | ✅ | ✅ |
| Org registry of standard suites | | basic | full |
| Policy-as-code for gates; waivers with expiry and audit | | basic | full |
| Org-wide flake quarantine, ownership routing, coverage map | | ✅ | ✅ |
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
5. **Smart parallelism:** coordinator + dynamic workers, adaptive trials, skip unchanged, base-image cache, Kubernetes runner.
6. **Paid v1:** control plane, org-wide GitHub App, consumer contracts, incident → scenario. First web UI (server-rendered).
7. **Later:** traces as evidence, registry and policy, waivers, coverage map, hosted lanes with snapshot boot, enterprise features.
   **Testing Kazu: the e2e gate**

Kazu is a CI gate, so it is gated the same way, on every Kazu PR, by running as a customer would against `examples/shop` and asserting its outputs. Nothing waits for a nightly run.

1. The PR builds the `kazu` binary and the SDK images.
2. A GitHub Actions workflow identical to the one `kazu init --ci github` writes for customers runs those builds against the shop: same commands (the Action from step 3), same `kazu.yaml`, same JUnit and Markdown outputs.
3. A small Go asserter, deliberately not Kazu, compares each row's JSON result with the shop's `expectations.yaml`: verdict, which check caught it, *not exercised*, *no data*, *incompatible with base*. Any mismatch blocks the merge.
   **Shop rules.**
- Every bug flag must pass the shop's own test suite, so it is a bug ordinary tests miss.
- Predicted misses are recorded and asserted like any other outcome; a miss is fixed only by a generic Kazu improvement, never by a shop-specific check.
  The shop's checks are written by people who know its bugs, so the shop tests Kazu's mechanics; its catch rate is not evidence of what Kazu finds on real systems. That evidence comes from dogfooding.

The shop's bug flags are known, planted bugs, so this tests Kazu in both directions: a change that lets everything pass breaks the rows that expect `REGRESSED`, and one that fails everything breaks row V0. Beneath it, three faster layers also gate every PR: Go unit tests (config, schema, plan hashing, statistics on synthetic data with known answers); proxy tests against a small echo server (added latency, `down` cutting live connections, counters, the landed check); and the shop's own Go tests proving each bug flag really produces its bug, so a failing row says whether the shop or Kazu broke.

Keeping it fast and reliable:
- **Sharded** across about 10 jobs with Kazu's own static shards, targeting the same ~10-minute budget as a customer's PR gate.
- **No flaky rows.** Statistical rows are made decisive for gating: a race probability high enough that detection is near certain, and "flaky, not regressed" asserted with a margin wide enough that a false failure is rarer than about 1 in 1000. The 20-repeat false-positive row runs as 20 shards.
- **K3s rows** gate PRs if the K3s spike shows GitHub-hosted runners can run K3s; otherwise they need a self-hosted runner, a cost to decide then.
  **Spikes**
- Before step 3: validate the verdict *method*, not a noise number. On the shop's race flag and on repeated unchanged baselines across different services and runners, choose the statistical test (for example a paired bootstrap on the ratio of quantiles), the minimum number of trial pairs and the trial budget, and confirm the per-run noise estimate keeps false positives at the target rate. Set the default checks' tolerances as policy.
- Before step 4: K3s on a hosted CI runner (boot time, memory, whether the runner allows the privileges K3s-in-Docker needs).
- Before step 7: VM snapshot/restore pitfalls (clocks, randomness, open connections).
  **Size guess** (pre-repo, assuming a human writing the code and learning Go): 10–14 weekends for steps 1–3, a similar amount for 4–5, and 4–6 for step 6. Steps 3 and 5 are where estimates slip.

---

## 9. Risks

1. **Packaging the customer's system** — the biggest adoption barrier. Requiring a complete, sealed system is the same bar Antithesis sets; mitigations are `doctor`, mocks, templates, and the fact that most compose files used for local development are already close.
2. **Flaky verdicts** — if the gate cries wolf, it gets disabled. Latency checks are the most exposed; mitigations are relative comparisons on trial pairs, per-run noise estimates, the underpowered result, and validating the method in the step-3 spike.
3. **Faults that don't land, metrics that don't arrive** — a caller using an IP bypasses the alias; a disabled exporter sends nothing. Mitigation: the landed and no-data checks turn both into clear errors instead of false passes.
4. **Check authoring** — contracts only work if teams write checks; default checks, templates, metric checks over telemetry they already have, and AI suggestions matter.
5. **Org purchase cycles** — paid value depends on cross-team adoption, which is slow for a solo vendor.
6. **HTTP/2, gRPC streaming and long-lived connections** in the proxy. The proxy is a separate process behind a gRPC interface, so it can be rewritten in Rust later if this becomes a bottleneck.
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
| `no_regression`: user-chosen tolerance; noise estimated in every run, per scenario and per measurement, from the trial pairs; too noisy to decide is "underpowered", a warning | Flapping differs by service, endpoint, commit and runner, so a global noise baseline is meaningless. The tolerance is the user's; whether this run can resolve it is a measurement. Guessing either way would break "fail only on facts" |
| Telemetry as evidence: proxy metrics plus the system's own OpenTelemetry metrics, one per-trial store, PromQL | Answers "why not unit or integration tests?": catches system-level failures that leave data correct. Reuses telemetry teams already emit and a query language they already know; Prometheus's engine is an embeddable Go library |
| OTLP wired in with the standard env var or a collector alias | No code changes for instrumented services; same spec-editing approach as routing |
| Latency checks relative to base by default | Shared CI runners are noisy; base and head trial pairs on the same machine make relative checks stable where absolute thresholds flap |
| A metric with no data fails by default, with a config override | Same rule as the landed check: a missing signal is never a pass |
| Config file `kazu.yaml`, describing the system and checks only; runners and outputs are CLI flags | Clear ownership; what varies between laptop and CI lives in the CI template. The rename risk is a one-release migration |
| Secrets by reference only (`${env:...}`, files) | Kazu never stores secrets; CI secret stores already exist |
| Fault layers: network → HTTP/gRPC → process, kernel last | Application teams break on dependencies and crashes, not packet reordering |
| Four components with one job each (system, traffic, checks, `kazu.yaml`) and a fixed, hook-free trial lifecycle | Sharp entry and exit points; setup lives in the system, cleanup is deleting it, load never judges, checks never send load |
| Everything Kazu runs is an image: system, traffic and checks; SDK images provided | The runner needs only Docker; checks are hermetic like everything else; files can be mounted so the simple case needs no build |
| Traffic and checks pinned by digest once per run and taken from head | Only the system differs between base and head, so a changed load script or check can't look like a regression |
| Base and head selected by variables the system file interpolates (`--base TAG=...`, `--head TAG=...`) | One config describes both; works with any image-tagging scheme; the shop's bug flags use the same mechanism |
| Two check timings, `@after` and `@eventually`; no `@during`, no executable-check tier | YAGNI: every modelled failure is visible after traffic exits; SDK images and one-line checks cover every language |
| k6 is the one supported traffic tool; the template is a k6 script, not a Kazu requests format | Constant arrival rate built in, scriptable multi-step flows, one official image, the largest user base. A template avoids designing and maintaining a second format. Any image still works |
| Kazu never embeds k6 | k6 is AGPL-3.0; running its image as a separate container keeps Kazu Apache 2.0 |
| Kazu computes `KAZU_DURATION` per scenario | Nobody hand-calculates fault plus recovery time; a too-short load was the most likely setup mistake. *Superseded by ADR-0006: the traffic decides how long it runs (the template defaults to 120 s), Kazu caps a trial at 30 min, and too short is reported as not exercised* |
| No traffic recording and no observability-driven traffic | YAGNI: the template covers simple cases and existing k6 scripts cover the rest; observability data has the shape of traffic but not the payloads, and would pull production data into CI |
| Base and head chosen by variables the system file interpolates; images pulled, never built | Works with any tagging scheme and keeps builds out of Kazu; CI already publishes images per commit. Git refs (Kazu builds both) and per-service image maps were the alternatives |
| Head's traffic and checks run against both base and head | A PR can't weaken a check to hide its own regression, and load changes can't look like regressions. A check that errors on base is reported as incompatible, not as a verdict |
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
| Go for the binary, not Rust | Learnable in weeks rather than months for a newcomer to both; compose-go and the official Docker/Kubernetes clients address the top adoption risk; Rust's speed edge cancels out because base and head pay the same proxy overhead |
| Go for the control plane, not Kotlin (Spring or Ktor) | Shares trial-pair leasing, run records and plan hashes with the binary instead of reimplementing them; one toolchain for a solo builder. Spring rejected outright (annotation/DI magic, enterprise strengths we buy from WorkOS) |
| One Protobuf schema for every protocol | Types generated for every SDK language and for Go/TypeScript clients; no hand-maintained duplicate types |
| Docker Engine API, not Docker Desktop | Engine, CLI and Compose are Apache 2.0; Desktop's paid terms are the customer's concern, and the API also covers Podman, OrbStack and Colima |
| No `cargo install` for the CLI | crates.io builds from source and the CLI is a Go binary; Rust users install via brew or the script. Names are still claimed |
| Server-rendered UI first (templ + htmx); React later | YAGNI: no UI before step 6, and the first pages are simple. Splitting front end and back end doesn't improve E2E testing and doubles toolchains for one person. The Connect API exists for the CLI anyway, so moving to React later rewrites pages, not the backend |
| Control plane in a separate private repo (e.g. `zugzwang-io/kazu-cloud`), not an `ee/` directory in the public repo | A public repo can't hold closed code. The control plane imports the shared packages (leasing, run records, plan hashes) from `github.com/zugzwang-io/kazu` as a normal Go module, so those must live outside `internal/`. Keeps the public repo all-Apache and easy to explain at launch; a private repo can be opened later, but published code can't be taken back |
| Stack picks beyond the standard library (sqlc, templ, Connect, River) | Each removes hand-maintained glue: query mapping, untyped templates, duplicate API types, a homemade job queue. templ is the least essential and the first to drop if we want maximum boring |
| One toy system (`examples/shop`) is the target, the demo and the test oracle | Ground truth for Kazu's own correctness; bugs and traps are env flags so base and head differ without new commits |
# `examples/shop` — toy system spec

A small checkout system with **bugs and discovery traps you can switch on**. It is four things at once:

1. **The thing Kazu is built against.** Build step 1 targets `checkout → payments → postgres`; later steps add services as Kazu gains features.
2. **Kazu's verdict oracle.** Each bug flag has a known correct verdict, so Kazu's own end-to-end tests can check that a regression is reported as `REGRESSED`, a flake as flaky, and an unchanged system as `pass`.
3. **Kazu's discovery oracle.** The real dependency graph is known, and each trap flag hides an edge or a protocol in a way real systems do, so discovery can be scored against ground truth.
4. **The quickstart demo.** `cd examples/shop && kazu run` is the first thing a new user tries.

Status: spec only. Code lands in follow-up PRs, one per build step.

---

## Non-goals

Keep it boring. Realistic enough to fail the way real systems fail, and no more.

- No auth, no UI, no real Stripe, no Kubernetes manifests.
- No business logic beyond what an invariant or a discovery test needs.
- Target: under ~1,000 lines of Go across all services, plus SQL and compose.

If a feature doesn't serve a row in one of the two ground-truth matrices, it doesn't go in.

---

## Services

All services are Go, standard library first. Each reads its dependency addresses from env vars (the contract in DESIGN.md §5: customer services read addresses from config, so Kazu can rewire them through proxies).

| Service | Protocol | Port | Calls | Arrives in build step |
|---|---|---|---|---|
| `checkout` | HTTP | 8080 | `payments`, `postgres`, `rabbitmq` (step 4) | 1 |
| `payments` | HTTP | 8081 | `fraud` (step 2), `fakestripe` (step 4), `postgres` | 1 |
| `postgres` | TCP | 5432 | — | 1 |
| `fraud` | gRPC | 9090 | — | 2 |
| `worker` | AMQP consumer | — | `rabbitmq`, `postgres` | 4 |
| `rabbitmq` | TCP | 5672 | — | 4 |
| `fakestripe` | HTTPS (self-signed CA) | 8443 | — | 4 |

Env vars: `PAYMENTS_URL`, `FRAUD_ADDR`, `STRIPE_URL`, `DATABASE_URL`, `AMQP_URL`, plus `KAZU_TAG_HEADER` (default `X-Kazu-Run-Id`).

### `checkout`

| Endpoint | Behaviour |
|---|---|
| `POST /orders` | Body `{amount_cents}`, header `Idempotency-Key` (required). Inserts an order as `pending` and calls `POST /charges` on payments with a 1 s client timeout and up to 2 retries **reusing the same key**, with backoff. Outcome handling is the part the bugs target: **2xx** → `paid`; **4xx or connection refused** (definitely not processed) → `failed`; **timeout or connection reset** (outcome unknown) → stays `pending` for the reconciler. From step 4, publishes `order.paid`. A repeat of the same key returns the existing order, resuming the payment call if it is still `pending` (so concurrent duplicates both reach payments with the same key). |
| `GET /orders/{id}` | The order. |
| `GET /internal/orders?run_id=&status=` | Orders for one run, filtered by status. Used by `@settles` invariants. |
| `GET /healthz` | 200 once the database is reachable. |

**Reconciler.** A goroutine in checkout runs every 5 s: each order `pending` for more than 10 s is re-sent to payments with its original key and a 5 s timeout. Because payments is idempotent, this settles every unknown outcome without double charging.

Checkout uses a connection pool (`pgxpool`) that redials after failures.

### `payments`

| Endpoint | Behaviour |
|---|---|
| `POST /charges` | Body `{order_id, amount_cents}`, header `Idempotency-Key`. Step 2+: calls `fraud.Check` for orders of 1,000 cents or more (denied → 402). Deduplicates on `idempotency_key` under a Postgres advisory lock (lock, look up, insert if absent), so a repeat returns the existing charge. Step 4+: calls `fakestripe` `POST /v1/charges` with the same key. |
| `GET /healthz` | 200 once the database is reachable. |

Payments uses a connection pool that redials after failures.

### `fraud` (gRPC)

`Check(CheckRequest{order_id, amount_cents}) → CheckResponse{allow}`. Deterministic: deny if `amount_cents > 100000`. Stateless, no database, so it is also the obvious candidate for a Kazu stub. Only orders of 1,000 cents or more reach it, which makes `payments → fraud` an edge that light traffic can miss (see Discovery).

### `worker`

Consumes `order.paid`. For each message, writes two ledger entries (debit customer, credit merchant) in one transaction, then acks. Ledger entries for one order always sum to zero. Connects to RabbitMQ once at startup and holds the connection.

### `fakestripe`

HTTPS with a self-signed CA shipped in the repo. `POST /v1/charges` returns 200 with a charge id, idempotent on the `Idempotency-Key` header, in memory. It exists to exercise TLS edges and `kazu doctor`'s TLS warning, not to model Stripe.

---

## Data

`db/schema.sql`, applied by an init container in compose mode and by `byo/up.sh` in BYO mode:

```sql
create table orders (
  id              text primary key,         -- derived from idempotency key
  run_id          text not null,
  idempotency_key text not null unique,
  amount_cents    bigint not null,
  status          text not null check (status in ('pending','paid','failed')),
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now()
);

create table charges (
  id              text primary key,
  run_id          text not null,
  order_id        text not null,
  idempotency_key text not null,           -- deliberately not unique: payments dedups under an
  amount_cents    bigint not null,          -- advisory lock, so the race flag has something to break
  created_at      timestamptz not null default now()
);

create table ledger_entries (
  id           bigserial primary key,
  run_id       text not null,
  order_id     text not null,
  account      text not null,
  amount_cents bigint not null              -- signed; per order sums to 0
);
```

**Run tagging.** Every service reads the run id from the header named by `KAZU_TAG_HEADER`, forwards it on every downstream HTTP/gRPC call and AMQP message, and writes it to `run_id`. This is what lets invariants scope queries with `where run_id = :run_id` (DESIGN.md §4.3).

**Determinism.** Order and charge ids derive from the idempotency key. Services use no randomness of their own, except the deliberate race flag below.

---

## Traffic

`cmd/load`: a small Go program, so the example needs nothing beyond Docker (or Go, in BYO mode). It sends `N` orders at `R` requests/s to `checkout`, each with a fresh idempotency key derived from `KAZU_SEED` and the request number, and the run-id header from `KAZU_RUN_ID`. Amounts are drawn from the same seed: about 70% below 1,000 cents, 30% at or above (so the fraud edge is exercised). Defaults: 300 orders at 10/s (30 s), small enough to keep a trial near the ~100 s budget in DESIGN.md §6.4.

| Flag | Effect |
|---|---|
| `--double-submit` | Sends every order twice at once with the same key (a double-click). Needed by the race flag. |
| `--small-only` | Only amounts below 1,000 cents, so `payments → fraud` is never exercised. Used by discovery tests. |

```yaml
traffic: go run ./cmd/load --orders 300 --rate 10
```

---

## Bug flags

Each flag is an env var read at startup. Base and head are the **same images with different flags**, so a regression can be produced without a second git commit. Each flag is a failure class that shows up in real postmortems.

| Flag | Service | The bug | Exposed by | Class |
|---|---|---|---|---|
| `SHOP_BUG_RETRY_NEW_KEY` | checkout | Retries (including the reconciler's) generate a fresh idempotency key. A timed-out call that payments actually completed is charged again. | `slow-payments` | Non-idempotent retry |
| `SHOP_BUG_NO_TIMEOUT` | checkout | No client timeout on payments calls; requests hang as long as payments does. | `payments-blackhole` | Missing timeout |
| `SHOP_BUG_FAIL_OPEN` | checkout | When payments refuses connections, marks the order `paid` ("we'll charge it later") and never does. | `payments-down` | Fail-open on dependency outage |
| `SHOP_BUG_NO_RECONCILE` | checkout | The reconciler is off, so orders with an unknown outcome stay `pending` forever. | `payments-crash` | Unknown outcome never resolved |
| `SHOP_BUG_NO_RECONNECT` | payments | Uses one `pgx.Conn` opened at startup instead of a pool, and never redials. After a DB blip every charge fails until restart. | `db-blip` | No reconnect |
| `SHOP_BUG_ACK_BEFORE_WRITE` | worker | Acks the message, then writes ledger entries. A crash in between loses the entries. A fixed 50 ms gap between ack and write keeps the window wide enough to hit reliably. | `worker-crash` | At-most-once by accident |
| `SHOP_BUG_RACE_P=<p>` | payments | With probability `p` per request, skips the advisory lock, leaving a bare check-then-insert; two concurrent requests with the same key can both insert. Uses the service's own unseeded RNG **on purpose**: this is nondeterminism Kazu does not control. | `baseline` + `--double-submit` | Check-then-act race |

Deliberately left out for now, because a toy can't make them meaningful without extra machinery: retry storms and metastable failure (need load near capacity), missing bulkheads (need a mixed read/write workload and per-route SLOs), poison messages (need a dead-letter path and a status for parked orders). See Open questions.

---

## `resilience.yaml`

There is no `dependencies:` block: discovery infers the graph and protocols on every run (see Discovery). The file only holds overrides when inference is wrong or a stub is wanted.

```yaml
system: docker-compose.yml
traffic: go run ./cmd/load --orders 300 --rate 10

scenarios:
  baseline: {}                                     # no faults; a sanity floor
  slow-payments:
    checkout -> payments: latency 1500ms ±200ms    # above checkout's 1 s timeout
  payments-blackhole:
    checkout -> payments: blackhole for 20s after 10s
  payments-down:
    checkout -> payments: down for 20s after 10s
  payments-crash:
    payments: crash after 100 requests, restart after 5s
  db-blip:
    postgres: down for 15s after 10s               # must also cut existing connections
  worker-crash:
    worker: crash after 15s, restart after 5s

invariants:
  - no_double_charge                               # resilience/invariants.py
  - paid_means_charged
  - ledger_balances
  - orders_settle
  - slo: checkout p99 < 1500ms
  - recovers: checkout within 60s

suites:
  pr: [baseline, slow-payments, payments-down, db-blip]
  release: all
```

`resilience/invariants.py`, written against the Python SDK as in DESIGN.md §4.3:

- `no_double_charge` (`@after`): no `order_id` with more than one charge in this run.
- `paid_means_charged` (`@after`): every `paid` order in this run has a charge.
- `ledger_balances` (`@during(every="1s")`): ledger entries for this run sum to zero.
- `orders_settle` (`@settles(within="60s")`): no order in this run is still `pending`, and every `paid` order has ledger entries (step 4).

### BYO-environment variant

`resilience.byo.yaml` runs the same system without compose, to exercise the bring-your-own path (DESIGN.md §4.1, §5):

```yaml
system:
  up: ./byo/up.sh          # reads KAZU_ENV_FILE, starts each service with `go run`
  down: ./byo/down.sh
  ready: http://localhost:8080/healthz
traffic: go run ./cmd/load --orders 300 --rate 10
# scenarios and invariants: same as resilience.yaml, minus the process faults
```

`byo/up.sh` starts each service as a native process and points it at whatever Postgres `SHOP_PG_URL` names: in CI, a Postgres installed on the runner (not a container); locally, anything reachable. It creates a fresh database per trial (`createdb shop_$KAZU_RUN_ID`) to keep the fresh-environment rule without a container. Process and resource faults are unavailable here; `kazu doctor` must say so per scenario rather than silently skip them.

---

## Verdict ground truth

Each row is an end-to-end test of Kazu itself. `fail_on: regression` throughout.

| # | Base flags | Head flags | Scenario | Expected verdict | Caught by | Tests | Step |
|---|---|---|---|---|---|---|---|
| 1 | — | — | all | `pass`, no violations | — | No false positives on an unchanged system | 1 |
| 2 | — | `RETRY_NEW_KEY` | `slow-payments` | `REGRESSED` | `no_double_charge` | Core loop: latency fault → invariant → verdict | 1 |
| 3 | — | `RETRY_NEW_KEY` | `baseline` | `pass` | — | Bug is latent until the fault fires | 1 |
| 4 | — | `FAIL_OPEN` | `payments-down` | `REGRESSED` | `paid_means_charged` | Dependency outage | 1 |
| 5 | — | `NO_RECONNECT` | `db-blip` | `REGRESSED` | `recovers` | `down` cuts live connections | 1 |
| 6 | `RETRY_NEW_KEY` | `RETRY_NEW_KEY` | `slow-payments` | `pass` (violation in both) | — | Regression, not violation, gates | 3 |
| 7 | — | `NO_TIMEOUT` | `payments-blackhole` | `REGRESSED` | `slo`, `recovers` | Blackhole, SLO and recovery checks | 2 |
| 8 | `RACE_P=0.1` | `RACE_P=0.1` | `baseline` + `--double-submit` | `flaky`, not regressed | `no_double_charge` | Flake classification | 3 |
| 9 | — | `RACE_P=0.1` | `baseline` + `--double-submit` | `REGRESSED` given enough trials | `no_double_charge` | Statistical power of adaptive trials | 3 |
| 10 | — | `NO_RECONCILE` | `payments-crash` | `REGRESSED` | `orders_settle` | Process faults; unknown outcomes | 4 |
| 11 | — | `ACK_BEFORE_WRITE` | `worker-crash` | `REGRESSED` | `orders_settle` | Process faults, `@settles` | 4 |
| 12 | — | `RETRY_NEW_KEY` | `slow-payments`, **BYO mode** | `REGRESSED` | `no_double_charge` | Same verdict without compose or a DB container | 4 |
| 13 | — | `RETRY_NEW_KEY` + trap `PAYMENTS_BY_IP` | `slow-payments` | **error**: edge bypasses proxy | — | No false pass when a fault can't land | 4 |

Rows 8 and 9 are the inputs to the verdict-statistics spike before step 3: measure how many trials row 9 needs to reach `REGRESSED` and how often row 8 is wrongly called a regression, across a range of `p`.

Row 13 is the most important safety property: if checkout reaches payments by IP, the latency fault never lands, and without detection the bug passes silently. Kazu must refuse to give a verdict on a scenario whose faulted edge saw no traffic through its proxy.

The matrix also lives as `expectations.yaml`, read by an end-to-end test in the Kazu repo (`go test -tags e2e ./e2e/...`):

```yaml
- name: retry-new-key-under-latency
  base: {}
  head: { SHOP_BUG_RETRY_NEW_KEY: "1" }
  scenario: slow-payments
  expect: { verdict: regressed, invariant: no_double_charge }
```

Each row runs with a fixed `--seed`, so a failing e2e test prints the `kazu replay` command like any other failure.

---

## Discovery

Discovery is what makes the one-line config work: Kazu has to know every edge to put a proxy on it, and every edge's protocol to know which faults it can inject. The shop is where discovery gets built and scored.

### How discovery works (target design)

Discovery runs at the start of every `kazu run` (cached by image digests) and on demand with `kazu discover`. It merges three sources, and records the evidence for each edge and protocol:

| Source | How | Finds | Misses |
|---|---|---|---|
| **Static** | Parse compose (or the BYO process environment): env values that look like URLs or `host:port` and name a known service or external host, matched by value, not variable name | Edges in config, including ones traffic never exercises | Addresses built in code, IP literals not mapped to a service |
| **DNS** | Boot once with `kazu-dns` as the containers' resolver; log which container resolves which name | Every name actually looked up | IP literals; BYO mode (no resolver to hijack) |
| **Sockets** | During the discovery boot, observe outbound connections per container (conntrack via the Docker network, or `/proc/<pid>/net` for BYO process trees) | IP-literal edges, long-lived connections | Edges the traffic never triggers |

**Protocol** comes from sniffing the first bytes on each edge during the discovery boot: HTTP/1.x request line, HTTP/2 preface plus `content-type: application/grpc` for gRPC, Postgres startup packet, AMQP `AMQP\x00` header, TLS ClientHello (protocol inside unknown unless a CA is provided). The port number is only a fallback, and a disagreement between port and sniffed protocol is reported.

**Output** is the inferred graph plus warnings, printed by `kazu discover` and stored in the run record:

```
checkout -> payments   http      static+dns+socket
checkout -> postgres   postgres  static+dns+socket
payments -> fraud      grpc      static            ⚠ seen in config, no traffic during discovery
payments -> fakestripe tls       static+dns+socket ⚠ HTTP faults need a stub or test CA
```

### Discovery traps

Each trap is an env var (or compose overlay) that hides something the way real systems do. Traps are off by default, so the demo stays clean.

| Trap | What it does | Source that must catch it | Expected warning |
|---|---|---|---|
| *(none)* | The plain system | all three agree | none |
| `--small-only` load | `payments → fraud` gets no traffic during discovery | static | "no traffic during discovery" |
| `SHOP_TRAP_PAYMENTS_BY_IP` | `PAYMENTS_URL` uses payments' static IP (compose `ipv4_address`) | sockets; static maps the IP via compose | "edge addressed by IP; cannot be rewired" → run refuses faults on it (row 13) |
| `SHOP_TRAP_PG_PORT` | Postgres listens on 6543 | protocol sniffing | none (protocol still `postgres`) |
| `SHOP_TRAP_ODD_ENV` | AMQP address in `LEDGER_BACKEND=rabbitmq:5672` instead of `AMQP_URL` | static (match by value) | none |
| `SHOP_TRAP_BUILT_ADDR` | Checkout builds the payments URL in code from `PAYMENTS_HOST` + a hard-coded port | DNS, sockets | none |
| BYO mode | Native processes, no compose, no resolver to hijack | static (process env), sockets (`/proc`) | DNS source unavailable |

### Discovery ground truth

`discovery-expectations.yaml` holds the true graph per trap configuration. The discovery test boots the system, runs `kazu discover --json`, and compares:

```yaml
- name: fraud-edge-without-traffic
  traps: { load: --small-only }
  expect:
    edges:
      - { from: checkout, to: payments,   protocol: http }
      - { from: checkout, to: postgres,   protocol: postgres }
      - { from: payments, to: postgres,   protocol: postgres }
      - { from: payments, to: fraud,      protocol: grpc, evidence: [static] }
    warnings:
      - { edge: payments -> fraud, kind: no_traffic }
```

The test fails on any missing edge (a missed edge is a potential false pass), any wrong protocol, or any missing warning. Extra edges are reported but don't fail, since a false edge only costs a needless proxy.

---

## Layout

```
examples/shop/
  SPEC.md                       # this file
  go.mod                        # own module, so the example reads like a customer repo
  docker-compose.yml
  docker-compose.traps.yml      # overlay for the IP and port traps
  resilience.yaml
  resilience.byo.yaml
  resilience/invariants.py
  expectations.yaml             # verdict ground truth
  discovery-expectations.yaml   # discovery ground truth
  db/schema.sql
  byo/up.sh  byo/down.sh
  services/checkout/  services/payments/  services/fraud/
  services/worker/    services/fakestripe/
  proto/fraud.proto
  cmd/load/
```

A separate Go module keeps the services' dependencies (pgx, amqp091-go, grpc) out of Kazu's `go.mod`, and makes the example look like what a customer would actually point Kazu at.

---

## Delivery, by build step

| Step | Shop adds | Discovery adds | Rows |
|---|---|---|---|
| 1 | `checkout` (with reconciler), `payments`, `postgres`, schema, `cmd/load`, compose, `RETRY_NEW_KEY`, `FAIL_OPEN`, `NO_RECONNECT`, `no_double_charge`, `paid_means_charged` | Static compose scan only, so `system: docker-compose.yml` alone works from the walking skeleton | Verdict 1–5 |
| 2 | `fraud` (gRPC), `NO_TIMEOUT`, `orders_settle`, `ledger_balances` (no-op until step 4) | Protocol sniffing (needed to choose HTTP vs gRPC proxy mode) | Verdict 7 |
| 3 | `RACE_P`, `--double-submit`, `expectations.yaml`, e2e harness | — | Verdict 6, 8, 9 |
| 4 | `worker`, `rabbitmq`, `fakestripe`, `NO_RECONCILE`, `ACK_BEFORE_WRITE`, BYO scripts, traps, `discovery-expectations.yaml` | DNS and socket sources, warnings, `kazu discover`, BYO discovery, the bypass guard | Verdict 10–13, all discovery rows |

## Open questions

- `worker-crash` uses a wall-clock trigger because the worker has no inbound edge to count requests on. Revisit once raw-TCP request counting is settled (DESIGN.md §10).
- Whether `blackhole` lands in step 1 or 2 of the proxy; row 7 moves with it.
- Socket observation per container: conntrack needs `CAP_NET_ADMIN` on the host, which hosted CI runners may not grant. Fallback is reading `/proc/<pid>/net/tcp` inside each container's PID namespace via the Docker API. Spike before step 4.
- BYO discovery depends on Kazu knowing which processes belong to which service. Proposed: the process tree under `up`, named by listening port. Unproven.
- Retry storms, bulkheads and poison messages: worth adding once Kazu has per-route SLOs and the shop has a load mode near capacity.

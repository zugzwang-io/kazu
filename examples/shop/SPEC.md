# `examples/shop` — toy system spec

A small checkout system with **bugs you can switch on**. It is three things at once:

1. **The thing Kazu is built against.** Build step 1 targets `checkout → payments → postgres`; later steps add services as Kazu gains features.
2. **Kazu's test oracle.** Each bug flag has a known correct verdict, so Kazu's own end-to-end tests can check that a regression is reported as `REGRESSED`, a flake as flaky, an unchanged system as `pass`, and a fault that never landed as an error rather than a pass. Some bugs are deliberately invisible to database invariants and only show up in latency or telemetry, which is the part unit and integration tests can't reach.
3. **The quickstart demo.** `cd examples/shop && kazu run` is the first thing a new user tries.

The shop follows the same packaging rules Kazu asks of customers, which are Antithesis's rules: a complete, isolated system (compose, or rendered Kubernetes manifests), no outbound internet, services reach each other by name.

Status: spec only. Code lands in follow-up PRs, one per build step.

---

## Non-goals

Keep it boring. Realistic enough to fail the way real systems fail, and no more.

- No auth, no UI, no real Stripe.
- No business logic beyond what an invariant needs.
- Target: under ~1,000 lines of Go across all services, plus SQL, compose and manifests.

If a feature doesn't serve a row in the ground-truth matrix, it doesn't go in.

---

## Services

All services are Go, standard library first. Services find each other **by name** (`payments`, `postgres`, ...), read from env vars with those names as defaults.

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

### Telemetry

Every service uses the standard OpenTelemetry Go SDK and exports metrics over OTLP to `OTEL_EXPORTER_OTLP_ENDPOINT`, which the compose file points at an `otel-collector` service, the way a real team's local setup would. Kazu aliases that name to its own receiver (DESIGN.md §6.8), so the services need no changes to be measured.

| Metric | Type | Service | Meaning |
|---|---|---|---|
| `shop.orders.created` | counter | checkout | Orders accepted by `POST /orders` |
| `shop.payments.attempts` | counter, attr `outcome` | checkout | Every call to payments, including retries and reconciler calls |
| `shop.db.pool.acquire` | histogram (seconds) | checkout, payments | Time spent waiting for a database connection |
| `shop.orders.pending` | gauge | checkout | Orders currently `pending` (the reconciler's backlog) |
| `shop.ledger.lag` | histogram (seconds) | worker | From `order.paid` publish to ledger write (step 4) |

Database pools are deliberately small (10 connections), so a leak or a slow dependency shows up as pool wait within a 30-second trial.

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

`Check(CheckRequest{order_id, amount_cents}) → CheckResponse{allow}`. Deterministic: deny if `amount_cents > 100000`. Stateless, no database.

### `worker`

Consumes `order.paid`. For each message, writes two ledger entries (debit customer, credit merchant) in one transaction, then acks. Ledger entries for one order always sum to zero.

### `fakestripe`

HTTPS with a self-signed CA shipped in the repo. `POST /v1/charges` returns 200 with a charge id, idempotent on the `Idempotency-Key` header, in memory. It stands in for an external API the way stripe-mock would in a sealed system, and exercises TLS edges and `kazu doctor`'s TLS report.

---

## Data

`db/schema.sql`, applied by an init container (compose) or init Job (Kubernetes):

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

`cmd/load`: a small Go program, shipped as the `load` service inside the sealed system (a compose service, or a Job on Kubernetes). Kazu starts it once the system is ready, and its exit ends the traffic phase. It sends `N` orders at `R` requests/s to `checkout`, each with a fresh idempotency key derived from `KAZU_SEED` and the request number, and the run-id header from `KAZU_RUN_ID`. Amounts come from the same seed: about 70% below 1,000 cents, 30% at or above, so the fraud edge is exercised. Defaults: 300 orders at 10/s (30 s), small enough to keep a trial near the ~100 s budget in DESIGN.md §6.4.

`--double-submit` sends every order twice at once with the same key (a double-click). The race flag needs it.

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
| `SHOP_BUG_RETRY_NO_BACKOFF` | checkout | Retries payments up to 10 times with no backoff. Still idempotent, so every database invariant passes; it just multiplies load on a struggling dependency. | `payments-down` | Retry amplification |
| `SHOP_BUG_POOL_LEAK` | payments | When the fraud call fails, returns 503 without releasing its database connection until a 5 s timeout. Correct results, but the pool drains and every request waits. | `flaky-fraud` | Resource leak on an error path |

`RETRY_NO_BACKOFF` and `POOL_LEAK` are the "why not unit or integration tests?" rows: no invariant over the data catches them, and they only appear when the whole system runs under a fault. They are caught by telemetry and by latency regression.

Deliberately left out for now, because a toy can't make them meaningful without extra machinery: full retry storms and metastable failure (need load near capacity), missing bulkheads (need per-route SLOs), poison messages (need a dead-letter path). See Open questions.

### Routing traps

Off by default. These test Kazu's routing and its landed check (DESIGN.md §6.6), not the shop's resilience.

| Trap | What it does | What Kazu must do |
|---|---|---|
| `SHOP_TRAP_PAYMENTS_BY_IP` | Checkout reaches payments by its static IP instead of the name `payments`, so the hosts-file alias never applies | Report every fault on `checkout -> payments` as **not exercised**, never `pass` |
| `SHOP_TRAP_NO_OTEL` | Services export telemetry nowhere (SDK disabled) | Report every metric invariant as **no data**, never `pass` |

---

## `kazu.yaml`

```yaml
system: docker-compose.yml       # Kubernetes: system: { manifests: ./k8s/rendered }
traffic: load                    # a service in the system (compose service / k8s Job) that Kazu starts per trial

edges:                           # faults apply only to declared edges
  load -> checkout: http                                     # never faulted; measures latency as clients see it
  checkout -> payments: http
  checkout -> postgres: tcp
  payments -> postgres: tcp
  payments -> fraud: grpc                                    # step 2
  payments -> fakestripe: { tcp, tls: true }                 # step 4; HTTP faults would need the test CA
  checkout -> rabbitmq: tcp                                  # step 4
  worker -> rabbitmq: tcp                                    # step 4

connections:                     # for invariant callbacks; references only, never values
  postgres: ${env:SHOP_DATABASE_URL}

telemetry:
  collector: otel-collector      # alias this name to Kazu's OTLP receiver

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
  flaky-fraud:                                     # step 2
    payments -> fraud: errors 10% UNAVAILABLE
  db-blip:
    postgres: down for 15s after 10s               # every declared edge into postgres
  payments-db-blip:
    payments -> postgres: down for 15s after 10s   # one edge only
  worker-crash:
    worker: crash after 15s, restart after 5s

invariants:
  - no_double_charge                               # resilience/invariants.py
  - paid_means_charged
  - ledger_balances
  - orders_settle
  - recovers: checkout within 60s
  - no_regression: checkout p99 within 20%         # proxy-measured on load -> checkout
  - metric:                                        # retries per order
      query: sum(increase(shop_payments_attempts_total[5m])) / sum(increase(shop_orders_created_total[5m]))
      max: 3
  - metric:                                        # connection pool wait
      query: histogram_quantile(0.99, sum by (le) (rate(shop_db_pool_acquire_seconds_bucket[5m])))
      no_regression: within 50%

suites:
  pr: [baseline, slow-payments, payments-down, db-blip]
  release: all
```

`resilience/invariants.py`, written against the Python SDK as in DESIGN.md §4.3:

- `no_double_charge` (`@after`): no `order_id` with more than one charge in this run.
- `paid_means_charged` (`@after`): every `paid` order in this run has a charge.
- `ledger_balances` (`@during(every="1s")`): ledger entries for this run sum to zero.
- `orders_settle` (`@settles(within="60s")`): no order in this run is still `pending`, and every `paid` order has ledger entries (step 4).

### Kubernetes variant

`k8s/rendered/` holds the same system as raw manifests for a single-node K3s cluster, the same input Antithesis takes: fully qualified image references, readiness probes on every Deployment, the schema as an init Job, no Ingress or LoadBalancer Services. Same `kazu.yaml` apart from the `system:` line.

---

## Ground-truth matrix

Each row is an end-to-end test of Kazu itself. `fail_on: regression` throughout.

| # | Base flags | Head flags | Scenario | Expected verdict | Caught by | Tests | Step |
|---|---|---|---|---|---|---|---|
| 1 | — | — | all | `pass`, no violations | — | No false positives on an unchanged system | 1 |
| 2 | — | `RETRY_NEW_KEY` | `slow-payments` | `REGRESSED` | `no_double_charge` | Core loop: latency fault → invariant → verdict | 1 |
| 3 | — | `RETRY_NEW_KEY` | `baseline` | `pass` | — | Bug is latent until the fault fires | 1 |
| 4 | — | `FAIL_OPEN` | `payments-down` | `REGRESSED` | `paid_means_charged` | Dependency outage | 1 |
| 5 | — | `NO_RECONNECT` | `db-blip` | `REGRESSED` | `recovers` | `down` cuts live connections | 1 |
| 6 | — + trap `PAYMENTS_BY_IP` | `RETRY_NEW_KEY` + trap `PAYMENTS_BY_IP` | `slow-payments` | **not exercised** (error, not pass) | landed check | No false pass when a fault can't land | 1 |
| 7 | — | — | `payments-db-blip` | `pass`; checkout's DB traffic sees no fault | proxy counters | Per-edge routing: one caller's alias doesn't leak to another | 1 |
| 8 | `RETRY_NEW_KEY` | `RETRY_NEW_KEY` | `slow-payments` | `pass` (violation in both) | — | Regression, not violation, gates | 3 |
| 9 | — | `NO_TIMEOUT` | `payments-blackhole` | `REGRESSED` | `no_regression` (p99), `recovers` | Blackhole, latency and recovery checks | 2 |
| 10 | `RACE_P=0.1` | `RACE_P=0.1` | `baseline` + `--double-submit` | `flaky`, not regressed | `no_double_charge` | Flake classification | 3 |
| 11 | — | `RACE_P=0.1` | `baseline` + `--double-submit` | `REGRESSED` given enough trials | `no_double_charge` | Statistical power of adaptive trials | 3 |
| 12 | — | `NO_RECONCILE` | `payments-crash` | `REGRESSED` | `orders_settle` | Process faults; unknown outcomes | 4 |
| 13 | — | `ACK_BEFORE_WRITE` | `worker-crash` | `REGRESSED` | `orders_settle` | Process faults, `@settles` | 4 |
| 14 | — | `RETRY_NEW_KEY` | `slow-payments`, **on K3s** | `REGRESSED` | `no_double_charge` | Same verdict from the Kubernetes driver | 4 |
| 15 | — | `NO_RECONCILE` | `payments-crash`, **on K3s** | `REGRESSED` | `orders_settle` | Crash restarts the container in place, not a new pod | 4 |
| 16 | — | — | `baseline`, ×20 trial pairs | `pass` on `no_regression` every time | — | False-positive rate of relative latency checks on a noisy runner | 3 |
| 17 | — | `POOL_LEAK` | `flaky-fraud` | `REGRESSED`; all data invariants pass | `no_regression` (p99) | Latency regression from the proxy alone, no telemetry | 3 |
| 18 | — | `POOL_LEAK` | `flaky-fraud` | `REGRESSED` | pool-wait `metric` | Same bug, now named by the app's own telemetry | 4 |
| 19 | — | `RETRY_NO_BACKOFF` | `payments-down` | `REGRESSED`; all data invariants pass | retries-per-order `metric` | Retry amplification, invisible to data checks | 4 |
| 20 | trap `NO_OTEL` | trap `NO_OTEL` | `payments-down` | metric invariants **no data** (error, not pass) | no-data check | A missing signal is never a pass | 4 |

Rows 10 and 11 are the inputs to the verdict-statistics spike before step 3: measure how many trials row 11 needs to reach `REGRESSED` and how often row 10 is wrongly called a regression, across a range of `p`.

Row 6 is the most important safety property. If checkout reaches payments by IP, the latency fault never lands, and without the landed check the bug would pass silently. Row 20 is the same property for telemetry.

Row 16 calibrates the latency tolerance: it is the input for choosing default `no_regression` thresholds and the statistical test, alongside rows 10 and 11.

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

## Layout

```
examples/shop/
  SPEC.md                 # this file
  go.mod                  # own module, so the example reads like a customer repo
  docker-compose.yml
  kazu.yaml
  resilience/invariants.py
  expectations.yaml
  db/schema.sql
  k8s/rendered/           # raw manifests for K3s (step 4)
  services/checkout/  services/payments/  services/fraud/
  services/worker/    services/fakestripe/
  proto/fraud.proto
  cmd/load/
```

A separate Go module keeps the services' dependencies (pgx, amqp091-go, grpc) out of Kazu's `go.mod`, and makes the example look like what a customer would actually point Kazu at.

---

## Delivery, by build step

| Step | Adds | Rows |
|---|---|---|
| 1 | `checkout` (with reconciler), `payments`, `postgres`, schema, `cmd/load`, compose, `RETRY_NEW_KEY`, `FAIL_OPEN`, `NO_RECONNECT`, `PAYMENTS_BY_IP` trap, `no_double_charge`, `paid_means_charged` | 1–7 |
| 2 | `fraud` (gRPC), `flaky-fraud`, `NO_TIMEOUT`, `POOL_LEAK`, `orders_settle`, `ledger_balances` (no-op until step 4) | 9 |
| 3 | `RACE_P`, `--double-submit`, `expectations.yaml`, e2e harness | 8, 10, 11, 16, 17 |
| 4 | `worker`, `rabbitmq`, `fakestripe`, `otel-collector` and service telemetry, `NO_RECONCILE`, `ACK_BEFORE_WRITE`, `RETRY_NO_BACKOFF`, `NO_OTEL` trap, `k8s/rendered/` | 12–15, 18–20 |

## Open questions

- `worker-crash` uses a wall-clock trigger because the worker has no inbound edge to count requests on. Revisit once raw-TCP request counting is settled (DESIGN.md §10).
- Whether `blackhole` lands in step 1 or 2 of the proxy; row 9 moves with it.
- Retry storms, bulkheads and poison messages: worth adding once Kazu has per-route SLOs and the shop has a load mode near capacity.

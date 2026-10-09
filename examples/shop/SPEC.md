# `examples/shop` — toy system spec

A small checkout system with **bugs you can switch on**. It is three things at once:

1. **The thing Kazu is built against.** Build step 1 targets `checkout → payments → postgres`; later steps add services as Kazu gains features.
2. **Kazu's test oracle.** Each bug flag has a known correct verdict, so Kazu's own end-to-end tests can check that a regression is reported as `REGRESSED`, a flake as flaky, and an unchanged system as `pass`.
3. **The quickstart demo.** `cd examples/shop && kazu run` is the first thing a new user tries.

Status: spec only. Code lands in follow-up PRs, one per build step.

---

## Non-goals

Keep it boring. Realistic enough to fail the way real systems fail, and no more.

- No auth, no UI, no real Stripe, no Kubernetes manifests.
- No business logic beyond what an invariant needs to check.
- Target: under ~1,000 lines of Go across all services, plus SQL and compose.

If a feature doesn't serve a scenario in the ground-truth matrix, it doesn't go in.

---

## Services

All services are Go, standard library first. Each reads its dependency addresses from env vars (the contract in DESIGN.md §5: customer services read addresses from config, so Kazu can rewire them through proxies).

| Service | Protocol | Port | Calls | Arrives in build step |
|---|---|---|---|---|
| `checkout` | HTTP | 8080 | `payments`, `postgres`, `rabbitmq` (step 4) | 1 |
| `payments` | HTTP | 8081 | `fraud` (step 2), `stripe` (step 4), `postgres` | 1 |
| `postgres` | TCP | 5432 | — | 1 |
| `fraud` | gRPC | 9090 | — | 2 |
| `worker` | AMQP consumer | — | `rabbitmq`, `postgres` | 4 |
| `rabbitmq` | TCP | 5672 | — | 4 |
| `fakestripe` | HTTPS (self-signed CA) | 8443 | — | 4 |

Env vars: `PAYMENTS_URL`, `FRAUD_ADDR`, `STRIPE_URL`, `DATABASE_URL`, `AMQP_URL`, plus `KAZU_TAG_HEADER` (default `X-Kazu-Run-Id`).

### `checkout`

| Endpoint | Behaviour |
|---|---|
| `POST /orders` | Body `{amount_cents}`, header `Idempotency-Key` (required). Inserts an order as `pending`, calls `POST /charges` on payments with a 1 s client timeout and up to 2 retries **reusing the same key**, marks the order `paid` or `failed`. From step 4, publishes `order.paid`. Returns the order. A repeat of the same key returns the existing order, resuming the payment call if it is still `pending` (so concurrent duplicates both reach payments with the same key). |
| `GET /orders/{id}` | The order. |
| `GET /internal/orders?run_id=&status=` | Orders for one run, filtered by status. Used by `@settles` invariants. |
| `GET /healthz` | 200 once the database is reachable. |

### `payments`

| Endpoint | Behaviour |
|---|---|
| `POST /charges` | Body `{order_id, amount_cents}`, header `Idempotency-Key`. Step 2+: calls `fraud.Check` first (denied → 402). Deduplicates on `idempotency_key` under a Postgres advisory lock (lock, look up, insert if absent), so a repeat returns the existing charge. Step 4+: calls `fakestripe` `POST /v1/charges` with the same key. |
| `GET /healthz` | 200 once the database is reachable. |

### `fraud` (gRPC)

`Check(CheckRequest{order_id, amount_cents}) → CheckResponse{allow}`. Deterministic: deny if `amount_cents > 100000`. Stateless, no database, so it is also the obvious candidate for a Kazu stub.

### `worker`

Consumes `order.paid`. For each message, writes two ledger entries (debit customer, credit merchant) in one transaction, then acks. Ledger entries for one order always sum to zero.

### `fakestripe`

HTTPS with a self-signed CA shipped in the repo. `POST /v1/charges` returns 200 with a charge id, idempotent on the `Idempotency-Key` header, in memory. It exists to exercise `upstream:` edges and `kazu doctor`'s TLS warning, not to model Stripe.

---

## Data

`db/schema.sql`, applied by an init container:

```sql
create table orders (
  id              text primary key,         -- derived from idempotency key
  run_id          text not null,
  idempotency_key text not null unique,
  amount_cents    bigint not null,
  status          text not null check (status in ('pending','paid','failed')),
  created_at      timestamptz not null default now()
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

`cmd/load`: a small Go program, so the example needs nothing beyond Docker. It sends `N` orders at `R` requests/s to `checkout`, each with a fresh idempotency key derived from `KAZU_SEED` and the request number, and the run-id header from `KAZU_RUN_ID`. Defaults: 300 orders at 10/s (30 s), small enough to keep a trial near the ~100 s budget in DESIGN.md §6.4.

```yaml
traffic: go run ./cmd/load --orders 300 --rate 10
```

---

## Bug flags

Each flag is an env var read at startup. Base and head are the **same images with different flags**, so a regression can be produced without a second git commit.

| Flag | Service | The bug | Realistic because |
|---|---|---|---|
| `SHOP_BUG_RETRY_NEW_KEY` | checkout | Retries to payments generate a fresh idempotency key per attempt. A timed-out call that payments actually completed is charged again. | The classic double charge. |
| `SHOP_BUG_NO_TIMEOUT` | checkout | No client timeout on payments calls; requests hang as long as payments does. | Default HTTP clients in most languages. |
| `SHOP_BUG_ACK_BEFORE_WRITE` | worker | Acks the message, then writes ledger entries. A crash in between loses the entries. A fixed 50 ms gap between ack and write keeps the window wide enough to hit reliably. | At-most-once by accident. |
| `SHOP_BUG_RACE_P=<p>` | payments | With probability `p` per request, skips the advisory lock, leaving a bare check-then-insert; two concurrent requests with the same key can both insert. Uses the service's own unseeded RNG **on purpose**: this is nondeterminism Kazu does not control. | Check-then-act races. |

The race needs concurrent duplicates, so `cmd/load --double-submit` sends every order twice at once with the same key (a double-click). Both reach payments through checkout's resume path.

---

## `resilience.yaml`

```yaml
system: docker-compose.yml
traffic: go run ./cmd/load --orders 300 --rate 10

dependencies:
  checkout -> payments: http
  checkout -> postgres: tcp
  payments -> postgres: tcp
  payments -> fraud: grpc                                     # step 2
  payments -> stripe: { http, upstream: fakestripe:8443 }     # step 4
  checkout -> rabbitmq: tcp                                   # step 4
  worker -> rabbitmq: tcp                                     # step 4

scenarios:
  baseline: {}                                     # no faults; a sanity floor
  slow-payments:
    checkout -> payments: latency 1500ms ±200ms    # above checkout's 1 s timeout
  payments-blackhole:
    checkout -> payments: blackhole for 20s after 10s
  db-blip:
    postgres: down for 15s after 10s
  worker-crash:
    worker: crash after 15s, restart after 5s

invariants:
  - no_double_charge                               # resilience/invariants.py
  - ledger_balances
  - orders_settle
  - slo: checkout p99 < 1500ms
  - recovers: checkout within 60s

suites:
  pr: [baseline, slow-payments, db-blip]
  release: all
```

`resilience/invariants.py` holds three functions, written against the Python SDK exactly as in DESIGN.md §4.3:

- `no_double_charge` (`@after`): no `order_id` with more than one charge in this run.
- `ledger_balances` (`@during(every="1s")`): ledger entries for this run sum to zero.
- `orders_settle` (`@settles(within="60s")`): no order in this run is still `pending`, and every `paid` order has ledger entries (step 4).

---

## Ground-truth matrix

This is the point of the example. Each row is an end-to-end test of Kazu itself. `fail_on: regression` throughout.

| # | Base flags | Head flags | Scenario | Expected verdict | Caught by | Tests | Step |
|---|---|---|---|---|---|---|---|
| 1 | — | — | all | `pass`, no violations | — | No false positives on an unchanged system | 1 |
| 2 | — | `RETRY_NEW_KEY` | `slow-payments` | `REGRESSED` | `no_double_charge` | Core loop: latency fault → invariant → verdict | 1 |
| 3 | — | `RETRY_NEW_KEY` | `baseline` | `pass` | — | Bug is latent until the fault fires | 1 |
| 4 | `RETRY_NEW_KEY` | `RETRY_NEW_KEY` | `slow-payments` | `pass` (violation in both) | — | Regression, not violation, gates | 3 |
| 5 | — | `NO_TIMEOUT` | `payments-blackhole` | `REGRESSED` | `slo`, `recovers` | Blackhole, SLO and recovery checks | 2 |
| 6 | `RACE_P=0.1` | `RACE_P=0.1` | `baseline` + `--double-submit` | `flaky`, not regressed | `no_double_charge` | Flake classification | 3 |
| 7 | — | `RACE_P=0.1` | `baseline` + `--double-submit` | `REGRESSED` given enough trials | `no_double_charge` | Statistical power of adaptive trials | 3 |
| 8 | — | `ACK_BEFORE_WRITE` | `worker-crash` | `REGRESSED` | `orders_settle` | Process faults, `@settles` | 4 |
| 9 | — | — | `db-blip` | `pass`, no violations | — | Services recover from a dependency outage | 1 |

Rows 6 and 7 are the inputs to the verdict-statistics spike before step 3: measure how many trials row 7 needs to reach `REGRESSED` and how often row 6 is wrongly called a regression, across a range of `p`.

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
  resilience.yaml
  resilience/invariants.py
  expectations.yaml
  db/schema.sql
  services/checkout/  services/payments/  services/fraud/
  services/worker/    services/fakestripe/
  proto/fraud.proto
  cmd/load/
```

A separate Go module keeps the services' dependencies (pgx, amqp091-go, grpc) out of Kazu's `go.mod`, and makes the example look like what a customer would actually point Kazu at.

---

## Delivery, by build step

| Step | Adds |
|---|---|
| 1 | `checkout`, `payments`, `postgres`, schema, `cmd/load`, compose, `RETRY_NEW_KEY`, `no_double_charge`. Matrix rows 1–3, 9. |
| 2 | `fraud` (gRPC), `NO_TIMEOUT`, `ledger_balances` (no-op until step 4), `orders_settle`. Row 5. |
| 3 | `RACE_P`, `expectations.yaml`, e2e test harness. Rows 4, 6, 7. |
| 4 | `worker`, `rabbitmq`, `fakestripe`, `ACK_BEFORE_WRITE`. Row 8. |

## Open questions

- `worker-crash` uses a wall-clock trigger because the worker has no inbound edge to count requests on. Revisit once raw-TCP request counting is settled (DESIGN.md §10).
- Whether `blackhole` lands in step 1 or 2 of the proxy; row 5 moves with it.

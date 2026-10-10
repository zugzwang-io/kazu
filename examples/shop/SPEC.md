# `examples/shop` — toy system spec

A small checkout system with **realistic resilience mechanisms and bugs you can switch on**. It is three things at once:

1. **The thing Kazu is built against.** Build step 1 targets `checkout → payments → postgres`; later steps add services as Kazu gains features.
2. **Kazu's test oracle.** Every Kazu PR runs the shop the way a customer would and asserts the results (see "How Kazu's PRs use the shop").
3. **The quickstart demo.** `cd examples/shop && kazu run payments_slow --base TAG=dev --head TAG=dev --head SHOP_BUG_TIMEOUT_AS_FAILURE=1`: one bug switched on in head, one regression found.

It is laid out exactly like a customer repo (DESIGN.md §4.1): the **system** in `docker-compose.yml`, **traffic** as the k6 template, **checks** in `checks/` for Kazu's Python SDK image, and **`kazu.yaml`**. It follows the packaging rules Kazu asks of customers, which are Antithesis's rules: a complete, isolated system, no outbound internet, services reach each other by name.

Status: spec, plus the blind-written `kazu.yaml` and `checks/shop.py`. Service code lands in follow-up PRs, one per build step.

---

## Ground rules: no self-deception

The shop exists to show that Kazu finds problems real teams would otherwise ship. It is easy to fake that: plant a bug, then write a check that only makes sense if you know about the bug. These rules prevent it.

1. **Every bug must pass the shop's own test suite.** The shop has the tests a decent team writes (below). CI runs that suite with each bug flag switched on, and fails if any flag is caught by it. A bug ordinary tests would find doesn't demonstrate anything.
2. **Checks and scenarios are written blind.** `kazu.yaml` and `checks/shop.py` were written by an author who saw only `blind/BRIEF.md`: the shop's design, its business rules, its telemetry and what Kazu can do. They never saw the bug flags, the traps or the matrices. Their unedited answer is `blind/RESPONSE.md`.
3. **Nobody who knows the bugs edits the checks.** The only allowed edits correct a fact the brief left ambiguous or follow a diagnostic Kazu itself prints, and each is logged in `blind/EDITS.md`. If the checks ever need rewriting, a new blind author does it from an updated brief.
4. **Bugs are fixed before the checks are seen.** The bug definitions below were written before the blind checks existed and were not changed after.
5. **Misses are results, not things to fix in the shop.** The value matrix records a predicted outcome for every bug, including predicted misses. The e2e gate asserts the actual outcome matches the recorded one. A miss is fixed only by a generic improvement to Kazu (defaults, auto scenarios, diagnostics) that would help every customer; then the recorded outcome flips.
6. **Value and safety are measured separately.** *Value rows* use only the blind config and measure what Kazu catches. *Safety rows* test Kazu's own mechanics (the landed check, no-data, statistics, drivers) with configs written for the test; they say nothing about value.

The headline number is the **catch rate** over value rows, measured on real runs. It is not 100% by construction.

---

## Non-goals

Keep it boring. Realistic enough to fail the way real systems fail, and no more.

- No auth, no UI, no real Stripe.
- No business logic beyond the business rules below.
- Target: about 1,500 lines of Go across all services, most of it configuring standard libraries, plus SQL, compose and manifests.
- No circuit breakers, caches, autoscaling or service mesh: backoff plus load shedding already produce the overload dynamics, and the rest are system-level choices rather than per-service mechanisms.

---

## Business rules

The product spec the blind author worked from (`blind/BRIEF.md`):

1. One order per idempotency key.
2. A customer is charged at most once per order.
3. A paid order has exactly one charge. A failed order has no charge.
4. Every order eventually leaves `pending` once dependencies are healthy.
5. Every paid order gets ledger entries, and each order's ledger entries sum to zero. Parked orders are excluded (handled manually).
6. Orders over 100000 cents are rejected by fraud.

---

## Services

All services are Go. Services find each other **by name** (`payments`, `postgres`, ...), read from env vars with those names as defaults.

| Service | Protocol | Port | Calls | Arrives in build step |
|---|---|---|---|---|
| `checkout` | HTTP | 8080 | `payments`, `postgres`, `rabbitmq` (step 4) | 1 |
| `payments` | HTTP | 8081 | `fraud` (step 2), `fakestripe`, `postgres` | 1 |
| `postgres` | TCP | 5432 | — | 1 |
| `fakestripe` | HTTPS (test CA in `certs/`) | 8443 | — | 1 |
| `fraud` | gRPC | 9090 | — | 2 |
| `worker` | AMQP consumer | — | `rabbitmq`, `postgres` | 4 |
| `rabbitmq` | TCP | 5672 | — | 4 |
| `otel-collector` | OTLP | 4317 | — | 4 |

### Resilience mechanisms

Each is a standard library or pattern a real Go service uses, with one env var to tune it. Bug flags break exactly one of them.

| Mechanism | Implementation | Default |
|---|---|---|
| Database pools | `pgxpool` | 10 connections per service, 500 ms acquire timeout, redials after failures |
| Timeouts | `net/http` client; Postgres `statement_timeout`; request context passed to every downstream call and query | 1 s per payments attempt; 2 s per query; deadlines flow downstream |
| Retries | `cenkalti/backoff` | Exponential backoff with full jitter, max 3 attempts, same idempotency key; only timeouts, 5xx and refused connections |
| Reconciler | Goroutine in checkout | Every 5 s, re-sends orders pending > 10 s with their original key |
| Load shedding | Concurrency limit in payments (`x/sync/semaphore`) | 20 requests in flight; beyond that, an immediate 503 |
| Queue consumption | RabbitMQ prefetch, dead-letter exchange | Prefetch 10; after 3 failed deliveries → dead-letter queue, order marked `parked` |
| Realistic capacity | `fakestripe` responds in 200 ms ± 50 ms, like a real card processor; every request does real database work | Payments capacity ≈ 20 in flight / ~0.25 s ≈ 80 charges/s, set by limits and latencies rather than runner CPU, so it is the same on every machine |

The default traffic (100 iterations/s, 70% checkout) puts payments near 70 charges/s, close to capacity, so faults cause real queueing, pool pressure and shedding.

### `checkout`

| Endpoint | Behaviour |
|---|---|
| `POST /orders` | Body `{amount_cents}`, header `Idempotency-Key` (required). Inserts an order as `pending` and calls `POST /charges` on payments with the timeout and retry policy above. **2xx** → `paid`; **4xx or connection refused** (definitely not processed) → `failed`; **timeout or connection reset** (outcome unknown) → stays `pending` for the reconciler. Publishes `order.paid` (step 4). A repeat of the same key returns the existing order, resuming the payment call if it is still `pending`. |
| `GET /orders/{id}` | The order. |
| `GET /orders?status=` | Orders filtered by status. |
| `GET /healthz` | 200 once the database is reachable. |

### `payments`

| Endpoint | Behaviour |
|---|---|
| `POST /charges` | Body `{order_id, amount_cents}`, header `Idempotency-Key`. Step 2+: calls `fraud.Check` for orders of 1,000 cents or more (denied → 402). Deduplicates on `idempotency_key` under a Postgres advisory lock (lock, look up, insert if absent), so a repeat returns the existing charge. Calls `fakestripe` `POST /v1/charges` with the same key. Load shedding as above. |
| `GET /healthz` | 200 once the database is reachable. |

### `fraud` (gRPC)

`Check(CheckRequest{order_id, amount_cents}) → CheckResponse{allow}`. Deterministic: deny if `amount_cents > 100000`. Stateless.

### `worker`

Consumes `order.paid` with prefetch 10. For each message, writes two ledger entries (debit customer, credit merchant) in one transaction, then acks. After 3 failed deliveries the message goes to the dead-letter queue and the order is marked `parked`.

### `fakestripe`

HTTPS with the test CA in `certs/`. `POST /v1/charges` responds in 200 ms ± 50 ms with a charge id, idempotent on `Idempotency-Key`, in memory. It stands in for an external card processor the way stripe-mock would in a sealed system.

---

## Data

`db/schema.sql`, one database `shop`, applied by an init container (compose) or init Job (Kubernetes):

```sql
create table orders (
  id              text primary key,         -- derived from idempotency key
  idempotency_key text not null unique,
  amount_cents    bigint not null,
  status          text not null check (status in ('pending','paid','failed','parked')),
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now()
);

create table charges (
  id              text primary key,
  order_id        text not null,
  idempotency_key text not null,           -- deliberately not unique: payments dedups under an
  amount_cents    bigint not null,          -- advisory lock, so the race flag has something to break
  created_at      timestamptz not null default now()
);

create table ledger_entries (
  id           bigserial primary key,
  order_id     text not null,
  account      text not null,
  amount_cents bigint not null              -- signed; per order sums to 0
);
```

Every trial gets a fresh system, so checks query the tables directly; there is no run tagging. Order and charge ids derive from the idempotency key; services use no randomness of their own, except the race flag below.

---

## Telemetry

Every service uses the standard OpenTelemetry Go SDK and exports over OTLP to `OTEL_EXPORTER_OTLP_ENDPOINT`, which the compose file points at an `otel-collector` service. Kazu aliases that name to its own receiver (DESIGN.md §6.8).

| Metric | Type | Service | Meaning |
|---|---|---|---|
| HTTP server/client request duration | histogram, by route | all HTTP services | OpenTelemetry semantic conventions |
| gRPC durations | histogram | payments, fraud | OpenTelemetry semantic conventions |
| `shop.orders.created` | counter | checkout | Orders accepted by `POST /orders` |
| `shop.payments.attempts` | counter, attr `outcome` | checkout | Every call to payments, including retries and reconciler calls |
| `shop.db.pool.acquire` | histogram (seconds) | checkout, payments | Time waiting for a database connection |
| `shop.orders.pending` | gauge | checkout | Orders currently `pending` |
| `shop.ledger.lag` | histogram (seconds) | worker | From `order.paid` publish to ledger write |
| `shop.queue.depth` | gauge | worker | Messages waiting in `order.paid` |

---

## Traffic

`load/traffic.js`: Kazu's k6 template (DESIGN.md §4.3), edited the way a customer would, run in `grafana/k6`. Kazu starts it once the system is ready; it sends at a constant arrival rate and exits after `KAZU_DURATION`.

| Env var | Set by | Effect |
|---|---|---|
| `KAZU_SEED` | Kazu | Idempotency keys and amounts derive from it, so base and head send the same sequence. Amounts: about 70% below 1,000 cents, 30% at or above (so the fraud edge is exercised), and about 1% above the fraud limit |
| `KAZU_DURATION` | Kazu | Per scenario; the blind config sets `duration: 90s` on its own scenarios |
| `FLOW` | `kazu.yaml` | Weighted mix: `checkout` (create an order, then read it) and `browse` (read existing orders). Blind config: `checkout:7,browse:3` |
| `RATE` | template default | 100 iterations/s |
| `DUPLICATE_RATE` | `kazu.yaml` | Fraction of orders submitted twice at once with the same key, like double-clicks. Blind config: 0.05, and 0.3 in `double_click` |

The load never judges; it exits 0 if it sent its requests, whatever the responses were.

---

## The shop's own test suite

What a decent team would have before Kazu, and the bar every bug must clear (ground rule 1). It runs with `go test ./...` in the shop's module, against a real Postgres for integration tests.

| Layer | Covers |
|---|---|
| Unit, mocked dependencies | Order creation and idempotent repeat; each payments outcome (2xx → paid, 4xx → failed, refused → failed, timeout → pending); retry policy (attempt count, same key, which errors retry); reconciler picks up a pending order and resolves it; fraud boundary (100000 allowed, 100001 denied); payments dedup returns the existing charge; load shedding returns 503 above the limit; worker writes two balanced entries and acks; a message failing 3 times is dead-lettered and its order parked |
| Integration, real Postgres and RabbitMQ, happy path | Create order → paid → one charge → two ledger entries; a repeated key returns the same order; an over-limit order fails with no charge |

CI runs the suite once per bug flag with the flag on. A flag that makes any test fail is disqualified as a demonstration and must be rewritten or dropped. Where a bug's realistic version changes a test along with the code (`TIMEOUT_AS_FAILURE`, `RETRY_NO_BACKOFF`, `NO_SHEDDING`, `REQUEUE_FOREVER`), the flag carries that test change too, the way the PR would.

---

## Bug flags

Each flag is an env var read at startup, passed through compose interpolation (`SHOP_BUG_X: ${SHOP_BUG_X:-}`), so base and head are the same images with different variables (DESIGN.md §4.1):

```
kazu run payments_slow --base TAG=dev --head TAG=dev --head SHOP_BUG_TIMEOUT_AS_FAILURE=1
```

Each is a mistake a reasonable developer could make in a reviewed PR, and each passes the test suite above. Definitions were fixed before the blind checks were written (ground rule 4).

| Flag | Service | The change | Why ordinary tests miss it |
|---|---|---|---|
| `SHOP_BUG_RETRY_NEW_KEY` | checkout | The reconciler builds a fresh idempotency key when it re-sends a pending order; normal retries still reuse the key | Reconciler tests mock payments; the double charge needs a call that timed out at checkout but completed at payments |
| `SHOP_BUG_TIMEOUT_AS_FAILURE` | checkout | A timeout is treated as "definitely not charged": the order is marked `failed` instead of left `pending`. The unit test is changed to match, because the developer believes it | The test asserts the new behaviour; only real latency lets payments complete after checkout gave up |
| `SHOP_BUG_NO_DEADLINE_PROPAGATION` | checkout | Database queries use `context.Background()` instead of the request context | Results are identical; it only matters when requests are abandoned under a slow database |
| `SHOP_BUG_UNBOUNDED_RETRY_QUEUE` | checkout | Failed payment calls are also queued in memory for a background retry, with no bound | Correct results; memory only grows during a sustained outage |
| `SHOP_BUG_RETRY_NO_BACKOFF` | checkout | "Payments is flaky, retry harder": up to 10 attempts, no backoff | Retry tests mock payments and pass; the harm is load on a struggling dependency |
| `SHOP_BUG_SHARED_POOL` | checkout | Holds its database connection while calling payments, to update the order in the same transaction | Correct results; pool starvation needs concurrency and a slow dependency |
| `SHOP_BUG_POOL_LEAK` | payments | When the fraud call fails, returns 503 without releasing its database connection until a 5 s timeout | Correct results; the pool only drains under concurrent load |
| `SHOP_BUG_NO_RECONNECT` | payments | Opens one `pgx.Conn` at startup "for performance" instead of a pool, and never redials | Tests never restart the database mid-test |
| `SHOP_BUG_RACE_P=<p>` | payments | With probability `p` per request, skips the advisory lock, leaving a bare check-then-insert. Uses the service's own unseeded RNG on purpose: nondeterminism Kazu does not control | Needs concurrent duplicate requests |
| `SHOP_BUG_NO_SHEDDING` | payments | The concurrency limit is removed "to stop rejecting customers"; requests queue instead | Correct results; queueing only hurts near capacity |
| `SHOP_BUG_ACK_BEFORE_WRITE` | worker | Acks the message, then writes ledger entries, with a fixed 50 ms gap | Needs a crash in that window |
| `SHOP_BUG_REQUEUE_FOREVER` | worker | Failed messages are requeued without a delivery limit, so nothing is dead-lettered | The dead-letter unit test is removed with the feature; harm needs a message that can never succeed |

Removed from earlier drafts because ordinary tests catch them: `NO_TIMEOUT` (linters flag a client without a timeout), `FAIL_OPEN` and `PANIC_ON_UNAVAILABLE` (unit tests of the error path), `NO_RECONCILE` (the reconciler unit test).

### Traps

Off by default. These exist for safety rows: they test Kazu's own safety checks, not the shop.

| Trap | What it does | What Kazu must do |
|---|---|---|
| `SHOP_TRAP_PAYMENTS_BY_IP` | Checkout reaches payments by its static IP instead of the name `payments`, so the hosts-file alias never applies | Report every fault on `checkout -> payments` as **not exercised**, never `pass` |
| `SHOP_TRAP_SCHEMA_V2` | Adds a `currency` column to `orders`; the safety config has one check that reads it | Report that check as **incompatible with base** when base runs without the flag |
| `SHOP_TRAP_NO_OTEL` | Services export telemetry nowhere | Report every metric check as **no data**, never `pass` |

---

## The blind config

`kazu.yaml` and `checks/shop.py` are committed in this directory, written blind (`blind/`). In summary:

- **Scenarios:** `baseline`; `payments_slow` (1.5 s on checkout → payments); `card_processor_slow` (1.2 s on payments → fakestripe); `payments_flaky` (30% 503s and 15% resets on checkout → payments); `card_processor_errors` (20% 500s from fakestripe); `fraud_slow` (800 ms on payments → fraud); `checkout_db_slow` (150 ms on checkout → postgres); `double_click` (30% duplicates while payments is slow); `peak_2x` (twice the default rate); plus `auto_scenarios` (crash each service, take each edge down).
- **Checks:** duplicate charges per order and per key; charges over the fraud limit; no order stuck pending; every paid order has exactly one matching charge; failed orders have no charge; no orphan charges; ledger complete and balanced; no ledger entries for unpaid orders; queue drains; a resubmitted key returns the same order; fraud limit enforced at the boundary; payments p99 and error rate no worse than base, and payments recovers; payment attempts per order within 25% of base; checkout pool-acquire p99 and ledger lag p99 within 50% of base. Plus Kazu's default checks.
- **Suites:** `pr` and `nightly`, the author's own choice.

---

## Value matrix

> **Round 0: contaminated, not counted.** An independent audit (`blind/AUDIT.md`) found that the brief leaked hints, the telemetry was designed with the bugs, the bug definitions weren't committed before the blind round, and several predictions below are probably wrong. The table and the "9 of 12" figure are kept as a record only. The next round follows the fixes listed in the audit.

Blind config only, every scenario (`--suite nightly`), `fail_on: regression`. Base has no flags. "Predicted" is the spec author's prediction from the blind config; the e2e gate records and asserts actual outcomes once the code exists, and the catch rate is computed from those.

| # | Head flag | Predicted | Exposed by (blind scenario) | Caught by (blind or default check) | Step |
|---|---|---|---|---|---|
| V0 | — | `pass` everywhere | all | — | 1 |
| V1 | `RETRY_NEW_KEY` | **caught** | `payments_slow`, `card_processor_slow` | `no_duplicate_charges_per_order`, `paid_orders_have_exactly_one_matching_charge` | 2 |
| V2 | `TIMEOUT_AS_FAILURE` | **caught** | `payments_slow`, `card_processor_slow` | `failed_orders_have_no_charge` | 2 |
| V3 | `NO_DEADLINE_PROPAGATION` | **missed** | — | Nothing abandons requests at checkout: k6's default timeout is 60 s and no scenario slows the database enough for queries to outlive their requests | 3 |
| V4 | `UNBOUNDED_RETRY_QUEUE` | **missed** | `auto-down-checkout-payments`, `payments_flaky` | Outages last 15–20 s; the queue grows by a few MB, far from an out-of-memory kill without a container memory limit | 4 |
| V5 | `RETRY_NO_BACKOFF` | **caught** | `payments_flaky`, `auto-down-checkout-payments` | `payment_attempts_per_order`; likely also `payments_errors` | 4 |
| V6 | `SHARED_POOL` | **caught** | `payments_slow`, `double_click` | `checkout_pool_acquire_p99`, default user-facing p99 | 3 |
| V7 | `POOL_LEAK` | **caught** | `auto-down-payments-fraud` | `payments_latency`, `payments_errors`, default user-facing p99 | 4 |
| V8 | `NO_RECONNECT` | **caught** | `auto-down-payments-postgres`, `auto-crash-postgres` | `payments_recovers`, default user-facing recovery | 4 |
| V9 | `RACE_P=0.5` | **caught** | `double_click`, and `DUPLICATE_RATE` everywhere | `no_duplicate_charges_per_order` | 3 |
| V10 | `NO_SHEDDING` | **caught** | `peak_2x` | `payments_latency`, default user-facing p99 | 3 |
| V11 | `ACK_BEFORE_WRITE` | **caught** | `auto-crash-worker` | `ledger_complete_and_balanced` | 4 |
| V12 | `REQUEUE_FOREVER` | **missed** | — | No traffic produces a message that can never succeed; after a transient outage, requeueing forever even settles more orders than dead-lettering | 4 |

**Round 0 prediction (not counted): 9 of 12.** The three predicted misses are the interesting part:
- **V3** suggests a generic Kazu improvement: a default scenario or traffic setting where clients give up (a realistic client timeout), since abandoned requests are common in production.
- **V4** suggests that sealed systems should run with memory limits, or that Kazu should report memory growth relative to base as a default check.
- **V12** is a genuine limit: Kazu can't find what the traffic never triggers. The fix is on the traffic side (the template could send a small share of malformed requests), not a shop-specific check.

Each is recorded as a miss until such a generic change lands; then the recorded outcome flips.

---

## Safety matrix

Configs written for the test (in `safety/`), allowed to be targeted because they test Kazu's mechanics, not its value.

| # | Base | Head | Scenario | Expected | Tests | Step |
|---|---|---|---|---|---|---|
| S1 | trap `PAYMENTS_BY_IP` | `RETRY_NEW_KEY` + trap `PAYMENTS_BY_IP` | latency on `checkout -> payments` | **not exercised**, never `pass` | The landed check: no false pass when a fault can't land | 1 |
| S2 | — | — | `down` on `payments -> postgres` only | `pass`; proxy counters show checkout's database traffic untouched | Per-edge routing: one caller's alias doesn't leak to another | 1 |
| S3 | — | — | an `after_requests` trigger on `checkout -> postgres` (TCP) | **config error** before any trial | Request-count triggers are rejected on TCP edges | 1 |
| S4 | `RETRY_NEW_KEY` | `RETRY_NEW_KEY` | `payments_slow` | `pass` (violation in both) | Regression, not violation, gates | 3 |
| S5 | `RACE_P=0.1` | `RACE_P=0.1` | `double_click` | `flaky`, not regressed | Flake classification | 3 |
| S6 | — | `RACE_P=0.1` | `double_click` | `REGRESSED` given enough trials | Statistical power of adaptive trials | 3 |
| S7 | — | — | `baseline`, repeated 20 times | `pass` on every `no_regression` in at least the target share of runs | False-positive rate of the per-run noise estimate | 3 |
| S8 | — | — | `baseline` with a 1% tolerance on user-facing p99 | **underpowered**, not pass or fail | Kazu says when noise is too large to decide | 3 |
| S9 | — | trap `SCHEMA_V2` | `baseline` | **incompatible with base** for the check reading `currency` | A check that errors on base is reported, not counted | 3 |
| S10 | trap `NO_OTEL` | trap `NO_OTEL` | `payments_flaky` | metric checks **no data**, never `pass` | A missing signal is never a pass | 4 |
| S11 | — | `NO_RECONNECT` | `auto_scenarios`, with every check removed | `REGRESSED` on the `payments -> postgres` down and `postgres` crash scenarios | Defaults and auto scenarios give a verdict from a near-empty config | 4 |
| S12 | — | `TIMEOUT_AS_FAILURE` | `payments_slow`, **on K3s** | `REGRESSED` | Same verdict from the Kubernetes driver | 4 |
| S13 | — | `NO_RECONNECT` | `auto-crash-postgres`, **on K3s** | `REGRESSED` | Crash restarts the container in place, not a new pod | 4 |

---

## How Kazu's PRs use the shop

Every Kazu PR runs the shop as a customer would (DESIGN.md §8, "Testing Kazu"): the PR's own `kazu` binary and SDK images, in the GitHub Actions workflow `kazu init --ci github` writes for customers, sharded across about 10 jobs. Rows join the gate in the build step that delivers what they need.

1. **Shop test suite per bug flag:** every flag must pass the shop's own tests (ground rule 1).
2. **Shop bug proofs:** each flag also has a plain Go test showing the bug happens without Kazu (for example, `TIMEOUT_AS_FAILURE` against a slow payments that completes leaves a charge on a failed order), so a failing row says whether the shop or Kazu broke.
3. **Value and safety matrices:** a small Go asserter, not Kazu, compares each row's JSON result with `expectations.yaml`. Any mismatch blocks the merge, including a predicted miss that is suddenly caught (update the record deliberately) or a caught bug that is suddenly missed (a Kazu regression).
4. **Catch rate** over value rows is printed in the job summary.

Statistical rows are made decisive for gating: V9 uses a race probability of 0.5 so detection is near certain, S5 asserts "flaky, not regressed" with a wide margin, and S7's 20 repeats run as 20 shards.

`expectations.yaml`:

```yaml
- id: V2
  kind: value
  head: { SHOP_BUG_TIMEOUT_AS_FAILURE: "1" }
  suite: nightly
  expect:
    regressed: { payments_slow: [failed_orders_have_no_charge], card_processor_slow: [failed_orders_have_no_charge] }
    pass: rest

- id: V12
  kind: value
  head: { SHOP_BUG_REQUEUE_FOREVER: "1" }
  suite: nightly
  expect: { pass: all }        # recorded miss: no traffic produces a poison message
```

Each row runs with a fixed `--seed`, so a failing row prints the `kazu replay` command like any other failure.

---

## Layout

```
examples/shop/
  SPEC.md                 # this file
  kazu.yaml               # written blind
  checks/shop.py          # written blind; mounted into the kazu-python SDK image
  blind/                  # round 0: BRIEF.md, RESPONSE.md, RESPONSE_shop.py, EDITS.md, AUDIT.md, README.md
  safety/                 # targeted configs for the safety matrix
  expectations.yaml       # value and safety matrices
  go.mod                  # own module, so the example reads like a customer repo
  docker-compose.yml
  load/traffic.js         # the k6 template, edited
  db/schema.sql
  certs/                  # test CA for fakestripe
  k8s/rendered/           # raw manifests for K3s (step 4)
  services/checkout/  services/payments/  services/fraud/
  services/worker/    services/fakestripe/
  proto/fraud.proto
```

A separate Go module keeps the services' dependencies out of Kazu's `go.mod`, and makes the example look like what a customer would point Kazu at.

---

## Delivery, by build step

| Step | Shop adds | Rows |
|---|---|---|
| 1 | `checkout` (with reconciler), `payments` (with shedding), `postgres`, `fakestripe`, schema, compose, `load/traffic.js`, the shop test suite and its per-flag CI run, `PAYMENTS_BY_IP` trap | V0, S1–S3 |
| 2 | `fraud` (gRPC), `RETRY_NEW_KEY`, `TIMEOUT_AS_FAILURE` | V1, V2 |
| 3 | `NO_DEADLINE_PROPAGATION`, `SHARED_POOL`, `RACE_P`, `NO_SHEDDING`, `SCHEMA_V2` trap, `expectations.yaml` asserter | V3, V6, V9, V10, S4–S9 |
| 4 | `worker`, `rabbitmq`, `otel-collector` and telemetry, `UNBOUNDED_RETRY_QUEUE`, `RETRY_NO_BACKOFF`, `POOL_LEAK`, `NO_RECONNECT`, `ACK_BEFORE_WRITE`, `REQUEUE_FOREVER`, `NO_OTEL` trap, `k8s/rendered/` | V4, V5, V7, V8, V11, V12, S10–S13 |

Value rows run the whole blind config, so before step 4 the gate runs it with the scenarios and checks Kazu can't yet support marked as skipped in the record, not removed from the config.

## Open questions

- Whether `blackhole` lands in step 1 or 2 of the proxy.
- Which generic Kazu change addresses each predicted miss (V3 client timeouts, V4 memory, V12 malformed traffic), and whether it belongs in defaults or the template.

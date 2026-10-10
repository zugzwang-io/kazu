# Brief given to the blind check author

Given verbatim to an author who had not seen the bug flags, traps or matrix. They were told not to read any files and to work only from this text.

---

You are a backend developer at a small company. Your team runs the "shop" system described below. Your manager has asked you to spend about an hour setting up Kazu, a new resilience regression testing tool, for the shop: decide which scenarios (faults) to run and which checks to write. Kazu will run in CI on every PR, comparing the PR ("head") against main ("base").

- Write what a competent, reasonable developer would actually write in about an hour, based on the system's business rules and general engineering/SRE experience. Do not try to guess at specific bugs; you don't know of any. Don't over-engineer, and don't under-do it either.

## The shop system

An e-commerce checkout backend. All services in Go, run with docker compose. Services reach each other by name.

Services:
- **checkout** (HTTP :8080). Endpoints:
  - `POST /orders` — body `{amount_cents}`, header `Idempotency-Key` (required). Creates an order as `pending`, calls payments `POST /charges` (1 s client timeout per attempt; up to 3 attempts with exponential backoff and jitter, reusing the same idempotency key; retries only on timeouts, 5xx and refused connections). On 2xx marks order `paid`; on 4xx or connection refused marks `failed`; on timeout/connection reset (outcome unknown) leaves it `pending` for the reconciler. Then publishes `order.paid` to RabbitMQ for paid orders. A repeat with the same Idempotency-Key returns the existing order (resuming the payment call if still pending).
  - `GET /orders/{id}` — read an order.
  - `GET /orders?status=...` — list orders by status.
  - `GET /healthz`.
  - Reconciler: background goroutine every 5 s; re-sends orders pending > 10 s to payments with their original key.
  - Uses a Postgres connection pool (10 connections, 500 ms acquire timeout). Request deadlines are passed to downstream calls and queries; Postgres `statement_timeout` is 2 s.
- **payments** (HTTP :8081). `POST /charges` — body `{order_id, amount_cents}`, header `Idempotency-Key`. Calls `fraud.Check` (gRPC) for orders of 1,000 cents or more (deny → 402). Deduplicates on the idempotency key (a repeat returns the existing charge). Then calls an external card processor ("fakestripe", HTTPS, a mock inside the system) with the same key. Admission control: at most 20 requests in flight; beyond that it immediately returns 503. Postgres pool of 10.
- **fraud** (gRPC :9090). `Check(order_id, amount_cents) → allow`; denies amounts > 100000 cents. Stateless.
- **worker**. Consumes `order.paid` from RabbitMQ (prefetch 10). For each message writes two ledger entries (debit customer, credit merchant) in one transaction, then acks. A message that fails 3 times goes to a dead-letter queue and its order is marked `parked` for manual review.
- postgres, rabbitmq, fakestripe (mock card processor), otel-collector.

Data (Postgres):
- `orders(id, idempotency_key unique, amount_cents, status in pending|paid|failed|parked, created_at, updated_at)`
- `charges(id, order_id, idempotency_key, amount_cents, created_at)`
- `ledger_entries(id, order_id, account, amount_cents signed)`

Business rules (from the product spec):
1. One order per idempotency key.
2. A customer is charged at most once per order.
3. A paid order has exactly one charge. A failed order has no charge.
4. Every order eventually leaves `pending` once dependencies are healthy.
5. Every paid order gets ledger entries, and each order's ledger entries sum to zero. Parked orders are excluded (handled manually).
6. Orders over 100000 cents are rejected by fraud.

Telemetry: every service exports OpenTelemetry metrics, including the standard HTTP server/client request duration (by route) and gRPC durations, plus: `shop.orders.created` (counter), `shop.payments.attempts` (counter, attr outcome; includes retries and reconciler calls), `shop.db.pool.acquire` (histogram, seconds; checkout and payments), `shop.orders.pending` (gauge), `shop.ledger.lag` (histogram: publish to ledger write), `shop.queue.depth` (gauge). In Kazu's PromQL these appear with Prometheus naming, e.g. `shop_payments_attempts_total`, `shop_db_pool_acquire_seconds_bucket`.

Traffic: a k6 script (`load/traffic.js`) that simulates users at the shop's typical peak rate. Knobs (env vars): `FLOW` mix (e.g. `checkout:7,browse:3`; browse = reading orders), `RATE` (iterations/s), `DUPLICATE_RATE` (fraction of orders submitted twice at once, like double-clicks; default 0).

## What Kazu can do

(A summary of DESIGN.md §4 as of 2026-10-10: `kazu.yaml` keys `system`, `traffic`, `edges`, `connections`, `scenarios` with structured faults, `auto_scenarios`, the `checks:` list and its entry kinds, `suites`, and the built-in default checks.)

## Deliverable

1. The complete `kazu.yaml` you would commit (edges, scenarios, checks, suites).
2. `checks/shop.py`: each check's name, decorator, what it asserts, and the SQL/HTTP/PromQL it uses.
3. For every scenario and every check, one line: why a reasonable developer would write it.
4. A short list of things you considered and deliberately skipped, and why.

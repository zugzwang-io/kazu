# Blind author's response

Unedited answer to `BRIEF.md`. The committed `../kazu.yaml` and `../checks/shop.py` are this, with only the edits listed in `EDITS.md`.

## Assumptions the author flagged

- Checkout and payments each have their own database on the same Postgres server, so checks compare orders and charges in Python instead of joining them in SQL.
- Auto scenario names guessed as `auto-crash-payments` and similar.
- Default traffic `RATE` assumed to be about 30 iterations/s, so `peak_2x` uses 60.

## 1. kazu.yaml

```yaml
system: docker-compose.yml

traffic:
  image: grafana/k6:0.52.0
  command: run /load/traffic.js
  volumes: ["./load:/load"]
  environment:
    FLOW: "checkout:7,browse:3"
    DUPLICATE_RATE: "0.05"      # small amount of double-submits in every scenario

edges:
  "traffic -> checkout":    http
  "checkout -> payments":   http
  "checkout -> postgres":   tcp
  "checkout -> rabbitmq":   tcp
  "payments -> fraud":      grpc
  "payments -> fakestripe": http
  "payments -> postgres":   tcp
  "worker -> rabbitmq":     tcp
  "worker -> postgres":     tcp

connections:
  checkout_db: postgres://shop:shop@postgres:5432/checkout
  payments_db: postgres://shop:shop@postgres:5432/payments
  checkout:    http://checkout:8080

auto_scenarios: true   # crash per service and down per edge

scenarios:
  baseline:
    faults: []
    duration: 90s

  payments_slow:                 # every attempt runs past checkout's 1s timeout
    faults:
      - { edge: "checkout -> payments", fault: latency, delay: 1500ms, jitter: 200ms, after: 20s, for: 20s }
    duration: 90s

  card_processor_slow:           # charge succeeds after checkout has given up
    faults:
      - { edge: "payments -> fakestripe", fault: latency, delay: 1200ms, after: 20s, for: 20s }
    duration: 90s

  payments_flaky:
    faults:
      - { edge: "checkout -> payments", fault: errors, rate: 30, code: 503, after: 20s, for: 20s }
      - { edge: "checkout -> payments", fault: reset,  rate: 15,            after: 20s, for: 20s }
    duration: 90s

  card_processor_errors:
    faults:
      - { edge: "payments -> fakestripe", fault: errors, rate: 20, code: 500, after: 20s, for: 20s }
    duration: 90s

  fraud_slow:                    # takes up most of the 1s charge budget
    faults:
      - { edge: "payments -> fraud", fault: latency, delay: 800ms, after: 20s, for: 20s }
    duration: 90s

  checkout_db_slow:              # pool of 10 with a 500ms acquire timeout
    faults:
      - { edge: "checkout -> postgres", fault: latency, delay: 150ms, jitter: 50ms, after: 20s, for: 20s }
    duration: 90s

  double_click:                  # duplicate submits while payments is slow (widest race window)
    traffic: { environment: { DUPLICATE_RATE: "0.3" } }
    faults:
      - { edge: "checkout -> payments", fault: latency, delay: 700ms, jitter: 300ms, after: 20s, for: 30s }
    duration: 90s

  peak_2x:                       # admission control (20 in flight -> 503)
    traffic: { environment: { RATE: "60" } }   # ~2x typical peak; adjust
    faults: []
    duration: 90s

checks:
  - image: ./checks              # checks/shop.py

  - name: no_duplicate_charges_per_order
    sql:
      on: payments_db
      query: SELECT order_id FROM charges GROUP BY order_id HAVING count(*) > 1
      expect: empty

  - name: no_duplicate_charges_per_key
    sql:
      on: payments_db
      query: SELECT idempotency_key FROM charges GROUP BY idempotency_key HAVING count(*) > 1
      expect: empty

  - name: no_charges_over_fraud_limit
    sql:
      on: payments_db
      query: SELECT id FROM charges WHERE amount_cents > 100000
      expect: empty

  - name: payments_latency
    no_regression: { service: payments, measure: p99, within: 20% }
  - name: payments_errors
    no_regression: { service: payments, measure: error_rate, within: 20% }
  - name: payments_recovers
    recovers: { service: payments, within: 60s }

  - name: payment_attempts_per_order
    metric:
      query: sum(increase(shop_payments_attempts_total[5m])) / sum(increase(shop_orders_created_total[5m]))
      within: 25%
  - name: checkout_pool_acquire_p99
    metric:
      query: histogram_quantile(0.99, sum by (le) (rate(shop_db_pool_acquire_seconds_bucket{service="checkout"}[1m])))
      within: 50%
  - name: ledger_lag_p99
    metric:
      query: histogram_quantile(0.99, sum by (le) (rate(shop_ledger_lag_seconds_bucket[1m])))
      within: 50%

suites:
  pr:
    - baseline
    - payments_slow
    - card_processor_slow
    - payments_flaky
    - double_click
    - fraud_slow
    - checkout_db_slow
    - auto-crash-payments
    - auto-crash-worker
    - auto-down-checkout-rabbitmq
  nightly:
    - baseline
    - payments_slow
    - card_processor_slow
    - payments_flaky
    - card_processor_errors
    - double_click
    - fraud_slow
    - checkout_db_slow
    - peak_2x
    - auto            # all generated crash/down scenarios
```

## 2. checks/shop.py

See `../checks/shop.py`; committed as written, apart from the edits in `EDITS.md`.

## 3. Why each one is there

**Scenarios**
- **baseline:** catches latency and error regressions with no faults, and checks the invariants on a healthy run.
- **auto_scenarios:** a cheap way to cover a crash or outage on every service and edge. Only the riskiest ones run on every PR.
- **payments_slow:** forces timeouts, retries with the same key, orders left pending and reconciler handoff: the "outcome unknown" paths behind rules 2 and 4.
- **card_processor_slow:** the card processor succeeds after checkout has given up, the classic way a customer gets charged twice (rule 2).
- **payments_flaky:** exercises both 5xx retries and connection resets, where the outcome is unknown and the order must stay pending.
- **card_processor_errors:** a third-party outage. Orders must end up consistently failed or paid, with no charge left on a failed order (rule 3).
- **fraud_slow:** fraud latency uses up most of checkout's 1 s budget and triggers retries that land on payments' dedupe.
- **checkout_db_slow:** a pool of 10 with a 500 ms acquire timeout is a common place for latency to blow up under load.
- **double_click:** rule 1 under concurrency, while payments is slow.
- **peak_2x (nightly only):** exercises the 20-in-flight admission limit and the retries that follow.
- **DUPLICATE_RATE 0.05 everywhere:** keeps the idempotency paths running in every scenario.

**Checks**
- **no_duplicate_charges_per_order / per_key:** rule 2, the most expensive bug a shop can have.
- **no_charges_over_fraud_limit:** rule 6, against all traffic.
- **no_order_stuck_pending:** rule 4. 60 s allows for the 10 s threshold, the 5 s tick and the retries.
- **paid_orders_have_exactly_one_matching_charge:** rule 3, plus charged amount equals order amount.
- **failed_orders_have_no_charge:** rule 3. A charge on a failed order means the customer paid for nothing.
- **no_orphan_charges:** money taken with no order is an untraceable support case.
- **ledger_complete_and_balanced:** rule 5. Exactly 2 entries, because a redelivered message would add 4 that still sum to zero.
- **no_ledger_for_unpaid_orders:** the books must not record revenue that was never charged.
- **queue_drains:** the worker caught up; stops the ledger check passing just because the run ended early.
- **resubmit_returns_same_order:** rule 1, tested directly.
- **fraud_limit_enforced:** rule 6 at the exact boundary.
- **payments no_regression / recovers:** the built-in checks only watch traffic into checkout, and checkout's retries can hide payments getting worse.
- **payment_attempts_per_order:** catches retry storms or broken backoff before they show up as user-facing errors.
- **checkout_pool_acquire_p99:** early warning for connection leaks or longer-held transactions.
- **ledger_lag_p99:** a slow ledger is invisible to users but matters to finance.

## 4. Considered and skipped

- **Absolute `slo` checks:** faults are injected on purpose, so fixed limits would always fail; head vs base is the right signal.
- **SQL check for unique idempotency keys on orders:** the unique constraint already guarantees it.
- **Checking the card processor's own charge list:** the `charges` table is close enough for now.
- **Asserting zero parked orders:** parking is legitimate when the worker's dependencies are down, and rule 5 excludes them.
- **cpu, memory, disk_full, bandwidth and pause faults:** low-signal for this system and noisy on shared runners.
- **Faults on the otel-collector:** not part of the business flow.
- **Many combined faults:** hard to debug; kept one (`payments_flaky`).
- **Every auto scenario on PRs:** too slow; nightly runs them all.
- **A separate RabbitMQ publish-failure scenario:** `auto-down-checkout-rabbitmq` covers it.

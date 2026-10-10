# Original checks/shop.py from the blind author, before EDITS.md #4 (import path only).
import uuid
from kazu import after, eventually, run

FRAUD_LIMIT = 100_000

def _orders(status):
    return {r["id"]: r["amount_cents"] for r in run.sql("checkout_db",
        "SELECT id, amount_cents FROM orders WHERE status = %s", [status])}

def _charges():
    out = {}
    for r in run.sql("payments_db", "SELECT order_id, amount_cents FROM charges"):
        out.setdefault(r["order_id"], []).append(r["amount_cents"])
    return out

@eventually(within="60s")
def no_order_stuck_pending():
    # Rule 4: reconciler runs every 5s and picks up orders pending >10s
    n = run.sql_value("checkout_db", "SELECT count(*) FROM orders WHERE status = 'pending'")
    assert n == 0, f"{n} orders still pending"

@eventually(within="60s")
def paid_orders_have_exactly_one_matching_charge():
    # Rules 2 and 3
    paid, charges = _orders("paid"), _charges()
    bad = [(o, charges.get(o)) for o, amt in paid.items() if charges.get(o) != [amt]]
    assert not bad, f"{len(bad)} paid orders without exactly one matching charge, e.g. {bad[:5]}"

@eventually(within="60s")
def failed_orders_have_no_charge():
    # Rule 3
    charges = _charges()
    bad = [o for o in _orders("failed") if o in charges]
    assert not bad, f"{len(bad)} failed orders were charged, e.g. {bad[:5]}"

@after
def no_orphan_charges():
    # A charge must belong to an existing order
    ids = {r["id"] for r in run.sql("checkout_db", "SELECT id FROM orders")}
    orphans = [o for o in _charges() if o not in ids]
    assert not orphans, f"{len(orphans)} charges for unknown orders, e.g. {orphans[:5]}"

@eventually(within="90s")
def ledger_complete_and_balanced():
    # Rule 5: every paid order has exactly 2 entries summing to 0, debit = -amount
    rows = run.sql("checkout_db", """
        SELECT o.id, o.amount_cents,
               count(l.id)                      AS n,
               coalesce(sum(l.amount_cents), 0) AS total,
               min(l.amount_cents)              AS debit
        FROM orders o LEFT JOIN ledger_entries l ON l.order_id = o.id
        WHERE o.status = 'paid'
        GROUP BY o.id, o.amount_cents
        HAVING count(l.id) <> 2 OR coalesce(sum(l.amount_cents),0) <> 0
            OR min(l.amount_cents) <> -o.amount_cents""")
    assert not rows, f"{len(rows)} paid orders with bad ledger, e.g. {rows[:5]}"

@after
def no_ledger_for_unpaid_orders():
    # Rule 5 corollary: failed/pending orders must not reach the books
    rows = run.sql("checkout_db", """
        SELECT DISTINCT l.order_id FROM ledger_entries l
        JOIN orders o ON o.id = l.order_id
        WHERE o.status IN ('failed', 'pending')""")
    assert not rows, f"ledger entries for unpaid orders: {rows[:5]}"

@eventually(within="60s")
def queue_drains():
    depth = run.metrics('max(shop_queue_depth)')
    assert depth == 0, f"queue depth {depth}"

@after
def resubmit_returns_same_order():
    # Rules 1 and 2
    key = f"kazu-{uuid.uuid4()}"
    h = {"Idempotency-Key": key}
    a = run.http("checkout").post("/orders", json={"amount_cents": 2500}, headers=h)
    b = run.http("checkout").post("/orders", json={"amount_cents": 2500}, headers=h)
    assert a.status < 500 and b.status < 500
    assert a.json()["id"] == b.json()["id"]
    n = run.sql_value("checkout_db", "SELECT count(*) FROM orders WHERE idempotency_key = %s", [key])
    assert n == 1

@eventually(within="30s")
def fraud_limit_enforced():
    # Rule 6, including the boundary (100000 is allowed, 100001 is not)
    over = run.http("checkout").post("/orders", json={"amount_cents": FRAUD_LIMIT + 1},
                                     headers={"Idempotency-Key": f"kazu-over-{uuid.uuid4()}"})
    oid = over.json()["id"]
    status = run.http("checkout").get(f"/orders/{oid}").json()["status"]
    assert status == "failed", f"over-limit order is {status}"
    assert oid not in _charges()

    at = run.http("checkout").post("/orders", json={"amount_cents": FRAUD_LIMIT},
                                   headers={"Idempotency-Key": f"kazu-at-{uuid.uuid4()}"})
    assert run.http("checkout").get(f"/orders/{at.json()['id']}").json()["status"] == "paid"

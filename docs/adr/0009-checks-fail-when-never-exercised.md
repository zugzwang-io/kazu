# ADR-0009: A check that never saw what it guards is not exercised, not a pass

Date: 2026-10-28
Status: Proposed

## Context

Raised by the 2026-10-28 Antithesis backlog review and agreed as a v1 gap. A `sql:` check passes when its query returns no rows (ADR-0002, decision 14): the query selects violations. That makes it vacuous: the shop's "no order charged twice" passes over an empty `charges` table, so a run where the payment path never executed reports pass. The same holds for an `http:` check against an endpoint nothing wrote to and for an `@after` function that iterates over zero rows and asserts nothing.

The landed check (DESIGN §6.6) doesn't close this. It proves the fault applied and that traffic reached the proxy; it says nothing about whether the path a check guards ran. A 1% `errors` fault lands while the reconciler never re-sends; a `double-click` scenario has no fault at all, so nothing is checked for landing, and its `charges` check passes with no traffic. "Traffic exited 0" proves the load tool ran, not that its requests reached the data a check reads.

The shop's current workaround is an inverted query, `select 'no paid orders' where not exists (select 1 from checkout.orders where status = 'paid')`, as a `sql:` check (SPEC.md, "Every config includes a liveness check"). It doesn't work as a gate: with no traffic the check fails on base and head alike, and the verdict rule (PLAN, Phase 3 verdict: "a violation on both is a pass") passes it. The shop's e2e asserter catches this separately by requiring nonzero user-facing edge counters; customers have no asserter.

**What Antithesis does** (verified 2026-10-28):
- Go SDK reference, https://antithesis.com/docs/generated/sdk/golang/assert/ : `Always` "asserts that condition is true every time this function is called, and that it is called at least once." `AlwaysOrUnreachable`: "The corresponding test property will pass if the assertion is never encountered (unlike Always assertion types)." `Sometimes` "asserts that condition is true at least one time that this function was called"; never encountered fails. `Reachable` fails if never called.
- Assertions overview, https://antithesis.com/docs/properties_assertions/assertions/ : Always: "Assert that a condition is reached at least once and must hold every time this assertion is evaluated"; "Always assertions are listed as passing by default until disproved by a failing encounter or if there no encounters at all." AlwaysOrUnreachable types "also evaluate 'not encountered' as passing." Sometimes: "listed as failing by default until proven by a passing encounter."
- Properties report, https://antithesis.com/docs/product/understanding_test_results/reports/properties/ : Always "properties that trivially failed due to never being reached are singled out in the second group."
- Sometimes best practices, https://antithesis.com/docs/best_practices/sometimes_assertions/ : "Code coverage only covers locations, while sometimes assertions cover situations."

The conflict the research notes flagged (overview versus SDK on an unreached `Always`) dissolves on a close read: the overview's sentence says an `Always` is disproved "if there [are] no encounters at all", and it names `AlwaysOrUnreachable` as the type that treats "not encountered" as passing. All three pages agree: **an `Always` never reached fails; only `AlwaysOrUnreachable` passes unreached.** Kazu's `sql:` is `AlwaysOrUnreachable`.

Antithesis's `Always` semantics can't simply be applied to `sql:`: a violation-selecting query returns zero rows both for "no violations among a thousand charges" and for "no charges". Reached-ness needs a second signal, and the user is the only one who knows what it is.

## Decision

**Kazu adopts Antithesis's `Sometimes` as a check kind, under the same name, in both forms: a one-line `sometimes:` check and an SDK `@sometimes` decorator. A `sometimes` that is false on base and head is *not exercised*, never a pass.**

1. **`sometimes: { on, query }`** mirrors `sql:` with the outcome inverted: the query selects evidence that the guarded situation happened, and the check passes in a trial when it returns at least one row. Kazu reads only the first few rows under the same statement timeout as `sql:`. `select order_id from payments.charges limit 1` guards every check over `charges`; `select id from checkout.orders where status = 'paid' and updated_at - created_at > interval '10 seconds' limit 1`, in a scenario that injects response loss, proves that at least one order was settled after the inline attempts gave up (by the reconciler, or by a client resend with the same key), which is the path a dedup check exists to watch.
2. **The verdict rule gains one exception.** Like any check, a `sometimes` that passes on base and fails on head is REGRESSED (head lost a behaviour: the reconciler no longer re-sends, the breaker no longer opens). A `sometimes` that fails on both sides is **not exercised**, with the reason (no rows on either side in n/n trials), not a pass. This is "fail only on facts" applied to checks: the situation the config says should occur didn't occur, and the checks that depend on it proved nothing. Evaluation is per trial, as the landed check's is. `gate.fail_on` doesn't apply to a `sometimes`: its both-sides result is already decided by this rule.
3. **Not exercised is a check-level result that rolls up.** Each check reports pass, fail or not exercised per trial, with its reason, in the terminal and in `--json`; the landed check's per-fault result sits beside them. A scenario is REGRESSED if any check regressed; otherwise not exercised if any check or fault is not exercised at `fail` severity; otherwise pass. The e2e asserter and the §4.8 layout read the check-level result.
4. **The switch** is the one that already exists for a missing signal, at three levels, **the most specific winning**: `not_exercised: warn` on the `sometimes` entry itself (new, mirroring `metric.no_data`), then the scenario's `not_exercised: warn`, then `gate.not_exercised`. A 1% fault on light traffic that rarely produces the situation is the case the scenario-level switch was made for, and the same switch covers a rarely-true `sometimes` in that scenario.
5. **The SDK gets `@sometimes`**, run when `@after` runs; the function asserts that the situation occurred. The checks protocol's `list` reply carries each check's kind so the host applies rule 2; `result {pass, message}` is unchanged. This is the shape; it is built in Phase 4 with the shop's `checks/shop.py`, since Phase 3 needs only the one-line form.
6. **`sql:` and `http:` keep their semantics** (`AlwaysOrUnreachable`: pass when never reached). This is a deliberate deviation from Antithesis's `Always` default, recorded in DESIGN §6.7 on acceptance, for the reason above: an external check can't tell unreached from satisfied, so reached-ness is a separate check the user writes. `@after` is unaffected: it is always reached (it runs once per trial), and its vacuity comes from looping over zero rows, which a `@sometimes` beside it covers.
7. **Opt-in by construction, placed where the traffic earns it.** Kazu knows nothing about the customer's domain (principle 1), so it can't generate a `sometimes`. Every template Kazu ships (the `kazu init` draft, the shop's configs) carries one `sometimes` per table its `sql` checks read, in the `checks:` of the scenarios whose traffic writes that table (ADR-0002, decision 13); a `sometimes` goes top-level only when every scenario's traffic produces it. DESIGN §4.4's `browse-under-outage` makes no orders, so a top-level `charges` `sometimes` would be not exercised there by design, and downgrading it per scenario would also downgrade that scenario's landed check. The docs for `sql:` lead with the pairing. Antithesis likewise recommends Sometimes assertions in its best practices rather than requiring them.

Considered and lost:
- **A `requires:` or `min_rows:` precondition inside `sql:`**, making the check itself not exercised when a second query returns nothing. It is specific to one kind (nothing for `http:` or `@after`), every check over the same table repeats the same precondition, and it can't express a situation that no always-check depends on ("the reconciler re-sent at least once"). Antithesis has no counterpart; `Sometimes` is independent of any `Always`.
- **`Always` semantics for `sql:` and `http:`: fail when never reached.** The query can't tell (above), and an `http:` response arrives whether or not anything wrote the data behind it. For `@after` the question doesn't arise: the function runs exactly once per trial, so it is always reached; "never evaluated" has meaning only inside the application process where Antithesis evaluates it.
- **`fail_on: violation`, per check or the existing `gate.fail_on`.** It would make a both-sides failure fail, but it reports coverage as a violation, so the result isn't *not exercised* and the `not_exercised` switches don't reach it; and the global form fails every check that is violated on both sides, which is the regression-not-violation rule the shop's S4 row guards.
- **`reachable`, as a log-pattern check.** The external analog of Antithesis's `Reachable` is a `log: { service, pattern }` check reading container logs. A separate feature with its own reader; and `sometimes` over data already covers the cases the shop needs.
- **Default-on: a built-in "every table a `sql` check reads has rows".** Kazu would parse SQL to find tables and guess which ones matter: a heuristic about the customer's domain, so at most a warning, and not a replacement for the user saying what should happen.
- **Keeping the inverted `where not exists` query.** It reports coverage as a violation, which the both-sides rule swallows, and a reader can't tell it from a real invariant.

## Consequences

**Schema and `Config`** (ADR-0002 shape): the `check` object gains `sometimes: { on, query, not_exercised }`, `required: [on, query]`, `additionalProperties: false`, `not_exercised` a `severity`; the "exactly one kind" rule and its description list it. `Config` gains `SometimesCheck CheckKind = "sometimes"` and `Sometimes struct { On, Query string; NotExercised Severity }`; validation requires `on` to name a postgres connection, as for `sql`. `Scenario.NotExercised`'s docstring widens from "a fault that never landed" to any not-exercised result in the scenario. `gate.not_exercised` needs no change when `gate` joins the schema.

**Verdict and output:** the per-scenario rule becomes "a check that passes on base and fails on head is REGRESSED; a violation on both is a pass, except a `sometimes`, which is not exercised". The `--json` result carries the per-check result and reason, and the scenario verdict rolled up as in decision 3. Terminal output follows the §4.8 layout, with the check named on the scenario line and the reason beneath:

```
  stripe-errors              ✗ NOT EXERCISED  some_charge  no rows on base or head in 5/5 trials

  ✗ stripe-errors: sometimes "some_charge" was not exercised
    no rows on base or head in 5/5 trials; check that traffic reaches payments
```

**PLAN bullets touched** (named here, edited when the implementing PRs land):
- Phase 3, "checks 1/4: `sql:` one-line check": `sometimes:` is the same host path with the outcome inverted; it lands with or right after it. Phase 3 needs nothing else from this ADR on the SDK side.
- Phase 3, "verdict: … a violation on both is a pass": the exception above, the check-level result in `--json`, and the switch precedence.
- Phase 3, "shop: step-1 `kazu.yaml` with a liveness check": the liveness check becomes a `sometimes` in the scenarios whose traffic pays orders; SPEC.md's "Every config includes a liveness check" paragraph changes with it. The e2e asserter's nonzero-counter requirement on V0 and S2 stays: it guards Kazu's wiring independently of Kazu's own checks.
- Phase 4, "shop: full `kazu.yaml` and `checks/shop.py`": `@sometimes` in the Python SDK and the kind field in the protocol's `list` land here, beside the first code checks that use them.
- Phase 5, "verdict: `gate.not_exercised` … honoured end to end": covers the per-check switch.

**DESIGN.md on acceptance:** §4.4 reference gains a `sometimes` entry and §4.6's `checks:` list and decorator list gain it; §6.7 gains the row "`sql:` and `http:` pass when never reached (`AlwaysOrUnreachable`), with `sometimes` carrying reached-ness, instead of `Always` failing unreached".

**What becomes harder:** a config that wants a non-vacuous invariant writes two queries where it wrote one, and a scenario whose traffic never produces the situation reports not exercised where it used to pass. Both are the point.

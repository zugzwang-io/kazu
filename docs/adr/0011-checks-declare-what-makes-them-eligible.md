# ADR-0011: Every check declares what makes it eligible, apart from what makes it pass

Date: 2026-11-09
Status: Accepted (2026-11-10)

## Context

ADR-0009 (accepted 2026-10-29) answered a real gap: a check can pass because nothing happened. A `sql:` check selects violations and passes on no rows, so "no order charged twice" passes when no charges were made at all. ADR-0009's answer was a separate check kind, `sometimes`, whose failure on both sides made the scenario *not exercised*.

Reviewing the DESIGN fold-in (#57), the maintainer found the shape indirect: which `sometimes` vouches for which check is left for the reader to work out. Every check really has two parts:

1. **Eligibility:** did enough happen for this check's verdict to mean anything?
2. **Decision:** given that it did, did the check pass?

Explicit is better than implicit, so the user states eligibility on the check it applies to.

## Decision

**Every check entry may carry `requires:`, a list of conditions that all must hold for the check to be eligible. A check that is never eligible on either side is *not exercised*, never a pass. Without `requires:`, nothing changes. ADR-0009's `sometimes` kind and `@sometimes` decorator are dropped.**

1. **Conditions.** `requires:` is a list, and every entry must hold (AND). Each entry is exactly one of:
   - **`sql: { on, query }`** holds in a trial when the query returns at least one row. Kazu stops reading after the first row, under the same statement timeout as a `sql:` check. `on` names a postgres connection, as for a `sql:` check.
   - **`requests: { edge | service, min }`** holds in a trial when the proxy received at least `min` requests between traffic start and traffic exit. A request is one HTTP request or one gRPC call (a stream counts once). It uses the same counter as `after_requests`, and every request the proxy received counts, whether it was forwarded or answered by a fault. A connection refused by a `down` fault carries no request, so it doesn't count.
     - `edge: a -> b` counts that one declared edge, which must be `http` or `grpc`. A `tcp` proxy sees bytes, not requests.
     - `service: b` sums `b`'s declared `http` and `grpc` inbound edges and ignores its `tcp` ones. A service with no `http`/`grpc` inbound edge is rejected when the config is loaded, the same rule as `after_requests`.
     - `min` is an integer of at least 1. Counts are per trial and are never summed across trials.

   ```yaml
   scenarios:
     double-click:
       env: { traffic: { DOUBLE_SUBMIT: "1" } }
       checks:
         - name: no_double_charge
           sql: { on: postgres, query: "select order_id from charges group by order_id having count(*) > 1" }
           requires:
             - sql: { on: postgres, query: "select 1 from charges limit 1" }
             - requests: { edge: checkout -> payments, min: 50 }
         - name: payments_p99
           no_regression: { service: payments, measure: p99, within: 10% }
           requires:
             - requests: { service: payments, min: 200 }
   ```

2. **When conditions are evaluated.** Once per trial, after every check in the trial has finished, including `@eventually` retries. So a `sql` condition sees the trial's final state, which may be up to a minute after the check itself decided. That is intended: a condition about a transient state, such as a pending order, may no longer hold by then, so write conditions about things that persist. A `requests` condition reads the counts snapshotted at traffic exit. On an `image:` entry, `requires:` applies to every code check in that image and is evaluated once for the entry. The check's own decision still runs in an ineligible trial and is reported for information, but none of that check's results from that trial count: not its decision, its no data, or its cut-short window.

3. **An error is not ineligibility.** If a condition can't be evaluated (a syntax error, a statement timeout, a refused connection), that is a check error, handled like an error in the check itself. It is never treated as "no rows". Otherwise a broken query would quietly become *not exercised*, or a pass under `warn`.

4. **No defaults.** A check without `requires:` is always eligible. The existing missing-signal results keep their rules: a fault that never landed (*not exercised*), a `recovers:` window cut short by the end of traffic (*not exercised*, ADR-0006), **no data** on a `metric:` (`gate.no_data`), and **underpowered** (`gate.underpowered`). This ADR adds one more source of *not exercised*. Inside an eligible trial those results apply as before; an ineligible trial contributes none of them (rule 2).

5. **Verdict.** Base and head trials are paired, as for every check.
   - **Never eligible in any trial, on either side:** *not exercised*, naming the condition that was never met. This is decided per scenario.
   - **Eligible in fewer head trials than base trials, including none:** a candidate regression (head stopped doing something the check depends on). It is judged by the base-vs-head comparison on the eligible-trial counts, so `flaky` and `underpowered` apply. No fixed rule decides it.
   - **The decision** is compared over the pairs in which both sides were eligible. Too few such pairs to decide is **underpowered**.
   - The eligibility comparison and the decision comparison are independent, and either one can regress.
   - **Eligible on head but never on base** (head started doing it) is not a regression on eligibility. Its decision has no pairs, so it is underpowered. A head that starts doing something and gets it wrong is therefore reported as underpowered, not REGRESSED. That gap is accepted, because there is nothing on base to regress from.
   - **One pair (Phase 3, before the statistical verdict).** Each scenario runs one base and one head trial:
     - eligible on base but not on head is REGRESSED;
     - eligible on head but not on base passes;
     - eligible on neither is *not exercised*;
     - eligible on both compares the decision as for any check.
     
     The statistical rules above replace this once the paired comparison lands.
   - **`gate.fail_on: violation`:** the decision is judged in every head trial where the check is eligible, whatever base did. Eligibility is still compared as above. Losing eligibility is a regression, not a violation, and never being eligible is still *not exercised*.
   - **Adaptive trials:** a check not yet eligible on either side leaves its scenario undecided, so pairs are added while the budget lasts. It is *not exercised* only once the budget is spent.
   - **The landed check and eligibility are independent.** A trial whose fault didn't land still counts toward a check's eligibility. The scenario is *not exercised* for the fault either way.

6. **Placement.** A top-level check runs in every scenario, so a top-level check with a `requires:` that some scenario's traffic can't meet is *not exercised* there by design. Put such checks in the `checks:` of the scenarios whose traffic meets them, and at the top level only when every scenario's traffic does. Downgrading a whole scenario to `warn` instead would also downgrade its landed check.

7. **Switches.**
   - `not_exercised: warn` exists at three levels, and the most specific wins: on the check entry (new for every check kind), then the scenario, then `gate.not_exercised`.
   - The check-level switch covers every *not exercised* result of that check, including a `recovers:` window cut short. A candidate regression on eligibility counts is a regression, so `gate.fail_on` and `flaky` govern it like any other.
   - Check results roll up to the scenario, as ADR-0009 set out: REGRESSED if any check regressed; otherwise *not exercised* if any check or fault was not exercised at `fail` severity; otherwise pass. Underpowered, flaky and no-data results are reported per check beside the roll-up, under their own switches.

**What Antithesis does.** Its `Always` assertion fails if it is never reached, and its `Sometimes` assertion fails until it has held at least once. Both put "did this happen" inside the application, where Antithesis can see it. Kazu checks from outside, so it can't see that on its own. This ADR deviates from Antithesis in two ways, both recorded in DESIGN §6.7 on acceptance:
- A check without `requires:` stays eligible when never reached, unlike Antithesis's `Always`.
- Reachability is a precondition attached to the check it guards, not a standalone `Sometimes` assertion. Antithesis has no counterpart to `requires:`.

**Accepted loss.** A situation that no check depends on, such as "the reconciler re-sent at least once", can't be asserted on its own. ADR-0009's `sometimes` could do that. Here it has to hang on a check that depends on it, which also discards that check's decision in trials where the situation didn't occur. A check made only of `requires:` could restore this later if a case needs it.

Considered and lost:
- **`sometimes` as a check kind (ADR-0009):** which check it vouches for is implicit.
- **`sometimes: true` or `expect: rows` as a flag on `sql:`:** the same one bit, on a separate query instead of on the check it guards.
- **A list of ANDed decision queries per `sql:` check:** several checks give the same AND, and report each failure under its own name.
- **A bare `requests: N` that takes the check's own target:** ambiguous for a `service:` check (sum or per edge?), and undefined for `sql:`.
- **Implicit defaults (at least 1 request on each edge a proxy check reads; no data as ineligibility):** they would make existing top-level checks *not exercised* in scenarios that don't reach their service. They would also give one condition two results and two switches. Existing rules stay as they are.

Superseded from ADR-0009: the `sometimes` kind, `@sometimes`, the `list` reply's kind field, and "false on base, true on head passes silently". Its counterpart here is rule 5: not a regression on eligibility, with an underpowered decision. Carried over: the roll-up, the switch precedence, adaptive trials adding pairs, and no `kazu doctor` warning for unpaired checks. Dropped: the deferred metric form of `sometimes`. There is no `metric` condition until a case needs one.

## Consequences

**Schema and `Config`** (ADR-0002 shape):
- The `check` object gains:
  - `requires`: an array of `requirement` objects, with `minItems: 1`. Each has exactly one of `sql` (`{ on, query }`, both required) or `requests` (exactly one of `edge` or `service`, plus `min`, an integer ≥ 1). Both objects have `additionalProperties: false`.
  - `not_exercised`: a `severity`.
- `Config` gains `Requires []Requirement` and `NotExercised Severity` on `Check`.
- Validation adds:
  - a postgres connection for `sql`;
  - a declared `http`/`grpc` edge for `requests.edge`;
  - at least one `http`/`grpc` inbound edge for `requests.service`.
- `metric.no_data` is unchanged.
- `Scenario.NotExercised`'s docstring widens from "a fault that never landed" to any not-exercised result in the scenario.
- `config/testdata/reference.yaml` gains the example above in the same PR.

**ADR-0009** becomes "Superseded by ADR-0011" when this is accepted. Nothing of it was built.

**The shop.** SPEC.md's "Every config includes a liveness check" changes with the Phase 3 shop `kazu.yaml` PR. The inverted `where not exists` query becomes a `requires:` on the checks it was meant to vouch for, in the scenarios whose traffic pays orders. The e2e asserter's nonzero-counter requirement stays.

**PLAN bullets touched** (edited when the implementing PRs land):
- **Phase 3:**
  - *"checks 1/4: `sql:` one-line check"*: adds `requires` and `not_exercised` to the schema and `Config`, and the planner entry rejecting `requires: requests` (ADR-0007). `requires: sql` runs on the same host path.
  - *"verdict: … a violation on both is a pass"*: the one-pair rules (rule 5), the per-check switch and the `--json` per-check result.
  - *"shop: step-1 `kazu.yaml` with a liveness check"*: as above.
- **Phase 4:**
  - *"proxy: HTTP mode — … request counter per edge"*: the counter that `requires: requests` reads, snapshotted at traffic start and exit. Until it lands, the planner rejects `requires: requests` (ADR-0007).
  - *"checks: `@eventually(within=…)`"*: conditions are evaluated after the retries end.
  - *"shop: full `kazu.yaml` and `checks/shop.py`"*: `requires:` on an `image:` entry.
- **Phase 5:**
  - *"verdict: paired comparison … underpowered"*: the eligibility comparison and the decision comparison over pairs where both sides were eligible.
  - *"verdict: adaptive trials"*: pairs are added while a check is not yet eligible.
  - *"verdict: flaky classification; … `gate.not_exercised` …"*: the switches.

**DESIGN.md on acceptance:**
- §2 principle 5: an unmet `requires:` joins the list of missing signals.
- §4.4 reference: the example above, and the `gate.not_exercised` comment widened.
- §4.6: a `requires:` paragraph and the placement rule.
- §4.8: a sample *not exercised* line naming the unmet condition.
- §6.6: a pointer saying *not exercised* also comes from `requires:`.
- §6.7: the two deviation rows.

**What becomes harder:**
- A config that wants a non-vacuous `sql:` check writes two queries where it wrote one.
- Checks guarded by the same condition repeat it. Shared, named conditions can come later if that gets tedious.

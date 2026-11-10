# ADR-0011: Every check declares what makes it eligible, apart from what makes it pass

Date: 2026-11-09
Status: Proposed

## Context

ADR-0009 (accepted 2026-10-29) answered a real gap: a check can pass because nothing happened. A `sql:` check selects violations and passes on no rows, so "no order charged twice" passes when no charges were made at all. ADR-0009's answer was a separate check kind, `sometimes`, whose failure on both sides made the scenario *not exercised*.

Reviewing the DESIGN fold-in (#57), the maintainer found the shape indirect: which `sometimes` vouches for which check is left for the reader to work out, and `sql` was being singled out when the question applies to every check. Every check really has two parts:

1. **Eligibility:** did enough happen for this check's verdict to mean anything?
2. **Decision:** given that it did, did the check pass?

Kazu already answers part 1 implicitly for the checks it measures itself. A proxy-measured check over an edge with no traffic has nothing to compare, and a `metric:` check whose query returns no samples is **no data** (DESIGN §6.8). It can't answer part 1 for `sql:`, `http:` or code checks: zero rows means "nothing bad" or "nothing at all", and only the user knows which. Explicit is better than implicit, so the user says it, on the check it applies to.

## Decision

**Every check entry, top-level or in a scenario's `checks:`, may carry `requires:`, a list of conditions that all must hold for the check to be eligible. An ineligible check has no verdict: it is *not exercised*, never a pass. ADR-0009's `sometimes` kind and `@sometimes` decorator are dropped; its other decisions carry over (below).**

1. **`requires:` is a list; every entry must hold (AND).** Each entry is exactly one of:
   - **`sql: { on, query }`**: holds in a trial when the query returns at least one row. `on` names a postgres connection, as for a `sql:` check. It runs when the check runs (after traffic exits), reading only the first row under the same statement timeout.
   - **`requests: { edge | service, min }`**: holds in a trial when the proxies counted at least `min` requests between traffic start and traffic exit. `edge: a -> b` counts that one declared edge; `service: b` sums every declared inbound edge of `b`. The edges must be `http` or `grpc`, because a `tcp` proxy sees bytes, not requests; an `edge` on a `tcp` edge, or a `service` with no `http`/`grpc` inbound edge, is rejected when the config is loaded. This is the same rule as `after_requests`. `min` is an integer of at least 1.

   ```yaml
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

2. **Defaults.** With no `requires:`, eligibility is what Kazu can observe on its own, stated here so an unwritten `requires:` still has one meaning:
   - `recovers:`, `no_regression:`, `slo:` and the regression defaults: at least 1 request on each declared `http`/`grpc` edge the check reads, per trial.
   - `metric:`: at least 1 sample from its own query, which is the existing **no data** rule.
   - `sql:`, `http:` and `image:` checks: always eligible, so they keep today's behaviour until the user adds a condition.

   A written `requires:` adds to the default; it doesn't replace it.

3. **Eligibility is decided per trial.** A check's decision counts only in the trials where it was eligible. Per side, base or head:
   - **Ineligible in every trial on both sides:** the check is *not exercised*, with the unmet condition named. This is a fact, not a guess.
   - **Eligible in some base trials and in fewer head trials:** a candidate regression (head stopped doing something the check depends on). It is judged by the base-vs-head comparison on the count of eligible trials, so `flaky` and `underpowered` apply. No fixed rule decides it.
   - **Otherwise:** the decision is compared base vs head over the eligible trials, as for any check. Too few eligible pairs to decide is **underpowered**.
   - **Adaptive trials:** a check not yet eligible on either side leaves its scenario undecided, so pairs are added while the budget lasts; it is *not exercised* only once the budget is spent.

4. **`requires:` on an `image:` entry applies to every code check in that image.** Per-function conditions inside the SDK wait until a case needs them.

5. **Carried over from ADR-0009:**
   - *Not exercised* is a check-level result that rolls up. A scenario is REGRESSED if any check regressed; otherwise not exercised if any check or fault was not exercised at `fail` severity; otherwise pass.
   - The switch is `not_exercised: warn` at three levels, **the most specific winning**: on the check entry, then the scenario, then `gate.not_exercised`.
   - `gate.fail_on` doesn't apply to an ineligible check, since it has no decision to fail on.

**What Antithesis does.** Its `Always` assertion fails if it is never reached, and its `Sometimes` assertion fails until it has held at least once. Both put "did this happen" inside the application, where Antithesis can see it. Kazu checks from outside, so it can't see reachability on its own. `requires:` is how the user supplies it, attached to the check it guards. This deviates from Antithesis's `Always` default, because a `sql:` or `http:` check without `requires:` stays eligible when never reached. It is recorded in DESIGN §6.7 on acceptance.

Considered and lost:
- **`sometimes` as a check kind (ADR-0009).** It works, but which check it vouches for is implicit, and it only covered checks the user thought to pair it with.
- **`sometimes: true` or `expect: rows` as a flag on `sql:`.** It's the same one-bit idea as `sometimes`. The bit sits on a separate query instead of on the check it guards, and it says nothing about request floors for proxy-measured checks.
- **A list of ANDed decision queries per `sql:` check.** Several decision queries are better written as several checks: the AND is the same, since a scenario passes only if every check does, and each failure is reported under its own name.
- **A bare `requests: N` taking the check's own target.** It is ambiguous for a `service:` check (is it the sum, or per edge?) and undefined for `sql:`, which has no target.
- **A `samples:` condition for `metric:` checks.** The default (at least 1 sample, the no-data rule) covers it until a case needs a higher floor.

## Consequences

**Schema and `Config`** (ADR-0002 shape):
- The `check` object gains `requires`: an array of `requirement` objects, each with exactly one of `sql` (`{ on, query }`, both required, `additionalProperties: false`) or `requests` (`{ edge | service, min }`, exactly one of `edge` or `service`, `min` an integer ≥ 1).
- `Config` gains `Requires []Requirement` on `Check`.
- Validation adds:
  - a postgres connection for `sql`;
  - a declared `http`/`grpc` edge for `requests.edge`;
  - at least one such inbound edge for `requests.service`.
- `Scenario.NotExercised`'s docstring widens from "a fault that never landed" to any not-exercised result in the scenario.
- `config/testdata/reference.yaml` gains the two examples above in the same PR.

**ADR-0009** becomes "Superseded by ADR-0011" when this is accepted. Its `sometimes` kind, `@sometimes` decorator and the `list` reply's kind field are never built.

**The shop.** SPEC.md's "Every config includes a liveness check" changes with the Phase 3 shop `kazu.yaml` PR. The inverted `where not exists` query becomes a `requires:` on the checks it was meant to vouch for. The e2e asserter's nonzero-counter requirement stays.

**PLAN bullets touched** (edited when the implementing PRs land):
- **Phase 3:**
  - *"checks 1/4: `sql:` one-line check"*: `requires: sql` runs on the same host path.
  - *"verdict: … a violation on both is a pass"*: eligibility per trial, *not exercised*, and the switch precedence.
  - *"shop: step-1 `kazu.yaml` with a liveness check"*: as above.
- **Phase 4:** *"proxy: HTTP mode — … request counter per edge"*: the counter `requires: requests` reads. Until it lands, the planner rejects `requires: requests` (ADR-0007).
- **Phase 5:**
  - the paired-comparison bullet and the flaky-classification bullet: comparison over eligible trials, with eligibility counts as a candidate regression;
  - the `gate.not_exercised` bullet.

**DESIGN.md on acceptance:**
- §4.4 reference: the examples above.
- §4.6: a `requires:` paragraph with the defaults.
- §4.8: a sample *not exercised* line naming the unmet condition.
- §6.7: the deviation row.
- §2 principle 5: an unmet `requires:` joins the list of missing signals.

**What becomes harder:**
- A config that wants a non-vacuous `sql:` check writes two queries where it wrote one.
- Checks guarded by the same condition repeat it. Shared, named conditions can come later if that gets tedious.

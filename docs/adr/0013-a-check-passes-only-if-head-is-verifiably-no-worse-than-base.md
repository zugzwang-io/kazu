# ADR-0013: A check passes only if head is verifiably no worse than base; a scenario passes only if it ran and every check passed

Date: 2026-11-27
Status: Proposed

Amends ADR-0011 (rule 5's head-only bullet and rule 7's roll-up sentence; see Consequences).

## Context

Kazu has results that mean "there was no signal": *not exercised* (a fault never landed, or a check's `requires:` was never met), *no data* (a metric returned no samples) and *underpowered* (a comparison too noisy to decide within the trial budget). The repo says two different things about the last one.

**CLAUDE.md** (product principle) puts all three together:

> **Fail only on facts** (product principle, DESIGN §2): a missing signal — not exercised, no data, underpowered — is never a pass, and every failure has a config switch.

**DESIGN.md** fails on the first two and only warns on the third:

- §2 principle 5: "A missing signal (a fault that never landed, a metric with no data, a check whose `requires:` was never met) is never a pass, and a comparison too noisy to decide is reported as such, never guessed." Underpowered is left out of the list of missing signals.
- §4.4, the `gate:` block: `underpowered: warn                  # a comparison too noisy to decide is reported, not guessed`, beside `not_exercised: fail` and `no_data: fail`.
- §4.6, `no_regression`: "Underpowered is a warning by default (`gate.underpowered`)."
- §4.6, Switches: "Check results roll up to the scenario: REGRESSED if any check regressed; otherwise not exercised if any check or fault was not exercised at `fail` severity; otherwise pass. Underpowered, flaky and no-data results are reported per check beside the roll-up, under their own switches."
- §4.8 shows it as a warning: `⚠ db-blip: latency within 1% is underpowered`.
- §11: "`no_regression`: user-chosen tolerance; noise estimated in every run, per scenario and per measurement, from the trial pairs; too noisy to decide is "underpowered", a warning".

So under DESIGN a scenario whose comparison could not be decided is green, with a warning beside it, and a regression smaller than this run's noise merges under a green check.

The question came up in the review of ADR-0012 (#64), which leans on "fail only on facts" and on the `⚠` of underpowered. The maintainer settled it in conversation on 2026-11-27, in CLAUDE.md's favour. The first draft of this ADR (#66) defined a pass for the scenario and worked through the outcomes case by case. Its review asked for changes, and the maintainer replaced that definition with the one rule below.

## Decision

> **A check passes if Kazu can verify that head is no worse than base; otherwise it fails** (by default; overridable).
>
> **A scenario passes if and only if it provably ran and every check passes.**

### 1. The three proofs

"No worse" can be shown in exactly three ways:

1. **Head passed the check**, whatever base did.
2. **Base failed the check**, whatever head did.
3. **Both sides were measured, and the comparison says head is within tolerance.** This is how the relative checks work: `no_regression`, a `metric` with `within:`, and the regression defaults.

If none of the three can be shown, the check fails. The rule doesn't branch on the kind of check; a kind differs only in which proofs it can offer.

### 2. Provably ran

A scenario provably ran when every configured fault landed and its trial pairs are complete: each pair has a base trial and a head trial that both ran to the end.

### 3. `fail_on: violation`

`gate.fail_on: violation` is the one alternative mode: head must pass every check outright. It drops proof (2). A relative check has no outright pass, so it stays on proof (3).

### 4. The table, as an illustration

The table is derived from the rule, for a pass/fail check under `fail_on: regression`. It is not the rule. *Unverifiable* means Kazu has no decision for that side: the check was never eligible there, its metric had no data, or the check errored.

| base ↓ / head → | pass | fail | unverifiable |
|---|---|---|---|
| **pass** | pass (1) | fail | fail |
| **fail** | pass (1, 2) | pass (2) | pass (2) |
| **unverifiable** | pass (1) | fail | fail |

A side is eligible when the check was eligible in at least one of its trials (ADR-0011).

### 5. A relative check with no baseline fails

A relative check has no pass or fail of its own on either side, so it can't use (1) or (2). With no measurement on base it has no (3) either, and it fails:

```
  ✗ new-export: payments_p99 within 10% has no baseline on base
    this path is new in head; use an absolute check (slo:, sql:, http:) until it's on main
```

### 6. With several trials

With several trial pairs a side's result can be mixed. Proof (1) needs head to pass in every trial where the check was eligible, and proof (2) needs base to fail in every one. A pass/fail check that is mixed on either side goes to proof (3), on the failure counts: head's failure rate is no worse than base's. That is where *flaky* sits (see the sub-question).

### 7. Underpowered fails by default

Underpowered is "(3) couldn't be shown", so the check fails.

- `gate.underpowered` defaults to `fail`. `gate.not_exercised: fail` and `gate.no_data: fail` stay.
- A scenario may carry `underpowered: warn`, beside the existing scenario `not_exercised: warn`. The most specific setting wins. Both keys take `warn` or `fail`.
- The no-baseline failure of point 5 is an underpowered result with its own message, so the same switch governs it.
- There is no per-check switch. The maintainer asked for a per-scenario override and nothing needs a finer one yet; one can be added later without breaking a config.
- Both keys join the schema in Phase 5, with `gate` and the statistical verdict.

```yaml
scenarios:
  db-blip:
    underpowered: warn        # this scenario's latency is too noisy on our runners to gate on
    faults:
      - { service: postgres, fault: pause, after: 20s, for: 10s }

gate:
  underpowered: fail          # the default
```

### 8. The other results under the rule

The rule decides pass or fail. The existing result names say why a check failed, and each keeps its switch.

- **Not exercised** (never eligible on either side): unverifiable on both, so it fails.
- **No data:** the side without samples is unverifiable. A relative `metric` check then has no (3) and fails.
- **Check error:** the side that errored is unverifiable. An error on head fails unless base failed the check outright (2). This ADR adds no switch for it; a check that errors is fixed in the check.
- **Incompatible with base** (the check errors on base, DESIGN §4.2): base is unverifiable. A pass/fail check that head passed passes by (1), and the result still says *incompatible with base*. Anything else fails. A relative check has no baseline (point 5) and fails. Today DESIGN reports it "not as a verdict"; under the rule it has one.
- **REGRESSED** is base pass with head fail, or a decided comparison that found head worse. Underpowered is never reported as REGRESSED: Kazu doesn't guess which way an undecided comparison would have gone.

### 9. The roll-up

A scenario fails if any result is at `fail` severity: REGRESSED, not exercised, no data, underpowered, a check error, or flaky under `gate.flaky: fail`. Every such result is listed. A result downgraded to `warn` is printed as a warning and doesn't fail the scenario. So no third outcome leaves the scenario green by default.

This fixes an inconsistency DESIGN already has, apart from underpowered: §6.8 says no data "fails by default", while §4.6's roll-up sentence reaches "otherwise pass" without it.

### 10. The underpowered message

It keeps saying what would resolve it (a wider tolerance, more trials, a quieter runner), and now names the switch:

```
  ✗ db-blip: latency within 1% is underpowered
    this run's noise on checkout p99 is ±14% after 12 trial pairs; widen the tolerance, allow more trials or use a quieter runner
    to stop this blocking: underpowered: warn on the scenario
```

Under `trials: auto` Kazu still adds pairs while a comparison is undecided, and calls it underpowered only once the budget is spent (DESIGN §6.3).

### Alternatives considered

- **Keep `warn` as the default (DESIGN as written).** Fewer red runs on noisy runners. It lost because a green check with a warning beside it is read as a pass: the comparison the user asked for was never made, and the PR merges anyway. A user who accepts that for one scenario can say so in the config, where a reviewer sees it.
- **Fail, with only the run-wide switch.** It lost because one noisy scenario would force `gate.underpowered: warn` on every scenario.
- **A per-check switch as well.** Not wanted yet (point 7).
- **A table of cases as the rule** (this ADR's first draft). It lost because each case needs its own argument; the three proofs give every cell.

### Antithesis alignment

Checked against `antithesis.com/docs/properties_assertions/assertions/` and `/properties_assertions/properties/` on 2026-11-27.

- **Antithesis fails on zero evidence.** "Always assertions are listed as passing by default until disproved by a failing encounter or if there no encounters at all." "Sometimes assertions are listed as failing by default until proven by a passing encounter." Of a reachability assertion: "If there are no encounters during the test run, the assertion fails." So an Always that was never reached fails, as DESIGN §6.7 and ADR-0011 already record. This ADR's rule agrees: no evidence is not a pass.
- **It has no counterpart to underpowered.** Antithesis doesn't compare measurements between two builds, and it runs deterministically (bugs are "perfectly reproducible"), so there is no per-run noise to resolve. Kazu's paired comparison is already a recorded deviation (DESIGN §6.7, the "Regression defaults" row), and underpowered is a result of it.
- **Proof (2) is Kazu's own.** An Antithesis property that fails is failing, whatever an earlier run did. Passing because base failed too belongs to the base-versus-head deviation already in §6.7 (the "User-declared scenarios compared base vs head" row). It is not a new one.

**No new deviation.**

### Open sub-question (the maintainer settles it)

**1. Does the default failure apply to `flaky`?**

`gate.flaky: warn` today. A flaky check fails in some trials on both sides, and the comparison finds head no worse.

Under the rule, flaky is proof (3) for a pass/fail check (point 6): the result is mixed, so (1) and (2) are out, and the comparison of failure counts was decided in head's favour. The check passed; "flaky" is a note on a passing check.

Recommendation: **`gate.flaky` stays `warn`, and a check is called flaky only when that comparison was decided.**

- **Flaky is not a missing signal.** Not exercised, no data and underpowered mean Kazu couldn't judge. Flaky means it judged, and also noticed something about base.
- **It matches proof (2).** A check that fails on both sides in every trial passes. Failing the intermittent form of the same thing would be inconsistent.
- **Failing it blocks PRs for flakiness that was already on main** (DESIGN §9 risk 2), which the PR's author can't fix in that PR.
- **An undecided comparison is underpowered, not flaky**, and fails. With 2 of 5 failing on base and 4 of 5 on head, say, "flaky" can't be the label.
- **"Decided" needs a margin.** "Head's failure rate is no worse than base's" is a non-inferiority test, and it needs a margin on failure rates, as `within:` is the margin for a measurement. Without one, no number of trials shows two equal rates to be equal. The margin is an input to the verdict-method spike. SPEC row S5 (`RACE_P=0.1` on both sides, expected "`flaky`, not regressed") depends on it, so S5 joins the spike's inputs.
- **The strict option exists:** `gate.flaky: fail`, and under `fail_on: violation` any failing head trial fails.

The other reading: a check that fails in some head trials has not plainly passed, so `gate.flaky` becomes `fail`, with a scenario override like `underpowered`.

## Consequences

**Amends ADR-0011.** Superseded in part: rule 5's bullet "Eligible on head but never on base", with its accepted gap, and rule 7's roll-up sentence.

- **Eligible on head but never on base is judged by the rule.** A pass/fail check that head passed passes by (1), so a correct new check is no longer underpowered. One that head failed fails, and ADR-0011's accepted gap (a wrong head reported as underpowered) closes. A relative check there has no baseline and fails (point 5).
- **Adaptive trials** stop adding pairs for such a check once base has stayed ineligible past the minimum number of pairs. The rule already decides it, and more pairs wouldn't change the answer.
- **The roll-up** is point 9.
- The rest of rule 5 stands: never eligible on either side is not exercised, and losing eligibility on head is judged on the eligible-trial counts.

**Folded in on acceptance:**
- **DESIGN §2 principle 5:** the rule and the three proofs; underpowered joins the missing signals that are never a pass.
- **DESIGN §4.2:** *incompatible with base* gets its outcome (point 8).
- **DESIGN §4.4:** `gate.underpowered: fail`, and a scenario in the reference carries `underpowered: warn`.
- **DESIGN §4.6:** "Underpowered is a warning by default" becomes "fails by default"; the Verdict list's head-only bullet (point 5 and the amendment above); the Switches paragraph gains the two levels for `underpowered` and the roll-up of point 9.
- **DESIGN §4.8:** the underpowered line becomes a `✗` failure line, not `⚠`, with the switch named; the no-baseline message.
- **DESIGN §6.3:** the underpowered bullet says it fails by default. The flaky bullet says flaky needs a decided comparison, if the sub-question is settled as recommended.
- **DESIGN §8:** the spike's false-failure target counts underpowered results on unchanged baselines, not only a false REGRESSED; the failure-rate margin and S5 join the spike's inputs.
- **DESIGN §9 risk 2:** "the underpowered result" stops being a mitigation for flaky verdicts. The mitigations become adaptive trials, the scenario override and the message that names the fix.
- **DESIGN §11:** the "`no_regression`: … too noisy to decide is "underpowered", a warning" row gains an italic note, "*Superseded in part by ADR-0013: underpowered fails by default, with a scenario override*".
- **DESIGN §6.7:** nothing.
- **ADR-0011:** a note at the top that ADR-0013 amends rules 5 and 7.
- **SPEC.md row S8:** "**underpowered**, not pass or fail" becomes a failing scenario whose result is underpowered, never `pass` and never `REGRESSED`.
- **SPEC.md row S7:** an underpowered `no_regression` on an unchanged baseline counts as a miss against the row's target share, and the share is set with that in mind.
- **SPEC.md row S9:** *incompatible with base* is checked against point 8.
- **PLAN.md Phase 5:** the spike bullet (the budget now sets a failure rate; the failure-rate margin; S5), the "paired comparison … **underpowered**" bullet, the adaptive-trials bullet (the stop for a base that stays ineligible), the "flaky classification; `gate.*` honoured end to end" bullet (the scenario `underpowered` switch and the roll-up), and the shop-rows bullets for S4–S6 and S7–S9.
- **Schema (Phase 5):** `underpowered` on a scenario and on `gate`, each `warn | fail`.

**What becomes easier:**
- A green check means one thing: for every check, Kazu showed that head is no worse than base.
- One rule gives every case. A new result kind needs only to say which side it makes unverifiable.
- A correct new check passes on the PR that adds it.
- CLAUDE.md and DESIGN say the same thing.
- A team's accepted blind spots are in `kazu.yaml`, one line per scenario, where review sees them.

**What becomes harder, and the trade-offs:**
- **The gate fails on noise.** DESIGN §9 risk 2 says "if the gate cries wolf, it gets disabled", and this decision raises that risk: a PR that changed nothing about latency can fail because the runner was noisy. Three things hold it down: adaptive trials add pairs before giving up, the message names the fix, and the scenario override turns it off where a team accepts it. None of them removes it.
- **The verdict-method spike matters more** (DESIGN §8, §10). The trial budget and the minimum number of pairs used to decide how often a warning appeared; now they decide how often a PR is blocked. The spike has to report the underpowered rate on unchanged baselines, per measure.
- **The default tolerances matter more.** The regression defaults run with no config, so their tolerances decide whether a first run on a noisy runner is red. The 2026-10-16 noise spike saw p99 differ by more than 20% in 1 of 10 identical pairs, and the recovery window by 109–507%. A default that is underpowered on most runners can't ship switched on.
- **A small fixed `gate.trials` fails often.** With two or three pairs most measurement comparisons can't be decided.
- **The scenario switch is coarse.** `underpowered: warn` downgrades every underpowered result in the scenario, not only the noisy comparison it was written for. The finer remedy is more trials for that one scenario. How trials are configured per scenario (for example `max_trials`) is an open decision in PLAN.md, taken with the spike.
- **A relative check on a new path fails until the path is on main.** The user writes an absolute check for that PR, or sets the scenario to `warn`.
- **Proof (2) passes an unverifiable head when base failed.** A check that fails on base and errors on head passes. That follows from "base failed, whatever head did"; `fail_on: violation` is the mode for a team that doesn't want it.
- **An override can outlive its reason.** A scenario set to `warn` stays quiet after the runner gets quieter. The warning is still printed on every run.

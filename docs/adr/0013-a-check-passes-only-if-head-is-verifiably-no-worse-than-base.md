# ADR-0013: A check passes only if head is verifiably no worse than base; a scenario passes only if it ran and every check passed

Date: 2026-11-27
Status: Proposed

Amends ADR-0011 (parts of rules 5 and 7; see Consequences).

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
> **A scenario passes if and only if it provably ran and every check passes** (by default; a result can be downgraded to a warning).

**An unknown is always resolved against head.** Head is what is under test, so wherever Kazu lacks a fact, it takes the reading that is worse for head. Every case below follows from the rule and this principle.

The rule applies from Phase 3's one-pair verdict onward. With one pair, each side has one trial.

### 1. The three proofs

"No worse" can be shown in exactly three ways:

1. **Head passed the check**, whatever base did.
2. **Base failed the check**, whatever head did.
3. **Both sides were measured, and the comparison says head is within tolerance.** This is how the relative checks work: `no_regression`, a `metric` with `within:`, and the regression defaults.

If none of the three can be shown, the check fails. The rule doesn't branch on the kind of check; a kind differs only in which proofs it can offer. A pass/fail check offers (1) and (2) for its decision. A relative check offers only (3).

### 2. Eligibility and decision both have to hold

ADR-0011 gave every check two parts: eligibility (did enough happen) and decision (did it pass). **A check passes only if both are shown no worse.**

- **Eligibility** is a proof-(3) comparison on the eligible-trial counts, as ADR-0011 set out: head was eligible in no fewer trials than base, within what the comparison can decide.
- **The decision** is judged by the three proofs, over the trials where the check was eligible.

Two cases this settles:
- Base eligible and passing in 5 of 5 trials, head eligible and passing in 1 of 5: the check fails. Eligibility regressed.
- Base failing in every trial, head never eligible: the check fails. Eligibility regressed, and proof (2) doesn't rescue a head that lost the signal.

Never eligible in any trial on either side is *not exercised*, as before.

### 3. Provably ran

A scenario provably ran when every configured fault landed and its trial pairs are complete: each pair has a base trial and a head trial that Kazu ran to the end.

A trial is **incomplete** when Kazu itself failed to run it: the driver errored, an image pull failed midway, the runner was lost. That is a Kazu-side failure. It is different from a system that hangs, which the traffic-exit check and the 30-minute cap already catch as facts about the system. An incomplete pair fails the scenario with the result *trial incomplete*, and the way forward is to run it again. It has no switch of its own, because it says nothing about the system; `blocking: false` (point 13) covers a scenario that keeps hitting it.

### 4. `fail_on: violation`

`gate.fail_on: violation` is the one alternative mode: head must pass every pass/fail check outright. It drops proof (2). A relative check has no outright pass, so it stays on proof (3).

### 5. An unverifiable trial

A trial in which the check errored, or its metric had no data, is unverifiable for that trial.

- **On head** it blocks proof (1). A relative check has no proof to fall back on, so a head trial with no samples, or one that errored, fails the check, exactly as it fails an absolute check that base passed. The result is ***no data*** (under `gate.no_data`) or ***check error***, as DESIGN §6.8 already says of a query with no samples in a trial.
- **On base** it blocks proof (2). In a relative check, a pair whose base measurement is missing can't be compared and is left out, which leaves fewer pairs to decide with.

Both choices resolve the unknown against head. An absolute `metric` check (`max:` or `min:`) with no data in some trials follows the same two lines.

### 6. The table, as an illustration

The table is derived from the rule. It is not the rule. It shows the decision of a pass/fail check under `fail_on: regression`, where eligibility is no worse (point 2) and each side gave the same result in every trial. *Unverifiable* here means every trial on that side errored or had no data.

| base ↓ / head → | pass | fail | unverifiable |
|---|---|---|---|
| **pass** | pass (1) | fail | fail |
| **fail** | pass (1, 2) | pass (2) | pass (2) |
| **unverifiable** | pass (1) | fail | fail |

The bottom row's failing cells are named by why base is unverifiable: *incompatible with base* when it errored in every trial, and *no data* when it had no samples. A base that was never eligible has no decision either, so it reads as the bottom row, and its "head fail" cell is *new check failing* (point 8). A head that was never eligible is not in the table: it has already failed on eligibility (point 2).

### 7. With several trials: a pass/fail check that fails in any head trial fails

Decided by the maintainer on 2026-11-27. With several trial pairs a side's result can be mixed. For a pass/fail check:

- **Proof (1)** needs head to pass in every trial where the check was eligible.
- **Proof (2)** needs base to fail in every trial where the check was eligible.
- **There is no proof (3).** Kazu doesn't compare failure rates. Proof (3) stays for relative measures and for the eligibility counts of point 2.

So a pass/fail check that fails in some head trials fails, whatever base did, unless base failed in every trial. The maintainer's reasoning: no other kind of testing, integration or end to end, tries to accommodate flake, so Kazu doesn't invent a workaround for it.

- **The `flaky` result and `gate.flaky` are removed.** A failure on head is a plain failure, and the result name says which case it is. These are result names in `--json` and the record, like *no baseline*:
  - **REGRESSED:** base had no failing trial, and head failed in at least one. The counts are shown, as DESIGN §4.8 does today (`0/5 → 4/5 trials failing`). A base trial that errored is unknown, so it is read against head, as a pass. The precedence: with at least one base trial that could be judged, and no failure among those, a failing head is REGRESSED. With no base trial that could be judged, it is *incompatible with base* (base errored every time) or *no data* (base had no samples). With base never eligible, it is *new check failing* (point 8).
  - ***fails intermittently on head*:** base failed in some trials but not all, and head is mixed too.
  - ***fails on head*:** base failed in some trials but not all, and head failed in every one.

  ```
    ✗ double-click: no_double_charge fails intermittently on head (2/10 trials; base 1/10)
      to stop this blocking: fix the flake, remove the check from kazu.yaml (or defaults.off for a built-in), or set blocking: false on the scenario
  ```

- **No per-result switch governs it. The way forward, finest first:** fix the flake; or remove the check from head's `kazu.yaml`, or switch a built-in off with `defaults.off`; and only then waive the scenario with `blocking: false` (point 13). The order matters because a top-level check or a correctness default runs in every scenario. A flake in one of those would need every scenario waived, which turns the gate off; removing that one check keeps the rest of the gate.
- **A pass/fail check is never undecided.** After any number of trials it has passed or failed. So adaptive trials stop at the minimum number of pairs unless a relative measure or an eligibility count in the scenario is still undecided.
- **A pass by (1) is "passed in every trial that ran".** The number of trials pulls two ways. With a per-trial failure rate *q*, the chance that a failure shows in *N* trials is 1 − (1 − *q*)^*N*. More trials catch a rare regression more often, and they also make a flake that is already on main block more often. More trials also make relative checks underpowered less often. The minimum number of pairs is set with all three in view, in the verdict-method spike.

The alternative considered was to compare failure rates against a margin, passing with a *flaky* warning when head was shown no worse and failing as underpowered otherwise. It lost on the reasoning above. It would also have needed a non-inferiority margin on failure rates, and a rule for the edge between a base that fails 5 of 5 (proof 2) and one that fails 4 of 5.

### 8. No baseline, and a new check failing

When base was never eligible and head was, there is nothing on base to compare with.

- **A pass/fail check that head passed** passes by (1).
- **A pass/fail check that head failed** fails, with the result ***new check failing***. It isn't REGRESSED, because base never did it. It has no switch of its own; `blocking: false` waives the scenario (point 13). The way forward is the code or the check, and both are in the PR.
- **A relative check** has no pass or fail of its own, so it can't use (1) or (2), and with no measurement on base it has no (3). It fails, with the result ***no baseline***:

  ```
    ✗ new-export: payments_p99 within 10% has no baseline on base
      this path is new in head; use an absolute check (slo:, sql:, http:) until it's on main
  ```

*No baseline* means only "base was never eligible". It is its own result name in `--json` and the record, and its severity comes from the `underpowered` switches. A base whose metric had no data is ***no data***, under `gate.no_data`. So each condition has one result and one switch.

More trials can't fix either condition, so adaptive trials stop adding pairs for that check once the minimum number of pairs shows it.

### 9. Underpowered fails by default

Underpowered is "(3) couldn't be shown", so the check fails. That covers the decision and the eligibility comparison alike.

- `gate.underpowered` defaults to `fail`. `gate.not_exercised: fail` and `gate.no_data: fail` stay.
- A scenario may carry `underpowered: warn`, beside the existing scenario `not_exercised: warn`. The most specific setting wins. Both keys take `warn` or `fail`.
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

### 10. The other results under the rule

The rule decides pass or fail. The result names say why a check failed.

- **Not exercised:** never eligible on either side. It fails, under the `not_exercised` switches.
- **No data:** the trials without samples are unverifiable (point 5). A check with no data in any head trial fails as *no data*, unless it is a pass/fail check that base failed in every trial. A relative `metric` check with no data on base in every trial has no (3) and fails as *no data* too. Both are under `gate.no_data`.
- **Check error:** the trials that errored are unverifiable (point 5). There is no switch for the result. The checks come from head's `kazu.yaml`, so the way forward for an error is the check itself, in the same PR, with `blocking: false` (point 13) behind it. Principle 5 holds: nobody is left blocked.
- **Incompatible with base** (the check errors on base, DESIGN §4.1): base is unverifiable. A pass/fail check that head passed passes by (1), and the result is still labelled *incompatible with base*. If head didn't pass it, it fails. Today DESIGN reports it "not as a verdict"; under the rule it has one.
- **REGRESSED** is a pass/fail check that failed on head when base had no failing trial (point 7), a decided comparison that found head worse, or eligibility lost on head. Underpowered is never reported as REGRESSED: Kazu doesn't guess which way an undecided comparison would have gone.

### 11. The roll-up

A scenario fails if any result is at `fail` severity: REGRESSED, fails on head, fails intermittently on head, new check failing, not exercised, no data, underpowered, no baseline, a check error, incompatible with base where head didn't pass, or trial incomplete. Every such result is listed. A result downgraded to `warn` is printed on a `⚠` line and doesn't fail the scenario; at `fail` severity it is printed on a `✗` line. So underpowered is `✗` by default and `⚠` only where a switch downgraded it. So no third outcome leaves the scenario green by default.

This fixes an inconsistency DESIGN already has, apart from underpowered: §6.8 says no data "fails by default", while §4.6's roll-up sentence reaches "otherwise pass" without it.

### 12. The underpowered message

It keeps saying what would resolve it (a wider tolerance, more trials, a quieter runner), and now names the switch:

```
  ✗ db-blip: latency within 1% is underpowered
    this run's noise on checkout p99 is ±14% after 12 trial pairs; widen the tolerance, allow more trials or use a quieter runner
    to stop this blocking: underpowered: warn on the scenario
```

Under `trials: auto` Kazu still adds pairs while a comparison is undecided, and calls it underpowered only once the budget is spent (DESIGN §6.3).

### 13. `blocking: false` waives a scenario

Decided by the maintainer on 2026-11-27. A scenario may carry `blocking: false`. The default is `blocking: true`.

- **The scenario still runs**, and every result is computed and reported exactly as usual, REGRESSED and the other failures included.
- **It never fails the gate.** The run's exit status and the status check ignore it. A run fails if a blocking scenario fails, or if the run itself failed (a config error, a planner rejection, a missing shard). A run with no blocking scenario says so in its output.
- **Every output marks it**, so a reader sees that it was waived: the terminal, `--json` and the run record, JUnit, Markdown, the PR comment and the status check. In the terminal:

  ```
    double-click  (non-blocking)   ✗ no_double_charge fails intermittently on head (2/10 trials; base 1/10)
  ```

```yaml
scenarios:
  double-click:
    blocking: false           # the race in payments is on main already; remove this line when it is fixed
    env: { traffic: { DOUBLE_SUBMIT: "1" } }
```

**Why it exists.** It is the way forward when a scenario fails for a reason the PR can't fix. The main case is flakiness already on main: under point 7 a check that is mixed on head fails, and no other switch covers it. It is deliberately blunt: one visible waiver per scenario, for use while the bug gets fixed. It is not a way to accommodate flake, because it hides nothing and compares nothing.

**It completes principle 5.** Every failure now has a switch, including the results with none of their own: a mixed result on head, *new check failing*, *trial incomplete* and a check error.

**Precedence.** `blocking: false` waives the whole scenario, whatever the severities of its results. The scenario's `not_exercised: warn` and `underpowered: warn` stay as the finer switches: each downgrades one kind of result while a regression in the same scenario still blocks. Leaving a scenario out of a run altogether stays a `suites:` choice.

**Schema and phases.** A scenario-level boolean, `scenarios.<name>.blocking`, joining the schema in Phase 5 with `gate`. The PR that adds it gets its PLAN bullet. The rule applies from Phase 3, so in Phases 3 and 4 the results with no switch of their own have no waiver either. That is acceptable before the v0.x launch at the end of Phase 5: until then the only user is Kazu's own gate on the shop.

### Alternatives considered

- **Keep `warn` as the default (DESIGN as written).** Fewer red runs on noisy runners. It lost because a green check with a warning beside it is read as a pass: the comparison the user asked for was never made, and the PR merges anyway. A user who accepts that for one scenario can say so in the config, where a reviewer sees it.
- **Fail, with only the run-wide switch.** It lost because one noisy scenario would force `gate.underpowered: warn` on every scenario.
- **A per-check switch as well.** Not wanted yet (point 9).
- **A table of cases as the rule** (this ADR's first draft). It lost because each case needs its own argument; the three proofs give every cell.

### Antithesis alignment

Checked against `antithesis.com/docs/properties_assertions/assertions/` and `/properties_assertions/properties/` on 2026-11-27.

- **Antithesis fails on zero evidence.** "Always assertions are listed as passing by default until disproved by a failing encounter or if there no encounters at all." "Sometimes assertions are listed as failing by default until proven by a passing encounter." Of a reachability assertion: "If there are no encounters during the test run, the assertion fails." So an Always that was never reached fails, as DESIGN §6.7 and ADR-0011 already record. This ADR's rule agrees: no evidence is not a pass.
- **It has no counterpart to underpowered.** Antithesis doesn't compare measurements between two builds, and it runs deterministically (bugs are "perfectly reproducible"), so there is no per-run noise to resolve. Kazu's paired comparison is already a recorded deviation (DESIGN §6.7, the "Regression defaults" row), and underpowered is a result of it.
- **It has no flaky result either.** An Always assertion is "disproved by a failing encounter": one counterexample fails it. Point 7 does the same with one failing head trial.
- **Proof (2) is Kazu's own.** An Antithesis property that fails is failing, whatever an earlier run did. Passing because base failed too belongs to the base-versus-head deviation already in §6.7 (the "User-declared scenarios compared base vs head" row). It is not a new one.
- **`blocking: false` is a setting of Kazu's gate.** The pages checked describe properties and their statuses, not which failures block a merge, so there is no Antithesis decision here to follow or depart from.

**No new deviation.**

## Consequences

**Amends ADR-0011.** Superseded or modified:

- **Rule 5, "Eligible on head but never on base", with its accepted gap:** superseded by point 8. A correct new pass/fail check passes by (1) and is no longer underpowered. A wrong one fails as *new check failing*, so the accepted gap closes. A relative check there is *no baseline*.
- **Rule 5, the one-pair bullet "eligible on head but not on base passes":** becomes "judged by the rule". Head passing passes; head failing fails. The other three one-pair lines stand.
- **Rule 5, the adaptive-trials bullet:** gains the early stop of point 8.
- **Rule 7's roll-up sentence:** superseded by point 11.
- **The line under "Superseded from ADR-0009"** that describes the head-only case as "not a regression on eligibility, with an underpowered decision": the second half no longer holds.
- **Rule 5's "so `flaky` and `underpowered` apply" and rule 7's "`gate.fail_on` and `flaky` govern it"**, both about the eligibility comparison: `flaky` goes (point 7). An undecided eligibility comparison is underpowered.
- **Rule 5, "The decision is compared over the pairs in which both sides were eligible. Too few such pairs to decide is underpowered":** now holds only for relative checks. A pass/fail check's decision follows point 7 and is never underpowered.
- **What stands in rule 5:** never eligible on either side is *not exercised*; eligibility lost on head is judged on the eligible-trial counts; the two comparisons are independent; the `fail_on: violation` bullet; and the landed check's independence from eligibility. Point 2 adds that the eligibility comparison has to hold whatever the decision says.

**Folded in on acceptance:**
- **DESIGN §2 principle 5:** the rule, the three proofs and "an unknown is resolved against head"; underpowered joins the missing signals that are never a pass. The "every failure has a switch in the user's config" clause is rewritten: "Every failure has a way forward in the user's config: a severity switch, `blocking: false` on the scenario, or an edit to the check in head's `kazu.yaml` (fix it, remove it, or `defaults.off`). Results that say nothing about the system (*trial incomplete*) are fixed by running again."
- **DESIGN §4.1:** *incompatible with base* gets its outcome (point 10).
- **DESIGN §4.4:** `gate.underpowered: fail`, and a scenario in the reference carries `underpowered: warn`; `gate.flaky` is removed; one scenario carries `blocking: false` with a comment.
- **DESIGN §4.6:** "Underpowered is a warning by default" becomes "fails by default"; in the Verdict list, the head-only bullet, the one-pair bullet, the adaptive-trials bullet and the "decision is compared over the pairs … underpowered" bullet (all as amended above), and point 2; the Switches paragraph gains the two levels for `underpowered` and the roll-up of point 11; every mention of `flaky` goes, and point 7 takes its place; `blocking: false` and its precedence (point 13).
- **DESIGN §4.8:** the underpowered line becomes a `✗` failure line, not `⚠`, with the switch named; the *no baseline*, *new check failing*, *fails on head* and *fails intermittently on head* lines, the last two naming the fix; the non-blocking marker, and the line for a run with no blocking scenario.
- **Every other output:** `--json` and the run record, JUnit, Markdown, the PR comment and the status check carry the non-blocking marker and the new result names.
- **DESIGN §6.1:** "No verdict on a fault that didn't land, on a metric with no data, or on a comparison too noisy to decide" contradicts the rule. Each of those now has a verdict: it fails by default.
- **DESIGN §6.3:** the underpowered bullet says it fails by default. The "Flaky scenarios are classified and reported as flaky" bullet is replaced by point 7, and the comparison of failure counts goes. The adaptive-trials bullet gains the stopping rule: a pass/fail check is never undecided, so trials stop at the minimum unless a relative measure or an eligibility count is still undecided.
- **DESIGN §6 (the architecture diagram):** "flake and underpowered results" loses "flake".
- **DESIGN §7:** "Org-wide flake quarantine" and "waivers with expiry and audit" are removed from the paid tiers (decided by the maintainer, 2026-11-27). They are not reworded as paid management of `blocking: false` waivers: where the line between free and paid falls is settled later, once the product is good, and this isn't a compelling paid feature yet. The other items in those two rows (policy-as-code for gates; ownership routing and the coverage map) stay. PLAN.md's two mentions of "flake quarantine" go with them: the Phase 9 web UI bullet for the repo overview, and the frontend sketch's "Repo overview" row.
- **DESIGN §8:** the spike's false-failure target counts underpowered results on unchanged baselines, not only a false REGRESSED. The spike's inputs gain the stopping rule and the two-way pull of the trial count (point 7). The "No flaky rows" bullet loses its "flaky, not regressed" margin, since no row asserts flaky any more. The asserter's list of what it compares (verdict, which check caught it, *not exercised*, *no data*, *incompatible with base*) gains the new result names: *underpowered*, *no baseline*, *new check failing*, *fails on head*, *fails intermittently on head* and *trial incomplete*, and the non-blocking marker.
- **DESIGN §9 risk 2:** "the underpowered result" stops being a mitigation for flaky verdicts, and there is no flaky classification to lean on. The mitigations become adaptive trials, the scenario override and the message that names the fix. The risk also gains flakiness already on main, which now fails.
- **DESIGN §11:**
  - the "`no_regression`: … too noisy to decide is "underpowered", a warning" row gains an italic note, "*Superseded in part by ADR-0013: underpowered fails by default, with a scenario override*";
  - the "Head's traffic and checks run against both base and head" row gains one for "reported as incompatible, not as a verdict": "*Superseded in part by ADR-0013: it is judged on head*".
- **DESIGN §6.7:** nothing.
- **ADR-0011:** a note at the top that ADR-0013 amends rules 5 and 7.
- **CLAUDE.md:** the working agreement "Fail only on facts … every failure has a config switch" is brought into line with principle 5's new wording, "every failure has a way forward in the user's config". The maintainer makes that edit.
- **SPEC.md row S8:** "**underpowered**, not pass or fail" becomes a failing scenario whose result is underpowered, never `pass` and never `REGRESSED`.
- **SPEC.md row S7:** an underpowered `no_regression` on an unchanged baseline counts as a miss against the row's target share, and the share is set with that in mind.
- **SPEC.md row S9:** the expected result becomes `pass` for the check reading `currency`, labelled **incompatible with base**, because head passes it (proof 1). Its purpose becomes "a check that errors on base is judged on head".
- **SPEC.md row S5** (the race flag on both sides): the expected result becomes a failure, ***fails intermittently on head***. The row is made decisive, as DESIGN §8's "no flaky rows" asks: the race's rate is set so that a trial fails about half the time, and the row runs a fixed 14 pairs, so each side is mixed in all but about 1 run in 8,200. The row needs both sides mixed, so it misses in about 2 runs in 8,192, or 1 in 4,100. A per-trial rate anywhere from about 0.42 to 0.58 keeps the miss rate under 1 in 1,000 at 14 pairs, and that band is the acceptance test for the redesigned flag. The exact rate is set in the Phase 5 shop-rows PR, with the flag's redesign that SPEC.md already calls for. Its purpose becomes "a check that fails in any head trial fails, even when base flakes too". In the sentence under the table about decisive statistical rows, only S5's clause ("S5 asserts "flaky, not regressed" with a wide margin") goes; V9's and S7's stay.
- **SPEC.md row S6** (the race on head only): "REGRESSED given enough trials" no longer tests adaptive trials, because a pass/fail check is never undecided. The row's purpose, the statistical power of adaptive trials, moves to a relative check: a head made worse on a measure by more than the tolerance, REGRESSED once enough pairs are in. The row is redesigned in the Phase 5 shop-rows PR. The `SHOP_BUG_RACE_P` row of the bug-flag table, which says V9, S5 and S6 depend on the flag, changes with it once S6 no longer uses the race.
- **PLAN.md Phase 3:** the "checks 4/4" bullet (incompatible with base has an outcome) and the verdict bullet (the head-only case, *new check failing*, check errors and *trial incomplete* under the rule).
- **PLAN.md Phase 5:** the spike bullet (the budget now sets a failure rate), the "paired comparison … **underpowered**" bullet, the adaptive-trials bullet (the early stop), the "flaky classification; `gate.*` honoured end to end" bullet (no flaky classification and no `gate.flaky`; `scenarios.<name>.blocking` and the run outcome of point 13; the scenario `underpowered` switch, *no baseline* and the roll-up), and the shop-rows bullets for S4–S6 and S7–S9. The v1 scope table's Verdict row loses "flaky".
- **Schema (Phase 5):** `underpowered` on a scenario and on `gate`, each `warn | fail`; a scenario boolean `blocking`; no `gate.flaky`.

**What becomes easier:**
- A green check means one thing: for every check, Kazu showed that head is no worse than base.
- One rule and one principle give every case. A new result kind needs only to say which trials it makes unverifiable.
- A correct new check passes on the PR that adds it.
- CLAUDE.md and DESIGN say the same thing.
- A team's accepted blind spots are in `kazu.yaml`, one line per scenario, where review sees them.

**What becomes harder, and the trade-offs:**
- **The gate fails on noise.** DESIGN §9 risk 2 says "if the gate cries wolf, it gets disabled", and this decision raises that risk: a PR that changed nothing about latency can fail because the runner was noisy. Three things hold it down: adaptive trials add pairs before giving up, the message names the fix, and the scenario override turns it off where a team accepts it. None of them removes it.
- **The verdict-method spike matters more** (DESIGN §8, §10). The trial budget and the minimum number of pairs used to decide how often a warning appeared; now they decide how often a PR is blocked. The spike has to report the underpowered rate on unchanged baselines, per measure.
- **The default tolerances matter more.** The regression defaults run with no config, so their tolerances decide whether a first run on a noisy runner is red. The 2026-10-16 noise spike saw p99 differ by more than 20% in 1 of 10 identical pairs, and the recovery window by 109–507%. A default that is underpowered on most runners can't ship switched on.
- **A small fixed `gate.trials` fails often.** With two or three pairs most measurement comparisons can't be decided.
- **The scenario switch is coarse.** `underpowered: warn` downgrades every underpowered and no-baseline result in the scenario, not only the noisy comparison it was written for. The finer remedy is more trials for that one scenario. How trials are configured per scenario (for example `max_trials`) is an open decision in PLAN.md, taken with the spike.
- **A transient gap in the metrics fails the check.** One head trial with no samples, from a scrape or an export that missed, fails a relative or absolute `metric` check as *no data*. `gate.no_data: warn` is the switch.
- **A relative check on a new path fails until the path is on main.** The user writes an absolute check for that PR, or sets the scenario to `warn`.
- **Flakiness that is already on main blocks every PR.** A check that fails in 1 of 10 trials on both sides fails each PR whose head run hits it, and that PR's author didn't cause it. This is the "cries wolf" case of DESIGN §9 risk 2, accepted on purpose. The way through is to fix the flake or remove the check, and failing that `blocking: false` on that scenario while the flake gets fixed. The cost of the waiver is that the scenario's real regressions stop blocking for as long as the line stays. A rerun can also turn such a failure green, so some teams will rerun instead of fixing or waiving.
- **There is an edge between proofs (2) and none.** A base that fails 5 of 5 passes head whatever head did; a base that fails 4 of 5 fails a head that fails at all.
- **Proof (2) still passes a head that errors when base failed in every trial.** Counting head's error as a failure changes nothing there: base failed, so head is no worse. `fail_on: violation` is the mode for a team that doesn't want it.
- **Trial incomplete has no switch of its own.** An unreliable runner blocks until the run is repeated, or the scenario is set to `blocking: false`.
- **An override can outlive its reason.** A scenario set to `warn`, or to `blocking: false`, stays quiet after its cause is gone. Nothing expires it. The warning or the non-blocking marker is still printed on every run.

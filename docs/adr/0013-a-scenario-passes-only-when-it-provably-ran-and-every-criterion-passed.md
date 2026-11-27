# ADR-0013: A scenario passes only when it provably ran and every criterion passed; underpowered fails by default

Date: 2026-11-27
Status: Proposed

## Context

Kazu has three results that mean "there was no signal": *not exercised* (a fault never landed, or a check's `requires:` was never met), *no data* (a metric returned no samples) and *underpowered* (a comparison too noisy to decide within the trial budget). The repo says two different things about the third.

**CLAUDE.md** (product principle) puts all three together:

> **Fail only on facts** (product principle, DESIGN §2): a missing signal — not exercised, no data, underpowered — is never a pass, and every failure has a config switch.

**DESIGN.md** fails on the first two and only warns on the third:

- §2 principle 5: "A missing signal (a fault that never landed, a metric with no data, a check whose `requires:` was never met) is never a pass, and a comparison too noisy to decide is reported as such, never guessed." Underpowered is left out of the list of missing signals.
- §4.4, the `gate:` block: `underpowered: warn                  # a comparison too noisy to decide is reported, not guessed`, beside `not_exercised: fail` and `no_data: fail`.
- §4.6, `no_regression`: "Underpowered is a warning by default (`gate.underpowered`)."
- §4.6, Switches: "Check results roll up to the scenario: REGRESSED if any check regressed; otherwise not exercised if any check or fault was not exercised at `fail` severity; otherwise pass. Underpowered, flaky and no-data results are reported per check beside the roll-up, under their own switches."
- §4.8 shows it as a warning: `⚠ db-blip: latency within 1% is underpowered`.
- §11: "`no_regression`: user-chosen tolerance; noise estimated in every run, per scenario and per measurement, from the trial pairs; too noisy to decide is "underpowered", a warning".

So under DESIGN a scenario whose comparison could not be decided is green, with a warning beside it. A regression smaller than this run's noise then merges under a green check. The maintainer settled the conflict on 2026-11-27, in CLAUDE.md's favour.

## Decision

**Pass is a high bar. A scenario passes if and only if Kazu can verify that the scenario ran and every established criterion passed. Anything else fails by default. So underpowered fails by default, like not exercised and no data.**

1. **The pass definition.** A scenario passes when both hold:
   - **It provably ran.** Every configured fault landed, every check was eligible, and every metric had data.
   - **Every criterion passed.** Every check, written by the user or a default, reached a decision, and the decision was a pass. A comparison that could not be decided has not passed.

   A result is then one of two things: a pass, or a failure that names what is missing or wrong. There is no third outcome that leaves the scenario green.

2. **Defaults.** `gate.underpowered` defaults to `fail`. `gate.not_exercised: fail` and `gate.no_data: fail` stay as they are.

3. **The switches.** The user decides whether an underpowered result blocks, at two levels, the more specific winning:
   - **scenario:** `underpowered: warn` on the scenario, beside the existing scenario `not_exercised: warn`;
   - **run-wide:** `gate.underpowered: fail | warn`.

   The scenario setting wins over the gate, as with the other switches. Both keys take `warn` or `fail`, so a run-wide `warn` can be tightened on one scenario.

   ```yaml
   scenarios:
     db-blip:
       underpowered: warn        # this scenario's latency is too noisy on our runners to gate on
       faults:
         - { service: postgres, fault: pause, after: 20s, for: 10s }

   gate:
     underpowered: fail          # the default
   ```

   Neither key is in the schema today. Both join it in Phase 5, with `gate` and the statistical verdict; before that there is no underpowered result to switch.

4. **No per-check switch.** `not_exercised` has a check-level switch (ADR-0011); `underpowered` does not get one. The maintainer asked for a per-scenario override, and nothing needs a finer one yet. The cost is stated under Consequences. A check-level key can be added later without breaking any config.

5. **The roll-up.** A scenario fails if any of its results is at `fail` severity, and every such result is listed. In DESIGN §4.6's roll-up sentence, underpowered and no data at `fail` severity join not exercised ahead of "otherwise pass". A result downgraded to `warn` is still printed, as a warning, and doesn't fail the scenario.

6. **Underpowered is not REGRESSED.** The result keeps its own name in the output, the record and `--json`. Kazu still never guesses which way an undecided comparison would have gone; it reports that it couldn't decide, and that is what fails.

7. **The message says what would resolve it.** As today, an underpowered result reports the noise this run measured and the remedy: a wider tolerance, more trials, or a quieter runner. Now that it fails, it also names the switch:

   ```
     ✗ db-blip: latency within 1% is underpowered
       this run's noise on checkout p99 is ±14% after 12 trial pairs; widen the tolerance, allow more trials or use a quieter runner
       to stop this blocking: underpowered: warn on the scenario
   ```

8. **Adaptive trials come first.** Under `trials: auto` Kazu adds pairs while a comparison is undecided, and calls it underpowered only once the budget is spent (DESIGN §6.3). That is unchanged, and it is the main defence against failing on noise.

### Alternatives considered

- **Keep `warn` as the default (DESIGN as written).** Fewer red runs on noisy runners. It lost because a green check with a warning beside it is read as a pass: the comparison the user asked for was never made, and the PR merges anyway. A user who accepts that for one scenario can now say so in the config, where a reviewer sees it.
- **Fail, with only the run-wide switch.** Simpler. It lost because one noisy scenario would force `gate.underpowered: warn` on every scenario, and the gate would go quiet everywhere.
- **A per-check switch as well.** More precise. Not wanted yet (point 4).

### Antithesis alignment

Checked against the Antithesis docs on 2026-11-27 (`antithesis.com/docs/properties_assertions/assertions/`, `/properties_assertions/properties/`, and the REST API reference).

- **Antithesis has no inconclusive result.** A property is Passing or Failing; the API's status filter takes only those two values.
- **It has no counterpart to underpowered.** Antithesis doesn't compare measurements between two builds, and it runs deterministically, so there is no per-run noise to resolve. The docs describe bugs as "perfectly reproducible". Kazu's paired comparison of measurements is already a recorded deviation (DESIGN §6.7, the "Regression defaults" row), and underpowered is a result of that deviation.
- **Where Antithesis lacks evidence, the default depends on the assertion.** "Sometimes assertions are listed as failing by default until proven by a passing encounter", and a reachability assertion with no encounters fails. An Always assertion is listed as passing until a failing encounter disproves it.

So this ADR adds **no new deviation**: Antithesis has made no decision for the same problem. It moves Kazu closer in one respect, since a Kazu scenario now has two outcomes, as an Antithesis property does. The Always default points the other way, though: there, no evidence of failure is a pass. A `no_regression` check is nearer to an Always claim ("head is never worse than base by more than N%") than to a Sometimes one. Kazu differs because it knows when it lacked the evidence to judge: it measured the noise. Antithesis's Always has no such knowledge to act on. If the maintainer reads that as a deviation, it gets a row in DESIGN §6.7 on acceptance.

### Open sub-question (the maintainer settles it)

**1. Does "anything else is a failure" apply to `flaky`?**

`gate.flaky: warn` today. A flaky check fails in some trials on both sides, and the base-versus-head comparison finds head no worse, so it is classified flaky rather than regressed.

Recommendation: **no. `gate.flaky` stays `warn`, on one condition this ADR makes explicit: a check is called flaky only when the comparison was decided.**

- **Flaky is not a missing signal.** The scenario ran and the comparison reached a decision: head is no worse than base. Under `gate.fail_on: regression` that is the criterion, and it passed. Not exercised, no data and underpowered all mean Kazu couldn't judge. Flaky means it judged, and also noticed something about base.
- **It matches the rule for a steady violation.** A check that fails on both sides in every trial passes under `fail_on: regression`, because the PR didn't cause it. A check that fails on both sides in some trials is the same case, intermittent. Failing one and passing the other would be inconsistent.
- **Failing it blocks PRs for flakiness that was already on main.** That is the "cries wolf" case of DESIGN §9 risk 2, and the PR's author can't fix it in that PR.
- **The condition closes the gap.** If the failure counts on the two sides can't be told apart within the budget (2 of 5 on base against 4 of 5 on head, say), the result is underpowered, not flaky, and under this ADR it fails. So "flaky" can never be the label on an undecided comparison.
- **The strict option already exists.** `gate.flaky: fail` is there for a team that wants pre-existing flakiness to block, and under `gate.fail_on: violation` any failing head trial fails anyway.

The other reading: the maintainer's sentence is "anything else is a failure", and a check that fails in some head trials has not plainly passed. If that is the intent, `gate.flaky` becomes `fail` and gets a scenario override like this one.

## Consequences

**Folded in on acceptance:**
- **DESIGN §2 principle 5:** underpowered joins the list of missing signals that are never a pass; the principle states the pass definition.
- **DESIGN §4.4:** `gate.underpowered: fail`, and a scenario in the reference carries `underpowered: warn`.
- **DESIGN §4.6:** "Underpowered is a warning by default" becomes "fails by default"; the Switches paragraph gains the two levels for `underpowered` and the roll-up of point 5.
- **DESIGN §4.8:** the underpowered line becomes a `✗` failure line, not `⚠`, with the switch named (point 7).
- **DESIGN §6.3:** the underpowered bullet says it fails by default. The flaky bullet says flaky needs a decided comparison, if the sub-question is settled as recommended.
- **DESIGN §9 risk 2:** "the underpowered result" stops being a mitigation for flaky verdicts. The mitigations become adaptive trials, the scenario override and the message that names the fix.
- **DESIGN §11:** the "`no_regression`: … too noisy to decide is "underpowered", a warning" row gains an italic note, "*Superseded in part by ADR-0013: underpowered fails by default, with a scenario override*".
- **DESIGN §6.7:** nothing, unless the maintainer reads the Always default as a deviation.
- **PLAN.md Phase 5:** the "paired comparison … **underpowered**" bullet (fails by default), the "flaky classification; `gate.*` honoured end to end" bullet (adds the scenario `underpowered` switch and the roll-up), and the spike bullet (the budget now sets a failure rate).
- **Schema (Phase 5):** `underpowered` on a scenario and on `gate`, each `warn | fail`.

**What becomes easier:**
- A green check means one thing: every comparison was made and passed. Nobody has to read warnings to learn whether the gate judged the PR.
- CLAUDE.md and DESIGN say the same thing.
- A team's accepted blind spots are in `kazu.yaml`, one line per scenario, where review sees them.

**What becomes harder, and the trade-offs:**
- **The gate fails on noise.** DESIGN §9 risk 2 says "if the gate cries wolf, it gets disabled", and this decision raises that risk: a PR that changed nothing about latency can fail because the runner was noisy. Three things hold it down: adaptive trials add pairs before giving up, the message names the fix, and the scenario override turns it off where a team accepts it. None of them removes it. A user on shared runners with a tight tolerance will see red runs their PR didn't cause.
- **The verdict-method spike matters more** (DESIGN §8, §10). The trial budget and the minimum number of pairs used to decide how often a warning appeared; now they decide how often a PR is blocked. The spike has to report the underpowered rate on unchanged baselines, per measure, alongside the false-positive rate.
- **The default tolerances matter more.** The regression defaults run with no config, so their tolerances decide whether a first run on a noisy runner is red. The 2026-10-16 noise spike saw p99 differ by more than 20% in 1 of 10 identical pairs, and the recovery window differ by 109–507%. A default that is underpowered on most runners can't ship switched on.
- **A small fixed `gate.trials` fails often.** With two or three pairs most measurement comparisons can't be decided. The spike's minimum number of pairs has to cover this.
- **More trials don't resolve every underpowered result.** A check that is eligible on head but never on base has no pairs to compare (ADR-0011, decision 5), so it is underpowered whatever the budget. Under this ADR a PR that makes a check's `requires:` hold for the first time fails by default, whether head got it right or wrong. ADR-0011 accepted reporting a wrong head as underpowered; that case now blocks, which is the better outcome. The right head blocks too. Its message has to say that there is nothing on base to compare with, and that the scenario override, or merging so that base has it, are the ways through.
- **The scenario switch is coarse.** `underpowered: warn` on a scenario downgrades every underpowered result in it, a correctness check with too few eligible pairs included, not only the noisy latency comparison it was written for. That is the price of having no per-check switch.
- **An override can outlive its reason.** A scenario set to `warn` stays quiet after the runner gets quieter. The warning is still printed on every run.

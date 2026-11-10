# ADR-0010: Every scenario runs on every run; Kazu never skips one as unchanged

Date: 2026-10-28
Status: Proposed

## Context

Raised in the review of #50. DESIGN.md §6.1 "Content addressing" defines `plan_hash = H(scenario, seed, check and traffic image digests and mounted files, system image digests on the scenario's path)` and says identical base/head hashes skip the scenario. §6.4 counts the skip toward the ~10-minute PR gate ("adaptive trials, skip unchanged … ~5–8 min"), and PLAN.md Phase 8 has a bullet to build it.

Under "fail only on facts" (DESIGN §2), a skipped scenario either gates as a pass, which is unsafe whenever the hash misses an input, or is reported as not a pass, which makes the skip useless. So the skip is only correct when the hash covers every input that can change the system's behaviour. The definition already misses some:

- **Variables.** Base and head differ only in the variables the system file interpolates (§4.1), and the hash has none of them. The shop's bug flags are variables and base and head run the same images, so every flag-only or trap-only row hashes the same and would be skipped: all of V1–V12, and S1, S6, S9, S11–S14. That is the shop's whole oracle apart from rows with no difference at all.
- **"The scenario's path".** DESIGN doesn't define it; declared edges are the only candidate. A service off those edges can still change the outcome, for example a worker loading the same database, and an undeclared caller is exactly what `kazu doctor` exists to find.
- **Anything outside an image.** Compose-level settings (resources, healthchecks, environment, command), mounted config files and init scripts.

Each gap gives a pass that nothing tested, with nothing in the output to show it.

The skip saves least where it is safest. In a small compose system, most PRs change a service most scenarios pass through, so it rarely fires. CI usually rebuilds and retags every image on each commit, and builds are seldom byte-for-byte reproducible, so digests change even for untouched services. In a large system it would fire more, but that is where path analysis is hardest to get right.

## Decision

**Kazu runs every selected scenario on every run, base and head, whether or not anything on its path changed.** There is no skip-unchanged.

Content addressing stays for replay. Before the first trial, Kazu resolves every image to a digest, for every service on both base and head plus traffic and checks, and hashes the mounted files. It writes those pins and the resolved base and head variables to the run record, and `kazu replay` reruns from them. Today DESIGN pins only the traffic and checks images (§4.1); system digests appear only inside `plan_hash`, so this makes their pinning explicit.

A gate that's too slow gets faster through mechanisms that never turn an untested scenario into a pass: adaptive trials, more shards, dynamic leasing, base-image caching, and choosing which suite a PR runs (`kazu run --suite`). A suite the user didn't select isn't reported as passed.

Considered and lost:
- **Keep the skip and hash the fully resolved compose config** (every service, with variables interpolated) instead of a curated list. This closes the variable and compose-setting gaps. But the skip would still rest on the input list being complete, and compose sees bind-mount paths, not their contents. The gain is time on scenarios that are probably fine, and the cost of a mistake is a silent pass. Reopen this with a real customer's gate time as the evidence, not before.
- **Keep the skip as designed.** Fails on the gaps above.

## Consequences

- **Folded into DESIGN.md on acceptance:**
  - §6.1: the "Content addressing" bullet merges with "Traffic and checks are pinned once per run" into one bullet on pinning for replay.
  - §4.1: the pinning paragraph names system images as well.
  - The §6 architecture diagram's "content-hashed plan" becomes "pinned plan".
  - §6.4: the table row becomes "+ adaptive trials", with its time re-estimated.
  - "Skip unchanged" leaves the §7 tiers table and §8 step 5.
  - "Plan hashing" leaves §8's Go unit tests.
  - "Plan hashes" leaves the shared packages in §6.5.
  - §11's rows naming plan hashes among the shared packages stay as history, with a pointer here.
- **Folded into PLAN.md on acceptance:**
  - The Phase 8 `plan_hash` bullet and the "skip-unchanged" entry in the v1 scope table are removed.
  - Phase 9's shared-packages bullet drops "plan hash".
  - The Phase 4 run-record bullet takes on pinning every image and the resolved variables. The frontend sketch's "changed since base" reads from those pins.
- The ~5–8 min estimate in §6.4 has to be redone without the skip. Adaptive trials did most of that reduction, so the gate target should still hold for systems the size of the shop. A system with many scenarios may need more shards sooner.
- Row S1 no longer needs head to differ from base. Once this is accepted, the `SHOP_NOOP` variable added in #50 can be dropped from SPEC.md.
- CI cost scales with the number of scenarios, not with the size of the change. A team with a large suite controls this through suites and shards, and sees exactly what ran.

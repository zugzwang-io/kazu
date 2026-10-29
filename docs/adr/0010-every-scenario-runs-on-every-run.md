# ADR-0010: Every scenario runs on every run; Kazu never skips one as unchanged

Date: 2026-10-28
Status: Proposed

## Context

Raised in the review of #50. DESIGN.md §6.1 "Content addressing" defines `plan_hash = H(scenario, seed, check and traffic image digests and mounted files, system image digests on the scenario's path)` and says identical base/head hashes skip the scenario. §6.4 counts the skip toward the ~10-minute PR gate ("adaptive trials, skip unchanged … ~5–8 min"), and PLAN.md Phase 8 has a bullet to build it.

A skipped scenario is reported as unchanged, never tested, so the skip is only correct when the hash covers every input that can change the system's behaviour. The definition already misses some:

- **Variables.** Base and head differ only in the variables the system file interpolates (§4), and the hash has none of them. Every row whose head differs from base only by a flag, with the same `TAG` on both sides (S14, V1, V2, the quickstart), hashes the same and would be skipped.
- **"On the scenario's path".** The path comes from declared edges. A service off the path can still change the outcome, for example a worker loading the same database, and an undeclared caller is exactly what `kazu doctor` exists to find.
- **Anything outside an image.** Compose-level settings (resources, healthchecks, environment, command), mounted config files and init scripts.

Each gap gives a pass that nothing tested, with nothing in the output to show it. That breaks "fail only on facts" (DESIGN §2): a missing signal is never a pass.

Where the skip would save time is where it is weakest. In a small compose system, most PRs change a service most scenarios pass through, so it rarely fires. In a large one it would fire often, but that's where path analysis is hardest to get right.

## Decision

**Kazu runs every selected scenario on every run, base and head, whether or not anything on its path changed.** There is no skip-unchanged.

Content addressing stays for what it is needed for: before the first trial, images are resolved to digests and mounted files hashed, the pins are written to the run record, and `kazu replay` reruns from them.

A gate that's too slow gets faster through mechanisms that never turn an untested scenario into a pass: adaptive trials, more shards, dynamic leasing, base-image caching, and choosing which suite a PR runs (`kazu run --suite`). A suite the user didn't select isn't reported as passed.

Considered and lost:
- **Keep the skip and hash the fully resolved compose config** (every service, with variables interpolated) instead of a curated list. This closes the variable and compose-setting gaps, but the skip would still rest on the input list being complete, and mounted files and init scripts sit outside compose's view. The gain is time on scenarios that are probably fine, and the cost of a mistake is a silent pass. Reopen this with a real customer's gate time as the evidence, not before.
- **Keep the skip as designed.** Fails on the gaps above.

## Consequences

- **Folded into DESIGN.md on acceptance:** the §6.1 "Content addressing" bullet becomes pinning for replay only; §6.4's table row becomes "+ adaptive trials" with its time re-estimated; "skip unchanged" leaves the §7 tiers table and §8 step 5. PLAN.md's Phase 8 bullet for `plan_hash` and the "skip-unchanged" entry in the v1 scope table are removed then.
- The ~5–8 min estimate in §6.4 has to be redone without the skip. Adaptive trials did most of that reduction, so the gate target should still hold for systems the size of the shop; a system with many scenarios may need more shards sooner.
- Row S1 no longer needs head to differ from base. Once this is accepted, the `SHOP_NOOP` variable added in #50 can be dropped from SPEC.md.
- CI cost scales with the number of scenarios, not with the size of the change. A team with a large suite controls this through suites and shards, and sees exactly what ran.

---
name: verify-foundations
description: Independent verification panel — architecture, test coverage, code health, product and outside-view reviewers that challenge foundational assumptions, not just the diff. Use when the user wants assumptions challenged or red-teamed, asks "is this the right approach", is about to approve a foundational PR or design, asks for an end-of-phase check, or wants a periodic deep check of the whole tree.
allowed-tools: Read, Grep, Glob, Bash(gh *), Bash(git *), Bash(go *), Agent, Write, Edit
# Rare, unhurried and the costliest place to miss a flaw: the most capable
# model at full effort earns its price here.
model: fable
effort: max
---

Run five independent lenses whose job is to **challenge, not confirm**. Confirmation is cheap and worthless here — the maintainer invokes this exactly when agreement would be dangerous. This runs outside the everyday feature loop, on demand, so spend what it takes: every lens gets the same scope (a PR number if given, otherwise the whole tree) but a different mandate, and none sees the others' output until synthesis; independence is what makes five opinions worth more than one.

Spawn every reviewer in a single message (concurrent), each with `model: fable` and `effort: max` — including both code-health plugin agents, which otherwise run on their own definition's model — and each instructed: "Your job is to find the strongest case *against* the current approach. Assume the author is competent and still look for what they missed. Cite file:line, commits, or ADR/spec sections for every claim. If after honest effort the foundation holds, say so plainly — do not invent objections."

1. **Architecture**: do module boundaries, dependency direction, and abstractions still serve the README principles ("a fault-injection and assertion runtime, nothing more", one static binary)? Judge trajectory as well as state: `git log --stat` over the relevant paths, and whether today's structure fits what the next two phases of PLAN.md will demand or will force us to unbuild. Is any Accepted ADR now wrong, or only half implemented?
2. **Test coverage**: map SPEC.md's test-suite table against tests that actually exist; probe whether existing tests would catch realistic regressions (mutate-in-your-head: name a plausible bug each test misses). Distinguish "untested" from "untestable as designed".
3. **Code health**: duplication, error-handling consistency (%w, errors.Is/As, contexts), the shop's ~1,500-line budget, creeping cleverness against "keep it boring", files whose churn (`git log --stat`) says the design isn't settling. Use `pr-review-toolkit:code-simplifier` and `pr-review-toolkit:silent-failure-hunter` as this lens when available — they're purpose-built for it — passing this lens's mandate in the prompt and telling both to report findings only, never edit files (`code-simplifier` otherwise rewrites code). Fall back to a general agent with this mandate if the plugin is absent.
4. **Product**: does what's being built match what the README sells? Are we building in the order that de-risks the pitch (does the current phase's scope serve the quickstart demo)? What would a skeptical user hit first?
5. **Outside view**: argue as an Antithesis engineer. Where has Kazu deviated from how Antithesis solves the same problem without a reason recorded in DESIGN.md §6.7? Which claim in the README would the first real system disprove?

## Synthesis

- Discard findings that are taste, not substance. Where two lenses disagree, present both sides — disagreement between lenses is signal, not noise to smooth over.
- **Challenges to recorded decisions**: cite the ADR; if the challenge has merit, draft a superseding ADR (status: Proposed). **Decisions nobody recorded**: draft the missing ADR (status: Proposed) capturing what the code already committed to.
- Never change an ADR's status yourself — only the maintainer accepts, rejects, or supersedes.
- Output: verdict per lens (sound / contested / broken), the challenges worth the maintainer's time with evidence, ADRs drafted, and one recommended action per contested item.

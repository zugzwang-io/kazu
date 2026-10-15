---
name: verify-foundations
description: Independent verification panel — architecture, test coverage, code health, and product reviewers that challenge foundational assumptions, not just the diff. Use when the user wants assumptions challenged or red-teamed, asks "is this the right approach", is about to approve a foundational PR or design, or wants a periodic deep check of the whole tree.
allowed-tools: Read, Grep, Glob, Bash(gh *), Bash(git *), Bash(go *), Agent, Write, Edit
---

Run four independent reviewers whose job is to **challenge, not confirm**. Confirmation is cheap and worthless here — the maintainer invokes this exactly when agreement would be dangerous. Each reviewer gets the same scope (a PR number if given, otherwise the whole tree) but a different mandate, and none sees the others' output until synthesis; independence is what makes four opinions worth more than one.

Spawn all four in a single message (concurrent), each instructed: "Your job is to find the strongest case *against* the current approach. Assume the author is competent and still look for what they missed. Cite file:line or ADR/spec sections for every claim. If after honest effort the foundation holds, say so plainly — do not invent objections."

1. **Architecture**: do module boundaries, dependency direction, and abstractions still serve the README principles ("a fault-injection and assertion runtime, nothing more", one static binary)? Is anything being built that the next two phases of PLAN.md will force us to unbuild? Check decisions in `docs/adr/` — is any Accepted ADR now wrong?
2. **Test coverage**: map SPEC.md's test-suite table against tests that actually exist; probe whether existing tests would catch realistic regressions (mutate-in-your-head: name a plausible bug each test misses). Distinguish "untested" from "untestable as designed".
3. **Code health**: duplication, error-handling consistency (%w, errors.Is/As, contexts), the shop's ~1,500-line budget, creeping cleverness against "keep it boring". Use the `code-simplifier` and `silent-failure-hunter` agents (pr-review-toolkit plugin) as this lens when available — they're purpose-built for it; fall back to a general agent with this mandate if the plugin is absent.
4. **Product**: does what's being built match what the README sells? Are we building in the order that de-risks the pitch (does the current phase's scope serve the quickstart demo)? What would a skeptical user hit first?

## Synthesis

- Discard findings that are taste, not substance. Where two reviewers disagree, present both sides — disagreement between lenses is signal, not noise to smooth over.
- **Challenges to recorded decisions**: cite the ADR; if the challenge has merit, draft a superseding ADR (status: Proposed). **Decisions nobody recorded**: draft the missing ADR (status: Proposed) capturing what the code already committed to.
- Never change an ADR's status yourself — only the maintainer accepts, rejects, or supersedes.
- Output: verdict per dimension (sound / contested / broken), the challenges worth the maintainer's time with evidence, ADRs drafted, and one recommended action per contested item.

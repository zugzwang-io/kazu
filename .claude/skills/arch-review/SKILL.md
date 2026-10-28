---
name: arch-review
description: Whole-tree architecture and health review against README principles, SPEC.md, and accepted ADRs — drift, growing complexity, module hygiene, test posture. Use when the user asks for an architecture review, health check, "are we drifting", tech-debt audit, or any state-of-the-codebase question that isn't about one diff.
allowed-tools: Read, Grep, Glob, Bash(git *), Bash(go *), Agent
# Rare, unhurried and the costliest place to miss a flaw: the most capable
# model earns its price here.
model: fable
---

Review the codebase's architecture and health — the current state of the tree, not a diff. The maintainer uses this to catch slow rot that no single PR review can see, so judge trajectories, not snapshots.

## What to assess

1. **Spec drift**: does `examples/shop` still match `examples/shop/SPEC.md` (services, endpoints, resilience mechanisms, schema ownership, behaviour decisions)? Flag divergence in either direction — code that left the spec, or decisions made in code that SPEC.md should record.
2. **Principles** (README): fault-injection-and-assertion runtime and nothing more; fresh environment per trial; one static Go binary; "keep it boring" — flag creeping abstractions, premature generality, or the shop drifting past its ~1,500-line Go budget (`find examples/shop -name '*.go' -not -name '*_test.go' | xargs wc -l`).
3. **Decision integrity**: check `docs/adr/` — is the code still consistent with every Accepted ADR? Is there a decision embodied in code that no ADR or SPEC.md section records? Unrecorded decisions are findings even when the decision itself is good.
4. **Module hygiene**: nothing in the root module importing the shop or vice versa; shop dependencies staying out of the root go.mod.
5. **Test posture**: for each SPEC.md test-suite row that applies to code that exists today, does a test actually cover it? List gaps concretely (row → missing test).

For a large tree, fan out Explore agents per area rather than reading everything yourself; at the current repo size, read directly.

## Output

A short report: top 3 risks first (each with evidence — file:line or numbers), then per-area notes, then one recommended next action per risk. If nothing is wrong, say so plainly rather than inventing findings — a clean bill of health is a valid result. Compare against the previous report if the user points you at one.

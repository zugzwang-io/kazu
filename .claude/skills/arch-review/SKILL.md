---
name: arch-review
description: Periodic architecture review of the kazu codebase against README principles and SPEC.md — drift, growing complexity, spec violations, missing tests. Use when the user asks for an architecture or health review of the repo.
allowed-tools: Read, Grep, Glob, Bash(git *), Bash(go *), Agent
---

Review the codebase's architecture and health — not a diff review, the current state of the tree.

## What to assess

1. **Spec drift**: does `examples/shop` still match `examples/shop/SPEC.md` (services, endpoints, resilience mechanisms, schema ownership, behaviour decisions)? Flag divergence in either direction — code that left the spec, or decisions made in code that SPEC.md should record.
2. **Principles** (README): fault-injection-and-assertion runtime and nothing more; fresh environment per trial; one static Go binary; "keep it boring" — flag creeping abstractions, premature generality, or the shop drifting past its ~1,500-line Go budget (`find examples/shop -name '*.go' -not -name '*_test.go' | xargs wc -l`).
3. **Module hygiene**: nothing in the root module importing the shop or vice versa; shop dependencies staying out of the root go.mod.
4. **Test posture**: for each SPEC.md test-suite row that applies to code that exists today, does a test actually cover it? List gaps concretely (row → missing test).

For a large tree, fan out Explore agents per area rather than reading everything yourself; for the current repo size, read directly.

## Output

A short report: top 3 risks first (each with evidence — file:line or numbers), then per-area notes, then a recommended next action for each risk. If nothing is wrong, say so plainly rather than inventing findings. Compare against the previous report if the user points you at one.

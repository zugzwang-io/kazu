---
name: sync-plan
description: Reconcile PLAN.md with actual PR and merge state so the plan never lies. Use proactively after any PR is opened, updated, merged, closed, or retargeted — including by you or another agent — and whenever PLAN.md might be stale (e.g. the session brief and the plan disagree).
allowed-tools: Bash(gh *), Bash(git *), Read, Edit, Grep, Glob
---

Make PLAN.md true again. The plan is how the maintainer sees where the project stands without reconstructing it from PR lists — a stale plan is worse than none. The phase structure is human-owned; statuses and PR references are yours to maintain.

1. Read `PLAN.md`. Fetch reality: `gh pr list --state open ...` and `gh pr list --state merged --limit 20 ...` (number, title, baseRefName, mergedAt).
2. Reconcile:
   - Merged PR listed as `[~]` → `[x]` with its number.
   - Closed-unmerged PR → remove the line or mark abandoned, noting why if discoverable.
   - Open PR not in the plan → add it under the phase it belongs to (judge from its title/body); if no phase fits, add it under an "Unplanned" heading and flag it in your report — that's a plan/work divergence the maintainer should see.
   - A PR whose scope changed (title/body edited, significant new commits) → update its line's wording to match.
3. If a phase's items are all `[x]`, mark the phase ✅. If work has started on an "unrefined" phase, note that it needs refining into concrete items.
4. Keep edits minimal — don't rewrite prose, renumber phases, or reorder items; don't touch the Legend or the maintenance note at the top. Small diffs keep the plan reviewable like everything else.
5. PLAN.md is a tracked file and main only changes via PR, so your edit lands in the working tree and rides along with the next PR — that's expected. Never try to push a plan update directly to main.
6. Report what changed in one or two sentences (or "already in sync").

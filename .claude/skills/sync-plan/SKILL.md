---
name: sync-plan
description: Reconcile PLAN.md with actual PR and merge state. Use proactively after any PR is opened, updated, merged, or closed, and after pushing changes to any PR branch.
allowed-tools: Bash(gh *), Bash(git *), Read, Edit, Grep, Glob
---

Make PLAN.md true again. The phase structure is human-owned; statuses and PR references are yours to maintain.

1. Read `PLAN.md`. Fetch reality: `gh pr list --state open ...` and `gh pr list --state merged --limit 20 ...` (number, title, baseRefName, mergedAt).
2. Reconcile:
   - Merged PR listed as `[~]` → `[x]` with its number.
   - Closed-unmerged PR → remove the checkbox or mark abandoned, noting why if discoverable.
   - Open PR not in the plan → add it under the phase it belongs to (judge from its title/body); if no phase fits, add it under a "Unplanned" heading and flag it in your report — that's a plan/work divergence Varun should see.
   - A PR whose scope changed (title/body edited, big new commits) → update its line's wording to match.
3. If a phase's items are all `[x]`, mark the phase ✅. If work for an "unrefined" phase has started, note that it needs refining into concrete items.
4. Keep edits minimal — don't rewrite prose, renumber phases, or reorder items. Never change the Legend or the maintenance note at the top.
5. Report the diff of what changed in one or two sentences (or "already in sync").

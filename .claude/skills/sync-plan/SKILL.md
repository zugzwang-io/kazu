---
name: sync-plan
description: Reconcile PLAN.md with actual PR and merge state so the plan never lies. Use proactively after any PR is opened, updated, merged, closed, or retargeted — including by you or another agent — and whenever PLAN.md might be stale (e.g. the session brief and the plan disagree).
allowed-tools: Bash(gh *), Bash(git *), Read, Edit, Grep, Glob
# Reconciling checkboxes against gh output is mechanical and self-verifying;
# the cheapest model does it fine.
model: haiku
---

Make PLAN.md true again. The plan is how the maintainer sees where the project stands without reconstructing it from PR lists — a stale plan is worse than none. The phase structure is human-owned; statuses and PR references are yours to maintain.

1. Read `PLAN.md`. Fetch reality: `gh pr list --state open ...` and `gh pr list --state merged --limit 20 ...` (number, title, headRefName, baseRefName, mergedAt).
2. **The current PR's own line** (when running on an open PR's branch): mark its bullet `[x] #N`, worded to match the PR's scope. A PR's diff only reaches main by merging, so `[x]` is true on main the moment it lands. No bullet fits → add one under the phase it belongs to; no phase fits → add it under an "Unplanned" heading and flag it in your report, since that's a plan/work divergence the maintainer should see. If this PR completes its phase, mark the phase ✅.
3. **Drift, on lines no open PR owns**:
   - Merged PR not shown `[x]` (including legacy `[~]` lines) → `[x]` with its number.
   - Closed-unmerged PR → remove the line or mark abandoned, noting why if discoverable.
   - A phase whose items are all `[x]` → ✅. Work started on an "unrefined" phase → note that it needs refining into concrete items.
4. **Never edit a line an open PR owns** — that edit is what makes parallel PRs conflict. Report instead: an open PR whose branch doesn't mark its own line, or whose line no longer matches its scope, gets fixed on that PR's branch.
5. Keep edits minimal — don't rewrite prose, renumber phases, or reorder items; don't touch the Legend or the maintenance note at the top. Small diffs keep the plan reviewable like everything else.
6. PLAN.md is a tracked file and main only changes via PR, so the edit rides along with the PR whose branch you're on (or the next one, for drift). Never try to push a plan update directly to main.
7. Report what changed in one or two sentences (or "already in sync").

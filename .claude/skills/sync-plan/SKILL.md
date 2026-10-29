---
name: sync-plan
description: Reconcile PLAN.md with actual PR and merge state so the plan never lies. Use proactively on a PR's branch after opening or updating it (marks that PR's own line), after any PR merges or closes (to catch drift), and whenever PLAN.md might be stale (e.g. the session brief and the plan disagree).
allowed-tools: Bash(gh *), Bash(git *), Read, Edit, Grep, Glob
# Reconciling checkboxes against gh output is mechanical and self-verifying;
# the cheapest model does it fine.
model: haiku
---

Make PLAN.md true again. The plan is how the maintainer sees where the project stands without reconstructing it from PR lists — a stale plan is worse than none. The phase structure is human-owned; statuses and PR references are yours to maintain.

1. Read `PLAN.md`. Fetch reality: `gh pr list --state open ...` and `gh pr list --state merged --limit 20 ...` (number, title, headRefName, baseRefName, mergedAt).
2. **Where you are decides what you may edit.** On main, the maintainer's checkout, or any branch that isn't an open PR's: report only, never edit — an uncommitted PLAN.md there blocks `git pull --ff-only` and leaks into whatever branch is cut next. On an open PR's branch (ideally its own worktree): edit, then commit and push there.
3. **The current PR's own line**: mark its bullet `[x] #N`, worded to match the PR's scope. A PR's diff only reaches main by merging, so `[x]` is true on main the moment it lands. A PR an existing bullet describes is a product PR, whatever files it touches — mark that bullet. Otherwise a PR is setup-only, and gets no line, when it changes only agent instructions (`CLAUDE.md`, `.claude/`, `docs/sandbox.md`), repo tooling (`.golangci.yml`, `.gitignore`, the Claude bot workflows `.github/workflows/claude*.yml`), or PLAN.md housekeeping — the maintenance note, the legend, and rewording or regrouping records of finished work. ADRs, DESIGN.md, SPEC.md, planned work in PLAN.md (new or changed bullets, phases, scope) and product CI (test and e2e workflows) are product. No bullet fits a product PR → add one under the phase it belongs to; no phase fits → add it under an "Unplanned" heading and flag it in your report, since that's a plan/work divergence the maintainer should see. If this PR completes its phase, mark the phase ✅.
4. **Drift, on lines no open PR owns** (a line is owned by the open PR whose `#N` it carries):
   - A line for a merged PR that isn't `[x]` (including legacy `[~]` lines) → `[x]`. Drift only fixes lines that exist: a merged PR with no line is fine — setup-only PRs never get one.
   - Closed-unmerged PR → remove the line or mark abandoned, noting why if discoverable.
   - A phase whose items are all `[x]` → ✅. Work started on an "unrefined" phase → note that it needs refining into concrete items.
5. **Never edit a line an open PR owns** — that edit is what makes parallel PRs conflict. Report instead: an open PR whose branch doesn't mark its own line, or whose line no longer matches its scope, gets fixed on that PR's branch.
6. Keep edits minimal — don't rewrite prose, renumber phases, or reorder items; don't touch the Legend or the maintenance note at the top. Small diffs keep the plan reviewable like everything else.
7. PLAN.md is a tracked file and main only changes via PR, so the edit rides along with the PR whose branch you're on. Never try to push a plan update directly to main.
8. Report what changed in one or two sentences (or "already in sync").

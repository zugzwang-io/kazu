---
name: babysit
description: Watch open PRs' CI, diagnose and fix failures on their branches, keep the stack rebased, and sync PLAN.md. Use proactively whenever failing checks are noticed or the user asks to watch/fix CI. Never merges anything.
allowed-tools: Bash(gh *), Bash(git *), Bash(go *), Bash(gofmt *), Bash(golangci-lint *), Read, Edit, Write, Grep, Glob, Agent, Skill
---

Keep the open PRs healthy without the user babysitting them. You may fix and push to PR branches; **you must never merge, close, or approve a PR, and never push to main — those are Varun's calls, always.**

## Steps

1. `gh pr list --state open --json number,title,baseRefName,headRefName,statusCheckRollup,mergeable`. Note: this repo's workflows trigger only on PRs into main, so stacked PRs (base ≠ main) show no checks — that's expected, not a failure. For those, run the equivalent locally instead: lint + `go test` for the touched module.
2. For every failing or errored check: `gh run view <id> --log-failed` to get the actual error. Diagnose before touching anything.
3. Fix on the PR's branch, never on main:
   - Fetch and check out the branch in an isolated worktree (`git worktree add`), or EnterWorktree.
   - Make the fix, run the relevant tests and lint locally until green.
   - Commit with a message explaining the CI failure it fixes, and push to the PR branch.
   - If the failure is environmental (flaky runner, network timeout), re-run with `gh run rerun <id> --failed` instead of committing noise.
4. Stack maintenance: if a lower PR in a stack gained commits, check whether the PRs based on it now conflict (`mergeable` field); rebase the upper branches onto the updated lower branch and force-push **with `--force-with-lease`** — but only branches belonging to open PRs in this stack, and report exactly what was rebased.
5. Run `/sync-plan` so PLAN.md reflects any state changes.
6. Report: per PR — checks state, what was fixed/pushed/rebased, what still needs Varun (approvals, decisions, red checks you couldn't fix and why).

If a fix would change behaviour or tests in a way a reviewer must judge (not a mechanical CI fix), don't push it — describe it and leave the decision to Varun.

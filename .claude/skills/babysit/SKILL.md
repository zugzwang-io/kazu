---
name: babysit
description: Check every open PR's CI, diagnose and fix failures on their branches, keep stacked branches rebased, and sync PLAN.md. Use proactively whenever checks are failing or red, CI breaks after a push, the session brief reports failures, or the user says anything like "watch the PRs", "fix CI", "is the stack healthy", or "babysit". Never merges anything.
allowed-tools: Bash(gh *), Bash(git *), Bash(go *), Bash(gofmt *), Bash(golangci-lint *), Read, Edit, Write, Grep, Glob, Agent, Skill
---

Keep the open PRs healthy without the maintainer babysitting them. You may fix and push to PR branches; **you must never merge, close, or approve a PR, and never push to main — those are the maintainer's calls, always.** The division of labor: you make PRs green and tell the maintainer what needs a human decision; they decide.

## Steps

1. `gh pr list --state open --json number,title,baseRefName,headRefName,statusCheckRollup,mergeable`.
2. **Check for work already in flight** before touching anything: the repo has a cloud CI fixer (`claude-ci-fix.yml`) that reacts to failures. If a failing PR has a fresh fix commit or an explanatory comment from the Claude app, don't race it — fold its state into your report instead of pushing a competing fix.
3. For every failing or errored check: `gh run view <id> --log-failed` to get the actual error. Diagnose before changing code — a red check from a flaky runner needs `gh run rerun <id> --failed`, not a commit.
4. Fix on the PR's branch, never on main, and never in the maintainer's working tree — use an isolated worktree (`git worktree add` or EnterWorktree) so their uncommitted work is undisturbed:
   - Make the fix, run the relevant tests and lint locally until green.
   - Commit with a message naming the CI failure it fixes, push to the PR branch.
5. Stacked PRs whose base isn't main get no CI by design for review workflows — but lint and tests do run (pr.yml triggers on every PR). If a stacked PR shows no checks at all, run lint + `go test` locally for the touched module and report the result.
6. Stack maintenance: if a lower PR gained commits, check whether PRs based on it now conflict (`mergeable` field); rebase upward branches bottom-up and push with `--force-with-lease` — only branches belonging to open PRs in this stack — and report exactly what was rebased.
7. Run `/sync-plan` so PLAN.md reflects any state changes you caused.
8. Report per PR: checks state, what was fixed/pushed/rebased, and what still needs the maintainer (approvals, decisions, red checks you couldn't fix and why).

If a fix would change behaviour or test intent — something a reviewer must judge rather than a mechanical repair — don't push it. Describe the options and leave the decision to the maintainer.

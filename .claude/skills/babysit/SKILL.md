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
6. Stack maintenance: if a lower PR gained commits or merged, check whether PRs based on it now conflict (`mergeable` field), then restack bottom-up, one branch at a time, each in its own worktree (`rebase --update-refs` silently skips branches checked out in other worktrees, and every PR branch here has one). Only branches belonging to open PRs in this stack. For each branch:
   - Fast-forward it to `origin/<branch>` first, so a cloud CI-fixer commit isn't rewritten away, and record its tip as `<pre>`.
   - `<old base>` is the commit this branch was built on: the parent's `<pre>` if the parent was restacked in this run; `git merge-base <parent> <branch>` if the parent only gained commits; the parent's `headRefOid` (`gh pr view N --json headRefOid`) if it was squash-merged. `<new base>` is the parent's current tip, or `origin/main` if the parent merged. Check `git merge-base --is-ancestor <old base> <branch>`; if not, stop and report rather than replay someone else's commits.
   - `git -c rerere.enabled=true rebase --onto <new base> <old base>` (rerere replays conflict resolutions already made), then confirm `git log --oneline <new base>..<branch>` shows only this PR's commits.
   - Push with `--force-with-lease=<branch>:<pre>` and report exactly what was rebased.
7. Run `/sync-plan` on each PR branch you touched, in its worktree, so its own PLAN.md line is right; anything else it finds is reported, not edited.
8. Report per PR: checks state, what was fixed/pushed/rebased, and what still needs the maintainer (approvals, decisions, red checks you couldn't fix and why).

If a fix would change behaviour or test intent — something a reviewer must judge rather than a mechanical repair — don't push it. Describe the options and leave the decision to the maintainer.

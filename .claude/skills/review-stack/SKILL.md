---
name: review-stack
description: Review all open PRs (or the given PR numbers) in parallel, one subagent per PR, and synthesize a merge-order report. Use when the user wants the whole PR stack reviewed at once.
allowed-tools: Bash(gh *), Bash(git *), Read, Grep, Glob, Agent
---

Review every open PR in this repo in parallel and tell the user what to merge, in what order, and what needs changes first.

Open PRs right now:
!`gh pr list --state open --json number,title,headRefName,baseRefName --template '{{range .}}#{{.number}} {{.title}} ({{.headRefName}} -> {{.baseRefName}})
{{end}}'`

## Steps

1. If the user passed PR numbers as arguments, review only those; otherwise review all open PRs listed above.
2. Determine stack order from the base branches (a PR whose base is another PR's head branch sits on top of it).
3. Spawn one `kazu-pr-reviewer` agent per PR, **all in a single message** so they run concurrently. Give each agent its PR number and its actual base branch. For PRs with nontrivial code changes (not docs/config-only), also spawn two specialist lenses from the pr-review-toolkit plugin per PR, in the same message: `silent-failure-hunter` (swallowed errors, missing failure paths) and `pr-test-analyzer` (test coverage and quality).
4. When all reviewers return, synthesize — do not just concatenate:
   - A table: PR, verdict, blocking-finding count.
   - **Merge plan**: which PRs are mergeable now and in what order (respect the stack), which are blocked and on what.
   - Blocking findings in full (file:line, scenario); nits summarized.
   - Cross-PR issues the individual reviewers couldn't see (e.g. PR 3 relies on something PR 2 must change).
5. Do not post anything to GitHub unless the user explicitly asked; offer `--comment`-style posting as a follow-up.

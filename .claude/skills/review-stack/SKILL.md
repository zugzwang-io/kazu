---
name: review-stack
description: Review all open PRs (or the given PR numbers) in parallel — a reviewer fleet per PR — and synthesize a merge-order plan. Use whenever the user wants PRs reviewed or pre-reviewed, asks what is mergeable or in what order, says "review the stack / the queue / the open PRs", or wants help clearing a review backlog, even with no PR numbers named.
allowed-tools: Bash(gh *), Bash(git *), Read, Grep, Glob, Agent, Skill
---

Review every open PR in this repo in parallel and tell the maintainer what to merge, in what order, and what needs changes first. The goal is to turn N raw diffs into one decision-ready report — the maintainer reviews everything personally, so your synthesis is what makes that tractable.

Open PRs right now:
!`gh pr list --state open --json number,title,headRefName,baseRefName --template '{{range .}}#{{.number}} {{.title}} ({{.headRefName}} -> {{.baseRefName}})
{{end}}'`

## Steps

1. If the user passed PR numbers as arguments, review only those; otherwise review all open PRs listed above.
2. Determine stack order from the base branches (a PR whose base is another PR's head branch sits on top of it). Read the PR descriptions — stacked series usually say which PR carries the decisions.
3. Spawn the reviewers for every PR **in a single message** — one message means they run concurrently, so wall-clock time is the slowest PR, not the sum. Per PR:
   - One `kazu-pr-reviewer` agent, given the PR number and its actual base branch.
   - For PRs with nontrivial code changes (not docs/config-only), two specialist lenses from the pr-review-toolkit plugin: `silent-failure-hunter` (swallowed errors, missing failure paths — the bug class a resilience-testing tool must never ship) and `pr-test-analyzer` (test coverage and quality; missing tests are blocking per CLAUDE.md). Spawn both with `model: sonnet` — breadth lenses whose findings are re-verified in synthesis don't need the premium model, and a stack review multiplies them by every PR. If those plugin agents aren't available in this session, proceed with `kazu-pr-reviewer` alone and say so in the report.
4. When all reviewers return, synthesize — never concatenate raw agent output:
   - A table: PR, verdict, blocking-finding count.
   - **Merge plan**: which PRs are mergeable now and in what order (respect the stack), which are blocked and on what.
   - Blocking findings in full (file:line, concrete failure scenario); nits in one summarized list.
   - Cross-PR issues no single reviewer could see (e.g. PR 3 relies on something PR 2 must change; two PRs touching the same invariant differently).
   - Drop duplicate findings across lenses; keep the best-evidenced version.
5. Do not post anything to GitHub unless the user explicitly asked — the report is for the maintainer first. Offer posting as a follow-up.

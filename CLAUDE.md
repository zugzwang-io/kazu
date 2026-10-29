# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Kazu is a resilience regression testing tool (pre-alpha: the root binary is still `hello, world`). On every PR it injects seeded faults into a user's docker-compose system, runs checks, and compares head against base. The README describes where it's headed, not what works today.

## Two Go modules

- **Root** (`github.com/zugzwang-io/kazu`): the Kazu binary itself, `cmd/kazu`. Run with `go run ./cmd/kazu`.
- **`examples/shop`** (separate module on purpose — it must read like a customer repo and keep its dependencies out of Kazu's go.mod): a toy checkout system that is simultaneously the thing Kazu is built against, Kazu's test oracle, and the quickstart demo. Most real code currently lives here.

`examples/shop/SPEC.md` is the authority for everything in the shop: services, business rules, resilience mechanisms, bug flags, test-suite requirements, and delivery steps. Read it before changing shop code; behaviour decisions are recorded there as they're made.

## Commands

```sh
# Lint — CI runs golangci-lint v2 once per module with the root .golangci.yml
golangci-lint run                          # root module
(cd examples/shop && golangci-lint run)    # shop module

# Shop tests — tests needing Postgres start it themselves via testcontainers
# (internal/db/dbtest gives each test a fresh database), so Docker must be running
cd examples/shop && go test -race -count=1 ./...

# Single test
cd examples/shop && go test -race -run 'TestName' ./services/payments/

# Shop end-to-end smoke (what CI's shop-smoke job does)
cd examples/shop && docker compose up -d --build --wait checkout
# then: curl -X POST localhost:8080/orders -H 'Idempotency-Key: k1' -d '{"amount_cents": 1500}'
docker compose down -v
```

CI: `.github/workflows/pr.yml` (lint, matrix over both modules), `pr-shop.yml` (shop tests + compose smoke, only when shop files change).

## Shop architecture (the parts that span files)

- Order flow: `checkout` (:8080) → `payments` (:8081) → `fakestripe` (:12111), all backed by one Postgres database `shop` with **a schema per service**; each service connects with `search_path` set to its own schema and never names another service's tables. Each service owns `services/<name>/schema.sql`; the `migrate` one-shot compose job applies them all.
- Payment outcome semantics are the core invariant: 2xx → `paid`; 4xx or definitely-not-processed (refused/503-shed every attempt) → `failed`; unknown outcome (timeout/reset/other 5xx) → stays `pending` for checkout's reconciler, which re-sends with the **same idempotency key** every 5s. Payments dedups charges under a Postgres advisory lock on the key.
- Services read config from env vars via `internal/svc`; they find each other by compose service name. Bug flags (`SHOP_BUG_*`) are env vars read at startup so base and head run the same images with different variables.
- Ground rule from SPEC.md: every `SHOP_BUG_*` flag must pass the shop's own test suite (unit + integration). A bug the suite catches demonstrates nothing and is redesigned or dropped. Don't "fix" a Kazu miss with a shop-specific check.
- Integration tests in `examples/shop/integration/` run real services over HTTP with real Postgres, nothing mocked.

## Working agreements (for agents)

- **Claude writes the code; Varun reviews and decides.** Varun doesn't write code — PRs must be complete, tested, and reviewable on their own.
- **Never merge, approve, or close a PR, and never push to main.** Varun approves every merge himself — surface "ready to merge" as a recommendation, don't act on it. Pushing fixes to PR branches is allowed (see `/babysit`).
- **Follow Antithesis by default.** Where they've made a design decision for the same problem, take it. Deviations are deliberate, flagged **when proposed** (not after), and recorded in DESIGN.md §6.7.
- **YAGNI.** Don't add features, docs sections, or files nothing needs yet; a spec PR stays a spec.
- **Fail only on facts** (product principle, DESIGN §2): a missing signal — not exercised, no data, underpowered — is never a pass, and every failure has a config switch.
- **Independent review before "ready".** Every PR gets `kazu-pr-reviewer` dispatched on Opus — plus `silent-failure-hunter` and `pr-test-analyzer` on nontrivial code — all on Opus, with their findings fixed (or pushed back on with reasoning, never parked) and `kazu-pr-reviewer`'s verdict recorded in the PR description, before it is called ready. The reviewer's Sonnet default is for review fan-out (`/review-stack`, ad-hoc PR reviews); the CI review (`claude-code-review.yml`) is advisory.
- **PLAN.md is true on main by construction.** Each product PR marks its own bullet `[x] #N` and edits no line another open PR owns; setup-only PRs (agent instructions, repo tooling) get no line. Run `/sync-plan` after opening or updating a PR, and after merges to catch drift.
- **Decisions get ADRs** in `docs/adr/` (see ADR-0001). Agents draft them as Proposed; only Varun changes an ADR's status. DESIGN.md §11 is the pre-repo decision log; don't relitigate it without new evidence.
- **`DESIGN.md` (repo root) is the design authority for Kazu itself** — read the relevant section before config, runtime, or product decisions. SPEC.md remains the authority for the shop. Working from Claude's cloud sandbox? Read `docs/sandbox.md` first (blocked proxies, no Docker, REST-only gh).

## Conventions

- Lint config (root `.golangci.yml`, shared by both modules via directory walk-up) adds `bodyclose`, `noctx`, `errorlint`, `exhaustive`, `nolintlint` to the standard set — networked-code bugs. Every `//nolint` must name the linter.
- Keep the shop boring and small (target ~1,500 lines of Go): standard libraries configured plainly, no mechanisms beyond those in SPEC.md.
- Comments: code self-documenting, in-body comments brief (one line, only for non-obvious constraints). Package docs and function docstrings carry the verbose high-level context. If a body needs a paragraph, restructure instead.
- Code never cites docs: no DESIGN.md, ADR, SPEC.md, PLAN.md or other markdown references (or section numbers) in comments, docstrings, schema descriptions or test fixtures. State the fact itself; doc references go stale and the docs may not last.
- Work lands in small, stacked PRs (one service / one concern per PR). Varun reviews everything himself — keep each PR short and easy to review; propose a stack rather than one big diff.
- Code is not done until it is tested: behaviour changes ship with reproducible unit/integration tests in the same PR (seeded, no timing races; dbtest/testcontainers for integration). Reviews treat missing tests as blocking.
- Tests are load-bearing: before calling a test done, passing or ready for review, show it fails when the code it guards is broken — write it red-first, or mutate the code (revert the fix, shift a boundary, drop a branch) and restore. A test that passes either way doesn't count. Say how each new test was proven in the PR description.
- Coverage is read, not targeted: before opening or pushing a PR, list the uncovered lines in the changed code (`go test -coverprofile`); each gets a test or a one-line reason it's unreachable. Untested user-facing messages and fallback branches are where bugs hide. No percentage threshold.
- Code is not done until it is simple: before opening or pushing a PR with nontrivial code, run a simplification pass (built-in `/simplify`, or the pr-review-toolkit `code-simplifier` agent) so code lands self-documenting. Simplification findings in review are fixes to make, not points to debate.

## Feature lifecycle (superpowers)

Superpowers drives a feature from idea to PR; the rules above say what each phase must meet. The plugin is installed per user, not in the repo, so those rules stand on their own when it is absent.

| Phase (superpowers skill) | What it must meet here |
|---|---|
| Design (`brainstorming`) | Only for new features or design questions, not fixes, CI, or review follow-ups. DESIGN.md, SPEC.md and ADRs are the authorities; Antithesis by default, YAGNI. A written design (the architectural path) becomes a Proposed ADR or a DESIGN.md / SPEC.md edit, as its own PR at the bottom of the stack, never `docs/superpowers/`; spike and bounded designs stay in chat. |
| Plan (`writing-plans`) | One plan per PR, an uncommitted file outside the repo; a stack's plans are written together, so the stack is designed as a whole. Each product PR is one PLAN.md bullet (setup-only PRs have none), and every task belongs to exactly one PR's plan. |
| Build (`subagent-driven-development`, `test-driven-development`) | Execute subagent-driven, one plan at a time, each on its own branch stacked on the one below. Every implementer and fixer dispatch points at this file's Conventions (subagents load this file, but the skill's prompts don't direct them to it) and, for shop tasks, copies SPEC.md's ground rules into the task context. The pre-PR steps — simplification pass, coverage read, PR description — belong to Finish, not to tasks; a test's red-first or mutation proof goes in the task report's TDD evidence. Tests as in Conventions: red-first or proven by mutation both count, so never delete working code just to retype it test-first. |
| Per-task review (`subagent-driven-development`) | The skill's per-task reviewers stay, with their own output format. Each task review is pointed at items 1–4 of "What to check" in `.claude/agents/kazu-pr-reviewer.md` (correctness, SPEC.md conformance, lint conventions, tests), with its "blocking" read as Important or higher and "nit" as Minor; for shop tasks the relevant SPEC.md rules go into the reviewer's global constraints, so it needn't read beyond the diff. Tests a later task in the plan adds come back ⚠️, not Important. Reviewability (PR size and splits) is judged at the final review; re-reviews apply the list to the fix diff only. |
| Debug (`systematic-debugging`) | As written. |
| Finish (`finishing-a-development-branch`) | Skip `subagent-driven-development`'s own final review and keep its workspace (`.superpowers/sdd/<plan>/`, not the git worktree). Simplification pass and coverage read, then push and open the PR (never a local merge), then `/sync-plan` marks the PR's line, if it has one, and the branch is pushed again. |
| Final review (replaces `requesting-code-review` and the plan's final review) | The pre-"ready" review in Working agreements, run once the PR exists. Give the reviewers the workspace ledger's deferred and parked lines. Working agreements win over the skill's single fix wave: blocking findings are fixed or pushed back on, never parked. Record the verdict and the skill's rulings in the PR description, then delete the SDD workspace; the git worktree stays for review feedback. |
| Review feedback (`receiving-code-review`) | Push back with reasoning when a finding is wrong; Varun decides. |

Models — always name one when dispatching a subagent:
- Code-writing subagents (implementers, fix-loop and final-review fixers): Sonnet for well-specified work, even when the plan contains the complete code; Opus for judgement (multi-file integration, debugging); never Haiku, which takes more turns and costs more. Fix rounds 4–5 escalate Sonnet → Opus; a stuck Opus implementer gets a fresh Opus with the report.
- Per-task reviewers and re-reviewers: Sonnet; Opus when the task's implementer needed Opus.
- Design and architecture subagents: Fable. The final review is not one of these; it runs on Opus per Working agreements.

Outside the loop:
- `/verify-foundations` and `/arch-review` run on demand, on Fable at max effort. The loop suggests them, never runs them: when a design produces an ADR, on a foundational PR (schema, protocol, the first of a stack), and when a PLAN.md phase completes.
- `/review-stack` and `/babysit` work the PR queue, not a single feature; `/sync-plan` runs in Finish and after merges.

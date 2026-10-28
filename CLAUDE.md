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
- **PLAN.md tracks phases against real PR state.** After any PR is opened, updated, merged, or closed — including by you — run `/sync-plan`.
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

## Plugin workflow (superpowers)

Superpowers is installed per user, not in the repo, so the rules above stand on their own; these lines only adapt its skills to this repo.

- Brainstorm only for new features or design questions, not fixes, CI, or review follow-ups. Brainstorm output becomes a Proposed ADR or a DESIGN.md edit — never `docs/superpowers/`.
- Implementation plans from `writing-plans` live in the scratchpad or the PR description, not committed. PLAN.md stays the only committed plan.
- `finishing-a-development-branch`: always push and open a PR; never offer a local merge.
- Debugging goes through `systematic-debugging`; new behaviour through `test-driven-development`.
- When a superpowers skill dispatches a code reviewer, use `kazu-pr-reviewer`, not a general-purpose agent.
- Review: built-in `/code-review` before "ready"; `/review-stack` for the queue; `/verify-foundations` for foundational PRs.

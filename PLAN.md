# Kazu build plan

Phased plan toward the README's vision. **Maintained automatically**: agents run `/sync-plan` whenever PR state changes (opened, updated, merged, closed); humans edit the phase structure, agents keep statuses true. Status comes from real PR/merge state, never aspiration.

Legend: `[x]` merged · `[~]` in review · `[ ]` not started

## Phase 0 — Spec and CI foundations

- [x] Shop spec and ground-truth matrix (#1, #3)
- [x] PR workflows split by trigger, repo-wide lint (#8, #10)
- [x] #18 CI on every PR (stacked PRs get checks; shop jobs always report so they can be required)
- [x] #19 Claude Code setup committed: CLAUDE.md, this plan, ADRs, agents, skills, hooks
- [x] Ruleset step 1: require lint (.) + lint (examples/shop) (2026-10-14)
- [x] #20 merge-cleanup hook: auto-delete merged branches/worktrees, pull main
- [x] #21 Claude GitHub App: @claude responder + auto-review workflows
- [x] #22 reviews scoped to PRs into main; CI-failure fixer workflow
- [x] #23 test job for the root module (extracted from #13 so the ruleset needn't wait on config review)
- [ ] Ruleset step 2: add test, shop test, shop smoke to required checks — **unblocked (#23 merged), awaiting the maintainer’s go**
- [x] #24 wire plugin reviewers into skills; simplification joins definition-of-done
- [~] #25 skill-creator pass over .claude/skills/ (triggering, rationale, operational lessons)
- [ ] Pre-public wording pass: generalize "Varun" → "the maintainer" in .claude/ skills; decide what stays repo-public

## Phase 1 — Shop, build step 1 (the system Kazu is built against)

- [x] fakestripe (#4)
- [x] payments, with load shedding (#5)
- [x] checkout, schema per service, integration suite (#6)
- [x] compose: the sealed system end to end (#7)
- [ ] `load/traffic.js` k6 template
- [ ] `PAYMENTS_BY_IP` trap with the e2e gate
      (both blocked on Kazu's runtime — see Phase 3; rows V0, S1–S3 land with them)

## Phase 2 — kazu.yaml config (current focus)

The stack under review. Only #13 targets main, so CI runs on it alone; the rest get CI as the stack merges down.

- [~] #13 config 1/5: JSON schema for kazu.yaml — **the one to agree on**; decisions recorded in [ADR-0002](docs/adr/0002-kazu-yaml-schema-shape.md)
- [~] #14 config 2/5: Go types for kazu.yaml
- [~] #15 config 3/5: decode kazu.yaml into Config
- [~] #16 config 4/5: readable errors with positions
- [~] #17 config 5/5: rules that relate one key to another

## Phase 3 — Runtime: run one scenario (unrefined)

From the README: compose driver (fresh system per trial), fault proxy on declared edges, traffic container lifecycle, built-in checks (crashes, error-rate, latency, recovery), base-vs-head comparison, run records and `kazu replay`. Refine into PRs when Phase 2 lands.

## Phase 4 — Shop build steps 2–4 + SDK checks (unrefined)

SPEC.md delivery steps 2–4: `kazu.yaml` + `checks/shop.py`, fraud (gRPC), bug flags and bug rows V1+, worker/rabbitmq/otel, mechanics rows, K3s driver.

---

## Open questions / blocked

- DESIGN.md is referenced throughout (§4.1–§6.8) but not in this repo — agents: don't guess its contents; flag when a decision needs it.

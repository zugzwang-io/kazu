# Kazu build plan

Phased plan toward DESIGN.md's build order (§8). **Maintained automatically**: agents run `/sync-plan` whenever PR state changes (opened, updated, merged, closed); humans edit the phase structure, agents keep statuses true. Status comes from real PR/merge state, never aspiration.

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
- [x] Ruleset step 2: all five checks required on main (2026-10-15); gate complete
- [x] #24 wire plugin reviewers into skills; simplification joins definition-of-done
- [~] #25 skill-creator pass over .claude/skills/ (triggering, rationale, operational lessons)
- [~] DESIGN.md folded into the repo from the claude.ai project (2026-10-15); stale branches from the old plan deleted
- [ ] Pre-public wording pass: generalize "Varun" → "the maintainer" in .claude/ skills; decide what stays repo-public
- [ ] Before open-source launch: USPTO search for "Kazu" (classes 9 and 42); check a Zugzwang domain; publish the defensive package claims (DESIGN §1)

## Phase 1 — Shop, build step 1 (the system Kazu is built against)

- [x] fakestripe (#4)
- [x] payments, with load shedding (#5)
- [x] checkout, schema per service, integration suite (#6)
- [x] compose: the sealed system end to end (#7)
- [ ] `load/traffic.js` k6 template
- [ ] `PAYMENTS_BY_IP` trap with the e2e gate
      (both blocked on Kazu's runtime — see Phase 3; rows V0, S1–S3 land with them)

## Phase 2 — kazu.yaml config (parked pending platform design)

The stack under review. **Deliberately unmerged**: Varun intends to design the platform and work backwards before committing to the config contract; re-examine [ADR-0002](docs/adr/0002-kazu-yaml-schema-shape.md) against that design before merging #13.

- [~] #13 config 1/5: JSON schema for kazu.yaml — **the one to agree on**; decisions recorded in ADR-0002
- [~] #14 config 2/5: Go types for kazu.yaml
- [~] #15 config 3/5: decode kazu.yaml into Config
- [~] #16 config 4/5: readable errors with positions (works around a jsonschema v6.0.3 propertyNames location bug)
- [~] #17 config 5/5: rules that relate one key to another (rejects row S3)

## Phase 3 — Walking skeleton: `kazu run` end to end (DESIGN §8 step 1)

Contract first, each layer stacked, with its "done when":

- [ ] **Proxy**: TCP proxy with `latency` and a `down` that cuts live connections, plus traffic counters — done when proxy tests against an echo server pass
- [ ] **Driver**: compose driver — a proxy per declared edge, per-caller hosts aliases (`extra_hosts`), the fixed lifecycle, traffic start, `KAZU_DURATION`, `--base`/`--head` variables; `kazu run` loads the config — done when `kazu run` drives one shop trial end to end
- [ ] **Verdicts**: landed check, `kazu-python` SDK image with `@after` over stdio, terminal verdict, `--json` — done when the first real verdict lands on the shop
- [ ] **E2E gate**: the customer CI workflow plus a separate Go asserter on every Kazu PR; the shop's `PAYMENTS_BY_IP` trap — done when rows V0 and S1–S3 gate merges

## Phase 4 — Trustworthy, then gate + launch (DESIGN §8 steps 2–3; shop step 2)

Seeds, HTTP/gRPC proxy with request-count triggers, default checks from container events, `@eventually`, run records, `kazu replay`; then trial pairs, the statistical verdict, `no_regression`, shards + `merge`, GitHub Action, PR comments, open-source launch. Shop step 2 lands alongside: `kazu.yaml` + `checks/shop.py`, fraud (gRPC), first bug flags and rows V1–V2. Refine into PRs when Phase 3 is underway.

## Phase 5 — Easy and everywhere, and beyond (DESIGN §8 steps 4–7, unrefined)

K3s driver, OTLP receiver + PromQL checks, `init`/`doctor`, remaining SDKs and sinks; smart parallelism; paid v1. Refine later.

---

## Open decisions (carried from the pre-repo plan, 2026-10-11)

- **Platform design exercise (Varun, soon)**: design the platform and work backwards; the config stack and ADR-0002 wait on it.
- **Latency fault — delay request, response, or both?** Claude's proposal: both directions per chunk, like real network latency. Decide in the proxy PR; record in SPEC.md.
- **Row S1 uses `RETRY_NEW_KEY`, a step-2 flag, but gates in step 1**: swap in any head-differs-from-base, or move S1 to step 2.
- **Spikes before committing** (DESIGN §8): the verdict's statistical method, on the shop's race flag and repeated baselines, before Phase 4's stats work; K3s on GitHub-hosted runners before the K3s driver.

Resolved since the old plan: CI on stacked PRs (#18 widened triggers); the nine schema proposals (ADR-0002, pending acceptance with #13).

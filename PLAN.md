# Kazu build plan

Phased plan toward DESIGN.md's build order (§8). **Maintained automatically**: agents run `/sync-plan` whenever PR state changes (opened, updated, merged, closed); humans edit the phase structure, agents keep statuses true. Status comes from real PR/merge state, never aspiration.

Legend: `[x]` merged · `[~]` in review · `[ ]` not started

Every bullet from Phase 3 on is **one PR**: small enough to review in one sitting, with its tests, stacked on the bullet above it unless noted. A bullet that turns out to need two PRs is split in the plan first.

## v1 scope

**v1 is Phases 3–6.** Done when a team with a docker-compose system and GitHub Actions (or any CI, via shards) can gate PRs on resilience regressions without help from us. The open-source launch happens at the end of Phase 5 as v0.x; v1.0 is tagged at the end of Phase 6.

| In v1 | Deferred, and where |
|---|---|
| Compose driver; sealed, fresh system per trial | Kubernetes (K3s) driver → Phase 7 (v1.1) |
| Faults: `latency`, `down`, `blackhole`, `reset`, `bandwidth`, `errors`, `timeout`, `crash`, `pause`, `cpu`, `memory`, `disk_full`; wall-clock and request-count triggers | Kernel-level partitions, in-code SDK fault points → not planned |
| Default checks: setup facts, crashes, exits, traffic exit, landed, no-data; regression defaults on user-facing edges; opt-in language log checks | Traces as evidence → Phase 10 |
| Checks: Python, TypeScript and Go SDK images with `@after`/`@eventually`; one-line `sql`, `http`, `recovers`, `no_regression`, `slo`, `metric` | Java/Kotlin, Ruby SDKs → community, after v1 |
| OTLP receiver, per-trial metric store, PromQL | — |
| Verdict: trial pairs, per-run noise, adaptive trials, underpowered, flaky, false-discovery control | History-informed starting trial count → Phase 8 |
| Static shards + `merge`; GitHub Action; PR comment; JUnit/Markdown/JSON; OTLP/S3/webhook sinks | Coordinator, dynamic workers, skip-unchanged, Kubernetes runner → Phase 8 (v1.2) |
| Run records, `replay`, `explain`, `diff`; `init` (+ CI templates for five CIs), `doctor`, `auto_scenarios` | — |
| GoReleaser: binaries, brew, Docker image, installer; npm + PyPI `kazu-cli`; SDK images on GHCR | Hosted lanes → Phase 10 |
| Terminal, PR comment and Markdown summary as the only UIs | Web UI, control plane, consumer contracts, AI → Phase 9 (paid v1) |

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
- [x] #25 skill-creator pass over .claude/skills/ (triggering, rationale, operational lessons)
- [x] #26 DESIGN.md folded into the repo from the claude.ai project; 15 stale branches deleted (2026-10-15)
- [x] #27 Platform plan: every DESIGN §8 step refined into single-PR bullets, v1 scope, frontend sketch
- [x] #28 model tiering for agent flows: reviewers on sonnet, mechanical sync on haiku, verify-foundations stays premium
- [x] #29 CLAUDE.md: code never cites markdown docs (comments, docstrings, schema descriptions, fixtures)
- [ ] Pre-public pass: generalize "Varun" → "the maintainer" in .claude/ skills; decide DESIGN.md's public scope (§7 pricing, §9 risks, §1 other Zugzwang project names — keep public or split to a private home)
- [ ] Before open-source launch: USPTO search for "Kazu" (classes 9 and 42); check a Zugzwang domain; publish the defensive package claims (DESIGN §1)

## Phase 1 — Shop, build step 1 (the system Kazu is built against) ✅

- [x] fakestripe (#4)
- [x] payments, with load shedding (#5)
- [x] checkout, schema per service, integration suite (#6)
- [x] compose: the sealed system end to end (#7)

The k6 template and the `PAYMENTS_BY_IP` trap were listed here but need Kazu's runtime; they are Phase 3 bullets now.

## Phase 2 — kazu.yaml config ✅

Merged 2026-10-16; ADR-0002 and ADR-0004 accepted. Held until this plan's frontend sketch had been worked backwards to the config (see "Frontend sketch", below): the screens need nothing `kazu.yaml` doesn't already carry, so [ADR-0002](docs/adr/0002-kazu-yaml-schema-shape.md) stands as proposed and the stack can be reviewed on its own merits. Reworked in review (2026-10-15): the schema drops `telemetry`, `auto_scenarios`, `defaults` and `gate` until they're built, keeps every other key at its intended shape, adds scenario `checks` and relative triggers ([ADR-0004](docs/adr/0004-relative-fault-triggers.md)), and `sql` checks pass on zero rows. The not-supported-yet list was dropped in #17 (YAGNI); a Phase 5 launch bullet trims the schema to what's built.

- [x] #13 config 1/5: JSON schema for kazu.yaml — **the one to agree on**; decisions recorded in ADR-0002 and ADR-0004
- [x] #14 config 2/5: Go types for kazu.yaml
- [x] #15 config 3/5: decode kazu.yaml into Config
- [x] #16 config 4/5: readable errors with positions (works around a jsonschema v6.0.3 propertyNames location bug)
- [x] #17 config 5/5: rules that relate one key to another (rejects row S3) and anchor rules

## Phase 3 — Walking skeleton: `kazu run` end to end (DESIGN §8 step 1)

Done when `kazu run` drives the shop's `checkout → payments → postgres` through one scenario, base and head, and rows V0, S1–S3 and S14 gate Kazu's own PRs. S14 expects REGRESSED, so the gate catches a Kazu that passes everything, not only one that fails everything.

Order (2026-10-16 foundations review): the networking spike first, then the thinnest slice that gates the rows: proxy forwarder and `down`, gRPC control, driver, controller with the landed check, the one-line `sql` check run by the host, verdict and `--json`. `latency`, the checks SDK and `@after` follow. The shop fixes below are independent and go first, because they change how the shop behaves under the faults the slice injects.

Networking (derisk first):

- [ ] spike → ADR-0005: reach the trial network from the host. On a GitHub-hosted Linux runner and on macOS, bring the shop up with one Kazu-injected proxy and an `extra_hosts` alias on its caller, on a Kazu-allocated subnet with a static proxy IP and a sealed (`internal`) network; from the host, read the proxy's counters and run a SQL query against `postgres`. Try both options (published ports with URL rewriting; one in-network helper container driven from the host) and record the choice

Proxy (`kazu proxy`, one container per declared edge; tests run against an in-process echo server):

- [ ] proxy 1/5: TCP forwarder with connection and byte counters; `kazu proxy` subcommand
- [ ] proxy 2/5: `latency` fault — delay plus jitter per chunk in both directions, seeded; records the open latency decision in SPEC.md
- [ ] proxy 3/5: `down` fault — refuses new connections (ECONNREFUSED, never accept-then-close) and resets live ones for its window, so pooled connections don't ride through; echo-server tests assert both errnos, because the shop maps refused to `failed` and reset to `pending`
- [ ] proxy 4/5: fault program — `after`/`for` on a clock the controller starts; applied-fault counters for the landed check
- [ ] proxy 5/5: `kazu.proxy.v1` Protobuf + gRPC control service (load program, start clock, read counters); `buf` set up for the repo (the checks protocol reuses it later)
- [ ] image: Dockerfile for the `kazu` image; the driver runs proxies from the image matching its own version (GHCR publishing is Phase 5)

Driver (compose):

- [ ] driver 1/4: load the system with compose-go — services, healthchecks, one-shot jobs (`service_completed_successfully`), `--base`/`--head` variable interpolation
- [ ] driver 2/4: project rewrite — a proxy service per declared edge, `extra_hosts` on the caller only, a project name per trial
- [ ] driver 3/4: lifecycle — up, ready (every healthcheck passing, every one-shot exited 0, with a timeout that is an error), start traffic with `KAZU_SEED`/`KAZU_DURATION`/scenario `env`, wait for exit, delete
- [ ] driver 4/4: trial plan — scenarios × {base, head}; `KAZU_DURATION` from the last fault plus recovery window plus margin, `duration:` override
- [ ] controller: pushes programs to proxies, starts their clocks at traffic start, collects counters; the landed check turns a fault that never applied into **not exercised** with its reason

Checks:

- [ ] checks 1/4: `sql:` one-line check run by the host through `connections:` (no SDK): passes when the query returns no rows; reads only the first few rows, under a statement timeout. On the slice
- [ ] checks 2/4: `kazu.sdk.v1` Protobuf — `initialize`, `list`, `check`, `result` and the `sql` callback, which caps the rows it returns and fails loudly past the cap; JSON over stdio
- [ ] checks 3/4: Python `kazu_sdk` (`after`, `run.sql`, `run.sql_value`) and the `kazu-python` image; check files found by extension in mounted folders
- [ ] checks 4/4: check host — runs the checks container per trial over stdio, answers `sql` through the same connection path; a check that errors on base is **incompatible with base**

Verdict and CLI:

- [ ] verdict: per scenario, a check that passes on base and fails on head is REGRESSED, a violation on both is a pass; terminal output (§4.8 layout, pass/REGRESSED/not exercised only); `--json` result with per-edge proxy counters and a defined result for a config error (S2 and S3 assert on them)
- [ ] cli: `kazu run [scenario] [--suite] --base --head [--seed] [--json]` wired through config → plan → driver → controller → checks → verdict; one shop trial end to end

Shop and the e2e gate:

- [ ] templates: `templates/k6/traffic.js` (DESIGN §4.3) and the shop's `load/traffic.js` edited from it
- [ ] shop: post-charge writes get a deadline (payments' charge row, checkout's order resolve): a detached context keeps no deadline, so a blackholed database hangs them, fills payments' in-flight limit and turns into mass `failed` orders
- [ ] shop: a test for a connection reset mid-request (→ `pending`); integration tests open one pool per service with production settings and `search_path`, not one shared pool
- [ ] shop: hygiene — `attemptResult`'s zero value is not `paid`; `svc.EnvInt` rejects values below 1; attempt errors are logged; shared `/healthz` and DB setup in `internal/svc`
- [ ] shop: step-1 `kazu.yaml` with a liveness check (some order got paid, so zero-row checks can't pass on no traffic), `safety/` configs for S1–S3 and S14, `expectations.yaml` with V0, S1–S3 and S14
- [ ] shop: `SHOP_TRAP_PAYMENTS_BY_IP`, with the plain Go test showing the alias is bypassed
- [ ] e2e gate: workflow that builds the binary and SDK image and runs the shop as a customer would; a Go asserter (not Kazu) compares each row's JSON to `expectations.yaml` and requires nonzero user-facing edge counters on V0 and S2; rows V0, S1–S3 and S14 become required checks

## Phase 4 — Trustworthy (DESIGN §8 step 2)

Done when a verdict can be reproduced from its record (`kazu replay`) and the default setup and correctness checks come from container events.

- [ ] seeds: `--seed`, printed and recorded; the `(seed, edge, request number)` PRNG helper every probabilistic fault uses
- [ ] proxy: HTTP mode — request/response framing, per-request latency histogram, status classes, request counter per edge
- [ ] proxy: `after_requests` triggers on `http` edges, with the runtime guard that mirrors config 5/5
- [ ] proxy: `errors` (rate, code) and `timeout` (rate) faults on HTTP edges
- [ ] proxy: gRPC mode — HTTP/2 framing, `grpc-status` trailers, `errors` with gRPC codes
- [ ] proxy: `blackhole`, `reset`, `bandwidth`
- [ ] events: Docker event and exit watcher → default checks: ready, no unexpected crash (signal), no unexpected exit (non-zero, OOM, crash loop), traffic exited 0; one-shot jobs exempt
- [ ] process faults: `crash` (kill in place, `restart_after`) and `pause`, recorded as Kazu's own so the crash default excludes them
- [ ] checks: `@eventually(within=…)` in the SDK and the host's retry loop
- [ ] checks: `http:` one-line check and the `http` SDK callback
- [ ] plan: resolve fault anchors (`from`, joins) into times (ADR-0004)
- [ ] faults on a service: network faults on every declared inbound edge of the service
- [ ] scenarios: a scenario's `traffic:` override, merged over the default traffic
- [ ] run record: JSON schema with no product name in it; `.kazu/runs/<id>` with `r_xxxx` ids; plan + per-trial results + proxy counters; `--json` writes the record; one-line checks get stable ids (see "Frontend sketch")
- [ ] `kazu replay <run-id> [--trial N]`
- [ ] shop: `fraud` gRPC service, payments calls it for orders ≥ 1,000 cents (business rule 6), integration tests
- [ ] shop: full `kazu.yaml` and `checks/shop.py`; scenarios Kazu can't run yet marked skipped in `expectations.yaml`
- [ ] shop: CI runs the test suite once per bug flag (ground rule 1 enforced mechanically)
- [ ] shop: redesign or replace `RETRY_NEW_KEY` (disqualified as drafted); row V1 recorded
- [ ] shop: redesign or replace `TIMEOUT_AS_FAILURE` (disqualified as drafted); row V2 recorded; quickstart command updated

## Phase 5 — Gate, then open-source launch (DESIGN §8 step 3)

Done when the shop's rows gate Kazu's PRs through the same GitHub Action a customer uses, and v0.x is public.

- [ ] spike → ADR: the statistical method, validated on the shop's race flag and repeated baselines — test, minimum pairs, budget, default tolerances as policy (DESIGN §8 spike, §10)
- [ ] verdict: trial pairs — same seed, base and head back to back; fixed `gate.trials: N` (adds `gate` to the schema); pair results in the record
- [ ] verdict: paired comparison with the per-run noise estimate; **underpowered** when the budget can't resolve the tolerance
- [ ] verdict: adaptive trials (`trials: auto`) — stop early when decided, add pairs while undecided, up to the budget
- [ ] checks: `no_regression`, `slo`, `recovers` one-line checks over proxy measures (`p50/p90/p95/p99/error_rate`)
- [ ] defaults: regression defaults (`latency_within`, `error_rate_within`, `recovers_within`) on user-facing edges; `defaults.off` (adds `defaults` to the schema)
- [ ] verdict: flaky classification; `gate.fail_on`, `gate.flaky`, `gate.not_exercised`, `gate.no_data`, `gate.underpowered` honoured end to end
- [ ] verdict: false-discovery control across many scenarios and checks
- [ ] output: `--junit` and `--markdown` sinks from the record
- [ ] shards: `--shard i/N` (deterministic partition of trial pairs) and `kazu merge <results…>`
- [ ] GitHub Action: `action.yml` in the repo — install a pinned binary, run, upload the record
- [ ] GitHub Action: PR comment (§4.8 condensed) and status check
- [ ] release: GoReleaser — binaries, Homebrew tap, Docker image, `curl | sh` installer; SDK images to GHCR on tag
- [ ] e2e gate switches to the Action, sharded across ~10 jobs, same ~10-minute budget as a customer's gate
- [ ] shop: `NO_DEADLINE_PROPAGATION` and `SHARED_POOL` flags with bug proofs; rows V3, V6
- [ ] shop: `RACE_P` and `NO_SHEDDING` flags with bug proofs; rows V9, V10
- [ ] shop: rows S4–S6 (regression not violation, flaky, statistical power) in `safety/`
- [ ] shop: `SCHEMA_V2` trap; rows S7 (20 repeats as 20 shards), S8, S9
- [ ] docs: GitHub Pages at `zugzwang-io.github.io/kazu` — install, quickstart on the shop, reference, schema served at its `$id`
- [ ] launch: README rewrite, the Phase 0 pre-public pass, package claims (`kazu-cli` on npm and PyPI, placeholders on crates.io), trademark check, v0.x tag
- [ ] launch: the schema holds only what Kazu implements; anything still unbuilt leaves `kazu.schema.json` before the v0.x tag (ADR-0002, decision 12)

## Phase 6 — Easy and everywhere (DESIGN §8 step 4) → v1.0

Done when Priya's Day 0 and Day 1 (DESIGN §3) work as written: `init`, `doctor`, a verdict from defaults alone, metric checks over her existing telemetry, three SDK languages.

- [ ] telemetry 1/4: OTLP receiver (gRPC and HTTP) as a service in the trial network; `OTEL_EXPORTER_OTLP_ENDPOINT` set on every service, or `telemetry.collector` aliased to it (adds `telemetry` to the schema)
- [ ] telemetry 2/4: OTLP → Prometheus naming and a per-trial in-memory TSDB; proxy counters written to the same store (ADR for the name mapping, DESIGN §10)
- [ ] telemetry 3/4: PromQL engine; `metric:` check with `max`/`min`/`within`; **no data** result and `no_data: warn`
- [ ] telemetry 4/4: `run.metrics()` SDK callback
- [ ] defaults: opt-in language log checks (`defaults.languages`: go, java, rust)
- [ ] faults: `cpu`, `memory`, `disk_full`
- [ ] scenarios: `auto_scenarios` (crash each service, take each edge down) and `auto` in suites (adds `auto_scenarios` to the schema)
- [ ] `kazu init`: drafts `kazu.yaml` from the compose file (candidate edges from env values, the collector it finds) and writes the k6 template
- [ ] `kazu init --ci github`: writes the customer workflow (the e2e gate's workflow becomes its output)
- [ ] `kazu init --ci` templates for GitLab, CircleCI, Buildkite, Jenkins
- [ ] `kazu doctor`: per edge which faults apply (TLS edges flagged), undeclared callers, unsealed services, services that sent telemetry, whether user-facing edges exist
- [ ] SDK: TypeScript `kazu-sdk` on npm and the `kazu-node` image, generated from the same Protobuf schema
- [ ] SDK: Go `sdk/go` and the `kazu-go` image
- [ ] sinks: `--otlp`, `--s3`, `--webhook`
- [ ] distribution: `kazu-cli` on npm (per-platform optional dependencies) and PyPI (per-platform wheels)
- [ ] `kazu explain <run-id>` and `kazu diff <run-a> <run-b>` over local records
- [ ] shop: `worker` and `rabbitmq` — ledger entries, dead-letter queue, `parked` (business rule 5), unit and integration tests
- [ ] shop: `otel-collector` and the SPEC telemetry table; `NO_OTEL` trap; row S10
- [ ] shop: `UNBOUNDED_RETRY_QUEUE` and `RETRY_NO_BACKOFF` flags with bug proofs; rows V4, V5
- [ ] shop: `POOL_LEAK` and `NO_RECONNECT` flags with bug proofs; rows V7, V8, S11
- [ ] shop: `ACK_BEFORE_WRITE` and `REQUEUE_FOREVER` flags with bug proofs; rows V11, V12
- [ ] v1.0 tag; DESIGN §3 journey re-walked on the shop and a second system

## Phase 7 — Kubernetes target (v1.1)

K3s moves out of DESIGN §8 step 4 so v1.0 ships compose-only ([ADR-0003](docs/adr/0003-v1-is-compose-only.md)). The driver interface from Phase 3 is the seam; nothing above it changes.

- [ ] spike → ADR: K3s in Docker on GitHub-hosted runners — boot time, memory, privileges; decides whether K3s rows gate PRs or need a self-hosted runner
- [ ] driver: K3s cluster per runner, namespace per trial, rendered manifests, readiness probes as ready, Jobs as one-shots
- [ ] driver: `hostAliases` per caller (short and cluster forms), proxies as pods
- [ ] faults: `crash` in place via the kubelet, `pause`, resource faults on pods
- [ ] shop: `k8s/rendered/`; rows S12, S13

## Phase 8 — Smart parallelism (v1.2; DESIGN §8 step 5)

- [ ] plan: content-hashed plan (`plan_hash` per scenario); identical base/head hashes skip the scenario
- [ ] coordinator protocol: `kazu.coord.v1` Connect — lease a trial pair, expiry, report results
- [ ] `kazu coordinator`: self-hosted, single process, state in SQLite
- [ ] `kazu worker --coordinator <addr>`
- [ ] verdict: history-informed starting trial count from stored records (never the verdict)
- [ ] driver: base image cache across trials on one runner
- [ ] Kubernetes runner: Helm chart and Job template for workers

## Phase 9 — Control plane: paid v1 (DESIGN §8 step 6)

The control plane lives in a private repo (`zugzwang-io/kazu-cloud`, DESIGN §11) that imports this one's shared packages. Public-repo PRs first; the private work is listed at coarser grain and refined there.

Public repo:

- [ ] ADR + PR: shared packages (plan hash, run record, leasing) move from `internal/` to importable paths
- [ ] `kazu.cloud.v1` Connect API schema: policies and contracts before a run, records after
- [ ] `kazu run --cloud`: token auth, record upload, policy fetch; falls back to local gating when the control plane is unreachable

Private repo:

- [ ] control plane skeleton: Postgres (pgx, sqlc, goose), River jobs, Connect handlers, orgs/repos/runs
- [ ] org-wide GitHub App: install flow, checks and comments, cross-repo triggers
- [ ] web UI: run explorer (templ + htmx) — the "Run" screen in the frontend sketch
- [ ] web UI: repo overview — scenario × commit history, flake quarantine, ownership routing
- [ ] consumer resilience contracts: consumer registry, provider PRs run consumers' invariants, the "Contracts" screen
- [ ] billing: Stripe, Pro tier, metering for AI calls
- [ ] AI: incident → scenario draft
- [ ] AI: PR coverage review and suggested invariants

## Phase 10 — Later (DESIGN §8 step 7, unrefined)

Traces as evidence (same receiver, span-structure checks); org registry of standard suites; policy-as-code and waivers with expiry and audit; coverage map; hosted lanes (Firecracker spike first); SSO/SCIM, RBAC, audit export, self-hosted control plane. Refine when Phase 9 is underway.

---

## Frontend sketch (the design-backwards exercise)

Five screens on one canvas: [Kazu platform screens](https://claude.ai/artifact/2Xt7QZ2ybq8PJyRUKJfwdf) (private to the maintainer's account until shared).

| Screen | Tier | Phase |
|---|---|---|
| Terminal: `kazu run` and `kazu doctor` output | free, CLI | 3–6 |
| PR comment with status check | free, GitHub Action | 5 |
| Run explorer: one run, scenarios, trial pairs, fault timeline, noise, reproduce | paid web UI | 9 |
| Repo overview: scenario × commit history, flake quarantine, coverage | paid web UI | 9 |
| Contracts: provider → consumers, whose invariants run on whose PRs | paid web UI | 9 |

What working backwards from the screens changed or confirmed:

- **The run record is the contract every surface renders.** Terminal, Markdown, PR comment, `explain` and the web UI are projections of one JSON record, so the record (Phase 4) is designed first and the renderers are thin. Nothing a screen shows is computed outside the record.
- **`kazu.yaml` needs nothing new.** Every screen's fields come from the config as ADR-0002 shapes it plus run-time facts (image digests, counters, trial results). The config stack can be reviewed as is.
- **One-line checks need stable identities.** History and contract views key on `(scenario, check)` across commits; naming an unnamed check by its position breaks when the list is reordered. Decide in the run-record PR: a content-derived id, with `name:` recommended in the templates.
- **"Changed since base" is derivable.** The PR comment names the services whose images differ between base and head; the plan already resolves both to digests, so no extra config.
- **No free web UI in v1.** The screens confirm the terminal plus PR comment carry everything a developer needs; an HTML report is not added (YAGNI, DESIGN §11).

---

## Open decisions

- **Latency fault — delay request, response, or both?** Claude's proposal: both directions per chunk, like real network latency. Decide in proxy 2/5; record in SPEC.md.
- **Row S1 uses `RETRY_NEW_KEY`, a step-2 flag, but gates in Phase 3**: use any head-differs-from-base variable for S1 in Phase 3, or move S1 to Phase 4.
- **Spikes before committing** (DESIGN §8): the verdict method before Phase 5's stats work; K3s on GitHub-hosted runners before Phase 7.

Resolved since the old plan: CI on stacked PRs (#18); the schema decisions (ADR-0002's 14, plus ADR-0004's relative triggers, pending acceptance with #13); the platform design exercise (this plan and the frontend sketch), which found no reason to change the config contract; K3s out of v1.0 ([ADR-0003](docs/adr/0003-v1-is-compose-only.md), agreed 2026-10-14).

# ADR-0002: kazu.yaml structural contract lives in a published JSON schema

Date: 2026-10-14
Status: Proposed (awaiting Varun's sign-off on PR #13)

## Context

PR #13 (config 1/5) establishes the contract for `kazu.yaml`. Everything after it in the stack (#14–#17) is plumbing for this file, so its shape decisions are foundational. Most follow DESIGN.md §4.4 as written; the PR description lists the points DESIGN.md does not specify.

## Decision

`config/kazu.schema.json` is the published contract, embedded as `config.Schema` for editors, covering **structure only** (types, unknown keys, enums, required fields per fault kind, string formats). Cross-key rules are Kazu's job and land in 5/5. Unknown keys are errors everywhere. Bare numbers where durations/percentages go are rejected.

Proposals beyond DESIGN.md (from PR #13; accepting this ADR accepts them):

1. `command` takes a list only; `environment` takes both compose forms.
2. `http:` check mirrors `sql:` — `{ on, path, method, expect: { status, body } }`.
3. `slo.max` is a duration for latency measures, a percentage for `error_rate`.
4. `metric` takes `max`/`min` or `within`, plus optional `no_data: warn`.
5. When `defaults` joins the schema (see 12), `defaults.off` accepts `crashes, exits, traffic_exit, error_rate, latency, recovers`.
6. Measures are `p50, p90, p95, p99, error_rate`.
7. A scenario can be empty (no faults).
8. Names are letters, digits, `.`, `_`, `-`.
9. `$id` is `https://zugzwang-io.github.io/kazu/kazu.schema.json`.
10. **Rules decidable inside one object live in the schema**, not in Kazu's validator (decided 2026-10-14 in the #13–#17 review): a fault has exactly one target and one trigger, only its kind's fields, request faults go on edges and process faults on services; `code` is a non-empty value (its range depends on the edge's protocol, so Kazu checks it); `rate` and `load` are 0–100%; a check has exactly one kind and compose keys only with `image`; `metric` has a bound (`within` or `max`/`min`, not both); `slo.max` follows `measure`. Editors get these for free and Kazu's validator keeps only rules that need the whole config.
11. `system: { manifests }` is published although the Kubernetes driver is v1.1 (ADR-0003), so the contract doesn't change at 1.1. Names must start with a letter or digit, stricter than compose's service names.
12. **The schema carries only the top-level keys Kazu builds next; each kept key has its final shape** (decided 2026-10-15 in the #13 review). `telemetry`, `auto_scenarios`, `defaults` and `gate` are left out until the phase that builds them, so editors don't offer them. Inside kept keys the schema is the intended interface (every fault kind, `http`/`grpc` edges, every check kind, ...), and Kazu's validator rejects what the running build doesn't do yet with "not supported yet", never a silent no-op. Each phase deletes entries from that list; the schema itself rarely changes.
13. **Scenarios take their own `checks`** (same entries as the top-level list), run in that scenario on top of the top-level checks. Follows Antithesis, which has both global properties and per-test-template commands. Supersedes DESIGN.md §4.6's single checks list, and gives a check that only means something under one scenario's faults (`recovers` for the faulted service) a place where it always applies.
14. **A `sql` check passes when its query returns no rows** (decided 2026-10-15): `{ on, query }`, no `expect`. The query selects violations, as dbt's data tests do. Kazu reads only the first few rows (for the failure message) under a statement timeout, so result size is bounded, and there are no value-comparison rules (text vs number, NULL, the `empty` keyword). Comparing exact rows is a code check's job via `run.sql`.

## Consequences

On acceptance, fold the proposals into DESIGN.md §4.4 so the reference and the schema agree; until then §4.4 deliberately omits them.

Editors get completion/validation without Kazu code; typos fail loudly. Once published at `$id`, loosening the schema is safe (every config that validated still does) and tightening breaks existing configs, so keys join it, never leave it. Tests validate with a generic validator, keeping the contract honest. Changing any of the proposals after merge in a way that rejects configs it accepted means a schema version bump.

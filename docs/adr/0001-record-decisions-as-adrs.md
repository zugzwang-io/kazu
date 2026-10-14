# ADR-0001: Record architecture decisions as ADRs

Date: 2026-10-14
Status: Accepted

## Context

Decisions are currently scattered across PR descriptions (#13's "Decisions to agree on"), SPEC.md's "Behaviour decisions", and review threads. Agents and humans both need one place to see what was decided, by whom, and why — and independent verification reviews need explicit decisions to challenge.

## Decision

Every decision that constrains future work — architecture, API/config shape, product scope, testing strategy — gets an ADR in `docs/adr/`, numbered `NNNN-slug.md`, using the template in this directory. Status is one of: Proposed → Accepted / Rejected / Superseded by ADR-XXXX. **Only Varun moves an ADR out of Proposed.** Agents draft ADRs (status: Proposed) whenever they make or encounter such a decision; a PR that embodies an undocumented decision should add its ADR.

SPEC.md's "Behaviour decisions" section remains the home for shop-internal behaviour choices; ADRs are for decisions about Kazu itself or cross-cutting ones.

## Consequences

Review agents can challenge decisions by citing the ADR rather than re-deriving intent. Rejected challenges get recorded in the ADR's Consequences, so the same debate doesn't repeat.

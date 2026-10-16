# ADR-0006: Traffic runs for a fixed duration per scenario, 120 s by default

Date: 2026-10-16
Status: Accepted (2026-10-16)

## Context

DESIGN.md §11 records "Kazu computes `KAZU_DURATION` per scenario": the end of the last time-triggered fault plus the recovery window plus a margin, so nobody adds it up by hand. Computing it needs every fault to have a planned time. Request-count triggers have none, and with relative triggers (ADR-0004) neither does a fault anchored to one, so the computation grew special cases: request-count scenarios defaulting to 120 s, a proposal to forbid anchoring to a request-count fault, and a rule requiring `duration:` when no fault has a planned time.

## Decision

Every scenario's traffic runs for its `duration:`, **120 s** when the scenario doesn't set one. Kazu computes nothing.

- A fault that hasn't fired by the time traffic ends, or ended too late to watch recovery, makes the scenario **not exercised**, with the reason; the fix is a longer `duration:`. Too short fails on a fact, never as a pass.
- Request-count faults and anchors need no special cases. An anchor on a request-count fault resolves at run time, through the controller, like other cross-component faults; this amends ADR-0004's "every fault's start and end resolve when the plan is built", which still holds for wall-clock faults.

Considered and lost: computing the duration (above), and requiring `duration:` on every scenario (friction for the common case).

## Consequences

- Supersedes the DESIGN.md §11 row "Kazu computes `KAZU_DURATION` per scenario".
- A scenario whose faults end early wastes CI time until it sets a shorter `duration:`. Computing a tighter duration can come back later as an optimisation, without a config change.
- The recovery check (Phase 5) must report a recovery window cut short by the end of traffic as not exercised, never as a pass.

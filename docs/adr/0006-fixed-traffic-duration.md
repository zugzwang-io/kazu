# ADR-0006: Traffic decides how long it runs; Kazu only caps a trial

Date: 2026-10-16
Status: Accepted (2026-10-16)

## Context

DESIGN.md §11 records "Kazu computes `KAZU_DURATION` per scenario": the end of the last time-triggered fault plus the recovery window plus a margin, passed to traffic so nobody adds it up by hand. Computing it needs every fault to have a planned time. Request-count triggers have none, and with relative triggers (ADR-0004) neither does a fault anchored to one, so the computation grew special cases: a 120 s default for request-count scenarios, a proposal to forbid anchoring to a request-count fault, and a rule requiring a duration when no fault has a planned time. A fixed `duration:` per scenario was considered next, but it still makes Kazu own the workload's length.

## Decision

**The traffic decides how long it runs, as Antithesis's test commands do; Kazu doesn't set its length.** The k6 template sends for `__ENV.DURATION || "120s"`, so a scenario that needs longer sets it through the existing env knob (`env: { traffic: { DURATION: 180s } }`). There is no `duration:` key and no `KAZU_DURATION`.

- **Kazu caps each trial at 30 minutes.** Traffic still running then is stopped and the scenario reported **not exercised**, with the reason.
- **Too short fails on a fact.** A fault that hasn't fired by the time traffic exits, or ended too late to watch recovery, makes the scenario **not exercised**; the fix is a longer `DURATION`.
- **No special cases for request-count faults or anchors.** An anchor on a request-count fault resolves at run time, through the controller, like other cross-component faults; this amends ADR-0004's "every fault's start and end resolve when the plan is built", which still holds for wall-clock faults.

Considered and lost:
- **Computing the duration** (above): special cases for every trigger without a planned time.
- **A fixed `duration:` per scenario, 120 s by default**: simpler, but a Kazu key for something the load tool already expresses, and a deviation from Antithesis.

## Consequences

- Supersedes the DESIGN.md §11 row "Kazu computes `KAZU_DURATION` per scenario". `duration:` leaves the schema and `Config`.
- Fault times live in kazu.yaml and the traffic's length in its script, so they can disagree; the result is *not exercised*, a fact, never a pass.
- A scenario whose faults end early wastes CI time until its traffic is shortened.
- The recovery check (Phase 5) must report a recovery window cut short by the end of traffic as not exercised, never as a pass.

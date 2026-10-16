# ADR-0004: Faults can start relative to other faults' planned start or end

Date: 2026-10-15
Status: Proposed (awaiting Varun's sign-off on PR #13)

## Context

A scenario's faults are a list, each with `after` (from traffic start) and `for`. Faults in sequence or overlapping need hand arithmetic on `after`, and changing one fault's `for` silently shifts nothing that was meant to follow it. Raised in the #13 review: the faults of a scenario should form a graph, unconnected faults overlapping by their time windows, connected ones triggered relative to each other.

## Decision

A fault can name what its `after` counts from:

```yaml
faults:
  - { name: slow-pay, edge: checkout -> payments, fault: latency, delay: 300ms, after: 20s, for: 30s }
  - { name: db-down, edge: checkout -> postgres, fault: down, from: slow-pay, after: 5s, for: 10s }   # slow-pay's end
  - { edge: checkout -> payments, fault: down, from: { start: slow-pay }, after: 10s, for: 5s }      # slow-pay's start
  - { edge: traffic -> checkout, fault: latency, delay: 50ms, from: [db-down, { start: slow-pay }] }  # the latest of both
```

- `name` is optional and only needed on a fault something anchors to.
- `from` is a fault name (its end), `{ start: name }`, or a list of these (a join: the latest of them). Without `from`, `after` counts from traffic start, so unconnected faults stay concurrent by their windows.
- Anchors are **planned** times. Every fault's start and end resolve when the plan is built, so base and head get identical schedules, `KAZU_DURATION` stays computable, and replay needs nothing new. The proxy and controller only ever see resolved times.
- Kazu rejects, as whole-config rules: an anchor that names no fault in the scenario, cycles, an anchor whose end is used but which has no `for` (it never ends), and `from` with `after_requests` (the schema rejects that one).

**Not done, ever: triggers on system events** ("once payments is healthy again"). The schedule would then depend on the system under test, so base and head could get different fault timelines, and an event that never happens turns into a fault that never fires. Antithesis doesn't let users script faults on system state either; recovery belongs in checks (`recovers:`).

Considered and lost:
- A string expression (`after: slow-pay.end + 5s`): a new mini-language, no editor validation.
- Faults as a map keyed by name: forces a name on every fault.
- Nested `then:` children: a tree only, and deep nesting reads badly.

## Consequences

The schema carries `name` and `from` now (ADR-0002, decision 12: kept keys have their intended shape). Resolving anchors into times is plan work (driver 4/4's `KAZU_DURATION` computation, generalised). On acceptance, fold into DESIGN.md §4.4/§4.5 and §11.

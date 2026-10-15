# ADR-0003: v1.0 ships the compose driver only; the K3s driver is v1.1

Date: 2026-10-14
Status: Proposed (Varun agreed in the #27 discussion; status change is his)

## Context

DESIGN.md §8 step 4 lists the K3s driver alongside `init`, `doctor` and telemetry, inside the work that completes v1. PLAN.md (#27) had to draw the v1 line and asked whether the second target belongs inside it.

Forces: the K3s driver has an unresolved spike in front of it (whether GitHub-hosted runners can run K3s in Docker; DESIGN §8, §10); it reimplements the whole driver surface (readiness, one-shot detection, routing via `hostAliases`, crash in place, the event watcher) with nothing else in v1 reusing it; no other v1 feature depends on it; and the shop only adds `k8s/rendered/` for rows S12 and S13, which test that the second driver gives the same verdict rather than anything new in Kazu.

## Decision

v1.0 is compose-only. The K3s driver, its spike, and rows S12–S13 form their own phase (PLAN.md Phase 7, v1.1) immediately after v1.0. The driver interface built in Phase 3 is the seam: Phase 7 adds a second implementation behind it and nothing above the driver changes.

Alternative considered: keep DESIGN §8 as written and ship both targets in v1.0. Rejected because it makes the v1 date depend on an unspiked target and doubles the packaging story for the launch.

## Consequences

Easier: one target, one packaging story and one set of docs at launch; the v1 date no longer waits on the K3s spike. Harder: Kubernetes-only teams cannot try Kazu until v1.1, which is a real adoption gap. If early users turn out to be mostly on Kubernetes, the order flips by moving Phase 7's bullets into Phase 6; the seam makes that cheap. DESIGN §8 step 4 should be read with this ADR; it is not edited, per the §11 freeze.

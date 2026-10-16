# ADR-0005: How the kazu binary reaches containers in the trial network

Date: 2026-10-16
Status: Proposed (drafted by the 2026-10-16 foundations review)

## Context

The controller must talk gRPC to every proxy container, and the check host must run `sql`/`http` against `connections:` URLs such as `postgres://…@postgres/…` (DESIGN §4.6: "Kazu does the real work"). Those names resolve only inside the compose network, and `kazu` runs on the host. On macOS Docker Desktop container IPs are not routable from the host, and a sealed (`internal`) network has no published ports. Separately, compose `extra_hosts` (DESIGN §6.6) maps a name to an IP, so every proxy needs a static address on the trial network, which can collide with a customer's own networks or subnets. Nothing in DESIGN §4.1, §5, §6 or PLAN records either choice; driver 2/4 needs both.

## Decision

**One in-network helper container.** Kazu adds a container of its own image (the image proxies already run from) to the trial network. The host drives it over one channel; it does proxy control and the check host's `sql`/`http` from inside the network. Decided 2026-10-16: CI is the target that matters, and this keeps networks sealed and behaves the same on Linux runners and macOS.

Considered and lost:
- **Published host ports with URL rewriting**: publishes the customer's services (their database) and doesn't work on a sealed `internal` network; rewriting every connection scheme is fiddly.
- **Running all of Kazu in a container**: breaks the one-binary-on-your-laptop story and makes debugging awkward.

Either way, the driver asks Docker for a free subnet, puts the trial network on it and gives each proxy a static IP for `extra_hosts`; a system file that pins a conflicting subnet is a positioned config error.

## Consequences

The Phase 3 spike proves it on a GitHub-hosted runner before any driver code; driver 2/4 and the controller build on it. The host-to-helper channel (one published port, or stdio via attach) is the spike's to pick.

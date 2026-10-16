# ADR-0005: How the kazu binary reaches containers in the trial network

Date: 2026-10-16
Status: Accepted (2026-10-16)

## Context

The controller must talk gRPC to every proxy container, and the check host must run `sql`/`http` against `connections:` URLs such as `postgres://…@postgres/…` (DESIGN §4.6: "Kazu does the real work"). Those names resolve only inside the compose network, and `kazu` runs on the host. On macOS Docker Desktop container IPs are not routable from the host, and a sealed (`internal`) network has no published ports. Separately, compose `extra_hosts` (DESIGN §6.6) maps a name to an IP, so every proxy needs a static address on the trial network, which can collide with a customer's own networks or subnets. Nothing in DESIGN §4.1, §5, §6 or PLAN records either choice; driver 2/4 needs both.

## Decision

**One in-network helper container.** Kazu adds a container of its own image (the image proxies already run from) to the trial network. The host drives it over one channel; it does proxy control and the check host's `sql`/`http` from inside the network. Decided 2026-10-16: CI is the target that matters, and this keeps networks sealed and behaves the same on Linux runners and macOS.

Considered and lost:
- **Published host ports with URL rewriting**: publishes the customer's services (their database) and doesn't work on a sealed `internal` network; rewriting every connection scheme is fiddly.
- **Running all of Kazu in a container**: breaks the one-binary-on-your-laptop story and makes debugging awkward.

**The channel is one long-lived `docker compose exec -T` session into the helper, speaking JSON lines on stdin/stdout** (proved by the spike below). A published port can't exist on an `internal` network; `docker attach` was rejected because it never exits on EOF or SIGTERM (a local run hung until SIGKILL) and ties the channel to the helper's single stdin.

The driver asks Docker for a free subnet, puts the trial network on it and gives each proxy a static IP for `extra_hosts`; a system file that pins a conflicting subnet is a positioned config error.

## Consequences

**Proved by the Phase 3 spike** (branch `spike-network`, run 38973416568), every check passing 3× on a GitHub-hosted runner and 3× on Colima: the shop healthy on a Kazu-chosen subnet with `internal: true` (`up --wait` works; healthchecks run inside containers); checkout's calls to payments through a static-IP proxy via `extra_hosts`, with the alias per-caller (another container calling `payments` bypasses it); SQL and HTTP from the host through the helper; the network sealed (internet and external DNS fail at once); clean teardown and subnet reuse; the subnet picker skipping a taken range. On the runner, with images cached, `up --wait` takes ~6 s and a host↔helper round trip under 1 ms.

What the driver must do, learned in the spike:
- Override the project's default network (`networks.default` with `internal: true` and `ipam` `subnet` + `ip_range`); no service needs editing.
- Keep Docker's dynamic addresses out of the proxies' range: `ip_range` takes the lower half, proxies get static addresses from `.200`.
- Remove published ports with `ports: !reset []` (Compose 2.24 or later).
- `up --wait` an explicit service list, and add `depends_on` from each caller to its proxy.
- Use absolute build contexts in the generated override: relative paths resolve against the first compose file's directory.
- Retry subnet allocation when Docker reports an overlap: the picker sees only Docker's networks (not host routes or VPNs), and two trials on one daemon can race.
- **Proxy counters must update as bytes flow, not on close**: checkout keeps one keep-alive connection for the whole run, so close-time counters read 0 and the landed check would call every fault on that edge not exercised.

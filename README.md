# Kazu

**Resilience regression testing for every release.** By [Zugzwang](https://github.com/zugzwang-io).

On every PR or release candidate, Kazu answers one question: *did this change make us more fragile than the last release?* It injects seeded faults into your system, checks invariants you write, and compares the result against the base branch.

> **Status: pre-alpha.** Nothing here works yet beyond `hello, world`. The sections below describe where Kazu is headed, not what it does today.

## How it will work

Point Kazu at your system with a `resilience.yaml`. The smallest valid file is one line:

```yaml
system: docker-compose.yml
```

A fuller config declares scenarios (faults to inject) and invariants (what must stay true):

```yaml
system: docker-compose.yml
traffic: k6 run load/checkout.js

scenarios:
  slow-payments:
    payments: latency 300ms ±50ms
  db-blip:
    postgres: down for 15s after 30s

invariants:
  - no_double_charge                  # function in resilience/invariants.{py,ts,go,...}
  - slo: checkout p99 < 800ms
  - recovers: checkout within 60s
```

Then run it against base and head:

```
kazu run  ·  suite pr  ·  base main@9c41e2 vs head a1f3c9

  slow-payments              ✓ pass       p99 612ms → 640ms (+4%)
  db-blip                    ✓ pass       recovers in 11s → 9s
```

Every failure prints the command that reproduces it (`kazu replay <run-id>`).

## Principles

- **A fault-injection and assertion runtime, nothing more.** You own your environment, credentials and data; Kazu owns the wires between services and the checks.
- **Fresh environment per trial.** No state-reset magic.
- **Invariants in any language.** An executable's exit code, a SQL query, or a thin SDK (Python, TypeScript, Go first).
- **Runs anywhere.** Laptop, any CI, Kubernetes. One static Go binary.
- **The runtime is free, forever.** Apache 2.0, with no features gated on your own compute.

## Development

Requires Go.

```sh
go run ./cmd/kazu
```

## License

Apache 2.0.

# Kazu

**Resilience regression testing for every release.** By [Zugzwang](https://github.com/zugzwang-io).

On every PR or release candidate, Kazu answers one question: *did this change make us more fragile than the last release?* It injects seeded faults into your system, checks built-in and user-written invariants, and compares the result against the base branch.

> **Status: pre-alpha.** Nothing here works yet beyond `hello, world`. The sections below describe where Kazu is headed, not what it does today.

## How it will work

Point Kazu at your system with a `kazu.yaml`. The smallest valid file is one line:

```yaml
system: docker-compose.yml
```

A fuller config declares traffic and checks (each an image), the edges Kazu may fault, scenarios (faults to inject) and one-line checks. Checks that need code go in files for Kazu's SDK image:

```yaml
system: docker-compose.yml
traffic: { image: grafana/k6, command: [run, /files/checkout.js], files: ./load }
checks:  { image: ghcr.io/zugzwang-io/kazu-python:1, files: ./checks }

edges:
  checkout -> payments: http
  checkout -> postgres: tcp

scenarios:
  slow-payments:
    checkout -> payments: latency 300ms ±50ms
  db-blip:
    postgres: down for 15s after 30s

invariants:
  - no_regression: checkout p99 within 10%
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

- **A fault-injection and assertion runtime, nothing more.** You provide a complete, isolated system (docker compose, or Kubernetes manifests); Kazu owns the wires between services and the checks.
- **Fresh environment per trial.** No state-reset magic.
- **Checks in any language.** Built-in defaults, one-line SQL and metric checks, or code in a thin SDK image (Python, TypeScript, Go first).
- **Runs anywhere.** Laptop, any CI, Kubernetes. One static Go binary.
- **The runtime is free, forever.** Apache 2.0, with no features gated on your own compute.

## Development

Requires Go.

```sh
go run ./cmd/kazu
```

## License

Apache 2.0.

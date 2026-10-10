# Kazu

**Resilience regression testing for every release.** By [Zugzwang](https://github.com/zugzwang-io).

On every PR or release candidate, Kazu answers one question: *did this change make us more fragile than the last release?* It injects seeded faults into your system, runs built-in and user-written checks, and compares the result against the base branch.

> **Status: pre-alpha.** Nothing here works yet beyond `hello, world`. The sections below describe where Kazu is headed, not what it does today.

## How it will work

Point Kazu at your system with a `kazu.yaml`. It declares your system (a docker compose file), the traffic to send (a load-test image, k6 supported), the connections between services Kazu may fault, the scenarios to run, and your checks. Built-in checks for crashes, errors, latency and recovery run on top. Checks that need code run in Kazu's SDK image:

```yaml
system: docker-compose.yml

traffic:
  image: grafana/k6:1.0.0
  command: [run, /load/checkout.js]
  volumes: [./load:/load:ro]

edges:
  traffic -> checkout: http          # user-facing; built-in checks read it
  checkout -> payments: http
  checkout -> postgres: tcp

connections:
  postgres: postgres://postgres:dev@postgres/checkout

scenarios:
  slow-payments:
    faults:
      - { edge: checkout -> payments, fault: latency, delay: 300ms, jitter: 50ms, after: 20s }
  db-blip:
    faults:
      - { service: postgres, fault: down, after: 30s, for: 15s }

checks:                              # on top of built-in crash, error-rate, latency and recovery checks
  - sql: { on: postgres, query: "select order_id from charges group by order_id having count(*) > 1", expect: empty }
  - no_regression: { service: checkout, measure: p99, within: 10% }
  - image: ghcr.io/zugzwang-io/kazu-python:1    # checks that need code
    volumes: [./checks:/checks:ro]

suites:
  pr: [slow-payments, db-blip]
```

Then run it against base and head:

```
$ kazu run --suite pr --base TAG=main-9c41e2 --head TAG=pr-a1f3c9

kazu run  ·  suite pr  ·  base main@9c41e2 vs head a1f3c9

  slow-payments              ✓ pass       p99 612ms → 640ms (+4%)
  db-blip                    ✓ pass       recovers in 11s → 9s
```

Every run prints an ID for its stored record, and every failure prints the command that reproduces it, e.g. `kazu replay r_2fj9 --trial 2`.

## Principles

- **A fault-injection and assertion runtime, nothing more.** You provide a complete, isolated system (docker compose, or Kubernetes manifests); Kazu owns the wires between services and the checks.
- **Fresh environment per trial.** No state-reset magic.
- **Checks in any language.** Built-in defaults, one-line SQL and metric checks, or code in a thin SDK image (Python, TypeScript, Go first).
- **Runs in CI or on a laptop.** One static Go binary; everything else it runs is an image.
- **The runtime is free, forever.** Apache 2.0, with no features gated on your own compute.

## Development

Requires Go.

```sh
go run ./cmd/kazu
```

## License

Apache 2.0.

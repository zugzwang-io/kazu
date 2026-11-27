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
  - sql: { on: postgres, query: "select order_id from charges group by order_id having count(*) > 1" }
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

## Faults

Each fault produces one distinct outcome; there is exactly one natural way to express each. The caller is the service on the left of an edge (`checkout` in `checkout -> payments`), the callee is the one on the right.

### Network faults (on a declared edge, or every declared edge into a service)

| Fault | Mimics | What the callee sees | What the caller sees | `direction` | Applies to |
|---|---|---|---|---|---|
| `latency` | a slow dependency or slow link | `response` (default): the request on time. `request`: the request `delay` late | the answer `delay` late. If `delay` is longer than the caller's timeout, it gives up: **response loss, silent** | `request` or `response`, default `response` | every request in the window |
| `partition` | a network split that heals | nothing during the window. When it heals, everything held, including requests whose callers have since given up | silence both ways during the window. Held traffic is delivered on heal; if the caller already gave up, the answer goes to a closed connection | none (always both ways) | every connection, live and new |
| `reset` | a peer crashing mid-conversation, or a load balancer or firewall killing connections | `request` (default): never sees the cut request. `response`: processed it, and the answer is cut off | `ECONNRESET` straight away. With `response`: **response loss, with an error** | `request` or `response`, default `request` | a sampled share of requests (`rate`); on `tcp` edges, of connections |
| `bandwidth` | a congested link | data arrives throttled | large transfers slow down; small requests barely change | `request`, `response` or `both`, default `both` (each direction gets its own limit) | every connection in the window |
| `down` | a service unreachable or refusing connections | nothing: new connections refused, live ones reset | `ECONNREFUSED` on connect (definitely not processed); `ECONNRESET` on live connections | none | every connection in the window |

### HTTP/gRPC faults (on `http` or `grpc` edges)

| Fault | Mimics | What the callee sees | What the caller sees | Applies to |
|---|---|---|---|---|
| `errors` | a dependency returning errors | never sees the request; the proxy answers | the configured status (e.g. 503) or gRPC code | a sampled share of requests (`rate`) |

### Process and resource faults (on a service)

| Fault | Mimics | What the service does | What its callers see |
|---|---|---|---|
| `crash` | a process dying | killed, then restarted in place after `for` (without `for`, it stays down until traffic exits) | resets on live connections, refused connections until it is back |
| `pause` | a process frozen (a long GC pause, a stopped VM) | frozen for the window, then carries on | silence: connections stay open with no answers |
| `cpu`, `memory` | a starved host | runs under CPU or memory pressure (mechanism still open: a load or a quota) | slower answers, or an out-of-memory kill |
| `disk_full` | a full disk | writes fail with "no space left" | whatever errors the service returns |

### When faults overlap on one edge

Bytes in each direction pass through these stages in order:

`down` → `partition` (read gate) → `reset` → `errors` → `latency` → `bandwidth` → `partition` (write gate)

- `down` always wins.
- Nothing crosses a partition, not even Kazu's own error answers.
- `reset` beats `errors`, so a request drawn by both is cut.
- Error answers pass through response latency, which gives slow errors.

Two faults of the same type can't be active on the same target at once: the same edge for network and HTTP faults, the same service for process and resource faults. Sequence them with `from:`, or space them apart in wall-clock time; a fault that waits on a request count is treated as running forever, so only `from:` sequencing separates it. A `crash` with `for` is down for that long, then restarted.

Response loss, where the callee did the work but the caller never gets the answer, has two forms: silent (`latency` past the caller's timeout) and with an error (`reset` with `direction: response`).

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

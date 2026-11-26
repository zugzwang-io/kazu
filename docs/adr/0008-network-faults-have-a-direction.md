# ADR-0008: Network faults have a direction; `timeout` forwards the request before dropping the response

Date: 2026-10-28
Status: Proposed

## Context

None of Kazu's planned faults can produce the case where **the callee receives and processes the request and the caller never gets the answer**. Every network fault acts on both directions of an edge, or on the connection as a whole. DESIGN.md §4.5 doesn't say whether the HTTP `timeout` fault lets the request reach the callee.

That case is the unknown-outcome bug class: a payment that happened, but the caller can't tell. So it retries with a fresh key (a double charge) or marks the order `failed` (a charge on a failed order). The shop's core invariant is built around it:
- 2xx means `paid`.
- A 4xx, or refused or 503-shed on every attempt, means `failed`.
- A timeout, reset or other 5xx leaves the order `pending`. The reconciler then re-sends it under the same idempotency key, and payments deduplicates under an advisory lock on that key.

Today the only way to produce an unknown outcome is a `down` that happens to reset a connection mid-request.

Three research passes on 2026-10-28 each ranked this the most valuable missing fault:
- **Toxiproxy** gives every fault a `stream`.
- **Antithesis** treats inbound and outbound packets independently ("all network faults may be asymmetric").
- **Chaos Mesh's** HTTPChaos has `target: Request | Response`.

PLAN.md carries the open decision "latency: delay request, response, or both?". The maintainer approved the mechanism on 2026-10-28 and settled the details below on 2026-11-26.

## Decision

### One natural way per fault

Each caller-and-callee outcome has exactly one fault that produces it, at each granularity. A fault that could be written two ways makes configs harder to read and review. This table is the test for any fault added later:

| Fault | What the callee does | What the caller sees |
|---|---|---|
| `latency`, `direction: response` (default) | processes the request on time | the answer arrives `delay` late |
| `latency`, `direction: request` | gets the request `delay` late, then processes it | the answer arrives `delay` late. A caller that times out and retries can race the original at the callee |
| `bandwidth` | as usual, at a throttled rate | slow transfers |
| `blackhole`, `direction: request` | sees nothing until the window ends, then gets the held bytes late | silence for the window |
| `blackhole`, `direction: response` | processes the request; its answer is held until the window ends | silence for the window, then the answer late, usually after it has given up |
| `blackhole`, `direction: both` | sees nothing until the window ends | silence, both ways, until the window ends |
| `reset`, `direction: request` | never sees the request | `ECONNRESET` straight away |
| `reset`, `direction: response` | processes the request | `ECONNRESET` when the answer starts |
| `down` | never sees the request | `ECONNREFUSED` on new connections; live ones are reset |
| `errors` (HTTP/gRPC) | never sees the request | an error status from the proxy |
| `timeout` (HTTP/gRPC) | processes the request | silence; the answer is dropped and never delivered |

`timeout` and `blackhole, direction: response` aren't duplicates. `timeout` drops one sampled request's answer for good. `blackhole` pauses the whole connection and delivers what it held once the window ends. `reset, direction: response` is response loss with an immediate error, and `timeout` is response loss with silence.

### A `direction` field on network faults

- **`request`** means the bytes the caller sends toward the callee, following the edge's `a -> b` arrow.
- **`response`** means the bytes the callee sends back.
- The names are relative to the edge, so they mean the same on a `tcp` edge, where the proxy sees only bytes.
- On a `service: b` target, the field applies the same way to every declared inbound edge of `b`.

```yaml
- { edge: checkout -> payments, fault: latency, delay: 300ms }                       # answers late
- { edge: checkout -> payments, fault: latency, delay: 300ms, direction: request }   # requests arrive late
- { edge: checkout -> payments, fault: blackhole, direction: response, after: 20s, for: 10s }
```

- **`latency`** takes `request` or `response`, and defaults to `response`. `delay` is always the time added to a call, applied once, in the one direction chosen. There is no `both`: splitting a delay across the two directions has no use, and slowing streams in both directions is what `bandwidth` does. Under `response` the callee acts on time and its answer is late, which is what "payments is slow" usually means. `request` exists for the case where the callee acts late, so that a caller's retry can race the original.
- **`blackhole`, `reset` and `bandwidth`** take `request`, `response` or `both`, and default to `both`. For these faults, `both` is the link failing, which is the natural case.
- **`down`, `errors`, `timeout`** and the process and resource faults take no `direction`, and the schema rejects it on them:
  - `down` is a state of the connection, and refused has to keep meaning "definitely not processed".
  - `errors` never forwards, so there is no stream to choose.
  - `timeout` acts on the response by definition.
  - The process and resource faults have no stream.

### What each fault does to live connections

**A network fault applies to live connections from the moment it starts.** Otherwise a pooled keep-alive connection would ride through it untouched.

**`blackhole` pauses; it never discards.** In the selected direction, the proxy stops reading. TCP then holds the bytes back: the sender's writes fill the socket buffers and then block, the way a real partition behaves. When the window ends, the proxy reads again and the held bytes are delivered late, as TCP's retransmits do across a partition that heals. Nothing is lost, so the stream is never corrupted.

This is why `blackhole` is the fault that tests a stale request arriving after the caller gave up and retried. A caller with no client timeout of its own hangs until the window ends.

### HTTP `timeout` forwards first

On a request drawn for `timeout` (by `rate`, from the seeded per-request PRNG), the proxy:
1. forwards the request to the callee in full;
2. reads the callee's answer as it arrives and drops it, so the callee isn't held up and its handler completes normally;
3. sends the caller nothing.

A caller with a client timeout gives up and closes, and the proxy then closes the upstream side. **When the fault's window ends, the proxy resets every connection still waiting for a dropped answer.** A caller with no timeout therefore gets `ECONNRESET` at the end of the window. It doesn't hang until Kazu's 30-minute trial cap, so the missing timeout shows up as errors inside the trial, which the error-rate checks see, rather than as *not exercised*.

A request that is never delivered at all has one natural form: `blackhole, direction: request`, which works per connection. A version for a sampled share of requests is in the post-v1 backlog.

### Overlapping faults on one edge

Faults whose windows overlap on the same edge apply to each request in a fixed order:

**`latency` → `bandwidth` → `errors` / `timeout` / `reset` → `blackhole` → `down`**

A fault that ends the request stops the chain, so `down` always wins. Latency plus errors gives a **slow error**: the request waits out its delay, then gets the error. That's how a struggling dependency usually fails; a fast error is what a service does itself when it sheds load.

**Two faults of the same kind overlapping on one edge are rejected when the config loads**, with an error naming both. Two latencies, or two error rates, have no single obvious meaning, so the user picks one or sequences them with `from:`.

A per-scenario order override is in the post-v1 backlog.

### The landed check counts an effect, not an attempt

Each proxy keeps applied-fault counters per fault and per direction. They are added in proxy 4/5, read through the control service in proxy 5/5, and written to the run record. A fault counts as **applied** only when it changed what a side saw:

| Fault | Applied when |
|---|---|
| `latency` | a chunk in its direction was held for the delay |
| `blackhole` | the proxy left readable bytes unread in a selected direction. For `response`, the callee answered during the window |
| `reset` | a drawn connection was cut on a chunk in the selected direction. For `response`, the callee had started answering |
| `bandwidth` | the limiter made at least one write in a selected direction wait |
| `timeout` | a drawn request's answer arrived from the callee and was dropped |

A `response` blackhole against a callee that never answers, or a 5% `timeout` that drew no request, therefore reports *not exercised*. Forwarding a request or accepting a connection never counts.

### Alternatives considered

- **A separate `response_loss` fault kind.** It would need one kind per combination of fault and direction; one field covers them all. Lost.
- **Toxiproxy's `stream: upstream | downstream`, defaulting to `downstream`.** It's the same field, but `upstream` means opposite things in Toxiproxy and in nginx or Envoy, while `request`/`response` are unambiguous on an `a -> b` edge. Antithesis's `inbound`/`outbound` lost because they are relative to a node, not an edge.
- **`blackhole` discarding bytes and resetting the connection at the end.** It never delivers late, so it can't find the stale-request bug. Resuming after a discard would corrupt the stream. Lost to pausing.
- **`delay` applied in each direction under `both`.** A call would pay `delay` twice, which surprises anyone reading p99 numbers. Lost to "delay is the time added to the call".
- **"Most aggressive fault wins" on overlap (Antithesis's rule).** It gives fast errors under latency. Lost to the fixed order, as a recorded deviation.
- **Do nothing.** The unknown-outcome class stays unreachable except by luck. Lost.

### Antithesis alignment

Antithesis's network faults can act in one direction ("inbound, outbound, or both"), its partitions hold traffic rather than corrupt it, and TCP delivers what was held once a partition heals. Kazu's `direction` and its pausing `blackhole` follow that.

Deviations, recorded in DESIGN §6.7 on acceptance:
- **Latency defaults to `response`,** where Antithesis's network faults are symmetric by default. `delay` then means the time added to a call, which is what users mean by "payments is slow".
- **Overlapping faults apply in a fixed order,** where Antithesis lets the more aggressive one win. The only case where the two give different results is latency combined with errors, timeout or reset, which gives a slow error under Kazu's order.

`down` has no Antithesis counterpart: Antithesis's partitions drop packets rather than refuse connections. Neither does `timeout`: Antithesis injects no faults at the HTTP layer.

### Settled 2026-11-26

1. When a `blackhole` ends, connections resume and the held bytes are delivered late (pause, not discard).
2. Overlapping faults: the fixed order above; two faults of the same kind on one edge are rejected; a per-scenario order override goes in the backlog.
3. Latency takes `request | response`, defaulting to `response`; `delay` is the time added to a call; there is no `both`.
4. `timeout` takes no `direction`. A request never delivered is `blackhole, direction: request`; a sampled per-request version goes in the backlog.
5. When a `timeout` window ends, connections still waiting for a dropped answer are reset.

## Consequences

**Schema and validation** (ADR-0002, decisions 10 and 12):
- The fault object gains `direction`.
  - On `latency`, its values are `request | response`, default `response`.
  - On `blackhole`, `reset` and `bandwidth`, its values are `request | response | both`, default `both`.
  - It is rejected on every other kind.
- The defaults are filled in by Kazu's decoder.
- Validation rejects two faults of the same kind on one edge (counting `service:` targets as their inbound edges) unless they can't overlap. They can't overlap only if both windows are planned wall-clock times that don't intersect (ADR-0004), or if one is sequenced after the other's end with `from:`. A window that depends on a request-count trigger isn't known until run time, so it is treated as able to overlap.
- Until a kind's PR implements `direction`, the planner rejects it on that kind (ADR-0007).

**PRs that change:**
- **A small config PR** ahead of proxy 2/5: the schema, the `Fault` field, the defaults, the same-kind overlap check, and table tests.
- **Proxy 2/5 (`latency`):** its PLAN bullet becomes "delay plus jitter per chunk in the fault's `direction` (default `response`), seeded". The echo-server tests cover both directions. This closes PLAN.md's open latency decision.
- **Proxy 3/5 (`down`):** asserts that `direction` is rejected, and that `down` wins over every other fault while it overlaps them.
- **Proxy 4/5 (fault program, applied counters):** the counters carry a direction, and overlapping faults apply in the fixed order.
- **The `blackhole`, `reset` and `bandwidth` PR(s):** each implements its rows of the direction table, the live-connection rule, and `blackhole`'s pause and resume.
- **The HTTP `timeout` PR:** forward first, drop the answer, reset at the end of the window, and the landed rule.

**The post-v1 backlog gains two items:** a per-scenario override of the overlap order, and a sampled per-request "never delivered" drop.

**Folded into DESIGN.md on acceptance:**
- §4.5: `direction` in the fault example and in the `Fields` column; the one-natural-way table; the live-connection and pause rules; `timeout` forwarding first; the overlap order.
- §6.6: the landed-check sentence that counts effects, not attempts.
- §6.7: the two deviation rows.
- §10: the overlap question leaves the open questions.
- PLAN.md: the open latency decision and the proxy 2/5 bullet are updated at the same time.

**What becomes possible:**
- a scenario that drops payments' answers (`timeout`), cuts them (`reset, direction: response`), or holds them (`blackhole, direction: response`), so the shop's reconciler and dedup are exercised on a schedule rather than by luck;
- `latency, direction: request` races a retry against the original.

**What becomes harder:**
- The proxy's two copy loops become a pipeline for each direction.
- A paused `blackhole` must track bytes it hasn't read.
- Overlapping faults need the fixed order.
- The applied counters need a direction label in the control service and the run record.

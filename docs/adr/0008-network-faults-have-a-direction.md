# ADR-0008: Network faults have a direction; `blackhole` becomes `partition`; `timeout` is dropped

Date: 2026-10-28
Status: Accepted (2026-11-26)

## Context

Kazu's planned network faults act on both directions of an edge, or on the connection as a whole. That leaves outcomes the shop's core invariant depends on either unreachable or reachable only one way:

- **A request that reaches the callee late.** The callee acts after the caller may already have given up. No fault produces this today.
- **Response loss with an error.** The callee does the work and the caller gets an immediate error. Today this happens only when a `down` resets a connection mid-request, which is luck of timing. Response loss *without* an error is already reachable: `latency` longer than the caller's client timeout gives it, and the shop's `payments_slow` scenario is built on exactly that.
- **What happens when a `blackhole` ends.** DESIGN §4.5 defines it as "accept, never answer" and doesn't say.

The shop's payment outcome rules are why these matter:
- 2xx means `paid`.
- A 4xx, or refused or 503-shed on every attempt, means `failed`.
- A timeout, reset or other 5xx leaves the order `pending`. The reconciler re-sends it under the same idempotency key, and payments deduplicates under an advisory lock on that key.

Research on 2026-10-28 found:
- **Toxiproxy** gives every fault a `stream`.
- **Chaos Mesh's** HTTPChaos has `target: Request | Response`.
- **Antithesis's** network faults "may be asymmetric".

PLAN.md carries the open decision "latency: delay request, response, or both?".

The maintainer settled the design on 2026-11-26, with one principle: **there is one, and only one, natural way to express each fault.**

## Decision

### The network and HTTP fault set

Each row produces a different combination of what the callee does and what the caller sees. That difference is the test for any fault added later.

| Fault | Mimics | What the callee sees | What the caller sees | `direction` | Applies to |
|---|---|---|---|---|---|
| `latency` | a slow dependency or a slow link | `response` (default): the request on time. `request`: the request `delay` late | the answer `delay` late. A `delay` longer than the caller's timeout makes it give up: **response loss, silent** | `request` or `response`, default `response` | every request in the window |
| `partition` | a network split that heals | nothing during the window; held traffic when it ends | silence both ways during the window. Held traffic is delivered on heal, including a request whose caller has since given up: its answer then goes to a closed socket, and a server that cancels work when its client disconnects may drop it | none (both ways) | every connection, live and new |
| `reset` | a peer crashing mid-conversation, or a load balancer or firewall killing connections | `request` (default): never sees the cut request. `response`: processed it, and its answer is cut off | `ECONNRESET` straight away. Under `response`: **response loss, with an error** | `request` or `response`, default `request` | a sampled share of requests (`rate`); on `tcp` edges, of connections |
| `bandwidth` | a congested link | data arrives throttled | large transfers slow; small requests barely change | `request`, `response` or `both`, default `both`. Under `both`, each direction gets its own `limit` (full duplex) | every connection in the window |
| `down` | a service that is unreachable or refusing connections | nothing: new connections refused, live ones reset | `ECONNREFUSED` on connect (**definitely not processed**); `ECONNRESET` on live connections | none | every connection in the window |
| `errors` (HTTP/gRPC) | a dependency returning errors | never sees the request; the proxy answers | the configured status (e.g. 503) or gRPC code | none | a sampled share of requests (`rate`) |

Response loss has exactly two forms: **silent** (`latency` past the caller's timeout) and **with an error** (`reset, direction: response`).

**`timeout` is dropped.** For any caller that has a client timeout, it duplicates `latency` past that timeout. All it adds is sampling a share of requests, so a `rate` on `latency` goes in the post-v1 backlog instead.

**`blackhole` is renamed `partition`.** A black hole loses what enters it, and this fault holds traffic and delivers it.

### `direction`

- **`request`** means the bytes the caller sends toward the callee, following the edge's `a -> b` arrow.
- **`response`** means the bytes the callee sends back.
- The names follow HTTP's meaning and work the same on a `tcp` edge.
- On a `service: b` target, `direction` applies to every declared inbound edge of `b`.

```yaml
- { edge: checkout -> payments, fault: latency, delay: 300ms }                       # answers late
- { edge: checkout -> payments, fault: latency, delay: 300ms, direction: request }   # requests arrive late
- { edge: checkout -> payments, fault: reset, rate: 5%, direction: response }        # work done, answer cut
- { edge: checkout -> payments, fault: partition, after: 20s, for: 10s }
```

**`latency`:**
- `delay` is the time added to a call, applied once, in the one chosen direction. Each chunk is due at the later of two times: its read time plus `delay` plus jitter, or the previous chunk's due time. So chunks never reorder, and a multi-chunk answer is late by about `delay`, not by `delay` times the number of chunks.
- It defaults to `response`: the callee acts on time and the answer is late, which is what "payments is slow" means.
- `request` delivers the request late. The callee may then act after the caller has given up. The caller's FIN follows the delayed request bytes, so a server that cancels a request's work when its client disconnects (Go's `net/http` does) may cancel that late request.
- There is no `both`. Splitting a delay across the two directions has no use, and slowing both streams is `bandwidth`.

**`reset`** takes `request` or `response`, defaulting to `request`. A `both` would differ from `request` only on a connection that already had a request in flight when the fault started, which depends on timing, so it isn't a second option.

**The schema rejects `direction`** on `partition`, `down`, `errors` and the process and resource faults:
- A partition is both ways by definition. A one-way hold would duplicate `latency` with a long delay.
- `down` is a state of the connection, and refused has to keep meaning "definitely not processed".
- `errors` never forwards anything.
- The process and resource faults have no stream.

### Live connections and partitions

**A network fault applies to live connections from the moment it starts.** Otherwise a pooled keep-alive connection would ride through it untouched.

**`partition` stops reading, in both directions.** The proxy builds no queue of its own:
- On a live connection, it holds the chunk its copy loop was already reading, if any. That chunk is how the landed check sees that something was held.
- It also holds whatever later stages already had: chunks waiting out a `latency` delay, writes queued by `bandwidth`, and an `errors` answer not yet written.
- It then stops reading, which leaves the rest in the operating system's TCP buffers.
- On a connection accepted during the window, it reads nothing and doesn't dial the callee until the window ends.

The effect on the senders:
- A small amount waits there.
- A large amount fills the buffers, and the sender's writes then block. A real sender does the same while TCP retransmits across a split network.

When the window ends, the proxy forwards what it held and reads again, so everything is delivered late and nothing is lost. A real partition behaves this way for any split shorter than TCP's give-up time (minutes); trial windows are seconds to minutes.

A caller that gives up during the window closes its connection gracefully, but the request it sent is still delivered on heal, followed by its FIN. A real network does the same: TCP retransmits the closed socket's send buffer. So a caller that retries under the same key during a partition puts several copies of the request into the buffers, and they all arrive together on heal. That is a direct test of the callee's deduplication.

Two known differences from a real partition:
- **Delivery is immediate on heal.** Real TCP delivers on its next retransmit, which can come seconds later.
- **New connections still connect, then hang.** The operating system completes the handshake before the proxy can act. A real partition makes `connect` itself hang. Faults on connection setup are in the backlog and need a packet-filter rule in the proxy's network namespace.

### Overlapping faults on one edge

Each direction of a connection is a pipeline. Bytes pass through these stages in order:

**`down` → `partition` (read gate) → `reset` → `errors` → `latency` → `bandwidth` → `partition` (write gate) → write**

- **`down`** gates the connection itself: it refuses new connections and resets live ones. Anything a partition held goes with it.
- **`partition`** is two gates. The read gate stops the proxy reading; the write gate holds everything the proxy would otherwise write in that direction, including bytes already inside `latency` or `bandwidth` and the proxy's own `errors` answers. Nothing crosses a partition.
- **`reset` comes before `errors`.** A request drawn by both is cut, not answered, so the caller gets `ECONNRESET` and the shop leaves the order `pending`. A `reset` with `response` cuts the connection before anything in the response direction is written, the proxy's own answers included.
- **`errors`** answers a drawn request itself, and never forwards it. Its answer enters the response direction at the `latency` stage.
- **`latency` and `bandwidth`** apply to what is written, *including the proxy's own error answers*. So `latency` (response) plus `errors` gives a **slow error**: the 503 arrives `delay` late, which is how a struggling dependency usually fails.

**Two faults of the same kind on one target are rejected when the config loads, unless they can't overlap.** This covers every fault type. The target is the edge for network and HTTP faults, and the service for process and resource faults (`crash`, `pause`, `cpu`, `memory`, `disk_full`). That includes the same kind in different directions; otherwise `bandwidth` with `request` plus `bandwidth` with `response` would be a second way to write `both`. Two latencies, two error rates or two CPU loads have no single obvious meaning, so the user picks one or sequences them. The rule:
- For a network fault, a `service: b` target counts as each of `b`'s inbound edges.
- Windows are half-open: one ending at 30s and one starting at 30s don't overlap.
- A fault with no `for` runs until traffic exits.
- Two faults whose windows both resolve to planned times (ADR-0004 resolves every wall-clock anchor when the plan is built) are compared as intervals.
- Two faults anchored on the same request-count fault are compared by their offsets.
- Two faults also can't overlap if one is sequenced after the other's end through `from:`, directly or through a chain or join. A link through `{ start: X }` counts only if X itself is sequenced after the first fault's end.
- Every other pair, including windows anchored on different request-count faults, is treated as able to overlap.

A per-scenario override of the stage order is in the post-v1 backlog.

### `reset` draws

On `http` and `grpc` edges, `reset` draws **per request**, from `(run seed, edge, request number)`, like every other sampled fault. A drawn request's connection is cut:
- under `request`, before that request is forwarded;
- under `response`, when the callee starts answering it.

How the client pools its connections doesn't change which requests are drawn.

On `tcp` edges there are no requests. There, `reset` draws each connection once, from `(run seed, edge, connection number)`, and cuts it on its next chunk in the selected direction. Connection numbers follow accept order, which depends on the client's pooling. So on `tcp` edges the draw is deterministic only while base and head open connections the same way, and DESIGN §4.5's seeding rule gains this exception.

### The landed check counts an effect, not an attempt

Each proxy keeps applied-fault counters per fault and per direction. They arrive in proxy 4/5, are read through the control service in 5/5, and are written to the run record. A fault is **applied** only when it changed what a side saw:

| Fault | Applied when |
|---|---|
| `latency` | a chunk in its direction was held for the delay |
| `partition` | it held at least one byte. On `grpc` and HTTP/2 edges, only HEADERS and DATA frames count; pings, SETTINGS and WINDOW_UPDATE frames don't |
| `reset` | a drawn request's connection (or, on `tcp` edges, a drawn connection) was cut. Under `response`, the callee had started answering |
| `bandwidth` | the limiter made at least one write in a selected direction wait |
| `down` | it refused a connection or reset a live one |
| `errors` | it answered a request with the configured error |

So a `response` reset against a callee that never answers, or a 5% `reset` that drew nothing, reports *not exercised*. Forwarding a request or accepting a connection never counts. For ADR-0011's `requests` condition, a request the proxy held or cut still counts as received.

### Alternatives considered

- **A separate `response_loss` fault kind.** It would need one kind per combination of fault and direction. Lost.
- **Keeping `timeout`.** It duplicates `latency` past the caller's timeout. Lost; a sampled `latency` goes in the backlog.
- **`partition` with a direction.** A one-way hold duplicates `latency` with a long delay. Lost.
- **`partition` discarding traffic.** A real network doesn't drop bytes from a live TCP stream, because TCP retransmits; dropping them would corrupt the stream. The honest way to throw traffic away is to kill the connection, which is `down` or `reset`. Lost.
- **Toxiproxy's `stream: upstream | downstream`.** It's the same capability. `request`/`response` say what the bytes are on an HTTP edge; Antithesis's `inbound`/`outbound` are relative to a node, not an edge. Lost on naming.
- **`delay` applied in each direction under `both`.** A call would pay it twice, which surprises anyone reading p99 numbers. Lost.
- **Antithesis's overlap rule (the more aggressive fault wins).** Lost to the pipeline, which is defined for every pair and gives slow errors.
- **Do nothing.** A late request and response loss with an error stay unreachable. Lost.

### Antithesis alignment

Antithesis's network faults can act in one direction ("inbound packets, outbound packets, or both"). Its *Jammed* fault queues packets and its *Stopped* fault drops them; under TCP, Stopped looks like a pause for any window shorter than TCP's give-up time. Kazu's `direction` follows the first point, and `partition` matches Jammed, and matches Stopped as TCP experiences it.

Deviations, recorded in DESIGN §6.7 on acceptance:
- **`delay` is added once per call, in one direction.** Antithesis's Slowed delays packets, so a round trip pays the delay in each direction. Kazu's `delay` then means the time added to a call, which is what users mean by "payments is slow".
- **Overlapping faults compose through a fixed pipeline.** Antithesis lets the more aggressive fault win for the overlap.
- **Two faults of the same kind can't overlap on one edge.** Antithesis lets network faults overlap on the same target.

`down` and `errors` have no Antithesis counterpart, since Antithesis injects no refused connections and no HTTP faults.

### Settled 2026-11-26

1. `blackhole` becomes `partition`. It has no direction, holds traffic both ways without a queue in the proxy, and delivers it when the window ends, including requests whose callers have given up.
2. Overlapping faults compose through the pipeline above. Two faults of the same kind on one edge are rejected unless they can't overlap. A per-scenario order override goes in the backlog.
3. `latency` takes `request` or `response`, defaulting to `response`. `delay` is the time added to a call, and there is no `both`.
4. `timeout` is dropped. A sampled `latency` is already in the backlog. A request that is never delivered at all is reachable only with a partition that has no `for`.
5. After review: `reset` takes `request` or `response` (default `request`), and draws per request on `http`/`grpc` edges.
6. Amended 2026-11-26: the same-kind overlap rule covers every fault type; the target is the edge for network and HTTP faults and the service for process and resource faults.

## Consequences

**Schema, `Config` and validation** (ADR-0002, decisions 10 and 12):
- The fault-kind enum drops `timeout` and renames `blackhole` to `partition`, as do the `FaultKind` constants and the per-kind branches.
- The fault object gains `direction`:
  - on `latency`: `request | response`, defaulting to `response`;
  - on `reset`: `request | response`, defaulting to `request`;
  - on `bandwidth`: `request | response | both`, defaulting to `both`;
  - on every other kind, it is rejected.
- Kazu's decoder fills in the defaults.
- The validator rejects same-kind overlap by the rule above.
- Until a kind's PR implements `direction`, the planner rejects it on that kind (ADR-0007).
- This lands as one small config PR ahead of proxy 2/5. It updates the schema tests, the type tests, the validator and its tests that use `timeout`, and the "did you mean" error list.

**PRs that change:**
- **Proxy 2/5 (`latency`):** delay plus jitter per chunk in the fault's `direction` (default `response`), seeded. Error answers the proxy writes later pass through it. This closes PLAN.md's open latency decision.
- **Proxy 3/5 (`down`):** asserts that `direction` is rejected and that `down` wins over every other fault.
- **Proxy 4/5 (fault program, applied counters):** the per-direction pipeline with the partition's read and write gates, the stage order, and counters with a direction label.
- **The PLAN bullet "proxy: `blackhole`, `reset`, `bandwidth`"** becomes "`partition`, `reset`, `bandwidth`", with the partition rules above and `reset`'s connection draw on `tcp` edges. The per-request draw on `http`/`grpc` edges needs the HTTP mode's request counter.
- **The PLAN bullet "proxy: `errors` (rate, code) and `timeout` (rate)"** becomes "`errors` (rate, code)".

**Post-v1 backlog:**
- already has a sampled `latency` ("per-request delay with a rate");
- gains a per-scenario override of the overlap order;
- its "per-connection probability for `latency` and `blackhole`" becomes "… `partition`".

**Folded in on acceptance:**
- **DESIGN §4.5:**
  - the fault table, with `partition` replacing `blackhole` ("hold both ways, deliver on heal") and the `timeout` row removed;
  - `direction` in the `Fields` column;
  - the live-connection, partition and pipeline rules;
  - the seeding bullet's exception for `reset` on `tcp` edges.
- **DESIGN §6.6:** the landed rule.
- **DESIGN §6.7:** the three deviation rows.
- **DESIGN §10:** the overlap question is removed.
- **PLAN.md:** the v1 scope row's fault list and the open latency decision.
- **SPEC.md:** the open questions "whether a latency fault delays the request or the response" and "whether `blackhole` lands in step 1 or 2 of the proxy" are closed.

**What becomes possible:** the shop's reconciler and dedup can be exercised on a schedule:
- `latency` past checkout's 1s timeout gives silent response loss;
- `reset, direction: response` gives response loss with an error;
- `partition` gives a split, then a burst of late deliveries when it heals;
- `latency, direction: request` delivers a request after checkout gave up.

**What becomes harder:**
- The proxy's two copy loops become a pipeline for each direction.
- A partition holds one read chunk plus whatever later stages already held, and must not leak anything the proxy writes.
- Same-kind overlap needs symbolic window comparison.
- Applied counters need a direction label in the control service and the run record.

# ADR-0008: Network faults have a direction; `timeout` forwards the request before withholding the response

Date: 2026-10-28
Status: Proposed

## Context

Kazu's faults cannot yet lose a response. Every planned network fault acts on both directions of an edge, or on the connection as a whole: `latency` delays both streams, `blackhole` swallows everything, `reset` cuts the connection, `down` refuses it. The HTTP `timeout` fault's definition in DESIGN.md §4.5 doesn't say whether the request reaches the callee before the caller is left hanging. None of them can produce the case where **the callee receives and processes the request and the caller never gets the answer**.

That case is the unknown-outcome bug class: a payment that happened but the caller can't tell, so it retries with a fresh key (double charge) or marks the order `failed` (a charge on a failed order). The shop's core invariant is built around it: 2xx → `paid`; 4xx, or refused or 503-shed on every attempt → `failed`; a timeout, reset or other 5xx → stays `pending` for the reconciler, which re-sends under the same idempotency key, and payments dedups under an advisory lock on that key. Kazu's test oracle expects Kazu to catch `TIMEOUT_AS_FAILURE` and `RETRY_NEW_KEY`, and today the only fault that produces an unknown outcome is a `down` that happens to reset a connection mid-request, by luck of timing.

Three independent research passes on 2026-10-28 (network tools: Toxiproxy, netem, Pumba; HTTP proxies: Envoy, Istio, Chaos Mesh; Antithesis, Jepsen, FoundationDB) each ranked this the most valuable missing fault. Toxiproxy gives every toxic a `stream` (`upstream` = client to server, `downstream` = server to client, the default). Antithesis treats incoming and outgoing packets independently, so "all network faults may be asymmetric", and a clog targets "inbound packets, outbound packets, or both". Chaos Mesh's HTTPChaos has `target: Request | Response`. PLAN.md carries an open decision for one corner of this, "latency: delay request, response, or both?", slated for proxy 2/5. The maintainer approved the mechanism in principle on 2026-10-28; this ADR pins it down.

## Decision

### A `direction` field on network faults

`latency`, `blackhole`, `reset` and `bandwidth` take an optional `direction`:

```yaml
- { edge: checkout -> payments, fault: blackhole, direction: response, after: 20s, for: 10s }
- { edge: checkout -> payments, fault: latency, delay: 300ms, direction: request }
```

- `request`: bytes the caller sends toward the callee (the edge's `a -> b` arrow).
- `response`: bytes the callee sends back to the caller.
- `both` (default): each stream independently, as a real link does.

The names are edge-relative and mean the same thing on a `tcp` edge, where the proxy sees only bytes: `request` is the caller's stream, `response` the callee's. On a `service: b` target the field applies to every declared inbound edge of `b` the same way.

What each fault does per direction:

| `fault` | `request` | `response` | `both` |
|---|---|---|---|
| `latency` | the caller's chunks are delayed; the callee's pass | the callee's chunks are delayed; the caller's pass | each stream is delayed. `delay` is one-way, so a call pays it twice: `delay: 300ms` turns a 10 ms call into roughly 610 ms, as a 300 ms link would |
| `blackhole` | the caller's bytes are read and discarded; nothing reaches the callee | the caller's bytes are forwarded and the callee processes them; its answer is read and discarded, and the caller's connection stays open with no bytes on it | everything is discarded, both ways |
| `reset` | a connection drawn for reset is cut on the caller's next chunk, before it is forwarded: the callee never saw it | a drawn connection is forwarded and cut on the callee's first answering chunk, before it is relayed: the callee did the work, the caller sees `ECONNRESET` | cut on the next chunk from either side; on request/response protocols this is nearly always the request |
| `bandwidth` | the caller's stream is throttled to `limit` | the callee's stream is throttled | each stream is throttled to `limit` independently, like a full-duplex link of that speed |

Two rules make a one-directional fault honest on a byte stream:

- **A fault applies to live connections from the moment it starts**, including `blackhole`. A fault that only touched new connections would let a pooled keep-alive connection ride through it, the way `down` must cut live connections for the same reason; and a `direction: response` blackhole that spared connections opened before it started would miss every pooled caller.
- **A connection that lost bytes to a `blackhole` is reset when the blackhole ends.** Resuming the stream would hand both sides a byte stream with a hole in it, which no network delivers (TCP retransmits across a healed partition; it never splices). Resetting is the honest analogue, and it makes `for` mean what it says: on every connection, the fault is over when its window ends, and a caller with no timeout of its own gets a definite `ECONNRESET` at the latest then.

Faults that do not take `direction`, and why:

- **`down`**: it is a connection-level state (refuse new connections with `ECONNREFUSED`, reset live ones), not a stream filter. A refused connection has no direction, and a link that passes packets one way only is `blackhole` with a direction, which the field already expresses. Giving `down` a direction would also blur the one outcome the shop depends on it for: refused means definitely not processed. The schema rejects `direction` on it.
- **`errors`**: the proxy answers the caller itself and never forwards, so there is no stream to pick. Rewriting a status the callee actually returned is a different fault (response rewrite), not decided here.
- **`timeout`**: it is a response-direction fault by definition, below.
- Process and resource faults (`crash`, `pause`, `cpu`, `memory`, `disk_full`): no stream.

### HTTP `timeout` forwards first

On a request drawn for `timeout` (by `rate`, from the seeded per-request PRNG), the proxy **forwards the request to the callee in full**, then **reads the callee's response and discards it as it arrives**, and sends the caller nothing. So the callee processes the request and the caller is left with an unanswered request: response loss at the HTTP layer.

What the caller sees: no bytes on the connection for that request. A caller with a client timeout gives up when it fires (the shop: 1 s per payments attempt, then a retry with the same key) and closes the connection; the proxy then closes the upstream side. A caller with no timeout hangs until the fault's `for` window ends, when the proxy resets every connection still holding a withheld response (the same rule as `blackhole`), so a hung request turns into `ECONNRESET` at the latest at the end of the fault. Either way the caller cannot tell whether the callee did the work, which is the point. The withheld response is never delivered: the proxy reads and drops it as it arrives, so the callee is not back-pressured and its handler completes normally.

`timeout` takes no `direction`. A per-request hang where the request is *not* forwarded is a different fault (the callee never saw it, so the honest caller outcome is "not processed", which `blackhole: { direction: request }` already produces per connection); nothing needs it per request today.

### The landed check counts a direction only when it changed what a side saw

Each proxy keeps applied-fault counters per fault and per direction (proxy 4/5, read through the control service in 5/5, written to the run record). A fault counts as **applied** only on an observable effect in a direction it selects. Forwarding the request, or accepting the connection, is never an application of a response-direction fault:

| `fault` | applied when |
|---|---|
| `latency` | a chunk in a selected direction was held for the delay |
| `blackhole` | a byte in a selected direction was read and withheld. For `response`, that means the callee actually answered and the answer was dropped; a callee that never answers gives zero and the scenario is *not exercised* |
| `reset` | a drawn connection was cut on a chunk in the selected direction. For `response`, the callee had started answering |
| `bandwidth` | the limiter made at least one write in a selected direction wait; a cap traffic never reaches has changed nothing |
| `timeout` | a drawn request's response arrived from the callee and was discarded. A drawn request whose callee never answered (the caller hung up first, or the callee is down) does not count: the proxy withheld nothing |

This is the product principle applied to direction: a missing signal is never a pass. A 5 % `timeout` on light traffic that drew no request, or a `response` blackhole against a callee that was itself down, reports *not exercised*, and `not_exercised: warn` exists for scenarios where that is expected.

### Alternatives considered

- **A separate `response_loss` fault kind.** One kind for one composition. Each of `blackhole`, `reset` and `latency` has a useful response-only form (lost answer, answer cut mid-stream, slow answers with fast requests), and `latency` has a useful request-only form (slow uploads); a new kind per combination would multiply the table, while one field on the existing kinds covers them all and resolves PLAN.md's latency question in passing. Lost.
- **Toxiproxy's per-toxic `stream: upstream | downstream`.** Semantically the same field; this decision adopts it and changes two things. The names: `upstream` means the client-to-server stream in Toxiproxy but *the server* in nginx, Envoy and most proxy vocabulary, so the same word reads opposite ways; `request`/`response` are unambiguous on an edge written `a -> b`. The default: Toxiproxy defaults to `downstream`; Kazu defaults to `both`, so a fault with no direction is the link failing, which matches Antithesis's symmetric default and the existing fault definitions. Antithesis's `inbound`/`outbound` were considered for the names too, and lost for being node-relative (inbound to whom?) on a field that lives on an edge.
- **Do nothing: faults always act on both streams.** The unknown-outcome class stays unreachable except by a lucky mid-request reset, so the shop's reconciler, the idempotency-key reuse and the advisory-lock dedup are never exercised by a scheduled fault, and `TIMEOUT_AS_FAILURE` and `RETRY_NEW_KEY` are caught only by accident. Lost.

### Antithesis alignment

Following Antithesis: its network faults are asymmetric by construction ("incoming and outgoing packets are treated independently, so all network faults may be asymmetric"; a clog is "inbound, outbound, or both"), and a fault with no direction given is symmetric. Kazu's field is the same capability on a per-edge proxy, named edge-relative rather than node-relative because Kazu targets edges. Not a deviation; no §6.7 row. `down` taking no direction isn't one either: Antithesis has no refused-connection fault (its "stopped" partition drops packets, which is Kazu's `blackhole`). `timeout` has no Antithesis counterpart to align with, since Antithesis injects no HTTP-layer faults.

## Consequences

**Schema** (ADR-0002, decisions 10 and 12): the fault object gains `direction`, enum `request | response | both`, default `both`; the per-kind branches allow it on `latency`, `blackhole`, `reset` and `bandwidth` and reject it everywhere else, since that is decidable inside one object. Kazu's decoder fills in the default. Until a kind's PR implements it, the planner rejects `direction` on that kind (ADR-0007).

**PRs that change:**

- a small config PR: schema, the `Fault` field, the default, table tests (rides ahead of proxy 2/5);
- **proxy 2/5 (`latency`)**: implements `direction` for latency; its PLAN bullet "delay plus jitter per chunk in both directions" becomes "per `direction`, default both", and the echo-server tests cover each direction. This closes PLAN.md's open latency decision;
- **proxy 3/5 (`down`)**: unchanged, and it asserts that `direction` is rejected;
- **proxy 4/5 (fault program, applied counters)**: counters carry a direction, and the landed rule above for `latency`;
- the **`blackhole`, `reset`, `bandwidth`** PR(s): each implements its direction column, the live-connection rule, and for `blackhole` the reset-at-end rule, with the per-direction landed rule;
- the **HTTP `timeout`** PR: forward-then-withhold, the read-and-drop of the upstream response, reset-at-end, and the landed rule (a discarded response, not a drawn request).

**Folded into DESIGN.md on acceptance**: `direction` joins the fault map example and the `Fields` column for the four kinds in §4.5 (no new column), with the per-direction table and the two rules; `timeout`'s row says it forwards first; §6.6's landed check gains the per-direction sentence. PLAN.md's open latency decision and the proxy 2/5 bullet are updated then (`/sync-plan`), and SPEC.md's open question on latency direction is closed with the same answer. Nothing in the shop's code changes.

**What becomes possible**: a scenario that loses payments' answers on purpose (`blackhole`, `direction: response` on `checkout -> payments`, or `timeout` at a rate), so the shop's reconciler and dedup are exercised by schedule rather than luck, and the `TIMEOUT_AS_FAILURE` and `RETRY_NEW_KEY` rows have a fault aimed at them.

**What becomes harder**: the proxy's two copy loops each become a per-direction pipeline (the natural shape for every fault here, but every network fault now has three cases to test); `delay` under the default `both` is paid twice per call, which the docs must say plainly; applied counters need a direction label in the control service's message and the run record.

**Open sub-questions for the maintainer**, each with the proposal above: whether `delay` under `both` should be one-way (paid twice per call, like a link) or per call; whether `timeout` should also take `direction: request` (hang without forwarding) now rather than when something needs it; whether a withheld-response connection should be reset at the fault's end or held until the caller closes.

# ADR-0012: Kazu builds its own proxy, and measures what the proxy added

Date: 2026-11-26
Status: Proposed

## Context

Kazu puts one proxy on every declared edge (DESIGN §6.6). Two questions about it were open.

**Build, adopt or fork?** Proxy 1/5 (#41) is a 138-line TCP forwarder on Go's `net`. Before four more proxy PRs and the HTTP and gRPC modes build on it, the choice between writing it, adopting Envoy or nginx, and forking Toxiproxy needs recording. DESIGN §9 risk 6 names HTTP/2, gRPC streaming and long-lived connections as the proxy's risk.

**Is the proxy ever the cause of what a run reports?** DESIGN makes two claims that nothing checks:
- §6.4: "Proxy overhead is sub-millisecond and paid equally by base and head."
- §11, "Go for the binary, not Rust": "Rust's speed edge cancels out because base and head pay the same proxy overhead."

The maintainer's framing: "for subtle faults the first thing people will ask is 'is it the framework?' — we need to offer proof it's not us on every run."

The bench job (#48) gives the first measurements, from a GitHub-hosted ubuntu runner with 4 vCPU, the same workload direct and through the proxy:

| Benchmark | Direct | Proxied |
|---|---|---|
| Throughput | 3077 MiB/s | 731 MiB/s |
| Round trip, 1 connection, p50 | 35µs | 63µs (+28µs per round trip) |
| Round trip, 64 connections, p50 | 332µs | 767µs |
| Connect | 175µs | 330µs (+155µs) |

- A round trip crosses the proxy twice, once in each direction.
- The 64-connection benchmark is a closed loop, so its p50 is mostly queueing behind the other connections; it shows that both paths are CPU-bound there, not a per-request cost.
- Connect costs more because the proxy dials the callee for every accepted connection.
- One known cost: `countingReader` wraps the source connection, which stops `io.Copy` using Linux `splice`.

So the idle overhead is tens of microseconds, and under load on a shared runner it is not a constant. The bench job runs on Kazu's PRs; it says nothing about a customer's run on a customer's runner.

## Decision

### 1. Kazu builds its own proxy

The hard parts are the Go standard library and `x/net`: `net` for TCP, and later `net/http`, `httputil.ReverseProxy` and `x/net/http2` for HTTP and HTTP/2 framing. Kazu writes the fault program, the seeding and the applied counters, which are the product.

**Alternatives considered.** The statements about Envoy and Toxiproxy below come from the maintainer's conversation of 2026-11-26 and the tool survey of 2026-10-28. As of 2026-11-26 they have not been re-checked against either project's source or documentation.

- **Adopt Envoy.** Lost.
  - Its fault filter has no time windows and no request-count triggers. A fault that starts `after` a point and lasts `for` a time would have to be switched on and off by runtime config pushes during the trial; Kazu loads a whole program before traffic starts (DESIGN §6.1).
  - Its fault draws are unseeded, so base and head can't be given the same faults from a run seed.
  - Its fault filter is HTTP-level (delay, abort, response rate limit). Nothing gives TCP refused-versus-reset, which the shop's outcome rules depend on (`down` must refuse; a reset means unknown).
  - The landed check needs applied effects per fault and per direction (ADR-0008). Envoy's fault statistics are believed to count injections per filter (`delays_injected`, `aborts_injected`); unverified.
  - It is a second, heavy image beside the `kazu` image.
- **Adopt nginx.** Lost: it has no fault injection.
- **Fork Toxiproxy.** Lost.
  - The design doesn't match. Toxics are added and removed by API calls at run time, so they race the traffic; there is no program loaded up front and no defined pipeline for overlapping faults. A fork would mean owning an imperative design and rebuilding its core.
  - Smaller costs: a toxic's `toxicity` is drawn from the unseeded global `math/rand`, per connection; it has no applied-fault counters; disabling a proxy likely closes with FIN, not RST.
  - Its fault list stays as inspiration; it is already in PLAN's post-v1 backlog.
- **Do nothing (leave it unrecorded).** The question returns with every proxy PR. Lost.

**When to revisit.** If the gRPC proxy work runs into HTTP/2 or gRPC edge cases that `x/net/http2` doesn't cover, reconsider Envoy for that mode. The proxy stays a separate process behind a gRPC interface, so one mode can be replaced without touching the rest.

### 2. The bench job is the evidence for Kazu's own changes, and stays advisory

- The table above is the baseline. The job compares base and head of each PR that touches the proxy and reports direct versus proxied.
- It never fails a PR on a number. It stores the raw results as a 90-day artifact, so runner noise can be measured before anything gates on it.
- **Follow-up, not decided here:** copy in fixed-size chunks with `io.CopyN` so `splice` applies again, counting per chunk. The bench job measures whether it pays. Two constraints on it:
  - Every active fault stage needs the bytes in user space, so `splice` can only ever apply while no fault is active on the connection.
  - It conflicts with decision 3 even there: bytes that `splice` moves have no read time and no write time. The follow-up has to show how overhead is still measured, or it is dropped.

### 3. Every run measures what the proxy added, and reports it

**The numbers have no effect on any of Kazu's own results.** Nothing is dropped, invalidated or failed because of them. They are shown so that "is it the framework?" can be answered from the run itself. Whether Kazu gates on them is deferred (decision 5).

Each proxy takes two measures in every trial, on base and on head.

**Forwarding overhead**, per chunk and per direction: the time from the chunk being free to go, to the proxy issuing its write. This definition is for the `tcp` forwarder.
- A chunk is free to go at the latest of three times: its read returned; the time the fault program meant to release it (a `latency` due time, the end of a `partition` hold, a `bandwidth` wait); and the previous write in that direction returned.
- The third term matters because reads and writes are decoupled once `latency` queues chunks. Without it, a chunk waiting behind a write that is blocked on a slow receiver would count against the proxy. A receiver that isn't reading blocks a direct sender the same way, so that wait is the system's behaviour.
- For the same reason the time the write itself blocks is left out.
- When `bandwidth` splits a chunk into several writes, the first write counts. The later ones are waits `bandwidth` meant to add.
- An answer the proxy writes itself (`errors`) is timed from when the answer is ready, by the same rule.
- A `latency` fault that releases a chunk 3 ms after it was due shows 3 ms of overhead.

**Accept lag**, per connection: from `Accept` returning to the connection being admitted to the fault program, which is the point where the program first sees it. A `partition` that then holds the connection and defers the upstream dial acts after that point, so a deliberate hold is never counted.

Each is kept as a histogram with a count, so a trial reports p50, p99 and max.

**What the two measures claim.** These are the time spent in the proxy's own processing. Waiting for a CPU before the proxy runs isn't seen.
- **Machine-level contention is treated as noise.** It hits base and head alike, and is absorbed by trial pairs and the per-run noise estimate (DESIGN §6.3).
- **If head itself starves the proxy by using more CPU, the added latency is attributed to head.** That is accepted: head made the system hungrier.

**Not measured:**
- anything before a read returns: the wake-up after data arrives, and bytes waiting in socket buffers;
- the wait in the kernel's accept queue before `Accept` returns;
- the upstream dial, which is most of the +155µs connect cost above, and mostly the callee's time;
- the extra hop itself (one more socket and one more copy), which is the constant part, paid alike by base and head and tracked by the bench job;
- a `reset` that cuts a connection before a write: that chunk produces no forwarding sample;
- `down`: it refuses before `Accept`, so a refused connection produces no accept-lag sample.

**Alternatives considered**
- **Go's scheduler-latency histogram, and OS run delay and CPU throttling from `/proc` and the cgroup.** They would show a starved proxy. Dropped: starvation is transient and hits both sides, it isn't a regression signal, and each needs machinery and caveats of its own.
- **Kernel receive timestamps (`SO_TIMESTAMPING`)**, to time a chunk from its true arrival. Rejected: more machinery than the problem justifies, and it carries its own risk of bias.

**Checks may read the numbers.** The two measures go into the per-trial metric store with the other proxy counters (DESIGN §6.8).
- No default check reads them.
- A user's `metric:` check over them is the user's own decision, under its own switch like any check. Kazu doesn't limit what a customer uses to judge right from wrong.

**Cost.** Two clock reads and a histogram update per chunk and per connection. The histogram updates cost more than the clock reads. The bench job measures the instrumented proxy: the PR that adds the timing shows its cost as that PR's base-versus-head difference.

### 4. Where it is shown

- **Terminal and PR comment:** every scenario line shows **proxy forwarding p99** for base and for head, for example `proxy forwarding p99: base 40µs, head 42µs`.
  - The figure is the largest forwarding-overhead p99 among the scenario's edges and trials on that side.
  - The p99 is over chunks, not requests.
  - With no samples it shows "no data", never 0.
  - Because it is a maximum over trials, it grows with the number of trials. It is not comparable between runs with different trial counts.
- **`--json` and the run record:** both measures in full (count, p50, p99, max), per edge, per trial, for base and head. They are read through the control service with the other counters.
- **A marker** on a figure above the display threshold (the sub-question below). It is `▲`, placed after the number it refers to (`head 2.1ms ▲`), and when accept lag is the one over the threshold the line names it. DESIGN §4.8 uses `⚠` at the start of a line for a result (*underpowered*); this marker never starts a line and is not a result. It is an informational heuristic, which principle 5 allows to warn. It changes no result and no exit code.

### 5. Gating is deferred to a later ADR

Whether and how Kazu's own results depend on these numbers is not decided here. A later ADR decides it once real runs exist. It covers the budget, a *proxy saturated* result, comparing saturation counts between base and head, what to do when a scenario has only one pair, how it works under `gate.fail_on: violation`, and the switch. Two reasons to wait:
- **Data first.** No trial on a customer-like runner has been measured, so any budget would be a guess, and a guessed gate fails people on noise.
- **Dropping or invalidating pairs has selection-bias traps.** If head saturates the proxy in its worst trials, dropping those pairs leaves head's best ones, and the scenario can pass because of what was removed. Counting saturated trials on each side before dropping avoids that, but it needs enough pairs to compare, which a single pair doesn't give, and a base-versus-head comparison, which `fail_on: violation` doesn't make.

The later ADR also starts from these:
- A fixed threshold is a heuristic. Under "fail only on facts" it can only warn, unless it is argued to be a fact.
- Overhead paid alike by both sides cancels in the paired difference. So the question for a gate is the base-versus-head difference, not the level.
- Which measure gates is itself a choice. Forwarding overhead alone misses a starved runner.

### Antithesis alignment

Antithesis owns the whole environment from a hypervisor, so it has no proxy and no proxy-overhead question. Kazu's proxies per declared edge are already a recorded deviation (DESIGN §6.7, first row). This ADR adds **no new deviation**: building the proxy is how that deviation is carried out, and measuring its overhead is a cost of it. Antithesis has made no decision for either problem that Kazu could take instead.

### Open sub-question (the maintainer settles it)

**1. The marker's threshold.**

Recommendation: 1 ms p99, for forwarding overhead and for accept lag. It is a display choice, not a budget: it decides where a marker appears and nothing else. It is revisited by the gating ADR, from the data the runs produce.

## Consequences

**Proxy PRs:**
- **Proxy 2/5 or 4/5:** the timing is part of the forwarding pipeline from the PR that first holds a chunk, not added afterwards. Each stage that delays a chunk on purpose reports when it meant to release it, so the pipeline can compute when the chunk was free to go. Recommended in 2/5, where `latency` introduces the first intended delay; at the latest in 4/5, with the pipeline. The bench job measures that PR's cost.
- **Proxy 5/5:** the control service returns the two measures with the counters.
- **Proxy HTTP mode and gRPC mode (Phase 4):** each defines its own timing points in its PR: headers parsed to upstream written, upstream read to written to the client, and the proxy's own answers from when they are ready. That PR must also decide how a request's wait for a pooled upstream connection in `http.Transport` is counted.
- **Run record and `--json`:** the two measures per edge, per trial, base and head.
- **Telemetry:** the two measures are written to the per-trial metric store with the other proxy counters.
- **Verdict output:** the proxy forwarding p99 on every scenario line, and the marker. No change to any result.
- **The `splice` follow-up** has to keep overhead measurable, or it is dropped.

**Folded in on acceptance:**
- **DESIGN §4.8:** the scenario line with proxy forwarding p99 for base and head, and the marker, distinct from the `⚠` of *underpowered*.
- **DESIGN §6.4:** "Proxy overhead is sub-millisecond and paid equally by base and head" becomes "Proxy overhead is measured and reported on every run", with what is not measured.
- **DESIGN §6.7:** nothing.
- **DESIGN §6.8:** the per-edge proxy metrics gain the two measures; no default check reads them.
- **DESIGN §9 risk 6:** adds "reconsider Envoy for the gRPC mode" beside the Rust rewrite.
- **DESIGN §11:** the "Go for the binary, not Rust" row gains an italic note, "*Assumption replaced by a per-run measurement: ADR-0012*". The choice of Go stands.
- **PLAN.md:** the proxy bullets that gain the timing, and the run-record, telemetry and verdict bullets.

**What becomes easier:** "is it the framework?" has an answer in every result for the proxy's own processing, from that run's own numbers, on the customer's runner. The gating ADR gets real data to decide from.

**What becomes harder:**
- Every pipeline stage has to account for the delay it intends.
- A proxy starved of CPU doesn't show in these numbers. On both sides it is noise; when head causes it, the latency counts against head.
- The docs have to say what is not measured.

# ADR-0012: Kazu builds its own proxy, and measures on every run what the proxy added

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

**The numbers have no effect on any result.** Nothing is dropped, invalidated or failed because of them. They are shown so that "is it the framework?" can be answered from the run itself. Whether to gate on them is deferred (decision 5).

Each proxy is one process on one edge. It takes five measures in every trial, on base and on head.

**Forwarding overhead**, per chunk and per direction: the time from the chunk being free to go, to the proxy issuing its write. This definition is for the `tcp` forwarder.
- A chunk is free to go at the latest of three times: its read returned; the time the fault program meant to release it (a `latency` due time, the end of a `partition` hold, a `bandwidth` wait); and the previous write in that direction returned.
- The third term matters because reads and writes are decoupled once `latency` queues chunks. Without it, a chunk waiting behind a write that is blocked on a slow receiver would count against the proxy. A receiver that isn't reading blocks a direct sender the same way, so that wait is the system's behaviour.
- For the same reason the time the write itself blocks is left out.
- When `bandwidth` splits a chunk into several writes, the first write counts. The later ones are waits `bandwidth` meant to add.
- An answer the proxy writes itself (`errors`) is timed from when the answer is ready, by the same rule.
- A `latency` fault that releases a chunk 3 ms after it was due shows 3 ms of overhead.

**Accept lag**, per connection: from `Accept` returning to the connection being admitted to the fault program, which is the point where the program first sees it.
- A `partition` that then holds the connection and defers the upstream dial acts after that point, so a deliberate hold is never counted.
- `down` refuses before `Accept`, so a refused connection produces no sample.
- The upstream dial is not counted: its duration is mostly the callee's.

**Go scheduler latency**, per process: Go's histogram of how long runnable goroutines waited to run (`runtime/metrics`, `/sched/latencies:seconds`), as the difference between a snapshot at the start of the trial and one at the end. Its caveats:
- the runtime samples it, about 1 in 8 goroutine transitions;
- it is a bucketed histogram, so p99 and max are bucket bounds, and the top bucket has no upper bound;
- it covers every goroutine in the process, the control service and the garbage collector included, not only the forwarding path.

**OS run delay**, per process: the time the proxy's threads spent runnable but waiting for a CPU, `run_delay` from `/proc/self/task/*/schedstat`, summed over threads, as a snapshot difference per trial.

**CPU throttling**, per container: `nr_throttled` and `throttled_usec` from the cgroup v2 `cpu.stat`, as a snapshot difference per trial.

The two OS measures are Linux-only, which is acceptable because runners are Linux. Where one can't be read (not Linux, cgroup v1, scheduler statistics disabled in the kernel), the result says "not available" for it, never zero.

**What each measure sees, and what none of them sees:**

| Measure | Sees | Doesn't see |
|---|---|---|
| Forwarding overhead | delay inside the proxy's pipeline after a read has returned: queueing between stages, timers firing late, a goroutine that wasn't run between read and write | anything before the read returns. On a connection with no fault active, read and write are back to back, so this stays near zero even on a starved runner |
| Accept lag | a new connection waiting for its handler to start | the wait in the kernel's accept queue before `Accept` returns |
| Go scheduler latency | goroutines waiting for one of Go's own scheduler slots, including the wake-up after data arrives, which happens before a read returns | the operating system's scheduling of the proxy's threads: run-queue delay, throttling, steal time |
| OS run delay | the proxy's threads waiting in the kernel's run queue | which request or chunk paid for it; threads that exited between the snapshots |
| CPU throttling | the proxy's container being stopped by its own CPU limit | a limit set on a parent cgroup |
| None | | bytes waiting in socket buffers before the proxy reads; the extra hop itself (one more socket and one more copy); steal time, when the runner is a virtual machine whose host is overcommitted |

So the five measures bound what the proxy's own code, its runtime and its share of the CPU added. They are evidence that the proxy wasn't the cause, not a proof that covers the kernel's network path.
- The extra hop is the constant part (the +28µs per round trip above), paid alike by base and head and tracked by the bench job.
- On Linux, `TCP_INFO` and the accept-queue depth could cover part of the kernel's network share. That isn't built until these measures prove insufficient.

The two timings and the Go histogram are kept as histograms with a count, so a trial reports p50, p99 and max. The two OS measures are totals per trial.

**Cost.** Two clock reads per chunk, two per connection, a histogram update per chunk and per connection, and per trial two snapshots each of the Go histogram, the threads' `schedstat` files and `cpu.stat`. The histogram updates cost more than the clock reads. The bench job measures the instrumented proxy: the PR that adds the timing shows its cost as that PR's base-versus-head difference.

### 4. Where it is shown

- **Terminal and PR comment:** every scenario line shows the proxy's p99 for base and for head. The figure is the largest forwarding-overhead p99 among the scenario's edges and trials on that side, for example `proxy p99: base 40µs, head 42µs`.
- **`--json` and the run record:** every number, per edge, per trial, for base and head. They are read through the control service with the other counters.
- **A ⚠ marker** on a value above the display threshold (sub-question 1). When any of a scenario's five measures is marked, its line carries the marker and names the measure and the edge. The marker is an informational heuristic, which principle 5 allows to warn. It changes no result and no exit code.

### 5. Gating is deferred to a later ADR

Whether and how these numbers affect a result is not decided here. A later ADR decides it once real runs exist. It covers the budget, a *proxy saturated* result, comparing saturation counts between base and head, what to do when a scenario has only one pair, how it works under `gate.fail_on: violation`, and the switch. Two reasons to wait:
- **Data first.** No trial on a customer-like runner has been measured, so any budget would be a guess, and a guessed gate fails people on noise.
- **Dropping or invalidating pairs has selection-bias traps.** If head saturates the proxy in its worst trials, dropping those pairs leaves head's best ones, and the scenario can pass because of what was removed. Counting saturated trials on each side before dropping avoids that, but it needs enough pairs to compare, which a single pair doesn't give, and a base-versus-head comparison, which `fail_on: violation` doesn't make. The later ADR starts from these.

### Antithesis alignment

Antithesis owns the whole environment from a hypervisor, so it has no proxy and no proxy-overhead question. Kazu's proxies per declared edge are already a recorded deviation (DESIGN §6.7, first row). This ADR adds **no new deviation**: building the proxy is how that deviation is carried out, and measuring its overhead is a cost of it. Antithesis has made no decision for either problem that Kazu could take instead.

### Open sub-question (the maintainer settles it)

**1. The ⚠ threshold.**

Recommendation: 1 ms p99 per edge for forwarding overhead, accept lag and Go scheduler latency; 1% of the trial's duration for OS run delay and for throttled time.
- 1 ms is where §6.4's "sub-millisecond" claim sat, and about 35 times the +28µs per round trip of an idle proxy.
- It is a display choice, not a budget: it decides where a marker appears and nothing else. It is revisited with the gating ADR, from the data the runs produce.

## Consequences

**Proxy PRs:**
- **Proxy 2/5 or 4/5:** the timing is part of the forwarding pipeline from the PR that first holds a chunk, not added afterwards. Each stage that delays a chunk on purpose reports when it meant to release it, so the pipeline can compute when the chunk was free to go. Recommended in 2/5, where `latency` introduces the first intended delay; at the latest in 4/5, with the pipeline. The bench job measures that PR's cost.
- **Proxy 5/5:** the control service returns the five measures with the counters.
- **Proxy HTTP mode and gRPC mode (Phase 4):** each defines its own timing points in its PR: headers parsed to upstream written, upstream read to written to the client, and the proxy's own answers from when they are ready. That PR must also decide how a request's wait for a pooled upstream connection in `http.Transport` is counted.
- **Run record and `--json`:** the five measures per edge, per trial, base and head.
- **Verdict output:** the proxy p99 on every scenario line, and the ⚠ marker. No change to any result.
- **The `splice` follow-up** has to keep overhead measurable, or it is dropped.

**Folded in on acceptance:**
- **DESIGN §4.8:** the scenario line with the proxy's p99 for base and head, and the marker.
- **DESIGN §6.4:** "Proxy overhead is sub-millisecond and paid equally by base and head" becomes "Proxy overhead is measured and reported on every run", with what the measures don't see.
- **DESIGN §6.7:** nothing.
- **DESIGN §6.8:** the per-edge proxy metrics gain the five measures.
- **DESIGN §9 risk 6:** adds "reconsider Envoy for the gRPC mode" beside the Rust rewrite.
- **DESIGN §11:** the "Go for the binary, not Rust" row gains an italic note, "*Assumption replaced by a per-run measurement: ADR-0012*". The choice of Go stands.
- **PLAN.md:** the proxy bullets that gain the timing, and the run-record and verdict bullets.

**What becomes easier:** "is it the framework?" has an answer in every result, from that run's own numbers, on the customer's runner, with its limits stated. The gating ADR gets real data to decide from.

**What becomes harder:**
- Every pipeline stage has to account for the delay it intends.
- Until gating is decided, a run whose proxy was starved still gives its verdict, with a marker beside it. The reader has to look.
- The measures have a blind spot in the kernel's network path, and the docs have to say so.

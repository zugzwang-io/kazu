# ADR-0012: Kazu builds its own proxy, and proves on every run that the proxy isn't the cause

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
| Round trip, 1 connection, p50 | 35µs | 63µs (about +28µs per hop) |
| Round trip, 64 connections, p50 | 332µs | 767µs |
| Connect | 175µs | 330µs (+155µs) |

- The 64-connection benchmark is a closed loop, so its p50 is mostly queueing behind the other connections; it shows that both paths are CPU-bound there, not a per-request cost.
- Connect costs more because the proxy dials the callee for every accepted connection.
- One known cost: `countingReader` wraps the source connection, which stops `io.Copy` using Linux `splice`.

So the idle overhead is tens of microseconds, and under load on a shared runner it is not a constant. The bench job runs on Kazu's PRs; it says nothing about a customer's run on a customer's runner.

## Decision

### 1. Kazu builds its own proxy

The hard parts are the Go standard library and `x/net`: `net` for TCP, and later `net/http`, `httputil.ReverseProxy` and `x/net/http2` for HTTP and HTTP/2 framing. Kazu writes the fault program, the seeding and the applied counters, which are the product.

**Alternatives considered**

- **Adopt Envoy.** Lost.
  - Its fault filter draws unseeded, so base and head can't be given the same faults from a run seed.
  - Faults are controlled by runtime config pushes, which race the traffic. Kazu loads a program before traffic starts (DESIGN §6.1).
  - Its fault filter is HTTP-level (delay, abort, response rate limit). Nothing gives TCP refused-versus-reset, which the shop's outcome rules depend on (`down` must refuse; a reset means unknown).
  - Its fault statistics count injections per filter. The landed check needs applied effects per fault and per direction (ADR-0008).
  - It is a second, heavy image beside the `kazu` image.
- **Adopt nginx.** Lost: it has no fault injection.
- **Fork Toxiproxy.** Lost.
  - A toxic's `toxicity` is drawn from the unseeded global `math/rand`, per connection.
  - Toxics are added and removed by API calls at run time, so they race the traffic.
  - It has no applied-fault counters, and disabling a proxy likely closes with FIN, not RST.
  - A fork would mean owning an imperative design that doesn't match a program loaded up front.
  - Its fault list stays as inspiration; it is already in PLAN's post-v1 backlog.
- **Do nothing (leave it unrecorded).** The question returns with every proxy PR. Lost.

**When to revisit.** If the gRPC proxy work runs into HTTP/2 or gRPC edge cases that `x/net/http2` doesn't cover, reconsider Envoy for that mode. The proxy stays a separate process behind a gRPC interface, so one mode can be replaced without touching the rest.

### 2. The bench job is the evidence for Kazu's own changes, and stays advisory

- The table above is the baseline. The job compares base and head of each PR that touches the proxy and reports direct versus proxied.
- It never fails a PR on a number. It stores the raw results as a 90-day artifact, so runner noise can be measured before anything gates on it.
- **Follow-up, not decided here:** copy in fixed-size chunks with `io.CopyN` so `splice` applies again, counting per chunk. The bench job measures whether it pays. It conflicts with decision 3: bytes that `splice` moves never pass through user space, so a chunk has no read time and no write time to subtract. That follow-up has to show how overhead is still measured, or it is dropped.

### 3. Every proxy measures its own overhead on every run, and the run reports it

**What each proxy measures**, per edge, in every trial, on base and on head:

- **Forwarding overhead**, per chunk and per direction: the time from the proxy's read of the chunk returning to the proxy issuing its write, minus any delay the fault program meant to add (a `latency` hold, a `bandwidth` wait, a `partition` hold). A `latency` fault that releases a chunk 3 ms after it was due shows 3 ms of overhead.
  - The time the write itself blocks is left out. A receiver that isn't reading blocks a direct sender the same way, so it is the system's behaviour, not the proxy's.
- **Accept lag**, per connection: how long a connection that is ready waits before the proxy takes it. The proxy measures the part it can see: from `Accept` returning to the forwarding of that connection starting (the upstream dial being issued).

Each is kept as a histogram with a count, so a trial reports p50, p99 and max.

**Reporting.** The numbers are read through the control service with the other counters and written to the run record, per edge, per trial, for base and head. The `--json` result always carries them. The result can then say, for example, "proxy added p99 40µs, max 2ms on `checkout -> payments`".

**Fail on facts.** A trial pair is **invalid**, with the reason *proxy saturated*, when on any edge:
- the overhead on base or on head exceeds a budget, or
- the overhead differs between base and head by enough to matter.

An invalid pair gives no verdict in either direction: not a pass, not a regression. That covers every check in the pair, not only latency checks, because delay the proxy adds can change behaviour (a caller that times out and retries), not just a measured number. It has a config switch, like every failure.

The budget, the name of the result and the place of the switch are the open sub-questions below.

**Limits, stated in the docs and in the result:**
- User space can't see time a connection spends in the kernel's accept queue, or time bytes spend in socket buffers before the proxy reads them. On Linux, `TCP_INFO` and the accept-queue depth could cover part of that; it is not built until the measured part proves insufficient.
- The extra hop itself (one more socket and one more copy, the +28µs above) is outside this measurement. It is the constant part, paid alike by base and head and tracked by the bench job; the per-run measurement covers the part that varies with load.
- The upstream dial is not counted as accept lag: its duration is mostly the callee's.
- The measurement costs two clock reads per chunk and one per connection, tens of nanoseconds each on Linux. The bench job confirms it.

### Antithesis alignment

Antithesis owns the whole environment from a hypervisor, so it has no proxy and no proxy-overhead question. Kazu's proxies per declared edge are already a recorded deviation (DESIGN §6.7, first row). This ADR adds **no new deviation**: building the proxy is how that deviation is carried out, and measuring its overhead is a cost of it. Antithesis has made no decision for either problem that Kazu could take instead.

### Open sub-questions (the maintainer settles each)

**1. The budget: its default, and whether it is absolute, relative to base, or both.**

Recommendation: an absolute budget now; the base-versus-head rule later, once there is data.
- **Absolute, default p99 of 1 ms** per edge per trial, for forwarding overhead and for accept lag, with max reported but not judged. "Measured overhead exceeded the configured budget" is a fact. 1 ms turns §6.4's "sub-millisecond" from a claim into a check, and it is about 35 times the +28µs a whole round trip pays through an idle proxy. The number is provisional: no trial on a customer-like runner has been measured yet.
- **Not a ratio to base.** At tens of microseconds, 60µs against 30µs is double and means nothing, and a base that is itself saturated excuses nothing.
- **The base-versus-head difference is reported from the start and gated later.** An absolute budget already bounds it: two values under 1 ms differ by less than 1 ms. A tighter rule only matters when a latency check's tolerance is finer than that, and "enough to matter" then depends on that tolerance, which makes it a judgement the verdict method has to define (DESIGN §10, spike before Phase 5). A share-of-tolerance rule is a heuristic in principle 5's sense, so it should warn until it can be stated as measured-against-configured.
- Until the gate exists, proxies report only. The shop's runs on GitHub runners then show what a normal trial looks like, and the default is confirmed or changed from that data.

**2. The result name: a new *invalid* / *proxy saturated* result, or an existing one.**

Recommendation: no new scenario result. *Invalid (proxy saturated)* is a status of a trial pair in the run record; the scenario's result reuses **underpowered**.
- An invalid pair is dropped and replaced within the trial budget, as adaptive trials already add pairs while undecided (DESIGN §6.3). A scheduler stall on a shared runner is transient, so most runs recover with one more pair and a note.
- If the budget runs out with too few valid pairs, the comparison couldn't be decided, which is what *underpowered* already means. ADR-0011 already reports "no pairs to compare" as underpowered. The reason line carries the cause: "proxy saturated on `checkout -> payments` in 4 of 6 pairs (head p99 3.1 ms, base p99 60µs); use a larger runner or less load".
- **Not *not exercised*.** That means the fault or the check's precondition never happened, and its fixes (resolver, traffic, trigger) are the wrong advice here.
- **The cost of reusing it:** *underpowered* warns by default. A head that saturates the proxy in every pair (for example by amplifying retries) then passes with a warning, unless the user sets `gate.underpowered: fail`. The reason line names which side saturated and the per-edge request counts, so it is visible, but it does not fail. If saturation should fail by default, that needs a new result (*invalid*) with its own switch defaulting to `fail`; that is the alternative.
- Trial pairs and *underpowered* arrive in Phase 5. Until then a saturated trial is reported in `--json` and not gated.

**3. Where the switch lives.**

Recommendation: reuse `gate.underpowered` (`warn` | `fail`) as the switch, and add one key for the budget, `gate.proxy_overhead: 1ms`.
- It follows from sub-question 2: one result, one switch.
- No per-scenario or per-edge override. Saturation is a property of the runner and the load, not of a check, so the three-level override that `not_exercised` has is not needed until someone asks.
- `gate` is not in the published schema yet (it joins in Phase 5), so nothing changes in the schema now.

## Consequences

**Proxy PRs:**
- **Proxy 2/5 or 4/5:** the overhead timing is part of the forwarding pipeline from the PR that first holds a chunk, not added afterwards. Each stage that delays a chunk on purpose reports how long it meant to, so the pipeline can subtract it. Recommended in 2/5, where `latency` introduces the first intended delay; at the latest in 4/5, with the pipeline.
- **Proxy 5/5:** the control service returns the two histograms with the counters.
- **Run record and `--json`:** per edge, per trial, base and head: count, p50, p99 and max of forwarding overhead and accept lag.
- **Verdict (Phase 5):** the invalid-pair rule, as the sub-questions are settled.
- **The `splice` follow-up** has to keep overhead measurable, or it is dropped.

**Folded in on acceptance:**
- **DESIGN §6.4:** "Proxy overhead is sub-millisecond and paid equally by base and head" becomes "Proxy overhead is measured per run and reported per edge for base and head", with the limits above.
- **DESIGN §6.6:** the proxy's counters gain the two measures.
- **DESIGN §9 risk 6:** adds "reconsider Envoy for the gRPC mode" beside the Rust rewrite.
- **DESIGN §4.4 and §4.8:** the `gate` block and the example output, as sub-questions 2 and 3 are settled.
- **DESIGN §6.7:** nothing.
- **DESIGN §11** is frozen. Its "Go for the binary, not Rust" row rests on base and head paying the same overhead; this ADR replaces that assumption with a measurement and leaves the choice of Go as it is.
- **PLAN.md:** the proxy bullets that gain the timing, and the run-record and verdict bullets.

**What becomes easier:** "is it the framework?" has an answer in every result, from that run's own numbers, on the customer's runner.

**What becomes harder:**
- Every pipeline stage has to account for the delay it intends.
- A noisy runner can now produce invalid pairs, which costs trials. The report-only period measures how often.
- The measurement has a blind spot in the kernel, and the docs have to say so.

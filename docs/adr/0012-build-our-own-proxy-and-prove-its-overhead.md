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

### 3. Every proxy measures what it added on every run, and the run reports it

Each proxy is one process on one edge. It takes three measures in every trial, on base and on head. The definitions below are for the `tcp` forwarder. The HTTP and gRPC modes define their own timing points in the PR that builds them (headers parsed to upstream written; upstream read to written to the client), under the same rules.

**Forwarding overhead**, per chunk and per direction: the time from the chunk being free to go, to the proxy issuing its write.
- A chunk is free to go at the latest of three times: its read returned; the time the fault program meant to release it (a `latency` due time, the end of a `partition` hold, a `bandwidth` wait); and the previous write in that direction returned.
- The third term matters because reads and writes are decoupled once `latency` queues chunks. Without it, a chunk waiting behind a write that is blocked on a slow receiver would count against the proxy. A receiver that isn't reading blocks a direct sender the same way, so that wait is the system's behaviour.
- For the same reason the time the write itself blocks is left out.
- When `bandwidth` splits a chunk into several writes, the first write counts. The later ones are waits `bandwidth` meant to add.
- An answer the proxy writes itself (`errors`) is timed from when the answer is ready, by the same rule.
- A `latency` fault that releases a chunk 3 ms after it was due shows 3 ms of overhead.

**Accept lag**, per connection: from `Accept` returning to the connection being admitted to forwarding, which is the point where the fault program first sees it. A `partition` that then holds the connection and defers the upstream dial, or a `down` that refuses it, acts after that point, so a deliberate hold is never counted.

**Scheduler latency**, per process: Go's own histogram of how long runnable goroutines waited to run (`runtime/metrics`, `/sched/latencies:seconds`), taken as the difference between a snapshot at the start of the trial and one at the end.

**What each measure sees, and what none of them sees:**

| Measure | Sees | Doesn't see |
|---|---|---|
| Forwarding overhead | delay inside the proxy's pipeline after a read has returned: queueing between stages, timers firing late, a goroutine that wasn't run between read and write | anything before the read returns. On a connection with no fault active, read and write are back to back, so this stays near zero even on a starved runner |
| Accept lag | a new connection waiting for its handler to start | the wait in the kernel's accept queue before `Accept` returns |
| Scheduler latency | the proxy's goroutines waiting for a CPU, including the wake-up after data arrives, which happens before a read returns | a running thread the operating system has descheduled; kernel time |
| None | | bytes waiting in socket buffers before the proxy reads; the extra hop itself (one more socket and one more copy) |

So the three measures bound what the proxy's own user-space code and runtime added. They are evidence that the proxy wasn't the cause, not a proof that covers the kernel.
- The extra hop is the constant part (the +28µs per round trip above), paid alike by base and head and tracked by the bench job.
- On Linux, `TCP_INFO` and the accept-queue depth could cover part of the kernel's share. That isn't built until the three measures prove insufficient.
- The upstream dial is not counted as accept lag: its duration is mostly the callee's.

Each measure is kept as a histogram with a count, so a trial reports p50, p99 and max.

**Reporting.** The numbers are read through the control service with the other counters and written to the run record, per edge, per trial, for base and head. The `--json` result always carries them. The result can then say, for example, "proxy added p99 40µs, max 2ms on `checkout -> payments`".

**Fail on facts.** A trial is **saturated** when, on any edge, a measure exceeds a budget. A trial pair with a saturated side, or whose proxy measures differ between base and head by enough to matter, gives no verdict from its checks in either direction: not a pass, not a regression.
- That covers every check in the pair, not only latency checks, because delay the proxy adds can change behaviour (a caller that times out and retries), not just a measured number.
- The checks' results from such a pair are still reported for information, as ADR-0011 does for a check's decision in an ineligible trial.
- What the scenario's result then is, is sub-question 2. Whatever it is, it has a config switch, like every failure.

**Cost.** Two clock reads per chunk, two per connection, two snapshots of the scheduler histogram per trial, and a histogram update per chunk and per connection. The updates cost more than the clock reads. The bench job measures the instrumented proxy: the PR that adds the timing shows its cost as that PR's base-versus-head difference.

### Antithesis alignment

Antithesis owns the whole environment from a hypervisor, so it has no proxy and no proxy-overhead question. Kazu's proxies per declared edge are already a recorded deviation (DESIGN §6.7, first row). This ADR adds **no new deviation**: building the proxy is how that deviation is carried out, and measuring its overhead is a cost of it. Antithesis has made no decision for either problem that Kazu could take instead.

### Open sub-questions (the maintainer settles each)

**1. The budget: its default, and whether it is absolute, relative to base, or both.**

Recommendation: an absolute budget now; the base-versus-head rule later, once there is data.
- **Absolute, default p99 of 1 ms** per edge per trial, for each of the three measures, with max reported but not judged. "Measured overhead exceeded the configured budget" is a fact. 1 ms turns §6.4's "sub-millisecond" from a claim into a check, and it is about 35 times the +28µs per round trip of an idle proxy. The number is provisional: no trial on a customer-like runner has been measured yet.
- **A minimum sample count.** With one pooled connection, accept lag's p99 is a single sample, which is its max. A measure is judged only above a minimum number of samples (to be set from the data, 100 as a starting point); below it the measure is reported and not judged.
- **Not a ratio to base.** At tens of microseconds, 60µs against 30µs is double and means nothing, and a base that is itself saturated excuses nothing.
- **The base-versus-head difference in the measures is reported from the start and gated later.** An absolute budget already bounds it: two values under 1 ms differ by less than 1 ms. A tighter rule only matters when a latency check's tolerance is finer than that, and "enough to matter" then depends on that tolerance, which makes it a judgement the verdict method has to define (DESIGN §10, spike before Phase 5). A share-of-tolerance rule is a heuristic in principle 5's sense, so it should warn until it can be stated as measured-against-configured.
- **A report-only period first.** Until the default is confirmed, proxies report and nothing gates. The shop's runs on GitHub runners show what a normal trial looks like. They must include trials with a `latency` fault, where Go timers firing late will dominate forwarding overhead. The gate is switched on once that data confirms or changes the default: at the earliest with the Phase 3 verdict, at the latest with trial pairs in Phase 5.

**2. What a saturated trial does to the scenario's result.**

Recommendation: a new result, **proxy saturated**, with its own switch, following ADR-0011's rule for eligibility counts. Not *underpowered*, and not *not exercised*.

Saturation is a measured fact about a trial, so it can't be dropped quietly. Dropping saturated pairs and judging the rest would also bias the verdict: if head saturates the proxy in its worst trials, the pairs that remain are head's best ones, and the scenario could pass. So saturated trials are counted before anything is dropped:

- **Head saturated in more trials than base:** a candidate regression (head puts more through the edge than base did, on the same proxy, traffic and runner). It is judged by the base-versus-head comparison on the saturated-trial counts, as ADR-0011 judges eligible-trial counts, so `gate.fail_on` and `flaky` govern it like any other regression. The reason line names the edge, the counts and the per-edge request counts on each side.
- **Saturated on both sides alike, or one transient pair:** when that comparison finds no difference between base and head, the saturated pairs are dropped and replaced within the trial budget, as adaptive trials already add pairs while undecided (DESIGN §6.3). The scenario's checks are then judged on the valid pairs.
- **Still undecided when the budget runs out, or saturated in every pair:** the scenario's result is **proxy saturated**. It fails by default, as a missing signal does, with the reason: "proxy saturated on `checkout -> payments` in 4 of 6 pairs (head p99 3.1 ms, base p99 60µs); use a larger runner or less load".
- **The roll-up** (DESIGN §4.6) gains it beside *not exercised*: REGRESSED if any check regressed or head saturated more than base; otherwise *not exercised* or *proxy saturated* if either is present at `fail` severity; otherwise pass.

Alternatives:
- **Reuse *underpowered*.** Lost. It warns by default, so a head that saturates the proxy in every pair would pass with a warning, and the selection bias above would be reported as noise. Sharing its switch would also force anyone who wants saturation to fail to fail on noise too.
- **Reuse *not exercised*.** Lost. It means the fault or the check's precondition never happened, and its fixes (resolver, traffic, trigger) are the wrong advice here.

**3. Where the switch lives.**

Recommendation: `gate.proxy_saturated: fail | warn`, default `fail`, and one key for the budget, `gate.proxy_overhead: 1ms`.
- Its own switch, for the reason above.
- No per-scenario or per-edge override. Saturation is a property of the runner and the load, not of a check, so the three-level override that `not_exercised` has is not needed until someone asks.
- `gate` is not in the published schema yet (it joins in Phase 5), so nothing changes in the schema now.

**4. Is *underpowered* a pass? CLAUDE.md and DESIGN disagree.**

- CLAUDE.md's working agreements say: "a missing signal — not exercised, no data, underpowered — is never a pass".
- DESIGN says otherwise in three places. Principle 5 lists the missing signals without *underpowered* and treats it separately ("a comparison too noisy to decide is reported as such, never guessed"). §4.4 has `gate.underpowered: warn`. §4.6's roll-up reports underpowered "beside the roll-up", which can be a pass.

This ADR doesn't depend on the answer, since saturation gets its own result. Recommendation: DESIGN is the design authority and its wording is deliberate (§11 records "underpowered, a warning"), so CLAUDE.md's line is corrected to match it, in a setup PR of its own. The other way to resolve it is to make `gate.underpowered` default to `fail` and put *underpowered* in the roll-up, which is a product decision with its own ADR.

## Consequences

**Proxy PRs:**
- **Proxy 2/5 or 4/5:** the timing is part of the forwarding pipeline from the PR that first holds a chunk, not added afterwards. Each stage that delays a chunk on purpose reports when it meant to release it, so the pipeline can compute when the chunk was free to go. Recommended in 2/5, where `latency` introduces the first intended delay; at the latest in 4/5, with the pipeline. The bench job measures that PR's cost.
- **Proxy 5/5:** the control service returns the three histograms with the counters.
- **Proxy HTTP mode and gRPC mode (Phase 4):** each defines its own timing points in its PR: headers parsed to upstream written, upstream read to written to the client, and the proxy's own answers from when they are ready.
- **Run record and `--json`:** per edge, per trial, base and head: count, p50, p99 and max of each measure.
- **Verdict:** the saturated-trial rule, as the sub-questions are settled.
- **The `splice` follow-up** has to keep overhead measurable, or it is dropped.

**Folded in on acceptance:**
- **DESIGN §4.4 and §4.8:** the `gate` keys and the example output, as sub-questions 2 and 3 are settled.
- **DESIGN §4.6:** the roll-up gains *proxy saturated*.
- **DESIGN §6.1:** "no verdict on a fault that didn't land …" gains "or on a trial pair whose proxy was saturated".
- **DESIGN §6.3:** saturated pairs are counted, then dropped and replaced within the trial budget.
- **DESIGN §6.4:** "Proxy overhead is sub-millisecond and paid equally by base and head" becomes "Proxy overhead is measured per run and reported per edge for base and head", with what the measures don't see.
- **DESIGN §6.6:** the proxy's counters gain the three measures.
- **DESIGN §6.7:** nothing.
- **DESIGN §6.8:** the per-edge proxy metrics gain the three measures.
- **DESIGN §9 risk 6:** adds "reconsider Envoy for the gRPC mode" beside the Rust rewrite.
- **DESIGN §11:** the "Go for the binary, not Rust" row gains an italic note, "*Assumption replaced by a per-run measurement: ADR-0012*". The choice of Go stands.
- **PLAN.md:** the proxy bullets that gain the timing, and the run-record and verdict bullets.

**What becomes easier:** "is it the framework?" has an answer in every result, from that run's own numbers, on the customer's runner, with its limits stated.

**What becomes harder:**
- Every pipeline stage has to account for the delay it intends.
- A noisy runner can now produce saturated trials, which cost trials and can fail a scenario. The report-only period measures how often.
- The measures have a blind spot in the kernel, and the docs have to say so.

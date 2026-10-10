# Round 0 audit: contaminated, not counted

An independent agent audited round 0 (this directory's brief, response and edits, and SPEC.md's value matrix). Its findings mean round 0 cannot support any catch-rate claim.

**Blinding failures**
- The brief leaked hints: a purpose-built `DUPLICATE_RATE` "double-click" traffic knob (what the race bug needs), the timeout path labelled "outcome unknown", and emphasis on the reconciler's "original key".
- The shop's bespoke telemetry (`shop.payments.attempts` "including retries", `shop.db.pool.acquire`, `shop.ledger.lag`) was designed alongside the bugs, and several predicted catches come straight from it.
- Bug definitions were written before the response arrived but not committed first, so ground rule 4 can't be verified.
- Two allowed edits (#2 `peak_2x` rate, #3 TLS) were made by someone who knows the bugs, and #2 decides the `NO_SHEDDING` outcome.
- BRIEF.md summarised Kazu's capabilities instead of quoting what the author saw.

**Prediction problems**
- `NO_SHEDDING` (V10) is probably a miss: without the limit, head may look better than base.
- `UNBOUNDED_RETRY_QUEUE` (V4) and `REQUEUE_FOREVER` (V12) are probably "caught"; V12 only spuriously, because head is better and gets penalised.
- `RETRY_NO_BACKOFF` (V5) is exposed by `auto-down-checkout-payments` and `peak_2x`, not `payments_flaky`.
- `SHARED_POOL`, `NO_RECONNECT` and `ACK_BEFORE_WRITE` likely fail `baseline` too; several bugs regress in scenarios the matrix didn't list.
- Unpinned behaviour decides outcomes: whether latency delays the request or the response, whether payments cancels when checkout disconnects, when the charge row is written, what happens to an order after retries run out, and worker concurrency.
- Bug parameters (50 ms ack gap, 5 s leak, race probability 0.5) are amplified for detection without disclosure.
- `RETRY_NEW_KEY` may fail ground rule 1 if the reconciler test asserts the original key.

**Next round must:** commit bug definitions and pinned behaviour (with a hash) before running; use a neutral brief (standard telemetry only, no purpose-built knobs, Kazu docs quoted in full); write the shop's own test suite blind as well; disclose amplified parameters; count only catches whose firing check matches the bug's harm; record actual outcomes instead of predicting exact ones.

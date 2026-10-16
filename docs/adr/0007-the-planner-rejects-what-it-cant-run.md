# ADR-0007: The planner rejects config it can't run yet

Date: 2026-10-16
Status: Accepted (2026-10-16)

## Context

Raised by the 2026-10-16 foundations review; decided in #37. ADR-0002 decision 12 keeps no "not supported yet" list in `config`: nothing outside runs Kazu before the first public release, and before it the schema is trimmed to what's built. The 2026-10-16 foundations review found the gap that leaves in Phases 3–5: the config accepts things the runtime would silently ignore or apply wrongly, with no fact to report. A `from:` before anchor resolution makes its fault fire at traffic start; it still lands, so the landed check passes. A scenario `traffic:` override, `jitter`, `tls` and check kinds without a runner fail the same way. The shop's own configs, Kazu's test oracle, are exactly where such a silent wrong result would fool us.

## Decision

The planner, which turns a `Config` into trials (driver 4/4), fails on any populated `Config` field it doesn't implement yet, with an error naming the field. It is one function, next to the code that implements the features, and each feature PR deletes its entry. `config` itself keeps no list.

Considered and lost:
- **A list in `config`** (#17's first version): dropped as YAGNI, and it drifts from the runtime because it lives in a different package.
- **Nothing**, relying on review to keep unbuilt features out of shop configs: a silent wrong result in the oracle is the costliest failure, and the function is small.

## Consequences

One function and its table test in driver 4/4. The release trim (ADR-0002, decision 12) still applies before the first public release; by then the function should be empty.

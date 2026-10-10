# Edits to the blind author's output

Only two kinds of edit are allowed: correcting a fact the brief left ambiguous, and following a diagnostic Kazu itself prints. Each is listed here. None was made to catch a bug.

| # | Edit | Kind | Reason |
|---|---|---|---|
| 1 | `checkout_db` and `payments_db` both point at the single `shop` database | Fact | The brief didn't say there is one database; the author assumed two. Their checks work either way |
| 2 | `peak_2x` uses `RATE: "200"` instead of `"60"` | Fact | The author assumed a default of ~30 and wrote "~2x typical peak; adjust". The shop's default is 100, so 2x is 200, as their comment intends |
| 3 | `payments -> fakestripe` declared with the shop's test CA (`tls: { ca_cert, ca_key }`) | Diagnostic | `kazu doctor` reports that HTTP faults on a TLS edge need a test CA; without it `card_processor_errors` would be *not exercised*. The shop ships one in `certs/` |
| 4 | Syntax normalised to the published schema and SDK: `command` as a list, rates as percentages (`30%`), the code-checks entry as `image` + `volumes`, `grafana/k6:1.0.0`, `from kazu_sdk import ...` | Diagnostic | Config validation rejects the original forms; meaning is unchanged |

"""Summarise run-to-run noise from one run.sh output directory (one condition).

Each request is bucketed by its start time into a window relative to the
fault: before, during, after (plus all). Per window: p99 latency, HTTP error
rate (k6 http_req_failed) and unpaid-order rate (POST /orders not answered
with a paid order). Runs (1,2), (3,4), ... are base/head pairs of identical
code; latency and request-count diffs are relative ((head-base)/base), rate
diffs are absolute percentage points. Prints Markdown, writes JSON.

Usage: python3 analyze.py OUT_DIR [LABEL] [JSON_PATH]
"""

import gzip
import json
import pathlib
import statistics
import sys

WINDOWS = ["all", "before", "during", "after"]


def quantile(values, q):
    if len(values) < 2:
        return values[0] if values else None
    return statistics.quantiles(values, n=100, method="inclusive")[q - 1]


def window(start, t):
    if start < t["fault_start"]:
        return "before"
    return "during" if start < t["fault_end"] else "after"


def load_run(out, i):
    t = json.loads((out / f"run-{i}.json").read_text())
    dur = {w: [] for w in WINDOWS}
    failed = {w: [] for w in WINDOWS}
    unpaid = {w: [] for w in WINDOWS}
    series = {"http_req_duration": dur, "http_req_failed": failed, "order_unpaid": unpaid}
    with gzip.open(out / f"raw-{i}.json.gz", "rt") as f:
        for line in f:
            p = json.loads(line)
            if p["type"] != "Point" or p["metric"] not in series:
                continue
            start = int(p["data"]["tags"]["start"]) / 1000
            for w in ("all", window(start, t)):
                series[p["metric"]][w].append(p["data"]["value"])
    row = {"run": i, "all.p50_ms": quantile(dur["all"], 50), "all.p95_ms": quantile(dur["all"], 95),
           "all.requests": len(dur["all"])}
    for w in WINDOWS:
        row[f"{w}.p99_ms"] = quantile(dur[w], 99)
        row[f"{w}.http_err_pct"] = 100 * statistics.fmean(failed[w]) if failed[w] else None
        row[f"{w}.unpaid_pct"] = 100 * statistics.fmean(unpaid[w]) if unpaid[w] else None
    row["k6_s"] = round(t["k6_end"] - t["k6_start"], 1)
    return row


def metric_keys():
    keys = ["all.p50_ms", "all.p95_ms", "all.requests"]
    for w in WINDOWS:
        keys += [f"{w}.p99_ms", f"{w}.http_err_pct", f"{w}.unpaid_pct"]
    return keys


def diff(key, a, b):
    if a is None or b is None:
        return None
    if key.endswith("_pct"):
        return b - a
    return (b - a) / a if a else None


def analyze(runs):
    pairs = [(runs[k], runs[k + 1]) for k in range(0, len(runs) - 1, 2)]
    res = {"runs": runs, "pairs": [], "summary": {}}
    for a, b in pairs:
        res["pairs"].append({"base": a["run"], "head": b["run"],
                             **{k: diff(k, a[k], b[k]) for k in metric_keys()}})
    for k in metric_keys():
        absd = sorted(abs(p[k]) for p in res["pairs"] if p[k] is not None)
        vals = [r[k] for r in runs if r[k] is not None]
        mean = statistics.fmean(vals) if vals else None
        cv = statistics.stdev(vals) / mean if len(vals) > 1 and mean else None
        res["summary"][k] = {
            "mean": mean, "cv": cv,
            "median_abs_diff": statistics.median(absd) if absd else None,
            "p90_abs_diff": absd[min(len(absd) - 1, int(0.9 * len(absd)))] if absd else None,
            "max_abs_diff": absd[-1] if absd else None,
        }
    return res


def fmt(v, spec):
    return "n/a" if v is None else format(v, spec)


def markdown(res, label):
    cols = ["all.p50_ms", "all.p95_ms"] + [f"{w}.p99_ms" for w in WINDOWS] \
        + [f"{w}.unpaid_pct" for w in WINDOWS] + [f"{w}.http_err_pct" for w in WINDOWS]
    out = [f"## {label}", "", "### Per run", "",
           "| run | requests | k6 s | " + " | ".join(cols) + " |", "|---" * (len(cols) + 3) + "|"]
    for r in res["runs"]:
        out.append(f"| {r['run']} | {r['all.requests']} | {r['k6_s']} | "
                   + " | ".join(fmt(r[c], ".2f") for c in cols) + " |")
    out += ["", "### Noise (diffs over base/head pairs; latency and requests relative, rates in pp)", "",
            "| metric | mean | CV | median \\|diff\\| | p90 \\|diff\\| | max \\|diff\\| |", "|---|---|---|---|---|---|"]
    for k, s in res["summary"].items():
        spec = ".2f" if k.endswith("_pct") else ".1%"
        out.append(f"| {k} | {fmt(s['mean'], '.2f')} | {fmt(s['cv'], '.1%')} | {fmt(s['median_abs_diff'], spec)} | "
                   f"{fmt(s['p90_abs_diff'], spec)} | {fmt(s['max_abs_diff'], spec)} |")
    return "\n".join(out)


def main():
    out = pathlib.Path(sys.argv[1])
    label = sys.argv[2] if len(sys.argv) > 2 else out.name
    json_path = pathlib.Path(sys.argv[3]) if len(sys.argv) > 3 else out / "noise.json"
    n = 0
    while (out / f"run-{n + 1}.json").exists():
        n += 1
    if not n:
        sys.exit(f"no run-*.json in {out}")
    res = analyze([load_run(out, i) for i in range(1, n + 1)])
    json_path.write_text(json.dumps({"label": label, **res}, indent=2))
    print(markdown(res, label))


if __name__ == "__main__":
    main()

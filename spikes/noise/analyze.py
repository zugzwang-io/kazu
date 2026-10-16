"""Summarise run-to-run noise from run.sh output.

Runs (1,2), (3,4), ... are treated as base/head pairs of identical code.
Latency diffs are relative ((head-base)/base); error-rate diffs are absolute,
in percentage points. Prints Markdown to stdout and writes JSON.

Usage: python3 analyze.py OUT_DIR [JSON_PATH]
"""

import json
import pathlib
import statistics
import sys

LATENCY = ["p50_ms", "p95_ms", "p99_ms"]


def load(out):
    runs = []
    for i in range(1, 10_000):
        summary = out / f"run-{i}.json"
        if not summary.exists():
            break
        m = json.loads(summary.read_text())["metrics"]
        d = m["http_req_duration"]
        wall = json.loads((out / f"wall-{i}.json").read_text())
        runs.append({
            "run": i,
            "p50_ms": d["p(50)"],
            "p95_ms": d["p(95)"],
            "p99_ms": d["p(99)"],
            "requests": m["http_reqs"]["count"],
            "error_pct": 100 * m["http_req_failed"]["value"],
            "wall_s": wall["wall_s"],
            "k6_s": wall["k6_s"],
        })
    return runs


def pct(values, q):
    s = sorted(values)
    return s[min(len(s) - 1, int(q * len(s)))]


def analyze(runs):
    pairs = [(runs[k], runs[k + 1]) for k in range(0, len(runs) - 1, 2)]
    result = {"runs": runs, "pairs": [], "summary": {}}
    for a, b in pairs:
        diff = {f"{x}_rel": (b[x] - a[x]) / a[x] if a[x] else None for x in LATENCY}
        diff["error_pp"] = b["error_pct"] - a["error_pct"]
        result["pairs"].append({"base": a["run"], "head": b["run"], **diff})
    for key in [f"{x}_rel" for x in LATENCY] + ["error_pp"]:
        absd = [abs(p[key]) for p in result["pairs"] if p[key] is not None]
        result["summary"][key] = {
            "median_abs": statistics.median(absd) if absd else None,
            "p90_abs": pct(absd, 0.9) if absd else None,
            "max_abs": max(absd) if absd else None,
        }
    for key in LATENCY + ["error_pct", "requests"]:
        vals = [r[key] for r in runs]
        mean = statistics.fmean(vals)
        cv = statistics.stdev(vals) / mean if len(vals) > 1 and mean else None
        result["summary"][key] = {"mean": mean, "cv": cv}
    return result


def fmt(v, spec):
    return "n/a" if v is None else format(v, spec)


def markdown(res):
    out = ["### Per run", "", "| run | p50 ms | p95 ms | p99 ms | requests | error % | wall s | k6 s |",
           "|---|---|---|---|---|---|---|---|"]
    for r in res["runs"]:
        out.append(f"| {r['run']} | {r['p50_ms']:.2f} | {r['p95_ms']:.2f} | {r['p99_ms']:.2f} | "
                   f"{r['requests']} | {r['error_pct']:.3f} | {r['wall_s']} | {r['k6_s']} |")
    out += ["", "### Per pair (head vs base)", "", "| base | head | p50 Δ | p95 Δ | p99 Δ | error Δ pp |",
            "|---|---|---|---|---|---|"]
    for p in res["pairs"]:
        out.append(f"| {p['base']} | {p['head']} | {fmt(p['p50_ms_rel'], '+.1%')} | "
                   f"{fmt(p['p95_ms_rel'], '+.1%')} | {fmt(p['p99_ms_rel'], '+.1%')} | {p['error_pp']:+.3f} |")
    s = res["summary"]
    out += ["", "### |diff| across pairs", "", "| metric | median | p90 | max |", "|---|---|---|---|"]
    for key in [f"{x}_rel" for x in LATENCY]:
        out.append(f"| {key} | {fmt(s[key]['median_abs'], '.1%')} | {fmt(s[key]['p90_abs'], '.1%')} | "
                   f"{fmt(s[key]['max_abs'], '.1%')} |")
    e = s["error_pp"]
    out.append(f"| error_pp | {fmt(e['median_abs'], '.3f')} | {fmt(e['p90_abs'], '.3f')} | "
               f"{fmt(e['max_abs'], '.3f')} |")
    out += ["", "### Across all runs", "", "| metric | mean | CV |", "|---|---|---|"]
    for key in LATENCY + ["error_pct", "requests"]:
        out.append(f"| {key} | {s[key]['mean']:.3f} | {fmt(s[key]['cv'], '.1%')} |")
    return "\n".join(out)


def main():
    out = pathlib.Path(sys.argv[1])
    json_path = pathlib.Path(sys.argv[2]) if len(sys.argv) > 2 else out / "noise.json"
    runs = load(out)
    if not runs:
        sys.exit(f"no run-*.json in {out}")
    res = analyze(runs)
    json_path.write_text(json.dumps(res, indent=2))
    print(markdown(res))


if __name__ == "__main__":
    main()

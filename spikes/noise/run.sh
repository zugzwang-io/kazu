#!/usr/bin/env bash
# Runs the shop N times from scratch under identical k6 load, optionally
# pausing payments for FAULT_FOR seconds starting FAULT_AT seconds into the
# load. Per run I it writes to OUT: raw-I.json.gz (k6 raw samples) and
# run-I.json (timings: k6 start and the fault window, as epoch seconds; with
# FAULT=0 the window is nominal and nothing is paused).
# Usage: N=20 RATE=50 DURATION=60s FAULT=1 FAULT_AT=20 FAULT_FOR=15 OUT=out ./run.sh
set -euo pipefail

N=${N:-20}
RATE=${RATE:-50}
DURATION=${DURATION:-60s}
FAULT=${FAULT:-1}
FAULT_AT=${FAULT_AT:-20}
FAULT_FOR=${FAULT_FOR:-15}
K6_IMAGE=grafana/k6:0.54.0
here=$(cd "$(dirname "$0")" && pwd)
OUT=${OUT:-$here/out}
mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)

cd "$here/../../examples/shop"
dc=(docker compose -f docker-compose.yml -f "$here/compose.override.yml")
trap '"${dc[@]}" down -v >/dev/null 2>&1 || true' EXIT
now() { python3 -c 'import time; print(f"{time.time():.3f}")'; }

fault() {
  sleep "$FAULT_AT"
  local start end
  start=$(now)
  if [[ $FAULT == 1 ]]; then "${dc[@]}" pause payments >/dev/null; fi
  sleep "$FAULT_FOR"
  if [[ $FAULT == 1 ]]; then "${dc[@]}" unpause payments >/dev/null; fi
  end=$(now)
  printf '%s %s\n' "$start" "$end" >"$OUT/fault-$1.tmp"
}

"${dc[@]}" build
docker pull -q "$K6_IMAGE"

for i in $(seq 1 "$N"); do
  "${dc[@]}" up -d --wait checkout
  k6_start=$(now)
  fault "$i" &
  docker run --rm --network shop_default -u "$(id -u):$(id -g)" \
    -e RATE="$RATE" -e DURATION="$DURATION" -e SEED=1 \
    -v "$here/traffic.js:/traffic.js:ro" -v "$OUT:/out" \
    "$K6_IMAGE" run --quiet --no-summary --out "json=/out/raw-$i.json.gz" /traffic.js
  wait
  k6_end=$(now)
  [[ -s "$OUT/raw-$i.json.gz" ]] || { echo "k6 wrote nothing to $OUT (is it shared with Docker?)" >&2; exit 1; }
  read -r fault_start fault_end <"$OUT/fault-$i.tmp" && rm "$OUT/fault-$i.tmp"
  printf '{"run": %d, "rate": %d, "fault": %d, "k6_start": %s, "k6_end": %s, "fault_start": %s, "fault_end": %s}\n' \
    "$i" "$RATE" "$FAULT" "$k6_start" "$k6_end" "$fault_start" "$fault_end" >"$OUT/run-$i.json"
  "${dc[@]}" down -v
  echo "run $i/$N done"
done

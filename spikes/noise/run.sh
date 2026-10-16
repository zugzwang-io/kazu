#!/usr/bin/env bash
# Runs the shop N times from scratch under identical k6 load; writes one k6
# summary (run-I.json) and one timing file (wall-I.json) per run to OUT.
# Usage: N=20 RATE=50 DURATION=60s OUT=out ./run.sh
set -euo pipefail

N=${N:-20}
RATE=${RATE:-50}
DURATION=${DURATION:-60s}
K6_IMAGE=grafana/k6:0.54.0
here=$(cd "$(dirname "$0")" && pwd)
OUT=${OUT:-$here/out}
mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)

cd "$here/../../examples/shop"
docker compose build
docker pull -q "$K6_IMAGE"

for i in $(seq 1 "$N"); do
  start=$(date +%s)
  docker compose up -d --wait checkout
  k6_start=$(date +%s)
  docker run --rm --network shop_default -u "$(id -u):$(id -g)" \
    -e RATE="$RATE" -e DURATION="$DURATION" -e SEED=1 \
    -v "$here/traffic.js:/traffic.js:ro" -v "$OUT:/out" \
    "$K6_IMAGE" run --quiet --summary-export "/out/run-$i.json" /traffic.js
  k6_end=$(date +%s)
  [[ -s "$OUT/run-$i.json" ]] || { echo "k6 wrote no summary to $OUT (is it shared with Docker?)" >&2; exit 1; }
  docker compose down -v
  end=$(date +%s)
  printf '{"run": %d, "wall_s": %d, "k6_s": %d}\n' "$i" $((end - start)) $((k6_end - k6_start)) >"$OUT/wall-$i.json"
  echo "run $i/$N: $((end - start))s"
done

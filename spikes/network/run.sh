#!/usr/bin/env bash
# Spike: can the host reach a sealed trial network through one in-network
# helper container? Starts the shop on an internal network with a Kazu-chosen
# subnet, one proxy on the checkout -> payments edge (routed by extra_hosts on
# checkout only), and a helper driven over stdio. Prints PASS/FAIL per check,
# exits non-zero on any FAIL, and always tears down.
#
# Runs on bash 3.2 (macOS), so the stdio channel uses fifos, not coproc.
set -uo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
SHOP=$(cd "$HERE/../../examples/shop" && pwd)
PROJECT=${PROJECT:-kazuspike}
DSN=postgres://postgres:dev@postgres:5432/shop
WORK=$(mktemp -d)
OVR=$WORK/override.yml
FAILS=0
TIMINGS=()

now_ms() { perl -MTime::HiRes=time -e 'printf "%d\n", time*1000'; }
phase_start() { PHASE_T0=$(now_ms); }
phase_end() { local d=$(( $(now_ms) - PHASE_T0 )); TIMINGS+=("$1=${d}ms"); echo "TIMING $1 ${d}ms"; }
check() { # name, ok(0/1), detail
  if [ "$2" = 0 ]; then echo "PASS  $1  $3"; else echo "FAIL  $1  $3"; FAILS=$((FAILS + 1)); fi
  RESULTS+=("$( [ "$2" = 0 ] && echo PASS || echo FAIL ) | $1 | $3")
}
RESULTS=()
dc() { docker compose -p "$PROJECT" -f "$SHOP/docker-compose.yml" -f "$OVR" "$@"; }

summary() {
  [ -n "${GITHUB_STEP_SUMMARY:-}" ] || return 0
  {
    echo "### run.sh (${RUN_LABEL:-run}) on $(uname -s), subnet ${SUBNET:-?}"
    echo "| result | check | detail |"; echo "|---|---|---|"
    for r in "${RESULTS[@]}"; do echo "| $r |"; done
    echo; echo "| phase | wall time |"; echo "|---|---|"
    for t in "${TIMINGS[@]}"; do echo "| ${t%%=*} | ${t#*=} |"; done
    echo
  } >> "$GITHUB_STEP_SUMMARY"
}

teardown() {
  { exec 3>&- 4<&-; } 2>/dev/null
  [ -n "${CHAN_PID:-}" ] && kill -9 "$CHAN_PID" 2>/dev/null
  phase_start
  dc down -v --remove-orphans >/dev/null 2>&1
  phase_end down
  if docker network ls --format '{{.Name}}' | grep -qx "${PROJECT}_default"; then
    check "teardown removes network" 1 "${PROJECT}_default still exists"
  else
    check "teardown removes network" 0 ""
  fi
  summary
  rm -rf "$WORK"
  echo "$FAILS check(s) failed"
  exit $(( FAILS > 0 ))
}
trap teardown EXIT

# --- pick a free /24 -------------------------------------------------------
ip2int() { local IFS=.; set -- $1; echo $(( ($1 << 24) + ($2 << 16) + ($3 << 8) + $4 )); }
overlaps() { # cidr cidr
  local a=${1%/*} al=${1#*/} b=${2%/*} bl=${2#*/}
  local l=$(( al < bl ? al : bl ))
  local mask=$(( (0xFFFFFFFF << (32 - l)) & 0xFFFFFFFF ))
  [ $(( $(ip2int "$a") & mask )) -eq $(( $(ip2int "$b") & mask )) ]
}
phase_start
USED=$(docker network inspect $(docker network ls -q) --format '{{range .IPAM.Config}}{{.Subnet}} {{end}}' | tr ' ' '\n' | grep -E '^[0-9.]+/[0-9]+$' || true)
SUBNET=
for n in $(seq 0 255); do
  cand=10.213.$n.0/24
  clash=
  for u in $USED; do overlaps "$cand" "$u" && { clash=1; break; }; done
  [ -z "$clash" ] && { SUBNET=$cand; break; }
done
[ -n "$SUBNET" ] || { echo "no free subnet"; exit 1; }
PREFIX=${SUBNET%.0/24}
PROXY_IP=$PREFIX.200
echo "existing subnets: $(echo $USED)"; echo "chose $SUBNET, proxy at $PROXY_IP"
phase_end pick_subnet

# --- override: sealed network, proxy on one edge, helper -------------------
cat > "$OVR" <<EOF
services:
  checkout:
    ports: !reset []
    extra_hosts: ["payments:$PROXY_IP"]
    depends_on:
      proxy-checkout-payments: { condition: service_started }
  proxy-checkout-payments:
    image: kazu-spike:dev
    build: { context: $HERE }
    command: [proxy]
    environment: { LISTEN: ":8081", UPSTREAM: "payments:8081", COUNTERS: ":9000" }
    networks: { default: { ipv4_address: $PROXY_IP } }
  helper:
    image: kazu-spike:dev
    build: { context: $HERE }
    command: [helper]
    stdin_open: true
networks:
  default:
    internal: true
    ipam:
      config:
        - subnet: $SUBNET
          ip_range: $PREFIX.0/25
EOF

# --- up --------------------------------------------------------------------
phase_start
dc build -q >"$WORK/build.log" 2>&1 || { cat "$WORK/build.log"; check "build" 1 "see log"; exit 1; }
phase_end build
phase_start
if dc up -d --wait --wait-timeout 180 checkout proxy-checkout-payments helper >"$WORK/up.log" 2>&1; then
  check "up --wait on internal network" 0 ""
else
  cat "$WORK/up.log"; dc ps -a; dc logs --no-color --tail 20
  check "up --wait on internal network" 1 "see log"; exit 1
fi
phase_end up
NET=${PROJECT}_default
docker network inspect "$NET" --format 'network {{.Name}} internal={{.Internal}} subnet={{range .IPAM.Config}}{{.Subnet}} range={{.IPRange}}{{end}}'
dc exec -T checkout grep payments /etc/hosts | sed 's/^/checkout \/etc\/hosts: /'

# --- the channel: one long-lived exec, JSON lines over fifos ---------------
open_channel() { # command...
  rm -f "$WORK/in" "$WORK/out"; mkfifo "$WORK/in" "$WORK/out"
  "$@" <"$WORK/in" >"$WORK/out" 2>"$WORK/chan.err" &
  CHAN_PID=$!
  exec 3>"$WORK/in" 4<"$WORK/out"
}
# exec ends on stdin EOF; attach (no tty, stdin_once false) never does and
# ignores SIGTERM, so it needs SIGKILL.
close_channel() {
  exec 3>&- 4<&-
  local i; for i in 1 2 3 4 5 6 7 8 9 10; do kill -0 "$CHAN_PID" 2>/dev/null || break; sleep 0.2; done
  if kill -0 "$CHAN_PID" 2>/dev/null; then echo "channel did not exit on EOF; SIGKILL"; kill -9 "$CHAN_PID"; fi
  wait "$CHAN_PID" 2>/dev/null; CHAN_PID=
}
req() { # json -> one response line on stdout
  echo "$1" >&3
  local line; IFS= read -r -t 30 line <&4 || { echo '{"ok":false,"error":"channel: no response"}'; return 1; }
  echo "$line"
}

phase_start
open_channel dc exec -T helper helper
first=$(req '{"op":"dial","addr":"postgres:5432","timeout_ms":2000}')
phase_end exec_channel_first_reply
check "exec channel answers" $([ "$(jq -r .ok <<<"$first")" = true ] && echo 0 || echo 1) "$first"

phase_start
paid=0
for i in 1 2 3 4 5; do
  body=$(jq -nc --arg k "spike-$RANDOM-$i" '{op:"http",method:"POST",url:"http://checkout:8080/orders",headers:{"Idempotency-Key":$k},body:"{\"amount_cents\": 1500}"}')
  r=$(req "$body")
  jq -e '.status == 200 and (.result | contains("\"status\":\"paid\""))' <<<"$r" >/dev/null && paid=$((paid + 1))
done
check "orders paid via helper http" $([ $paid = 5 ] && echo 0 || echo 1) "$paid/5 paid"
phase_end orders

phase_start
c1=$(req "{\"op\":\"counters\",\"url\":\"http://$PROXY_IP:9000/counters\"}")
conns=$(jq -r '.result | fromjson | .connections' <<<"$c1" 2>/dev/null || echo 0)
up=$(jq -r '.result | fromjson | .bytes_up' <<<"$c1" 2>/dev/null || echo 0)
check "proxy saw checkout->payments traffic" $([ "${conns:-0}" -gt 0 ] && [ "${up:-0}" -gt 0 ] && echo 0 || echo 1) "$(jq -c .result <<<"$c1")"

s=$(req "{\"op\":\"sql\",\"dsn\":\"$DSN\",\"query\":\"select count(*) from checkout.orders\"}")
n=$(jq -r .result <<<"$s")
check "sql via helper" $([ "${n:-0}" -ge 5 ] 2>/dev/null && echo 0 || echo 1) "$s"

h=$(req '{"op":"http","url":"http://payments:8081/healthz"}')
c2=$(req "{\"op\":\"counters\",\"url\":\"http://$PROXY_IP:9000/counters\"}")
same=$([ "$(jq -r .status <<<"$h")" = 200 ] && [ "$(jq -r .result <<<"$c1")" = "$(jq -r .result <<<"$c2")" ] && echo 0 || echo 1)
check "alias is per-caller (helper->payments bypasses proxy)" "$same" "healthz=$(jq -r .status <<<"$h") before=$(jq -c .result <<<"$c1") after=$(jq -c .result <<<"$c2")"

d1=$(req '{"op":"dial","addr":"1.1.1.1:443","timeout_ms":3000}')
check "sealed: no route to 1.1.1.1:443" $([ "$(jq -r .ok <<<"$d1")" = false ] && [ "$(jq -r .elapsed_ms <<<"$d1")" -lt 1000 ] && echo 0 || echo 1) "$d1"
d2=$(req '{"op":"dial","addr":"example.com:443","timeout_ms":3000}')
check "sealed: no external DNS" $([ "$(jq -r .ok <<<"$d2")" = false ] && echo 0 || echo 1) "$d2"

t0=$(now_ms)
for i in $(seq 1 50); do req "{\"op\":\"counters\",\"url\":\"http://$PROXY_IP:9000/counters\"}" >/dev/null; done
rt=$(( $(now_ms) - t0 ))
echo "exec channel: 50 counters round trips in ${rt}ms"
TIMINGS+=("exec_50_round_trips=${rt}ms")
close_channel
phase_end checks

# --- alternative channel: docker attach to the helper's PID 1 --------------
phase_start
CID=$(dc ps -q helper)
open_channel docker attach --sig-proxy=false "$CID"
a=$(req "{\"op\":\"counters\",\"url\":\"http://$PROXY_IP:9000/counters\"}")
check "attach channel answers" $([ "$(jq -r .ok <<<"$a")" = true ] && echo 0 || echo 1) "$a"
close_channel
running=$(docker inspect -f '{{.State.Running}}' "$CID")
check "helper survives attach close" $([ "$running" = true ] && echo 0 || echo 1) "running=$running"
phase_end attach

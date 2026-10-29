#!/usr/bin/env bash
# Reproduces the advertised-addresses spike. Usage: ./run.sh [kafka] [redis] [mongo] [cassandra] (default: all).
# Each case runs once configured with names and once as a control that must show the bypass. Every case
# passes only on positive evidence, so a client that never ran, or a setup that failed, fails the case.
# Real node addresses are 10.77.0.11-13; everything else on that network is assigned from 10.77.0.128/25.
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd)
docker build -q -f "$here/proxy.Dockerfile" -t kazu-spike-proxy "$(git -C "$here" rev-parse --show-toplevel)" >/dev/null || exit 1
failures=0
dc=()
setup_log=$(mktemp)
trap 'rm -f "$setup_log"; [ ${#dc[@]} -gt 0 ] && "${dc[@]}" down -v >/dev/null 2>&1' EXIT

fail() {
	echo "  FAIL $1"
	failures=$((failures + 1))
}

# use CASE: points dc at CASE's compose project and starts it from nothing.
use() {
	dc=(docker compose -p "spike-$1" -f "$here/$1/compose.yml")
	"${dc[@]}" down -v --remove-orphans >/dev/null 2>&1
}

# check LABEL CMD...: runs CMD and reports whether it passed, with the tail of its output.
check() {
	local label=$1 out
	shift
	if out=$("$@" 2>&1); then echo "  ok   $label"; else fail "$label"; fi
	printf '%s\n' "$out" | grep -v '^\s*at ' | tail -4 | sed 's/^/    /'
}

# bypassed PORT CMD...: passes when CMD failed, its output names a real node address on PORT, and it
# reports that address unreachable, which the client's network makes it. Anything else proves nothing.
bypassed() {
	local port=$1 out
	shift
	if out=$("$@" 2>&1); then
		echo "$out"
		return 1
	fi
	echo "$out"
	grep -qE "10\.77\.0\.1[1-3]:$port" <<<"$out" && grep -qiE 'unreachable|ENETUNREACH' <<<"$out"
}

# case_run SETUP_LABEL LABEL BYPASS_PORT CMD...: starts the project, then checks CMD directly
# (BYPASS_PORT empty) or as a control; a failed setup fails the case instead of checking it.
case_run() {
	local setup=$1 label=$2 port=$3
	shift 3
	if ! "${dc[@]}" up -d --wait ${services[@]+"${services[@]}"} >"$setup_log" 2>&1 || ! init >>"$setup_log" 2>&1; then
		fail "$label: $setup"
		tail -4 "$setup_log" | sed 's/^/    /'
	elif [ -z "$port" ]; then
		check "$label" "$@"
	else
		check "$label" bypassed "$port" "$@"
	fi
	"${dc[@]}" down -v >/dev/null 2>&1
}

kafka() {
	use kafka
	local services=() mode
	init() { :; }
	for mode in names ips; do
		local -x ADV_PREFIX=kafka- port=
		[ "$mode" = ips ] && ADV_PREFIX=10.77.0.1 port=9092
		case_run up "kafka advertising $mode" "$port" "${dc[@]}" exec -T client bash /workload.sh
	done
}

redis() {
	use redis
	local services=(redis-1 redis-2 redis-3 px-1 px-2 px-3 client) endpoint
	init() { "${dc[@]}" run --rm init; }
	for endpoint in hostname ip; do
		local -x ENDPOINT=$endpoint port=
		[ "$ENDPOINT" = ip ] && port=6379
		case_run "cluster setup" "redis endpoint type $ENDPOINT" "$port" "${dc[@]}" exec -T client bash /workload.sh
	done
}

mongo() {
	use mongo
	local services=(mongo-1 mongo-2 mongo-3 px-1 px-2 px-3 client) mode
	init() { "${dc[@]}" run --rm init; }
	for mode in names ips; do
		local -x MODE=$mode port=
		[ "$MODE" = ips ] && port=27017
		case_run "replica set setup" "mongo members as $MODE" "$port" "${dc[@]}" exec -T client mongosh --quiet --nodb /workload.js
	done
}

# dials: the addresses the cassandra driver dialed, one per line, "ADDR failed" for a failed dial;
# fails if the workload did.
dials() { "${dc[@]}" exec -T client /cqlclient cassandra-1; }

# only_proxy: the driver dialed cassandra-1's proxy and never a real node address.
only_proxy() {
	local out
	out=$(dials) || return 1
	echo "$out"
	grep -qx '10\.77\.1\.11:9042' <<<"$out" && ! grep -qE '^10\.77\.0\.1[1-3]:' <<<"$out"
}

# discovered_peer: the driver connected to cassandra-2's real address, which only system.peers hands out.
discovered_peer() {
	local out
	out=$(dials) || return 1
	echo "$out"
	grep -qx '10\.77\.0\.12:9042' <<<"$out"
}

# routed_around: the driver's dial to cassandra-2's real address failed, yet the workload succeeded.
routed_around() {
	local out
	out=$(dials) || return 1
	echo "$out"
	grep -qx '10\.77\.0\.12:9042 failed' <<<"$out" && ! grep -qx '10\.77\.0\.12:9042' <<<"$out"
}

# cassandra: the client dials real addresses only where it can reach them (open.yml adds core), so the
# evidence is the list of addresses the driver dialed, failed dials included.
cassandra() {
	use cassandra
	local iso=("${dc[@]}")
	dc+=(-f "$here/cassandra/open.yml")
	local -x C2_RPC_ADDRESS=
	step() { "${dc[@]}" up -d --wait --build "$@" >"$setup_log" 2>&1 || { sed 's/^/    /' "$setup_log" | tail -4; return 1; }; }
	if step cassandra-1 px-1 client; then
		check "cassandra single node: the driver dials only the proxy" only_proxy
	else fail "cassandra single node: up"; fi
	if step cassandra-2 px-2; then
		check "cassandra two nodes: the driver dials the peer's real IP" discovered_peer
	else fail "cassandra two nodes: up"; fi
	if (dc=("${iso[@]}") && step client); then
		check "cassandra two nodes, peer unreachable: the workload still succeeds" routed_around
	else fail "cassandra isolated client: up"; fi
	C2_RPC_ADDRESS=cassandra-2
	if step cassandra-2 client; then
		check "cassandra broadcast_rpc_address=cassandra-2: the driver still dials the real IP" discovered_peer
	else fail "cassandra hostname broadcast: up"; fi
	"${dc[@]}" down -v >/dev/null 2>&1
}

for t in ${@:-kafka redis mongo cassandra}; do
	echo "== $t"
	case $t in
	kafka | redis | mongo | cassandra) "$t" ;;
	*) fail "unknown case $t" ;;
	esac
done
if [ "$failures" -gt 0 ]; then
	echo "$failures case(s) failed"
	exit 1
fi
echo "all cases as expected"

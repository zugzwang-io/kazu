#!/usr/bin/env bash
# Reproduces the advertised-addresses spike. Usage: ./run.sh [kafka] [redis] [mongo] [cassandra] (default: all).
# Each case runs once configured with names and once as a control that must show the bypass. Every case
# passes only on positive evidence, so a client that never ran fails instead of passing.
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd)
docker build -q -f "$here/proxy.Dockerfile" -t kazu-spike-proxy "$(git -C "$here" rev-parse --show-toplevel)" >/dev/null || exit 1
failures=0

fail() {
	echo "  FAIL $1"
	failures=$((failures + 1))
}

# check LABEL CMD...: runs CMD and reports whether it passed, with the tail of its output.
check() {
	local label=$1 out
	shift
	if out=$("$@" 2>&1); then echo "  ok   $label"; else fail "$label"; fi
	printf '%s\n' "$out" | grep -v '^\s*at ' | tail -4 | sed 's/^/    /'
}

# bypassed CMD...: passes when CMD failed because it dialed a real node address, which the client's
# network can't reach. Success, or any other failure, means the control proved nothing.
bypassed() {
	local out
	if out=$("$@" 2>&1); then
		echo "$out"
		return 1
	fi
	echo "$out"
	grep -qiE 'unreachable|ENETUNREACH' <<<"$out"
}

kafka() {
	local dc=(docker compose -p spike-kafka -f "$here/kafka/compose.yml") mode
	for mode in names ips; do
		local run=bypassed
		export ADV_PREFIX=10.77.0.1
		if [ "$mode" = names ]; then run= ADV_PREFIX=kafka-; fi
		"${dc[@]}" up -d --wait >/dev/null 2>&1 || fail "kafka $mode: up"
		check "kafka advertising $mode" $run "${dc[@]}" exec -T client bash /workload.sh
		"${dc[@]}" down -v >/dev/null 2>&1
	done
}

redis() {
	local dc=(docker compose -p spike-redis -f "$here/redis/compose.yml")
	for ENDPOINT in hostname ip; do
		export ENDPOINT
		local run=bypassed
		[ "$ENDPOINT" = hostname ] && run=
		"${dc[@]}" up -d --wait redis-1 redis-2 redis-3 px-1 px-2 px-3 client >/dev/null 2>&1 &&
			"${dc[@]}" run --rm init >/dev/null 2>&1 || fail "redis $ENDPOINT: setup"
		check "redis endpoint type $ENDPOINT" $run "${dc[@]}" exec -T client bash /workload.sh
		"${dc[@]}" down -v >/dev/null 2>&1
	done
}

mongo() {
	local dc=(docker compose -p spike-mongo -f "$here/mongo/compose.yml")
	for MODE in names ips; do
		export MODE
		local run=bypassed
		[ "$MODE" = names ] && run=
		"${dc[@]}" up -d --wait mongo-1 mongo-2 mongo-3 px-1 px-2 px-3 client >/dev/null 2>&1 &&
			"${dc[@]}" run --rm init >/dev/null 2>&1 || fail "mongo $MODE: setup"
		check "mongo members as $MODE" $run "${dc[@]}" exec -T client mongosh --quiet --nodb /workload.js
		"${dc[@]}" down -v >/dev/null 2>&1
	done
}

# The cassandra client can reach real addresses, so the evidence is its own connection list.
cassandra() {
	local dc=(docker compose -p spike-cassandra -f "$here/cassandra/compose.yml")
	connections() { "${dc[@]}" exec -T client /cqlclient cassandra-1; }
	only_proxies() {
		local out
		out=$(connections) || return 1
		echo "$out"
		! grep -q '^10\.77\.0\.' <<<"$out"
	}
	direct_to_node() {
		local out
		out=$(connections) || return 1
		echo "$out"
		grep -q '^10\.77\.0\.' <<<"$out"
	}
	"${dc[@]}" up -d --wait --build cassandra-1 px-1 client >/dev/null 2>&1 || fail "cassandra: up"
	check "cassandra single node: every connection goes to a proxy" only_proxies
	"${dc[@]}" up -d --wait cassandra-2 px-2 >/dev/null 2>&1 || fail "cassandra-2: up"
	check "cassandra two nodes: the driver connects to a node's real IP" direct_to_node
	"${dc[@]}" down -v >/dev/null 2>&1
}

for t in ${@:-kafka redis mongo cassandra}; do
	echo "== $t"
	"$t"
done
if [ "$failures" -gt 0 ]; then
	echo "$failures case(s) failed"
	exit 1
fi
echo "all cases as expected"

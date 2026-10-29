# 200 keys hash to slots on all three primaries; redis-cli -c follows MOVED to whatever address it is given.
set -eo pipefail
shards=$(redis-cli -h redis-1 cluster shards </dev/null | grep -A1 -w hostname | grep -v -- '--' | paste - - | sort -u)
echo "$shards"
[ "$(wc -l <<<"$shards")" = 3 ] || { echo "FAIL slots don't cover all three primaries"; exit 1; }
for i in $(seq 1 200); do redis-cli -c -h redis-1 set "k$i" "v$i" </dev/null >/dev/null; done
ok=0; for i in $(seq 1 200); do [ "$(redis-cli -c -h redis-1 get "k$i" </dev/null)" = "v$i" ] && ok=$((ok+1)); done
echo "read_back=$ok"; [ "$ok" = 200 ]

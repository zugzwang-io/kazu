# Six partitions whose leaders must cover all three brokers; producing and consuming 300 messages needs each.
set -eo pipefail
B=/opt/kafka/bin; BS=kafka-1:9092
$B/kafka-topics.sh --bootstrap-server $BS --create --topic orders --partitions 6 --replication-factor 3 >/dev/null
leaders=$($B/kafka-topics.sh --bootstrap-server $BS --describe --topic orders | grep -o 'Leader: [0-9]' | sort -u)
echo "$leaders"
[ "$(wc -l <<<"$leaders")" = 3 ] || { echo "FAIL leaders don't cover all three brokers"; exit 1; }
seq 1 300 | $B/kafka-console-producer.sh --bootstrap-server $BS --topic orders --producer-property acks=all --producer-property delivery.timeout.ms=20000 --producer-property request.timeout.ms=5000
n=$($B/kafka-console-consumer.sh --bootstrap-server $BS --topic orders --from-beginning --group g1 --timeout-ms 15000 2>/dev/null | wc -l)
echo "consumed=$n"; [ "$n" = 300 ]

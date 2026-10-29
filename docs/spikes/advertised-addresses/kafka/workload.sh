# Six partitions spread over all three leaders; producing and consuming 300 messages needs every broker.
set -e
B=/opt/kafka/bin; BS=kafka-1:9092
$B/kafka-topics.sh --bootstrap-server $BS --create --if-not-exists --topic orders --partitions 6 --replication-factor 3 >/dev/null
$B/kafka-topics.sh --bootstrap-server $BS --describe --topic orders | grep -o 'Leader: [0-9]' | sort | uniq -c
seq 1 300 | $B/kafka-console-producer.sh --bootstrap-server $BS --topic orders --producer-property acks=all --producer-property delivery.timeout.ms=20000 --producer-property request.timeout.ms=5000
n=$($B/kafka-console-consumer.sh --bootstrap-server $BS --topic orders --from-beginning --group g1 --timeout-ms 15000 2>/dev/null | wc -l)
echo "consumed=$n"; [ "$n" = 300 ]

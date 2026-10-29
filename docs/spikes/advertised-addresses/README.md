# Spike: servers that hand out their own addresses

**Date:** 2026-10-28 · **Ran on:** Colima (arm64), with `kazu proxy` from main · **Reproduce:** `./run.sh [kafka] [redis] [mongo] [cassandra]`

## Question

Kazu routes an edge `a -> b` through a proxy by giving `a` a hosts-file entry that maps `b`'s name to the proxy. Clustered servers don't stay at the address the client was configured with. After the first connection they hand out their own addresses: Kafka in metadata (advertised listeners), Redis Cluster in `MOVED` redirects and `CLUSTER SHARDS`, MongoDB in the replica-set member list, Cassandra in `system.peers`. Can each one be configured in docker compose so that every connection still goes through a proxy, with no protocol-specific support in Kazu?

## Answer

Yes for Kafka, Redis Cluster and MongoDB: one setting makes each advertise names, and every byte then goes through the proxies. Single-node Cassandra needs nothing. Multi-node Cassandra can't be fixed by configuration: the driver connects straight to peers' IPs.

| System | Setting | Configured with names | Control |
|---|---|---|---|
| Kafka 3.8, 3 KRaft brokers (Java client) | `KAFKA_ADVERTISED_LISTENERS=PLAINTEXT://kafka-N:9092` | Pass: 6 partitions led by all 3 brokers; 300 messages produced (`acks=all`) and consumed | Advertising IPs: bootstrap goes through the proxy, then the client dials `10.77.0.1N` |
| Redis Cluster 7.4, 3 primaries (`redis-cli -c`) | `cluster-announce-hostname redis-N` and `cluster-preferred-endpoint-type hostname` | Pass: 200 keys written and read back across all 3 primaries | `cluster-preferred-endpoint-type ip`: `MOVED` sends the client to a container IP |
| MongoDB 7.0, 3-member replica set (mongosh) | `rs.initiate` with `mongo-N:27017` hosts | Pass: client seeded with `mongo-1` only finds `mongo-3` as primary; 100 writes with `w: 3` | Members as IPs: the client dials `10.77.0.11` |
| Cassandra 5.0, one node (gocql) | none | Pass: every connection goes to the proxy | — |
| Cassandra 5.0, two nodes (gocql) | none exists | The driver opens a direct connection to node 2's real IP | — |

For Cassandra, `broadcast_rpc_address` rejects a hostname (`Unknown broadcast_rpc_address 'cassandra-2'`), and `system.peers.rpc_address` is an `inet` column, so it can't hold a name. The fix is client-side: the driver's address translator, which is a code change.

## Other findings

- **No `names:` or `ports:` edge config was needed.** Each server advertised its compose service name, and one proxy port per node was enough: Kafka's controller port and the Redis cluster bus carry only node-to-node traffic. One declared edge per node (`checkout -> kafka-1`, `checkout -> kafka-2`, ...) works with the design as it stands.
- **`redis-cli --cluster create` accepts hostnames** (7.4). Setup doesn't need IPs either.
- **Drivers can route around a node silently.** With node 2's IP unreachable, gocql sent every query through node 1, and the workload succeeded. In a real trial nothing makes the IP unreachable, so the same driver would just connect to node 2 directly. The first connection went through the proxy, so a landed check that only asks "did each fault apply at least once" would report the fault as landed even though later traffic skipped it.
- **Redis Cluster needs a moment after `create`.** Commands return `CLUSTERDOWN` until every node reports `cluster_state:ok`; the init job waits for it.

## Method

Each system runs on a `core` network with its nodes. One `kazu proxy` per node sits on `core` and on an `internal` `edge` network. The client is on `edge` only, with `extra_hosts` mapping each node name to its proxy. Its only route to a node is through a proxy, so a workload that succeeds is proof that nothing bypassed one. Each workload is built so it can't succeed through a single node: partition leaders or hash slots spread over every node, or a primary that has to be discovered. The control configures the system to hand out IPs and passes only when the client fails because it dialed a real node address ("unreachable").

Cassandra's driver routes around unreachable nodes, so the failure method can't detect a bypass there. Its client is on both networks instead, and reads its own connections from `/proc/net/tcp`: a connection to `10.77.0.0/24` went around the proxy.

Every case passes only on positive evidence. Running the names case through the control's check fails, and a client that never ran fails both kinds of check.

## Not covered

- **One client per system.** Other libraries (franz-go, go-redis, Lettuce, the official Mongo drivers) should behave the same if they dial the address the server hands out, but none was run. Redis Cluster hostname support depends on the client library.
- **Not tried:** TLS, SASL, Redis Sentinel, sharded MongoDB, and the other systems that hand out addresses (Pulsar, NATS clusters, Elasticsearch sniffing, service registries).
- **Not on a GitHub-hosted runner.** The setup doesn't depend on the host beyond Docker networks; `spike-network` already proved those on a runner.

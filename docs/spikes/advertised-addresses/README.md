# Spike: servers that hand out their own addresses

**Date:** 2026-10-28 · **Ran on:** Colima (arm64), with `kazu proxy` from main · **Reproduce:** `./run.sh [kafka] [redis] [mongo] [cassandra]` (default: all; about 10 minutes)

The repo must sit under a path Colima shares with its VM (by default `$HOME`), or the workload bind mounts come up as empty directories. Run one copy at a time per Docker engine, because every case uses the subnets `10.77.0.0/24` and `10.77.1.0/24`.

## Question

Kazu routes an edge `a -> b` through a proxy by giving `a` a hosts-file entry that maps `b`'s name to the proxy. Clustered servers don't stay at the address the client was configured with. After the first connection they hand out their own addresses: Kafka in metadata (advertised listeners), Redis Cluster in `MOVED` redirects and `CLUSTER SHARDS`, MongoDB in the replica-set member list, Cassandra in `system.peers`. Can each one be configured in docker compose so that every connection still goes through a proxy, with no protocol-specific support in Kazu?

## Answer

Yes for Kafka, Redis Cluster and MongoDB, with the clients tested: one setting makes each advertise names, and every byte then goes through the proxies. Single-node Cassandra needs nothing. Multi-node Cassandra can't be fixed by configuration: the driver connects straight to peers' IPs.

| Case | Setting | Result |
|---|---|---|
| Kafka 3.8, 3 KRaft brokers (Java client) | `KAFKA_ADVERTISED_LISTENERS=PLAINTEXT://kafka-N:9092` | Pass: partition leaders on all 3 brokers (asserted); 300 messages produced (`acks=all`) and consumed |
| ↳ control | advertised listeners as IPs | Bootstrap goes through the proxy, then the client dials `10.77.0.1N:9092` |
| Redis Cluster 7.4, 3 primaries (`redis-cli -c`) | `cluster-announce-hostname redis-N` and `cluster-preferred-endpoint-type hostname` | Pass: slots on all 3 primaries (asserted); 200 keys written and read back |
| ↳ control | `cluster-preferred-endpoint-type ip` | `MOVED` sends the client to `10.77.0.1N:6379` |
| MongoDB 7.0, 3-member replica set (mongosh) | `rs.initiate` with `mongo-N:27017` hosts | Pass: client seeded with `mongo-1` only finds `mongo-3` as primary, writes 100 documents with `w: 3`, then dials every member the set reports |
| ↳ control | members registered as IPs | The client dials `10.77.0.11:27017` |
| Cassandra 5.0, one node (gocql) | none | Pass: the driver dials only the proxy |
| Cassandra 5.0, two nodes (gocql) | none | The driver dials node 2's real IP, `10.77.0.12:9042` |
| ↳ with `broadcast_rpc_address: cassandra-2` | | Same. Cassandra accepts the name but resolves it at startup; `system.peers.rpc_address` is an `inet` column and holds `10.77.0.12` |
| ↳ with node 2's real IP unreachable | | The dial to `10.77.0.12` fails, and the workload still succeeds through node 1 |

For Cassandra the only fix is client-side: the driver's address translator, which is a code change.

## Other findings

- **No `names:` or `ports:` edge config was needed.** Each server advertised its compose service name, and one proxy port per node was enough: Kafka's controller port and the Redis cluster bus carry only node-to-node traffic. One declared edge per node (`checkout -> kafka-1`, `checkout -> kafka-2`, ...) works with the design as it stands.
- **`redis-cli --cluster create` accepts hostnames** (7.4). Setup doesn't need IPs either.
- **Drivers can route around a node silently** (the last Cassandra row). In a real trial nothing makes the IP unreachable, so the driver simply connects to node 2 directly. The first connection went through the proxy, so a landed check that only asks "did each fault apply at least once" would report the fault as landed even though later traffic skipped it.
- **Redis Cluster needs a moment after `create`.** Commands return `CLUSTERDOWN` until every node reports `cluster_state:ok`; the init job waits for it.

## Method

Each system runs on a `core` network. The nodes have fixed addresses `10.77.0.11`–`13`, and everything else on `core` is assigned from `10.77.0.128/25`, so only a node can hold those addresses. One `kazu proxy` per node sits on `core` and on an `internal` `edge` network. The client is on `edge` only, with `extra_hosts` mapping each node name to its proxy, so its only route to a node is through a proxy. Each workload is built so it can't succeed through a single node: partition leaders and hash slots are asserted to cover every node, and Mongo's primary has to be discovered and every member dialed. A workload that succeeds is therefore proof that nothing bypassed a proxy. The control configures the system to hand out IPs and passes only when the client fails because it dialed a real node address (the output must name `10.77.0.1N` on the service port and report it unreachable).

Cassandra's driver routes around unreachable nodes, so a failure can't prove anything there. Its client records every address the driver dials, including failed dials and connections it later closed. Except for the "unreachable" case, the client is also on `core`, so a bypass succeeds and shows up in that list.

Every case passes only on positive evidence. A failed setup fails the case instead of checking it, and an unknown case name fails the run. Mutations that were checked to fail:
- running a names case through the control's check (Mongo and Redis);
- running the "unreachable" Cassandra case with the client on `core`.

## Not covered

- **One client per system.** Other libraries (franz-go, go-redis, Lettuce, the official Mongo drivers) should behave the same if they dial the address the server hands out, but none was run. Redis Cluster hostname support depends on the client library, and `redis-cli -c` only follows `MOVED`; it doesn't read the slot map the way cluster-aware clients do.
- **Not tried:** TLS, SASL, Redis Sentinel, sharded MongoDB, and the other systems that hand out addresses (Pulsar, NATS clusters, Elasticsearch sniffing, service registries).
- **Not on a GitHub-hosted runner.** The setup doesn't depend on the host beyond Docker networks; `spike-network` already proved those on a runner.

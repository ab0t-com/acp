---
name: acp-cluster
description: Deploy and operate ACP in HIGH-AVAILABILITY cluster mode (Raft) — bootstrap a 3- or 5-node coordd cluster, join nodes, understand leader election / write-forwarding / failover, do rolling upgrades, back up, and troubleshoot. Use this skill whenever setting up multi-node ACP, making coordd highly available, removing the single point of failure, handling leader/quorum/failover questions, or deploying ACP for production / commercial distribution.
---

# ACP High-Availability (Raft Cluster)

How to run `coordd` as a fault-tolerant cluster. Full design + guarantees:
`../../14_production_spec_2026-06-10.md`; ops runbook: `../../acp/docs/OPERATIONS.md`.

## Model (read first)

- A cluster is a **Raft group** of an odd number of nodes (**3** typical, 5 for more
  fault tolerance). One is the **leader**; it accepts writes, replicates each to a
  **majority**, then all nodes apply it. **Reads** are served by any node (slightly
  stale); **writes** sent to a follower are **forwarded to the leader**.
- Tolerates losing a **minority** (1 of 3, 2 of 5). On leader loss a new one is elected
  in ~1s with **no committed-data loss**.
- Single-node mode (no `-raft-addr`) = no HA, same API. Cluster mode is opt-in.
- Each node keeps its own Raft log (BoltDB) + periodic snapshots; the per-space stores
  are the deterministic FSM state.

## Bring up a 3-node cluster

Pick a shared token, per-node ids/addresses, and a `-peers` map (id→https URL, used for
write-forwarding). Run raft + http ports on a **private subnet**.

```bash
SHARED=<token>
PEERS=n1=https://h1:8443,n2=https://h2:8443,n3=https://h3:8443

# node 1 — bootstrap
coordd -token $SHARED -addr 0.0.0.0:8443 -data /srv/acp -hosts h1,IP1 \
       -node-id n1 -raft-addr IP1:8444 -raft-bootstrap -peers $PEERS
# node 2, node 3 — join
coordd -token $SHARED -addr 0.0.0.0:8443 -data /srv/acp -hosts h2,IP2 \
       -node-id n2 -raft-addr IP2:8444 -join https://h1:8443 -peers $PEERS
coordd -token $SHARED -addr 0.0.0.0:8443 -data /srv/acp -hosts h3,IP3 \
       -node-id n3 -raft-addr IP3:8444 -join https://h1:8443 -peers $PEERS
```

Verify: `curl -sk -H "Authorization: Bearer $SHARED" https://h1:8443/v1/cluster/status`
→ `{state:Leader, leader:n1, members:[n1,n2,n3]}`.

## Security: mTLS between nodes + encryption at rest (T22)

**mTLS between nodes (recommended for production):** generate a cluster CA once and copy
it to every node — then the Raft transport and inter-node HTTP are mutually
authenticated + encrypted (otherwise nodes use plain TCP, only acceptable on a trusted
private subnet).
```bash
coordd -init-cluster-ca -data /srv/acp          # writes cluster-ca.pem (+ key)
# copy /srv/acp/cluster-ca.pem AND cluster-ca-key.pem to every node's -data dir
```
On start, each node auto-issues its own cert from the CA (SANs from `-hosts`) and turns
on mTLS for the **Raft transport** automatically (the channel that carries all
replicated data). The inter-node HTTP forward/join channel stays TLS + bearer token.
Use the node's reachable IP in `-raft-addr` (not 0.0.0.0).

**Encryption at rest:** set a 32-byte (or 64-hex) key on every node so blobs (file
contents) are stored AES-256-GCM-encrypted; the SAME key must be on all nodes.
```bash
export ACP_DATA_KEY=<64-hex-chars>     # or: coordd -data-key-file /etc/acp/data.key
```
(For full at-rest coverage of logs/manifest/raft, also put `-data` on an encrypted
volume, e.g. LUKS.)

## Clients don't change

Agents connect to **any** node (same `ACP_SERVER/TOKEN/AGENT/SPACE`). Writes to a
follower are forwarded automatically; if a node dies, point the client at another.
(Behind a load balancer, list all nodes; reads land anywhere, writes forward to leader.)

## Operate

- **Status / leader:** `GET /v1/cluster/status`.
- **Add a node:** start it with `-join https://<any-member>`; it's added as a voter.
- **Failover:** automatic — nothing to do; clients retry against another node.
- **Rolling upgrade:** upgrade one **follower** at a time (stop → replace binary →
  start); do the **leader last** (it steps down cleanly on SIGTERM).
- **Backup:** snapshot each node's `-data`, or rely on replication + periodic off-host
  copy of one node's data. To rebuild a node: wipe its `-data/raft`, restart with `-join`.
- **Auth for prod:** use per-agent tokens/roles (`coordd -mkagent NAME -role …`) instead
  of the shared token.

## Limits to know (v2.0)

- Blob bytes replicate via the Raft log (capped by `-max-body`) — great for code/specs;
  for large binaries offload to object storage (roadmap T20).
- Per-store compaction is single-node; in cluster mode Raft snapshots bound growth, and
  CRDT docs compact via `acp admin compact <doc>` (goes through Raft).
- Run raft + HTTP on a **trusted private network**: intra-cluster forwarding skips TLS
  verification (bearer token still required). Encryption-at-rest / mTLS-between-nodes are
  roadmap (T22).

## Troubleshooting

| Symptom | Fix |
|---|---|
| writes fail with "no leader available" | a follower has no `-peers` entry for the leader, or no quorum (majority down) |
| node won't join | check raft port reachability + token; the join target forwards to the leader |
| split-brain worry | impossible with a correct odd-N quorum; never run an even number expecting HA |
| slow writes | quorum fsync latency; keep nodes on a low-latency network; don't oversize the cluster |
| a node fell far behind | it auto-catches up via snapshot install; if corrupt, wipe `-data/raft` + re-join |

Verified: unit determinism + in-process 3-node failover tests, plus a live 3-process
HTTP cluster (follower-forwarded writes, replication, leader-kill failover with
continued writes). See `../../tasklist_20260610_t15_ha.md`.

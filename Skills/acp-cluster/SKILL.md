---
name: acp-cluster
description: Deploy and operate ACP in HIGH-AVAILABILITY cluster mode (Raft, BETA) — decide whether one server plus backups is enough, bootstrap a 3- or 5-node coordd cluster, join nodes, understand leader election / write-forwarding / failover, do rolling upgrades, back up, and troubleshoot. Use this skill whenever setting up multi-node ACP, making coordd highly available, removing the single point of failure, handling leader/quorum/failover questions, or deploying ACP for production / commercial distribution.
---

# ACP High-Availability (Raft Cluster)

> **Cluster mode is BETA.** Single-server mode is the supported default. Run the **same version on every node**,
> and read the "Cluster mode is BETA — known limitations" section of `docs/OPERATIONS.md` before you start.

How to run `coordd` as a fault-tolerant cluster. Ops reference: `docs/OPERATIONS.md` §2c in the ACP repo
(https://github.com/ab0t-com/acp/blob/main/docs/OPERATIONS.md); choosing a deployment: `docs/RUN-ACP.md`.

## Do you need a cluster?

Usually not. **One server plus scheduled off-machine backups** survives restarts with nothing lost (state is on
disk) and is far simpler. Pick a cluster only when the server being down for a while is unacceptable, you can run
3 machines on a private, low-latency network, and you accept beta. A cluster is not a backup: a mistaken delete
replicates to every node (history and restore still undo it), so keep backups either way.

## Model (read first)

- A cluster is a **Raft group** of an odd number of nodes (**3** typical, 5 for more
  fault tolerance). One is the **leader**; it accepts writes, replicates each to a
  **majority**, then all nodes apply it. **Reads** are served by any node (slightly
  stale); **writes** sent to a follower are **forwarded to the leader**.
- Tolerates losing a **minority** (1 of 3, 2 of 5). On leader loss a new one is elected
  in ~1s with **no committed-data loss**.
- Single-node mode (no `-raft-addr`) = no HA, same API. Cluster mode is opt-in.
- Each node keeps its own Raft log + periodic snapshots; the per-space stores
  are the deterministic FSM state.

## Bring up a 3-node cluster

Pick per-node ids/addresses and a `-peers` map (id→https URL, used for write-forwarding). Run raft + http ports
on a **private subnet**, and set up the cluster CA (next section) before the first start. The example below uses
one shared key for the nodes; see "Logins on a cluster" for one login per agent.

```bash
SHARED=<token>
PEERS=n1=https://h1:8443,n2=https://h2:8443,n3=https://h3:8443

# node 1 — bootstrap
coordd -token $SHARED -addr 0.0.0.0:8443 -data /srv/acp -hosts h1,IP1,localhost,127.0.0.1 \
       -node-id n1 -raft-addr IP1:8444 -raft-bootstrap -peers $PEERS
# node 2, node 3 — join
coordd -token $SHARED -addr 0.0.0.0:8443 -data /srv/acp -hosts h2,IP2,localhost,127.0.0.1 \
       -node-id n2 -raft-addr IP2:8444 -join https://h1:8443 -peers $PEERS
coordd -token $SHARED -addr 0.0.0.0:8443 -data /srv/acp -hosts h3,IP3,localhost,127.0.0.1 \
       -node-id n3 -raft-addr IP3:8444 -join https://h1:8443 -peers $PEERS
```

Verify: `curl -sk -H "Authorization: Bearer $SHARED" https://h1:8443/v1/cluster/status`
→ `{state:Leader, leader:n1, members:[n1,n2,n3]}`.

## Security: mTLS between nodes + encryption at rest

**mTLS between nodes (recommended for any multi-host cluster):** generate a cluster CA once and copy
it to every node — then the Raft transport and the inter-node HTTP channel are mutually
authenticated + encrypted. Without a CA, nodes trust their network: coordd logs a loud startup
warning, and that is only acceptable on a trusted private subnet.
```bash
coordd -init-cluster-ca -data /srv/acp          # writes cluster-ca.pem (+ key)
# copy /srv/acp/cluster-ca.pem AND cluster-ca-key.pem to every node's -data dir
```
On start, each node auto-issues its own cert from the CA (SANs from `-hosts`) and turns
on mTLS for the **Raft transport** (the channel that carries all replicated data) and for the
inter-node HTTP forward/join channel. Agents are unaffected: they keep pinning each node's own
self-signed certificate. Use the node's reachable IP in `-raft-addr` (not 0.0.0.0).

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
- **Auth for prod:** one login per agent (see below), not the shared key.

## Logins on a cluster (v0.2.4)

Per-agent identities are **replicated**: a login made or revoked through the API (`POST`/`DELETE
/v1/admin/grants`) or created by an invite (`acp invite` / `acp join`) applies on every node at once, and a node that
joins later receives it. After the first leader shares its existing table, `-mkagent`/`-rmagent` refuse to edit the
file; use the API or invites.

To run one login per agent: start the first node with `-raft-bootstrap` on a **new** data folder and **without**
`-token`. Its first start makes the admin login `owner` (token in `/srv/acp/owner.token`, 0600) and, when it first
leads, replicates it to the cluster. Only that node mints; a node that joins never makes its own owner, and it
receives the cluster's table. A joining node authenticates its join with its own `-token` value (e.g.
`-token "$(cat owner.token)"`, copied from the first node), which must then be an **unscoped admin token**, because
the shared key stops working once logins exist. (The shared-key recipe above, `-token $SHARED` on every node, is
unchanged.) (This order isn't covered by a
published walkthrough yet; rehearse it on scratch machines.) Upgrade every node to v0.2.4 before the first write
after upgrading: this release raises the replicated command and snapshot versions, and an older node stops rather
than diverge.

## Limits to know

- **Beta.** Run one version on every node; upgrade every node before using a feature that adds a new kind of
  replicated write (see OPERATIONS §2c). `-max-docs` and `-idem-window` must be identical on every node.
- Blob bytes replicate via the Raft log (capped by `-max-body`): fine for code and specs, not for large binaries.
  There is no object-storage offload.
- Compaction is driven by the leader through the replicated log, so every node compacts identically; blob GC
  runs through the log too (its `grace_sec` window doesn't apply under Raft).
- Run raft + HTTP on a **private network**, with the cluster CA. Encryption at rest (`-data-key-file`, the same
  key on every node) covers blob contents; put `-data` on an encrypted volume for the rest.

## Troubleshooting

| Symptom | Fix |
|---|---|
| writes fail with "no leader available" | a follower has no `-peers` entry for the leader, or no quorum (majority down) |
| node won't join | check raft port reachability + token; the join target forwards to the leader |
| split-brain worry | impossible with a correct odd-N quorum; never run an even number expecting HA |
| slow writes | quorum fsync latency; keep nodes on a low-latency network; don't oversize the cluster |
| a node fell far behind | it auto-catches up via snapshot install; if corrupt, wipe `-data/raft` + re-join |

Tested: deterministic-replay unit tests, in-process 3-node failover tests, and a live 3-process cluster
(follower-forwarded writes, replication, leader-kill failover with continued writes).

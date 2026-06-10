# ACP Operations Runbook

How to deploy, run, and maintain `coordd` and connect agents.

## 1. Deploy the daemon

Pick a host both agents can reach (a cloud VM, or one of the agents' machines).

```bash
go build -o coordd ./cmd/coordd
./coordd -addr :8443 -data /srv/acp -hosts <public-host-or-ip>,127.0.0.1
```

First run creates under `-data`: `token` (shared bearer secret), `cert.pem`/`key.pem`
(TLS identity), and the state dirs. Note the log lines printing the cert path.

### Flags
| Flag | Default | Meaning |
|------|---------|---------|
| `-addr` | `:8443` | listen address |
| `-data` | `./acp-data` | state directory |
| `-token` | (auto) | shared bearer token (else read/created at `<data>/token`) |
| `-hosts` | `127.0.0.1,localhost` | SANs baked into the self-signed cert |
| `-enforce-leases` | `true` | reject commits to a path another agent holds a lease on |
| `-max-body` | `268435456` (256MB) | max request body |
| `-rate` | `50` | per-agent request rate (req/s), burst = 4× |
| `-event-retention N` | `0` | keep only the last N events (0 = unlimited); periodic compaction |
| `-mkagent NAME` | — | create a per-agent token, print it, exit (switches to per-agent mode) |
| `-role admin\|writer\|reader` | `writer` | role for `-mkagent` |

## 2. Auth modes

- **Shared token (default):** one secret in `<data>/token`; every agent uses it; agent
  id comes from the (trusted) `X-ACP-Agent` header. Fine for two cooperating agents.
- **Per-agent (recommended for >2 or any distrust):** run `coordd -data … -mkagent
  claudeA` and `-mkagent codexB`. This creates `<data>/agents.json` (token→agent) and
  switches the daemon to per-agent mode: identity is derived from the token (header
  can't spoof it), and the shared token stops working. **Revoke** an agent by deleting
  its line from `agents.json` and restarting.

## 2b. Spaces (multi-tenancy)

One daemon hosts many **isolated spaces** — each its own shared filesystem + channels
(event log, mailbox, leases, manifest/blobs, CRDT docs, presence) under
`-data/spaces/<space>/`. Pick a space per client with `ACP_SPACE` (default `default`);
spaces share nothing. This is how multiple independent collaborations — or one agent
working with several different teams/services — coexist on one daemon without seeing
each other.

- A space is created on first use; list them: `acp spaces` or `GET /v1/spaces`.
- `acp stats` reports the current space; pass `ACP_SPACE=...` to target another.
- On one box, run many harnesses against many servers/spaces freely — client state is
  namespaced per (server, space, agent) under `.acp/<ns>/` and `.acp-crdt/`, so nothing
  collides. Set `ACP_PROFILE` to give a connection a readable local namespace label.
- Use distinct `ACP_AGENT` (or per-agent tokens) per harness; the daemon warns on `beat`
  if one agent id is used by two live processes.

## 2c. High availability (cluster mode)

For production / no single point of failure, run a **3-node Raft cluster** (tolerates 1
node down, ~1s failover, no committed-data loss). Bootstrap one node, `-join` two more;
clients are unchanged. Full steps + ops (rolling upgrade, backup, failover) are in the
**`acp-cluster` skill** and `../../14_production_spec_2026-06-10.md`. Single-node mode
(no `-raft-addr`) remains the default for dev/small setups.

## 3. Connect an agent

Copy `cert.pem` (public) and the agent's token (secret) to its machine.

```bash
export ACP_SERVER=https://<host>:8443
export ACP_CERT=/path/to/cert.pem
export ACP_TOKEN=<token>
export ACP_AGENT=claudeA            # unique per agent
acp health && acp beat && acp who
```

## 4. Day-2 maintenance

- **Health/monitoring:** `acp health`; `acp stats` (or `GET /v1/stats`) for live counts;
  request logs go to stdout (`METHOD path -> code agent=… dur`).
- **Roles:** in per-agent mode, mint scoped tokens: `coordd -mkagent bot -role reader`
  (admin|writer|reader). Revoke by removing the line from `agents.json` + restart.
- **Blob GC:** unreferenced blobs accumulate (e.g. after deletes/overwrites). Reclaim:
  `acp admin gc --grace 600` (keeps blobs newer than 10 min to avoid racing an
  in-flight push). Safe to run live; schedule it (cron) if churn is high.
- **Compaction:** event log compacts automatically when `-event-retention` is set.
  CRDT op-logs compact automatically once they grow past ~4× their live size; force it
  with `acp admin compact [doc]`. Both bound on-disk/RAM growth.
- **MCP bridge:** to let an MCP harness use ACP, register `acp-mcp` with the same env:
  `claude mcp add acp -- env ACP_SERVER=… ACP_TOKEN=… ACP_CERT=… ACP_AGENT=bot /path/to/acp-mcp`.
- **Backups:** the entire state is the `-data` dir. Stop the daemon (SIGTERM, clean
  shutdown) or snapshot the filesystem, then copy `-data`. The event log and CRDT
  op-logs are append-only JSONL; the manifest/leases are JSON; blobs are immutable.
- **Restart:** stop with SIGINT/SIGTERM (drains connections, flushes logs — "stopped
  cleanly"). On start everything is replayed; event `Seq`, manifest version, leases,
  and CRDT docs all resume.

## 5. Failure handling

| Symptom | Cause / fix |
|---------|-------------|
| `401` on every call | wrong token, or per-agent mode active and you used the shared token |
| `429` | rate limit; raise `-rate` or back off |
| `423` on push/commit | a path is leased by another agent — wait/coordinate, or the holder releases |
| `409` on push | manifest moved; `push` auto-rebases. `409` on lease = held by someone live |
| watcher CPU/spin | fixed: `watch`/`crdt watch` back off 1s on reconnect |
| disk filling | run `acp admin gc`; (event/CRDT log compaction is roadmap — T11/T12) |
| daemon down | single point of failure today (HA via Raft is roadmap T15); restart it — state is durable |

## 6. Capacity notes

Good for a few agents and code/spec-sized workspaces. The event log and CRDT op-logs
are kept in RAM (durable on disk); very large/long-lived deployments need compaction
(roadmap). fsync-per-write bounds throughput to the disk's sync rate — ample for
agent coordination, not for high-frequency telemetry.

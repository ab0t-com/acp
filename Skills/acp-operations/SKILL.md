---
name: acp-operations
description: Deploy, secure, and operate the ACP coordination daemon (coordd) in production — TLS certs, shared vs per-agent tokens and roles, optional external reverse-proxy/token gateway, blob GC and log compaction, backups, monitoring/stats, graceful restart, and troubleshooting. Use this skill whenever setting up or running coordd, exposing it to another machine, rotating/revoking agent tokens, deciding whether an external proxy is needed, handling 401/403/409/423/429 errors, reclaiming disk, backing up state, or diagnosing a misbehaving ACP deployment.
---

# Operating ACP (coordd)

How to stand up and run the daemon so two agents on different machines can use it.
Companion runbook: `../../acp/docs/OPERATIONS.md`; security model:
`../../acp/docs/THREAT_MODEL.md`; deeper "how it connects" Q&A:
`../../12_technical_qa_2026-06-09.md`.

## Deploy

Run `coordd` on any host BOTH agents can reach (a small cloud VM, or co-located with
one agent). It needs no database and no VPN.

```bash
cd acp && go build -o coordd ./cmd/coordd
./coordd -addr :8443 -data /srv/acp -hosts <public-host-or-ip>,127.0.0.1
```

First run creates `/srv/acp/{token, cert.pem, key.pem, ...}`. Distribute to each agent:
- `cert.pem` (public — clients pin it as their only trusted root)
- the agent's token (secret)

Key flags: `-enforce-leases` (default on), `-max-body`, `-rate` (req/s/agent),
`-event-retention N` (compact event log to last N), `-mkagent NAME -role …`.

## Security & "do we need the external proxy?"

ACP secures itself end-to-end: **TLS 1.2+** (client pins the self-signed cert →
authenticated, no MITM) **+ a bearer token** per request, **+ identity-from-token** in
per-agent mode. So a separate proxy is **not required** for security.

Use the **external token proxy** (the one we can wire with a token) only if it buys
something ACP doesn't:
- you can't open a port to the daemon host (proxy as ingress / NAT traversal), or
- you want a single public endpoint / WAF / centralized access logging, or
- you want a second, independent auth layer in front.

If you use it: terminate or pass-through TLS to coordd, add the proxy's token as an
*extra* header, and keep ACP's own token+cert (don't disable them). It's defense-in-
depth, not a replacement. **No DNS proxy is needed** — clients reach the daemon by IP or
hostname directly. See `../../12_technical_qa_2026-06-09.md` for the full rationale.

## Identity & roles

- **Shared-token (default):** one secret; every agent uses it; fine for 2 trusted agents.
- **Per-agent (recommended for >2 or any distrust):**
  `./coordd -data /srv/acp -mkagent claudeA -role writer` → prints a token, writes
  `agents.json`, switches to per-agent mode (shared token stops working). Roles:
  `admin` (all), `writer` (read+write), `reader` (GET + presence). **Revoke** by
  deleting the line from `agents.json` and restarting.

## Day-2

- **Monitor:** `acp stats` / `GET /v1/stats` (events, leases, docs, blobs, base seq,
  uptime). Request logs print to stdout: `METHOD path -> code agent dur`.
- **Reclaim disk:**
  - blobs: `acp admin gc --grace 600` (admin role; keeps blobs <10 min old to avoid
    racing an in-flight push).
  - event log: set `-event-retention N` (auto-compacts).
  - CRDT docs: auto-compact past ~4× live size, or force `acp admin compact [doc]`.
- **Backup:** the whole state is `-data`. Snapshot the filesystem or stop the daemon
  (SIGTERM → "stopped cleanly") and copy it. Append-only JSONL + JSON + blob files.
- **Restart:** SIGINT/SIGTERM drains and flushes; on start everything is replayed
  (Seq, manifest version, leases, CRDT docs + epochs all resume).
- **Expose to MCP harnesses:** `acp-mcp` *is* an MCP (stdio) server — you don't create
  one, you register it. Wire it into Claude Code / Codex via `easymcp` — full steps in
  [`references/connect-acp-to-easymcp.md`](references/connect-acp-to-easymcp.md).

## Troubleshooting

| Symptom | Meaning / fix |
|---------|---------------|
| `401` | bad token, or per-agent mode active and the shared token was used |
| `403` | role not allowed (reader writing, or non-admin hitting `/v1/admin/*`) |
| `409` (push) | manifest version race — `push` auto-rebases; on lease it's held by someone live |
| `423` (push/commit) | path is leased by another agent — wait/coordinate |
| `429` | rate limit; raise `-rate` or back off |
| disk growing | run GC + enable `-event-retention`; force `admin compact` |
| can't connect | check the port is reachable host→host; cert SANs include the host (`-hosts`); consider the external proxy for ingress |
| daemon down | single point of failure today (HA/Raft is roadmap T15) — restart; state is durable |

## Capacity

Good for a few agents on code/spec-sized workspaces. fsync-per-write bounds throughput
(ample for coordination). For many agents / huge repos / HA, see TICKETS T11–T15.

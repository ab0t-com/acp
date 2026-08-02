# Storage modes — local mode & server mode (ACPDB)

ACP runs in one of two storage modes. Both speak the exact same protocol (`acp/1`), serve the same
API, and are **indistinguishable to your clients** — the difference is only how the server keeps its
data on disk. You pick the mode by choosing which server binary you run.

> **TL;DR** — Start with **local mode** (the default: one binary, zero dependencies, files you can
> read). Move to **server mode (ACPDB)** when a deployment keeps a large, long-lived history and you
> want **bounded, tunable memory** and **fast restarts** at that scale. You can convert between the
> two at any time, losslessly, in either direction.

## Which mode should I use?

| | **local mode** (default) | **server mode — ACPDB** |
|---|---|---|
| Binary | `coordd` | `coordd-server` |
| Best for | a machine's agents; dev; small–medium deployments | large, long-lived, many-tenant deployments |
| Dependencies | none (single static binary) | none (single static binary — self-contained) |
| Memory at rest | smallest (~15–25 MB) | small, bounded (~48 MB default, tunable) |
| Memory vs history | grows with retained history | **stays flat** — bounded by a configurable cache |
| Restart on big state | replays the log (grows with history) | fast, near-constant open |
| On-disk data | plain append-only files you can `grep` | a compact embedded database |
| One-line pitch | *smallest at rest* | *flat under load* |

**The honest crossover:** local mode is the better choice below roughly **100,000 retained events per
space** — it is smaller at rest and its files are directly inspectable. Above that, server mode's
slightly higher floor buys you a **ceiling**: memory stays flat as history grows, and compaction of a
large history is incremental instead of rewriting whole files. Neither mode is "faster" across the
board — pick by your retention and memory needs, not a benchmark headline.

**No external services.** Neither mode needs Redis, Postgres, or any sidecar. ACP embeds its own
storage — in server mode it **is** the database. One binary, one process, your data.

## Getting the binaries

Both ship with each release:

```bash
# local mode (default) — the standard bundle
curl -fsSL https://raw.githubusercontent.com/ab0t-com/acp/main/install.sh | bash
#   installs: acp, coordd, acp-mcp

# server mode (ACPDB) — the server bundle (separate download)
#   contains: coordd-server, acp-server, acp-mcp-server
#   from the release page: acp-server_<version>_<os>_<arch>.tar.gz
```

## Running server mode

Server mode is just a different daemon binary; everything else is identical.

```bash
coordd-server --data /var/lib/acp --addr :8443
```

Confirm the active mode over the API (operator introspection):

```bash
curl -sk -H "Authorization: Bearer $TOKEN" https://localhost:8443/v1/stats
# -> { ..., "storage": { "engine": "db", "format_version": 2, "metrics": { ... } } }
#    local mode reports "engine": "file".
```

> The `engine` value is a generic mode identifier (`"file"` or `"db"`) — an implementation detail for
> operators, never part of the wire contract your clients depend on.

### Tuning memory (server mode only)

Server mode keeps a bounded in-memory cache. Two optional knobs let you match it to your box; both
default to a conservative small-box setting, so it is safe to run with no tuning at all.

```bash
coordd-server --data /var/lib/acp \
  --mem-budget 512 \          # cache ceiling in MiB (the main memory lever; 0 = conservative default)
  --db-max-compactions 2      # background compaction workers (CPU lever; 0 = default)
```

`--mem-budget` bounds the cache, which is the dominant memory lever — but total process memory is the
cache plus a small fixed overhead plus the Go runtime. On a memory-constrained host, size
`--mem-budget` below your limit with headroom. (These flags have no effect in local mode, which keeps
no cache.)

## Converting between modes (lossless, either direction)

You are never locked in. Conversion is offline and **copy-based — your source directory is left
untouched**. Use the server bundle's tooling (it carries both engines):

```bash
# stop the daemon first
systemctl stop coordd

# local -> server (ACPDB)
acp-server admin migrate-store --from /var/lib/acp --to /var/lib/acp-db
coordd-server --data /var/lib/acp-db --addr :8443

# server (ACPDB) -> local (no lock-in; the reverse is a first-class, tested path)
acp-server admin migrate-store --from /var/lib/acp-db --to /var/lib/acp-file --reverse
coordd --data /var/lib/acp-file --addr :8443
```

Round-trips are logical-state-lossless: events, mailbox, CRDT documents (including compacted ones),
leases with their fencing counters, the blob manifest version, quotas, and the idempotency window all
carry across intact, both directions.

### Inspecting server-mode data without stopping

Local mode keeps plain files you can `grep` live. Server mode stores a compact database, so use the
read-only dump for the same visibility:

```bash
acp-server admin dump --state /var/lib/acp-db --space team \
  --stream crdt/board --from 58000 | grep presence
```

## Notes & limits

- **Default is local mode.** Server mode is strictly opt-in; nothing changes for existing deployments.
- **Clusters:** run one mode per node during steady state; upgrade one node at a time. Cross-mode
  cluster snapshots go through the built-in logical snapshot path (a node refuses a raw image from the
  other mode rather than come up wrong).
- **Encryption at rest:** `--data-key-file` encrypts blob content in both modes. See
  [OPERATIONS.md](OPERATIONS.md) and [THREAT_MODEL.md](THREAT_MODEL.md).
- **Compatibility:** both modes speak `acp/1` unchanged. Your agents, SDKs, the CLI, and `acp-mcp`
  work against either without modification.

## See also
- [OPERATIONS.md](OPERATIONS.md) — running, backup, upgrade
- [DOCKER.md](DOCKER.md) — container deployment
- [API_REFERENCE.md](API_REFERENCE.md) — the full API

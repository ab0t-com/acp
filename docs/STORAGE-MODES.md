# Storage — file storage & database storage

ACP keeps its data in one of two ways: **file storage** (the default) or **database storage**. Other pages and
older releases call these *local mode* / *file mode* and *server mode* / *server DB mode* (ACPDB); they are the
same two options, and `/v1/stats` reports them as `"file"` and `"db"`.

Both speak the exact same protocol (`acp/1`), serve the same
API, and are **indistinguishable to your clients** — the difference is only how the server keeps its
data on disk. You pick one by choosing which server binary you run.

> **TL;DR** — Start with **file storage** (the default: one binary, zero dependencies, files you can
> read). Move to **database storage** when a deployment keeps a large, long-lived history and you
> want **bounded, tunable memory** at that scale. You can convert between the
> two at any time, losslessly, in either direction.

## Which one should I use?

| | **file storage** (default) | **database storage** (ACPDB) |
|---|---|---|
| Binary | `coordd` | `coordd-server` |
| Best for | a machine's agents; dev; small–medium deployments | large, long-lived, many-tenant deployments |
| Dependencies | none (single static binary) | none (single static binary — self-contained) |
| Memory at rest | smallest (~15–25 MB) | small, bounded (~48 MB default, tunable) |
| Memory vs history | grows with retained history | **stays flat** — bounded by a configurable cache |
| Restart on big state | replays the log (grows with history) | fast, near-constant open |
| On-disk data | plain append-only files you can `grep` | a compact embedded database |
| One-line pitch | *smallest at rest* | *flat under load* |

**The honest crossover:** file storage is the better choice below roughly **100,000 retained events per
space** — it is smaller at rest and its files are directly inspectable. Above that, database storage's
slightly higher floor buys you a **ceiling**: memory stays flat as history grows, and compaction of a
large history is incremental instead of rewriting whole files. Neither is "faster" across the
board — pick by your retention and memory needs, not a benchmark headline.

**No external services.** Neither needs Redis, Postgres, or any sidecar. ACP embeds its own
storage — with database storage it **is** the database. One binary, one process, your data.

## Getting the binaries

Both ship with each release:

```bash
# file storage (default) — the standard bundle
curl -fsSL https://raw.githubusercontent.com/ab0t-com/acp/main/install.sh | bash
#   installs: acp, coordd, acp-mcp

# database storage — the server bundle (separate download)
#   contains: coordd-server, acp-server, acp-mcp-server
#   from the release page: acp-server_<version>_<os>_<arch>.tar.gz
```

## Running database storage

Database storage is just a different server binary; everything else is identical.

```bash
coordd-server --data /var/lib/acp --addr :8443
```

Confirm which one is active over the API (operator introspection):

```bash
curl -sk -H "Authorization: Bearer $TOKEN" https://localhost:8443/v1/stats
# -> { ..., "storage": { "engine": "db", "format_version": 2, "metrics": { ... } } }
#    file storage reports "engine": "file".
```

> The `engine` value is a generic storage identifier (`"file"` or `"db"`) — an implementation detail for
> operators, never part of the wire contract your clients depend on.

### Tuning memory (database storage only)

Database storage keeps a bounded in-memory cache. Two optional knobs let you match it to your box; both
default to a conservative small-box setting, so it is safe to run with no tuning at all.

```bash
coordd-server --data /var/lib/acp \
  --mem-budget 512 \          # cache ceiling in MiB (the main memory lever; 0 = conservative default)
  --db-max-compactions 2      # background compaction workers (CPU lever; 0 = default)
```

`--mem-budget` bounds the cache, which is the dominant memory lever — but total process memory is the
cache plus a small fixed overhead plus the Go runtime. On a memory-constrained host, size
`--mem-budget` below your limit with headroom. (These flags have no effect with file storage, which keeps
no cache.)

## Converting between them (lossless, either direction)

You are never locked in. Conversion is offline and **copy-based — your source directory is left
untouched**. Use the server bundle's tooling (it carries both engines):

```bash
# stop the daemon first
systemctl stop coordd

# file -> database
acp-server admin migrate-store --from /var/lib/acp --to /var/lib/acp-db
coordd-server --data /var/lib/acp-db --addr :8443

# database -> file (no lock-in; the reverse is a first-class, tested path)
acp-server admin migrate-store --from /var/lib/acp-db --to /var/lib/acp-file --reverse
coordd --data /var/lib/acp-file --addr :8443
```

Round-trips are logical-state-lossless: events, mailbox, CRDT documents (including compacted ones),
leases with their fencing counters, the blob manifest version, quotas, and the idempotency window all
carry across intact, both directions.

### Inspecting database-storage data without stopping

File storage keeps plain files you can `grep` live. Database storage stores a compact database, so use the
read-only dump for the same visibility:

```bash
acp-server admin dump --state /var/lib/acp-db --space team \
  --stream crdt/board --from 58000 | grep presence
```

## Notes & limits

- **The default is file storage.** Database storage is strictly opt-in; nothing changes for existing deployments.
- **Clusters (beta):** run one storage type per node during steady state; upgrade one node at a time. Cross-storage
  cluster snapshots go through the built-in logical snapshot path (a node refuses a raw image from the
  other storage type rather than come up wrong).
- **Encryption at rest:** `--data-key-file` encrypts shared file contents with either. See
  [OPERATIONS.md](OPERATIONS.md) and [THREAT_MODEL.md](THREAT_MODEL.md).
- **Compatibility:** both speak `acp/1` unchanged. Your agents, SDKs, the CLI, and `acp-mcp`
  work against either without modification.

## See also
- [RUN-ACP.md](RUN-ACP.md) — what to pick and the shortest path
- [OPERATIONS.md](OPERATIONS.md) — running, backup, upgrade
- [DOCKER.md](DOCKER.md) — container deployment
- [API_REFERENCE.md](API_REFERENCE.md) — the full API

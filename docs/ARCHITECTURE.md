# How ACP works — and why it's fast at what it's for

> A database is fast at exactly the operations its **data structure**, **storage engine**, and
> **consistency model** make cheap — and slow at the ones they make expensive. Speed isn't a feature you
> bolt on; it's a **shape you commit to**. This page is the shape ACP commits to, and why that makes it
> fast for multi-agent (and multi-human) coordination — the job it's built for.

If you just want to pick a storage mode and run it, see [STORAGE-MODES.md](STORAGE-MODES.md); this page
is the *why* underneath it.

## The core idea: a shared, real-time filesystem

At its heart ACP is a **shared, versioned, content-addressed filesystem** with a comms line on top.
File *contents* are immutable blobs (addressed by SHA-256), a versioned **manifest** maps paths →
hashes, and agents `push` / `pull` / `checkout` real files with 3-way reconciliation — with changes
propagating in real time over a follow-able event stream. Many agents (or people) work on the same body
of files without trampling each other. That shared workspace *is* the product; everything below serves it.

## Right data, right tier

The reason ACP is efficient is that it **refuses to force one storage shape onto everything.** Most
systems pick a single structure (a B-tree, an LSM, a columnar store) and bend every workload onto it.
ACP instead routes each kind of coordination data to the tier whose *cheap* operation matches it:

| Coordination need | Tier / structure | Why it's the right shape |
|---|---|---|
| **Ordered facts** ("what happened, in what order") | a totally-ordered, append-only **event log** | appends are sequential I/O — the cheapest write there is; reads are replay-by-offset |
| **Live co-editing** one hot document | **CRDT** documents (text + JSON) | many writers converge with no locks and no lost updates |
| **Mutual exclusion** across machines | **fencing-token leases** | a stalled holder always loses its token — safe distributed locking |
| **Big opaque bytes** (files, artifacts) | **content-addressed blobs** on the filesystem | dedup + integrity by hash; the wrong thing to stuff in a key-value engine |
| **"Who's here right now"** | an **ephemeral presence tier** | never written to durable storage — cursor spam *structurally cannot* bloat the log |

Each tier is fast because it's only ever asked to do the operation it was built for. That's the whole
trick: your coordination hot-path is somebody else's expensive edge case, and vice-versa.

## The storage engine: two profiles, one wire

ACP ships two storage profiles from one codebase, both speaking the identical `acp/1` wire — a client
cannot tell which one backs the server:

- **Local mode (default).** File-backed, zero dependencies, smallest at rest. The durable log lives in
  plain append-only files you can `grep`. Memory grows with the history you retain — perfect for a
  machine's agents, development, and small-to-medium deployments.
- **Server DB mode (ACPDB).** An embedded **log-structured merge-tree (LSM)** storage engine behind the
  same seam. This is the same engine *family* that powers write-heavy systems like Cassandra and the
  distributed SQL databases — writes buffer in memory, flush to sorted immutable tables, and compact in
  the background. The payoff for ACP: **memory stays flat under any retained history** (bounded by a
  configurable cache, not by how much you keep), compaction is **incremental** instead of rewriting whole
  files, snapshots are atomic checkpoints, and offboarding a tenant is a single range-delete.

There is no external database to run in either mode. **In server mode, ACP *is* the database** — one
static binary, one process, your data.

## Why it's fast — by design, not by accident

- **Append-optimized writes.** The spine is an ordered log; appends are sequential, and **group commit**
  coalesces concurrent writes onto one `fsync` per window (and always `fsync`-before-ack, so an
  acknowledged fact is durable). This is the same shape that makes commit-log systems throughput
  monsters — with coordination semantics on top.
- **Flat memory at scale (server mode).** Because the LSM engine keeps only a bounded cache resident,
  the memory ceiling doesn't rise as your history grows. You size it once; it holds the line.
- **Cheap compaction (server mode).** Reclaiming space is incremental sorted-table merging, not a
  whole-file rewrite — so housekeeping cost scales with churn, not with total data.
- **Presence never touches disk.** The awareness tier is ephemeral by construction, so high-frequency
  "live cursor" traffic costs nothing durable and can't degrade the log.
- **Strongly consistent, and it survives failure.** Every write is a Raft-replicated deterministic
  command with a single-writer leader, so ordering, leases, and state never fork and survive node loss —
  the correctness you need for locks and shared truth, without a second system to run.

## The edge — where ACP wins, and where it shouldn't be used

ACP's advantage is being **correct-and-cheap for coordination** specifically: sharing ordered facts,
messaging, taking exclusive locks, co-editing files and documents, and seeing each other live — as a
single self-hosted substrate, at coordination scale (thousands of agents, HA-first, bounded resources).
Few systems give you the log, the locks, the CRDT, the blobs, *and* presence in one binary.

Being honest about the shape means being honest about what it is **not** for:

- **Analytics / OLAP** — no columnar store, no aggregation engine, no SQL. Reach for ClickHouse, DuckDB,
  or a warehouse.
- **Ad-hoc / secondary-index queries** — you read by coordination access patterns (the event stream,
  a mailbox, a document, a lease, a manifest path), not arbitrary predicates.
- **Web-scale OLTP** (millions of QPS on one key) — that's an adtech-KV shape (DynamoDB, Cassandra,
  Aerospike), not a coordination substrate.
- **A data warehouse / bulk cold storage** — the log is for coordination facts and retention, not
  petabytes.

The rule of thumb: **pick the store whose *cheap* operation is your *hot path*.** If your hot path is
"a fleet of agents and people coordinating on shared state," that's the operation ACP made cheap. If
it's analytics or ad-hoc query, put that behind a purpose-built store and feed it from ACP's event
stream — the same way well-designed systems pair a coordination/OLTP store with a separate analytics one
rather than bending either against its grain.

## See also
- [STORAGE-MODES.md](STORAGE-MODES.md) — pick a mode, run it, tune memory, migrate losslessly
- [OPERATIONS.md](OPERATIONS.md) — deploy, back up, cluster, troubleshoot
- [THREAT_MODEL.md](THREAT_MODEL.md) — the security model
- [API_REFERENCE.md](API_REFERENCE.md) — the wire API

# Changelog

## v0.2.0 — 2026-08-01
Minor release. Introduces the opt-in **server DB storage mode** and ships a broad security +
resource hardening pass across the data plane. Wire protocol `acp/1` is unchanged (additive).
- **Server DB storage mode (ext-10; opt-in, default `file`).** A second build profile embeds a
  write-optimized storage engine for higher write throughput and faster cold start, with a
  bounded, configurable memory footprint (`-mem-budget`). The default file-backed profile is
  unchanged — most deployments keep running it. The server profile ships as separate `acp-server` /
  `coordd-server` / `acp-mcp-server` artifacts (CGO-free/static); storage is byte-identical across
  profiles and migration is lossless both ways.
- **CRDT document delete + reclaim.** `DeleteDoc` frees a document's `-max-docs` slot and reclaims
  its on-disk storage. A per-document generation fence prevents a delete/recreate race from
  silently dropping an in-flight edit. Cluster fence enforcement is controlled by
  `-cluster-crdt-delete-fence` (default on); see `docs/OPERATIONS.md` for rolling-upgrade behavior.
- **Security + resource hardening (data plane).** A broad pass: tightened durable-state file
  permissions, bounded memory / file-descriptor / rate-limit growth axes, hardened CRDT compaction
  and snapshot handling, fail-closed lease and blob consumer paths, safer offline admin tooling, a
  bounded raft commit-wait, and checksum-verified release artifacts. No wire/API change; no user
  action required.

## v0.1.7 — 2026-07-28
Daemon + client release; the wire protocol version `acp/1` is unchanged — every feature is an
additive, capability-negotiated endpoint or field. This release adds a live presence tier,
finishes least-privilege tenancy, and coalesces durable appends for far higher write throughput.
- **Awareness — the ephemeral presence tier (ext-9; capabilities `awareness`, `awareness-ws`).**
  A live, self-expiring channel for *who is here and what they are doing right now* — cursor
  positions, selections, typing/status — kept separate from the durable event log. Each agent
  publishes a small JSON state keyed by `(actor, session)` that expires on a TTL (default 30 s)
  or an explicit leave; everyone else reads a snapshot and follows a delta stream. Two transports:
  `GET /v1/awareness?follow=true` (NDJSON, like the event stream) and an OPTIONAL bidirectional
  **WebSocket** (`?transport=ws`) with a 15 Hz server-side coalescing tick, so a hundred cursor
  moves a second cost a follower at most fifteen frames. **Awareness never touches durable
  storage** — it is never written to the event log, CRDT, or snapshots (guaranteed, not
  incidental), so live presence can never bloat or corrupt the durable record. Surfaces: SDK
  (`SetAwareness`/`Awareness`/`FollowAwareness`/`ClearAwareness`), CLI (`acp awareness
  set|get|watch|clear`), MCP (`acp_awareness_set|get|clear`), and a runnable
  `examples/awareness-cursors/`. Guide: `docs/AWARENESS.md`. This is the missing middle tier
  between the durable log (facts, forever) and the coarse heartbeat roster (alive, hour-scale).
- **Least-privilege tenancy, finished (ext-7; capabilities `tenancy`, `tenancy.subscope`).**
  Completes scoped tokens with **sub-scope project labels** — a lightweight partition *within* a
  space for scoping and accounting (NOT a new isolation boundary; a separate space remains the
  only hard boundary). A grant may be pinned to a `sub_scope` and/or an allowlist of `spaces`
  (server-forced and unforgeable), carried in a versioned, fail-closed grant file — an older
  binary refuses a scoped grant file rather than run it without the scope. Adds `/v1/subscopes`
  discovery, `max_docs` as a per-space replicated quota (a document past the cap → `507` with
  `{used, limit}`), per-sub-scope quotas (the tighter bound governs), and a scope `deny` list
  (for example, a token that may read but never publish presence). SDK/CLI/MCP carry `sub_scope`
  and the quota signal. `tenancy` is advertised only when scoped tokens **and** quotas are
  enforced; `tenancy.subscope` additionally when sub-scopes are.
- **Group commit (ext-10; flag `-group-commit`, default on).** Concurrent durable appends — event
  log and mailbox — now share a single `fsync` per batching window instead of one per append.
  Durability is unchanged (an acknowledged write still survives a crash, verified by a
  crash-injection gate), a lone writer's latency stays within ~1% of before, and burst throughput
  under concurrency improves **15–58×**. Set `-group-commit=false` for the previous
  one-fsync-per-append behavior — an instant, per-daemon rollback.
- **Data-directory lockfile.** A second `coordd` started against a `-data` directory already in
  use now refuses to start with a clear error, instead of two daemons silently corrupting shared
  state.
- **Fixed.** Several cluster-determinism edge cases in the replicated state machine (HA
  correctness) and a connection-handling gap on the new WebSocket upgrade path.
- **Compatibility.** Wire stays `acp/1`; every feature is capability-negotiated, so a client and
  daemon at different feature levels interoperate unchanged. The scoped-grant file gains a new
  version only when sub-scope/`deny` grants exist (older binaries refuse it by design — re-mint to
  downgrade). A document-create refused by a quota now returns `507`. Specs:
  `rfc/acp-ext-9-presence-awareness.txt`, `rfc/acp-ext-7-scoped-tokens-quotas-subtenancy.txt`,
  `rfc/acp-ext-10-storage-engine.txt`.

## v0.1.6 — 2026-07-27
Daemon + client release; the wire protocol version `acp/1` is unchanged. A stability and
hardening release focused on bounded resource use and cluster correctness.
- **Configurable retention across the durable stores.** The event log (`-event-retention`,
  bounded by default) and the mailbox (`-mail-retention`, keeps all unread mail + the most
  recent read messages per recipient) now have first-class retention; presence entries,
  rate-limit state, and free-form event/message fields are size- and count-bounded. Storage
  and memory stay flat under sustained load or churn.
- **Cluster determinism (HA).** Background state changes — lease expiry, blob GC, and
  event/mailbox/CRDT compaction — are now leader-driven and replicated through the log, so
  every node reaches byte-identical state (acp-1 §17); compaction is bounded and gated so it
  never floods the Raft log at scale.
- **Inter-node security (HA).** Every node-to-node channel — the Raft transport and the HTTP
  forward/join channel — is mutually authenticated against the cluster CA when one is
  configured; external client cert-pinning is unchanged (acp-1 §14.4). Configure a cluster CA
  for any multi-host deployment — see `docs/OPERATIONS.md`.
- General input-validation and test-stability hardening.

## v0.1.5 — 2026-07-25
Daemon + client release (the wire protocol version `acp/1` is unchanged — an op and a
negotiated capability, per the extension model).
- **Identity-preserving MOVE for the structured (JSON) CRDT (ext-14).** A fifth op `mv`
  relocates an existing node — a subtree under an object key, or an array element — to a
  new location **without minting a new id**, so a peer's concurrent edit to the moved
  thing follows it instead of being lost. This makes drag-to-reorder, move-item-between-
  columns (kanban), reparent-a-node (outliner/tree), and relocate-a-block **safe on a live
  multi-editor surface**. Guarantees: concurrent moves of one node collapse to a single
  winner (no duplicate); a move that would create a parent cycle is ignored and the node
  **stays put** (nothing vanishes); delete-after-move deletes (no resurrection). All
  deterministic, convergent, and compaction-transparent. Negotiated by a new capability
  string **`crdtjson-move`** (advertised alongside `crdtjson`); `mv` travels through the
  existing `POST /v1/crdt/json/ops` (no new endpoint). The reference JSON-Patch mapper now
  emits a real `mv` for RFC-6902 `move`, and the CLI gains `acp crdt json mv <doc> <from>
  <to>`. Old behavior (delete+reinsert) duplicated the subtree and dropped concurrent
  edits. Spec: `rfc/acp-ext-14-crdt-move.txt`; guide: `docs/CRDT_JSON_MOVE.md`.
- **Performance & memory (structured CRDT).** Reads of a structured document are now
  **O(1) amortized** (the resolved view is memoized between writes), and long-lived or
  heavily-edited documents keep their resident size **bounded to live content** through
  cheaper, automatic compaction (a counter-based trigger replaces a per-document scan).
  Large or overwrite-heavy JSON documents now use materially less memory and CPU under
  sustained load. No API or wire change.
- **Operator note (mixed-version clusters):** `crdtjson-move` is advertised always-on. On a
  multi-node Raft cluster, **upgrade all nodes before relying on `mv`** — a node still on a
  pre-move binary silently no-ops incoming `mv` ops and would diverge. Single-node
  deployments are unaffected.

## v0.1.3 — 2026-07-17
Agent-facing surface (bridge/client only; the daemon and wire protocol `acp/1` are unchanged).
- **`acp-mcp` now bridges the shared filesystem and the structured CRDT to agents.** The MCP tool
  surface — previously comms + leases + the text CRDT only — adds: the shared filesystem
  (`acp_manifest`, `acp_file_get`, `acp_file_put`, `acp_blob_get`, `acp_blob_put`, `acp_commit`)
  and the ext-5 structured JSON CRDT (`acp_crdt_json_get`, `acp_crdt_json_ops`, `acp_crdt_json_list`).
  An LLM agent can now co-edit files and structured documents over MCP, not just send messages.
- The `acp_crdt_json_ops` tool description teaches the op discipline so agents discover it: paths are
  arrays of segments (numeric = array index), the op set is `set`/`del`/`lins`/`ldel`, and **arrays
  are edited with `lins`/`ldel`, never a whole-array `set`** (which would silently drop concurrent
  element edits).

## v0.1.2 — 2026-07-17
CLI usability + correctness patch (client-only; the daemon and wire protocol `acp/1` are unchanged).
- **Progressive-disclosure help.** `acp help` is now a short, task-grouped overview; `acp help <topic>`
  (comms, files, crdt, leases, admin, config, update) drills in with verbs, flags, and examples;
  `acp help all` keeps the full listing. The `crdt` topic documents the structured-CRDT (ext-5) op
  discipline — `set`/`del`/`lins`/`ldel`, array-of-segments paths, and "use list ops for arrays,
  never a whole-array `set`" (a whole-array replace silently loses concurrent element edits).
- **Clearer sync messages.** A stale-but-non-overlapping push is now labelled `STALE (run 'acp pull'
  to reconcile)`, distinct from a true `CONFLICT` (an in-place `<<<`/`===`/`>>>` marker overlap you
  must resolve). The conflict hint now tells you to `acp push --force` after resolving.
- **Lease-token cache fix.** The remembered fencing token now lives under the ACP home
  (`$ACP_HOME` or `~/.acp/leases/<ns>.json`) instead of a CWD-relative `.acp/`, so `acp lease
  renew`/`release` work from any directory (previously a different CWD than `acquire` returned
  "not holder").
- Docs: the shared-filesystem guide's conflict mechanics corrected (in-place markers vs `.remote`).

## v0.1.1 — 2026-07-13
- Capability negotiation: `/v1/healthz` now returns a `capabilities` array so clients can feature-detect instead of trial-and-erroring requests. This release advertises `batchevents`, `channels`, `crdtjson`, `quotas`, `scopedtokens`.
- Channels with wildcard topic filtering (`channels`) — publish/subscribe on named channels with `*`-style topic filters (RFC ext-1 §4, accepted).
- Batch event append (`batchevents`) — append multiple events to the log atomically in one call (RFC ext-4, accepted).
- Structured JSON CRDT (`crdtjson`) — daemon-materialized JSON documents with path-addressed get/set/delete, alongside the existing text CRDT (RFC ext-5, accepted).
- Scoped tokens and per-space quotas (`scopedtokens`, `quotas`) — mint narrower-than-admin grants and set per-space resource limits (RFC ext-7 §4/§5, accepted).
- New CLI verb: `acp update [--check] [--force] [--version vX.Y.Z]` — self-updates the installed binaries from the release channel, verifying checksums before swapping them in; follows `releases/latest.txt` by default or pins an older retained release.
- Built with Go 1.25.12, clearing the stdlib CVEs present in older Go toolchains.
- Published RFC proposals for future extensions: presence/awareness (ext-9), CRDT undo/redo (ext-11), per-document schema validation (ext-12), and outbound webhooks (ext-13). All are marked PROPOSAL — not implemented, not shipped.
- Wire protocol unchanged: still `acp/1`; extensions remain additive and capability-negotiated, so v0.1.0 clients keep working unmodified against a v0.1.1 daemon.

## v0.1.0 — 2026-06-10
First public release.
- Shared filesystem (content-addressed push/pull + 3-way merge) and CRDT live co-editing.
- Comms: directed mailbox + totally-ordered event log; fencing-token leases.
- Multi-tenant spaces; per-agent identity + roles (admin/writer/reader).
- High availability: 3/5-node Raft cluster with automatic failover.
- mTLS between cluster nodes (cluster CA) and AES-256-GCM blob encryption at rest.
- MCP bridge (acp-mcp) and a Go SDK.
- Typed client config with profiles (~/.acp/config.json).

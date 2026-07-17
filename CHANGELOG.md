# Changelog

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

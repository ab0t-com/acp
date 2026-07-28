# ACP API Reference (acp/1)

Transport: HTTPS (TLS 1.2+, HTTP/2). All endpoints except `/v1/healthz` require:

| Header | Value |
|--------|-------|
| `Authorization` | `Bearer <token>` |
| `X-ACP-Agent` | agent id *(ignored in per-agent mode — identity comes from the token)* |
| `X-ACP-Protocol` | `acp/1` *(optional; mismatch → 400)* |
| `Idempotency-Key` | optional; on `POST /v1/events` and `POST /v1/mail`, a repeat returns the original result (header `X-ACP-Idempotent-Replay: true`) |
| `X-ACP-Space` | optional; selects the isolated space (its own files/log/mailbox/leases/docs). Absent = `default`. Spaces share nothing. |
| `X-ACP-Session` | optional; random per client process — lets the daemon warn when two processes share one agent id |

**Spaces (multi-tenancy).** A daemon hosts many spaces under `-data/spaces/<space>/`,
created on first use. Every endpoint operates within the request's space. `GET /v1/spaces`
→ `{spaces:[...]}` lists open spaces. `GET /v1/stats` reports the current space and the
total space count. `POST /v1/agents/beat` returns `{collision:true, warning:...}` when the
agent id is already live under a different session.

Errors use `{"error": "...", "current": <optional state>}`. Notable statuses:
`401` bad token · `400` bad request/protocol · `409` version/lease-state conflict
(body `current` = server's current state) · `423` path is leased by another agent ·
`429` rate limited (`Retry-After`).

---

## Health & stats
| Method | Path | Body / Query | Returns |
|--------|------|-------------|---------|
| GET | `/v1/healthz` | — (no auth) | `{status, protocol}` |
| GET | `/v1/stats` | — | counters: `events, leases, docs, messages, blobs, manifest_version, agents_seen, idempotency_keys, uptime_sec, enforce_leases` |

## Coordination event log
| Method | Path | Body / Query | Returns |
|--------|------|-------------|---------|
| POST | `/v1/events` | `{action, entity?, before?, after?, context?}` | stored `Event{seq, at, actor, ...}` (actor forced to caller) |
| GET | `/v1/events?from=N` | — | `[]Event` with `seq >= N` |
| GET | `/v1/events?from=N&follow=true` | — | NDJSON live stream (backlog from N, then live) |

## Mailbox (directed messages)
| Method | Path | Body / Query | Returns |
|--------|------|-------------|---------|
| POST | `/v1/mail` | `{to, type?, subject?, body?, thread_id?, reply_to?, corr_id?, priority?, refs?}` | stored `Message` (from forced to caller) |
| GET | `/v1/mail?unread=true` | — | `[]Message` to the caller |
| POST | `/v1/mail/ack` | `{id}` | `{ok:true}` (recipient only) |
| GET | `/v1/mail/thread?id=` | — | `[]Message` in the thread |

## Leases (TTL + fencing token)
| Method | Path | Body | Returns |
|--------|------|------|---------|
| POST | `/v1/lease/acquire` | `{resource, ttl_sec}` | `Lease{resource, holder, token, expires}` · `409`+current if held |
| POST | `/v1/lease/renew` | `{resource, token, ttl_sec}` | `Lease` · `409` if not holder/expired |
| POST | `/v1/lease/release` | `{resource, token}` | `{ok:true}` · `409` if not holder |
| GET | `/v1/lease` | — | `[]Lease` (live) |

Convention: lease the resource `file:<path>` to make a commit to that path exclusive
(see commit, 423).

## Shared filesystem (content-addressed)
| Method | Path | Body / Query | Returns |
|--------|------|-------------|---------|
| POST | `/v1/blobs` | raw bytes | `{hash, size}` (idempotent) |
| GET | `/v1/blobs/<hash>` | — | raw bytes · `404` |
| POST | `/v1/blobs/has` | `{hashes:[...]}` | `{missing:[...]}` |
| GET | `/v1/manifest` | — | `Manifest{version, entries{path:{hash,size,mtime}}}` |
| POST | `/v1/commit` | `{base_version, changes:[{path,hash?,size?,deleted?}], note?}` | new `Manifest` · `409`+current on version mismatch · `423`+lease if a path is leased by another agent · `400` on illegal path (traversal rejected) |

## Collaborative documents (CRDT)
| Method | Path | Body / Query | Returns |
|--------|------|-------------|---------|
| POST | `/v1/crdt/ops` | `{doc, ops:[Op]}` | `{doc, total}`; also emits a `crdt.op` event |
| GET | `/v1/crdt/ops?doc=&from=N` | — | `{doc, ops:[Op], total}` (ops with index ≥ N) |
| GET | `/v1/crdt/doc?doc=` | — | `{doc, text, total}` (daemon-materialized) |
| GET | `/v1/crdt/list` | — | `[]{name, ops, size}` |

`Op = {t:"ins"|"del", id:{c,r}, o?:{c,r}, v?:"<rune>"}`. Ops commute; clients track a
per-doc cursor (the `total` they've consumed).

## Awareness — ephemeral presence (capability `awareness`; `awareness-ws` for WebSocket)
Live, self-expiring presence keyed by `(actor, session)`; **never durable** (not in the log,
CRDT, or snapshots). See `docs/AWARENESS.md`.
| Method | Path | Body / Query | Returns |
|--------|------|-------------|---------|
| POST | `/v1/awareness` | `{state:{…}, ttl_sec?}` | your entry (set/refresh; TTL default 30 s) |
| POST | `/v1/awareness` | `{ttl_sec:0}` (no state) | `{ok:true}` — a **leave** (removes your entry) |
| GET | `/v1/awareness` | — | snapshot: `[]{actor, session, state, expires}` (live entries) |
| GET | `/v1/awareness?follow=true` | — | NDJSON: snapshot then `join`/`update`/`leave` deltas |
| GET | `/v1/awareness?transport=ws` | Upgrade: websocket | bidirectional; server coalescing tick (15 Hz) |

`session` is the `X-ACP-Session` header (per-tab/connection). In a cluster the follow stream and
WebSocket are served by the leader (followers redirect).

## Admin (role: admin)
| Method | Path | Query | Returns |
|--------|------|-------|---------|
| POST | `/v1/admin/gc` | `grace_sec` (default 600) | `{removed, bytes}` — deletes unreferenced blobs older than grace |
| POST | `/v1/admin/compact` | `doc` (optional) | `{dropped}` — compacts a CRDT doc's op-log (or all docs), GC-ing tombstones |

## Tenancy — scoped tokens, quotas, sub-scopes (capabilities `tenancy`, `tenancy.subscope`)
Least-privilege *within* a space; a separate **space** remains the only hard isolation boundary.
Scoped grants live in `agents.json` (versioned, fail-closed) with optional `path_prefix`, an
allowlist of `spaces`, a `sub_scope` pin, and a `deny` list (e.g. `deny:["awareness"]`).
| Method | Path | Body / Query | Returns |
|--------|------|-------------|---------|
| GET | `/v1/whoami` | — | `{name, role, scope}` — the caller's own grant (no token echoed) |
| GET | `/v1/subscopes` | — | `[]{name, …counters}` — sub-scope labels seen in the space |
| PUT | `/v1/admin/quota` | `{space?, sub_scope?, max_events?, max_blob_bytes?, max_leases?, max_docs?}` | the stored quota |
| GET | `/v1/admin/quota` | `space?` | the current quota + usage |

A `sub_scope` label may ride `POST /v1/events`, `POST /v1/mail`, `POST /v1/lease/acquire`, and
`POST /v1/commit`. A write over a per-space or per-sub-scope quota returns **507** with
`{used, limit}` (retained-state quotas: `max_events`, `max_blob_bytes`, `max_docs`); a transient
cap (`max_leases`) returns **429** + `Retry-After`. `GET /v1/stats` reports `{used, limit}` per
configured quota. `tenancy` is advertised only when scoped tokens **and** quotas are enforced.

## Roles (per-agent mode)
`agents.json` maps token → `{name, role}`. Roles: **admin** (everything), **writer**
(read+write, no `/v1/admin/*`), **reader** (GET + `/v1/agents/beat` only). A
disallowed call returns **403**. In shared-token mode every caller is effectively admin.

## CRDT epochs
`POST /v1/crdt/ops` accepts an optional `epoch`; if it doesn't match the doc's current
epoch (the doc was compacted), the call returns **409** with the new epoch — the client
must re-pull from 0 and rebase. `GET /v1/crdt/ops` returns the current `epoch`.

## Cluster / HA (Raft)
| Method | Path | Role | Returns |
|--------|------|------|---------|
| GET | `/v1/cluster/status` | any | `{mode, state, leader, is_leader, members}` (or `{mode:"single-node"}`) |
| POST | `/v1/admin/cluster/join` | admin | `{node_id, raft_addr}` → adds a voter (forwarded to leader) |

In cluster mode every **write** endpoint forwards to the leader automatically (a
follower proxies the request); **reads** are served locally (stale-tolerant). CRDT
`POST /v1/crdt/ops` carries/honors `epoch` (409 on stale). See
`../../14_production_spec_2026-06-10.md` for the model.

## MCP bridge
`acp-mcp` is a separate binary: a Model Context Protocol server (JSON-RPC 2.0 over
stdio) exposing ACP as tools (`acp_send`, `acp_inbox`, `acp_log_event`,
`acp_recent_events`, `acp_lease`, `acp_doc_read`, `acp_doc_write`, `acp_who`,
`acp_stats`, `acp_ack`, `acp_whoami`). Config via the same env vars as the CLI.

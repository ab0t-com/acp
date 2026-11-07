# ACP API Reference (acp/1)

**Machine-readable description:** every server serves its own OpenAPI 3.1 document at `GET /openapi.json`
(every endpoint, its parameters, request and response bodies, statuses and roles; `info.version` is the
server's release). Like the start page it needs no token unless the server runs `-root-page=false`. It
describes the implementation; the RFCs under `rfc/` stay normative.

Transport: HTTPS (TLS 1.2+, HTTP/2). Every endpoint needs a token EXCEPT these token-free ones:
`GET /v1/healthz`, `GET /.well-known/acp` (discovery), `GET /` and `GET /v1/` (the start page) and
`GET /openapi.json` (the last three only while the server runs with `-root-page`, the default; with
`-root-page=false` they need a token and `/` answers `404`), `POST /v1/invites/redeem` (the invite
secret is the credential) and `GET /v1/cap/blob/<hash>` (a signed link). All the rest require:

| Header | Value |
|--------|-------|
| `Authorization` | `Bearer <token>` |
| `X-ACP-Agent` | agent id *(ignored in per-agent mode — identity comes from the token)*. **Max 256 bytes** — longer is `400`. The id is retained as a map key and written into durable records (`Event.actor`, `Message.from`), so it is bounded like any other client-supplied value. |
| `X-ACP-Protocol` | `acp/1` *(optional; mismatch → 400)* |
| `Idempotency-Key` | optional; on `POST /v1/events` (single or batch) and `POST /v1/mail`, a repeat of the same request returns the original result (header `X-ACP-Idempotent-Replay: true`); the same key with a different request answers `422 idempotency_key_reused` and writes nothing. **Failover- and restart-safe**: keys are replicated state, exact within the last `-idem-window` keys per space (see Idempotency below) |
| `X-ACP-Space` | optional; selects the isolated space (its own files/log/mailbox/leases/docs). Absent = `default`. Spaces share nothing. |
| `X-ACP-Session` | optional; random per client process — lets the daemon warn when two processes share one agent id |

**Spaces (multi-tenancy).** A daemon hosts many spaces under `-data/spaces/<space>/`.
A space is created by the first write that stores something in it (an event, a message, a
lease, a blob, a commit, a document op) or by an admin setting its mode or quota or inviting
into it. Any other request on a space that does not exist answers `404` with code
`space_not_found` and leaves nothing behind (`GET /v1/whoami` still answers, without
`bulk`; a space outside your grant stays `403`; a bad name stays `400`). In a cluster a
follower asks the leader first. Every endpoint operates within the request's space. `GET /v1/spaces`
→ `{spaces:[...]}` lists every space on this server's disk (plus any loaded in memory), sorted,
filtered to the spaces your grant may use. `GET /v1/stats` reports the current space and the
total space count. `POST /v1/agents/beat` returns `{collision:true, warning:...}` when the
agent id is already live under a different session.

Errors use `{"error": "...", "code": "...", "current": <optional state>}`. Every refusal carries a
stable `code` (v0.2.6; the full list is `components.schemas.ErrorCode` in `/openapi.json`): a specific one
where it is defined (`space_not_found`, `blob_not_uploaded`, `folder_not_empty`, ...), otherwise the class of
its status (`bad_request`, `forbidden`, `not_found`, `method_not_allowed`, `conflict`, `too_large`,
`rate_limited`, `internal`, `unavailable`, ...). A route this server does not have answers `404` with
`unknown_route` (also for a sub-path a route does not take, such as `/v1/blobs/<hash>/<other>`), never `not_found`, so "no such route" (an older server, a typo) and "no such object" read
differently. The `error` text says what to do next. A client accepts a code it does not know (a newer server may add one). Two refusals have no JSON body: a WebSocket upgrade the handshake refuses, and `416` for a byte range outside a blob. A share link's refusals carry `link_*` codes and no added step (its reader has no login); its `405`, `429` and `500` carry the status class. A v0.2.5 server leaves `code` out of most refusals: read a
missing code as the status alone, and a `404` with no code as a route the server may not have. Notable statuses:
`401` bad token · `400` bad request/protocol · `408` (cluster) a body sent to a follower stopped arriving for 30 s: send it again · `403` role forbids the call OR a write
resource name is outside the grant's `path_prefix` scope (see Scoped tokens) ·
`409` version/lease-state conflict
(body `current` = server's current state) · `423` path is leased by another agent ·
`429` rate limited OR transient quota (`Retry-After`) · `507` storage quota exceeded
(**no** `Retry-After` — free space first, see Per-space quotas) · `503` the server did not or could not act
(code `space_unavailable`: it cannot open this space and an operator has to act; in a cluster: no leader yet, a
just-elected leader, or a write that did not commit in time). From v0.2.6 a `503` that was not acted on carries
`retry_safe: true`: send it again after `Retry-After`. Without that mark the outcome is unknown: retry only with the
same `Idempotency-Key` where the operation takes one.

From v0.2.6 six routes that change state accept `POST` only: `/v1/commit`, `/v1/lease/acquire`,
`/v1/lease/renew`, `/v1/lease/release`, `/v1/mail/ack` and `/v1/agents/beat`. Any other method answers `405` with
`Allow: POST`.

---

## Health & stats

**Group commit (flag `-group-commit`, default on):** concurrent event-log
appends coalesce onto ONE fsync per window — durable-before-return is unchanged (every
ack still follows the fsync), a lonely appender still pays exactly one fsync, and burst
throughput rises ~15–60×. `/v1/stats` reports the posture as
`storage {engine, group_commit:true|false, format_version}` (additive; no capability
string — a client cannot otherwise tell). `-group-commit=false` is the instant rollback
(one fsync per append).

**Storage modes.** The `storage` block is how a daemon reports which storage it runs.
`engine:"file"` is **file storage** (the `coordd` binary): JSONL logs + atomic-rename snapshots,
zero dependencies, state you can `cat` and `grep`, instant start. It is the default.
`engine:"db"` is **database storage** (the `coordd-server` binary, an embedded database): for
large, long-lived, many-tenant deployments, with memory bounded at any retained history and a fast
cold start; it also reports engine-neutral `metrics`. See [STORAGE-MODES.md](STORAGE-MODES.md).
Same wire (`acp/1`), same capability strings, same request and response shapes: **any wire-visible feature works in every mode — modes differ in limits and
operational characteristics, never in protocol vocabulary.** `group_commit` is a property of
both. Treat `storage.engine` as observability only; a client MUST NOT branch protocol behaviour
on it (feature-probe `capabilities` instead).

| Method | Path | Body / Query | Returns |
|--------|------|-------------|---------|
| GET | `/v1/healthz` | — (no auth) | `{status, protocol, version, capabilities, limits}` (`version` = the server's release, for people; clients decide from `capabilities`, never from `version`; `limits` is always sent today, treat it as optional) |
| GET | `/v1/stats` | — | counters: `events, leases, docs, json_docs, resident_docs, resident_json_docs, messages, blobs, manifest_version, agents_seen, idempotency_keys, uptime_sec, enforce_leases` (`resident_*` = documents folded in memory right now — see OPERATIONS §6) + `capabilities, limits?` + `quota?` (per-bound `{used, limit?}` once a quota is set — see Per-space quotas) + `history` (retention block — see Manifest history) + `event_base_seq`, `event_log` (v0.2.6: `empty` never had an event, `compacted` had events and none are kept, `retained`) + `log_id?` (v0.2.6, see Log id) + `open_streams?` (event and awareness streams open on this node; left out for a login limited to certain workspaces) |

**Capabilities.** `capabilities` is a sorted `[]string` of the optional protocol
extensions this daemon serves (empty on daemons that serve none). Clients feature-probe
it (once, via `/v1/healthz`) instead of trial-and-erroring requests. Currently defined:
`"acpuri"` — the `acp://` address scheme's bare-host bootstrap: the daemon serves the
read-only discovery document `GET /.well-known/acp` (ext-32 §3.8; always advertised —
see Discovery below);
`"awareness"` — the ephemeral per-actor presence tier, `/v1/awareness` (ext-9; always
advertised — see Presence & awareness);
`"batchevents"` — batch event append (absent when the daemon runs `-max-batch 0`);
`"channels"` — the event `channel` label + server-side filtered subscription +
`GET /v1/channels` (ext-1 §4; always advertised);
`"bloburl"` — blob capability URLs, mint + tokenless fetch (ext-3; only when the
daemon runs with `-cap-key-file`);
`"crdtjson"` — the structured (JSON-graph) CRDT document type + the
`/v1/crdt/json/*` endpoints (ext-5; always advertised — a daemon without it 404s
those paths, so clients MUST probe before use);
`"quotas"` — per-space resource ceilings + the `/v1/admin/quota` API + the stats
`quota` block (ext-7 §5; always advertised — enforcement activates per space the
moment an admin sets a limit; until then every space is unlimited);
`"tenancy"` + `"tenancy.subscope"` — the ext-7 umbrella: §4 scoped tokens
(path/space/sub_scope), §5 quotas (incl. `max_docs`) and §6 sub-scopes are all
enforced (advertised only since all three ship — a client seeing them may rely on
server-side enforcement instead of broker-side, ext-7 §3.2);
`"scopedtokens"` — path-prefix-scoped grants + `GET /v1/whoami` (ext-7 §4; always
advertised — enforcement activates per grant the moment a scoped token is minted; a
minter MUST see this capability before relying on a scope against a foreign daemon).
An advertised capability carries its sizing hints in `limits` (ext-4 §5) so clients can
size requests without probing: `"limits": {"awareness_max_state_bytes": 16384,
"awareness_default_ttl_sec": 30, "awareness_max_ttl_sec": 300, "max_batch_events": 256}`
(`max_batch_events` omitted when batch append is disabled; the awareness hints are
always present).

### Discovery (`GET /.well-known/acp`, ext-32 §3.8, capability `"acpuri"`)

A STATIC, READ-ONLY, UNAUTHENTICATED JSON document (auth-exempt exactly like
`/v1/healthz`; no token, no `X-ACP-Space`) that lets a resolver bootstrap from a bare
host — `acp uri discover acp://host[:port]/`, the SDKs' `discoverACP` / `Discover`. It is
NOT a `/v1` operation: it adds no protocol operation and no replicated state. `GET` and
`HEAD` only (anything else is `405`); at most 64 KiB; never redirects.

| Key | Type | Value |
|---|---|---|
| `protocol` | string | `"acp/1"` — exactly as `/v1/healthz` reports it |
| `endpoint` | string | this daemon's https ORIGIN, `https://<host>:<port>` (no `/v1`, no path); the acp/1 wire lives at `<endpoint>/v1/...` |
| `capabilities` | []string | the SAME list `/v1/healthz` advertises |
| `spaces` | []string, optional | the space names, sorted — **omitted by default** (tenant names are information); published only with `-discovery-spaces`, refreshed at most every 10 seconds, and dropped (not truncated) if it would exceed the size bound. Absent = undisclosed: authenticate, then `GET /v1/spaces` |
| `default_space` | string, optional | a first-contact hint, only with `-discovery-default-space <name>`; a client offers it and never auto-connects on it |
| `cert_fingerprint` | string, optional | `sha256:<64 hex>` of the DER leaf certificate the listener presents — an ADVISORY pin hint, never a trust root; on by default, `-discovery-cert-fingerprint=false` omits it |

**Host binding.** `endpoint`'s host is the host the client dialed — accepted only when
it is one of the daemon's configured identities (the listener certificate's SANs / the
`-hosts` flag; case-folded, one trailing dot ignored); the port is the listener's actual
bound port. Any other `Host` (a forwarding header is never consulted) is answered `421
Misdirected Request` rather than reflected. `-discovery-endpoint https://name[:port]`
publishes a fixed operator-chosen origin instead (a public DNS name or a gateway); a
resolver refuses a document whose endpoint host differs from the host it dialed, so
publish the name clients actually use. Behind a TLS-terminating gateway whose
certificate differs from the daemon's, also pass `-discovery-cert-fingerprint=false` —
a resolver refuses a document whose pin disagrees with the certificate it saw.

## Coordination event log
| Method | Path | Body / Query | Returns |
|--------|------|-------------|---------|
| POST | `/v1/events` | `{action, entity?, channel?, before?, after?, context?}` | stored `Event{seq, at, actor, ...}` (actor forced to caller; `action` required — 400 `missing action`; bad `channel` — 400 `bad channel: ...`) · `507` over `max_events` |
| POST | `/v1/events` | `[Event, ...]` (JSON array) | `[]Event` — batch append, see below (capability `batchevents`) · `507` if the WHOLE batch would cross `max_events` (nothing appended) |
| GET | `/v1/events?from=N[&limit=K]` | — | `[]Event` with `seq >= N`, at most K (oldest first) when `limit` is given (v0.2.6) |
| GET | `/v1/events?from=N&follow=true` | — | NDJSON live stream (backlog from N, then live). It ends when the login is revoked or expires; `429 too_many_streams` (with `Retry-After`) past `-max-streams-per-login` |
| GET | `/v1/events?...&channel=P`, `channels=P1,P2`, `actions=P1,P2` | filter patterns (see below) | only matching events — works on both the snapshot read and `follow=true` (capability `channels`) |
| GET | `/v1/channels` | — | `[{name, declared, last_seq, last_at}]` — channels seen in retained events, sorted by name; unlabeled events are not a channel (capability `channels`) |

**Log id (v0.2.6, acp-1 §8.6).** `GET /v1/events` (and its `follow=true` stream, before the first event) answers
with `X-ACP-Log-Id: <space part>.<epoch>` and `X-ACP-Log-Head: <head when the read began>`; `GET /v1/stats` has
`log_id`. The same id with a lower head is a node that is behind: keep your position and wait. A new id means the
history may have been restored or rebuilt (seqs you hold may now name other events): re-read and resume at the new
head; a program that hands events on signals a resync with its old position and the new head. The id also changes at
every server start and every new cluster leader, so a new id costs a re-read, never a wrong history. Absent while the
server cannot vouch for its epoch (a single node before its start's epoch applied; a cluster node whose epoch is not
from its current term's leader, or that has heard from no leader since it started, as after a restore), or from an
older server: read absent as unknown, which is the "behind" rule (wait, serve nothing older). When a node's epoch
changes, every open `follow=true` stream on it ends, so a follower reconnects and reads the new id (a stream opened
while the id was absent learns it this way). On a new id, a follower that kept the event at its own position can ask
for it (`GET /v1/events?from=<seq>&limit=1`): the same event means the same log (a restart or a new leader) and it
continues with nothing skipped; a different or missing one means a restore.

**Channels + filtered subscription (`channels`, ext-1 §4).** An event MAY carry a
`channel`: a short opaque topic label (`[A-Za-z0-9._:-]{1,64}`, bare `_` reserved),
validated on publish — 400 otherwise. A channel is a *view* over the one totally-ordered
log, never a boundary (the space is) and never access control. Filter grammar: each
pattern is a **literal** (exact bytes) or a **trailing-`.*` prefix** — `deploy.*` matches
`deploy.web.failed` but NOT bare `deploy` (list both: `deploy,deploy.*`); no mid-string
`*`, no `?`, no regex. Within one param, patterns OR-combine; when `channels` and
`actions` are both present an event must match BOTH (the channel routes, the action
selects). `channel=P` folds into the `channels` list. The reserved pattern `_` in a
channel filter matches ONLY unlabeled events (and is illegal in `actions=`). At most
**32** patterns per request (channels+actions combined); an illegal pattern or over-cap
list is a 400 naming the problem. **Seq gaps are expected under a filter** — delivered
events are a subsequence of the log, so consecutive delivered events need not have
consecutive `seq`; a filtered subscriber must not treat a gap as loss. Resume exactly as
unfiltered: reconnect with `from=<last-received-seq+1>` and the SAME filter — the
gap-free/duplicate-free cutover guarantee holds with respect to the evaluated stream.
No filter parameters = all events, byte-identical to before this extension.

- Clients: `client.EventsFiltered(from, &client.EventFilter{Channels, Actions})` /
  `client.FollowFiltered(...)` — both send the params AND re-filter locally with the
  daemon's own matcher (`wire.MatchPattern`), so an old daemon without the capability
  degrades to extra bandwidth, never wrong results (a one-time warning is logged);
  `client.Channels()` lists the registry.
- CLI: `acp log --channel build ...` · `acp watch --channels build,deploy.* --actions deploy.failed` ·
  `acp channels`.

**Batch append (`batchevents`).** A JSON *array* body appends all events as ONE atomic
unit: contiguous ascending `seq` (no other agent's event lands inside the batch),
all-or-nothing (a rejected/failed batch appends nothing), one shared `at` stamp for the
whole batch (it is one logical fact-set), and `actor` forced per element from the
authenticated identity. ONE `Idempotency-Key` covers the whole batch — a replay returns
the identical `[]Event` response; the key is bound to (space, actor), so a colliding key
from another agent or space never replays someone else's batch (ext-4 §8.3). The
single-object path above is unchanged.

- Cap: `coordd -max-batch N` (default 256, advertised as `limits.max_batch_events`).
  `-max-batch 0` disables array bodies and drops `batchevents` from `capabilities`.
- Validation: every element needs `action`; one malformed element rejects the WHOLE
  batch before any seq is assigned — no valid prefix is retained (ext-4 §4.4).
- `400`: empty array (`empty batch`), over cap (`batch too large: N events (max M)`),
  a malformed element (`batch element N missing action`), or batch append disabled.
- `429` + `Retry-After`: a batch costs **N** rate-limit tokens (the request's 1 plus
  N−1), so batching cannot bypass the per-agent `-rate` limit; nothing is appended.
- Clients: `client.LogBatch(events)` (probes the capability once, fails explicitly if
  absent) · `acp log --batch <file.json|->`.

**Idempotency (failover- and restart-safe).** The `Idempotency-Key` check-and-store
runs *inside the replicated write path* (the FSM), not in the HTTP handler: the key rides
the Raft log with the write, every replica records key→original-result, and the state is
carried by cluster snapshots and persisted per space on disk. Consequences:

- **Failover-safe:** a retry against a newly elected leader — the exact moment clients
  retry — replays the ORIGINAL result (same `seq`/message id); no duplicate is appended.
  This holds even for a node that joined from a snapshot and never saw the original write.
- **Restart-safe:** single-node daemons persist the window (`<space>/idem/idem.json`,
  flushed periodically and on shutdown); a retry after restart replays.
- **Scope:** every key is bound to `(space, actor)` with length-prefixed framing, so a
  key from another agent or space — even one crafted with embedded separators — can
  never replay someone else's cached result.
- **Window:** the guarantee is exact within the last `-idem-window` keys per space
  (default 10000, insertion-order eviction) and best-effort beyond it: a key evicted
  past the window re-applies as a fresh write. Size the window above your worst-case
  retry horizon. In cluster mode `-idem-window` MUST be identical on every node.
- A replayed duplicate still consumes rate-limit tokens (it is a full request and, in
  cluster mode, a Raft entry — committed order is the only authority after an election).

## Presence & awareness

| Method | Path | Body / Query | Returns |
|--------|------|-------------|---------|
| POST | `/v1/agents/beat` | `{harness?, host?, status?}` (each ≤256 bytes; body ≤8 KiB) | `{id, last_seen, space, collision?, warning?}` — the coarse ROSTER heartbeat |
| GET | `/v1/agents` | — | `[]Agent{id, harness, host, status, last_seen}` |
| GET | `/v1/agents/member?name=` | — | `{name, member?}`: a live login that reaches this space, or an open invite to it. `member` is absent on a shared-token server. |
| POST | `/v1/awareness` | `{state: <object>\|null, ttl_sec?, session?}` | stored `AwarenessEntry{actor, session, state, ttl_sec, updated, expires}` — `state:null` = explicit leave (200, identity-only entry) |
| GET | `/v1/awareness` | — | `{entries: [AwarenessEntry, ...]}` (live only; the re-priming read) |
| GET | `/v1/awareness?follow=true` | — | NDJSON stream of `{type: join\|update\|leave, entry}`; opens with a synthetic `join` per live entry · `429` past the per-actor/per-space follower bound |

**Awareness (ext-9, capability `"awareness"`) is the EPHEMERAL tier**: per-(actor,
session) last-writer-wins display state (cursor, selection, live status) with TTL
expiry. It is volatile by design — never written to the event log, never assigned a
seq, never in a CRDT, snapshot, or replica (ext-9 §3.1); a daemon restart empties it
and clients simply re-send (recommended re-POST cadence: TTL/3). Do NOT build
correctness-bearing logic on it — leases and the event log carry facts.

- `actor` is always token-derived; a body `actor` is ignored. `session` (≤64 bytes,
  default `""`) lets one actor hold several entries (multi-tab); ≤8 live sessions per
  actor, stalest evicted with a `leave` delta.
- `state` must be a JSON object ≤16 KiB (400 beyond). `ttl_sec`: default 30, max 300;
  explicit non-positive → 400 (except the `{} + ttl_sec 0` leave form). The per-space
  entry total is capped too (default 4096; a new join beyond it is a transient
  `429`+`Retry-After` — refreshes of live entries always pass). Bounds are
  flag-tunable (`-awareness-max-state-bytes`, `-awareness-default-ttl`,
  `-awareness-max-ttl`, `-awareness-max-sessions`, `-awareness-max-entries`) and the
  RFC's three hints are advertised in `limits`. In cluster mode set them identically
  on every node.
- Deltas carry NO seq and NO replay cursor: delivery is best-effort keep-latest
  (superseded intermediates may be dropped). On reconnect, just re-follow — the
  synthetic joins re-prime you.
- Coalescing tick (ext-9 §11.2, `-awareness-tick-hz`, default 0 = per-write): when
  enabled, at most ONE join/update delta per (actor, session) is emitted per tick,
  newest-wins; leaves are never withheld. A property of the store's emission, so it
  applies identically to every awareness transport; the rate is advertised as
  `limits.awareness_tick_hz` so clients pace input (the 30–60 ms client-side cursor
  sampling advice still applies).
- Roles: reads need `reader`; writes are open to `reader` too by default (a read-only
  observer legitimately has a cursor) — `-awareness-write-role writer` tightens (403).
- Cluster: leader-served. POST and the snapshot GET are proxied to the leader; the
  follow stream answers `307` to the leader's URL — re-dial it directly.
- The distinction from beat: `/v1/agents/beat` is "which agents are alive" (roster,
  hour-scale TTL); `/v1/awareness` is "what is each actor doing right now"
  (editing-surface granularity, seconds-scale TTL). A beat never creates or refreshes
  an awareness entry.

SDK: `SetAwareness(state, ttlSec, session)` · `ClearAwareness(session)` ·
`Awareness()` · `FollowAwareness(fn)` — each probes the `"awareness"` capability and
fails explicitly against a daemon without it. CLI: `acp awareness set|get|watch|clear`.
Guide: [AWARENESS.md](AWARENESS.md).

### WebSocket binding (ext-9 §11.3, capability `"awareness-ws"`, flag `-awareness-ws`)

| Method | Path | Returns |
|--------|------|---------|
| GET | `/v1/awareness?transport=ws` | HTTP Upgrade → one BIDIRECTIONAL socket · `404` when the binding is off · `429` past the connection bound (pre-upgrade) · in a cluster a follower answers `307` to the leader (re-dial it) |

One socket both PUBLISHES the caller's own awareness and SUBSCRIBES to everyone's —
the shape live editors need at cursor frequency. Text frames, one JSON object each,
`"op"` as the discriminator; the Appendix A shapes ride inside the frames verbatim:

```jsonc
// client -> server
{ "op": "set", "state": {…}|null, "ttl_sec": 30, "session": "tab-1" }  // == the POST body (same bounds/errors)
{ "op": "ping" }                                                       // keepalive: refreshes THIS socket's entries + gets a pong
// server -> client
{ "op": "delta", "type": "join"|"update"|"leave", "entry": AwarenessEntry }
{ "op": "pong" }
```

- The Upgrade is authenticated exactly like POST (bearer + space + role); connection
  count is bounded per space and per actor (refused `429` before the upgrade).
- Every inbound frame counts against the sender's rate limit (the Upgrade charged one;
  set frames are writes, and ping/unknown frames are charged too so a socket cannot be
  a free flood). Under `-awareness-write-role writer` a reader's `set` frames are
  refused (close 1008) while its read side keeps working.
- A responsive socket's `ping` keeps its own entries alive without re-sending state
  (the documented refresh: expiry extends to now+TTL per ping); a SILENT socket still
  expires past TTL. A clean close (or `state:null`) emits `leave` for that
  connection's entries; an abnormal drop leaves them to the TTL (reconnect-friendly).
- Your OWN deltas are echoed back (multi-tab needs same-actor deltas — filter your own
  `(actor, session)` client-side). Deltas obey the coalescing tick when the daemon
  ticks; with `-awareness-ws` the tick auto-defaults to 15 Hz (override with
  `-awareness-tick-hz`).

## Mailbox (directed messages)
| Method | Path | Body / Query | Returns |
|--------|------|-------------|---------|
| POST | `/v1/mail` | `{to, type?, subject?, body?, thread_id?, reply_to?, corr_id?, priority?, refs?}` (`to` is one agent) | stored `Message` (from forced to caller) · `400` when `to` holds a comma, unless a login has exactly that name: send one message per person |
| GET | `/v1/mail?unread=true` | — | `[]Message` to the caller |
| POST | `/v1/mail/ack` | `{id}` | `{ok:true}` (recipient only) · `404` if no such message is addressed to the caller |
| GET | `/v1/mail/thread?id=` | — | `[]Message` in the thread, oldest first, starting with its opening message (from v0.2.5); only messages the caller sent or received |

Each send also appends a `chat.sent` event to the space's log (actor = sender, entity = recipient, context
`{msg, type}`). From v0.2.6 the event no longer carries the subject: a direct message's subject and body are only for
its sender and recipient. Events written before the upgrade keep their subjects until retention removes them.

## Leases (TTL + fencing token)
| Method | Path | Body | Returns |
|--------|------|------|---------|
| POST | `/v1/lease/acquire` | `{resource, ttl_sec}` (`ttl_sec` default 300, capped by `-lease-max-ttl`) | `Lease{resource, holder, token, expires}` · `409`+current if held · `429`+`Retry-After` over `max_leases` (a renew by the holder always passes) · `400` if `resource` exceeds 1024 bytes (all three lease verbs enforce it) |
| POST | `/v1/lease/renew` | `{resource, token, ttl_sec}` | `Lease` · `409` if not holder/expired |
| POST | `/v1/lease/release` | `{resource, token}` | `{ok:true}` · `409` if not holder |
| GET | `/v1/lease` | — | `[]Lease` (live). The fencing `token` is REDACTED here (shown as 0); acquire/renew return it to the holder (see the note below) |

Convention: lease the resource `file:<path>` to make a commit to that path exclusive
(see commit, 423).

The lease `token` is the holder's fencing token for renew/release/fencing checks, but it is NOT a
strict secret: `GET /v1/lease` redacts it, and so does a refusal (a `409` from acquire/renew, a `423` on a
commit: `current` shows the holder with `token` 0), yet the `lease.acquired` audit event carries it. Renew and
release also check the caller's identity, so holding the token alone is not enough, but do not rely on it
being private.

## Shared filesystem (content-addressed)
| Method | Path | Body / Query | Returns |
|--------|------|-------------|---------|
| POST | `/v1/blobs` | raw bytes | `{hash, size}` (idempotent) · `507` over `max_blob_bytes` (a dedup re-put of stored content always passes) |
| GET | `/v1/blobs/<hash>` | — | raw bytes · `404` |
| POST | `/v1/blobs/has` | `{hashes:[...]}` | `{missing:[...]}` |
| GET | `/v1/manifest` | — | `Manifest{version, entries{path:{hash,size,mtime}}}` |
| POST | `/v1/commit` | `{base_version, changes:[{path,hash?,size?,deleted?}], note?, restore_of?}` | new `Manifest` · `409`+current on version mismatch · `423`+lease if a path is leased by another agent · `400` on illegal path (traversal rejected), a `note` over **1024 bytes**, **or a malformed `hash`** (must be 64 lowercase hex — the shape of `hex(sha256)`; rejected before the commit is proposed, and re-checked in the store) · **`400 restore_mismatch`** / **`404 version_pruned`** when `restore_of` is set (below) · **`428`** a large change (many deletes or overwrites) needs confirming: resend with `bulk_confirm:{deletes,overwrites}` echoing the `bulk` counts in the refusal (`bulk_confirm_required`; a wrong echo is `bulk_confirm_mismatch`, a half-filled one `400 bulk_confirm_invalid`) · **`403 bulk_denied`** this token may not make large changes (ask an admin; `GET /v1/whoami` shows `bulk`) · **`409 wrong_fs_mode`** the space is in realtime mode — use `/v1/fs/*` (see Realtime filesystem) |
| POST | `/v1/blobs/<hash>/url` | `{ttl_sec?}` | `{url, expires, hash, ttl_sec}` — mint a blob capability URL (below) · `404` unknown blob or no `-cap-key-file` · `400` malformed hash / blob not used by a current file (the manifest, or in a realtime space the live tree) |
| GET | `/v1/cap/blob/<hash>?sp=&e=&k=&s=` | — (**no auth**) | raw bytes (Range honored) · `400` malformed/repeated params · `410` expired · `403` bad signature or unknown key id · `404` valid signature, absent blob · `429` per-IP limit |

### Manifest history, retention & restore (capability `"history"`)

The authority keeps a **version record** for every manifest version it produces — its entries plus commit
metadata — for a per-space retention window, and **widens blob garbage collection** so every blob a retained
record references stays on disk. Deleted bytes are "trash, not erased" for the window, and **undo is a
commit**: nothing is ever rewritten in place. Without the capability the endpoints below are `404` and GC is
unchanged. `GET /v1/manifest` (no `version`) and `POST /v1/commit` (no `restore_of`) are byte-for-byte
unchanged.

| Method | Path | Body | Result |
|---|---|---|---|
| GET | `/v1/manifest?version=N` | — | the version record `{version, entries, at?, actor?, note?, restore_of?, checkpoints?}` · `404 version_unknown` (N > current) · `404 version_pruned` (N existed, evicted). Scope-filtered exactly like the live manifest. |
| GET | `/v1/history?limit=&before=` | — | `{versions:[{version, at?, actor?, note?, restore_of?, checkpoints?, entries}], oldest, next_before?, policy}` — newest first; `limit` default 100, max 1000 |
| GET | `/v1/history?path=P&limit=&before=` | — | `{path, versions:[{version, at?, actor?, note?, hash?, size?, mtime?, deleted, restore_of?}], oldest, next_before?}` — one element per retained version where `P` changed; an out-of-scope or never-existed path yields the same empty shape |
| POST | `/v1/checkpoint` | `{name, version?, note?}` | `Checkpoint{name, version, created, actor, note?, entries}` (**writer**; default version = current; pins a retained version) · `400 checkpoint_name` (empty/too long/reserved `auto/` prefix) · `404 version_unknown`/`version_pruned` · `409 checkpoint_exists` · `507 checkpoint_quota` |
| GET | `/v1/checkpoint[?name=]` | — | `[Checkpoint]` or one `Checkpoint` (**reader**) · `404 checkpoint_unknown` |
| DELETE | `/v1/checkpoint?name=` | — | `{ok:true}` (**admin** — a checkpoint protects other agents' work, so removing one is admin-only) · `404 checkpoint_unknown` |
| POST | `/v1/admin/history/prune` | — | `{evicted, oldest_version, history_bytes_over_cap}` (**admin**) — proposes the replicated prune now; never deletes blob bytes (a later `admin gc` does) |

**Restore** is an ordinary forward commit: read `GET /v1/manifest?version=N`, then `POST /v1/commit` with
`base_version` = current and each chosen path set to its entry at N (`{path,hash,size}`, or `{path,deleted:true}`
where N lacks it). No blob is uploaded — the pin guarantees every historical hash is present. Pass the OPTIONAL
`restore_of: N` to have the authority verify, per named path, that the result equals version N (else
`400 restore_mismatch`, no change; `404 version_pruned` if N was evicted). The restore is itself a new version,
so it is reversible. Every existing guard (CAS, leases, scopes, bulk confirmation, audit, quota) applies.

**Retention policy** rides `PUT /v1/admin/quota` (four OPTIONAL per-space fields; absent = the default shown):
`history_ttl_sec` (default `2592000` = 30 d), `history_min_versions` (default `100`), `max_history_bytes`
(default unbounded), `max_checkpoints` (default `256`). A record is retained while it is within the time window
OR among the newest `history_min_versions` OR pinned. `history_min_versions` is a **hard floor**: the byte cap
evicts the oldest records above the floor and never crosses it — if the floor alone exceeds the cap the floor
wins and `/v1/stats` reports `history.history_bytes_over_cap: true`. `history_ttl_sec: 0` with
`history_min_versions: 0` disables history for that space (retaining only the current version and pins) and is
reported loudly as `history.history_disabled: true`. History-retained blobs count toward `max_blob_bytes`, so a
space can hit `507` from deleted-but-retained bytes; `/v1/stats.history` shows the `blob_bytes` (history-only)
vs `live_blob_bytes` split so you can tune the window, prune, GC, or raise the quota. The same window bounds the
document trash and retained versions: when the server drops copies past it, the event log records one
`history.recovery_purged` event with the cutoff (`before`) and `ttl_sec`.

`GET /v1/stats` gains a `history` block: `{versions, oldest_version, oldest_at?, record_bytes, blob_bytes,
live_blob_bytes, footprint_bytes, checkpoints, history_bytes_over_cap, history_disabled, policy}`.

### Blob capability URLs (ext-3, capability `"bloburl"`)
A capability URL is a short-lived, **tokenless**, signed URL granting read of exactly
ONE blob in ONE space until an expiry — the safe way to show a blob to an untrusted
reader (browser, thin client) without handing out a bearer token. Enable by starting
coordd with `-cap-key-file` (see [OPERATIONS.md](OPERATIONS.md) for provisioning + rotation).

- **Mint** (`POST /v1/blobs/<hash>/url`) is authenticated; **readers may mint** —
  minting delegates only what the minter can already read. The blob must exist AND be
  used by a current file of the space: an entry of the manifest, or in a realtime space a live
  file of the tree (the frozen manifest and the trash do not count there). `ttl_sec` is clamped to
  `[10, -cap-max-ttl]` (default max 900); omitted/0 → 300. The returned `url` is
  root-relative — resolve it against the daemon's base URL (the SDK's `MintBlobURL`
  does this). Each mint appends one `blob.capurl.minted` audit event (entity = hash,
  context = expiry + key id — never the signature); fetches are not evented.
- **Fetch** (`GET /v1/cap/blob/...`) is the ONLY unauthenticated data path.
  `s` = lowercase-hex `HMAC-SHA256(key, "acp-blobcap-v1" \0 space \0 hash \0 e)`,
  compared in constant time; `k` names the signing key (first 8 hex of sha256(key)).
  `X-ACP-Space` is ignored — the space is signature-bound via `sp`. Validation order:
  parse → expiry (`410`) → signature (`403`) → existence (`404`), so unauthenticated
  callers get no existence oracle. Responses stream `application/octet-stream`;
  encrypted-at-rest blobs are returned as **plaintext** (an authorized delegated read).
- **Treat the URL as a secret** for that blob: it is a bearer capability. The daemon
  redacts `e=`/`s=` from its own request log; keep TTLs short and mint per use.
  Per-IP rate limit on the fetch path: `-cap-rate` (default 10/s, burst 4×).

## Collaborative documents (CRDT)
| Method | Path | Body / Query | Returns |
|--------|------|-------------|---------|
| POST | `/v1/crdt/ops` | `{doc, ops:[Op], epoch?}` | `{doc, total, epoch, dropped?, pending?}` (`dropped` = deletes of text the doc does not hold, not stored; `pending` = ops waiting for text that has not arrived); also emits a `crdt.op` event · `400` on an illegal doc name, bad JSON, more than 4096 ops or an insert that is not exactly one character (nothing in that push is applied) · `409` stale `epoch` (see CRDT epochs), or a push from a deleted document (the name was deleted and this first push leans on text it does not carry: nothing stored, re-read the document) · `507` when creating the document would pass the space's document limit (`max_docs` or `-max-docs`; existing documents keep accepting writes) |
| GET | `/v1/crdt/ops?doc=&from=N` | — | `{doc, ops:[Op], total}` (ops with index ≥ N) |
| GET | `/v1/crdt/doc?doc=` | — | `{doc, text, total}` (daemon-materialized) |
| DELETE | `/v1/crdt/doc?doc=` | — | `{doc, deleted:true, trash?}` — removes the document and frees a slot under the document limit; a stale push from before the delete is refused instead of bringing it back. Needs write scope on the document (`403` otherwise). `404 doc_not_found` when the name holds no document. Since v0.2.6 the deleted document is kept in the document trash (`trash: {doc, gen, deleted_at, expires_at, ops}`) for the space's `history_ttl_sec`; no trash when that is 0 |
| GET | `/v1/crdt/trash` | — | `{trash: [{doc, gen, deleted_at, expires_at, ops}]}` — deleted text documents you can restore (your read scope), oldest first; at most 1000 per space, the oldest dropped first |
| GET | `/v1/crdt/versions?doc=[&epoch=&at=]` | — | The text-document undo window (v0.2.6). Without `epoch`: `{doc, versions: [{doc, epoch, compacted_at, expires_at, ops}]}`, oldest first. With `epoch`: `{doc, epoch, at, ops, text}`, the text after the first `at` operations of that version (default all) · `404 version_not_found`. Kept for `history_ttl_sec`, the last 16 compactions per document, so a very busy document's window can be shorter |
| POST | `/v1/crdt/trash/restore` | `{doc, gen}` | `{doc, gen, restored:true}` — brings the entry back under its name (a new generation: clients holding the deleted copy re-fetch) · `409 doc_exists` while a document has the name · `404 trash_not_found` · `507` at the document limit. Names under `.acp-trash/` are reserved on every document route (`400 reserved_doc_name`) |
| GET | `/v1/crdt/list?limit=&cursor=&sizes=` | — | `[]{name, ops, size}`, sorted by name, one page at a time: `limit` default 500, max 5000; the next page's cursor is in the `X-ACP-Next-Cursor` response header (absent on the last page); `sizes=0` skips the size computation (`size` is 0; faster) |

`Op = {t:"ins"|"del", id:{c,r}, o?:{c,r}, v?:"<rune>"}`. Ops commute; clients track a
per-doc cursor (the `total` they've consumed).

**What a push could not apply (from v0.2.5, acp-1 §12.3.1).** The push answer carries `dropped` (ops the server did
not apply and never will from this push: a delete of a teammate's text it does not hold yet; not counted in `total`)
and `pending` (ops stored but waiting for text that has not arrived; they apply by themselves when it lands). Both
appear only when non-zero; treat an absent one as 0. After `dropped` > 0 for a delete, re-pull the document and, if
the text is there now, push the delete again.

## Structured (JSON) collaborative documents (ext-5, capability `"crdtjson"`)
A second, independent CRDT type whose materialized value is a **JSON structure**
(objects / arrays / scalars) instead of a character sequence: objects are OR-maps
(observed-remove, add-wins), scalars are LWW registers, arrays use the same
insert-after-origin rule as the text CRDT. Two agents editing **disjoint fields never
conflict**; concurrent writes to the same field resolve deterministically. Docs are
namespaced separately from text docs (the same name may exist on both).

| Method | Path | Body / Query | Returns |
|--------|------|-------------|---------|
| POST | `/v1/crdt/json/ops` | `{doc, ops:[JsonOp], epoch?, create_intermediate?}` | `{doc, total, epoch}`; emits a `crdt.op` event (`context.kind:"json"`) · `507` when creating the document would pass the document limit · `409`+`current` epoch if the doc was compacted · `409`+`current` **value** on an unresolvable path · `400` malformed op / unknown target nid / over the expansion bounds |
| GET | `/v1/crdt/json/ops?doc=&from=N` | — | `{doc, ops:[JsonOp], total, epoch}` (canonical ops with index ≥ N — v1 sync is full replay, cursored) |
| GET | `/v1/crdt/json/doc?doc=` | — | `{doc, json, total, epoch}` — the daemon-materialized value, **canonical bytes** (sorted object keys; identical on every replica) |
| DELETE | `/v1/crdt/json/doc?doc=` | — | `{doc, deleted:true, trash?}` — removes the structured document (frees a document-limit slot) · `403` outside write scope · `404 doc_not_found` · kept in the trash like a text document |
| GET | `/v1/crdt/json/trash` | — | the structured-document trash (same shape as `/v1/crdt/trash`) |
| POST | `/v1/crdt/json/trash/restore` | `{doc, gen}` | restore a structured document (same answers as `/v1/crdt/trash/restore`) |
| GET | `/v1/crdt/json/list?limit=&cursor=&sizes=` | — | `[]{name, ops, size}` (size = materialized JSON bytes), paged exactly like `/v1/crdt/list` (`X-ACP-Next-Cursor`) |

`JsonOp = {t:"set"|"del"|"lins"|"ldel"|"mv", ...}` addressed either by **path**
(`"path": ["ci","runs",0]` — object keys + array indexes from the root; a client
convenience) or by **nid** (canonical: `target` = parent node id `{c,r}`, plus `key`
for object set/del, `index_origin` for lins, `elem` for ldel; `idx` on a lins is a
live-index convenience resolved to an origin). `set`/`lins` carry `value`: a scalar
writes a register; an object/array is a **container literal** the daemon expands into
one node per member (bounded: depth ≤ 32, ≤ 4096 expanded ops per push → `400`).
`del` of an array index is translated to `ldel`.

### Identity-preserving move (`mv`) — ext-14, capability `"crdtjson-move"`
A fifth op `{t:"mv", from:[src…], path:[dst…]}` (path form; or nid form `{t:"mv",
child:{c,r}, target:{c,r}, key|index_origin}`) **relocates an existing node** — a
subtree under an object key, or an array element — to a new location **preserving its
nid**. No new endpoint: `mv` travels through `POST /v1/crdt/json/ops`. Guarantees:
identity preserved (a concurrent edit addressed to the node follows it), concurrent
moves of one node collapse to one LWW winner (no duplicate), a move that would create a
parent cycle is ignored and the node **stays put** (nothing vanishes), delete-after-move
deletes (no resurrection); all deterministic + convergent + compaction-transparent.
Errors: `400` unknown/root child or a move exceeding depth-32; `409` an unresolvable
source/dest path or a move into the node's own subtree. Advertised in `/v1/healthz`
capabilities as `"crdtjson-move"` (alongside `"crdtjson"`). The Go SDK's JSON-Patch
mapper (`pkg/client`) emits `mv` for RFC-6902 `move` when the capability is present.
Spec: [rfc/acp-ext-14-crdt-move.txt](../rfc/acp-ext-14-crdt-move.txt).

**The leader stamps everything.** Whatever form the client submits, the daemon
resolves paths against committed state, expands literals, and mints every node id,
tag, and LWW stamp **before** the op is stored/replicated; client-supplied ids and
stamps are overwritten (anti-spoof). Consequently **"last writer wins" means "last
arrival at the leader"** — ties are impossible: the full ordering token is
`(leader time, actor, op id)`. A path whose intermediate object key is missing is a
`409` carrying the current materialized value (rebase and retry), or is auto-created
as nested objects when `create_intermediate: true`.

**Epochs** work exactly like the text CRDT (see CRDT epochs): compaction bumps the
doc's epoch and a stale `epoch` on push → `409` + current epoch → re-pull from 0.
Admin compaction of a json doc: `POST /v1/admin/compact?doc=<name>&kind=json`.

### Which CRDT type? (decision guide)
- **Text CRDT** (`/v1/crdt/*`) — anything a human/agent edits as *prose or code*:
  files, notes, markdown. Character-level merges; concurrent typing interleaves.
- **JSON CRDT** (`/v1/crdt/json/*`) — anything with *fields*: dashboards, board/task
  models, form state, config trees, liquid-UI documents. Field-level merges;
  concurrent edits of different fields both survive; same-field scalar conflicts
  resolve LWW (whole value, no interleaving).
- **Don't** serialize JSON into the text CRDT (disjoint-field edits collide as
  character runs, often yielding invalid JSON), and **don't** store prose in one
  LWW register (concurrent paragraphs silently drop) — reference a text doc from a
  register instead, e.g. `{"body": "crdt:doc:notes"}`.

## Admin (role: admin)
| Method | Path | Query | Returns |
|--------|------|-------|---------|
| POST | `/v1/admin/gc` | `grace_sec` (default 600; a whole number 0–315360000, else `400 bad_grace_sec`) | `{removed, bytes}` — deletes unreferenced blobs older than grace (re-uploading bytes the server already has counts as a fresh upload) |
| POST | `/v1/admin/compact` | `doc` (optional), `kind=json` for a structured doc | `{dropped}` — compacts a CRDT doc's op-log (or all docs when `doc` is omitted, both types — on a single server only; **in a cluster `doc` is required**), GC-ing tombstones; bumps the epoch so clients re-pull from 0 |
| PUT | `/v1/admin/quota` | body `{space?, max_events?, max_docs?, max_blob_bytes?, max_leases?, max_mail?, sub_scope?/sub_scopes?, history_*, bulk_*}` (all fields: `GET /openapi.json`, schema `Quota`). Only the fields you send change (from v0.2.6); `null` returns one to its default/unlimited | the resulting full `{space, quota}` · `400` a field the server does not know (named in the error, nothing changed), a negative/out-of-range limit or a bad space name · `403` if the named `space` is outside the grant's space scope — see Per-space quotas |
| GET | `/v1/admin/quota` | `space` (optional) | `{space, quota}` · `403` if the named `space` is outside the grant's space scope |

> **Space scope applies to the `space` PARAMETER too.** These two routes select a space out-of-band
> (body/query) rather than via `X-ACP-Space`, so they enforce the grant's space allowlist on that
> parameter — and enforce it *before* the space would be auto-created, so a refused name never
> instantiates state. An unscoped admin is unaffected.

## Grants, spaces and invites (role: admin)

Per-agent access, workspaces and one-paste joining. Full request/response shapes: `GET /openapi.json`.
Grant and cluster calls need an **unscoped** admin grant (`403` for a scoped admin).

| Method | Path | Body / Query | Returns |
|--------|------|-------------|---------|
| GET | `/v1/admin/grants` | — | every per-agent grant: `[{fingerprint, name, role, scope?}]` — never a token |
| POST | `/v1/admin/grants` | `{name, role?, scope?, expires_at? \| ttl_sec?}` (`role` default `writer`; unknown fields are `400`; a grant with an end answers `401 grant_expired` past it and its streams close) | `{name, role, fingerprint, token, expires_at?}` — live on every node at once, **the token is shown once**. `409` when the server runs with one shared token (make the first per-agent grant with `coordd -mkagent` and restart) |
| DELETE | `/v1/admin/grants?fingerprint=` or `?name=` | — | `{revoked, result}` · `404` no such grant · `409` the name matches several grants (use the fingerprint) or it is the last grant |
| GET | `/v1/admin/spaces` | — | `[AdminSpace]` — every space on this server within your grant, with whether it is loaded and empty |
| DELETE | `/v1/admin/spaces?space=&confirm=` | `confirm` repeats the name; `discard_events=<exact event count>` to drop an event-only space's log on purpose | `{space, dropped}` (`dropped:false` = already gone) · `409` the default space, or a space that is not empty (the message lists what it holds) |
| POST | `/v1/admin/invites` | a `name` that already exists adds `space` to that agent (`add_member: true`, same role and limits, redeemed only with its own token). `{space, name? \| name_prefix?, role?, scope?, ttl_sec?, uses?, note?, grant_ttl_sec?}` — `grant_ttl_sec` (60 up to 10 years) ends each joiner's login that long after they join; `role` writer or reader only, `ttl_sec` 60–604800 (default 86400), `uses` 1–10 (default 1); `name` needs `uses` 1 | `201 {invite, secret}` — **the secret is shown once**. `403 invite_not_contained` (it would grant more than your own grant), `409` shared-token server / name taken, `429 invite_cap` |
| GET | `/v1/admin/invites?space=&status=` | — | `{invites:[...]}`, oldest first; never a secret |
| DELETE | `/v1/admin/invites/<id>` | — | `{revoked}` — stops new redemptions; agents that already joined keep access |
| DELETE | `/v1/admin/invites/<id>/member?name=` | `name` picks the member of a multi-use invite | `{revoked, name, space?}` (`space` when only this space was taken away) — removes the access the invite gave, from the login that redeemed it and never another login of that name · `409 member_ambiguous` the invite was redeemed more than once (removed members counted): name one, so a late retry never reaches a later member · `409 member_changed` the login that redeemed it is gone and another holds its name (remove that one through the grants API); sent again after it succeeded, exactly the first `200` · `404 member_unknown` · `403 member_not_contained` the login reaches beyond your own grant · `409` last grant |
| POST | `/v1/invites/redeem` | `{id, secret, name?, redeem_key?, client?}` (`id` and `secret` come from the invite code; **no token**: the invite secret is the credential; `Authorization` and `X-ACP-Space` are ignored) | `{token, fingerprint, name, space, role, scope?, inviter, retry?}` — store the token now. Send a `redeem_key` so a retry after a lost answer returns a fresh token for the same membership instead of spending another use. An invite with `add_member` needs the member's token in `Authorization` (`403 invite_member_only`) and answers `added: true` with no token. A name you choose cannot be one a removed agent held (`409 name_taken`); at the server's limit of agents, `507 identity_limit`. `404 invite_unknown` (unknown invite and wrong secret look the same), `410` expired/used up/revoked, `409 name_taken`, `403 member_revoked`, `429 rate_limited` |

Invite error bodies are `{error, reason, code}`, `code` holding the same token, with stable `reason` values (`invite_malformed`,
`invite_role_forbidden`, `invite_ttl_invalid`, `invite_uses_invalid`, `invite_scope_*`, ...).
The CLI wraps these as `acp invite` / `acp join` / `acp invites` (see `acp help invite`).

## Realtime filesystem (per-space mode)

A space is in one of two filesystem modes. **`cas`** (the default) is the versioned shared folder of
`/v1/manifest` + `/v1/commit`. **`realtime`** is a live tree where edits from several agents merge with no
version conflicts and a delete goes to a recoverable trash. Check the mode before choosing the surface:
the wrong one answers `409` with `code: "wrong_fs_mode"`.

| Method | Path | Body / Query | Returns |
|--------|------|-------------|---------|
| GET | `/v1/space/config` | — | `{fs_mode:"cas"\|"realtime"}` |
| PUT | `/v1/space/config` | `{fs_mode, confirm?}` (**admin**) | `{fs_mode, no_op?, checkpoint?, version?}` — switches the whole space without losing anything (a checkpoint is pinned first). A non-empty space answers `428 fs_mode_confirm_required`: resend `confirm` echoing the exact counts; changed counts are `428 fs_mode_confirm_mismatch`. `409` blocked: `leases_held`, `live_docs_unflushed`, `unrepresentable_path`, `unrepresentable_manifest`, `checkpoint_cap_at_capacity`. Per-prefix switches are `400 per_prefix_unsupported` |
| POST | `/v1/admin/fs/mode` | `{mode}` (**admin**) | shortcut for an EMPTY space, no confirm; a non-empty space answers `428` (use `PUT /v1/space/config`) |
| GET | `/v1/fs/tree` | — | `{nodes:[FsNode]}` — every live file and folder with stable node ids (a read-scoped token sees only its part) |
| POST | `/v1/fs/ops` | `{ops:[...]}` (create, mkdir, rename, move, delete, restore; 1–4096 ops, applied atomically, later ops see earlier ones; a delete may carry `if_empty: true`: the whole batch answers `409 folder_not_empty` unless the path is an empty folder at that point) | `{applied, outstamped?, warning?}` (`outstamped` = ops with no effect because a newer placement won: re-read and retry) · `403 bulk_denied` deleting many nodes without the bulk permission · `400 bad_blob_hash` / `400 blob_not_uploaded` when a create's `blob` is malformed or not stored (upload it with `POST /v1/blobs`, then retry; the batch changed nothing) |
| GET | `/v1/fs/trash` | `?checkpoint=` | `{trash:[FsTrashEntry]}` — recoverable deleted nodes. With the pin a realtime-to-cas switch made, lists what was in the trash at the switch |
| POST | `/v1/fs/trash/restore` | `{node_id, checkpoint}` | `{restored, version}` — in a space switched back to cas, puts a file that was in the trash at the switch back as a normal commit |
| POST | `/v1/admin/fs/prune` | — | `{evicted, oldest_trashed_at, history_bytes_over_cap, integrity_anomalies}` (**admin**) — apply the trash retention policy now |

## Start page and API description (no token by default)

| Method | Path | Returns |
|--------|------|---------|
| GET | `/` and `/v1/` | the start page: a JSON "start here" document for people and AI agents (what this server is, what to do first, how to get access, what each part of the API does, where the docs, SDKs and skills are). Never contains workspace, agent or identity data. An operator may replace it (`-root-page-file`) or turn it off (`-root-page=false`: then `404`) |
| GET | `/openapi.json` | this server's OpenAPI 3.1 description (same switch as the start page) |

## Per-space quotas (ext-7 §5, capability `"quotas"`)

`max_docs` caps CRDT document CREATION per store (text and json
each) as replicated per-space config. When set it **overrides** the `-max-docs`
node flag for that space (the decision becomes purely replicated state); 0 /
absent = the flag default governs (in a cluster, the leader's flag: each write carries it). Creation over the cap → **507** with
`used=/limit=` (no Retry-After — persistent; raise the quota or delete docs);
existing docs always append. Stats gain `quota.docs {used, limit}` when set.
SDK: `APIError.OverQuota()` classifies any 507 quota refusal (do NOT blind-retry).
Spaces isolate *data*; quotas bound *resources*, so one tenant of a shared daemon
cannot exhaust it for the others. An admin sets per-space ceilings; the daemon
enforces them on every write path (in cluster mode inside the replicated FSM — every
node, including one freshly bootstrapped from a snapshot, enforces the same numbers).

`PUT /v1/admin/quota {"space": "company_a", "max_events": 100000,
"max_blob_bytes": 1073741824, "max_leases": 64}` **changes only the fields it sends**
(from v0.2.6; before, it replaced the whole quota and silently reset the history and
large-change settings): a field you leave out keeps its value, `null` returns it to its
default. In the stored quota an absent or `0` limit means **unlimited** (never "block
all" — an unconfigured space behaves exactly as before quotas existed). Negative or beyond-int64 limits are `400`.
`space` defaults to the request's own `X-ACP-Space`; naming a fresh space creates it,
so a quota can be in force **before a tenant's first write**. The two RFC fields
`max_mail_fanout` / `max_groups` are accepted + stored but not yet enforced (group
mail / groups are not shipped).

**The 429-vs-507 client contract:**
- `507 Insufficient Storage` — a **storage** cap (`max_events` on the *retained*
  event count, `max_blob_bytes` on stored plaintext bytes). The condition persists
  until room is freed, so **do not blind-retry**: free space first (event-log
  compaction / `acp admin gc` — or the admin raises the quota), then retry. A `507`
  carries **no** `Retry-After`. A batch append that would cross the cap is refused
  whole (nothing appended); an `Idempotency-Key` on a refused write is NOT consumed —
  the same key retries cleanly after room frees.
- `429 Too Many Requests` — the **transient** cap (`max_leases` on concurrently live
  leases) plus ordinary rate limiting. Carries `Retry-After` (hinting the soonest
  lease expiry): back off and retry. A lease **renew** by the current holder never
  counts as a new lease; contention on a held resource stays `409`.

`GET /v1/stats` reports each bound as `"quota": {"events": {"used": N, "limit": M?},
"blob_bytes": {...}, "leases": {...}}` (`limit` omitted where unset; the whole block
absent until a quota is configured). `used` is the exact counter enforcement reads —
poll it to back off *before* hitting the wall.

**Multi-tenant model** (one space per customer or team): your own control plane holds
the admin token, decides each tenant's budget and pushes it via `PUT /v1/admin/quota`;
coordd enforces; tenant clients handle `429/507` like any HTTP client. No client-side
changes are required — enforcement lives in the protocol, not in a broker.

## Sub-scopes (ext-7 §6, capability `"tenancy.subscope"`)

| Method | Path | Body / Query | Returns |
|--------|------|-------------|---------|
| GET | `/v1/subscopes` | — | `[{name, events, last_seq, last_at}]` — labels seen in the RETAINED log (a derived view, like `/v1/channels`; `[]` when none) |

An OPTIONAL `sub_scope` label (`[A-Za-z0-9._:-]{1,64}`, 400 beyond) may ride
`POST /v1/events` (single + batch elements), `POST /v1/mail`, lease acquire bodies,
and `POST /v1/commit` — partitioning resource names within a space for scoping and
ACCOUNTING only. It is NOT an isolation boundary (§6.3): any reader of the space
reads every sub-scope; confidential separation needs a separate SPACE.

- A grant minted with `-sub-scope <label>` is PINNED: every sub_scope-bearing write
  it produces is server-forced to that label (a differing client label → 403 — the
  pin is unforgeable, like actor-forcing). Unpinned grants may use any valid label
  (implicit creation, §6.5). `whoami` echoes the pin.
- The label lands on stored events/messages and on the lease/commit AUDIT events
  (`lease.acquired`, `file.edited`/`file.deleted`), which is what `/v1/subscopes`
  and the stats `subscopes` breakdown count. Mail labels ride the stored message
  but are not in the event log, so they don't count in discovery (documented).
- A sub_scope grant requires the **version-3** `agents.json` envelope: an older
  binary refuses the file at its version check instead of silently dropping the
  constraint (fail-closed; re-mint rather than downgrade). Counters reflect the
  RETAINED log — compaction ages labels out of the window.
- Per-sub-scope QUOTAS (§5.5): `PUT /v1/admin/quota {"sub_scope":"p1","max_events":N}`
  caps a label's RETAINED events (per-label 507 `events[sub_scope p1] used=/limit=`
  past it; compaction frees room). Space and label caps both apply — the tighter
  governs. A sub-scope PUT edits one label; a space-level PUT preserves existing
  sub-quotas; an all-zero sub-scope PUT removes the label's caps. Configured limits
  show in `/v1/subscopes` (`limit`) and quiet-but-configured labels still list.
- Grant-level barring (ext-9 §8.2): mint with `-deny awareness` to bar a grant from
  awareness WRITES (its reads stay open; 403 / WS close 1008). Unknown deny surfaces
  refuse at mint/load (fail-closed); deny-grants require the v3 envelope; `whoami`
  echoes the deny.
- SDK: `Subscopes()`, `AcquireLeaseLabeled(res, ttl, subScope)`; events/mail/commits
  carry `SubScope` on their wire structs directly. CLI: `acp subscopes`,
  `--sub-scope` on `log`/`send`/`lease acquire`. MCP: `acp_subscopes` +
  `sub_scope` args on `acp_log_event`/`acp_send`.

## Scoped tokens (ext-7 §4, capability `"scopedtokens"`)
A per-agent grant MAY carry a **scope** that narrows it below its role:

```json
{ "name": "pod-42a", "role": "writer",
  "scope": { "path_prefix": ["projects/42/", "file:projects/42/"] } }
```

Mint with `coordd -data <dir> -mkagent pod-42a -role writer
-path-prefix projects/42/,file:projects/42/` (comma list). Each prefix must match
`[A-Za-z0-9._:/-]{1,128}`, non-absolute, with no `.`/`..`/empty segment — a
traversal-shaped prefix is rejected **at mint** (and again at load).

**Enforcement (write resource names, subtree match on path-segment boundaries):**
every commit `Change.Path` and every lease-acquire `resource` must lie inside at
least one listed prefix's subtree; otherwise **403** naming the offending
path/resource, the WHOLE commit refused, nothing applied. A scope only **narrows**:
reads are untouched (unless the grant also carries a `read_prefix`, see Read scopes),
the role check still runs first (a scope on a `reader` is moot; even an `admin`'s
commit paths are scoped), and the name-less write surfaces (events, mail, blob store
— a blob is content-addressed; names bind at commit) stay open. A commit path
containing `..` is `400` malformed input *before* any scope verdict — it can neither
dodge nor ride the prefix match.

**Prefix discipline (ext-27 §B.7):** a prefix is a SUBTREE and must end in `/`.
`projects/42/` admits `projects/42/x` and everything below; it refuses the
byte-adjacent sibling `projects/420/x` **and** a FILE named exactly `projects/42`
(the directory node is readable, never write-admissible — no file/dir collision).
`-mkagent` refuses a bare `projects/42`; a bare value in an EXISTING `agents.json`
is normalized to `projects/42/` at load with a logged WARNING naming the fix (the
grant keeps its subtree; only the old byte-prefix over-match is gone). Lease names
are matched the same way: commits guarded by leases use `file:<path>` names, so a
grant that should take file leases lists a `file:projects/42/` prefix too.

| Method | Path | Body / Query | Returns |
|--------|------|-------------|---------|
| GET | `/v1/whoami` | — | `{agent, role, bulk?, scope?, expires_at?}` (`bulk`, absent when the space does not exist, = `"allowed"` or `"denied"`: may you make large changes in this space) — the CALLER's own grant (`path_prefix`, `read_prefix`, `spaces`, `sub_scope`, `deny` as present), so a client can self-limit; `scope` omitted when unscoped; the token is **never** echoed. Any role. |

**Fail-closed grants file:** the moment any grant is scoped, `agents.json` is
written as the versioned envelope `{"version": 2, "agents": {token: {name, role,
scope}}}` (version 3 when any grant carries `sub_scope`/`deny`, version 4 when any
carries `read_prefix`). A coordd older than the envelope **refuses to start** on
that file instead of silently dropping the constraint and granting full role.
Unscoped-only files keep today's flat format, byte-identical. See [OPERATIONS.md](OPERATIONS.md) for the
upgrade order.

## Read scopes (ext-27, capability `"scopedtokens.read"`)
A grant MAY additionally carry a **read scope** — the READ counterpart of
`path_prefix` — minted with `-read-prefix p1[,p2…]` (each must end in `/`):

```json
{ "name": "pod-42a", "role": "writer",
  "scope": { "path_prefix": ["projects/42/", "file:projects/42/"],
             "read_prefix": ["projects/42/"] } }
```

**What it filters** — the token sees only its **slice of the TREE**: `GET
/v1/manifest` (a filtered `200`, never `403`); `GET /v1/blobs/{hash}` (and `HEAD`),
`POST /v1/blobs/has` and `POST /v1/blobs/{hash}/url` (a hash is readable/mintable
only if a path the token may read references it: a manifest path, or in a realtime space a
live file); collaborative-document
reads, ops-pulls and lists (text and JSON); and every write-echo body — commit `200`
and `409` manifests, lease listings. **An out-of-scope read is byte-, status- and
header-identical to a genuinely-missing resource** (filtered `200` / `404` / the
empty-document shape) — never a distinguishable `403`; only WRITES deny as `403`.
A read scope only narrows; an unscoped grant's responses are byte-identical to
today.

**Honest caveat — not confinement.** The **event log stays space-visible and carries
out-of-scope paths and blob hashes**; the mailbox, presence/awareness, `/v1/stats`,
`/v1/subscopes` and lease listings stay space-wide; the global manifest `version`
bumps on every out-of-scope commit. A read scope is content hygiene for a
semi-trusted bot/partner principal; hard isolation is a **separate space**. It does
NOT make a direct-to-browser token safe: an untrusted browser/end-user deployment
must use a broker or a separate space until a grant-pinned event/stats/presence
filter exists.

**Commit-by-hash (possession).** A read-scoped commit may reference a blob hash only
if that hash is reachable via a readable path **or the same token uploaded those
bytes** on this node; otherwise the commit fails with the SAME `400 blob … not
uploaded` an unknown hash gives (no laundering of out-of-scope bytes into scope, no
oracle). Possession is node-local and bounded — lost on restart/failover, on space
close, or under very heavy upload churn — so **a read-scoped commit refused with
`not uploaded` (code `blob_not_uploaded`) for bytes you hold means: re-PUT the bytes and retry** (`acp push`
does this itself; note `/v1/blobs/has` reports an uncommitted upload as missing
until it is committed). One read-scoped commit may carry at most **524,288** blob
references; more is `400 too many changes for a read-scoped commit … split the
commit` (a property of the request). Unscoped commits are uncapped.

**Rules:** every `path_prefix` the grant can write must be covered by a
`read_prefix` (refused at mint/load otherwise); a grant with a `read_prefix` and no
`path_prefix` writes only inside its `read_prefix` (since v0.2.6; before, it could write
anywhere); `read_prefix` requires the **v4**
`agents.json` envelope (a coordd older than read scopes refuses the file). In a cluster,
**upgrade every node before minting a read-scoped token** ([OPERATIONS.md](OPERATIONS.md) §2c).

## Roles (per-agent mode)
`agents.json` maps token → `{name, role, scope?}` (see Scoped tokens). Roles: **admin** (everything), **writer**
(read+write, no `/v1/admin/*`), **reader** (GET + `/v1/agents/beat` +
`POST /v1/awareness` — ephemeral presence, tightenable via `-awareness-write-role` —
+ minting `POST /v1/blobs/<hash>/url` — a read-shaped delegation — only; `/v1/admin/*` is
admin-territory for ALL methods, so a reader cannot GET `/v1/admin/quota` either). A
disallowed call returns **403**. In shared-token mode every caller is effectively admin.

## CRDT epochs
`POST /v1/crdt/ops` accepts an optional `epoch`; if it doesn't match the doc's current
epoch (the doc was compacted), the call returns **409** with the new epoch — the client
must re-pull from 0 and rebase. `GET /v1/crdt/ops` returns the current `epoch`.
Structured docs (`/v1/crdt/json/ops`) use the identical barrier with their own
per-doc epochs.

## Cluster / HA (Raft)
| Method | Path | Role | Returns |
|--------|------|------|---------|
| GET | `/v1/cluster/status` | any | `{mode, command_schema_v}` plus, in cluster mode, `{state, leader, is_leader, members}` (`mode:"single-node"` otherwise). Compare `command_schema_v` across members to spot a mixed-version cluster |
| POST | `/v1/admin/cluster/join` | admin (unscoped) | `{node_id, raft_addr}` → `{joined}`; adds a voter (forwarded to leader); also `{joined}` when already a member · `409` if refused (different `-idem-window`, older command schema, not in cluster mode, voter cap, id/address conflict) |
| POST | `/v1/admin/cluster/leave` | admin (unscoped) | `{node_id}` → removes a voter (forwarded to leader) · `400` `node_id` missing · `500` not in cluster mode / failed |

In cluster mode every **write** endpoint forwards to the leader automatically (a
follower proxies the request); **reads** are served locally (stale-tolerant). Right after an election the new
leader answers writes `503` with `Retry-After: 1` until it has applied the log it inherited (normally milliseconds);
from v0.2.6 that `503` carries `retry_safe: true`, and `acp`, the mount, `acp-mcp` and both SDKs send the request
again by themselves (up to 3 sends) when it is safe: a read, a request with an `Idempotency-Key`, or one marked
`retry_safe`. CRDT
`POST /v1/crdt/ops` carries/honors `epoch` (409 on stale). See
the protocol spec (`rfc/acp-1.txt`) for the model.

## MCP bridge
`acp-mcp` is a separate binary: a Model Context Protocol server (JSON-RPC 2.0 over
stdio) exposing ACP as tools. Its `tools/list` is the authoritative list; in v0.2.6 it is:

- who and what: `acp_whoami`, `acp_who`, `acp_stats`, `acp_subscopes`, `acp_awareness_set`, `acp_awareness_get`,
  `acp_awareness_clear`
- talking: `acp_send`, `acp_inbox`, `acp_ack`, `acp_thread`, `acp_log_event`, `acp_recent_events`, `acp_catchup`,
  `acp_wait_events`, `acp_lease`
- live text documents: `acp_doc_read`, `acp_doc_write`, `acp_doc_trash`, `acp_doc_restore`, `acp_doc_versions`
- structured documents: `acp_crdt_json_get`, `acp_crdt_json_ops`, `acp_crdt_json_list`
- shared files: `acp_manifest`, `acp_file_get`, `acp_file_put`, `acp_blob_put`, `acp_blob_get`, `acp_commit`,
  `acp_history`, `acp_restore`
- realtime filesystem: `acp_fs_mode`, `acp_fs_tree`, `acp_fs_ops`, `acp_fs_trash`, `acp_fs_restore`
- `acp://` addresses: `acp_read_address`, `acp_list_address`, `acp_write_address`, `acp_share`, `acp_address_info`

`acp_catchup`, `acp_wait_events` and `acp_thread` arrived in v0.2.5. Config via the same env vars as the CLI.

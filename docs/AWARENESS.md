# Awareness — live, ephemeral presence (ext-9)

*Introduced in v0.1.7. Capabilities: `awareness` (HTTP), `awareness-ws` (WebSocket). Wire version
`acp/1` unchanged.*

Awareness is a **live, self-expiring presence channel**: "who is here, and what are they doing
*right now*." Cursor positions, text selections, a "typing…" flag, a current-step status — the
fast-changing signals a collaborative UI shows the moment they happen and forgets seconds later.

It is the missing middle between ACP's two existing tiers:

| Tier | Question it answers | Lifetime |
|---|---|---|
| Event log (`/v1/events`) | *What happened?* (durable facts) | forever |
| **Awareness (`/v1/awareness`)** | ***What is happening right now?*** | **seconds (TTL)** |
| Heartbeat roster (`/v1/agents`) | *Who is alive?* | minutes / hours |

**The defining guarantee: awareness is never durable.** An awareness state is held in memory only.
It is **never** written to the event log, a CRDT document, or a snapshot, and it never replicates
as durable state. So a client can stream cursor motion at UI frequency forever without ever
growing or corrupting the durable record — the failure mode where fast presence traffic bloats a
document is structurally impossible.

## Model

Each awareness entry is keyed by **`(actor, session)`**:

- `actor` — the authenticated agent (server-derived; a client cannot spoof it).
- `session` — an opaque per-connection/per-tab id the client chooses (the `X-ACP-Session`
  header). One human in two browser tabs is two sessions, so two independent cursors.

You publish your **whole** current state each time (last-writer-wins for your `(actor, session)`);
there are no partial patches to reconcile. An entry disappears when its **TTL** elapses (default
30 s — so a crashed or disconnected client fades out on its own) or when you send an explicit
**leave**.

## HTTP API

| Method | Path | Body | Returns |
|---|---|---|---|
| `POST` | `/v1/awareness` | `{state:{…}, ttl_sec?}` | your stored entry; sets/refreshes it |
| `POST` | `/v1/awareness` | `{ttl_sec:0}` (empty/absent state) | a **leave** — removes your entry now |
| `GET` | `/v1/awareness` | — | snapshot: every live entry in the space |
| `GET` | `/v1/awareness?follow=true` | — | NDJSON stream: snapshot, then live `join`/`update`/`leave` deltas |

The follow stream is the same shape as the event stream (`application/x-ndjson`, one JSON object
per line). Reconnect at any time to get a fresh snapshot — there is no cursor to manage, because
old presence is not replayed; you always resync to *current* truth.

`state` is an opaque JSON object of your choosing (bounded in size). A common convention is a
`doc` field so a viewer can filter to one document's cursors client-side:
`{"doc":"boards/q3","cursor":{"x":40,"y":12},"status":"typing"}`.

## WebSocket transport (optional)

For sub-second cursor motion, one bidirectional socket is cleaner than a POST plus a follow
stream:

```
GET /v1/awareness?transport=ws        (Upgrade: websocket)
```

The socket carries your own `set`/`leave` frames **up** and everyone's deltas **down** — the shape
Figma/Miro/Liveblocks-style presence uses. The server applies a **coalescing tick (15 Hz by
default)**: no matter how fast clients write, each follower receives at most one delta per key per
tick, so a hundred cursor moves a second cost a follower at most fifteen frames. Fan-out is bounded
by (live entries × tick rate × subscribers) by construction. The socket requires the same bearer
token as every other endpoint; in a cluster it is served by the leader (followers redirect).

A daemon that implements only the HTTP tier is fully conformant — the WebSocket tier is advertised
separately as `awareness-ws` and negotiated independently.

## Client surfaces

- **SDK (Go):** `SetAwareness(state, ttl)`, `Awareness()` (snapshot), `FollowAwareness(ctx, fn)`
  (stream), `ClearAwareness()` (leave). Each is capability-probed.
- **CLI:** `acp awareness set '<json>'`, `acp awareness get`, `acp awareness watch`,
  `acp awareness clear`.
- **MCP:** `acp_awareness_set`, `acp_awareness_get`, `acp_awareness_clear` — an LLM agent can post
  and read live presence, its tool descriptions teaching the ephemeral discipline.
- **Runnable example:** [`examples/awareness-cursors/`](../examples/awareness-cursors/) — two
  terminals, a walking cursor over both transports.

## Scoping presence

A scoped token (ext-7) can be barred from writing presence with a `deny:["awareness"]` grant while
still reading it — for a display or dashboard that should observe but never publish. Reads remain
open per the space model (awareness is not confidential separation; a separate **space** is the
only hard boundary).

## When to use it

- **Human + agent collaborative surfaces:** live cursors, selections, typing indicators,
  multi-tab presence, "who is on this board" — without any of it entering the durable document.
- **Agent fleets:** each worker posts `{"task":"T-52","step":"tests","pct":60}`; an operator
  view (`acp awareness get`, or the MCP tool) shows what every agent is doing *this second*.

## Notes

- Presence is node-local in a cluster; the follow stream and WebSocket are served by the leader.
- Adoption is one capability probe plus two calls (`set` + `follow`); nothing else is required of
  a daemon or peer that does not implement it.
- Full normative spec: [`rfc/acp-ext-9-presence-awareness.txt`](../rfc/acp-ext-9-presence-awareness.txt).

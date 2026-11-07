# Using ACP from a web page

People can work in an ACP workspace from a browser — typing into the same notes as their agents,
chatting, reading mail, opening shared files — with the TypeScript SDK's browser build. This guide
covers how to connect a page safely and how to keep people's work from getting lost in a tab that has
no disk.

A working, tested example is in [`examples/guides/browser-backend/`](../examples/guides/browser-backend/):
a backend, a page, and a script that runs two "tabs" and an agent through it.

## The shape: a backend on your page's own site

```
browser page ──(same site, a session cookie)──▶ your backend ──(the person's ACP login)──▶ coordd
```

The page talks only to **a backend on its own site**. The backend knows who the person is, holds their
ACP login, and forwards the page's requests to coordd. The page holds no ACP token.

Why not connect the page straight to coordd?

- **A token in a page is a token anyone on that page can read.** Every script on the page, every
  browser extension and any injected code can read it. An ACP login lasts until it is removed unless
  it was given an end, and a writer login can change everything in its workspace.
- **Narrowing a login is not privacy in a page.** A login can be limited to parts of the file tree,
  but the workspace's activity log, who messaged whom, presence and lease names stay visible to every
  login of that workspace. Only a separate workspace keeps those apart.
- **coordd does not answer pages from other sites** (it sends no cross-site headers), and browsers
  reject its automatic self-signed certificate.

All three are deliberate safety defaults. Connecting a page directly, with short-lived narrow
logins, is a possible future addition; the backend is the way that works today, and it stays useful
afterwards (your backend is where people sign in).

## What your backend must do

1. **Know who the person is.** Your own sign-in, or an ACP invite the person redeems *through the
   backend* (`POST /v1/invites/redeem` needs no token: the invite is the credential). A backend that
   forwards for anyone who can reach it is an open door.

   **An invite works once.** If it was already redeemed (someone ran `acp join` with it, or the
   backend redeemed it before), the backend cannot redeem it again: coordd answers that it "was already
   used". The backend has no login of its own to sign in with, so pick one:

   - **Ask an admin for a new invite for the backend to redeem** (`acp invite --name <name> --space
     <workspace>`). If that name already has a login, coordd refuses with `name_taken`: use another
     name, or revoke the old login first. This is the normal way: one invite per person.
   - **Reuse a login that already exists**, for a single-person or demo backend. Read its token from a
     file only the backend can read (the `token_file` a saved profile points to, see `acp status`),
     check it with `GET /v1/whoami`, and give the browser only your own session cookie. Never one
     shared login for several people: rule 2 below no longer holds.
2. **Give each person their own ACP login.** Then coordd records their name (not the backend's) on
   everything they do, each person has their own rate budget (coordd limits each login, 50 requests a
   second by default), and a stolen session is worth one person. An invite redeemed through the
   backend makes exactly that: one writer (or reader) login in one workspace, never an admin.
3. **Keep the login server-side.** Give the browser a session cookie marked `HttpOnly; Secure;
   SameSite=Strict`. Never send the token to the page, never put it in a URL.
4. **Forward a list of routes, not everything.** Only the `/v1/…` routes your page uses — never
   `/v1/admin/…`, never the invite routes.
5. **Replace the credential on the way.** Set `Authorization` to the person's login, set
   `X-ACP-Space` to their workspace, and drop `X-ACP-Agent` and any other header the page sent.
6. **Refuse a coordd that uses one shared key.** There, identity is a header anyone can set. Ask
   `GET /v1/whoami` with a made-up `X-ACP-Agent`: if that name comes back, the server is in shared-key
   mode.
7. **Verify coordd's certificate** (its `cert.pem`, or a public certificate). Never turn TLS
   verification off outside a laptop demo.
8. **Pass event streams through unbuffered**, and close the upstream request when the page goes away.
9. **Sign the person out when coordd answers 401** for their login (an admin removed them).
10. **Refuse writes from other sites.** Check `Origin` / `Sec-Fetch-Site` on anything that is not a
    GET, and accept the login only as JSON.

The example's `backend.mjs` does all ten in under 400 lines of Node, with no dependencies.

## Connecting the page

```js
import { Client, TextDocSession, JSONDocSession, EventFilter, mountVirtualFS } from "/sdk/acp.esm.js";

const me = await (await fetch("/me")).json();             // your backend: who is signed in
const client = new Client({
  baseUrl: location.origin + "/acp",                       // your backend, not coordd
  agent: me.name,                                          // a label only: coordd names you by your login
  space: me.space,
});                                                        // no token: the client sends no Authorization
```

Leave `token` out (or pass `""`): the client then sends no `Authorization` header at all, and the
browser sends the session cookie by itself.

**One stream per page.** A browser opens only a few connections per site, and every live stream holds
one. Let every feature share the client's one stream with `client.subscribe(fn, { filter })` instead of
opening a stream each. It reconnects by itself, resumes where it left off, and calls `onResync` if the
server no longer has the events you missed (re-read what you show).

| Feature | In the page |
|---|---|
| Notes people type into together | `TextDocSession.open(client, "notes.md", { onRemote })`, then `setText(editor.value)` on every edit |
| A board, a form, settings | `JSONDocSession.open(client, "board", { onChange })`, then `apply([{ t: "lins", path: ["todo"], idx: 0, value: "…" }])` |
| Files | `mountVirtualFS({ uri: "acp://<backend-host>/<space>/<folder>/", client, conflict: "throw" })`, then `await fs.flush()` to save (see "Files" below) |
| Chat | a channel on the event log, in the room-message shape (see "Chat" below): `client.appendIdem(…)` and `client.subscribe(…)` |
| Mail | `client.send(…)`, `client.inbox(true)`; `client.waitForMail()` or `subscribe` to `chat.sent` to hear of new mail |
| Who's here | `client.setAwareness(state, 30, "tab")` and `client.awareness()` over plain HTTP |
| Locks | `client.withLease(name, ttl, (lease, lost) => …)` |
| History and undo | `client.history()`, `client.restore(…)` |

**Which routes each feature needs.** Your backend forwards only the routes in this table for the features
your page uses (every path is under `/v1`; the page's calls arrive at your backend as `/acp/v1/…`). Checked
against the SDK and `/openapi.json`. Every feature also needs the shared stream (`GET /events`) when it
follows live changes. The example's allowlist (`backend.mjs`) is the union of all rows.

| Feature | GET | POST |
|---|---|---|
| Shared stream (`subscribe`, used by all live features) | `/events`, `/stats` | (none) |
| Text notes (`TextDocSession`) | `/crdt/ops` | `/crdt/ops` |
| Board or form (`JSONDocSession`) | `/healthz`, `/crdt/json/doc` | `/crdt/json/ops` |
| Files in a normal space (`mountVirtualFS`) | `/healthz`, `/space/config`, `/manifest`, `/blobs/<hash>` | `/blobs`, `/commit` |
| Files in a realtime space | `/healthz`, `/space/config`, `/whoami`, `/fs/tree`, `/fs/trash`, `/blobs/<hash>`, `/crdt/doc`, `/crdt/ops` | `/blobs`, `/fs/ops`, `/crdt/ops` |
| Chat | `/events` (read), `/channels` (optional list) | `/events` |
| Mail | `/mail`, `/mail/thread`; for `waitForMail`: `/whoami`, `/stats`, `/events` | `/mail`, `/mail/ack` |
| Who's here | `/healthz`, `/awareness` | `/awareness` |
| Locks (`withLease`) | `/lease` (list) | `/lease/acquire`, `/lease/renew`, `/lease/release` |
| History and undo | `/history`, `/manifest` | `/commit` (a restore is a new commit) |

Add `/agents` and `/agents/beat` only if the page lists or announces agents. A page never needs
`/admin/…`, `/invites/…` or `/spaces`. Which of the two file rows applies depends on the workspace; let both
through if you do not know (`GET /space/config` says which).

**Chat.** A room message is one event on a channel. Send exactly this shape (the full rules are in
[ROOM-MESSAGES.md](ROOM-MESSAGES.md)); CLI and MCP agents in the same room then see it, and you see theirs:

```js
const ts = Date.now();                                      // milliseconds
const id = `c-${ts}-${me.name}-${crypto.randomUUID().slice(0, 8)}`;   // unique; reuse it on a retry
await client.appendIdem({
  channel: "chat",                                           // the room
  action:  "chat.msg",                                       // always this
  context: { id, from: me.name, text, ts, kind: "human" },   // "human" for a person, "agent" for an agent
}, id);                                                      // the id doubles as the idempotency key

client.subscribe(
  (e) => show(e.actor, e.context.text),                      // e.actor is who really posted (set by coordd)
  { filter: new EventFilter({ channels: ["chat"], actions: ["chat.msg"] }), from: 1 },
);
```

Show each `context.id` once, and skip events without an `id` or `text`. `from` is only a name typed by the
sender; trust `actor`. `chat.message` or a missing `id` is not a room message: other clients ignore it.

**Files.** The URI names a folder in the workspace. Workspace files sit at the **root**: the workspace
itself is `acp://<host>/<space>/`, and `acp://<host>/<space>/docs/` is its `docs/` folder. A path such as
`/files/` means a folder called `files` (the example keeps its files in one on purpose); do not add it unless
your files live there. When you pass `client`, **the host in the URI is never contacted**: requests go to the
client's `baseUrl`, which is your backend. Use your backend's host (`location.host`) so the URI reads as what the
page talks to; the space in the URI must equal the client's space or `mountVirtualFS` refuses.

`writeFile` only buffers. Nothing reaches the server until `flush()` resolves:

```js
const fs = mountVirtualFS({ uri: `acp://${location.host}/${me.space}/docs/`, client, conflict: "throw",
                            onPending: (unsaved) => setBadge(unsaved ? "unsaved" : "saved") });
await fs.writeFile("note.md", text);      // buffered: fs.hasPending() is now true
await fs.flush();                         // saved when this resolves; rejects (ECONFLICT, ELOCKED, EIO…) and stays pending otherwise
// fs.hasPending() is false only after every change was acknowledged
addEventListener("beforeunload", (e) => { if (fs.hasPending()) e.preventDefault(); });
```

The option `flushDelayMs` makes the mount flush by itself that many milliseconds after the last change (off by default); a failed automatic flush leaves the change pending, so keep checking `hasPending()`. `close()` flushes and then stops.

**Live documents.** `TextDocSession` runs the text merge in the page and follows the document through
the shared stream (a slow poll covers a stream that is down). Render `onRemote(text)` into the editor
straight away. If your editor renders later (a framework that batches updates), pass the text the user
edited from: `setText(newText, { base })` — then a teammate's change that arrived in between is kept,
not undone. A document deleted and made again by a teammate is picked up as the new document: the
session shows what the server holds, and text not yet saved is kept in front of it. `JSONDocSession` sends your changes and reads the merged value back; the server does the
merging. For arrays, insert and delete elements (`lins`, `ldel`) and move them (`mv`); never write a
whole array with `set` (a teammate's change to it at the same moment would be lost).

**Presence.** Browsers cannot authenticate coordd's presence WebSocket (a page cannot set headers on a
WebSocket), so pages use the HTTP calls above, through your backend.

## Keeping people's work safe without a disk

A page has no disk to fall back on: when the tab closes, whatever it held in memory is gone. These
rules keep that from losing anyone's work — yours or a teammate's.

| # | Rule | What to do in the page |
|---|---|---|
| R1 | **Nothing is saved until the server says so.** | Show "unsaved" while `hasPending()` is true (on the file mount and on each document session; both also call `onPending(unsaved)` when it changes) and ask before the tab closes: `addEventListener("beforeunload", e => { if (fs.hasPending() \|\| notes.hasPending()) e.preventDefault(); })`. A failed save stays unsaved; it never turns into "saved". |
| R2 | **A retry never doubles anything.** | Send events and mail with an idempotency key (`appendIdem`, `sendIdem`); the document sessions re-send the same changes, which the server applies once. A JSON change is not retried for you: if `apply()` fails without a clear refusal, look at the document (the session has re-read it) before sending again. |
| R3 | **Never delete because something looks missing.** | A partial or cut-off answer is an error, not an empty folder — the SDK rejects a truncated reply rather than use it. Only delete what the person asked to delete. |
| R4 | **Browser storage is a cache, never the copy.** | The browser may clear IndexedDB, Cache storage or the private file system at any time. An empty store means "nothing cached", never "files deleted". Do not mirror a workspace into browser storage and sync it both ways. |
| R5 | **A change based on an old view never overwrites a newer one.** | Open the file mount with `conflict: "throw"`: when a teammate saved the file after you opened it, your save fails with `ECONFLICT` and carries your bytes, and the page asks: keep theirs, keep yours, or merge. (The default `"sidecar"` policy writes your copy as `<name>.conflict` into the shared folder instead — right for agents, odd in a web UI.) Anything people type into at the same time should be a document, which has no conflicts at all. |
| R6 | **Big deletions need a clear yes.** | When the server refuses a large deletion until it is confirmed (`428`, `bulkConfirmRequired()`), show the counts to the person and resend only after they agree — never automatically. `TextDocSession.setText` refuses an edit that wipes almost all of a shared document (`DocTruncationError`) until you pass `{ confirm: true }` after asking. |
| R7 | **Check what you download.** | The SDK checks every file's content against its hash and fails on a mismatch (`BlobIntegrityError`); keep it on, and if you cache files in the browser, cache them by hash. |
| R8 | **Merge in the right order.** | Diff the person's edit against the text they were looking at, then merge the teammates' changes — never the other way round, which undoes what a teammate just typed. `TextDocSession` does this; pass `base` if your editor renders late. |
| R9 | **After a long gap, re-read.** | When the stream says it could not resume (`onResync`), re-read the file list or document you show instead of carrying on with a hole. The file mount and document sessions do this themselves. |
| R10 | **Recovery is a click away.** | Earlier versions (`history`, `restore`) and, in a realtime workspace, the trash are available to the page; offer them. |
| R11 | **Limit what one person can damage.** | One ACP login per person, limited to their workspace (invites do this) and, where it fits, to part of the tree. |

**Tabs and sleeping pages.** Each tab is its own editor: never share one document session (or its
replica id) between tabs. Browsers slow down timers in hidden tabs: a presence entry expires when its time to live runs out
(30 seconds in the example) and lease renewals can lapse. A lapsed lease is safe — the server refuses a stale lock
holder — and `withLease` tells you through `lost`; stop and re-acquire when the tab is visible again.

**Leaving.** `close()` on a text document session or the file mount saves what is pending and stops
following. It rejects, and keeps the changes, if the server could not be reached.

## Certificates and connections

- Browsers need a certificate they trust on **your site** (the page and the backend). coordd itself can
  keep its own certificate: the backend verifies it with coordd's `cert.pem`.
- Serve the backend over HTTP/2 if many tabs stay open: over HTTP/1.1 a browser allows only about six
  connections per site.
- See "Browser front door" in [OPERATIONS.md](OPERATIONS.md) for the server side and
  [THREAT_MODEL.md](THREAT_MODEL.md) ("Web pages") for what each rule protects against.

## Not available yet

- A page connecting to coordd directly (no backend): would need coordd to accept chosen sites and
  short-lived narrow logins.
- Presence over a WebSocket from a page.
- Keeping unsaved changes across a tab close (an outbox in browser storage).
- Separating one person's view of the activity log within a workspace: use one workspace per audience.

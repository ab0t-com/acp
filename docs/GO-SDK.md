# ACP Go SDK

The `pkg/client` package is the official **Go SDK** for ACP. It speaks the frozen
`acp/1` wire to a `coordd` daemon and gives you the coordination primitives — a
shared content-addressed filesystem, a totally-ordered event log, a directed
mailbox, fencing-token leases, CRDT co-editing, and live presence — as ordinary
Go calls, so you can **embed ACP coordination directly in your own Go service**.

> **Availability.** The Go SDK is published and `go get`-able today — it lives as a
> nested module in this repository under `sdk/go/`:
> `go get github.com/ab0t-com/acp/sdk/go/pkg/client`. It is engine-clean client code
> over the frozen `acp/1` wire (no daemon or storage internals), needs a running
> `coordd` (the free self-hosted binary / Docker image, a separate product), and
> versions independently of the daemon. The **`acp` CLI** and the **`acp-mcp`**
> bridge remain the other client surfaces.

## When to use the SDK (vs. the CLI / MCP)

| You are… | Use |
|---|---|
| a **Go program/service** that wants ACP as its coordination layer | the **Go SDK** (`pkg/client`) |
| a **shell script / one-off / human** | the **`acp` CLI** |
| an **LLM agent harness** (tool calls) | the **`acp-mcp`** bridge |

The SDK is the right tool when you want typed, in-process calls — an app backend
on ACP, a Go microservice using ACP for cross-service locks and a durable event
feed, or a custom integration — without shelling out or hand-writing JSON.

## Install & import

```bash
go get github.com/ab0t-com/acp/sdk/go/pkg/client@latest
```

```go
import (
    "github.com/ab0t-com/acp/sdk/go/pkg/client"
    "github.com/ab0t-com/acp/sdk/go/pkg/wire"      // the wire types you construct/read
    "github.com/ab0t-com/acp/sdk/go/pkg/crdt"      // text co-editing
    "github.com/ab0t-com/acp/sdk/go/pkg/crdtjson"  // structured (JSON) co-editing
)
```

The SDK's methods accept and return the types in `pkg/wire`, `pkg/crdt`, and
`pkg/crdtjson`. These are public, importable, and **stable** (they track the
frozen `acp/1` wire), so you construct arguments and read results with them.

## Connect

```go
// New(baseURL, token, agentID, certPath, insecure)
c, err := client.New("https://coordd.example:8443", token, "builder-1", "coordd.pem", false)
if err != nil { log.Fatal(err) }
c.SetSpace("acme")            // select a space (the hard isolation boundary)
if err := c.Health(); err != nil { log.Fatal(err) } // reachable + protocol compatible?
```

- `token` — your agent's bearer token (from the operator). Identity is derived
  from the token server-side; you cannot spoof another actor.
- `certPath` — the daemon's TLS cert for self-signed setups; `""` for the system
  trust store. `insecure=true` skips verification (dev only).

## Errors

Server failures are `*client.APIError` with the HTTP status and predicates:

```go
_, err := c.AcquireLease("build:main", 30)
var apiErr *client.APIError
if errors.As(err, &apiErr) {
    switch {
    case apiErr.Locked():    // 423 – held by someone else
    case apiErr.Conflict():  // 409 – version/lease conflict; reconcile & retry
    case apiErr.OverQuota(): // 507 – a storage quota is full (retrying won't help; 429 is the transient one)
    case apiErr.GrantExpired():  // 401 – this login reached its end; only a new login helps
    case apiErr.SpaceNotFound(): // 404 – no workspace of that name (nothing has written to it yet,
                                 //       or the name is wrong); Error() is the server's sentence
    case apiErr.FolderNotEmpty(): // 409 – a delete with wire.FSOp.IfEmpty met more than an empty
                                  //       folder; nothing applied, re-read the tree
    }
}
```

- **A workspace nothing has written to yet** answers `SpaceNotFound` to a reader. About to
  write? `ManifestForWrite`, `FSModeForWrite` and `PullCRDTOpsForWrite` treat it as empty, and
  your first write creates it.
- **No one-shot call waits for ever.** A call with no response headers within 60 s, or whose
  body stops for 30 s, ends with a `*client.TimeLimitError` (a write may still have been
  applied: read before sending it again). Streams are not bound. `c.SetTimeLimits(headers,
  stall)` changes both; 0 turns one off.
- **A redirect to another host or port is refused**, on every call but the live awareness
  stream, which follows a cluster follower's redirect to its leader.

## Core operations

```go
// Event log (the ordered spine): assign a monotonic Seq (a total order).
c.Append(wire.Event{Action: "build.done", Entity: "svc/api", Context: map[string]any{"ok": true}})
c.AppendIdem(wire.Event{Action: "deploy.start"}, "deploy-42") // retry-safe (exactly-once)
events, _ := c.ReadEvents(0)                                  // one-shot read
c.Follow(0, func(e wire.Event) error { /* live */ return nil })

// Filter server-side (channels + actions):
f := &client.EventFilter{Channels: []string{"deploy"}, Actions: []string{"build.*"}}
hits, _ := c.EventsFiltered(0, f)

// Mailbox (directed handoffs):
c.Send(wire.Message{To: "reviewer-1", Type: "request", Subject: "review", Refs: []string{"prs/7"}})
inbox, _ := c.Inbox(true); /* ... */ c.Ack(inbox[0].ID)

// Leases (safe mutual exclusion via fencing tokens):
lease, _ := c.AcquireLease("build:main", 30) // lease.Token is your fencing token
c.RenewLease("build:main", lease.Token, 30)
c.ReleaseLease("build:main", lease.Token)

// Shared filesystem (content-addressed blobs + CAS-versioned manifest):
hash, size, _ := c.PutBlob(strings.NewReader("hello"))
m, _ := c.ManifestForWrite() // a workspace nothing has written to yet is empty at version 0
c.Commit(wire.CommitRequest{
    BaseVersion: m.Version, Actor: "builder-1",
    Changes: []wire.Change{{Path: "docs/readme.md", Hash: hash, Size: size}},
}) // a 409 (Conflict) => re-read the manifest, rebase, retry
rc, _ := c.GetBlob(hash); defer rc.Close()

// Presence: durable roster + ephemeral awareness.
c.Beat("claude-code", "host-1", "working")
c.SetAwareness(map[string]any{"cursor": []int{12, 40}}, 30, "session-1")
```

## Following events in a long-running program

`FollowForever` reconnects and resumes by itself until its context ends; `Subscribe` runs many
subscribers, each with its own filter, starting point (nil `From` = from now on) and `OnResync`,
over one connection.

```go
err := c.FollowForever(last+1, handle, &client.FollowForeverOptions{
    Context:      ctx,
    Filter:       &client.EventFilter{Channels: []string{"team"}},
    OnResync:     func(g client.StreamGap) error { return reloadSnapshot() }, // events you missed are gone
    LogID:        lastLogID, // the log `last` belongs to ("" = none kept)
    LastEvent:    &client.EventMark{Seq: last, Hash: lastHash}, // lastHash = client.EventHash(e) of the event at `last`
    OnStreamOpen: func(o client.StreamOpen) { lastLogID = o.LogID }, // keep it with your position
})

stop := c.Subscribe(handle, client.SubscribeOptions{
    Filter: &client.EventFilter{Actions: []string{"chat.*"}},
})
defer stop()
```

**A restored server is never taken for the same history.** From v0.2.6 the server names its
event log (`X-ACP-Log-Id`), and the name changes at every server start and with every new
cluster leader. On a new name the follow checks the event at its position (the last one
delivered, or `LastEvent`): the same event means the same history (a restart, a leader change),
and the follow goes on with no gap. Another event, or none, is a restore: `OnResync` gets a
`client.GapRestored` gap (`"restored"`) with your old position (`From`), both names
(`PreviousLogID`, `LogID`) and the new head (`Head`), and delivery goes on at the new head —
re-read what you derive from the events. Without `OnResync`, `FollowForever` stops with a
`*client.LogChangedError` (a subscriber gets it through `OnError`): re-read, then follow again
from `err.Gap.ResumeAt` with `LogID: err.Gap.LogID`. The same name with a lower head is a
cluster node that is behind: no gap, the stream waits for it. A check answered by a node that
names no log, or another one, decides nothing and is asked again (`EventAt` callers can see a
`*client.LogUnconfirmedError`).

- `LogPosition(ctx)` gives the head and its log name from one answer — the place to start a
  follower "from now" (`head+1`, with `LogID` set). `EventAt(ctx, seq)` reads one event and the
  log that answered.
- `client.EventHash` is the same hash as the TypeScript SDK's `eventHash`.
- Against a server older than v0.2.6 (no log name) the earlier rule stays: a head below your
  position is a `GapBehind`.
- A login that reached its end stops `FollowForever` with that refusal; `Subscribe` tells each
  subscriber once through `OnError` and stops. A missing workspace does not stop them: a
  follower can start before the first write.

## Live co-editing (CRDT)

Text: keep a local replica, fold in pulled ops, push generated ops.

```go
doc := crdt.New("alice/editor-1") // your agent id, or `<agent>/<suffix>` (one per session); the server refuses other names
ops, _, epoch, _ := c.PullCRDTOps("notes.txt", 0)
for _, op := range ops { doc.Apply(op) }
c.PushCRDTOps("notes.txt", doc.GenerateOps("hello world"), epoch)
text, _, _ := c.CRDTText("notes.txt")
```

To learn what a push could not apply, use `PushCRDTOpsResult` (same arguments; `PushCRDTOps` is unchanged). Its
`Dropped` counts ops the server did not apply and never will from this push (a delete of a teammate's text it does
not hold yet); `Pending` counts ops stored but waiting for text that has not arrived — they apply by themselves when
it lands. Both are 0 on servers older than v0.2.5.

Structured (JSON): read the converged value, mutate via a local `crdtjson.Doc`.

```go
raw, _, epoch, _ := c.CRDTJSONDoc("board") // json.RawMessage
c.PushCRDTJSONOps("board", []crdtjson.Op{{T: crdtjson.OpSet, Key: "status"}}, epoch, true)
```

### Deleted documents and earlier text

From v0.2.6, deleted documents and earlier text stay reachable for the workspace's history time:

```go
entry, _ := c.DeleteDoc("notes.txt")        // nil entry: this workspace keeps no trash
trash, _ := c.DocTrash()                    // what can come back
c.RestoreDoc("notes.txt", trash[0].Gen)     // refused while a live doc holds the name
versions, _ := c.DocVersions("notes.txt")   // kept from before each compaction, oldest first
old, _ := c.DocVersionAt("notes.txt", versions[0].Epoch, 0) // old.Text; 0 = the whole version
```

Structured documents have the twins `DeleteJSONDoc`, `JSONDocTrash` and `RestoreJSONDoc`. A server
older than v0.2.6 answers that it does not keep deleted documents.

## More packages

- `pkg/acpuri` parses and resolves `acp://` addresses, with the same rules as the TypeScript SDK:
  see [ACP-ADDRESSES.md](ACP-ADDRESSES.md).
- `pkg/acpignore` is the `.acpignore` matcher: `acpignore.Load(root)`, then `m.Ignored(path)`.
  Code that syncs a folder needs more than the matcher: Section 7 of
  [the ignore-file extension](../rfc/acp-ext-43-ignore-file.txt) lists what a client must do with
  the answer (an ignored path is never a deletion).
- `pkg/merge`: `merge.Union(shared, local)` joins two texts with no common ancestor; both stay whole.

## Which primitive for what

| Record… | Use |
|---|---|
| a **fact** / audit trail (correctness) | the **event log** (`Append`) |
| **mutual exclusion** across machines | a **lease** (fencing token) |
| a **directed handoff** to one agent | the **mailbox** (`Send`) |
| an **artifact** (a file) | **blobs + a commit** (CAS) |
| a **live-co-edited** document | a **CRDT** doc (text or JSON) |
| an ephemeral **hint** ("what I'm doing now") | **awareness** (never correctness) |

## Notes

- **The space is the only hard isolation boundary.** Paths, channels, and
  sub-scopes organize within a space; they are not security boundaries.
- Your token may be **scoped** (least-privilege). An out-of-scope write returns
  `403`; if you rely on a scope as a security control, confirm the daemon
  advertises it (`Health()` / `Stats()`).

See `docs/API_REFERENCE.md` for the underlying HTTP endpoints and
`docs/ARCHITECTURE.md` for why the primitives are shaped this way.

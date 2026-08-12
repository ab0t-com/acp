# ACP Go SDK

The `pkg/client` package is the official **Go SDK** for ACP. It speaks the frozen
`acp/1` wire to a `coordd` daemon and gives you the coordination primitives — a
shared content-addressed filesystem, a totally-ordered event log, a directed
mailbox, fencing-token leases, CRDT co-editing, and live presence — as ordinary
Go calls, so you can **embed ACP coordination directly in your own Go service**.

> **Availability.** The Go SDK is delivered by ACP/EXT-23 and is consumed as a Go
> module: `go get github.com/ab0t-com/acp/pkg/client`. This requires the module
> source (`pkg/…`) to be published in the public repository — a release step. If
> your ACP install is CLI-only today, the **`acp` CLI** and the **`acp-mcp`**
> bridge are the client surfaces (see the `acp-client` skill); the Go SDK becomes
> available with the release that ships EXT-23.

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
go get github.com/ab0t-com/acp/pkg/client@latest
```

```go
import (
    "github.com/ab0t-com/acp/pkg/client"
    "github.com/ab0t-com/acp/pkg/wire"      // the wire types you construct/read
    "github.com/ab0t-com/acp/pkg/crdt"      // text co-editing
    "github.com/ab0t-com/acp/pkg/crdtjson"  // structured (JSON) co-editing
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
    case apiErr.OverQuota(): // 507 – a storage quota is full
    }
}
```

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
m, _ := c.Manifest()
c.Commit(wire.CommitRequest{
    BaseVersion: m.Version, Actor: "builder-1",
    Changes: []wire.Change{{Path: "docs/readme.md", Hash: hash, Size: size}},
}) // a 409 (Conflict) => re-read the manifest, rebase, retry
rc, _ := c.GetBlob(hash); defer rc.Close()

// Presence: durable roster + ephemeral awareness.
c.Beat("claude-code", "host-1", "working")
c.SetAwareness(map[string]any{"cursor": []int{12, 40}}, 30, "session-1")
```

## Live co-editing (CRDT)

Text: keep a local replica, fold in pulled ops, push generated ops.

```go
doc := crdt.New("replica-A")
ops, _, epoch, _ := c.PullCRDTOps("notes.txt", 0)
for _, op := range ops { doc.Apply(op) }
c.PushCRDTOps("notes.txt", doc.GenerateOps("hello world"), epoch)
text, _, _ := c.CRDTText("notes.txt")
```

Structured (JSON): read the converged value, mutate via a local `crdtjson.Doc`.

```go
raw, _, epoch, _ := c.CRDTJSONDoc("board") // json.RawMessage
c.PushCRDTJSONOps("board", []crdtjson.Op{{T: crdtjson.OpSet, Key: "status"}}, epoch, true)
```

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

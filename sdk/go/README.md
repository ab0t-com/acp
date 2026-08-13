# ACP Go SDK

The official Go client for **ACP** — a self-hosted coordination substrate that
gives a fleet of agents a **shared filesystem** and a **comms line** over one
frozen wire protocol (`acp/1`). This module is client code: it talks to a running
`coordd` daemon and turns the coordination primitives into ordinary Go calls.

- **Shared filesystem** — content-addressed blobs + a versioned, compare-and-swap
  manifest (`PutBlob` / `GetBlob` / `Manifest` / `Commit`).
- **Comms** — a directed mailbox (`Send` / `Inbox` / `Ack`) and a totally-ordered
  event log you can tail live (`Append` / `Follow`).
- **Leases** — cross-machine mutual exclusion with fencing tokens.
- **CRDT co-editing** — text and JSON documents that converge with no locks and no
  lost updates (the supporting feature underneath live collaboration).

> **What you need to run it.** The SDK is a **client**. It requires a reachable
> `coordd` — the ACP coordination daemon you run yourself. `coordd` ships as a
> **free public binary and Docker image**; you self-host it (one process, or an
> HA cluster). The daemon is a **separate product** and is **not** part of this
> module — this module is purely the `acp/1` client. See the ACP project for how
> to run `coordd`.

## Install

```bash
go get github.com/ab0t-com/acp/sdk/go/pkg/client
```

```go
import (
    "github.com/ab0t-com/acp/sdk/go/pkg/client"
    "github.com/ab0t-com/acp/sdk/go/pkg/wire"
    "github.com/ab0t-com/acp/sdk/go/pkg/crdt"     // text co-editing
    "github.com/ab0t-com/acp/sdk/go/pkg/crdtjson" // structured (JSON) co-editing
)
```

The module is **standard-library only** — no third-party dependencies. The
`wire`, `crdt`, and `crdtjson` packages are public, importable types: the SDK's
methods accept and return them, so you construct arguments and read results with
them directly. They track the frozen `acp/1` wire and are a committed API surface.

## Connect

```go
// New(baseURL, token, agentID, certPath, insecure)
//   token    – your agent's bearer token (minted by the coordd operator)
//   agentID  – your logical actor label (the true identity is derived from the
//              token server-side; you cannot spoof another actor)
//   certPath – path to the daemon's TLS cert (for self-signed setups); "" uses
//              the system trust store
//   insecure – true to skip TLS verification (dev only)
c, err := client.New("https://coordd.example:8443", token, "builder-1", "coordd.pem", false)
if err != nil {
    log.Fatal(err)
}

c.SetSpace("acme")           // select a space (the hard isolation boundary); default is "default"
if err := c.Health(); err != nil {
    log.Fatal(err)           // daemon reachable + protocol compatible?
}
```

## Errors

Every call returns a Go `error`. **Every server-reported failure is a
`*client.APIError`** — blob put/get and the stream-open paths included — exposing
the HTTP status and convenience predicates:

```go
_, err := c.AcquireLease("build:main", 30)
var apiErr *client.APIError
if errors.As(err, &apiErr) {
    switch {
    case apiErr.Conflict():  // 409 – CAS/epoch conflict, OR a contended lease
                             //       acquire (apiErr.Current is the holding
                             //       lease / current state — reconcile, retry)
    case apiErr.Locked():    // 423 – a WRITE gated by another holder's lease
                             //       (e.g. a commit to a leased file:<path>)
    case apiErr.OverQuota(): // 507 – a persistent storage quota; do NOT
                             //       blind-retry (429 is the transient one)
    default:                 // apiErr.Status has the raw code
    }
}
```

## Shared filesystem — blobs + a versioned manifest

File **contents** are immutable blobs (addressed by SHA-256); a versioned
**manifest** maps paths → hashes. Commits are compare-and-swap on the manifest
version, so concurrent writers can't silently clobber each other.

```go
// 1) Upload content (idempotent: same bytes -> same hash, no re-store).
hash, size, err := c.PutBlob(strings.NewReader("hello world"))

// 2) Read the current manifest and commit a change against its version (CAS).
m, err := c.Manifest()
m2, err := c.Commit(wire.CommitRequest{
    BaseVersion: m.Version,
    Actor:       "builder-1",
    Changes:     []wire.Change{{Path: "docs/readme.md", Hash: hash, Size: size}},
    Note:        "add readme",
})
// A 409 (apiErr.Conflict()) means someone else committed first: re-read the
// manifest, rebase your Changes on the new version, and retry.

// 3) Fetch content by hash.
rc, err := c.GetBlob(hash)
defer rc.Close()
```

## Comms — the mailbox (directed messages)

```go
_, err := c.Send(wire.Message{
    To:       "reviewer-1",
    Type:     "request",          // inform | request | response | propose | ack | handoff
    Subject:  "review needed",
    Body:     "PR #7 is ready",
    Refs:     []string{"prs/7"},
    ThreadID: "prs/7-review",     // threads are keyed by the thread_id YOU set
})

msgs, err := c.Inbox(true)        // unread only
for _, m := range msgs {
    // ... handle m ...
    _ = c.Ack(m.ID)               // mark read
}

// A thread is every message carrying the SAME caller-chosen ThreadID
// (participant-only). Note: a message's own ID is NOT its thread id — set
// ThreadID on each message of the conversation and query by that value.
thread, err := c.Thread("prs/7-review")
```

## Comms — the event log (the ordered spine)

```go
// Append a fact. The daemon assigns a monotonic Seq (a total order).
ev, err := c.Append(wire.Event{
    Action:  "build.done",
    Entity:  "svc/api",
    Context: map[string]any{"commit": "abc123", "ok": true},
})

// Retry-safe append (exactly-once under retries):
ev, err = c.AppendIdem(wire.Event{Action: "deploy.start", Entity: "svc/api"}, "deploy-42")

// One-shot read from a sequence number:
events, err := c.ReadEvents(0)

// Follow live (backlog then live; resumes by Seq):
err = c.Follow(0, func(e wire.Event) error {
    fmt.Printf("#%d %s %s\n", e.Seq, e.Action, e.Entity)
    return nil // return an error to stop following
})

// Filter by channel/action (filtered streams see seq GAPS by design — resume
// with from=<last-seen-seq+1> and the SAME filter):
f := &client.EventFilter{Channels: []string{"deploy"}, Actions: []string{"build.*"}}
err = c.FollowFiltered(0, f, func(e wire.Event) error { return nil })
```

## Leases — safe mutual exclusion (fencing tokens)

```go
lease, err := c.AcquireLease("build:main", 30) // 30s TTL
if err != nil {
    // A contended acquire is a 409: apiErr.Conflict() == true and
    // apiErr.Current is the holding lease (who has it, until when).
}
// Convention: lease "file:<path>" to gate commits — when -enforce-leases is on
// (the default), a commit touching <path> by anyone but the holder is a 423.
// lease.Token is your FENCING TOKEN — a stale holder (whose lease expired and was
// re-taken) always has a lower token and is rejected, so "the lock expired but I
// kept going" is caught.

_, err = c.RenewLease("build:main", lease.Token, 30) // extend before expiry
err = c.ReleaseLease("build:main", lease.Token)      // release when done
leases, err := c.ListLeases()
```

## CRDT — live co-editing (the supporting feature)

Many editors converge on one document with no locks and no lost updates. You keep
a local replica (`crdt.RGA`), fold in ops you pull, and push ops you generate.

```go
doc := crdt.New("replica-A")                 // stable, unique replica id per client

ops, total, epoch, err := c.PullCRDTOps("notes.txt", 0)
for _, op := range ops {
    doc.Apply(op)
}
myOps := doc.GenerateOps("hello world\nsecond line")
_, _, err = c.PushCRDTOps("notes.txt", myOps, epoch)

text, _, err := c.CRDTText("notes.txt")      // converged text (server-side)
_ = doc.Text()                               // or your local replica
_ = total
```

For structured documents (boards, records, config) use the JSON CRDT
(`CRDTJSONDoc` / `PushCRDTJSONOps` with the `crdtjson` package).

## Presence (ephemeral hint — never correctness)

```go
_, err := c.Beat("claude-code", "host-1", "working")   // durable-ish roster
roster, err := c.Agents()

_, err = c.SetAwareness(map[string]any{"cursor": []int{12, 40}}, 30, "session-1") // lossy, TTL'd
snap, err := c.Awareness()
```

## Which primitive for what

| You want to record… | Use |
|---|---|
| an **artifact** (a file) | **blobs + a commit** (the shared filesystem) |
| a **directed handoff** to one agent | the **mailbox** (`Send`) |
| a **fact** / audit trail (correctness) | the **event log** (`Append`) |
| **mutual exclusion** across machines | a **lease** (fencing token) |
| a **live-co-edited** document | a **CRDT** doc (text or JSON) |
| an ephemeral **hint** ("what I'm doing now") | **awareness** (never correctness) |

## Notes on identity and scope

- **Identity is derived from your token server-side.** You cannot spoof another
  actor by setting a field; the daemon stamps the true actor on every write.
- **The space is the only hard isolation boundary.** Paths, channels, and
  sub-scopes are organization within a space, not a security boundary.
- Your token may be **scoped** (least-privilege) by the operator. An out-of-scope
  write returns `403`.

## A minimal end-to-end program

```go
package main

import (
    "log"
    "strings"

    "github.com/ab0t-com/acp/sdk/go/pkg/client"
    "github.com/ab0t-com/acp/sdk/go/pkg/wire"
)

func main() {
    c, err := client.New("https://coordd.example:8443", token, "builder-1", "coordd.pem", false)
    if err != nil { log.Fatal(err) }
    c.SetSpace("acme")

    hash, size, err := c.PutBlob(strings.NewReader("# Readme\n"))
    if err != nil { log.Fatal(err) }

    m, err := c.Manifest()
    if err != nil { log.Fatal(err) }

    if _, err := c.Commit(wire.CommitRequest{
        BaseVersion: m.Version,
        Actor:       "builder-1",
        Changes:     []wire.Change{{Path: "README.md", Hash: hash, Size: size}},
        Note:        "add readme",
    }); err != nil { log.Fatal(err) }

    if _, err := c.Append(wire.Event{Action: "docs.published", Entity: "README.md"}); err != nil {
        log.Fatal(err)
    }
}
```

---

*This module speaks the frozen `acp/1` wire; the public `wire`/`crdt`/`crdtjson`
type packages track it and are a committed API surface. Run your own `coordd`
(free public binary + Docker image) to serve it.*

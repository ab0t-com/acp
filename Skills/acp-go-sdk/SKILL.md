---
name: acp-go-sdk
description: Build a Go program or service that embeds ACP (Agent Coordination Protocol) as its coordination layer using the official Go SDK (github.com/ab0t-com/acp/pkg/client) — connect to coordd and, as typed in-process calls, append/follow the ordered event log, send/receive mailbox messages, take fencing-token leases, put/commit content-addressed files against a versioned manifest, co-edit text and JSON documents (CRDT), and publish/read presence. Use this skill whenever you are writing Go code (an app backend, a microservice, a custom integration) that must coordinate with other agents/services through ACP rather than shell out to the `acp` CLI; whenever you see `pkg/client`, `pkg/wire`, `pkg/crdt`, `import "github.com/ab0t-com/acp/..."`, `client.New(...)`, `wire.Event`, `wire.CommitRequest`, or "the ACP Go SDK / Go client"; or when the task is "use ACP from my Go app / embed ACP in a service." For shell/human use prefer the `acp` CLI (`acp-client` skill); for an LLM tool-call harness prefer `acp-mcp`.
---

# Using the ACP Go SDK

The Go SDK (`github.com/ab0t-com/acp/pkg/client`) lets a Go program use ACP's
coordination primitives as ordinary typed calls — so you can **embed ACP directly
in your own service** instead of shelling out to the CLI. It speaks the frozen
`acp/1` wire to one coordination daemon (`coordd`).

> **Availability.** The SDK is delivered by ACP/EXT-23 and consumed as a Go module
> (`go get github.com/ab0t-com/acp/pkg/client`). This requires the module source to
> be published in the public repo (a release step). If your install is CLI-only
> today, use the **`acp` CLI** (`acp-client` skill) or **`acp-mcp`**; the Go SDK
> becomes available with the release that ships EXT-23.

## When to use the SDK

- **Yes:** a Go app backend on ACP; a Go microservice that needs cross-service
  **locks** (leases) + a durable, ordered **event feed**; a custom integration
  that reads/writes the shared filesystem or co-edits documents in-process.
- **Prefer the CLI** (`acp-client` skill) for shell scripts, one-offs, and humans.
- **Prefer `acp-mcp`** for an LLM harness that drives ACP through tool calls.

## Mental model (read first)

- There is **one authority** (`coordd`). You and every other participant are
  *clients*; you never talk peer-to-peer — you rendezvous through the daemon,
  which gives one consistent view (ordering, locks, audit).
- **Right primitive for the job:** facts/audit → the **event log**; mutual
  exclusion → a **lease** (fencing token); a directed handoff → the **mailbox**;
  a file/artifact → **blobs + a CAS commit**; live co-authoring of one document →
  a **CRDT** doc; an ephemeral "what I'm doing now" hint → **awareness** (never
  build correctness on it).
- **Identity is derived from your token** server-side — you cannot spoof another
  actor. The **space** is the only hard isolation boundary.

## The three public type packages

Your code imports these alongside the client; they are the typed vocabulary the
SDK accepts and returns, and they are **stable** (they track the frozen `acp/1`
wire):

- `pkg/wire` — `Event`, `Message`, `Lease`, `Manifest`/`ManifestEntry`, `Change`,
  `CommitRequest`, `Agent`, the awareness types, and the header/version constants.
- `pkg/crdt` — the **text** CRDT: `Op`, `ID`, and the `RGA` authoring helper
  (`New`, `GenerateOps`, `Apply`, `Text`).
- `pkg/crdtjson` — the **structured (JSON)** CRDT: `Op`, `Doc`, and the op-type
  constants.

## Quickstart

```go
import (
    "log"; "strings"
    "github.com/ab0t-com/acp/pkg/client"
    "github.com/ab0t-com/acp/pkg/wire"
)

func main() {
    c, err := client.New("https://coordd.example:8443", token, "builder-1", "coordd.pem", false)
    if err != nil { log.Fatal(err) }
    c.SetSpace("acme")
    if err := c.Health(); err != nil { log.Fatal(err) }

    // Record a fact on the ordered log.
    c.Append(wire.Event{Action: "build.done", Entity: "svc/api"})

    // Take a lock, write a file against the manifest (CAS), release.
    lease, _ := c.AcquireLease("release:svc/api", 30)     // lease.Token = fencing token
    hash, size, _ := c.PutBlob(strings.NewReader("# Readme\n"))
    m, _ := c.Manifest()
    if _, err := c.Commit(wire.CommitRequest{
        BaseVersion: m.Version, Actor: "builder-1",
        Changes: []wire.Change{{Path: "README.md", Hash: hash, Size: size}},
    }); err != nil { log.Fatal(err) }         // 409 => re-read manifest, rebase, retry
    c.ReleaseLease("release:svc/api", lease.Token)
}
```

## What you can build

- **An app backend on ACP** — a self-hosted service whose shared state, files,
  and event feed *are* ACP (no separate database to run).
- **Cross-service coordination** — fencing leases for "one owner at a time" +ordered, replayable events for the audit trail.
- **Collaborative surfaces** — text/JSON CRDT co-editing for boards, docs, and
  live records shared by agents and people.
- **A shared workspace for a fleet** — content-addressed, deduplicated files with
  a versioned manifest and 3-way reconciliation.

## Errors & scoping

- Server failures are `*client.APIError`: use `Conflict()` (409, reconcile &
  retry), `Locked()` (423, someone else holds the lease), `OverQuota()` (507).
- Your token may be **scoped** (least-privilege). An out-of-scope write returns
  `403`; if you rely on a scope as a security control, confirm the daemon
  advertises it via `Health()` / `Stats()`.

## Reference

- **`docs/GO-SDK.md`** — the full SDK guide (every primitive, worked examples).
- **`docs/GO-SDK-USE-CASES.md`** — what the SDK unlocks vs. the CLI/MCP, patterns, and ideas to build.
- **`docs/API_REFERENCE.md`** — the underlying HTTP wire the SDK speaks.
- **`docs/ARCHITECTURE.md`** — why the primitives are shaped this way.
- **`acp-client` skill** — the CLI equivalent for shell/human use.

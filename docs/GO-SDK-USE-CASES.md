# The Go SDK: what it unlocks, and ideas to build

> A companion to **[GO-SDK.md](GO-SDK.md)** (the reference guide). This page is
> about *why* you'd reach for the Go SDK and *what* to build with it.

## The mental shift

ACP already gives you an ordered event log, a content-addressed shared
filesystem, fencing-token leases, CRDT co-editing, presence, and a mailbox —
today, through the `acp` CLI and the `acp-mcp` bridge. The **Go SDK doesn't add
features. It changes how you reach them**: from *a tool you invoke* to **a
coordination layer you build your Go service on** — typed, in-process,
embeddable.

That distinction is the whole point. You don't build a product by shelling out to
a CLI or running a subprocess; you build it by importing a library and calling
it. The SDK is that library.

## What it newly enables (vs. the CLI / MCP)

| You want to… | Before the SDK | With the SDK |
|---|---|---|
| **Embed ACP in your own Go service** | Fork the `acp` CLI per op, or run an `acp-mcp` subprocess | One `import`, typed in-process calls in your own binary |
| **React to the log live, in real code** | Pipe `acp watch` and parse a subprocess's lines | `Follow` in a goroutine; act on typed events with your own logic |
| **Coordinate on a hot path** (many ops/sec) | Fork-a-process-per-call overhead | Reused connection, goroutines, batched idempotent writes |
| **Write robust, testable integrations** | Parse CLI stdout strings (brittle) | Compile-checked wire types + typed errors (`Conflict()`/`Locked()`/`OverQuota()`) |
| **Build tools / bridges / other-language SDKs** | Wire types were internal — nothing to build on | Public `pkg/wire`/`crdt`/`crdtjson` — the one definition to build & codegen from |

**In one line:** the SDK turns ACP into an embeddable coordination layer for Go
programs — the thing you can't get by scripting a CLI.

## Patterns to build

Each of these is a shape ACP is *good at* because of how it's built. Pick the one
whose hot path matches your problem.

### 1. An app backend on ACP
Your service's shared state, files, and activity feed *are* ACP — no separate
database, lock service, or message bus to run. One `coordd`, one binary of yours.
- **Fits because:** ACP is a shared realtime filesystem + coordination substrate.
- **Use:** `Commit`/`Manifest` (files), `Append`/`Follow` (state + feed), leases (locks).

### 2. A distributed-lock / single-owner service
"Exactly one worker owns this resource at a time," safe even if a worker stalls.
- **Fits because:** leases carry a **fencing token** — a stalled owner always
  loses its token and is rejected.
- **Use:** `AcquireLease` → `lease.Token` on protected writes → `ReleaseLease`.

### 3. A durable, ordered event bus with replay + audit
Append facts to a totally-ordered log; anyone replays from any point; follow live.
- **Fits because:** the log is append-optimized and totally ordered (the spine of
  shared truth), with retention.
- **Use:** `Append`/`LogBatch` (write), `ReadEvents`/`Follow` (replay + live).

### 4. A collaborative-editing backend
Many people/agents edit one document and converge — no locks, no lost updates.
- **Fits because:** CRDT text and JSON are first-class, server-merged.
- **Use:** `crdt.New`/`GenerateOps`/`PushCRDTOps` (text), `PushCRDTJSONOps`/`CRDTJSONDoc` (structured).

### 5. A shared workspace / artifact store for a fleet
Deduplicated, integrity-checked files under a versioned manifest with CAS commits.
- **Fits because:** content is addressed by hash (dedup + integrity), the manifest
  is versioned, commits are compare-and-swap.
- **Use:** `PutBlob`/`GetBlob`, `Manifest`/`Commit`, `MissingBlobs`.

### 6. A real-time presence surface
"Who's online" (durable roster) + "what they're doing right now" (ephemeral,
TTL'd, lossy) — live cursors, status, activity.
- **Fits because:** presence is a dedicated tier; the ephemeral half never touches
  durable storage, so cursor spam can't bloat anything.
- **Use:** `Beat`/`Agents` (roster), `SetAwareness`/`Awareness`/`FollowAwareness`.

### 7. A reactive agent / worker
A service that sleeps until something happens — a handoff arrives, a lease frees,
a file changes — then acts.
- **Fits because:** the follow stream is backlog-then-live and resumable by
  sequence; you hold it in a goroutine and react with real Go.
- **Use:** `Follow`/`FollowFiltered` + `EventFilter`, plus the mailbox for handoffs.

### 8. A bridge / adapter to another system
Ingest from GitHub/Slack/Kafka → the ACP log; or follow the ACP log → feed a
purpose-built store you query separately.
- **Fits because:** ACP is the coordination truth; specialized query/search/
  analytics live in a **sidecar** you feed from the event stream (don't bend ACP
  into a query engine).
- **Use:** `Append` (ingest) or `Follow` → your sidecar (Postgres, a search index, …).

## Two quick recipes

**A single-owner worker (lock + do + record + unlock):**
```go
lease, err := c.AcquireLease("job:reindex", 60)
if err != nil { return } // *APIError.Locked() => someone else owns it
defer c.ReleaseLease("job:reindex", lease.Token)

// ... do the work, writing files/events guarded by lease.Token ...
c.Append(wire.Event{Action: "job.done", Entity: "job:reindex"})
```

**A reactive relay (wake on the log, act, forward):**
```go
c.Follow(0, func(e wire.Event) error {
    if e.Action == "task.assigned" && e.Context["to"] == "me" {
        // ... do the task, then hand off ...
        c.Send(wire.Message{To: "next-stage", Type: "handoff", Refs: []string{e.Entity}})
    }
    return nil // return an error to stop
})
```

## What the SDK does *not* do (so you pair it well)

- **It doesn't run your code inside the daemon.** There are no server-side plugins
  (deliberate — arbitrary logic in the authority would break determinism and the
  security model). You extend ACP by building **clients**, and you get
  trigger/reactive behavior by **following the event stream** in your own process.
- **It isn't a query engine.** You read by coordination access patterns (the log, a
  mailbox, a document, a lease, a manifest path), not arbitrary SQL. For querying,
  search, or analytics, feed a **sidecar** from the event stream (pattern #8).
- **It's the *Go* SDK.** Other languages consume ACP via the CLI/MCP today, or via
  future SDKs generated from these same public wire types.

## Availability

The Go SDK is delivered by ACP/EXT-23 and consumed as a Go module
(`go get github.com/ab0t-com/acp/pkg/client`), which requires the module source to
be published in the public repository (a release step). Until then, the **`acp`
CLI** and **`acp-mcp`** are the client surfaces — see the `acp-client` skill. When
the SDK release lands, everything on this page applies as written.

---

*See **[GO-SDK.md](GO-SDK.md)** for the full reference, **[API_REFERENCE.md](API_REFERENCE.md)**
for the underlying wire, and **[ARCHITECTURE.md](ARCHITECTURE.md)** for why the
primitives are shaped this way.*

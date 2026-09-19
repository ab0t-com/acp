# ACP SDKs and modes — how to use ACP, and how to pick

The mount is one way to use an ACP space; the SDKs are another. This document
covers the **Go** and **TypeScript** SDKs, how they relate to the mount, which ACP
skill supports each mode, and a decision guide for picking a mode.

The through-line: the **mount is the same core (`pkg/view`) driven implicitly by
the filesystem**, while the **SDK is the same substrate driven explicitly by your
program**. The mount says "just use files"; the SDK says "call the API." They talk
to the same daemon over the same frozen `acp/1` wire.

## The Go SDK

Install the public Go SDK — the externally-consumable module. Always lead with this
path; the in-repo canonical `pkg/client` (module `github.com/ab0t-com/acp`) is NOT
go-gettable and would pull in daemon code.

```sh
go get github.com/ab0t-com/acp/sdk/go/pkg/client
```

It is a plain HTTPS client: pinned daemon cert, bearer token, an agent id on every
request; all methods are safe for concurrent use. Sibling packages live under
`github.com/ab0t-com/acp/sdk/go/pkg/{wire,crdt,crdtjson,acpuri}`.

> Internals: `sdk/go` is a generated, engine-clean re-export of `pkg/client`. Daemon
> engineers build against `pkg/client` in-repo; external users always `go get` the
> public `sdk/go/...` path.

```go
import "github.com/ab0t-com/acp/sdk/go/pkg/client"

// New(base, token, agent, certPath string, insecure bool)
c, err := client.New("https://coord.example:8443", token, "builder", "cert.pem", false)
if err != nil { log.Fatal(err) }
c.SetSpace("team")

if err := c.Health(); err != nil { log.Fatal(err) }
```

### Shared files (CAS): manifest + content-addressed blobs

This is exactly what the mount's CAS backend drives, but explicit:

```go
// Read the current tree.
m, _ := c.Manifest()                       // wire.Manifest: Version + Entries[path]

// Write a file: upload bytes (content-addressed), then commit referencing the hash.
hash, size, _ := c.PutBlob(bytes.NewReader([]byte("- ship it\n")))
_, err = c.Commit(wire.CommitRequest{
    BaseVersion: m.Version,                 // CAS guard: someone else committing first → 409
    Changes:     []wire.Change{{Path: "notes.md", Hash: hash, Size: size}},
    Note:        "builder: add note",
})

// Stream a blob back.
rc, _ := c.GetBlob(hash); defer rc.Close()
```

A `409` means another writer landed first; re-read `Manifest()` (or the `409`
body's `current`) and retry the disjoint subset — the mount automates exactly this
loop (which the mount automates), but with the SDK you own it.

### Realtime tree + live docs

The realtime backend's surface, explicit:

```go
mode, _ := c.FSMode()                       // "cas" | "realtime"
nodes, _ := c.FSTree()                       // []wire.FSNode, already materialized
_, _ = c.FSMkdir("proj/docs")
_, _ = c.FSCreate("proj/docs/readme.md", blobHash, "")   // blob file
_, _ = c.FSMove("proj/a.md", "proj/b.md")
_, _ = c.FSDelete("proj/old.md")             // to trash; FSTrash/FSRestore recover

// A live text doc (CRDT): read, then push/pull ops.
text, total, _ := c.CRDTText("proj/spec.md")
ops, total, epoch, _ := c.PullCRDTOps("proj/spec.md", 0)
_, _, _ = c.PushCRDTOps("proj/spec.md", myOps, epoch)
```

### Comms, presence, leases, history

The rest of the substrate the mount does not expose as files:

```go
c.Send(wire.Message{To: "reviewer", Body: "PR up"})   // mailbox
msgs, _ := c.Inbox(true)                                // unread
c.FollowFiltered(0, &client.EventFilter{Actions: []string{"file.*"}}, func(e wire.Event) error {
    // the same event stream the mount's live-follow consumes
    return nil
})
lease, _ := c.AcquireLease("hot/file.md", 60)           // don't-clobber a hot path
c.RenewLease("hot/file.md", lease.Token, 60)
h, _ := c.History(20, 0)                                 // version timeline (undo)
c.PathHistory("notes.md", 20, 0)
```

## The TypeScript SDK — `@ab0t/acp`

`sdk/ts` publishes `@ab0t/acp`. Its `Client` mirrors the Go client's surface with
idiomatic `async` methods:

```ts
import { Client } from "@ab0t/acp";

const c = new Client({ baseUrl: "https://coord.example:8443", token, agent: "builder",
                       space: "team" /* fetchImpl/webSocket optional; TLS pinning via your fetchImpl */ });

const m = await c.manifest();
const { hash, size } = await c.putBlob(new TextEncoder().encode("- ship it\n"));
await c.commit({ baseVersion: m.version, changes: [{ path: "notes.md", hash, size }] });

await c.send({ to: "reviewer", body: "PR up" });
const tree = await c.fsTree();
await c.fsOps([/* realtime tree ops */]);
```

### `mountVirtualFS` — the in-process virtual filesystem (not an OS mount)

The TS SDK also ships a **programmatic** virtual filesystem, `mountVirtualFS`,
which is the TypeScript parity of the Go `pkg/view` core: the same overlay + barrier
+ the never-stale-negative cache rule (a cache may be stale but never serves a wrong negative),
exposed as a `VirtualFS` object your code calls.
**It is not a kernel mount — there is no folder on disk.** It is how a JS/TS program
gets "filesystem-shaped" access to a space without the OS mount facility.

```ts
import { mountVirtualFS } from "@ab0t/acp";

const vfs = mountVirtualFS({
  uri: "acp://coord.example:8443/team/",
  profiles,                    // exactly one of `profiles` or `client`
  conflict: "sidecar",         // "sidecar" (default) | "throw"
});
await vfs.writeFile("notes.md", new TextEncoder().encode("- ship it\n"));
const report = await vfs.flush();          // the barrier: one commit
for (const e of await vfs.readdir("")) { /* ... */ }
```

Note the TS conflict vocabulary is `"sidecar" | "throw"` (the Go/native equivalent
of `throw` is `--conflict=error` → `ESTALE`). It auto-detects the space's fs mode
and picks the CAS or realtime backend, exactly as the native mount does.

Build against the stable surface above — `Client`, `mountVirtualFS`, and the
`uri`/`wire`/`crdt` re-exports from `index.ts`.

## SDK vs mount: the same core, two interfaces

| | Mount (native) | SDK |
|---|---|---|
| Interface | The filesystem — `ls`, `cat`, editor save, `mv`, `rm` | Explicit API calls in your program |
| Who drives the barrier | The kernel, at `fsync`/`close` (+ flush-delay) | You, by calling `flush()` / `Commit()` |
| Cognitive overhead | Near-zero — nothing new to learn | Medium — an API to code against |
| Needs an OS mount facility | **Yes** (FUSE/macFUSE/NFS/WebDAV) | No — runs anywhere your program runs |
| Conflict handling | Sidecar file (or `ESTALE`) — automatic | You choose per policy and handle the result |
| Same guarantees | never-stale-negative cache, no-silent-loss, bulk guard, retained history | Same substrate; you implement the retry/guard loops the mount automates |

Use the mount when you want the substrate to *disappear* behind ordinary files; use
the SDK when you are writing a program that needs typed, explicit control (or when
you cannot mount).

## The skills, and which mode each supports

ACP ships skills that are the playbooks for each mode:

| Skill | Audience | Modes it supports |
|---|---|---|
| **acp-client** | An agent using ACP to collaborate | The client's-eye view of all modes: `acp push`/`pull` (CLI file-sync), `acp crdt sync` (live co-edit), mailbox/leases/event-log, and the mount. |
| **acp-go-sdk** | Developers writing Go programs against ACP | Using the public Go SDK (`sdk/go/pkg/client`) to drive the substrate explicitly. |
| **acp-operations** | Operators | Running `coordd`: TLS, tokens/roles, GC, backups, monitoring — the daemon every mode talks to. |
| **acp-cluster** | Operators | High-availability (Raft) `coordd` — orthogonal to the client mode. |

There is no separate MCP or mount skill today; MCP is the `acp-mcp` bridge (the same
verbs as the CLI, exposed as model tools) and the mount is documented here and in
`acp-client`.

## How to pick a mode

Answer these in order:

1. **Can you (or the agent) install and use an OS mount facility on this host?**
   - **Yes, and the agent works in a filesystem** → use the **mount**. It is the
     lowest-friction mode: existing tools work, nothing new to learn, changes from
     peers appear as files. Best default for an agent that has a shell and a
     filesystem.
   - **No** (locked-down host, no FUSE/macFUSE, no admin) → you cannot mount; pick
     from the modes below. Nothing is lost — they reach the identical space.

2. **Are you writing a program, or driving interactively/via a model?**
   - **A program that needs typed, explicit control** → use the **SDK** (Go
     `pkg/client` or TS `@ab0t/acp`). Use `mountVirtualFS` (TS) if you want
     filesystem-shaped calls without an OS mount.
   - **A model that should call tools** → use **MCP** (`acp-mcp`): the same verbs as
     the CLI, exposed as tool calls in the model's context.
   - **A shell, a script, or a human at a terminal** → use the **CLI** (`acp push`,
     `acp pull`, `acp fs`, `acp send`, `acp history`).

3. **What is the collaboration shape?** (orthogonal to the above — applies in every
   mode)
   - **Disjoint work** (each agent owns a subtree) → any mode; no coordination
     needed, disjoint paths never conflict.
   - **Same file, two writers, reconcile later** → CAS mode; the mount's sidecar (or
     the SDK's conflict result) keeps both versions.
   - **Same file, live co-authoring, auto-merge** → realtime mode (a space flipped
     with `acp fs mode realtime`); the mount mounts the realtime backend, or drive
     the CRDT surface via the SDK / `acp crdt`.

The modes compose: the same space can be mounted by one agent, driven by `acp push`
from CI, and read through the SDK by a third — simultaneously, over one daemon.

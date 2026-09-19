# When to mount, the limits, and the bigger picture

*Part 3 of a three-part series on ACP's native mount.
[Part 1](./01_the-shared-folder-for-your-agents.md) built the mental model;
[Part 2](./02_how-the-mount-works.md) opened up the mechanism. This part is about the
the real trade-offs, and what the same daemon does beyond files.*

---

Part 2 made the case for the mount by showing how it works. This part makes the
opposite case where it applies, because a surface you understand includes knowing
when to reach for a different one. The mount is one of four ways to touch an ACP
space; sometimes it is the wrong one, and the substrate loses nothing when you pick
another.

## What the mount needs, and what it costs

The mount's defining strength — it *is* a folder — comes from a real dependency: it is
the only surface that needs an **OS mount facility**. On Linux that is FUSE; on macOS,
macFUSE or a loopback NFS server; on Windows, a loopback WebDAV server on a drive
letter. That dependency has three real costs.

**A setup and permission step.** FUSE has to be present (`apt install fuse3` on most
Linux boxes; a user-approved system extension on macOS). On a bare machine this is one
package. In a container it is more, and worth spelling out.

A container cannot mount FUSE with default Docker settings, because `mount(2)` is a
privileged syscall the default sandbox blocks. An agent container that mounts its
workspace therefore needs three grants:

```yaml
# docker-compose.yml — on the AGENT services only, never on coordd
devices:      ["/dev/fuse:/dev/fuse"]     # expose the host's FUSE control device
cap_add:      ["SYS_ADMIN"]               # FUSE mounting needs CAP_SYS_ADMIN
security_opt: ["apparmor:unconfined"]     # the default AppArmor profile denies mount(2)
```

These are real privileges. Grant them to the agent nodes and **never to `coordd`** —
the daemon mounts nothing and needs none of it, which is exactly why the substrate
stays clean while only the edges take on privilege. This is the true price of a native
in-container mount, and it is right to state it plainly rather than bury it.

**A whole-file write model.** The mount treats a file as a unit: an edit is a
read-modify-write of the whole file, committed as one content-addressed blob. That is
what keeps the commit model a clean one-commit barrier (Part 2), but it has an edge.
Very large files (past a configurable cap, 256 MiB by default) are read-only through
the mount — they still stream for reads, but a write returns an error rather than
buffering a quarter-gigabyte in memory. If your workload is appending to multi-gigabyte
logs through the filesystem, the mount is not the tool.

**Two POSIX corners.** Because of the correctness-first read path, memory-mapping a
file as `MAP_SHARED` is not supported (`MAP_PRIVATE`, which executables and `git` use,
works). And POSIX **byte-range advisory locks** taken on the mount are local to that
one mount — they are not a coordination mechanism *across* the shared space. If two
agents need mutual exclusion on a resource, the substrate has a purpose-built
primitive for it — a lease — and that is the right tool, not `flock`.

**A locked-down host may not allow any of it.** Some environments forbid `/dev/fuse`
or `SYS_ADMIN` outright. There is a built-in escape — the same `acp mount` command
with `--backend=nfs` or `--backend=webdav` serves the identical View over a loopback
that mounts through the OS's own client, no FUSE required — but where even that is not
possible, you are not stuck. You reach for one of the other three surfaces, which
need no mount facility at all.

## The contrast: explicit surfaces

The CLI, MCP, and SDK share a property the mount does not have: they are **explicit**.
You name the operation — `push`, `pull`, `commit`, `send`, `acquire a lease` — and the
coordination is visible in your command history, your code, or the model's tool-call
log. That explicitness is a feature in three situations.

**Auditability.** When you need a record of exactly which operations touched the space
and when, an explicit verb log is that record by construction. The mount deliberately
hides coordination; the verb surfaces deliberately expose it.

**Portability.** The explicit surfaces run anywhere the binary or your program runs,
with no OS privilege. A locked-down CI runner, a serverless function, a restricted
PaaS — all can `acp push`, call the SDK, or serve MCP tools, none of which they could
mount on.

**Fine-grained control for a reasoning model.** This is the one most specific to
agents. When you *want* a language model to reason about coordination as part of its
task — "take a lease on `build:main`, then edit, then hand off to the reviewer with a
message" — the MCP surface puts each of those as a distinct, inspectable tool call in
the model's context. The model decides, step by step, and you can see it decide. The
mount does the opposite: it removes coordination from the model's reasoning entirely.
Both are valid; they are just aimed at different jobs.

The mount, then, is best when you want **existing tools and existing agents to just
operate on shared files** — a coding agent that already thinks in `open/grep/edit/save`,
a build, a person in an editor, a shell script, `rsync`. It is the lowest-friction path
when the participant has a filesystem and you can install the mount facility. When you
cannot, or when you want coordination to be explicit and audited, the verb surfaces
give you the identical space with a visible interface.

## Pick a mode

A short decision path, in order:

1. **Can this host install and use a mount facility, and does the participant already
   work in a filesystem?**
   → **Mount.** Lowest friction; existing tools work; a peer's changes arrive as files.
   The best default for a coding agent with a shell.

2. **No mount facility (locked-down host, no FUSE, no admin)?**
   → One of the surfaces below. Nothing is lost — they reach the identical space.

3. **Writing a program that needs typed, explicit control?**
   → **SDK** (Go or TypeScript). The TypeScript SDK also offers an in-process
   filesystem-shaped API for programs that want file semantics without an OS mount.

4. **Want a model to reason about coordination as first-class actions?**
   → **MCP** — the verbs as inspectable tool calls in the model's context.

5. **A shell, a script, or a human at a terminal?**
   → **CLI** — `acp push`, `acp pull`, `acp send`, `acp history`.

And a shape that cuts across all of them: if each participant owns its **own subtree**,
there is nothing to coordinate at all — disjoint paths never conflict, in any surface.
The modes also compose. The same space can be **mounted** by one agent, driven by
`acp push` from a **CI job**, and read through the **SDK** by a third, simultaneously,
over one daemon.

## The bigger picture: one substrate, many surfaces

Everything so far has been about files, because the mount is about files. But the files
are one primitive on a substrate that carries several, and the same `coordd` you are
already running exposes all of them over the same wire. In passing, so you know what is
under your feet:

- **A total-order event log.** The ordered spine from Part 1 is directly usable: append
  a fact, tail the stream live from any sequence number, and every client derives the
  same shared history. It is the substrate's audit trail and its coordination bus.

- **Leases — mutual exclusion across machines.** A lease is a TTL-bounded claim on a
  named resource, carrying a **fencing token** so a stalled holder whose lease expired
  cannot come back and clobber the new owner. This is the "who is editing this" answer
  that a filesystem lock cannot give across hosts.

- **A mailbox — the comms line.** Directed messages to a named agent, durable, with
  threads. When a participant needs to hand something to a *specific* other participant
  rather than announce it, the mailbox is the channel.

- **Presence and awareness.** A durable-ish roster of who is connected (`beat` / `who`),
  and a lossy, TTL'd **awareness** channel for ephemeral hints — "what I'm working on
  right now," a cursor position. Awareness is explicitly a hint, never correctness; you
  never build a guarantee on it.

- **CRDT documents for live co-editing.** Underneath the mount's realtime mode is a
  conflict-free replicated data type: many agents edit the same text (or structured
  JSON) document at once and it **converges** with no locks and no lost updates. It is
  what lets a directory tree behave like a live collaborative drive — concurrent
  appends from two agents both survive — and it is available directly through the SDK
  and CLI as well as through the mount.

- **Capability links.** You can mint a short-lived, signed, read-only link to a
  resource and hand it to someone who has no token of their own. The credential never
  travels in the link; the signature and its expiry are the grant. It is how you share
  one blob or one view without provisioning an account.

- **Wiki-style sub-linking between spaces.** Every resource has a stable address of the
  form `acp://host/space/path`. That address is not just how you mount or connect — it
  is a **link you can write into a file**. A document in one space can reference a
  subtree of another with an `acp://…` address, and it resolves the way a hyperlink
  does, across spaces and subtrees rather than within a single tree. The shared
  filesystem gains a cross-reference layer: notes that point at the artifacts they
  describe, an index space that links out to the working spaces it tracks, one address
  that names, connects, and shares.

Here is the whole picture in one frame:

```
                    ┌──────────────────────────────────────────┐
   surfaces  ─────► │  mount  ·  CLI  ·  MCP tools  ·  SDK       │
                    └───────────────────┬──────────────────────┘
                                        │  acp/1 wire
                    ┌───────────────────▼──────────────────────┐
   substrate ─────► │  coordd                                    │
                    │  shared files (blobs + versioned manifest) │
                    │  event log · leases · mailbox              │
                    │  presence/awareness · CRDT docs            │
                    │  capability links · acp:// addressing      │
                    └────────────────────────────────────────────┘
```

That is the design in one line: **one substrate, many surfaces.** You run a single
daemon. You touch it through whichever surface fits the participant and the host — the
folder when you want the coordination to disappear, an explicit verb when you want it
in plain sight. The primitives underneath do not change; only the interface does. The
mount is the surface that asks the least of you, and this series has been, at bottom, an
argument that it can ask that little precisely *because* of how carefully the substrate
underneath it keeps its promises.

If you are starting from zero, start with the mount on a Linux box: the copy-paste
mounting walkthrough gets you a shared folder in about a minute, and `acp mount --help`
lists every option once you want to tune one. Everything else is one address away.

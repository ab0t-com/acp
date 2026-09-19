# A shared folder for your agents: the idea behind Mountable ACP

*Part 1 of a three-part series on ACP's native mount. Part 2 opens up the
mechanism; [Part 3](./03_when-to-mount-and-the-bigger-picture.md) covers the
trade-offs and the wider substrate.*

---

## The problem: files, and everyone touching them

Put two agents to work on the same codebase and you hit the oldest problem in
distributed systems, wearing new clothes.

Agent A, on one machine, is writing `src/app.py`. Agent B, on another, is writing
`README.md` and needs to see A's latest tree to do its job. A third process — a CI
job, a human in an editor, a fourth agent — occasionally touches the same files.
Nobody is coordinating through shared memory, because there is no shared memory:
they are separate processes, often on separate hosts.

The usual answers are all some flavor of "make each participant learn a protocol."
Poll a git remote. Call a sync API. Push to a bucket and pull from it. Each of these
works, and each adds a step the participant has to *remember to do* — fetch before
you read, publish after you write, resolve a conflict when the push is rejected.

For a human that step is a habit. For a coding agent it is worse than a habit: it is
tokens spent reasoning about coordination instead of the task, and a thing that can
be forgotten under load. An agent that thinks in files — open, grep, edit, save,
build — now has to think in *fetch, edit, publish, reconcile*. That is real,
recurring friction, and it competes directly with the work.

The question this series answers is narrow and practical:

> How do you give every participant — an agent, a script, or a person — a shared
> place to work on files, such that they use the tools they already have and never
> learn a coordination protocol at all?

To answer it we first need the thing underneath the answer.

## The substrate: one daemon, three primitives, one wire

ACP — the Agent Coordination Protocol — is a self-hosted coordination substrate.
"Self-hosted" is the important word: you run it yourself, as one process, and it
belongs to you. That process is a daemon called **`coordd`**. Everything else in
ACP is a client of it.

```
  agent A            agent B            a CI job           a person
     │                  │                  │                  │
     └───────┬──────────┴────────┬─────────┴─────────┬────────┘
             │   acp/1 wire (TLS + pinned cert + bearer token)
        ┌────▼─────┐
        │  coordd  │   the one daemon you run — the substrate
        └──────────┘
```

`coordd` gives its clients three primitives. You can build a surprising amount on
just these three.

**A shared filesystem, as content-addressed blobs plus a versioned manifest.** File
*contents* are stored as **blobs** addressed by the hash of their bytes — identical
bytes are the same blob, stored once, and a hash can never name the wrong content. A
**manifest** is the map from paths to the hashes at those paths, and it carries a
**version number**. You change the tree by committing a new manifest against the
version you read, a compare-and-swap: if someone else committed since you read, your
commit is rejected and you rebase and retry. That single rule — commit against the
version you saw — is what stops two writers from silently clobbering each other.

**A total-order event log.** Every meaningful thing that happens gets appended to a
durable log, and the daemon stamps each entry with a monotonically increasing
sequence number. That sequence *is* a total order: every client sees the same events
in the same order, and can resume from the last number it processed. This is the
spine that lets independent clients derive a consistent shared view of what is going
on — including, as we will see, "a file just changed."

**A comms line.** A directed **mailbox** (send a message to a named agent; read your
inbox) rides alongside the log, for the times a participant needs to hand something
to a specific other participant rather than announce it to everyone.

All of this travels over one frozen protocol, the **`acp/1` wire**: HTTPS to a
single endpoint, secured by a pinned self-signed certificate and a bearer token, with
the caller's identity derived from the token server-side so it cannot be forged.
"Frozen" means the wire does not change out from under you — every client, whatever
its version, speaks the same `acp/1`, so a new client and an old daemon interoperate.
No VPN, no message broker, no cloud account. One port, one daemon, your machine.

That is the whole substrate. Now: how do you *touch* it?

## Four surfaces over one substrate

ACP exposes the same shared filesystem and comms line four different ways. They are
not alternatives to each other in the sense that you must pick one forever — the same
space can be used through all four at once. They differ in **how much the participant
has to learn**, and in **where they can run**.

| Surface | How you use it | What you learn | Where it runs |
|---|---|---|---|
| **Mount** | Just files — `ls`, `cat`, an editor's save, `mv`, `rm` | Nothing new | Needs an OS mount facility |
| **CLI** (`acp …`) | Explicit verbs: `acp push`, `acp pull`, `acp send` | A verb set | Anywhere the binary runs |
| **MCP** (`acp-mcp`) | Tool calls a model invokes | A tool schema | Anywhere the bridge runs |
| **SDK** (Go / TypeScript) | API calls in your own program | An API surface | Anywhere your program runs |

The **CLI** turns the primitives into shell verbs. You `acp pull` to get the latest
tree, edit, and `acp push` to share your changes; `acp send` drops a message in a
teammate's mailbox; `acp watch` tails the event log. It is explicit and scriptable
and runs anywhere the `acp` binary runs, with no special privileges.

The **MCP** surface is the same verbs again, exposed as tools a language model can
call directly through the Model Context Protocol. When you want the *model itself* to
reason about coordination — "acquire a lease on this file, then edit it, then message
the reviewer" — MCP puts those actions in the model's hands as first-class tools.

The **SDK** (Go and TypeScript) is the substrate as a typed library. You call
`Commit`, `PutBlob`, `Send`, `Follow` from your own program and own the control flow.
It is the right surface when you are *building* something on top of ACP.

These three share a family resemblance: each is an **explicit interface**. You name
the operation — push, pull, send, commit — and the coordination is visible in your
code or your command history. That explicitness is a genuine feature, and Part 3
returns to why you would sometimes want it.

But look again at the first row.

## The mount: the surface with nothing to learn

The **mount** presents an ACP space as an **ordinary directory** on disk. You mount
it once. From then on there is no verb, no API, no tool call. You `cd` into a folder,
`ls` what is there, `cat` a file, open one in your editor and save it, `mv` it, `rm`
it. When another participant changes a file in the same space — from their own mount,
from `acp push`, from the SDK, from anywhere — it simply *appears* in your folder,
with nothing to run.

There is no application and no window. Nothing to click. If you can use a filesystem,
you can use the mount — and so can every tool you already have: `grep`, `find`, a
compiler, `rsync`, a shell script, a person in Finder or Explorer.

Here is the entire interface:

```sh
mkdir -p /mnt/team                                   # an empty folder to mount at
acp mount acp://coord.example:8443/team/ /mnt/team   # mount the space (Ctrl-C unmounts)
#   -> mounted acp://coord.example:8443/team/ at /mnt/team (read-write, CAS mode, manifest v42)

cd /mnt/team
grep -rn TODO .                                      # your normal tools, unchanged
echo "- ship it" >> notes.md                         # editing a file commits it to the space
```

That address — `acp://coord.example:8443/team/` — names the daemon and the space, and
nothing else. In particular it **never carries your token**: the credential comes
from your environment or profile, exactly as it does for every other `acp` command.
The address is safe to write down, paste, and share.

Why does this surface matter more than a convenience? Because of what it removes. The
CLI, MCP, and SDK surfaces each ask the participant to hold a model of ACP in their
head — a verb set, a schema, an API. The mount asks for nothing. An agent set up with
a mount **does not think about ACP at all.** It spends its whole budget on the work,
because the coordination has disappeared underneath the one interface it already
knows: the filesystem.

That is the design goal in one sentence: **the substrate should vanish behind
ordinary files.** Everything in Part 2 — how writes commit, how a peer's edit
arrives, how a runaway delete is refused, how two writers avoid losing a byte — is in
service of making that disappearance *safe*, so that "just use files" is not a
comforting slogan but a literal, load-bearing truth.

## The hook: two agents, one folder

Strip the idea to its smallest real demonstration. Two agents, in two isolated
containers on the same host, each mount the same space as a plain folder:

```
  agent-a container                 agent-b container
  ┌───────────────────┐            ┌───────────────────┐
  │ /workspace/shared │            │ /workspace/shared │
  │   specs/plan.md   │            │   specs/plan.md   │
  │   src/app.py      │            │   src/app.py      │
  └─────────┬─────────┘            └─────────┬─────────┘
            │   acp/1 wire (TLS + token)     │
            └───────────────┬────────────────┘
                       ┌─────▼─────┐
                       │  coordd   │
                       └───────────┘
```

Agent A writes a file into its folder with a plain shell redirect — no ACP verb.
Agent B, which runs *nothing but a read*, finds the file on its own folder a moment
later. B edits a sibling file; A sees B's change appear the same way. Neither agent
ran a fetch or a publish. Neither knows there is a daemon, a protocol, or another
agent at all. They just used files, and collaborated.

This is not a thought experiment. It is a runnable example that ships in the ACP
repository, and it prints exactly that result. In [Part 2](./02_how-the-mount-works.md)
we take it apart: what happens at the instant you save, how B's mount knew to show
the new file, and the mechanisms that keep the whole thing correct even when two
agents reach for the same byte at the same time.

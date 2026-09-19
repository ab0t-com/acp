# ACP mount — documentation index

The **mount** makes an ACP space appear as an **ordinary folder**. Once it is
mounted, an agent, a script, or a person works with the tools they already have —
`ls`, `cat`, `cd`, `grep`, `find`, an editor's save, `cp`, `mv`, `rm`, a build —
and collaborates with the rest of the fleet without knowing there is a daemon, a
protocol, or another agent involved. There is **no app, no GUI, nothing visual**:
the shared space *is* a directory on disk.

The mount is one of several ways to use ACP. It is the **near-zero-cognitive-overhead**
one: an agent set up this way does not think about ACP at all — it just uses files.

> **Status:** the mount is built, `-race`-clean, and **cross-OS**: `acp mount`
> auto-selects the adapter per OS (Linux→FUSE, macOS→FUSE-or-NFS, Windows→WebDAV).
> **Linux (FUSE) is fully tested on real kernels**; macOS and Windows
> are built + wired and proven on the Linux side, with real-device mount verification
> still pending.

## The modes, and what each costs the agent

ACP exposes the same shared filesystem + comms substrate four ways. Pick per task
and per host — they are not exclusive, and the same space can be used through all
of them at once.

| Mode | How the agent uses it | Cognitive overhead | Runs where |
|---|---|---|---|
| **Mount / native** | Just files (`ls`, `cat`, editor, `mv`, `rm`) — nothing new to learn | **Near-zero** — the space *is* the working directory | Needs an OS mount facility (FUSE/macFUSE/NFS/WebDAV): setup + permission cost, platform limits |
| **CLI** (`acp …`) | Explicit verbs: `acp push`, `acp pull`, `acp fs`, `acp history`, `acp send` | Low — a verb set to learn | Anywhere the `acp` binary runs; no special OS privilege |
| **MCP** (`acp-mcp`) | Tool calls the model invokes (push/pull/send/…) | Low–medium — a tool schema in context | Anywhere the MCP bridge runs |
| **SDK** (Go `pkg/client`, TS `@ab0t/acp`) | Explicit API calls in your own program | Medium — an API surface to code against | Anywhere your program runs; no special OS privilege |

**The trade-off.** The mount is the only mode that needs an OS mount
facility (Linux FUSE, macOS macFUSE, or the loopback NFS/WebDAV paths). That
brings a setup step, a permission cost, and platform limits (see the platform
support tables in the mount guides below). The CLI, MCP, and SDK modes run anywhere with
none of that — they trade the "it just works, no client to learn" feel for
portability. When an agent has a filesystem and you can install the mount
facility, the mount is the lowest-friction mode; when you cannot, the other three
give the same substrate with an explicit interface.

## The documents

- **[mounting-walkthrough.md](mounting-walkthrough.md)** — the dead-simple,
  copy-paste "how to mount on Linux / macOS / Windows" a person or an agent can
  follow top to bottom: prereqs per OS, the exact `acp mount` command, what
  success looks like, how to unmount, one-line troubleshooting. Platform status
  (Linux fully tested; macOS/Windows built + wired, real-device pending).
- **[mount-for-users.md](mount-for-users.md)** — for agent-users and people. What
  the native mount feels like, a 60-second quickstart with real commands, the
  safety you get for free, when to choose the mount over the other modes, platform
  support, and troubleshooting the common "it didn't mount" cases.
- **[sdk-and-modes.md](sdk-and-modes.md)** — how to use ACP from the Go and TS
  SDKs, how the SDK relates to the mount (explicit API vs implicit files), which
  ACP skill supports each mode, and a decision guide for picking a mode.

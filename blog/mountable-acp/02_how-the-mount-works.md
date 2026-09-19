# How the mount actually works

*Part 2 of a three-part series on ACP's native mount.
[Part 1](./01_the-shared-folder-for-your-agents.md) built the mental model;
[Part 3](./03_when-to-mount-and-the-bigger-picture.md) covers the trade-offs and the
wider substrate.*

---

In Part 1 we ended with two agents in two containers, each seeing the other's files
appear in a plain folder with no command run. This part opens that up. We will follow
a byte from an editor's save into the shared space and back out to a peer, and along
the way meet the handful of rules that make "just use files" safe enough to trust
without re-checking.

## One core, thin adapters

An operating system does not know what ACP is. It knows how to talk to a filesystem
through a mount facility — on Linux, **FUSE** (Filesystem in Userspace, the kernel
interface that lets an ordinary program answer filesystem calls). So the mount is a
program that speaks FUSE on one side and the `acp/1` wire on the other.

The important structural decision is that **all the filesystem behavior lives in one
place**, a component called the **View**, and the OS-facing pieces are thin shims
around it:

```
     kernel / OS filesystem client
              │
   ┌──────────┴───────────┐
   │  OS adapter          │   maps kernel calls to View calls;
   │  (FUSE | NFS | WebDAV)│   maps View errors to OS error codes
   └──────────┬───────────┘
              │  Stat · Readdir · Open · Read · write verbs · Flush
   ┌──────────┴───────────┐
   │       View (core)     │   the whole filesystem semantics:
   │                       │   snapshot + overlay, the write barrier,
   │                       │   conflicts, the bulk guard, live-follow
   └──────────┬───────────┘
              │  content-addressed blobs + a versioned manifest
   ┌──────────┴───────────┐
   │   ACP client (acp/1)  │
   └──────────────────────┘
              │  HTTPS (pinned cert, bearer token)
           coordd
```

The View is deliberately kernel-free: it answers `Stat`, `Readdir`, `Open`, the write
verbs, and the barrier from an in-memory snapshot of the space merged with the mount's
own not-yet-committed changes — with no FUSE anywhere in it. That is what lets every
platform share *identical* behavior, because the rules that matter are written once.

You never choose an adapter. **`acp mount <space> <dir>` is one command on every
operating system**, and it selects the adapter for you: Linux uses native FUSE; macOS
uses FUSE if macFUSE is installed and otherwise falls back to a loopback NFS server
that needs nothing installed; Windows uses a loopback WebDAV server mapped to a drive
letter. A `--backend` flag overrides the choice for the rare case you know better.

A word on status, plainly: **Linux/FUSE is the fully tested path**, proven on real
kernels. The macOS and Windows adapters are built, wired into that same one command,
and exercised through their own protocol clients on Linux; the on-device kernel-mount
step on a real Mac or Windows box is the remaining verification. Everything below is
about the Linux/FUSE path, where the semantics are the same across adapters because
they come from the shared View.

## The read path, and one rule about caching

When you `cat notes.md`, the adapter asks the View for the file. The View serves from
a **snapshot** — a recent fetch of the manifest, projected into a directory tree. A
snapshot is reused for a short window (about one second by default), so a burst of
lookups in a directory is one fetch, not a hundred. File *contents* come from blobs,
which are immutable and content-addressed, so once fetched they can be cached by their
hash without any risk of staleness — the hash names the bytes exactly.

Caching a distributed filesystem is where correctness usually goes to die, so the
mount holds one rule above the others:

> **A cache may be stale. It may never lie. And a negative is never cached.**

Unpack that. A **positive** answer — "this path exists, here are its attributes" — is
allowed to come from a snapshot up to a second old. That is *bounded staleness*: you
might see a version of the tree that is a moment behind, which is visible, expected,
and never wrong about existence.

A **negative** answer — "no such file" — is different, and this is the subtle part.
Kernels cache negative lookups, and editors constantly create a file and then
immediately stat it. If the mount let the kernel cache "no such file" and served that
stale, a program that just created a file could be told its own file does not exist.
So the mount **never** serves a "not found" from an old snapshot: on a miss it
re-fetches the manifest first, and only then answers "not found." On the FUSE side it
also tells the kernel to cache negatives for zero seconds. A miss is always confirmed
against the current tree before it is reported.

There is a matching rule for reads of file bytes. Each open file handle is served with
the kernel page cache **bypassed** (FUSE's direct-I/O mode). This costs the page
cache — repeat reads come from the mount's own in-memory blob cache instead — but it
buys correctness: if a file is rewritten upstream to a different length, a page-cached
reader can be handed the *old* length and read back truncated, wrong bytes, silently,
until the cache expires. Bypassing it means a fresh open always sees the full, current
content. (One consequence: memory-mapping `MAP_SHARED` is unsupported; `MAP_PRIVATE`,
which executables and `git` use, works — so the cost to a normal toolchain is ~zero.)

The through-line: an agent reads across the mount and *trusts the bytes without
re-verifying*. That trust is the whole value, so every path that could hand back a
stale or invented answer is closed by construction.

## The write path: one commit at the barrier

Writing through the mount feels exactly like writing to any folder, but underneath,
your changes are **batched and committed together** so an editor's save lands as one
clean version instead of a dozen partial ones.

Here is the mechanism. When you write bytes, the mount buffers the whole file in an
overlay — the same overlay the read path consults, which is why a file you are
mid-writing is visible to your own reads immediately. The buffered changes reach the
daemon only through the **barrier**: one commit carrying every buffered change at once,
with new bytes uploaded as content-addressed blobs first and the manifest pointed at
them.

The barrier runs at two moments:

- **`fsync(2)`** — synchronously. The barrier's outcome *is* the return value of your
  `fsync`, so a program that syncs knows immediately whether its write landed.
- **`close(2)`** of a written file — after a short coalescing window (about 100 ms by
  default). That window is what folds an editor's "write to a temp file, rename,
  fsync" dance into one commit rather than several.

Metadata-only operations — `mv` (rename) and `rm` (delete) — carry no bytes, so they
ride along on the *next* barrier. In practice you never think about this. It matters
only when you want a change visible to others *this instant*, and then the answer is
one word: `sync`.

```sh
echo "- ship it" >> notes.md   # buffered; commits ~100 ms after close, or now on fsync
mv notes.md docs/notes.md      # metadata only — rides the next barrier
sync                           # force it: the move lands now
```

### Two writers, one file: nobody loses a byte

Because commits are compare-and-swap on the manifest version, two mounts can reach for
the same file at the same time. A plain filesystem resolves this by letting the last
writer win and throwing the other version away — a silent loss. The mount refuses to
do that.

The default policy is called **sidecar**. The first commit stands as the file. The
second writer's bytes land **right beside it**, intact, as a sibling:

```
notes.md                                          # the version that committed first
notes.md.conflict-bob-20260919T120000Z-a1b2c3     # the version that raced it — kept whole
```

Both byte sets are on disk. Nobody is blocked and nothing is lost; you (or a later
pass) open the `.conflict-…` sibling and reconcile at your leisure. Every such event
is also written to a plain journal you can read any time:

```sh
cat /mnt/team/.acp/conflicts.jsonl   # path, who, both hashes, byte counts — one line each
```

The sidecar name is generated from the writer's identity, a nanosecond timestamp, and
random bytes — never by consulting the tree — so it cannot accidentally collide with
or overwrite anything, visible or not.

If you would rather a racing write **fail loudly** than make a sidecar, mount with
`--conflict=error`: the losing write returns a "stale" error so your program knows to
re-read and retry, and it still holds your bytes rather than dropping them. Either way,
the guarantee is the same: never a silent loss. (Part 3 explains why the sidecar is the
default on every platform while the hard-error signal is a FUSE-only refinement.)

### A runaway delete is refused, not obeyed

The mistake everyone fears when handing an agent a real filesystem is the `rm -rf` on
the wrong path. The mount has a **bulk guard** for exactly this. Before it
acknowledges a delete that would push the batch over a threshold, it measures the
**net loss** the operation would cause and refuses the crossing operation outright,
leaving the space untouched:

```sh
rm -rf dataset/            # 24 files, past the default threshold
#   -> rm: cannot remove 'dataset/...': Operation not permitted
ls dataset/ | wc -l        # still 24 — nothing was deleted
```

"Net loss" is the key phrase. A delete counts only if it actually destroys content —
so a whole-directory **rename** measures zero (every file simply reappears at its new
path) and is never mistaken for a wipe. The refusal is also all-or-nothing: the guard
rolls back any sub-threshold deletes already buffered in the burst, so a later,
unrelated save can never quietly carry a partial mass-delete along with it.

When you genuinely mean it, mount with `--allow-bulk` and the mass change goes through
on purpose. Even then it is reversible, which is the next mechanism.

## Live-follow: changes arrive on the event

A peer's change becomes visible to your mount two ways. The baseline is that snapshot
refresh — about a second, no command run. The upgrade is **live-follow**: the mount
keeps a background stream open on the coordination event log, and the instant a
teammate commits, it marks the touched path stale and tells the kernel to drop its
cached entry, so your very next read re-fetches. That makes a teammate's edit to an
*already-cached* file appear well inside the refresh window rather than waiting for it.

The never-stale-negative rule from the read path still holds: a path flagged by an
event is re-fetched *before* it is served, and if that re-fetch fails, the read
returns an error rather than stale-bytes-dressed-as-fresh. A mount also never invalidates a write
it is in the middle of making. The current sequence number the follower has reached is
visible in the mount's status file, so you can see the follow position advancing.

## Undo: history and restore

Every version of every file is retained. A wrong overwrite or a deliberate
`--allow-bulk` delete is not the end of anything:

```sh
acp history                 # the version timeline
acp restore notes.md        # bring a file back — as a new forward commit
```

Restore does not rewind the world; it commits the old content *forward*, so it appears
through your live mount immediately and the timeline stays append-only. You can also
mount a read-only snapshot of a past version by adding `?v=<N>` to the address.

## The `.acp/` control directory

Every mount carries a small control folder, `.acp/`. It is reachable by name but is
**not** listed in an ordinary `ls` of the root — so `rsync`, `git`, and `find` walking
the tree never trip over it — and it holds three read-only files:

- **`status`** — a live JSON snapshot: whether the mount is connected, the space
  version it is on, anything you have written that has not committed yet, how many
  conflicts are held, the bulk-guard thresholds and current tally, and the follow
  position. If something looks stuck, this is the first place to look:

  ```sh
  cat /mnt/team/.acp/status      # or: acp-mount status /mnt/team
  ```

- **`conflicts.jsonl`** — the append-only conflict journal shown above.
- **`README`** — an always-present, self-describing help file. An agent that lands on
  an unfamiliar mount and `cat`s it learns, with no other context, that this is a live
  shared space edited with ordinary tools, how conflicts and mass deletes are handled,
  that history and undo exist, and how to unmount. It names no internals — the space is
  simply "shared files."

Everything in `.acp/` is the mount's own state, already within your token's scope. It
is a window into the mechanism, not a control panel — nothing in it is something you
have to configure.

## The whole thing, running

None of this is theoretical. The two-container demo from Part 1 ships as a runnable
example, and its output is exactly the mechanism above, observed:

```
### each container MOUNTS the shared space at /workspace/shared (native FUSE)
### agent-a writes a file INTO its mount — a plain shell redirect, no ACP verb
### agent-b does NOTHING but read — its OWN mount surfaces the file (CAS refresh, no verb)
### agent-b edits through ITS mount — adds code next to the spec (again, just files)
### agent-a does NOTHING but read — agent-b's code appears on ITS OWN mount
  PASS: agent-b's mount surfaced agent-a's spec (no verb)
  PASS: agent-a's mount surfaced agent-b's code (no verb)
  PASS: coordd's manifest holds specs/plan.md
  PASS: coordd's manifest holds src/app.py
  PASS: the two mounts converged byte-identically
=== RESULT: PASS (5 checks) — two containers shared a NATIVE filesystem via coordd ===
```

Agent A's write went through the barrier as one commit; agent B's mount learned of it
and re-fetched; the bulk guard, the conflict rule, and the retained history stood
underneath, unused because they were not needed — which is the point.

The mount is not the only way to touch this substrate, and not always the right one.
[Part 3](./03_when-to-mount-and-the-bigger-picture.md) covers when to mount, what the
mount cannot do, and what the same daemon does beyond files.

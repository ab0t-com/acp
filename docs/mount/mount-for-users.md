# The ACP mount — a shared folder for your agents

You mount an ACP space once. From then on it is **just a folder**. You `cd` into
it, `ls` what is there, `cat` a file, open one in your editor, save it, `mv` it,
`rm` it. Another agent — on another machine — edits a file in the same space, and
it **appears in your folder** with no command, no sync step, nothing to run. You
never learn a client, a verb set, or an API. The shared space *is* your working
directory.

There is no app and no window. Nothing to click. If you can use a filesystem, you
can use the mount — and so can every tool you already have (`grep`, `find`, a
compiler, `rsync`, a shell script, a person in Finder or Explorer).

That is the whole idea: **an agent set up with a mount spends its budget on the
work, not on the protocol.** It doesn't have to think about ACP at all.

---

## 60-second quickstart

You need three things: the daemon's address, your token, and its pinned
certificate — the same credentials every ACP tool uses, resolved from your profile
or the `ACP_*` environment. Then:

```sh
# 1. Make an empty directory to mount at.
mkdir -p /mnt/team

# 2. Mount the space. Runs in the foreground; Ctrl-C unmounts cleanly.
acp mount acp://coord.example:8443/team/ /mnt/team
#   -> mounted acp://coord.example:8443/team/ at /mnt/team (read-write, CAS mode, manifest v42) — Ctrl-C to unmount
```

`acp mount` resolves your profile (like every other `acp` command) and hands the
connection to the mount. **The address never carries your token** — the credential
comes from your environment.

Now, in another shell, just use files:

```sh
cd /mnt/team
ls -R                                  # everything in the space
grep -rn TODO .                        # your normal tools work
cat notes.md                           # read a file
echo "- ship it" >> notes.md           # edit a file — this commits to the space
mkdir docs && mv notes.md docs/        # reorganise
```

**See a peer's file appear on your own mount, with no verb.** Leave your mount up.
When a teammate (or another agent) writes a file into the same space — from their
mount, from `acp push`, from the SDK, from anywhere — it shows up in your folder:

```sh
# ... a teammate commits proj/REVIEW.md from their machine ...
ls /mnt/team/proj/                     # REVIEW.md is just there now
cat /mnt/team/proj/REVIEW.md           # read it — no sync, no command
```

You did not run anything to fetch it. That is the mount following the space live.

When you are done, unmount:

```sh
# Ctrl-C in the mount's shell, or from anywhere:
acp umount /mnt/team
```

> Prefer to keep working in the same shell? Add `--daemon` to background the mount:
> `acp mount --daemon acp://coord.example:8443/team/ /mnt/team` returns immediately,
> prints the mount's pid and a log path, and keeps running until you `acp umount` it.

---

## When your writes actually reach the space (the one thing worth knowing)

Writing through the mount feels exactly like writing to any folder. Under the hood
your changes are **batched and committed together** so an editor's save (write to a
temp file, rename, `fsync`) lands as **one** clean version, not a dozen partial
ones. In practice:

- **Editing bytes** (`echo >`, `>>`, an editor save) lands a moment after you
  `close` the file (a short coalescing window, ~100 ms by default). Calling
  `fsync` (or `sync`) lands it **immediately**.
- **Pure metadata moves** — `mv` (rename) and `rm` (delete) — carry no bytes, so
  they ride along on the **next** commit. If you want a move or delete to land
  *right now*, run `sync` after it.

You almost never have to think about this. It matters only when you want a change
to be visible to others *this instant* — then `sync`.

---

## The safety you get for free

The mount is built so an agent can act boldly, because the floor underneath is
safe. Nothing here is something you configure or remember — it is just on.

### A runaway delete is refused before it can happen

If a command would delete or overwrite a large fraction of the space at once — the
classic `rm -rf` the wrong thing — the mount **refuses it** and the space is left
**completely untouched**. The delete stops at the first file that would cross the
threshold; nothing partial leaks through, not even onto your next save.

```sh
rm -rf dataset/                        # 24 files, over the safety threshold
#   -> rm: cannot remove 'dataset/...': Operation not permitted
ls dataset/ | wc -l                    # still 24 — nothing was deleted
```

A whole-directory **rename** is *not* a delete (the files simply move), so moving a
big tree is never mistaken for wiping it.

When you genuinely mean it, mount with `--allow-bulk` and the mass change goes
through on purpose:

```sh
acp mount --allow-bulk acp://coord.example:8443/team/ /mnt/team
rm -rf /mnt/team/dataset/              # now it lands — you asked for it
```

Even then, it is reversible — every version of every file is retained, so
`acp history` + `acp restore` brings anything back as a new forward version.

### A same-file race never loses a byte

Two agents can write the same file at the same time. A plain filesystem would let
the last writer silently win and throw the other version away. The mount **never**
does that. By default, the first commit stands as the file, and the second writer's
version lands **right beside it** as a sibling you can just open:

```
notes.md                                 # the version that won
notes.md.conflict-bob-20260919T120000Z-a1b2c3   # the version that "lost" — kept intact
```

Both versions are on disk. Nobody is blocked, nothing is lost — you (or a later
pass) open the `.conflict-…` file and reconcile at your leisure. Every conflict is
also written to a plain log you can read any time:

```sh
cat /mnt/team/.acp/conflicts.jsonl     # path, who, both versions, byte counts
```

If you would rather a racing write **fail loudly** instead of making a sidecar,
mount with `--conflict=error`: the losing write returns an error (so your program
knows to re-read and retry) and holds your bytes — still never a silent loss.

### A status file that tells you the truth

Every mount has a small control folder, `.acp/`, with a live status file. It is not
listed in an ordinary `ls` (so `rsync`, `git`, and `find` never trip over it), but
you can always read it:

```sh
acp-mount status /mnt/team             # or: cat /mnt/team/.acp/status
```

It reports whether the mount is connected, the space version it is on, anything you
have written that has not committed yet, how many conflicts are held, and the
bulk-guard thresholds and current tally. If something looks stuck, this is the
first place to look.

### Live-follow — changes arrive on the event, not on a timer

A teammate's change becomes visible to your mount **the instant it lands**, not
after a poll interval. (On Linux the mount is notified of the event and refreshes
that file immediately; see the platform notes below for macOS.) A file that does
not exist is *never* remembered as missing — if a teammate creates it, your very
next look finds it.

---

## When to choose the mount over the other modes

ACP gives you the same shared space four ways. Use the mount when it fits, and one
of the others when it doesn't.

**Choose the mount when:**
- Your agent already works in a filesystem and you want zero new interface to
  learn — this is the lowest-friction option.
- You want existing tools (`grep`, a build, an editor, `rsync`, a person in
  Finder/Explorer) to operate on the shared space directly.
- You are running a swarm where each agent owns its own subtree — disjoint folders
  never collide, so there is nothing to coordinate.

**Choose the CLI (`acp push`/`pull`/`fs`), MCP, or SDK instead when:**
- You are on a locked-down host where you cannot install or use a mount facility
  (see the trade-off below) — these run anywhere with no OS privilege.
- You want explicit, scripted control over exactly when a file is pushed or pulled.
- You are building a program and want a typed API rather than filesystem calls
  (that is the SDK — see `sdk-and-modes.md`).

The modes are not exclusive. The same space can be mounted by one agent, driven by
`acp push` from a CI job, and read through the SDK by a third — all at once.

---

## Platform support and the setup trade-off

The mount is the one mode that needs an **OS mount facility**. That is its cost:
a setup step, a permission cost, and platform limits. The CLI, MCP, and SDK have
none of that. Here is the state per platform:

`acp mount <space> <mountpoint>` is **one command on every OS** — it auto-selects the
adapter for you (Linux → FUSE, macOS → FUSE if macFUSE is present else NFS-loopback,
Windows → WebDAV at a drive letter); `--backend=fuse|nfs|webdav` overrides.

| Platform | What it needs | State |
|---|---|---|
| **Linux** | `/dev/fuse` + `fusermount3` (`apt install fuse3` / `dnf install fuse3`) | **Primary, fully supported and tested on real kernels.** This is the flagship path. |
| **macOS (macFUSE)** | macFUSE (a user-installed system extension — `brew install --cask macfuse`, then approve it in System Settings › Privacy & Security) | Built + wired + auto-selected; live-follow is time-bounded rather than instant (macOS has no kernel-notify path). **Real-device mount verification pending.** |
| **macOS (no kext)** | The built-in NFS client (nothing to install) | Auto-fallback when macFUSE is absent (`--backend=nfs` forces it); loopback NFSv3, may prompt for `sudo`. Fully Linux-tested; **real-device mount verification pending.** |
| **Windows** | The built-in WebClient service (run once from an elevated shell), mounted at a drive letter (e.g. `Z:`) | Built + wired + auto-selected via WebDAV-loopback. Fully Linux-tested; **real-device mount verification pending.** |

The macOS/Windows adapters are **dispatched by `acp mount` today**; what
remains is running the kernel-mount step on a real Mac/Windows box. macOS/Windows
mount support is being finished; until then, prefer the SDK/CLI on those hosts if
you need a proven-on-device path.

On a host where you **cannot** get a mount facility, you are not stuck — use
`acp push`/`acp pull`, `acp fs`, or the SDK. They give you the identical shared
space with an explicit interface; there is just no folder to `cd` into.

`acp-mount --help` lists every option (cache sizes, the file-size cap, the commit
timing window, the per-request timeout, and more). The defaults are sensible; most
users never change them.

---

## Troubleshooting the common "it didn't mount"

**"mountpoint is not empty" / "is not a directory."** The mount refuses to shadow
an existing directory's contents (that is the classic trap where files seem to
vanish). Point it at an **empty** directory, and create it first if it doesn't
exist: `mkdir -p /mnt/team`.

**"need /dev/fuse and fusermount3."** The mount facility isn't installed. On Linux:
`apt install fuse3` (or `dnf install fuse3`). On macOS: install macFUSE and approve
its system extension. On a host where you can't install it, use the SDK/CLI instead.

**"daemon … unreachable" or a token error, right away.** The mount checks the
daemon, your token, and the space mode **before** it touches the mountpoint, so a
bad connection fails cleanly at the command — never as a hung, half-mounted folder.
Check `ACP_SERVER`, `ACP_TOKEN`, and `ACP_CERT` (or your profile).

**"stale mount (transport endpoint is not connected)."** A previous mount was
hard-killed and left a wedged folder. The mount usually clears this itself on the
next start; if not, run `acp umount /mnt/team` and remount. For a folder that is
*truly* stuck (even `acp umount` hangs), the mount's own `--help` and status output
print the last-resort kernel-abort recipe.

**A write "succeeded" but the file isn't in the space yet.** A metadata move or
delete rides the next commit, and a byte edit commits a fraction of a second after
you close the file. Run `sync` to force it now, and read `.acp/status` to see
anything still pending.

**A file has a `.conflict-…` sibling you didn't create.** That is the no-silent-loss
guarantee working: someone else wrote the same file at the same time, and your
version was kept beside theirs. Open it, reconcile, delete the sidecar. The full
record is in `.acp/conflicts.jsonl`.

**You need to undo a delete or a bad edit.** Every version is retained.
`acp history` shows the timeline; `acp restore` brings a file back as a new forward
version, visible through your live mount immediately.

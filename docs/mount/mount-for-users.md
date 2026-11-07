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

`acp mount` picks the profile whose server is the address's host and port (like
every other `acp` command given an `acp://` address) and hands the connection to the
mount. **The address never carries your token**, and your token only ever goes to the
host it belongs to: if no profile is set up for that host, `acp mount` stops and says
how to add one. (Running `acp-mount` directly with `ACP_TOKEN`, also set
`ACP_SERVER` to the same host, or pass `--token` on the command line.)

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
> A mount point reached through a symlinked folder is fine. If the background mount
> does not report itself within 15 s, it is asked to stop (it saves what it buffered
> first, and keeps anything it could not save beside the mount) and the log is shown;
> it is never killed. On macOS `--daemon` is not available in this release: run the
> mount in the foreground, or under launchd.

---

## When your writes actually reach the space (the one thing worth knowing)

Writing through the mount feels exactly like writing to any folder. Under the hood
your changes are **batched and committed together** so an editor's save (write to a
temp file, rename, `fsync`) lands as **one** clean version, not a dozen partial
ones. In practice:

- **Editing bytes** (`echo >`, `>>`, an editor save) lands a moment after you
  `close` the file (a short coalescing window, ~100 ms by default). Calling
  `fsync` (or `sync`) lands it **immediately**.
- **Moves and deletes** (`mv`, `rm`, `rmdir`, `mkdir`) land the same way, a moment
  after the last one. A burst of them, like `rm` over a folder, is one commit.
  If you want one to land *right now*, run `sync` after it.

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

The same protection covers a live text document: saving one that removes almost
all of it (more than 90 % of a document of 256+ characters — what an editor that
crashed after emptying the file would send) is refused with `Operation not
permitted` and kept unsent, because a text document has no undo. Save the file
again with its text and that save goes through instead.

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

On a live (realtime) space the sibling is simply `notes.md.conflict` (then
`notes.md.conflict.2`, and so on, if that name is taken). The same applies when an
editor saves through a temporary file and renames it over `notes.md` just as a
teammate writes it: theirs keeps the name and yours becomes `notes.md.conflict`.
If the same version of yours conflicts again, the existing sidecar is reused rather
than copied.

On a live space, if `notes.md` is a shared live text file, a save never overwrites it,
in place or through a temp file and a rename. What changed since you read the file is
written INTO the shared document, and whatever teammates typed meanwhile stays. The
mount can't tell which program read a file, so it keeps your last few reads and your
last landed save, compares your save with one of them, and deletes only text that all
of them held: a read by another program (an indexer, `git status`, `grep`) never turns
your next autosave into a delete of what a teammate wrote since. So a teammate's line
that your save only leaves out stays, and the mount's log says so. To delete it, read
the file, save once with the line kept, then save again without it (or use
`acp crdt sync`; a mount started with `--allow-bulk` always compares with your newest
read, but it also turns off the bulk-delete guards, so use it for that job only). If you
save without having read the file through this mount, your text is added and nothing is
deleted: a line you changed is there in both forms, and the mount's log says so. Tidy
that up by reading the file and saving again. If a teammate deletes the document while
your save is on its way, `fsync` fails with `EIO` and nothing is lost: the file still
shows your save, and the next `fsync` or write-back puts your whole text into whatever
document is at that name now, in front of anything a teammate started there. If the
document was restored from the trash by then, your save lands on it as an ordinary
edit. If it goes after a save of yours landed and you have not read the file since,
your next save starts it again, or joins what a teammate started there, as a save with
no read does.

Editors that move the original away (vim, by default on many systems: `mv live.md
live.md~`, write a new `live.md`, delete the backup) keep co-editing on a live document
this mount has shown you (you read it, or saved it): a text file written at the name
within 2 seconds of the move that keeps at least half of the lines you saw goes into
the document, and the old text stays as a plain file at the backup name, unless you
removed it. Anything else is a new file, exactly the bytes written, and the move stands
(the document is at the backup name, or in the trash, restorable, once the backup is
deleted): a file that keeps less than half, one that is not text, or a save to a
document this mount never showed (a script's `mv report.md report.old; gen > report.md`). In vim, `set backupcopy=yes` avoids the
move altogether. A file that is not text (it holds a NUL byte, or is not valid UTF-8)
still replaces the document as a plain file; the document goes to the trash with its
text, so you can restore it.

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

**Reads fail with an I/O error for a while, and the log says the server's event log is behind, or not yet
known.** The mount saw the server's log go back (a cluster node catching up, or a server restored to an earlier
point), or the server stopped saying which log it serves (a cluster election), or named a new one that is being
checked. Rather than show you files the server may no longer have, reads wait until it is clear; then they work
again, or, after a restore, the mount reads everything again. Your saves are not affected.

**"stale mount (transport endpoint is not connected)."** A previous mount was
hard-killed and left a wedged folder. The mount usually clears this itself on the
next start; if not, run `acp umount /mnt/team` and remount. For a folder that is
*truly* stuck (even `acp umount` hangs), the mount's own `--help` and status output
print the last-resort kernel-abort recipe.

**"served by a live acp-mount" / "the mount lock folder … is not this user's own".** One
mount point has one lock, in `/tmp/acp-mount-<uid>/`, keyed by its parent folder and its name
as that folder holds it: a second start at the same folder, by any path that reaches that name
(a link, `..`, another case or Unicode spelling the folder takes for it) or from any session, is
refused while the first runs, so it can never abort a live mount. On macOS names are compared
case- and Unicode-folded, so two folders whose names differ only in case (on a case-sensitive
volume) share one lock: the second start is refused, nothing is lost. On Linux, under a parent
you can enter but not list, on a case-insensitive disk, two spellings of one name take two
locks: start the mount by one spelling. If the folder was made by someone else (or its mode changed), the start says so:
remove it, then start again. The lock holds as long as that folder and its files stay, so:
- a `/tmp` cleaner that deletes files it does not know is held (macOS `periodic` clean-tmps,
  an old tmpwatch; current systemd-tmpfiles leaves locked files alone) can let a second start
  in: keep `/tmp/acp-mount-*` out of its list;
- `sudo acp-mount` runs as root, with root's own lock folder: mount as the user you unmount
  as;
- a service with systemd `PrivateTmp=yes` has its own `/tmp` and its own mounts.

Where `/tmp` can't be used (a read-only root, no `/tmp`), the mount can't take its lock and
does not start; there is no override, because a lock place the environment picks is what
let two starts take two locks. Give the host a writable `/tmp`.

**The mount went away with writes unsaved.** It keeps their bytes in
`<mount point>.unsaved-<time>-<pid>/` beside the mount point (or, if that folder can't take
them, in `/tmp/acp-mount-<uid>/`, which a restart may clear), and the exit line names it.
Mount again and copy them back in.

**A write "succeeded" but the file isn't in the space yet.** A move, a delete or a
byte edit commits a fraction of a second after the last change. If it is still not
there, the commit was refused or the server is out of reach. Run `sync` to see the
error, and read `.acp/status` to see anything still pending.

**A file has a `.conflict-…` sibling you didn't create.** That is the no-silent-loss
guarantee working: someone else wrote the same file at the same time, and your
version was kept beside theirs. Open it, reconcile, delete the sidecar. The full
record is in `.acp/conflicts.jsonl`.

**You need to undo a delete or a bad edit.** Every version is retained.
`acp history` shows the timeline; `acp restore` brings a file back as a new forward
version, visible through your live mount immediately.

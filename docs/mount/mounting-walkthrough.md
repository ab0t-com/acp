# How to mount an ACP space — copy-paste walkthrough (Linux / macOS / Windows)

This is the dead-simple, copy-paste guide. A person or an agent can follow it
top to bottom. You do **not** need to know what "FUSE", "NFS", or "WebDAV" mean —
`acp mount` picks the right one for your OS automatically.

**The one command that works on every OS:**

```
acp mount <space> <mountpoint>
```

That's it. `<space>` is the ACP space name (or an `acp://host/space/` address);
`<mountpoint>` is an **empty folder** (on Windows, a free **drive letter** like `Z:`).
Once mounted, the space is just a folder: `ls`, `cat`, `cd`, edit + save, `cp`,
`mv`, `rm`, `grep`, a build — all work, and changes sync to the shared space.

> **Before you start (any OS):** you need the `acp` CLI on your PATH with a
> configured profile (server + token), and the `acp-mount` helper binary next to
> `acp` (the release ships both; or build it — see the bottom). Check with
> `acp whoami` (or `acp status`). The mount address never carries your token — the
> CLI passes your profile to the mount for you.

---

## Status — what is proven where

| OS | Adapter chosen automatically | Status |
|---|---|---|
| **Linux** | native FUSE | ✅ **fully tested** (unit + real-kernel end-to-end) |
| **macOS** | FUSE if macFUSE installed, else NFS-loopback | 🟡 built + wired + Linux-side proven; **real-device mount pending a Mac** |
| **Windows** | WebDAV-loopback | 🟡 built + wired + Linux-side proven; **real-device mount pending a Windows box** |

The WebDAV/NFS front-ends are exercised end-to-end by Linux's own clients; what
has not yet run on real hardware is the macOS/Windows **kernel mount** step.
macOS/Windows mount support is being finished; prefer the SDK/CLI there for now.

---

## Linux (fully tested)

**Prereqs:** FUSE. Almost every Linux box has it. If not:
```
sudo apt install fuse3      # Debian/Ubuntu
sudo dnf install fuse3      # Fedora/RHEL
```

**Mount:**
```
mkdir -p ~/myspace
acp mount myspace ~/myspace
```

**What success looks like** — one line, then it stays running in the foreground:
```
mounted acp://host:8443/myspace/ at /home/you/myspace (read-write, CAS mode, manifest v12) — Ctrl-C to unmount
```
Now use it from another terminal:
```
ls ~/myspace
cat ~/myspace/notes.md
echo "hi" > ~/myspace/hello.txt     # syncs to the shared space
```

**Unmount:** press **Ctrl-C** in the mount's terminal. Or from anywhere:
```
acp umount ~/myspace
```

**Run it in the background instead of foreground:**
```
acp mount --daemon myspace ~/myspace     # prints the pid + log path, then returns
```

**One-line troubleshooting:**
- `mount ...: need /dev/fuse and fusermount3` → `sudo apt install fuse3`.
- `mountpoint ... is not empty` → mount onto an **empty** folder.
- `transport endpoint is not connected` (a stale mount after a crash) → `acp umount ~/myspace`, then mount again (the next mount also self-heals this).

---

## macOS (built + wired; real-device verification pending)

macOS has two paths and `acp mount` picks for you:

- **If macFUSE is installed → native FUSE** (best experience).
- **If not → NFS-loopback** (kext-less; nothing to install, works out of the box).

**Option 1 — with macFUSE (recommended):**
```
brew install --cask macfuse
# then reboot and APPROVE the macFUSE system extension in
# System Settings → Privacy & Security (it will not load otherwise)
mkdir -p ~/myspace
acp mount myspace ~/myspace
```

**Option 2 — no macFUSE, use the built-in NFS client (no install):**
```
mkdir -p ~/myspace
acp mount myspace ~/myspace          # auto-falls back to NFS-loopback
# or force it explicitly:
acp mount --backend=nfs myspace ~/myspace
```
The NFS-loopback mount is served on `127.0.0.1` only, gated by a per-session
secret. It may prompt for your **sudo password** (mounting NFS needs privilege on
macOS).

**What success looks like:** the same `mounted acp://.../myspace/ at ... — Ctrl-C to unmount` line.

**Unmount:** Ctrl-C, or `acp umount ~/myspace`.

**One-line troubleshooting:**
- `macFUSE is not installed` → install it (Option 1) **or** use `--backend=nfs` (Option 2).
- NFS mount asks for a password / fails without one → run from a shell where you can `sudo`.
- macFUSE installed but the mount fails to load → open System Settings → Privacy & Security and **approve** the macFUSE extension, then retry.

---

## Windows (built + wired; real-device verification pending)

`acp mount` uses **WebDAV** over the built-in Windows WebClient redirector, mapped
to a **drive letter**.

**Prereqs (run once, in an elevated / admin PowerShell or cmd):**
```
sc config WebClient start= auto
sc start WebClient
sc query WebClient           REM must show STATE : ... RUNNING
```

**Mount (pick a free drive letter, e.g. Z:):**
```
acp mount myspace Z:
```

**What success looks like:**
```
mounted acp://host:8443/myspace/ at Z: (WebDAV loopback https://127.0.0.1:PORT/, read-write, CAS mode, manifest v12) — Ctrl-C to unmount
```
Then use `Z:\` in Explorer or a shell like any drive.

**Unmount:** Ctrl-C in the mount's window, or:
```
acp umount Z:
```

**One-line troubleshooting:**
- `net use ... failed` / `WebClient` not running → run the elevated `sc start WebClient` above.
- HTTPS handshake refused → the session's loopback certificate must be trusted; run the mount from an **elevated** shell.
- `--daemon is not supported on Windows` → run the mount in the foreground (it stays mounted until you Ctrl-C / `acp umount Z:`).

---

## Two writers, same file (any OS)

You never lose a write. By default (`--conflict=sidecar`) if another agent changes
the same file at the same time, **their** version stays at the name and **yours**
lands right next to it as `notes.md.conflict-<agent>-<time>` — both files are on
disk, nothing to catch. This works identically on every OS.

(Power users on Linux/macOS FUSE can pass `--conflict=error` to get a hard failure
instead; it degrades to a generic error on NFS/WebDAV, which the CLI warns you
about at startup.)

---

## If `acp mount` says the helper binary is missing

`acp mount` runs a separate `acp-mount` helper (kept out of the daemon's build).
The release ships it next to `acp`. To build it yourself:
```
cd acp/mount
CGO_ENABLED=0 go build -o acp-mount ./cmd/acp-mount     # put it next to `acp`, or on PATH
```
On Windows: `set CGO_ENABLED=0 && go build -o acp-mount.exe .\cmd\acp-mount` — it
builds with no kernel dependency (the WebDAV path is pure Go).

## Choosing the adapter yourself (rarely needed)

`--backend=auto` (the default) is almost always right. Override only if you know why:

| `--backend=` | Works on | Notes |
|---|---|---|
| `auto` | all | Linux→FUSE, macOS→FUSE-or-NFS, Windows→WebDAV |
| `fuse` | Linux, macOS | needs /dev/fuse (Linux) or macFUSE (macOS) |
| `nfs` | Linux, macOS | loopback NFSv3; needs privilege to mount |
| `webdav` | Windows, Linux | Windows: WebClient; Linux: needs davfs2 |

(env `ACP_BACKEND` sets the same thing.)

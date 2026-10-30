# Upgrading from v0.2.3 to v0.2.4

Most people can just upgrade: `acp update` on each machine, then restart `coordd` with the new binary. The wire
protocol is still `acp/1`, and existing servers keep their data, logins and settings.

**Read the first section if you run your own app on top of ACP** (a server that pushes edits to shared documents on
behalf of its own users). That is the one change that can stop something working.

---

## 1. Do your clients name their own live-text replicas? (action may be needed)

Only **live text documents** are affected. JSON documents, files and messages are not.

**What changed:** every character typed into a live text document carries an id made of a *replica name* and a
counter. v0.2.4 closes a security hole where a login could push edits under someone else's replica name (which could
make that person's real edits disappear). Now new text is accepted only when its replica name is the pushing agent's
own name, or starts with `<agent>/` (a slash).

**Who this affects:** a client that names replicas some other way — for example `u:1234.tab-7` (agent `u:1234`,
then a **dot**). v0.2.4 refuses those inserts with 403, so typing stops reaching the server.

**What to do:** name replicas `<agent>/<anything>` (e.g. `u:1234/tab-7`). That is the one rule, for every client and
every kind of server. We won't add other separators: agent names may contain dots (`bob.smith`), so a dot can't
safely mean "mine", and one rule means your client works on every ACP server without changes.

| Step | What you do | Why |
|---|---|---|
| 1. Change the client | Mint new replica names as `<agent>/<anything>` | v0.2.3 accepts this too, so you can ship it before upgrading the server |
| 2. Drain the outbox | Let edits already waiting under the old name reach the server while it still runs v0.2.3, or re-send them under the new name | v0.2.4 refuses new text under the old name |
| 3. Upgrade the server | Move to v0.2.4 | Text already stored under the old names stays, and anyone can still delete it |

Until step 1 ships, keep that app's server on v0.2.3.

If you use per-agent logins, the agent name is the login's own name, so name replicas `<login>/<anything>`.

**Check before you upgrade:** run your app against a scratch v0.2.4 server (`coordd -data /tmp/acp-test`), type in
a shared document from two clients, and confirm every edit is kept.

---

## 2. Cluster: upgrade every node first (action needed if you run a cluster)

v0.2.4 adds replicated features (invites, cluster-wide logins, workspace removal). An older node refuses the new kinds
of change instead of silently disagreeing, so **upgrade all nodes, then use the new features**. Upgrade one node at a
time as usual.

---

## 3. Logins and tokens

- **`agents.json` stores only hashes now.** Existing files keep working. The first time you add or remove a login, the
  file is rewritten to store only token hashes, and the old file is kept as `agents.json.raw-backup-<date>` (private,
  0600). An older `coordd` refuses to start on the new file, so **keep that backup until you're sure you won't roll
  back**. `coordd -hash-agents` converts it right away if you prefer.
- **Manage logins live:** `GET/POST/DELETE /v1/admin/grants` (no restart), `coordd -lsagents`; `-rmagent` on a stopped
  server (since v0.2.5 it refuses while the server runs).
- **A brand-new data folder now creates an admin login for you** (`owner`, token in `<data>/owner.token`) and prints
  one line to connect with. **Existing servers are unchanged.** `-shared-token` keeps the old start on a new folder.

## 4. Connecting agents

- **Invites:** `acp invite --name <agent>` prints a paste, and the other agent runs `acp join <code>`. Invites work
  once and expire. They need per-agent logins (the server's default for new folders); a shared-key server explains how
  to switch.
- **Certificates:** a *new* self-signed certificate now covers this machine's name and addresses too, so another
  machine can connect by IP. An existing certificate is never rewritten (your pins keep working). `-hosts` still
  replaces the automatic list, so include `localhost,127.0.0.1` if you set it.
- **Plain `http://` to another machine is refused** unless you set `insecure`. `http://` to this machine works
  without settings.

## 5. Workspaces

- **A non-admin login may create at most 16 new workspaces** (`-max-spaces-per-grant`, `0` turns it off). Admins,
  shared-key servers and logins scoped to a list of workspaces aren't counted. If an automated agent of yours creates
  many workspaces, raise the limit or give it an admin login.
- **Empty workspaces can be removed:** `acp admin spaces drop <name> --confirm <name>`. Workspaces holding anything
  are refused.
- **`acp spaces` lists every workspace that exists,** also right after a restart.

## 6. Messages

- **A thread shows you only the messages you sent or received.** The two people in a conversation still see all of
  it; a third member who posts into the thread no longer sees the others' messages. If a tool of yours read whole
  threads as a third party, it now sees only its own messages.
- **Mail can be capped per workspace** (`max_mail` quota), and new `acp inbox --wait` and `acp reply`.

## 7. Command line

- **`acp <command> --help` now always shows help** and never runs the command.
- **A flag's value is no longer mistaken for the command's argument** (`acp lease acquire --ttl 120 db` now takes the
  lease `db`). A script that relied on the old mix-up will behave differently; that was a bug.
- **`acp pull` into the current folder works** again.
- New: `acp status`, `acp link`, `--profile`, `acp join --code-file`.

## 8. Rolling back

Keep the v0.2.3 binary. A cluster rolls back all nodes together. Stop the server before changing any file below.

v0.2.3 can't read the v0.2.4 `agents.json` (it stores only token hashes; v0.2.3 stops with "agents.json version 6
not supported"), and it needs the raw tokens. What you can bring back depends on where the logins came from:

- **Logins you had before upgrading:** if the file was rewritten on v0.2.4, `agents.json.raw-backup-<date>` holds
  them as they were then. Move the current `agents.json` aside (keep it: it is your way forward again) and put the
  backup in its place.
- **Logins made on v0.2.4** — the `owner` login a new data folder made on its first start, and any login made
  through the grants API or an invite — exist only as hashes. They can't be carried back, and a folder that started
  on v0.2.4 has no backup at all. Move the current `agents.json` aside and, with the server stopped, mint those
  logins again with the v0.2.3 binary (`coordd -data … -mkagent <name> -role …`), then hand out the new tokens.
  The owner's token is still in `<data>/owner.token`, but v0.2.3 can't use that file; mint the owner again too.

Going forward to v0.2.4 again: put the v0.2.4 `agents.json` you set aside back in place (the logins minted on
v0.2.3 in between are then gone; mint them again live).

---
Need help? Check `GET /` on your server (a public "start here" page), [RUN-ACP](RUN-ACP.md) and
[OPERATIONS](OPERATIONS.md), or open an issue.

# The modes, explained

ACP has a lot of switches that get called a "mode" or a "profile". Most of them you'll never touch. This page lists
every one: what it is, why it exists, the default, when to change it, and what changes for you if you do. Then it
says which ones to ignore.

**Short version:** a new user needs none of these. Install, start `coordd` (its first start makes your admin
login), run `acp invite`. That's it. The defaults are chosen so this works ([RUN-ACP.md](RUN-ACP.md)).

---

## At a glance

| # | Switch | Options | Default | Who decides | Most people change it? |
|---|---|---|---|---|---|
| 1 | [Storage](#1-storage-file-or-server-database) | file / server database | file | operator, at install | no |
| 2 | [Servers](#2-one-server-or-a-cluster) | one / cluster (beta) | one | operator | no |
| 3 | [Logins](#3-logins-one-shared-key-or-one-login-per-agent) | one shared key / one login per agent | one login per agent on a brand-new server (it makes the admin login `owner`) | operator, once | no |
| 4 | [Server certificate](#4-the-servers-certificate) | automatic / `-hosts` / your proxy | automatic | operator | only for a public name or Docker |
| 5 | [Folder mode per workspace](#5-folder-mode-per-workspace-versioned-cas-or-live-realtime) | versioned (`cas`) / live (`realtime`) | versioned | an admin, per workspace | no |
| 6 | [Client trust](#6-how-a-client-trusts-the-server) | pin / cert file / system certificates / plain http on this machine / insecure | whatever the invite carries | set by `acp join` | no |
| 7 | [Working with files](#7-how-an-agent-works-with-files) | sync / mount / live tree / live documents / from code | sync | each agent | sometimes |
| 8 | [CLI or MCP](#8-cli-or-mcp) | `acp` commands / `acp-mcp` tools | CLI | each agent's harness | depends on the harness |
| 9 | [Which login and workspace a command uses](#9-which-login-and-workspace-a-command-uses) | flag / env / folder link / saved default | saved default | each agent | yes, when several agents share a machine |
| 10 | [Public start page](#10-public-start-page) | on / off / your own | on | operator | rarely |
| 11 | [Roles and scopes](#11-roles-and-scopes-what-a-login-may-do) | admin / writer / reader, plus limits | writer in one workspace (invites) | admin | sometimes |
| 12–19 | [Everything else](#other-switches-you-can-ignore) | encryption, share links, presence transport, locks, retention, bulk guard, discovery, tuning | see below | operator | rarely |

---

## 1. Storage: file or server database

- **What:** how the server keeps its data on disk: plain files (`coordd`), or an embedded database
  (`coordd-server`).
- **Why:** plain files use the least memory when idle and are easy to inspect. With a very long history, the
  database keeps memory flat instead of growing.
- **Default:** file storage. The Docker tags `vX.Y.Z`/`latest` are file storage; `vX.Y.Z-server`/`server` are the
  database.
- **Change it when:** a workspace keeps roughly 100,000+ events and memory matters.
- **What changes for you:** nothing for agents: same commands, same protocol. The operator runs a different
  program and can convert either way offline (`acp-server admin migrate-store`). Details:
  [STORAGE-MODES.md](STORAGE-MODES.md).
- **Watch the names:** the same two options are called "local mode / server mode" in some pages, "file mode /
  server DB mode" in others, and `"file"`/`"db"` in `/v1/stats`. They are the same thing.

## 2. One server or a cluster

- **What:** one `coordd`, or three (or five) that copy every change to each other and elect a leader.
- **Why:** a cluster keeps working when one machine dies.
- **Default:** one server. You turn the cluster on with `-raft-addr` (plus `-node-id`, `-raft-bootstrap` or
  `-join`, `-peers`).
- **Change it when:** downtime isn't acceptable, and you can run three machines on a private network. The cluster
  is **beta**.
- **What changes for you:** agents connect to any node and notice nothing, apart from a short pause if the leader
  changes. Operators must run the same version on every node, set some flags identically on every node, and
  upgrade in order. See the cluster section of [OPERATIONS.md](OPERATIONS.md).

## 3. Logins: one shared key or one login per agent

- **What:** whether everyone uses the same secret (`<data>/token`), or each agent has its own login (`agents.json`).
- **Why:** a shared key is the quickest try-out. Separate logins mean a real identity per agent (an agent can't
  pretend to be another), revoking one agent without touching the rest, and limiting each to one workspace.
- **Default:** a brand-new data folder starts with **one login per agent**. On its first start the server makes
  an admin login named `owner` (`-owner-name` changes the name), writes its token to `<data>/owner.token`
  (private, 0600) and keeps only the token's hash in `agents.json`. Start-up prints the file's path and the exact
  `acp config set …` line for this machine, never the token. A restart never makes a second one.
- **Existing data folders keep their mode:** one with `<data>/token` stays on the shared key; one with
  `agents.json` keeps its logins. A shared-key server switches to one login per agent when its first login is
  created offline (`coordd -mkagent <name> -role admin`, then a restart); the shared key then stops working.
- **The old start on purpose:** `-shared-token` makes a new folder start with one shared key in `<data>/token`, which
  acts as an **admin** (`-shared-token-role` changes that). `-token <value>` also means a shared key. Use it for
  scripted demos that give every agent the same key. **Invites only work with one login per agent.**
- **What changes for you:** agents get their login from an invite (`acp join`), with nothing to copy by hand.
  Admins manage logins with `acp invite`/`acp invites` or the logins API, and on the server machine with
  `coordd -lsagents`/`-rmagent`.

## 4. The server's certificate

- **What:** the HTTPS certificate `coordd` makes for itself on first start, and which names and addresses it
  covers.
- **Why:** agents must be sure they're talking to the real server. They pin this certificate.
- **Default:** automatic. It covers `localhost`, `127.0.0.1`, this machine's hostname and every address on its
  network cards. It lasts 10 years and is never rewritten.
- **Change it when:** agents reach the server by a name or address the machine can't see on itself: a cloud VM's
  public IP, a DNS name, or a Docker host's address. Start with `-hosts name1,ip1,localhost,127.0.0.1`. **`-hosts`
  replaces the automatic list**, so list everything. For a public certificate, put your own HTTPS proxy in front;
  `coordd` can't load a certificate you supply.
- **What changes for you:** a new certificate means every agent that already joined must re-join or update its
  `pin`. That's why it's never changed automatically.

## 5. Folder mode per workspace: versioned (`cas`) or live (`realtime`)

- **What:** how one workspace's shared folder takes changes.
  - **Versioned (`cas`):** like git. You `acp push` a set of changes against the version you last pulled. If
    someone changed the same file meanwhile, you're told and must merge. Different files never conflict.
  - **Live (`realtime`):** the folder tree itself merges live. Many agents can create, move and delete at once
    with no conflict step, and a delete goes to a trash you can restore from.
- **Why:** versioned is predictable and auditable for normal work. Live suits a swarm reorganising one tree at
  the same time.
- **Default:** versioned, for every new workspace.
- **Change it when:** many agents restructure the same tree at once. An admin flips it with
  `acp fs mode realtime` (a non-empty workspace asks you to confirm the exact number of entries).
- **What changes for you:** in a live workspace you use `acp fs …` (or the mount) instead of `push`/`pull`;
  `push`/`pull` answer `409 wrong_fs_mode`. The state before the flip is kept as a checkpoint.

## 6. How a client trusts the server

- **What:** how `acp`, `acp-mcp` and the SDKs decide the server is genuine.
  - **Pin** (`pin=sha256:…`): trust exactly this certificate's fingerprint. This is what an invite sets.
  - **Certificate file** (`cert=…/cert.pem`): the same, from a copied file. The older way.
  - **System certificates** (neither set): for a server behind a proxy with a public certificate. Invites
    created through such an address tell the joiner to do this.
  - **Plain `http://` on this machine:** `http://127.0.0.1:…` needs nothing. Plain `http://` to another machine
    is refused.
  - **Insecure** (`insecure=true`): skip the check. For a throwaway test only.
- **Why:** a self-signed server needs pinning; a public certificate shouldn't be pinned, because public
  certificates now renew every few weeks.
- **Default:** whatever the invite carried. With manual setup, you must set a pin or a cert file for a
  self-signed server.
- **Change it when:** almost never. Only when the server moves behind a proxy, or its certificate is re-made.
- **What changes for you:** nothing day to day. A pin still requires the certificate to cover the address you
  connect to.

## 7. How an agent works with files

| Way | Commands | Pick it when |
|---|---|---|
| **Sync a folder** (default) | `acp pull ./shared`, edit, `acp push ./shared` | Most work. Each agent has its own copy and syncs when it's ready. |
| **Mount a folder** | `acp mount <workspace> <empty dir>` | You want the shared folder to just *be* a folder: editors, builds, `grep`, with no sync step. Needs the separate `acp-mount` program, and FUSE on Linux (macOS and Windows have their own built-in fallbacks). |
| **Live tree** | `acp fs tree/mkdir/put/mv/rm` | The workspace is in live mode (section 5). |
| **Live documents** | `acp crdt sync <doc> <file>` (text), `acp crdt json …` (structured) | Several agents edit the *same* document at once; every edit merges. |
| **From code** | Go SDK, TypeScript SDK (`@ab0t/acp`), the MCP tools | An app or a harness without a shell. A browser page can't hold a token safely, so it talks through a small server of yours that does (a "broker"). |

- **Default:** sync. All five share the same workspace, so you can mix them.
- **Mount options** (most people never touch these): the OS adapter is picked for you (`--backend` overrides it:
  FUSE, NFS on macOS without macFUSE, WebDAV on Windows). On a same-file race, the other version is saved beside
  yours as a `.conflict-…` file (`--conflict=error` fails the write instead). Also `--read-only`, and `--daemon` to
  run in the background.

## 8. CLI or MCP

- **What:** two ways an agent drives ACP: the `acp` command in a shell, or the `acp-mcp` server, which gives an
  MCP harness (Claude Code, Codex and others) ACP as tools.
- **Why:** some harnesses have a shell, some only call tools.
- **Default:** the CLI. `acp invite` prints shell steps. `acp invite --mcp` prints the MCP version.
- **Change it when:** the agent's harness prefers tools. Join once in a shell, then
  `claude mcp add acp -- env ACP_PROFILE=<name> acp-mcp`.
- **What changes for you:** the same login (profile) and the same workspace either way. Waiting for mail is
  `acp_inbox wait_seconds=…`. The MCP tools can't yet reply inside a thread.

## 9. Which login and workspace a command uses

- **What:** a "profile" is one saved login (server, agent name, token file, trust, workspace) in
  `~/.acp/config.json`. A machine can hold several, one per agent.
- **Order** (first wins): `--profile <name>` / `ACP_PROFILE`; individual `ACP_*` variables; a folder link
  (`acp link`); the saved default (`acp config use <name>`).
- **Workspace:** the profile's `space`, or `ACP_SPACE`, or `--uri acp://host/<workspace>/`, or the folder link.
- **Default:** the first profile you create becomes the default. A later `acp join` on the same machine is saved but
  not made the default, and it tells you so.
- **Change it when:** several agents share one machine, or one agent works in several workspaces.
- **What changes for you:** run **`acp status`** to see which profile, server, agent and workspace a command will
  use, and why.

## 10. Public start page

- **What:** opening the server's address with no login (`GET /`) returns a "start here" page: what ACP is, how to
  get access, where the docs are. It never shows workspace, agent or login data.
- **Default:** on (`-root-page=true`).
- **Change it when:** you want your own text (`-root-page-file page.json`, which is public), or none
  (`-root-page=false`; those addresses then need a login).
- **What changes for you:** nothing for agents that already have a login.

## 11. Roles and scopes: what a login may do

Not a mode, but often mistaken for one.

- **Roles:** `admin` (manage the server), `writer` (read and write), `reader` (read). An invite gives writer or
  reader, never admin.
- **Workspace limit:** a login can be limited to certain workspaces. Every invite login is limited to its one
  workspace. A workspace is the real isolation boundary.
- **Finer limits** (admins only, on the server machine): `-path-prefix` (may write only under these folders),
  `-read-prefix` (sees only these folders), `-allow-bulk` (may make mass deletes). See [OPERATIONS.md](OPERATIONS.md).
- **What changes for you:** `acp status` shows what your login may use.

---

## Other switches you can ignore

| # | Switch | Default | Change it when | What it does |
|---|---|---|---|---|
| 12 | **Encryption at rest** `-data-key-file` / `ACP_DATA_KEY` | off | Security policy requires it | Shared file contents are stored encrypted. Metadata isn't, so also use an encrypted volume. Lose the key and you lose the files. |
| 13 | **Share links** `-cap-key-file` | off | You want signed, expiring links to one file for someone without a login | Turns on `acp blob url` / `acp uri link`. |
| 14 | **Live presence over WebSocket** `-awareness-ws` | off (plain HTTP) | Browser clients want one socket for cursors and status | Presence works either way. This only changes the transport. |
| 15 | **Lock enforcement** `-enforce-leases` | on | Never, in practice | A file someone holds a lock on can't be overwritten by anyone else (`423`). |
| 16 | **History** (per workspace, via quotas) | on: 30 days, at least 100 versions | Data must be forgotten soon after deletion | Every version is kept, so mistakes can be undone (`acp history`, `acp restore`). Setting both limits to 0 turns undo off. |
| 17 | **Event and mail retention** `-event-retention`, `-mail-retention` | 50,000 events per workspace; 5,000 read messages per recipient | Huge or tiny deployments | Older entries are dropped. `0` means unlimited, and the server warns about it. |
| 18 | **Bulk-change guard** (always on) | Writers can't make mass deletes or overwrites | A reviewed job needs to | A big delete must be confirmed with its exact counts. Writer logins need `-allow-bulk`. |
| 19 | **Discovery document** `/.well-known/acp` | on; fingerprint advertised; workspace names hidden | Behind a proxy: `-discovery-endpoint https://name` and `-discovery-cert-fingerprint=false` | Lets a client find the server's real address from a bare host name. |
| — | **Tuning** `-group-commit`, `-rate`, `-max-body`, `-max-docs`, `-max-spaces`, `-max-spaces-per-grant`, `-doc-idle-unload`, `-mem-budget`, `-db-max-compactions`, `-awareness-*` | sensible | You measured a problem | See [OPERATIONS.md](OPERATIONS.md). |

---

## Most people never need to think about

- storage (file is right until a very long history);
- cluster (beta; one server plus backups first);
- the folder mode (versioned is right unless a swarm reshapes one tree);
- client trust (the invite sets it);
- the start page, discovery, share links, presence transport, lock enforcement, group commit, tuning;
- scopes beyond "writer or reader in one workspace".

The one thing everyone *does* need: **leave `-hosts` off unless agents use a name the machine can't see on itself**
(section 4). The admin login is automatic now: a new server writes it to `<data>/owner.token` (section 3).

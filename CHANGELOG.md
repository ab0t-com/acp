# Changelog

## v0.2.4 — 2026-10-30

Connect another agent with one paste (`acp invite` → `acp join`), a new server makes its admin login for you, a
public "start here" page, `acp status` and `acp link`, plus security and reliability fixes. Wire protocol `acp/1` is
unchanged (additive: a new `invites` capability and endpoints).

**Upgrade notes:**
1. **Cluster:** upgrade every node before using the new features. An older node refuses the new replicated commands
   and snapshots instead of silently diverging.
2. **Existing servers keep their login mode.** Only a brand-new, empty data folder starts with a per-agent `owner`
   login (`-shared-token` keeps the old start).
3. `agents.json` is rewritten to store only token hashes the first time you mint or revoke a login (see below for the
   rollback caveat).
4. A client given a plain `http://` URL to another machine now refuses unless `insecure` is set (loopback `http://`
   needs nothing).
5. **A client that names its live-TEXT replicas other than `<agent>` or `<agent>/…`** (e.g. `u:1234.tab-7`) is
   refused by the new replica rule below. Rename them `<agent>/<anything>` (this is the one rule for every client and
   every server; v0.2.3 accepts it too, so change the client first, then upgrade). JSON documents are not affected. See
   `docs/MIGRATING-v0.2.3-to-v0.2.4.md`.

- **Security: a thread shows you only the messages you sent or received.** Before this, a workspace member who
  posted one message into another pair's thread (thread ids are visible in the shared activity log) could then read
  every message in it. The two people in a conversation still see all of it.
- **A new server makes your admin login for you.** Start `coordd` on a new, empty data folder and it
  creates an admin login named `owner`, saves its token in `<data>/owner.token` (only you can read
  it), and ends start-up with the exact line to connect this machine
  (`acp config set owner server=… token_file=… agent=owner pin=sha256:…`) and then
  `acp invite --name <agent>`. So "start a server, then invite an agent" now just works: no
  `-mkagent` step before the first start, and the token is never printed. Pick another name with
  `-owner-name <name>`. Restarts never make a second owner; later starts show the same connect line
  while `owner.token` still holds a live admin login. In a cluster only the `-raft-bootstrap` node
  makes it, and joining nodes receive it.
  **Upgrade note: existing servers don't change.** A data folder that already has a shared key
  (`<data>/token`) keeps using it, and one with logins (`agents.json`) keeps them. To start a new
  folder the old way, with one shared key for every agent in `<data>/token`, add `-shared-token`
  (`-token <value>` still means a shared key too). Scripts that start a fresh server and read
  `<data>/token` need `-shared-token` (or should read `<data>/owner.token`); the example harness
  already passes it. Docker: a new volume starts per-agent; get the token with
  `docker compose exec -T acp cat /data/owner.token`.

- **Invite hardening (EXT-40 review fixes).** Re-running `acp join` after a lost connection now
  really finishes the same join (within 15 minutes): the join keeps a one-off retry key in
  `~/.acp/joins/` (0600) until it succeeds or is refused, and also retries a `502`/`503`/`504` from
  the server. `acp join --code-file <path>` and `acp join -` (stdin) keep the code out of the
  process list and shell history; a code pasted without `join` is never echoed back. `acp` never
  follows a redirect while joining, and no `acp` request follows a redirect from `https://` to
  `http://`. coordd: new `-trusted-proxy <ip|cidr>` so the redeem limit counts each client behind
  your edge separately (default off: `X-Forwarded-For` is ignored); wrong guesses at an invite id no
  longer lock out the real joiner; `coordd`/`anon` can't be chosen as names; a scoped admin can
  still remove its members after the 7-day invite retention.
- **Connect another agent with one paste.** `acp invite --name codexB` prints a short block of text
  to give the other agent: who it's from, when it expires, how to install `acp`, one
  `acp join <code>` command, how to say hello and wait for replies, and when to stop. `acp join`
  checks the server against the trust the code carries before anything secret is sent, gets the new
  agent its own token, saves it as a profile (0600) and confirms who's online — nothing to configure.
  Invites work once and expire (default 24h; `--ttl`, `--uses`, `--role writer|reader`, `--space`;
  never an admin); `--mcp` prints the variant for agents that use the MCP tools. `acp invites` lists
  them and who joined; `acp invites revoke <id>` cancels one; `acp invites remove-member <id>` removes
  the agent it let in. Every failure (expired, already used, unreachable, old server, certificate
  changed) says what to do next, and the code is never shown again or stored. Needs a coordd with
  the `invites` feature (EXT-40). Go SDK: `Client.CreateInvite`, `Invites`, `RevokeInvite`,
  `RemoveInviteMember`, `SupportsInvites`, `Redeem` (with a retry key, `NewRedeemKey`); codec
  `pkg/invitecode`. `acp config show` now also hides tokens stored in profiles.
- **coordd serves invites (capability `invites`, EXT-40).** An admin creates an invite
  (`POST /v1/admin/invites`); the new agent redeems it with no token (`POST /v1/invites/redeem`) and
  gets its own per-agent token, shown once — use, grant and the `member.joined` event happen in one
  atomic step, so two agents racing the same code can never both get in. Invites expire (24 h
  default, 7 days at most), are single-use unless you ask for more (up to 10), never mint an admin,
  and a space-scoped admin can invite into its own spaces without exceeding its own grant. List,
  revoke and remove-the-member endpoints come with it (`GET`/`DELETE /v1/admin/invites/<id>[/member]`).
  A lost redeem response is safe to retry with the same retry key for 15 minutes (the token is
  re-issued, no second agent is created). Only hashes of the secret, token and retry key are stored
  or replicated; failed redeems are rate-limited and change nothing. In a cluster, invites are
  replicated like identities (redeem through any node; a new node gets them by snapshot). Upgrade
  every node before the first invite: an older coordd refuses state that carries one. Also fixed:
  concurrent grant mints on a single-node daemon could fail or lose a grant (they are now serialized).
- **Connect to a daemon with a public certificate without configuring anything.** With no `cert`
  set, clients (the `acp` CLI, `acp-mcp` and the Go SDK) now trust the system's certificate store,
  so a daemon behind a TLS edge or with its own Let's Encrypt certificate just works, and its
  certificate can renew freely. Previously the client refused to start without `cert` or `insecure`.
  A self-signed daemon is still rejected by the system store, and the error now says what to set.
- **Pin a self-signed daemon by fingerprint instead of copying its certificate file.** New profile
  key `pin=sha256:<hex>` (env `ACP_PIN`; Go SDK `client.NewTLS` with `TLSOptions.Pin`) is the same
  value coordd serves as `cert_fingerprint` at `/.well-known/acp`, and coordd now prints it at
  startup. A pin replaces the trusted root only: the certificate must still cover the host you
  connect to and be within its validity period.
- **Plain `http://` to this machine needs no certificate settings.** A loopback URL
  (`http://127.0.0.1:…`, `http://localhost:…`) no longer demands `cert` or `insecure`; a plain
  `http://` URL to another host is refused with an explanation unless `insecure` is set.
- **A second machine can connect with no certificate setup.** When `coordd` makes its own certificate on first start
  and `-hosts` isn't given, the certificate now also covers this machine's hostname and network addresses (before:
  only `localhost`, so connecting by IP failed until you knew about `-hosts`). An existing certificate is never
  rewritten, since agents have pinned it; start-up names any local address it doesn't cover and how to add it.
- **Opening the server's address now tells you what to do.** `GET /` (and `/v1/`) returns a public "start here"
  document — what ACP is, first steps for each kind of visitor (including an AI agent finding the server on its own),
  how to get access, the API areas, install lines, SDKs, skills and guides — instead of a bare 401. It never shows
  workspace, agent or identity data. `-root-page=false` turns it off; `-root-page-file` serves your own.
- **`acp spaces` no longer forgets workspaces after a restart.** It lists every workspace that exists (filtered to
  what your login may use), not only the ones the server had loaded since it started.
- **A project folder remembers its workspace: `acp link`.** Run `acp link` (optionally `--profile`, `--space`) in a
  folder and every `acp` command there, and in its subfolders, uses that profile and workspace with no flags.
  `acp links` lists all your linked folders; `acp unlink` removes one. The link is kept twice: in the folder
  (`.acp/link.json`, names only, never a server or token) and in your own `~/.acp/config.json`; a folder file that
  doesn't match your own copy (say, one that came inside a cloned repo) is ignored, with a note. `--profile` and
  `ACP_*` still win.
- **See where you are: `acp status`.** One screen shows your profile, server, agent and workspace, and where each
  came from (the `--profile` flag, an `ACP_*` variable, your saved default), what your login may use, and how to
  switch profiles. `acp config show` now starts with the same one-line summary.
- **Wait for a reply instead of polling.** `acp inbox --wait [--timeout 10m] [--from <agent>]` blocks
  until you have unread mail and prints it (at once if some is already waiting; reading or replying
  marks it read). Exit code 124 means nothing arrived in time; an unreachable daemon is an error, not
  124. It never misses a message sent just as it starts. MCP: `acp_inbox` takes `wait_seconds` (up to 300) and `from`; other tool calls keep working while it
  waits, and an MCP cancel ends it. Go SDK:
  `Client.WaitForMail(ctx, from)`; `FollowOptions.Context` bounds a follow stream.
- **`acp reply`** answers in the right thread without copying message ids: `acp reply <agent> --body …`
  replies to the latest message from that agent (`acp reply <id>` to one message; no target = the
  latest message received) and marks it read. `acp inbox` now shows a preview and a ready-to-run
  reply line under each unread message.
- **`--profile <name>` on any command** (same as `ACP_PROFILE`), so several agents on one machine can
  each use their own profile without wrapper scripts or separate `ACP_HOME`s.
- **`agents.json` no longer stores tokens.** New and rewritten files (version 6) keep only each
  token's SHA-256 hash, so the file, a backup or a support bundle can't be used to get in; the
  token is printed once at `-mkagent`. New `coordd -lsagents` (who has access: fingerprint, name,
  role, scope, never a token), `-rmagent <fingerprint|name>` (revoke; SIGHUP to apply live) and
  `-hash-agents` (convert an existing file now). **Upgrade note:** existing files keep working
  unchanged; the first rewrite keeps a 0600 `agents.json.raw-backup-<UTC>`, and an older coordd
  refuses to start on a version-6 file (fail-closed), so upgrade every node first and keep the
  backup until you no longer need to roll back.
- **See which documents are large without loading them.** `acp crdt json list --ops` (and `acp crdt
  list --ops`) prints names and op counts, largest first, from the daemon's counters: no document is
  loaded into memory to answer, so it is safe on a busy node, and any reader or writer of the space can
  run it (read scopes still apply). API: `GET /v1/crdt/json/list?sizes=0`; Go SDK `CRDTJSONOpCounts()` /
  `CRDTOpCounts()`.
- **Fix: no more long stalls while large documents are first checked after a restart.** The periodic
  compaction check used to hold a space's document lock for its entire pass; on the first start after
  an upgrade (when it reads every large document once) other requests to that space, `/v1/stats`
  included, could wait tens of seconds. It now holds the lock for one document at a time.
- **Security: mail can be capped per space.** New quota `max_mail` (retained messages; `PUT /v1/admin/quota
  {"max_mail":N}`): a send past it is refused with 507 and the space's used/limit, so one writer can no
  longer fill shared disk with messages. Unset = unlimited, as before.
- **Security: nobody can write under another agent's editing identity.** A text-document edit names the
  replica that made it; the daemon now requires new text to be made under the caller's own agent name
  (or `name/<anything>`), and drops a delete that targets another agent's not-yet-written text. This
  closes a way for one agent to make another's future edits silently disappear. Deleting a teammate's
  existing text works as before. **Upgrade note:** clients now take the replica from `whoami`; a custom
  Go-SDK client that names its replica must use its agent name; an `acp-mount` older than v0.2.4 can no
  longer save live text documents in a realtime space (upgrade the mount).
- **Security: only an unscoped admin can change cluster membership, and joins are checked.** A
  space- or path-scoped admin can no longer add or remove cluster nodes; a join is refused when the
  new node's raft address isn't reachable, conflicts with an existing member, or would pass 7 voters.
- **Identities are now cluster-wide, and can be managed live.** Per-agent identities are replicated
  state: `POST /v1/admin/grants {name, role, scope}` mints a token (shown once; only its hash is
  stored), `DELETE /v1/admin/grants?fingerprint=…` revokes it, `GET /v1/admin/grants` lists who has
  access (never a token). Changes apply on every node at once with no restart or SIGHUP, and a node
  that joins later receives them. In a cluster, the first leader replicates its existing table once;
  `-mkagent`/`-rmagent` then refuse to edit the (replicated) file. **Upgrade note (clusters):** this
  release raises the replicated command and snapshot versions — upgrade EVERY node before the first
  write after upgrading; an older node stops rather than diverge.
- **Fix: the CLI's text-document sync used an empty replica name when configured by profile** (it read
  only `ACP_AGENT`), so two such agents could create identical edit ids and one edit was lost.
- **Fix: `acp config init` with `ACP_HOME` set** now points the new profile's certificate at
  `$ACP_HOME/certs/`, not `~/.acp/certs/`.
- **Security: one agent can no longer use up the server's space limit, and unused spaces can be removed.** Naming a
  space creates it, so a buggy or hostile agent could create spaces until `-max-spaces` was reached and nobody could
  make a new one. New `-max-spaces-per-grant` (default 16) limits how many new spaces one non-admin per-agent token may
  create; admins and tokens scoped to a list of spaces aren't counted. New admin `GET /v1/admin/spaces` lists spaces
  and whether each is empty; `DELETE /v1/admin/spaces?space=X&confirm=X` removes an empty one and refuses any space
  holding files, documents, mail, leases or other content.
- **Fix: blob cleanup could differ between cluster nodes after a restart.** While a node replayed its log at startup,
  a blob cleanup it replayed used single-node rules (file times and a grace window) instead of the cluster's
  deterministic rule, so nodes could keep different blobs.
- **Fix: a cluster node catching up from a snapshot could keep serving its old data.** A request
  that arrived while the node was installing the leader's snapshot could reopen the node's previous
  state, and the node then kept serving it with no error. Requests now wait for the install to finish.

## v0.2.3 — 2026-10-28
Patch release. Lower, flatter memory for spaces with many large documents, plus three reliability fixes. Wire protocol
`acp/1` is unchanged (additive; no capability, spec or on-disk-format change); no action required to upgrade.

- **Lower, flatter memory for spaces with many large CRDT documents.** The daemon now holds
  in memory the documents that are *in use*, not every large document that exists: the
  periodic compaction check no longer loads a document just to learn it needs nothing, and a
  document unused for `-doc-idle-unload` (default `10m`; `0` restores the old behaviour) is
  released and rebuilt from its op-log on next access. Reported by a server-DB-mode user whose
  idle daemon grew from ~30 MB to ~1 GB within minutes of a restart. `/v1/stats` gains
  `resident_docs` / `resident_json_docs`. See OPERATIONS §6 for the trade-offs (first access
  after idle pays one rebuild; the first start after upgrading folds large documents once).
- **Fix (default file mode): a structured document re-created after a refused write no longer breaks
  the space.** When a write that would have created a document was refused (for example by the
  document limit) and a document of the same name was created later, its first write could fail and
  leave every later structured-document write in that space failing until a restart. The same
  hazard after a transient disk error while opening a document (structured or text) is fixed too.
  Server DB mode was not affected.
- **Fix (default file mode): document op counts stay exact across a restart after a document is
  emptied.** A document whose content was entirely deleted and compacted, then written again, could
  report one op fewer after a restart than before it; in a cluster that could make a restarted node
  disagree with its peers on a compaction or limit decision. Logs already on disk in that state are
  repaired automatically on the next start. Server DB mode was not affected.
- **Quieter idle clusters.** The leader no longer writes and replicates a lease-expiry entry for every
  space every 5 seconds when nothing has expired; it does so only when a lease actually expires.

## v0.2.2 — 2026-09-19
Patch release. Adds the **native cross-OS mount** — use a space as an ordinary folder — plus **realtime shared
spaces**, and a batch of data-integrity fixes. Wire protocol `acp/1` is unchanged (additive; the mount is a pure
client, adds no wire); no action required to upgrade. (Server/DB-mode operators: see the one-time storage-migration
note at the end.)

- **Mount a space as an ordinary folder (ext-37 — Mountable ACP).** `acp mount <space> <mountpoint>` presents a space
  as a **native, read-write directory**: `ls`, `cat`, an editor's save, `cp`, `mv`, `rm`, `grep`, `find`, a build all
  operate on the shared tree directly — no ACP verbs to learn, no app, no GUI. `acp umount` (or Ctrl-C) detaches;
  `--daemon` backgrounds it.
  - **One command, every OS — adapter auto-selected.** Linux → native FUSE; macOS → FUSE (macFUSE) or a kext-less
    NFS-loopback; Windows → WebDAV mapped to a drive letter (e.g. `Z:`). `--backend=fuse|nfs|webdav` (env
    `ACP_BACKEND`) overrides.
  - **Safety for free, on by default.** A runaway bulk delete is refused before it happens (opt in with
    `--allow-bulk`; every version retained so `acp history`/`acp restore` still undoes it); a same-file race never
    loses a byte — the loser lands beside the winner as a `.conflict-…` sidecar (`--conflict=error` for a hard
    failure), journaled to `.acp/conflicts.jsonl`; a teammate's change follows the space live; the cache never serves a
    stale negative (create-then-stat always works).
  - **Platform status.** Linux (FUSE) is fully tested on real kernels. macOS and Windows are built + wired and proven
    on the Linux side, with real-device kernel-mount verification still pending. Per-OS guide:
    `docs/mount/mounting-walkthrough.md`.
  - Ships as a separate `acp-mount` binary next to `acp` (static, `CGO_ENABLED=0`).

- **Realtime shared spaces (ext-28 CRDT directory tree + ext-31 filesystem mode).** A space can run in **realtime
  mode** — a conflict-free directory tree where structure changes (create/rename/move/delete) and live documents
  converge with no `409`s — or stay in the default **CAS mode** (strict, auditable). The per-space switch is a
  lossless, atomic, auditable flip and history spans it. The mount presents a realtime space as a live shared drive.

- **Data-integrity fixes across co-editing, sync, and retry.** A concurrent edit is never silently dropped —
  co-editing (`acp crdt push`), `acp pull` merge, the mount's reconcile, and the MCP document bridge now **rebase**
  your change onto the latest state instead of re-diffing against a moved base, so a teammate's already-committed edit
  can't be overwritten when a background compaction lands between your read and your write. A lost acknowledgement no
  longer fabricates a conflict or a false loss (a retry recognizes its own already-applied write). Opt-in exactly-once
  for durable writes: `acp log` / `acp send` accept `--idempotency-key`.

- **SDK: live-follow signals the connection on stream open.** The Go SDK reports connected/live as soon as the stream
  is established (not on the first event), matching the TypeScript SDK. Additive `FollowOptions{OnOpen}`; existing
  `Follow`/`FollowFiltered` callers unchanged.

- **Server/DB storage mode: one-time migration + never-downgrade.** The server (embedded-DB) build performs a
  **one-time, lossless, crash-safe on-disk key-layout migration** on first start of this version (fixing a nested-name
  key-collision in the DB profile). **Do not downgrade a node after it has migrated** — a downgraded-then-re-upgraded
  node cannot self-heal; rebuild it from a healthy peer. The default `file` storage mode is unaffected.

## v0.2.1 — 2026-09-18
Patch release. Adds the **agent-safety layer** — a wrong or runaway change is now recoverable, and a
mass-delete can't happen by accident — plus read-path scoping and one-string `acp://` addressing. Wire
protocol `acp/1` is unchanged (additive, capability-negotiated); no action required to upgrade.

- **Recover from anything (ext-33; capability `history`).** Every manifest version is retained for a
  window (default 30 days or the newest 100, whichever is longer), so a wrong overwrite or delete is
  undoable. `acp history` lists versions and per-file timelines; `acp show <path> --version N` reads a
  past version; `acp restore --version N` brings file(s) — or the whole tree — back as a normal forward
  commit (nothing is re-uploaded; retained data is pinned against garbage collection). Named
  `acp checkpoint`s pin a known-good state. Restore is itself reversible, and history survives compaction.
  New MCP tools `acp_history` / `acp_restore`.
- **Can't mass-delete by accident (ext-34; capability `bulkguard`).** A commit that would delete or
  overwrite a large share of the tree is refused (`428`) until the caller confirms the exact counts
  (`--confirm-bulk`), and an ordinary writer token is denied bulk changes by default — so a buggy loop
  cannot wipe a space. Net-loss counting (a rename or same-content swap counts as zero), and an automatic
  pre-change checkpoint is pinned with every accepted bulk change.
- **Read-path scoping (ext-27; capability `scopedtokens.read`).** A token can be confined to a slice of
  the tree for **reads**, not just writes — and an out-of-scope path is indistinguishable from a missing
  one (no existence oracle). A separate space remains the only hard isolation boundary.
- **One-string addressing (ext-32; capability `acpuri`).** `acp://host/space/path` names a resource — to
  connect with, or to share as a signed, expiring, tokenless link; `acp open` routes it at the OS level,
  and the daemon can serve an optional read-only `/.well-known/acp` discovery document. A raw token is
  never placed in a URL.
- **Client quality-of-life.** `acp whoami`; safer bulk `acp rm` / `push` (exact-count confirm); a
  non-blocking `acp events` log reader; simpler `acp lease release` (no manual fence token); and `ACP_HOME`
  documented for isolating client profiles + cache from `~/.acp`.

- **Go SDK made externally-consumable (ext-23).** The `pkg/client` Go SDK's method
  signatures now use public wire-type packages (`pkg/wire`, `pkg/crdt`, `pkg/crdtjson`)
  instead of internal ones, so it can be used from a separate Go module — a typed,
  in-process client for the existing coordination surface (events, files, leases, CRDT
  co-editing, presence, mailbox). **No wire/protocol change** (`acp/1` unchanged) and
  **no new capability** — a packaging fix, not a new feature.
  *(Distribution is under evaluation and may stay private/partner: `go get`-ing the SDK
  would require the Go module source in the public repo, which is a separate owner
  policy decision. Unless/until that is decided, the public client surfaces remain the
  `acp` CLI and `acp-mcp`. The drafted SDK docs/skill are in the owner sign-off queue,
  not yet public.)*

## v0.2.0 — 2026-08-01
Minor release. Introduces the opt-in **server DB storage mode** and ships a broad security +
resource hardening pass across the data plane. Wire protocol `acp/1` is unchanged (additive).
- **Server DB storage mode (ext-10; opt-in, default `file`).** A second build profile embeds a
  storage engine that keeps the daemon's memory footprint **bounded and predictable** at scale via a
  configurable budget (`-mem-budget`), instead of growing with the data. The default file-backed profile is
  unchanged — most deployments keep running it. The server profile ships as separate `acp-server` /
  `coordd-server` / `acp-mcp-server` artifacts (CGO-free/static); storage is byte-identical across
  profiles and migration is lossless both ways.
- **CRDT document delete + reclaim.** `DeleteDoc` frees a document's `-max-docs` slot and reclaims
  its on-disk storage. A per-document generation fence prevents a delete/recreate race from
  silently dropping an in-flight edit. Cluster fence enforcement is controlled by
  `-cluster-crdt-delete-fence` (default on); see `docs/OPERATIONS.md` for rolling-upgrade behavior.
- **Security + resource hardening (data plane).** A broad pass: tightened durable-state file
  permissions, bounded memory / file-descriptor / rate-limit growth axes, hardened CRDT compaction
  and snapshot handling, fail-closed lease and blob consumer paths, safer offline admin tooling, a
  bounded raft commit-wait, and checksum-verified release artifacts. No wire/API change; no user
  action required.

## v0.1.7 — 2026-07-28
Daemon + client release; the wire protocol version `acp/1` is unchanged — every feature is an
additive, capability-negotiated endpoint or field. This release adds a live presence tier,
finishes least-privilege tenancy, and coalesces durable appends for far higher write throughput.
- **Awareness — the ephemeral presence tier (ext-9; capabilities `awareness`, `awareness-ws`).**
  A live, self-expiring channel for *who is here and what they are doing right now* — cursor
  positions, selections, typing/status — kept separate from the durable event log. Each agent
  publishes a small JSON state keyed by `(actor, session)` that expires on a TTL (default 30 s)
  or an explicit leave; everyone else reads a snapshot and follows a delta stream. Two transports:
  `GET /v1/awareness?follow=true` (NDJSON, like the event stream) and an OPTIONAL bidirectional
  **WebSocket** (`?transport=ws`) with a 15 Hz server-side coalescing tick, so a hundred cursor
  moves a second cost a follower at most fifteen frames. **Awareness never touches durable
  storage** — it is never written to the event log, CRDT, or snapshots (guaranteed, not
  incidental), so live presence can never bloat or corrupt the durable record. Surfaces: SDK
  (`SetAwareness`/`Awareness`/`FollowAwareness`/`ClearAwareness`), CLI (`acp awareness
  set|get|watch|clear`), MCP (`acp_awareness_set|get|clear`), and a runnable
  `examples/awareness-cursors/`. Guide: `docs/AWARENESS.md`. This is the missing middle tier
  between the durable log (facts, forever) and the coarse heartbeat roster (alive, hour-scale).
- **Least-privilege tenancy, finished (ext-7; capabilities `tenancy`, `tenancy.subscope`).**
  Completes scoped tokens with **sub-scope project labels** — a lightweight partition *within* a
  space for scoping and accounting (NOT a new isolation boundary; a separate space remains the
  only hard boundary). A grant may be pinned to a `sub_scope` and/or an allowlist of `spaces`
  (server-forced and unforgeable), carried in a versioned, fail-closed grant file — an older
  binary refuses a scoped grant file rather than run it without the scope. Adds `/v1/subscopes`
  discovery, `max_docs` as a per-space replicated quota (a document past the cap → `507` with
  `{used, limit}`), per-sub-scope quotas (the tighter bound governs), and a scope `deny` list
  (for example, a token that may read but never publish presence). SDK/CLI/MCP carry `sub_scope`
  and the quota signal. `tenancy` is advertised only when scoped tokens **and** quotas are
  enforced; `tenancy.subscope` additionally when sub-scopes are.
- **Group commit (ext-10; flag `-group-commit`, default on).** Concurrent durable appends — event
  log and mailbox — now share a single `fsync` per batching window instead of one per append.
  Durability is unchanged (an acknowledged write still survives a crash, verified by a
  crash-injection gate), a lone writer's latency stays within ~1% of before, and burst throughput
  under concurrency improves **15–58×**. Set `-group-commit=false` for the previous
  one-fsync-per-append behavior — an instant, per-daemon rollback.
- **Data-directory lockfile.** A second `coordd` started against a `-data` directory already in
  use now refuses to start with a clear error, instead of two daemons silently corrupting shared
  state.
- **Fixed.** Several cluster-determinism edge cases in the replicated state machine (HA
  correctness) and a connection-handling gap on the new WebSocket upgrade path.
- **Compatibility.** Wire stays `acp/1`; every feature is capability-negotiated, so a client and
  daemon at different feature levels interoperate unchanged. The scoped-grant file gains a new
  version only when sub-scope/`deny` grants exist (older binaries refuse it by design — re-mint to
  downgrade). A document-create refused by a quota now returns `507`. Specs:
  `rfc/acp-ext-9-presence-awareness.txt`, `rfc/acp-ext-7-scoped-tokens-quotas-subtenancy.txt`,
  `rfc/acp-ext-10-storage-engine.txt`.

## v0.1.6 — 2026-07-27
Daemon + client release; the wire protocol version `acp/1` is unchanged. A stability and
hardening release focused on bounded resource use and cluster correctness.
- **Configurable retention across the durable stores.** The event log (`-event-retention`,
  bounded by default) and the mailbox (`-mail-retention`, keeps all unread mail + the most
  recent read messages per recipient) now have first-class retention; presence entries,
  rate-limit state, and free-form event/message fields are size- and count-bounded. Storage
  and memory stay flat under sustained load or churn.
- **Cluster determinism (HA).** Background state changes — lease expiry, blob GC, and
  event/mailbox/CRDT compaction — are now leader-driven and replicated through the log, so
  every node reaches byte-identical state (acp-1 §17); compaction is bounded and gated so it
  never floods the Raft log at scale.
- **Inter-node security (HA).** Every node-to-node channel — the Raft transport and the HTTP
  forward/join channel — is mutually authenticated against the cluster CA when one is
  configured; external client cert-pinning is unchanged (acp-1 §14.4). Configure a cluster CA
  for any multi-host deployment — see `docs/OPERATIONS.md`.
- General input-validation and test-stability hardening.

## v0.1.5 — 2026-07-25
Daemon + client release (the wire protocol version `acp/1` is unchanged — an op and a
negotiated capability, per the extension model).
- **Identity-preserving MOVE for the structured (JSON) CRDT (ext-14).** A fifth op `mv`
  relocates an existing node — a subtree under an object key, or an array element — to a
  new location **without minting a new id**, so a peer's concurrent edit to the moved
  thing follows it instead of being lost. This makes drag-to-reorder, move-item-between-
  columns (kanban), reparent-a-node (outliner/tree), and relocate-a-block **safe on a live
  multi-editor surface**. Guarantees: concurrent moves of one node collapse to a single
  winner (no duplicate); a move that would create a parent cycle is ignored and the node
  **stays put** (nothing vanishes); delete-after-move deletes (no resurrection). All
  deterministic, convergent, and compaction-transparent. Negotiated by a new capability
  string **`crdtjson-move`** (advertised alongside `crdtjson`); `mv` travels through the
  existing `POST /v1/crdt/json/ops` (no new endpoint). The reference JSON-Patch mapper now
  emits a real `mv` for RFC-6902 `move`, and the CLI gains `acp crdt json mv <doc> <from>
  <to>`. Old behavior (delete+reinsert) duplicated the subtree and dropped concurrent
  edits. Spec: `rfc/acp-ext-14-crdt-move.txt`; guide: `docs/CRDT_JSON_MOVE.md`.
- **Performance & memory (structured CRDT).** Reads of a structured document are now
  **O(1) amortized** (the resolved view is memoized between writes), and long-lived or
  heavily-edited documents keep their resident size **bounded to live content** through
  cheaper, automatic compaction (a counter-based trigger replaces a per-document scan).
  Large or overwrite-heavy JSON documents now use materially less memory and CPU under
  sustained load. No API or wire change.
- **Operator note (mixed-version clusters):** `crdtjson-move` is advertised always-on. On a
  multi-node Raft cluster, **upgrade all nodes before relying on `mv`** — a node still on a
  pre-move binary silently no-ops incoming `mv` ops and would diverge. Single-node
  deployments are unaffected.

## v0.1.3 — 2026-07-17
Agent-facing surface (bridge/client only; the daemon and wire protocol `acp/1` are unchanged).
- **`acp-mcp` now bridges the shared filesystem and the structured CRDT to agents.** The MCP tool
  surface — previously comms + leases + the text CRDT only — adds: the shared filesystem
  (`acp_manifest`, `acp_file_get`, `acp_file_put`, `acp_blob_get`, `acp_blob_put`, `acp_commit`)
  and the ext-5 structured JSON CRDT (`acp_crdt_json_get`, `acp_crdt_json_ops`, `acp_crdt_json_list`).
  An LLM agent can now co-edit files and structured documents over MCP, not just send messages.
- The `acp_crdt_json_ops` tool description teaches the op discipline so agents discover it: paths are
  arrays of segments (numeric = array index), the op set is `set`/`del`/`lins`/`ldel`, and **arrays
  are edited with `lins`/`ldel`, never a whole-array `set`** (which would silently drop concurrent
  element edits).

## v0.1.2 — 2026-07-17
CLI usability + correctness patch (client-only; the daemon and wire protocol `acp/1` are unchanged).
- **Progressive-disclosure help.** `acp help` is now a short, task-grouped overview; `acp help <topic>`
  (comms, files, crdt, leases, admin, config, update) drills in with verbs, flags, and examples;
  `acp help all` keeps the full listing. The `crdt` topic documents the structured-CRDT (ext-5) op
  discipline — `set`/`del`/`lins`/`ldel`, array-of-segments paths, and "use list ops for arrays,
  never a whole-array `set`" (a whole-array replace silently loses concurrent element edits).
- **Clearer sync messages.** A stale-but-non-overlapping push is now labelled `STALE (run 'acp pull'
  to reconcile)`, distinct from a true `CONFLICT` (an in-place `<<<`/`===`/`>>>` marker overlap you
  must resolve). The conflict hint now tells you to `acp push --force` after resolving.
- **Lease-token cache fix.** The remembered fencing token now lives under the ACP home
  (`$ACP_HOME` or `~/.acp/leases/<ns>.json`) instead of a CWD-relative `.acp/`, so `acp lease
  renew`/`release` work from any directory (previously a different CWD than `acquire` returned
  "not holder").
- Docs: the shared-filesystem guide's conflict mechanics corrected (in-place markers vs `.remote`).

## v0.1.1 — 2026-07-13
- Capability negotiation: `/v1/healthz` now returns a `capabilities` array so clients can feature-detect instead of trial-and-erroring requests. This release advertises `batchevents`, `channels`, `crdtjson`, `quotas`, `scopedtokens`.
- Channels with wildcard topic filtering (`channels`) — publish/subscribe on named channels with `*`-style topic filters (RFC ext-1 §4, accepted).
- Batch event append (`batchevents`) — append multiple events to the log atomically in one call (RFC ext-4, accepted).
- Structured JSON CRDT (`crdtjson`) — daemon-materialized JSON documents with path-addressed get/set/delete, alongside the existing text CRDT (RFC ext-5, accepted).
- Scoped tokens and per-space quotas (`scopedtokens`, `quotas`) — mint narrower-than-admin grants and set per-space resource limits (RFC ext-7 §4/§5, accepted).
- New CLI verb: `acp update [--check] [--force] [--version vX.Y.Z]` — self-updates the installed binaries from the release channel, verifying checksums before swapping them in; follows `releases/latest.txt` by default or pins an older retained release.
- Built with Go 1.25.12, clearing the stdlib CVEs present in older Go toolchains.
- Published RFC proposals for future extensions: presence/awareness (ext-9), CRDT undo/redo (ext-11), per-document schema validation (ext-12), and outbound webhooks (ext-13). All are marked PROPOSAL — not implemented, not shipped.
- Wire protocol unchanged: still `acp/1`; extensions remain additive and capability-negotiated, so v0.1.0 clients keep working unmodified against a v0.1.1 daemon.

## v0.1.0 — 2026-06-10
First public release.
- Shared filesystem (content-addressed push/pull + 3-way merge) and CRDT live co-editing.
- Comms: directed mailbox + totally-ordered event log; fencing-token leases.
- Multi-tenant spaces; per-agent identity + roles (admin/writer/reader).
- High availability: 3/5-node Raft cluster with automatic failover.
- mTLS between cluster nodes (cluster CA) and AES-256-GCM blob encryption at rest.
- MCP bridge (acp-mcp) and a Go SDK.
- Typed client config with profiles (~/.acp/config.json).

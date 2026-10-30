# Changelog

## v0.2.5 — 2026-10-30

Catch up on what happened in one screen (`acp catchup`), read the activity log as plain lines, wait for something to
happen instead of polling, a guide to how agents work as a team, and fixes that keep revoked logins revoked,
workspace history from being erased and live-text edits from being lost in a cluster. Wire protocol `acp/1` is
unchanged apart from additions (`dropped` / `pending` on a live-text push, `note` on per-file history) and one
stricter check (a live-text insert carries exactly one character).

**Upgrade notes:**
1. **Cluster: upgrade EVERY node before the first live-text push after upgrading.** This version raises the replicated
   command version to 6 (a delete of a teammate's text is now checked when the push is applied, not before). An older
   node that receives a version-6 command stops on purpose instead of applying it differently from its peers; an
   upgraded node applies an older leader's commands exactly as that leader's other nodes do, so upgrading the
   followers first, then the leader, is safe. A node that stops says so in its log (`OPERATOR ACTION`) and starts again
   once it runs this version.
2. Nothing else changes on disk: documents, histories and snapshots from v0.2.4 are read as they are.
3. **`coordd -mkagent` / `-rmagent` / `-hash-agents` now refuse while the server is running on that data folder**, and
   say how to do it live (`POST` / `DELETE /v1/admin/grants`, `acp invite`, `acp invites remove-member`). A script
   that revoked offline and then sent SIGHUP must switch to the grants API, or stop the server, run the tool, and
   start it. (The one exception is the documented first step: a running single-node shared-key server may still get
   its first login with `-mkagent`, then restart.)
4. **A workspace with events can no longer be removed by accident** (`DELETE /v1/admin/spaces` answers 409 naming
   `events=N`), because removing it erased its activity history. If events are all it holds and you mean to discard
   them, repeat the count: `acp admin spaces drop <name> --confirm <name> --discard-events N` (API:
   `&discard_events=N`). In a cluster this rides the same command version 6 as note 1.
5. **Cluster:** right after an election the new leader answers writes `503` with `Retry-After: 1` until it has
   applied the log it inherited (normally milliseconds; longer after a long outage, and for as long as a leader is
   cut off from the rest of the cluster — its log then says "leader readiness: still applying …"). Reads, health
   checks and single-node servers are not affected. Clients that retry a 503 need no change.
6. A server that crashed leaves its `LOCK` file behind but not the lock, so the offline tools in note 3 work on it
   without any cleanup; they refuse only while a coordd process is actually running on the folder.
7. **`-hosts` now adds to the automatic certificate names** (see below). Only a NEW data folder makes a certificate,
   so running servers are unaffected; use `-hosts-exact` for the old "only these names".

**What you may notice right after upgrading:**
- MCP agents: `acp_recent_events` now answers in readable lines, not JSON. Ask for `format: "raw"` to get the exact
  events as JSON lines, as before.
- `--json` and `acp watch` (raw NDJSON) are unchanged. The plain `acp events` line keeps its leading fields and now
  ends with ` · who · when · "note"` (a script reading the leading fields is unaffected). `--raw` used to be ignored;
  it now means `--json`. `--since`, `--actor`, `--path`, `--kind` and the others used to be ignored too; they now
  narrow the output.
- `acp catchup` writes a small private file under `~/.acp/cursors/` (or `$ACP_HOME/cursors/`), one per server,
  workspace and agent. Delete it to start over.

### Added — catch up, read the log as a story, wait for something to happen
- **`acp catchup`: what happened since you last looked, in one screen.** Files changed (who, how often, the last
  `--note`), claims taken, released and expired, who joined, posts, live documents, mail between others (subjects
  only) and your unread mail, and what is open now, each with the exact command for the rest. It remembers where you
  stopped (a small private file under `~/.acp/cursors/`, one per server, workspace and agent) and starts there next
  time; `--peek` looks without moving your place, `--mark` moves it without looking, `--since-seq N` or `--since 2h`
  is a one-off look. If you were away longer than the log keeps, it tells you how much is gone and shows which files
  changed instead. It counts and groups; it never summarises. MCP agents get the same as `acp_catchup`.
- **`acp events --story`: the log as readable lines.** One line per event in plain words (who, what, which version,
  and why: the commit note is looked up for file changes), runs of the same thing collapsed into one line with a
  count, the newest 50 lines by default. Housekeeping is hidden and counted (`--all` shows it); `--no-group` gives one
  line per event. A teammate's post that copies an action only the server writes (say `file.edited`) is marked
  `(says)` instead of being shown as fact. The plain `acp events` line stays the default and `--json` is unchanged;
  `--raw` is another name for `--json`, and `--plain` keeps the plain line.
- **Narrow the log to what matters:** `--actor`, `--not-actor me`, `--path src/`,
  `--kind files|claims|mail|docs|people|notes|system|housekeeping`, `--since <seq|2h|3d|last>` (`last` = after your
  catch-up mark), `--before N`, on `acp events` and `acp watch`. `acp events --limit N` now reads only the end of the
  log instead of downloading all of it first.
- **Wait for something instead of checking again and again:** `acp events --wait --kind claims --not-actor me
  --timeout 5m` follows the live stream (no polling), returns as soon as something matches (a burst gets two seconds to
  land), prints it and `--since-seq N` to wait from next, and exits 124 if nothing happens in time (1 if the server
  can't be reached). MCP agents get `acp_wait_events` (up to 300 seconds; other tool calls keep working meanwhile).
- **`acp watch --story`** prints one readable line per event; `acp watch` without it stays raw NDJSON for scripts.
- **Limits to know about:**
  - The `(says)` mark checks file changes against the history the server still keeps, and checks every other
    action only for the fields the server always writes. A teammate who copies those fields exactly can still post
    a convincing fake (for example a "claim expired" line). Only a server-side rule can stop that; one is being
    considered.
  - A file change older than the history the server keeps can't be checked, so it is shown without a mark.
  - The catch-up place is kept per machine: two copies of one agent on two machines each have their own. In a
    sandbox that loses its home folder, pass the number from the last line: `acp catchup --since-seq N`.
  - A catch-up reads every event since your place (up to the 50,000 the server keeps) to count them. Only the
    one-screen digest reaches an MCP agent's context.
  - "Until 04:10" on a claim is when it would end at the time it was taken. Renewals aren't in the log, so
    `acp lease list` is the truth.

### Added — agents working as a team
- **A guide to how agents talk and keep track** ([docs/AGENT-TEAMS.md](docs/AGENT-TEAMS.md)): which tool fits asking,
  announcing, deciding, remembering, keeping a record, claiming and catching up — as suggestions, not rules — plus
  the room message shape ([docs/ROOM-MESSAGES.md](docs/ROOM-MESSAGES.md)). `acp help comms` opens with the same
  table.
- **Notice new messages: `acp inbox --brief`** prints one line about unread mail and nothing when there is none,
  made for a hook or a status line. [docs/AGENT-SETUP.md](docs/AGENT-SETUP.md) shows the Claude Code
  `UserPromptSubmit` hook and status line.
- **Address a post to someone:** `acp log … --to claudeB`; they find it with `acp events --for-me` or
  `acp watch --for-me` (which also match `@claudeB` written in a note, as a whole word).
- **Point at files from a message:** `acp send … --refs plan.md,notes/a.md`.
- **MCP agents can answer inside a conversation and use channels:** `acp_send` takes `reply_to` (fills in the
  recipient and thread), `thread_id`, `corr_id`, `priority` and `refs`; new `acp_thread`; `acp_log_event` takes
  `channel`, `context` and `to`; `acp_recent_events` takes `for_me`.
- **MCP agents show up in `acp who`.** The MCP bridge now sends a heartbeat when it starts, when the host identifies
  itself (the harness shows as e.g. `claude-code`, or set `ACP_HARNESS`), and every 30 minutes.
- **Say why when you change files: `acp push --note "…"` and `acp rm <path> --note "…"`.** The note is kept with the
  new version in the history (it used to always say `push` / `rm`), up to 1024 bytes.
- **Push won't publish a conflict copy.** After a pull that saved `<path>.remote` (both sides changed a new file),
  `acp push` refuses until it's merged and deleted, instead of sharing the other agent's copy as a new file; the
  pull's message now says exactly what to do (merge, delete the `.remote`, `acp push --force`). A `.remote` file that
  is already shared in the workspace is treated like any other file.

### Added — the server tells you more
- **A live-text push now says what it could not apply.** The answer to `POST /v1/crdt/ops` gains `dropped` (ops the
  server did not apply and never will from this push: a delete of a teammate's text it does not hold yet) and
  `pending` (ops stored but waiting for text that has not arrived; they apply by themselves when it lands). Both
  appear only when non-zero, so every other push answers exactly as before and older clients ignore them. Go SDK:
  `PushCRDTOpsResult` returns them (`PushCRDTOps` is unchanged); TypeScript: `pushCRDTOps` returns them typed. See
  protocol spec acp-1 §12.3.1.
- **Per-file history shows why each version exists.** `GET /v1/history?path=` (and so `acp history --path`) now
  carries each version's commit note (`note`), when the commit had one.
- **A cluster node that can't join now says why.** It used to retry twenty times and give up silently; it now logs
  each reason once (for a refused token: "start this node with -token <an admin login's token>, or put it in
  <data>/token") and an `OPERATOR ACTION` line if it gives up. `coordd -h` (`-join`) says which token a joiner needs.

### Changed
- **`-hosts` adds to the automatic certificate names instead of replacing them.** `coordd -hosts acp.example.com`
  on a new data folder used to make a certificate that no longer covered `localhost` or this machine's addresses;
  now it covers those plus the names you list. `-hosts-exact` keeps the old "only these" behaviour. An existing
  certificate is never rewritten, so running servers are unaffected. `coordd -hosts-exact` without `-hosts` now
  refuses to start and says why (it was silently ignored).
- **MCP agents read the event log a page at a time, as readable lines.** `acp_recent_events` used to return
  everything from a seq as JSON, which could pour thousands of events into a model's context. It now returns the
  newest 20 lines by default (at most 200 per call, oldest first), one readable line per event with similar runs
  grouped (`03:52  codexB  edited parser.go (v41): "fixed tokenizer"  #1201`), and ends with a line saying where you
  are and how to go on (`before=` / `since=` to page, `actor=`, `kind=`, `actions=`, `channels=`, `path=`, `text=` to
  narrow, or `acp_catchup`). **`format: "raw"` gives the exact events as JSON lines, as before**, with the same
  limits. `all=true` reads everything, still 200 per page. The old `from=` still works, now as a page start.
- **Plain `acp events` shows who and when, and the note if there is one**, appended to each line
  (`seq=12 note.added docs/plan.md · alice · 2026-10-30 09:14 · "ship Monday"`), so the leading fields stay where
  they were. `--json` is unchanged.
- **Commands are grouped by what they're about**, as new spellings (every old command works exactly as before):
  `acp mail send|inbox|reply|read|thread`, `acp events [watch|post|channels|subscopes]`, `acp files
  push|pull|show|rm|manifest|blob`, `acp history [restore|show|checkpoint]`, `acp docs …` (live documents; also
  `acp crdt …`), `acp config link|links|unlink`, `acp invites list`, `acp workspaces`. `acp <group> --help` shows the
  whole group with the older spelling of each command, and the overview lists the groups.
- **Help is easier to find.** A mistyped command suggests the nearest one (`acp inbx` → did you mean "inbox"?), a
  command name given as a help topic points at `acp <command> --help`, and asked-for help (`acp help`, `--help`) now
  prints on stdout so `acp help | less` works (usage errors still go to stderr). The overview now lists `status` and
  `link`.

### Fixed
- **A revoked login can no longer come back.** On a running server, `coordd -rmagent` edited `agents.json` behind the
  server's back, and the server's next login change (an invite joining, a grant made) wrote its own copy over it, so
  the revoked token kept working — and the SIGHUP that was supposed to apply the revoke reloaded it. The offline
  tools now refuse while the server runs (upgrade note 3). The same lock also stops a `-mkagent` racing a brand-new
  server's first start from being overwritten.
- **Removing a workspace no longer erases its events.** A workspace whose only content was its event log counted as
  empty, so `DELETE /v1/admin/spaces` deleted that history with it. Events now count as content (upgrade note 4); a
  workspace created by mistake (only read, or pinged) is still removable, since reads and pings leave no event.
- **A delete of a teammate's just-written text is no longer lost after a cluster election.** The server now decides
  whether a delete names text it already holds at the moment the push is applied, in order with every push before
  it — not on the leader beforehand, where its view could still be catching up right after an election and a delete
  of text that had already been accepted was dropped with a success answer. A delete of text that has NOT yet
  reached the server is still dropped (it cannot be told apart from an attempt to erase another agent's future
  text); the push answer now says so (`dropped`) and the server logs how many such ops it dropped per push. This is
  the command version 6 change in upgrade note 1.
- **Cluster:** a newly elected leader no longer judges writes against a view still catching up with its own log (see
  upgrade note 5). Two cluster joins at the same moment can no longer both pass the 7-voter limit.
- **One login on two machines no longer loses a live-text edit.** The CLI and the MCP bridge used to write live text
  under the plain login name, so two machines (or two bridges) on the same login could make identical edit ids, and
  the server kept only the first. Each checkout now uses its own `<login>/<random>` name, kept with its local copy of
  the document; an existing local copy switches to one the next time it syncs and keeps its text.
- **A live-text insert must carry exactly one character (one Unicode code point).** A push like
  `{"t":"ins","v":"Ben"}` used to be answered 200 and stored as `B`; it is now refused with 400 (`an insert carries
  exactly one character`) and nothing in that push is applied. Every official client already sends one code point
  per op (an emoji made of several code points, such as a family or a flag, is several ops, as before). The
  TypeScript engine now replays an old multi-character insert the same way the server does (its first code point),
  so a reload no longer changes the text.
- **The server refuses a commit note over 1024 bytes** (400, naming the limit), the same limit as a checkpoint note;
  it used to keep a note of any size in every version's history. `acp push --note` already stopped at 1024.
- **A thread now starts with its opening message.** `acp thread <id>` (and every client reading
  `GET /v1/mail/thread`) used to show only the replies. You still see only messages you sent or received.
- **`acp crdt sync` and MCP `acp_doc_write` say when the server didn't apply everything** (the new push counts): a
  deletion of text the server doesn't have yet isn't kept, and an edit can wait for earlier text to arrive. Both used
  to look like a normal success.
- **Two documents whose names differ only by `/` vs `_` (`notes/a`, `notes_a`) no longer share one local copy** in
  `acp crdt sync`, which could mix their text. Existing local copies are still found.
- **`acp admin spaces` lists events like any other content** (a workspace with events can't be dropped), and says a
  workspace this node hasn't opened since it started is unknown, not empty.
- **TypeScript live mount with per-agent logins:** it now asks the server for its login's name before the first live
  write and names its replica after it, instead of after the configured `agent` option (a mismatch made every live
  write a 403). An old, empty text insert now reads as the same character in the TypeScript engine as on the server.
- Our own clients follow the one replica rule (`<agent>` or `<agent>/<suffix>`): the TypeScript realtime mount
  (`<agent>/<hex>`, was a dot) and the Go `crdt-coedit` example. The SDK quickstarts (TypeScript, Go) name their
  live-text replicas by the agent id (`alice/editor-1`), as the server requires since v0.2.4; copied as written they
  used to get a 403.
- **Go SDK: reading a document or thread whose name has `&`, `+`, `#` or a space now reads the right one.** The name
  was put in the request unescaped, so `notes & ideas.txt` read back as a different, empty document (writes were
  never affected). The TypeScript SDK was already correct.
- **A value that looks like a flag is a value.** `acp … --note "--force"` used to also switch on `--force`, and
  `acp rm a.md --note why` would have treated `why` as a path; the word after a flag that takes a value is now always
  that value.
- **`acp` refuses a `--profile` placed after the command** instead of silently ignoring it.
  `acp send bob --profile carol` used to run under your default profile with no warning; it now says to put
  `--profile` before the command.
- **Smoother first steps, from watching new agents get started:** `acp pull ./shared` makes the folder if it isn't
  there; a message cut short in `acp inbox` says `full text: acp read <id>`; JSON output shows `<`, `>` and `&` as
  written (it used to show `>`); `acp reply <agent>` says when that agent has other unread messages you may not
  have seen; reading mail between other agents says it's private to them (instead of "no such message");
  `acp reply` to someone who never wrote to you says how to start; `acp history <path>` shows that file's history.
- **`acp status` explains an unreadable `token_file`** (which file, and how to fix it) instead of stopping with the
  error.
- **Server messages are cleaner:** no internal reference codes in log lines, the server database edition's errors no
  longer name its storage library, and the start-up hint reads `https://<this machine's address>:<port>`.
- The shared-key comparison is constant-time (it returned early when the lengths differed).
- The lease fence-token cache under your client home is now private (0600 in a 0700 folder; it was readable by other
  users of the machine when the client home was first created by a lease command).
- **The live-text size the server keeps for each document could be one character too high per delete that arrived
  before the text it deleted.** That number decides when a document's history is tidied (compacted), so such
  documents were tidied later than they should have been; the stored text itself was always right, and the
  TypeScript engine already counted correctly.

### Corrections to the v0.2.4 notes
- `GET /v1/admin/spaces` knows what a space holds only if the answering node has it loaded (`"open": true`); a space
  not used since the server started shows no `empty`/`content` — that means unknown. `DELETE` always checks for real.
- `-max-spaces-per-grant` counts the spaces created through one server process, in memory: a restart starts it
  again, and in a cluster each node counts its own. For a hard bound, give the grant a space list. `coordd -h` says so.
- Rolling back to v0.2.3: logins made on v0.2.4 (a new folder's `owner`, grants, invites) exist only as hashes and
  can't be carried back; mint them again with the v0.2.3 binary ([docs/MIGRATING-v0.2.3-to-v0.2.4.md](docs/MIGRATING-v0.2.3-to-v0.2.4.md) §8).
- The public start page's certificate sentence now also holds behind your own HTTPS proxy or edge.
- SIGHUP re-reads the logins in `agents.json` but not invites: invites are managed live (`acp invite`, `acp invites`).
- A node joining a cluster that uses per-agent logins needs one of its admin logins' tokens
  ([docs/OPERATIONS.md](docs/OPERATIONS.md) §2c).

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

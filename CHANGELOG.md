# Changelog

## v0.2.6 — 2026-11-06

Agents can now work together without tripping over each other: `acp help team` is a one-screen menu of the ways to
co-edit, claim, see and agree; `acp who` shows who holds what; and a clash names the teammate and gives the next
command. `acp://` addresses work end to end in the CLI, the MCP bridge, the Go and TypeScript SDKs and the mount, with
one-step signed share links (`acp share`). `.acpignore` keeps files out of the share. There is a guide and a tested
example for using ACP from a web page. The server describes its whole API at `GET /openapi.json` and says which version
it runs. Many fixes stop lost work in folder sync, the mount and live documents. Security: six write routes now accept
only `POST` (a reader login could write through `GET`). ACP is licensed Apache-2.0 from this release. The wire
protocol is still `acp/1`: this release adds fields and routes and changes some behaviour, each listed below with an
upgrade note.

**Upgrading from v0.2.5? Read [docs/MIGRATING-v0.2.5-to-v0.2.6.md](docs/MIGRATING-v0.2.5-to-v0.2.6.md) first.**

### Docs
- **`docs/BROWSER.md` fills the gaps a first-time page builder hit**: the room-message shape inline (with the post and subscribe code), a table of which `/v1` routes each page feature needs, a `flush()` / `hasPending()` snippet, the file-mount address explained (workspace files sit at the root; the host in the address is your backend's and is never contacted), and what a backend does when the invite was already redeemed.
- **The `acp-client` skill now points to the new `acp://` guide** and is brought up to date (invites, `acp status`,
  history, the structured-document rules and an `acp://` section).
- **New guide `docs/ACP-ADDRESSES.md`: "acp:// addresses: a link to your shared folder"**: what an address is and why you
  would use one, how to get your workspace's address, how to read, write and share through one (agent and human
  examples), signed share links, the common errors with their fixes, and why an address never carries a login.
- **`docs/API_REFERENCE.md` now matches the server** (checked section by section against the server's own
  `/openapi.json`): the token-free endpoints are listed; `healthz` shows `version` and `limits`; `GET /v1/spaces` lists
  every space on disk within your grant; `POST /v1/crdt/ops` returns `epoch`/`dropped`/`pending` and a new document
  over the limit is `507`; the document list paging (`limit`, `cursor`, `X-ACP-Next-Cursor`, `sizes=0`), the two
  document `DELETE` calls, the `mail/ack` `404`, the commit `428`/`403` large-change refusals, `409 wrong_fs_mode` and
  the 1024-byte note limit are documented; new sections cover grants, spaces, invites, the realtime filesystem and the
  start page / `/openapi.json`; quota, cluster-status, `whoami` and the lease-token wording were corrected.
- **The `acp://` specification (ext-32) now states two rules every client already follows.** In a link's query, `&`
  is the only separator: a share link built from a signed download URL whose query holds a `;` is refused, and inside
  an `acp://` address a `;` is just a character (so `?v=1;foo=2` is one bad version pin). And the query names that
  would carry a login (`token`, `password`, `api_key` and the others listed) are refused in any capitalisation,
  including Unicode letters that fold to the same name (`toKen` with a Kelvin sign, `paſſword` with a long s), while
  look-alikes such as `tokens` are ignored. Both rules are pinned by the shared Go and TypeScript test vectors.
- **The mount specification (ext-37) now describes an editor's safe save on a live text file**: renaming a plain file
  over a live text document writes its text into the document instead of replacing it (see the TypeScript fix below).
- **New guide `docs/BROWSER.md`: "Using ACP from a web page"**: the shape that works today (a backend on the page's
  own site holds one ACP login per person; the page holds no token), what that backend must do, how to wire the page
  (one shared stream, live documents, files, chat, mail, presence), and eleven rules that keep people's work safe in a
  tab with no disk (nothing is saved until the server says so, a change based on an old view never overwrites a newer
  one, browser storage is a cache, big deletions need a clear yes, and more). `docs/OPERATIONS.md` gains a "Browser
  front door" recipe and `docs/THREAT_MODEL.md` a "Web pages" section.
- **New example `examples/guides/browser-backend/`**: a dependency-free Node backend that logs people in with an ACP
  invite (redeemed by the backend), keeps each person's login server-side, forwards an allowlist of routes (never the
  admin routes) and replaces the page's credential headers; a page built on the SDK's browser bundle; and a
  self-checking `run.sh` where two "tabs" and a command-line agent chat, mail, co-edit notes and a board, and share
  files through it, plus 12 named tests of the backend.
- **Landing page (`ui/landing.html`)**: the hero now says plainly that agents on different computers need to be able to
  reach the computer running ACP, and the small grey footer text is darker so it meets the 4.5:1 contrast on every
  background in the light theme.

### Added
- **A log id tells a server restored from a copy apart from one that is behind.** `GET /v1/events` and its stream
  answer with `X-ACP-Log-Id` (`<space part>.<epoch>`) and `X-ACP-Log-Head`, and `GET /v1/stats` with `log_id`. The
  epoch is new at every server start and with every new cluster leader. A node shows the id only for an epoch it can
  vouch for (a single node: minted since its start; a cluster node: minted in its current term by a leader it has
  heard from) and otherwise sends none (unknown), so the epoch that comes back with restored data is never shown: a
  restored history shows no id, then a new one, even after new events have passed the old head. When a node's
  epoch changes, every open event stream on it ends, so a follower reconnects and reads the new id, also one whose
  stream opened while the id was absent. A cluster leader answers writes only once its epoch has applied, and a
  node keeps its epoch across a crash. `GET /v1/events` takes `limit`. The same id with a lower head is a node that is behind. Clients use
  it from this release; an older server sends no id (unknown).
- **Changes behaviour:** **`acp rm` takes a folder.** `acp rm __pycache__` removes every shared file under it, in one version. A folder of 10 or
  more files (the workspace's bulk minimum) asks first, or needs `--confirm-bulk`, however small a part of the workspace
  it is. `acp rm .` is refused. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#cli-rm-folder))
- **`acp push` says when it is sending build or cache output** (`__pycache__/`, `node_modules/`, `venv/`, `*.pyc`), with
  what to add to `.acpignore` and the `acp rm` that takes it back off the share. It is a note: the push goes ahead.
- **Go SDK: `pkg/merge`.** `merge.Union(shared, local)` joins two texts with no common ancestor: both stay whole.
- **`acp help team`: one screen on working together.** Co-edit, claim, see, agree, finish: a menu of tools with the
  exact commands, in no required order. The invite paste has one line pointing at it (`Working with others: acp help
  team`), and so do `acp help`, `acp status`, the help for `comms`, `leases`, `files` and `crdt`, the clash refusals, and
  `acp team` / `acp claim` when someone tries those. `acp help leases` now says what a `file:<path>` lease does.
  From the tools, the `acp_lease` description teaches the same.
- **Changes behaviour:** **The tool teaches at the moment two agents clash.** `acp who` ends each agent's row with what it holds and the time
  left (`holds file:notes.md (10m left)`), and lists a holder not seen lately; it shows no fence token. A push, put, rm or
  restore of a file under someone's lease now says who holds it and until when, with the commands to ask them, to wait
  until it ends (woken by the release or the expiry, no polling) and to see every claim. `acp lease acquire` of something
  held says the same on standard error; the `HELD:` line and `--json` are unchanged. A stale push, a stale `acp put` and
  a pull conflict add the exact commands for that file to claim it or co-edit it next time (no co-edit offer for a binary
  file). Each printed command keeps your `--profile`, `--space` or address and quotes names. MCP: `acp_who` gains `holds`;
  `acp_lease`, `acp_file_put`, `acp_commit`, `acp_restore` and `acp_write_address` answer the same way, naming tools. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#cli-who-rows))
- **Changes behaviour:** **A `503` says when it is safe to send a request again.** Right after a cluster election, with no leader, or on a
  local storage fault, the server refused before doing anything, and the error now carries `retry_safe: true`. A write
  that timed out waiting for the cluster has no such mark, because it can still land. Older clients ignore the field. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#statuses))
- **Changes behaviour:** **`acp invite --name <existing agent> --space <another>` now adds the workspace to that agent.** It used to fail with
  "already a member". The paste says so, the agent runs `acp join` where its login lives, and it uses the new workspace
  with the token it has (`acp --space proj2 …`): no new profile, no new token. `acp join` sends that token only to the
  server address and certificate its profile names. An older `acp` is told to update. `acp invites remove-member` takes
  that workspace away and the login keeps the others. `acp_whoami` lists the workspaces of a login that has several. Go:
  `Invite.AddMember`, `RedeemRequest.AsMember`, `Redeemed.Added`, `RemoveInviteMemberDetail`. TypeScript:
  `redeemInvite(req, { asMember: true })`, and `Redeemed.token` is now optional. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#invite-existing))
- **`acp`, the mount, `acp-mcp` and both SDKs ride out a short cluster election by themselves.** On a `503` with
  `Retry-After` they send the request again, up to 3 sends in all, when that is safe: a read, a request with an
  `Idempotency-Key`, or one the server marked `retry_safe`. A plain write with no mark is never sent twice, and a `503`
  with no `Retry-After` needs an operator. Go: `(*APIError).RetrySafe()`. TypeScript: `err.retrySafe()`.
- **One agent, one name, more than one workspace.** `acp invite --name codexB --space proj2` used to refuse a name that
  already exists. Now it makes an invite that adds `proj2` to codexB's login: same name, same token, same role. Only
  codexB's own login can redeem it, so the code alone gives nothing to anyone else. The server answers `409` if the
  member is already there or can use every workspace, or if the invite offers other terms than its login has (one
  login has one role and one set of folder limits). Removing a member through an invite now takes away that invite's
  workspace and leaves the others; the last one ends the login, as before.
- **Logins that end, from the command line.** `acp invite --grant-ttl 90d` ends each joiner's login that long after they
  join (`--ttl` is still how long the invite stays open). `acp join`, `acp status`, `acp whoami`, `acp invites` and the
  `acp_whoami` MCP tool show the end. Against a server older than v0.2.6, which cannot end a login, `acp invite` takes the
  invite back and says so, rather than give out a login that never ends. An ended login now says "this login expired at
  ...; ask an admin for a new login, retrying cannot help" everywhere, and `acp status` no longer calls that "can't reach
  the server". `acp watch`, `acp awareness watch` and `acp crdt watch` stop with that message instead of reconnecting for
  ever. In the Go SDK: `InviteRequest.GrantTTLSec`, `Invite.GrantTTLSec`, `Redeemed.ExpiresAt`, `Grant.ExpiresAt`,
  `APIError.GrantExpired()`, `IsGrantExpired(err)`; `FollowForever` returns that refusal, `Subscribe` tells each subscriber
  once through `OnError` and stops. A mount's event follower stops too, and a save refused this way stays pending.
- **`.acpignore`: keep files out of the share (ext-43).** Put a `.acpignore` at the top of a synced folder, with
  gitignore patterns (`__pycache__/`, `*.log`, `/build`, `!keep.log`, `**/gen`; no `[...]`). An ignored path is outside the
  sync both ways: push never uploads it and never reads its absence as a deletion, pull never fetches, overwrites or
  deletes it, and no flag changes that. The file is shared like any other, so one line reaches the team; add `/.acpignore`
  to keep your rules to yourself. There are no built-in patterns. A file acp can't read stops push and pull and names
  the line. Push and pull show what was ignored; push says when an ignored path is still in the workspace (it stays
  until you `acp rm` it). Without the file nothing changes.
- **`acp push --dry-run`** says what a push would add, edit and delete, and refuses what the push would refuse. It sends
  nothing and changes nothing. (The server's own large-change check needs the real push.) Before this the flag was
  accepted and a real push ran. `acp pull --dry-run` is refused rather than running a real pull.
- **`acp ignore check <path>`** says whether a path is ignored and which line decided it, with no connection needed.
  It exits 0 when the path stays out of the sync and 1 when it is in, like `git check-ignore`.
- Go SDK: `pkg/acpignore`, the matcher, with the shared test vectors.
- **TypeScript SDK: logins that end.** `whoami()` shows `expires_at`. New `createGrant` and `grants` (with `ttl_sec` or
  `expires_at`), and `createInvite`, `invites` and `redeemInvite` (with `grant_ttl_sec`). When a login has ended you get a
  `GrantExpiredError` that says to ask for a new login: `followForever` and `subscribe` stop instead of reconnecting for
  ever, and a mount fails with `EACCES` and keeps your writes pending. Following a brand-new workspace from the start no
  longer reports a false history gap.
- **A folder delete can say "only if it is still empty".** `POST /v1/fs/ops` takes `"if_empty": true` on a delete. If
  a teammate's file landed in the folder first, the whole batch answers `409 folder_not_empty` and nothing changes, so
  their file is never moved to the trash along with the folder. It is decided at the server, where the order is known.
  Clients use it for rmdir and for renaming over an empty folder.
- **A web page can read the public documents.** `/v1/healthz`, `/.well-known/acp` and `/openapi.json` send
  `Access-Control-Allow-Origin: *`, with no credentials, so connect-by-address and API explorers work from any site.
  Every other route stays same-origin, and `/openapi.json` stops being public when `-root-page=false` puts it behind a token.
- **`/v1/stats` says what `event_base_seq: 0` means.** New `event_log`: `empty` (never had an event), `compacted` (had
  events, none kept, seqs carry on from `events`) or `retained`.
- **Temporary logins.** A grant can now end: `POST /v1/admin/grants` takes `expires_at` or `ttl_sec`,
  `coordd -mkagent NAME -expires-in 24h` does the same offline, and an invite takes `grant_ttl_sec` so each joiner's login
  ends that long after they join. Past its end a token answers `401 grant_expired` everywhere and its open streams close
  on the spot. `GET /v1/whoami`, the grant list and `-lsagents` show the end. A login with no end works as before. A data
  folder that uses this is written as `agents.json` version 7, which older servers refuse, so a temporary login can never
  become a permanent one after a downgrade; remove the temporary logins first if you need to go back.
- **TypeScript SDK: get a deleted document back.** `deleteDoc`, `docTrash` and `restoreDoc` (and `deleteJSONDoc`,
  `jsonDocTrash`, `restoreJSONDoc` for structured documents) delete a live document, list what can come back, and restore
  it. `docVersions` and `docVersionAt` read the text a server tidy-up removed from a text document, whole or at any
  operation. An older server answers with `NoDocUndoError`. (Not published to npm yet.)
- **The Go SDK can follow events for you, like the TypeScript one.** `FollowForever` reconnects and resumes by itself,
  and `OnResync` tells you when the events you missed are gone (retention removed them, or the server's history went
  back), before the next event arrives. `Subscribe` runs many subscribers over one shared connection, each with its own
  filter and starting point, with nothing delivered twice.
- **Bring back a deleted document, or read an earlier version of one.** New commands: `acp docs delete <doc>`,
  `acp docs trash`, `acp docs restore <doc> [--gen N]` and `acp docs versions <doc> [--epoch N] [--at M]`, plus
  `acp docs json delete|trash|restore` for structured documents. An agent has the tools `acp_doc_trash`,
  `acp_doc_restore` and `acp_doc_versions`. Go SDK: `DeleteDoc`, `DocTrash`, `RestoreDoc`, `DocVersions`,
  `DocVersionAt` and the structured twins. A server older than v0.2.6 answers with a plain "this server does not keep
  deleted documents".
- **New `coordd -data-must-exist`** (off by default): refuse to start when the data folder holds no server state.
  An unmounted data volume or a mistyped `-data` looks exactly like a first start, so without it the server comes up
  as a brand-new empty server with a new owner login. Turn it on in a service or container once the server exists.
- **Server-database edition: the server says so before converting stored data in place** on its first start after
  an upgrade, and the upgrade notes now ask for a backup first (a stopped copy of `-data`, or `coordd admin dump` with
  the previous binary).
- **TypeScript SDK: a client with no token.** `new Client({ baseUrl })` (no `token`, or `""`) sends no `Authorization`
  header at all — for a web page behind a backend that adds the person's login. It used to send `Bearer undefined`.
  `agent` is optional too.
- **TypeScript SDK: live document sessions.** `TextDocSession` co-edits one text document (the merge runs in the page;
  hand it the editor's text, render `onRemote`) and `JSONDocSession` one structured document (the server merges;
  `apply()` path ops, read the value back). Both follow the client's one shared event stream, with a slow poll as a
  fallback. The text session keeps the rules every ACP text client follows: edits are diffed against the text the user
  was looking at (pass `{ base }` from an editor that renders late, and a teammate's change that arrived in between is
  kept); unacknowledged edits are re-sent unchanged; after a server tidy-up they are carried over, never re-diffed; and
  wiping almost all of a shared document needs `{ confirm: true }`.
- **Changes behaviour:** **TypeScript SDK: "unsaved changes" signal.** `mountVirtualFS(...).hasPending()` is true from the first buffered
  change until the server acknowledged it — including while a flush is on the wire, when `status().pending` already
  reads 0 — and `onPending(unsaved, status)` reports each change, so a page can warn before it closes. The document
  sessions have the same `hasPending()` / `onPending`. (These TypeScript additions are not published to npm yet.) ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#ts-has-pending))

### Changed
- **Changes behaviour:** **In a cluster, a v0.2.5 node stops as soon as a v0.2.6 node leads.** A v0.2.6 leader
  replicates its log epoch (the log id) at once, so a node still on v0.2.5 stops then, not at the next write. Upgrade
  the followers one at a time and the leader last, and keep the leader running until a majority is upgraded; if the
  older nodes do stop, start them on v0.2.6 and nothing is lost.
  ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#cluster-order))
- **Changes behaviour:** **MCP bridge: a file that is not UTF-8 is not shown as text.** `acp_file_get`, `acp_blob_get`
  and `acp_read_address` answered such a file with every bad byte replaced by U+FFFD, and an `acp_file_put` of that text
  then landed over the file with no conflict. They now refuse it, say so, and record no view, so a put can't land unseen.
  ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#mcp-not-text))
- **Changes behaviour:** **Go SDK: no call waits for ever.** Every one-shot call ends with a `*client.TimeLimitError`
  when no response headers come within 60 s of the request being sent, or the body stops for 30 s; streams are not
  bound. `Client.SetTimeLimits` changes both, 0 turns one off. The TypeScript SDK has the same defaults.
  ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#go-time-limits))
- **Changes behaviour:** **Go SDK, `acp watch` and the Go mount: a server restored from a backup is never taken for
  the same history, and a restart skips nothing.** The server names its event log, and the name changes at every start
  and with every new cluster leader. On a new name, a follower checks the event at its own position (its seq and the
  `EventHash` it kept): the same event is the same history (a restart, a leader change), and it goes on with nothing
  skipped and nothing said. Another event, or none, is a restore: `FollowForever` and `Subscribe` tell `OnResync`
  (`GapRestored`, `"restored"`: the old position, both names and the new head) and go on at the new head; with no
  `OnResync`, `FollowForever` stops with a `*client.LogChangedError` (a subscriber gets it through `OnError`) where it
  went on with another history's events as if they were the ones it had. `acp watch` says `resync:` on stderr, with
  where it was and where it goes on. A mount re-reads what it holds and follows from the new head, and one that can't
  ask which log the server is on waits instead of following blind. A head below what the follower has seen, under the
  same name or none (a node that is behind, or a restore not yet named), never serves an older tree: a mount refuses
  reads (EIO) until the log is back where it was, and a stream waits. `acp wait` and `acp inbox --wait` (and the MCP
  `acp_wait_events` and `acp_inbox` waits) no longer wait past what a restored server records next, and never take
  history from before they began as new. `acp crdt watch` pulls its document again. `OnStreamOpen` (on
  `FollowOptions` too), `LogID` and `LastEvent` keep the name and the event with a position you store; `LogPosition`
  reads the head and its name from one answer. The TypeScript SDK has the same rule and the same hash. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#go-log-id))
- **Changes behaviour:** **Go SDK and mount: the follower's rule is written down and shared.** A stream that names no log while one is held waits on a lower head instead of
  delivering events again; the first log id with a lower head is `GapBehind`; a `"restored"` gap's `From` is the event
  after the last one checked; an event check answered by a node that is behind decides nothing. A Go mount refuses
  reads (EIO) while it can't tell which log the server is on, and clears "behind" only on
  proof from the log it holds. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#go-follow-model))
- **Changes behaviour:** **Go mount: a barrier's report lists `Merged` and `Remade` documents.** A document whose
  record the bounds took is saved as a first contact (nothing deleted) until a save of it lands, a read since
  included; a save over a deleted document with nothing at its name makes it again and says so.
  ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#go-mount-report))
- **Changes behaviour:** **`acp pull` stops before removing most of the folder.** A pull that would remove 10 files or more
  and at least half of the folder, or that finds the server's history no longer holds what the folder synced, exits
  non-zero and changes nothing; `--allow-bulk-delete` lets it through. Removed files are kept in `.acp/trash/` for 7 days.
  ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#cli-pull-stop))
- **Changes behaviour:** **`acp lease list` lines say how long is left**: `(ends in 4m)` or `(ended)`, after the same
  `expires=<unix seconds>`. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#cli-lease-list))
- **Changes behaviour:** **Commits on the file-storage edition wait until their rename is on disk**, about 4 ms more on a
  cloud disk, so a crash right after an answer can't lose the commit. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#slower-commits))
- **Changes behaviour:** **`acp push --force` stops on a file holding conflict markers.** `--allow-markers` sends it
  anyway. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#push-markers))
- **Changes behaviour:** **Go SDK: a redirect to another host or port is refused**, on every call but the live awareness
  stream, which follows a cluster follower's redirect to its leader. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#go-redirect))
- **Changes behaviour:** **The MCP address tools use only the bridge's own login**, never another profile on the
  machine; a second server needs a second bridge. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#mcp-address-login))
- **Changes behaviour:** **Go mount: a save over a live document with no read first adds to it.** `cp draft.md
  /mnt/team/live.md` used to replace the shared text; it now adds its text and deletes nothing, and the mount logs it.
  Read the file first to replace text, or mount with `--allow-bulk`. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#mount-save-no-read))
- **Changes behaviour:** **Go mount: a conflict leaves your version as a file beside theirs** (`<name>.conflict`,
  logged in `.acp/conflicts.jsonl`), and the save succeeds; `--conflict=error` brings back the failing save.
  ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#mount-conflict-sidecar))
- **Changes behaviour:** **TypeScript SDK: `subscribe` with no `from` starts at the log's head**, and calls `onError` once
  per outage when a stream keeps failing. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#ts-subscribe-head))
- **Changes behaviour:** **TypeScript SDK: a failed `flush()` of a realtime mount carries the barrier's report** on
  `err.detail.report`. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#ts-flush-report))
- **Changes behaviour:** **TypeScript SDK: the package ships no source maps.** ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#ts-no-source-maps))
- **Changes behaviour:** **`acp who` lists the newest first and says what it is.** It is everyone seen in this workspace lately, on any job, so
  a name your job never mentioned is nothing to wait for. A note on stderr says so and points at `acp catchup`. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#cli-who-rows))
- **Changes behaviour:** **The note on `acp send` says what it knows.** It said "you have not read" about mail you had just seen with `acp inbox
  --wait`, and pointed at a command that clears nothing. It now says the mail is still marked unread, and that `acp read
  <id>` or `acp reply` marks it read. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#messages))
- **Changes behaviour:** **A refused link and an unreachable server now end with what to do.** An expired, changed or dead link says to ask
  whoever sent it for a new one, with the `acp share` command. A port that can't be says so and to check the address. When
  nothing answers, the CLI, `acp status` and every MCP tool add `Nothing is answering at host:port: check the port, and
  that the server is running`, and a misspelt host gets its own sentence. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#messages))
- **Changes behaviour:** **TypeScript SDK: a refused link or port says what to do, in the same words as the CLI.** `readACPCapability` adds who
  can make a new link to a 410, 403 or 404, and the date to a 410; `err.status` is unchanged. An expired link that gets no
  answer at all says it has expired, with the original error as `err.cause`. A bad port names the range; `err.code` is
  unchanged. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#messages))
- **Changes behaviour:** **A direct message's subject stays between the two of you.** The workspace activity log still shows that a message
  was sent and who sent it to whom, but no longer its subject. Events already in the log keep theirs until retention
  removes them. **On upgrade:** a program that read `context.subject` from `chat.sent` events gets nothing; the sender
  and recipient read the subject from the message itself. `acp events`, the story view and catch-up read as before,
  with the count of messages between others and no subjects. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#dm-subject))
- **Changes behaviour:** **Looking at a workspace no longer creates it.** A mistyped `ACP_SPACE=porj2 acp pull` used to create `porj2` and hold
  one of the server's 1000 workspace slots. Now the first write that stores something creates a workspace (a message, a
  file, a live document, a lease, or an admin setting its mode, quota or an invite), and anything else on a name that does
  not exist answers `404` with code `space_not_found`, leaves nothing behind and holds no slot. A removed workspace stays
  removed. `GET /v1/whoami` still answers (without `bulk`). **On upgrade:** a program that read a new workspace before
  its first write must treat `space_not_found` as an empty workspace; clients older than v0.2.6 stop with that message
  on `acp push` to a brand-new workspace (send a first message, or upgrade the client).
  - **What the clients do with it.** `acp push`, `acp put`, `acp crdt sync`, `acp crdt json set`, the MCP write tools and
    a read-write mount treat a name that does not exist as an empty workspace, and their first write creates it;
    `acp push` says so on stderr, so a mistyped name shows, and `--dry-run` creates nothing. A push, sync or MCP write with
    nothing to send creates nothing either. Commands that only read (`acp pull`, `ls`, `who`, `inbox`, `events`, `stats`,
    the MCP read tools) print the server's sentence and exit non-zero, and `acp pull` leaves your folder alone. `acp inbox
    --wait`, `acp watch`, `acp events --wait`, `acp awareness watch` and `acp crdt watch` answer at once instead of waiting
    on a typo. `acp status` stays exit 0 and says `workspace new1 does not exist yet`. `acp inbox --brief` stays silent, so a
    hook adds nothing. A read-only or `?v=` mount reports the name instead of showing an empty folder; a read-write
    mount starts empty and follows the workspace once it exists. The MCP bridge's heartbeat stays quiet. In the Go SDK:
    `(*APIError).SpaceNotFound()`, `IsSpaceNotFound`, `ManifestForWrite`, `FSModeForWrite`, `PullCRDTOpsForWrite`,
    `EmptySpaceManifest`; `MissingBlobs` answers all of them missing. `FollowForever` and `Subscribe` keep trying, so a
    follower can start before the first write. In the TypeScript SDK: `err.spaceNotFound()`, `isSpaceNotFound`,
    `manifestForWrite`, `pullCRDTOpsForWrite`; a mount and a live document session start empty, a `?v=` mount fails with
    `ENOENT`, and the message of this one error is the server's sentence, with no `acp 404:` in front (Go too). ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#mistyped-space))
- **Changes behaviour:** **`--json` on `acp lease` and `acp send` / `acp reply`, for scripts.** The wording of `acp lease acquire` changed in
  v0.2.5 (`token=N` became `fence token N`) and scripts that read it stopped finding the number. `acp lease
  acquire|renew|release|list --json` and `acp send|reply --json` now print one stable JSON result, with a `status` of
  `acquired`, `renewed`, `released`, `held`, `sent` or `replied`; a held lease still exits 2. The plain first line of
  acquire and renew carries both old spellings (`acquired db token=7 expires=… (fence token 7)`), so a script written
  for either goes on working. New scripts should use `--json`. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#cli-json-lease))
- **Changes behaviour:** **`acp lease` no longer calls the fence token a secret.** It is a rising number; renew and release also check who is
  calling. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#cli-json-lease))
- **Changes behaviour:** **Revoking a login now ends its open streams, and one login can hold at most 64.** A revoked login used to keep
  reading a workspace's activity through any event or awareness stream it already had open, until the socket closed.
  Now every node closes those streams as soon as the revoke reaches it, however it happened (`DELETE
  /v1/admin/grants`, a removed invite member, a SIGHUP reload). An awareness socket gets a close frame saying why, and a
  reconnect gets `401`. Past `-max-streams-per-login` (default 64 on each server, `0` = unlimited) a new stream answers
  `429 too_many_streams` with `Retry-After`; ordinary reads are not limited. `/v1/stats` reports `open_streams`. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#streams))
- **Changes behaviour:** **A mistyped flag is refused.** `acp send --subjct x` used to be ignored and the message went without a subject; now
  nothing happens and the error names the nearest flag. Only a flag no command reads is refused, so a working command
  line stays working. `--to` and `--action` errors now show the command to run. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#cli-flags))
- **Changes behaviour:** **`acp events` and `acp thread` now print readable text by default.** `acp events` shows the newest 50 story lines:
  `--plain` gives the old `seq=` line, `--json` or `--raw` the stored events, and `--from N` still reads everything from
  there. `acp thread` prints the conversation: `--json` gives the old output. A script that reads `acp events` without
  naming a view gets a one-line tip on stderr. `acp watch` is unchanged. **If you parse either, add the flag.** ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#cli-events-text))
- **Changes behaviour:** **`acp invite` prints a new paste.** It names the workspace, says to look around first (`who`, `catchup`), to claim a
  piece before starting, to write agreements where everyone sees them, and to finish with DONE. It came from what new
  agents said they wished they had been told when we watched them get started. The `acp join` command in it is unchanged. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#invite-paste)) ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#cli-lease-list))
- **Changes behaviour:** **`acp who` shows the real harness.** `acp join` and `acp beat` no longer say `unknown`: they use `ACP_HARNESS`, then
  the profile's `harness`, then what they can detect (Claude Code, Codex, Gemini CLI, Cursor), then `cli`. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#invite-paste))
- **Changes behaviour:** **A commit that would make one path both a file and a folder is refused.** `a` beside `a/b` used to land, and then
  every pull of that workspace failed. The commit now answers `400` and names the pair. Replacing a file with a folder
  in one commit still works. A workspace that already holds such a pair stays usable: commits that do not add to it
  land, deleting either side repairs it, and restoring an old version is not blocked. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#refusals))
- **Changes behaviour:** **Reusing an `Idempotency-Key` for a different request is now refused instead of silently dropping the new write.**
  The same key with a different body used to answer 200 with the first result, and the new event or message was never
  stored. It now answers `422 idempotency_key_reused`, writes nothing, and the key keeps replaying its original. A true
  retry still replays, whatever the whitespace or member order. Keys recorded before this version still replay as they
  did. Use a new key for each new request. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#refusals))
- **Changes behaviour:** **A big commit no longer pushes your team's messages out of the activity log.** `-event-retention N` used one count
  for everything, and one commit writes an event per file, so a commit of 50,000 files evicted every earlier message.
  Audit events and conversation are now counted separately: the log keeps the newest N of each, up to 4×N in all, and
  still drops only from the old end, so cursors and `event_base_seq` work as before. A space with `max_events` keeps the
  floor to half that quota, and a space with per-label event quotas has no floor. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#event-retention))
- **Changes behaviour:** **Deleting a live document can be undone, and deleting one that does not exist says so.** `DELETE /v1/crdt/doc`
  (and `/v1/crdt/json/doc`) used to erase the document for good and answered `deleted: true` even for a name that
  never existed (a typo looked like success). The deleted document now goes to a document trash for the workspace's
  history window (`history_ttl_sec`, 30 days by default): `GET /v1/crdt/trash` lists it and
  `POST /v1/crdt/trash/restore {"doc", "gen"}` brings it back (refused with `409 doc_exists` while another document
  has the name). A name with no document now answers `404 doc_not_found`. The trash does not count toward the
  document limit. **Changed behaviour:** a script that deleted names that might not exist must accept 404; document
  names starting with `.acp-trash/` are reserved (`400 reserved_doc_name`). A workspace with history turned off
  keeps no trash. Upgrade every server of a cluster before the first document delete after upgrading. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#doc-delete-404))
- **Changes behaviour:** **Text you deleted from a live document stays recoverable after compaction.** Compaction used to remove the last
  way back to deleted text. Its previous history now stays readable for the workspace's history window, the last 16
  compactions per document: `GET /v1/crdt/versions?doc=` lists them and `?epoch=&at=` reads the text at a point in one;
  write that text back to restore it. These copies use disk until they expire, are not documents, and do not count
  toward the document limit. Structured documents are not covered. Upgrade every server of a cluster first. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#versions))
- **Changes behaviour:** **Changing one workspace limit no longer resets the others.** `PUT /v1/admin/quota` used to replace the whole
  quota, so setting, say, `max_events` quietly put the history settings back to their defaults (30 days / 100
  versions, so older history was then removed) and loosened the large-change protection. Now only the fields you send
  change; send a field as `null` to return it to its default. **Changed behaviour:** a script that relied on leaving a
  field out to clear it must now send it as `0` (unlimited) or `null` (default). ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#quota-merge))
- **Changes behaviour:** **License: Apache License 2.0** (was MIT). From v0.2.6 the binaries, Docker image, SDKs, and the public docs and
  specifications are licensed under Apache-2.0. Releases up to v0.2.5 stay available under MIT, as published. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#license))

### Fixed
- **Two first writes into the same new object of a structured document no longer lose one writer's keys.** Each
  push is resolved against the stored document on the server, and a missing object on its path is created there.
  When two pushes to one document were resolved before either was applied (two writers setting `color.primary` and
  `color.accent` into a new `color` object at once), each created its own object, the last one won the key, and the
  other writer's values were lost with a `200`. The server now takes one push per document at a time, from that
  resolution to the write. The race was older; this release's log id made the first write to a workspace slower,
  which widened it enough to happen every time in the examples.
- **Both mounts: three ways a save could silently delete a teammate's text or lose its own, each with a test and a
  shared table row.** (1) After a save that was merged (saved with no read, so the teammate's text was added to, not
  deleted), one read later, a save from the same old buffer deleted the text the merge had kept, with nothing
  reported; the same after a remount, after the document was made again, and after a save that stood in front of
  another document's text at its name. A merged save now counts as the view its writer had only for the text the
  writer wrote: such a save is merged again (`merged`), or keeps the other text and says so (`kept`), until a save holds
  that text once. (2) TS mount: a read after the server compacted the document released the text an earlier save had
  kept, so the next save deleted it silently though the earlier flush had said `kept`; a compaction now reads as the
  same document, as everywhere else. (3) Go mount: a refused `rm -rf` that ran over a save whose barrier was still out
  dropped that save when the barrier then failed (the file went back to its older text; the only message was the
  `rm -rf`'s EPERM); every delete now records the save it ran over, and the rollback un-does exactly that. Also: a
  record dropped at the mount's hard bound (more than 256 documents with kept text) is now forgotten like any other
  evicted record, so a save after it is a first contact (nothing deleted, `merged`) rather than a refusal; the held
  kept ids stay as a second guard. Rows 46, 57 and 60–66 of the shared table pin all of this in both mounts.
- **Go mount: `--daemon` through a symlinked folder, and a parent that never kills a mounted child.** The parent
  looked for its child's mount under the path as typed, while the kernel lists the resolved one (Linux through a
  symlinked folder; macOS for everything under `/tmp` or `$TMPDIR`), so after 15 s it killed a child that had mounted,
  skipping the child's exit barrier: bytes written through the mount in that window were lost, and the only message
  said the start had failed. The parent now looks for the resolved path too; when it still cannot see the mount it
  asks the child to stop (the child saves what it buffered, keeps what it cannot save beside the mount, and
  unmounts) and shows the child's log; a child that does not stop is left running and named, never killed. On
  macOS `--daemon` is refused with a message until it has been tested on a device (run in the foreground, or under
  launchd). A dead macFUSE mount's `ENXIO` is now read like `ENOTCONN` wherever a stale mount is healed or reported.
- **Go SDK and mount: the log follower and kept text follow one written model.**
  - Kept text is no longer deleted after three faults in a row (a failed push, the document deleted, then
    restored): the refused save keeps what it kept for the document's return.
  - A document whose record the mount's bounds took is a first contact until a save of it lands, a read since
    included, so a save from an older buffer deletes nothing. (One case stayed open until the fix below: after a
    merged save, one read later, a save from the same old buffer could still delete the merged-in text.)
  - A mount lets go of what it held for a document that was deleted or made again.
  - A save made over a deleted document, with nothing at its name, says it made the document again (`Remade`), and a
    first contact says `Merged`.
  - A follower stream that names no log waits on a lower head instead of delivering events again. The first log id
    with a lower head is behind. A restore's `From` covers events from an unnamed stream. An event check answered by a
    node that is behind decides nothing. The event hub's restart mark moves only on checked events.
  - A Go mount refuses reads while it can't tell the server's log (no id after one, a check out or failed), and
    clears "behind" only on proof from the log it holds.
  - A shared table of follower cases is checked by the Go and TypeScript tests.
- **Go mount, SDK and CLI: more fixes.** A follower takes a new log id only once its check passes, a
  stream that names no log never moves the mount's mark, and a check answered by a node that names no log (or
  another) decides nothing and is asked again; the first reconnect after a stream ends is jittered. A save from an
  older view's pick says `kept` on the save chained over it after a failed push; a save whose dropped deletes were
  all deleted by a teammate first says nothing kept. A refused `rm -rf` never puts an older write back over a file
  renamed onto that name, and keeps a write already sent. `--daemon` on macOS checks the mount point without
  `/proc`, waits for this run's own "mounted" line when the point was already a (stale) mount, and names its
  default log within the name limit. A kept-bytes file cut short at exit is removed, not left looking whole. A
  send refused with 501 or 507, or by a TLS check, is "not sent"; a redeem whose earlier try lost its answer keeps
  its key on a 429 or a redirect too. `acp invites remove-member --name` removes a member by name.
- **Go mount: more fixes.** The mount point lock reads its folder's names only, never statting an entry
  (a dead or wedged mount nearby no longer fails or hangs the start before it is healed), works under a folder you
  can enter but not list (a 0711 `/srv/mounts`), takes one lock for a name's NFC and NFD spellings on macOS, and opens
  its file through the checked folder itself. A `--log-file` you name may be a link (`/dev/stderr`); only the default
  log refuses one. `--daemon` on a mount point another `acp-mount` serves says so and exits non-zero, where it printed
  "mounted". A save that keeps the newest view's text through an older view's pick says `kept` again. A refused
  `rm -rf` keeps the writes it ran over on a live (realtime) mount too. Bytes still unsaved at exit are kept beside
  the mount point on the NFS and WebDAV backends too. Tests no longer leave lock files in your `/tmp/acp-mount-<uid>`.
- **CLI and Go SDK: more fixes.** A send whose 503 "can still land" (a cluster commit that timed out),
  or a bare 500, is "may or may not have arrived", never "NOT sent"; only a 503 marked retry-safe did nothing. A redeem
  whose earlier try lost its answer keeps its key when a TLS or pin check then refuses. A `--ttl` too long for a
  32-bit build is refused, never wrapped negative. Followers ask for one event (`limit=1`) when they check a log.
- **Go mount: one mount point has one lock, by any path that reaches its name, and nobody else can plant it.** The lock that keeps a
  second `acp-mount` from aborting a live one was placed by the environment (`XDG_RUNTIME_DIR`, `TMPDIR`) and keyed
  by the path as typed, so a start from cron, `su` or a script, or by a symlinked or differently cased path, took a
  second lock and could abort the live mount, losing its unsaved writes. It is now in `/tmp/acp-mount-<uid>/`, keyed
  by the mount point's folder (links followed, device and inode) and its name as the folder holds it. That folder must
  be yours with mode 700 (one made by someone else is refused, with what to do), the lock file is opened without
  following a link and never truncated, and the `--daemon` log goes there too instead of a predictable name in `/tmp`.
  On macOS the name is keyed case- and Unicode-folded (so two folders whose names differ only in case, on a
  case-sensitive volume, share one lock: a start is refused, nothing is lost). On Linux, under a folder you can enter
  but not list, on a case-insensitive disk, two spellings of a name take two locks; the mount guide lists this and the
  other exceptions (a `/tmp` cleaner, `sudo`).
- **Go mount: bytes still unsaved when a mount goes away are kept on disk.** They are written to
  `<mount point>.unsaved-<UTC time>-<pid>/` beside the mount point (or, if that folder can't take it, this user's own
  `/tmp/acp-mount-<uid>/`, on Windows its cache folder), each file at its path, and the exit log
  names the folder; they were only listed in the log. A script's backup rename (`mv f f.old; gen > f; rm f.old`) whose
  new text the document's truncation guard would refuse now stands as the plain file the script wrote; it waited
  behind `EPERM` with the bytes nowhere on the server. The editor rename's half rule counts only lines with a letter or
  digit, so a file regenerated over the same skeleton (blank lines, lone braces) is not taken for the document.
- **Go mount: kept text stays kept, and is said only while it is there.** A save restored from the trash, or written on
  one that was, kept the text the save before it had left out, instead of deleting it; a save chained on one in flight
  no longer carries that save's choice down the chain (a delete the writer made was silently not made); a save after
  a teammate deleted the kept text no longer reports `kept` again; and a document whose last save holds kept text is
  the last one a busy mount forgets. Past its bounds (256 documents, or 64 MB counting each landed view) the oldest
  such record goes but its kept ids stay: a save whose ops would delete that text is refused (`EPERM`) and kept
  pending until you read the file and save it once with that text (or mount with `--allow-bulk`).
- **Go mount: a refused `rm -rf` keeps the writes it ran over.** A file written and not yet saved, then caught in an
  `rm -rf` the bulk guard refused, went back to its old bytes; it now keeps the new ones. A write refused for buffer
  space (`ENOSPC`) no longer changes the file it was refused for.
- **CLI, MCP bridge and Go SDK: more fixes.** `acp status` with an address shows that address's login
  state and says so when the address does not resolve. A mistyped subcommand of any command suggests the nearest one.
  `acp mount` refuses a `-space` or a leading `--space` that differs from the address. Sending mail says "may or may
  not have arrived" only when the request went out (or a gateway lost the answer), not for an error before sending.
  `RedeemUnknownError` no longer prints the redeem key (a credential), a redeem the caller ends hands the key back, and
  a certificate pin refusal is not retried. The MCP bridge answers JSON that is not a request object with `-32600`
  (`null` included), and a file it refused to show as text is not written on an earlier listing either.
- **Go mount: a save written while the previous one was checking the log no longer doubles the document.** When that
  check failed, the new save had no view to work from and sent its whole text again, so every line appeared twice,
  with nothing reported. It now takes over the views the failed save held.
- **Go mount (CAS): four more ways a lost answer or a refusal went wrong.**
  - `rm` or `mv` of a new file whose create landed with its answer lost now reaches the server; it did nothing, and the
    file came back.
  - A write of a file that a refused `rm -rf` was rolled back over is kept pending when its barrier fails; it was
    dropped while the error said it was pending. (On the realtime mount the matching case for a save whose barrier
    was still out at the moment of the `rm -rf` stayed open until the fix below.)
  - A commit that carried a conflict's sidecar, with its answer lost, no longer makes a second sidecar, and the first is
    logged to `.acp/conflicts.jsonl`.
  - A failed barrier no longer reports its paths as landed.
- **Go mount: a refusal that names a file such as `report-404.md` is no longer read as "node gone".** The mount takes a
  400 or 404 that says "not found", as the TypeScript mount does, and nothing else.
- **MCP bridge:**
  - `acp_fs_ops` counts what a batch trashes on the batch's own view: a move and then a delete of the moved folder is
    counted as the whole folder.
  - `acp_restore` restores a folder's files, and refuses a path in neither version; it used to answer "already match".
  - `acp_send` sends each message with an Idempotency-Key. After a lost answer, `acp_send` and `acp_log_event` say
    that the outcome is unknown, instead of "NOT sent".
  - A request line that is not JSON gets a JSON-RPC error. A line over 16 MiB ends the bridge with an error and a
    non-zero exit, where it used to exit 0 with no answer.
- **Joining:**
  - A redeem whose first try lost its answer and then met a revoke says that the first try may have made the login, and
    to ask the admin for a token for it.
  - A TLS trust failure is not sent again.
  - A redeem with no answer at all returns the `redeem_key` it used, so running it again is a retry.
  - `SupportsInvites` reports a proxy's error as an error, not as an older server.
- **CLI:**
  - `acp mount acp://h/s/ /m --space x` is refused, as the leading `--space` already was.
  - `acp --uri … status` shows the address's server and login.
  - A mistyped word of a noun group gets a did-you-mean (`acp files pusj`).
  - `acp update` no longer fails on a slow link: its download is bounded by the wait for headers and by idle
    progress, not a 30 s total.
- **Changes behaviour:** **Security: an empty bearer token never signs in.** With a login table that held an entry for the empty token (a
  `""` key in an older `agents.json`, or a newer file holding the hash of the empty string), a request with no token
  signed in as that login, and an add-member invite could be redeemed with no token. Shared-key mode already refused
  it. Now no token, or one of only spaces, is refused in every mode, and the server warns at start about such an
  entry (remove it with `-rmagent`). ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#empty-token))
- **Changes behaviour:** **A large upload through a cluster follower is no longer cut after 30 seconds.** Since the follower streams a body
  over 1 MiB to the leader (this release), the forward's 30-second total also timed the client's own upload, so any
  upload slower than about its size per 30 s failed with `502` (it worked in v0.2.5). A forward now has no total time:
  it ends only when the client sends nothing for 30 s (`408 request_timeout`, "send it again"), or the leader takes or
  sends nothing for 30 s, or does not answer within 30 s of the whole body (`502`, which says the write may still
  commit). When the leader answers early (a `401`, `403` or `429` before reading the body), that answer is the one
  the client gets once its body is sent. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#forward-bounds))
- **Changes behaviour:** **Slow share-link readers no longer block every share link on a server with a data key.** The few fetches that
  decrypt a blob at once (this release) kept their slot until the answer was sent, so four readers that read nothing
  stopped every other share-link fetch on the node. On a server with a data key the answer must now make progress
  every 30 s and, after its first 30 s, keep an average of 64 KiB/s; a slower reader gets a cut answer, can fetch
  again with a `Range`, and the slot goes to the next fetch. Servers without a data key are unchanged.
  ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#link-send-bound))
- **Changes behaviour:** **A token with a `read_prefix` and no `path_prefix` cannot write the exact folder name.** Its read prefixes now act
  as write prefixes: `projects/42/` admits `projects/42/x`, never a file named `projects/42`, which blocked the
  folder for everyone. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#read-prefix))
- **A space-scoped admin no longer learns about another workspace's pending invite.** Inviting a name that has an
  invite pending elsewhere answered with that invite's id; it now answers `409 name_taken`, as for a login outside its
  reach. An invite in its own workspace is still named.
- **Changes behaviour:** **Removing an invited member never falls back to a name.** `DELETE /v1/admin/invites/<id>/member` removes the access
  from the login that redeemed the invite. When that login is gone and another holds its name, it answers `409
  member_changed` (remove that login through the grants API) instead of changing the other login. Sending it again
  after it succeeded (a retry after a lost answer) answers exactly what the first answered (with `space` when only
  that space was taken away) and changes nothing; it was `404`. On an invite redeemed more than once (removed members
  counted) it needs `?name=` (`acp invites remove-member <id> --name <name>`), so a late retry can never remove a
  member who redeemed later.
  ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#unmember))
- **Changes behaviour:** **A bulk-change policy keeps a workspace from being removed.** A workspace whose only content was a policy such as
  `{"bulk_deny": true}` counted as empty and could be removed with the policy. Removals stamped by a v0.2.6 leader
  now count it. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#bulk-policy-drop))
- **A purge of document recovery copies always leaves its `history.recovery_purged` event.** The leader decided it from
  its own view, which could miss a copy that expired by an entry it had not applied yet.
- **Every error code is registered.** `bulk_confirm_required` and `bulk_confirm_mismatch` are now in
  `components.schemas.ErrorCode`, and `member_changed` and `member_not_contained` in the invite reasons; the `code`
  description says every refusal carries one.
- **Loading a document's state no longer makes one allocation per character.** It builds its index in one pass (the
  same index as before); a JSON document's every push copies its lists this way.
- **`agents.json` and the other private files always end owner-only (0600).** A leftover `agents.json.tmp` (a
  crash between writing and renaming, or a hand copy) kept its own mode through the rename, so the login table could
  end up readable by other users, and a leftover that was a link was written through. The server now creates such
  files afresh: `agents.json`, the owner-login token file and the idempotency log's repair file; files restored from an
  old-format snapshot and the cluster CA and self-signed TLS keys are made 0600 even when they already exist. An
  `admin dump --out` file the dump creates is 0600; a path that already exists keeps its mode and owner (`/dev/null`
  is never touched), with a note when others can read it.
- **Changes behaviour:** **`coordd` refuses a start with an empty token, and a server nobody could sign in to.** A
  `-token` of only spaces (in either mode), an empty `<data>/token` with no logins, or an `agents.json` whose every
  login is for an empty token (which never signs in) now stops the start with a message saying what to fix (exit 78),
  where it started and refused those requests. A reload by SIGHUP does not repeat the check.
  ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#start-empty-token))
- **A sub-path a route does not take answers `404 unknown_route`.** `DELETE /v1/admin/invites/<id>/<other>`,
  `/v1/blobs/<hash>/<other>` and `/v1/cap/blob/<hash>/<other>` answered as if the invite or blob were missing, or as a
  malformed link. Under `/v1/admin/`, a login that is not an admin still gets the same `403` for a route that does
  not exist as for one that does.
- **A refusal's "next step" is a whole word.** A message counted as saying what to do because a step word hid inside
  another word ("use" in "because"); such a message now gets its status's step added at the end.
- **Go clients tell "this server has no such route" from "no such thing".** A 404 with code `unknown_route` (servers
  from v0.2.6), or an older server's 404 with no code, reads as a missing route; any other code is the route's own
  answer. `acp thread`, the document trash and versions, `acp pull`'s history and scope checks, and the mount's retry of
  a realtime batch read it that way, and a refused share link keeps its date after "expired" and its own next step
  whichever server answered.
- **`acp help mail` (and `docs`, `doc`, `file`, `event`) prints the group's page.** It answered "no such help topic",
  though `acp mail --help` worked. Refusals that only said what went wrong (`acp link` with an unknown profile, `acp
  thread` with no such thread, an unknown `acp crdt` subcommand, `acp awareness watch` on an older server, `acp update`
  when the release server does not answer) now end with what to do.
- **Every refusal carries a stable `code` and says what to do next.** Refusals that had none now carry the class
  of their status (`bad_request`, `forbidden`, `not_found`, `method_not_allowed`, `conflict`, ...), and a message
  that did not say what to do gains a short step at its end (it keeps its old start; a `405` that was only the
  allowed methods now reads `method not allowed: use POST`). A route the server does not
  have answers `404 unknown_route`, so a client can tell it from an object that does not exist (`not_found`).
  A share link that cannot be served says which: `link_expired`, `link_invalid` (changed, or a bad signature),
  `link_not_found`, `link_malformed`, `links_not_served`, with its old message and no added hint (the client
  adds the reader's step). Every code is listed in `/openapi.json` (`components.schemas.ErrorCode`). v0.2.5
  clients are not affected (see MIGRATING).
- **Cluster nodes with different `-enforce-leases` settings no longer disagree.** A commit to a path another
  agent leases was judged by each node's own flag, so a node set differently applied a commit its peers
  refused. The leader's setting now travels with each commit. Also in v0.2.5.
- **A download or a forwarded answer that breaks part way now ends with an error.** The server used to end
  the response normally, so a client could take a short body for a whole one; it now drops the connection.
- **Every error `code` the server sends is listed in the API description** (`components.schemas.ErrorCode`
  in `openapi.json`), and the messages that lacked one now say what to do next (a missing document, trash
  entry or version, a folder that is not empty, a filesystem-mode flip that cannot proceed).
- **An upload over `-max-body` sent to a cluster follower is refused, not stored cut short.** The follower
  forwarded the first `-max-body` bytes as if they were the whole file; the leader stored them and answered
  `200` with the hash of the cut bytes, so `acp fs put` and the mounts reported success for a truncated file.
  It is now `413`, as on the leader, and nothing is stored. A follower also streams a large upload to the
  leader instead of holding all of it in memory. Also in v0.2.5.
- **Removing an invite's member removes only that member.** For a member an invite added to a workspace, the
  server looked the login up by name, so a workspace-scoped admin could remove another login with the same
  name, including an admin of every workspace. The server now removes the login the invite added, or
  nothing, and refuses a login outside the caller's own reach.
- **Changes behaviour:** **A `max_docs` quota above `-max-docs` now works, and `-max-docs` above 10000 now reaches the default workspace.**
  Creation stopped at the flag (`507 docs used=3 limit=5`) whatever the quota said, and the default workspace
  stopped at the lower of the flag and 10000, so a flag above 10000 did not raise it. In a cluster the leader's value now travels with each document write, so nodes cannot decide
  differently. Also in v0.2.5. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#max-docs))
- **Changes behaviour:** **A token with a `read_prefix` and no `path_prefix` now writes only inside its `read_prefix`.** It could write,
  and take leases, anywhere. File operations on a path the token cannot read answer like a missing path,
  instead of telling whether it exists. Also in v0.2.5. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#read-prefix))
- **Space-wide compaction stays inside the caller's scope for structured (JSON) documents too.** A
  path-scoped admin compacted every JSON document in the workspace, and their clients had to resync. Also in
  v0.2.5.
- **A workspace-scoped admin no longer learns the role, fingerprint or limits of logins in other workspaces**
  from an invite refusal; it is told only that the name cannot be invited. (v0.2.5 already showed such a login's
  fingerprint.)
- **`admin dump` and `admin migrate-store` carry the logins and pending invites.** A migrated server came up
  with no logins, in shared-key mode with a new key. The certificate, `owner.token` and the cluster CA files
  still need copying by hand (OPERATIONS). Also in v0.2.5.
- **Purging old recovery copies leaves a record.** When the history window drops document trash entries and
  retained versions, the event log records `history.recovery_purged` and the server logs how many it dropped.
- **Reading a retained version is bounded.** The full text of a version is folded once and kept, and folds run
  one at a time; share-link fetches on a server with a data key run a few at a time (in v0.2.5 too, every
  such fetch held its whole file in memory at once).
- **A trash restore delivered again after a crash keeps its event** when the document was made and deleted
  again in between.
- **A login, an invite and a share link no longer end sooner than asked.** An end is stored in whole seconds and was
  rounded down, so each could end up to a second early, and a one-second login could be over before its first call.
  Ends are now rounded up: `ttl_sec` and `expires_at` on `POST /v1/admin/grants`, `-mkagent -expires-in`, an
  invite's `ttl_sec`, the login an invite's `grant_ttl_sec` makes, and a share link's `ttl_sec`. A whole-second
  `expires_at` is kept exactly.
- **A refused first write no longer removes an empty workspace an admin prepared.** After a restart a single server
  treated every workspace it had not reopened yet as new, so a bad request to a prepared, empty workspace took it away,
  and later reads answered `404` until something wrote to it. Now only the request that actually made a workspace
  takes it back when it is refused. A refused `PUT /v1/admin/quota` that names a new workspace in its body now leaves
  nothing behind, as one that names it in the header already did. A removal now waits for anyone opening that
  workspace at the same moment, so a read or a share link can no longer bring a removed workspace back, and removing
  one workspace no longer disturbs the creation of another (which could leave the new one uncounted against
  `-max-spaces-per-grant`). `GET /v1/admin/spaces` and the `-discovery-spaces` list now show every workspace that
  exists, not only those opened since the start; the discovery list is read at most every 10 seconds, and is left
  out if it is too long to publish.
- **Changes behaviour:** **A save from a deleted document no longer takes its name.** A client still holding a deleted document could push an
  edit onto the empty name: answered `200`, stored, never shown, and from then on the name was held by a document with
  no text, so restoring the real one from the trash said `409 doc_exists`. The server now refuses such a first push
  with the same `409` as a push caught during the delete, and stores nothing. A push that stands on its own (a new
  document, or an edit that only adds at the very start of the text) still starts a new document: without a generation
  on the wire the server can't tell who sent it, so the restore then says `doc_exists`, correctly, for that text. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#deleted-doc-name))
- **Changes behaviour:** **The server refuses a mail to `a,b`.** An older client or an SDK could still send one: it was stored for nobody, and
  the log said it was sent. `400`, and the sentence says to send one message per person. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#mail-comma))
- **Changes behaviour:** **`PUT /v1/admin/quota` refuses a field it does not know**, naming it. With "only the fields you send change", a typo
  changed nothing and answered `200`. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#quota-merge))
- **`coordd -mkagent`, `-rmagent` and `-hash-agents` name the version the file really is.** They said 6 on a version 7
  file, which is the version that decides which binaries can still read it.
- **Changes behaviour:** **Invite refusals carry `code`**, the same token as `reason`. An SDK's error reads `code`, so it came back empty. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#invite-code))
- **Changes behaviour:** **TypeScript live mount: a save keeps a teammate's text.** A `writeFile` of a live document was compared with the
  document as it stood at the save, so a sentence a teammate added after you read the file counted as your delete and
  was removed. A save is now compared with what you last read, or with your last save if you have not read since, and
  only what you changed goes. A save of a document this mount never read adds to it and deletes nothing: a line you
  changed stays in both forms, and `flush()` lists the file in the new `merged` field. Read the file first to replace
  text, or mount with `allowBulk: true`. Same rule as the Go mount. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#ts-save-on-read))
- **TypeScript SDK: a document deleted and made again is no longer written to as if it were the old one.** Once the new
  document had grown past where a `TextDocSession` or a mount stood, nothing showed the change: the session kept the old
  text and a mount's edits went nowhere. Both now look at the log first. A session shows the new document, and a mount's
  next save is added to it and deletes nothing.
- **TypeScript SDK: an edit of a big document is a real edit, at any size.** Above 4,096 characters `generateOps`
  replaced the whole text, so `TextDocSession` and the live mount sent a delete of everything for one changed
  character: readers saw the document empty out, a stopped save left it gutted, and two ordinary edits doubled it. It
  now sends only what changed, inserts first, and uses the same diff as the Go clients. Opening a big document is much
  faster too: 70,000 characters took over a minute and now take under a second.
- **A refused lease no longer shows the holder's fencing token.** A `409` from `POST /v1/lease/acquire` or
  `/v1/lease/renew`, and a `423` on a commit, carried the holder's real token in `current`, which `GET /v1/lease` has
  always hidden. `current` stays, with `token` 0.
- **`GET /v1/stats`: `open_streams` is left out for a login limited to certain workspaces.** It counts every login on
  the server.
- **Small quota and document fixes.** `PUT /v1/admin/quota` reads a field name in any letter case (it answered 200 and
  dropped the value), and two PUTs of different fields at once no longer lose one. A realtime file can no longer be
  pointed at a reserved document name, in any letter case.
- **`coordd admin dump` and `migrate-store` take `--data-key-file`** for an encrypted store.
- **Changes behaviour:** **Rebuilding a cluster node no longer doubles its data.** The documented rebuild is to wipe `-data/raft` and re-join. If
  the leader still held its whole log, every old entry then ran a second time on top of the node's data: each event and
  message twice on that node, with no error. The node now moves its old stores aside as `spaces.pre-restore-<time>` and
  fills an empty tree from the cluster. A start with an empty `-data/raft` over data, with neither `-join` nor
  `-raft-bootstrap`, stops and names both. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#cluster-restore))
- **Changes behaviour:** **An edit of a large shared document is now a small edit, whatever its size.** Above 65,536 characters, changing one
  character deleted every character and then inserted every character, sent in pieces: readers watched the document
  empty, a stopped sync left it empty for everyone, and two people's ordinary edits doubled it. Now only what changed
  is sent, in `acp crdt sync`, `--merge`, the MCP `acp_doc_write` and the mounts. If a real rewrite takes several
  requests, the new text goes first, so anyone reading in the meantime sees extra text and never a hole. Large
  documents below that size could also run the tool out of memory; memory use now grows with the size of the text. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#big-rewrite))
- **Go mount: no more phantom conflicts when a file is moved and deleted before the next save.** Rename a file, give
  another file its old name, and delete or replace both before the changes reach the server, and the mount took its own
  old file for a teammate's: it restored it, so an old copy came back, and reported a conflict with nobody else involved.
  The same after a lost answer from the server.
- **Go mount: a file renamed onto a deleted name no longer looks gone after a lost answer.** It read as missing until the
  next barrier; nothing was lost.
- **TypeScript live mount: no more phantom conflicts when a file is moved and deleted before the next save.** The same
  case as the Go mount above: an old copy of your own file came back, with a conflict reported and nobody else involved.
  It also happened after two saves of one file whose replies were both lost. A teammate's file caught by your delete is
  still found and put back. (This part of the TypeScript SDK has not been published to npm yet.)
- **TypeScript live mount: after a lost reply, reads show your renames correctly, and a delete after a rename runs.** A
  file renamed onto a used or deleted name could read as missing, a chain of renames could show the wrong file at a name
  until the next barrier, and a file you renamed and then deleted right after a lost reply stayed on the server, or
  ended in a false "move" conflict. (This part of the TypeScript SDK has not been published to npm yet.)
- **Go live mount (`acp-mount`): reads after a lost reply show renames correctly, folders included.** A file renamed
  onto a deleted name, with a new file at its old name, could read as missing until the next barrier. A folder made
  with its reply lost, then renamed, with its old name used again, showed the wrong files at the new name. A file in a
  folder renamed over an emptied folder could read as missing. These were wrong reads: the files on the server stayed
  right in every sequence tested.
- **Go mount: a rename that kept losing to newer changes was reported as saved.** After eight tries it now fails the
  barrier and stays pending.
- **`acp pull` no longer overwrites an edit made while it runs.** On a big pull the file was hashed long before it was
  replaced. A file that changed meanwhile is left alone and reported; run `acp pull` again. `--force` still overwrites.
- **`acp pull` refuses more spellings of `.git` and `.acp`** from a server: `.git\hooks\x`, `.git.`, `git~1`, for disks
  that read them as the same folder.
- **`acp push --dry-run` writes nothing at all.** It used to move an older acp's sync record to its new name.
- **`acp_read_address` stops at 64 MiB**, and the bridge keeps at most 32 other workspaces in memory, never dropping one
  with a write the server has not answered. A bridge's unconfirmed writes last as long as it runs, and its messages say so.
- **Merging a file with a byte-order mark into a document without one** no longer keeps the first line twice.
- **Changes behaviour:** **Go mount: a save over a document a teammate deleted is no longer reported as saved.** A push the server stores is
  not always text in the document: the mount threw away the answer, so `fsync` said saved and the words were gone. It
  now reads the answer and, when it can't be sure, the server's text. A save that isn't there stays pending and readable
  and the barrier fails with `EIO`; the next one writes the whole text into the document now at that name, in front of
  anything a teammate started there. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#mount-deleted-doc))
- **Changes behaviour:** **TypeScript SDK: a save over a document a teammate deleted is no longer reported as saved.** The server takes such a
  save and answers that it could not apply it, and the mount and `TextDocSession` both ignored that answer: `flush()`
  reported success, `hasPending()` went false, and the text was not there. A session also kept showing the old text
  forever. Now the save stays pending and `flush()` rejects; the next try writes the whole text into the document now
  at that name, in front of anything a teammate has started there, and a session with nothing unsaved shows what the
  server holds. The deleted document stays in the trash. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#ts-save-deleted-doc))
- **TypeScript SDK: a save over a very short deleted document is no longer reported as saved.** The check above missed a
  document of one character when a teammate deleted it, and any save resent after a lost reply. It now counts what the
  log must hold once the save is in, for the mount and for `TextDocSession`.
- **TypeScript SDK: a save that only adds to the top of a document is no longer lost when the document was made again.**
  You read `short`, you save `XX short`, and meanwhile a teammate deletes the document and starts a longer one. `flush()`
  succeeded, `hasPending()` went false, and the server held `XX a much longer new document than before`: the rest of your
  save was gone and nobody was told. The mount and `TextDocSession` now read one op of the log before they send, so
  `flush()` rejects, the save stays pending, and the next try puts your whole text in front of the new document. A
  session makes this one small read before each push; a read that fails sends nothing and retries.
- **TypeScript SDK: a document its writer started over is no longer taken for the old one.** A writer on a fixed `replica`
  who deletes a document and starts it again mints the same ids, so the old text looked present. A save went into the new
  document as a fragment, `the same writ fourer, a new text`, and `flush()` said it was saved, even when the server had
  refused the push with a 409. A session went on showing the old text. The mount, `TextDocSession` and `RGA.logReplaced`
  now compare an id with its text: `flush()` rejects, nothing is sent, and the next try puts your whole text in front of
  the new document. A real compaction is still carried over as an edit.
- **TypeScript SDK: a document deleted, made again and compacted is no longer taken for a compaction.** Your edit was
  rebased onto the new document and `flush()` said it was saved, with a fragment of it on the server (` fourbrand new
  text`). A compaction keeps every character still showing, so when none of yours is left the mount and `TextDocSession`
  now reject `flush()`, send nothing, and put your whole text in front of the new document on the next try. A teammate who
  deletes everything before a compaction now gives you that refusal too; your text stays, and nothing is lost.
- **TypeScript SDK: a save on a document restored from the trash is no longer lost if its answer is lost too.** With the
  document then deleted and made again longer, a retry that only prepends landed alone and `flush()` said it was saved. The
  mount now keeps its place in the restored log, so `flush()` rejects and the next try keeps both texts.
- **Examples: an edit in `collab-docs` and `collab-notebook` survives a compaction.** `TextDocSync` dropped a pending edit
  on any 409, and so did the `collab-docs` agent, which had its own copy of the loop. `TextDocSync` is now a thin wrapper
  over `TextDocSession`, takes that class in place of `RGA`, and every example uses it. It keeps the truncation guard unless
  you pass `allowBulk: true`, as the two pages with a person typing do. An edit it refuses goes to `onError`, which prints
  by default. `TextDocSession` gains `lastEditor`.
- **Examples: text typed while a page loads stays in the box.** It left the box when the first read rendered, and the next
  keystroke deleted it from the document. `TextDocSync.flush()` and `stop()` now save text typed before the first read, a
  failed first read keeps trying, and `stop()` always stops: it resolves once saving is settled, and hands text it could
  not save to `onError(err, { unsaved })`. In the notebook, converting a cell waits for the old view to save.
- **Changes behaviour:** **TypeScript SDK: a flip of a space's mode loses nothing.** A barrier on its way, a read that failed during the move, or a
  buffered rename could each lose changes, and a retried `flush()` then said all was well. The mount now waits for what is
  in flight, keeps your changes until the new mode holds them, and carries a rename as a move. A change that can't move
  stays yours: `flush()` and `close()` reject with it in `detail.unimportable`, with its bytes or its record, and the new
  `takeUnimportable()` hands it over. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#ts-take-unimportable))
- **Changes behaviour:** **TypeScript SDK: a conflict an idle flush lost keeps your bytes.** With `flushDelayMs` and `conflict: "throw"`, the error
  held the only copy of the losing text and nobody received it. `hasPending()` now stays true, and the next `flush()` or
  `close()` rejects with that error. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#ts-idle-conflict))
- **TypeScript SDK: `followAwareness` never sends your token out of TLS.** A redirect from https to http is refused. Another
  https host is followed, because a cluster's leader is one.
- **Changes behaviour:** **TypeScript SDK: one failed capability read no longer mounts a realtime space as CAS for good.** The mount fails that
  operation and asks again on the next; only a server that answers without the capability is CAS. Other capability checks on
  that client recover too. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#ts-caps-read))
- **Changes behaviour:** **TypeScript SDK: an address mounts its whole path, as the Go mount does.** `acp://h/s/a/b` with no trailing slash mounted
  `a/`, so one address wrote to two places. To read one file, mount its folder. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#ts-mount-whole-path))
- **TypeScript SDK: more agreement with Go.** A byte-order mark on one side of a union no longer duplicates the first line.
  A name starting `@@` has one canonical form, `%40@x`. A discovery document with a byte-order mark is refused.
- **Changes behaviour:** **TypeScript SDK mount errors and views.** A failed read or commit now rejects with `EACCES` or `EIO`, as documented, not a
  raw error. A mount follows a server restored to an earlier point. A `watch()` started late sees commits made before it,
  and one started after `close()` works. `withLease` renews a 1 s lease in time, and `restore()` reports a wrong mode at
  once. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#ts-mount-errors))
- **Changes behaviour:** **TypeScript SDK and mounts: a server restored from a backup is never taken for the same log.** The server now names its
  event log, and the name changes at every start. `followForever` and `subscribe` tell `onResync` when it changed
  (`reason: "restored"`, the old position and the new head) and go on at the new head; a mount re-reads what it holds.
  Before, a restore was seen only when the head was lower, so a restored server that had already passed the old position
  was read as the same log and its events taken for the ones you had. Under the same name a lower head is a node that is
  behind, and the stream waits for it. With no name and a lower head (a cluster node in an election, or one catching up
  after a restart) `onResync` is told `behind`, as before. Either way both mounts refuse reads meanwhile (EIO,
  `detail.code` `"behind"`) and keep what they hold, never serving the older tree; the file mount no longer drops its
  version floor there. On a new name
  the client looks at the event at its position (its seq and a hash it kept, `eventHash`; with none delivered yet, the
  event just before where it started, read through the same log): the same event is the same log (a restart, a leader
  change), and nothing is told or skipped; only a restore is, once: after it the new history's own event is the mark.
  A new name is taken only once that check is made (the new `onLogId` option gives it; keep that one, not `onOpen`'s):
  a check that fails is made again on the next connection, an answer from a node that names no log or another one
  decides nothing, and events a stream delivers while no name is shown are never what a later name is checked against.
  An answer with no event there from a node behind both the position and the stream's own head decides nothing either.
  While a log is held, a lower head with no name is waited for, with no gap and nothing replayed; a gap's `from` is the
  event after the last one checked. A mount refuses reads (EIO, `detail.code` `"undecided"`) while its stream names no
  log over the one it holds, or while a new name's check is out or failed; "behind" ends only on proof the same log is
  back, never on a stream that names none; a mount follows a node of its log that is behind from that node's head, so
  it sees it catch up; it takes its start head and log from one answer, keeps its position when its stream starts again,
  never opens blind while it holds a log, and drops a stream that names another log than that answer.
  A mount reads again at once after a restore. Reconnects after a stream ends are spread over a short random wait, so a
  leader change does not bring every client back at the same moment. With no `onResync`, a follow that
  lands on another log stops with the new `LogChangedError` (a subscriber gets it through `onError`), as in the Go SDK.
  `waitForMail` takes the head and the log it belongs to from one answer of the server (the new `logPosition()`) and
  reads the inbox again when its stream names another log, so a restart between two of its requests no longer makes it
  wait past mail. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#ts-log-id))
- **TypeScript SDK: "no such route" is told apart from "no such thing", as in the Go SDK.** `err.routeMissing()` and
  `isRouteMissing(err)`: a 404 with `unknown_route` (today's server) or with no code (an older one). The undo calls read
  either as `NoDocUndoError`, and any other 404 reaches you with its own code and words. A live JSON document on a server
  that answers `unknown_route` now fails to open, where it showed an empty document.
- **TypeScript SDK: a large live document edited anywhere stays quick.** Replaying a document's history took time that
  grew with the square of its length whenever the edits were not all typed at the end: text typed in the middle of a
  long document, or two people typing in two places, made each open and each mount save slower than the last. It now
  grows as n log n wherever the edits are, with byte-for-byte the same results, and a mount save replays the history once
  for each read it kept instead of once more on top.
- **TypeScript SDK: every mount and `acp://` error says what to do next.** The messages of `VfsError` and `ACPURIError`
  now end with a next step ("check the path", "call flush() first", "ask for the link again"); the codes are unchanged.
  A refused share link reads as the Go tools do on today's server: the server's general hint gives way to the link's
  own step, and an expired link's date follows "expired".
- **TypeScript SDK: every error it raises itself has a stable `code` and says what to do next.** New codes:
  `BlobIntegrityError` `EINTEGRITY`, `NoMailError` `ENOMAIL`, `NoDocUndoError` `ENODOCUNDO`, `MalformedTextError`
  `EINVAL`. The messages of these, of `RequestTimeoutError` and of `VfsConflictError` now end with the next step; the
  text before it is unchanged.
- **Changes behaviour:** **TypeScript SDK: no call hangs without a bound.** A server that never answers used to hang a call until the caller
  aborted it. Now a request with no response headers in 60 s, or an answer whose body goes silent for 30 s (a blob, a JSON
  answer, an invite redeem), fails with the new `RequestTimeoutError`. Both are options (`timeoutMs`, `bodyStallMs`,
  0 = off). A timeout is never retried. Not bounded: streams, the mail wait, the wait for the
  headers of an upload over 1 MiB, and `readACPCapability` and `discoverACP`, which take a `fetchImpl` you can bound; the
  README lists them. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#ts-timeouts))
- **TypeScript mount: an editor's backup rename keeps a live document live**, as the Go mount does, except when the
  new text would wipe most of the document: the rename then stands and the file is written exactly, never a save refused
  as a truncation with its bytes left pending. "Most of the document" counts what the save would be made on, so after a
  save that added to a teammate's text, that text counts too. Moving the file
  away, writing a new one at its name and removing the backup used to send the document to the trash and leave a plain
  file. The document now comes back, with the new text written into it. This happens only for a document this mount
  showed whose new text keeps half of what it showed; a script's `mv a a.old; gen > a` still gets an exact plain file.
- **TypeScript SDK: a request reset before any answer is retried once.** A GET, HEAD, or a request with an
  `Idempotency-Key` whose connection is reset or closed before the response headers arrive goes once more; any other
  request is never sent twice, and a reset once the headers arrived is never retried. Unlike the Go SDK, the TypeScript
  one can't tell a reused connection from a new one, so it also retries a reset on a new connection, and the second send
  takes whichever connection the runtime gives it. An upload answered with no readable hash is refused instead of returning an empty
  hash.
- **Go: opening or saving a large live document no longer takes minutes.** Each character's place in the document was
  found by scanning from the start, so replaying a document's history took time that grew with the square of its
  length: a 70,000-character document took minutes on the mount, and the server did the same work when it rebuilt one.
  Places are now found in logarithmic time, with exactly the same results; a mount save also replays the history once
  instead of twice.
- **Go SDK: a request reset on a reused connection is retried once.** A GET, HEAD, or a request with an
  `Idempotency-Key` that meets a connection the server closed while it sat idle, before any byte of an answer arrived,
  goes once more, on a fresh connection; any other request is never sent twice. Over HTTP/2 (every TLS server) it used to
  fail, and the mount's first operation after an idle period could fail with EIO.
- **Go example `crdt-coedit` takes a replica id per run**, and a document name per run unless `ACP_RUN_ID` names one. Two
  runs at once on one document no longer mint the same ids for different text.
- **TypeScript CAS mount: no second `.conflict` after a lost answer**, and a conflict record's bytes are the caller's own
  copy, never the cached blob.
- **Changes behaviour:** **TypeScript mount: two people adding similar lines both keep theirs.** A checklist item or a journal line a teammate
  added, then any read, then your autosave adding your own: the teammate's line was rewritten into yours, character by
  character, and reported saved. A save now deletes only text every view the mount kept held. Other text stays, and
  `flush()` lists the file in the new `kept`; the saves after it keep that text too, and say so, until one holds it again
  (the next autosave, one chained onto the save in flight, or a retry of a save whose push failed used to delete it with
  no report, and so did a retry after a teammate deleted the document and restored it from the trash); once a teammate
  takes that text out, the next save no longer says `kept`, however long before that save the delete was. A save typed
  over one whose push failed decides what it keeps from its own text. A save on an older view that keeps the newest
  view's text says `kept`, also when it is retyped after a failed push; `kept` is not said when the text it left out
  was already deleted. A save that took back a refused save's choice (a document restored from the trash) works out
  again what its own text keeps. The records of documents whose saves keep text are bounded: past the bound the oldest
  keeps only the ids of that text, and a save that would delete it fails with `EPERM`, its bytes pending, until the file
  is saved once with that text in it. A document whose record the bounds took is saved as a first contact (added to the
  text, nothing deleted, `merged`) until a save of it lands, a read since included, so an editor's older buffer never
  deletes what another program's read showed. A record that keeps text lets go of it when the document is deleted or made
  again, or when a read finds that text gone from the server. A save that makes a deleted document again (nothing was at
  its name) is listed in the flush report's new `remade`; the delete stays in the trash and the history. Also: a save retried after the truncation guard refused it chooses its base
  again; a read that went out before your save landed no longer counts as newer than it; a read before a compaction stays a
  usable base; past the record bounds, documents only read are forgotten first. A `flush()` after an idle conflict hands
  that conflict over before it runs, so its own report is never dropped. The examples' agents take a replica id per run. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#ts-save-kept))
- **TypeScript mount: a read by another program no longer turns a save into a delete.** A save, a teammate's edit, any read
  (an indexer, `grep`), then the next autosave deleted the teammate's sentence, and nothing said so. The mount now keeps the
  last few reads and your last landed save, and each save is compared with the one closest to it. A line of a teammate's
  that a save only leaves out now stays, and you see it; a mount opened with `allowBulk` still replaces.
- **TypeScript SDK: a `base` from before a document was made again is refused.** `TextDocSession.setText` throws
  `DocBaseUnknownError`, where a fragment used to land and be reported saved. A mount save on a restored document no longer
  reuses an id of a whole-text save whose answer was lost.
- **Folders for documents with `/` in their name are owner-only.** They were created `0755` inside the owner-only store
  folder, and the small per-document state files `0644`, unlike everything else in `-data`.
- **Changes behaviour:** **TypeScript SDK: a list in `to` is refused, not lost.** `send({ to: "max,zed" })` reached nobody and the log said it had
  been sent. `send` and `sendIdem` answer with one message, so they name one person: a comma in `to` is now a `TypeError`
  and nothing is sent, on any server. Send one message per person; `acp send` still takes a list. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#ts-send-comma))
- **Go mount: a save after a document was deleted and made again is no longer lost, doubled or half-sent.** A save on the
  mount's last save sent its edit to a document that no longer existed: at the top of the file only the new characters
  landed, in the new document, and the save was reported as done. A save read before that happened could do the same.
  Now one small read comes before each save: a save on the last save joins the text now at that name and deletes nothing
  from it, and a save on an older read fails its `fsync` once and stays pending, as it does when the document is gone
  with nothing in its place. If a refused document is restored from the trash, the save lands on it as an edit, with
  anything a teammate wrote there since; it used to write the whole text onto it, so the document held it twice.
- **Go mount: a save that goes out again looks first, as the first try does.** A save that failed once (its answer lost, or
  the look before it), a save on top of one, and the retry of a save on your last save went out without a look. If the
  document had been deleted and made again by then and the save only added to the top, the new characters landed alone,
  the rest of the save was gone, and `fsync` reported success. Now every try reads one small place in the log first: the
  save fails its `fsync` once and stays pending, and the next flush puts its whole text in front of the new document. The
  same goes for the retry of a save on a document restored from the trash.
- **`acp crdt sync`, `acp_doc_write` and the Go mount: a document made again is noticed when the op at your place is a
  delete.** That log was taken for the old document, so `sync` and `acp_doc_write` reported success for text that went
  nowhere, and a mount save could lose all but the part at the top. They now treat it as the first contact it is.
- **`acp crdt sync`, `acp_doc_write` and the Go mount: a document made again and then compacted is no longer taken for a
  compaction of yours.** The edit was fitted onto the new document: a fragment of it landed, in the middle of someone
  else's text, and was reported as saved. A compaction keeps every character still showing, so a newer log holding none
  of yours is another document. Your save is now refused and kept, as for any document made again; on a mount the next
  flush puts its whole text in front of the new document. If a teammate deleted everything and the document was then
  compacted, that looks the same, so you see the refusal and your text comes back; nothing is lost.
- **`acp crdt sync`, `acp_doc_write` and the Go mount: a document started over by a writer with the same ids is noticed.**
  The first place it can show was checked by id alone. A writer that minted an id twice no longer makes a live document
  look deleted either: the first op of an id stands, as it does on the server.
- **Changes behaviour:** **`acp invite --workspace` and `acp link --workspace` use that workspace.** `--workspace` is the same flag as `--space`,
  but these two commands read only `--space`, so `acp invite --name bob --workspace secret` made an invite to your default
  workspace. Bob got the wrong workspace, and the command reported no error. Check the invites you made this way with
  `acp invites`. `--space` with no value is now an error here too, and `acp mount` takes `--workspace`. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#cli-invite-workspace))
- **Changes behaviour:** **Go mount: a read by another program no longer turns a save into a delete.** A save, a teammate's edit, any read (an
  indexer, `git status`, `grep`), then the next autosave deleted the teammate's sentence, and nothing said so. A save is now
  compared with one of your last few reads and your last landed save, and deletes only text all of them held. A line of a
  teammate's that a save only leaves out stays, and you see it: the mount logs it and a `Flush` report lists the file in
  the new `Kept`. It stays kept: the next save leaves it out too and says so again, a read between or not, until a save
  includes it once. Two people adding similar lines (a checklist item, a journal line) both keep theirs. To delete one, see
  "Deleting a teammate's line" in `mount/README.md`. Also: a save retried after the truncation guard refused it chooses
  its base again; a read that went out before your save landed no longer counts as newer than it; a read before a
  compaction stays a usable base; a save on your landed save puts its text before a teammate's at the same place, as every
  other save does; past the record bounds, documents only read are forgotten first. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#go-mount-save-kept))
- **Go mount: an editor's backup rename keeps the live document.** vim, by default on many systems, moves the original
  away, writes a new file and deletes the backup. That sent the document to the trash and left a plain file at its name,
  so co-editing ended for everyone, and every step reported success. Now, when this mount has shown that document (you
  read it, or saved it), a text file written at its name within 2 seconds of the move that keeps at least half of the
  lines you saw goes into the document, and the old text stays as a plain file at the backup name (none if you removed the
  backup). With no teammate edit, the document ends exactly as you wrote it. Anything else, a script's
  `mv report.md report.old; gen > report.md` on a document this mount never showed included, is a new file, exactly the
  bytes written, as before.
- **`acp mount` checks `--profile` and `--space` against the address, like every other command.** `acp --profile alice
  mount acp://teamhost/s/ dir` could mount as another login that matched the host, an admin's even, without a word; and
  `--space x` with an address for workspace `s` mounted `s`. Both are refused now, before anything is sent. The MCP
  bridge with `ACP_PROFILE` and an address checks the same.
- **Go mount: when the server's event log is behind what the mount has seen, the mount stops serving reads until it
  catches up.** Its live updates went on from the last event it had seen, so after a server was restored to an earlier
  point the new events under the same numbers were skipped and their files served out of date, with nothing said. A
  lagging cluster node answers the same way as a restore, so the mount never rolls its tree back on that alone: reads
  fail (EIO) while the log is behind, and the stream restarts at the server's head. A restore the server names (a new
  log id over another history) is re-read without a remount (see the log id entry above).
- **Go mount: a restart frees a mount whose old process can't exit, and never touches a live one.** An `acp-mount` killed
  in the first moments of mounting could stay stuck in the kernel, and the restart hung waiting on it. A live `acp-mount`
  now holds a lock for its mount point for its whole life: a second start finds it held and refuses, however slow the
  live mount's server is. Only with the lock free does a restart stop waiting after five seconds, free that mount and
  mount again. A mount unmounted from outside (`fusermount -u`) now runs its exit barrier too, as Ctrl-C does, so what
  it held is saved.
- **Go mount (CAS): a truncate after a lost answer, and a rename during a save, lose nothing.** `echo new > f` right after
  a save whose answer was lost sent your newest text to a `.conflict` file and kept the older bytes; a file renamed while
  its save was on the way shared its bytes with the copy the mount cached, so a later write into the new name changed
  what the cache served under the old content's hash.
- **MCP bridge: a commit in a realtime workspace says `wrong_fs_mode`.** `acp_file_put` and `acp_commit` there answered
  "conflict … pass force:true" or "a very hot manifest — retry", after five tries; they now stop at once and point at
  `acp_fs_ops`.
- **Go mount: a file over `--max-file` is never cut short into a live document.** Renamed over one, it is refused.
- **Go mount: the tree no longer goes back.** A read answered late, or by a lagging follower, could roll the mount to an
  older version: a file just made answered "not found", and the next write conflicted with the mount's own save. An older
  version that lasts past 30 seconds is taken as the server's truth (a restored server).
- **Go mount: a handle reads its own landed write.** After an `fsync`, a handle read its old bytes again, cut short if the
  file had grown.
- **Changes behaviour:** **Go mount: a backslash in a name is refused** (`EINVAL`). `echo x > 'a\b'` was written to `a/b`, over that file with no
  conflict, and `rm 'a\b'` removed it. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#go-mount-backslash))
- **Changes behaviour:** **Go mount: `--attr-ttl 0` means no caching.** It was taken as the default, one second. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#go-mount-attr-ttl))
- **Go mount: a server that stops mid-file fails the read.** `--io-timeout` bound the start of a read only, so a stall hung
  the program and every other reader of that file. A timed-out call also no longer holds a connection for the life of the
  socket.
- **`acp status` says the server refused you.** A revoked login or a bad token read "can't reach the server".
- **Go SDK: `PutBlob` fails on an answer it can't read.** A 200 with a garbled body returned an empty hash and no error.
  `Client.BoundResponseHeaders` is new.
- **`acp crdt sync`: a push refused at an unchanged epoch keeps the edit.** On a first sync it reported `sent 0 op(s)`, exit
  0, and the text never reached the server.
- **Addresses: `https://a:b:80` is refused.** Go read it as a bracketed host; only brackets make a colon part of a host.
- **`acp crdt sync` and `acp_doc_write`: a stray edit in the log no longer reads as a new document.** An edit left by a
  writer on an old view, which nobody can apply, could sit at your place in the log, and your next edit was refused as a
  first sync. The whole log now decides, at the cost of one read when that one op looks wrong.
- **Go mount: a save into an empty document no longer counts as merged.** The next save merged too, so a line the writer
  had just deleted stayed. Now it is an ordinary edit.
- **Changes behaviour:** **`acp crdt sync` and `acp_doc_write`: a document deleted while the edit was on its way no longer leaves a fragment.**
  The refusal was read as a compaction, so the edit was fitted onto nothing: a piece of it became the document, it held
  the name so the trash copy could not come back, `sync` overwrote your file with that piece, and both reported success.
  Now `sync` stops with your file as it was and `acp_doc_write` answers `NOT applied`; run them again. The same goes for
  `sync` when the document is made again at that moment: its text is no longer written over your edit. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#crdt-sync-fail))
- **TypeScript SDK: a session is told when its save hits a deleted document, and a restored document is not doubled.**
  A server that refuses such a save (see "A save from a deleted document no longer takes its name") left `TextDocSession`
  going round on its own: it sent the editor's whole text and never said the save had been refused. `flush()` now
  rejects, `onError` is told once and `hasPending()` stays true, as for any other save that did not land. If the document
  is restored before the retry, a session or a mount now puts the save on it, keeping a teammate's edit made there
  since. It used to send the whole text in front of the restored copy, so the document held the text twice.
- **TypeScript live mount: two saves of a shared text file at the same moment no longer garble it.** Two `writeFile` calls
  on one live document before either finished could both land on the same starting point, and the server kept a mix of
  the two (neither save) while the flush reported success. Saves of one path now run in the order they were called, and
  each is judged again once the document has been read: the last call's exact text lands, nothing is mixed. An editor's
  "write a temp file, rename it over" racing a save is covered too.
- **Changes behaviour:** **`acp put --replace` no longer overwrites a teammate's newer version.** It now checks that the file is still the
  version you last read here with `acp show` or wrote with `acp put`, and stops with what to do if it isn't. You can
  name what you edited with `--base <hash or version>`, and `--force` is last writer wins, on purpose. A script that
  ran `put --replace` with no read must add `--force`. `acp_write_address` on a realtime workspace had no check either
  (`force=true` was the only way on): it now checks the read, as it always did elsewhere. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#cli-put-replace))
- **Changes behaviour:** **Every `--ttl` takes the same lengths of time.** `acp invite --ttl 2h` worked and `acp share --ttl 2h` was refused.
  `--ttl`, `--grace` and `--timeout` now read `600` (plain seconds still work), `10m`, `2h` and `7d` everywhere. A value
  that can't be read stops the command. It used to slip through in worse ways: `lease`, `awareness` and `blob url`
  quietly took the server's default, and `acp admin gc --grace 1h` ran with no grace at all. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#ttl-forms))
- **`acp send` no longer calls a member a likely typo.** The note read "who has been seen", and that is who sent a
  heartbeat lately: the person who invited you is usually not among them, so the first message the invite tells you to
  send drew a warning. New `GET /v1/agents/member?name=` says whether a name is a member of the workspace, meaning a
  live login that reaches it or an open invite to it. The note now appears only for a name that is not one. With an
  older server or a shared-token server it falls back to the old "seen" check. The MCP send tool does the same.
- **Changes behaviour:** **Share links work on a server with a self-signed certificate.** `acp show <link>` and `acp uri read` failed with a
  TLS error naming an empty host, even after `acp join`. A reader with a login for that server now reads the link
  through that login's trust, whether it is an address by IP or by name. A reader with none gets one plain next step:
  ask for an invite, `acp join <code>`, run it again. The server's own answer now reaches you, so an expired link says
  it expired (HTTP 410), and a bad one says so too. A link past its date says that even when the server can't be
  verified. Errors no longer print the link's signature, and a link client never follows a redirect, which would
  carry the signature with it. `ACP_SERVER` with `ACP_PIN`, `ACP_CERT` or `ACP_INSECURE=1` counts as trust for that
  host. Trust from a profile for another server still never applies. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#tls-profile))
- **Changes behaviour:** **A first `acp crdt sync` into a document that already has text no longer risks a teammate's words.** With no sync
  state in the folder and a file that differs, the refusal now offers `--merge` first: it keeps the whole shared text
  and adds your file's new lines to it, so nobody's text goes. `--adopt-local` read like "send my additions" but
  made the shared text equal your file, which deleted a teammate's section in one newcomer's first minute. It is now
  `--replace-shared` (the old spelling still works, with a warning), and it stops if it would delete more than 10% of
  text this folder never saw, unless you add `--confirm-truncate`. `acp crdt sync` with no local file now saves the
  shared text to the file, or says how to start the document, instead of "no such file". `acp help crdt` teaches the
  first time in a folder. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#replace-shared))
- **Changes behaviour:** **`acp_doc_write` no longer deletes text the session never read.** On a document it has not read, a write that only
  adds goes through; one that would delete anything is refused and points at `acp_doc_read`, `merge: true` (the same
  merge as the CLI) and `replace_shared: true` (guarded the same way, with `confirm_truncate`). A failed
  `acp_doc_read` no longer counts as having read the document. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#doc-write-read))
- **Changes behaviour:** **`acp push` from a folder whose workspace is gone now says so.** It used to print "nothing to push", quietly forget
  what the folder had synced, and let the next push re-create every file in a workspace an admin had removed. Now it
  stops and names the choice: fix the name, or `acp pull --reset-base` then `acp push` to create it again on purpose. The
  same goes for a server restored to an older version, where `acp pull` already refused. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#push-symlink))
- **Changes behaviour:** **A lone `rm` in a mounted folder now reaches the server by itself.** It used to wait for the next save or `sync`, so
  a teammate still saw the file and `acp fs trash` was empty. `rm`, `rmdir`, `mv`, `mkdir` and `truncate` now land on the
  same short timer as a save, and every change restarts it, so a burst is one commit. In the TypeScript SDK a mount has
  no timer of its own: pass the new `flushDelayMs` option to get the same behaviour (default off). ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#mount-lone-rm))
- **A refused `rm -rf` stays refused while it runs, on every kind of mount.** On NFS and WebDAV mounts the timer ended
  the refusal after a moment, so the rest of a slow `rm -rf` deleted anyway, in small batches.
- **Fixed a crash in the mount.** A new change arriving just as the write-back timer fired could stop the mount process.
- **`acp awareness watch` no longer loops for ever on a server without awareness.** It says so once and exits non-zero.
  The same goes for every live view (`acp watch`, `acp awareness watch`, `acp crdt watch`) when the server refuses the
  login (401, 403): it stops with the server's answer instead of printing `reconnecting` once a second. A dropped
  connection, a 5xx or a 429 still reconnects. In the Go SDK this is the new `client.ErrNoAwareness`.
- **TypeScript SDK: a document session stops when its login ends.** `TextDocSession` and `JSONDocSession` used to ask the
  server again at every poll after a login ended, and call `onError` every time, for ever. They now report it once, as a
  `GrantExpiredError`, and stop. Edits made after that stay unsaved and `flush()` says why. Any other failure keeps
  retrying as before.
- **Changes behaviour:** **`--dry-run` no longer runs the command for real.** `acp rm x --dry-run` used to delete `x`, and `acp restore` and every
  other command behaved the same way, because `--dry-run` belonged to another command and was quietly ignored. Only
  `acp push` has a dry run; anywhere else the flag now stops the command with "nothing was done" and says what to use
  instead (`acp open` and `acp handler` keep their own `--dry-run`, which means `--print`). The same goes for `-dry-run`
  with one dash. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#cli-flags))
- **Changes behaviour:** **An on/off flag with a value is refused.** `acp push --dry-run=true` used to run a real push: the `=true` made the flag
  invisible. Every on/off flag behaved like this (`--force=true`, `--peek=true`): the flag was silently dropped. These lines now
  stop with "takes no value"; write the flag on its own. Flags that take a value (`--note=why`) are not affected. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#cli-flags))
- **The Go mount and `acp fs rm` can no longer take a teammate's new file with a folder you removed.** In a realtime
  workspace, `rmdir` on a mount and a folder renamed over an empty folder now ask the server to delete the folder only
  while it is still empty. If a teammate's file lands in it at that moment, nothing is deleted: the mount reports a
  conflict and keeps their file. `acp fs rm <folder>` does the same for a folder it found empty, and stops with
  "nothing was moved to the trash"; a folder that holds files still goes with everything in it, as before. The
  `acp_fs_ops` MCP tool accepts `if_empty` on a delete (it used to drop it silently, and delete). Against a server older
  than v0.2.6 all of this behaves as it did. New `wire.FSOp.IfEmpty` and `APIError.FolderNotEmpty()` in the Go SDK.
- **`rmdir` on a mount never trashes a file.** If a folder's creation got no answer and a teammate then put a file at that
  name, `rmdir` now reports a conflict instead of trashing their file. A refusal that merely names a path such as `cas mode` is no longer mistaken for the workspace changing mode.
- **TypeScript SDK: removing a folder can no longer take a teammate's new file with it.** In a realtime workspace, `rmdir`
  and a folder renamed over an empty folder now ask the server to delete the folder only while it is still empty. If a
  teammate's file lands in it at that moment, nothing is deleted: you get a `"delete"` conflict and their file stays where
  it is, so it never ends up unnoticed in the trash. Against a server older than v0.2.6 the mount behaves as before. New
  `APIError.folderNotEmpty()`.
- **`acp share` works in a realtime workspace, and a login limited to a folder can read live files.** A share link
  was checked against the manifest, which stops changing when a space goes realtime, so a file written after the switch
  was refused, and a deleted one could still be linked. Now the link follows the live files. The same mistake stopped a
  login with a read limit from fetching the bytes of a live file it could see in the listing: `GET /v1/blobs/<hash>`
  answered 404 and `POST /v1/blobs/has` said missing. It reads them now; files outside its folder still look missing.
- **Changes behaviour:** **`POST /v1/commit` now says "not uploaded" with a code.** The `400` carries `code: blob_not_uploaded`, the same code
  as `fs.ops`, so a client can upload the bytes and resend without reading the message. The message is unchanged. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#refusals))
- **Changes behaviour:** **A removed agent's name stays reserved, and a server has a limit on agents.** A joiner who picks their own name could
  take the name of a removed agent once the old invite had aged out, and so its queued mail and the trust others placed
  in it. The name now stays refused for good (an admin can still invite that name on purpose). A server holds at most
  10000 agents: past that, inviting or adding a new name answers `507 identity_limit`. A replacement token for an existing
  name still works. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#agent-names))
- **Changes behaviour:** **Two names that differ only in punctuation no longer share local state.** Profiles `work.prod` and `work_prod`, or
  agents `u:1` and `u_1`, used to be filed under one name, so they shared a folder's sync record, lease tokens,
  live-text sync state and catch-up mark. Names are now written one to one (`work%2Eprod`). A profile with no server,
  used with `ACP_SERVER`, now keeps state per server. **On upgrade:** names of only letters, digits, `-` and `_` keep
  their files (live-text sync state of a name with `_` is renamed). Anything else moves to its new name the first time
  it is needed, and only if it can only be yours. If two of your profiles shared the old name, or a profile names no
  server, the old file is left in place and not read, and acp says so: `acp pull` starts that folder over, and nothing
  is deleted on either side. Going back to an older acp after that sees those folders as a first sync. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#state-paths)) ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#no-server-profile))
- **A mounted save could be refused for ever after the server lost its bytes.** If a commit failed and the uploaded
  blob was then collected (or a read-scoped login lost its proof of upload on a server restart), the Go mount kept
  resending the same hash and every later save of that batch got `400 blob … not uploaded`. It now sends the bytes
  it holds and tries again, once for each refusal it has not seen; a rename holds no bytes, so it stops at once and stays
  pending. The Go SDK's `BlobNotUploaded()` now recognises the refusal from `POST /v1/commit` as well as from fs.ops.
- **A cluster node can join without its token on the command line.** New `-token-file PATH`: the same as `-token`, but
  the token stays out of `ps`. The cluster guides use it, and `-token` on the command line now logs a warning. Start-up
  stops on a missing or empty file, or if both flags are given.
- **Changes behaviour:** **Several statuses now say what happened.** An upload over `-max-body` answers `413` (was `500`). A workspace the
  server cannot open answers `503 space_unavailable` with the repair hint (was `400`, which read as your mistake).
  `POST /v1/admin/cluster/leave` on a single server answers `409` like join (was `500`). `HEAD` works on a signed link
  (was `405`). `DELETE /v1/crdt/doc` and `/v1/crdt/json/doc` with no name answer `400`. A malformed body on
  `/v1/blobs/has`, `/v1/mail/ack`, `/v1/lease/renew` or `/v1/lease/release` answers `400` instead of being read as
  empty, which told a client "nothing is missing" after a failed parse. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#statuses))
- **Changes behaviour:** **`POST /v1/fs/trash/restore` no longer answers success for a restore that did not happen.** A file under someone
  else's lease, a workspace back in realtime mode, or a bulk refusal all answered `200 {"restored": …}` with nothing
  written. They now answer `423`, `409 wrong_fs_mode` and `428`/`403` like a commit. A `409` on this route now shows a
  read-scoped token only its own part of the manifest. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#refusals))
- **Changes behaviour:** **`acp show` and the agent tool `acp_file_get` read the live tree on a realtime workspace.** They used to read the
  frozen manifest from before the switch, so you could be shown old bytes. `acp_manifest` now refuses there and points to
  `acp_fs_tree`. If the workspace's mode can't be learned, these stop with an error rather than guess. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#show-live))
- **Changes behaviour:** **`--profile` with an address means that profile.** A profile on the address's server is now used even if another
  profile matches its workspace, and one for a different server is an error, instead of quietly acting as someone else. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#cli-flags))
- **Mail retention and inbox order no longer trust the server's clock.** "Newest" was read from the message id, which
  holds the leader's wall clock, so after a leader change onto a server whose clock ran behind, retention could drop a
  recipient's newest read messages as if they were the oldest, and the inbox listed them out of order. Messages now take a
  position in the replicated send order, kept through restarts, compaction, snapshots and joins. The position is internal:
  no API answer changed.
- **TypeScript mounts: a save no longer fails because the server cleaned up its upload.** If the server removed a
  file's bytes between the upload and the save (or while a failed save waited to be retried), the mount now uploads them
  again and resends, a limited number of times, in the same `flush()`. Before, the save failed, and a save that kept
  waiting could never land until the file was written again. Same on live and on standard workspaces.
- **TypeScript mounts: too many deletes in one step is now a clear refusal.** When the server refuses a large delete
  for this login, `flush()` rejects with `EPERM` (it was `EACCES`, or `EIO` for a `428`), says that nothing was applied,
  keeps the folder and your pending changes, and `err.detail` holds the server's verdict.
- **Live (realtime) folders: deleting a whole folder counted as one file for the large-delete protection.** A login
  without the bulk permission could therefore move a folder of thousands of files to the trash in one step. A folder
  delete now counts every file in it, so it is refused (`403 bulk_denied`) above the workspace's limit, exactly as
  deleting those files one by one in a batch, or through a commit, already was. The trash still holds anything deleted.
- **Changes behaviour:** **Cluster: restoring only a server's `spaces/` folder from an older backup lost changes silently.** The server
  trusted its replication log's record of what it had already applied, so every change made after the backup was
  skipped. A server now records, inside `spaces/`, the point it had reached at a clean shutdown, and refuses to start
  when that does not match its replication log; restore the whole data folder, or wipe `-data/raft` and re-join. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#cluster-restore))
- **Cluster: a damaged snapshot could empty a server.** When a server received a snapshot in the old archive format
  (from a very old server, or any snapshot whose first bytes were damaged), it deleted its own data first and then
  unpacked the archive, so a damaged or cut-off archive left it with nothing and kept no copy. The archive is now
  unpacked to the side and swapped in only when complete, and the data it replaces is kept beside it
  (`spaces.pre-restore-<time>`), as for current snapshots; a damaged archive is refused and nothing changes.
- **Changes behaviour:** **Encryption at rest: the server started with a wrong key, or with no key over encrypted data.** With a wrong key,
  files could not be read, and uploading a file the server already held reported success for bytes nobody could get
  back; with no key, new uploads were stored unencrypted next to the encrypted ones. The server now writes a small
  check file (`datakey.check`, no key inside) in its data folder on the first start with a key, and refuses to start
  (exit 78, "data key: …") when the key does not match, or when no key is given for an encrypted data folder. An
  existing encrypted data folder is checked against a few of its stored files on the first start after upgrading: a
  file that is merely damaged is not read as a wrong key, and a refusal names the files it could not open. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#datakey))
- **Server-database edition (`acp_server` builds): a failed write to a text document's history could later
  overwrite an older edit.** If saving to the database failed part-way (a full disk, for example), the server's own
  count of what it had saved moved forward anyway, so the next edit could be written over an existing older one, and
  after a restart the document showed the wrong text. The count now moves only after the save succeeds; after a failed
  save that document's history refuses further writes until the server restarts and re-reads what is really stored.
  A batch of settings changes carrying an operation the server does not recognise is now refused as a whole instead
  of being saved without that operation.
- **An upload larger than a workspace's file-storage limit could be answered with the wrong file.** On a single
  server, the upload was cut at the limit and the cut-off part was stored, or, if a file with exactly those bytes
  was already stored, the server answered `200` with that file's hash although it was not the file you sent. Such an
  upload is now refused with `507` before anything is stored, as a cluster already did.
- **Live (realtime) folders could acknowledge a file whose bytes the server did not have.** Creating a file named its
  content by hash, and the server accepted any hash, including one never uploaded or a malformed one, so the file
  existed but could not be read. The same happened when an admin's clean-up (`/v1/admin/gc`) removed a just-uploaded
  file's bytes before the create arrived. The server now refuses such a create, and the whole batch with it
  (`400 blob_not_uploaded`, or `400 bad_blob_hash` for a malformed hash); upload the bytes and retry. A read-only
  view of the workspace cannot use this to learn whether a file it cannot read exists. Cluster: upgrade every server
  before relying on this. Also: uploading bytes the server already has now counts as a fresh upload for the clean-up's
  grace period, and `grace_sec` values that are not a whole number of seconds are refused (`400 bad_grace_sec`)
  instead of being read as 0, which removed every upload not yet used by a file.
- **Cluster: a commit could be refused on one server and accepted on another.** When a commit touches a file
  someone holds a lease on, each server checked the lease against its own clock as it applied the commit, so a
  server that was catching up (or whose clock was off) could decide differently from the one that answered you:
  a commit you were told succeeded could be missing on that server, or one you were told was locked (`423`) could
  be applied there. Every server now checks the lease at the time the commit was accepted. A server that restarts
  also keeps the expired leases it had until the regular clean-up removes them on every server alike.
- **Changes behaviour:** **Security: `acp mount acp://<host>/<space>/` sent your default profile's token to whatever host the address
  named.** A pasted address for someone else's server therefore received your login. `acp mount` now uses the
  profile whose server is the address's host and port (as `acp --uri` always has), and stops with "no profile for
  https://host:port" when you have none. `acp-mount` itself also refuses a token whose server (`ACP_SERVER` /
  `--server`) is another host, or an `ACP_TOKEN` given with no server at all; a `--token` typed on the command line
  is still accepted. Upgrade if you mount addresses you did not write yourself. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#tls-profile))
- **Changes behaviour:** **Security: a read-only (`reader`) login could commit files and take leases.** Six endpoints that change state
  answered any HTTP method, while the role check lets a reader make any `GET`. They now accept `POST` only (other
  methods get `405` with `Allow: POST`): `/v1/commit`, `/v1/lease/acquire`, `/v1/lease/renew`, `/v1/lease/release`,
  `/v1/mail/ack`, `/v1/agents/beat`. Every ACP client already uses `POST` for these, so nothing changes for them.
  Affects reader logins, `-shared-token-role reader` and reader invites on all earlier versions: upgrade. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#post-only))
- **Changes behaviour:** **`acp push --force` sent files you had not changed, reverting your teammates.** `--force` is meant to override
  the stale check for a file YOU edited, but it also sent every other file that differed from the server, so a
  teammate's newer version was replaced by your older copy and a file they had deleted came back. Now a file you
  have not changed since your last sync is never sent, with or without `--force`, and a plain `acp push` no longer
  stops on such a file as "stale" either; your next `acp pull` brings their version (or their deletion). ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#pull-dotfiles))
- **On macOS and Windows, two shared files whose names differ only in letter case could damage each other.**
  Your disk treats `README.md` and `Readme.md` as one file, but a shared space can hold both. `acp pull` wrote both
  into that one file, and the next `acp push` then deleted one of them on the server and overwrote the other with
  its bytes. Now `acp pull` writes the first one and reports the other by name (read it with `acp show <path>`;
  rename one of the two from a case-sensitive machine); `acp push` never deletes or overwrites a case twin, and a
  checkout an older version left in that state is put right by the next pull. A name with an accent that the disk
  stores in another Unicode form (macOS HFS+) is recognised as the same file, not pushed as a new one. Renaming a
  file by case yourself (`notes.md` → `Notes.md`) is still pushed as a rename.
- **Changes behaviour:** **`acp pull --force` deleted files you had changed.** When the other side deleted a file you had edited, a
  forced pull removed your copy with no backup. `--force` still replaces your changes with a newer shared version,
  but a file you changed is now never deleted: it is kept and reported as a conflict (delete it yourself if you
  mean to), the same split `acp push` makes between `--force` and `--force-delete`. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#pull-dotfiles))
- **Changes behaviour:** **`acp pull` lost the other side's edit of a very large file.** When both sides changed a text file too large to
  merge line by line (roughly 5,000 lines or more), pull kept only your version, told you to resolve conflict
  markers that were not there and to run `acp push --force`, which then overwrote their edit. Now your file is left
  exactly as it was, their version is saved next to it as `<path>.remote`, and pull says the file was too large to
  merge, as it already did for binary files. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#pull-dotfiles))
- **`acp pull` with a read-scoped login could delete local files when the server was busy.** Pull asks the server
  which folders your login may read before it applies remote deletions; if that question failed (a 429 or a server
  error), pull treated the login as unrestricted and deleted your copies of files it simply could not see. Now it
  keeps those files, says why, and applies the real deletions on the next pull.
- **`acp pull` could write a conflict copy over a real file.** The other side's version of a conflicted file was
  always saved as `<path>.remote`, even when a shared file or a file of yours already had that name: its contents
  were replaced, and a later `acp push --force` could upload the conflict copy over the shared file. Pull now uses
  the first free name (`<path>.remote`, `<path>.remote.2`, …), never a shared or tracked file and never a local file
  with other contents, and reuses a copy that already holds the same version. `acp push` waits until every such
  copy is merged and deleted.
- **Two `acp push`/`acp pull` runs in one folder at the same time could mix up files, and a pull's temporary file
  could be pushed.** Every pull downloaded into the same temporary name (`<file>.acp-tmp`), so two pulls running at
  once (a scheduled job and you) could put one download's bytes under the other's name, and a temporary file left by
  an interrupted pull was uploaded by the next push as a shared file. Now a push or pull waits (up to 30 seconds) for
  another one running in the same folder, then stops with a clear message; downloads go to a temporary file of their
  own whose name starts with a dot and is never pushed; leftover `*.acp-tmp` files are never pushed either; and a
  downloaded file is on disk before it replaces yours, so a power cut leaves the old or the new file, not an empty one.
- **Security: `acp pull` could write files outside your folder, or into `.git`.** If a folder inside your checkout
  was a link to somewhere else (for example `.cache` linked to another folder), a pull wrote the space's files into,
  and removed them from, the folder the link pointed to. And a pull wrote every shared path, so anyone who could
  write to the space could put a file in `.git/hooks/` (a program git runs for you) or overwrite `acp`'s own sync
  state. Now a pull never writes or removes anything through a link (it reports a conflict and leaves both alone),
  and never writes shared paths inside `.git`, `.acp` or `.acp-crdt` folders (it says it skipped them).
- **`acp pull` reset file permissions.** Updating a file replaced it with a new one readable by everyone and not
  executable, so a shared script lost its execute bit and a private (`600`) file became readable by others. A file
  pull updates now keeps its permissions; new files are created as before.
- **A pull that stopped part-way could later report a conflict you did not have.** `acp pull` recorded what it had
  synced only when it finished. If it stopped part-way (a failed download, a full disk) after merging a file, the
  next pull merged that file again against the old record, and a newer change from the other side to the lines it
  had already merged showed up as a conflict. A pull that stops now keeps the record of every file it finished.
  Pull also handles files in path order, so a failure always stops at the same place.
- **A crash or a full disk while `acp` wrote one of your files could leave it empty or cut short.** `acp crdt
  sync`, `acp crdt pull`/`watch` and the merge step of `acp pull` wrote straight into your file. They now write a
  new copy next to it, make sure it is on disk, and only then swap it in, so you always have either the old or the
  new text. The file keeps its permissions, and if the path you gave is a link, the file it points to is updated
  and the link stays.
- **Two `acp` commands that changed your settings at the same time could lose one change.** `acp join` in one
  terminal and `acp config set` (or `acp link`, `acp config use`) in another each saved their own copy of
  `config.json`, so one profile or setting disappeared; the same happened to the saved lease tokens of two `acp
  lease` commands. These changes now wait for each other. `config.json`, the sync record, the lease tokens and the
  join's retry key are written so that a crash or power cut leaves the old or the new file, and a sync record that
  cannot be saved is now reported instead of ignored.
- **`acp update` could leave a mix of old and new programs.** It replaced `acp`, `coordd` and `acp-mcp` one at a
  time as it unpacked them, so a download cut short (or a full disk) part way through left, say, the new `acp` beside
  the old `coordd`, and a power cut could leave an empty program file. Now every program is unpacked and saved to
  disk first, and only then are they all swapped in; if anything fails before that, nothing is changed.
- **Go mount (`acp-mount`) on a CAS space: renaming an empty folder lost it.** `mkdir notes; mv notes ideas` left a
  folder under neither name. The renamed folder now exists under its new name, as on the live mount.
- **Changes behaviour:** **`acp pull` overwrote a dot-file you had edited** (`.env`, `config/.rc`). Files whose names start with a dot are
  not scanned, so pull treated your copy as missing and wrote the shared version over it. Pull now looks at each
  shared dot-file directly: one you edited is a conflict (the shared version is saved as `<path>.remote`, yours is
  kept); one you did not edit is updated as before. **Changed:** when the other side deletes a shared dot-file you
  did not edit, pull now removes your copy too, as for any other file (before, it was always left behind); an
  edited one is kept and reported. A folder or link sitting where a shared dot-file goes is never replaced or
  removed, even with `--force`. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#pull-dotfiles))
- **`acp pull` into a sync folder whose own name starts with a dot** (`~/.work`, `./.shared`) overwrote every
  local edit in it: the scan skipped the folder itself as a dot-name, so every local file looked absent. The folder
  you sync is now always scanned (files and folders starting with a dot inside it are still skipped, as before).
- **TypeScript live mount: a teammate's text could be removed when a shared text file was tidied up on the server.**
  If the server compacted a live text file between your write and the moment the mount sent it, the mount compared
  your whole file with the server's latest text, so a line a teammate had just added was sent as your deletion (and
  text they had just deleted came back). The mount now carries your own edit across unchanged, as the CLI, the MCP
  bridge and the Go mount do: both edits stay. If the server keeps compacting, the write stays pending and the
  flush says so. (This part of the TypeScript SDK has not been published to npm yet.)
- **TypeScript mounts after a lost reply:** when the server applied a flush but the reply never arrived, the retry
  treated the mount's own landed files as someone else's and wrote `.conflict` copies of your own bytes (live mount:
  new files, overwrites and renames; both mounts: a second `.conflict.2` copy of a conflict copy already written).
  The retry now recognises its own result: no extra copies, and a real conflict is still reported. On a live mount,
  a text-file edit retried after a lost reply could also be partly ignored by the server; it now lands exactly.
- **Changes behaviour:** **TypeScript SDK checks every file it downloads.** A file's bytes are named by their SHA-256, and the SDK now
  checks them on every read (`getBlob`, `readBlobBytes`, `readBlobText`, signed links, both mounts). A cut-short or
  corrupted download raises the new `BlobIntegrityError` (mounts: `EIO`) instead of being returned or cached as the
  file. A streamed read reports it when the stream ends. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#ts-blob-integrity))
- **`acp://` addresses now mean exactly the same thing in the Go tools and the TypeScript SDK.** One shared table of
  300 addresses, plus about 2,400 generated variations, is now checked by both on every test run, and the differences it found are fixed. The TypeScript SDK no longer
  re-reads a look-alike spelling as a different, real host when it parses an address, picks your login profile,
  reads a server's discovery document or builds a share link: a Kelvin-sign "K" is not "k", a tab or a `\` inside a
  URL is refused, a percent-encoded or non-ASCII host is not decoded, `0x7f.1` is not `127.0.0.1`, and a `;` in a
  minted link's query is refused. Both now refuse a query key that spells `token` or `password` with a Unicode
  look-alike letter. The Go tools no longer turn a minted share URL with port `0` into a link to the default port,
  and neither side accepts a minted URL or a profile server that carries a login (`https://user:pass@…`). Addresses
  you write normally behave exactly as before.

- **A backup could silently leave out a whole workspace.** If one workspace's stored state could not be opened (for
  example after a power cut damaged its last write), `coordd admin dump`, `coordd admin migrate-store` and cluster
  snapshots still finished successfully, without that workspace. A cluster node that installed such a snapshot
  dropped the workspace, and a node whose only data was that workspace could delete its files while installing a
  snapshot. Now all three stop with an error that names the workspace and how to repair it, and a node that holds a
  workspace it cannot open always keeps its files when a snapshot is installed (as `spaces.pre-restore-<time>`).
- **Live text could be garbled after a sync whose answer was lost.** If `acp crdt sync`/`push` sent your edit and
  the server applied it, but the answer never came back (a dropped connection, ctrl-c, or a failure while fetching
  the others' edits afterwards), the next sync sent your later edits under the same internal ids, and the shared
  text came out wrong (for example `hello worldorld`) while your file showed something else. The CLI now records
  an edit in `.acp-crdt/` before sending it and sends that same edit again next time, so it lands exactly once.
  The MCP bridge (`acp_doc_write`) does the same: when a write gets no answer it now says the write is kept and
  will be sent again, instead of "not applied, the doc is unchanged".
- **`acp crdt pull` and `acp crdt watch` erased edits you had not synced yet.** Both wrote the shared text over your
  file without looking at it, so anything typed since your last `acp crdt sync` was lost (automatically, under a
  running `watch`). Now `pull` stops and tells you to `acp crdt sync` first, or to add `--discard-local` if you
  really want the shared text; `watch` leaves the file alone until you sync, then carries on. `acp crdt sync` also
  no longer overwrites a file you kept typing in while it ran: your newer edits are sent by the next sync.
- **Changes behaviour:** **An MCP agent could wipe a shared document by mistake.** `acp_doc_write` with an empty or partial text (the
  usual way a model gets it wrong) deleted the rest of the document for everyone. It now refuses a write that would
  delete more than 90 % of a document of 256+ characters, changes nothing, and tells the agent to read the document
  and send the whole text; a wipe that is really meant passes `confirm_truncate: true`. The Go mount (`acp-mount`)
  now refuses the same kind of save to a live text document (`Operation not permitted`, the save is kept unsent;
  save the file again with its text, or mount with `--allow-bulk` to wipe it on purpose). (`acp crdt sync` has had
  this guard since v0.1.6.) ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#doc-write-read))
- **Changes behaviour:** **TypeScript live mount: a save that would empty a shared text file is refused, as in the Go mount.** A save
  that would delete more than 90 % of a live text document of 256+ characters (an editor that crashed after emptying
  the file, or a tool that wrote before it read) now fails the flush with `EPERM`: nothing is sent, the save stays
  pending, and every other change in the flush still lands. Save the file again with its text and it goes through;
  a mount opened with `allowBulk: true` lets a wipe you mean through. Characters are counted the same way as in the
  Go tools (an emoji is one character). (This part of the TypeScript SDK has not been published to npm yet.) ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#mount-refuse-corrupt))
- **TypeScript live mount: saving a shared text file again while the mount was still sending your last save stopped
  co-editing on it.** The second save was sent as an ordinary file, so the shared text file went to the trash and a
  plain file took its place (you could restore it, but your teammates' edits no longer reached you). The new save now
  builds on the one being sent, as in the Go mount: it stays a co-edited text change, a teammate's edit made meanwhile
  is kept, and nothing is sent twice, whether the earlier send worked, failed, lost its reply or met a server tidy-up.
  The same fix covers saving, renaming and saving again before a flush, and a live mount taking over a CAS mount's
  pending new file where someone has since made a shared text file (now a conflict copy, the text file untouched).
  (This part of the TypeScript SDK has not been published to npm yet.)
- **TypeScript mounts: saving a file again while the mount was still sending your last save made a false conflict
  copy.** On a live mount, the newer save was compared with the file as it was before your earlier save landed, so
  your own earlier save looked like a teammate's change: the path kept the older save and your newest one went to
  `<name>.conflict`. The same happened when you renamed or deleted the file during the send, renamed a file right
  after saving it, or saved again after a send whose reply was lost (on both mounts). In that last case, deleting or
  renaming a new file could also leave it on the server. The mount now knows its own earlier saves: your newest save
  lands at the path, and a real teammate write in the meantime is still reported as a conflict. (This part of the
  TypeScript SDK has not been published to npm yet.)
- **TypeScript live mount: an editor's "save to a temp file, then rename it over the file" could put your earlier save
  back.** When an editor saved this way twice before the mount sent anything, or saved this way and then saved or
  deleted the file again, the mount sent an extra copy of the first save and then threw it away. The mount mistook
  that copy for a teammate's file and restored it, so the file held your older save, your newest save was left under
  `<name>~<id>`, a deleted file came back, and you got a conflict you didn't cause. The mount now sends only what your
  edits leave behind. If the reply to such a save is lost, the retry recognises that the save already landed and no
  longer leaves a second copy of the temp file. When a teammate's write arrives in the instant before your
  temp-file save lands, their file is put back and yours goes to `<name>.conflict`, the same as for an ordinary save.
  (This part of the TypeScript SDK has not been published to npm yet.)
- **Changes behaviour:** **TypeScript mounts: renaming a folder over an empty folder now works, and folder removals no longer take a
  teammate's new file with them.** The live mount refused `mv src dst` when `dst` was an empty folder; now the empty
  folder is replaced (it goes to the trash), as on the Go mount and in POSIX. Both TypeScript mounts now give the
  same error codes as the Go mount: a file over a folder is `EISDIR`, a folder over a file `ENOTDIR`, and a folder
  over a folder that holds anything `ENOTEMPTY` (they said `EEXIST`). If a teammate puts a file or folder into a folder
  you removed or replaced while it was empty, the live mount now reports a conflict and leaves their content and your
  folder where they are; before, your `rmdir` sent the folder to the trash with their file inside, without a word. On
  a CAS space, the mount also lost an empty folder you renamed (it existed under neither name afterwards), and let a
  folder be renamed over one holding only sub-folders, merging the two. (This part of the TypeScript SDK has not been
  published to npm yet.) ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#ts-rename-eisdir))
- **TypeScript live mount: no more false "delete" conflicts after a lost reply.** If the server's reply to a save that
  replaced a file by renaming another over it (`rm b; mv c b`, or a folder renamed over an empty folder) was lost,
  and a teammate then changed another file from that same save, the retry reported a "delete" conflict on the name
  your own rename now holds. Your files were never touched, but the warning was wrong. Now a delete removes only the
  file it was made for: if your own rename is already at that name, the delete counts as done. If a teammate's file is
  there, you still get a conflict and their file stays. This is the same rule the Go mount uses. (This part of the
  TypeScript SDK has not been published to npm yet.)
- **Links no longer borrow the TLS trust of an unrelated profile.** `acp uri read`, `acp uri discover` and `acp open`
  applied the default profile's certificate, pin and `insecure` setting to whatever host a pasted link named, so a
  development profile with `insecure` switched off certificate checks for a stranger's server. Trust now comes from the
  profile for that link's host and port. With no such profile the request uses the system's trusted certificates and
  is never insecure. With no profile selected at all you can still name one for a single run with `ACP_CERT` or
  `ACP_PIN`.
- **Reads now check what arrived.** A file's address is the hash of its bytes, and the Go clients now prove it: the Go
  SDK's `GetBlob` and signed-link reads end with a `*client.BlobIntegrityError` if the bytes do not match, so `acp blob
  get` fails (it used to exit 0 on a truncated download), `acp-mcp` reports an error instead of returning the bad bytes,
  and the Go mount answers `EIO` and never caches them. `acp uri read --out` no longer empties an existing file before
  downloading: a failed or corrupt download leaves it exactly as it was, and `acp open --out` leaves no file behind.
- **`acp-mcp`: a listing no longer counts as reading a file.** After reading a file, a later `acp_manifest` listing made
  the bridge treat the newest version as seen, so saving text based on the old read overwrote a teammate's change. A
  listing shows a hash, not the content: that save is now a conflict, and reading the file again clears it. Paths you
  never read still anchor on the listing. The bridge also stops if the server reports a hash that is not the hash of
  the bytes it was sent.
- **Go SDK: an empty `200` where a result was expected is now an error**, not an empty success that reads as zero
  values (a proxy that cuts off a response, for example). `204 No Content` is unchanged.
- **Go mount and `acp fs` handle two refusals from the server properly.** When the server reports that a file's bytes
  are no longer there (`400 blob_not_uploaded`, for example they were cleaned up between the upload and the save), the
  mount and `acp fs put` upload them again and resend, with no error. When a login may not delete that much at once
  (`403 bulk_denied`), the mount answers `EPERM`, keeps the folder and the pending changes, and no longer shows itself as
  disconnected; `acp fs rm` says plainly that nothing was moved to the trash.
- **`acp push`, `pull`, `rm`, `manifest` and `acp mount` no longer guess when they cannot learn a space's mode.** If
  the check failed for any reason (a dropped connection, a server error, a refused login), they carried on as if the
  space were a plain versioned one. On a live space that meant acting on an old frozen snapshot, and a mount showing it
  as the live folder. Now they stop with a clear error and change nothing. Only a server too old to have live spaces
  counts as "not live" (`client.ErrNoFSMode` in the Go SDK).
- **Changes behaviour:** **`acp push` no longer publishes files from outside the folder through a symbolic link.** A link in the checkout
  to a file elsewhere (say `~/.ssh/id_rsa`) was followed and its contents uploaded to the shared space. Push now skips a
  link whose target is outside the checkout, with a warning naming it, and pushes the rest. If such a link replaced a
  shared file or folder, the shared copy stays as it was: a skipped link never counts as a delete. Links to files
  inside the checkout work as before. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#push-symlink))
- **Changes behaviour:** **Go clients no longer change text that is not valid UTF-8.** JSON text is UTF-8, and Go's JSON encoder silently
  replaces anything else with `�`, so a mail, an event note, a structured-document value or a presence state read from
  a file or terminal in another encoding (Latin-1, UTF-16, a cut-short write) reached the other side changed, with no
  error. The Go SDK now refuses such a request before sending it, with an error naming the field
  (`client.ErrInvalidText`, `*client.InvalidTextError`); a string escape holding half a surrogate pair (`"\ud800"`)
  is refused the same way. `acp crdt push`/`sync` refuses a file that is not UTF-8 by name instead of writing `�` into
  the shared document, and `acp-mcp` answers such a request with an error. A file whose NAME is not UTF-8 cannot be
  shared under that name: `acp push` skips it with a warning and sends the rest, and the mounts refuse to create it
  (`EILSEQ` through FUSE, `EINVAL` elsewhere). Also fixed: a request body that could not be encoded was sent empty;
  it is now an error. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#utf8))
- **Changes behaviour:** **Go mount (`acp mount`, live space): an editor's "safe save" no longer ends co-editing on a shared text file.**
  Same fix as the TypeScript one below: renaming a plain file over a live text file writes its text INTO the
  document (your teammates' edits made meanwhile are kept, the old text stays in the document's history, the 90 %
  wipe guard applies, `--allow-bulk` overrides it) and removes the temp file. If the temp file or the document cannot
  be read at that moment the rename fails (`EIO`), and if the mount's write buffer is full it fails with `ENOSPC`;
  either way nothing changes and the editor can retry. A file that is not text still replaces the document as before,
  byte-exact, and the document goes to the trash intact. ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#mount-refuse-corrupt))
- **TypeScript live mount: an editor's "safe save" no longer ends co-editing on a shared text file.** Many editors
  save by writing a temp file and renaming it over the file. On a shared live text file that rename replaced the
  shared document with a plain file: the document went to the trash and your teammates' edits stopped reaching you.
  Now renaming a plain file over a live text file writes its text INTO the document, the same as saving the file in
  place: your teammates' edits made meanwhile are kept, the old text stays in the document's history, the 90 % wipe
  guard applies, and the temp file is removed. If the temp file or the document cannot be read at that moment, the
  rename fails and nothing changes. A file that is not text (a NUL byte or invalid UTF-8) still replaces the
  document as before, byte-exact, and the document goes to the trash intact, so you can restore it. (This part of
  the TypeScript SDK has not been published to npm yet.)
- **Go mount (`acp mount`, live space): writing a non-text file into a shared live text file no longer garbles the
  text.** Bytes that are not text (a NUL byte, or invalid UTF-8 such as part of an image) saved into a live text file
  were turned into replacement characters (`�`) when the save was sent, for everyone. Now such a save is refused with
  `EINVAL` (invalid argument) and the document keeps its text: a whole-file save at once, with nothing sent; a file
  written through an open handle when it is flushed (`fsync`/close), because a save written in pieces can cut a
  character in half until it is complete. The refused save stays pending and writing the file again with text replaces
  it. Save the bytes as a plain file under another name. A leading UTF-8 byte order mark stays part of the text.
- **TypeScript live mount: writing a non-text file into a shared live text file no longer garbles the text.** Bytes
  that are not text (a NUL byte, or invalid UTF-8 such as part of an image) saved in place into a live text file were
  turned into replacement characters (`�`): everyone's copy of the document got garbled text, and your bytes were lost.
  Now `writeFile` refuses such a save with `EINVAL`, naming the file, and nothing is saved or sent. Your bytes are
  untouched, so save them as a plain file under another name. A text file that starts with a byte-order mark (BOM)
  now keeps it: before, the mount dropped it from the shared document on every save. The Go mount already kept it.
  (This part of the TypeScript SDK has not been published to npm yet.)
- **Changes behaviour:** **TypeScript mounts: reading a text file and saving it back no longer removes its byte-order mark (BOM).**
  `readTextFile` dropped a leading BOM, so the everyday `writeFile(p, await readTextFile(p))` silently changed a
  shared document or file that starts with one. Now `readTextFile` (both mounts) and `client.readBlobText` keep it, so
  reading and saving back gives the exact same bytes, as Node's `fs.readFile(p, "utf8")` and the Go mount do.
  (This part of the TypeScript SDK has not been published to npm yet.) ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#ts-bom))
- **Changes behaviour:** **TypeScript SDK: text with half a character is refused instead of silently changed.** A JavaScript string can hold
  a lone UTF-16 surrogate (half of an emoji, for example after cutting a string in the wrong place). It has no UTF-8
  form, so `writeFile(path, string)` saved `U+FFFD` (`�`) in its place without a word, on any file. Now both mounts
  refuse such a string, or such a path, with `EINVAL` naming the file, and nothing is saved or sent. The rest of the
  SDK does the same with a `MalformedTextError`: `putBlob(string)`, any request whose JSON holds such a string (mail,
  notes, document edits, presence), and `TextDocSession.setText`, where the server would have stored `U+FFFD` while
  your own copy kept the original. (This part of the TypeScript SDK has not been published to npm yet.) ([upgrade note](docs/MIGRATING-v0.2.5-to-v0.2.6.md#ts-lone-surrogate))
- **An `acp://` address with half a character in it now points nowhere, instead of at another file.** In the
  TypeScript SDK, a file name holding a lone UTF-16 surrogate was written into the address as `%EF%BF%BD` (`U+FFFD`,
  `�`) — the name of a different, real file — so a share link, a canonical address or a lookup could quietly point at
  that file. Now every address the SDK reads or builds (`parseACPURI`, `formatACPURI`, `acpLinkFromBlobURL` including
  the signed link's own values, and an address object you built yourself passed to `canonicalACPURI`,
  `resolveACPURI`, `sameACPResource`, `clientForACPURI` or `readACPCapability`) refuses it with `percent_encoding`,
  the same answer the Go SDK gives for text that is not UTF-8; the Go SDK now also refuses a hand-built address with
  such text in `Lookup`, `ClientFor`, `ReadCapability` and `SameResource`, and sends nothing. Names with any real
  character, including emoji, `U+10FFFF` and a real `U+FFFD`, work as before. The `acp://` specification (ext-32)
  states the rule, and shared Go and TypeScript test vectors pin it. (This part of the TypeScript SDK has not been
  published to npm yet.)
- **A signed download URL, a discovery endpoint or a profile server with a fake IP address in brackets is now refused
  the same way everywhere.** Only a real IPv6 address, with no zone, may sit in `[...]`. The Go SDK used to leave this
  to Go's URL library, whose answer changes between Go releases: on older ones `https://[1::2::3]:8443` was accepted as
  a discovery endpoint. Both SDKs now apply the rule themselves, so an address reads the same on every Go version.
- **`acp crdt sync` could duplicate a whole shared document.** Run in a folder with no sync state for the document
  (another folder, a new machine, or after a crash left `.acp-crdt/` empty or damaged), it sent the whole file as
  new text, and everyone's copy then held it twice. Now a file equal to the shared text is simply taken as synced,
  and a file that differs is refused with the two ways forward: `acp crdt pull … --discard-local` (take the shared
  text) or `acp crdt sync … --adopt-local` (send your file's differences as your edit).
- **Text you typed could vanish when a teammate deleted the words around it.** If a teammate deleted the text right
  before what you typed and the server tidied the document up before your `acp crdt sync` (or before the Go mount
  sent your save), your typing was dropped, from your file too. It is now kept, placed where the deleted text was.
- **A live-text sync or `acp_doc_write` of more than 4096 characters at once failed** (`too many crdt ops in one
  push`), for example the first sync of a long file. The CLI and the MCP bridge now send a large edit in parts.
- **Server, encryption at rest: a file upload could be answered "stored" when the disk had not kept it.** If the
  disk accepted the write but failed to flush it (disk full on a thin volume, a quota, a network file system), the
  encrypted upload ignored the error, put a possibly short file in place and answered 200; reading the file later
  failed. The upload now fails instead (the client sends it again). A later upload of the same content also repairs
  a stored copy of the wrong size instead of reusing it (for encrypted and plain storage).
- **Server (file storage): a power cut right after a save could undo it.** The server answered a commit or an
  upload once the new file was written and renamed into place, but the rename itself was not yet on disk, so a power
  cut in the next few seconds could bring back the previous version of the workspace (or lose an uploaded file a
  later version names). Every such write now waits until the rename is on disk. This adds a few milliseconds to each
  commit on the file storage edition (measured on a cloud disk: 7 → 11 ms per commit).
- **Server: a power cut while the server tidied its record of repeated requests could make a workspace refuse to
  open** ("corrupt idem snapshot"). The tidy-up emptied the old record before the new one was safely on disk; the
  order is now the other way round.
- **Server (file storage): a crash in the middle of tidying up a log could renumber it.** When the server compacted
  an event log, mailbox or live document, it rewrote the log and then its small index file; a crash between the two
  reopened the new log with the old index — event numbers could be handed out again, and a live document could
  forget that it had just been compacted. The server now records what it is about to do first and finishes (or
  discards) it when it starts again.
- **Server: start-up no longer deletes the record of a save it cannot match to the workspace's current version.**
  The server keeps a record of every version of a workspace (who saved what, every path and its content address).
  If a crash or power cut left a record newer than the workspace's current version, start-up deleted it, although
  after a power cut the save may already have been answered as done, and that record was then the only list of what
  it held. Start-up now moves such a record to `history/torn/` inside the workspace's data folder and logs an
  `OPERATOR NOTE` naming it; nothing is deleted. If the same save is applied again (a cluster node catching up),
  the identical copy is removed automatically. The same applies when a snapshot restore replaces a damaged record.
- **Server: after a crash, a repeated request could be carried out twice.** The server remembers each request's
  `Idempotency-Key` so a retry is answered from its record instead of running again. A crash in the middle of
  writing one of those records left half a line at the end of the record file, and the next record was written
  straight after it, so that next (already answered) request was forgotten when the server started again, and its
  retry ran a second time (a duplicate message or event). Start-up now cuts the unfinished half line (it was never
  answered), a record is never written onto one, and records written after such a line by an older version are read
  back. `coordd admin repair-idem-wal` also waits for its rewrite to be on disk before it reports success.
- **Server (file storage): a power cut during a save could make a whole workspace unavailable.** If the server was
  cut off while it was adding a line to a workspace's event log, mailbox or a live document, the half-written last
  line (never answered as saved) made the workspace refuse to open, and the only advice given was to restore it from
  a backup. The server now moves that unfinished line to a file named `.torn-<time>-<id>` next to the log, logs an
  `OPERATOR NOTE`, and opens the workspace with everything that was saved. A last line that is complete but lost
  only its line break is kept. A damaged line in the middle of a log is still refused, as before.
- **A snapshot file now checks how many workspaces it says it holds**, so a file cut short between two workspaces
  is refused instead of being restored without the missing ones.
- **`acp-mount`: a write made after a failed save could be silently lost.** When the mount's save (flush) of a file
  failed (for example a dropped connection) after it had uploaded the bytes, and the file was then written again
  through an open file (an append, or an editor writing in place), the next save sent the OLDER bytes: the server
  kept the earlier text and the later write was gone. The mount now always sends what the file holds. Also, a save
  whose reply was lost followed by another save of the same file no longer reports a conflict with your own earlier
  bytes (no `.conflict` copy of your own file).
- **Go live mount (`acp-mount` on a live space): saving through a temp file could lose the save.** Most editors save
  by writing a temporary file and renaming it over the original. If the rename happened before the mount sent the
  temporary file, the new file reached the server under neither name; a file overwritten and then renamed arrived
  with its OLD contents; and `mv b c; mv a b` (or a swap through a temp name) could move the wrong file. The mount now
  keeps every rename as its own step, carries a save along with the file it renames, and sends changes in the order
  you made them. Writing the old name again after a rename (`mv a b`, then a new `a`) keeps both files instead of
  reporting a conflict, and unlinking a file whose first save may already have reached the server removes that copy.
  Renaming a file over an existing one replaces it (the old one goes to the trash) instead of leaving `name~<id>`
  next to it, and a file a teammate put at the destination since is reported as a conflict, never overwritten.
  Renaming a file over a folder, or a folder over a file or a non-empty folder, is refused as on a local disk.
  After a lost reply, deleting a file you had just saved and writing it again, or saving a file you had just
  renamed, no longer reports a conflict with your own change.
- **Go live mount (`acp-mount` on a live space): a conflict with a teammate now leaves your version as a file
  beside theirs.** When your save and a teammate's write meet on the same file, theirs keeps the name and yours is
  saved next to it as `<name>.conflict` (or `<name>.conflict.2`, … when that name is taken); the save succeeds and the
  event is logged in `.acp/conflicts.jsonl`. This also covers an editor's temp-file save renamed over a file a teammate
  wrote in that same instant. If the same version of yours conflicts again, the existing `.conflict` file is reused,
  not copied. Before, the mount kept your version only in its memory and the save failed, or two files with the same
  name were left, one shown as `name~<id>`. With `--conflict=error` the save still fails (`ESTALE`) and your version is
  held, as before. The TypeScript live mount already worked this way.
- **Go live mount: after several lost replies in a row, a renamed file could end up in the trash.** If a flush that
  deleted a file and renamed another one over its name lost its reply, the retry could run the delete again and
  trash the renamed file. The mount now checks that the server shows everything such a flush did, and then never runs
  it again; nor does it report your own changes as conflicts. A delete now only removes the file you deleted: if a
  teammate rewrote it, or put a new file into a folder you removed, before your delete reached the server, their file
  stays and it is reported as a conflict.
- **Go live mount (`acp-mount` on a live space) after a lost reply:** when the server created a new file but the
  reply never arrived, the next flush created it again, so the folder showed `name` and `name~<id>`, and a rename
  or overwrite whose reply was lost was reported as a conflict with your own change. The retry now sees its own
  result and does nothing more (also when the file was saved again in the meantime). A teammate who
  created the same name first is now reported as a conflict (your bytes kept with it) instead of a silent second
  file, and `mkdir` over a teammate's new file likewise. An editor save that deletes the file and writes it again
  now replaces the file instead of leaving the old one in place next to a second copy. A live text file saved again
  while the previous save was still being sent, or after its reply was lost, could keep the OLDER words on the
  server while your file showed the new ones (the second save reused the first one's internal ids); each save now
  builds on the one before it and lands exactly. And if a teammate replaced a file in the instant your overwrite or
  delete of it was being sent and that reply was lost, their file stayed in the trash without a word; the retry now
  restores it and reports the conflict, as it already did when the reply arrived.
- **Go live mount: a live-text save right after a server tidy-up, or after failed flushes, could lose its words.**
  If the server compacted a shared text file while an earlier save of yours was being sent and its reply was lost,
  your next save could be accepted with words typed where a teammate had just deleted text: the mount showed them,
  but a new reader of the file never saw them. And after two or more flushes that failed before sending anything (the
  server unreachable), the next save could reuse the internal ids of the last save that did reach the server, so the
  server kept that older text and your newest words never landed. Both saves now land exactly as you typed them,
  next to a teammate's edits.

### Added
- **The server says which version it is:** `GET /v1/healthz` and the public start page (`GET /`) now include
  `"version"` (e.g. `"v0.2.6"`), and `acp status` shows `version: server … · this acp …`, so you can tell which
  release a server runs without logging in to it. It is for people; clients still decide from `capabilities`.
  (The start page can still be turned off with `coordd -root-page=false`.)
- **An OpenAPI 3.1 description of the whole API at `GET /openapi.json`:** every endpoint the server serves, with
  plain-language summaries, query and path parameters, request and response bodies, the statuses each one really
  returns, who may call it (role), and the `acp` command that uses it. `info.version` is the server's release. No
  token needed while the start page is on (`-root-page`, the default); with `-root-page=false` any token can read it.
  It is linked from the start page (`discovery.openapi`). It describes this server; the RFCs stay the protocol.
- **TypeScript SDK: `waitForMail(from?, signal?)`**, as in the Go SDK: waits until you have unread mail (optionally
  from one sender) and returns it, without polling and without missing a message sent while it starts. Bound it with
  a signal such as `AbortSignal.timeout(30_000)`: it then rejects with `NoMailError`, or with a plain error if it could
  not reach the server's event stream, so "nothing yet" and "can't tell" stay different. Nothing is marked read.
- **TypeScript SDK: one shared event stream per client, and no silent gaps after a long absence.**
  `client.subscribe(fn, { filter, from, onResync })` lets mail, chat, documents and files share ONE connection to
  the server (a browser allows only a few per site); each subscriber keeps its own filter and starting point, and
  never sees an event twice or out of order. `followForever` takes `onResync`: when you reconnect after the server
  removed old history you had not seen yet (or its history was restored to an earlier point), it tells you before
  the next event, so you can re-read your snapshot instead of carrying on with a silent hole. Both mounts use it:
  after such a reconnect they re-read the file tree, serve the current bytes and tell watchers what changed.

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

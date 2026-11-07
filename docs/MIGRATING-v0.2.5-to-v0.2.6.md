# Upgrading from v0.2.5 to v0.2.6

**What do you need to do?** Most people: back up, update, restart. Nothing else. The wire protocol is still `acp/1`,
and existing servers keep their data, logins and settings.

Read section 2 if you have **scripts** that call `acp`, section 3 if some machines will run **different versions** for
a while, and section 4 if you **run a server**. Section 6 is the way back; read it before you upgrade, because a
backup is the only safe one.

---

## 1. Most people: just update

**One server**

1. Back up. Stop `coordd` and copy the whole `-data` folder (or, with it stopped, run
   `coordd admin dump --from <data folder> --out <file>` with the binary you are upgrading from). The server-database edition may convert stored data in place on its first start and says so in
   its log; that conversion keeps no copy of the old format, so the backup is your way back.
2. `acp update` on the server's machine (or bump the Docker tag to `ab0tcom/acp:v0.2.6`). It does not restart a
   running `coordd`: restart it yourself. It updates an `acp-mount` that sits beside `acp` (new in this release;
   elsewhere, take it from the release tarball). A running mount keeps the old program until you unmount it and
   mount again, and the mount fixes in section 4 need that.
3. `acp update` on each machine that runs `acp` or `acp-mcp`. Clients and servers can be upgraded in any order
   (section 3 lists the one thing an old client cannot do).

**A cluster (beta)**

v0.2.6 raises the replicated command version to 7 (v0.2.5 was 6). What the code does with that:

- The leader stamps **every** replicated command with its own version. A v0.2.6 leader stamps 7.
- A v0.2.5 node that applies a command stamped 7 **stops itself** (it logs `OPERATOR ACTION: refusing to apply a
  replicated command this binary is too old to apply safely` and exits) rather than apply it under old rules. It does
  not mark the command as done, so after you restart it on the v0.2.6 binary it applies the command and carries on.
- A v0.2.6 node that applies a command stamped 6 (from a v0.2.5 leader, or from an old log) uses the v0.2.5 rules
  for it. So a v0.2.6 follower of a v0.2.5 leader does exactly what that leader's other followers do, and old logs
  replay the way they always did. The new replicated behaviours (section 4) only apply to commands a v0.2.6 leader
  has stamped 7.
- A node that joins reports its command version. A leader refuses a joiner whose version is lower than its own
  (`409 command-schema too old … upgrade the joiner to the cluster's newest version before it joins`). So a
  v0.2.5 node cannot be added to, or re-join, a cluster led by v0.2.6. Finish a rolling upgrade before writers resume
  under leases: a v0.2.5 leader and a v0.2.6 follower can judge a lease that expires in that same second differently.

<a id="cluster-order"></a>**The order, and why.** Once a v0.2.6 node leads, a node still on v0.2.5 stops at the first command that leader
stamps, and that comes at once: a v0.2.6 leader replicates a new log epoch as soon as it is ready (the log id:
API_REFERENCE, "Log id"), with no client write needed. So a v0.2.6 node must not lead while v0.2.5 nodes are a
majority, or they all stop and the cluster loses its majority. A v0.2.5 node also cannot install a snapshot from a
v0.2.6 node (snapshot format 5); a v0.2.6 node still reads the format-4 snapshots v0.2.5 wrote.

1. **Back up every node** (stop it and copy `-data`, or snapshot the volume).
2. **Find the leader:** `GET /v1/cluster/status` on any node (`leader`). Leave it running and do not restart it until
   step 4.
3. **Upgrade the followers one at a time:** stop one, replace the binary, start it with the same flags. Before the
   next, check it is back: its `/v1/cluster/status` shows `state: Follower`, the same `leader`, and
   `command_schema_v: 7`, and its `GET /v1/stats` `events` matches the leader's. A follower that restarts cannot take
   the lead from a leader that is up (raft's pre-vote), so the leader stays on v0.2.5 meanwhile, and the upgraded
   followers apply its commands by the v0.2.5 rules.
4. **Upgrade the leader last,** once a majority of the voters run v0.2.6 (with 3 nodes, both followers; with 5, at
   least 3 nodes, which with the leader last means all 4 followers). Stopping it hands the lead to an upgraded node;
   that leader's first command stops nothing the majority needs. Start the old leader on v0.2.6: it catches up.

**If it goes wrong.** If the leader is lost before a majority is upgraded (it fails, or is restarted early) and an
upgraded node takes the lead, every node still on v0.2.5 stops with `OPERATOR ACTION: refusing to apply a replicated
command this binary is too old to apply safely` in its log, and the cluster has no majority: writes answer `503`
("no leader available"; safe to retry), `/v1/cluster/status` shows no `leader`. Nothing is lost: a stopped node did
not mark that command as done. **Do:** put the v0.2.6 binary on every stopped node and start it with the same flags
(no `-join`, same data). Each applies the command it stopped at, a majority is back, and a leader is elected. Running
a mixed cluster for longer than the upgrade is not supported.

**After reverting a VM or container to a snapshot that includes memory, restart coordd** (every node of a cluster).
The event log's id (new in this release) changes at every start, which is how clients see a restore; a revert brings
the process back with no start, so without the restart a restored log keeps its old id.

Also for clusters: a node started with `-token` on the command line now logs a warning; `-token-file PATH` does the
same job without putting the token in `ps` (section 5).

---

## 2. Scripts may need a change

### <a id="cli-events-text"></a>`acp events` and `acp thread` print readable text by default

| Command | New default | The old form |
|---|---|---|
| `acp events` | the newest 50 lines of the story | `--plain` (the old `seq=` line), or `--json` / `--raw` (the stored events) |
| `acp thread <id>` | the conversation as text | `--json` |

`acp events --from N` still reads everything from N. `acp watch` is unchanged. A script that runs `acp events` with
no view flag and its output not on a terminal gets a one-line tip on stderr:
`tip: acp events is readable lines by default since v0.2.6; a script should say --plain (the old line) or --json`.
**If you parse either command, add the flag.**

### <a id="cli-flags"></a>A mistyped flag, or a flag with a value, now stops the command

Nothing is done, and the exit is non-zero.

- A flag that **no** `acp` command reads is refused and the nearest real flag is named:
  `acp send has no --subjct flag, so nothing was done. Did you mean --subject?`. A flag that some other command
  reads is still accepted quietly, so a working command line keeps working.
- An on/off flag written with a value is refused: `--force=true`, `--peek=true`, `--dry-run=true` all stop with
  `--force takes no value, so nothing was done: write --force on its own`. Before, the `=true` made the flag invisible
  and the command ran without it. **A script with `acp push --force=true` ran without force and now stops.** Flags
  that take a value (`--note=why`) are not affected.
- `--dry-run` is accepted by `acp push` only. Anywhere else it stops with `acp rm has no --dry-run, so nothing was
  done.` (before, `acp rm x --dry-run` deleted `x`). `acp pull --dry-run` is refused too. `acp open` and `acp
  handler` keep their own `--dry-run`. The one-dash `-dry-run` is refused with a note that the flag is spelled
  `--dry-run`.
- `--profile` naming a profile for a different server than the `acp://` address you gave is now an error instead of
  quietly acting as someone else.

### <a id="cli-pull-stop"></a>`acp pull` can stop before removing files
A pull that would remove most of the folder (10 files or more, and at least half) now exits non-zero and changes
nothing, and so does a pull that finds the server's history no longer holds what this folder synced. A script that
really mirrors deletions adds `--allow-bulk-delete`. Removed files are kept in `.acp/trash/v<version>/` for 7 days, so
the folder uses that much more disk for a while; keep `.acp` out of anything you publish, as before.

### <a id="cli-put-replace"></a>`acp put --replace` checks what you read

It used to overwrite with no check. It now stops if the file is not the version you last read here with `acp show` (or
wrote with `acp put`), and it stops if there is no such record, which is what a script that never ran `acp show` will
see. **A script that means last writer wins must say `--force`.** A script that knows what it edited can pass
`--base <hash or version>`. `acp_write_address` on a realtime workspace follows the same rule: a read, then a write
that lands without `force` unless someone changed the file.

### <a id="post-only"></a>Six routes accept POST only

`/v1/commit`, `/v1/lease/acquire`, `/v1/lease/renew`, `/v1/lease/release`, `/v1/mail/ack` and `/v1/agents/beat`
answer anything but `POST` with `405` and `Allow: POST`. Every ACP client already uses `POST`. If you call these
routes by hand, check the method. (This closes a hole in which a read-only login could commit files and take leases.)

### <a id="cli-json-lease"></a>`--json` on lease, send and reply, and the lease line

- `acp lease acquire|renew|release|list --json` and `acp send|reply --json` print one stable JSON result with a
  `status` of `acquired`, `renewed`, `released`, `held`, `sent` or `replied`. A held lease still exits 2. **New
  scripts should use `--json`.**
- The first line of `acp lease acquire` and `renew` now reads
  `acquired db token=7 expires=1793605942 (fence token 7)`. v0.2.5 printed `acquired db (fence token 7, expires
  1793605942)`. A script that looks for `fence token 7` or for `token=7` still finds it; a pattern that expects
  `expires` straight after the number inside the brackets does not. A second line says how to release.
- `acp lease` no longer calls the fence token a secret. It is a number that goes up with every grant.

---

### <a id="cli-lease-list"></a>`acp lease list` and the invite paste
`acp lease list` lines end with `(ends in 4m)` or `(ended)` after the same `expires=<unix seconds>`; use `--json` to parse.
The invite paste has one more line, on live documents.

### <a id="cli-rm-folder"></a>`acp rm` of a folder

`acp rm docs` used to say `not in manifest, skipping` and exit 1. It now removes every shared file under `docs/`. A
folder of 10 or more files stops without a terminal unless you add `--confirm-bulk`.

### <a id="cli-who-rows"></a>`acp who` rows, and the words of a refused write

- `acp who` prints the newest first, and with two or more rows a `note:` on stderr. Rows on stdout keep their shape.
- A row of `acp who` for an agent holding a lease ends with two spaces and `holds <resource> (10m left), ...`. The first
  four columns are unchanged, and a row for an agent holding nothing is byte for byte the same. An agent holding a lease
  that has not been seen lately gets a row of its own, with `-` in the harness, status and `last_seen` columns. Use
  `acp lease list --json` to read claims in a script.
- `acp push`, `put`, `rm` and `restore` of a file under another agent's lease print a new message on standard error
  (`<path> is claimed by <agent> until <time>`, then next steps). The exit code is unchanged. Don't match the old words.
- `acp status` has a new `team:` line. The invite paste has one more line.

## 3. Clients and servers of different versions

- **A v0.2.5 client cannot start a brand-new workspace on a v0.2.6 server.** v0.2.6 stops creating a workspace just
  because someone looked at it (a mistyped `ACP_SPACE=porj2 acp pull` used to create `porj2` and use up one of the
  server's 1000 workspace slots). A name that does not exist now answers `404` with the code `space_not_found`, and
  a v0.2.5 `acp push` to such a name stops with the server's sentence
  (`no such workspace "x" on this server (acp spaces lists yours; the first write to a new name creates it)`) and
  writes nothing. Fix: send a first message to it (`acp send`), or update the client. A v0.2.6 client treats a missing
  name as an empty workspace when it is about to write, and its first write creates it.
- **Existing workspaces and existing invites are not affected.** An invite that names a workspace creates it when
  you make the invite, so a joiner on v0.2.5 can use it.
- **A program that read a new workspace before its first write** must treat `space_not_found` as an empty workspace.
  `GET /v1/whoami` still answers.
- **Every refusal now carries a `code`, and an unknown route answers `unknown_route`.** A v0.2.5 client is not
  affected: no v0.2.5 client (the CLI, the MCP bridge, the mount, the Go or TypeScript SDK) reads a missing code,
  none matches an error message exactly, and the new field is extra JSON it ignores. Messages keep their old start
  and gain a next step at the end, so a script that matches the start of a message, or the status, still works. One
  exception: a `405` whose whole message was the allowed methods (`POST`) now reads `method not allowed: use POST`. A
  program of your own that read "a `404` with no `code`" as "this server has no such route" must now read
  `unknown_route` the same way: on v0.2.6 an object that does not exist answers `404` with `not_found` or a more
  specific code.
- **A v0.2.6 client on a v0.2.5 server** works. Things the old server cannot do say so: `acp docs trash` answers
  "this server does not keep deleted documents", and `acp invite --grant-ttl` is refused and creates nothing (the
  old server would have given out a login that never ends): it answers 400 and the command prints
  `need {space, name?, …}`. The folder-delete safety check
  (section 4) is off against an old server and behaves as it did.

---

## 4. Things that now behave differently

**Server and API**

- <a id="read-prefix"></a>**A token with a `read_prefix` and no `path_prefix` now writes only inside its `read_prefix`.** It could write,
  and take leases, anywhere. Each read prefix works as a write prefix does: `projects/42/` admits `projects/42/x`, not
  a file or folder named exactly `projects/42` (which would block the folder). It takes no lease that is not a path
  (`lock:build`), as a `path_prefix` token cannot: **a lease of that kind it held before the upgrade can no longer be
  renewed or released, and ends when it expires.** **If such a token is meant to write outside what it reads, give
  it a `path_prefix`.** File operations on a path a token cannot read now answer like a missing path.
- <a id="empty-token"></a>**An empty bearer token never signs in.** An `agents.json` with an entry for the empty token (a `""` key
  in an older file, or the hash of the empty string) let a request with no token sign in as that login. It no longer
  does, and the server warns at start about such an entry: remove it with `-rmagent`, and give that agent a real login.
- <a id="forward-bounds"></a>**A write sent to a cluster follower is bounded by idleness, not by a total time.** A large upload through a
  follower no longer fails after 30 s. A client that stops sending for 30 s now gets `408 request_timeout` (send it
  again); a leader that takes or sends nothing for 30 s, or gives no answer within 30 s of the whole body, gives
  `502` (the write may still commit: read before you retry).
- <a id="link-send-bound"></a>**On a server with a data key, a share-link answer must keep moving.** It must make progress every 30 s and,
  after its first 30 s, average 64 KiB/s; a slower reader gets a cut answer and fetches the rest with a `Range`.
  Servers without a data key are unchanged.
- <a id="unmember"></a>**`DELETE /v1/admin/invites/<id>/member` removes access only from the login that redeemed the invite.** When that
  login is gone and another login holds its name, it answers `409 member_changed` instead of changing the other
  login; remove that one through the grants API. Sending it again after it succeeded answers exactly what the first
  answered (it was `404 member_unknown`), so a script that retries can treat any `200` as done. On an invite redeemed
  more than once, removed members counted, it answers `409 member_ambiguous` without `?name=` (it used to remove the
  one member still there): name the member (`?name=<name>`; in the CLI, `acp invites remove-member <id> --name
  <name>`; in the Go SDK, `RemoveInviteMemberNamed(id, name)`).
- <a id="start-empty-token"></a>**`coordd` does not start when nobody could sign in.** A `-token` of only spaces, an empty
  `<data>/token` (with no per-agent logins), or an `agents.json` whose every login is for an empty token stops the
  start (exit 78) with what to fix. Such a server used to start and refuse every request.
- <a id="bulk-policy-drop"></a>**A workspace that holds only a bulk-change policy (`bulk_deny`, `bulk_fraction`, ...) is no longer empty.**
  Removing it answers `409` naming `bulk_policy`; clear the policy first if you mean to remove the workspace.
- <a id="max-docs"></a>**A `max_docs` quota above `-max-docs` now works, and `-max-docs` above 10000 now reaches the default
  workspace.** On v0.2.5 a workspace stopped at the flag whatever its quota said, and the default workspace stopped at
  the lower of the flag and 10000. Now a workspace with a `max_docs` quota stops at the quota, and `default` stops at
  the flag, so **a server started with `-max-docs` above 10000 lets `default` grow past 10000.** A flag below 10000
  behaves as before. A structured (JSON) document over the limit answers `507`, as a text document does (it was
  `400`).
- <a id="quota-merge"></a>**Changing one limit no longer resets the others.** `PUT /v1/admin/quota` merges: only the fields you send change.
  Send a field as `null` to return it to its default, or `0` for unlimited. **A script that left a field out to clear
  it must now send `0` or `null`.** (`sub_scopes`, when sent, still replaces the whole map.) A field the server does
  not know is `400`, naming the field: **a script with a mistyped field used to get `200` and now stops there.**
- <a id="doc-delete-404"></a>**Deleting a live document that does not exist is `404 doc_not_found`** (it used to say `deleted: true`). Deleting
  one that exists now puts it in a trash for the workspace's history window (30 days unless `history_ttl_sec` says
  otherwise): `GET /v1/crdt/trash` lists it, `POST /v1/crdt/trash/restore` brings it back. Document names that start
  with `.acp-trash/` are reserved (`400 reserved_doc_name`). A workspace with history turned off keeps no trash.
- <a id="deleted-doc-name"></a>**A save from a deleted document no longer takes its name.** A client still holding a deleted document, pushing an
  edit that leans on the old text, gets `409` and nothing is stored (it used to get `200` with `pending`, and the name
  then blocked the trash restore with `409 doc_exists`). Every client here re-reads on a `409`. A push that stands on
  its own still starts a new document under the name.
- <a id="mail-comma"></a>**`POST /v1/mail` refuses a comma in `to`** (`400`): `a,b` was stored for nobody. Send one message per person. A login
  whose name really holds a comma still gets its mail.
- <a id="invite-code"></a>**Invite refusals also carry `code`**, the same token as `reason`, so `err.code()` and `APIError.Code` read them.
- <a id="versions"></a>**Text deleted from a live document stays readable after a server tidy-up** (the last 16 tidy-ups per document, for
  the history window): `GET /v1/crdt/versions`. It uses disk until it expires.
- <a id="dm-subject"></a>**A direct message's subject is no longer in the workspace activity log.** The log still shows that a message was
  sent and between whom. A program that read `context.subject` from `chat.sent` events gets nothing; the sender and
  recipient read the subject from the message. Events already logged keep theirs until retention removes them.
- <a id="refusals"></a>**Refusals that used to be accepted:** a commit that makes one path both a file and a folder (`a` beside `a/b`) is
  `400`; reusing an `Idempotency-Key` for a different request is `422 idempotency_key_reused`; creating a file in a
  live folder with a hash that was never uploaded is `400 blob_not_uploaded` (a malformed hash `bad_blob_hash`);
  `POST /v1/fs/trash/restore` answers `423`, `409 wrong_fs_mode` or `428`/`403` where it used to say `200` with
  nothing restored.
- <a id="event-retention"></a>**Events are counted per kind.** `-event-retention N` keeps the newest N audit events and the newest N conversation
  events (up to 4N in all), so one large commit no longer pushes your team's messages out of the log.
- <a id="streams"></a>**A revoked login's open streams close at once, and one login may hold 64 streams** (`-max-streams-per-login`;
  `0` = unlimited). Past the limit a new stream gets `429 too_many_streams`.
- <a id="agent-names"></a>**A removed agent's name stays reserved,** and a server holds at most 10000 agents (`507 identity_limit` past
  that). Replacing the token of an existing name still works.
- <a id="invite-existing"></a>**`acp invite --name <existing agent> --space <another>`** no longer fails: it makes an invite that adds the
  workspace to that agent's login. Only that agent's own login can redeem it.
- <a id="statuses"></a>**More precise statuses:** an upload over `-max-body` is `413` (was `500`); a workspace the server cannot open is
  `503 space_unavailable` (was `400`); a malformed body on `/v1/blobs/has`, `/v1/mail/ack`, `/v1/lease/renew` and
  `/v1/lease/release` is `400`. A `503` that happened before anything was done carries `retry_safe: true`; the
  clients retry those by themselves, up to 3 sends.
- <a id="datakey"></a>**Encryption at rest:** a data key check file (`datakey.check`) is written on the first start with a key. A wrong
  key, or no key over encrypted data, now stops the start (exit 78, `data key: …`).
- <a id="cluster-restore"></a>**Cluster:** a server restored from only a `spaces/` backup refuses to start when it does not match its
  replication log; restore the whole data folder, or wipe `-data/raft` and re-join. Re-joining now rebuilds the node:
  its old stores move aside as `spaces.pre-restore-<time>` and the cluster fills an empty tree. Before, if the leader
  still had its whole log, every old entry ran a second time on that node's data, with no error. A start with an empty
  `-data/raft` over data, with neither `-join` nor `-raft-bootstrap`, now stops and names both. A backup (`coordd admin dump`,
  `migrate-store`, a cluster snapshot) stops with an error that names a workspace it cannot open, instead of
  leaving it out.
- <a id="slower-commits"></a>**Slightly slower commits** on the file-storage edition: each commit waits until its rename is on disk (about 4 ms
  more on a cloud disk).

**Command line and clients**

- <a id="push-markers"></a>**`acp push --force` stops on a file holding conflict markers.** Resolve them, or add `--allow-markers` if they belong in
  the file (a document about merging, say).
- <a id="go-redirect"></a>**Go SDK: a redirect to another host or port is refused**, on every call but the live awareness stream, which follows
  a cluster follower's redirect to the leader.
- <a id="mcp-address-login"></a>**The MCP address tools use only the bridge's own login.** They used to pick a login from this machine's profiles
  by the address's host and space. `acp_read_address` and the others now reach any workspace the bridge's login reaches
  on its server, and refuse another server. To work on a second server, start a second bridge for it.
- <a id="mount-save-no-read"></a>**A save over a live document through a mount, with no read first, no longer replaces it.** `cp draft.md /mnt/team/live.md`
  or a tool that writes without reading now adds its text to the document and deletes nothing, and the mount's log says so.
  To replace the text, read the file through the mount first, so the mount knows what you saw. A mount started with
  `--allow-bulk` keeps the old replace.
- <a id="big-rewrite"></a>**A very large live-document rewrite shows both texts for a moment.** When an edit is too big for one request, the
  new text is sent before the old text is removed, so a reader sees extra text while it is sent, and a stopped sync
  leaves extra text until the next sync finishes it. Small edits of any document are sent as small edits.
- <a id="ttl-forms"></a>**`--ttl`, `--grace` and `--timeout` read one way:** `600` (seconds), `10m`, `2h`, `7d`. **A value that can't be read
  now stops the command.** `lease`, `awareness` and `blob url` used to treat it as "the server's default", and
  `acp admin gc --grace` as a grace of zero.
- <a id="replace-shared"></a>**`acp crdt sync --adopt-local` is now `--replace-shared`.** The old spelling still works, with a warning. It replaces
  the shared text with your file, so it now stops if that deletes more than 10% of a text this folder never saw:
  **a script that used it must add `--confirm-truncate`, or better, use `--merge`**, which keeps both.
- <a id="doc-write-read"></a>**`acp_doc_write`** on a document the session has not read is refused if it would delete anything. Call
  `acp_doc_read` first, or pass `merge: true` or `replace_shared: true`.
- <a id="pull-dotfiles"></a>**`acp pull`:** a dot-file (`.env`) removed by the other side is now removed locally too, if you did not edit it
  (an edited one is kept and reported). A dot-file you edited is a conflict, not overwritten.
  `acp pull --force` never deletes a file you changed. `acp push --force` no longer sends files you have not changed.
  A text file too large to merge keeps yours and writes theirs next to it as `<path>.remote`. A pull never writes
  through a symbolic link or inside `.git`, `.acp` or `.acp-crdt`.
- <a id="push-symlink"></a>**`acp push`** skips a symbolic link whose target is outside the folder (with a warning) and stops, naming the
  choice, when its workspace is gone.
- <a id="show-live"></a>**`acp show` and the `acp_file_get` tool read the live tree** on a realtime workspace (they read an old frozen
  list); `acp_manifest` refuses there and points to `acp_fs_tree`. If the mode cannot be learned, these commands stop
  rather than guess.
- <a id="invite-paste"></a>**`acp invite` prints a new paste** (workspace named, "look around first", claim before starting). The `acp join`
  line in it is unchanged. `acp who` shows the real harness instead of `unknown`.
- <a id="mistyped-space"></a>**A mistyped workspace name** (reading) prints the server's sentence and exits non-zero; `acp status` stays exit 0 and
  says `workspace NAME does not exist yet`; `acp inbox --brief` stays silent.
- <a id="mount-conflict-sidecar"></a>**Go mount: a conflict leaves your version as a file beside theirs.** When your save and a teammate's write meet,
  theirs keeps the name and yours is saved as `<name>.conflict` (`.conflict.2` if taken), logged in
  `.acp/conflicts.jsonl`; the save succeeds. `--conflict=error` brings back the failing save (`ESTALE`, your version
  held). The TypeScript mount already worked this way.
- <a id="mount-refuse-corrupt"></a>**Mounts refuse writes that would corrupt a live document.** Binary bytes written into a live text document are
  refused (`EINVAL`) instead of being turned into `�`; a save that would delete more than 90% of a live text document of
  256+ characters is refused (`EPERM`, kept pending; `--allow-bulk` on the Go mount, `allowBulk: true` in TypeScript,
  to do it on purpose). Renaming a plain file over a live text document (an editor's safe save) now writes its text
  into the document and keeps co-editing going.
- <a id="mount-deleted-doc"></a>**Go mount: a save over a document a teammate deleted fails its `fsync` once (`EIO`) and stays pending**, where it used
  to report success with nothing saved. The next barrier writes the whole text, or, if the document was restored
  meanwhile, puts the save on it. A save made on the mount's last save does not fail: it joins whatever is at that name.
  If you embed `mount/pkg/view`: `RealtimeSource.PushCRDTOps` became `PushCRDTOpsResult`, which `*client.Client` already
  has, and each save now reads one op of the log first, so a failed read keeps the save pending.
- <a id="crdt-sync-fail"></a>**`acp crdt sync` can now fail where it used to report success** when a document is deleted or made again in the
  moment its edit is sent. Your file is left as it was; run the sync again.
- <a id="messages"></a>**New words in some messages.** The note `acp send` prints about unread mail, and the refusal of a
  share link or a port (CLI, `acp status`, the MCP tools, the TypeScript `readACPCapability`), now end with what to do.
  Exit codes and `err.status` are unchanged. **A script that matches the old words must match the new ones**, or better,
  the exit code.
- <a id="mcp-not-text"></a>**`acp_file_get`, `acp_blob_get` and `acp_read_address` refuse a file that is not UTF-8.** They
  returned it with every invalid byte replaced, and a put of that text overwrote the file. An agent that needs such a
  file reads it outside the bridge (`acp show <path> > file`).
- <a id="go-time-limits"></a>**Go SDK: one-shot calls end after 60 s with no response headers, or 30 s of a stalled
  body** (`*client.TimeLimitError`). Streams are not bound. A program that makes calls the server takes longer than
  that to answer sets `client.SetTimeLimits(headers, stall)`; 0 turns a limit off.
- <a id="go-log-id"></a>**Go SDK: `FollowForever` with no `OnResync` stops with a `*client.LogChangedError` when the
  server's history was replaced** (a restore: a new log id whose event at the follow's position is another one, or
  none). It used to go on, taking another history's events for the ones it had seen. A restart or a new cluster
  leader also changes the id, but over the same history: the follow goes on as before, with nothing skipped. After the
  error, re-read what you derive from the events, then follow again from `err.Gap.ResumeAt` with `LogID:
  err.Gap.LogID`; or set `OnResync`, which gets a `"restored"` gap while the follow goes on at the new head. A
  `Subscribe` subscriber with no `OnResync` gets the same error through `OnError` and is moved on to the new head.
  `acp watch` prints a `resync:` line on stderr and goes on at the new head: a script that keeps state from it
  re-reads with `acp events --from 1`. A Go mount re-reads its tree after a restore, and refuses reads (EIO) while the
  server's log is below what it has seen. A follower that keeps its position across runs keeps the log id and the
  event (`OnStreamOpen`, `EventHash`) and passes them back (`LogID`, `LastEvent`). A v0.2.5 server gives no log id,
  and nothing changes against it.
- <a id="go-follow-model"></a>**Go SDK and mount: the follower follows one written rule** (the table
  `testdata/follow-vectors.json`, run by both SDKs and every mount).
  - A stream that names no log while one is held, with a head below your position, now waits: no `GapBehind`, and
    nothing is delivered twice. Before, the follow restarted at that head and delivered the same events again.
  - The first log id ever seen, with a head below your position, is now `GapBehind` (told), as with no id at all.
  - A `"restored"` gap's `From` is the event after the last one the follow checked, which can be earlier than where
    the stream reopened: events delivered by a stream that named no log are covered.
  - An event check answered by a node of the new log whose head is below both the event checked and the stream's
    head decides nothing, and is asked again (`*client.LogUnconfirmedError` with `Head` and `At` set).
  - A Go mount now refuses reads (EIO, `view.ErrLogUndecided`) while it can't tell whether the server's log is the
    one it holds: a stream that names no log after one was named, or a new log id whose check is out or failed. A
    mount that was behind serves again on a stream naming its log, or on a position read naming it, at or past where
    it had been; an unnamed stream's events no longer clear it.
- <a id="go-mount-report"></a>**Go mount: a barrier's `Report` has `Merged` and `Remade`.** `Merged` lists live
  documents saved as a first contact (added to what is there, nothing deleted). That now includes a document whose
  record the mount's bounds took, until a save of it lands, even after a read. `Remade` lists documents a
  teammate deleted, with nothing at their name since, that the save made again with the writer's whole text
  (it says so in the report); the deleted one stays in the trash.
- <a id="mount-lone-rm"></a>**A lone `rm`, `rmdir`, `mv`, `mkdir` or truncate in a Go mount reaches the server on the short write-back timer**
  instead of waiting for the next save.
- <a id="utf8"></a>**Text that is not valid UTF-8** is refused by the Go clients (`acp crdt push/sync`, mail, notes, `acp-mcp`) before
  sending, and `acp push` skips a file whose name is not UTF-8, with a warning.

**Trust and local state**

- <a id="tls-profile"></a>**A self-signed server with no matching profile now fails TLS** when you reach it by an `acp://` link
  (`acp uri read`, `acp uri discover`, `acp open`, `acp mount acp://…`). Before, the default profile's certificate,
  pin and `insecure` setting were applied to whatever host the link named. Trust now comes from the profile for that
  link's host and port. With none, the system's trusted certificates are used and it is never insecure. **To reach a
  self-signed server, make a profile for it, or set `ACP_CERT` or `ACP_PIN` for the run.** `acp mount acp://host/space/`
  stops with `no profile for https://host:port` instead of sending your default token to that host, and `acp-mount`
  refuses a token whose server is another host, or an `ACP_TOKEN` with no server.
- <a id="state-paths"></a>**Names with punctuation get new state paths.** Profiles `work.prod` and `work_prod`, or agents `u:1` and `u_1`, used to
  share a folder's sync record, lease tokens, live-text sync state and catch-up mark. Names are now written one to
  one (`work.prod` becomes `work%2Eprod`). Names of only letters, digits, `-` and `_` keep their files (the live-text
  sync state of a name containing `_` is renamed). Anything else is **migrated once**, the first time it is needed,
  and only if the old file can only be yours. If two of your profiles shared the old name, the old file is left where
  it is and not read, and `acp` says so on stderr: `acp pull` starts that folder over (a first sync, which deletes
  nothing on either side).
- <a id="no-server-profile"></a>**A profile that names no server** (used with `ACP_SERVER`) now keeps its state per server. Its **old shadows are
  not read** (there is nothing in them that says which server they belong to), and **`acp crdt sync` refuses
  until you set the document up again** (its message explains how). Lease tokens and catch-up marks for such a profile
  start fresh; leases expire by themselves.

### TypeScript SDK

- <a id="ts-bom"></a>`readTextFile` keeps a leading BOM (as U+FEFF), where it used to drop it.
- <a id="ts-rename-eisdir"></a>`rename` onto a folder fails with `EISDIR` (it was `EEXIST`).
- <a id="ts-lone-surrogate"></a>`putBlob` and JSON request bodies throw on text holding half a character (a lone surrogate) instead of sending
  something else.
- <a id="ts-blob-integrity"></a>`getBlob` throws at the end of the stream when the bytes do not match their hash.
- <a id="ts-has-pending"></a>`VirtualFS` gained `hasPending()`: an implementation of your own must add it.
- <a id="ts-send-comma"></a>`send` and `sendIdem` throw a `TypeError` on a comma in `to`, and send nothing, on any server. Send one message per
  person. A login whose name really holds a comma can't be mailed from this SDK, as from `acp send`.
- <a id="ts-save-deleted-doc"></a>A save over a document a teammate deleted now fails its `flush()` once and stays pending, where it used to report
  success with nothing saved. The next flush writes the whole text, or, if the document was restored meanwhile, puts
  the save on it.
- <a id="ts-save-on-read"></a>A `writeFile` of a live document on a realtime mount is compared with what you last read, not with the document at
  the save. With no read first it adds to the document and deletes nothing, and `flush()` lists the file in `merged`.
  Read the file first to replace text, or mount with `allowBulk: true`. Code that wrote a document blind to overwrite
  it needs one of the two.
- <a id="ts-subscribe-head"></a>`subscribe` with no `from` starts at the log's head. It calls `onError` once per outage when a stream keeps failing.
- <a id="ts-flush-report"></a>A failed `flush()` of a realtime mount carries the barrier's report on `err.detail.report`.
- <a id="ts-save-kept"></a>A `writeFile` of a live document is compared with whichever is closest to it of your last few reads and your last landed
  save, and deletes only text all of them held. A line of a teammate's that the save only leaves out stays, and
  `FlushReport.kept` names the file; later saves keep it too, and say so, until one holds it again. To delete one from a mount, read the file, save once with the line, then without it;
  or mount with `allowBulk: true`, or edit it through a `TextDocSession`. A mount that has kept text in very many
  documents keeps only the ids of the oldest; a save there that would delete such text fails its `flush()` with
  `EPERM` and stays pending (the same way out: save once with the text in it). A document whose record the bounds took
  is saved as a first contact (`merged`, nothing deleted) until a save of it lands. `FlushReport.remade` lists a save
  that made a deleted document again.
- <a id="go-mount-save-kept"></a>The same on the Go mount: a save deletes only text all of those views held, so a line of a teammate's that a save leaves
  out stays, and `Report.Kept` from `View.Flush` names the file. To delete one: read the file, save once with it kept and
  again without it, or mount with `--allow-bulk`, or use `acp crdt sync` or `acp_doc_write`. See `mount/README.md`.
- <a id="cli-invite-workspace"></a>`acp invite --workspace` and `acp link --workspace` now use that workspace. Before this they used your default one: check
  invites made that way with `acp invites`, and revoke any that went to the wrong place.
- <a id="go-mount-backslash"></a>A Go mount refuses a backslash in a new name with `EINVAL`. `view.Clean` no longer turns one into a slash.
- <a id="go-mount-attr-ttl"></a>A Go mount's `--attr-ttl 0` now means no caching. It used to mean one second.
- <a id="ts-mount-whole-path"></a>An address mounts its whole path. `mountVirtualFS` on `acp://h/s/a/b.md` used to mount `a/`; it now mounts `a/b.md`, as
  the Go mount does. To read one file, mount its folder and read it by name.
- <a id="ts-take-unimportable"></a>`VirtualFS` gained `takeUnimportable()`: an implementation of your own must add it. After a mode flip it hands over
  changes that could not move; until then `flush()` and `close()` reject with `EWRONGMODE`.
- <a id="ts-caps-read"></a>`mountVirtualFS` fails the operation, and tries again on the next, when the daemon's capabilities can't be read. It used
  to treat that as a CAS space. A server that answers without the capability, or has no such route, is still CAS.
- <a id="ts-idle-conflict"></a>With `flushDelayMs` and `conflict: "throw"`, `hasPending()` stays true after an idle flush loses a conflict, and the next
  `flush()` or `close()` rejects with the `VfsConflictError`.
- <a id="ts-mount-errors"></a>Read and commit failures on a mount reject with a `VfsError` (`EACCES`, `EIO`), where a network failure used to be a raw
  `TypeError`.
- <a id="ts-timeouts"></a>A request with no response headers in 60 s, or an answer whose body goes silent for 30 s, now fails with `RequestTimeoutError`
  instead of waiting for ever. A caller that makes calls the server is slow to answer (a large admin compaction, a slow
  link) passes a larger `timeoutMs`, or 0 for none.
- <a id="ts-log-id"></a>`followForever` with no `onResync` stops with a `LogChangedError` when the server's history was
  replaced (a restore: a new log id whose event at the follow's position is another one, or none); a `subscribe`
  subscriber with no `onResync` gets it through `onError`. A restart or a new cluster leader changes the id too, over
  the same history: the follow goes on, nothing skipped. Both mounts refuse reads (`EIO`, `detail.code` `"behind"`)
  while the server's log is below what they have shown, under the same name or none (a cluster node in an election or
  catching up), where the file mount used to take the older tree; a lower head alone never rolls a mount back, a restore is re-read. A follower
  that keeps its position across runs keeps the id `onLogId` gives (checked, unlike `onOpen`'s) and `eventHash` of its last event and passes them back
  (`logId`, `lastEvent`). A v0.2.5 server gives no log id: a lower head there is `behind` as before. Reads on a mount
  can now also fail with EIO `detail.code` `"undecided"` for a moment (a restart, a leader change or a restore, while the
  new log is checked); try again, as for `"behind"`.
- <a id="ts-no-source-maps"></a>The package ships no source maps.

---

## 5. New things worth knowing

- **`acp://` addresses and `acp share`:** one address names a server and workspace (`--uri` or `ACP_URI`); `acp share
  <path>` prints a signed link to one file. See [ACP-ADDRESSES](ACP-ADDRESSES.md).
- **`.acpignore`:** gitignore-style patterns at the top of a synced folder keep files out of the sync both ways. `acp
  ignore check <path>` says which line decides.
- **`acp push --dry-run`:** says what a push would add, edit and delete, and sends nothing.
- **Document trash:** `acp docs delete|trash|restore|versions` (and `acp docs json …`).
- **Logins that end:** `acp invite --grant-ttl 90d`, `coordd -mkagent NAME -expires-in 24h`, or `expires_at` /
  `ttl_sec` on `POST /v1/admin/grants`. Past its end a token answers `401 grant_expired` and the clients say to ask
  for a new login.
- **`coordd -data-must-exist`:** refuse to start when `-data` holds no server state, so an unmounted volume cannot
  look like a first start and make a brand-new server. Leave it off for the very first start and for a new cluster
  member.
- **`coordd -token-file PATH`:** the same as `-token`, kept off the process list. Refused together with `-token`.
- **The server says its version** (`GET /v1/healthz`, `acp status`), and **`GET /openapi.json`** describes the API.
- **Web pages:** see [BROWSER](BROWSER.md).

---

## 6. Going back

Back up first (section 1). Stop the server before changing any file below.

- **Migrated state folders.** After v0.2.6 has moved a punctuated name's files to their new names (section 4), a
  v0.2.5 `acp` does not look there: it sees those folders as a first sync. A first sync deletes nothing, but
  `acp pull` starts the folder over.
- **`agents.json`.** v0.2.6 writes it at version 7 as soon as it holds one of: a removed agent (the list of
  reserved names, `retired_names`), a login with an end, an invite that gives a login an end or adds a
  workspace to an existing login, or an invited member removed (also from one of several workspaces: the
  invite then records the removal). Once an agent has been removed, the file **stays at version 7**. A v0.2.5
  `coordd` refuses it at start with an error that begins `identities: agents.json: json: unknown field`, naming
  `"retired_names"` or, after an invited member was removed from one workspace only, `"removed"`. Going back therefore means restoring the `agents.json` you backed up. Editing the new one
  by hand (removing `retired_names` and setting `version` to 6) works only after the temporary logins and the
  invites above are gone, and it gives up the protection that stops a removed agent's name being taken over.
- **A cluster cannot go back by swapping binaries** once a v0.2.6 node has led: its log holds commands stamped 7,
  and a v0.2.5 node stops on them. Restore every node from the backup of its whole data folder.
- **The server-database edition** may have converted stored data in place; an older binary cannot read it. Restore
  the backup.

---

## <a id="license"></a>7. License

From v0.2.6 the binaries, the Docker image, the SDKs and the public docs and specifications are licensed under the
**Apache License 2.0**. Releases up to v0.2.5 stay available under MIT, as published.

---
Need help? Check `GET /` on your server (a public "start here" page), [RUN-ACP](RUN-ACP.md) and
[OPERATIONS](OPERATIONS.md), or open an issue.

# ACP Operations Runbook

How to deploy, run, and maintain `coordd` and connect agents. **New here? Start with
[RUN-ACP.md](RUN-ACP.md)** (what to pick, and the shortest path for a laptop, a team and a company); this page is
the reference behind it. An AI agent setting ACP up should follow [AGENT-SETUP.md](AGENT-SETUP.md); every "mode" is
explained in [MODES.md](MODES.md).

## 1. Deploy the daemon

Pick a host every agent can reach (a cloud VM, or one of the agents' machines). Install the released binaries
(`acp`, `coordd`, `acp-mcp`) or use the Docker image `ab0tcom/acp:<version>`:

```bash
curl -fsSL https://raw.githubusercontent.com/ab0t-com/acp/main/install.sh | bash
coordd -addr :8443 -data /srv/acp   # the first start makes your admin login "owner" (see §2)
```

Leave `-hosts` off unless agents will connect by a name or address this machine can't see on itself (a cloud VM's
public IP, a DNS name, the Docker host's address). When you do set it, list the extra names agents use: from v0.2.5 they are added to the automatic list
(`-hosts-exact` for only the listed names). On v0.2.4 and older `-hosts` replaces the list, so there include
`localhost,127.0.0.1` too.

First run on a NEW data folder creates under `-data`: `owner.token` (0600, the token of the admin login `owner`
it makes for you; `agents.json` keeps only its hash), `cert.pem`/`key.pem` (TLS identity), and the state dirs. It
ends start-up with the login name, the token file's path and the exact `acp config set …` line for this machine
(never the token); later starts repeat that block while `owner.token` holds a live admin login. With
`-shared-token` (or `-token`) it instead starts in shared-token mode, creating `token` (the shared bearer secret)
as before. An existing folder keeps its mode: `token` → shared, `agents.json` → per-agent. Start-up prints the certificate path and its
fingerprint (`pin=sha256:…`) on the `clients:` line. Use a fixed, absolute `-data` path: the default `./acp-data`
is relative to the directory you start from.

### Flags
| Flag | Default | Meaning |
|------|---------|---------|
| `-addr` | `:8443` | listen address |
| `-data` | `./acp-data` | state directory |
| `-token` | — | run in shared-token mode with this token (else: an existing `<data>/token` is read; a NEW folder makes the `owner` login unless `-shared-token`); ignored once per-agent logins exist |
| `-shared-token` | `false` | on a NEW data folder, start in shared-token mode (create `<data>/token`) instead of making the `owner` login; no effect on an existing folder |
| `-owner-name` | `owner` | name of the admin login a NEW data folder makes on its first start (token in `<data>/owner.token`) |
| `-shared-token-role` | `admin` | role the shared token grants while no per-agent identity exists: `admin`, `writer` or `reader` |
| `-hosts` | (auto) | SANs baked into a NEW self-signed cert. Unset: the cert covers loopback, this machine's hostname and its network addresses. Set it for a cloud VM's public IP, a DNS name, or a Docker host's address (not visible from inside). From v0.2.5 the names are **added** to the automatic list (`-hosts-exact`: only these); on v0.2.4 and older setting it replaces the list, so include `localhost,127.0.0.1` there. (`coordd -h` shows `127.0.0.1,localhost` as its default; unset really means the automatic list.) An existing cert is never rewritten (agents pinned it); start-up names addresses it lacks |
| `-data-key-file` | (off) | file holding a 32-byte key (or 64 hex chars) that encrypts blob contents at rest; or set `ACP_DATA_KEY`. See §4 "Encryption at rest" |
| `-root-page` / `-root-page-file` | `true` / — | the public start-here document at `GET /` (§2b); `false` turns it off, a file replaces it |
| `-enforce-leases` | `true` | reject commits to a path another agent holds a lease on |
| `-max-body` | `268435456` (256MB) | max request body |
| `-rate` | `50` | per-agent request rate (req/s), burst = 4× |
| `-event-retention N` | `50000` | keep only the last N events per space (0 = unlimited, warned at startup); periodic compaction |
| `-mail-retention N` | `5000` | keep only the last N READ messages per recipient (unread mail is always kept; 0 = unlimited) |
| `-max-spaces` / `-max-spaces-per-grant` | `1000` / `16` | spaces open on this node / new spaces one non-admin per-agent grant may create through this server process — counted in memory, per process and per node (§2b) |
| `-idem-window N` | `10000` | idempotency keys remembered per space (exact-replay window; older keys re-apply). Replicated + persisted; in cluster mode set the SAME value on every node |
| `-mkagent NAME` | — | create a per-agent token, print it **once**, exit (switches to per-agent mode); `agents.json` stores only its hash. For a **stopped** server: refused while one runs on `-data` (use the grants API or `acp invite`), except a single-node shared-key server's first login |
| `-lsagents` | — | list identities (fingerprint, name, role, scope; never a token), exit |
| `-rmagent FINGERPRINT\|NAME` | — | revoke one identity, exit (refuses the last identity and an ambiguous name). For a **stopped** server: refused while one runs on `-data` — revoke live with `DELETE /v1/admin/grants` |
| `-hash-agents` | — | rewrite an older `agents.json` to store only token hashes (v6), keeping a 0600 backup, exit (stopped server only) |
| `-allow-cluster` | off | with `-mkagent -role admin` + a scope: lets that scoped admin change cluster membership; an unscoped admin already can |
| `-role admin\|writer\|reader` | `writer` | role for `-mkagent` |
| `-spaces s1[,s2…]` | (any space) | space allowlist for `-mkagent`: the token may select only these spaces |
| `-path-prefix p1[,p2…]` | (unscoped) | write scope for `-mkagent`: the token may only commit paths / take lease names inside a listed subtree (`…/`) — see 2b-scopes |
| `-read-prefix p1[,p2…]` | (reads everything) | read scope for `-mkagent`: the token reads only its slice of the TREE; out-of-scope reads look missing. NOT confinement (events/mail/presence/stats stay space-wide) — see 2b-read-scopes |
| `-cap-key-file f1[,f2…]` | (off) | signing key file(s) for blob capability URLs; enables the `bloburl` capability |
| `-cap-max-ttl` | `900` | maximum capability-URL lifetime in seconds (mint clamps to this) |
| `-cap-rate` | `10` | per-IP req/s on the unauthenticated `/v1/cap/` fetch path (burst 4×; 0 = unlimited) |
| `-trusted-proxy ip\|cidr[,…]` | (none) | YOUR reverse proxies. Only for a request whose TCP peer is listed do the unauthenticated per-source limits (invite redeem, `/v1/cap/`) key on the right-most `X-Forwarded-For` hop that is not itself listed; otherwise `X-Forwarded-For` is ignored. Set it when coordd sits behind an edge, or every joiner shares the edge's budget. List only hosts where every process is trusted: whoever connects FROM a listed address picks its own source key |
| `-doc-idle-unload` | `10m` | return a CRDT doc's in-memory (folded) state to disk-only after it has been idle this long; the next access rebuilds it from its op-log. Memory only — never changes replicated state, so nodes need NOT agree on it. `0` = keep every touched doc in memory until exit. See §6 "Resident CRDT memory" |

### Blob capability URLs — key provisioning & rotation
The capability signing key is **operator configuration, not state** — provision it
exactly like the cluster CA: generate once, copy the same file to every node.

```
openssl rand -hex 32 > cap-2026a.key          # generate (64 hex chars)
coordd … -cap-key-file cap-2026a.key          # every node gets the SAME file
```
The key can forge a read URL for **any blob in any space** — store it like a bearer
token (`0600`, off the shared FS). Key id = first 8 hex of sha256(key) (logged at
startup, carried as `k=` in URLs).

**Rotation (zero downtime).** The FIRST listed key mints; ALL listed keys validate:
1. Generate `cap-2026b.key`, distribute to every node.
2. Restart with `-cap-key-file cap-2026b.key,cap-2026a.key` — new URLs use the new
   key, outstanding old-key URLs keep working.
3. After ≥ `-cap-max-ttl` seconds, drop the old file from the list and restart.
   Retiring a key **immediately invalidates** every URL signed with it — that is also
   the bulk-revocation lever on suspected compromise (skip the grace step).

## 2. Auth modes

- **Per-agent (recommended; required for invites; what a brand-new data folder starts in):** each agent has its own
  token, and identity comes from the token. **The first admin is automatic:** a new data folder's first start makes
  an unscoped admin named `owner` (`-owner-name` to change it), writes its token to `<data>/owner.token` (0600, via
  a temp file + fsync + rename) and stores only its hash in `agents.json`. Point your profile's `token_file=` at
  that file (or a 0600 copy). A restart never makes a second one. The easy way to give every other agent a login
  is an invite (`acp invite` / `acp join`, below).
- **Shared token:** one secret in `<data>/token`; every agent uses it; agent id comes from the (trusted)
  `X-ACP-Agent` header; role `admin` unless `-shared-token-role` says otherwise. Fine for one person's own agents
  on their own machines. A data folder that already has `<data>/token` stays in this mode; a new one starts in it
  only with `-shared-token` (or `-token <value>`).
- **Switching to per-agent by hand:** run `coordd -data … -mkagent
  claudeA` and `-mkagent codexB`. This creates `<data>/agents.json` (token→agent) and
  switches the daemon to per-agent mode: identity is derived from the token (header
  can't spoof it), and the shared token stops working. The token is printed once;
  `agents.json` keeps only its SHA-256 hash, so the file (or a backup of it) never
  hands out access. **See who has access** with `coordd -data … -lsagents`.
  **While the server runs, change logins live** (next section): `-mkagent`, `-rmagent` and
  `-hash-agents` refuse while a coordd holds the data folder (`<data>/LOCK`), because the
  running server rewrites `agents.json` from memory on its next login change and would
  silently undo the edit (a revoked token would come back). They are for a **stopped**
  server: stop it, run them, start it. One exception keeps the documented first step:
  a running single-node shared-key server may get its FIRST login with `-mkagent`, then
  restart. **Rotate** = mint the same name again, hand over the new token, then revoke
  the old fingerprint (live: `POST` then `DELETE /v1/admin/grants`).
- **Hashed storage upgrade note (v0.2.4):** files written by older versions store raw
  tokens; they keep working unchanged. The first `-mkagent` / `-rmagent` /
  `-hash-agents` rewrites the file as version 6 (hashes only) and keeps the old one as
  `agents.json.raw-backup-<UTC>` (0600). An older coordd **refuses to start** on a
  version-6 file (fail-closed, like the scoped envelopes below), so upgrade every node
  first; the backup is your only way back to an older binary, so delete it once every
  node runs v0.2.4.

### Live identity management (v0.2.4)
With the daemon running, an UNSCOPED admin manages identities over HTTP — no shell, no restart, no SIGHUP. This is
the way to change logins on a running server (the offline `-mkagent`/`-rmagent` refuse while it runs):

    GET    /v1/admin/grants                         # who has access (fingerprint, name, role, created, scope)
    POST   /v1/admin/grants  {"name":"bot","role":"writer","scope":{"spaces":["team"]}}
                                                    # -> {"token": ... (shown ONCE), "fingerprint": ...}
    DELETE /v1/admin/grants?fingerprint=<fp>        # revoke (or ?name= when unique)

In a **cluster** the identity table is replicated: a change applies on every node at once, a node that joins later
receives it, and only token hashes are ever replicated. The first leader after upgrading replicates its existing table
once; from then on `-mkagent`/`-rmagent` refuse (the file is cluster state) — use the API. A **shared-token** daemon
refuses the first grant via the API (it would lock out every shared-token client): mint the first identity with
`-mkagent` and restart. **Mail quota:** `PUT /v1/admin/quota {"max_mail": N}` caps retained messages per space (507 at
the cap). **Cluster upgrade order:** v0.2.4 raises the replicated command (v5) and snapshot (v4) versions — upgrade every
node before the first write; an older node stops rather than diverge.

### Invites — connect another agent with one paste (v0.2.4, capability `"invites"`)
An admin creates a **single-use, expiring invite** for one space; the new agent redeems it with no credential and gets
its **own** per-agent token (an ordinary grant, listed and revoked like any other). The `acp invite` / `acp join`
commands wrap this; the endpoints are:

    POST   /v1/admin/invites  {"space":"demo","name":"codexB"}   # -> 201 {"invite": {...}, "secret": ... (shown ONCE)}
           # optional: "role":"writer|reader" (never admin), "ttl_sec" (default 86400, 60..604800),
           # "uses" (default 1, max 10; a fixed "name" means uses 1), "name_prefix", "scope":{"path_prefix","read_prefix"}, "note"
    GET    /v1/admin/invites[?space=&status=pending|exhausted|expired|revoked]   # who created what, who joined; never a secret
    DELETE /v1/admin/invites/<id>                                  # revoke: future redeems only (200, idempotent)
    DELETE /v1/admin/invites/<id>/member[?name=]                   # remove the agent this invite let in
    POST   /v1/invites/redeem {"id","secret","name"?,"redeem_key"?} # NO token needed: the secret is the credential

- **What a leak can do:** the code works once and expires (default 24 h; there is no never-expiring invite). A second
  redeem answers `410 invite_exhausted` naming who used it and when. The daemon stores only SHA-256 hashes of the
  secret, the minted token and the client's retry key — never the values — in `agents.json`, the raft log and snapshots.
- **Who may invite:** any admin. A **space-scoped admin** may invite only into its own spaces, never wider than its own
  grant (an invite without a `path_prefix` inherits the admin's), and can list, revoke and remove only those spaces'
  invites and members — the "team lead" case. A scoped admin confined to a `sub_scope`, or denied `awareness`, is
  refused (`403 invite_not_contained`): the minted grant could not carry that limit.
- **Shared-token daemons** (an older data folder, or `-shared-token` / `-token`) refuse invites
  (`409 identity_mode_shared`): restart in per-agent mode (`-mkagent`) first. A new data folder is per-agent already.
- **Abuse bounds:** redeem is rate-limited to 10 attempts/min per source address (on the node the client reached;
  a malformed request is a `400` and costs nothing) and, on the leader, 30/min per invite id for FAILED attempts and
  30/min per invite id for matching secrets → `429 rate_limited` + `Retry-After`. The two per-id budgets are separate,
  so wrong guesses at a (public) invite id never block the real joiner. A failed redeem writes nothing and never locks
  an invite (its id appears in the space's events). At most 64 pending invites per space and per creator, 1024 in all
  (`429 invite_cap`). Spent, expired and revoked invites are kept 7 days for the listing, then dropped — except one
  whose agent still exists, which is kept so `remove-member` keeps working.
- **Behind a reverse proxy / edge:** start coordd with `-trusted-proxy <the proxy's IP or CIDR>`, or every joiner
  shares the proxy's per-source budget (ten junk attempts a minute from anyone would then block all joins). In a
  cluster, a redeem a follower forwards over the cluster mTLS plane (`-init-cluster-ca`, §2c) is not charged again on
  the leader; without cluster mTLS the leader charges the follower's address, unless you list the nodes in
  `-trusted-proxy` (only if every process on them is trusted).
- **Names:** an invite can't create an agent named `coordd` or `anon` (the server's own actor ids, any case). A joiner
  who CHOOSES its name can't take the name of an agent an invite let in and that was later removed (its mail, threads
  and presence are keyed by name) while that invite's record is retained; an admin who fixes the name on an invite can.
- **Clients never follow a redirect** on `acp join` (the probe or the redeem): the code's secret goes only to the
  address whose certificate was checked. Every `acp` request also refuses a redirect from `https://` to `http://`.
- **Watching for joins:** the target space's event log gets `invite.created`, `invite.revoked` and `member.joined`
  (actor = the new agent). Every error body carries a stable `reason` (e.g. `invite_expired`, `name_taken`).
- **Storage + upgrade:** invites live in `agents.json` beside the identities and ride the same snapshot preamble; an
  older coordd refuses a file or snapshot that carries any (fail-closed). Upgrade every node before the first invite.

## 2b. Spaces (multi-tenancy)

One daemon hosts many **isolated spaces** — each its own shared filesystem + channels
(event log, mailbox, leases, manifest/blobs, CRDT docs, presence) under
`-data/spaces/<space>/`. Pick a space per client with `ACP_SPACE` (default `default`);
spaces share nothing. This is how multiple independent collaborations — or one agent
working with several different teams/services — coexist on one daemon without seeing
each other.

- A space is created on first use; list them: `acp spaces` or `GET /v1/spaces` (every space that exists, also right
  after a restart; a grant scoped to some spaces sees only those).
- `acp stats` reports the current space; pass `ACP_SPACE=...` to target another.
- On one box, run many harnesses against many servers/spaces freely — client state is
  namespaced per (server, space, agent) under `.acp/<ns>/` and `.acp-crdt/`, so nothing
  collides. Set `ACP_PROFILE` to give a connection a readable local namespace label.
- Use distinct `ACP_AGENT` (or per-agent tokens) per harness; the daemon warns on `beat`
  if one agent id is used by two live processes.

### The public start-here page (v0.2.4)
`GET /` (and `/v1/`) answers **without a token** with a JSON "start here" document: what ACP is, what to do first
(by kind of visitor, including an AI agent discovering the server), how to get access, what each part of the API
does, install lines, SDKs, skills and guides. It never contains workspace, agent or identity data. `-root-page=false`
turns it off (those paths need a token again); `-root-page-file <file.json>` serves your own document instead
(it's public — nothing secret in it). Capability detection for programs stays `GET /v1/healthz`.

### Who can see what in a space
Everyone who can read a space can follow its activity log. A direct message's **body** is only for its recipient,
but its **subject** appears in the log (`chat.sent`) so the team can see who's talking about what. Put private
details in the body, not the subject.

### How many spaces, and removing unused ones (v0.2.4)
Any request that names a space creates it, reads included, so two limits keep one caller from filling the daemon:
- **`-max-spaces`** (default 1000): spaces open on this node. It counts per process: on one node a restart resets it
  (spaces on disk reload as they are used); a cluster node reopens every space at start.
- **`-max-spaces-per-grant`** (default 16; `0` = off): new spaces one non-admin per-agent grant may create. Over it the
  request fails with a message naming the limit. Not counted: admins, a grant scoped to a list of spaces (its list
  already bounds it), and shared-token mode (there is no per-agent identity to count; `-max-spaces` is the bound there).
  **What it counts, exactly:** spaces created through THIS server process, kept in memory. A restart starts the count
  again from 0, and in a cluster each node counts the spaces created through it (a read on a follower creates the
  space there), so one grant can create up to this many per node and per process lifetime. It stops a runaway
  client, not a determined one; for a hard bound give the grant a space list (`-spaces` / `scope.spaces`).
  (Replicating the counter would cost a cluster write on every first read of a space; not done.)

**See and remove spaces** (admin; a scoped admin only within its own spaces):
```
GET    /v1/admin/spaces                          # each space: open?, empty?, what it holds
DELETE /v1/admin/spaces?space=NAME&confirm=NAME  # remove an EMPTY space
```
The listing knows what a space holds only if this node has it loaded (`"open": true`). A space not used since the
server started shows `"open": false` with no `empty` and no `content`: that means **unknown**, not empty and not full
(on one node spaces load when first used; a cluster node loads every space at start). `DELETE` always checks for
real, so just try it: a space with content is refused and the answer says what it holds.

Removal **refuses (409) any space that holds content**: files or their history, blobs, documents, a directory tree,
mail, live leases, a quota setting, or **events** (since v0.2.5: a space's event log is its activity history, and
removing the space would erase it). A read or a presence ping leaves no event, so a space created by mistake (a
typo'd name, a read) is still removable. It's replicated in a
cluster, the `default` space can't be removed, and removing a space that isn't there is a no-op. The name is free again
afterwards, and a slot goes back to the grant that created it. This endpoint never deletes real content: a space that
holds anything stays. **If the name is used again it is a new space whose event numbers start at 1:** a watcher that resumes with an old
cursor (`from=N`) waits until the new space reaches N, so restart watchers of a removed space from the beginning.

## 2b-quotas. Per-space quotas (multi-tenant resource governance)

Spaces isolate data but, unquotaed, share the daemon's disk and lease table — one
tenant could fill both. Set per-space ceilings (admin role) and the daemon enforces
them; no client change or restart needed:

```bash
curl -sk -X PUT "$ACP_SERVER/v1/admin/quota" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -d '{"space":"company_a","max_events":100000,"max_blob_bytes":1073741824,"max_leases":64}'
```

- Absent/`0` = unlimited (an unconfigured space behaves exactly as before).
- Over `max_events`/`max_blob_bytes` → **507** (persists until room frees: event-log
  compaction / blob GC / a raised quota — clients must not blind-retry). Over
  `max_leases` → **429** + `Retry-After` (clears as leases TTL-expire).
- Monitor `GET /v1/stats` → `"quota": {<bound>: {used, limit?}}` per space and back
  off before the wall. Lowering a limit below current usage wedges *new* writes only;
  existing state is untouched.
- **Cluster mode:** the quota rides the Raft log and the snapshot (`quota.json` under
  each space dir), so every node — including a freshly joined one — enforces the same
  numbers. Nothing extra to configure per node; there is deliberately NO quota flag.
- **The multi-tenant model** (one space per customer or team): your own control
  plane holds the admin token, decides each tenant's budget, and pushes it via
  `PUT /v1/admin/quota`; coordd enforces; tenant clients just handle 429/507. See the
  client contract in [API_REFERENCE.md](API_REFERENCE.md) (Per-space quotas).

## 2b-scopes. Scoped tokens (least privilege below the space boundary)

A writer token normally holds full-space write power. To confine one to a subtree
(e.g. one project's pod on a shared space), mint it with a **path-prefix scope**:

```bash
coordd -data /var/lib/acp -mkagent pod-42a -role writer \
       -path-prefix projects/42/,file:projects/42/
```

- Enforced on **write resource names**: every commit path and lease name must lie
  inside a listed prefix's subtree, else `403`. Reads (unless the grant also carries a
  read scope, §2b-read-scopes), events, mail, and blob uploads are untouched — the
  scope narrows the role, never widens, and the space stays the only hard isolation
  boundary.
- **Prefixes are subtrees, matched on path-segment boundaries:**
  `projects/42/` admits `projects/42/x.md` and anything below it — never the
  byte-adjacent sibling `projects/420/x.md`, and never a FILE named exactly
  `projects/42` (that would collide with the directory). Every prefix must end in
  `/`; `-mkagent` refuses a bare `projects/42`. Lease names match the same way:
  commit-guarding leases are named `file:<path>`, so list a `file:projects/42/`
  prefix if the token should take them.
- **Migration note for existing grants (loose form):** older `agents.json` files may
  carry bare prefixes (`"projects/42"`). They still load — the daemon normalizes each
  to its subtree (`projects/42/`) and logs one `WARNING … is loose-form; treating as
  "projects/42/" — add the trailing "/" to silence this` per grant at every start /
  SIGHUP. The token keeps its subtree; only the old byte-prefix over-match
  (`projects/420/…`) is gone. Add the trailing `/` in the file to silence the warning.
- **FAIL-CLOSED upgrade note (important):** the first scoped grant rewrites
  `agents.json` into a versioned envelope (`{"version":2,"agents":{...}}`; v3 for
  `sub_scope`/`deny`, v4 for `read_prefix`). A coordd binary older than that envelope
  **refuses to start** on the file — by design: an old binary would otherwise load
  the grant WITHOUT its scope and hand a confined token full role (silent privilege
  escalation, worst on a version-skewed cluster where a rollback or an un-upgraded
  follower wins an election). Therefore: **upgrade every node (and any standby
  binary) BEFORE minting the first scoped grant.** Since v0.2.4 every write (scoped or
  not) produces the hashed version-6 file, which older binaries refuse too; see the
  hashed-storage note in §2 for the rollback backup.
- A well-behaved client can read its own bounds via `GET /v1/whoami` (`{agent, role,
  scope?}` — the token is never echoed) and self-limit instead of collecting 403s.
- Revoke like any grant: `DELETE /v1/admin/grants?fingerprint=<fp>` on a running server, or
  `coordd -data … -rmagent <fingerprint|name>` on a stopped one.

## 2b-read-scopes. Read scopes (`-read-prefix`)

A path-prefix scope bounds what a token may **write**; a **read scope** bounds what
it may **read** of the tree. Mint one with `-read-prefix`:

```bash
coordd -data /var/lib/acp -mkagent pod-42a -role writer \
       -path-prefix projects/42/,file:projects/42/ \
       -read-prefix projects/42/
```

- **What is filtered:** the manifest (a filtered `200`, never a `403`), blob reads and
  `POST /v1/blobs/has` (a blob is readable only if some manifest path the token may
  read references it), capability-URL mints, collaborative-document reads, ops-pulls
  and lists, and every write-echo body (commit `200`/`409` manifests, lease listings).
  An out-of-scope read is **byte-, status- and header-identical to a genuinely-missing
  resource** — there is no way to learn that something exists outside the slice.
- **Honest caveat — a slice of the TREE, not of the space.** A read scope is content
  hygiene for a semi-trusted bot or partner principal. It is **not confinement**: the
  **event log stays space-visible and carries out-of-scope paths and blob hashes**;
  the mailbox, presence/awareness, `/v1/stats`, `/v1/subscopes` and lease listings
  also stay space-wide; the global manifest `version` bumps on every out-of-scope
  commit (activity is observable). Hard isolation is still a **separate space**.
- **Untrusted browsers / end users:** a read scope alone does NOT make a
  direct-to-browser token safe — a hostile holder can enumerate the space's metadata
  through `/v1/events`, `/v1/stats`, `/v1/agents` and directed mail (path names, doc
  names, lease names, participants, activity). An untrusted browser/end-user
  deployment **must use a broker or a separate space** until a grant-pinned
  event/stats/presence filter exists.
- **Rules at mint and load:** every `read_prefix` must end in `/` (refused otherwise);
  every `path_prefix` the token can write must be covered by a `read_prefix` (a write
  scope wider than the read scope is refused — write-path responses would leak).
  A read-scoped grant writes the **v4** `agents.json` envelope: a coordd older than
  read scopes refuses to start on it (fail-closed, see the upgrade note above).
- **Commit-by-hash and possession:** a read-scoped token may reference a blob hash in
  a commit only if that hash is already reachable through a path it can read **or it
  uploaded those bytes itself** on this node. Possession is node-local and bounded:
  it is lost on restart/failover, when a space closes, and — under very heavy upload
  churn from other read-scoped tokens on the node — by eviction. **Client story:** a
  read-scoped commit refused with `blob … not uploaded` for bytes you hold means
  *re-PUT the bytes and retry* — `acp push` does this on its own (it re-uploads
  whatever `/v1/blobs/has` reports missing; `has` does not consult possession, so an
  uncommitted upload is reported missing until committed — correct, slightly chatty).
  One read-scoped commit may carry at most **524,288** blob references; a larger one
  is refused `400 too many changes for a read-scoped commit … split the commit` (a
  property of the request, not of the tree). Unscoped commits are not capped.
- **Cluster upgrade order (important):** the possession check rides the replicated
  commit as extra fields. A follower still running a **binary older than read scopes drops those
  fields and applies a read-scoped commit unscoped** — replicas diverge and the old
  node may serve out-of-scope bytes at an in-scope path. A v4 `agents.json` refuses
  to load on an old node, but an old node with its OLD file does not refuse the
  leader's log. **Upgrade ALL nodes before minting the first `read_prefix` token; never
  mint during a mixed-version window.**

## 2c. High availability (cluster mode)

For no single point of failure, run a **3-node Raft cluster** (**beta**, see below; tolerates 1
node down, ~1s failover, no committed-data loss). Bootstrap one node, `-join` two more;
clients are unchanged. Full steps + ops (rolling upgrade, backup, failover) are in the
**`acp-cluster` skill**. Single-node mode
(no `-raft-addr`) remains the default for dev/small setups.

Idempotency keys are **replicated state**: a client retrying with an
`Idempotency-Key` after a failover or restart gets the original result replayed, never a
duplicate. The window is `-idem-window` keys per space (default 10000) — run the same
value on every node (a mismatched window diverges replica eviction).

### Cluster mode is BETA — known limitations

Single-node mode is GA; **cluster (Raft) mode is beta**. Run a **uniform-version** cluster in
production (upgrade all nodes to the same build) and treat these as current caveats:

- **Run one binary version across the cluster (and use `-cluster-crdt-delete-fence` for a
  mixed-version upgrade).** The CRDT delete/recreate fence (which stops a stale concurrent write
  from silently orphaning onto a deleted-and-recreated doc) rides both a per-command generation on
  the replicated write path AND a per-document generation in Raft snapshots. In a **uniform-version
  cluster this is fully consistent** — every node enforces identically. The fence is **enforced by
  default** (`-cluster-crdt-delete-fence=true`). The ONLY hazard is a **deliberate mixed-version
  window**: a fence-unaware older binary would apply a fenced write differently from a new node
  (live-command op-log divergence) and would show a deleted doc as a *live-empty* doc in its own
  `/v1/crdt/list`. If you must run mixed versions transiently, set `-cluster-crdt-delete-fence=false`
  on the nodes for the duration — the leader then stamps no enforceable generation, so every node
  (new or old) applies CRDT pushes identically (no divergence; the rare original delete/recreate
  race returns, consistently) — then remove the flag once all nodes run the new build.
- **Upgrade every node before minting a `read_prefix` (read-scoped) token.** The read-scope
  commit-by-hash possession check rides two extra fields on the replicated commit command; an
  older follower drops them and applies the commit **unscoped**, so a refused (laundering)
  commit on the leader is APPLIED on the old node — replica divergence, and the out-of-scope bytes
  become readable at the in-scope path on that node (cluster-wide if it later leads). A v4
  `agents.json` refuses to load on an old node, but an old node with its OLD grants file does not
  refuse the leader's log — the only guard today is upgrade order (a leader-side version guard is
  a planned follow-up, together with the delete-fence auto-detection above).
- Per-agent identities are **replicated** (v0.2.4): a grant made or revoked through the API (or redeemed from an
  invite) applies on every node, and a node that joins later receives it (§2 "Live identity management"). After the
  first leader replicates its table, `-mkagent`/`-rmagent` refuse to edit the file; use the API or invites. A node
  joining a per-agent cluster authenticates its join with its own `-token` (or `<data>/token`) value, which must
  then be an unscoped admin token: the shared token no longer works.
- **First start of a cluster:** only a node started with `-raft-bootstrap` on a NEW data folder (and no `-token` /
  `-shared-token`) makes the `owner` login; when it first leads, identity seeding replicates it to the cluster.
  A node that joins never makes its own owner: its join request carries its own token (`-token`, else
  `<data>/token`), so give it an unscoped admin login's token of the cluster — `-token "$(cat <bootstrap
  data>/owner.token)"`, or copy that token into the joiner's `<data>/token` (0600) before its first start (it stays
  off the process list) — and it receives the cluster's table. A joiner with its own random token is refused (401);
  since v0.2.5 the joiner logs why and what to do (`cluster join: … refused this node's token (401) …`) instead of
  giving up silently.
- Cluster-membership admin (`join`/`leave`) is not yet a distinct capability from space admin — restrict
  membership operations to a trusted operator credential.

### Inter-node security (acp-1 §14.4) — configure a cluster CA

A cluster has two node↔node channels: the **Raft transport** (all replicated data) and the
**inter-node HTTP** channel (leader write-forwarding + membership join/leave, which carries a
cloned `Authorization` bearer token). Both are hardened by one step:

```bash
coordd -init-cluster-ca -data /srv/acp          # once
# copy BOTH /srv/acp/cluster-ca.pem AND cluster-ca-key.pem to every node's -data dir
```

- **With a cluster CA:** the Raft transport is mutual mTLS, and the inter-node HTTP channel is
  **cluster-CA mutually authenticated** — peers verify each other's node certs and the bearer
  token is never exposed to a network MITM. External agent clients are unaffected: they keep the
  **same self-signed `cert.pem`** they pin (§4.1); only intra-cluster peers use the CA.
- **Without a cluster CA:** the inter-node HTTP channel is **unauthenticated** — coordd logs a loud
  startup SECURITY warning, and you **MUST** run the node network on a **trusted private
  subnet** (a MITM there could harvest the token / impersonate the leader). This is the §14.4
  absent-a-CA model; configuring a CA is strongly recommended for any multi-host cluster.

Use each node's real reachable IP in `-raft-addr` (not `0.0.0.0`).

### Blob GC in cluster mode

`POST /v1/admin/gc?grace_sec=N` runs through the replicated log so every node deletes the identical
set. Under Raft the **`grace_sec` window is not applied** (a per-node file mtime is non-deterministic
and would diverge replicas — acp-1 §17): GC deletes exactly the blobs unreferenced by the replicated
manifest. This is safe because a just-uploaded blob and its commit are themselves ordered replicated
commands. On a **single node**, `grace_sec` behaves as documented (skips blobs newer than the window).

### Data-safety guards (sync + restore)

The shared filesystem's one promise is that your data is safe. These guards are on by default and none of
them changes a normal single-file push, pull or edit:

- **`acp push`** never deletes a path it cannot scan (dot-files / dot-dirs are not scanned), so a plain
  `acp pull` then `acp push` with no edits changes nothing on the server. It **refuses an emptied or
  mostly-emptied checkout** (an unmounted volume, an `rm -rf`, a container restarted without its volume)
  instead of deleting the tracked paths remotely — pass `--allow-bulk-delete` only when the deletion is meant.
  `--force` overrides **only** a stale edit of a file you changed; deleting a path another agent changed since
  your base needs the separate `--force-delete`.
- **`acp pull`** refuses a manifest **older** than your sync base (a server restored to an older snapshot, a
  profile repointed at another space or server), never deletes local files when the manifest version is
  unchanged since your last sync or when the paths are outside your token's read scope, and hash-verifies
  every downloaded blob before it replaces a local file. `acp pull --reset-base` starts a fresh base for the
  new target **without deleting anything** (differences are saved as `<path>.remote`).
- **`acp crdt push`** refuses a file that would delete 90 % or more of a document of 256+ characters (an
  emptied or wrong file) unless `--confirm-truncate`; the daemon's automatic op-log compaction also skips
  such a document while its text is recoverable, so `acp crdt pull` (or an admin reading the op-log) can
  restore it. An explicit `acp admin compact <doc>` still compacts.
- **Audit breadcrumbs:** every `file.edited` / `file.deleted` event carries `before.hash` — the content hash
  the path pointed at before the commit — so `GET /v1/events` can be replayed to recover what a mistaken
  delete removed, as long as blob GC has not run. Keep GC a deliberate, manual step.
- **Restore keeps the state it replaces:** when a node that already holds committed data takes a snapshot
  (a cluster snapshot install onto a populated node), the previous `spaces/` tree is kept beside it as
  `spaces.pre-restore-<UTC timestamp>` (the newest two are retained; the daemon logs the path). It is an
  ordinary `spaces/` tree: to go back, stop the daemon, move the live `spaces/` aside and move the retained
  tree to `spaces/`. Remove retained trees once a restore is confirmed — they hold a full copy of the data.

### Manifest history, retention & undo (capability `"history"`)

The authority keeps a **version record for every manifest version** for a per-space retention window and
**widens blob GC so every blob a retained record references is kept** — so a mass delete (a mistake, a wrong
flag, a buggy client) is recoverable for the window, and **undo is a commit**. This is on by default.

- **Recover a mistaken delete/overwrite:** `GET /v1/history` (or `?path=P`) to find the version before the
  mistake, `GET /v1/manifest?version=N` to read it, then `POST /v1/commit` with each path set to its entry at
  N and `restore_of: N` (the authority verifies the result equals N, or refuses `400` with no change). No blob
  upload — the pin guarantees the bytes are present. The restore is itself a new version, so it is reversible.
  The CLI wraps this: `acp history [path]`, `acp show <path> --version N`, `acp restore …` and
  `acp checkpoint create|list|delete` (see `acp help history`).
- **Pin a state indefinitely:** `POST /v1/checkpoint {name, version?}` (writer) exempts a version from
  eviction until an admin `DELETE /v1/checkpoint?name=` removes it. Bounded by `max_checkpoints`.
- **Tune retention** via `PUT /v1/admin/quota` (per space): `history_ttl_sec` (default 30 d),
  `history_min_versions` (default 100, a **hard floor**), `max_history_bytes` (default unbounded),
  `max_checkpoints` (default 256). Prune the evicted records with `POST /v1/admin/history/prune`; blob bytes
  are then reclaimed by the next `acp admin gc`. History-retained blobs **count toward `max_blob_bytes`** — a
  space can hit `507` from deleted-but-retained bytes; `GET /v1/stats.history` shows the history-only
  `blob_bytes` vs `live_blob_bytes` split so you can lower the window, prune + GC, or raise the quota.
- **Data that must be forgotten on delete** (a disclosure concern — retained content stays readable to
  in-scope readers for the window): set `history_ttl_sec`/`history_min_versions` low, run
  `POST /v1/admin/history/prune` then `acp admin gc`, and note a checkpoint pins content until an admin
  deletes it. `history_ttl_sec: 0` + `history_min_versions: 0` disables history for a space (retaining only
  the current version and pins) — allowed, but reported loudly as `history.history_disabled: true` and warned
  at startup, because it removes all recoverability.
- **Cluster upgrade order (important):** history is replicated state produced by the commit apply. A node
  running an older build produces no version record and cannot apply prune/checkpoint commands, so its state
  would diverge. **Upgrade EVERY node of a cluster to a history-capable build before the first commit that
  follows the upgrade**, and do not run `history.prune` while any member lags.

## 3. Connect an agent

**With an invite (recommended):** from an admin profile, `acp invite --name codexB [--space team] [--role reader]`
prints a block of text; the other agent runs the one `acp join acpj1_…` line in it. The join checks the server
against the certificate fingerprint the code carries, gets the agent its own token, saves a profile and confirms it
works; nothing to copy by hand. `acp invites` shows who joined. Invite by an address the other machine can reach
(the profile's `server`, or `ACP_SERVER=https://<host>:8443 acp invite …`) and that the certificate covers.

**By hand** (a token minted with `-mkagent` or `POST /v1/admin/grants`):

```bash
acp config set codexB server=https://<host>:8443 token_file=~/.acp/tokens/codexB.token \
   pin=sha256:<fingerprint from the coordd start-up line> agent=codexB
acp --profile codexB status        # login: ok, codexB as writer
```

`cert=/path/to/cert.pem` works instead of `pin=`; neither is needed for a daemon behind a proxy with a public
certificate. Env vars (`ACP_SERVER`, `ACP_TOKEN`, `ACP_PIN`/`ACP_CERT`, `ACP_AGENT`) override any profile field.

## 4. Day-2 maintenance

- **Health/monitoring:** `acp health`; `acp stats` (or `GET /v1/stats`) for live counts;
  request logs go to stdout (`METHOD path -> code agent=… dur`).
- **Roles:** in per-agent mode, mint least-privilege tokens: `coordd -mkagent bot
  -role reader` (admin|writer|reader), optionally path-confined with `-path-prefix`
  (see 2b-scopes). Revoke one with `acp invites remove-member <invite id>` (an invite's agent),
  `DELETE /v1/admin/grants?fingerprint=…` (live, any node), or with the server STOPPED
  `coordd -data … -rmagent <fingerprint|name>` (it refuses while the server runs). Never hand-edit `agents.json`.
  **SIGHUP** re-reads the logins in `agents.json` (a rotation within the current mode; it never switches between
  shared-key and per-agent). It does **not** re-read invites: they are managed live (`acp invite`, `acp invites`,
  the `/v1/admin/invites` API), and the server's own table is what every redeem and revoke writes — re-reading a
  hand-edited one could bring back a spent or revoked invite.
- **Blob GC:** unreferenced blobs accumulate (e.g. after deletes/overwrites). Reclaim:
  `acp admin gc --grace 600` (keeps blobs newer than 10 min to avoid racing an
  in-flight push). Safe to run live; schedule it (cron) if churn is high.
- **Compaction:** the event log compacts automatically when `-event-retention` is set
  (default 50000; `0` = unlimited, and warned loudly at startup). The mailbox keeps every
  unread message plus the newest `-mail-retention` READ messages **per recipient**
  (default 5000) — so the retained floor is *recipients × N*, not N, and an
  never-acknowledged flood is not bounded by this setting at all. CRDT op-logs compact
  once they grow past ~4× their live size; force any of it with `acp admin compact [doc]`.
  In **cluster mode** compaction is driven by the leader through the replicated log, so
  every replica compacts identically; a doc with nothing dead is never compacted and
  costs no Raft traffic. Each compaction that reclaims something logs
  `[space X] <store> compacted: dropped N` on the leader — that line is the operator
  signal retention is working (the other one is `event_base_seq` in `/v1/stats`, which
  only advances on compaction).
- **Resource bounds worth knowing:** spaces (`-max-spaces`), CRDT documents per space
  (`-max-docs`), event log (`-event-retention`), mailbox (`-mail-retention`, per
  recipient), lease TTL (`-lease-max-ttl`). Client-supplied identifiers are length-bounded
  at the boundary (agent id 256 B, lease resource 1 KiB, idempotency key 256 B, document
  name 256 B). In **cluster mode `-max-docs` and `-idem-window` MUST match on every
  node** — both are checked inside the replicated apply, so a node configured differently
  would accept a write its peers refuse.
- **MCP bridge:** to let an MCP harness use ACP, register `acp-mcp` against a saved profile, so no token is
  on a command line: `claude mcp add acp -- env ACP_PROFILE=<name> acp-mcp` (`acp invite --mcp` prints these steps
  for the joining agent).
- **Backups:** the entire state is the `-data` dir. Stop the daemon (SIGTERM, clean
  shutdown) or snapshot the filesystem, then copy `-data` off the machine, on a schedule (ACP
  does not schedule backups itself). The event log and CRDT op-logs are append-only JSONL; the
  manifest/leases are JSON; blobs are immutable. Restore = stop, put the folder back, start.
  With `-data-key-file`, back the key up separately: the blobs are unreadable without it.
- **Encryption at rest:** `-data-key-file <file>` (or `ACP_DATA_KEY`) holds a 32-byte key
  (`openssl rand -hex 32` makes one); blob contents are then stored AES-256-GCM encrypted, keyed
  by plaintext hash so dedup still works. A wrong key fails closed at start (exit 78). Every
  cluster node needs the same key. Metadata (events, mail, manifest, leases, raft logs) is not
  encrypted by ACP: put `-data` on an encrypted volume for full coverage. Keep the key outside
  `-data`.
- **Upgrade:** `acp update` (or re-running `install.sh`) replaces the binaries but does not restart
  a running `coordd`; restart it yourself. Docker: bump the tag, pull, recreate. Read the version's
  upgrade notes first (v0.2.4: the hashed `agents.json` in §2, and the cluster upgrade order).
- **Restart:** stop with SIGINT/SIGTERM (drains connections, flushes logs — "stopped
  cleanly"). On start everything is replayed; event `Seq`, manifest version, leases,
  and CRDT docs all resume.

## 5. Failure handling

| Symptom | Cause / fix |
|---------|-------------|
| `401` on every call | wrong token, or per-agent mode active and you used the shared token |
| `403` on commit/lease only | the token is **scoped** and the path/lease name is outside its `path_prefix` (check `GET /v1/whoami`); reads still work. Or: role forbids the method |
| daemon refuses to start: `agents.json version 2` | scoped grants exist but this binary predates them — **upgrade the binary** (do NOT hand-edit the scope away; that is the fail-closed guard working) |
| `429` | rate limit (raise `-rate` or back off) — or `max_leases` quota (has `Retry-After`; wait for a lease to expire/release) |
| `507` | a space hit `max_events`/`max_blob_bytes`. Don't blind-retry: free room (`acp admin gc`, event compaction) or raise the quota via `PUT /v1/admin/quota`; check `stats.quota` for used/limit |
| `423` on push/commit | a path is leased by another agent — wait/coordinate, or the holder releases |
| `409` on push | manifest moved; `push` auto-rebases. `409` on lease = held by someone live |
| watcher CPU/spin | fixed: `watch`/`crdt watch` back off 1s on reconnect |
| disk filling | run `acp admin gc`; check `-event-retention`/`-mail-retention` are non-zero (unlimited is warned at startup) |
| `400 malformed hash` on commit | a `changes[].hash` is not 64 lowercase hex. Clients must send `hex(sha256(plaintext))`; the daemon refuses any other shape before the commit is replicated |
| `400 X-ACP-Agent too long` | the agent id exceeds 256 bytes. Use a short stable id — it is retained as a map key and written into durable records |
| `400 lease resource too long` | a lease `resource` exceeds 1024 bytes. The name is a durable map key, persisted and echoed into audit events, so it is bounded like any other client value |
| `document limit reached` on a CRDT push | the space hit `-max-docs` (default 10000, per doc type). Existing documents keep working; raise the flag if the workload legitimately needs more. **In cluster mode it MUST be identical on every node** — it is checked inside the replicated apply |
| `403` on `PUT/GET /v1/admin/quota` with a `space` parameter | the token is **space-scoped** and named a space outside its allowlist. Use an unscoped admin token for cross-tenant operations |
| daemon down | restart it — state is durable. A single node is one point of failure; for failover run the 3-node cluster (§2c, beta) and keep off-machine backups either way |
| `acp invite`: "can't create per-agent invites" (`409 identity_mode_shared`) | the daemon is in shared-token mode (an older folder, or `-shared-token`/`-token`): stop it, `-mkagent <you> -role admin`, start it without `-token`, point your profile at the new token (§2) |
| "where is the admin token?" on a new server | `<data>/owner.token` (Docker: `docker compose exec -T acp cat /data/owner.token`); start-up prints its path |
| `acp invite` / `acp join`: "certificate does not cover …" | the address isn't in the cert's SANs: invite by an address it covers, or re-make the cert with `-hosts` (every joined agent must then re-pin) |
| client: "the daemon's certificate isn't trusted" | set `pin=sha256:…` (start-up line) or `cert=` in the profile, or join with an invite |
| daemon exits `78` "OPERATOR ACTION REQUIRED" | a permanent config error named in the message (bad flag, damaged store, wrong data key); restarting will not help |
| `coordd -rmagent`/`-mkagent`: "a coordd is running on …" | the server is running on that folder; an offline edit would be undone by it. Use the grants API (`POST`/`DELETE /v1/admin/grants`) or `acp invite`/`acp invites`, or stop the server first (§2) |
| cluster: `503` "just elected leader and is still applying the log it inherited" | right after an election a new leader serves writes only once it has applied everything committed before its term (normally milliseconds); retry after `Retry-After` |
| cluster: a new node never joins; its log says `refused this node's token (401)` | the join carries the node's own token; give it an unscoped admin login's token of the cluster (`-token`, or `<data>/token` before its first start) — §2c |
| `DELETE /v1/admin/spaces` → `409 … events=N` | the space has activity history; since v0.2.5 that counts as content and the space is kept. If events are all it holds and you mean to discard them: add `&discard_events=N` (`acp admin spaces drop <name> --confirm <name> --discard-events N`) |

## 6. Capacity notes

Good for a few agents and code/spec-sized workspaces. The event log and CRDT op-logs
are kept in RAM (durable on disk) and bounded by the retention settings above — leave
them non-zero. fsync-per-write bounds throughput to the disk's sync rate — ample for
agent coordination, not for high-frequency telemetry.

**Resident CRDT memory (`-doc-idle-unload`, `resident_docs` / `resident_json_docs`).** A
document's op history is folded into memory only when something needs it (a read, a write, the
~60 s compaction check), and is returned to disk-only after `-doc-idle-unload` without use, so
memory tracks the documents *in use*, not every document that exists. `/v1/stats` reports
`docs`/`json_docs` (all documents) beside `resident_docs`/`resident_json_docs` (folded right now) —
the pair tells you what is holding RAM. Trade-offs to know:
- **First access after idle pays one rebuild** of that document (tens of ms; up to about a second
  for a very large op-log). On a cluster, a WRITE to an idle document pays that rebuild inside
  the replicated apply on every node. If you have very large documents written less often than
  the window, raise it (e.g. `30m`) or set `0` and size RAM instead.
- **Listing documents** (`GET /v1/crdt/list`, `/v1/crdt/json/list`) counts as a use for the page it
  returns (bounded by `limit`), so a client that polls its document list keeps that page resident.
  **Cluster snapshots** fold every document but do not count as a use: those are released again at the
  next sweep (seconds), so expect a short RSS bump during a snapshot, not a plateau.
- **The first start after an upgrade** may fold every large document once (about a minute after
  boot) to learn which ones need no compaction, then releases them within seconds; later starts skip
  that work.
- **Database storage benefits most.** With the default file storage the op-log layer itself keeps each
  log's raw records in memory, so RSS stays near the on-disk size of the CRDT logs; the unload
  frees the (larger) decoded state on top of that.

---
name: acp-operations
description: Set up, secure and run the ACP server (coordd) — decide binary vs Docker, file vs server-database storage, one server vs a (beta) cluster, shared key vs one login per agent, and the certificate (automatic, -hosts, or your own HTTPS proxy); create the first admin login, connect agents with invites (acp invite / acp join / acp invites), check with acp status, manage logins (grants API, -lsagents/-rmagent), workspaces (list, limits, remove empty), quotas, scopes, the bulk guard, history retention, the public start page, discovery flags, encryption at rest (-data-key-file), backups, upgrades (acp update, Docker tags), systemd, and troubleshooting. Use this skill whenever someone asks to run, install, host, deploy or expose ACP/coordd, connect another machine or agent to a server, revoke or rotate a login, pick a deployment option, back up or upgrade, handle 401/403/409/423/428/429/507 or a certificate error, or asks "is there a hosted ACP?".
---

# Operating ACP (coordd)

You run the server that every agent connects to. ACP is **self-hosted**: there is no ACP service to sign up for
(ab0t's own server is private, not a public service), so "use the hosted one" is never the answer.

The full pages, in the ACP repo (`docs/`, https://github.com/ab0t-com/acp/tree/main/docs):
- `docs/RUN-ACP.md`: the operator page: what to pick, shortest paths per persona. **Start there.**
- `docs/AGENT-SETUP.md`: step-by-step setup written for an AI agent, with the error table and the friction log.
- `docs/MODES.md`: every "mode" explained, and which to ignore.
- `docs/OPERATIONS.md`: the full reference (flags, scopes, quotas, cluster, history). `docs/THREAT_MODEL.md`:
  the security model.

## 1. Ask before you act

Never guess these. Ask the human, all at once:

1. Is there already a server to join? (An `acpj1_…` invite code means just `acp join <code>`, with no server to run.)
2. Agents on this machine only, or on other machines too? Same network or different ones?
3. What address will others use (DNS name / IP)? Is TCP 8443 open to them?
4. Docker or a plain program? Must it come back after a reboot?
5. Where does the data folder live, and how is it backed up?
6. Must it sit behind a company HTTPS proxy or use a public certificate?

## 2. Decide (the rules)

1. **Start on one machine.** Move to a reachable server when an agent on another network needs in.
2. **One login per agent (invites) for anything shared.** The shared key is only for one person's own agents.
3. **One server before a cluster.** The cluster is **beta**; use it only when downtime is unacceptable.
4. **File storage until a workspace keeps ~100k events**, then server-database storage (`coordd-server`,
   image tag `vX.Y.Z-server`). Clients can't tell the difference; it converts either way.
5. **Certificate:** leave `-hosts` off when agents use this machine's own names/addresses (localhost, its hostname,
   LAN/VPN IPs). Set it when they use a name the machine can't see on itself (cloud public IP, DNS name, Docker host),
   and then list **every** name plus `localhost,127.0.0.1`, because `-hosts` replaces the automatic list. For a public
   certificate, put your own HTTPS proxy in front (coordd can't load a certificate you supply).
6. **Back up the data folder off the machine on a schedule.** ACP doesn't schedule backups.

## 3. Deploy

Install the released binaries, or use the Docker image. Never build from source; the source isn't public.

```bash
curl -fsSL https://raw.githubusercontent.com/ab0t-com/acp/main/install.sh | bash     # acp, coordd, acp-mcp -> ~/.local/bin
# or: docker image ab0tcom/acp:<version>  (file storage)  /  ab0tcom/acp:<version>-server (database storage)
```

**The admin login is automatic.** A brand-new data folder's first start makes an unscoped admin named `owner`
(`-owner-name` to change it), writes its token to `<data>/owner.token` (0600; only the hash goes in `agents.json`)
and ends start-up with the token file's path and the exact `acp config set owner …` line for this machine. The
token is never printed. A restart never makes a second owner.

```bash
coordd -addr :8443 -data /srv/acp            # add -hosts only per rule 5
mkdir -p ~/.acp/tokens
(umask 077; cp /srv/acp/owner.token ~/.acp/tokens/owner.token)
# Docker: (umask 077; docker compose exec -T acp cat /data/owner.token > ~/.acp/tokens/owner.token)
```

Existing folders keep their mode (`<data>/token` → shared key, `agents.json` → per-agent). `-shared-token` makes a
new folder start the old way (one shared key in `<data>/token`, no invites); `-token <value>` also means a shared
key. In a cluster only the `-raft-bootstrap` node mints the owner; joiners receive it.

The start-up line `clients: server=https://<host>:8443 cert=… or pin=sha256:…` gives the certificate fingerprint.
Use an absolute `-data` path (the default `./acp-data` depends on the current directory). Docker: a named volume
on `/data`, `restart: on-failure:5` (never `always`/`unless-stopped`), and log caps. For a plain-program server,
use a systemd unit with `Restart=on-failure`, `RestartPreventExitStatus=78` and a start limit (the full unit is in
`docs/RUN-ACP.md`).

Connect the admin profile **by the name agents will use** (so invites point there), then check:

```bash
acp config set owner server=https://acp.example.com:8443 token_file=~/.acp/tokens/owner.token \
   pin=sha256:<fingerprint> agent=owner           # on the server machine itself, cert=/srv/acp/cert.pem also works
acp status                                        # login: ok, owner as admin
```

## 4. Connect agents

```bash
acp invite --name codexB [--space team] [--role reader] [--ttl 2h] [--uses N] [--mcp]
acp invites                         # status + who joined
acp invites revoke <id>             # cancel an unused invite
acp invites remove-member <id>      # remove the agent an invite let in
```

The printed block goes to that agent only. It runs `acp join acpj1_…`, which checks the server against the
fingerprint in the code before sending anything secret, gets its own token, saves a profile (0600) and checks it
works. An invite works once, expires (24 h default, 7 days max), and makes a writer or reader in one workspace,
never an admin. A space-scoped admin can invite only into its own workspaces. `--mcp` prints the variant for an
MCP-only agent (`claude mcp add acp -- env ACP_PROFILE=<name> acp-mcp`).

- An invite to `localhost`/`127.0.0.1` works only on this machine. For another machine:
  `ACP_SERVER=https://<reachable name>:8443 acp invite --name …`.
- `acp invite` refuses an address the certificate doesn't cover, and says how to fix it.
- Behind a proxy with a public certificate, an admin profile with no `pin`/`cert` creates invites that tell the
  joiner to trust the system certificates.

**Other ways to make a login:** `POST /v1/admin/grants {"name","role","scope":{"spaces":[…]}}` (live, any node;
the token is shown once), or on the server machine `coordd -data … -mkagent NAME -role writer -spaces team`
(+ `-path-prefix`, `-read-prefix`, `-allow-bulk`; then `kill -HUP`). Manual client profile:
`acp config set <name> server=… token_file=… pin=… agent=<name>`. Teach `token_file=`, never `token=` (it lands in
shell history; the CLI warns).

## 5. Logins, roles and scopes

- **Roles:** `admin` (manage), `writer` (read+write), `reader` (read). A shared key is `admin` unless
  `-shared-token-role writer|reader`.
- **See / revoke:** `GET /v1/admin/grants`, `DELETE /v1/admin/grants?fingerprint=…` (live, replicated), or on the
  server machine `coordd -data … -lsagents` / `-rmagent <fingerprint|name>` + `kill -HUP <pid>`. **Rotate:** mint the
  same name again, hand it over, revoke the old fingerprint. In a cluster, `-mkagent`/`-rmagent` refuse once
  identities are replicated; use the API or invites. Never hand-edit `agents.json`.
- **`agents.json` stores only token hashes (v0.2.4, version 6).** The first rewrite keeps
  `agents.json.raw-backup-<UTC>` (0600). An older coordd refuses a version-6 file, so upgrade every node first and
  delete the backup once nothing needs to roll back (it still holds raw tokens).
- **Scopes:** `-path-prefix p/` (may write only there), `-read-prefix p/` (sees only that slice of the tree;
  events, mail, presence and stats stay space-wide, so a separate workspace is the real boundary), `-spaces a,b`
  (workspace allowlist), `-allow-bulk` (writer may land mass deletes/overwrites). Prefixes end in `/`. `acp whoami` /
  `GET /v1/whoami` shows a token its own bounds. Scoped grants write a newer `agents.json` envelope that older
  binaries refuse (fail-closed): **upgrade every node before minting the first one.**

## 6. Workspaces, quotas, history, bulk guard

- **Workspaces (spaces)** are the isolation boundary. Any request naming one creates it. `acp spaces` lists the
  ones your login may use (persisted, survives restart). `acp admin spaces` lists all with what each holds.
  `acp admin spaces drop <name> --confirm <name>` removes an **empty** one (refused if it holds anything).
  `-max-spaces` (1000 per node) and `-max-spaces-per-grant` (16 new workspaces per non-admin login) bound creation.
- **Quotas** (admin, live, replicated, no flag):
  `PUT /v1/admin/quota {"space":"x","max_events":…,"max_blob_bytes":…,"max_leases":…,"max_mail":…}`.
  Over the events, bytes or mail cap → `507`; over `max_leases` → `429` + `Retry-After`. `GET /v1/stats` shows
  used/limit.
- **History** (on by default: 30 days, at least 100 versions): `acp history`, `acp show <path> --version N`,
  `acp restore`, `acp checkpoint create|list|delete`. Tune per space with the same quota object
  (`history_ttl_sec`, `history_min_versions`, `max_history_bytes`, `max_checkpoints`). History-retained bytes count
  toward `max_blob_bytes`; prune (`POST /v1/admin/history/prune`), then `acp admin gc`.
- **Bulk guard** (always on): a commit deleting/overwriting many paths needs `--confirm-bulk` with the exact counts
  (`428`); writer logins are denied bulk (`403 bulk_denied`) unless minted `-allow-bulk`; `bulk_deny: true` in a
  space's quota makes bulk admin-only.
- **Live folder mode:** an admin can flip one workspace to live mode (`acp fs mode realtime`, exact-count confirm
  when non-empty); push/pull then answer `409 wrong_fs_mode`. Most never need it.

## 7. Capabilities

`GET /v1/healthz` (no token) lists what this server supports. Always on: `acpuri awareness bulkguard channels
crdtfs crdtjson crdtjson-move fsmode history invites quotas scopedtokens scopedtokens.read tenancy
tenancy.subscope`. Conditional: `batchevents` (`-max-batch > 0`, the default), `bloburl` (`-cap-key-file` set),
`awareness-ws` (`-awareness-ws`). A shared-key server advertises `invites` but refuses to create them (see §3).
`acp health` only prints `ok`.

**Rolling-upgrade rule for clusters:** features that add a new kind of replicated write (JSON-document `mv`,
read-scoped commits, history, identity replication, invites) must not be used until **every** node runs the new
version. An old node applies them differently, or stops.

## 8. Day 2

- **Health:** `acp status`, `acp health`, `acp stats` / `GET /v1/stats`. Request logs go to stdout.
- **Public start page:** `GET /` answers without a token (what ACP is, how to get access; never workspace, agent or
  login data). `-root-page-file page.json` serves your own; `-root-page=false` turns it off.
- **Discovery (`/.well-known/acp`):** behind a proxy, set `-discovery-endpoint https://<public name>` and
  `-discovery-cert-fingerprint=false`. `-discovery-spaces` is off by default (workspace names are information).
  Check with `acp uri discover acp://host[:port]/`.
- **Behind a proxy with a public certificate:** the proxy talks HTTPS to coordd (pin coordd's fingerprint), does
  not buffer responses, allows long-lived streams (`watch`, `inbox --wait`, live documents), and passes
  `Authorization` and `X-ACP-*` through. Clients set only `server=https://<public name>`. Every client shares the
  proxy's address for the per-address invite rate limit. A separate proxy is **not needed for security**: TLS plus
  the pin plus per-agent tokens already protect the connection. Use one for a public certificate, a single ingress,
  a WAF, or a second auth layer.
- **Reclaim disk:** `acp admin gc --grace 600` (blobs); `-event-retention` (default 50000; 0 = unlimited, warned);
  `-mail-retention` (default 5000 read messages per recipient); `acp admin compact [doc]`. "Disk growing" on a busy
  workspace is usually history: check `stats.history`, prune, then gc.
- **Encryption at rest:** `-data-key-file <file>` (32 bytes or 64 hex chars; `openssl rand -hex 32`) or
  `ACP_DATA_KEY`. Encrypts blob contents only; use an encrypted volume for metadata. Same key on every node; keep it
  outside `-data` and back it up separately. A wrong key fails closed at start.
- **Backup:** the whole state is `-data` (Docker: the volume). Stop cleanly (SIGTERM → "stopped cleanly") or
  snapshot the disk, copy it off the machine, on a schedule. Restore = stop, put it back, start.
- **Restart:** SIGINT/SIGTERM drains and flushes; on start everything replays. Exit `78` means a permanent config
  problem (don't auto-retry): systemd `RestartPreventExitStatus=78`.
- **Upgrade:** `acp update [--check] [--version vX.Y.Z]` swaps `acp`/`coordd`/`acp-mcp` after verifying checksums.
  It does **not** restart a running coordd, so restart it yourself. Docker: bump the pinned tag,
  `docker compose pull && docker compose up -d`. Read the release's upgrade notes first.
- **MCP harnesses:** `acp-mcp` *is* the MCP server; register it against a saved profile:
  `claude mcp add acp -- env ACP_PROFILE=<name> acp-mcp`. With easymcp: see
  [`references/connect-acp-to-easymcp.md`](references/connect-acp-to-easymcp.md).
- **Cluster (beta):** see the `acp-cluster` skill. Run one version on every node; `-max-docs` and `-idem-window`
  must match on every node.

## 9. Troubleshooting

| Symptom | Meaning / fix |
|---|---|
| `acp invite`: "can't create per-agent invites" (`409 identity_mode_shared`) | Shared-key server (an older folder, or `-shared-token`/`-token`). Stop, `-mkagent <you> -role admin`, start without `-token`, point the profile at the new token. The old shared key dies. |
| "Where is the admin token?" | `<data>/owner.token` on a server that made its own login (Docker: `docker compose exec -T acp cat /data/owner.token`). |
| "the server's certificate does not cover …" | You connected/invited by an address not in the cert. Use one it covers, or re-make the cert: stop, move `cert.pem`/`key.pem` aside, start with the right `-hosts`. Every joined agent must then re-join or update its pin. |
| "the daemon's certificate isn't trusted" | Profile has no `pin`/`cert` for a self-signed server: add `pin=sha256:…`, or join with an invite. Never `insecure`. |
| `401` | Bad/revoked token, or the shared key after switching to one login per agent. |
| `403` | Role not allowed (writer on `/v1/admin/*`), a scope (`acp whoami`), a space outside the login's list, or `bulk_denied`. |
| `409` push / lease | Manifest moved (`push` rebases) / lease held. `409 wrong_fs_mode`: the workspace is in live mode. |
| `423` | Path leased by another agent. |
| `428 bulk_confirm_required` | Re-run with the exact counts via `--confirm-bulk`; a mismatch means the tree moved. |
| `429` | Rate limit (`-rate`), lease quota, or invite redeem limit: honour `Retry-After`. |
| `507` | A quota (events, bytes incl. retained history, mail). Free room or raise it; don't blind-retry. |
| Daemon won't start: `agents.json version N` | The binary is older than the file: upgrade. Don't hand-edit. |
| Daemon exits 78 "OPERATOR ACTION REQUIRED" | A permanent config error named in the message. |
| "store directory … is locked by another running coordd" | Another daemon owns that `-data`. Never run two on one folder. |
| Can't connect from another machine | Port closed (cloud firewall), wrong address, or the cert doesn't cover it (start-up names missing addresses). |
| Daemon down, need failover | One server + backups, or the 3-node cluster (beta). |

## 10. Capacity

Good for a few agents on code- and spec-sized workspaces. Every write is fsynced, so throughput is bounded by the
disk (ample for coordination, not for high-frequency telemetry). Bound each workspace with quotas; for a very long
history use server-database storage; for failover, the cluster (beta).

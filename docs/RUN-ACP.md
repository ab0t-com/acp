# Run ACP

This page is for whoever runs the ACP server, whether that's you on a laptop, a team lead with a small VM, or a
company with its own infrastructure. It covers what to pick, what to ask first, and the shortest path for each case.
Everything else (flags, quotas, scopes, the cluster) lives in [OPERATIONS.md](OPERATIONS.md); this page links there
when you need it.

> **ACP is self-hosted.** You run the server (`coordd`) on your own machine or infrastructure. There is no ACP
> service to sign up for; the ab0t server you may have seen mentioned is ab0t's own private one, not a public
> service.

If you're an AI agent asked to set ACP up, read [AGENT-SETUP.md](AGENT-SETUP.md) instead. It's this page turned into
steps for you. If the words "mode" or "profile" are confusing, [MODES.md](MODES.md) explains each one.

---

## In one minute

- **One server, many agents.** Agents never talk to each other directly. They all connect to one `coordd`, which
  holds the shared files, messages and history.
- **Start on one machine.** When an agent on another machine needs to join, move the server (or add a name to it) so
  that machine can reach it.
- **Give every agent its own login.** `acp invite --name <agent>` prints a block of text. The other agent pastes it
  and runs one `acp join` command, and it's connected with its own login and nothing to copy by hand.
- **The server makes its own certificate.** You don't need to buy or install one. The invite carries the
  certificate's fingerprint, so the joining agent trusts the right server automatically. (Behind your own HTTPS
  proxy or edge with a public certificate, the invite says to trust that certificate the usual way instead.)
- **Your data is one folder.** Everything the server knows lives in its data folder (`-data`). Back that folder up.

---

## Questions to ask first

Answer these before you install anything. If you're setting this up *for* someone, ask them. An AI agent should
always ask its human rather than guess.

| # | Question | Why it matters | If you don't know |
|---|---|---|---|
| 1 | **Is there already an ACP server you should join?** Do you have an invite code (`acpj1_…`)? | Then you don't run a server at all: `acp join <code>`. | Ask whoever mentioned ACP. |
| 2 | **Where will the agents run?** All on this machine, on several machines on one network, or on different networks? | Decides where the server goes and what name it needs. | Start with this machine; you can move later. |
| 3 | **Who connects?** Only your own agents, or other people's agents too? | Other people means their own logins (invites), roles, and maybe separate workspaces. | Assume other people: use invites. |
| 4 | **What address will the others use to reach it?** A DNS name, a public IP, a private-network address? | The server's certificate must cover that address. | Use this machine's name on your network. |
| 5 | **Docker or a plain program?** | Pick whichever you already use. | Plain program for a laptop, Docker for a server. |
| 6 | **Must it come back after a reboot?** | You need a service (systemd) or Docker Compose. | Yes for anything shared. |
| 7 | **Where should the data folder live, and how is it backed up?** | It's the only copy of shared history and messages. | A fixed path such as `/srv/acp`, copied off the machine daily. |
| 8 | **Is a public HTTPS name with a normal certificate required** (company policy, browsers)? | Then you put your own HTTPS proxy in front. | No: the built-in certificate is fine. |
| 9 | **Can the server ever be down?** | "Never" means a 3-node cluster, which is **beta**. | One server plus backups. |
| 10 | **Will one workspace keep a very long history** (hundreds of thousands of events)? | Then use the server database storage. | No: the default file storage. |

---

## Decide: pick this when

### Plain program or Docker

| Pick | When | Notes |
|---|---|---|
| **Plain program** (`install.sh`) | A laptop, a dev box, or a server where you'd rather run a systemd service than Docker. | Installs `acp`, `coordd` and `acp-mcp` to `~/.local/bin`. Upgrade with `acp update`, then restart `coordd`. |
| **Docker** (`ab0tcom/acp:v0.2.5`) | You already run containers, or you want upgrades to be "change the tag, pull, recreate". | Data lives in a volume. Use a bounded restart policy (`on-failure:5`), never `always`. See [DOCKER.md](DOCKER.md). |

Both run the same server.

### Storage: file or server database

| Pick | When |
|---|---|
| **File storage** (default: `coordd`, image tag `vX.Y.Z`) | Almost always. It uses the least memory when idle, and its files are plain enough to inspect with `grep`. |
| **Server database storage** (`coordd-server`, image tag `vX.Y.Z-server`) | A workspace keeps roughly **100,000+ events** of history and you want memory to stay flat as it grows. |

Clients can't tell the difference, and you can convert either way later without losing anything. See
[STORAGE-MODES.md](STORAGE-MODES.md).

### One server or a cluster

| Pick | When |
|---|---|
| **One server** (default) | Almost always. Pair it with daily off-machine backups. A restart loses nothing, because state is on disk. |
| **3-node cluster** (**beta**) | You need it to keep working when a machine dies, you can run three machines on a private network, and you accept beta. |

The cluster is **beta**: run the same version on every node, and read the cluster section of
[OPERATIONS.md](OPERATIONS.md) before you start one.

### Logins: one shared key or one login per agent

| Pick | When |
|---|---|
| **One login per agent** (recommended) | Any team, any agent you didn't start yourself, anything you might need to revoke. This is what invites give you: each agent gets its own login, scoped to one workspace, as a writer or reader. |
| **One shared key** | Only your own agents on your own machines, for a quick try. Everyone who has the key is an admin by default, and an agent's name is whatever it claims. Invites don't work in this mode. |

A brand-new server starts with one login per agent, and makes the first one for you: on its first start it creates
an admin login named `owner` and writes its token to `<data>/owner.token` (private, 0600). Start-up prints where
the file is and the exact `acp config set …` line that connects this machine; the token itself is never printed.
Want a different name? Start the first time with `-owner-name <name>`.

Existing data folders keep the mode they have: a folder with a shared key in `<data>/token` stays on the shared
key, and one with logins in `agents.json` keeps them. For the old start on a new folder (one shared key in
`<data>/token`), add `-shared-token`; `-token <value>` also still means a shared key.

### The certificate

| Pick | When | What you do |
|---|---|---|
| **The automatic certificate** | Agents connect by `localhost`, this machine's name, or an address this machine has on one of its network cards (LAN, VPN). | Nothing. Leave `-hosts` off. |
| **The automatic certificate plus `-hosts`** | Agents connect by a name or address the machine can't see on itself: a cloud VM's public IP, a DNS name, or the Docker host's address. | Set `-hosts` to the extra names and addresses agents will use. From v0.2.5 they're added to the automatic list (`-hosts-exact`: only these); on v0.2.4 and older `-hosts` replaces the list, so include `localhost,127.0.0.1` there. |
| **Your own HTTPS proxy with a public certificate** | Company policy, a public DNS name, or clients that must use the system's trusted certificates. | Run nginx, Caddy or Traefik in front, talking HTTPS to `coordd`. Clients then need no certificate settings at all. See "Behind a proxy" below. |

The certificate is made once, on first start, and never rewritten, because agents have pinned it. To change what
it covers, stop `coordd`, move `cert.pem` and `key.pem` out of the data folder, and start it again with the right
`-hosts`. Every agent then has to re-join, or update its `pin`. At start-up, `coordd` names any local address the
certificate doesn't cover.

Never set `insecure=true` on a client outside a throwaway test.

### Reaching it across networks

| Situation | What works |
|---|---|
| Everything on one machine | `https://localhost:8443`. Nothing to set up. |
| Same office or home network | This machine's name or LAN IP. The automatic certificate already covers it. |
| Laptops on different networks (home, café, NAT) | Put the server on a machine both can reach: a small cloud VM with port 8443 open, or a private network you already use (Tailscale, WireGuard). If that network was up when `coordd` first started, its address is already in the certificate. |
| A public name with normal HTTPS | Your own proxy in front (below). |

ACP doesn't provide a relay or tunnel. Agents need an address they can reach.

### Where data lives, and backups

- **Server:** everything is in the data folder: shared files and their history, messages, the activity log,
  documents, logins (stored as hashes only) and the certificate. Use a fixed absolute path (`-data /srv/acp`). The
  default `./acp-data` depends on the folder you start from: start `coordd` somewhere else and you get a brand-new,
  empty server with a different certificate.
- **Docker:** the `/data` volume.
- **Each agent's machine:** its logins in `~/.acp/config.json` (readable only by that user), plus its working copies
  of shared files.

**Backups:** stop the server cleanly (Ctrl-C or SIGTERM, which prints "stopped cleanly"), or snapshot the disk, then
copy the data folder somewhere off the machine. To restore, stop the server, put the folder back, and start it. Do
this on a schedule (a nightly cron job is enough). ACP doesn't schedule backups for you.

---

## The shortest paths

Each path ends the same way: `acp status` says you're logged in, and `acp invite` connects the next agent.

### Solo developer: one laptop, a few agents

```bash
# 1. Install acp, coordd and acp-mcp
curl -fsSL https://raw.githubusercontent.com/ab0t-com/acp/main/install.sh | bash

# 2. Start the server and leave it running (no -hosts: the certificate covers this machine).
#    The first start makes your admin login "owner" (token in ~/acp-data/owner.token, never on screen)
#    and ends with the exact line to connect this machine.
coordd -data ~/acp-data

# 3. In another terminal, paste the `acp config set owner …` line it printed, then check
acp config set owner server=https://127.0.0.1:8443 token_file=$HOME/acp-data/owner.token \
   agent=owner pin=sha256:<from the start-up line>
acp status                         # login: ok, owner as admin

# 4. Connect an agent: paste what this prints into it
acp invite --name codexB
```

The other agent runs the one `acp join acpj1_…` line from the paste and is connected. An invite that points at
`localhost` only works for agents on this laptop. For another machine, see the next path.

### Team lead: one server for a small team on several machines

1. **Get a machine everyone can reach**, for example a small cloud VM. Open TCP port 8443 to your team, and decide
   the name they'll use (`acp.example.com` or the public IP).
2. **Write this `docker-compose.yml`** (or use the plain program with the same flags):

   ```yaml
   services:
     acp:
       image: ab0tcom/acp:v0.2.5            # a fixed version; you choose when to upgrade
       command: ["-addr", ":8443", "-data", "/data",
                 "-hosts", "acp.example.com,203.0.113.5,localhost,127.0.0.1"]   # every name agents use
       ports: ["8443:8443"]
       volumes: ["acp-data:/data"]
       restart: on-failure:5                # bounded restart; never always/unless-stopped
       logging: { options: { max-size: "10m", max-file: "3" } }
   volumes:
     acp-data:
   ```
3. **Start the server, then copy out your admin token.** The first start makes the admin login `owner` by itself
   and writes its token to `/data/owner.token` in the volume (the log says so; the token is never printed):

   ```bash
   docker compose up -d
   mkdir -p ~/.acp/tokens
   (umask 077; docker compose exec -T acp cat /data/owner.token > ~/.acp/tokens/owner.token)
   docker compose logs acp | grep -o 'pin=sha256:[0-9a-f]*' | head -1   # the certificate fingerprint
   ```
4. **Connect yourself by the public name**, so the invites you create point there:

   ```bash
   acp config set owner server=https://acp.example.com:8443 token_file=~/.acp/tokens/owner.token \
      pin=sha256:<from step 3> agent=owner
   acp status
   ```
5. **Invite each person's agent** into the team's workspace: `acp invite --name alice-claude --space team`. Each
   invite works once, expires in 24 hours, and gives a writer login (`--role reader` for read-only). Check who
   joined with `acp invites`. Remove someone with `acp invites remove-member <id>`.
6. **Back up the volume nightly.** For example, stop, copy, start:
   `docker compose stop acp && docker run --rm -v <project>_acp-data:/data -v "$PWD":/backup alpine tar czf /backup/acp-$(date +%F).tgz -C /data . && docker compose start acp`.
   (`<project>` is your Compose project name, usually the folder name. `docker volume ls` shows the full volume
   name.) Copy the `.tgz` somewhere off the machine.

**Upgrades:** change the tag, then `docker compose pull && docker compose up -d`. Read the release notes first:
v0.2.4 rewrites `agents.json` in a form older versions refuse, and keeps a backup beside it for rolling back.

**Plain program instead of Docker:** install with `install.sh`, run the same `coordd` flags with `-data /srv/acp`
from a systemd service (below). The first start writes the admin token to `/srv/acp/owner.token`; copy it to
`~/.acp/tokens/owner.token` (0600) on the machine you manage from.

### Company: own infrastructure, security review

Start from the team lead path and add:

| Need | Do this |
|---|---|
| A public name and a normal HTTPS certificate | Your proxy in front (below). |
| Least privilege | Invites only (writer or reader, one workspace each). For finer control, an admin can mint logins limited to folders (`-path-prefix`, `-read-prefix`): see the scoped-tokens section of [OPERATIONS.md](OPERATIONS.md). Nothing uses the shared key. |
| Separate teams or customers | One **workspace** per team or customer. A workspace is the isolation boundary. Logins created by invites only reach their own workspace. Set per-workspace limits with quotas. |
| Encryption at rest | Put the data folder on an encrypted volume (LUKS, cloud disk encryption). For file contents, also add `-data-key-file` (below). |
| Survive a machine loss | One server plus off-machine backups, or a 3-node cluster (**beta**). |
| A long history in one workspace | Server database storage. |
| Your own start page | `-root-page-file your-page.json`, or `-root-page=false` to turn the public start page off. |

What ACP doesn't do yet, so nobody is surprised in a review: there's no single sign-on (Okta, Entra and so on); the
activity log is append-only but not tamper-evident; at-rest encryption inside ACP covers file contents, not
metadata (the encrypted volume covers the rest); the cluster is beta. [THREAT_MODEL.md](THREAT_MODEL.md) has the
details.

---

## Running it for real

### Survive a reboot (plain program, systemd)

```ini
# /etc/systemd/system/coordd.service
[Unit]
Description=ACP server (coordd)
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=300
StartLimitBurst=5

[Service]
User=acp
ExecStart=/usr/local/bin/coordd -addr :8443 -data /srv/acp
Restart=on-failure
RestartSec=5
RestartPreventExitStatus=78
KillSignal=SIGTERM

[Install]
WantedBy=multi-user.target
```

`Restart=on-failure` with the start limit stops after 5 failures in 5 minutes. Exit code 78 means "a problem only a
person can fix" (a bad flag, a damaged data folder, a wrong encryption key), so systemd doesn't retry it. Create an
`acp` system user, install the program somewhere it can run it (`ACP_INSTALL_DIR=/usr/local/bin` with `install.sh`),
and create `/srv/acp` owned by that user.

With Docker, `restart: on-failure:5` covers crashes but not a host reboot. To start on boot, run
`docker compose up -d` from a small systemd unit. Don't switch to an unbounded restart policy.

### Encryption at rest for file contents

```bash
(umask 077; openssl rand -hex 32 > /etc/acp/data.key)
coordd -data /srv/acp -data-key-file /etc/acp/data.key      # or set ACP_DATA_KEY
```

Shared file contents are then stored encrypted (AES-256-GCM). Keep the key **outside** the data folder and back it
up separately: without it the files can't be read, and a wrong key stops the server from starting. Every cluster
node needs the same key. Messages, the activity log and other metadata aren't covered, so use an encrypted volume
for those.

### Behind a proxy with a public certificate

`coordd` only speaks HTTPS and always uses its own certificate. It can't load yours. So the proxy holds the public
certificate and connects to `coordd` over HTTPS. Your proxy must:

- connect to `https://<coordd>:8443`, trusting `coordd`'s own certificate (or its fingerprint);
- not buffer responses, and allow long-lived connections (`acp watch`, `acp inbox --wait` and live documents stream
  for minutes);
- pass the `Authorization` header and the `X-ACP-*` headers through unchanged.

Then start `coordd` with `-discovery-endpoint https://acp.example.com` and `-discovery-cert-fingerprint=false`, so
its discovery document names the public address and doesn't advertise a fingerprint that clients would never see.
Clients set only `server=https://acp.example.com`, with no `cert` and no `pin`: they trust the system's
certificates. Invites you create through that address tell joining agents to do the same. Behind a proxy, every
client shares the proxy's address for the per-address invite rate limit.

A tested nginx/Caddy recipe isn't published yet. The list above is what such a recipe has to do.

### Check it's healthy

```bash
acp status                 # profile, server, workspace, and "login: ok, <you> as admin"
acp health                 # ok
acp stats                  # counters for this workspace
curl -sk https://localhost:8443/v1/healthz   # no login needed: status + features (-k: a local probe only)
```

Opening the server's address with no login (`GET /`) returns a public "start here" page. It never includes
workspace, agent or login data.

### Manage logins

| Task | Command |
|---|---|
| Connect a new agent | `acp invite --name <agent> [--space <ws>] [--role reader] [--ttl 2h]` |
| See invites and who joined | `acp invites` |
| Cancel an unused invite | `acp invites revoke <id>` |
| Remove an agent an invite let in | `acp invites remove-member <id>` |
| List every login (on the server machine) | `coordd -data /srv/acp -lsagents` |
| Revoke a login (server running) | `DELETE /v1/admin/grants?fingerprint=<fp>` with an admin token (see [OPERATIONS.md](OPERATIONS.md)) |
| Revoke a login (server stopped) | `coordd -data /srv/acp -rmagent <fingerprint or name>`, then start it (refused while the server runs) |
| List workspaces / remove an empty one | `acp admin spaces` / `acp admin spaces drop <name> --confirm <name>` |

`-mkagent` and `-rmagent` refuse while the server is running on that folder (the running server would undo the edit),
and in a cluster once logins are shared across nodes. Use invites, or the logins API described in
[OPERATIONS.md](OPERATIONS.md).

---

## When something goes wrong

| You see | Do this |
|---|---|
| `acp invite`: "this server gives every agent one shared token, so it can't create per-agent invites" | The server runs on one shared key: an older data folder, or one started with `-shared-token` or `-token`. To switch: stop `coordd`, run `(umask 077; coordd -data <dir> -mkagent <you> -role admin \| sed -n 's/^agent .* token: //p' > ~/.acp/tokens/<you>.token)`, start it again (without `-token`), and point your profile at that token file. The old shared key stops working. |
| Where is my admin token? | On a server that made its own login: `<data>/owner.token` (Docker: `docker compose exec -T acp cat /data/owner.token`). Each start prints its path and the connect line while that file holds a live admin login. |
| `acp invite`: "the server's certificate does not cover …" | You invited by an address the certificate doesn't list. Invite by one it does list, or re-make the certificate with the right `-hosts` (see "The certificate"). |
| An invite that points at `127.0.0.1`/`localhost` doesn't work on another machine | Invite by an address that machine can reach: `ACP_SERVER=https://<name>:8443 acp invite --name <agent>`. |
| Client: "the daemon's certificate isn't trusted" | Add `pin=sha256:…` (printed at start-up) or `cert=<path to cert.pem>` to the profile. Or use an invite, which sets this for you. |
| `401 invalid or missing bearer token` | Wrong or revoked login. After switching to one-login-per-agent, the old shared key is dead. Get an invite. |
| `coordd`: "store directory … is locked by another running coordd" | A server is already running on that data folder. Stop it first; never run two on one folder. |
| `coordd` exits with "OPERATOR ACTION REQUIRED" (exit 78) | The message names the problem (a bad flag, an unreadable data folder, a wrong key). Fix it; restarting won't help. |
| `coordd` refuses to start: `agents.json version …` | The program is older than the logins file. Upgrade it. Don't hand-edit the file. |
| `listen :8443: address already in use` | Something else has the port. Stop it, or use `-addr :9443`. |
| Agents on another network can't connect | Check the port is open between them, and that the certificate lists the address they use (the start-up log names addresses it doesn't cover). |

More symptoms (403, 409, 423, 429, 507): the "Failure handling" table in [OPERATIONS.md](OPERATIONS.md).

---

## Where next

- [AGENT-SETUP.md](AGENT-SETUP.md): the same setup, written for an AI agent to follow.
- [MODES.md](MODES.md): every "mode" in ACP, what it does, and which ones you can ignore.
- [OPERATIONS.md](OPERATIONS.md): the full reference (flags, scopes, quotas, cluster, history, limits).
- [DOCKER.md](DOCKER.md) · [STORAGE-MODES.md](STORAGE-MODES.md) · [THREAT_MODEL.md](THREAT_MODEL.md)
- The `acp-operations` skill: give it to an agent that runs the server. The `acp-client` skill: give it to agents
  that use one.

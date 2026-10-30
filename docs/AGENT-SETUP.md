# Set up ACP: instructions for an AI agent

You are an AI agent, and a person asked you to set up ACP: shared files and messages for AI agents. This file tells
you what to ask, what to run, how to check it worked, what to do when something fails, and how to report what was
hard. Follow it in order. For release v0.2.4 and later.

People reading this: the human-oriented version is [RUN-ACP.md](RUN-ACP.md).

---

## 0. Rules for you

1. **Ask, don't guess,** on every decision in step 2. A wrong guess here means a server nobody can reach, or a
   login you have to revoke.
2. **Never show a token or an invite code in chat, logs or files you write.** Tokens go into `0600` files and are
   referenced by `token_file=`. Invite codes go only to the agent they're for.
3. **Never use `insecure=true`, `ACP_INSECURE` or `curl -k` against a server other agents will use.** Trust comes
   from the certificate pin; the commands below set it for you.
4. **Don't pass `-hosts` unless step 2 says to.** It replaces the automatic certificate names; it doesn't add to
   them.
5. **Never delete or overwrite an existing data folder** (`acp-data`, `/srv/acp`, a Docker volume), or a
   `cert.pem`/`key.pem`. It holds other people's shared state and the certificate their agents trust.
6. **Don't run the server as root,** and don't use `restart: always` or `unless-stopped` in Docker.
7. **Write down friction as you go** (step 7). It's part of the job, not optional.

---

## 1. Do you already have an invite code?

If the person gave you text containing `acpj1_…`, you are **joining**, not setting up a server:

```bash
acp version || curl -fsSL https://raw.githubusercontent.com/ab0t-com/acp/main/install.sh | bash
acp join acpj1_...                       # the whole code, exactly as given
acp --profile <your-name> status         # "login: ok, <your-name> as writer"
```

Then follow the "How to talk" lines in the invite text. Done: skip to step 7.

If `acp` isn't found after installing, run `export PATH="$HOME/.local/bin:$PATH"` (`install.sh` prints this when
it's needed).

---

## 2. Ask your human these questions (all at once, in one message)

Copy this, fill in the defaults, and send it:

> To set up ACP I need a few answers (defaults in brackets):
> 1. Will the agents all run **on this machine**, or on **other machines** too? [this machine]
> 2. If other machines: what **address** will they use to reach this one (a DNS name or IP)? Is TCP port **8443**
>    open to them? [n/a]
> 3. **Docker** or a plain program? [plain program on a laptop; Docker on a server]
> 4. Must the server **start again after a reboot**? [no for a laptop, yes for a server]
> 5. Where should the **data folder** live? It holds all shared state and must be backed up. [`~/acp-data`]
> 6. What **names** should the agents have (one per agent)? [`codexB`]
> 7. Is there a company **HTTPS proxy** or public certificate this must sit behind? [no]

Turn the answers into a plan:

| Answers | Path | Notes |
|---|---|---|
| 1 = this machine | **Path A** (step 3A) | No `-hosts`. |
| 1 = other machines, and the address is one this machine already has (LAN IP, VPN IP, its hostname) | **Path A** + a service (step 3C if 4 = yes) | No `-hosts`. The certificate covers this machine's own addresses. Check with step 4. |
| 1 = other machines, and the address is a public IP or DNS name this machine can't see on itself (cloud VM) | **Path B** (step 3B) | `-hosts <every address>,localhost,127.0.0.1`. |
| 3 = Docker | **Path B** | Docker always needs `-hosts` for other machines (the container can't see the host's addresses). |
| 7 = yes | Stop and tell the human | The proxy setup needs someone with access to it. Point them at "Behind a proxy" in [RUN-ACP.md](RUN-ACP.md). |

Leave these at their defaults unless the human asks: file storage (not the server database), one server (not a
cluster: that's beta), the public start page on, encryption at rest off. See [MODES.md](MODES.md) if they ask.

---

## 3. Commands

### 3A. This machine only (plain program)

```bash
# Install (acp, coordd, acp-mcp into ~/.local/bin)
curl -fsSL https://raw.githubusercontent.com/ab0t-com/acp/main/install.sh | bash
export PATH="$HOME/.local/bin:$PATH"

# Check there's no server here already. If this folder exists, STOP and ask the human.
ls ~/acp-data 2>/dev/null && echo "EXISTS: ask before continuing"

# Start the server in the background, logging to a file. On its FIRST start it makes the human's
# admin login "owner" by itself: the token goes to ~/acp-data/owner.token (0600), never the screen.
nohup coordd -data ~/acp-data > ~/acp-data.log 2>&1 &
sleep 2; grep -m1 'listening on' ~/acp-data.log      # ... auth=per-agent (1 identities) ...
grep -A3 'FIRST START' ~/acp-data.log                # the login name, its token file, the connect line

# Keep the token with the client config (copy, 0600), then connect the admin profile.
# (Or paste the printed `acp config set owner …` line as-is: it points at ~/acp-data/owner.token.)
mkdir -p ~/.acp/tokens
(umask 077; cp ~/acp-data/owner.token ~/.acp/tokens/owner.token)
acp config set owner server=https://localhost:8443 token_file=~/.acp/tokens/owner.token \
   cert=$HOME/acp-data/cert.pem agent=owner
acp status
```

If the human wants their own name instead of `owner`, add `-owner-name <name>` to that first start and use the
name in the profile too. If agents on other machines will connect, use the address they'll use (from question 2,
e.g. `server=https://192.168.1.20:8443`) instead of `localhost`, so the invites you create point there. If
`~/acp-data` already existed with a shared key (`~/acp-data/token`), no login is made: see the
`shared token` row in section 5.

### 3B. A server other machines reach (Docker)

```bash
mkdir -p ~/acp-server && cd ~/acp-server
cat > docker-compose.yml <<'EOF'
services:
  acp:
    image: ab0tcom/acp:v0.2.4
    command: ["-addr", ":8443", "-data", "/data",
              "-hosts", "ADDRESSES,localhost,127.0.0.1"]
    ports: ["8443:8443"]
    volumes: ["acp-data:/data"]
    restart: on-failure:5
    logging: { options: { max-size: "10m", max-file: "3" } }
volumes:
  acp-data:
EOF
# Replace ADDRESSES with the human's answer to question 2 (comma-separated, no spaces), e.g.
#   acp.example.com,203.0.113.5
sed -i 's/ADDRESSES/acp.example.com,203.0.113.5/' docker-compose.yml

# Start. The first start makes the admin login "owner" (token in /data/owner.token, never printed);
# copy the token out of the volume into a private file.
docker compose up -d
sleep 3
mkdir -p ~/.acp/tokens
(umask 077; docker compose exec -T acp cat /data/owner.token > ~/.acp/tokens/owner.token)
test -s ~/.acp/tokens/owner.token && echo "admin login saved"
PIN=$(docker compose logs acp | grep -o 'pin=sha256:[0-9a-f]*' | head -1 | cut -d= -f2)
echo "$PIN"            # sha256:... (a fingerprint, not a secret)

# Install acp on this machine, then connect by the PUBLIC name so invites point there
curl -fsSL https://raw.githubusercontent.com/ab0t-com/acp/main/install.sh | bash
acp config set owner server=https://acp.example.com:8443 token_file=~/.acp/tokens/owner.token \
   pin=$PIN agent=owner
acp status
```

Plain program instead of Docker: install with `install.sh`, use `coordd -data /srv/acp -hosts <addresses>,localhost,127.0.0.1`,
run it from the systemd unit in [RUN-ACP.md](RUN-ACP.md), and copy `/srv/acp/owner.token` (written on the first
start) to `~/.acp/tokens/owner.token` (0600). The pin is on the server's start-up line `clients: … pin=sha256:…`.

### 3C. Start after a reboot (plain program)

Use the systemd unit in [RUN-ACP.md](RUN-ACP.md) → "Survive a reboot". This needs `sudo`: ask the human to run it,
or ask permission first. With Docker, `restart: on-failure:5` already covers crashes; for reboots, the same page
shows a small systemd unit.

### 3D. Connect the other agents

For each agent name from question 6:

```bash
acp invite --name codexB                 # add --space <workspace> to put it in a named workspace
acp invite --name codexB --mcp           # for an agent that uses MCP tools instead of a shell
```

Each prints a block of text. Give that block **only** to that agent (or to the human, to pass on). It works once and
expires in 24 hours. Don't paste it into a shared channel or a file in a repository.

For an MCP agent on this machine, after it has joined:
`claude mcp add acp -- env ACP_PROFILE=codexB acp-mcp` (then restart the harness).

In a project folder an agent works in, `acp link` (after joining) makes every `acp` command there use the right
login and workspace with no flags.

---

## 4. Verify (do every step; report the results)

| # | Run | Expect |
|---|---|---|
| 1 | `acp version` | `acp v0.2.4 (protocol acp/1)` or newer |
| 2 | `acp status` | `login: ok, owner as admin` and the right server |
| 3 | `acp health` | `ok` |
| 4 | `acp send --to owner --body "setup check"` then `acp inbox --wait --timeout 30s` | the message appears |
| 5 | `acp invites` (after 3D) | each invited agent listed; `JOINED` once it has joined |
| 6 | Other machines only: on another machine, `acp join` an invite, then `acp --profile <name> status` | `login: ok` |
| 7 | Reboot survival only: restart the service, then `acp status` | still `login: ok` |

If step 6 fails with a certificate message, see the table below. Don't work around it with `insecure`.

---

## 5. What to do on each common error

| Error (first line) | Cause | Do |
|---|---|---|
| `acp: command not found` | `~/.local/bin` isn't on `PATH` | `export PATH="$HOME/.local/bin:$PATH"` |
| `this server gives every agent one shared token, so it can't create per-agent invites` | The data folder already used one shared key (an older server), or it was started with `-shared-token` / `-token` | **Ask the human first** (switching stops the shared key). Then: stop `coordd`; `(umask 077; coordd -data <folder> -mkagent owner -role admin \| sed -n 's/^agent .* token: //p' > ~/.acp/tokens/owner.token)`; start again without `-token`/`-shared-token`; `acp config set owner token_file=~/.acp/tokens/owner.token`; `acp status`. |
| `the server's certificate does not cover "<addr>"` | You're inviting or connecting by an address the certificate doesn't list | Use an address it lists (the start-up log names the ones it misses). Otherwise **ask the human** before re-making the certificate: stopping, moving `cert.pem`/`key.pem` aside and restarting with `-hosts` breaks every agent that already joined. |
| `note: this invite points at 127.0.0.1 …` | The admin profile uses `localhost` | For another machine: `ACP_SERVER=https://<address>:8443 acp invite --name <agent>` |
| `the daemon's certificate isn't trusted` | The profile has no `pin` or `cert` | `acp config set <profile> pin=sha256:…` (from the start-up log), or join with an invite |
| `acp 401: invalid or missing bearer token` | Wrong token file, a revoked login, or the old shared key after switching | Check `token_file` points at the right file (`acp status`); get a new invite |
| `missing token/agent` | No profile, or the wrong one | `acp status` shows which profile is used and why; `acp --profile <name> …` or `acp config use <name>` |
| `This invite expired.` / `…was cancelled.` / `already used` | The invite is spent | Ask the admin for a new one: `acp invite --name <agent>` |
| `"<name>" is already a member of this server` | That name exists | Pick another `--name`, or remove the old one (`acp invites remove-member <id>`) |
| `store directory … is locked by another running coordd` | A server is already running on that folder | Don't start a second one. Use the running one, or stop it first. |
| `listen :8443: address already in use` | The port is taken | Find what has it; ask before stopping anything you didn't start. Or use `-addr :9443` (and `:9443` everywhere). |
| `OPERATOR ACTION REQUIRED` (exit 78) | A permanent config problem, named in the message | Fix what it names; restarting won't help. If it mentions the data folder or a key, stop and ask the human. |
| `refuses to start … agents.json version` | The `coordd` program is older than the data folder | Upgrade (`acp update`, or a newer image tag). Never hand-edit `agents.json`. |
| Docker: permission denied in `/data` | Usually a host folder mounted instead of a named volume (the image runs as user 10001) | Use the named volume from 3B. |
| Connection times out from another machine | Port closed, wrong address, or NAT | Ask the human to open TCP 8443 or confirm the address; a cloud firewall/security group is the usual cause |

Something not in this table: read the whole error message, since most name the next step. Try it once, then record
it as friction (step 7) and tell the human.

---

## 6. Tell your human what you did

When you finish, send the human this summary (fill it in; no tokens, no invite codes):

```
ACP is running.
- Server:        https://<address>:8443   (Docker / plain program, version …)
- Data folder:   <path or volume name>    ← back this up; it's the only copy
- Starts on boot: yes / no
- Admin login:   profile "owner", token in ~/.acp/tokens/owner.token (keep private)
- Agents invited: codexB (joined), …
- To invite more: acp invite --name <agent>
- To stop/start: <the exact commands for this setup>
- Friction log:  <path to acp-setup-friction.md>  (N items)
```

---

## 7. Report your friction

ACP's makers want to know every place this setup was slow, confusing or wrong, so the next person doesn't hit it.
Keep a log **while you work**, not from memory at the end.

**Where:** append to `acp-setup-friction.md` in your current working folder (create it if it doesn't exist).
Then:
- give the human the file path in your summary (step 6);
- if you're connected to an ACP server and its admin collects these, also send it:
  `acp send --to <admin> --subject "acp setup friction" --body "$(cat acp-setup-friction.md)"`
  (the subject is visible to the workspace; the body only to the recipient);
- with the human's OK, open an issue at https://github.com/ab0t-com/acp/issues titled `Setup friction: <one line>`,
  pasting the entries.

**Format:** one header, then one entry per problem. Record an entry even when you solved the problem yourself.
If nothing went wrong, write one `F0 none` entry so we know the path was clean.

```markdown
# ACP setup friction — <YYYY-MM-DD> — <your harness, e.g. Claude Code> — path <3A|3B|join>
acp version: <output of acp version> · OS: <uname -sr> · Docker: <version or no>

### F1 · <step, e.g. 3B admin login> · <blocker | painful | annoying | polish>
- doing:    <what you were trying to do, one line>
- ran:      `<the exact command>`
- expected: <one line>
- got:      <the first 1-3 lines of output; tokens, invite codes and private addresses removed>
- fix:      <what got you past it, or "stuck">
- cost:     <minutes lost, retries>
- doc gap:  <which file/section was wrong or missing, or "none">
```

Severity: **blocker** = you couldn't continue without the human; **painful** = you lost more than 5 minutes or had to
guess; **annoying** = a confusing message or an extra step; **polish** = wording.

Never put a token, an invite code, or an address the human wants private into this file.

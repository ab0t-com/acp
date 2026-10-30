# Quickstart: two agents on two machines

ACP is self-hosted: one machine runs the server, and every agent connects to it. Here machine S runs the server,
and two agents on machines 1 and 2 join it with invites. Setting up on one laptop instead? The README quickstart
is shorter, and [docs/RUN-ACP.md](../docs/RUN-ACP.md) covers every case.

## Server (machine S, one every agent can reach)
```bash
curl -fsSL https://raw.githubusercontent.com/ab0t-com/acp/main/install.sh | bash
coordd -addr :8443 -data /srv/acp -hosts s.example.com,localhost,127.0.0.1   # -hosts only because agents use a DNS name
```
The first start makes your admin login `owner`: its token goes to `/srv/acp/owner.token` (private, never printed),
and start-up ends with the exact `acp config set owner …` line for this machine. The `clients: … pin=sha256:…`
line shows the certificate fingerprint. Leave `-hosts` off when agents reach S by an address S has on itself (its
hostname, a LAN or VPN IP); when you set it, list every name agents use, because it replaces the automatic list.

## Connect the admin (on S) and invite
```bash
mkdir -p ~/.acp/tokens
(umask 077; cp /srv/acp/owner.token ~/.acp/tokens/owner.token)
acp config set owner server=https://s.example.com:8443 token_file=~/.acp/tokens/owner.token \
   pin=sha256:<fingerprint> agent=owner
acp status                     # login: ok, owner as admin
acp invite --name claudeA      # give the printed block to agent A
acp invite --name codexB       # and this one to agent B
```
Connect by the name the agents will use (`s.example.com` here), so the invites point there. Each invite works
once and expires in 24 hours.

## Each agent (machines 1 and 2)
```bash
curl -fsSL https://raw.githubusercontent.com/ab0t-com/acp/main/install.sh | bash
acp join acpj1_...             # from its invite; checks the server, gets its own login, saves a profile
acp status                     # login: ok, <name> as writer
```

## Collaborate
```bash
acp push ./workspace                        # A shares files
acp pull ./workspace                        # B receives
acp send --to codexB --body "split the work"          # A
acp inbox --wait                                      # B waits for mail
acp reply claudeA --body "ok, I take the backend"     # B answers in the thread
acp crdt sync spec.md spec.md                         # both: co-edit one file live
```
On a machine that holds several logins, add `--profile <name>` to pick one (`acp status` shows which one is in
use). In a project folder, `acp link` remembers the login and workspace for every command run there.

## No single point of failure
One server plus scheduled off-machine backups of `/srv/acp` first. A 3-node cluster is **beta**: see the
`acp-cluster` skill and [docs/OPERATIONS.md](../docs/OPERATIONS.md).

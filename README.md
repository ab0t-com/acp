# ACP — Agent Coordination Protocol

<p align="center">
  <img src="assets/acp-hero.png" alt="ACP — where AI agents work together" width="1215" height="472" style="max-width: 100%; height: auto; border-radius: 16px;" />
</p>

<p align="center">
  <b>Where AI agents work together.</b><br/>
  A shared filesystem and a comms line for multi-agent systems: <b>self-hosted</b>, with isolated workspaces and
  per-agent logins, and an optional 3-node cluster (<b>beta</b>).
</p>

<p align="center">
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/license-MIT-7C6CF0"></a>
  <img alt="release v0.2.5" src="https://img.shields.io/badge/release-v0.2.5-2DD4BF">
  <img alt="protocol acp/1" src="https://img.shields.io/badge/protocol-acp%2F1-FF9466">
</p>

A self-hostable system that gives multiple AI agent harnesses — on different machines —
a **shared filesystem** and a **comms line**, so they can collaborate on projects, specs,
and problems. Small Go server + CLI + MCP bridge. TLS, one login per agent, isolated workspaces.

**ACP is self-hosted.** You run the server (`coordd`) on your own machine or infrastructure; there is no ACP
service to sign up for. A one-page overview of what it does for you: [ui/landing.html](ui/landing.html) (open it
in a browser).

> This is the **public client repo**: install scripts, prebuilt release binaries, docs and skills. The
> implementation source is maintained privately; releases are published here. Licensed **MIT**. For companies:
> https://ab0t.com.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/ab0t-com/acp/main/install.sh | bash
# installs: acp (CLI), coordd (daemon), acp-mcp (MCP bridge) -> ~/.local/bin
acp version
```

Just the client: `ACP_BINS=acp curl -fsSL .../install.sh | bash`. Specific version:
`ACP_VERSION=vX.Y.Z`. Custom dir: `ACP_INSTALL_DIR=/usr/local/bin`.

Already installed? `acp update` fetches the latest release, verifies its checksum, and swaps
your binaries in place (`--check` to just see if you're behind; `--version vX.Y.Z` to pin).

## Start here

| You want to… | Read |
|---|---|
| Run a server (laptop, team, company): what to pick and the shortest path | [docs/RUN-ACP.md](docs/RUN-ACP.md) |
| Have an AI agent set ACP up for you: point it at this one file | [docs/AGENT-SETUP.md](docs/AGENT-SETUP.md) |
| Understand the "modes" (storage, logins, certificates, folder modes…) and which to ignore | [docs/MODES.md](docs/MODES.md) |
| Join a server someone invited you to | `acp join acpj1_…` (the invite text has the steps) |

## Quickstart: two agents in two minutes

```bash
# 1) start the server and leave it running (no -hosts: its certificate already covers this machine).
#    The first start makes your admin login "owner" (token in ~/acp-data/owner.token, never on screen)
#    and ends with the exact line to connect this machine.
coordd -data ~/acp-data

# 2) in another terminal: paste that `acp config set owner …` line, then check
acp config set owner server=https://127.0.0.1:8443 token_file=$HOME/acp-data/owner.token \
   agent=owner pin=sha256:<from the start-up line>
acp status                        # login: ok, owner as admin

# 3) invite an agent: paste the printed block into it
acp invite --name codexB
```

The other agent runs the one line from that block, and it's connected with its own login:

```bash
acp join acpj1_...                # checks the server, gets its own login, saves a profile
```

Then they talk and share:

```bash
acp send --to codexB --body "you backend, me frontend"
acp inbox --wait                  # blocks until a reply arrives; answer with: acp reply codexB --body "…"
acp pull ./shared                 # get shared files; edit; acp push ./shared
acp crdt sync spec.md spec.md     # co-edit one file live, auto-merges
```

An invite to `127.0.0.1` only works for agents on this machine. For agents on other machines, run the server
where they can reach it and invite by that address: see [docs/RUN-ACP.md](docs/RUN-ACP.md). Setting a login up by
hand: [docs/OPERATIONS.md](docs/OPERATIONS.md) §3.

## Run with Docker

Prefer containers? One command gives you a full ACP server:

```bash
docker run -d --name coordd --restart on-failure:5 -p 8443:8443 -v acp-data:/data ab0tcom/acp:v0.2.5
```

Shared filesystem, comms, live co-editing and presence in a single small image (`linux/amd64` + `arm64`). Your
admin login, TLS certificate and state all live in the `acp-data` volume, so upgrades are just "change the tag,
pull, recreate", with nothing lost.

The first start makes your admin login `owner` and logs the line that connects this machine. Copy the token out
of the volume into a private file, connect, and invite:

```bash
mkdir -p ~/.acp/tokens
(umask 077; docker exec coordd cat /data/owner.token > ~/.acp/tokens/owner.token)
docker logs coordd 2>&1 | grep -o 'pin=sha256:[0-9a-f]*' | head -1      # the certificate fingerprint
acp config set owner server=https://127.0.0.1:8443 token_file=~/.acp/tokens/owner.token \
   agent=owner pin=sha256:<fingerprint>
acp invite --name codexB
```

Agents on other machines need the certificate to cover the name they use: add
`-addr :8443 -data /data -hosts <name>` after the image name (from v0.2.5 the names are added to the automatic ones;
on v0.2.4 and older `-hosts` replaces them, so list `localhost,127.0.0.1` too). Do it on the volume's first start: the
certificate is never rewritten.

Want **database storage** (ACPDB): the same protocol backed by an embedded database that keeps memory flat as a
long history grows? Same command, the `-server` tag (it runs `coordd-server` for you):
`ab0tcom/acp:v0.2.5-server`.

**Full walkthrough (certificates, configuration, docker-compose, upgrades) in [docs/DOCKER.md](docs/DOCKER.md).**

## What you get

- **Shared filesystem** — content-addressed; `acp push` / `acp pull` with 3-way merge.
- **Live co-editing** — `acp crdt sync` lets agents edit the *same* file and auto-merge (CRDT).
- **Comms** — directed mailbox (`send`/`inbox`) + a totally-ordered event log (`watch`).
- **Live presence** — ephemeral awareness (cursors, typing, status) that self-expires in seconds and never touches durable storage; optional WebSocket.
- **Safe concurrency** — TTL leases with fencing tokens; never lose work.
- **Recover from anything** — every version is retained; `acp history` / `acp restore` undo a wrong overwrite or delete as a normal forward commit, and a named `acp checkpoint` pins a known-good state.
- **Can't mass-delete by accident** — a commit that would wipe a large share of the tree is refused until you confirm the exact counts (`--confirm-bulk`), and an ordinary writer token can't land a bulk change at all — a runaway loop can't wreck the workspace.
- **Isolated workspaces** — many workspaces on one server; every invited login is limited to its workspace. Per-agent identity + roles.
- **Invites** — `acp invite` prints one block of text; the other agent runs `acp join` and has its own login. Invites work once and expire.
- **Cluster (beta)** — run a 3-node **Raft** cluster (auto failover); **mTLS** between nodes. **Encryption at rest** for shared file contents.
- **Two storage options** — file storage by default, or **database storage (ACPDB)**: `coordd-server` embeds a real database for bounded, flat memory at scale. Same protocol, lossless conversion either way, no sidecar — *with database storage, ACP is the database*. → [docs/STORAGE-MODES.md](docs/STORAGE-MODES.md)
- **MCP bridge** — `acp-mcp` exposes ACP as tools to any MCP harness (Claude Code, Codex).

## Capabilities & extensions

Every daemon advertises what it supports in `/v1/healthz`'s `capabilities` array, so clients
can feature-detect instead of trial-and-erroring requests:

- `channels` — pub/sub on named channels with wildcard (`*`) topic filtering.
- `batchevents` — atomic multi-event append in one call.
- `crdtjson` — structured (JSON) CRDT documents, in addition to text CRDT sync.
- `crdtjson-move` — identity-preserving `mv` op for the JSON CRDT: reorder/relocate a node or array element without losing a peer's concurrent edit to it (safe drag-to-reorder, move-between-columns, reparent).
- `scopedtokens` — narrower-than-admin token grants.
- `scopedtokens.read` — confine a token to a subtree for **reads** too, not just writes; an out-of-scope path is indistinguishable from a missing one (no existence oracle).
- `history` — retained version history + restore/checkpoint: undo a wrong overwrite or delete as a forward commit (nothing re-uploaded; retained data is pinned against GC).
- `bulkguard` — a commit that would delete/overwrite a large share of the tree is refused (`428`) until the exact counts are confirmed; ordinary writer tokens are denied bulk changes by default.
- `acpuri` — one-string `acp://host/space/path` addressing, signed + expiring tokenless share links, and an optional read-only `/.well-known/acp` discovery document.
- `quotas` — per-space resource limits.
- `invites` — single-use, expiring invites: an admin creates one (`acp invite`), and the new agent redeems it (`acp join`) for its own login, limited to one workspace and never an admin.
- `tenancy`, `tenancy.subscope` — scoped tokens + quotas + sub-scope project labels within a space (least-privilege; a space remains the only hard isolation boundary).
- `awareness`, `awareness-ws` — live, self-expiring presence (cursors, typing, status), never written to durable storage; optional WebSocket transport with a 15 Hz server-side coalescing tick.

Extension proposals live in [rfc/](rfc/) as RFC-2119 documents; a capability only appears in
`healthz` once its extension is accepted and implemented.

## More

Full client guide: [docs/](docs/) and the **acp-client** skill in [Skills/](Skills/).
Run the server: **acp-operations** skill. Cluster (beta): **acp-cluster** skill.

## Build on it — TypeScript / JavaScript SDK

Prefer code to the CLI? [`@ab0t/acp`](https://www.npmjs.com/package/@ab0t/acp) talks to a daemon over
the same `acp/1` wire — Node ≥ 20 and evergreen browsers, **zero runtime dependencies**.

```bash
npm install @ab0t/acp
```

```js
import { Client } from "@ab0t/acp";
const c = new Client({ baseUrl: "https://<host>:8443", token: "<token>", agent: "app-1" });
await c.append({ action: "run.start", entity: "job/1" });      // ordered event log
const { hash, size } = await c.putBlob("# hello\n");            // content-addressed file
await c.send({ to: "worker-2", type: "inform", subject: "hi", body: "up" });  // mailbox
```

The same API runs in the browser, plus CRDT co-editing, live presence, and auto-reconnecting agent
loops (`followForever`, `withLease`). Full quickstart + API reference on
[npm](https://www.npmjs.com/package/@ab0t/acp). A Go SDK with the same interface contract lives in
[sdk/go/](sdk/go/): see [docs/GO-SDK.md](docs/GO-SDK.md).

## Docs

- [docs/RUN-ACP.md](docs/RUN-ACP.md) — **run a server**: what to pick, the shortest path for a laptop, a team, a company.
- [docs/AGENT-SETUP.md](docs/AGENT-SETUP.md) — the same setup, written for an AI agent to follow.
- [docs/MODES.md](docs/MODES.md) — every "mode" explained, and which ones you can ignore.
- [docs/AGENT-TEAMS.md](docs/AGENT-TEAMS.md) — how agents talk and work together.
- [docs/API_REFERENCE.md](docs/API_REFERENCE.md) — the wire API.
- [docs/OPERATIONS.md](docs/OPERATIONS.md) — the full reference: flags, logins, workspaces, quotas, cluster, back up, troubleshoot.
- [docs/DOCKER.md](docs/DOCKER.md) — run the daemon as a container (Docker Hub image, compose, TLS).
- [docs/THREAT_MODEL.md](docs/THREAT_MODEL.md) — security model.
- [docs/AWARENESS.md](docs/AWARENESS.md) — **live presence** (ext-9): cursors, typing, status over HTTP or WebSocket.
- [docs/STORAGE-MODES.md](docs/STORAGE-MODES.md) — **storage**: file storage (default) vs database storage (ACPDB) — crossover, memory tuning, lossless migration.
- [docs/mount/](docs/mount/) — mount a workspace as an ordinary folder.
- [rfc/acp-1.txt](rfc/acp-1.txt) — **the full protocol specification** (RFC-style, normative for `acp/1`).
- [rfc/acp-ext-1-channels-groups-pubsub.txt](rfc/acp-ext-1-channels-groups-pubsub.txt) — **extension proposal**: channels, groups & topic-filtered pub/sub (additive to `acp/1`).
- [Skills/](Skills/) — agent skills: acp-client, acp-operations, acp-cluster.
- [examples/](examples/) — quickstart walkthroughs.

## License

MIT — see [LICENSE](LICENSE). Built by [ab0t-com](https://ab0t.com).

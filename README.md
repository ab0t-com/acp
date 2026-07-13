# ACP — Agent Coordination Protocol

<p align="center">
  <img src="assets/acp-hero.png" alt="ACP — where AI agents work together" width="1215" height="472" style="max-width: 100%; height: auto; border-radius: 16px;" />
</p>

<p align="center">
  <b>Where AI agents work together.</b><br/>
  A shared filesystem and a comms line for multi-agent systems — self-hosted, multi-tenant, highly available.
</p>

<p align="center">
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/license-MIT-7C6CF0"></a>
  <img alt="release v0.1.0" src="https://img.shields.io/badge/release-v0.1.0-2DD4BF">
  <img alt="protocol acp/1" src="https://img.shields.io/badge/protocol-acp%2F1-FF9466">
</p>

A self-hostable system that gives multiple AI agent harnesses — on different machines —
a **shared filesystem** and a **comms line**, so they can collaborate on projects, specs,
and problems. Small Go daemon + CLI + MCP bridge. TLS, multi-tenant, highly available.

> This is the **public client repo**: install scripts, prebuilt release binaries, docs,
> and skills. The implementation source is maintained privately; releases are published
> here. Licensed **MIT**. Commercial/enterprise: see `ENTERPRISE.md` / https://ab0t.com.

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

## What you get

- **Shared filesystem** — content-addressed; `acp push` / `acp pull` with 3-way merge.
- **Live co-editing** — `acp crdt sync` lets agents edit the *same* file and auto-merge (CRDT).
- **Comms** — directed mailbox (`send`/`inbox`) + a totally-ordered event log (`watch`).
- **Safe concurrency** — TTL leases with fencing tokens; never lose work.
- **Multi-tenant** — isolated **spaces** on one daemon; per-agent identity + roles.
- **HA** — run a 3-node **Raft** cluster (auto failover); **mTLS** between nodes; **encryption at rest**.
- **MCP bridge** — `acp-mcp` exposes ACP as tools to any MCP harness (Claude Code, Codex).

## Capabilities & extensions

Every daemon advertises what it supports in `/v1/healthz`'s `capabilities` array, so clients
can feature-detect instead of trial-and-erroring requests:

- `channels` — pub/sub on named channels with wildcard (`*`) topic filtering.
- `batchevents` — atomic multi-event append in one call.
- `crdtjson` — structured (JSON) CRDT documents, in addition to text CRDT sync.
- `scopedtokens` — narrower-than-admin token grants.
- `quotas` — per-space resource limits.

Extension proposals live in [rfc/](rfc/) as RFC-2119 documents; a capability only appears in
`healthz` once its extension is accepted and implemented.

## Quickstart

```bash
# 1) run a daemon (any host both agents can reach)
coordd -addr :8443 -data ./acp-data -hosts <host>,127.0.0.1
#    prints a shared token (acp-data/token) and a pinned cert (acp-data/cert.pem)

# 2) configure a client profile (typed config in ~/.acp)
acp config init
acp config set prod server=https://<host>:8443 token=<token> cert=/path/cert.pem agent=claudeA
acp config use prod
acp who                                   # see who's online

# 3) collaborate
acp pull ./workspace                      # get shared files
acp send --to codexB --subject hi --body "you backend, me frontend"
acp crdt sync spec.md spec.md             # co-edit one file, auto-merges
```

Full client guide: [docs/](docs/) and the **acp-client** skill in [Skills/](Skills/).
HA deploy: **acp-cluster** skill. Operations: **acp-operations** skill.

## Docs

- [docs/API_REFERENCE.md](docs/API_REFERENCE.md) — the wire API.
- [docs/OPERATIONS.md](docs/OPERATIONS.md) — deploy, secure, cluster, back up, troubleshoot.
- [docs/THREAT_MODEL.md](docs/THREAT_MODEL.md) — security model.
- [rfc/acp-1.txt](rfc/acp-1.txt) — **the full protocol specification** (RFC-style, normative for `acp/1`).
- [rfc/acp-ext-1-channels-groups-pubsub.txt](rfc/acp-ext-1-channels-groups-pubsub.txt) — **extension proposal**: channels, groups & topic-filtered pub/sub (additive to `acp/1`).
- [Skills/](Skills/) — agent skills: acp-client, acp-operations, acp-cluster.
- [examples/](examples/) — quickstart walkthroughs.

## License

MIT — see [LICENSE](LICENSE). Built by [ab0t-com](https://ab0t.com).

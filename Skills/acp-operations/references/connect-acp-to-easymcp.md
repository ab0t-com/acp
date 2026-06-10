# Connecting ACP to EasyMCP

How to wire the ACP MCP bridge (`acp-mcp`) into your agents using the `easymcp` CLI.
Read this when an operator wants an MCP harness (Claude Code / Codex) to reach ACP.

## First: clear up "does acp-mcp have an MCP?"

**`acp-mcp` IS the MCP server.** It speaks MCP (JSON-RPC 2.0 over **stdio**) and exposes
ACP as tools (`acp_send`, `acp_inbox`, `acp_doc_read/write`, `acp_lease`, `acp_log_event`,
`acp_recent_events`, `acp_who`, `acp_stats`, ...). You do **not** create an MCP with
easymcp.

**`easymcp` is the manager/installer.** You use it to *register* the existing `acp-mcp`
stdio server as an instance and *install* it into an agent's config — the same way you
manage the rest of your MCP fleet. EasyMCP's heavier features (Docker runtime, OpenAPI →
tools, facets, downstream API auth) aren't required for a plain local stdio server, but
registering through easymcp keeps ACP consistent with everything else and gives you
`check`/`logs`/`ps`/`facet` for it. For the blessed end-to-end flow, also see
`easymcp-master-guide` (in the EasyMCP public repo Skills).

## Prerequisites

1. A running `coordd` (see `../SKILL.md` / `../../../acp/docs/OPERATIONS.md`).
2. The `acp-mcp` binary built at a stable path:
   ```bash
   cd /home/ubuntu/tools/shared_filesystem/acp
   go build -o /home/ubuntu/.local/bin/acp-mcp ./cmd/acp-mcp   # any stable path is fine
   ```
3. This agent's connection env: `ACP_SERVER`, `ACP_TOKEN`, `ACP_CERT`, `ACP_AGENT`
   (in per-agent mode the token IS the identity; mint one with `coordd -mkagent`).

## Register the instance (local process, stdio)

`acp-mcp` is a local-process stdio MCP server; pass its env via `--env`:

```bash
easymcp instance add acp \
  --kind local_process \
  --transport stdio \
  --command /home/ubuntu/.local/bin/acp-mcp \
  --env ACP_SERVER=https://<coordd-host>:8443 \
  --env ACP_TOKEN=<this-agent-token> \
  --env ACP_CERT=/path/to/cert.pem \
  --env ACP_AGENT=claudeA \
  --description "ACP shared-filesystem + comms bridge"
```

Notes:
- `acp-mcp` takes **no command-line args** — everything is env, so no `--arg` needed.
- **Per agent identity:** each agent needs its own `ACP_AGENT` (and its own token in
  per-agent mode). Either register a separate instance per identity (e.g. `acp-claudeA`,
  `acp-codexB`) or set the right env on each machine.
- **Per space / per service:** to put an agent in a specific isolated space (or to wire
  it to several independent ACP services at once), add `--env ACP_SPACE=<space>` and
  register one instance per (server, space, agent), e.g. `acp-projX`, `acp-projY`. Each
  is its own process with its own env → fully isolated, no client-side clash.
- **Secrets:** `--env ACP_TOKEN=…` stores the token in easymcp's instance config —
  protect that config like any secret. (The `--token-env-var` / auth-mode flags target
  `remote_http` instances; for a local stdio process, env is the straightforward path.)

## Verify, then install into the agent

```bash
easymcp check acp                              # connectivity check (spawns acp-mcp, lists tools)
easymcp ps                                     # see it registered
easymcp agent install claude-code acp --scope project   # or:  easymcp agent install codex acp
```

`agent install` writes the MCP server entry into the harness's config (project/local/user
scope). After it, the harness exposes the `acp_*` tools.

### Optional: install only a subset of tools (facets)

If you don't want all `acp_*` tools in a given agent, carve a facet and install that:

```bash
# define/inspect facets for the instance, then install the faceted address:
easymcp agent install claude-code acp:writer-tools --scope project
```

See the `easymcp-facets` skill for authoring facets.

## Day-2 / troubleshooting

| Command | Use |
|---------|-----|
| `easymcp check acp` | confirm acp-mcp launches and tools list (verifies env + coordd reachable) |
| `easymcp logs acp` | read the bridge's stderr (it prints `acp-mcp ready (agent=…)` on start) |
| `easymcp ps` / `easymcp inspect acp` | runtime state / definition |
| `easymcp reload` / `easymcp restart acp` | apply changed env/config |
| `easymcp rm acp` | remove the instance |

If `check` fails: confirm `coordd` is reachable from this host, `ACP_CERT` matches the
daemon's cert, the token is valid (and not the shared token while in per-agent mode), and
the `acp-mcp` path is correct. These map to the same 401/403/connection issues in the ops
runbook.

## Why use easymcp here at all (vs. hand-editing config)

You can register a stdio MCP server by hand, but going through easymcp gives you one
managed inventory, `check`/`logs`/`ps`, facet slicing, and consistent install across
Claude Code and Codex — the same controls you use for every other MCP connection.

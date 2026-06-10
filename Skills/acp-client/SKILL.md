---
name: acp-client
description: Use ACP (Agent Coordination Protocol) as a client/agent to collaborate with ANOTHER agent over a shared filesystem and comms line — connect to coordd, push/pull shared files, co-edit the same file with auto-merge (CRDT), send/receive messages, take leases, and follow the coordination event log. Use this skill whenever you (an agent) need to share files or coordinate with another agent/harness on a different machine, whenever you see ACP / coordd / `acp` CLI / a shared workspace / `ACP_SERVER`, or whenever the task is "work together with another Claude/Codex/agent" — even if ACP isn't named explicitly.
---

# Using ACP as a Client Agent

ACP gives two (or more) agent harnesses on different machines a **shared filesystem**
and a **comms line** through one coordination daemon (`coordd`). You are a *client*.
This skill is the playbook for getting work done through it.

Full design/reference lives one or two levels up:
`../../06_acp_protocol_design_2026-06-09.md`, `../../09_crdt_layer_2026-06-09.md`,
`../../acp/README.md`, `../../acp/docs/API_REFERENCE.md`.

## Mental model (read first)

- There is **one authority** (`coordd`). You and the other agent are clients; you
  never talk peer-to-peer — you rendezvous through the daemon, which gives a single
  consistent view (ordering, locks, audit).
- **Two ways to share files — pick per task:**
  - **File-sync** (`acp push`/`pull`): git-like, whole-file, any type. Different files
    → no conflict. Same file → 3-way auto-merge (markers on true overlap).
  - **CRDT docs** (`acp crdt sync`): two agents edit the *same* text file at once and it
    **auto-merges, no conflict**. Use for live co-authoring of one spec/source file.
- **Comms:** directed **mailbox** (`send`/`inbox`) + a totally-ordered **event log**
  (`log`/`watch`) that both agents derive shared state from.
- **Don't clobber:** take a **lease** on a hot file before editing it; commits to a
  leased path by anyone else are rejected.

## Setup (once per machine)

Set env (the operator gives you these — token + cert come from the daemon host):

```bash
export ACP_SERVER=https://<host>:8443
export ACP_CERT=/path/to/cert.pem      # pins the daemon; do NOT use ACP_INSECURE in prod
export ACP_TOKEN=<your token>
export ACP_AGENT=<your unique id>       # e.g. claudeA  (in per-agent mode, identity is the token's)
export ACP_HARNESS=claude-code         # optional, shows in `acp who`
acp health && acp beat && acp who      # confirm you can reach it and see peers
```

If you're an MCP-capable harness, prefer the **`acp-mcp`** server (tools:
`acp_send`, `acp_inbox`, `acp_doc_read/write`, `acp_lease`, ...) over shelling out.
`acp-mcp` *is* the MCP server; register it into your harness with `easymcp` — see
`../acp-operations/references/connect-acp-to-easymcp.md`.

## The standard agent loop

Run this rhythm while collaborating (a few seconds between iterations, or drive it
from `acp watch`):

1. `acp beat` — announce you're alive.
2. `acp pull ./workspace` — get the latest shared files.
3. `acp inbox --unread` → `acp read <id>` — handle messages (read = marks acked).
4. `acp watch --from <lastSeq>` (background) — react to the other agent's events
   (`file.edited`, `lease.acquired`, `chat.sent`, `task.*`, `crdt.op`).
5. Do work:
   - editing a *shared/hot* file: `acp lease acquire file:<path> --ttl 600`, edit,
     `acp push ./workspace`, then `acp lease release file:<path> <token>`.
   - live co-authoring one file: `acp crdt sync <doc> <localfile>` (repeat as you edit).
6. `acp log --action <ns.action> --entity <path> --note "..."` — broadcast what you did.
7. `acp send --to <agent> --subject ... --body ...` — ask questions / hand off.

## Best practices (do these)

- **Pull before you push.** Push auto-rebases on version races but a fresh pull avoids
  surprises and lets the 3-way merge work from the right base.
- **Own your lane.** Agree on a file/dir split so you mostly edit *different* files —
  the cheapest way to avoid conflicts. Use leases only for genuinely shared/hot files.
- **Lease then edit then release.** Keep TTLs short; renew (`acp lease renew`) if you
  need longer. A crashed holder's lease auto-expires (fencing tokens keep it safe).
- **Talk through the log, not just mail.** `acp log` is the shared memory both agents
  replay; use clear namespaced actions (`spec.section.done`, `build.green`).
- **For same-file live editing, use `crdt sync`, not push/pull.** It never conflicts.
- **Resolve file-sync conflicts:** on `pull`, non-overlapping edits merge automatically;
  on overlap you get `<<<<<<< local / ======= / >>>>>>> remote` markers in the file —
  edit them out, then `acp push`.
- **Idempotency for retries:** when scripting events/mail, set an `Idempotency-Key`
  header (via the SDK) so a network retry doesn't double-send.

## Common tasks → commands

| Goal | Command |
|------|---------|
| See who's online | `acp who` |
| Get latest files | `acp pull ./ws` |
| Share my changes | `acp push ./ws` |
| Delete a shared file | `acp rm path` (or delete locally + `acp push`) |
| Co-author one file live | `acp crdt sync spec.md ./spec.md` / `acp crdt watch spec.md ./spec.md` |
| Message an agent | `acp send --to codexB --subject hi --body "..."` |
| Read my mail | `acp inbox --unread` then `acp read <id>` |
| Claim a file | `acp lease acquire file:app.go --ttl 600` |
| Broadcast a fact | `acp log --action build.green --entity ci` |
| Follow activity | `acp watch --from 1` |
| Health/counters | `acp health` / `acp stats` |

## Pitfalls

- Two agents editing the **same** file via `push`/`pull` get a conflict — switch that
  file to `crdt sync`, or lease it.
- `ACP_AGENT` only sets your label in shared-token mode; in per-agent mode your identity
  is fixed by your token (you can't impersonate another agent).
- A `423` on push means another agent holds a lease on that path — wait or coordinate
  via mail. A `409` is a version race (push rebases automatically).

---
name: acp-client
description: Use ACP (Agent Coordination Protocol) as a client/agent to collaborate with ANOTHER agent over a shared filesystem and comms line — connect to coordd (by `ACP_SERVER` or an `acp://` address), probe capabilities, push/pull shared files (STALE vs CONFLICT, --force), co-edit a text file live with the CRDT, edit a structured JSON document (ext-5 crdtjson: set/del/lins/ldel), send/receive messages, take leases, follow the coordination event log, address/share/mount a resource with the `acp://` URI scheme (ext-32: `acp uri`, `ACP_URI`/`--uri`, signed link-sharing, `.well-known/acp` discovery, mount), and self-update the CLI. Use this skill whenever you (an agent) need to share files or coordinate with another agent/harness on a different machine, whenever you see ACP / coordd / `acp` CLI / a shared workspace / `ACP_SERVER` / `ACP_URI` / `acp://` / `crdtjson` / `lins`/`ldel` / capability negotiation / mount / `acp update`, or whenever the task is "work together with another Claude/Codex/agent" — even if ACP isn't named explicitly.
---

# Using ACP as a Client Agent

ACP gives two (or more) agent harnesses on different machines a **shared filesystem**
and a **comms line** through one coordination daemon (`coordd`). You are a *client*.
This skill is the playbook for getting work done through it.

Full spec/reference: `../../rfc/acp-1.txt` (the base protocol — the filesystem model is
§11 blobs+manifest, the text CRDT §12, leases §10, the event log §8),
`../../rfc/acp-ext-5-structured-crdt.txt` (the structured-CRDT spec).

## Mental model (read first)

- There is **one authority** (`coordd`). You and the other agent are clients; you
  never talk peer-to-peer — you rendezvous through the daemon, which gives a single
  consistent view (ordering, locks, audit).
- **Three data planes — pick per task** (they compose):
  - **Shared filesystem** (`acp push`/`pull`): git-like, content-addressed, whole-file,
    any type, CAS-versioned manifest. Different files → no conflict. Same file → 3-way
    merge; a true overlap needs a human/agent decision (see "File-sync" below — this is
    **not** silent auto-merge on overlap).
  - **CRDT docs** (`acp crdt sync` for text, `acp crdt json` for structured JSON,
    ext-5/`crdtjson`): two agents edit the *same* document at once and it **converges
    with no conflict step** — but structured JSON has an array-op discipline you must
    follow (see below) or you *will* silently lose concurrent edits.
  - **Comms & coordination:** directed **mailbox** (`send`/`inbox`) + a totally-ordered
    **event log** (`log`/`watch`, filterable by **channel**, ext-1) that both agents
    derive shared state from; fencing-token **leases** for a mutex.
- **Capability-negotiated:** `channels`, `crdtjson`, `scopedtokens`, `quotas` are always
  on; `batchevents` and `bloburl` depend on daemon config. **Probe before you rely on
  one** (see "Capability negotiation" below) — don't assume every daemon has every
  extension.
- **Your token may be scoped.** `GET /v1/whoami` (`acp whoami`) shows your own
  `{role, scope?}`: `path_prefix` = the subtrees you may WRITE (else 403);
  `read_prefix` (ext-27, daemons advertising `scopedtokens.read`) = the slice of the
  TREE you may READ — anything outside it looks genuinely missing (a filtered manifest,
  404 blob, empty doc), never a 403, so don't debug "missing" files that are simply out
  of your slice. Events, mail, presence and stats stay space-wide. If a read-scoped
  commit is refused `blob … not uploaded` for bytes you just uploaded, re-PUT them and
  retry (`acp push` does this by itself); a commit with more than 524,288 file
  references must be split.
- **Don't clobber:** take a **lease** on a hot file before editing it; commits to a
  leased path by anyone else are rejected (423).

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

## Capability negotiation

The `acp` CLI has **no verb that prints the capability list** — `acp health` just prints
`ok`. To know what a daemon supports, hit `/v1/healthz` directly (or read the SDK's error
text, which names the missing capability):

```bash
curl -sk -H "Authorization: Bearer $ACP_TOKEN" "$ACP_SERVER/v1/healthz"
# {"status":"ok","protocol":"acp/1","capabilities":["batchevents","channels","crdtjson",
#  "quotas","scopedtokens"],"limits":{"max_batch_events":256}}
```

`channels`, `crdtjson`, `quotas`, `scopedtokens` are unconditional on any current daemon.
`batchevents` only appears if the operator left batch append enabled (`-max-batch>0`);
`bloburl` only appears if a blob-capability signing key is provisioned. If you drive ACP
through `pkg/client` (Go), you don't need to probe yourself — every capability-gated SDK
method (`PushCRDTJSONOps`, `LogBatch`, `MintBlobURL`, …) probes `/v1/healthz` itself
(cached after the first hit) and fails with an explicit "capability not advertised" error
rather than silently degrading. Probe explicitly when scripting the CLI/HTTP directly
against a daemon whose version you don't control.

## Structured CRDT (ext-5) — the array-op discipline (read before writing arrays)

`acp crdt json` gives you a live-converging **JSON document** (a board, form, config —
anything structured), materialized server-side from five commuting ops:

| op | meaning |
|---|---|
| `set` | bind a map key / write a scalar **in place** (LWW, leader-stamped — converges fine for scalar fields) |
| `del` | observed-remove a map key |
| `lins` | **insert** a list element at a live index (carries element identity) |
| `ldel` | tombstone a list element by live index |
| `mv` | **relocate** an existing node (subtree or array element) **preserving its identity** — ext-14, capability `crdtjson-move`. `{"t":"mv","from":[src…],"path":[dst…]}` |

**Footgun 1 (found by dogfooding, 2026-07-17): for arrays, use `lins`/`ldel` — never
add/remove an array element via a whole-array `set`.** A whole-array `set` is a
read-modify-write: two concurrent writers both get `200`, and the later `set` **silently
clobbers** the other's element change. `lins`/`ldel` carry element identity and converge
conflict-free — that's the entire point of the CRDT.

**Footgun 2 (fixed by ext-14): to MOVE — reorder or relocate — use `mv`, never
delete+reinsert.** `mv` keeps the moved node's identity, so a peer's concurrent edit
**follows it** instead of being lost; concurrent moves collapse to one winner (no
duplicate); a cycle-forming move is ignored (stays put, nothing vanishes). Feature-detect
`crdtjson-move` in `/v1/healthz`. Delete+reinsert (the old way) duplicated the subtree and
dropped concurrent edits. Full array/path discipline (path-as-array vs slash-string;
`create_intermediate` only vivifies maps, so seed a new array as a container literal): the
structured-CRDT spec `../../rfc/acp-ext-5-structured-crdt.txt`.

**The CLI's structured-doc surface is `get`/`set`/`del`/`mv`/`list` — no `lins`/`ldel` verb:**

```bash
acp crdt json get board                          # daemon-materialized JSON
acp crdt json set board status '"green"'         # scalar field — fine via CLI (quote strings)
acp crdt json del board obsoleteField
acp crdt json mv board doing.0 done.-            # MOVE (ext-14): reorder/relocate, identity-preserving
```

To insert/delete an **array element** safely you need `lins`/`ldel`, which the CLI doesn't
expose — go through `pkg/client.PushCRDTJSONOps` (Go) or `POST /v1/crdt/json/ops` directly
with `{"t":"lins","path":["board","todo"],"idx":0,"value":{...}}` /
`{"t":"ldel","path":["board","todo"],"idx":0}`. If your write path already speaks RFC 6902
JSON-Patch (e.g. a liquid-UI-style `node.patch`/`data.patch` frame), `pkg/client/jsonpatch.go`
(`JSONPatchToOps`/`JSONPatchToOpsWithDoc`) maps it onto the right `set`/`del`/`lins`/`ldel`/`mv`
ops for you — including array vs object disambiguation and RFC-6902 `move` → a real identity-
preserving `mv` — instead of hand-computing them.

Text CRDT (`acp crdt sync`) and structured JSON CRDT (`acp crdt json`) **coexist** — pick
text for prose/code, JSON for structured state (dashboards, forms, config).

## File-sync: the push/pull/merge model (STALE vs CONFLICT)

`acp push`/`acp pull` share a content-addressed, CAS-versioned manifest (git-like). The
mechanics that matter in practice — the base protocol §11 (`../../rfc/acp-1.txt`) has the
full model:

- **On `pull`:** non-overlapping edits (base→local and base→remote touch different
  regions) merge **automatically**, reported as `merged <path> (clean 3-way)`. A genuine
  overlap is a **CONFLICT**: the client writes `<<<<<<< local / ======= / >>>>>>> remote`
  markers **in place** into the file (no `.remote` sibling in this case — that sibling
  file is only written when there's no common-ancestor blob to diff against at all, e.g.
  the file was created independently on both sides).
- **On `push`:** if the remote moved since your base for a path you touched — even a
  version race with no real content overlap — the client **blocks and labels it `STALE
  (run 'acp pull' to reconcile)`**, distinct from `CONFLICT`. In practice this is
  routine: a follow-up `acp pull` resolves most STALE cases silently (no decision
  needed); only a true marker overlap (`CONFLICT`) needs you to actually resolve text.
- **Resolving a CONFLICT is not "edit, then plain `push`."** The base entry for that path
  stays pinned to its pre-conflict hash until resolved, so a plain `acp push` bounces
  again with the same conflict — even after you've hand-edited the markers away. You
  must **`acp push --force`** after resolving (this only overrides the stale-base check
  for paths you touched; it does not blindly clobber other agents' unrelated changes).
  Don't `acp pull` again mid-resolution — it regenerates fresh markers around your
  partial edit; finish resolving in one pass, then force-push.

## Addressing: the `acp://` URI scheme (ext-32)

`acp://host[:port]/space/path` is ACP's **address** — the "URL of the shared filesystem." It resolves
CLIENT-side to the plain wire (`https://host:port/v1/…` + an `X-ACP-Space` header); it adds no new operation,
it just NAMES a resource. Shape: port-less means `:8443`; the **space is the first segment** (the hard
boundary); a trailing `/` names a collection (directory); `?v=N` pins a manifest version; `#fragment` is
client-only.

- **THE IRONCLAD RULE: a token NEVER rides in the URL** — not as `user@host`, not as `?token=…`. Such an
  address is refused *before any request*. Auth comes from your local profile (matched by the address's
  host:port) or, for a shared file, from a signed capability in the link (below). This is by design: URLs get
  pasted, logged, and stored — a bearer token must not.

- **One-string connect (CLI + MCP).** Instead of `ACP_SERVER` + space + token, point a tool at one address:
  ```bash
  export ACP_URI=acp://coordd.example/team/     # or: acp --uri acp://coordd.example/team/ who
  # acp-mcp:  claude mcp add acp -- env ACP_PROFILE=me ACP_URI=acp://coordd.example/team/ /path/to/acp-mcp
  ```
  A **connection address is the space root only** (`acp://host[:port]/space/`) — a file path, a `?v=` pin, or a
  signed link is refused (never silently connected to the whole space). The credential is your profile for that
  host:port; the space comes from the address.

- **The `acp uri` tool** (pure address math — no live connection except `read`/`discover`):
  ```bash
  acp uri parse     acp://Host/team/docs/spec.md     # structured breakdown
  acp uri canonical acp://Host:8443/team/docs/       # normalize (lowercase host, elide :8443)
  acp uri resolve   acp://host/team/docs/spec.md     # show the exact wire mapping it reduces to
  acp uri read      "<signed acp:// file link>" [--out f]   # one tokenless GET of a shared file
  acp uri discover  acp://host/                       # bare-host bootstrap (see Discovery)
  ```

- **Share a file as a link (no login).** For handing a specific file to someone without a profile, mint a
  **signed, scoped, expiring capability link** (needs a daemon advertising `bloburl`): the `acp://…?h=<hash>&sp&e&k&s`
  form carries a capability (not a token) that resolves to a tokenless one-shot fetch. `acp uri link
  <blob-capability-url> <path>` builds one; `acp uri read` (or any resolver) fetches it. The link is
  **version-pinned** — it names the exact bytes you shared; edit the file and re-mint. (A *living*, path-signed
  link that tracks edits is planned, not shipped.)

- **Discovery (`.well-known/acp`).** A bare `acp://host/` bootstraps from `GET https://host/.well-known/acp`
  (unauthenticated, read-only) — endpoint + capabilities + an advisory cert-fingerprint pin. Fail-closed: the
  endpoint host must match, no redirect is followed, a bad pin never *lowers* TLS trust. A daemon that doesn't
  serve it returns `discovery_unsupported` (older daemons); check with `acp uri discover`.

- **Mount a space as a filesystem (read path).** The TS SDK's `mountVirtualFS({uri})` exposes a space/prefix as
  `readdir`/`readFile`/`stat` over the frozen wire (read-only P0; a `read_prefix`-scoped token mounts only its
  slice). The native `acp mount acp://host/space/prefix /mnt/x` (FUSE) and the write path are the Mountable-ACP
  epic — gated, not yet shipped. Use push/pull (above) for writing today.

## The standard agent loop

Run this rhythm while collaborating (a few seconds between iterations, or drive it
from `acp watch`):

1. `acp beat` — announce you're alive.
2. `acp pull ./workspace` — get the latest shared files.
3. `acp inbox --unread` → `acp read <id>` — handle messages (read = marks acked).
4. `acp watch --from <lastSeq>` (background) — react to the other agent's events
   (`file.edited`, `lease.acquired`, `chat.sent`, `task.*`, `crdt.op`); add
   `--channels a,b.*` / `--actions x,y.*` to scope the stream to a topic (ext-1
   wildcards are prefix-only — a literal or a trailing `.*`, no mid-string globs).
5. Do work:
   - editing a *shared/hot* file: `acp lease acquire file:<path> --ttl 600`, edit,
     `acp push ./workspace`, then `acp lease release file:<path> <token>`.
   - live co-authoring one text file: `acp crdt sync <doc> <localfile>` (repeat as you edit).
   - live co-editing one structured document: `acp crdt json set/del <doc> <path> <json>`
     (use `pkg/client`/the HTTP API for `lins`/`ldel` — see "Structured CRDT" above).
6. `acp log --action <ns.action> --entity <path> --note "..."` — broadcast what you did.
7. `acp send --to <agent> --subject ... --body ...` — ask questions / hand off.

## `acp update` — self-update the CLI

`acp update [--check] [--force] [--version vX.Y.Z]` checks the release channel's
`latest.txt`, compares to your running build, downloads the matching platform tarball,
**verifies it against `checksums.txt`** (fail-closed — a checksum mismatch aborts), and
atomically swaps the `acp`/`coordd` binaries in place (never introduces `acp-mcp` if you
didn't already have it). `--check` reports current vs. latest without downloading.
Releases are PATCH-only and append-only, so `--version vX.Y.Z` can always pin/reinstall
an older retained release, not just the newest.

## Best practices (do these)

- **Pull before you push.** Push auto-rebases on version races but a fresh pull avoids
  surprises and lets the 3-way merge work from the right base.
- **Own your lane.** Agree on a file/dir split so you mostly edit *different* files —
  the cheapest way to avoid conflicts. Use leases only for genuinely shared/hot files.
- **Lease then edit then release.** Keep TTLs short; renew (`acp lease renew`) if you
  need longer. A crashed holder's lease auto-expires (fencing tokens keep it safe).
- **Talk through the log, not just mail.** `acp log` is the shared memory both agents
  replay; use clear namespaced actions (`spec.section.done`, `build.green`).
- **For same-file live editing, use `crdt sync`/`crdt json`, not push/pull.** They
  converge with no conflict step (structured JSON still needs the `lins`/`ldel`
  discipline for arrays — see above).
- **A CONFLICT needs `push --force` after you resolve the markers** — a plain `push`
  bounces again by design (see "File-sync" above). A STALE push usually just needs a
  plain `acp pull` first.
- **Idempotency for retries:** when scripting events/mail, set an `Idempotency-Key`
  header (via the SDK) so a network retry doesn't double-send.

## Common tasks → commands

| Goal | Command |
|------|---------|
| See who's online | `acp who` |
| Check what a daemon supports | `curl -sk -H "Authorization: Bearer $ACP_TOKEN" $ACP_SERVER/v1/healthz` |
| Get latest files | `acp pull ./ws` |
| Share my changes | `acp push ./ws` (after resolving a CONFLICT: `acp push --force`) |
| Delete a shared file | `acp rm path` (or delete locally + `acp push`) |
| Co-author one text file live | `acp crdt sync spec.md ./spec.md` / `acp crdt watch spec.md ./spec.md` |
| Read/write a structured doc | `acp crdt json get board` / `acp crdt json set board status '"green"'` |
| Insert/delete an array element | `pkg/client.PushCRDTJSONOps` with `lins`/`ldel` (no CLI verb) |
| Message an agent | `acp send --to codexB --subject hi --body "..."` |
| Read my mail | `acp inbox --unread` then `acp read <id>` |
| Claim a file | `acp lease acquire file:app.go --ttl 600` |
| Broadcast a fact | `acp log --action build.green --entity ci` |
| Follow activity (scoped) | `acp watch --from 1 --channels a,b.* --actions x,y.*` |
| Health/counters | `acp health` / `acp stats` |
| Connect by one address | `export ACP_URI=acp://host/space/` (or `acp --uri acp://host/space/ <cmd>`) |
| Inspect / normalize an address | `acp uri parse\|canonical\|resolve acp://host/space/path` |
| Share a file as a link (no login) | `acp uri link <blob-capability-url> <path>` → send it; recipient `acp uri read "<link>"` |
| Bootstrap from a bare host | `acp uri discover acp://host/` |
| Update the CLI | `acp update --check` / `acp update` |

## Pitfalls

- Two agents editing the **same** file via `push`/`pull` on a true overlap get a
  `CONFLICT` (in-place markers) — switch that file to `crdt sync`, or lease it, to avoid
  the resolution step entirely.
- **Whole-array `set` on a structured CRDT doc silently drops a concurrent element
  change** — always use `lins`/`ldel` for array structure, `set` only for scalar fields.
- After resolving a CONFLICT's markers, a plain `acp push` bounces again — you need
  `acp push --force`; don't `acp pull` again mid-resolution (it regenerates markers).
- `ACP_AGENT` only sets your label in shared-token mode; in per-agent mode your identity
  is fixed by your token (you can't impersonate another agent).
- A `423` on push means another agent holds a lease on that path — wait or coordinate
  via mail. A `409` is a version race (push auto-retries/rebases); a `STALE` label on
  push means "pull first," not "blocked forever."
- Don't assume every daemon has every extension — `batchevents`/`bloburl` are
  conditionally advertised; probe `/v1/healthz` rather than trial-and-error.

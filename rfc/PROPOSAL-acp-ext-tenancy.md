# PROPOSAL (draft) — ACP extension: sub-tenancy, scoped tokens, and per-space quotas

> **Status: DRAFT PROPOSAL (unapproved, external).** Contributed by a downstream ACP consumer (Living City).
> A candidate `acp-ext-N` for the protocol owner's review — NOT a private change. Numbers are provisional.
> Companion to the [[reference_acp_protocol_and_process]] rules and `DECISION-acp-is-core-comms.md`.

## Why (the gap)
ACP **spaces** (`acp-1` §7) give *company-level* multi-tenancy: hard isolation of events/mail/leases/blobs/
manifest/presence — "spaces share nothing." That is sufficient for **one space per company** (see
`tickets/acp-scale-integration/ticket.md`). But a real deployment needs more *inside* a space:
- **Sub-tenancy** — many projects/pods within one company, wanting soft isolation without a whole new space.
- **Scoped tokens** — an agent/pod token that is **read-only**, or **actor-scoped**, or **path-prefixed**
  (can only touch `projects/<id>/…`), rather than a single shared writer token per space.
- **Per-space quotas** — bound event-log growth, blob bytes, lease counts, mail fan-out per space, so one
  tenant can't exhaust the daemon.

Today the downstream engine enforces these by **broker convention** (it is the tenancy authority: it maps
company/project → space, path-prefixes the manifest `projects/<id>/`, sets `context.project` on events,
prefixes lease names, and applies quota/budget as engine policy). That works, but it is **engine-side only**
— the protocol itself can't enforce sub-tenancy, so a leaked space token has full-space power.

## What this proposes (additive, capability-negotiated)
1. **Scoped tokens** — token grants carry optional scope: `role` (reader/writer/admin, already in §5) PLUS
   `path_prefix` (manifest/lease/channel names must start with it), `actor` (fixed actor id), and `read_only`.
   A scoped token cannot exceed its grant; the daemon rejects out-of-scope ops (`403`).
2. **Per-space quotas** — optional daemon config / admin API per space: `max_events`, `max_blob_bytes`,
   `max_leases`, `max_fanout`; over-quota → `429`/`507`. Advertised in `/v1/stats`.
3. **Optional named sub-scopes** — a lightweight `subspace`/`project` label (like `acp-ext-1` channels are to
   the event log) that scoped tokens + quotas can key on, WITHOUT the full cost of a separate space.

All additive: absent → today's behavior; negotiated via a `tenancy` capability string (per `acp-ext-1` §7);
wire stays `acp/1`. Non-goals: this does not replace spaces (spaces remain the hard boundary); it adds soft
sub-tenancy + least-privilege tokens + quotas *within* a space.

## Relationship to our system
The engine stays the **policy authority** (it decides quotas/mappings); this extension lets ACP **enforce**
least-privilege at the protocol edge (defense-in-depth), so a distributed pod holds only a path-prefixed,
read-scoped token for its project — not a full-space writer token. Recommended only if the owner sees it as a
**general** gap; otherwise we keep enforcing it broker-side. To be written up as a full `acp-ext-N` RFC (in
the `rfc/` format of `acp-ext-1..6`) if the owner is interested.

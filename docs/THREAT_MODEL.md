# ACP Threat Model

Scope: the `coordd` daemon + `acp`/`pkg/client` over an untrusted network. Assumes the
daemon host itself is trusted (state is plaintext on its disk).

## Assets
- Shared files (blobs + manifest), collaborative docs, mailbox, event log/audit trail.
- Credentials: the bearer token(s) and the TLS private key (`key.pem`).

## Trust assumptions
- Agents are **cooperative but each authenticated separately** (per-agent mode).
- The daemon host and its disk are trusted; operators are trusted.
- The network between agents and daemon is **untrusted**.

## Adversaries & mitigations

| Threat | Mitigation | Status |
|--------|-----------|--------|
| **Eavesdropping / MITM** on the wire | TLS 1.2+; clients **pin** the daemon cert (`ACP_CERT`) as their only root — no `InsecureSkipVerify` in prod | ✅ |
| **Unauthorized access** | Bearer token required on all non-health endpoints; 192-bit random; constant-time value compare | ✅ |
| **Identity spoofing** (claiming to be another agent) | In per-agent mode, identity is derived from the **token**, not the client-supplied header; actor/from forced server-side | ✅ |
| **Path traversal** (write outside a workspace) | `validPath` rejects absolute/`..`/non-canonical paths at the authority; CRDT doc names validated too | ✅ (tested) |
| **Lost updates / clobbering** | CAS-versioned commits; **fencing-token** leases; optional lease-enforced commits (423) | ✅ (tested) |
| **Replay / duplicate writes** (network retry) | `Idempotency-Key` on events & mail; CRDT ops idempotent by ID | ✅ |
| **Resource exhaustion / DoS** | `-max-body` cap (MaxBytesReader); per-agent token-bucket rate limit (429); blob GC | ✅ (basic) |
| **Stale-lock corruption** (holder stalls past TTL) | Fencing tokens: a resumed stale holder always has a lower token and is rejected | ✅ (tested) |
| **Credential leakage** | Keep `token`/`key.pem`/`agents.json` off the shared FS; `key.pem`/`agents.json` are `0600` | ⚠️ operator duty |

## Cluster mode (HA) — additional surface
- **Intra-cluster traffic:** with a **cluster CA** (`coordd -init-cluster-ca`), the Raft
  transport (which carries all replicated data) is **mutually authenticated + encrypted
  (mTLS)** — T22, done. The inter-node HTTP forward/join channel stays TLS + bearer
  token. Without the CA, the raft transport is plain TCP — run nodes on a trusted
  private subnet.
- **Encryption at rest:** blob (file) contents are **AES-256-GCM encrypted** when
  `ACP_DATA_KEY`/`-data-key-file` is set (T22). Keyed by plaintext hash (dedup intact);
  wrong key fails closed. Metadata (event/mailbox/lease/manifest/raft logs) is not yet
  app-encrypted — put `-data` on an encrypted volume (LUKS) for full coverage.
- **Blob bytes ride the Raft log** (size-capped) — replicated to every node; large-file
  object-store offload is roadmap (T20).
- **Quorum:** correctness needs an odd node count; HA tolerates a minority loss. No
  split-brain with a correct configuration.

## Known gaps (roadmap)
- **Per-resource ACLs/roles** — addressed: roles admin/writer/reader (T14). Per-path
  ACLs still open.
- **Encryption at rest** — blobs addressed (AES-256-GCM, T22); metadata/raft logs rely
  on volume encryption (LUKS). Full app-layer metadata encryption is future work.
- **HA + node-to-node auth** — addressed: Raft cluster (T15) + mTLS (T22). A single
  compromised *node* still sees its own space data.
- **Audit log not tamper-evident** — hash-chaining is roadmap.
- **Audit is append-only but not tamper-evident** — a host-level attacker could edit
  JSONL. (Future: hash-chain the event log.)
- **Rate limiting is per-agent in-memory** — resets on restart; not a hard quota.
- **Mailbox/event history readable by any authenticated agent** — fine under the
  cooperative assumption; not for multi-tenant.

## Recommendations for sensitive deployments
1. Use **per-agent tokens** (`-mkagent`) and rotate/revoke via `agents.json`.
2. Run the daemon on a hardened, access-controlled host; restrict who can read `-data`.
3. Consider mTLS client certs (extension point) in addition to tokens.
4. Add the roadmap items (ACLs, encryption at rest, hash-chained audit) before
   multi-tenant or low-trust use.

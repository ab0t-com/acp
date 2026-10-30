# ACP Threat Model

Scope: the `coordd` daemon + `acp`/`pkg/client` over an untrusted network. Assumes the
daemon host itself is trusted (state is plaintext on its disk, apart from file contents when
encryption at rest is on).

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
| **Eavesdropping / MITM** on the wire | TLS 1.2+; clients **pin** the self-signed daemon cert (`pin=sha256:…`, which an invite carries, or a `cert=` file) as their only root, or validate a public certificate against the system store when the daemon sits behind a TLS proxy — no `InsecureSkipVerify` in prod | ✅ |
| **Unauthorized access** | Bearer token required on all non-health endpoints; 192-bit random; constant-time value compare | ✅ |
| **Identity spoofing** (claiming to be another agent) | In per-agent mode, identity is derived from the **token**, not the client-supplied header; actor/from forced server-side | ✅ |
| **Path traversal** (write outside a workspace) | `validPath` rejects absolute/`..`/non-canonical paths at the authority; CRDT doc names validated too | ✅ (tested) |
| **Lost updates / clobbering** | CAS-versioned commits; **fencing-token** leases; optional lease-enforced commits (423) | ✅ (tested) |
| **Replay / duplicate writes** (network retry) | `Idempotency-Key` on events & mail; CRDT ops idempotent by ID | ✅ |
| **Resource exhaustion / DoS** | `-max-body` cap (MaxBytesReader); per-agent token-bucket rate limit (429); blob GC | ✅ (basic) |
| **Stale-lock corruption** (holder stalls past TTL) | Fencing tokens: a resumed stale holder always has a lower token and is rejected | ✅ (tested) |
| **Credential leakage** | Keep `token`/`key.pem`/`agents.json` off the shared FS; `key.pem`/`agents.json` are `0600` | ⚠️ operator duty |
| **Leaked / over-broad writer token** (full-space write power) | Path-prefix **scoped tokens**: a grant may confine commit paths + lease names to listed folder subtrees (matched on whole path segments), 403 outside; fail-closed grants file (below) | ✅ (tested) |
| **Noisy/hostile co-tenant exhausts shared state** | Per-space **quotas**: `max_events`/`max_blob_bytes` → 507, `max_leases` → 429; replicated config | ✅ (tested) |

## Blob capability URLs (`-cap-key-file`) — additional surface

Enabling `bloburl` opens ACP's **only unauthenticated data path**: `GET /v1/cap/blob/…`
(exactly the `/v1/cap/` prefix is auth-exempt; the exemption is matched on the cleaned
path, so traversal cannot widen it — pinned by tests).

- **A capability URL IS a bearer secret for one blob.** Anyone holding it (browser
  history, proxy log, referrer, screenshot) reads that blob until expiry. Bounded by:
  one blob, one space (signature-bound — a header cannot repoint it), read-only,
  TTL ≤ `-cap-max-ttl` (default 15 min, mint default 5 min). Mint per use, deliver out
  of band, keep TTLs short.
- **Forgery:** HMAC-SHA256 with a 256-bit operator key; constant-time compare
  (`crypto/hmac.Equal` is the only comparison on the path); domain-separated canonical
  string (`acp-blobcap-v1`); signature checked BEFORE blob existence, so
  unauthenticated callers get no existence oracle and 403s carry no information.
- **Our own logs:** the request logger redacts `e=`/`s=` on `/v1/cap/` lines (a logged
  URL is a leaked capability); the mint audit event carries expiry + key id, never the
  signature. Test-enforced.
- **Key compromise blast radius:** the signing key forges read URLs for any *known*
  hash in any space. Mitigations: provision/store like a bearer token, rotate via the
  key-id list ([OPERATIONS.md](OPERATIONS.md)) — retiring a key instantly kills its URLs; hashes are
  sha256 of content, so an attacker must already know what to steal.
- **Abuse:** per-IP token bucket on the fetch path (`-cap-rate`, 429 + Retry-After),
  cheap failures (≤ one HMAC), bounded query parsing. Replay within TTL is BY DESIGN
  (it is a capability); there is no per-URL revocation in this cut.
- **Encrypted-at-rest note:** a capability fetch returns **plaintext** (like any
  authorized read). If that is unacceptable for a space, don't mint URLs for it.

## Cluster mode (beta) — additional surface
- **Intra-cluster traffic:** with a **cluster CA** (`coordd -init-cluster-ca`), the Raft
  transport (which carries all replicated data) is **mutually authenticated + encrypted
  (mTLS)**. The inter-node HTTP forward/join channel is **also cluster-CA
  mutually authenticated** when a CA is present (SNI-routed dual cert: external clients
  keep the pinned self-signed cert untouched, node peers get a CA cert +
  `RequireAndVerifyClientCert`), so the forwarded bearer token is not exposed to a
  node-network MITM. Without the CA, both channels fall back to the trusted-private-subnet
  model and the daemon warns loudly at startup.
- **Replica divergence as a security property:** an apply that consults anything outside
  replicated state — wall-clock, iteration order, or **node-local storage** — silently
  forks replicas. Lease expiry, blob GC, compaction, and commit validation are all written
  to depend only on the command and replicated state. Notably, a commit's `hash` is
  validated by SHAPE before it can reach the filesystem, so the outcome cannot depend on
  which files happen to exist on a given node.
- **Encryption at rest:** blob (file) contents are **AES-256-GCM encrypted** when
  `ACP_DATA_KEY`/`-data-key-file` is set. Keyed by plaintext hash (dedup intact);
  wrong key fails closed. Metadata (event/mailbox/lease/manifest/raft logs) is not yet
  app-encrypted — put `-data` on an encrypted volume (LUKS) for full coverage.
- **Blob bytes ride the Raft log** (size-capped) — replicated to every node; large-file
  object-store offload is not available yet.
- **Quorum:** correctness needs an odd node count; HA tolerates a minority loss. No
  split-brain with a correct configuration.

## Scoped tokens — least privilege WITHIN a space

Roles gate *endpoints*; a scope gates *write resource names* below the role: a grant
minted with `-path-prefix projects/42/` can only commit paths / take lease names
under that prefix (matched on whole path segments, 403 outside, whole commit refused). Threat-model
consequences:

- **A leaked scoped token is a bounded credential**, not full-space write power —
  the reason this exists. It still READS the whole space: scopes are least-privilege
  for writes, **not confidentiality**. The **space remains the ONLY hard isolation
  boundary**; confidential separation still requires a separate space.
- **Fail-closed by construction (the design's center):** scoped grants force
  `agents.json` into a versioned envelope that a pre-scope binary CANNOT parse — it
  refuses to start instead of silently loading the grant without its scope. A
  fail-open alternative (the minter checks the daemon's capability first) was
  rejected: the realistic failure is version skew in one cluster (a rollback or
  un-upgraded follower wins an election), and a security control must not silently
  un-enforce on failover. The startup error IS the guarantee; the price is the
  documented upgrade order ([OPERATIONS.md](OPERATIONS.md), section 2b-scopes).
- **Unspoofable:** the scope is derived from the token server-side and plumbed via
  request context, never a header; `GET /v1/whoami` echoes the grant but never the
  token. Traversal-shaped names cannot dodge or ride the prefix match: `..` paths
  are rejected as malformed input *before* any scope verdict, and traversal-shaped
  *prefixes* are rejected at mint and at load.
- **Residual risk:** a scoped token can still write name-less surfaces (events,
  mail, blob uploads — names bind at commit) and consume its space's quota
  headroom; a scoped **admin** keeps its admin endpoints (scope only narrows
  resource names). Prefixes match whole path segments and must end in `/`
  (`-mkagent` refuses a bare `projects/42`), so `projects/42/` never admits `projects/420/…`.

## Known gaps (roadmap)
- **Per-resource ACLs/roles** — addressed: roles admin/writer/reader +
  path-prefix write scopes and read scopes. Full per-path ACLs (per-path readers, grants
  per method) remain a named non-goal for now.
- **Encryption at rest** — blobs addressed (AES-256-GCM); metadata/raft logs rely
  on volume encryption (LUKS). Full app-layer metadata encryption is future work.
- **HA + node-to-node auth** — addressed: Raft cluster (beta) + mTLS between nodes. A single
  compromised *node* still sees its own space data.
- **Audit is append-only but not tamper-evident** — a host-level attacker could edit
  the log files. (Future: hash-chain the event log.)
- **Rate limiting is per-agent in-memory** — resets on restart; not a hard quota. Bucket
  maps are bounded by idle eviction.
- **The SDK treats the server as untrusted too.** `pkg/client` buffers at most 64 MiB of a
  control-plane response, so a hostile or compromised daemon cannot OOM an agent with one
  oversized reply; blob reads and event-follow are streamed rather than buffered. The CLI
  likewise refuses server-supplied paths that escape the checkout root. A compromised
  daemon can still lie about CONTENT — the guarantee is availability, not authenticity.
- **Shared-token mode has no agent-to-agent isolation** — `X-ACP-Agent` is trusted
  verbatim, so any token holder can act as any agent id (read/ack another agent's mail,
  hold its leases, author events under its name). The id is length-bounded (256 bytes) so
  it cannot be used as a memory amplifier, but it is *not* authenticated. Per-agent tokens
  (`-mkagent`) close this entirely.
- **Space is not a token-scoped boundary unless the grant says so** — a grant may carry a `spaces`
  allowlist (enforced fail-closed on every space-selecting surface, including admin routes
  that name a space out-of-band), but grants without one may select any space. Every grant an
  **invite** creates is limited to its one space; a grant minted with `-mkagent` needs `-spaces`
  to be. Treat spaces as an organisational boundary unless every token carries an allowlist.
- **Mailbox metadata and event history are readable by every agent that can read the space** —
  message subjects and the activity log are space-wide (a message body is only for its
  recipient). A path-prefix or read scope does NOT change this. **For several tenants on one
  daemon:** one space per tenant, and every tenant login limited to its space(s) (invites do
  this; `-mkagent` needs `-spaces`). An unscoped login can open any space.

## Recommendations for sensitive deployments
1. Use **per-agent logins** only (invites, or `-mkagent` with `-spaces`); no shared token. Revoke
   with `acp invites remove-member`, `DELETE /v1/admin/grants`, or `-rmagent` + SIGHUP — never by
   editing `agents.json`.
2. Run the daemon on a hardened, access-controlled host; restrict who can read `-data`.
3. Put `-data` on an encrypted volume and set `-data-key-file` for blob contents; keep the key
   outside `-data` and back it up separately.
4. One space per tenant or team, with space-limited logins and per-space quotas.
5. Off-machine backups of `-data` on a schedule (a cluster is not a backup).
6. Still not available — decide whether you can accept them: SSO / an external identity
   provider, a tamper-evident (hash-chained) audit log, per-path ACLs beyond scopes, app-layer
   encryption of metadata, and a GA cluster (cluster mode is beta).

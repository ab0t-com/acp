# Security Policy

## Reporting
Report vulnerabilities privately to security@ab0t.com (or via https://ab0t.com).
Please do not open public issues for security reports.

## Model (summary)
- Transport: TLS 1.2+; clients pin the daemon's self-signed cert (no public CA, no MITM window).
- AuthN: bearer token (constant-time compare). In per-agent-token mode the identity is derived from
  the token and is unspoofable; in shared-token mode the client-supplied `X-ACP-Agent` header is
  trusted as-is — use per-agent tokens for untrusted or multi-tenant deployments.
- AuthZ: roles admin/writer/reader.
- Isolation: per-space; path-traversal rejected at the authority.
- Concurrency safety: fencing-token leases + CAS commits + CRDT epoch barrier.
- HA: Raft cluster; **mTLS between nodes** (cluster CA); **encryption at rest** for blobs (AES-256-GCM).
- Run the daemon on a trusted host; put `-data` on an encrypted volume for full at-rest coverage.

Full details: docs/THREAT_MODEL.md.

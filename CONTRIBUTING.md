# Contributing

## Proposing a protocol extension

`acp/1` is the base wire protocol and does not change. New capabilities are proposed as an
**RFC** in [rfc/](rfc/):

- Write it as an RFC-2119 document (MUST/SHOULD/MAY) that is **additive** to `acp/1` — it
  must not change or remove existing request/response shapes, only add new ones.
- Gate the feature behind a new capability string that a daemon advertises in `/v1/healthz`'s
  `capabilities` array, so clients feature-detect instead of trial-and-erroring requests.
- Mark the document **PROPOSAL** in its "Status of This Memo" section until the protocol
  owner accepts it (and, once implemented, updates that section to say so).
- Use [rfc/acp-ext-4-batch-event-append.txt](rfc/acp-ext-4-batch-event-append.txt) as the
  format template: Abstract, wire-shape changes, capability name, and a "Status of This
  Memo" section.

Open a GitHub issue or PR with the RFC text before writing implementation code — extensions
are reviewed as protocol changes first, code second.

## Wire compatibility

`acp/1` is additive-only. A client or daemon that does not implement a given extension must
continue to interoperate, unmodified, with one that does. Anything that would break an
existing request/response shape is a **breaking change**, not an extension — it requires
bumping the protocol version, not silently altering `acp/1`.

## Before a PR is accepted

On Go >= 1.25.12, all of the following must be clean:

```bash
gofmt -l .
go build ./...
go vet ./...
go test -race ./...
```

PRs that don't pass this gate won't be merged.

## Releases

Releases are cut by maintainers only — contributors do not publish binaries or tarballs.
Versions are PATCH-only and append-only: a new release is the next patch, and no previously
published version is ever deleted or overwritten.

## Reporting issues

- Bugs and feature requests: open a GitHub issue on `ab0t-com/acp`.
- Security vulnerabilities: **do not** open a public issue — follow [SECURITY.md](SECURITY.md).

## Sign-off

Commits must include a Developer Certificate of Origin sign-off (`git commit -s`), certifying
you have the right to submit the change under this project's MIT license.

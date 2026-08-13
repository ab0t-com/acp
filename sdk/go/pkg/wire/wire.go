// Package wire is the PUBLIC, externally-consumable definition of the ACP
// (Agent Coordination Protocol) v1 on-the-wire types — the shared JSON
// vocabulary between coordd (the daemon) and every client.
//
// These are the types the reference Go SDK (github.com/ab0t-com/acp/sdk/go/pkg/client)
// accepts and returns. They are exported here so that a customer in their OWN Go
// module can construct arguments to, and read results from, the SDK. (Before
// ACP/EXT-23 the SDK's method signatures named types under internal/, which Go's
// import rules forbid any other module from naming — so the SDK could not be
// used outside this repository. This package closes that gap.)
//
// One source of truth: every identifier below is a Go type ALIAS (or a thin
// re-export) of the single canonical definition. There is no second copy to
// drift; pkg/wire.Event and the daemon's Event are the identical type.
//
// Stability: this surface tracks the frozen "acp/1" wire. Within a major
// version, identifiers are not removed or changed incompatibly; new OPTIONAL
// wire fields appear as new OPTIONAL struct fields (additive). Treat it as a
// committed public API.
//
// See rfc/acp-ext-23-external-client-packages.txt and PUBLIC_REPO/docs/API_REFERENCE.md.
package wire

import "github.com/ab0t-com/acp/sdk/go/internal/wire"

// Protocol version and the request headers a client sets. (The SDK sets these
// for you; they are exported for advanced/manual use.)
const (
	// ProtocolVersion is the frozen wire version string.
	ProtocolVersion = wire.ProtocolVersion
	// HeaderAgentID identifies the calling agent.
	HeaderAgentID = wire.HeaderAgentID
	// HeaderProtocol carries ProtocolVersion.
	HeaderProtocol = wire.HeaderProtocol
	// HeaderSpace selects the isolated shared space (the only hard boundary).
	HeaderSpace = wire.HeaderSpace
	// HeaderSession is a random per-client-process id (collision detection).
	HeaderSession = wire.HeaderSession
	// DefaultSpace is used when no space is selected.
	DefaultSpace = wire.DefaultSpace
	// UnlabeledPattern is the reserved channel-filter pattern matching ONLY
	// events with no channel label.
	UnlabeledPattern = wire.UnlabeledPattern
)

// Core coordination types.
type (
	// Event is one row in the totally-ordered, append-only coordination log.
	Event = wire.Event
	// Message is a directed mailbox message (agent -> agent).
	Message = wire.Message
	// Lease is a TTL-bounded advisory lock with a fencing token.
	Lease = wire.Lease
	// ManifestEntry is one path in the shared workspace.
	ManifestEntry = wire.ManifestEntry
	// Manifest is the versioned snapshot of the whole shared workspace.
	Manifest = wire.Manifest
	// Change is one path mutation in a manifest commit.
	Change = wire.Change
	// CommitRequest is an atomic, CAS-guarded manifest update.
	CommitRequest = wire.CommitRequest
	// Agent is presence/roster info.
	Agent = wire.Agent
	// ErrorResponse is the standard error body (Current is set on 409 conflicts).
	ErrorResponse = wire.ErrorResponse
)

// Awareness (ext-9) — the ephemeral per-(actor, session) presence tier.
type (
	// AwarenessSet is the POST /v1/awareness request body.
	AwarenessSet = wire.AwarenessSet
	// AwarenessEntry is one live awareness entry.
	AwarenessEntry = wire.AwarenessEntry
	// AwarenessSnapshot is the GET /v1/awareness response.
	AwarenessSnapshot = wire.AwarenessSnapshot
	// AwarenessDelta is one NDJSON line of the awareness follow stream.
	AwarenessDelta = wire.AwarenessDelta
	// AwarenessFrame is one text frame of the ext-9 §11.3 WebSocket binding.
	AwarenessFrame = wire.AwarenessFrame
)

// MatchPattern reports whether the ACP filter pattern pat matches s (a literal,
// or a trailing-".*" prefix over a dotted namespace). Exposed so client- and
// server-side channel/action filtering can never diverge.
func MatchPattern(pat, s string) bool { return wire.MatchPattern(pat, s) }

// ValidPattern reports whether pat is a well-formed channel/action filter
// pattern (nil = valid). Useful for validating a filter before sending it.
func ValidPattern(pat string) error { return wire.ValidPattern(pat) }

// ValidChannelName reports whether s is a legal channel name (nil = valid).
func ValidChannelName(s string) error { return wire.ValidChannelName(s) }

// ValidDocName reports whether name is a legal collaborative-document name
// (nil = valid).
func ValidDocName(name string) error { return wire.ValidDocName(name) }

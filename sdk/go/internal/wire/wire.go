// Package wire defines the on-the-wire types for ACP (Agent Coordination
// Protocol) v1 — the shared vocabulary between coordd (the daemon) and every
// client. Everything is JSON over HTTP/2+TLS; these structs are the schema.
//
// Design notes:
//   - Events carry a monotonic Seq assigned by the daemon. Seq gives a TOTAL
//     order across all agents — the backbone of consistency. Clients resume a
//     stream by remembering the last Seq they saw.
//   - Leases carry a fencing Token (monotonic). A holder presents the token on
//     every protected write; a stale holder (whose lease expired and was taken
//     by someone else) always has a lower token and is rejected. This is the
//     standard fix for the "lock expired but the old holder is still alive" bug.
//   - The shared filesystem is content-addressed: blobs are keyed by SHA-256,
//     and a versioned Manifest maps paths -> blob hashes. Commits are CAS
//     (compare-and-swap) on the manifest Version, so concurrent writers can't
//     silently clobber each other.
package wire

const (
	// ProtocolVersion is sent/checked so client and daemon can detect a mismatch.
	ProtocolVersion = "acp/1"

	// HeaderAgentID identifies the calling agent.
	HeaderAgentID = "X-ACP-Agent"
	// HeaderProtocol carries ProtocolVersion.
	HeaderProtocol = "X-ACP-Protocol"
	// HeaderSpace selects the isolated shared space (filesystem + channels) on the
	// daemon. Empty/absent = the "default" space. A daemon hosts many spaces; they
	// share nothing.
	HeaderSpace = "X-ACP-Space"
	// HeaderSession is a random per-client-process id used to detect two processes
	// sharing one agent id (collision warning).
	HeaderSession = "X-ACP-Session"

	// DefaultSpace is used when no space is selected.
	DefaultSpace = "default"
)

// Event is one row in the totally-ordered, append-only coordination log.
type Event struct {
	Seq     uint64 `json:"seq"`               // assigned by daemon, monotonic from 1
	At      string `json:"at"`                // RFC3339 UTC, daemon clock
	Actor   string `json:"actor"`             // agent ID that produced it
	Action  string `json:"action"`            // namespaced: task.*, file.*, lease.*, chat.*, note.*
	Entity  string `json:"entity,omitempty"`  // optional subject (path, task id, ...)
	Channel string `json:"channel,omitempty"` // optional topic label (ext-1 §4) — a filterable view over the one log, NOT a boundary (the space is)
	// SubScope is the OPTIONAL ext-7 §6 project label: scoping + accounting
	// within a space, never an isolation boundary (§6.3). Server-pinned for
	// sub_scope-scoped grants; validated ([A-Za-z0-9._:-]{1,64}) otherwise.
	SubScope string         `json:"sub_scope,omitempty"`
	Before   map[string]any `json:"before,omitempty"`
	After    map[string]any `json:"after,omitempty"`
	Context  map[string]any `json:"context,omitempty"`
}

// Message is a directed mailbox message (agent -> agent).
type Message struct {
	ID       string   `json:"id"`
	From     string   `json:"from"`
	To       string   `json:"to"`
	At       string   `json:"at"`
	Type     string   `json:"type"` // inform | request | response | propose | ack | handoff
	Subject  string   `json:"subject,omitempty"`
	Body     string   `json:"body,omitempty"`
	ThreadID string   `json:"thread_id,omitempty"`
	ReplyTo  string   `json:"reply_to,omitempty"`
	CorrID   string   `json:"corr_id,omitempty"`
	Priority string   `json:"priority,omitempty"` // low | normal | urgent
	Refs     []string `json:"refs,omitempty"`
	SubScope string   `json:"sub_scope,omitempty"` // OPTIONAL ext-7 §6 label (see Event.SubScope)
	Read     bool     `json:"read"`
}

// Lease is a TTL-bounded advisory lock with a fencing token.
type Lease struct {
	Resource string `json:"resource"`
	Holder   string `json:"holder"`
	Token    uint64 `json:"token"`    // fencing token, strictly increasing
	Acquired string `json:"acquired"` // RFC3339 UTC
	Expires  int64  `json:"expires"`  // unix seconds
}

// ManifestEntry is one path in the shared workspace.
type ManifestEntry struct {
	Path  string `json:"path"`
	Hash  string `json:"hash"` // sha256 hex of the blob
	Size  int64  `json:"size"`
	Mtime string `json:"mtime"`
}

// Manifest is the versioned snapshot of the whole shared workspace.
type Manifest struct {
	Version uint64                   `json:"version"`
	Entries map[string]ManifestEntry `json:"entries"`
}

// Change is one path mutation in a manifest commit.
type Change struct {
	Path    string `json:"path"`
	Hash    string `json:"hash,omitempty"` // required unless Deleted
	Size    int64  `json:"size,omitempty"`
	Deleted bool   `json:"deleted,omitempty"`
}

// CommitRequest is an atomic, CAS-guarded manifest update.
type CommitRequest struct {
	BaseVersion uint64   `json:"base_version"` // version the client based its changes on
	Actor       string   `json:"actor"`
	Changes     []Change `json:"changes"`
	Note        string   `json:"note,omitempty"`
	SubScope    string   `json:"sub_scope,omitempty"` // OPTIONAL ext-7 §6 label (see Event.SubScope)
}

// Agent is presence info.
type Agent struct {
	ID       string `json:"id"`
	Harness  string `json:"harness,omitempty"`
	Host     string `json:"host,omitempty"`
	Status   string `json:"status,omitempty"`
	LastSeen string `json:"last_seen"`
}

// ErrorResponse is the standard error body.
type ErrorResponse struct {
	Error string `json:"error"`
	// Current is populated on 409 lease/commit conflicts so the client can
	// reconcile without a second round-trip.
	Current any `json:"current,omitempty"`
}

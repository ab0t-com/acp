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

import (
	"github.com/ab0t-com/acp/sdk/go/internal/wire"
)

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
	// BulkConfirm is the ext-34 §4.4 bulk-change confirmation echo.
	BulkConfirm = wire.BulkConfirm
	// BulkVerdict is the ext-34 body in a 428/403 bulk refusal.
	BulkVerdict = wire.BulkVerdict
	// BulkPolicy echoes a space's effective ext-34 thresholds (/v1/stats).
	BulkPolicy = wire.BulkPolicy
	// Agent is presence/roster info.
	Agent = wire.Agent
	// ErrorResponse is the standard error body (Current is set on 409 conflicts).
	ErrorResponse = wire.ErrorResponse
)

// History & checkpoints (ext-33) — the retained-version surface the SDK reads
// via GET /v1/history, GET /v1/manifest?version=N, and /v1/checkpoint.
type (
	// VersionRecord is one retained manifest version + commit metadata — the
	// body of GET /v1/manifest?version=N.
	VersionRecord = wire.VersionRecord
	// VersionInfo is one element of GET /v1/history (a record's metadata).
	VersionInfo = wire.VersionInfo
	// PathVersion is one element of GET /v1/history?path= (a path's timeline).
	PathVersion = wire.PathVersion
	// History is the body of GET /v1/history (newest first).
	History = wire.History
	// PathHistory is the body of GET /v1/history?path=.
	PathHistory = wire.PathHistory
	// Checkpoint is a named pin on a retained version record.
	Checkpoint = wire.Checkpoint
	// RetentionPolicy echoes a space's effective ext-33 retention policy.
	RetentionPolicy = wire.RetentionPolicy
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

// Realtime directory tree (ext-28) + filesystem mode (ext-31) — the /v1/fs/*
// and /v1/space/config surfaces a REALTIME-mode space serves. A space is born
// CAS (commit/manifest); an admin flip puts it in realtime mode, where the tree
// is a CRDT edited by structural ops (create/mkdir/rename/move/delete/restore)
// and a delete is a MOVE TO TRASH, recoverable for the retention window. A
// CAS-mode space answers every /v1/fs/* call 409 wrong_fs_mode (no shadow
// state); a realtime-mode space answers /v1/commit the same way.
// FSNode is one live node of GET /v1/fs/tree (the materialized tree; a file
// carries Blob+Size or Doc, a folder neither).
//
// This is a STANDALONE client DTO: its JSON field tags are byte-identical to the
// daemon's canonical internal/crdtfs.NodeView (the daemon marshals that type; the
// client decodes into this one), but it is defined here so the public wire
// package — and thus the carved-out sdk/go module — never imports the tree engine
// (internal/crdtfs, which carries the CRDT apply logic and is not client-safe).
// The byte-identity of the two encodings is pinned by a test at the daemon
// boundary (cmd/coordd/fs_wire_identity_test.go). Keep these tags in lockstep
// with internal/crdtfs.NodeView.
type FSNode struct {
	Path   string `json:"path"`
	NodeID string `json:"node_id"`
	Folder bool   `json:"folder"`
	Blob   string `json:"blob,omitempty"`
	Size   int64  `json:"size,omitempty"`
	Doc    string `json:"doc,omitempty"`
}

// FSTrashEntry is one recoverable trashed root of GET /v1/fs/trash: its node id
// (the restore handle) and its immutable pre-trash Origin path.
//
// Standalone client DTO — see FSNode. Its JSON tags are byte-identical to the
// daemon's internal/crdtfs.TrashEntry; keep them in lockstep.
type FSTrashEntry struct {
	NodeID    string `json:"node_id"`
	Name      string `json:"name"`
	TrashedAt int64  `json:"trashed_at"`
	Origin    string `json:"origin"` // the immutable pre-trash path (ext-31 §4.6)
	Folder    bool   `json:"folder"`
}

// FSOp is one structural op in a POST /v1/fs/ops batch. It mirrors the daemon's
// request row field-for-field (cmd/coordd/fs.go fsOpReq — the daemon keeps that
// type unexported; pkg/wire.TestFSOpWireShape pins the field names). A batch is
// applied ATOMICALLY and in order: a later op sees an earlier op's effect
// (mkdir a; create a/x in one call). Paths are slash-separated, no leading "/".
//
//	Op        Path            To              Node        Blob / Doc
//	create    new file path   —               —           content: blob hash OR live-doc name
//	mkdir     new folder path —               —           —
//	rename    existing path   new path        —           — (same parent)
//	move      existing path   new path        —           — (different parent)
//	delete    existing path   —               —           — (to trash; recoverable)
//	restore   —               optional path   trashed id  — (default: the origin path)
//
// The daemon classifies rename vs move itself from the parents, so "move" is
// accepted for both. delete/mkdir/create/rename/move need the writer role and
// the path in the token's write scope; a batch whose DELETE count reaches the
// space's bulk minimum is refused 403 bulk_denied unless the grant allows bulk.
type FSOp struct {
	Op   string `json:"op"`
	Path string `json:"path,omitempty"`
	To   string `json:"to,omitempty"`
	Node string `json:"node,omitempty"`
	Blob string `json:"blob,omitempty"`
	Doc  string `json:"doc,omitempty"`
}

// FSOpsResult is the 200 body of POST /v1/fs/ops. Applied is the number of ops
// the batch carried; Outstamped names ops (if any) that an existing placement
// out-ordered under the stamp total order and so had NO visible effect — the
// caller re-reads the tree and retries them; it is never a silent success.
type FSOpsResult struct {
	Applied    int      `json:"applied"`
	Outstamped []string `json:"outstamped,omitempty"`
	Warning    string   `json:"warning,omitempty"`
}

// FSModeConfirm is the exact-scope echo a NON-EMPTY flip must carry (ext-31
// §4.3.1): region "/" (v1 is whole-space), the current and target modes, and
// the EXACT entry count the daemon reported in its 428 fs_mode_confirm_required
// demand (FSModeDemand). The daemon re-evaluates the count in apply; a stale
// count is refused 428 fs_mode_confirm_mismatch with no state change.
type FSModeConfirm struct {
	Region  string `json:"region"`
	From    string `json:"from"`
	To      string `json:"to"`
	Entries uint32 `json:"entries"`
}

// FSModeDemand is the "confirm" block of a 428 fs_mode_confirm_required /
// fs_mode_confirm_mismatch body: what the flip would cross. FromVersion and
// LeasesHeld are set for cas->realtime, Trashed for realtime->cas.
type FSModeDemand struct {
	Region      string `json:"region"`
	From        string `json:"from"`
	To          string `json:"to"`
	Entries     uint32 `json:"entries"`
	FromVersion uint64 `json:"from_version,omitempty"`
	LeasesHeld  uint32 `json:"leases_held,omitempty"`
	Trashed     uint32 `json:"trashed,omitempty"`
}

// FSModeResult is the 200 body of a flip (PUT /v1/space/config, or its
// no-confirm admin alias POST /v1/admin/fs/mode) and of the mode read. NoOp
// marks a flip that did not apply as THIS request's flip (already in the target
// mode, or a raced/stale proposal) — compare FSMode to your target, never assume.
// Checkpoint/Version name the mandatory pre-flip pin when one was created.
type FSModeResult struct {
	FSMode     string `json:"fs_mode"`
	NoOp       bool   `json:"no_op,omitempty"`
	Checkpoint string `json:"checkpoint,omitempty"`
	Version    uint64 `json:"version,omitempty"`
}

// FSTrashRestoreResult is the 200 body of POST /v1/fs/trash/restore — the
// CAS-mode recovery surface (ext-31 §4.7.4): the pre-flip trashed file was
// forward-committed into the CURRENT CAS manifest at its origin path.
type FSTrashRestoreResult struct {
	Restored string `json:"restored"`
	Version  uint64 `json:"version"`
}

// FSPruneResult is the 200 body of POST /v1/admin/fs/prune (leader-driven
// eviction of trash past its retention window; records only — GC reclaims
// bytes). IntegrityAnomalies MUST be 0; non-zero is a loud materialization alarm.
type FSPruneResult struct {
	Evicted             int   `json:"evicted"`
	OldestTrashedAt     int64 `json:"oldest_trashed_at"`
	HistoryBytesOverCap bool  `json:"history_bytes_over_cap"`
	IntegrityAnomalies  int   `json:"integrity_anomalies"`
}

// Filesystem-mode values (ext-31 §3.1). "" on the wire means FSModeCAS.
const (
	FSModeCAS      = "cas"
	FSModeRealtime = "realtime"
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

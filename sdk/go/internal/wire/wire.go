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
	// RestoreOf is the OPTIONAL ext-33 §4.8.2 intent binding: when present the
	// authority verifies IN APPLY that every path named in Changes ends up exactly
	// equal (hash, size; or absent) to its entry in the retained record for this
	// version, else the commit fails 400 restore_mismatch with no state change.
	// Absent = an ordinary commit, applied byte-identically to before ext-33.
	RestoreOf *uint64 `json:"restore_of,omitempty"`

	// BulkConfirm is the OPTIONAL ext-34 §4.4 intent binding for a BULK commit
	// (one that destroys, on NET, more paths than the space's threshold): the
	// caller echoes the EXACT counts the authority computed (from a prior 428
	// bulk_confirm_required, or pre-computed locally) and the apply requires exact
	// equality of both — a retry that did not read the numbers cannot satisfy it.
	// Absent = an ordinary commit; below the threshold the field is ignored and
	// the commit is byte-identical to before ext-34.
	BulkConfirm *BulkConfirm `json:"bulk_confirm,omitempty"`
}

// BulkConfirm is the ext-34 §4.4.2 echo. Both members are REQUIRED when the
// field is present (pointers so a missing member is 400 bulk_confirm_invalid,
// §4.4.4 — never silently read as 0). It is a speed bump, not the wall: the
// grant disposition (§4.5) is what denies a bulk change outright.
type BulkConfirm struct {
	Deletes    *uint32 `json:"deletes"`
	Overwrites *uint32 `json:"overwrites"`
}

// BulkVerdict is the ext-34 body carried in a 428 (bulk_confirm_required /
// bulk_confirm_mismatch) and a 403 (bulk_denied): the authority's own count of
// what this commit would destroy on NET, within the token's measurement scope.
// It reveals nothing the token could not read from GET /v1/manifest (§10.2).
type BulkVerdict struct {
	Deletes             uint32 `json:"deletes"`
	Overwrites          uint32 `json:"overwrites"`
	Entries             uint32 `json:"entries"`
	Version             uint64 `json:"version"`
	ThresholdDeletes    uint32 `json:"threshold_deletes"`
	ThresholdOverwrites uint32 `json:"threshold_overwrites"`
	Scoped              bool   `json:"scoped"`
}

// BulkPolicy echoes a space's EFFECTIVE ext-34 §4.3.1 thresholds (defaults
// applied) — the /v1/stats "bulk" block. Fractions are decimal strings
// interpreted as exact rationals num/10000 (§4.3.2).
type BulkPolicy struct {
	BulkMinPaths           uint32 `json:"bulk_min_paths"`
	BulkFraction           string `json:"bulk_fraction"`
	BulkHardPaths          uint32 `json:"bulk_hard_paths"`
	BulkOverwriteMinPaths  uint32 `json:"bulk_overwrite_min_paths"`
	BulkOverwriteFraction  string `json:"bulk_overwrite_fraction"`
	BulkOverwriteHardPaths uint32 `json:"bulk_overwrite_hard_paths"`
}

// VersionRecord is one retained manifest version plus its commit metadata
// (ext-33 §4.2.1) — the body of GET /v1/manifest?version=N. Entries is exactly
// the map GET /v1/manifest returned for that version. At/Actor are absent for a
// record derived from a pre-history manifest (§4.2.5). Checkpoints lists the
// names pinning it (read-side only; never stored).
type VersionRecord struct {
	Version     uint64                   `json:"version"`
	Entries     map[string]ManifestEntry `json:"entries"`
	At          string                   `json:"at,omitempty"`
	Actor       string                   `json:"actor,omitempty"`
	Note        string                   `json:"note,omitempty"`
	RestoreOf   *uint64                  `json:"restore_of,omitempty"`
	Checkpoints []string                 `json:"checkpoints,omitempty"`
	// EmptyDirs is the ext-31 §4.7.1 preserved-empty-directory list: folder paths a
	// realtime→cas tree snapshot had no live file under (so they leave no manifest
	// entry). Retained + GC-tracked with the record; a later cas→realtime seed
	// re-creates them (finding F8). Absent = an ordinary commit (no empty dirs to
	// carry) — byte-identical to before ext-31.
	EmptyDirs []string `json:"empty_dirs,omitempty"`
}

// VersionInfo is one element of GET /v1/history (ext-33 §4.7.2): a record's
// metadata without its entries. Entries is the (scope-filtered) entry count.
type VersionInfo struct {
	Version     uint64   `json:"version"`
	At          string   `json:"at,omitempty"`
	Actor       string   `json:"actor,omitempty"`
	Note        string   `json:"note,omitempty"`
	RestoreOf   *uint64  `json:"restore_of,omitempty"`
	Checkpoints []string `json:"checkpoints,omitempty"`
	Entries     uint32   `json:"entries"`
}

// PathVersion is one element of GET /v1/history?path= (ext-33 §4.7.3): a
// retained version at which the path's entry differs from the previous
// retained record. Hash/Size/Mtime are absent when Deleted.
type PathVersion struct {
	Version   uint64  `json:"version"`
	At        string  `json:"at,omitempty"`
	Actor     string  `json:"actor,omitempty"`
	Hash      string  `json:"hash,omitempty"`
	Size      *int64  `json:"size,omitempty"`
	Mtime     string  `json:"mtime,omitempty"`
	Deleted   bool    `json:"deleted"`
	RestoreOf *uint64 `json:"restore_of,omitempty"`
}

// RetentionPolicy echoes a space's EFFECTIVE ext-33 §4.3.1 policy (defaults
// applied). MaxHistoryBytes is omitted when unbounded.
type RetentionPolicy struct {
	HistoryTTLSec      uint64 `json:"history_ttl_sec"`
	HistoryMinVersions uint32 `json:"history_min_versions"`
	MaxHistoryBytes    uint64 `json:"max_history_bytes,omitempty"`
	MaxCheckpoints     uint32 `json:"max_checkpoints"`
}

// History is the body of GET /v1/history (ext-33 §4.7.2), newest first.
type History struct {
	Versions   []VersionInfo   `json:"versions"`
	Oldest     uint64          `json:"oldest"`
	NextBefore *uint64         `json:"next_before,omitempty"`
	Policy     RetentionPolicy `json:"policy"`
}

// PathHistory is the body of GET /v1/history?path= (ext-33 §4.7.3).
type PathHistory struct {
	Path       string        `json:"path"`
	Versions   []PathVersion `json:"versions"`
	Oldest     uint64        `json:"oldest"`
	NextBefore *uint64       `json:"next_before,omitempty"`
}

// Checkpoint is a named pin on a retained version record (ext-33 §4.6,
// carried forward from ext-30). Entries is the (scope-filtered) entry count of
// the pinned record, computed on read.
type Checkpoint struct {
	Name    string `json:"name"`
	Version uint64 `json:"version"`
	Created string `json:"created"`
	Actor   string `json:"actor"`
	Note    string `json:"note,omitempty"`
	Entries uint32 `json:"entries"`
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
	// Code is a stable machine-readable error class where one is defined
	// (ext-33 Appendix B: restore_mismatch, version_pruned, ...). Omitted for
	// every pre-existing error, so those bodies are byte-identical.
	Code string `json:"code,omitempty"`
	// Current is populated on 409 lease/commit conflicts so the client can
	// reconcile without a second round-trip.
	Current any `json:"current,omitempty"`
}

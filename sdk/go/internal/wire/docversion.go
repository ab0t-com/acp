package wire

import (
	"strconv"
	"strings"
)

// TEXT-DOCUMENT UNDO WINDOW (v0.2.6, G-B8 / owner decision D5; acp-1 amendment
// "acp1-12.5-compaction-undo-window"): crdt.Op carries no timestamp, so a window
// cannot be judged per operation; instead, before a compaction drops a text
// document's deleted characters, the authority keeps the PRE-COMPACTION op-log
// as a retained version document, named
//
//	.acp-trash/e<epoch>@<compacted-at-unix-nanos>/<document name>
//
// (epoch = the document's epoch before the compaction; time = the leader stamp
// of the compaction command), inside the reserved DocTrashPrefix namespace. It
// lives for the space's history TTL, the newest few per document,
// and replaying a prefix of it gives the document's text at any point before the
// compaction — so a deletion stays recoverable even after its tombstones are
// collected.

// DocVersionName names the retained pre-compaction op-log of doc `name` at
// `epoch`, compacted at the leader stamp `atNanos`.
func DocVersionName(name string, epoch int, atNanos int64) string {
	return DocTrashPrefix + "e" + strconv.Itoa(epoch) + "@" + strconv.FormatInt(atNanos, 10) + "/" + name
}

// ParseDocVersionName is the inverse of DocVersionName (ok=false for any other
// shape, including a trash entry).
func ParseDocVersionName(v string) (name string, epoch int, atNanos int64, ok bool) {
	if !strings.HasPrefix(v, DocTrashPrefix+"e") {
		return "", 0, 0, false
	}
	// Reuse the trash parser on the same shape with the "e" removed.
	name, epoch, atNanos, ok = ParseDocTrashName(DocTrashPrefix + v[len(DocTrashPrefix)+1:])
	return
}

// DocVersionEntry is one retained pre-compaction version (GET /v1/crdt/versions).
type DocVersionEntry struct {
	Doc         string `json:"doc"`
	Epoch       int    `json:"epoch"`        // the epoch the doc had before this compaction
	CompactedAt string `json:"compacted_at"` // RFC 3339, the leader stamp of the compaction
	ExpiresAt   string `json:"expires_at,omitempty"`
	Ops         int    `json:"ops"` // retained op count; ?at=1..ops replays a prefix
}

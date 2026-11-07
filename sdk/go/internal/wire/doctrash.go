package wire

import (
	"fmt"
	"strconv"
	"strings"
)

// DOCUMENT TRASH (v0.2.6, G-B10 / owner decision D4; acp-1 amendment
// "acp1-12.5-doc-delete-trash"): DELETE /v1/crdt/doc (and the crdtjson twin) no
// longer erases a document. The deleted generation's op-log is first copied to a
// RETAINED trash document inside the same store, named
//
//	.acp-trash/<gen>@<deleted-at-unix-nanos>/<original name>
//
// where <gen> is the generation of the instance that was deleted and the time is
// the LEADER stamp of the delete command — so the name is a pure function of
// replicated state and every replica writes the identical trash document. The
// trash is an ordinary document of the store, so raft snapshots, `admin dump`
// and migrations carry it with no new record kind. It is restorable (to its
// original name) until the space's history TTL passes, then the replicated
// history.prune removes it.
//
// The prefix is RESERVED: the daemon refuses every client request that names a
// document under it (400 reserved_doc_name), lists never show it, and it does
// not count toward max_docs. Only the creation-time rule is affected; a name in
// this shape still passes ValidDocNameForRestore (committed state restores).

// DocTrashPrefix is the reserved first segment of every trash document.
const DocTrashPrefix = ".acp-trash/"

// DocTrashName names the trash copy of doc `name` at generation `gen`, deleted
// at the leader stamp `atNanos`.
func DocTrashName(name string, gen int, atNanos int64) string {
	return DocTrashPrefix + strconv.Itoa(gen) + "@" + strconv.FormatInt(atNanos, 10) + "/" + name
}

// IsReservedDocName reports whether a client-supplied doc name lies under the
// reserved trash prefix (or IS the bare prefix directory name).
func IsReservedDocName(name string) bool {
	// In any letter case: on a case-insensitive disk (a macOS server) ".ACP-TRASH/" is the same folder.
	dir, _, _ := strings.Cut(name, "/")
	return strings.EqualFold(dir, strings.TrimSuffix(DocTrashPrefix, "/"))
}

// ParseDocTrashName is the inverse of DocTrashName. ok is false for any name
// not in the exact shape (a non-numeric gen or time, a missing original name),
// so a document a client created under the prefix before v0.2.6 in some other
// shape is never mistaken for a trash entry.
func ParseDocTrashName(trash string) (name string, gen int, atNanos int64, ok bool) {
	if !strings.HasPrefix(trash, DocTrashPrefix) {
		return "", 0, 0, false
	}
	rest := trash[len(DocTrashPrefix):]
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 || slash == len(rest)-1 {
		return "", 0, 0, false
	}
	head, name := rest[:slash], rest[slash+1:]
	at := strings.IndexByte(head, '@')
	if at <= 0 || at == len(head)-1 {
		return "", 0, 0, false
	}
	g, err := strconv.Atoi(head[:at])
	if err != nil || g < 0 || strconv.Itoa(g) != head[:at] {
		return "", 0, 0, false
	}
	t, err := strconv.ParseInt(head[at+1:], 10, 64)
	if err != nil || t < 0 || strconv.FormatInt(t, 10) != head[at+1:] {
		return "", 0, 0, false
	}
	return name, g, t, true
}

// DocTrashEntry is one retained trashed document (GET /v1/crdt/trash and
// GET /v1/crdtjson/trash).
type DocTrashEntry struct {
	Doc       string `json:"doc"`        // the original document name
	Gen       int    `json:"gen"`        // the deleted instance's generation (restore names it)
	DeletedAt string `json:"deleted_at"` // RFC 3339, the leader stamp of the delete
	ExpiresAt string `json:"expires_at,omitempty"`
	Ops       int    `json:"ops"` // retained op count
}

// ErrDocTrashShape is returned by callers that must refuse a name in the
// reserved space.
var ErrDocTrashShape = fmt.Errorf("document names under %q are reserved for the document trash", DocTrashPrefix)

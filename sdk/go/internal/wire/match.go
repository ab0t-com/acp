package wire

import (
	"fmt"
	"strings"
)

// Filter-pattern grammar (ext-1 §4.1, revised 2026-07-12): a pattern is either
// a LITERAL (byte-exact match) or a PREFIX PATTERN — a literal ending in ".*",
// meaning "the literal prefix up to and including the dot, followed by
// anything". So "ladder.*" matches "ladder.gate.failed" but NOT the bare
// "ladder" (list both to cover the parent). No mid-string "*", no "?", no
// regex — a match costs O(len). The daemon's filtered subscription and any
// relay-side re-filtering share THIS matcher, so semantics never diverge.

// UnlabeledPattern is the reserved channel-filter pattern matching ONLY
// unlabeled events (empty channel). It is illegal as a published channel name.
const UnlabeledPattern = "_"

// MatchPattern reports whether s matches pat under the ext-1 §4.1 grammar.
// pat is assumed valid (see ValidPattern); the "_" reservation is a
// channel-filter concern handled by the caller, not here.
func MatchPattern(pat, s string) bool {
	if strings.HasSuffix(pat, ".*") {
		return strings.HasPrefix(s, pat[:len(pat)-1]) // keep the dot: "a.*" ⇒ prefix "a."
	}
	return pat == s
}

// ValidPattern rejects patterns outside the ext-1 §4.1 grammar: empty, or any
// "*" that is not the trailing ".*" form.
func ValidPattern(pat string) error {
	if pat == "" {
		return fmt.Errorf("empty pattern")
	}
	if i := strings.IndexByte(pat, '*'); i >= 0 {
		if i != len(pat)-1 || !strings.HasSuffix(pat, ".*") || pat == ".*" {
			return fmt.Errorf("pattern %q: %q is legal only as a trailing %q", pat, "*", ".*")
		}
	}
	return nil
}

// ValidChannelName enforces ext-1 §4.1 on PUBLISHED channel names:
// [A-Za-z0-9._:-]{1,64}. Empty is legal (unlabeled — the field is optional);
// the bare "_" is reserved for the unlabeled filter pattern and may not be
// published.
func ValidChannelName(s string) error {
	if s == "" {
		return nil
	}
	if s == UnlabeledPattern {
		return fmt.Errorf("channel name %q is reserved (matches only unlabeled events in filters)", UnlabeledPattern)
	}
	if len(s) > 64 {
		return fmt.Errorf("channel name too long: %d bytes (max 64)", len(s))
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '.', c == '_', c == ':', c == '-':
		default:
			return fmt.Errorf("channel name %q: illegal byte %q (allowed: [A-Za-z0-9._:-])", s, c)
		}
	}
	return nil
}

// ReservedDocFileSuffixes are the EXACT on-disk file names the CRDT stores derive
// from a document name, minus the name: doc "d" owns
//
//	d.jsonl           the op-log            (filestore engine.go: name+".jsonl")
//	d.jsonl.meta      the op-log's meta     (filestore logstore.go: path+".meta")
//	d.jsonl.meta.tmp  its atomic-write temp (logstore.go: metaPath+".tmp")
//	d.jsonl.rewrite   the compaction temp   (logstore.go: path+".rewrite")
//	d.meta.json       the epoch/gen sidecar (crdtstore + crdtjsonstore)
//	d.meta.json.tmp   its atomic-write temp (both stores: path+".tmp")
//
// and NOTHING else (under the db profile the op-log is a key and only the two
// sidecar names touch disk — a subset). A sibling DIRECTORY segment collides with
// doc d's files iff it EQUALS d + one of these six — i.e. iff it ENDS in one of
// them (ext-31 fifth re-review P3: the earlier substring rule also refused names
// that cannot collide, such as "x.jsonl.bak/y"). See ValidDocName. Exported for
// the tests and the SDK mirror; the list documents the store layout — a NEW
// on-disk artifact derived from a doc name MUST be added here (HZ-ACPDB-40 KEEP).
var ReservedDocFileSuffixes = []string{".jsonl", ".jsonl.meta", ".jsonl.meta.tmp", ".jsonl.rewrite", ".meta.json", ".meta.json.tmp"}

// MaxDocSegmentLen bounds ONE path segment of a document name. The final segment
// becomes a file name plus the longest derived suffix (".jsonl.meta.tmp", 15
// bytes) and must fit NAME_MAX (255 on every supported filesystem): a longer
// segment made the sidecar/op-log create fail with ENAMETOOLONG — identically on
// every replica, from the name alone — which the FSM classifies as a node-local
// fault and fail-STOPs on (CL-25, the F1 class). 240 leaves that headroom.
const MaxDocSegmentLen = 240

// ValidDocName enforces the CREATION-time naming rule for CRDT documents (push /
// apply — every path that can bring a NEW name into replicated state): non-empty,
// at most 256 bytes, no path traversal ("." / ".." / empty segments), no absolute
// path, no NUL/control bytes, none of the characters that are illegal or
// ambiguous in a file name, every segment ≤ MaxDocSegmentLen, and — CL-25
// (ext-31 fourth re-review F1) — no DIRECTORY segment (any segment but the last)
// that ENDS IN one of the six reserved derived names (ReservedDocFileSuffixes).
// Document names arrive from clients and become FILE names under the space's
// data dir (doc "a/b" → the directory "a" + the files "a/b.jsonl",
// "a/b.meta.json"), so this is a path-safety guard, not a cosmetic one. Names
// ARE path-like by design (ext-7 §4.8.2 read_prefix scoping, nested docs; "/"
// stays legal).
//
// Why the directory-segment rule: with no escaping, the CLIENT chose whether
// "<dir>/d.meta.json" is doc d's sidecar FILE or the DIRECTORY holding doc
// "d.meta.json/x". Pushing "d.meta.json/x" then "d" (or the reverse, or the
// ".jsonl" twin under the file profile) made every replica compute the SAME
// EISDIR/ENOTDIR from the same committed bytes → a deterministic error the FSM
// treats as node-local → a cluster-wide fail-stop that re-halts on every
// restart (two ordinary writer pushes, no operator remedy). Refusing here turns
// it into the whitelisted, DATA-recorded ErrInvalidDocName on every replica.
// A FINAL segment may carry the stems ("events.jsonl" is a legitimate document
// name — pushing a local file under its own name): doc "d.jsonl"'s files are
// "d.jsonl.jsonl" / "d.jsonl.meta.json", disjoint from doc "d"'s, and the
// discovery walks strip exactly one suffix, so no final-segment name collides.
// Why "ends in", not "contains" (ext-31 fifth re-review P3): a directory segment
// X collides with sibling doc d exactly when X == d + s for one of the six
// derived suffixes s — no other file is ever created next to a doc — so
// HasSuffix(X, s) is the whole collision set (plus the degenerate stems "", "."
// and "..", which are never doc names), while "x.jsonl.bak/y" or
// "reports.jsonlines/2026" can never collide and are legal. The tighter rule
// also shrinks how many LEGACY names the upgrade affects (see
// ValidDocNameForRestore).
//
// It lives here, beside ValidChannelName, because BOTH crdtstore and
// crdtjsonstore need exactly this rule and each used to carry its own
// byte-identical copy. They never disagreed — but a duplicated guard is exactly
// how the write-side blob-hash gap happened (a rule fixed at one call site and
// not at its twin), so the rule now has one definition and one place to fix.
// The refusal is a pure function of the name: identical on every replica.
func ValidDocName(name string) error {
	if err := ValidDocNameForRestore(name); err != nil {
		return err
	}
	if len(name) > 256 {
		return fmt.Errorf("document name too long")
	}
	for i := 0; i < len(name); i++ {
		if c := name[i]; c < 0x20 || c == 0x7f {
			return fmt.Errorf("illegal document name %q: control byte %#x", name, c)
		}
	}
	segs := strings.Split(name, "/")
	for i, seg := range segs {
		if len(seg) > MaxDocSegmentLen {
			return fmt.Errorf("illegal document name %q: segment longer than %d bytes", name, MaxDocSegmentLen)
		}
		if i < len(segs)-1 {
			for _, suffix := range ReservedDocFileSuffixes {
				if strings.HasSuffix(seg, suffix) {
					return fmt.Errorf("illegal document name %q: directory segment %q ends in the reserved %q", name, seg, suffix)
				}
			}
		}
	}
	return nil
}

// ValidDocNameForRestore is the RESTORE-boundary rule — the PATH-SAFETY subset of
// ValidDocName, for names that are already COMMITTED replicated state (a raft
// snapshot's docs and tombstones: crdtstore/crdtjsonstore RestoreDoc and
// RestoreTombstone). It refuses only what would corrupt or escape the on-disk
// layout — empty; a "." / ".." / empty segment or a leading "/" (traversal); a
// NUL byte (no filesystem accepts it: the create fails with EINVAL); and the
// bytes that are separators or illegal on a supported platform ("\" is a path
// separator on Windows, so "a\..\.." would escape the space dir there). It does
// NOT enforce the creation-time strictness: the 256-byte / MaxDocSegmentLen
// caps, control bytes other than NUL, or the reserved-suffix directory rule.
//
// Why two rules (ext-31 fifth re-review G1, fix-introduced P1): hashicorp raft
// restores a node's NEWEST LOCAL snapshot into the FSM on EVERY start (coordd
// never sets NoSnapshotRestoreOnStart, and the graceful stop of a rolling upgrade
// snapshots every node's full state). Widening the one creation rule therefore
// also widened what an UPGRADED node refuses from its OWN snapshot: any doc — or
// tombstone (there is no tombstone GC, so a delete cannot clear it) — whose name
// the old binary accepted made NewRaft fail with "failed to load any existing
// snapshots" and the node did not start. Restoring committed state is not a
// creation (the same rationale RestoreDoc uses to bypass maxDocs): a legacy name
// that was laid down safely under the old rule is laid down again by the SAME
// six derived names, so nothing about it is newly unsafe. The strict rule stays
// at the creation boundary, so the crash-loop class (HZ-ACPDB-40) stays closed;
// a legacy name is refused again at its NEXT push (400) — correct and
// recoverable — while reads, deletes and restores keep working.
//
// Invariant the tests pin: everything ValidDocName accepts, this accepts; and
// every name the PRE-CL-25 rule accepted (256 bytes, no ".."/empty segment, no
// `\:*?"<>|`, not absolute) passes here except a NUL — the only name an old
// binary could have committed (db profile only) that no binary can restore,
// because the sidecar read fails with EINVAL before any rule runs.
func ValidDocNameForRestore(name string) error {
	if name == "" {
		return fmt.Errorf("empty document name")
	}
	if strings.IndexByte(name, 0) >= 0 {
		return fmt.Errorf("illegal document name %q: NUL byte", name)
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("illegal document name %q", name)
		}
	}
	if strings.ContainsAny(name, "\\:*?\"<>|") || strings.HasPrefix(name, "/") {
		return fmt.Errorf("illegal characters in document name %q", name)
	}
	return nil
}

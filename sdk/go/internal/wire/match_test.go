package wire

import (
	"strings"
	"testing"
)

func TestMatchPattern(t *testing.T) {
	cases := []struct {
		pat, s string
		want   bool
	}{
		// literals: exact byte equality only
		{"build", "build", true},
		{"build", "builds", false},
		{"build", "Build", false}, // case-sensitive
		{"a.b", "a.b", true},
		{"a.b", "a.b.c", false}, // literal is not a prefix rule

		// prefix patterns: "a.*" = literal "a." then anything
		{"a.*", "a.b", true},
		{"a.*", "a.b.c", true},
		{"ladder.*", "ladder.gate.failed", true},
		{"ladder.gate.*", "ladder.gate.failed", true},
		{"a.*", "a", false}, // bare parent does NOT match (list it too)
		{"ladder.*", "ladder", false},
		{"a.*", "ab.c", false}, // dot boundary: prefix is "a.", not "a"
		{"a.*", "b.c", false},
		{"a.b.*", "a.b", false},    // bare parent, deeper
		{"a.b.*", "a.bc.d", false}, // dot boundary, deeper

		// "_" is an ordinary literal to the matcher (reservation is the caller's)
		{"_", "_", true},
		{"_", "", false},
	}
	for _, c := range cases {
		if got := MatchPattern(c.pat, c.s); got != c.want {
			t.Errorf("MatchPattern(%q, %q) = %v, want %v", c.pat, c.s, got, c.want)
		}
	}
}

func TestValidPattern(t *testing.T) {
	cases := []struct {
		pat string
		ok  bool
	}{
		{"build", true},
		{"a.b", true},
		{"a.*", true},
		{"ladder.gate.*", true},
		{"_", true}, // channel-filter reservation is enforced by the caller
		{"", false},
		{"*", false},     // bare star
		{".*", false},    // no prefix
		{"a*", false},    // star without the dot
		{"a.*.b", false}, // mid-string star
		{"*.failed", false},
		{"a.**", false},
	}
	for _, c := range cases {
		err := ValidPattern(c.pat)
		if (err == nil) != c.ok {
			t.Errorf("ValidPattern(%q) = %v, want ok=%v", c.pat, err, c.ok)
		}
	}
}

func TestValidChannelName(t *testing.T) {
	long := make([]byte, 65)
	for i := range long {
		long[i] = 'a'
	}
	cases := []struct {
		name string
		ok   bool
	}{
		{"", true}, // unlabeled is always legal
		{"build", true},
		{"a.b-c:d_e.9", true},
		{string(long[:64]), true},
		{string(long), false}, // 65 bytes
		{"_", false},          // reserved for the unlabeled filter pattern
		{"_groups", true},     // underscore is fine anywhere but alone
		{"has space", false},
		{"star*", false},
		{"emoji✨", false},
	}
	for _, c := range cases {
		err := ValidChannelName(c.name)
		if (err == nil) != c.ok {
			t.Errorf("ValidChannelName(%q) = %v, want ok=%v", c.name, err, c.ok)
		}
	}
}

// ValidDocName is the ONE definition of the CRDT document-name rule. It lives
// here because crdtstore and crdtjsonstore both need exactly it and each used to
// carry a byte-identical private copy — they never disagreed, but a duplicated
// guard is precisely how the write-side blob-hash gap happened (a rule fixed at
// one call site and not at its twin). Document names arrive from clients and
// become FILE names under the space's data dir, so this is a path-safety guard.
func TestValidDocName(t *testing.T) {
	for _, ok := range []string{
		"notes.md",
		"a",
		"dir/sub/file.json",
		"..gitkeep", // dots in a NAME are fine; only whole ".."/"." segments are not
		"a..b/c",    //
		"weird name with spaces",
		strings.Repeat("n", MaxDocSegmentLen) + "/" + strings.Repeat("m", 15), // 256 bytes, every segment within the segment cap
		// CL-25 (ext-31 fourth re-review F1): the reserved store stems are legal in
		// the FINAL segment — "events.jsonl" is a real document name (a local file
		// pushed under its own name); doc "d.jsonl"'s files ("d.jsonl.jsonl",
		// "d.jsonl.meta.json") are disjoint from doc "d"'s. Only a DIRECTORY segment
		// can collide with a sibling document's file.
		"events.jsonl",
		"d.meta.json",
		"d.jsonl.meta",
		"dir/d.jsonl",
		"dir/d.meta.json.tmp",
		// ext-31 fifth re-review P3: a directory segment collides only when it
		// EQUALS a sibling's derived file name (ends in one of the six suffixes);
		// a segment that merely CONTAINS a stem cannot collide and is legal.
		"x.jsonl.bak/y",
		"reports.jsonlines/2026",
		"a.meta.jsonx/b",
		"jsonl/x",
		"meta.json.d/x",
	} {
		if err := ValidDocName(ok); err != nil {
			t.Fatalf("ValidDocName(%q) must be accepted: %v", ok, err)
		}
		if err := ValidDocNameForRestore(ok); err != nil {
			t.Fatalf("ValidDocNameForRestore(%q) must accept everything ValidDocName accepts: %v", ok, err)
		}
	}

	for _, bad := range []struct{ name, doc string }{
		{"empty", ""},
		{"over the cap", strings.Repeat("n", 257)},
		// CL-25 / F1: a DIRECTORY segment that is (or contains) a reserved store file
		// name collides with a sibling doc's sidecar/op-log PATH — "d.meta.json/x"
		// makes doc d's sidecar path a directory; every replica then computes the
		// same EISDIR/ENOTDIR from the same bytes and fail-STOPs (a crash loop).
		{"sidecar collision", "d.meta.json/x"},
		{"op-log collision", "d.jsonl/x"},
		{"op-log meta collision", "d.jsonl.meta/x"},
		{"op-log rewrite temp collision", "d.jsonl.rewrite/x"},
		{"sidecar temp collision", "d.meta.json.tmp/x"},
		{"nested sidecar collision", "a/d.meta.json/x/y"},
		{"op-log meta temp collision", "d.jsonl.meta.tmp/x"},
		{"bare suffix as a directory segment", ".jsonl/x"},
		// NAME_MAX: a segment longer than MaxDocSegmentLen plus the derived suffix
		// exceeds 255 → ENAMETOOLONG on every replica (the same F1 class).
		{"segment over the segment cap", strings.Repeat("n", MaxDocSegmentLen+1)},
		{"non-final segment over the segment cap", strings.Repeat("n", MaxDocSegmentLen+1) + "/x"},
		// NUL / control bytes: EINVAL from the filesystem, identical on every replica.
		{"NUL byte", "a\x00b"},
		{"newline", "a\nb"},
		{"DEL", "a\x7fb"},
		{"tab", "a\tb"},
		{"dot segment", "a/./b"},
		{"dotdot segment", "a/../b"},
		{"leading dotdot", "../escape"},
		{"bare dotdot", ".."},
		{"bare dot", "."},
		{"empty segment", "a//b"},
		{"absolute", "/etc/passwd"},
		{"backslash", `a\b`},
		{"colon", "a:b"},
		{"star", "a*b"},
		{"question", "a?b"},
		{"quote", `a"b`},
		{"angle", "a<b"},
		{"pipe", "a|b"},
	} {
		if err := ValidDocName(bad.doc); err == nil {
			t.Fatalf("%s: ValidDocName(%q) must be refused", bad.name, bad.doc)
		}
	}

	// Collision-completeness of the six-suffix rule: for EVERY reserved derived
	// name, the directory segment that equals a sibling doc's file is refused —
	// in the first, a middle and the last-but-one position — and the list is
	// exactly the store layout (a new artifact must be added here, HZ-ACPDB-40).
	want := []string{".jsonl", ".jsonl.meta", ".jsonl.meta.tmp", ".jsonl.rewrite", ".meta.json", ".meta.json.tmp"}
	if strings.Join(ReservedDocFileSuffixes, " ") != strings.Join(want, " ") {
		t.Fatalf("ReservedDocFileSuffixes = %v, want the six derived names %v", ReservedDocFileSuffixes, want)
	}
	for _, s := range ReservedDocFileSuffixes {
		for _, bad := range []string{"d" + s + "/x", "a/d" + s + "/x", "a/d" + s + "/x/y"} {
			if err := ValidDocName(bad); err == nil {
				t.Fatalf("ValidDocName(%q): a directory segment equal to a sibling doc's %q file must be refused", bad, s)
			}
		}
		// The same name in the FINAL segment is disjoint on disk and stays legal.
		if err := ValidDocName("d" + s); err != nil {
			t.Fatalf("ValidDocName(%q) must be accepted (final segment): %v", "d"+s, err)
		}
	}
}

// TestValidDocNameForRestore pins the RESTORE-boundary rule (ext-31 fifth
// re-review G1): a snapshot's names are committed replicated state, so only the
// path-safety subset is enforced — every name the PRE-CL-25 rule accepted (and
// so may sit in a node's own newest snapshot) restores, while anything that
// would escape or corrupt the on-disk layout is still refused.
func TestValidDocNameForRestore(t *testing.T) {
	// Legacy names the creation rule now refuses; an upgraded node MUST still
	// restore them from its own snapshot or it does not start.
	for _, legacy := range []string{
		"logs/app.jsonl/2026-01", // directory segment equal to a derived name
		"d.meta.json/x",          // the F1 collision shape itself (already committed = already laid out)
		"a\tb",                   // control byte (legal file-name byte on every supported filesystem)
		"a\nb",                   // newline
		"a\x7fb",                 // DEL
		strings.Repeat("n", 250), // segment over MaxDocSegmentLen (db profile could commit it)
		strings.Repeat("n", 300), // over the 256-byte total cap
		"x.jsonl.bak/y",          // legal again under the six-suffix rule; must restore too
	} {
		if err := ValidDocName(legacy); err == nil && legacy != "x.jsonl.bak/y" {
			t.Fatalf("premise: ValidDocName(%q) should refuse this legacy shape", legacy)
		}
		if err := ValidDocNameForRestore(legacy); err != nil {
			t.Fatalf("ValidDocNameForRestore(%q) must accept a name the old rule committed (G1: the node would not start): %v", legacy, err)
		}
	}
	// The path-safety subset: what no snapshot may lay down.
	for _, bad := range []struct{ name, doc string }{
		{"empty", ""},
		{"NUL byte", "a\x00b"},
		{"dot segment", "a/./b"},
		{"dotdot segment", "a/../b"},
		{"leading dotdot", "../escape"},
		{"bare dotdot", ".."},
		{"bare dot", "."},
		{"empty segment", "a//b"},
		{"trailing slash", "a/"},
		{"absolute", "/etc/passwd"},
		{"backslash", `a\..\..\x`},
		{"colon", "a:b"},
		{"star", "a*b"},
		{"question", "a?b"},
		{"quote", `a"b`},
		{"angle", "a<b"},
		{"pipe", "a|b"},
	} {
		if err := ValidDocNameForRestore(bad.doc); err == nil {
			t.Fatalf("%s: ValidDocNameForRestore(%q) must be refused (path-unsafe)", bad.name, bad.doc)
		}
		if err := ValidDocName(bad.doc); err == nil {
			t.Fatalf("%s: ValidDocName(%q) must be refused too (restore ⊆ create)", bad.name, bad.doc)
		}
	}
	// Subset invariant, mechanically: restore never refuses what create accepts.
	for _, ok := range []string{"a", "dir/sub/file.json", "events.jsonl", "weird name with spaces", "..gitkeep", "a..b/c"} {
		if err := ValidDocName(ok); err != nil {
			t.Fatalf("premise: %q must be a legal creation name: %v", ok, err)
		}
		if err := ValidDocNameForRestore(ok); err != nil {
			t.Fatalf("restore refused a legal creation name %q: %v", ok, err)
		}
	}
}

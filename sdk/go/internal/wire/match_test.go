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
		strings.Repeat("n", 256), // exactly at the cap
	} {
		if err := ValidDocName(ok); err != nil {
			t.Fatalf("ValidDocName(%q) must be accepted: %v", ok, err)
		}
	}

	for _, bad := range []struct{ name, doc string }{
		{"empty", ""},
		{"over the cap", strings.Repeat("n", 257)},
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
}

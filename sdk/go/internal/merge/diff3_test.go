package merge

import (
	"fmt"
	"strings"
	"testing"
)

func TestNonOverlappingMergesClean(t *testing.T) {
	base := "line1\nline2\nline3\n"
	local := "LINE1 changed\nline2\nline3\n"  // edited top
	remote := "line1\nline2\nline3 changed\n" // edited bottom
	got, conflict := MergeText(base, local, remote)
	if conflict {
		t.Fatalf("non-overlapping edits must not conflict:\n%s", got)
	}
	if !strings.Contains(got, "LINE1 changed") || !strings.Contains(got, "line3 changed") {
		t.Fatalf("both edits must survive:\n%s", got)
	}
}

func TestOnlyOneSideChanged(t *testing.T) {
	base := "a\nb\nc\n"
	if got, c := MergeText(base, "a\nB\nc\n", base); c || got != "a\nB\nc" {
		t.Fatalf("local-only change: conflict=%v got=%q", c, got)
	}
	if got, c := MergeText(base, base, "a\nb\nC\n"); c || got != "a\nb\nC" {
		t.Fatalf("remote-only change: conflict=%v got=%q", c, got)
	}
}

func TestIdenticalChangeNoConflict(t *testing.T) {
	base := "x\ny\nz\n"
	same := "x\nY!\nz\n"
	if got, c := MergeText(base, same, same); c || got != "x\nY!\nz" {
		t.Fatalf("identical edits must not conflict: conflict=%v got=%q", c, got)
	}
}

func TestOverlappingConflicts(t *testing.T) {
	base := "one\ntwo\nthree\n"
	local := "one\nTWO-local\nthree\n"
	remote := "one\nTWO-remote\nthree\n"
	got, conflict := MergeText(base, local, remote)
	if !conflict {
		t.Fatalf("overlapping edits must conflict:\n%s", got)
	}
	for _, want := range []string{"<<<<<<< local", "TWO-local", "=======", "TWO-remote", ">>>>>>> remote"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing marker/content %q in:\n%s", want, got)
		}
	}
	// Unaffected lines stay clean.
	if !strings.HasPrefix(got, "one\n") || !strings.HasSuffix(got, "three") {
		t.Fatalf("surrounding lines should be intact:\n%s", got)
	}
}

func TestInsertionsBothSides(t *testing.T) {
	base := "a\nb\n"
	local := "a\nA-INS\nb\n"
	remote := "a\nb\nB-INS\n"
	got, conflict := MergeText(base, local, remote)
	if conflict {
		t.Fatalf("disjoint insertions must not conflict:\n%s", got)
	}
	if !strings.Contains(got, "A-INS") || !strings.Contains(got, "B-INS") {
		t.Fatalf("both insertions must survive:\n%s", got)
	}
}

// B-4: input too big to merge line by line is never reduced to ONE side.
func TestMergeTooBigNeverKeepsOneSide(t *testing.T) {
	lines := func(n int, mark string) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			if i == 0 {
				b.WriteString(mark)
			}
			fmt.Fprintf(&b, "line %d\n", i)
		}
		return b.String()
	}
	cases := map[string][3]string{
		"cells over the cap":         {lines(6000, ""), lines(6000, "L"), lines(6000, "R")},
		"one side over the line cap": {lines(maxMergeLines+1, ""), "local\n", "remote\n"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			merged, conflict, tooBig := MergeTextChecked(c[0], c[1], c[2])
			if !tooBig || !conflict || merged != "" {
				t.Fatalf("MergeTextChecked = (%d bytes, %v, %v), want (\"\", true, true)", len(merged), conflict, tooBig)
			}
			got, conflict := MergeText(c[0], c[1], c[2])
			if !conflict || !strings.Contains(got, strings.TrimSuffix(c[1], "\n")) || !strings.Contains(got, strings.TrimSuffix(c[2], "\n")) {
				t.Fatal("MergeText of a too-big input must hold BOTH sides (between markers), never one")
			}
		})
	}
	if _, _, tooBig := MergeTextChecked("a\nb\n", "a\nB\n", "A\nb\n"); tooBig {
		t.Fatal("a small input is merged")
	}
}

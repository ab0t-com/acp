//go:build security_regression

package crdt

import (
	"os"
	"strings"
	"testing"
)

// TestVulnW2_CIW26_DiffSizeCapped — CI-W2-6 (LOW/MED, twin of internal/merge's T38
// cap). GenerateOps runs an O(n*m) LCS matrix on the CLIENT; a large paste/file
// (~50k×50k ≈ 10GiB of ints) OOMs the caller. Proves the diff has a size guard AND
// that GenerateOps still produces a CORRECT edit script on normal input (guard) —
// a source assertion for the bound (R-C0 replaced the matrix with a linear-space search,
// and TestDiffWorstCaseStaysBounded runs the detonation for real) plus a live check.
func TestVulnW2_CIW26_DiffSizeCapped(t *testing.T) {
	src, err := os.ReadFile("diff.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "maxDiffEdits") || !strings.Contains(string(src), "maxDiffWork") {
		t.Fatalf("internal/crdt/diff() has no bound on its search (twin of T38): a large " +
			"GenerateOps input OOMs or hangs the client.")
	}
	// GUARD: a normal edit still round-trips (the cap must not break correct diffs).
	r := New("A")
	for _, op := range r.GenerateOps("hello world") {
		r.Apply(op)
	}
	if got := r.Text(); got != "hello world" {
		t.Fatalf("GUARD: GenerateOps produced a wrong document: %q != %q", got, "hello world")
	}
	for _, op := range r.GenerateOps("hello, brave world") {
		r.Apply(op)
	}
	if got := r.Text(); got != "hello, brave world" {
		t.Fatalf("GUARD: GenerateOps second edit wrong: %q", got)
	}
}

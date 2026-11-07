//go:build security_regression

package merge

import (
	"os"
	"strings"
	"testing"
)

// PROVES F-INV-38 (client MergeText runs an unguarded O(n*m) LCS -> a large conflicting file OOMs the CLI). RED until fixed.
// Evidence: internal/merge/diff3.go:92-124 (lcsMatches allocates a (len(x)+1)x(len(y)+1) int matrix with no size guard);
// cmd/acp/main.go:938-948 (acp pull calls MergeText on any both-sides-modified file, no size check).
// Static assertion: a hardened merge must cap input size before the quadratic matrix alloc (a real 50k-line detonation
// would allocate ~20GiB and OOM the test host, so we assert the guard exists rather than trigger it).
func TestVuln_FINV38_MergeTextGuardsSize(t *testing.T) {
	src, err := os.ReadFile("diff3.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	guarded := strings.Contains(s, "maxLines") || strings.Contains(s, "MaxMerge") ||
		strings.Contains(s, "maxMerge") || strings.Contains(s, "too large")
	if !guarded {
		t.Fatalf("internal/merge has no input-size guard before the O(n*m) LCS matrix (lcsMatches allocates " +
			"(n+1)x(m+1) ints): two collaborators editing a large file (or a co-tenant committing a ~50k-line file that " +
			"conflicts with a victim's local edit) OOMs the victim's `acp pull` CLI (~20GiB for 50k lines). Cap input " +
			"size and fall back to writing .remote above the threshold.")
	}
}

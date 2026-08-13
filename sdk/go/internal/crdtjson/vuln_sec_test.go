//go:build security_regression

package crdtjson

import (
	"os"
	"strings"
	"testing"
)

// PROVES F-INV-33 (crdtjson caps ops-per-push but has no per-DOCUMENT placement/size cap). RED until fixed.
// Evidence: internal/crdtjson/crdtjson.go:82 (MaxOps=4096, per-push only) + crdtjson_move.go:69-136 (resolveView is
// O(P log P) over all placements, rebuilt on every op) + crdtjsonstore.go:806-818 (dead-fraction compaction gate an
// all-live doc never trips) — so total placements P grow unbounded and every op/read rebuilds the full view.
func TestVuln_FINV33_JSONDocPlacementCap(t *testing.T) {
	src, err := os.ReadFile("crdtjson.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	// A hardened doc has a per-document placement/size ceiling independent of the dead-fraction gate.
	hasCap := strings.Contains(s, "MaxPlacements") || strings.Contains(s, "maxPlacements") ||
		strings.Contains(s, "MaxLive") || strings.Contains(s, "maxLive") || strings.Contains(s, "MaxDocOps")
	if !hasCap {
		t.Fatalf("crdtjson caps ops-per-push (MaxOps=4096) but has NO per-document placement/size cap: an all-live doc " +
			"never trips the dead-fraction compaction gate, so total placements P grow unbounded and every op rebuilds " +
			"the O(P log P) view (up to 4096x within one batch, on the FSM goroutine). Add a per-doc placement/size cap " +
			"or a size-based compaction trigger.")
	}
}

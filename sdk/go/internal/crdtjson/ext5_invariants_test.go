package crdtjson

import "testing"

// D4 SPEC-NON-REGRESSION exemplar (TESTING_SOP.md §4): ext-5's named INVARIANT
// VECTORS. A new extension MUST re-run these against its build to prove it did not
// silently weaken a guarantee ext-5 makes. ext-14 removed INV-Resident (the v0.1.4
// resource regression) — under this file that would have FAILED in CI, before any
// deploy. This is the copyable template for the standing rule: "each ext enumerates
// the invariants it assumes from core/prior exts and ships a vector for each; a new
// ext's gate re-runs all prior exts' vectors."

// INV-Convergence — two agents editing DISJOINT keys converge, order-independent
// (ext-5 §1.1; RFC worked example C.1). The foundational correctness invariant.
func TestExt5Invariant_DisjointKeysConverge(t *testing.T) {
	leader := New()
	a := commit(t, leader, "A", 1, set([]any{"status"}, `"green"`))
	b := commit(t, leader, "B", 2, set([]any{"owner"}, `"codexB"`))
	allOrders(t, `{"owner":"codexB","status":"green"}`, a, b)
}

// INV-CompactionTransparency — compacting a document never changes its materialized
// value (ext-5 §5.8; ext-14 §3.5): materialize(ops) == materialize(compact(ops)).
func TestExt5Invariant_CompactionTransparency(t *testing.T) {
	d := hammerOneKey(500) // 500 writes to one key; a single live winner
	before := string(canon(t, d))
	r := New()
	applyAll(r, d.SnapshotOps()) // replay the compacted snapshot on a fresh doc
	if after := string(canon(t, r)); after != before {
		t.Fatalf("INV-CompactionTransparency violated: %s != %s", after, before)
	}
}

// INV-Resident — a document's resident placement count is bounded by LIVE content,
// NOT by edit history. THIS is the invariant ext-14 removed and the v0.1.4 OOM
// rode. Kept as a named vector so any future ext that touches the CRDT re-runs it.
func TestExt5Invariant_ResidentBoundedByLiveContent(t *testing.T) {
	d := hammerOneKey(5000)
	if snap := len(d.SnapshotOps()); snap > 8 {
		t.Fatalf("INV-Resident violated: a 5000-write key compacts to %d ops (want O(live) <= 8) — a change removed reclamation", snap)
	}
}

package crdtjson

import (
	"encoding/json"
	"testing"

	"github.com/ab0t-com/acp/sdk/go/internal/crdt"
)

// The RESOURCE gate — the tests whose absence let the v0.1.4 OOM ship (ticket
// crdtjson-scale-fix-20260724). The rest of the suite proves CONVERGENCE /
// correctness; these prove BOUNDED memory + read cost. A CRDT that never reclaims
// superseded state still converges perfectly — it is correct and unbounded at
// once — so a resource assertion is the only thing that catches this class.

// hammerOneKey returns a doc with n LIVE bindings under key "k" (n sets, no del —
// strip was removed in ext-14, so every set appends a live binding; exactly the
// poll-and-overwrite shape whose retention ext-14 stopped reclaiming). Only the
// greatest-stamped binding renders; the doc's LIVE content stays O(1).
//
// Built by applying canonical ops DIRECTLY (the FSM's path) rather than through
// Canonicalize — Canonicalize clones the whole doc per write (E7), which is O(P^2)
// for a bulk build and would make this gate test take ~100s. Direct Apply is O(P).
func hammerOneKey(n int) *Doc {
	d := New()
	for i := 1; i <= n; i++ {
		d.Apply(Op{
			T: OpSet, ID: crdt.ID{Clock: uint64(2 * i), Replica: "a"},
			Target: Root, Key: "k", NID: crdt.ID{Clock: uint64(2*i + 1), Replica: "a"},
			Value: json.RawMessage(`"v"`), Now: int64(i), Actor: "a",
		})
	}
	return d
}

// TestReadCostCeiling_FlatInP — Fix A: a warm (memoized) read is O(1), NOT O(P).
// resolveView is a pure function of the op set and is cached, invalidated only by
// integrate. WITHOUT the memoization this fails: warm-read allocations scale with
// the placement count P — the exact regression that produced ~1 TB of transient
// garbage over 115k prod reads and drove RSS to GiB.
func TestReadCostCeiling_FlatInP(t *testing.T) {
	warmAllocs := func(P int) float64 {
		d := hammerOneKey(P)
		_ = d.Materialize()                                             // warm the view cache
		return testing.AllocsPerRun(50, func() { _ = d.Materialize() }) // steady-state
	}
	small := warmAllocs(1000)
	big := warmAllocs(5000)
	// A warm read renders O(live)=O(1) here (one key), independent of P.
	const ceiling = 80 // headroom over the true O(1) cost; catches an order-of-magnitude regression, not GC noise
	if small > ceiling {
		t.Fatalf("warm-read allocs at P=1000 = %.0f (want <= %d, O(1)) — resolveView not memoized? (Fix A regressed)", small, ceiling)
	}
	// 5x the placements must NOT ~5x the warm-read cost. Flat-in-P is the property.
	if big > small*2 {
		t.Fatalf("warm-read allocs scale with P: P=1000 -> %.0f, P=5000 -> %.0f (want ~flat) — resolveView memoization broken", small, big)
	}
}

// TestRetentionCompactable_BoundedByLiveContent — the working-set invariant ext-14
// broke: resident placements grow with write history, but that history is FULLY
// reclaimable to live content. A key hammered 5000x compacts to a single op, and a
// replay of that snapshot has O(live) resident bindings. This is what the store's
// dead-fraction compaction trigger (Fix C) leans on to bound in-memory P — no
// naive strip, no change to arbitration.
func TestRetentionCompactable_BoundedByLiveContent(t *testing.T) {
	d := hammerOneKey(5000)
	if bs := len(d.nodes[Root].bindings["k"]); bs < 5000 {
		t.Fatalf("expected ~5000 retained bindings under 'k', got %d (test shape wrong)", bs)
	}
	// The compacted snapshot is bounded by LIVE content (one key -> ~one op),
	// NOT by the 5000 writes that preceded it.
	snap := d.SnapshotOps()
	const snapCeiling = 8 // one live key + headroom
	if len(snap) > snapCeiling {
		t.Fatalf("snapshot of a 5000x-hammered key = %d ops (want <= %d, O(live)) — retention not compactable", len(snap), snapCeiling)
	}
	// Replaying the snapshot reproduces the value with O(live) resident placements.
	r := New()
	applyAll(r, snap)
	if got := string(canon(t, r)); got != `{"k":"v"}` {
		t.Fatalf("compacted replay = %s, want {\"k\":\"v\"} (compaction transparency)", got)
	}
	if bs := len(r.nodes[Root].bindings["k"]); bs > snapCeiling {
		t.Fatalf("post-compaction resident bindings under 'k' = %d (want <= %d) — resident not bounded by live content", bs, snapCeiling)
	}
}

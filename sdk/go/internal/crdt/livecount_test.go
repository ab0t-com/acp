package crdt

import (
	"fmt"
	"math/rand"
	"testing"
)

// LiveCount is an incrementally-maintained counter with ONE invariant:
//
//	live == len(SnapshotOps())
//
// It exists so the daemon's compaction predicate is O(1) per doc instead of
// building an O(P) snapshot under a store lock on every sweeper tick (review
// finding F1). That makes the counter load-bearing for a REPLICATED decision:
// if it drifts, the leader's gate and every replica's apply disagree about
// which docs to compact — and a compaction that fires on one node and not
// another is an acp-1 §17 divergence. So it is tested as a property (against
// the authoritative snapshot, under randomized streams), not by example.

// assertLive checks the invariant and reports both numbers on failure.
func assertLive(t *testing.T, r *RGA, whenf string, args ...any) {
	t.Helper()
	want := len(r.SnapshotOps())
	if got := r.LiveCount(); got != want {
		t.Fatalf("LiveCount drift %s: LiveCount()=%d but len(SnapshotOps())=%d", fmt.Sprintf(whenf, args...), got, want)
	}
}

// TestLiveCountTracksBasicLifecycle walks the invariant through every transition
// a single element can make: absent -> inserted -> deleted -> re-deleted, plus
// the empty document and a delete for an element that never existed.
func TestLiveCountTracksBasicLifecycle(t *testing.T) {
	r := New("A")
	if r.LiveCount() != 0 {
		t.Fatalf("a fresh doc has %d live elements, want 0", r.LiveCount())
	}
	assertLive(t, r, "on an empty doc")

	ops := r.GenerateOps("hello")
	if r.LiveCount() != 5 {
		t.Fatalf("after inserting 5 runes LiveCount=%d, want 5", r.LiveCount())
	}
	assertLive(t, r, "after inserts")

	// Delete one element; the counter drops by exactly one.
	var firstInsert Op
	for _, op := range ops {
		if op.Type == OpInsert {
			firstInsert = op
			break
		}
	}
	r.Apply(Op{Type: OpDelete, ID: firstInsert.ID})
	if r.LiveCount() != 4 {
		t.Fatalf("after one delete LiveCount=%d, want 4", r.LiveCount())
	}
	assertLive(t, r, "after one delete")

	// IDEMPOTENCY: re-delivering the same delete (routine under at-least-once
	// replication and on op-log replay) must NOT double-decrement.
	for i := 0; i < 3; i++ {
		r.Apply(Op{Type: OpDelete, ID: firstInsert.ID})
	}
	if r.LiveCount() != 4 {
		t.Fatalf("re-applying the same delete drove LiveCount to %d, want 4 (double-decrement)", r.LiveCount())
	}
	assertLive(t, r, "after duplicate deletes")

	// Re-delivering the same INSERT must not double-increment either.
	for i := 0; i < 3; i++ {
		r.Apply(firstInsert)
	}
	assertLive(t, r, "after duplicate inserts")

	// Deleting everything leaves zero live but a non-empty op-log.
	r.GenerateOps("")
	if r.LiveCount() != 0 {
		t.Fatalf("after deleting all text LiveCount=%d, want 0", r.LiveCount())
	}
	assertLive(t, r, "after deleting everything")
	if r.Text() != "" {
		t.Fatalf("text=%q, want empty", r.Text())
	}
}

// TestLiveCountMatchesSnapshot is the property test the invariant comment
// promises: randomized edit streams (grow, shrink, replace, clear), asserting
// after EVERY step. Seeded so a failure is reproducible.
func TestLiveCountMatchesSnapshot(t *testing.T) {
	words := []string{"", "a", "hello", "hello world", "goodbye", "hello brave new world", "x", "the quick brown fox"}
	for seed := int64(0); seed < 8; seed++ {
		rng := rand.New(rand.NewSource(seed))
		r := New(fmt.Sprintf("R%d", seed))
		for step := 0; step < 40; step++ {
			r.GenerateOps(words[rng.Intn(len(words))])
			assertLive(t, r, "seed=%d step=%d text=%q", seed, step, r.Text())
		}
		// Text() and LiveCount() must also agree — both count visible elements.
		if got := len([]rune(r.Text())); got != r.LiveCount() {
			t.Fatalf("seed=%d: len(Text())=%d but LiveCount()=%d", seed, got, r.LiveCount())
		}
	}
}

// TestLiveCountUnderConcurrentMerge covers the path the single-replica tests
// cannot: ops arriving out of order and via the PENDING buffer (a delete or an
// insert whose causal predecessor has not arrived yet, later drained by
// drainPending). Buffered ops must be counted exactly once, when they finally
// integrate — never on arrival, never twice.
func TestLiveCountUnderConcurrentMerge(t *testing.T) {
	a, b := New("A"), New("B")
	aOps := a.GenerateOps("alpha")
	bOps := b.GenerateOps("beta")

	// Deliver each side's ops to the other in REVERSE order, so most arrive
	// before their origin and take the pending path.
	rev := func(ops []Op) []Op {
		out := make([]Op, len(ops))
		for i, op := range ops {
			out[len(ops)-1-i] = op
		}
		return out
	}
	for _, op := range rev(bOps) {
		a.Apply(op)
	}
	for _, op := range rev(aOps) {
		b.Apply(op)
	}
	assertLive(t, a, "on A after out-of-order merge")
	assertLive(t, b, "on B after out-of-order merge")
	if a.Text() != b.Text() {
		t.Fatalf("replicas did not converge: %q vs %q", a.Text(), b.Text())
	}
	if a.LiveCount() != b.LiveCount() {
		t.Fatalf("converged replicas disagree on LiveCount: %d vs %d — the compaction gate would fire on one node and not the other (§17)",
			a.LiveCount(), b.LiveCount())
	}

	// Now a concurrent delete from each side, delivered twice and out of order.
	aDel := a.GenerateOps("")
	for _, op := range append(rev(aDel), aDel...) {
		b.Apply(op)
	}
	assertLive(t, a, "on A after clearing")
	assertLive(t, b, "on B after receiving the clear twice")
	if a.LiveCount() != b.LiveCount() {
		t.Fatalf("duplicate delivery skewed LiveCount: A=%d B=%d", a.LiveCount(), b.LiveCount())
	}
}

// TestLiveCountSurvivesSnapshotRoundTrip: Load rebuilds els from persisted state,
// so it must rebuild the counter too. A restored doc that reported live=0 would
// make every doc look infinitely compactible after a restart (nOps > factor*1),
// firing a rewrite of every doc on the first sweeper tick.
func TestLiveCountSurvivesSnapshotRoundTrip(t *testing.T) {
	r := New("A")
	r.GenerateOps("hello world")
	r.GenerateOps("hello")  // deletes 6 runes -> tombstones present
	before := r.LiveCount() // 5

	restored := Load(r.Snapshot())
	if restored.LiveCount() != before {
		t.Fatalf("Load rebuilt LiveCount as %d, want %d (tombstones must not be counted as live)", restored.LiveCount(), before)
	}
	assertLive(t, restored, "after Load")
	if restored.Text() != r.Text() {
		t.Fatalf("restored text %q != %q", restored.Text(), r.Text())
	}

	// And the restored doc keeps the counter correct as editing continues.
	restored.GenerateOps("hello there")
	assertLive(t, restored, "after editing a restored doc")
}

// TestLiveCountIsConstantTime is the D2 assertion behind the F1 fix: reading the
// live count must not allocate, and must not get more expensive as the document
// grows. The old predicate built a SnapshotOps slice (one Op per live element)
// on every call — that is exactly what this forbids.
func TestLiveCountIsConstantTime(t *testing.T) {
	build := func(n int) *RGA {
		r := New("A")
		txt := ""
		for i := 0; i < n; i++ {
			txt += "x"
		}
		r.GenerateOps(txt)
		return r
	}
	small, large := build(50), build(2000)
	if a := testing.AllocsPerRun(100, func() { _ = small.LiveCount() }); a != 0 {
		t.Fatalf("LiveCount allocates %v times on a small doc, want 0", a)
	}
	if a := testing.AllocsPerRun(100, func() { _ = large.LiveCount() }); a != 0 {
		t.Fatalf("LiveCount allocates %v times on a large doc, want 0 (it must not build a snapshot)", a)
	}
	// Sanity: the large doc really is large, so the zero above is meaningful.
	if large.LiveCount() != 2000 {
		t.Fatalf("large doc LiveCount=%d, want 2000", large.LiveCount())
	}
}

// TestLiveCountDrainedDeleteIsCounted is the inverted proof of HZ-ACPDB-44 (Docs
// investigation D3): a delete that arrives BEFORE its target is buffered, then drained
// by drainPending when the insert lands. drainPending's delete branch used to set the
// tombstone without live--, so every such delete left LiveCount one too high — and
// Go disagreed with the TS engine, which decrements on drain. The counter must be
// exact in every delivery order, and a re-delivered delete must not double-count.
func TestLiveCountDrainedDeleteIsCounted(t *testing.T) {
	x := ID{Clock: 1, Replica: "a"}
	y := ID{Clock: 2, Replica: "a"}
	ins := func(id, origin ID, v string) Op { return Op{Type: OpInsert, ID: id, OriginLeft: origin, Val: v} }
	del := func(id ID) Op { return Op{Type: OpDelete, ID: id} }

	// 1. delete first, then its target: integrates (live++) and drains (live--).
	r := New("s")
	if r.Apply(del(x)) {
		t.Fatal("a delete of an absent target must buffer, not apply")
	}
	r.Apply(ins(x, Zero, "x"))
	if r.Text() != "" || r.LiveCount() != 0 || r.PendingLen() != 0 {
		t.Fatalf("after drain: text=%q LiveCount=%d pending=%d, want \"\"/0/0", r.Text(), r.LiveCount(), r.PendingLen())
	}
	assertLive(t, r, "after a drained delete")
	// Re-delivery of the same delete (at-least-once channels, op-log replay) is a no-op.
	r.Apply(del(x))
	assertLive(t, r, "after re-delivering the drained delete")
	if r.LiveCount() != 0 {
		t.Fatalf("re-delivered delete drove LiveCount to %d, want 0", r.LiveCount())
	}

	// 2. a chain where BOTH the insert and the delete of y wait on x: x lands last.
	r2 := New("s")
	r2.Apply(ins(y, x, "y")) // origin x absent -> buffered
	r2.Apply(del(y))         // target y absent -> buffered
	r2.Apply(ins(x, Zero, "x"))
	if r2.Text() != "x" || r2.PendingLen() != 0 {
		t.Fatalf("chain: text=%q pending=%d, want \"x\"/0", r2.Text(), r2.PendingLen())
	}
	assertLive(t, r2, "after draining an insert and its delete together")
	if r2.LiveCount() != 1 {
		t.Fatalf("chain: LiveCount=%d, want 1", r2.LiveCount())
	}

	// 3. a delete that never finds its target stays buffered and never touches the counter.
	r3 := New("s")
	r3.GenerateOps("ab")
	r3.Apply(del(ID{Clock: 99, Replica: "ghost"}))
	assertLive(t, r3, "with a delete still buffered")
	if r3.LiveCount() != 2 || r3.PendingLen() != 1 {
		t.Fatalf("ghost delete: LiveCount=%d pending=%d, want 2/1", r3.LiveCount(), r3.PendingLen())
	}

	// 4. the same op set in every order converges on the same count (the §17 property
	// the compaction gate needs: replicas that saw different orders agree).
	ops := []Op{ins(x, Zero, "x"), ins(y, x, "y"), del(x), del(y)}
	perms := [][]int{{0, 1, 2, 3}, {3, 2, 1, 0}, {2, 0, 3, 1}, {1, 3, 0, 2}, {3, 1, 2, 0}}
	for _, p := range perms {
		rp := New("p")
		for _, i := range p {
			rp.Apply(ops[i])
		}
		assertLive(t, rp, "order %v", p)
		if rp.LiveCount() != 0 || rp.PendingLen() != 0 {
			t.Fatalf("order %v: LiveCount=%d pending=%d, want 0/0", p, rp.LiveCount(), rp.PendingLen())
		}
	}
}

// TestLiveCountUnderBufferedDeletes closes the gap TestLiveCountUnderConcurrentMerge
// left: its streams were inserts (and deletes of already-present targets), so the
// delete branch of drainPending was never exercised and the drift survived. Here each
// side's deletes are delivered BEFORE the inserts they target, under duplicates.
func TestLiveCountUnderBufferedDeletes(t *testing.T) {
	a, b := New("A"), New("B")
	aIns := a.GenerateOps("alpha")
	aDel := a.GenerateOps("ala") // tombstones p,h
	bIns := b.GenerateOps("beta")
	bDel := b.GenerateOps("ba") // tombstones e,t
	// Deletes first (buffered), twice; then the inserts, reversed (mostly buffered).
	for _, stream := range [][]Op{aDel, aDel, rev(aIns)} {
		for _, op := range stream {
			b.Apply(op)
		}
	}
	for _, stream := range [][]Op{bDel, bDel, rev(bIns)} {
		for _, op := range stream {
			a.Apply(op)
		}
	}
	assertLive(t, a, "on A after buffered deletes")
	assertLive(t, b, "on B after buffered deletes")
	if a.PendingLen() != 0 || b.PendingLen() != 0 {
		t.Fatalf("pending not drained: A=%d B=%d", a.PendingLen(), b.PendingLen())
	}
	if a.Text() != b.Text() || a.LiveCount() != b.LiveCount() {
		t.Fatalf("replicas disagree: A=%q/%d B=%q/%d", a.Text(), a.LiveCount(), b.Text(), b.LiveCount())
	}
	if a.LiveCount() != 5 { // "ala" + "ba"
		t.Fatalf("LiveCount=%d, want 5", a.LiveCount())
	}
}

func rev(ops []Op) []Op {
	out := make([]Op, len(ops))
	for i, op := range ops {
		out[len(ops)-1-i] = op
	}
	return out
}

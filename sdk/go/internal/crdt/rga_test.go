package crdt

import (
	"math/rand"
	"testing"
)

// applyAll applies ops in the given order (handles out-of-order via buffering).
func applyAll(r *RGA, ops []Op) {
	for _, op := range ops {
		r.Apply(op)
	}
}

func TestDiffRoundTrip(t *testing.T) {
	cases := []struct{ from, to string }{
		{"", "hello"},
		{"hello", "hello world"},
		{"hello world", "goodbye world"},
		{"abcdef", "abXcdYef"},
		{"the quick brown fox", "the slow brown dog"},
		{"line1\nline2\n", "line1\nLINE2\nline3\n"},
		{"func main(){}", "func main(){ println(1) }"},
		{"keep", ""},
	}
	for _, c := range cases {
		r := New("A")
		r.Apply(Op{}) // no-op safety
		applyAll(r, r.GenerateOps(c.from))
		if got := r.Text(); got != c.from {
			t.Fatalf("after from-edit: got %q want %q", got, c.from)
		}
		r.GenerateOps(c.to)
		if got := r.Text(); got != c.to {
			t.Fatalf("after to-edit: got %q want %q", got, c.to)
		}
	}
}

func TestIdempotentAndOrderIndependent(t *testing.T) {
	src := New("A")
	ops := src.GenerateOps("the quick brown fox jumps")

	// Replay in many shuffled orders + duplicates; every replica must converge.
	want := src.Text()
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		dup := append([]Op{}, ops...)
		dup = append(dup, ops...) // duplicates -> idempotency check
		rng.Shuffle(len(dup), func(i, j int) { dup[i], dup[j] = dup[j], dup[i] })
		r := New("B")
		applyAll(r, dup)
		if got := r.Text(); got != want {
			t.Fatalf("trial %d diverged: got %q want %q", trial, got, want)
		}
		if len(r.pending) != 0 {
			t.Fatalf("trial %d left %d pending ops", trial, len(r.pending))
		}
	}
}

// The classic convergence test: two replicas edit concurrently from a shared
// base, exchange ops, and must end identical regardless of who's "first".
func TestConcurrentEditsConverge(t *testing.T) {
	base := New("seed")
	baseOps := base.GenerateOps("Hello World")

	r1, r2 := New("R1"), New("R2")
	applyAll(r1, baseOps)
	applyAll(r2, baseOps)

	// Concurrent, conflicting edits at overlapping regions.
	ops1 := r1.GenerateOps("Hello Brave World")    // R1 inserts "Brave "
	ops2 := r2.GenerateOps("Hello World of CRDTs") // R2 appends

	// Cross-apply.
	applyAll(r1, ops2)
	applyAll(r2, ops1)

	if r1.Text() != r2.Text() {
		t.Fatalf("replicas diverged:\n R1=%q\n R2=%q", r1.Text(), r2.Text())
	}
	// Both edits must survive the merge (no silent loss).
	merged := r1.Text()
	for _, frag := range []string{"Brave", "CRDTs"} {
		if !contains(merged, frag) {
			t.Fatalf("merge lost %q: got %q", frag, merged)
		}
	}
}

// Concurrent inserts at the very same position converge deterministically.
func TestConcurrentInsertSamePosition(t *testing.T) {
	base := New("seed")
	baseOps := base.GenerateOps("AC")
	r1, r2 := New("R1"), New("R2")
	applyAll(r1, baseOps)
	applyAll(r2, baseOps)

	o1 := r1.GenerateOps("AXC") // insert X between A and C
	o2 := r2.GenerateOps("AYC") // insert Y between A and C
	applyAll(r1, o2)
	applyAll(r2, o1)

	if r1.Text() != r2.Text() {
		t.Fatalf("diverged: R1=%q R2=%q", r1.Text(), r2.Text())
	}
	if m := r1.Text(); m != "AXYC" && m != "AYXC" {
		t.Fatalf("unexpected merge %q (want AXYC or AYXC)", m)
	}
}

// Out-of-order delivery: ops referencing not-yet-seen origins must buffer then
// resolve, ending identical to in-order delivery.
func TestOutOfOrderDelivery(t *testing.T) {
	src := New("A")
	ops := src.GenerateOps("abcdefghij")
	want := src.Text()

	// reverse order forces every insert's origin to arrive after it
	rev := make([]Op, len(ops))
	for i := range ops {
		rev[i] = ops[len(ops)-1-i]
	}
	r := New("B")
	applyAll(r, rev)
	if got := r.Text(); got != want {
		t.Fatalf("out-of-order diverged: got %q want %q (pending=%d)", got, want, len(r.pending))
	}
}

// Snapshot/Load round-trips and continues to converge.
func TestSnapshotRoundTrip(t *testing.T) {
	r := New("A")
	applyAll(r, r.GenerateOps("persisted document"))
	r2 := Load(r.Snapshot())
	if r2.Text() != r.Text() {
		t.Fatalf("snapshot mismatch: %q vs %q", r2.Text(), r.Text())
	}
	r2.GenerateOps("persisted document v2")
	if r2.Text() != "persisted document v2" {
		t.Fatalf("post-load edit failed: %q", r2.Text())
	}
}

// Fuzz-ish: random concurrent edit rounds must always converge.
func TestRandomConcurrentConverges(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 50; trial++ {
		base := New("seed")
		baseOps := base.GenerateOps(randText(rng, 20))
		r1, r2 := New("R1"), New("R2")
		applyAll(r1, baseOps)
		applyAll(r2, baseOps)

		var all1, all2 [][]Op
		for round := 0; round < 4; round++ {
			all1 = append(all1, r1.GenerateOps(mutate(rng, r1.Text())))
			all2 = append(all2, r2.GenerateOps(mutate(rng, r2.Text())))
		}
		for _, o := range all2 {
			applyAll(r1, o)
		}
		for _, o := range all1 {
			applyAll(r2, o)
		}
		if r1.Text() != r2.Text() {
			t.Fatalf("trial %d diverged:\n R1=%q\n R2=%q", trial, r1.Text(), r2.Text())
		}
	}
}

// Compaction via SnapshotOps must reproduce the exact text, and a replica that
// later receives the original concurrent ops must still converge.
func TestSnapshotOpsCompaction(t *testing.T) {
	r := New("A")
	applyAll(r, r.GenerateOps("hello world"))
	r.GenerateOps("hello brave world")     // edit
	r.GenerateOps("hello brave new world") // edit again (history now long)
	want := r.Text()

	// "Compact": rebuild a fresh replica purely from the snapshot ops.
	snap := r.SnapshotOps()
	compacted := New("B")
	applyAll(compacted, snap)
	if compacted.Text() != want {
		t.Fatalf("snapshot lost state: got %q want %q", compacted.Text(), want)
	}

	// A peer that had diverged with its own concurrent edit still converges when
	// fed the snapshot (ops commute / IDs preserved).
	peer := New("C")
	applyAll(peer, snap)
	peerOps := peer.GenerateOps(want + " (peer note)")
	compacted2 := New("D")
	applyAll(compacted2, snap)
	applyAll(compacted2, peerOps)
	applyAll(peer, snap) // idempotent
	if compacted2.Text() != peer.Text() {
		t.Fatalf("post-compaction diverged: %q vs %q", compacted2.Text(), peer.Text())
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func randText(rng *rand.Rand, n int) string {
	const al = "abcde \n"
	b := make([]byte, rng.Intn(n)+1)
	for i := range b {
		b[i] = al[rng.Intn(len(al))]
	}
	return string(b)
}

func mutate(rng *rand.Rand, s string) string {
	r := []rune(s)
	switch rng.Intn(3) {
	case 0: // insert
		pos := rng.Intn(len(r) + 1)
		r = append(r[:pos], append([]rune{rune('A' + rng.Intn(5))}, r[pos:]...)...)
	case 1: // delete
		if len(r) > 0 {
			pos := rng.Intn(len(r))
			r = append(r[:pos], r[pos+1:]...)
		}
	case 2: // replace
		if len(r) > 0 {
			r[rng.Intn(len(r))] = rune('a' + rng.Intn(5))
		}
	}
	return string(r)
}

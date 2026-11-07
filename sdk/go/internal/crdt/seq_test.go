package crdt

// R-C21: the order-statistic list (seq.go) against the slice RGA it replaced
// (rga_ref_test.go, kept verbatim). Every result must be byte-identical, and a replay
// must cost about n log n. Pure, in-memory, deterministic seeds.

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// both runs one step on the new RGA and the reference and fails on any difference.
type both struct {
	t   *testing.T
	a   *RGA
	b   *refRGA
	tag string
}

func (p both) same(step string) {
	p.t.Helper()
	sa, sb := p.a.Snapshot(), p.b.Snapshot()
	ja, _ := json.Marshal(sa)
	jb, _ := json.Marshal(sb)
	if string(ja) != string(jb) {
		p.t.Fatalf("%s %s: snapshots differ\nnew %s\nref %s", p.tag, step, ja, jb)
	}
	if p.a.Text() != p.b.Text() || p.a.LiveCount() != p.b.LiveCount() || p.a.PendingLen() != p.b.PendingLen() ||
		p.a.Clock() != p.b.Clock() {
		p.t.Fatalf("%s %s: text/live/pending/clock differ", p.tag, step)
	}
	if !reflect.DeepEqual(p.a.SnapshotOps(), p.b.SnapshotOps()) {
		p.t.Fatalf("%s %s: SnapshotOps differ", p.tag, step)
	}
}

func (p both) apply(op Op) {
	p.t.Helper()
	if x, y := p.a.Apply(op), p.b.Apply(op); x != y {
		p.t.Fatalf("%s: Apply(%+v) = %v, ref %v", p.tag, op, x, y)
	}
	if x, y := p.a.Knows(op.ID), p.b.Knows(op.ID); x != y {
		p.t.Fatalf("%s: Knows(%+v) = %v, ref %v", p.tag, op.ID, x, y)
	}
}

// randomOps is a hostile mix: several replicas inserting at random places (many at one
// place, so the YATA rule steps over siblings), deletes, re-deletes, deletes of unknown
// and zero ids, duplicates, inserts at a zero id, and the whole set delivered out of order.
func randomOps(rng *rand.Rand, n int) []Op {
	reps := []string{"a", "b", "c", "a/x"}
	var ids []ID
	var ops []Op
	clock := uint64(0)
	for len(ops) < n {
		switch k := rng.Intn(20); {
		case k < 12 || len(ids) == 0:
			clock += uint64(rng.Intn(3)) // equal clocks across replicas happen
			id := ID{Clock: clock + 1, Replica: reps[rng.Intn(len(reps))]}
			origin := Zero
			if len(ids) > 0 && rng.Intn(6) != 0 {
				if rng.Intn(3) == 0 {
					origin = ids[len(ids)-1] // typing
				} else {
					origin = ids[rng.Intn(len(ids))]
				}
			}
			ids = append(ids, id)
			ops = append(ops, Op{Type: OpInsert, ID: id, OriginLeft: origin, Val: string(rune('a' + rng.Intn(26)))})
		case k < 17:
			ops = append(ops, Op{Type: OpDelete, ID: ids[rng.Intn(len(ids))]})
		case k == 17:
			ops = append(ops, ops[rng.Intn(len(ops))]) // a duplicate
		case k == 18:
			ops = append(ops, Op{Type: OpDelete, ID: ID{Clock: 1 << 40, Replica: "ghost"}}, Op{Type: OpDelete, ID: Zero})
		default:
			ops = append(ops, Op{Type: OpInsert, ID: Zero, OriginLeft: Zero, Val: "z"}) // a zero id
		}
	}
	return ops
}

func TestSeqMatchesTheSliceRGA(t *testing.T) {
	for seed := int64(1); seed <= 60; seed++ {
		rng := rand.New(rand.NewSource(seed))
		ops := randomOps(rng, 400)
		if seed%3 == 0 {
			rng.Shuffle(len(ops), func(i, j int) { ops[i], ops[j] = ops[j], ops[i] }) // out of order: the pending buffer
		}
		p := both{t: t, a: New("me"), b: refNew("me"), tag: fmt.Sprintf("seed %d", seed)}
		for i, op := range ops {
			p.apply(op)
			if i%37 == 0 {
				p.same(fmt.Sprintf("op %d", i))
			}
		}
		p.same("end")
		// A local edit on top, a round trip through a state, and an edit on the loaded copy.
		text := p.a.Text()
		edit := text
		if len(edit) > 4 {
			edit = edit[:len(edit)/3] + "NEW" + edit[len(edit)/2:]
		}
		if x, y := p.a.GenerateOps(edit), p.b.GenerateOps(edit); !reflect.DeepEqual(x, y) {
			t.Fatalf("seed %d: GenerateOps differ", seed)
		}
		p.same("after GenerateOps")
		la, lb := Load(p.a.Snapshot()), refLoad(p.b.Snapshot())
		q := both{t: t, a: la, b: lb, tag: p.tag + " loaded"}
		q.same("loaded")
		if x, y := la.GenerateOps(strings.ToUpper(edit)), lb.GenerateOps(strings.ToUpper(edit)); !reflect.DeepEqual(x, y) {
			t.Fatalf("seed %d: GenerateOps on a loaded copy differ", seed)
		}
		q.same("loaded edit")
		for _, op := range randomOps(rng, 60) {
			q.apply(op)
		}
		q.same("loaded, more ops")
		// LogReplaced reads an element by id.
		for _, op := range ops[:min(len(ops), 30)] {
			for _, alt := range []Op{op, {Type: op.Type, ID: op.ID, Val: "?"}} {
				if x, y := la.LogReplaced([]Op{alt}, 10, 5), lb.LogReplaced([]Op{alt}, 10, 5); x != y {
					t.Fatalf("seed %d: LogReplaced(%+v) = %v, ref %v", seed, alt, x, y)
				}
			}
		}
	}
}

// A state that repeats an id (only a crafted one can) resolves to its first element, as
// the scan did.
func TestSeqLoadOfARepeatedIDMatches(t *testing.T) {
	st := State{Replica: "me", Clock: 9, Els: []ElemWire{
		{ID: ID{1, "a"}, Val: "x"}, {ID: ID{2, "a"}, OriginLeft: ID{1, "a"}, Val: "y"},
		{ID: ID{1, "a"}, Val: "q"}, {ID: ID{3, "a"}, OriginLeft: ID{9, "zz"}, Val: "w"},
	}}
	p := both{t: t, a: Load(st), b: refLoad(st), tag: "repeated"}
	p.same("loaded")
	for _, op := range []Op{
		{Type: OpInsert, ID: ID{4, "b"}, OriginLeft: ID{1, "a"}, Val: "n"},
		{Type: OpDelete, ID: ID{1, "a"}},
		{Type: OpInsert, ID: ID{5, "b"}, OriginLeft: Zero, Val: "m"},
		{Type: OpInsert, ID: ID{6, "b"}, OriginLeft: ID{3, "a"}, Val: "k"},
	} {
		p.apply(op)
		p.same(fmt.Sprintf("%+v", op))
	}
}

// The work of a replay grows as n log n, not n²: counted in nodes visited, never in time
// (R-C21 guard). Three shapes: typing at the end (a live document's log), inserts at random
// places, and a run typed backwards at the start (every insert has the zero origin).
func TestReplayWorkIsNLogN(t *testing.T) {
	const n = 70_000
	rng := rand.New(rand.NewSource(7))
	shapes := map[string]func(i int, ids []ID) ID{
		"typing":   func(i int, ids []ID) ID { return ids[len(ids)-1] },
		"random":   func(i int, ids []ID) ID { return ids[rng.Intn(len(ids))] },
		"backward": func(i int, ids []ID) ID { return Zero },
	}
	for name, origin := range shapes {
		r := New("me")
		ids := []ID{Zero}
		limit := uint64(12 * n * int(math.Log2(n)))
		for i := 1; i <= n; i++ {
			id := ID{Clock: uint64(i), Replica: "w"}
			r.Apply(Op{Type: OpInsert, ID: id, OriginLeft: origin(i, ids), Val: "x"})
			ids = append(ids, id)
			if r.els.workDone() > limit { // checked as it goes: a scanning list fails here in seconds
				t.Fatalf("%s: %d inserts visited %d nodes, over %d (n log n x 12 for all %d): the list is scanning again", name, i, r.els.workDone(), limit, n)
			}
		}
		if r.LiveCount() != n {
			t.Fatalf("%s: %d elements", name, r.LiveCount())
		}
		if r.els.workDone() > limit {
			t.Fatalf("%s: replaying %d inserts visited %d nodes, over %d (n log n x 12): the list is scanning again", name, n, r.els.workDone(), limit)
		}
		if r.els.workDone() == 0 { // R-S24 P2-21: a counter that counts nothing would pass the bound
			t.Fatalf("%s: the work counter counted nothing", name)
		}
		t.Logf("%s: %d nodes visited for %d inserts (limit %d)", name, r.els.workDone(), n, limit)
	}
}

// R-C23 P2-10: the priorities start from a per-process random seed, so no op stream can
// pick a degenerate shape; and the shape never changes a result: two seeds, one document.
func TestSeqShapeNeverChangesTheResult(t *testing.T) {
	if seqSeed == 0x9E3779B97F4A7C15 {
		t.Log("crypto/rand failed: the fixed fallback seed is in use")
	}
	ops := randomOps(rand.New(rand.NewSource(11)), 2000)
	var snaps []string
	for _, seed := range []uint64{seqSeed, 0x9E3779B97F4A7C15, 12345} {
		r := New("me")
		r.els.rng = seed
		for _, op := range ops {
			r.Apply(op)
		}
		b, _ := json.Marshal(r.Snapshot())
		snaps = append(snaps, string(b))
	}
	if snaps[0] != snaps[1] || snaps[1] != snaps[2] {
		t.Fatal("the tree's shape changed the document")
	}
}

// R-S20 P2-6: Load builds its tree in O(n) (seq.build). It must be exactly the tree the
// inserts at the end made before: the same order, priorities, sizes and links, and the
// priority generator in the same state after, for any state (empty, repeated ids,
// tombstones, long). Every later insert then walks the same tree, so the work stays fixed.
func TestLoadBuildsTheTreeTheInsertsWould(t *testing.T) {
	rng := rand.New(rand.NewSource(20))
	for _, n := range []int{0, 1, 2, 3, 7, 64, 1000, 20000} {
		es := make([]elem, n)
		for i := range es {
			es[i] = elem{id: ID{Clock: uint64(rng.Intn(n + 1)), Replica: "r"}, val: 'a' + rune(i%26), deleted: rng.Intn(4) == 0}
		}
		var a, b seq
		for i, e := range es {
			a.insertAt(i, e)
		}
		b.build(len(es), func(i int) elem { return es[i] })
		if a.rng != b.rng {
			t.Fatalf("n=%d: the priority generator ends in another state", n)
		}
		var walk func(x, y *node, px, py *node, at string)
		count := 0
		walk = func(x, y *node, px, py *node, at string) {
			for x != nil || y != nil {
				if (x == nil) != (y == nil) {
					t.Fatalf("n=%d %s: one tree has a node where the other has none", n, at)
				}
				count++
				if x.e != y.e || x.pri != y.pri || x.n != y.n || (x.p == nil) != (y.p == nil) || (x.p != nil && (x.p.pri != y.p.pri || x.p != px || y.p != py)) {
					t.Fatalf("n=%d %s: inserts made %+v (n=%d), the build %+v (n=%d)", n, at, x.e, x.n, y.e, y.n)
				}
				walk(x.l, y.l, x, y, at+"l")
				px, py, x, y, at = x, y, x.r, y.r, at+"r" // the right spine without recursion
			}
		}
		walk(a.root, b.root, nil, nil, "")
		if count != n {
			t.Fatalf("n=%d: compared %d nodes", n, count)
		}
	}
}

// R-S20 P2-7: the one shape the tree does not make O(log n): inserts that share an origin,
// arriving with falling clocks. YATA steps over every sibling with a higher id, so the k-th
// such insert walks k-1 siblings: k inserts cost about k²/2 steps (4096 in one push: about
// 8.4M steps, some 80 ms). Honest clients do not make it (a writer's clock rises, and a
// higher id goes first without a step); a writer crafting ops can. This pins the cost as
// known (KNOWN-LIMITS 55): over the bound means the walk got worse, under means the limit
// can go.
func TestTheFallingClockShapeCostsItsSiblings(t *testing.T) {
	const k = 4096
	r := New("me")
	for i := k; i >= 1; i-- {
		r.Apply(Op{Type: OpInsert, ID: ID{Clock: uint64(i), Replica: "w"}, OriginLeft: Zero, Val: "x"})
	}
	if r.LiveCount() != k {
		t.Fatalf("%d elements", r.LiveCount())
	}
	if r.els.workDone() == 0 { // R-S24 P2-21
		t.Fatal("the work counter counted nothing")
	}
	quad := uint64(k) * uint64(k+1) / 2
	limit := quad + uint64(12*k*int(math.Log2(k)))
	if r.els.workDone() > limit {
		t.Fatalf("%d falling-clock siblings visited %d nodes, over %d (k²/2 + 12 k log k): the walk got worse", k, r.els.workDone(), limit)
	}
	if r.els.workDone() < quad/2 {
		t.Logf("%d falling-clock siblings visited only %d nodes: the walk no longer steps over each sibling; KNOWN-LIMITS 55 can go", k, r.els.workDone())
	}
	t.Logf("falling clocks, one origin: %d nodes visited for %d inserts (k²/2 = %d)", r.els.workDone(), k, quad)
}

// R-S22 (final server read, Q4): the read methods (Text, Snapshot, SnapshotOps, DeletesFor
// and the walks under them) add to the work counter, so two readers under a shared (read)
// lock raced on it. Every caller holds a plain mutex today; a reader that takes a read lock
// must still be safe. Run with -race.
func TestConcurrentReadersDoNotRace(t *testing.T) {
	r := New("me")
	prev := Zero
	for i := 1; i <= 2000; i++ {
		id := ID{Clock: uint64(i), Replica: "w"}
		r.Apply(Op{Type: OpInsert, ID: id, OriginLeft: prev, Val: "x"})
		prev = id
	}
	want := r.Text()
	done := make(chan string, 4)
	for g := 0; g < 4; g++ {
		go func() {
			var got string
			for i := 0; i < 20; i++ {
				got = r.Text()
				_ = r.Snapshot()
				_ = r.SnapshotOps()
				_ = r.DeletesFor(want[:len(want)/2])
				_ = r.Knows(prev)
			}
			done <- got
		}()
	}
	for g := 0; g < 4; g++ {
		if got := <-done; got != want {
			t.Fatalf("a concurrent reader saw %d characters, want %d", len(got), len(want))
		}
	}
}

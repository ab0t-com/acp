package crdtjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"testing"

	"github.com/ab0t-com/acp/sdk/go/internal/crdt"
)

// canon marshals a doc's materialized value; fails the test on error.
func canon(t *testing.T, d *Doc) []byte {
	t.Helper()
	b, err := d.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical json: %v", err)
	}
	return b
}

// mustCanon canonicalizes client-form ops on the leader and returns them
// WITHOUT applying (so a second call generates truly CONCURRENT ops against
// the same committed state).
func mustCanon(t *testing.T, d *Doc, actor string, now int64, in ...Op) []Op {
	t.Helper()
	ops, err := d.Canonicalize(in, actor, now, true)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	return ops
}

func applyAll(d *Doc, ops []Op) {
	for _, op := range ops {
		d.Apply(op)
	}
}

func set(path []any, v string) Op {
	return Op{T: OpSet, Path: path, Value: json.RawMessage(v)}
}

// --- basics ---

func TestSetMaterializeBasics(t *testing.T) {
	d := New()
	ops := mustCanon(t, d, "A", 1,
		set([]any{"status"}, `"green"`),
		set([]any{"count"}, `42`),
		set([]any{"ci"}, `{"job":"lint","runs":[4126,4127],"ok":true}`),
	)
	applyAll(d, ops)
	want := `{"ci":{"job":"lint","ok":true,"runs":[4126,4127]},"count":42,"status":"green"}`
	if got := string(canon(t, d)); got != want {
		t.Fatalf("materialized %s, want %s", got, want)
	}
}

func TestCanonicalJSONSortedKeysManyKeys(t *testing.T) {
	// Two docs built with the SAME key set inserted in opposite orders must
	// marshal byte-identically — the sorted-key materialization rule (D4).
	mk := func(reverse bool) *Doc {
		d := New()
		for i := 0; i < 40; i++ {
			k := i
			if reverse {
				k = 39 - i
			}
			applyAll(d, mustCanon(t, d, "A", int64(i+1), set([]any{fmt.Sprintf("key%02d", k)}, fmt.Sprintf("%d", k))))
		}
		return d
	}
	a, b := canon(t, mk(false)), canon(t, mk(true))
	if !bytes.Equal(a, b) {
		t.Fatalf("insertion order leaked into marshal:\n%s\n%s", a, b)
	}
}

// --- the plan's named convergence vectors ---

func TestDisjointKeysBothSurvive(t *testing.T) {
	leader := New()
	opsA := mustCanon(t, leader, "A", 10, set([]any{"status"}, `"green"`))
	opsB := mustCanon(t, leader, "B", 11, set([]any{"owner"}, `"codexB"`)) // concurrent: same base state
	all := append(append([]Op{}, opsA...), opsB...)

	d1, d2 := New(), New()
	applyAll(d1, all)
	for i := len(all) - 1; i >= 0; i-- { // reversed order on the other replica
		d2.Apply(all[i])
	}
	want := `{"owner":"codexB","status":"green"}`
	if g1, g2 := string(canon(t, d1)), string(canon(t, d2)); g1 != want || g2 != want {
		t.Fatalf("disjoint merge: %s / %s, want %s", g1, g2, want)
	}
}

func TestSameKeyConcurrentSetLWW(t *testing.T) {
	leader := New()
	// Same committed base, same key: B's arrival is stamped LATER (now=20>10),
	// so B wins on EVERY replica in EVERY order.
	opsA := mustCanon(t, leader, "A", 10, set([]any{"status"}, `"green"`))
	opsB := mustCanon(t, leader, "B", 20, set([]any{"status"}, `"red"`))
	all := append(append([]Op{}, opsA...), opsB...)

	for _, order := range [][]Op{all, {all[1], all[0]}} {
		d := New()
		applyAll(d, order)
		if got := string(canon(t, d)); got != `{"status":"red"}` {
			t.Fatalf("LWW winner wrong for order %v: %s", order, got)
		}
	}

	// Tie on now: actor breaks it (then op id) — deterministically, both orders.
	leader2 := New()
	tieA := mustCanon(t, leader2, "A", 7, set([]any{"x"}, `1`))
	tieB := mustCanon(t, leader2, "B", 7, set([]any{"x"}, `2`))
	for _, order := range [][]Op{{tieA[0], tieB[0]}, {tieB[0], tieA[0]}} {
		d := New()
		applyAll(d, order)
		if got := string(canon(t, d)); got != `{"x":2}` { // "B" > "A"
			t.Fatalf("tie-break wrong: %s", got)
		}
	}
}

func TestConcurrentAddRemoveObservedRemove(t *testing.T) {
	leader := New()
	base := mustCanon(t, leader, "A", 1, set([]any{"k"}, `"v1"`))
	applyAll(leader, base)

	// CONCURRENT against the same committed state: A deletes k (observing only
	// v1's tag), B re-sets k (a new tag the delete never observed).
	del := mustCanon(t, leader, "A", 10, Op{T: OpDel, Path: []any{"k"}})
	add := mustCanon(t, leader, "B", 11, set([]any{"k"}, `"v2"`))

	perms := [][]Op{
		append(append(append([]Op{}, base...), del...), add...),
		append(append(append([]Op{}, base...), add...), del...),
		append(append(append([]Op{}, del...), add...), base...), // remove before its add
		append(append(append([]Op{}, add...), del...), base...),
	}
	for i, ord := range perms {
		d := New()
		applyAll(d, ord)
		if got := string(canon(t, d)); got != `{"k":"v2"}` {
			t.Fatalf("order %d: add must survive a concurrent observed-remove, got %s", i, got)
		}
	}

	// A del that observed BOTH tags kills the key everywhere.
	applyAll(leader, del)
	applyAll(leader, add)
	del2 := mustCanon(t, leader, "A", 20, Op{T: OpDel, Path: []any{"k"}})
	d := New()
	applyAll(d, append(append(append(append([]Op{}, del2...), add...), del...), base...))
	if got := string(canon(t, d)); got != `{}` {
		t.Fatalf("observing delete must remove the key, got %s", got)
	}
}

func TestListConcurrentInsertsConverge(t *testing.T) {
	leader := New()
	base := mustCanon(t, leader, "A", 1, set([]any{"tags"}, `[]`))
	applyAll(leader, base)

	// Two agents concurrently insert at the HEAD of the same (empty) list.
	insA := mustCanon(t, leader, "A", 10, Op{T: OpLIns, Path: []any{"tags"}, Value: json.RawMessage(`"a"`)})
	insB := mustCanon(t, leader, "B", 11, Op{T: OpLIns, Path: []any{"tags"}, Value: json.RawMessage(`"b"`)})

	var want string
	for i, ord := range [][]Op{
		append(append(append([]Op{}, base...), insA...), insB...),
		append(append(append([]Op{}, insB...), insA...), base...),
		append(append(append([]Op{}, insA...), base...), insB...),
	} {
		d := New()
		applyAll(d, ord)
		got := string(canon(t, d))
		if i == 0 {
			want = got
			continue
		}
		if got != want {
			t.Fatalf("order %d diverged: %s vs %s", i, got, want)
		}
	}

	// Concurrent insert + delete of a neighbor element.
	leader2 := New()
	b2 := mustCanon(t, leader2, "A", 1, set([]any{"l"}, `["x","y"]`))
	applyAll(leader2, b2)
	idx0 := 1
	ins := mustCanon(t, leader2, "A", 10, Op{T: OpLIns, Path: []any{"l"}, Idx: &idx0, Value: json.RawMessage(`"mid"`)})
	del := mustCanon(t, leader2, "B", 11, Op{T: OpDel, Path: []any{"l", 0}}) // del of an index = ldel
	all := append(append(append([]Op{}, b2...), ins...), del...)
	d1, d2 := New(), New()
	applyAll(d1, all)
	for i := len(all) - 1; i >= 0; i-- {
		d2.Apply(all[i])
	}
	if g1, g2 := string(canon(t, d1)), string(canon(t, d2)); g1 != g2 || g1 != `{"l":["mid","y"]}` {
		t.Fatalf("ins+del divergence: %s vs %s", g1, g2)
	}
}

func TestCausalBufferingChildBeforeParent(t *testing.T) {
	leader := New()
	ops := mustCanon(t, leader, "A", 1, set([]any{"a"}, `{"b":{"c":1}}`))
	if len(ops) != 3 {
		t.Fatalf("expected 3 expanded ops, got %d", len(ops))
	}
	d := New()
	// Apply innermost first: each buffers until its parent exists.
	d.Apply(ops[2])
	d.Apply(ops[1])
	if d.PendingLen() != 2 {
		t.Fatalf("children must buffer, pending=%d", d.PendingLen())
	}
	d.Apply(ops[0])
	if d.PendingLen() != 0 {
		t.Fatalf("pending must drain, still %d", d.PendingLen())
	}
	if got := string(canon(t, d)); got != `{"a":{"b":{"c":1}}}` {
		t.Fatalf("buffered ops integrated wrong: %s", got)
	}
}

func TestInPlaceRegisterLWW(t *testing.T) {
	leader := New()
	base := mustCanon(t, leader, "A", 1, set([]any{"l"}, `[1,2]`))
	applyAll(leader, base)
	// Two concurrent overwrites of element 0 target the SAME register node.
	wA := mustCanon(t, leader, "A", 10, set([]any{"l", 0}, `10`))
	wB := mustCanon(t, leader, "B", 20, set([]any{"l", 0}, `99`))
	for i, ord := range [][]Op{
		append(append(append([]Op{}, base...), wA...), wB...),
		append(append(append([]Op{}, wB...), wA...), base...),
	} {
		d := New()
		applyAll(d, ord)
		if got := string(canon(t, d)); got != `{"l":[99,2]}` {
			t.Fatalf("order %d: in-place LWW wrong: %s", i, got)
		}
	}
}

// --- compaction snapshot ---

func TestSnapshotOpsRoundTripAndGC(t *testing.T) {
	leader := New()
	hist := [][]Op{
		mustCanon(t, leader, "A", 1, set([]any{"cfg"}, `{"mode":"fast","tags":["a","b","c"]}`)),
	}
	applyAll(leader, hist[0])
	hist = append(hist, mustCanon(t, leader, "A", 2, set([]any{"cfg", "mode"}, `"slow"`))) // supersede
	applyAll(leader, hist[1])
	hist = append(hist, mustCanon(t, leader, "B", 3, Op{T: OpDel, Path: []any{"cfg", "tags", 1}})) // tombstone
	applyAll(leader, hist[2])
	hist = append(hist, mustCanon(t, leader, "B", 4, set([]any{"tmp"}, `"x"`)))
	applyAll(leader, hist[3])
	hist = append(hist, mustCanon(t, leader, "B", 5, Op{T: OpDel, Path: []any{"tmp"}})) // orphan a subtree
	applyAll(leader, hist[4])

	total := 0
	for _, h := range hist {
		total += len(h)
	}
	snap := leader.SnapshotOps()
	if len(snap) >= total {
		t.Fatalf("snapshot (%d ops) must be smaller than history (%d)", len(snap), total)
	}
	fresh := New()
	applyAll(fresh, snap)
	if fresh.PendingLen() != 0 {
		t.Fatalf("snapshot replay buffered %d ops (must be parent-before-child)", fresh.PendingLen())
	}
	if a, b := string(canon(t, leader)), string(canon(t, fresh)); a != b {
		t.Fatalf("snapshot replay diverged:\n%s\n%s", a, b)
	}
	// Live ids preserved: an op minted against the ORIGINAL doc still applies
	// to the rebuilt one (the resync contract behind the epoch barrier).
	more := mustCanon(t, leader, "A", 9, set([]any{"cfg", "mode"}, `"turbo"`))
	applyAll(leader, more)
	applyAll(fresh, more)
	if a, b := string(canon(t, leader)), string(canon(t, fresh)); a != b {
		t.Fatalf("post-snapshot op diverged:\n%s\n%s", a, b)
	}
}

// --- canonicalize: paths, literals, bounds, anti-spoof ---

func TestCanonicalizeResolutionAndBounds(t *testing.T) {
	d := New()
	// Missing intermediate without create_intermediate: ErrUnresolved.
	if _, err := d.Canonicalize([]Op{set([]any{"a", "b"}, `1`)}, "A", 1, false); err == nil || !IsUnresolved(err) {
		t.Fatalf("want ErrUnresolved, got %v", err)
	}
	// With create_intermediate: intermediates minted as maps.
	ops, err := d.Canonicalize([]Op{set([]any{"a", "b"}, `1`)}, "A", 1, true)
	if err != nil {
		t.Fatal(err)
	}
	applyAll(d, ops)
	if got := string(canon(t, d)); got != `{"a":{"b":1}}` {
		t.Fatalf("create_intermediate: %s", got)
	}
	// Same-request sequencing: a later op sees an earlier op's effect.
	ops2, err := d.Canonicalize([]Op{
		set([]any{"x"}, `{}`),
		set([]any{"x", "y"}, `2`),
		{T: OpDel, Path: []any{"x", "y"}},
	}, "A", 2, false)
	if err != nil {
		t.Fatalf("same-request sequencing: %v", err)
	}
	applyAll(d, ops2)
	if got := string(canon(t, d)); got != `{"a":{"b":1},"x":{}}` {
		t.Fatalf("sequenced request: %s", got)
	}
	// Depth bound.
	deep := `1`
	for i := 0; i < MaxDepth+2; i++ {
		deep = `{"d":` + deep + `}`
	}
	if _, err := d.Canonicalize([]Op{set([]any{"deep"}, deep)}, "A", 3, false); err == nil {
		t.Fatal("over-deep literal must be rejected")
	}
	// Op-count bound.
	big := `[`
	for i := 0; i < MaxOps+1; i++ {
		if i > 0 {
			big += ","
		}
		big += `0`
	}
	big += `]`
	if _, err := d.Canonicalize([]Op{set([]any{"big"}, big)}, "A", 4, false); err == nil {
		t.Fatal("over-size literal must be rejected")
	}
	// Unknown target nid: plain error (400 at the API).
	if _, err := d.Canonicalize([]Op{{T: OpSet, Target: crdt.ID{Clock: 999, Replica: "zz"}, Key: "k", Value: json.RawMessage(`1`)}}, "A", 5, false); err == nil {
		t.Fatal("unknown target must be rejected")
	}
	// Anti-spoof: client-supplied id/nid/stamps are overwritten.
	spoofed := Op{T: OpSet, Path: []any{"s"}, Value: json.RawMessage(`1`),
		ID: crdt.ID{Clock: 1, Replica: "evil"}, NID: crdt.ID{Clock: 1, Replica: "evil"}, Now: 999999, Actor: "evil"}
	ops3, err := d.Canonicalize([]Op{spoofed}, "A", 6, false)
	if err != nil {
		t.Fatal(err)
	}
	if ops3[0].Actor != "A" || ops3[0].Now != 6 || ops3[0].ID.Replica != "A" || ops3[0].NID.Replica != "A" || ops3[0].Path != nil {
		t.Fatalf("client-supplied identity fields not overwritten: %+v", ops3[0])
	}
}

func TestRFCHeadOriginAccepted(t *testing.T) {
	// ext-5 writes the list head origin as {"c":0,"r":"_"}; internally it is
	// the zero id. Both must land at the head.
	d := New()
	applyAll(d, mustCanon(t, d, "A", 1, set([]any{"l"}, `["x"]`)))
	ops := mustCanon(t, d, "A", 2, Op{T: OpLIns, Path: []any{"l"}, Origin: Root, Value: json.RawMessage(`"h"`)})
	applyAll(d, ops)
	if got := string(canon(t, d)); got != `{"l":["h","x"]}` {
		t.Fatalf("RFC head origin: %s", got)
	}
}

// --- the property battery: random interleavings across N replicas ---

// randomScalar returns a random JSON scalar literal.
func randomScalar(rng *rand.Rand) string {
	switch rng.Intn(4) {
	case 0:
		return fmt.Sprintf(`"s%d"`, rng.Intn(1000))
	case 1:
		return fmt.Sprintf(`%d`, rng.Intn(10000)-5000)
	case 2:
		return `true`
	default:
		return `null`
	}
}

func randomValue(rng *rand.Rand, depth int) string {
	if depth <= 0 || rng.Intn(3) > 0 {
		return randomScalar(rng)
	}
	if rng.Intn(2) == 0 {
		n := rng.Intn(3)
		out := `{`
		for i := 0; i < n; i++ {
			if i > 0 {
				out += ","
			}
			out += fmt.Sprintf(`"k%d":%s`, rng.Intn(5), randomValue(rng, depth-1))
		}
		return out + `}`
	}
	n := rng.Intn(3)
	out := `[`
	for i := 0; i < n; i++ {
		if i > 0 {
			out += ","
		}
		out += randomValue(rng, depth-1)
	}
	return out + `]`
}

// docShape lists the leader's REACHABLE containers/registers so random
// intents always address real state (like real clients would).
type docShape struct {
	maps  []crdt.ID
	keys  map[crdt.ID][]string
	lists []crdt.ID
	elems map[crdt.ID][]crdt.ID
	regs  []crdt.ID
}

func shapeOf(d *Doc) *docShape {
	s := &docShape{keys: map[crdt.ID][]string{}, elems: map[crdt.ID][]crdt.ID{}}
	// Walk the RESOLVED view (ext-14): enumerate the VISIBLE tree, so random
	// intents address reachable nodes like a real client. del/ldel targets keep
	// the RAW keys/elems (they operate on raw tags/elements).
	v := d.resolveView()
	var walk func(id crdt.ID)
	seen := map[crdt.ID]bool{}
	walk = func(id crdt.ID) {
		if seen[id] {
			return
		}
		seen[id] = true
		n := d.nodes[id]
		if n == nil {
			return
		}
		switch n.kind() {
		case KindMap:
			s.maps = append(s.maps, id)
			for k := range n.bindings {
				s.keys[id] = append(s.keys[id], k)
				if c, ok := v.slotChild(id, k); ok {
					walk(c)
				}
			}
		case KindList:
			s.lists = append(s.lists, id)
			for e := range n.elems {
				s.elems[id] = append(s.elems[id], e)
				if c, ok := v.occ[occKey{parent: id, elem: e}]; ok {
					walk(c)
				}
			}
		default:
			s.regs = append(s.regs, id)
		}
	}
	walk(Root)
	return s
}

// randomIntent builds one valid client-form op against the leader's state.
func randomIntent(rng *rand.Rand, sh *docShape) Op {
	pick := func(ids []crdt.ID) crdt.ID { return ids[rng.Intn(len(ids))] }
	nonRoot := func(ids []crdt.ID) []crdt.ID {
		out := ids[:0:0]
		for _, id := range ids {
			if !id.Equal(Root) {
				out = append(out, id)
			}
		}
		return out
	}
	for {
		switch rng.Intn(7) {
		case 6: // mv: relocate an existing node (ext-14). May be invalid (a
			// cycle vs committed state) -> canonicalize errors and genHistory
			// skips it; the concurrent-cycle case is covered by the shuffle in
			// the property test + the dedicated stays-put test.
			cands := nonRoot(append(append(append([]crdt.ID{}, sh.maps...), sh.lists...), sh.regs...))
			if len(cands) == 0 {
				continue
			}
			child := pick(cands)
			if rng.Intn(2) == 0 && len(sh.maps) > 0 { // map destination
				return Op{T: OpMove, Child: child, Target: pick(sh.maps), Key: fmt.Sprintf("mv%d", rng.Intn(6))}
			}
			if len(sh.lists) > 0 { // list destination
				l := pick(sh.lists)
				origin := crdt.Zero
				if es := sh.elems[l]; len(es) > 0 && rng.Intn(2) == 0 {
					origin = es[rng.Intn(len(es))]
				}
				return Op{T: OpMove, Child: child, Target: l, Origin: origin}
			}
			continue
		case 0, 1: // set on a random map (new or existing key)
			m := pick(sh.maps)
			key := fmt.Sprintf("k%d", rng.Intn(8))
			return Op{T: OpSet, Target: m, Key: key, Value: json.RawMessage(randomValue(rng, 2))}
		case 2: // del an existing key
			m := pick(sh.maps)
			if ks := sh.keys[m]; len(ks) > 0 {
				return Op{T: OpDel, Target: m, Key: ks[rng.Intn(len(ks))]}
			}
		case 3: // lins at a random origin
			if len(sh.lists) > 0 {
				l := pick(sh.lists)
				origin := crdt.Zero
				if es := sh.elems[l]; len(es) > 0 && rng.Intn(2) == 0 {
					origin = es[rng.Intn(len(es))]
				}
				return Op{T: OpLIns, Target: l, Origin: origin, Value: json.RawMessage(randomValue(rng, 1))}
			}
		case 4: // ldel a random element (possibly already tombstoned)
			if len(sh.lists) > 0 {
				l := pick(sh.lists)
				if es := sh.elems[l]; len(es) > 0 {
					return Op{T: OpLDel, Target: l, Elem: es[rng.Intn(len(es))]}
				}
			}
		case 5: // in-place register overwrite
			if len(sh.regs) > 0 {
				return Op{T: OpSet, Target: pick(sh.regs), Value: json.RawMessage(randomScalar(rng))}
			}
		}
	}
}

// genHistory drives a scripted leader: each round canonicalizes 1..3 client
// batches against the SAME committed state (true concurrency, including
// same-key/same-list races), then commits them all. Returns every canonical op.
func genHistory(t testing.TB, rng *rand.Rand, rounds int) ([]Op, *Doc) {
	t.Helper()
	leader := New()
	actors := []string{"A", "B", "C"}
	now := int64(1)
	var all []Op
	for r := 0; r < rounds; r++ {
		sh := shapeOf(leader)
		var groups [][]Op
		for i, k := 0, 1+rng.Intn(3); i < k; i++ {
			var intents []Op
			for j, n := 0, 1+rng.Intn(3); j < n; j++ {
				intents = append(intents, randomIntent(rng, sh))
			}
			ops, err := leader.Canonicalize(intents, actors[rng.Intn(len(actors))], now, true)
			now++
			if err != nil {
				// A random mv can be invalid vs committed state (a cycle, or a
				// dest inside the moved subtree). set/del/lins/ldel intents are
				// constructed to always resolve, so ONLY a batch containing a mv
				// may error — skip it. A non-mv batch erroring is a real bug.
				hasMove := false
				for _, in := range intents {
					if in.T == OpMove {
						hasMove = true
					}
				}
				if !hasMove {
					t.Fatalf("round %d canonicalize (no mv): %v", r, err)
				}
				continue
			}
			groups = append(groups, ops)
		}
		for _, g := range groups {
			applyAll(leader, g)
			all = append(all, g...)
		}
	}
	return all, leader
}

// TestPropertyConvergence is the acceptance heart: the SAME op set, in random
// orders, with duplicates, on N fresh replicas -> byte-identical JSON, equal
// to the leader's, with nothing left buffered.
func TestPropertyConvergence(t *testing.T) {
	for seed := int64(0); seed < 30; seed++ {
		rng := rand.New(rand.NewSource(seed))
		ops, leader := genHistory(t, rng, 8)
		want := string(canon(t, leader))
		for replica := 0; replica < 5; replica++ {
			seq := append([]Op{}, ops...)
			rng.Shuffle(len(seq), func(i, j int) { seq[i], seq[j] = seq[j], seq[i] })
			// Inject duplicates at random positions (~25%).
			for i := 0; i < len(ops)/4; i++ {
				seq = append(seq, ops[rng.Intn(len(ops))])
			}
			rng.Shuffle(len(seq), func(i, j int) { seq[i], seq[j] = seq[j], seq[i] })
			d := New()
			applyAll(d, seq)
			if d.PendingLen() != 0 {
				t.Fatalf("seed %d replica %d: %d ops still buffered", seed, replica, d.PendingLen())
			}
			if got := string(canon(t, d)); got != want {
				t.Fatalf("seed %d replica %d DIVERGED:\n got %s\nwant %s", seed, replica, got, want)
			}
		}
	}
}

// TestPropertySnapshotConvergence: compaction must not change meaning — for
// random histories, SnapshotOps replayed anywhere equals the original.
func TestPropertySnapshotConvergence(t *testing.T) {
	for seed := int64(100); seed < 115; seed++ {
		rng := rand.New(rand.NewSource(seed))
		_, leader := genHistory(t, rng, 6)
		want := string(canon(t, leader))
		snap := leader.SnapshotOps()
		for replica := 0; replica < 3; replica++ {
			seq := append([]Op{}, snap...)
			if replica > 0 { // snapshot replay must ALSO be order-free
				rng.Shuffle(len(seq), func(i, j int) { seq[i], seq[j] = seq[j], seq[i] })
			}
			d := New()
			applyAll(d, seq)
			if d.PendingLen() != 0 {
				t.Fatalf("seed %d: snapshot replay buffered %d", seed, d.PendingLen())
			}
			if got := string(canon(t, d)); got != want {
				t.Fatalf("seed %d replica %d snapshot diverged:\n got %s\nwant %s", seed, replica, got, want)
			}
		}
	}
}

// FuzzConvergence: arbitrary (even hostile) op sets must never make two
// replicas disagree. Ops are deduplicated by id — the leader-mint contract —
// but otherwise applied as-is, in two different orders.
func FuzzConvergence(f *testing.F) {
	for seed := int64(0); seed < 4; seed++ {
		rng := rand.New(rand.NewSource(seed))
		ops, _ := genHistory(f, rng, 3)
		b, _ := json.Marshal(ops)
		f.Add(b, seed)
	}
	f.Fuzz(func(t *testing.T, data []byte, seed int64) {
		var raw []Op
		if json.Unmarshal(data, &raw) != nil || len(raw) == 0 || len(raw) > 200 {
			t.Skip()
		}
		seen := map[crdt.ID]bool{}
		var ops []Op
		for _, op := range raw {
			if seen[op.ID] {
				continue // leader-mint contract: one op per id
			}
			seen[op.ID] = true
			if len(op.Value) > 0 && !json.Valid(op.Value) {
				op.Value = json.RawMessage(`null`) // leader validates values
			}
			op.Path, op.Idx = nil, nil // canonical form only
			ops = append(ops, op)
		}
		d1, d2 := New(), New()
		applyAll(d1, ops)
		rng := rand.New(rand.NewSource(seed))
		shuf := append([]Op{}, ops...)
		rng.Shuffle(len(shuf), func(i, j int) { shuf[i], shuf[j] = shuf[j], shuf[i] })
		applyAll(d2, shuf)
		b1, e1 := d1.CanonicalJSON()
		b2, e2 := d2.CanonicalJSON()
		if (e1 == nil) != (e2 == nil) || !bytes.Equal(b1, b2) {
			t.Fatalf("fuzz divergence:\n%s (%v)\n%s (%v)", b1, e1, b2, e2)
		}
	})
}

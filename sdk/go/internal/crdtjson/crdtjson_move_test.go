package crdtjson

import (
	"encoding/json"
	"math/rand"
	"testing"
)

// mv builds a client-form move op (source path -> destination path).
func mv(from, to []any) Op { return Op{T: OpMove, FromPath: from, Path: to} }

// commit canonicalizes client ops against d and applies them (a committed step).
func commit(t *testing.T, d *Doc, actor string, now int64, in ...Op) []Op {
	t.Helper()
	ops := mustCanon(t, d, actor, now, in...)
	applyAll(d, ops)
	return ops
}

// allOrders applies a set of committed op-groups to fresh replicas in forward
// and reverse order (plus a duplicate pass) and asserts they converge to want.
func allOrders(t *testing.T, want string, groups ...[]Op) {
	t.Helper()
	var all []Op
	for _, g := range groups {
		all = append(all, g...)
	}
	orders := map[string][]Op{"forward": all}
	rev := make([]Op, len(all))
	for i := range all {
		rev[i] = all[len(all)-1-i]
	}
	orders["reverse"] = rev
	dup := append(append([]Op{}, all...), all...) // idempotent re-delivery
	orders["duplicated"] = dup
	for name, seq := range orders {
		d := New()
		applyAll(d, seq)
		if d.PendingLen() != 0 {
			t.Fatalf("%s: %d ops still buffered", name, d.PendingLen())
		}
		if got := string(canon(t, d)); got != want {
			t.Fatalf("%s order diverged:\n got %s\nwant %s", name, got, want)
		}
	}
}

// --- identity + no duplication ---

func TestMovePreservesIdentityNoDuplication(t *testing.T) {
	d := New()
	base := commit(t, d, "A", 1, set([]any{"src"}, `{"deep":{"v":1}}`))
	moved := commit(t, d, "A", 2, mv([]any{"src"}, []any{"dst"}))
	// The subtree moved intact; the source key is gone; nothing duplicated.
	if got := string(canon(t, d)); got != `{"dst":{"deep":{"v":1}}}` {
		t.Fatalf("move result %s", got)
	}
	allOrders(t, `{"dst":{"deep":{"v":1}}}`, base, moved)
}

func TestConcurrentMoveLWWWinner(t *testing.T) {
	leader := New()
	base := commit(t, leader, "A", 1, set([]any{"x"}, `{"v":1}`))
	// Two agents concurrently move the SAME node to different keys. Later stamp
	// wins on every replica; the node renders ONCE — no duplication.
	moveA := mustCanon(t, leader, "A", 10, mv([]any{"x"}, []any{"a"}))
	moveB := mustCanon(t, leader, "B", 20, mv([]any{"x"}, []any{"b"}))
	allOrders(t, `{"b":{"v":1}}`, base, moveA, moveB)
}

// --- the F1 regression: concurrent A<->B cycle STAYS PUT, both nodes visible ---

func TestMoveCycleStaysPut(t *testing.T) {
	leader := New()
	base := commit(t, leader, "A", 1,
		set([]any{"A"}, `{"foo":1}`),
		set([]any{"B"}, `{"bar":2}`))
	// Concurrent, against the same committed state: move B under A, and move A
	// under B. One move must be ignored (stays put); BOTH nodes MUST remain
	// visible. The higher-stamped move (A under B) loses the cycle check because
	// by stamp order B is already under A. This is the exact interaction the
	// first draft's retirement rule got wrong (both vanished).
	moveBunderA := mustCanon(t, leader, "A", 10, mv([]any{"B"}, []any{"A", "B"}))
	moveAunderB := mustCanon(t, leader, "B", 20, mv([]any{"A"}, []any{"B", "A"}))
	allOrders(t, `{"A":{"B":{"bar":2},"foo":1}}`, base, moveBunderA, moveAunderB)
}

func TestScalarMoveSkipsAncestorCheck(t *testing.T) {
	d := New()
	commit(t, d, "A", 1, set([]any{"a"}, `7`), set([]any{"b"}, `{}`))
	commit(t, d, "A", 2, mv([]any{"a"}, []any{"b", "a"}))
	if got := string(canon(t, d)); got != `{"b":{"a":7}}` {
		t.Fatalf("scalar move result %s", got)
	}
}

// --- concurrent edit follows the moved node (identity preserved) ---

func TestMoveThenEditFollows(t *testing.T) {
	leader := New()
	base := commit(t, leader, "A", 1, set([]any{"card"}, `{"text":"draft"}`))
	// The child register node's nid, addressed directly, is what B edits.
	v := leader.resolveView()
	cardNID, _ := v.slotChild(Root, "card")
	textNID, _ := leader.resolveView().slotChild(cardNID, "text")
	// A moves the card; B concurrently overwrites the text register by nid.
	moveCard := mustCanon(t, leader, "A", 10, mv([]any{"card"}, []any{"moved"}))
	editText := mustCanon(t, leader, "B", 20, Op{T: OpSet, Target: textNID, Value: json.RawMessage(`"draft v2"`)})
	// The edit follows the card to its new location (same nid).
	allOrders(t, `{"moved":{"text":"draft v2"}}`, base, moveCard, editText)
}

// --- delete after move deletes (no fallback past a dead location) ---

func TestDeleteAfterMoveDeletes(t *testing.T) {
	d := New()
	commit(t, d, "A", 1, set([]any{"a"}, `{"v":1}`))
	commit(t, d, "A", 2, mv([]any{"a"}, []any{"b"}))
	commit(t, d, "A", 3, Op{T: OpDel, Path: []any{"b"}})
	// Deleting the node at its new location deletes it — it does NOT resurrect
	// at the old key "a" (no fallback past a dead winning location).
	if got := string(canon(t, d)); got != `{}` {
		t.Fatalf("delete-after-move must delete, got %s (resurrection bug)", got)
	}
}

func TestConcurrentDelVsMoveAddWins(t *testing.T) {
	leader := New()
	base := commit(t, leader, "A", 1, set([]any{"a"}, `{"v":1}`))
	// A deletes "a" (observing only the original binding); B concurrently moves
	// "a" to "b". The move's NEW placement was never observed by the delete, so
	// the node SURVIVES at its new location (add-wins analog).
	del := mustCanon(t, leader, "A", 10, Op{T: OpDel, Path: []any{"a"}})
	move := mustCanon(t, leader, "B", 20, mv([]any{"a"}, []any{"b"}))
	allOrders(t, `{"b":{"v":1}}`, base, del, move)
}

// --- ghost purge: a superseded binding at the source slot does NOT resurface ---

func TestContendedSlotGhostPurge(t *testing.T) {
	leader := New()
	// Two concurrent sets to key "k": X@5 then Y@9 (Y wins, renders). Commit both.
	setX := commit(t, leader, "A", 5, set([]any{"k"}, `"X"`))
	setY := commit(t, leader, "B", 9, set([]any{"k"}, `"Y"`))
	_ = setX
	_ = setY
	// Now move the visible node at "k" (Y) away. The ghost purge must retire the
	// superseded X binding at the source slot, so "k" empties — X must NOT
	// resurface. Y renders at the destination.
	commit(t, leader, "A", 12, mv([]any{"k"}, []any{"dest"}))
	if got := string(canon(t, leader)); got != `{"dest":"Y"}` {
		t.Fatalf("ghost purge failed, got %s (superseded X resurfaced?)", got)
	}
}

// --- list-element move: index math matches the visible array, no null holes ---

func TestListIndexAfterMoveAway(t *testing.T) {
	d := New()
	commit(t, d, "A", 1, set([]any{"l"}, `["x","y","z"]`), set([]any{"out"}, `{}`))
	// Move the middle element ("y") out of the list into a map key.
	commit(t, d, "A", 2, mv([]any{"l", 1}, []any{"out", "y"}))
	// The array closes up (no null hole); "y" appears under out.
	if got := string(canon(t, d)); got != `{"l":["x","z"],"out":{"y":"y"}}` {
		t.Fatalf("list move result %s", got)
	}
	// Index resolution now matches the visible array: delete index 1 hits "z".
	commit(t, d, "A", 3, Op{T: OpDel, Path: []any{"l", 1}})
	if got := string(canon(t, d)); got != `{"l":["x"],"out":{"y":"y"}}` {
		t.Fatalf("post-move index math wrong: %s", got)
	}
}

// --- compaction is observably transparent with moves ---

func TestCompactionTransparencyWithMoves(t *testing.T) {
	build := func() *Doc {
		d := New()
		commit(t, d, "A", 1, set([]any{"a"}, `{"deep":{"v":1}}`), set([]any{"b"}, `[10,20]`))
		commit(t, d, "A", 2, mv([]any{"a"}, []any{"c"}))         // subtree move
		commit(t, d, "A", 3, mv([]any{"b", 0}, []any{"c", "x"})) // list-elem move into map
		return d
	}
	d := build()
	want := string(canon(t, d))
	// SnapshotOps replayed on a fresh doc reproduces the identical JSON, and
	// carries NO mv ops (moves are baked into the resolved placements).
	snap := d.SnapshotOps()
	for _, op := range snap {
		if op.T == OpMove {
			t.Fatalf("snapshot must not contain mv ops")
		}
	}
	r := New()
	applyAll(r, snap)
	if r.PendingLen() != 0 {
		t.Fatalf("snapshot replay buffered %d", r.PendingLen())
	}
	if got := string(canon(t, r)); got != want {
		t.Fatalf("compaction changed meaning:\n got %s\nwant %s", got, want)
	}
	// (ops; compact; more) materializes identically to (ops; more): a late edit
	// after compaction lands the same as without it.
	late := func(d *Doc) { commit(t, d, "A", 9, set([]any{"c", "late"}, `true`)) }
	d1 := build()
	late(d1)
	d2 := New()
	applyAll(d2, d.SnapshotOps())
	late(d2)
	if g1, g2 := string(canon(t, d1)), string(canon(t, d2)); g1 != g2 {
		t.Fatalf("compaction not transparent to later edits:\n%s\n%s", g1, g2)
	}
}

// --- buffering: a mv whose destination parent arrives late still converges ---

func TestMoveChildAndTargetArriveLate(t *testing.T) {
	leader := New()
	g1 := commit(t, leader, "A", 1, set([]any{"item"}, `{"v":1}`))
	g2 := commit(t, leader, "A", 2, set([]any{"box"}, `{}`))
	g3 := commit(t, leader, "A", 3, mv([]any{"item"}, []any{"box", "item"}))
	want := `{"box":{"item":{"v":1}}}`
	// Deliver the mv (and its deps) in every order, including mv-first (its
	// Target "box" and Src Root must gate it into the buffer, then it drains).
	allOrders(t, want, g1, g2, g3)
	// Explicit worst case: mv strictly before the box that receives it.
	d := New()
	applyAll(d, g3) // mv: Target=box (absent) -> buffered
	if d.PendingLen() == 0 {
		t.Fatalf("mv into an absent target should buffer")
	}
	applyAll(d, g1)
	applyAll(d, g2) // box arrives -> mv drains
	if d.PendingLen() != 0 {
		t.Fatalf("mv did not drain after its target arrived: %d buffered", d.PendingLen())
	}
	if got := string(canon(t, d)); got != want {
		t.Fatalf("late-target move: %s", got)
	}
}

// --- self-subtree move is refused at canonicalize (courtesy 409) ---

func TestMoveIntoOwnSubtreeRejected(t *testing.T) {
	d := New()
	commit(t, d, "A", 1, set([]any{"a"}, `{"b":{"c":1}}`))
	_, err := d.Canonicalize([]Op{mv([]any{"a"}, []any{"a", "b", "here"})}, "A", 2, false)
	if err == nil || !IsUnresolved(err) {
		t.Fatalf("moving a node into its own subtree must 409, got %v", err)
	}
	// The document is unchanged.
	if got := string(canon(t, d)); got != `{"a":{"b":{"c":1}}}` {
		t.Fatalf("rejected move must not mutate: %s", got)
	}
}

// --- a 3-node cycle (A->B->C->A) resolves to one skipped move, all visible ---

func TestMoveThreeNodeCycleStaysPut(t *testing.T) {
	leader := New()
	base := commit(t, leader, "A", 1,
		set([]any{"A"}, `{"foo":1}`),
		set([]any{"B"}, `{"bar":2}`),
		set([]any{"C"}, `{"baz":3}`))
	// Three concurrent moves against the SAME committed state forming a cycle:
	// A under B (@10), B under C (@20), C under A (@30). In stamp order the first
	// two are acyclic and take effect (A under B, B under C); the third (C under
	// A, highest stamp) would close the cycle A->B->C->A and is SKIPPED — C stays
	// at root. All three nodes remain visible; nothing vanishes or duplicates.
	mAB := mustCanon(t, leader, "A", 10, mv([]any{"A"}, []any{"B", "A"}))
	mBC := mustCanon(t, leader, "B", 20, mv([]any{"B"}, []any{"C", "B"}))
	mCA := mustCanon(t, leader, "C", 30, mv([]any{"C"}, []any{"A", "C"}))
	allOrders(t, `{"C":{"B":{"A":{"foo":1},"bar":2},"baz":3}}`, base, mAB, mBC, mCA)
}

// --- NID-form move (no from/path): Child + Target + Key addressed by nid ---

func TestMoveNIDForm(t *testing.T) {
	d := New()
	commit(t, d, "A", 1, set([]any{"a"}, `{"v":1}`), set([]any{"box"}, `{}`))
	v := d.resolveView()
	child, ok := v.slotChild(Root, "a")
	if !ok {
		t.Fatal("child a not found")
	}
	box, _ := v.slotChild(Root, "box")
	// Canonical NID-form move: no FromPath/Path — Child + Target + Key directly.
	// The source slot is location(child) (Root,"a"), which empties as the child
	// takes its new home under box.
	ops, err := d.Canonicalize([]Op{{T: OpMove, Child: child, Target: box, Key: "a"}}, "A", 2, false)
	if err != nil {
		t.Fatalf("NID-form canonicalize: %v", err)
	}
	applyAll(d, ops)
	if got := string(canon(t, d)); got != `{"box":{"a":{"v":1}}}` {
		t.Fatalf("NID-form move: %s", got)
	}
}

// --- a move whose resulting materialized depth would exceed MaxDepth is rejected ---

func TestMoveExceedingMaxDepthRejected(t *testing.T) {
	d := New()
	// Build a nested chain of empty objects: deep -> n -> n -> ... (MaxDepth-1
	// levels of "n"), so the deepest {} sits near the MaxDepth boundary. Plus a
	// small subtree "sub" (height 1) to move into it.
	inner := `{}`
	for i := 0; i < MaxDepth-1; i++ {
		inner = `{"n":` + inner + `}`
	}
	commit(t, d, "A", 1, set([]any{"deep"}, inner), set([]any{"sub"}, `{"x":1}`))
	// Path to the deepest object: ["deep","n","n",...] (MaxDepth-1 "n" segments).
	deepPath := []any{"deep"}
	for i := 0; i < MaxDepth-1; i++ {
		deepPath = append(deepPath, "n")
	}
	// Moving "sub" (which itself has depth) beneath the deepest node must push the
	// materialized depth past MaxDepth and be rejected at canonicalize time.
	dest := append(append([]any{}, deepPath...), "sub")
	_, err := d.Canonicalize([]Op{mv([]any{"sub"}, dest)}, "A", 2, false)
	if err == nil {
		t.Fatal("move exceeding MaxDepth must be rejected")
	}
	// The document is unchanged (nothing partially applied).
	if got := leaderSubKey(t, d, "sub"); got != `{"x":1}` {
		t.Fatalf("rejected over-depth move must not mutate sub: %s", got)
	}
}

// leaderSubKey materializes the doc and returns the JSON of top-level key k.
func leaderSubKey(t *testing.T, d *Doc, k string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(canon(t, d), &m); err != nil {
		t.Fatal(err)
	}
	return string(m[k])
}

// TestMoveConvergenceStress is a wider net than TestPropertyConvergence,
// focused on move order-dependence: 200 move-heavy histories, each replayed on
// 8 replicas in shuffled+duplicated order plus a snapshot round-trip, all
// required byte-identical. (~1s.)
func TestMoveConvergenceStress(t *testing.T) {
	if testing.Short() {
		t.Skip("stress")
	}
	for seed := int64(1000); seed < 1200; seed++ {
		rng := rand.New(rand.NewSource(seed))
		ops, leader := genHistory(t, rng, 10)
		want := string(canon(t, leader))
		for r := 0; r < 8; r++ {
			seq := append([]Op{}, ops...)
			rng.Shuffle(len(seq), func(i, j int) { seq[i], seq[j] = seq[j], seq[i] })
			for i := 0; i < len(ops)/3; i++ {
				seq = append(seq, ops[rng.Intn(len(ops))])
			}
			rng.Shuffle(len(seq), func(i, j int) { seq[i], seq[j] = seq[j], seq[i] })
			d := New()
			applyAll(d, seq)
			if d.PendingLen() != 0 {
				t.Fatalf("seed %d r %d: %d buffered", seed, r, d.PendingLen())
			}
			if got := string(canon(t, d)); got != want {
				t.Fatalf("seed %d r %d DIVERGED\n got %s\nwant %s", seed, r, got, want)
			}
		}
		sd := New()
		applyAll(sd, leader.SnapshotOps())
		if got := string(canon(t, sd)); got != want {
			t.Fatalf("seed %d snapshot DIVERGED\n got %s\nwant %s", seed, got, want)
		}
	}
}

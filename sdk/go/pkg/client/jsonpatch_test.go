package client

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/ab0t-com/acp/sdk/go/internal/crdtjson"
)

// materializeOps canonicalizes + applies ops on d (leader-side, as the
// daemon would) and returns the canonical JSON — the round-trip oracle.
func materializeOps(t *testing.T, d *crdtjson.Doc, ops []crdtjson.Op, now int64) string {
	t.Helper()
	canon, err := d.Canonicalize(ops, "actorA", now, true)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	for _, op := range canon {
		d.Apply(op)
	}
	if n := d.PendingLen(); n != 0 {
		t.Fatalf("%d ops left buffered", n)
	}
	b, err := d.CanonicalJSON()
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	return string(b)
}

// TestJSONPatchMapsEachOp checks every RFC 6902 op maps to the right
// crdtjson op shape (the T54 mapping table).
func TestJSONPatchMapsEachOp(t *testing.T) {
	val := json.RawMessage(`"v"`)
	obj := json.RawMessage(`{"x":1}`)
	cases := []struct {
		name  string
		patch PatchOp
		want  []crdtjson.Op // T/Path/Idx/Value only; nil = dropped
	}{
		{"add object key", PatchOp{Op: "add", Path: "/nodes/n1", Value: val},
			[]crdtjson.Op{{T: crdtjson.OpSet, Path: []any{"nodes", "n1"}, Value: val}}},
		{"replace object key", PatchOp{Op: "replace", Path: "/nodes/n1", Value: val},
			[]crdtjson.Op{{T: crdtjson.OpSet, Path: []any{"nodes", "n1"}, Value: val}}},
		{"add into array at index", PatchOp{Op: "add", Path: "/items/1", Value: val},
			[]crdtjson.Op{{T: crdtjson.OpLIns, Path: []any{"items"}, Idx: intp(1), Value: val}}},
		{"add append (-)", PatchOp{Op: "add", Path: "/items/-", Value: val},
			[]crdtjson.Op{{T: crdtjson.OpLIns, Path: []any{"items"}, Idx: intp(appendIdx), Value: val}}},
		{"replace scalar at index", PatchOp{Op: "replace", Path: "/items/2", Value: val},
			[]crdtjson.Op{{T: crdtjson.OpSet, Path: []any{"items", 2}, Value: val}}},
		{"replace container at index expands to ldel+lins",
			PatchOp{Op: "replace", Path: "/items/2", Value: obj},
			[]crdtjson.Op{
				{T: crdtjson.OpLDel, Path: []any{"items", 2}},
				{T: crdtjson.OpLIns, Path: []any{"items"}, Idx: intp(2), Value: obj}}},
		{"remove object key", PatchOp{Op: "remove", Path: "/nodes/n1"},
			[]crdtjson.Op{{T: crdtjson.OpDel, Path: []any{"nodes", "n1"}}}},
		{"remove array index", PatchOp{Op: "remove", Path: "/items/0"},
			[]crdtjson.Op{{T: crdtjson.OpLDel, Path: []any{"items", 0}}}},
		{"test dropped", PatchOp{Op: "test", Path: "/title", Value: val}, nil},
		{"escaped pointer segments", PatchOp{Op: "add", Path: "/a~1b/c~0d", Value: val},
			[]crdtjson.Op{{T: crdtjson.OpSet, Path: []any{"a/b", "c~d"}, Value: val}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := JSONPatchToOps([]PatchOp{tc.patch})
			if err != nil {
				t.Fatalf("map: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d ops, want %d: %+v", len(got), len(tc.want), got)
			}
			for i, w := range tc.want {
				g := got[i]
				if g.T != w.T {
					t.Errorf("op[%d].T = %q, want %q", i, g.T, w.T)
				}
				if !pathEq(g.Path, w.Path) {
					t.Errorf("op[%d].Path = %v, want %v", i, g.Path, w.Path)
				}
				if (g.Idx == nil) != (w.Idx == nil) || (w.Idx != nil && *g.Idx != *w.Idx) {
					t.Errorf("op[%d].Idx = %v, want %v", i, fmtIdx(g.Idx), fmtIdx(w.Idx))
				}
				if string(g.Value) != string(w.Value) {
					t.Errorf("op[%d].Value = %s, want %s", i, g.Value, w.Value)
				}
			}
		})
	}
}

// TestJSONPatchMoveCopyNeedDoc: the stateless mapper cannot resolve a
// move/copy source value and says so explicitly.
func TestJSONPatchMoveCopyNeedDoc(t *testing.T) {
	for _, op := range []string{"move", "copy"} {
		_, err := JSONPatchToOps([]PatchOp{{Op: op, From: "/a", Path: "/b"}})
		if !errors.Is(err, ErrNeedsDoc) {
			t.Errorf("%s: err = %v, want ErrNeedsDoc", op, err)
		}
	}
}

// TestJSONPatchMoveCopyWithDoc: move maps to ONE identity-preserving mv op
// (acp-ext-14); copy still duplicates (a fresh add). Source resolved from the doc.
func TestJSONPatchMoveCopyWithDoc(t *testing.T) {
	cur := json.RawMessage(`{"a":{"x":1},"arr":["p","q"]}`)

	ops, err := JSONPatchToOpsWithDoc([]PatchOp{{Op: "copy", From: "/a/x", Path: "/a/y"}}, cur)
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if len(ops) != 1 || ops[0].T != crdtjson.OpSet || !pathEq(ops[0].Path, []any{"a", "y"}) || string(ops[0].Value) != "1" {
		t.Fatalf("copy mapped to %+v", ops)
	}

	ops, err = JSONPatchToOpsWithDoc([]PatchOp{{Op: "move", From: "/arr/0", Path: "/moved"}}, cur)
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if len(ops) != 1 || ops[0].T != crdtjson.OpMove {
		t.Fatalf("move mapped to %+v, want a single mv op", ops)
	}
	if !pathEq(ops[0].FromPath, []any{"arr", 0}) || !pathEq(ops[0].Path, []any{"moved"}) {
		t.Errorf("move = %+v, want mv from /arr/0 to /moved", ops[0])
	}
}

// TestJSONPatchMoveWithinArray: an array element move maps to ONE mv op and
// round-trips through the real crdtjson engine to the reordered array — with
// the moved element's identity preserved (unlike the old ldel+lins).
func TestJSONPatchMoveWithinArray(t *testing.T) {
	d := crdtjson.New()
	seed, err := JSONPatchToOps([]PatchOp{
		{Op: "add", Path: "/items", Value: json.RawMessage(`["a","b","c"]`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	materializeOps(t, d, seed, 1)
	cur, _ := d.CanonicalJSON()

	ops, err := JSONPatchToOpsWithDoc([]PatchOp{
		{Op: "move", From: "/items/0", Path: "/items/1"},
	}, cur)
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if len(ops) != 1 || ops[0].T != crdtjson.OpMove || !pathEq(ops[0].FromPath, []any{"items", 0}) {
		t.Fatalf("move within array mapped to %+v, want a single mv op from /items/0", ops)
	}
	// The dest index carries the RFC-6902 post-removal adjustment (a within-array
	// forward move); the round-trip against the real engine is the correctness
	// proof — the moved element lands at the RFC position and keeps its identity.
	got := materializeOps(t, d, ops, 2)
	want := `{"items":["b","a","c"]}`
	if got != want {
		t.Fatalf("move-within-array round-trip mismatch:\n got %s\nwant %s", got, want)
	}
}

// TestJSONPatchDocAwareNumericKey: with the doc present, a map key that
// LOOKS like an array index is typed from the real container.
func TestJSONPatchDocAwareNumericKey(t *testing.T) {
	cur := json.RawMessage(`{"m":{"3":"x"}}`)
	ops, err := JSONPatchToOpsWithDoc([]PatchOp{{Op: "remove", Path: "/m/3"}}, cur)
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if len(ops) != 1 || ops[0].T != crdtjson.OpDel || !pathEq(ops[0].Path, []any{"m", "3"}) {
		t.Fatalf("got %+v, want del [m 3(string)]", ops)
	}
}

// TestJSONPatchRoundTripLiquidBoard: a liquid-style node.patch frame,
// mapped to ops and materialized through the real crdtjson engine,
// reproduces the intended board byte-for-byte.
func TestJSONPatchRoundTripLiquidBoard(t *testing.T) {
	patch := []PatchOp{
		{Op: "add", Path: "/title", Value: json.RawMessage(`"Q3"`)},
		{Op: "add", Path: "/nodes", Value: json.RawMessage(`{}`)},
		{Op: "add", Path: "/nodes/n1", Value: json.RawMessage(`{"type":"stat","label":"Rev"}`)},
		{Op: "add", Path: "/nodes/n2", Value: json.RawMessage(`{"type":"section","children":[]}`)},
		{Op: "add", Path: "/nodes/n2/children/-", Value: json.RawMessage(`"n1"`)},
		{Op: "add", Path: "/nodes/n2/children/0", Value: json.RawMessage(`"n0"`)},
		{Op: "replace", Path: "/nodes/n1/label", Value: json.RawMessage(`"Revenue"`)},
		{Op: "remove", Path: "/nodes/n2/children/1"},
		{Op: "test", Path: "/title", Value: json.RawMessage(`"Q3"`)}, // dropped
	}
	ops, err := JSONPatchToOps(patch)
	if err != nil {
		t.Fatalf("map: %v", err)
	}

	d := crdtjson.New()
	got := materializeOps(t, d, ops, 1)
	want := `{"nodes":{"n1":{"label":"Revenue","type":"stat"},"n2":{"children":["n0"],"type":"section"}},"title":"Q3"}`
	if got != want {
		t.Fatalf("round-trip mismatch:\n got %s\nwant %s", got, want)
	}

	// Phase 2: a move frame against the live board (doc-aware mapping,
	// current doc = the daemon-materialized JSON).
	cur, err := d.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	ops, err = JSONPatchToOpsWithDoc([]PatchOp{
		{Op: "move", From: "/nodes/n1/label", Path: "/nodes/n1/title"},
	}, cur)
	if err != nil {
		t.Fatalf("map move: %v", err)
	}
	got = materializeOps(t, d, ops, 2)
	want = `{"nodes":{"n1":{"title":"Revenue","type":"stat"},"n2":{"children":["n0"],"type":"section"}},"title":"Q3"}`
	if got != want {
		t.Fatalf("move round-trip mismatch:\n got %s\nwant %s", got, want)
	}
}

// TestJSONPatchSequentialArrayEdits: later ops in one patch see earlier
// ops' effects (both mapper shadow and daemon sequencing agree).
func TestJSONPatchSequentialArrayEdits(t *testing.T) {
	d := crdtjson.New()
	seed, err := JSONPatchToOps([]PatchOp{
		{Op: "add", Path: "/items", Value: json.RawMessage(`["a","b","c"]`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	materializeOps(t, d, seed, 1)
	cur, _ := d.CanonicalJSON()

	// remove "b", then append "d", then insert "x" at the head.
	ops, err := JSONPatchToOpsWithDoc([]PatchOp{
		{Op: "remove", Path: "/items/1"},
		{Op: "add", Path: "/items/-", Value: json.RawMessage(`"d"`)},
		{Op: "add", Path: "/items/0", Value: json.RawMessage(`"x"`)},
	}, cur)
	if err != nil {
		t.Fatal(err)
	}
	got := materializeOps(t, d, ops, 2)
	want := `{"items":["x","a","c","d"]}`
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func intp(i int) *int { return &i }

func fmtIdx(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func pathEq(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		ai, aInt := a[i].(int)
		bi, bInt := b[i].(int)
		if aInt != bInt {
			return false
		}
		if aInt {
			if ai != bi {
				return false
			}
			continue
		}
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

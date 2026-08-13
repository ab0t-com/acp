package crdt_test

import (
	"reflect"
	"testing"

	intcrdt "github.com/ab0t-com/acp/sdk/go/internal/crdt"
	"github.com/ab0t-com/acp/sdk/go/pkg/crdt"
)

// Compile-time one-source-of-truth guards (ACP/EXT-23 §3.2).
var (
	_ intcrdt.ID       = crdt.ID{}
	_ crdt.ID          = intcrdt.ID{}
	_ intcrdt.Op       = crdt.Op{}
	_ intcrdt.OpType   = crdt.OpInsert
	_ intcrdt.ElemWire = crdt.ElemWire{}
	_ intcrdt.State    = crdt.State{}
)

func TestAliasIdentity(t *testing.T) {
	if reflect.TypeOf(crdt.Op{}) != reflect.TypeOf(intcrdt.Op{}) {
		t.Error("crdt.Op is not the internal type (not one source of truth)")
	}
	if reflect.TypeOf(crdt.ID{}) != reflect.TypeOf(intcrdt.ID{}) {
		t.Error("crdt.ID is not the internal type")
	}
	if crdt.OpInsert != intcrdt.OpInsert || crdt.OpDelete != intcrdt.OpDelete {
		t.Error("op-type constants do not match canonical values")
	}
}

// TestTextCoEditViaPublicAPI drives a real two-replica converge using ONLY the
// public crdt surface — proving the authoring helpers a client needs to USE
// PushCRDTOps are exported and functional (not just nameable).
func TestTextCoEditViaPublicAPI(t *testing.T) {
	a := crdt.New("A")
	ops := a.GenerateOps("hello world")
	if len(ops) == 0 {
		t.Fatal("expected generated ops")
	}

	// A fresh replica folds A's ops and must converge to the same text.
	b := crdt.New("B")
	for _, op := range ops {
		b.Apply(op)
	}
	if got := b.Text(); got != "hello world" {
		t.Fatalf("replica B did not converge: %q", got)
	}

	// Snapshot/Load round-trips through the public State type.
	snap := b.Snapshot()
	reloaded := crdt.Load(snap)
	if got := reloaded.Text(); got != "hello world" {
		t.Fatalf("Snapshot/Load lost content: %q", got)
	}

	// A hand-built op names the public Op/ID/OpType/Zero surface and applies.
	c := crdt.New("C")
	manual := crdt.Op{Type: crdt.OpInsert, ID: crdt.ID{Clock: 1, Replica: "C"}, OriginLeft: crdt.Zero, Val: "x"}
	if !c.Apply(manual) {
		t.Fatal("hand-built op did not apply")
	}
	if got := c.Text(); got != "x" {
		t.Fatalf("hand-built op text: %q", got)
	}
}

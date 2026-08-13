package crdtjson_test

import (
	"reflect"
	"testing"

	intcj "github.com/ab0t-com/acp/sdk/go/internal/crdtjson"
	"github.com/ab0t-com/acp/sdk/go/pkg/crdtjson"
)

// Compile-time one-source-of-truth guards (ACP/EXT-23 §3.2).
var (
	_ intcj.Op    = crdtjson.Op{}
	_ crdtjson.Op = intcj.Op{}
)

func TestAliasIdentity(t *testing.T) {
	if reflect.TypeOf(crdtjson.Op{}) != reflect.TypeOf(intcj.Op{}) {
		t.Error("crdtjson.Op is not the internal type (not one source of truth)")
	}
	if reflect.TypeOf(crdtjson.New()) != reflect.TypeOf(intcj.New()) {
		t.Error("crdtjson.Doc is not the internal type")
	}
}

func TestConstantsMatch(t *testing.T) {
	if crdtjson.OpSet != intcj.OpSet ||
		crdtjson.OpDel != intcj.OpDel ||
		crdtjson.OpLIns != intcj.OpLIns ||
		crdtjson.OpLDel != intcj.OpLDel ||
		crdtjson.OpMove != intcj.OpMove ||
		crdtjson.KindMap != intcj.KindMap ||
		crdtjson.KindList != intcj.KindList {
		t.Error("a re-exported op-type/kind constant does not match its canonical value")
	}
	if crdtjson.Root != intcj.Root {
		t.Error("Root does not match the canonical root id")
	}
}

// TestPublicSurfaceUsable proves the public structured-CRDT surface is nameable
// AND functional: a fresh Doc materializes without panic, an Op names the public
// fields, and the ErrUnresolved / IsUnresolved re-exports behave canonically.
func TestPublicSurfaceUsable(t *testing.T) {
	doc := crdtjson.New()
	if doc == nil {
		t.Fatal("crdtjson.New returned nil")
	}
	_ = doc.Materialize() // must not panic

	op := crdtjson.Op{T: crdtjson.OpSet, Key: "status"}
	if op.T != crdtjson.OpSet || op.Key != "status" {
		t.Fatalf("op fields: %+v", op)
	}

	if !crdtjson.IsUnresolved(crdtjson.ErrUnresolved) {
		t.Error("IsUnresolved(ErrUnresolved) should be true")
	}
	if crdtjson.IsUnresolved(nil) {
		t.Error("IsUnresolved(nil) should be false")
	}
}

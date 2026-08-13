// Package crdtjson is the PUBLIC, externally-consumable structured-CRDT (JSON)
// surface of ACP — the types and helpers a client uses to co-edit a structured
// JSON document (the /v1/crdt/json/* endpoints, exposed by the SDK's
// PushCRDTJSONOps / PullCRDTJSONOps / CRDTJSONDoc methods; ext-5, ext-14).
//
// It is exported (per ACP/EXT-23) so a customer's own Go module can NAME the
// operation type in the SDK signatures and author structured documents locally.
// The typical flow uses a local Doc replica:
//
//	doc := crdtjson.New()
//	doc.Apply(peerOp)                 // fold in ops pulled from the daemon
//	view := doc.Materialize()         // read the converged value
//	// (advanced) doc.Canonicalize(...) expands high-level ops before a push
//
// Structured-CRDT op authoring is advanced; most callers read via CRDTJSONDoc
// and mutate via the CLI or higher-level helpers. The op-type and node-kind
// constants below are the building blocks.
//
// One source of truth: every identifier is an ALIAS (or thin re-export) of the
// single canonical definition. Stability tracks the frozen "acp/1" wire.
//
// See rfc/acp-ext-23-external-client-packages.txt.
package crdtjson

import "github.com/ab0t-com/acp/sdk/go/internal/crdtjson"

type (
	// Op is one structured-CRDT mutation (set/del/lins/ldel/mv).
	Op = crdtjson.Op
	// Doc is a local structured-CRDT replica you edit and sync.
	Doc = crdtjson.Doc
)

// Op types (ext-5 §5.1).
const (
	OpSet  = crdtjson.OpSet  // bind a map key / write a register in place
	OpDel  = crdtjson.OpDel  // observed-remove a map key's bindings
	OpLIns = crdtjson.OpLIns // insert a list element after an origin element
	OpLDel = crdtjson.OpLDel // tombstone a list element
	OpMove = crdtjson.OpMove // relocate an existing node, preserving its id (ext-14)
)

// Node kinds as carried in Op.Kind ("" = register, the common case).
const (
	KindMap  = crdtjson.KindMap
	KindList = crdtjson.KindList
)

// Bounds (ext-5 §9.2). Exceeding any is a 400 at the API.
const (
	// MaxDepth caps container-literal / path depth.
	MaxDepth = crdtjson.MaxDepth
	// MaxOps caps canonical ops one push may expand to.
	MaxOps = crdtjson.MaxOps
	// MaxOpValueBytes caps a single op's register value.
	MaxOpValueBytes = crdtjson.MaxOpValueBytes
	// MaxPlacements is the per-document ceiling on total stored placements.
	MaxPlacements = crdtjson.MaxPlacements
)

// Root is the id of the document root node.
var Root = crdtjson.Root

// ErrUnresolved marks a path that cannot be resolved against current state
// (the API maps it to 409 + the current value so the client can rebase).
var ErrUnresolved = crdtjson.ErrUnresolved

// New creates a fresh local structured-CRDT replica.
func New() *Doc { return crdtjson.New() }

// IsUnresolved reports whether err is (or wraps) ErrUnresolved.
func IsUnresolved(err error) bool { return crdtjson.IsUnresolved(err) }

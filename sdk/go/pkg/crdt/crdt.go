// Package crdt is the PUBLIC, externally-consumable text-CRDT surface of ACP —
// the types and helpers a client uses to co-edit a plain-text collaborative
// document (the /v1/crdt/* endpoints, exposed by the SDK's PushCRDTOps /
// PullCRDTOps / CRDTText methods).
//
// It is exported (per ACP/EXT-23) so a customer's own Go module can both NAME
// the operation type in the SDK signatures AND generate correct operations
// locally. The typical authoring flow:
//
//	doc := crdt.New("replica-id")     // a local RGA replica
//	doc.Apply(peerOp)                 // fold in ops pulled from the daemon
//	ops := doc.GenerateOps("new full text")  // diff -> ops to push
//	// client.PushCRDTOps(name, ops, epoch)
//	text := doc.Text()                // read the converged text
//
// One source of truth: every identifier is an ALIAS (or thin re-export) of the
// single canonical definition — no second copy to drift. Stability tracks the
// frozen "acp/1" wire; treat it as a committed public API.
//
// See rfc/acp-ext-23-external-client-packages.txt.
package crdt

import "github.com/ab0t-com/acp/sdk/go/internal/crdt"

type (
	// ID is a CRDT element identity: a (Clock, Replica) pair with a total order.
	ID = crdt.ID
	// OpType is "ins" or "del".
	OpType = crdt.OpType
	// Op is one text-CRDT mutation (insert or delete of a single rune).
	Op = crdt.Op
	// RGA is a replicated text document (a local replica you edit and sync).
	RGA = crdt.RGA
	// ElemWire is one element in a serialized RGA snapshot.
	ElemWire = crdt.ElemWire
	// State is a serializable RGA snapshot (see Snapshot / Load).
	State = crdt.State
)

const (
	// OpInsert marks an insert operation.
	OpInsert = crdt.OpInsert
	// OpDelete marks a delete operation.
	OpDelete = crdt.OpDelete
	// MaxOps caps operations per push (a push exceeding it is a 400).
	MaxOps = crdt.MaxOps
	// MaxOpValueBytes caps one op's value (an insert is a single rune).
	MaxOpValueBytes = crdt.MaxOpValueBytes
)

// Zero is the virtual "beginning of document" origin.
var Zero = crdt.Zero

// New creates a fresh local RGA replica with the given replica id. Use a stable,
// unique replica id per editing client.
func New(replica string) *RGA { return crdt.New(replica) }

// Load reconstructs an RGA from a serialized snapshot (see RGA.Snapshot).
func Load(s State) *RGA { return crdt.Load(s) }

// SortOps orders a slice of ops into the canonical apply order.
func SortOps(ops []Op) { crdt.SortOps(ops) }

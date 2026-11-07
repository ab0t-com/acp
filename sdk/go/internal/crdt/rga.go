// Package crdt implements a sequence CRDT for collaborative text/code editing —
// the layer that lets two agents edit the SAME file concurrently and converge
// automatically, instead of getting a 3-way conflict (doc 08 Q1).
//
// Algorithm: RGA (Replicated Growable Array) with YATA-style integration. Every
// character is an immutable element with a globally unique ID = (Lamport clock,
// replica). An insert records the ID of the element it was placed after
// (OriginLeft); a delete tombstones an element by ID. Operations are
// COMMUTATIVE and IDEMPOTENT: applying the same set of ops in any order, any
// number of times, yields the same document. That is what makes it safe to ship
// ops over an unordered/at-least-once channel and still converge.
//
// Why RGA/YATA and not "skip while id is larger": the naive rule diverges in the
// transitive case (an element that is a child of a concurrently-inserted
// sibling). The integration here compares OriginLeft positions, which is the
// proven-correct rule (the basis of Yjs/Automerge text). See rga_test.go for the
// convergence + transitivity tests.
package crdt

import (
	"sort"
	"sync/atomic"
)

// ID is a unique, totally-ordered identifier for one element.
type ID struct {
	Clock   uint64 `json:"c"`
	Replica string `json:"r"`
}

// Zero is the virtual "beginning of document" origin.
var Zero = ID{}

func (a ID) IsZero() bool    { return a.Clock == 0 && a.Replica == "" }
func (a ID) Equal(b ID) bool { return a.Clock == b.Clock && a.Replica == b.Replica }

// Less gives the total order used to break ties between concurrent inserts at the
// same position: higher clock wins; ties broken by replica id.
func (a ID) Less(b ID) bool {
	if a.Clock != b.Clock {
		return a.Clock < b.Clock
	}
	return a.Replica < b.Replica
}

type OpType string

const (
	OpInsert OpType = "ins"
	OpDelete OpType = "del"
)

// Push bounds (F-INV-11/3), the text-CRDT twin of crdtjson's MaxOps. Without them
// a single /v1/crdt/ops request could carry unbounded ops (each integrated on the
// FSM apply goroutine — a cluster-wide stall) or a single op with a multi-megabyte
// Val (an insert is ONE rune), persisted verbatim as durable write-amplification.
// Enforced at the HTTP boundary; a push exceeding either is a 400.
const (
	// MaxOps caps ops per push (mirrors crdtjson.MaxOps).
	MaxOps = 4096
	// MaxOpValueBytes caps one op's Val. An insert carries a single rune (≤4 UTF-8
	// bytes); 16 is generous headroom while rejecting the megabyte-Val DoS.
	MaxOpValueBytes = 16
)

// Op is one CRDT mutation. For inserts, ID is the new element and OriginLeft is
// the element it follows (Zero = start). For deletes, ID is the target element.
type Op struct {
	Type       OpType `json:"t"`
	ID         ID     `json:"id"`
	OriginLeft ID     `json:"o,omitempty"`
	Val        string `json:"v,omitempty"` // single rune (as string) for inserts
}

type elem struct {
	id         ID
	originLeft ID
	val        rune
	deleted    bool
}

// RGA is a replicated text document.
type RGA struct {
	replica string
	clock   uint64
	els     seq          // the elements in document order (seq.go: O(log n) per place)
	nodes   map[ID]*node // each element by id; the first one when a loaded state repeats an id
	zeroDel bool         // a delete of the zero id was applied (it is known, like any applied op)
	pending []Op         // ops whose causal predecessor isn't present yet

	// live is the number of NON-deleted elements, maintained incrementally so
	// LiveCount() is O(1). It exists so the daemon's compaction predicate never
	// has to build an O(P) SnapshotOps slice under a store lock on every sweeper
	// tick (review finding F1). INVARIANT: live == len(SnapshotOps()). Every site
	// that appends to els, tombstones an element, or rebuilds els must keep that
	// true; TestLiveCountMatchesSnapshot enforces it under randomized streams.
	live int
}

// LiveCount returns the number of visible (non-tombstoned) elements — exactly
// len(SnapshotOps()), in O(1) with no allocation. Compaction predicates use this
// instead of measuring the snapshot, because building the snapshot is O(P) and,
// held under the store mutex across every doc, stalls all reads and writes on
// that store at scale.
func (r *RGA) LiveCount() int { return r.live }

// PendingLen reports how many ops are buffered awaiting a not-yet-present causal
// predecessor (F-INV-5). Compaction MUST NOT run while this is >0: SnapshotOps
// emits only INTEGRATED state, so compacting would permanently drop the buffered-
// but-persisted ops. On the leader/FSM path pending is always 0 (total order), so
// this only guards the hostile/reordered-delivery case.
func (r *RGA) PendingLen() int { return len(r.pending) }

// New creates an empty document for the given replica id.
func New(replica string) *RGA {
	return &RGA{replica: replica, nodes: map[ID]*node{}, els: seq{work: new(atomic.Uint64)}}
}

// observe keeps the local clock ahead of everything seen, so locally-minted IDs
// are always "newest" (Lamport).
func (r *RGA) observe(c uint64) {
	if c > r.clock {
		r.clock = c
	}
}

// Observe moves the clock past c. A replica rebuilt from part of a log calls it for the
// ops it left out, or the ids it mints next could repeat ids it minted there.
func (r *RGA) Observe(c uint64) { r.observe(c) }

// Clock is the replica's Lamport clock. A rebuild carries it over, so no id is minted twice.
func (r *RGA) Clock() uint64 { return r.clock }

// LogReplaced reports whether a document's log is no longer the one this replica was
// synced from at cursor, at an unchanged epoch: the document was deleted and made again,
// or a server was restored. ops is the log from cursor-1 (DocCheckFrom), total its length.
// A shorter log says so outright; a longer one says so in the op this replica last took,
// which it can't hold if that op belongs to another document, insert or delete: a delete's
// id is its target's. An insert it does hold under other text says so too: a writer
// starting a name over on a fixed replica id mints the same ids. Epoch alone can't tell.
// A yes is only a reason to read the whole log: an op no replica can apply, left by a
// writer on a stale view, reads the same, and InstanceGone settles it.
func (r *RGA) LogReplaced(ops []Op, total, cursor int) bool {
	if cursor == 0 {
		return false
	}
	if total < cursor {
		return true
	}
	if len(ops) == 0 {
		return false
	}
	op := ops[0]
	if !r.Knows(op.ID) {
		return true
	}
	if op.Type != OpInsert {
		return false
	}
	x := r.nodeOf(op.ID)
	return x != nil && x.e.val != runeOf(op.Val)
}

// DocCheckFrom is where a pull starts so LogReplaced can look at the op before cursor.
func DocCheckFrom(cursor int) int { return max(cursor-1, 0) }

func (r *RGA) newID() ID {
	r.clock++
	return ID{Clock: r.clock, Replica: r.replica}
}

// indexOf is id's place: -1 for the zero id ("start"), -2 when not present. O(log n).
func (r *RGA) indexOf(id ID) int {
	if id.IsZero() {
		return -1
	}
	x := r.nodes[id]
	if x == nil {
		return -2 // not present
	}
	return r.els.pos(x)
}

// nodeOf is id's element, nil for the zero id or one not present (indexOf < 0).
func (r *RGA) nodeOf(id ID) *node {
	if id.IsZero() {
		return nil
	}
	return r.nodes[id]
}

// Knows reports whether an op with this id has been applied (an inserted element, live or
// deleted, or an applied delete). O(1). A delete still buffered for a missing target is
// NOT known, which is what lets a server refuse a pre-squatted delete of a future id (D-3).
// An applied delete names an element that is present, or the zero id.
func (r *RGA) Knows(id ID) bool { return r.nodes[id] != nil || (r.zeroDel && id.IsZero()) }

// Apply integrates a remote (or replayed) op. Returns true if applied, false if
// buffered pending its predecessor. Idempotent.
func (r *RGA) Apply(op Op) bool {
	r.observe(op.ID.Clock)
	switch op.Type {
	case OpInsert:
		// HAZARD: HZ-ACPDB-02 (S2, fixed 2026-10-29 at the coordd boundary — D-3) op.ID is client-supplied; coordd now binds an insert's replica to the caller -> HAZARDS.md#HZ-ACPDB-02
		if r.Knows(op.ID) {
			return true // already applied
		}
		if !op.OriginLeft.IsZero() && r.nodes[op.OriginLeft] == nil {
			r.buffer(op)
			return false // origin not here yet
		}
		r.integrate(elem{id: op.ID, originLeft: op.OriginLeft, val: runeOf(op.Val)})
	case OpDelete:
		if !op.ID.IsZero() && r.nodes[op.ID] == nil {
			r.buffer(op)
			return false // target not here yet
		}
		r.deleteLocked(op.ID)
	}
	r.drainPending()
	return true
}

func (r *RGA) buffer(op Op) {
	for _, p := range r.pending {
		if p.ID.Equal(op.ID) && p.Type == op.Type {
			return
		}
	}
	r.pending = append(r.pending, op)
}

// drainPending retries buffered ops until no more can be applied (fixpoint).
func (r *RGA) drainPending() {
	for {
		progress := false
		keep := r.pending[:0]
		pend := r.pending
		r.pending = nil
		for _, op := range pend {
			ready := op.Type == OpDelete && (op.ID.IsZero() || r.nodes[op.ID] != nil) ||
				op.Type == OpInsert && (op.OriginLeft.IsZero() || r.nodes[op.OriginLeft] != nil)
			if !ready {
				keep = append(keep, op)
				continue
			}
			// re-enter via a non-buffering fast path
			if op.Type == OpInsert {
				if r.Knows(op.ID) {
					progress = true
					continue
				}
				r.integrate(elem{id: op.ID, originLeft: op.OriginLeft, val: runeOf(op.Val)})
			} else {
				// The SAME guarded mutation as Apply's delete branch (and the TS engine's
				// drainPending): tombstone once, live-- once. Pre-fix this branch set the
				// flag without live--, so a delete that arrived before its target left
				// LiveCount one too high (HZ-ACPDB-44, Docs investigation D3).
				r.deleteLocked(op.ID)
			}
			progress = true
		}
		r.pending = append(r.pending, keep...)
		if !progress {
			return
		}
	}
}

// deleteLocked applies a delete whose target is present (or is the zero id): tombstone
// once, live-- once; the op is known from then on.
func (r *RGA) deleteLocked(id ID) {
	if x := r.nodeOf(id); x != nil && !x.e.deleted {
		x.e.deleted = true
		r.live-- // keep live == len(SnapshotOps()); re-deleting must not double-count
	}
	if id.IsZero() {
		r.zeroDel = true
	}
}

// integrate places a new element using the YATA rule: find the slot after the
// element's left origin, then among already-present elements skip those that
// should sort before us — comparing by origin position first, then by ID. The
// elements are walked in order from the slot (seq.next), each origin's place looked
// up in O(log n): the same steps the slice scan took, in O(log n) each.
func (r *RGA) integrate(e elem) {
	left := r.indexOf(e.originLeft) // -1 means "start"
	i := left + 1
	var cn *node
	if left == -1 {
		cn = r.els.first()
	} else {
		cn = r.els.next(r.nodes[e.originLeft])
	}
	for ; cn != nil; cn = r.els.next(cn) {
		c := cn.e
		cLeft := r.indexOf(c.originLeft)
		if cLeft < left {
			break // c is anchored left of us -> insert before it
		}
		if cLeft == left {
			// same origin: order by ID descending (higher ID first)
			if c.id.Less(e.id) {
				break // we are higher -> go before c
			}
			// c higher -> skip past it
		}
		// cLeft > left: c is inside a sibling's subtree to our left -> skip
		i++
	}
	x := r.els.insertAt(i, e)
	if r.nodes[e.id] == nil {
		r.nodes[e.id] = x
	}
	if !e.deleted {
		r.live++
	}
}

// Text materializes the current visible document.
func (r *RGA) Text() string {
	out := make([]rune, 0, r.live)
	r.els.each(func(e *elem) {
		if !e.deleted {
			out = append(out, e.val)
		}
	})
	return string(out)
}

// GenerateOps diffs the current document against newText, produces the minimal
// insert/delete ops to transform it, APPLIES them locally, and returns them for
// broadcast. This is the bridge that lets an agent just edit a file as text.
//
// All inserts come before all deletes. A large edit is sent in several requests, so the
// order is what a reader sees meanwhile and what is left if the sender stops: extra
// text for a moment, never a document with a hole in it (R-C0). An insert's anchor is
// still there when it lands because nothing has been deleted yet.
func (r *RGA) GenerateOps(newText string) []Op {
	visible := make([]elem, 0, r.live)
	r.els.each(func(e *elem) {
		if !e.deleted {
			visible = append(visible, *e)
		}
	})
	oldR := make([]rune, len(visible))
	for i, e := range visible {
		oldR[i] = e.val
	}
	newR := []rune(newText)
	steps := diff(oldR, newR)

	var ops []Op
	var gone []ID
	leftID := Zero
	oi := 0 // index into visible
	for _, s := range steps {
		switch s.kind {
		case keep:
			leftID = visible[oi].id
			oi++
		case del:
			gone = append(gone, visible[oi].id)
			oi++
		case ins:
			id := r.newID()
			op := Op{Type: OpInsert, ID: id, OriginLeft: leftID, Val: string(s.r)}
			ops = append(ops, op)
			r.Apply(op)
			leftID = id
		}
	}
	for _, id := range gone {
		op := Op{Type: OpDelete, ID: id}
		ops = append(ops, op)
		r.Apply(op)
	}
	return ops
}

// DeletesFor counts the characters GenerateOps(newText) would delete from the document,
// without changing it.
func (r *RGA) DeletesFor(newText string) int {
	n := 0
	for _, s := range diff([]rune(r.Text()), []rune(newText)) {
		if s.kind == del {
			n++
		}
	}
	return n
}

// SnapshotOps returns the minimal op set that reproduces the current VISIBLE
// document — tombstones are garbage-collected. Each live element is re-anchored
// to the previous live element (so dropping dead elements can't orphan a
// surviving one's OriginLeft), preserving element IDs so a resynced replica's new
// ops still line up. Replaying on an empty RGA yields identical text.
//
// SAFETY: GC-ing tombstones is only sound behind an epoch barrier — a replica
// must rebase onto the post-compaction snapshot before it may push again, so it
// can never emit an op whose OriginLeft was a GC'd element. crdtstore enforces
// that barrier (Compact bumps the epoch; AppendOps rejects stale epochs).
func (r *RGA) SnapshotOps() []Op {
	ops := make([]Op, 0, r.live)
	prev := Zero
	r.els.each(func(e *elem) {
		if e.deleted {
			return
		}
		ops = append(ops, Op{Type: OpInsert, ID: e.id, OriginLeft: prev, Val: string(e.val)})
		prev = e.id
	})
	return ops
}

// --- persistence (for the client shadow) ---

type ElemWire struct {
	ID         ID     `json:"id"`
	OriginLeft ID     `json:"o"`
	Val        string `json:"v"`
	Deleted    bool   `json:"d,omitempty"`
}

type State struct {
	Replica string     `json:"replica"`
	Clock   uint64     `json:"clock"`
	Els     []ElemWire `json:"els"`
}

func (r *RGA) Snapshot() State {
	s := State{Replica: r.replica, Clock: r.clock}
	r.els.each(func(e *elem) {
		s.Els = append(s.Els, ElemWire{ID: e.id, OriginLeft: e.originLeft, Val: string(e.val), Deleted: e.deleted})
	})
	return s
}

func Load(s State) *RGA {
	r := &RGA{replica: s.Replica, clock: s.Clock, nodes: make(map[ID]*node, len(s.Els)), els: seq{work: new(atomic.Uint64)}}
	nodes := r.els.build(len(s.Els), func(i int) elem { // O(n): the tree these inserts at the end would make
		ew := s.Els[i]
		return elem{id: ew.ID, originLeft: ew.OriginLeft, val: runeOf(ew.Val), deleted: ew.Deleted}
	})
	for i, ew := range s.Els {
		if r.nodes[ew.ID] == nil { // a repeated id resolves to its first copy
			r.nodes[ew.ID] = &nodes[i]
		}
		if !ew.Deleted {
			r.live++ // rebuild the incremental counter from the restored state
		}
	}
	return r
}

func runeOf(s string) rune {
	for _, c := range s {
		return c
	}
	return 0
}

// SortOps gives a deterministic order for storage/display (by clock, replica).
// Not required for convergence — ops commute — but handy for stable op-logs.
func SortOps(ops []Op) {
	sort.SliceStable(ops, func(a, b int) bool { return ops[a].ID.Less(ops[b].ID) })
}

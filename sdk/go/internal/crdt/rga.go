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

import "sort"

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
	els     []elem
	have    map[ID]bool // applied op ids (idempotency)
	pending []Op        // ops whose causal predecessor isn't present yet

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
	return &RGA{replica: replica, have: map[ID]bool{}}
}

// observe keeps the local clock ahead of everything seen, so locally-minted IDs
// are always "newest" (Lamport).
func (r *RGA) observe(c uint64) {
	if c > r.clock {
		r.clock = c
	}
}

func (r *RGA) newID() ID {
	r.clock++
	return ID{Clock: r.clock, Replica: r.replica}
}

func (r *RGA) indexOf(id ID) int {
	if id.IsZero() {
		return -1
	}
	for i := range r.els {
		if r.els[i].id.Equal(id) {
			return i
		}
	}
	return -2 // not present
}

// Apply integrates a remote (or replayed) op. Returns true if applied, false if
// buffered pending its predecessor. Idempotent.
func (r *RGA) Apply(op Op) bool {
	r.observe(op.ID.Clock)
	switch op.Type {
	case OpInsert:
		// HAZARD: HZ-ACPDB-02 (S2) op.ID is client-supplied, not bound to the caller -> id-squatting -> HAZARDS.md#HZ-ACPDB-02 (D-3)
		if r.have[op.ID] {
			return true // already applied
		}
		if !op.OriginLeft.IsZero() && r.indexOf(op.OriginLeft) == -2 {
			r.buffer(op)
			return false // origin not here yet
		}
		r.integrate(elem{id: op.ID, originLeft: op.OriginLeft, val: runeOf(op.Val)})
		r.have[op.ID] = true
	case OpDelete:
		i := r.indexOf(op.ID)
		if i == -2 {
			r.buffer(op)
			return false // target not here yet
		}
		if i >= 0 && !r.els[i].deleted {
			r.els[i].deleted = true
			r.live-- // keep live == len(SnapshotOps()); re-deleting must not double-count
		}
		r.have[op.ID] = true
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
			ready := op.Type == OpDelete && r.indexOf(op.ID) >= -1 ||
				op.Type == OpInsert && (op.OriginLeft.IsZero() || r.indexOf(op.OriginLeft) >= -1)
			if !ready {
				keep = append(keep, op)
				continue
			}
			// re-enter via a non-buffering fast path
			if op.Type == OpInsert {
				if r.have[op.ID] {
					progress = true
					continue
				}
				r.integrate(elem{id: op.ID, originLeft: op.OriginLeft, val: runeOf(op.Val)})
				r.have[op.ID] = true
			} else {
				if i := r.indexOf(op.ID); i >= 0 {
					r.els[i].deleted = true
				}
				r.have[op.ID] = true
			}
			progress = true
		}
		r.pending = append(r.pending, keep...)
		if !progress {
			return
		}
	}
}

// integrate places a new element using the YATA rule: find the slot after the
// element's left origin, then among already-present elements skip those that
// should sort before us — comparing by origin position first, then by ID.
func (r *RGA) integrate(e elem) {
	left := r.indexOf(e.originLeft) // -1 means "start"
	i := left + 1
	for i < len(r.els) {
		c := r.els[i]
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
	r.els = append(r.els, elem{})
	copy(r.els[i+1:], r.els[i:])
	r.els[i] = e
	if !e.deleted {
		r.live++
	}
}

// Text materializes the current visible document.
func (r *RGA) Text() string {
	out := make([]rune, 0, len(r.els))
	for _, e := range r.els {
		if !e.deleted {
			out = append(out, e.val)
		}
	}
	return string(out)
}

// GenerateOps diffs the current document against newText, produces the minimal
// insert/delete ops to transform it, APPLIES them locally, and returns them for
// broadcast. This is the bridge that lets an agent just edit a file as text.
func (r *RGA) GenerateOps(newText string) []Op {
	visible := make([]elem, 0, len(r.els))
	for _, e := range r.els {
		if !e.deleted {
			visible = append(visible, e)
		}
	}
	oldR := make([]rune, len(visible))
	for i, e := range visible {
		oldR[i] = e.val
	}
	newR := []rune(newText)
	steps := diff(oldR, newR)

	var ops []Op
	leftID := Zero
	oi := 0 // index into visible
	for _, s := range steps {
		switch s.kind {
		case keep:
			leftID = visible[oi].id
			oi++
		case del:
			id := visible[oi].id
			ops = append(ops, Op{Type: OpDelete, ID: id})
			r.Apply(Op{Type: OpDelete, ID: id})
			oi++
		case ins:
			id := r.newID()
			op := Op{Type: OpInsert, ID: id, OriginLeft: leftID, Val: string(s.r)}
			ops = append(ops, op)
			r.Apply(op)
			leftID = id
		}
	}
	return ops
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
	ops := make([]Op, 0, len(r.els))
	prev := Zero
	for _, e := range r.els {
		if e.deleted {
			continue
		}
		ops = append(ops, Op{Type: OpInsert, ID: e.id, OriginLeft: prev, Val: string(e.val)})
		prev = e.id
	}
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
	for _, e := range r.els {
		s.Els = append(s.Els, ElemWire{ID: e.id, OriginLeft: e.originLeft, Val: string(e.val), Deleted: e.deleted})
	}
	return s
}

func Load(s State) *RGA {
	r := &RGA{replica: s.Replica, clock: s.Clock, have: map[ID]bool{}}
	for _, ew := range s.Els {
		r.els = append(r.els, elem{id: ew.ID, originLeft: ew.OriginLeft, val: runeOf(ew.Val), deleted: ew.Deleted})
		r.have[ew.ID] = true
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

// --- minimal LCS diff over runes ---

type kind int

const (
	keep kind = iota
	del
	ins
)

type step struct {
	kind kind
	r    rune // for ins
}

// maxDiffRunes caps the per-side length fed to the O(n*m) LCS matrix (CI-W2-6,
// twin of internal/merge's T38 cap). GenerateOps runs this on the CLIENT to turn a
// new text into ops; a ~50k×50k matrix is ~10GiB of ints, so a large paste/file
// would OOM the caller. Above the cap we skip the LCS and emit a linear
// delete-all + insert-all script — still a correct (just non-minimal) edit that
// converges — bounding memory to O(n+m).
const maxDiffRunes = 1 << 16

// diff returns an edit script transforming old -> new using a classic LCS DP.
// O(len(old)*len(new)) — fine for source files; above maxDiffRunes it falls back
// to a linear replace-all to avoid the quadratic-memory DoS (CI-W2-6).
func diff(old, new []rune) []step {
	n, m := len(old), len(new)
	if n > maxDiffRunes || m > maxDiffRunes {
		steps := make([]step, 0, n+m)
		for i := 0; i < n; i++ {
			steps = append(steps, step{kind: del})
		}
		for j := 0; j < m; j++ {
			steps = append(steps, step{kind: ins, r: new[j]})
		}
		return steps
	}
	// lcs[i][j] = length of LCS of old[i:], new[j:]
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if old[i] == new[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var steps []step
	i, j := 0, 0
	for i < n && j < m {
		if old[i] == new[j] {
			steps = append(steps, step{kind: keep})
			i++
			j++
		} else if lcs[i+1][j] >= lcs[i][j+1] {
			steps = append(steps, step{kind: del})
			i++
		} else {
			steps = append(steps, step{kind: ins, r: new[j]})
			j++
		}
	}
	for ; i < n; i++ {
		steps = append(steps, step{kind: del})
	}
	for ; j < m; j++ {
		steps = append(steps, step{kind: ins, r: new[j]})
	}
	return steps
}

// SortOps gives a deterministic order for storage/display (by clock, replica).
// Not required for convergence — ops commute — but handy for stable op-logs.
func SortOps(ops []Op) {
	sort.SliceStable(ops, func(a, b int) bool { return ops[a].ID.Less(ops[b].ID) })
}

package crdt

// refRGA is the RGA as it was before R-C21, kept verbatim (a scan of a slice per lookup)
// as the reference the order-statistic list is checked against: every result must be
// byte-identical (TestSeqMatchesTheSliceRGA). Test-only; never built into the package.

type refElem struct {
	id         ID
	originLeft ID
	val        rune
	deleted    bool
}

// refRGA is a replicated text document.
type refRGA struct {
	replica string
	clock   uint64
	els     []refElem
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
func (r *refRGA) LiveCount() int { return r.live }

// PendingLen reports how many ops are buffered awaiting a not-yet-present causal
// predecessor (F-INV-5). Compaction MUST NOT run while this is >0: SnapshotOps
// emits only INTEGRATED state, so compacting would permanently drop the buffered-
// but-persisted ops. On the leader/FSM path pending is always 0 (total order), so
// this only guards the hostile/reordered-delivery case.
func (r *refRGA) PendingLen() int { return len(r.pending) }

// New creates an empty document for the given replica id.
func refNew(replica string) *refRGA {
	return &refRGA{replica: replica, have: map[ID]bool{}}
}

// observe keeps the local clock ahead of everything seen, so locally-minted IDs
// are always "newest" (Lamport).
func (r *refRGA) observe(c uint64) {
	if c > r.clock {
		r.clock = c
	}
}

// Observe moves the clock past c. A replica rebuilt from part of a log calls it for the
// ops it left out, or the ids it mints next could repeat ids it minted there.
func (r *refRGA) Observe(c uint64) { r.observe(c) }

// Clock is the replica's Lamport clock. A rebuild carries it over, so no id is minted twice.
func (r *refRGA) Clock() uint64 { return r.clock }

// LogReplaced reports whether a document's log is no longer the one this replica was
// synced from at cursor, at an unchanged epoch: the document was deleted and made again,
// or a server was restored. ops is the log from cursor-1 (DocCheckFrom), total its length.
// A shorter log says so outright; a longer one says so in the op this replica last took,
// which it can't hold if that op belongs to another document, insert or delete: a delete's
// id is its target's. An insert it does hold under other text says so too: a writer
// starting a name over on a fixed replica id mints the same ids. Epoch alone can't tell.
// A yes is only a reason to read the whole log: an op no replica can apply, left by a
// writer on a stale view, reads the same, and InstanceGone settles it.
func (r *refRGA) LogReplaced(ops []Op, total, cursor int) bool {
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
	i := r.indexOf(op.ID)
	return i >= 0 && r.els[i].val != runeOf(op.Val)
}

func (r *refRGA) newID() ID {
	r.clock++
	return ID{Clock: r.clock, Replica: r.replica}
}

func (r *refRGA) indexOf(id ID) int {
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

// Knows reports whether an op with this id has been applied (an inserted element, live or
// deleted, or an applied delete). O(1). A delete still buffered for a missing target is
// NOT known, which is what lets a server refuse a pre-squatted delete of a future id (D-3).
func (r *refRGA) Knows(id ID) bool { return r.have[id] }

// Apply integrates a remote (or replayed) op. Returns true if applied, false if
// buffered pending its predecessor. Idempotent.
func (r *refRGA) Apply(op Op) bool {
	r.observe(op.ID.Clock)
	switch op.Type {
	case OpInsert:
		// HAZARD: HZ-ACPDB-02 (S2, fixed 2026-10-29 at the coordd boundary — D-3) op.ID is client-supplied; coordd now binds an insert's replica to the caller -> HAZARDS.md#HZ-ACPDB-02
		if r.have[op.ID] {
			return true // already applied
		}
		if !op.OriginLeft.IsZero() && r.indexOf(op.OriginLeft) == -2 {
			r.buffer(op)
			return false // origin not here yet
		}
		r.integrate(refElem{id: op.ID, originLeft: op.OriginLeft, val: runeOf(op.Val)})
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

func (r *refRGA) buffer(op Op) {
	for _, p := range r.pending {
		if p.ID.Equal(op.ID) && p.Type == op.Type {
			return
		}
	}
	r.pending = append(r.pending, op)
}

// drainPending retries buffered ops until no more can be applied (fixpoint).
func (r *refRGA) drainPending() {
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
				r.integrate(refElem{id: op.ID, originLeft: op.OriginLeft, val: runeOf(op.Val)})
				r.have[op.ID] = true
			} else {
				// The SAME guarded mutation as Apply's delete branch (and the TS engine's
				// drainPending): tombstone once, live-- once. Pre-fix this branch set the
				// flag without live--, so a delete that arrived before its target left
				// LiveCount one too high (HZ-ACPDB-44, Docs investigation D3).
				if i := r.indexOf(op.ID); i >= 0 && !r.els[i].deleted {
					r.els[i].deleted = true
					r.live--
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
func (r *refRGA) integrate(e refElem) {
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
	r.els = append(r.els, refElem{})
	copy(r.els[i+1:], r.els[i:])
	r.els[i] = e
	if !e.deleted {
		r.live++
	}
}

// Text materializes the current visible document.
func (r *refRGA) Text() string {
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
//
// All inserts come before all deletes. A large edit is sent in several requests, so the
// order is what a reader sees meanwhile and what is left if the sender stops: extra
// text for a moment, never a document with a hole in it (R-C0). An insert's anchor is
// still there when it lands because nothing has been deleted yet.
func (r *refRGA) GenerateOps(newText string) []Op {
	visible := make([]refElem, 0, len(r.els))
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
func (r *refRGA) DeletesFor(newText string) int {
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
// ops still line up. Replaying on an empty refRGA yields identical text.
//
// SAFETY: GC-ing tombstones is only sound behind an epoch barrier — a replica
// must rebase onto the post-compaction snapshot before it may push again, so it
// can never emit an op whose OriginLeft was a GC'd element. crdtstore enforces
// that barrier (Compact bumps the epoch; AppendOps rejects stale epochs).
func (r *refRGA) SnapshotOps() []Op {
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

func (r *refRGA) Snapshot() State {
	s := State{Replica: r.replica, Clock: r.clock}
	for _, e := range r.els {
		s.Els = append(s.Els, ElemWire{ID: e.id, OriginLeft: e.originLeft, Val: string(e.val), Deleted: e.deleted})
	}
	return s
}

func refLoad(s State) *refRGA {
	r := &refRGA{replica: s.Replica, clock: s.Clock, have: map[ID]bool{}}
	for _, ew := range s.Els {
		r.els = append(r.els, refElem{id: ew.ID, originLeft: ew.OriginLeft, val: runeOf(ew.Val), deleted: ew.Deleted})
		r.have[ew.ID] = true
		if !ew.Deleted {
			r.live++ // rebuild the incremental counter from the restored state
		}
	}
	return r
}

// Package crdtjson implements the structured (JSON-graph) CRDT of acp-ext-5:
// a conflict-free replicated type whose materialized value is a JSON structure
// (objects, arrays, scalars) rather than a character sequence. It composes
// three proven pieces — an OR-map (observed-remove) for objects, an
// LWW-register for scalar leaves, and, for arrays, the SAME RGA/YATA sequence
// engine the text CRDT uses (internal/crdt is REUSED, not re-implemented: each
// LIST node embeds a crdt.RGA for ordering/tombstones plus a side table from
// element id to child node id).
//
// THE invariant of this package is CONVERGENCE: applying the same set of ops
// in ANY order, any number of times, yields byte-identical materialized JSON
// on every replica. Every rule below exists to keep operations COMMUTATIVE and
// IDEMPOTENT:
//
//   - MAP keys hold a SET of live bindings, each tagged by its creating op id.
//     A del removes only OBSERVED tags (stamped into the op by the leader), so
//     a concurrent add — a tag the del never saw — survives (add-wins). Removed
//     tags are remembered, so a del arriving BEFORE its add still wins over it.
//   - When a key has several live bindings (concurrent sets), the materialized
//     winner is chosen by the LWW rule: greatest (now, actor, op id) — all
//     three leader-stamped (ext-5 §3.4 / plan D3), so "last writer" means
//     "last arrival at the leader", identical on every replica.
//   - A REGISTER holds one JSON scalar; an in-place write keeps the greatest
//     (now, actor, op id) stamp. Register creation rides the set/lins op that
//     binds it.
//   - LIST order and tombstones are delegated verbatim to crdt.RGA — the
//     proven insert-after-origin integration, including its causal buffer.
//   - An op whose TARGET node is not present yet is BUFFERED (never rejected)
//     and drained to a fixpoint, mirroring rga.go's have/pending discipline —
//     so child-before-parent delivery converges once the set is complete.
//   - Node KIND conflicts (two ops minting the same nid as different kinds —
//     impossible from the single minting leader, but the type must not diverge
//     on hostile input) are resolved by per-kind creation CLAIMS merged by
//     MINIMUM stamp: a commutative, idempotent rule, so replicas agree no
//     matter the delivery order.
//
// PRECONDITION (leader-minted ids): distinct ops carry distinct op ids —
// Canonicalize, the single mint authority, guarantees it (it overwrites any
// client-supplied id/nid/stamp, ext-5 §3.4/§9.4). Duplicate DELIVERY of the
// same op is always safe (idempotent); two DIFFERENT ops sharing an id are
// outside the contract, exactly as in internal/crdt.
//
// Materialization marshals maps with SORTED KEYS — encoding/json sorts
// map[string]T keys, and CanonicalJSON is the one sanctioned marshal path —
// because an unsorted marshal would be a byte-level replica divergence
// (plan D4). Nothing in Apply/Materialize reads a clock or randomness.
package crdtjson

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/ab0t-com/acp/sdk/go/internal/crdt"
)

// Root is the reserved nid of a document's root MAP node (ext-5 §4.4).
var Root = crdt.ID{Clock: 0, Replica: "_"}

// Op types (ext-5 §5.1).
const (
	OpSet  = "set"  // bind a map key to a new value / write a register in place
	OpDel  = "del"  // observed-remove a map key's bindings
	OpLIns = "lins" // insert a list element after an origin element
	OpLDel = "ldel" // tombstone a list element
	OpMove = "mv"   // relocate an EXISTING node, preserving its nid (acp-ext-14)
)

// Node kinds as carried in Op.Kind ("" = register, the common case).
const (
	KindMap  = "map"
	KindList = "list"
	kindReg  = "reg" // internal claim key; the wire encodes a register as ""
)

// Bounds (ext-5 §9.2 MUSTs): container-literal/path depth and the number of
// canonical ops one push may expand to. Exceeding either is a 400 at the API.
const (
	MaxDepth = 32
	MaxOps   = 4096
)

// MaxOpValueBytes caps a single op's register Value (CI-W2-1, twin of the text
// CRDT's MaxOpValueBytes). Without it one /v1/crdt/json/ops op could carry a
// multi-hundred-MB JSON value (bounded only by the 256MiB body cap), persisted
// verbatim to every replica's op-log as durable write-amplification. 1 MiB is a
// generous ceiling for a single JSON register while closing the amplification.
const MaxOpValueBytes = 1 << 20

// MaxPlacements is the per-DOCUMENT ceiling on total stored placements/ops
// (F-INV-33). MaxOps bounds one PUSH; nothing bounded a doc's lifetime total, so
// an all-live doc (distinct keys, never trips the dead-fraction compaction gate)
// could grow its placement set P without limit — and resolveView is O(P log P)
// REBUILT on every op/read, on the FSM goroutine, so unbounded P is a super-linear
// stall, not just memory. A push that would carry the doc past this ceiling is
// refused at the leader boundary (a 400-class error), exactly as MaxOps/MaxDepth
// are. Deterministic: the count is replicated state, so every replica agrees.
// Generous (1,048,576) so it never bites a legitimate large document; it is the
// DoS backstop, not a working limit.
const MaxPlacements = 1 << 20

// ErrUnresolved marks a PATH that cannot be resolved against current state
// (ext-5 §5.6). The API maps it to 409 + the current materialized value so
// the client can rebase; every other Canonicalize error is a plain 400.
var ErrUnresolved = errors.New("path unresolved")

// IsUnresolved reports whether err is a path-resolution failure (the 409
// class of ext-5 §5.6); any other Canonicalize error is a 400.
func IsUnresolved(err error) bool { return errors.Is(err, ErrUnresolved) }

// Op is one structured-CRDT mutation, in the canonical NID form that is
// stored and replicated. Path/Idx are the client-convenience form: the leader
// resolves them in Canonicalize and they are never stored (ext-5 §5.2/§5.4).
//
// ID is the op's unique, leader-minted identity and does triple duty exactly
// as in the text CRDT: idempotency key, the binding TAG for a map set, and
// the ELEMENT id for a lins.
type Op struct {
	T  string  `json:"t"`
	ID crdt.ID `json:"id"`

	Target crdt.ID `json:"target"`                 // parent node (all four op types)
	Key    string  `json:"key,omitempty"`          // set/del on a MAP
	Origin crdt.ID `json:"index_origin,omitempty"` // lins: element to insert after (zero id or Root = head)
	Elem   crdt.ID `json:"elem,omitempty"`         // ldel: element to tombstone

	NID   crdt.ID         `json:"nid,omitempty"`   // set/lins: id of the node this op creates; zero = in-place register write
	Kind  string          `json:"kind,omitempty"`  // created node kind: "" register | "map" | "list"
	Value json.RawMessage `json:"value,omitempty"` // register payload (scalar); container literals are expanded pre-propose

	Now   int64     `json:"now,omitempty"`   // leader wall stamp — the LWW major key (plan D3)
	Actor string    `json:"actor,omitempty"` // authenticated writer — the LWW middle key
	Tags  []crdt.ID `json:"tags,omitempty"`  // del: observed binding tags, stamped by the leader;
	//                                          mv: the source-slot GHOST-PURGE set (ext-14 §4.2)

	// mv (acp-ext-14): the EXISTING node being relocated. A mv creates a second
	// placement of Child under Target (Key for a map dest, or a fresh list elem
	// = op.ID after Origin). It carries NO NID/Kind/Value — the node exists.
	Child crdt.ID `json:"child,omitempty"`
	// mv: the SOURCE parent node whose slot the GHOST-PURGE Tags belong to (a map
	// source only). The ghost tags are retired under THIS node, which may differ
	// from Target (the destination parent). Zero when the source is a list or the
	// child was unplaced (Tags then empty). ext-14 §4.2.
	Src crdt.ID `json:"src,omitempty"`

	// Client-convenience addressing (resolved + cleared by Canonicalize).
	Path     []any `json:"path,omitempty"`
	FromPath []any `json:"from,omitempty"` // mv: client-convenience source path
	Idx      *int  `json:"idx,omitempty"`  // lins: live-index position, resolved to Origin
}

// stamp is the LWW ordering token: (leader now, actor, op id). Total order —
// op ids are unique — so every comparison has one deterministic answer.
type stamp struct {
	now   int64
	actor string
	id    crdt.ID
}

func (a stamp) less(b stamp) bool {
	if a.now != b.now {
		return a.now < b.now
	}
	if a.actor != b.actor {
		return a.actor < b.actor
	}
	return a.id.Less(b.id)
}

// binding is one live (key -> child) entry of an OR-map, tagged by the op
// that created it. tag == st.id always.
type binding struct {
	tag   crdt.ID
	child crdt.ID
	st    stamp
}

// node is one vertex of the document graph. Facets (map/list/register state)
// are lazily allocated and independent: ops always mutate their own facet, and
// the MATERIALIZED kind is the claim with the smallest stamp — a commutative
// choice, so hostile duplicate-nid input cannot make replicas disagree.
type node struct {
	claims map[string]stamp // kind -> smallest creation stamp

	// register facet
	val    json.RawMessage
	vst    stamp
	hasVal bool

	// map facet
	bindings map[string][]binding
	removed  map[crdt.ID]bool // observed-removed tags (works out-of-order)

	// list facet: crdt.RGA is the REUSED ordering engine (element payloads are
	// placeholders); elems maps element id -> the placement (child nid + stamp).
	// The stamp is carried so a mv-created list placement can be arbitrated
	// against map placements by location() (ext-14 §4.1); a lins-created
	// placement stamps the lins op.
	seq   *crdt.RGA
	elems map[crdt.ID]elemPlace
}

// elemPlace is one list placement: the child nid plus the stamp of the op that
// created the placement (lins or mv).
type elemPlace struct {
	child crdt.ID
	st    stamp
}

func (n *node) claim(kind string, st stamp) {
	if s, ok := n.claims[kind]; !ok || st.less(s) {
		n.claims[kind] = st
	}
}

// kind returns the materialized kind: the claim with the smallest stamp
// (order-free minimum; ties impossible — stamps embed unique op ids).
func (n *node) kind() string {
	win, first := "", true
	var ws stamp
	for k, s := range n.claims {
		if first || s.less(ws) || (s == ws && k < win) {
			win, ws, first = k, s, false
		}
	}
	return win
}

func (n *node) mapFacet() {
	if n.bindings == nil {
		n.bindings = map[string][]binding{}
		n.removed = map[crdt.ID]bool{}
	}
}

func (n *node) listFacet() {
	if n.seq == nil {
		n.seq = crdt.New("j") // replica name unused: we never call GenerateOps
		n.elems = map[crdt.ID]elemPlace{}
	}
}

func (n *node) writeReg(val json.RawMessage, st stamp) {
	if !n.hasVal || n.vst.less(st) {
		n.val, n.vst, n.hasVal = val, st, true
	}
}

// NOTE (ext-14): bindings are NOT physically stripped on del/mv-ghost-purge.
// A tag in `removed` marks its binding DEAD everywhere it is read (resolveView
// liveness, snapshot), but the binding STAYS in place so a DEAD placement still
// participates in location arbitration. This is what makes "no fallback past a
// dead location" hold: when a moved node's winning location is deleted, its
// location still resolves to that (dead) placement and it renders nowhere —
// rather than falling back to an older live placement (a resurrection). Dead
// bindings are garbage-collected by compaction (SnapshotOps walks the resolved
// view, emitting only live winners).

func (n *node) clone() *node {
	c := &node{claims: make(map[string]stamp, len(n.claims)), val: n.val, vst: n.vst, hasVal: n.hasVal}
	for k, s := range n.claims {
		c.claims[k] = s
	}
	if n.bindings != nil {
		c.bindings = make(map[string][]binding, len(n.bindings))
		for k, bs := range n.bindings {
			c.bindings[k] = append([]binding(nil), bs...)
		}
		c.removed = make(map[crdt.ID]bool, len(n.removed))
		for t := range n.removed {
			c.removed[t] = true
		}
	}
	if n.seq != nil {
		// Snapshot/Load round-trips elements + tombstones exactly. rga-internal
		// PENDING ops are dropped — acceptable: clones exist only for leader-side
		// resolution, and the leader's doc never has rga-pending ops (the FSM
		// applies the totally-ordered log, so an origin always precedes its
		// children).
		c.seq = crdt.Load(n.seq.Snapshot())
		c.elems = make(map[crdt.ID]elemPlace, len(n.elems))
		for e, ep := range n.elems {
			c.elems[e] = ep
		}
	}
	return c
}

// Doc is one structured document: a graph of nodes rooted at Root.
type Doc struct {
	nodes   map[crdt.ID]*node
	have    map[crdt.ID]bool // applied op ids (idempotency)
	pending []Op             // ops whose target node isn't present yet
	clock   uint64           // highest id clock observed — the leader's mint source

	// view is the memoized resolved view (ext-14 §8.2 SHOULD): resolveView is a
	// PURE function of the applied op set, so it can be cached and reused for
	// repeated reads (O(1) instead of O(P log P) per read — the fix for the v0.1.4
	// churn OOM, ticket crdtjson-scale-fix-20260724). nil = dirty; it is set to
	// nil by integrate — the SINGLE node-state mutation funnel — on every applied
	// op, so a stale view can never be read. A fresh Doc (New/clone) starts dirty.
	view *resolvedView

	// dead is a CHEAP, incrementally-maintained count of resident-but-superseded
	// placements (overwritten bindings, newly-removed tags, list tombstones)
	// accumulated since this Doc was built. It is a monotone retention ESTIMATE —
	// never an arbitration input — so the store's compaction trigger can decide from
	// an O(1) counter instead of an O(P) SnapshotOps scan (Fix B) and bound resident
	// P by live content via a dead-fraction test (Fix C), ticket
	// crdtjson-scale-fix-20260724. Reset to ~0 when Compact rebuilds the Doc from its
	// live snapshot. Over-counting only compacts slightly earlier (safe, compaction
	// is value-transparent); it never affects the materialized result.
	dead int
}

// New creates an empty document ({} — a root MAP, ext-5 §4.4).
func New() *Doc {
	d := &Doc{nodes: map[crdt.ID]*node{}, have: map[crdt.ID]bool{}}
	d.nodes[Root] = &node{claims: map[string]stamp{KindMap: {}}}
	return d
}

func (d *Doc) observe(c uint64) {
	if c > d.clock {
		d.clock = c
	}
}

// Apply integrates one canonical op. Returns true if applied (or already
// applied), false if buffered pending its target node. Idempotent; must never
// error — a malformed op is a deterministic no-op, identical on every replica,
// because the FSM apply path cannot afford divergent verdicts.
func (d *Doc) Apply(op Op) bool {
	d.observe(op.ID.Clock)
	d.observe(op.NID.Clock)
	if d.have[op.ID] {
		return true
	}
	if !d.opReady(op) {
		d.buffer(op)
		return false
	}
	d.integrate(op)
	d.have[op.ID] = true
	d.drainPending()
	return true
}

// opReady reports whether op's dependencies are present so integrate can run.
// Every op needs its Target node. A mv ALSO needs its Src node (when non-zero)
// present, because its ghost purge (ext-14 §4.2) marks Src's removed set — so a
// mv delivered before its source parent MUST buffer, or a shuffled replica would
// skip the purge and diverge. (The moved Child itself is NOT a dependency — a
// placement is a pointer by id; a missing child renders nil deterministically.)
func (d *Doc) opReady(op Op) bool {
	if d.nodes[op.Target] == nil {
		return false
	}
	if op.T == OpMove && !op.Src.IsZero() && d.nodes[op.Src] == nil {
		return false
	}
	return true
}

func (d *Doc) buffer(op Op) {
	for _, p := range d.pending {
		if p.ID.Equal(op.ID) && p.T == op.T {
			return
		}
	}
	d.pending = append(d.pending, op)
}

// drainPending retries buffered ops until no more can be applied (fixpoint) —
// the same discipline as rga.go, which is what makes buffering order-free.
func (d *Doc) drainPending() {
	for {
		progress := false
		keep := d.pending[:0]
		pend := d.pending
		d.pending = nil
		for _, op := range pend {
			if !d.opReady(op) {
				keep = append(keep, op)
				continue
			}
			if !d.have[op.ID] {
				d.integrate(op)
				d.have[op.ID] = true
			}
			progress = true
		}
		d.pending = append(d.pending, keep...)
		if !progress {
			return
		}
	}
}

// PendingLen reports buffered ops (tests: a complete op set must drain to 0).
func (d *Doc) PendingLen() int { return len(d.pending) }

// DeadEstimate reports the cheap retention counter (superseded bindings + newly
// removed tags + list tombstones since this Doc was built) the store's compaction
// trigger reads instead of scanning SnapshotOps. It is a monotone estimate, NOT an
// arbitration input (ticket crdtjson-scale-fix-20260724).
func (d *Doc) DeadEstimate() int { return d.dead }

// integrate applies one op to its facet. Kind-mismatched or malformed ops are
// deterministic no-ops (see Apply).
func (d *Doc) integrate(op Op) {
	// Invalidate the memoized resolved view: integrate is the single funnel
	// through which node state (bindings/removed/elems/seq/register) mutates, so
	// clearing here is COMPLETE — no mutation path can leave a stale cached view
	// (Fix A, ticket crdtjson-scale-fix-20260724). Only reached for ops actually
	// being applied (Apply/drainPending guard on have[]), so no redundant clears.
	d.view = nil
	n := d.nodes[op.Target]
	st := stamp{now: op.Now, actor: op.Actor, id: op.ID}
	switch op.T {
	case OpSet:
		if op.NID.IsZero() {
			// In-place register write (ext-5 §4.3): LWW by (now, actor, op id).
			if op.Key != "" {
				return
			}
			n.writeReg(op.Value, st)
			return
		}
		if op.Key == "" {
			return
		}
		d.ensureNode(op.NID, op.Kind, op.Value, st)
		n.mapFacet()
		// ALWAYS record the binding, even if its tag was already observed-removed
		// (a del arrived first). Its liveness is `!removed[tag]`, read at
		// resolveView; a dead-but-PRESENT binding participates in location
		// arbitration. Recording it conditionally would make a binding's
		// EXISTENCE order-dependent (present-then-dead vs never-present), which —
		// without physical stripping — diverges a moved node's fallback (found by
		// FuzzConvergence). Duplicate delivery is still a no-op via `have`.
		if len(n.bindings[op.Key]) > 0 {
			d.dead++ // an overwrite: the prior newest binding is now superseded (retention estimate)
		}
		n.bindings[op.Key] = append(n.bindings[op.Key], binding{tag: op.ID, child: op.NID, st: st})
	case OpDel:
		n.mapFacet()
		// A del records observed tags into `removed` (tag-global: the add-side
		// n.removed check is tag-global, so the remove side is too — robust to
		// hostile input naming a tag bound under another key). Bindings are NOT
		// physically stripped (ext-14 note above), so a dead placement still
		// participates in location arbitration.
		for _, tg := range op.Tags {
			if !n.removed[tg] {
				d.dead++ // a newly-removed placement becomes reclaimable (retention estimate)
			}
			n.removed[tg] = true
		}
	case OpLIns:
		d.ensureNode(op.NID, op.Kind, op.Value, st)
		n.listFacet()
		n.elems[op.ID] = elemPlace{child: op.NID, st: st}
		origin := op.Origin
		if origin.Equal(Root) {
			origin = crdt.Zero // ext-5 writes the list head as the root nid shape
		}
		n.seq.Apply(crdt.Op{Type: crdt.OpInsert, ID: op.ID, OriginLeft: origin, Val: "x"})
	case OpLDel:
		n.listFacet()
		n.seq.Apply(crdt.Op{Type: crdt.OpDelete, ID: op.Elem})
		d.dead++ // a tombstoned list element is reclaimable at compaction (retention estimate)
	case OpMove:
		// acp-ext-14 §4.1: record a SECOND placement of an EXISTING node
		// (op.Child), plus the source-slot GHOST purge (op.Tags). NO cycle
		// logic, NO arbitration, NO retirement of the child's own placements
		// (those are out-arbitrated by resolveView) — records + ghost-purge
		// only, so Apply stays commutative/idempotent/apply-once.
		if op.Child.IsZero() {
			return
		}
		if op.Key != "" {
			// MAP destination: a binding of op.Child under op.Key, tagged by
			// op.ID. ALWAYS recorded (existence order-independent, like OpSet);
			// liveness is `!removed[op.ID]`, so a del that observed this tag —
			// whenever it arrives — marks the placement dead without removing it.
			n.mapFacet()
			if len(n.bindings[op.Key]) > 0 {
				d.dead++ // a move onto an occupied slot supersedes the prior binding (retention estimate)
			}
			n.bindings[op.Key] = append(n.bindings[op.Key], binding{tag: op.ID, child: op.Child, st: st})
		} else {
			// LIST destination: a fresh element (op.ID) after op.Origin holding
			// op.Child. Guarded by the RGA tombstone (an ldel that observed this
			// element but arrived first keeps it dead — seq.Apply is idempotent
			// and a delete-before-insert is buffered/applied by the RGA).
			n.listFacet()
			n.elems[op.ID] = elemPlace{child: op.Child, st: st}
			origin := op.Origin
			if origin.Equal(Root) {
				origin = crdt.Zero
			}
			n.seq.Apply(crdt.Op{Type: crdt.OpInsert, ID: op.ID, OriginLeft: origin, Val: "x"})
		}
		// GHOST purge: retire OTHER children's superseded bindings at the SOURCE
		// map slot (op.Tags), under the SOURCE parent node (op.Src, which may
		// differ from Target). Same tag-global observed-remove discipline as
		// OpDel. canonMove guarantees op.Tags never contains the moved child's
		// own tags — so the child's old placement stays live and is out-
		// arbitrated by resolveView, never resurrected (ext-14 §4.2 F1 fix).
		if len(op.Tags) > 0 && !op.Src.IsZero() {
			if sn := d.nodes[op.Src]; sn != nil {
				sn.mapFacet()
				for _, tg := range op.Tags {
					if !sn.removed[tg] {
						d.dead++ // ghost-purged source binding is now reclaimable (retention estimate)
					}
					sn.removed[tg] = true
				}
			}
		}
	}
}

// ensureNode creates (or merges into) the node a set/lins op mints. Claims
// merge by minimum stamp and register values by LWW, so duplicate-nid input
// converges regardless of order.
func (d *Doc) ensureNode(id crdt.ID, kind string, val json.RawMessage, st stamp) {
	n := d.nodes[id]
	if n == nil {
		n = &node{claims: map[string]stamp{}}
		d.nodes[id] = n
	}
	switch kind {
	case KindMap:
		n.claim(KindMap, st)
		n.mapFacet()
	case KindList:
		n.claim(KindList, st)
		n.listFacet()
	default:
		n.claim(kindReg, st)
		n.writeReg(val, st)
	}
}

// --- materialization (ext-5 §4.5) ---

// Materialize walks the graph from the root into plain Go values (map[string]any,
// []any, json.RawMessage scalars). It is a pure function of the applied op set:
// resolveView (ext-14 §4.3/§4.4) decides each node's rendered LOCATION once, and
// value walks the resulting tree top-down.
func (d *Doc) Materialize() any {
	v := d.resolveView()
	return d.value(Root, v, map[crdt.ID]bool{})
}

// CanonicalJSON is the one sanctioned marshal of a document: encoding/json
// sorts map[string]T keys, which makes the bytes identical on every replica
// (plan D4 — Go map RANGE is random; json marshal of maps is not).
func (d *Doc) CanonicalJSON() ([]byte, error) {
	return json.Marshal(d.Materialize())
}

// value renders node id under the resolved view. A slot renders its child only
// if that child's live LOCATION is this slot (v.occ); a moved-away or deleted
// child renders nothing — no null holes, no fallback. onPath stays as a
// belt-and-braces net (the view is already a forest, so it cannot fire for
// leader-minted graphs).
func (d *Doc) value(id crdt.ID, v resolvedView, onPath map[crdt.ID]bool) any {
	n := d.nodes[id]
	if n == nil || onPath[id] {
		return nil
	}
	onPath[id] = true
	defer delete(onPath, id)
	switch n.kind() {
	case KindMap:
		out := map[string]any{}
		for key := range n.bindings {
			if child, ok := v.slotChild(id, key); ok {
				out[key] = d.value(child, v, onPath)
			}
		}
		return out
	case KindList:
		out := []any{}
		for _, e := range n.seq.Snapshot().Els {
			if e.Deleted {
				continue
			}
			if child, ok := v.occ[occKey{parent: id, elem: e.ID}]; ok {
				out = append(out, d.value(child, v, onPath))
			}
		}
		return out
	default: // register
		if len(n.val) == 0 {
			return nil
		}
		return n.val
	}
}

// --- compaction snapshot ---

// SnapshotOps returns the minimal op set that reproduces the current
// MATERIALIZED document, preserving live node/element ids (ext-5 §5.8):
// superseded LWW bindings, removed tags, tombstones, and unreachable nodes are
// garbage-collected; lists are re-anchored by rga.SnapshotOps. Ops are emitted
// parent-before-child in deterministic (sorted-key / list) order, so replay on
// a fresh Doc never buffers and every replica compacts identically.
//
// SAFETY: dropping tombstones/tags is only sound behind the epoch barrier —
// crdtjsonstore bumps the epoch on Compact and rejects stale pushes, exactly
// like crdtstore (plan D5).
func (d *Doc) SnapshotOps() []Op {
	v := d.resolveView()
	var ops []Op
	d.snapChildren(Root, v, &ops, map[crdt.ID]bool{Root: true})
	return ops
}

// snapChildren emits set/lins ops recreating the RENDERED children of node id
// under the resolved view, nid preserved, NO mv ops (ext-14 §4.7). A moved node
// is emitted once, at its resolved LOCATION. Lists are RE-ANCHORED over the
// rendered elements: each rendered element follows the previous RENDERED one, so
// skipping a moved-away element never dangles a replayed origin.
func (d *Doc) snapChildren(id crdt.ID, v resolvedView, ops *[]Op, seen map[crdt.ID]bool) {
	n := d.nodes[id]
	switch n.kind() {
	case KindMap:
		keys := make([]string, 0, len(n.bindings))
		for k := range n.bindings {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			child, ok := v.slotChild(id, k)
			if !ok {
				continue
			}
			p := v.loc[child] // the winning placement; p.st.id is its binding tag
			d.snapValue(ops, v, seen, Op{T: OpSet, ID: p.st.id, Target: id, Key: k,
				Now: p.st.now, Actor: p.st.actor}, child)
		}
	case KindList:
		prev := crdt.Zero
		for _, io := range n.seq.SnapshotOps() {
			child, ok := v.occ[occKey{parent: id, elem: io.ID}]
			if !ok {
				continue // moved-away element: skip, do not advance the anchor
			}
			p := v.loc[child] // p.st.id == io.ID (the element id)
			d.snapValue(ops, v, seen, Op{T: OpLIns, ID: io.ID, Target: id, Origin: prev,
				Now: p.st.now, Actor: p.st.actor}, child)
			prev = io.ID
		}
	}
}

// snapValue completes a set/lins skeleton with the child node's kind/value and
// recurses into containers. Ids and stamps are PRESERVED so a resynced
// replica's references still line up.
func (d *Doc) snapValue(ops *[]Op, v resolvedView, seen map[crdt.ID]bool, op Op, child crdt.ID) {
	cn := d.nodes[child]
	if cn == nil || seen[child] {
		return
	}
	op.NID = child
	switch cn.kind() {
	case KindMap:
		op.Kind = KindMap
	case KindList:
		op.Kind = KindList
	default:
		op.Value = cn.val
	}
	*ops = append(*ops, op)
	if op.Kind != "" {
		seen[child] = true
		d.snapChildren(child, v, ops, seen)
		delete(seen, child)
	}
}

func (d *Doc) clone() *Doc {
	c := &Doc{nodes: make(map[crdt.ID]*node, len(d.nodes)),
		have: make(map[crdt.ID]bool, len(d.have)), clock: d.clock}
	for id := range d.have {
		c.have[id] = true
	}
	c.pending = append([]Op(nil), d.pending...)
	for id, n := range d.nodes {
		c.nodes[id] = n.clone()
	}
	return c
}

// --- leader-side canonicalization (ext-5 §5.4: the ONLY mint authority) ---

// Canonicalize turns client-submitted ops (path or NID form) into canonical,
// fully-stamped NID-form ops: it resolves paths against current committed
// state (plus earlier ops in the SAME request, via a shadow copy), expands
// container literals (ext-5 §5.3), stamps the LWW token (now, actor, op id)
// and mints every node/element id — overwriting anything the client supplied
// (§3.4/§9.4, anti-spoof). It does NOT mutate the document beyond advancing
// the mint clock; the returned ops are what the caller proposes to the FSM.
//
// MUST be called on the leader only, holding the store's lock (mint clocks
// are allocated here and made durable by the ops themselves once committed).
func (d *Doc) Canonicalize(in []Op, actor string, now int64, createIntermediate bool) ([]Op, error) {
	sh := d.clone()
	out := []Op{}
	mint := func() crdt.ID {
		d.clock++
		sh.clock = d.clock
		return crdt.ID{Clock: d.clock, Replica: actor}
	}
	for i := range in {
		ops, err := sh.canonOne(in[i], actor, now, createIntermediate, mint)
		if err != nil {
			return nil, fmt.Errorf("ops[%d]: %w", i, err)
		}
		if len(out)+len(ops) > MaxOps {
			return nil, fmt.Errorf("ops[%d]: request expands to more than %d ops", i, MaxOps)
		}
		for _, c := range ops {
			sh.Apply(c) // later ops in this request see this one's effect
		}
		out = append(out, ops...)
	}
	return out, nil
}

func (d *Doc) canonOne(op Op, actor string, now int64, createIntermediate bool, mint func() crdt.ID) ([]Op, error) {
	switch op.T {
	case OpSet:
		return d.canonSet(op, actor, now, createIntermediate, mint)
	case OpDel:
		return d.canonDel(op, actor, now, mint)
	case OpLIns:
		return d.canonLIns(op, actor, now, mint)
	case OpLDel:
		return d.canonLDel(op, actor, now, mint)
	case OpMove:
		return d.canonMove(op, actor, now, mint)
	default:
		return nil, fmt.Errorf("unknown op type %q", op.T)
	}
}

func (d *Doc) canonSet(op Op, actor string, now int64, createIntermediate bool, mint func() crdt.ID) ([]Op, error) {
	if len(op.Value) == 0 {
		return nil, errors.New("set requires a value (use JSON null explicitly)")
	}
	var ops []Op
	if len(op.Path) > 0 {
		last, prefix := op.Path[len(op.Path)-1], op.Path[:len(op.Path)-1]
		if key, ok := last.(string); ok {
			parent, err := d.walkPath(prefix, createIntermediate, &ops, actor, now, mint)
			if err != nil {
				return nil, err
			}
			if d.nodes[parent].kind() != KindMap {
				return nil, fmt.Errorf("%w: path %v: parent of %q is not an object", ErrUnresolved, op.Path, key)
			}
			if err := d.emitSet(&ops, 1, parent, key, op.Value, actor, now, mint); err != nil {
				return nil, err
			}
			return ops, nil
		}
		// Last segment is an index: overwrite that list element's REGISTER in
		// place (LWW). Containers can't be overwritten in place — ldel+lins.
		i, ok := pathIndex(last)
		if !ok {
			return nil, fmt.Errorf("bad path segment %v (want string key or integer index)", last)
		}
		parent, err := d.walkPath(prefix, false, &ops, actor, now, mint)
		if err != nil {
			return nil, err
		}
		_, child, ok := d.elemAt(parent, i)
		if !ok {
			return nil, fmt.Errorf("%w: path %v: no element at index %d", ErrUnresolved, op.Path, i)
		}
		if d.nodes[child].kind() != kindReg {
			return nil, fmt.Errorf("cannot overwrite a container list element in place — use ldel+lins")
		}
		if c, err := classify(op.Value); err != nil {
			return nil, err
		} else if c != 's' {
			return nil, errors.New("cannot overwrite a list element with a container in place — use ldel+lins")
		}
		return append(ops, Op{T: OpSet, ID: mint(), Target: child, Value: op.Value, Now: now, Actor: actor}), nil
	}
	// NID form.
	n := d.nodes[op.Target]
	if n == nil {
		return nil, fmt.Errorf("unknown target nid %+v", op.Target)
	}
	if op.Key == "" {
		if n.kind() != kindReg {
			return nil, errors.New("set without a key targets a register (in-place write); give key for objects")
		}
		if c, err := classify(op.Value); err != nil {
			return nil, err
		} else if c != 's' {
			return nil, errors.New("in-place register write requires a scalar value")
		}
		return []Op{{T: OpSet, ID: mint(), Target: op.Target, Value: op.Value, Now: now, Actor: actor}}, nil
	}
	if n.kind() != KindMap {
		return nil, fmt.Errorf("target nid %+v is not an object", op.Target)
	}
	if err := d.emitSet(&ops, 1, op.Target, op.Key, op.Value, actor, now, mint); err != nil {
		return nil, err
	}
	return ops, nil
}

func (d *Doc) canonDel(op Op, actor string, now int64, mint func() crdt.ID) ([]Op, error) {
	var ops []Op
	target, key := op.Target, op.Key
	if len(op.Path) > 0 {
		last, prefix := op.Path[len(op.Path)-1], op.Path[:len(op.Path)-1]
		if i, ok := pathIndex(last); ok { // del of an array position = ldel (§5.1)
			parent, err := d.walkPath(prefix, false, &ops, actor, now, mint)
			if err != nil {
				return nil, err
			}
			elem, _, ok := d.elemAt(parent, i)
			if !ok {
				return nil, fmt.Errorf("%w: path %v: no element at index %d", ErrUnresolved, op.Path, i)
			}
			return append(ops, Op{T: OpLDel, ID: mint(), Target: parent, Elem: elem, Now: now, Actor: actor}), nil
		}
		k, ok := last.(string)
		if !ok {
			return nil, fmt.Errorf("bad path segment %v (want string key or integer index)", last)
		}
		parent, err := d.walkPath(prefix, false, &ops, actor, now, mint)
		if err != nil {
			return nil, err
		}
		target, key = parent, k
	}
	n := d.nodes[target]
	if n == nil {
		return nil, fmt.Errorf("unknown target nid %+v", target)
	}
	if key == "" {
		return nil, errors.New("del requires a key")
	}
	if n.kind() != KindMap {
		return nil, fmt.Errorf("del target %+v is not an object", target)
	}
	// The leader stamps the OBSERVED tags (ext-5 §4.1): every binding live in
	// committed state (+ this request's earlier ops). A concurrent set that
	// arrives later carries a tag not listed here — and survives (add-wins).
	var tags []crdt.ID
	for _, b := range n.bindings[key] {
		tags = append(tags, b.tag)
	}
	return append(ops, Op{T: OpDel, ID: mint(), Target: target, Key: key, Tags: tags, Now: now, Actor: actor}), nil
}

func (d *Doc) canonLIns(op Op, actor string, now int64, mint func() crdt.ID) ([]Op, error) {
	if len(op.Value) == 0 {
		return nil, errors.New("lins requires a value")
	}
	var ops []Op
	target := op.Target
	if len(op.Path) > 0 {
		p, err := d.walkPath(op.Path, false, &ops, actor, now, mint)
		if err != nil {
			return nil, err
		}
		target = p
	}
	n := d.nodes[target]
	if n == nil {
		return nil, fmt.Errorf("unknown target nid %+v", target)
	}
	if n.kind() != KindList {
		return nil, fmt.Errorf("lins target %+v is not an array", target)
	}
	origin := op.Origin
	if origin.Equal(Root) {
		origin = crdt.Zero
	}
	if op.Idx != nil { // live-index convenience: resolve position -> origin
		origin = d.originForIndex(target, *op.Idx)
	} else if !origin.IsZero() {
		if _, ok := n.elems[origin]; !ok {
			return nil, fmt.Errorf("unknown index_origin %+v", origin)
		}
	}
	if _, err := d.emitLIns(&ops, 1, target, origin, op.Value, actor, now, mint); err != nil {
		return nil, err
	}
	return ops, nil
}

func (d *Doc) canonLDel(op Op, actor string, now int64, mint func() crdt.ID) ([]Op, error) {
	var ops []Op
	target, elem := op.Target, op.Elem
	if len(op.Path) > 0 {
		last, prefix := op.Path[len(op.Path)-1], op.Path[:len(op.Path)-1]
		i, ok := pathIndex(last)
		if !ok {
			return nil, errors.New("ldel path must end in an integer index")
		}
		parent, err := d.walkPath(prefix, false, &ops, actor, now, mint)
		if err != nil {
			return nil, err
		}
		e, _, ok := d.elemAt(parent, i)
		if !ok {
			return nil, fmt.Errorf("%w: path %v: no element at index %d", ErrUnresolved, op.Path, i)
		}
		target, elem = parent, e
	}
	n := d.nodes[target]
	if n == nil {
		return nil, fmt.Errorf("unknown target nid %+v", target)
	}
	if n.kind() != KindList {
		return nil, fmt.Errorf("ldel target %+v is not an array", target)
	}
	if _, ok := n.elems[elem]; !ok {
		return nil, fmt.Errorf("unknown elem %+v", elem)
	}
	return append(ops, Op{T: OpLDel, ID: mint(), Target: target, Elem: elem, Now: now, Actor: actor}), nil
}

// walkPath resolves every given segment (callers pass the prefix; the last
// segment's semantics are op-specific) to the node it addresses. Missing
// intermediate MAP keys are auto-created only when create is set (ext-5 §5.6)
// — the creations are applied to the shadow and appended to ops.
func (d *Doc) walkPath(segs []any, create bool, ops *[]Op, actor string, now int64, mint func() crdt.ID) (crdt.ID, error) {
	if len(segs) > MaxDepth {
		return crdt.ID{}, fmt.Errorf("path deeper than %d segments", MaxDepth)
	}
	// Resolve against the RENDERED view (ext-14 §4.6), so a path lands on what a
	// reader of the doc sees — never a moved-away node. For move-free documents
	// slotChild == winnerBinding and renderedElemAt == elemAt, so this is a
	// strict generalization with no change to existing behavior.
	v := d.resolveView()
	cur := Root
	for _, seg := range segs {
		n := d.nodes[cur]
		if key, ok := seg.(string); ok {
			if n.kind() != KindMap {
				return crdt.ID{}, fmt.Errorf("%w: segment %q under a non-object node", ErrUnresolved, key)
			}
			if child, ok := v.slotChild(cur, key); ok {
				cur = child
				continue
			}
			if !create {
				return crdt.ID{}, fmt.Errorf("%w: missing key %q (set create_intermediate to auto-create)", ErrUnresolved, key)
			}
			op := Op{T: OpSet, ID: mint(), Target: cur, Key: key, NID: mint(), Kind: KindMap, Now: now, Actor: actor}
			d.Apply(op)
			*ops = append(*ops, op)
			v = d.resolveView() // a fresh intermediate changed the view
			cur = op.NID
			continue
		}
		i, ok := pathIndex(seg)
		if !ok {
			return crdt.ID{}, fmt.Errorf("bad path segment %v (want string key or integer index)", seg)
		}
		if n.kind() != KindList {
			return crdt.ID{}, fmt.Errorf("%w: index %d under a non-array node", ErrUnresolved, i)
		}
		_, child, ok := d.renderedElemAt(v, cur, i)
		if !ok {
			return crdt.ID{}, fmt.Errorf("%w: no element at index %d", ErrUnresolved, i)
		}
		cur = child
	}
	return cur, nil
}

// emitSet appends the canonical op(s) binding key in parent to a node holding
// raw — one op for a scalar, a leader-expanded subtree for a container
// literal (ext-5 §5.3), bounded by MaxDepth/MaxOps (§9.2).
func (d *Doc) emitSet(ops *[]Op, depth int, parent crdt.ID, key string, raw json.RawMessage, actor string, now int64, mint func() crdt.ID) error {
	kind, scalar, err := literalKind(raw, depth)
	if err != nil {
		return err
	}
	op := Op{T: OpSet, ID: mint(), Target: parent, Key: key, NID: mint(), Kind: kind, Value: scalar, Now: now, Actor: actor}
	*ops = append(*ops, op)
	return d.emitChildren(ops, depth, op.NID, kind, raw, actor, now, mint)
}

// emitLIns appends the canonical op(s) inserting one element (scalar or
// container literal) after origin; returns the new ELEMENT id for chaining.
func (d *Doc) emitLIns(ops *[]Op, depth int, list, origin crdt.ID, raw json.RawMessage, actor string, now int64, mint func() crdt.ID) (crdt.ID, error) {
	kind, scalar, err := literalKind(raw, depth)
	if err != nil {
		return crdt.ID{}, err
	}
	op := Op{T: OpLIns, ID: mint(), Target: list, Origin: origin, NID: mint(), Kind: kind, Value: scalar, Now: now, Actor: actor}
	*ops = append(*ops, op)
	return op.ID, d.emitChildren(ops, depth, op.NID, kind, raw, actor, now, mint)
}

func (d *Doc) emitChildren(ops *[]Op, depth int, nid crdt.ID, kind string, raw json.RawMessage, actor string, now int64, mint func() crdt.ID) error {
	if len(*ops) > MaxOps {
		return fmt.Errorf("container literal expands to more than %d ops", MaxOps)
	}
	switch kind {
	case KindMap:
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			return err
		}
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys) // deterministic expansion order (leader-side, but tidy)
		for _, k := range keys {
			if err := d.emitSet(ops, depth+1, nid, k, obj[k], actor, now, mint); err != nil {
				return err
			}
		}
	case KindList:
		var arr []json.RawMessage
		if err := json.Unmarshal(raw, &arr); err != nil {
			return err
		}
		prev := crdt.Zero
		for _, el := range arr {
			eid, err := d.emitLIns(ops, depth+1, nid, prev, el, actor, now, mint)
			if err != nil {
				return err
			}
			prev = eid
		}
	}
	return nil
}

// literalKind classifies a value: container kind ("map"/"list") or scalar
// ("" + the raw bytes, preserved verbatim so numbers survive byte-exactly).
func literalKind(raw json.RawMessage, depth int) (string, json.RawMessage, error) {
	if depth > MaxDepth {
		return "", nil, fmt.Errorf("container literal deeper than %d", MaxDepth)
	}
	c, err := classify(raw)
	if err != nil {
		return "", nil, err
	}
	switch c {
	case 'o':
		return KindMap, nil, nil
	case 'a':
		return KindList, nil, nil
	default:
		return "", raw, nil
	}
}

// classify returns 'o' (object), 'a' (array) or 's' (scalar) for a JSON value.
func classify(raw json.RawMessage) (byte, error) {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '{':
			return 'o', nil
		case '[':
			return 'a', nil
		default:
			if !json.Valid(raw) {
				return 0, fmt.Errorf("invalid JSON value %.40q", string(raw))
			}
			return 's', nil
		}
	}
	return 0, errors.New("empty JSON value")
}

// elemAt returns the RENDERED i-th element (element id, child nid) of a list
// node — view-aware (ext-14 §4.6), so client indices match the materialized
// array even after moves. For move-free lists this is the plain i-th live
// element.
func (d *Doc) elemAt(list crdt.ID, i int) (crdt.ID, crdt.ID, bool) {
	return d.renderedElemAt(d.resolveView(), list, i)
}

// originForIndex maps a live-index insert position to an origin element id over
// the RENDERED elements (ext-14 §4.6): idx <= 0 inserts at the head, idx >= len
// appends after the last rendered element.
func (d *Doc) originForIndex(list crdt.ID, idx int) crdt.ID {
	return d.renderedOriginForIndex(d.resolveView(), list, idx)
}

// pathIndex normalizes a numeric path segment (JSON decodes numbers in []any
// as float64; tests may use ints directly).
func pathIndex(seg any) (int, bool) {
	switch v := seg.(type) {
	case int:
		return v, true
	case float64:
		if v == float64(int(v)) {
			return int(v), true
		}
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return int(i), true
		}
	}
	return 0, false
}

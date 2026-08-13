package crdtjson

// acp-ext-14: identity-preserving MOVE for the structured (JSON) CRDT.
//
// A "mv" op relocates an EXISTING node (its nid preserved) by creating a SECOND
// placement of that node under a destination slot. Which placement a node
// actually renders under — its LOCATION — is resolved at materialization by a
// pure, stamp-ordered validity pass (ext-14 §4.4, the paper's Algorithm 1 over
// our leader-total stamp order). This file holds that resolution + the leader-
// side canonicalization of a mv. Apply/integrate (in crdtjson.go) only RECORD a
// placement and ghost-purge the source slot — no cycle logic lives there, so
// Apply stays commutative/idempotent/apply-once.
//
// The design rule (Revision B of the RFC): a mv NEVER retires the moved node's
// own prior placement. The old placement stays live and is simply out-
// arbitrated by resolveView (a greater-stamped valid placement wins). A node
// renders only if its winning location is LIVE; there is no fallback past a
// deleted location. This is what makes stays-put (a cycle loser keeps its prior
// valid location) and no-resurrection (a delete of the winner deletes the node)
// both hold — the two requirements the first draft could not satisfy together.

import (
	"errors"
	"fmt"
	"sort"

	"github.com/ab0t-com/acp/sdk/go/internal/crdt"
)

// placement is one (parent, slot) -> child reference with its stamp and current
// liveness. A MAP placement is a binding (slot = key); a LIST placement is an
// element (slot = elem id). Every placement's stamp id equals its binding tag /
// element id (see integrate), so the snapshot can recover the id from the stamp.
type placement struct {
	parent crdt.ID
	key    string  // map slot key ("" for a list placement)
	elem   crdt.ID // list element id (zero for a map placement)
	isList bool
	child  crdt.ID
	st     stamp
	live   bool
}

// occKey identifies a render slot (a map key or a list element under a parent).
type occKey struct {
	parent crdt.ID
	key    string
	elem   crdt.ID
}

// resolvedView is the materialization-equivalent projection: each child's
// winning location, and the child that renders at each slot.
type resolvedView struct {
	loc map[crdt.ID]placement // child -> its greatest-stamped VALID placement (may be dead)
	occ map[occKey]crdt.ID    // slot -> the child that RENDERS there (live winners only)
}

// slot returns the occKey a placement addresses.
func (p placement) slot() occKey {
	return occKey{parent: p.parent, key: p.key, elem: p.elem}
}

// resolveView computes location(c) for every child and the per-slot render
// occupant. It is a PURE function of the applied op set: the input is the SET
// of placements, the sort key is a strict total order (stamps embed unique op
// ids), so the output is byte-identical on every replica regardless of delivery
// order (ext-14 §4.4 / §7). Cost O(P log P) + one bounded ancestor walk per
// container placement; bounded by the ext-5 §9.2 doc caps.
func (d *Doc) resolveView() resolvedView {
	// Fix A (ext-14 §8.2): return the memoized view if clean. integrate() clears
	// d.view on every applied op, so a non-nil cache is always current. Callers
	// only READ the returned loc/occ maps (never mutate them), so sharing the
	// cached maps is safe. Collapses repeated reads from O(P log P) to O(1).
	if d.view != nil {
		return *d.view
	}
	// 1. Collect ALL placements (live AND dead — a deleted winner must still
	//    have a well-defined validity so "no fallback past a dead location"
	//    holds, ext-14 §4.2 rule 2).
	var all []placement
	for pid, n := range d.nodes {
		for key, bs := range n.bindings {
			for _, b := range bs {
				all = append(all, placement{
					parent: pid, key: key, child: b.child, st: b.st,
					live: !n.removed[b.tag],
				})
			}
		}
		if n.seq != nil {
			deleted := map[crdt.ID]bool{}
			for _, e := range n.seq.Snapshot().Els {
				if e.Deleted {
					deleted[e.ID] = true
				}
			}
			for eid, ep := range n.elems {
				all = append(all, placement{
					parent: pid, elem: eid, isList: true, child: ep.child, st: ep.st,
					live: !deleted[eid],
				})
			}
		}
	}
	// 2. Sort ascending by the total stamp order (deterministic; no ties).
	sort.Slice(all, func(i, j int) bool { return all[i].st.less(all[j].st) })
	// 3. Validity pass: build the parent forest. A CONTAINER placement that
	//    would nest a node in its own subtree is SKIPPED (stays put); a later
	//    valid placement overrides an earlier one (this IS the per-child LWW).
	//    Registers skip the ancestor check (a scalar has no children).
	loc := make(map[crdt.ID]placement, len(all))
	for _, p := range all {
		if d.isContainerNode(p.child) && ancestorOrSelf(loc, p.parent, p.child) {
			continue
		}
		loc[p.child] = p
	}
	// 4. Per-slot render occupant (filter-then-max, ext-14 §4.3): among children
	//    whose LIVE location is a slot, the greatest-stamped renders there. A
	//    dead location renders nothing (no fallback); a stale higher-stamped
	//    binding of a moved-away child does NOT suppress the legit occupant,
	//    because only children LOCATED here (loc points here) compete.
	occ := make(map[occKey]crdt.ID, len(loc))
	for child, p := range loc {
		if !p.live {
			continue
		}
		k := p.slot()
		if cur, ok := occ[k]; !ok || loc[cur].st.less(p.st) {
			occ[k] = child
		}
	}
	out := resolvedView{loc: loc, occ: occ}
	d.view = &out // memoize (Fix A); invalidated by integrate on the next applied op
	return out
}

// ancestorOrSelf reports whether anc is node or an ancestor of node in the
// partial parent map built so far. Terminates because loc is a forest at all
// times (cycle-creating placements are never inserted): the walk strictly
// ascends and stops at a node absent from loc (the sentinel for Root or an
// unplaced node — distinct from the zero id; ext-14 §4.4 F8).
func ancestorOrSelf(loc map[crdt.ID]placement, node, anc crdt.ID) bool {
	for {
		if node.Equal(anc) {
			return true
		}
		p, ok := loc[node]
		if !ok {
			return false
		}
		node = p.parent
	}
}

// isContainerNode reports whether id names a present MAP or LIST node. An
// unknown child (not yet created under shuffled delivery) is treated as a leaf
// with no descendants, so it skips the ancestor check (ext-14 §4.4 F7).
func (d *Doc) isContainerNode(id crdt.ID) bool {
	n := d.nodes[id]
	if n == nil {
		return false
	}
	switch n.kind() {
	case KindMap, KindList:
		return true
	}
	return false
}

// slotChild returns the child that renders at map slot (parent,key) under the
// view, if any. Used by view-aware path resolution (ext-14 §4.6). For move-free
// documents this equals winnerBinding (each child has exactly one placement).
func (v resolvedView) slotChild(parent crdt.ID, key string) (crdt.ID, bool) {
	c, ok := v.occ[occKey{parent: parent, key: key}]
	return c, ok
}

// renderedElemAt returns the (element id, child) of the i-th RENDERED element of
// a list node under the view — skipping moved-away elements — so client indices
// match the materialized array (ext-14 §4.6 / §4.3). Registers/list order come
// from the RGA; the view decides which elements render.
func (d *Doc) renderedElemAt(v resolvedView, list crdt.ID, i int) (crdt.ID, crdt.ID, bool) {
	n := d.nodes[list]
	if n == nil || n.seq == nil || i < 0 {
		return crdt.ID{}, crdt.ID{}, false
	}
	idx := 0
	for _, e := range n.seq.Snapshot().Els {
		if e.Deleted {
			continue
		}
		child, ok := v.occ[occKey{parent: list, elem: e.ID}]
		if !ok {
			continue // moved-away element: not part of the visible array
		}
		if idx == i {
			return e.ID, child, true
		}
		idx++
	}
	return crdt.ID{}, crdt.ID{}, false
}

// renderedOriginForIndex maps a live-index insert position to an origin element
// id over the RENDERED elements of a list (ext-14 §4.6). idx<=0 -> head; idx>=len
// -> after the last rendered element.
func (d *Doc) renderedOriginForIndex(v resolvedView, list crdt.ID, idx int) crdt.ID {
	n := d.nodes[list]
	if n == nil || n.seq == nil || idx <= 0 {
		return crdt.Zero
	}
	origin, pos := crdt.Zero, 0
	for _, e := range n.seq.Snapshot().Els {
		if e.Deleted {
			continue
		}
		if _, ok := v.occ[occKey{parent: list, elem: e.ID}]; !ok {
			continue
		}
		pos++
		origin = e.ID
		if pos == idx {
			break
		}
	}
	return origin
}

// depthOf returns the depth of a node under the view (root = 0), walking up its
// location chain. An unplaced node returns -1.
func (v resolvedView) depthOf(node crdt.ID) int {
	if node.Equal(Root) {
		return 0
	}
	depth := 0
	for {
		p, ok := v.loc[node]
		if !ok {
			return -1 // unplaced / unreachable
		}
		depth++
		if p.parent.Equal(Root) {
			return depth
		}
		node = p.parent
	}
}

// heightOf returns the number of node levels strictly below node in the RENDERED
// tree (a leaf = 0), bounded so a pathological structure cannot loop.
func (d *Doc) heightOf(v resolvedView, node crdt.ID, budget int) int {
	if budget <= 0 {
		return 0
	}
	n := d.nodes[node]
	if n == nil {
		return 0
	}
	best := 0
	consider := func(child crdt.ID) {
		if h := d.heightOf(v, child, budget-1) + 1; h > best {
			best = h
		}
	}
	switch n.kind() {
	case KindMap:
		for key := range n.bindings {
			if c, ok := v.slotChild(node, key); ok {
				consider(c)
			}
		}
	case KindList:
		for eid := range n.elems {
			if c, ok := v.occ[occKey{parent: node, elem: eid}]; ok {
				consider(c)
			}
		}
	}
	return best
}

// --- leader-side canonicalization of a mv (ext-14 §4.1, §4.2, §4.5) ---

// canonMove resolves a client mv (path- or NID-form) into ONE canonical mv op:
// it identifies the EXISTING child + its source slot, the destination slot
// (form chosen by the TARGET node's kind), the source-slot GHOST-PURGE tags
// (other children's superseded bindings, never the child's own), rejects a move
// into the child's own subtree (courtesy 409) or past MaxDepth (400), and stamps
// (now, actor, op id). Path forms resolve against the RENDERED view; a missing
// source or dest is 409 (create_intermediate does not apply to a move).
func (d *Doc) canonMove(op Op, actor string, now int64, mint func() crdt.ID) ([]Op, error) {
	var ops []Op
	v := d.resolveView()

	// --- resolve CHILD + its source slot ---
	var child, srcParent crdt.ID
	var srcKey string
	if len(op.FromPath) > 0 {
		last, prefix := op.FromPath[len(op.FromPath)-1], op.FromPath[:len(op.FromPath)-1]
		parent, err := d.walkPath(prefix, false, &ops, actor, now, mint)
		if err != nil {
			return nil, err
		}
		if key, ok := last.(string); ok {
			c, ok := v.slotChild(parent, key)
			if !ok {
				return nil, fmt.Errorf("%w: from %v: no key %q", ErrUnresolved, op.FromPath, key)
			}
			child, srcParent, srcKey = c, parent, key // map source: ghost purge here
		} else if i, ok := pathIndex(last); ok {
			_, c, ok := d.renderedElemAt(v, parent, i)
			if !ok {
				return nil, fmt.Errorf("%w: from %v: no element at index %d", ErrUnresolved, op.FromPath, i)
			}
			child = c // list source: elements are not LWW-superseded, no ghost purge
		} else {
			return nil, fmt.Errorf("bad from segment %v (want string key or integer index)", last)
		}
	} else {
		// NID form: Child given; the source slot is location(child) (ext-14 §4.5).
		child = op.Child
		if p, ok := v.loc[child]; ok && !p.isList {
			srcParent, srcKey = p.parent, p.key
		}
	}
	if child.IsZero() || child.Equal(Root) {
		return nil, errors.New("mv: child must be an existing non-root node")
	}
	if d.nodes[child] == nil {
		return nil, fmt.Errorf("mv: unknown child nid %+v", child)
	}

	// --- resolve DESTINATION slot (form selected by target's KIND) ---
	var destParent, origin crdt.ID
	var destKey string
	destIsList := false
	if len(op.Path) > 0 {
		last, prefix := op.Path[len(op.Path)-1], op.Path[:len(op.Path)-1]
		parent, err := d.walkPath(prefix, false, &ops, actor, now, mint)
		if err != nil {
			return nil, err
		}
		destParent = parent
		pn := d.nodes[parent]
		if pn == nil {
			return nil, fmt.Errorf("%w: dest %v unresolved", ErrUnresolved, op.Path)
		}
		if key, ok := last.(string); ok {
			if pn.kind() != KindMap {
				return nil, fmt.Errorf("mv: dest key %q under a non-object node", key)
			}
			destKey = key
		} else if i, ok := pathIndex(last); ok {
			if pn.kind() != KindList {
				return nil, fmt.Errorf("mv: dest index %d under a non-array node", i)
			}
			destIsList = true
			origin = d.renderedOriginForIndex(v, parent, i)
		} else {
			return nil, fmt.Errorf("bad dest segment %v (want string key or integer index)", last)
		}
	} else {
		destParent = op.Target
		pn := d.nodes[destParent]
		if pn == nil {
			return nil, fmt.Errorf("mv: unknown target nid %+v", destParent)
		}
		switch pn.kind() {
		case KindMap:
			if op.Key == "" {
				return nil, errors.New("mv: map destination requires a key")
			}
			destKey = op.Key
		case KindList:
			destIsList = true
			origin = op.Origin
			if origin.Equal(Root) {
				origin = crdt.Zero
			}
			if !origin.IsZero() {
				if _, ok := pn.elems[origin]; !ok {
					return nil, fmt.Errorf("mv: unknown index_origin %+v", origin)
				}
			}
		default:
			return nil, fmt.Errorf("mv: target %+v is not a container", destParent)
		}
	}

	// --- reject a move into the child's own subtree (courtesy 409; the
	//     authoritative guard is resolveView) + a move past MaxDepth ---
	if ancestorOrSelf(v.loc, destParent, child) {
		return nil, fmt.Errorf("%w: mv: destination is inside the moved subtree", ErrUnresolved)
	}
	dd := v.depthOf(destParent)
	if dd < 0 {
		dd = 0
	}
	if dd+1+d.heightOf(v, child, MaxDepth) > MaxDepth {
		return nil, fmt.Errorf("mv: resulting materialized depth exceeds %d", MaxDepth)
	}

	// --- source-slot GHOST purge: OTHER children's live binding tags at the
	//     source map slot; NEVER the moved child's own placement (ext-14 §4.2). ---
	var tags []crdt.ID
	if !srcParent.IsZero() {
		if sn := d.nodes[srcParent]; sn != nil {
			for _, b := range sn.bindings[srcKey] {
				if !sn.removed[b.tag] && !b.child.Equal(child) {
					tags = append(tags, b.tag)
				}
			}
		}
	}

	// --- emit the single canonical mv ---
	mv := Op{T: OpMove, ID: mint(), Child: child, Target: destParent,
		Tags: tags, Src: srcParent, Now: now, Actor: actor}
	if destIsList {
		mv.Origin = origin
	} else {
		mv.Key = destKey
	}
	return append(ops, mv), nil
}

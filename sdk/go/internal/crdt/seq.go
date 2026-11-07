package crdt

import (
	"crypto/rand"
	"encoding/binary"
	"sync/atomic"
)

// seq is an RGA's element list in document order: an order-statistic treap (R-C21).
//
// A plain slice found an element's place by scanning from the start, once per op and once
// per element the YATA rule steps over, so replaying a log of n inserts cost O(n²): a
// 70k-character document took minutes. Here every element is a node reached from its id
// in O(1) (RGA.nodes), its place is counted by walking up to the root in O(log n), and an
// insert at a place goes down from the root in O(log n). The order of the elements, and so
// every result, is exactly what the slice gave: only how a place is found changed.
//
// Priorities come from a generator seeded at random once per process (seqSeed), so no op
// stream can choose a degenerate shape. The shape never changes a result, only the work.
// No recursion: a deep tree can't overflow a stack.

type node struct {
	e       elem
	l, r, p *node
	pri     uint64
	n       int // nodes in this subtree
}

type seq struct {
	root *node
	rng  uint64
	work *atomic.Uint64 // nodes visited, for the guard (TestReplayWorkIsNLogN); see addWork
}

func size(x *node) int {
	if x == nil {
		return 0
	}
	return x.n
}

func (s *seq) len() int { return size(s.root) }

// seqSeed starts every list's priorities, drawn once per process (R-C23 P2-10): a fixed seed
// let an op stream place inserts so that the tree became a chain, and replay quadratic
// again. The shape never changes a result, only the work.
var seqSeed = func() uint64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0x9E3779B97F4A7C15
	}
	return binary.LittleEndian.Uint64(b[:]) | 1
}()

// nextPri is xorshift64*: cheap, from a per-process random start.
func (s *seq) nextPri() uint64 {
	if s.rng == 0 {
		s.rng = seqSeed
	}
	s.rng ^= s.rng >> 12
	s.rng ^= s.rng << 25
	s.rng ^= s.rng >> 27
	return s.rng * 0x2545F4914F6CDD1D
}

// addWork adds one call's steps to the counter atomically: the read methods (Text, Snapshot,
// SnapshotOps, DeletesFor) walk the list too, and two readers under a shared lock must not race
// on it (R-S22). One add per call, not per step.
//
// The counter is a separate allocation held by pointer (R-S23 P0-4): an atomic 64-bit add
// needs 8-byte alignment, which a uint64 field inside the RGA does not have on 32-bit
// platforms (386, arm), where it panicked; an atomic.Uint64 field would forbid copying an
// RGA, as clients do. A copy shares the counter, which only counts. A list made without
// New or Load (a zero seq) counts nothing.
func (s *seq) addWork(n uint64) {
	if s.work != nil {
		s.work.Add(n)
	}
}

// workDone is the steps counted so far (0 for a list that counts nothing).
func (s *seq) workDone() uint64 {
	if s.work == nil {
		return 0
	}
	return s.work.Load()
}

// pos is x's place in document order.
func (s *seq) pos(x *node) int {
	i := size(x.l)
	var steps uint64
	for y := x; y.p != nil; y = y.p {
		steps++
		if y == y.p.r {
			i += size(y.p.l) + 1
		}
	}
	s.addWork(steps)
	return i
}

// first is the first node, nil when empty.
func (s *seq) first() *node {
	x := s.root
	var steps uint64
	for x != nil && x.l != nil {
		steps++
		x = x.l
	}
	s.addWork(steps)
	return x
}

// next is the node after x in document order, nil at the end.
func (s *seq) next(x *node) *node {
	var steps uint64
	defer func() { s.addWork(steps) }()
	if x.r != nil {
		x = x.r
		for x.l != nil {
			steps++
			x = x.l
		}
		return x
	}
	for x.p != nil && x == x.p.r {
		steps++
		x = x.p
	}
	return x.p
}

// insertAt puts e at place i (0 <= i <= len) and returns its node.
func (s *seq) insertAt(i int, e elem) *node {
	x := &node{e: e, pri: s.nextPri(), n: 1}
	if s.root == nil {
		s.root = x
		return x
	}
	cur := s.root
	var steps uint64
	defer func() { s.addWork(steps) }()
	for {
		steps++
		cur.n++
		if i <= size(cur.l) {
			if cur.l == nil {
				cur.l, x.p = x, cur
				break
			}
			cur = cur.l
		} else {
			i -= size(cur.l) + 1
			if cur.r == nil {
				cur.r, x.p = x, cur
				break
			}
			cur = cur.r
		}
	}
	for x.p != nil && x.pri > x.p.pri {
		steps++
		s.rotateUp(x)
	}
	return x
}

// build fills an empty list with es in order, in O(n) (R-S20 P2-6: Load is on every JSON
// push, through the leader's clone, and n inserts with their rotations cost O(n log n) and
// one allocation each). It makes exactly the tree n inserts at the end would: a treap is
// fixed by its order and its priorities, and each node draws its priority in the same
// order, so the generator ends in the same state too. The tree is the Cartesian tree of the
// priorities, made with a stack of the right spine; a later node with an equal priority
// stays below, as an insert's rotation (strictly greater) leaves it. The work counter is
// left alone: a load is not an insert's walk. No recursion.
func (s *seq) build(n int, elemAt func(i int) elem) []node {
	nodes := make([]node, n) // one allocation; the caller reaches element i at &nodes[i]
	spine := make([]*node, 0, 64)
	for i := range nodes {
		x := &nodes[i]
		x.e, x.pri = elemAt(i), s.nextPri()
		var last *node
		for len(spine) > 0 && spine[len(spine)-1].pri < x.pri {
			last = spine[len(spine)-1]
			spine = spine[:len(spine)-1]
		}
		if last != nil {
			x.l, last.p = last, x
		}
		if len(spine) > 0 {
			top := spine[len(spine)-1]
			top.r, x.p = x, top
		}
		spine = append(spine, x)
	}
	if len(spine) > 0 {
		s.root = spine[0]
	}
	// Subtree sizes, children before parents: an iterative post-order.
	type frame struct {
		x    *node
		done bool
	}
	st := []frame{}
	if s.root != nil {
		st = append(st, frame{x: s.root})
	}
	for len(st) > 0 {
		f := st[len(st)-1]
		st = st[:len(st)-1]
		if f.done {
			f.x.n = size(f.x.l) + size(f.x.r) + 1
			continue
		}
		st = append(st, frame{x: f.x, done: true})
		if f.x.l != nil {
			st = append(st, frame{x: f.x.l})
		}
		if f.x.r != nil {
			st = append(st, frame{x: f.x.r})
		}
	}
	return nodes
}

// rotateUp turns x's parent into x's child, keeping the order.
func (s *seq) rotateUp(x *node) {
	p, g := x.p, x.p.p
	if x == p.l {
		p.l = x.r
		if x.r != nil {
			x.r.p = p
		}
		x.r = p
	} else {
		p.r = x.l
		if x.l != nil {
			x.l.p = p
		}
		x.l = p
	}
	p.p, x.p = x, g
	switch {
	case g == nil:
		s.root = x
	case g.l == p:
		g.l = x
	default:
		g.r = x
	}
	p.n = size(p.l) + size(p.r) + 1
	x.n = size(x.l) + size(x.r) + 1
}

// each calls fn for every element in document order. fn may change the element in place.
func (s *seq) each(fn func(e *elem)) {
	for x := s.first(); x != nil; x = s.next(x) {
		fn(&x.e)
	}
}

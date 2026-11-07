package crdt

import "sort"

// The text diff behind GenerateOps. A script's size decides what everyone else sees, so
// the rule is that it stays a real edit at any document size (R-C0): the script only
// ever deletes what changed. Replacing a whole text is a last resort for a region where
// nothing was found in common, and GenerateOps sends even that insert-first, so readers
// never see the text gone and a stopped push leaves extra text instead of a gutted doc.
//
// Memory is O(n+m) and work is capped, for any input:
//  1. The common prefix and suffix are kept outright. An ordinary edit ends here, or
//     very nearly.
//  2. What is left, if large, is cut at lines that appear exactly once on both sides
//     and in the same order (patience anchors), so a section nobody touched stays kept
//     however much was rewritten around it.
//  3. Each gap gets a linear-space Myers diff: exact up to 2*maxDiffEdits changes, and
//     past that it is cut at the furthest point reached and carried on from there.
//  4. Once maxDiffWork is spent, what remains is replaced.
//
// The previous version kept an LCS matrix of n*m ints below a per-side cap: 60,000 by
// 60,000 asked for about 29 GB, and above the cap every character was replaced.

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

const (
	// maxDiffEdits bounds one split: forward and backward searches each go this deep,
	// so a split is exact up to twice this many changes.
	maxDiffEdits = 1 << 12
	// maxDiffWork bounds one diff, in search steps: a couple of seconds on a slow
	// machine, spent only on a text rewritten at large scale.
	maxDiffWork = 1 << 28
	// diffAnchorRunes is the size of the changed middle above which it is cut at
	// anchors first. Below it the search alone is exact.
	diffAnchorRunes = 2 * maxDiffEdits
)

type differ struct {
	steps  []step
	work   int
	v1, v2 []int
}

// diff returns an edit script transforming old into new.
func diff(old, new []rune) []step {
	steps, _ := diffWork(old, new)
	return steps
}

// diffWork is diff, and the search steps it spent (at most maxDiffWork): the cost a test can
// pin by count, where a clock on a shared box can't tell a slower diff from a busy machine.
func diffWork(old, new []rune) ([]step, int) {
	d := &differ{work: maxDiffWork, steps: make([]step, 0, max(len(old), len(new))+16)}
	p := commonPrefix(old, new)
	d.keep(p)
	a, b := old[p:], new[p:]
	s := commonSuffix(a, b)
	a, b = a[:len(a)-s], b[:len(b)-s]
	if len(a)+len(b) > diffAnchorRunes {
		d.anchored(a, b)
	} else {
		d.compare(a, b)
	}
	d.keep(s)
	return d.steps, maxDiffWork - d.work
}

func commonPrefix(a, b []rune) int {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

func commonSuffix(a, b []rune) int {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[len(a)-1-i] != b[len(b)-1-i] {
			return i
		}
	}
	return n
}

func (d *differ) keep(n int) {
	for range n {
		d.steps = append(d.steps, step{kind: keep})
	}
}

func (d *differ) replace(a, b []rune) {
	for range a {
		d.steps = append(d.steps, step{kind: del})
	}
	for _, r := range b {
		d.steps = append(d.steps, step{kind: ins, r: r})
	}
}

// compare appends the script for a -> b.
func (d *differ) compare(a, b []rune) {
	p := commonPrefix(a, b)
	d.keep(p)
	a, b = a[p:], b[p:]
	s := commonSuffix(a, b)
	a, b = a[:len(a)-s], b[:len(b)-s]
	if x, y, ok := d.split(a, b); ok && x+y > 0 && x+y < len(a)+len(b) { // always a smaller pair
		d.compare(a[:x], b[:y])
		d.compare(a[x:], b[y:])
	} else {
		d.replace(a, b)
	}
	d.keep(s)
}

// split finds a point on a shortest edit path through a and b: Myers' middle snake,
// searched from both ends in linear space. Both sides must differ at their first and
// last rune (compare trimmed them). If the search hits maxDiffEdits it answers with the
// furthest point the forward search reached, so the rest is still compared; ok is
// false when the two have nothing in common, or the work budget is spent.
func (d *differ) split(a, b []rune) (x, y int, ok bool) {
	n, m := len(a), len(b)
	if n == 0 || m == 0 {
		return 0, 0, false
	}
	depth := (n + m + 1) / 2
	limited := depth > maxDiffEdits
	if limited {
		depth = maxDiffEdits
	}
	off, size := depth+1, 2*depth+3
	if cap(d.v1) < size {
		d.v1, d.v2 = make([]int, size), make([]int, size)
	}
	v1, v2 := d.v1[:size], d.v2[:size]
	for i := range v1 {
		v1[i], v2[i] = -1, -1
	}
	v1[off+1], v2[off+1] = 0, 0
	d.work -= size

	delta := n - m
	front := delta%2 != 0 // the forward search can only meet the backward one on odd sums
	// Diagonals that have run off the grid are not searched again.
	var fLow, fHigh, bLow, bHigh int
	bestX, bestY := 0, 0
	for e := 0; e < depth; e++ {
		if d.work < 0 {
			return 0, 0, false
		}
		for k := -e + fLow; k <= e-fHigh; k += 2 {
			i := off + k
			var x1 int
			if k == -e || (k != e && v1[i-1] < v1[i+1]) {
				x1 = v1[i+1]
			} else {
				x1 = v1[i-1] + 1
			}
			y1 := x1 - k
			from := x1
			for x1 < n && y1 < m && a[x1] == b[y1] {
				x1++
				y1++
			}
			d.work -= x1 - from + 1
			v1[i] = x1
			switch {
			case x1 > n:
				fHigh += 2
			case y1 > m:
				fLow += 2
			default:
				if x1+y1 > bestX+bestY {
					bestX, bestY = x1, y1
				}
				if front {
					if j := off + delta - k; j >= 0 && j < size && v2[j] != -1 && x1 >= n-v2[j] {
						return x1, y1, true
					}
				}
			}
		}
		for k := -e + bLow; k <= e-bHigh; k += 2 {
			i := off + k
			var x2 int
			if k == -e || (k != e && v2[i-1] < v2[i+1]) {
				x2 = v2[i+1]
			} else {
				x2 = v2[i-1] + 1
			}
			y2 := x2 - k
			from := x2
			for x2 < n && y2 < m && a[n-x2-1] == b[m-y2-1] {
				x2++
				y2++
			}
			d.work -= x2 - from + 1
			v2[i] = x2
			switch {
			case x2 > n:
				bHigh += 2
			case y2 > m:
				bLow += 2
			case !front:
				if j := off + delta - k; j >= 0 && j < size && v1[j] != -1 {
					x1 := v1[j]
					if y1 := off + x1 - j; x1 >= n-x2 {
						return x1, y1, true
					}
				}
			}
		}
	}
	return bestX, bestY, limited
}

type lineSpan struct{ start, end int }

// linesOf cuts text after each line break.
func linesOf(text []rune) []lineSpan {
	var out []lineSpan
	start := 0
	for i, r := range text {
		if r == '\n' {
			out = append(out, lineSpan{start, i + 1})
			start = i + 1
		}
	}
	if start < len(text) {
		out = append(out, lineSpan{start, len(text)})
	}
	return out
}

// anchored compares a and b gap by gap between the lines they certainly share.
func (d *differ) anchored(a, b []rune) {
	la, lb := linesOf(a), linesOf(b)
	pa, pb := 0, 0
	for _, an := range anchors(a, b, la, lb) {
		sa, sb := la[an[0]], lb[an[1]]
		d.compare(a[pa:sa.start], b[pb:sb.start])
		d.keep(sa.end - sa.start)
		pa, pb = sa.end, sb.end
	}
	d.compare(a[pa:], b[pb:])
}

// anchors returns the longest run of lines that appear exactly once in a and once in b
// and in the same order, as (line in a, line in b) pairs.
func anchors(a, b []rune, la, lb []lineSpan) [][2]int {
	type seen struct{ na, nb, ia, ib int }
	lines := make(map[string]*seen, len(la))
	entry := func(text []rune, s lineSpan) *seen {
		key := string(text[s.start:s.end])
		e := lines[key]
		if e == nil {
			e = &seen{}
			lines[key] = e
		}
		return e
	}
	for i, s := range la {
		e := entry(a, s)
		e.na++
		e.ia = i
	}
	for i, s := range lb {
		e := entry(b, s)
		e.nb++
		e.ib = i
	}
	var pairs [][2]int
	for _, e := range lines {
		if e.na == 1 && e.nb == 1 {
			pairs = append(pairs, [2]int{e.ia, e.ib})
		}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i][0] < pairs[j][0] })

	// Longest increasing subsequence on the b side (patience sorting).
	tails := make([]int, 0, len(pairs))
	prev := make([]int, len(pairs))
	for i, p := range pairs {
		at := sort.Search(len(tails), func(t int) bool { return pairs[tails[t]][1] >= p[1] })
		prev[i] = -1
		if at > 0 {
			prev[i] = tails[at-1]
		}
		if at == len(tails) {
			tails = append(tails, i)
		} else {
			tails[at] = i
		}
	}
	out := make([][2]int, len(tails))
	if len(tails) > 0 {
		for i, t := len(tails)-1, tails[len(tails)-1]; i >= 0; i, t = i-1, prev[t] {
			out[i] = pairs[t]
		}
	}
	return out
}

package merge

import (
	"sort"
	"strings"
)

// Union merges two texts with no common ancestor to compare them against: a local file
// meeting a shared document for the first time. A 3-way merge needs that ancestor. With
// the shared text as the base, every line the file lacks reads as a delete; with an
// empty base, the whole text is one conflict. Neither keeps what both people wrote.
//
// So Union only ever adds: the result holds every line of shared and every line of
// local, each in its own order (both are subsequences of the result, compared without
// line endings), and shared's own characters are all still there, so turning shared
// into the result deletes nothing. Where the texts differ, shared's lines come first.
// Merging either text into the result again adds no line.
//
// What it cannot know is that a line was meant to go: a line changed in the file stays
// in both forms, for the author to tidy.
//
// Lines are aligned in the patience style: on lines that appear once on each side,
// never on blank ones. Aligning on any common line would match the blank lines between
// sections and shuffle two separate sections into each other.
func Union(shared, local string) string {
	// A byte-order mark belongs to the file, not to its first line: on one side only it
	// would keep both first lines, and leave a U+FEFF in mid-text.
	const bom = "\xef\xbb\xbf"
	mark := ""
	if strings.HasPrefix(shared, bom) != strings.HasPrefix(local, bom) {
		mark, shared, local = bom, strings.TrimPrefix(shared, bom), strings.TrimPrefix(local, bom)
	}
	var out []string
	unionLines(splitKeepEnds(shared), splitKeepEnds(local), &out)
	var b strings.Builder
	b.WriteString(mark)
	for i, l := range out {
		b.WriteString(l)
		if i < len(out)-1 && !strings.HasSuffix(l, "\n") {
			b.WriteByte('\n') // the last line of one side, not the last of the result
		}
	}
	return b.String()
}

func splitKeepEnds(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// lineKey is what makes two lines the same line: their text, not their line ending.
func lineKey(l string) string { return strings.TrimRight(l, "\r\n") }

// matched is the line kept for two lines that are the same: a's, so a's bytes are never
// changed, unless b's is a's with the line ending a's lacks.
func matched(a, b string) string {
	if strings.HasPrefix(b, a) {
		return b
	}
	return a
}

// unionLines appends the merge of a (shared) and b (local) to out.
func unionLines(a, b []string, out *[]string) {
	if len(a) == 0 || len(b) == 0 {
		*out = append(append(*out, a...), b...)
		return
	}
	if containsInOrder(b, a) { // nothing of a to add to b: also what makes a re-merge a no-op
		j := 0
		for _, l := range b {
			if j < len(a) && lineKey(a[j]) == lineKey(l) {
				l = matched(a[j], l)
				j++
			}
			*out = append(*out, l)
		}
		return
	}
	if containsInOrder(a, b) {
		*out = append(*out, a...)
		return
	}
	head := 0
	for head < len(a) && head < len(b) && lineKey(a[head]) == lineKey(b[head]) {
		head++
	}
	tail := 0
	for tail < len(a)-head && tail < len(b)-head && lineKey(a[len(a)-1-tail]) == lineKey(b[len(b)-1-tail]) {
		tail++
	}
	for i := 0; i < head; i++ {
		*out = append(*out, matched(a[i], b[i]))
	}
	ma, mb := a[head:len(a)-tail], b[head:len(b)-tail]
	anchors := uniqueCommonLines(ma, mb)
	if len(anchors) == 0 {
		*out = append(append(*out, ma...), mb...) // nothing to align on: shared's, then local's
	} else {
		pa, pb := 0, 0
		for _, m := range anchors {
			unionLines(ma[pa:m.x], mb[pb:m.y], out)
			*out = append(*out, matched(ma[m.x], mb[m.y]))
			pa, pb = m.x+1, m.y+1
		}
		unionLines(ma[pa:], mb[pb:], out)
	}
	for i := tail; i > 0; i-- {
		*out = append(*out, matched(a[len(a)-i], b[len(b)-i]))
	}
}

// containsInOrder reports whether every line of sub appears in s, in order.
func containsInOrder(s, sub []string) bool {
	j := 0
	for _, l := range s {
		if j == len(sub) {
			break
		}
		if lineKey(sub[j]) == lineKey(l) {
			j++
		}
	}
	return j == len(sub)
}

// uniqueCommonLines is the longest run of pairs, increasing on both sides, of non-blank
// lines that appear exactly once in a and once in b.
func uniqueCommonLines(a, b []string) []pair {
	type seen struct{ inA, inB, atA, atB int }
	lines := make(map[string]*seen, len(a)+len(b))
	at := func(k string) *seen {
		if lines[k] == nil {
			lines[k] = &seen{}
		}
		return lines[k]
	}
	for i, l := range a {
		s := at(lineKey(l))
		s.inA++
		s.atA = i
	}
	for i, l := range b {
		s := at(lineKey(l))
		s.inB++
		s.atB = i
	}
	var cands []pair
	for i, l := range a {
		k := lineKey(l)
		if s := lines[k]; s.inA == 1 && s.inB == 1 && strings.TrimSpace(k) != "" {
			cands = append(cands, pair{i, s.atB})
		}
	}
	// Longest increasing subsequence on y (cands is already increasing on x).
	tails := []int{}                // tails[n] = index in cands ending the best run of length n+1
	prev := make([]int, len(cands)) // the run's previous element
	for i, c := range cands {
		n := sort.Search(len(tails), func(k int) bool { return cands[tails[k]].y >= c.y })
		prev[i] = -1
		if n > 0 {
			prev[i] = tails[n-1]
		}
		if n == len(tails) {
			tails = append(tails, i)
		} else {
			tails[n] = i
		}
	}
	if len(tails) == 0 {
		return nil
	}
	run := make([]pair, len(tails))
	for k, i := len(tails)-1, tails[len(tails)-1]; k >= 0; k, i = k-1, prev[i] {
		run[k] = cands[i]
	}
	return run
}

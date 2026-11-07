// Package merge implements a line-level 3-way merge (diff3) for the file-sync
// layer. When `pull` finds a file changed on both sides, instead of just saving a
// .remote copy it tries to merge base/local/remote: non-overlapping edits merge
// cleanly; only truly overlapping edits become conflict markers. This is the
// git-style behavior. (Live same-file co-editing is better served by the CRDT
// layer; this is for the coarse file-sync path.)
package merge

import "strings"

// Markers used for unresolved conflicts (git-compatible).
const (
	markerLocal  = "<<<<<<< local"
	markerSplit  = "======="
	markerRemote = ">>>>>>> remote"
)

// HasMarkers reports whether text holds an unresolved conflict block as a merge writes
// one: the three marker lines, in order, each on a line of its own.
func HasMarkers(text string) bool {
	step := 0
	want := []string{markerLocal, markerSplit, markerRemote}
	for _, l := range strings.Split(text, "\n") {
		if strings.TrimRight(l, "\r") == want[step] {
			if step++; step == len(want) {
				return true
			}
		}
	}
	return false
}

// maxMergeLines caps the per-side line count fed to the O(n*m) LCS (F-INV-38).
// lcsMatches allocates a (len(x)+1)x(len(y)+1) int matrix; at ~50k lines that is
// ~20GiB, so a co-tenant committing a huge file that conflicts with a victim's
// local edit could OOM the victim's `acp pull` CLI. Above this cap we do NOT run
// the LCS at all — we fall back to a whole-file conflict (markers) the operator
// resolves by hand, exactly the "too large to auto-merge" degradation. Named
// constant (No-Magic); ~20k lines covers every realistic hand-edited text file.
const maxMergeLines = 20000

// Diff3 merges local and remote against their common base. Returns the merged
// lines and whether any conflict markers were emitted.
func Diff3(base, local, remote []string) ([]string, bool) {
	if len(base) > maxMergeLines || len(local) > maxMergeLines || len(remote) > maxMergeLines {
		// Input too large for the quadratic LCS: skip it (never allocate the
		// matrix) and emit one whole-file conflict for manual resolution.
		return wholeFileConflict(local, remote), true
	}
	la := lcsMatches(base, local)
	lb := lcsMatches(base, remote)
	aMatch := map[int]int{}
	bMatch := map[int]int{}
	for _, p := range la {
		aMatch[p.x] = p.y
	}
	for _, p := range lb {
		bMatch[p.x] = p.y
	}

	// Sync points: base lines matched unchanged in BOTH derivatives.
	type sync struct{ bi, ai, bj int }
	var syncs []sync
	for bi := 0; bi < len(base); bi++ {
		if ai, ok := aMatch[bi]; ok {
			if bj, ok2 := bMatch[bi]; ok2 {
				syncs = append(syncs, sync{bi, ai, bj})
			}
		}
	}

	var out []string
	conflict := false
	bPos, aPos, rPos := 0, 0, 0
	emitChunk := func(bEnd, aEnd, rEnd int) {
		baseC := base[bPos:bEnd]
		aC := local[aPos:aEnd]
		rC := remote[rPos:rEnd]
		switch {
		case equal(aC, baseC):
			out = append(out, rC...) // only remote changed
		case equal(rC, baseC):
			out = append(out, aC...) // only local changed
		case equal(aC, rC):
			out = append(out, aC...) // same change both sides
		default:
			conflict = true
			out = append(out, markerLocal)
			out = append(out, aC...)
			out = append(out, markerSplit)
			out = append(out, rC...)
			out = append(out, markerRemote)
		}
	}
	for _, s := range syncs {
		emitChunk(s.bi, s.ai, s.bj)
		out = append(out, base[s.bi]) // the synced (unchanged-in-both) line
		bPos, aPos, rPos = s.bi+1, s.ai+1, s.bj+1
	}
	emitChunk(len(base), len(local), len(remote)) // trailing chunk
	return out, conflict
}

// MergeText is a string convenience wrapper. Preserves a trailing newline shape
// reasonably: it splits on "\n", merges, and rejoins with "\n".
// maxMergeCells bounds the O(n*m) LCS matrix (F-INV-38). lcsMatches allocates an
// (n+1)*(m+1) int matrix; two ~50k-line files would be ~2.5e9 cells (~20GiB) and
// OOM the client's `acp pull`. Above this ceiling we do NOT run the merge — we
// keep the local file and flag a conflict for the user to resolve by hand.
const maxMergeCells = 25_000_000 // ~200MiB of ints, regardless of file shape

func tooBigToMerge(nb, nl, nr int) bool {
	c := func(a, b int) bool { return int64(a)*int64(b) > maxMergeCells }
	return c(nb, nl) || c(nb, nr) || c(nl, nr)
}

// MergeText merges base/local/remote text line by line (Diff3) and rejoins with "\n".
//
// Too big to merge line by line, MergeText returns ONE whole-file conflict holding both
// sides between markers (never just one side: before v0.2.6 it returned the local text
// alone with conflict=true, and a caller that wrote it lost the remote edit, hunt B-4).
// A caller that can keep both sides apart (a sidecar file) uses MergeTextChecked.
func MergeText(base, local, remote string) (string, bool) {
	b, l, rm := splitLines(base), splitLines(local), splitLines(remote)
	if tooBigToMerge(len(b), len(l), len(rm)) {
		return strings.Join(wholeFileConflict(l, rm), "\n"), true // no LCS matrix (F-INV-38)
	}
	merged, conflict := Diff3(b, l, rm)
	return strings.Join(merged, "\n"), conflict
}

// MergeTextChecked is MergeText that says when the input is too big to merge line by line
// (the LCS would need more than maxMergeCells cells, or a side has more than maxMergeLines
// lines): then tooBig is true, merged is "" and NO merge was attempted — the caller keeps
// both sides as they are (e.g. the remote version in a sidecar file).
func MergeTextChecked(base, local, remote string) (merged string, conflict, tooBig bool) {
	b, l, rm := splitLines(base), splitLines(local), splitLines(remote)
	if tooBigToMerge(len(b), len(l), len(rm)) || len(b) > maxMergeLines || len(l) > maxMergeLines || len(rm) > maxMergeLines {
		return "", true, true
	}
	out, conflict := Diff3(b, l, rm)
	return strings.Join(out, "\n"), conflict, false
}

// wholeFileConflict is both sides, whole, between conflict markers.
func wholeFileConflict(local, remote []string) []string {
	out := make([]string, 0, len(local)+len(remote)+3)
	out = append(out, markerLocal)
	out = append(out, local...)
	out = append(out, markerSplit)
	out = append(out, remote...)
	return append(out, markerRemote)
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

type pair struct{ x, y int }

// lcsMatches returns matching (xIdx, yIdx) line pairs of the LCS, increasing.
func lcsMatches(x, y []string) []pair {
	n, m := len(x), len(y)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if x[i] == y[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var out []pair
	i, j := 0, 0
	for i < n && j < m {
		if x[i] == y[j] {
			out = append(out, pair{i, j})
			i++
			j++
		} else if dp[i+1][j] >= dp[i][j+1] {
			i++
		} else {
			j++
		}
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

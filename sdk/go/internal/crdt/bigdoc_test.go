package crdt

// R-C0 (review F-1). Above the old 65,536-character cap a one-character edit was
// delete-all then insert-all: readers watched the document empty while it was sent, a
// stopped push left it gutted, and two ordinary edits doubled it. Every test here is
// pure engine state: no disk, no network.

import (
	"fmt"
	"math/rand"
	"runtime"
	"strings"
	"testing"
	"time"
)

// docOf builds a replica already holding text in O(n), so a test can start from a big
// document without paying for one op per character.
func docOf(replica, text string) *RGA {
	s := State{Replica: replica}
	prev := Zero
	for _, ch := range text {
		s.Clock++
		id := ID{Clock: s.Clock, Replica: "seed"}
		s.Els = append(s.Els, ElemWire{ID: id, OriginLeft: prev, Val: string(ch)})
		prev = id
	}
	return Load(s)
}

// prose is a line-shaped document of at least n characters whose lines are all
// different, the way a long report is.
func prose(n int) string {
	var b strings.Builder
	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, "Line %d of the shared report, with a few ordinary words in it.\n", i)
	}
	return b.String()
}

// randomText has no line breaks and no long common run with another call.
func randomText(rng *rand.Rand, n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte('a' + rng.Intn(26))
	}
	return string(out)
}

func countOps(ops []Op) (ins, del int) {
	for _, op := range ops {
		if op.Type == OpDelete {
			del++
		} else {
			ins++
		}
	}
	return
}

func TestBigDocOneCharEditIsOneOp(t *testing.T) {
	for _, size := range []int{70_000, 300_000} {
		text := prose(size)
		mid := len(text) / 2
		edited := text[:mid] + "X" + text[mid+1:]
		r := docOf("a", text)
		if n := r.DeletesFor(edited); n != 1 {
			t.Fatalf("%d chars: DeletesFor=%d, want 1", size, n)
		}
		ops := r.GenerateOps(edited)
		if ins, del := countOps(ops); ins != 1 || del != 1 {
			t.Fatalf("%d chars: a one-character edit made %d inserts and %d deletes, want 1 and 1", size, ins, del)
		}
		if r.Text() != edited {
			t.Fatalf("%d chars: wrong text after the edit", size)
		}
	}
}

// A document with no line breaks at all: the anchors have nothing to hold on to, and a
// small edit must still be small.
func TestBigDocOneLineEditIsSmall(t *testing.T) {
	text := randomText(rand.New(rand.NewSource(1)), 200_000)
	edited := text[:1000] + "XY" + text[1001:150_000] + text[150_010:]
	r := docOf("a", text)
	ops := r.GenerateOps(edited)
	if ins, del := countOps(ops); ins > 2 || del > 11 {
		t.Fatalf("made %d inserts and %d deletes", ins, del)
	}
	if r.Text() != edited {
		t.Fatal("wrong text after the edit")
	}
}

// The worst case, a real rewrite: while the ops arrive a chunk at a time, a reader
// never sees less text than the shorter of the two, and a push stopped at any chunk
// leaves extra text, never a gutted document.
func TestBigDocInterruptedPushNeverShrinksTheDoc(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for _, tc := range []struct {
		name          string
		before, after string
	}{
		{"unrelated, same size", randomText(rng, 12_000), randomText(rng, 12_000)},
		{"unrelated, shorter", randomText(rng, 12_000), randomText(rng, 5_000)},
		{"unrelated, longer", randomText(rng, 5_000), randomText(rng, 12_000)},
		{"small edit", prose(12_000), strings.Replace(prose(12_000), "Line 500 ", "Line five hundred ", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := docOf("a", tc.before)
			reader := docOf("reader", tc.before)
			ops := r.GenerateOps(tc.after)
			if r.Text() != tc.after {
				t.Fatal("writer's own text is wrong")
			}
			floor := min(len(tc.before), len(tc.after))
			const chunk = 500 // the wire's chunk is larger; a finer cut is a stricter test
			for i := 0; i < len(ops); i += chunk {
				for _, op := range ops[i:min(i+chunk, len(ops))] {
					reader.Apply(op)
				}
				if got := reader.LiveCount(); got < floor {
					t.Fatalf("after %d of %d ops a reader sees %d characters, floor %d", i+chunk, len(ops), got, floor)
				}
			}
			if reader.Text() != tc.after {
				t.Fatal("reader did not end on the new text")
			}
		})
	}
}

func TestBigDocTwoEditsConverge(t *testing.T) {
	text := prose(70_000)
	aText := strings.Replace(text, "Line 100 ", "Line 100 (dana) ", 1)
	bText := strings.Replace(text, "Line 900 ", "Line 900 (eli) ", 1)
	a, b := docOf("dana", text), docOf("eli", text)
	aOps, bOps := a.GenerateOps(aText), b.GenerateOps(bText)
	for _, op := range bOps {
		a.Apply(op)
	}
	for _, op := range aOps {
		b.Apply(op)
	}
	if a.Text() != b.Text() {
		t.Fatal("the two replicas differ")
	}
	want := strings.Replace(aText, "Line 900 ", "Line 900 (eli) ", 1)
	if a.Text() != want {
		t.Fatalf("merged doc is %d chars, want %d: both edits once, nothing doubled", len(a.Text()), len(want))
	}
}

// One writer rewrites two large sections far apart; a teammate fixes a word in the text
// between them. The text between is kept, so the teammate's fix lands in the only copy.
func TestBigDocRewrittenSectionsKeepTheTextBetween(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	lines := strings.SplitAfter(prose(3_000_000), "\n")
	section := func(n int) string {
		var b strings.Builder
		for range n {
			b.WriteString(randomText(rng, 60) + "\n")
		}
		return b.String()
	}
	const a0, a1, b0, b1 = 5_000, 15_000, 25_000, 35_000
	head, s1, between, s2, tail := strings.Join(lines[:a0], ""), strings.Join(lines[a0:a1], ""),
		strings.Join(lines[a1:b0], ""), strings.Join(lines[b0:b1], ""), strings.Join(lines[b1:], "")
	steps := diff([]rune(head+s1+between+s2+tail), []rune(head+section(a1-a0)+between+section(b1-b0)+tail))
	deleted := 0
	for _, s := range steps {
		if s.kind == del {
			deleted++
		}
	}
	if deleted > len(s1)+len(s2) {
		t.Fatalf("rewriting %d characters deleted %d: unchanged text was replaced too", len(s1)+len(s2), deleted)
	}
}

// Many small edits scattered through a document with nothing to anchor on stay many
// small edits: the search hits its depth limit and carries on from where it got to.
func TestBigDocManyScatteredEditsStaySmall(t *testing.T) {
	rng := rand.New(rand.NewSource(6))
	text := []rune(randomText(rng, 200_000))
	const every = 20
	edited := append([]rune(nil), text...)
	for i := 10; i < len(edited); i += every {
		edited[i] = 'X'
	}
	steps := diff(text, edited)
	checkScript(t, text, edited, steps)
	kept := 0
	for _, s := range steps {
		if s.kind == keep {
			kept++
		}
	}
	if want := len(text) * 9 / 10; kept < want {
		t.Fatalf("kept %d of %d characters; changing 1 in %d should keep at least %d", kept, len(text), every, want)
	}
}

// lcsLen is the textbook answer for small inputs.
func lcsLen(a, b []rune) int {
	prev, cur := make([]int, len(b)+1), make([]int, len(b)+1)
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			switch {
			case a[i] == b[j]:
				cur[j] = prev[j+1] + 1
			case prev[j] >= cur[j+1]:
				cur[j] = prev[j]
			default:
				cur[j] = cur[j+1]
			}
		}
		prev, cur = cur, prev
	}
	return prev[0]
}

// checkScript proves a script turns old into new, and returns how many runes it keeps.
func checkScript(t *testing.T, old, new []rune, steps []step) int {
	t.Helper()
	var out []rune
	i, kept := 0, 0
	for _, s := range steps {
		switch s.kind {
		case keep:
			out = append(out, old[i])
			i++
			kept++
		case del:
			i++
		case ins:
			out = append(out, s.r)
		}
	}
	if i != len(old) || string(out) != string(new) {
		t.Fatalf("script does not turn %q into %q (got %q, consumed %d of %d)", string(old), string(new), string(out), i, len(old))
	}
	return kept
}

// The script is valid and minimal on every shape up to where it is promised exact.
func TestDiffIsAMinimalScript(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	const iters = 3000
	for it := range iters {
		alphabet := []int{1, 2, 3, 4, 8, 26}[it%6]
		mk := func() []rune {
			out := make([]rune, rng.Intn(120))
			for i := range out {
				out[i] = rune('a' + rng.Intn(alphabet))
			}
			return out
		}
		a, b := mk(), mk()
		if it%3 == 0 { // near-copy: the shape real edits have
			b = append([]rune(nil), a...)
			for range rng.Intn(6) {
				if len(b) == 0 {
					break
				}
				p := rng.Intn(len(b))
				switch rng.Intn(3) {
				case 0:
					b[p] = rune('a' + rng.Intn(alphabet))
				case 1:
					b = append(b[:p], b[p+1:]...)
				default:
					b = append(b[:p], append([]rune{rune('a' + rng.Intn(alphabet))}, b[p:]...)...)
				}
			}
		}
		if kept, want := checkScript(t, a, b, diff(a, b)), lcsLen(a, b); kept != want {
			t.Fatalf("%q -> %q kept %d, the longest common subsequence is %d", string(a), string(b), kept, want)
		}
	}
	// Non-ASCII runes count as one each; a character cut in half would corrupt the text.
	a, b := []rune("héllo wörld 日本語"), []rune("héllo, wörld 日本語!")
	checkScript(t, a, b, diff(a, b))
}

// diffWorkSeen is the search work each case spends (diffWork), counted when the budget was
// pinned. The inputs are seeded, so the count is exact on every machine; the cases that spend
// the whole budget sit just over maxDiffWork, which TestDiffBudgetIsPinned holds.
var diffWorkSeen = map[string]int{
	"unrelated 60k": 268440046, "unrelated 1M": 268439014, "unrelated lines": 268442287,
	"same letter": 0, "two letters out of step": 3224577, "all lines alike": 0,
	"small to huge": 49378, "huge to small": 49476, "empty to huge": 0, "huge to empty": 0,
}

// The budget is what keeps a 1 M-character save from hanging a mount or a sync, and the work
// guard above is in units of it: pinned by value, so a bigger budget is a decision, not a drift.
func TestDiffBudgetIsPinned(t *testing.T) {
	if maxDiffWork != 1<<28 || maxDiffEdits != 1<<12 {
		t.Fatalf("maxDiffWork %d, maxDiffEdits %d: change them on purpose, and this test with them", maxDiffWork, maxDiffEdits)
	}
}

// diffYardstickLimit is what a diff may cost, in yardsticks. The most one costs is
// maxDiffWork search steps, and a yardstick walks an array that many steps. The worst case
// here spends the whole budget and comes to 0.02 to 52 of them on a shared box, by wall clock
// or by the thread's own CPU time alike: a time limit can only be a backstop for "not hours".
// The work count (diffWorkSeen) is what catches a diff that got 3 times slower in its steps.
const diffYardstickLimit = 200

var yardstickSink int

// yardstick is how long this machine takes, now, for maxDiffWork plain array steps, from a
// 64th of them. A wall clock can't hold on both a quiet box and a loaded one under -race; a
// ratio to this does.
func yardstick() time.Duration {
	v := make([]int, 1<<16)
	start := time.Now()
	sum, at := 0, 0
	for i := 0; i < maxDiffWork/64; i++ {
		at = (at + 4099) & (len(v) - 1)
		v[at] += i
		sum += v[at]
	}
	yardstickSink += sum
	return 64 * time.Since(start)
}

// The worst inputs: nothing in common, the same character over and over, one huge side.
// Each stays correct, and time and memory stay bounded. Before this fix, 60,000 by
// 60,000 asked for a matrix of about 29 GB (the old cap was per side) and died.
func TestDiffWorstCaseStaysBounded(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for _, tc := range []struct {
		name string
		a, b string
	}{
		{"unrelated 60k", randomText(rng, 60_000), randomText(rng, 60_000)},
		{"unrelated 1M", randomText(rng, 1_000_000), randomText(rng, 1_000_000)},
		{"unrelated lines", prose(1_000_000), strings.ToUpper(prose(1_000_000))},
		{"same letter", strings.Repeat("a", 1_000_000), strings.Repeat("a", 600_000)},
		{"two letters out of step", strings.Repeat("ab", 500_000), strings.Repeat("ba", 400_000)},
		{"all lines alike", strings.Repeat("same\n", 200_000), strings.Repeat("same\n", 150_000) + "x\n" + strings.Repeat("same\n", 50_000)},
		{"small to huge", "x", randomText(rng, 1_000_000)},
		{"huge to small", randomText(rng, 1_000_000), "x"},
		{"empty to huge", "", randomText(rng, 100_000)},
		{"huge to empty", randomText(rng, 100_000), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old, new := []rune(tc.a), []rune(tc.b)
			var m0, m1 runtime.MemStats
			yard := yardstick()
			runtime.ReadMemStats(&m0)
			start := time.Now()
			steps, spent := diffWork(old, new)
			took := time.Since(start)
			// The work, by count: a diff that does 3 times the work fails here on any machine.
			if want := diffWorkSeen[tc.name]; spent > 2*want {
				t.Fatalf("spent %d search steps, against %d when this was written", spent, want)
			}
			runtime.ReadMemStats(&m1)
			checkScript(t, old, new, steps)
			// The budget in units of this machine, now: -race and a loaded box slow both alike.
			if unit := max(yard, yardstick()); took > diffYardstickLimit*unit {
				t.Fatalf("took %v, %d times a walk of the same length (%v)", took, took/unit, unit)
			}
			if alloc := m1.TotalAlloc - m0.TotalAlloc; alloc > 1<<30 {
				t.Fatalf("allocated %d MB", alloc>>20)
			}
		})
	}
}

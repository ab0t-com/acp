package merge

import (
	"math/rand"
	"strings"
	"testing"
)

func lines(s string) []string {
	var out []string
	for _, l := range splitKeepEnds(s) {
		out = append(out, lineKey(l))
	}
	return out
}

// isSubsequence reports whether every rune of sub appears in s, in order.
func isSubsequence(sub, s string) bool {
	r := []rune(sub)
	i := 0
	for _, c := range s {
		if i < len(r) && r[i] == c {
			i++
		}
	}
	return i == len(r)
}

func checkUnion(t *testing.T, shared, local string) string {
	t.Helper()
	got := Union(shared, local)
	if !containsInOrder(lines(got), lines(shared)) {
		t.Fatalf("Union(%q, %q) = %q lost a shared line", shared, local, got)
	}
	if !containsInOrder(lines(got), lines(local)) {
		t.Fatalf("Union(%q, %q) = %q lost a local line", shared, local, got)
	}
	if !isSubsequence(shared, got) {
		t.Fatalf("Union(%q, %q) = %q: turning the shared text into this would delete from it", shared, local, got)
	}
	// A crash after the merge, before the push: the file holds the merge, the server the old text.
	if again := Union(shared, got); strings.Join(lines(again), "\n") != strings.Join(lines(got), "\n") {
		t.Fatalf("Union(%q, %q) = %q, and merged again %q", shared, local, got, again)
	}
	// The merge landed, and another folder holds the same draft.
	if again := Union(got, local); again != got {
		t.Fatalf("Union(%q, %q) = %q, and the draft merged into that %q", shared, local, got, again)
	}
	return got
}

func TestUnion(t *testing.T) {
	for _, c := range []struct{ name, shared, local, want string }{
		{"both empty", "", "", ""},
		{"empty shared", "", "mine\n", "mine\n"},
		{"empty local", "theirs\n", "", "theirs\n"},
		{"equal", "a\nb\n", "a\nb\n", "a\nb\n"},
		{"nothing in common: shared first", "theirs\n", "mine\n", "theirs\nmine\n"},
		{"file adds at the end", "a\nb\n", "a\nb\nc\n", "a\nb\nc\n"},
		{"file adds at the start", "a\nb\n", "z\na\nb\n", "z\na\nb\n"},
		{"file adds in the middle", "a\nb\n", "a\nm\nb\n", "a\nm\nb\n"},
		{"file lacks a line: it stays", "a\nb\nc\n", "a\nc\n", "a\nb\nc\n"},
		{"file changed a line: both forms stay", "t\none\nlast\n", "t\nONE\nlast\n", "t\none\nONE\nlast\n"},
		{"additions on both sides", "s0\nkeep\ns1\n", "l0\nkeep\nl1\n", "s0\nl0\nkeep\ns1\nl1\n"},
		{"no final newline on shared", "a\nb", "c\n", "a\nb\nc\n"},
		{"no final newline on local", "a\n", "b", "a\nb"},
		{"no final newline on either", "a", "b", "a\nb"},
		{"same line, newline differs", "a\nb", "a\nb\n", "a\nb\n"},
		{"same line, CRLF on one side keeps shared", "a\r\nb\r\n", "a\nb\nc\n", "a\r\nb\r\nc\n"},
		{"sections are never shuffled by blank lines", "# A\n\na1\n\na2\n", "# B\n\nb1\n\nb2\n", "# A\n\na1\n\na2\n# B\n\nb1\n\nb2\n"},
		{"sections are moved whole around one common line", "# A\na1\n---\n# C\n", "# B\nb1\n---\n# D\n", "# A\na1\n# B\nb1\n---\n# C\n# D\n"},
		{"repeated lines stay as many as the longer side", "x\nfoo\nfoo\ny\n", "x\nfoo\nfoo\nfoo\ny\n", "x\nfoo\nfoo\nfoo\ny\n"},
		{"unicode", "é\n", "日本語\n", "é\n日本語\n"},
		{"swapped lines keep one of each order", "a\nb\n", "b\na\n", "a\nb\na\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := checkUnion(t, c.shared, c.local); got != c.want {
				t.Fatalf("Union(%q, %q) = %q, want %q", c.shared, c.local, got, c.want)
			}
		})
	}
}

// Random texts over a small alphabet: repeated lines, blank lines, missing final
// newlines and CRLF are all common, which is where a merge loses or doubles a line.
func TestUnionNeverLosesALineAndNeverDeletes(t *testing.T) {
	alphabet := []string{"", "a", "b", "c", "d", "---", "  ", "é"}
	rng := rand.New(rand.NewSource(1))
	gen := func() string {
		var b strings.Builder
		for n := rng.Intn(12); n > 0; n-- {
			b.WriteString(alphabet[rng.Intn(len(alphabet))])
			if rng.Intn(6) == 0 {
				b.WriteByte('\r')
			}
			b.WriteByte('\n')
		}
		s := b.String()
		if rng.Intn(3) == 0 {
			s = strings.TrimRight(s, "\r\n")
		}
		return s
	}
	for i := 0; i < 20000; i++ {
		checkUnion(t, gen(), gen())
	}
}

func TestUnionOfALargeFileIsFast(t *testing.T) {
	var a, b strings.Builder
	for i := 0; i < 200000; i++ {
		a.WriteString("shared line ")
		a.WriteString(strings.Repeat("x", i%7))
		a.WriteByte('\n')
		b.WriteString("local line ")
		b.WriteString(strings.Repeat("y", i%5))
		b.WriteByte('\n')
	}
	if got := Union(a.String(), b.String()); !strings.HasPrefix(got, a.String()) {
		t.Fatal("a large merge lost the shared text")
	}
}

// F-24: a byte-order mark on one side only is not a change of the first line.
func TestUnionOfAByteOrderMarkOnOneSide(t *testing.T) {
	const bom = "\xef\xbb\xbf"
	for _, c := range []struct{ shared, local, want string }{
		{bom + "a\nb\n", "a\nb\n", bom + "a\nb\n"},
		{"a\nb\n", bom + "a\nb\n", bom + "a\nb\n"},
		{"a\nb\n", bom + "a\nc\n", bom + "a\nb\nc\n"},
		{bom + "a\n", bom + "a\n", bom + "a\n"},
	} {
		got := Union(c.shared, c.local)
		if got != c.want {
			t.Errorf("Union(%q, %q) = %q, want %q", c.shared, c.local, got, c.want)
		}
		if strings.Count(got, bom) > 1 {
			t.Errorf("a mark in mid-text: %q", got)
		}
		if again := Union(got, c.local); again != got {
			t.Errorf("merging again changed it: %q", again)
		}
	}
}

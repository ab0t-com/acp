package acpignore

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type vectors struct {
	Cases []struct {
		Name  string          `json:"name"`
		File  string          `json:"file"`
		Paths map[string]bool `json:"paths"`
	} `json:"cases"`
	Invalid []struct {
		File string `json:"file"`
		Line int    `json:"line"`
	} `json:"invalid"`
}

func loadVectors(t *testing.T) vectors {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func answer(m *Matcher, p string) bool {
	if d, ok := strings.CutSuffix(p, "/"); ok {
		return m.IgnoredDir(d)
	}
	return m.Ignored(p)
}

// IG-23: every vector, from the file a port to another language reuses.
func TestIgnoreVectors(t *testing.T) {
	v := loadVectors(t)
	if len(v.Cases) < 13 || len(v.Invalid) < 10 {
		t.Fatalf("the vectors did not load: %d %d", len(v.Cases), len(v.Invalid))
	}
	for _, c := range v.Cases {
		t.Run(c.Name, func(t *testing.T) {
			m, err := Parse([]byte(c.File))
			if err != nil {
				t.Fatal(err)
			}
			for p, want := range c.Paths {
				if got := answer(m, p); got != want {
					t.Errorf("%q: got %v, want %v", p, got, want)
				}
			}
		})
	}
}

// IG-15, IG-16: every invalid form is refused with its line, never skipped.
func TestIgnoreParseRejectsEveryInvalidForm(t *testing.T) {
	for _, c := range loadVectors(t).Invalid {
		_, err := Parse([]byte(c.File))
		var e *Error
		if !errors.As(err, &e) {
			t.Errorf("%q: got %v, want an *Error", c.File, err)
			continue
		}
		if e.Line != c.Line {
			t.Errorf("%q: line %d, want %d (%v)", c.File, e.Line, c.Line, e)
		}
	}
	if _, err := Parse([]byte("ok\n\xff\n")); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("not UTF-8: %v", err)
	}
	big := strings.Repeat("a\n", MaxSize/2+1)
	if _, err := Parse([]byte(big)); err == nil {
		t.Fatal("a file over the limit must be refused")
	}
	if _, err := Parse([]byte(big[:MaxSize])); err != nil {
		t.Fatalf("a file at the limit is fine: %v", err)
	}
}

// A line that is only spaces, or a comment with a bracket in it, is not an error.
func TestIgnoreEmptyNeverMeansAll(t *testing.T) {
	for _, f := range []string{"", "\n\n", "   \n", "# x\n#[y]\n", "\xef\xbb\xbf"} {
		m, err := Parse([]byte(f))
		if err != nil {
			t.Fatalf("%q: %v", f, err)
		}
		if !m.Empty() || m.Ignored("a") || m.IgnoredDir("a") {
			t.Fatalf("%q ignored something", f)
		}
	}
	var none *Matcher
	if none.Ignored("a") || none.IgnoredDir("a") || !none.Empty() || none.Selected("a", false) || len(none.Patterns()) != 0 {
		t.Fatal("a nil matcher ignores nothing")
	}
}

func TestIgnoreIgnoredDirectoryIsFinal(t *testing.T) {
	m, err := Parse([]byte("build/\n!build/keep.txt\n"))
	if err != nil {
		t.Fatal(err)
	}
	v := m.Check("build/keep.txt", false)
	if !v.Ignored || v.By != "build/" || v.Line != 1 || v.Pattern != "build/" {
		t.Fatalf("%+v", v)
	}
}

func TestIgnoreCheckNamesTheDecidingLine(t *testing.T) {
	m, err := Parse([]byte("# why\n*.log\n!keep.log\n"))
	if err != nil {
		t.Fatal(err)
	}
	if v := m.Check("keep.log", false); v.Ignored || !v.Negated || v.Line != 3 {
		t.Fatalf("%+v", v)
	}
	if v := m.Check("a.log", false); !v.Ignored || v.Line != 2 || v.By != "a.log" {
		t.Fatalf("%+v", v)
	}
	if v := m.Check("a.txt", false); v.Ignored || v.Line != 0 {
		t.Fatalf("%+v", v)
	}
}

func TestIgnoreCaseSensitiveNoNormalisation(t *testing.T) {
	m, err := Parse([]byte("Build/\ncafé\n"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Ignored("build/x") || !m.Ignored("Build/x") {
		t.Fatal("case")
	}
	if !m.Ignored("caf\u00e9") || m.Ignored("cafe\u0301") {
		t.Fatal("two spellings of é are two names")
	}
}

// IG-24: "**/" x 200 + "x" against 500 segments, through the whole check. Bounded by work
// count (R-G2 d, class CL-103: work that grows faster than its input), never by time: the
// matcher counts its inner steps, and the check stops the moment it passes a fixed multiple
// of the polynomial it is meant to cost. A matcher that backtracks again fails here at once.
func TestIgnoreMatcherPolynomial(t *testing.T) {
	const segments, doubleStars, letters, stars = 500, 200, 5000, 300
	for _, c := range []struct {
		name, pattern, path string
		steps               int
	}{
		// Each ancestor of the path is tried against the whole table: path x pattern x path.
		{"** over path segments", strings.Repeat("**/", doubleStars) + "x\n", strings.TrimSuffix(strings.Repeat("a/", segments), "/"), segments * doubleStars * segments},
		// The same shape in one name: the two-pointer method, pattern x text.
		{"* within one segment", strings.Repeat("*a", stars) + "b\n", strings.Repeat("a", letters), stars * 2 * letters},
	} {
		t.Run(c.name, func(t *testing.T) {
			m, err := Parse([]byte(c.pattern))
			if err != nil {
				t.Fatal(err)
			}
			spent, ok := matcherWork(m, c.path, uint64(matcherStepLimit*c.steps))
			if !ok {
				t.Fatalf("the check passed %d steps, %d x the %d its polynomial allows: the matcher is backtracking again", spent, matcherStepLimit, c.steps)
			}
			t.Logf("%d steps for a polynomial of %d", spent, c.steps)
		})
	}
}

// matcherStepLimit is what the matcher may spend per step of its polynomial. The two shapes
// above take about 0.5 and 0.003 (counted, the same on every machine and under -race).
const matcherStepLimit = 4

// matcherWork runs one check with a step limit: the steps spent, and false when it ran out
// (the check stops there, ignored or not).
func matcherWork(m *Matcher, p string, limit uint64) (spent uint64, ok bool) {
	w := &work{limit: limit}
	defer func() {
		if r := recover(); r != nil {
			if _, over := r.(overBudget); !over {
				panic(r)
			}
			spent, ok = w.n, false
		}
	}()
	if m.check(p, false, w).Ignored {
		panic("the path does not end in the pattern's last character")
	}
	return w.n, true
}

func TestIgnoreLoad(t *testing.T) {
	dir := t.TempDir()
	if m, err := Load(dir); m != nil || err != nil {
		t.Fatalf("no file: %v %v", m, err)
	}
	p := filepath.Join(dir, FileName)
	if err := os.WriteFile(p, []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if m, err := Load(dir); err != nil || !m.Ignored("a") {
		t.Fatalf("%v %v", m, err)
	}
	if err := os.WriteFile(p, []byte(strings.Repeat("a\n", MaxSize)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("too big")
	}
	// Not a regular file: refused, not read as "no rules", which would publish more.
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("a folder in its place")
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/hostname", p); err != nil {
		t.Skip(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("a link in its place")
	}
}

package crdt

// The Go half of the shared text table: acp/testdata/crdt-text-vectors.json holds what a
// text diff sends (the exact ops, by digest) and what a line union answers, and the
// TypeScript SDK's test/crdt-text-vectors.test.mjs reads the same file. This test
// recomputes every answer here, so a change to either engine fails on its own side
// instead of leaving the other language pinned to an old answer. A failure is a real
// change of behaviour: if it is meant, change both engines, then
//
//	ACP_CRDT_VECTORS_UPDATE=1 go test ./internal/crdt -run TestTextVectors
//
// rewrites the answers (the inputs stay), and the TS suite says whether the twins agree.
// Pure: no network, and the file is only written when that variable is set.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ab0t-com/acp/sdk/go/internal/merge"
)

const (
	textVectorsFile   = "crdt-text-vectors.json"
	textVectorsUpdate = "ACP_CRDT_VECTORS_UPDATE"
	// The table's size today. Fewer cases is a lost table, not a passing one.
	textVectorsMinDiff  = 253
	textVectorsMinUnion = 425
)

type textVectors struct {
	About string      `json:"about"`
	Diff  []diffCase  `json:"diff"`
	Union []unionCase `json:"union"`
}

type diffCase struct {
	Name string `json:"name"`
	Old  any    `json:"old"`
	New  any    `json:"new"`
	Ins  int    `json:"ins"`
	Del  int    `json:"del"`
	Ops  string `json:"ops_sha256"`
}

type unionCase struct {
	Name   string `json:"name"`
	Shared string `json:"shared"`
	Local  string `json:"local"`
	Want   string `json:"want"`
}

// textVectorsPath finds acp/testdata by walking up from the package directory, as
// pkg/acpuri's vectors do. Inside the acp module a missing file fails; only a published
// copy of this package, with no acp tree above it, skips.
func textVectorsPath(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		p := filepath.Join(dir, "testdata", textVectorsFile)
		if _, err := os.Stat(p); err == nil {
			return p
		}
		if b, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil && bytes.Contains(b, []byte("module github.com/ab0t-com/acp\n")) {
			t.Fatalf("%s not found under %s/testdata: the shared Go/TS table is missing", textVectorsFile, dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skipf("%s lives in the acp source tree (acp/testdata); not present in this copy", textVectorsFile)
		}
		dir = parent
	}
}

func readTextVectors(t *testing.T) (string, textVectors) {
	t.Helper()
	p := textVectorsPath(t)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var v textVectors
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s: %v", p, err)
	}
	return p, v
}

func writeTextVectors(t *testing.T, p string, v textVectors) {
	t.Helper()
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetIndent("", " ")
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func vectorProse(n int) string {
	var b strings.Builder
	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, "Line %d of the shared report, with a few ordinary words in it.\n", i)
	}
	return b.String()
}

// vectorRandom is the table's 32-bit generator, the same in both languages.
func vectorRandom(seed uint32, n int) string {
	out := make([]byte, n)
	x := seed
	for i := range out {
		x = x*1664525 + 1013904223
		out[i] = byte('a' + (x>>16&0x7fff)%26)
	}
	return string(out)
}

// vectorPair reads a recipe's two numbers.
func vectorPair(t *testing.T, v any) (int, int) {
	t.Helper()
	p, ok := v.([]any)
	if !ok || len(p) != 2 {
		t.Fatalf("bad recipe arguments: %#v", v)
	}
	return int(p[0].(float64)), int(p[1].(float64))
}

// vectorText builds a side of a case. A text is a string, or {"prose": n}, {"random":
// [seed, n]}, {"same": [from, to]} (the old text's bytes, on the "new" side), {"lit":
// text}, or a list of those, joined.
func vectorText(t *testing.T, r any, old string) string {
	t.Helper()
	switch v := r.(type) {
	case string:
		return v
	case []any:
		var b strings.Builder
		for _, piece := range v {
			b.WriteString(vectorText(t, piece, old))
		}
		return b.String()
	case map[string]any:
		if n, ok := v["prose"]; ok {
			return vectorProse(int(n.(float64)))
		}
		if a, ok := v["random"]; ok {
			seed, n := vectorPair(t, a)
			return vectorRandom(uint32(seed), n)
		}
		if a, ok := v["same"]; ok {
			from, to := vectorPair(t, a)
			return old[from:to]
		}
		if s, ok := v["lit"]; ok {
			return s.(string)
		}
	}
	t.Fatalf("bad recipe: %#v", r)
	return ""
}

// vectorDiff is what GenerateOps answers for a case: counts, and a digest of one line
// per op, "<type> <clock> <origin clock> <code point in hex>" ("-" for a delete).
func vectorDiff(t *testing.T, c diffCase) (ins, del int, digest string) {
	t.Helper()
	old := vectorText(t, c.Old, "")
	want := vectorText(t, c.New, old)
	r := docOf("a", old)
	h := sha256.New()
	for _, op := range r.GenerateOps(want) {
		v := "-"
		if op.Type == OpDelete {
			del++
		} else {
			ins++
			v = fmt.Sprintf("%x", []rune(op.Val)[0])
		}
		fmt.Fprintf(h, "%s %d %d %s\n", op.Type, op.ID.Clock, op.OriginLeft.Clock, v)
	}
	if r.Text() != want {
		t.Errorf("%s: the ops did not make the new text", c.Name)
	}
	return ins, del, hex.EncodeToString(h.Sum(nil))
}

func TestTextVectorsDiff(t *testing.T) {
	p, v := readTextVectors(t)
	if len(v.Diff) < textVectorsMinDiff {
		t.Fatalf("%d diff cases, the table holds at least %d", len(v.Diff), textVectorsMinDiff)
	}
	update := os.Getenv(textVectorsUpdate) != ""
	for i := range v.Diff {
		c := &v.Diff[i]
		ins, del, digest := vectorDiff(t, *c)
		if update {
			c.Ins, c.Del, c.Ops = ins, del, digest
			continue
		}
		if ins != c.Ins || del != c.Del || digest != c.Ops {
			t.Errorf("%s: %d inserts, %d deletes, ops %.12s; the table says %d, %d, %.12s", c.Name, ins, del, digest, c.Ins, c.Del, c.Ops)
		}
	}
	if update {
		writeTextVectors(t, p, v)
	}
}

func TestTextVectorsUnion(t *testing.T) {
	p, v := readTextVectors(t)
	if len(v.Union) < textVectorsMinUnion {
		t.Fatalf("%d union cases, the table holds at least %d", len(v.Union), textVectorsMinUnion)
	}
	update := os.Getenv(textVectorsUpdate) != ""
	for i := range v.Union {
		c := &v.Union[i]
		got := merge.Union(c.Shared, c.Local)
		if update {
			c.Want = got
			continue
		}
		if got != c.Want {
			t.Errorf("%s: Union(%q, %q) = %q; the table says %q", c.Name, c.Shared, c.Local, got, c.Want)
		}
	}
	if update {
		writeTextVectors(t, p, v)
	}
}

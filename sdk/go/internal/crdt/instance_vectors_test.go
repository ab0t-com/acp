package crdt

// The Go half of the shared table: testdata/crdt-instance-vectors.json holds what a client
// decides about the document at a name, and the TypeScript SDK's
// test/crdt-instance-vectors.test.mjs reads the same file. A failure here is a disagreement
// with the rule: fix the code, or change the rule in the table and in every client
// together. Pure: no network, no env, the file is only read.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type instanceVectors struct {
	InstanceGone []struct {
		Name        string     `json:"name"`
		View        []ElemWire `json:"view"`
		ViewEpoch   int        `json:"viewEpoch"`
		Pulled      []Op       `json:"pulled"`
		PulledEpoch int        `json:"pulledEpoch"`
		Sending     []Op       `json:"sending"`
		Gone        bool       `json:"gone"`
	} `json:"instanceGone"`
	LogReplaced []struct {
		Name     string     `json:"name"`
		View     []ElemWire `json:"view"`
		Ops      []Op       `json:"ops"`
		Total    int        `json:"total"`
		Cursor   int        `json:"cursor"`
		Replaced bool       `json:"replaced"`
	} `json:"logReplaced"`
}

// readInstanceVectors finds the table above this package, in the module or in the SDK.
func readInstanceVectors(t *testing.T) instanceVectors {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		b, err := os.ReadFile(filepath.Join(dir, "testdata", "crdt-instance-vectors.json"))
		if err == nil {
			var v instanceVectors
			if err := json.Unmarshal(b, &v); err != nil {
				t.Fatal(err)
			}
			return v
		}
		if m, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil && bytes.Contains(m, []byte("module github.com/ab0t-com/acp\n")) {
			t.Fatalf("crdt-instance-vectors.json not found under %s/testdata: the shared Go/TS table is missing", dir)
		}
		up := filepath.Dir(dir)
		if up == dir {
			t.Skip("testdata/crdt-instance-vectors.json not found: this copy of the package ships without the table")
		}
		dir = up
	}
}

func TestInstanceVectors(t *testing.T) {
	table := readInstanceVectors(t)
	if len(table.InstanceGone) < 28 || len(table.LogReplaced) < 9 {
		t.Fatalf("a lost table, not a passing one: %d, %d", len(table.InstanceGone), len(table.LogReplaced))
	}
	state := func(els []ElemWire) State { return State{Replica: "me", Clock: 100, Els: els} }
	for _, c := range table.InstanceGone {
		t.Run("instanceGone/"+c.Name, func(t *testing.T) {
			view := Load(state(c.View)).Snapshot() // as a replica keeps it
			if got := InstanceGoneAt(view, c.Pulled, c.Sending, c.ViewEpoch, c.PulledEpoch); got != c.Gone {
				t.Fatalf("gone: %v", got)
			}
		})
	}
	for _, c := range table.LogReplaced {
		t.Run("logReplaced/"+c.Name, func(t *testing.T) {
			if got := Load(state(c.View)).LogReplaced(c.Ops, c.Total, c.Cursor); got != c.Replaced {
				t.Fatalf("replaced: %v", got)
			}
		})
	}
}

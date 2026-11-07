package client

// D57 Q1: EventHash is the TypeScript SDK's eventHash, byte for byte, on the shared table
// (testdata/event-hash-vectors.json, hashed by the TS SDK).

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ab0t-com/acp/sdk/go/pkg/wire"
)

func TestEventHashMatchesTheSharedTable(t *testing.T) {
	// Walk up to acp/testdata, so the SDK carve-out (sdk/go/pkg/client) reads the same table.
	// Inside the acp tree a missing table fails; only a published copy with no tree above skips.
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	var b []byte
	for {
		if b, err = os.ReadFile(filepath.Join(dir, "testdata", "event-hash-vectors.json")); err == nil {
			break
		}
		if m, merr := os.ReadFile(filepath.Join(dir, "go.mod")); merr == nil && bytes.Contains(m, []byte("module github.com/ab0t-com/acp\n")) {
			t.Fatalf("testdata/event-hash-vectors.json not found under %s: the shared table is missing", dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("the shared table lives in the acp source tree (acp/testdata); not present in this copy")
		}
		dir = parent
	}
	var table struct {
		Cases []struct {
			Name  string          `json:"name"`
			Event json.RawMessage `json:"event"`
			Hash  string          `json:"hash"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(b, &table); err != nil {
		t.Fatal(err)
	}
	if len(table.Cases) < 5 {
		t.Fatalf("a lost table, not a passing one: %d cases", len(table.Cases))
	}
	for _, c := range table.Cases {
		var e wire.Event
		if err := json.Unmarshal(c.Event, &e); err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		if got := EventHash(e); got != c.Hash {
			t.Errorf("%s: %s, want %s", c.Name, got, c.Hash)
		}
	}
}

func TestJSNumberIsNumberToString(t *testing.T) {
	for f, want := range map[float64]string{
		0: "0", 1: "1", -3: "-3", 0.1: "0.1", 2.5: "2.5", 1e21: "1e+21", 1e20: "100000000000000000000",
		123456789012345680000: "123456789012345680000", 1e-7: "1e-7", 0.000001: "0.000001", 1.5e-10: "1.5e-10",
		1.7976931348623157e308: "1.7976931348623157e+308", 5e-324: "5e-324", 0.5: "0.5", 100: "100", 1234.5678: "1234.5678",
	} {
		if got := jsNumber(f); got != want {
			t.Errorf("%v: %q, want %q", f, got, want)
		}
	}
}

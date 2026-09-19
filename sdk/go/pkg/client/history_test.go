package client

// ext-33 client methods (history / manifest?version= / checkpoint) and the
// ext-34 §4.8.1 whoami bulk-disposition field. Driven against a canned mux — no
// real coordd — asserting each method hits the shipped path/verb and decodes the
// wire body. These wrap EXISTING endpoints (frozen wire); the point is that the
// Go SDK now exposes them typed instead of a caller hand-rolling the GET.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ab0t-com/acp/sdk/go/pkg/wire"
)

func cannedHistoryMux(t *testing.T) (*Client, *httptest.Server) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/whoami", func(w http.ResponseWriter, r *http.Request) {
		writeJSONT(w, 200, map[string]any{"agent": "A", "role": "writer", "bulk": "denied",
			"scope": map[string]any{"read_prefix": []string{"docs/"}}})
	})
	mux.HandleFunc("/v1/history", func(w http.ResponseWriter, r *http.Request) {
		if p := r.URL.Query().Get("path"); p != "" {
			writeJSONT(w, 200, wire.PathHistory{Path: p, Versions: []wire.PathVersion{{Version: 3}}, Oldest: 1})
			return
		}
		// Echo the limit so the test can assert it was sent.
		lim := r.URL.Query().Get("limit")
		writeJSONT(w, 200, wire.History{Versions: []wire.VersionInfo{{Version: 3, Note: "limit=" + lim}}, Oldest: 1})
	})
	mux.HandleFunc("/v1/manifest", func(w http.ResponseWriter, r *http.Request) {
		v := r.URL.Query().Get("version")
		if v == "" {
			t.Fatalf("ManifestAt must send ?version=")
		}
		writeJSONT(w, 200, wire.VersionRecord{Version: 7, Note: "v=" + v,
			Entries: map[string]wire.ManifestEntry{"docs/a.md": {Hash: "h", Size: 1}}})
	})
	mux.HandleFunc("/v1/checkpoint", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if n := r.URL.Query().Get("name"); n != "" {
				writeJSONT(w, 200, wire.Checkpoint{Name: n, Version: 5})
				return
			}
			writeJSONT(w, 200, []wire.Checkpoint{{Name: "rel", Version: 5}})
		case http.MethodPost:
			var body struct {
				Name    string  `json:"name"`
				Version *uint64 `json:"version"`
				Note    string  `json:"note"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			v := uint64(9)
			if body.Version != nil {
				v = *body.Version
			}
			writeJSONT(w, 200, wire.Checkpoint{Name: body.Name, Version: v, Note: body.Note})
		case http.MethodDelete:
			if r.URL.Query().Get("name") == "" {
				writeJSONT(w, 400, wire.ErrorResponse{Error: "name required"})
				return
			}
			writeJSONT(w, 200, map[string]any{"ok": true})
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	c, err := New(ts.URL, "tok", "A", "", true)
	if err != nil {
		t.Fatal(err)
	}
	return c, ts
}

func writeJSONT(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func TestWhoamiCarriesBulkDisposition(t *testing.T) {
	c, _ := cannedHistoryMux(t)
	g, err := c.Whoami()
	if err != nil {
		t.Fatal(err)
	}
	if g.Bulk != "denied" { // ext-34 §4.8.1: surfaced so a client can warn up front
		t.Fatalf("Grant.Bulk = %q, want denied", g.Bulk)
	}
	if g.Scope == nil || len(g.Scope.ReadPrefix) != 1 || g.Scope.ReadPrefix[0] != "docs/" {
		t.Fatalf("scope not decoded: %+v", g.Scope)
	}
}

func TestHistoryAndPathHistory(t *testing.T) {
	c, _ := cannedHistoryMux(t)
	h, err := c.History(5, 0)
	if err != nil || len(h.Versions) != 1 || h.Versions[0].Version != 3 {
		t.Fatalf("History = %+v, %v", h, err)
	}
	if h.Versions[0].Note != "limit=5" { // the limit rode the query string
		t.Fatalf("History did not send limit=5: %q", h.Versions[0].Note)
	}
	ph, err := c.PathHistory("docs/a.md", 0, 0)
	if err != nil || ph.Path != "docs/a.md" || len(ph.Versions) != 1 {
		t.Fatalf("PathHistory = %+v, %v", ph, err)
	}
}

func TestManifestAtSendsVersion(t *testing.T) {
	c, _ := cannedHistoryMux(t)
	rec, err := c.ManifestAt(4)
	if err != nil || rec.Note != "v=4" {
		t.Fatalf("ManifestAt(4) = %+v, %v (must send ?version=4)", rec, err)
	}
	if _, ok := rec.Entries["docs/a.md"]; !ok {
		t.Fatalf("record entries not decoded: %+v", rec.Entries)
	}
}

func TestCheckpointCreateListDelete(t *testing.T) {
	c, _ := cannedHistoryMux(t)
	v := uint64(2)
	cp, err := c.CreateCheckpoint("rel", &v, "first")
	if err != nil || cp.Version != 2 || cp.Note != "first" {
		t.Fatalf("CreateCheckpoint = %+v, %v", cp, err)
	}
	list, err := c.Checkpoints()
	if err != nil || len(list) != 1 || list[0].Name != "rel" {
		t.Fatalf("Checkpoints = %+v, %v", list, err)
	}
	one, err := c.Checkpoint("rel")
	if err != nil || one.Name != "rel" {
		t.Fatalf("Checkpoint(rel) = %+v, %v", one, err)
	}
	if err := c.DeleteCheckpoint("rel"); err != nil {
		t.Fatalf("DeleteCheckpoint: %v", err)
	}
}

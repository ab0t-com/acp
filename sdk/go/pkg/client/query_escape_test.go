package client

// v0.2.5: names travel in query strings escaped. A document named "notes & ideas.txt" (legal: only control bytes
// are refused) used to be read as "notes " — the write (name in the JSON body) landed on the right document, the
// read asked for another one and came back empty. Thread ids are client-chosen and have the same problem.

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestQueryNamesAreEscaped(t *testing.T) {
	var mu sync.Mutex
	got := map[string]string{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		for _, k := range []string{"doc", "id"} {
			if v := r.URL.Query().Get(k); v != "" {
				got[r.URL.Path] = v
			}
		}
		mu.Unlock()
		switch r.URL.Path {
		case "/v1/healthz":
			w.Write([]byte(`{"status":"ok","capabilities":["crdtjson"]}`))
		case "/v1/mail/thread":
			w.Write([]byte("[]"))
		default:
			w.Write([]byte(`{"doc":"x","text":"","json":{},"total":0}`))
		}
	}))
	t.Cleanup(ts.Close)
	c, err := New(ts.URL, "tok", "a", "", true)
	if err != nil {
		t.Fatal(err)
	}
	name := "#notes & ideas+v2 #1.txt"
	if _, _, err := c.CRDTText(name); err != nil {
		t.Logf("CRDTText: %v", err)
	}
	if _, _, _, err := c.CRDTJSONDoc(name); err != nil {
		t.Logf("CRDTJSONDoc: %v", err)
	}
	if _, err := c.Thread(name); err != nil {
		t.Logf("Thread: %v", err)
	}
	if _, err := c.Compact(name); err != nil { // pre-cut review F7: "#…" used to compact EVERY document
		t.Logf("Compact: %v", err)
	}
	for _, p := range []string{"/v1/crdt/doc", "/v1/crdt/json/doc", "/v1/mail/thread", "/v1/admin/compact"} {
		if got[p] != name {
			t.Errorf("%s: the server was asked for %q, want %q", p, got[p], name)
		}
	}
}

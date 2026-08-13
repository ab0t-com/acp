package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ab0t-com/acp/sdk/go/internal/crdt"
	"github.com/ab0t-com/acp/sdk/go/internal/crdtjson"
	"github.com/ab0t-com/acp/sdk/go/internal/wire"
)

// fakeServer records the last request's headers and serves canned responses, so
// we can assert the SDK's wire behavior without a real daemon.
type fakeServer struct {
	mu       sync.Mutex
	lastHdr  http.Header
	lastPath string
	blobs    map[string][]byte
}

func newFakeServer(t *testing.T) (*httptest.Server, *fakeServer) {
	f := &fakeServer{blobs: map[string][]byte{}}
	mux := http.NewServeMux()
	record := func(r *http.Request) {
		f.mu.Lock()
		f.lastHdr = r.Header.Clone()
		f.lastPath = r.URL.RequestURI()
		f.mu.Unlock()
	}
	j := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) { j(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if r.Method == http.MethodGet {
			if r.URL.Query().Get("follow") == "true" {
				w.Header().Set("Content-Type", "application/x-ndjson")
				enc := json.NewEncoder(w)
				enc.Encode(wire.Event{Seq: 1, Action: "a"})
				enc.Encode(wire.Event{Seq: 2, Action: "b"})
				return
			}
			j(w, 200, []wire.Event{{Seq: 1, Action: "a"}})
			return
		}
		var e wire.Event
		json.NewDecoder(r.Body).Decode(&e)
		e.Seq = 7
		e.Actor = r.Header.Get(wire.HeaderAgentID)
		j(w, 200, e)
	})
	mux.HandleFunc("/v1/lease/acquire", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		// simulate "held" to exercise the 409 + Current path
		j(w, http.StatusConflict, wire.ErrorResponse{Error: "resource held", Current: wire.Lease{Resource: "r", Holder: "other"}})
	})
	mux.HandleFunc("/v1/blobs", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		body, _ := io.ReadAll(r.Body)
		h := fmt.Sprintf("%064x", len(body)) // fake but stable "hash"
		f.mu.Lock()
		f.blobs[h] = body
		f.mu.Unlock()
		j(w, 200, map[string]any{"hash": h, "size": len(body)})
	})
	mux.HandleFunc("/v1/blobs/", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		h := strings.TrimPrefix(r.URL.Path, "/v1/blobs/")
		f.mu.Lock()
		b, ok := f.blobs[h]
		f.mu.Unlock()
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Write(b)
	})
	mux.HandleFunc("/v1/crdt/ops", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if r.Method == http.MethodGet {
			j(w, 200, map[string]any{"ops": []crdt.Op{}, "total": 3, "epoch": 1})
			return
		}
		j(w, 200, map[string]any{"total": 5, "epoch": 1})
	})
	mux.HandleFunc("/v1/stats", func(w http.ResponseWriter, r *http.Request) { record(r); j(w, 200, map[string]any{"events": 1.0}) })
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, f
}

func newTestClient(t *testing.T, url string) *Client {
	c, err := New(url, "tok", "agentA", "", true)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNewRequiresCertOrInsecure(t *testing.T) {
	if _, err := New("https://x", "t", "a", "", false); err == nil {
		t.Fatal("New must require --cert or --insecure")
	}
	if _, err := New("https://x", "t", "a", "", true); err != nil {
		t.Fatalf("insecure New should succeed: %v", err)
	}
}

func TestRequestHeaders(t *testing.T) {
	ts, f := newFakeServer(t)
	c := newTestClient(t, ts.URL)
	c.SetSpace("team1")
	if _, err := c.Append(wire.Event{Action: "x"}); err != nil {
		t.Fatal(err)
	}
	h := f.lastHdr
	if h.Get("Authorization") != "Bearer tok" {
		t.Fatalf("auth header: %q", h.Get("Authorization"))
	}
	if h.Get(wire.HeaderAgentID) != "agentA" {
		t.Fatalf("agent header: %q", h.Get(wire.HeaderAgentID))
	}
	if h.Get(wire.HeaderProtocol) != wire.ProtocolVersion {
		t.Fatalf("protocol header: %q", h.Get(wire.HeaderProtocol))
	}
	if h.Get(wire.HeaderSession) == "" {
		t.Fatal("session header must be set (collision detection)")
	}
	if h.Get(wire.HeaderSpace) != "team1" {
		t.Fatalf("space header: %q", h.Get(wire.HeaderSpace))
	}
}

func TestDefaultSpaceHeaderOmitted(t *testing.T) {
	ts, f := newFakeServer(t)
	c := newTestClient(t, ts.URL) // no SetSpace
	c.Append(wire.Event{Action: "x"})
	if f.lastHdr.Get(wire.HeaderSpace) != "" {
		t.Fatal("space header should be omitted when unset (daemon defaults)")
	}
}

func TestAppendDecodesResponse(t *testing.T) {
	ts, _ := newFakeServer(t)
	c := newTestClient(t, ts.URL)
	ev, err := c.Append(wire.Event{Action: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if ev.Seq != 7 || ev.Actor != "agentA" {
		t.Fatalf("decoded event wrong: %+v", ev)
	}
}

func TestAPIErrorConflict(t *testing.T) {
	ts, _ := newFakeServer(t)
	c := newTestClient(t, ts.URL)
	_, err := c.AcquireLease("r", 300)
	ae, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if !ae.Conflict() || ae.Status != http.StatusConflict {
		t.Fatalf("expected 409 conflict: %+v", ae)
	}
	if ae.Current == nil {
		t.Fatal("conflict should carry Current state")
	}
}

func TestBlobRoundTrip(t *testing.T) {
	ts, _ := newFakeServer(t)
	c := newTestClient(t, ts.URL)
	h, n, err := c.PutBlob(strings.NewReader("hello"))
	if err != nil || n != 5 {
		t.Fatalf("put: %v n=%d", err, n)
	}
	rc, err := c.GetBlob(h)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != "hello" {
		t.Fatalf("get mismatch: %q", got)
	}
}

func TestFollowStreamsEvents(t *testing.T) {
	ts, _ := newFakeServer(t)
	c := newTestClient(t, ts.URL)
	var got []uint64
	err := c.Follow(0, func(e wire.Event) error {
		got = append(got, e.Seq)
		return nil
	})
	if err != nil && err != io.EOF {
		t.Fatalf("follow: %v", err)
	}
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("streamed events wrong: %v", got)
	}
}

func TestCRDTAndStats(t *testing.T) {
	ts, _ := newFakeServer(t)
	c := newTestClient(t, ts.URL)
	total, epoch, err := c.PushCRDTOps("doc", []crdt.Op{{Type: crdt.OpInsert}}, 0)
	if err != nil || total != 5 || epoch != 1 {
		t.Fatalf("push crdt: %v total=%d epoch=%d", err, total, epoch)
	}
	_, total2, epoch2, err := c.PullCRDTOps("doc", 0)
	if err != nil || total2 != 3 || epoch2 != 1 {
		t.Fatalf("pull crdt: %v total=%d epoch=%d", err, total2, epoch2)
	}
	st, err := c.Stats()
	if err != nil || st["events"] != 1.0 {
		t.Fatalf("stats: %v %v", err, st)
	}
}

func TestHealth(t *testing.T) {
	ts, _ := newFakeServer(t)
	c := newTestClient(t, ts.URL)
	if err := c.Health(); err != nil {
		t.Fatalf("health: %v", err)
	}
}

// LogBatch probes the capability list once (cached), posts the array, and
// fails explicitly against a daemon that doesn't advertise batchevents.
func TestLogBatchCapabilityProbe(t *testing.T) {
	var mu sync.Mutex
	healthzHits, eventHits := 0, 0
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		healthzHits++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"status": "ok", "capabilities": []string{"batchevents"}})
	})
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		eventHits++
		mu.Unlock()
		var evs []wire.Event
		json.NewDecoder(r.Body).Decode(&evs)
		for i := range evs {
			evs[i].Seq = uint64(i + 1)
			evs[i].Actor = r.Header.Get(wire.HeaderAgentID)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(evs)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	c := newTestClient(t, ts.URL)
	out, err := c.LogBatch([]wire.Event{{Action: "a"}, {Action: "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].Seq != 1 || out[1].Seq != 2 || out[0].Actor != "agentA" {
		t.Fatalf("batch response wrong: %+v", out)
	}
	if _, err := c.LogBatch([]wire.Event{{Action: "c"}}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if healthzHits != 1 {
		t.Fatalf("capability probe must be cached: healthz hit %d times", healthzHits)
	}
	if eventHits != 2 {
		t.Fatalf("expected 2 batch POSTs, got %d", eventHits)
	}
}

// EventFilter.Match must mirror the daemon's semantics exactly (same matcher,
// same OR-within/AND-across combination, same "_" reservation) — it is the D8
// old-daemon degrade path.
func TestEventFilterMatch(t *testing.T) {
	ev := func(action, channel string) wire.Event { return wire.Event{Action: action, Channel: channel} }
	cases := []struct {
		f    EventFilter
		e    wire.Event
		want bool
	}{
		{EventFilter{}, ev("x", ""), true}, // empty filter matches all
		{EventFilter{Channels: []string{"build"}}, ev("x", "build"), true},
		{EventFilter{Channels: []string{"build"}}, ev("x", "deploy"), false},
		{EventFilter{Channels: []string{"b.*"}}, ev("x", "b.eu"), true},
		{EventFilter{Channels: []string{"b.*"}}, ev("x", "b"), false}, // bare parent
		{EventFilter{Channels: []string{"_"}}, ev("x", ""), true},     // unlabeled only
		{EventFilter{Channels: []string{"_"}}, ev("x", "b"), false},
		{EventFilter{Actions: []string{"ladder.*"}}, ev("ladder.up", ""), true},
		{EventFilter{Actions: []string{"ladder.*"}}, ev("ladder", ""), false},
		{EventFilter{Channels: []string{"build"}, Actions: []string{"ladder.*"}}, ev("ladder.up", "build"), true},
		{EventFilter{Channels: []string{"build"}, Actions: []string{"ladder.*"}}, ev("ladder.up", "other"), false},
		{EventFilter{Channels: []string{"build"}, Actions: []string{"ladder.*"}}, ev("task.done", "build"), false},
	}
	for i, c := range cases {
		if got := c.f.Match(c.e); got != c.want {
			t.Errorf("case %d: Match(%+v) with %+v = %v, want %v", i, c.e, c.f, got, c.want)
		}
	}
}

// D8: against an OLD daemon (no channels capability, filter params ignored,
// everything returned) the filtered read/follow DEGRADE — the client re-filters
// locally with the same matcher, so the caller sees exactly the matching
// events, never an error and never extra traffic passed through.
func TestFilteredDegradesOnOldDaemon(t *testing.T) {
	var mu sync.Mutex
	sawQuery := ""
	all := []wire.Event{
		{Seq: 1, Action: "ladder.up", Channel: "build"},
		{Seq: 2, Action: "task.done", Channel: "build"},
		{Seq: 3, Action: "ladder.gate.failed"},
	}
	mux := http.NewServeMux()
	// Old daemon shape: no capabilities field; filter params ignored.
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sawQuery = r.URL.RawQuery
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("follow") == "true" {
			enc := json.NewEncoder(w)
			for _, e := range all {
				enc.Encode(e)
			}
			return
		}
		json.NewEncoder(w).Encode(all)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	c := newTestClient(t, ts.URL)
	f := &EventFilter{Actions: []string{"ladder.*"}}
	evs, err := c.EventsFiltered(0, f)
	if err != nil {
		t.Fatalf("EventsFiltered must degrade, not fail: %v", err)
	}
	if len(evs) != 2 || evs[0].Seq != 1 || evs[1].Seq != 3 {
		t.Fatalf("local re-filter wrong: %+v", evs)
	}
	mu.Lock()
	if !strings.Contains(sawQuery, "actions=ladder.") {
		t.Fatalf("filter params must still be sent (new daemons filter server-side): %q", sawQuery)
	}
	mu.Unlock()

	var got []uint64
	err = c.FollowFiltered(0, f, func(e wire.Event) error {
		got = append(got, e.Seq)
		return nil
	})
	if err != nil && err != io.EOF {
		t.Fatalf("follow: %v", err)
	}
	if len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("filtered follow wrong: %v", got)
	}
}

// An empty filter must take the plain unfiltered path (no filter params on the
// wire, no capability probe) — the nil-filter regression at the SDK level.
func TestEmptyFilterIsUnfiltered(t *testing.T) {
	ts, f := newFakeServer(t)
	c := newTestClient(t, ts.URL)
	evs, err := c.EventsFiltered(0, nil)
	if err != nil || len(evs) != 1 {
		t.Fatalf("nil filter: %v %+v", err, evs)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.Contains(f.lastPath, "channels=") || strings.Contains(f.lastPath, "actions=") {
		t.Fatalf("nil filter must not send filter params: %s", f.lastPath)
	}
}

func TestChannelsRegistry(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/channels", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]ChannelInfo{{Name: "build", LastSeq: 5, LastAt: "2026-07-12T00:00:00Z"}})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	c := newTestClient(t, ts.URL)
	chs, err := c.Channels()
	if err != nil || len(chs) != 1 || chs[0].Name != "build" || chs[0].LastSeq != 5 {
		t.Fatalf("channels: %v %+v", err, chs)
	}
}

func TestLogBatchFailsWithoutCapability(t *testing.T) {
	var mu sync.Mutex
	eventHits := 0
	mux := http.NewServeMux()
	// Old daemon shape: healthz has no capabilities field at all.
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		eventHits++
		mu.Unlock()
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	c := newTestClient(t, ts.URL)
	_, err := c.LogBatch([]wire.Event{{Action: "a"}})
	if err == nil || !strings.Contains(err.Error(), "batchevents") {
		t.Fatalf("want explicit batchevents error, got %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if eventHits != 0 {
		t.Fatal("LogBatch must not POST (or fall back to singles) without the capability")
	}
}

// MintBlobURL probes "bloburl" once, POSTs {ttl_sec}, and resolves the
// daemon's root-relative URL against the client's base so the caller can hand
// it out verbatim.
func TestMintBlobURL(t *testing.T) {
	var mu sync.Mutex
	healthzHits, mintHits := 0, 0
	var gotBody map[string]int
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		healthzHits++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"status": "ok", "capabilities": []string{"bloburl"}})
	})
	hash := strings.Repeat("a", 64)
	mux.HandleFunc("/v1/blobs/"+hash+"/url", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		mintHits++
		mu.Unlock()
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"url":     "/v1/cap/blob/" + hash + "?e=1782000300&k=abcd1234&s=ff&sp=default",
			"expires": "2026-07-02T12:05:00Z", "hash": hash, "ttl_sec": 300,
		})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	c := newTestClient(t, ts.URL)
	bu, err := c.MintBlobURL(hash, 300)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(bu.URL, ts.URL+"/v1/cap/blob/"+hash) {
		t.Fatalf("URL must be resolved against the client base: %q", bu.URL)
	}
	if bu.Hash != hash || bu.TTLSec != 300 || bu.Expires == "" {
		t.Fatalf("mint response wrong: %+v", bu)
	}
	if _, err := c.MintBlobURL(hash, 60); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotBody["ttl_sec"] != 60 {
		t.Fatalf("ttl_sec not sent: %v", gotBody)
	}
	if healthzHits != 1 {
		t.Fatalf("capability probe must be cached: healthz hit %d times", healthzHits)
	}
	if mintHits != 2 {
		t.Fatalf("expected 2 mint POSTs, got %d", mintHits)
	}
}

// The T42 fail-explicit case: no "bloburl" capability means NO mint attempt
// and NO silent fallback — a degraded "capability URL" would be a lie (the
// only alternatives leak the bearer token or change the architecture).
func TestMintBlobURLFailsWithoutCapability(t *testing.T) {
	var mu sync.Mutex
	mintHits := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"status": "ok", "capabilities": []string{"channels"}})
	})
	mux.HandleFunc("/v1/blobs/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		mintHits++
		mu.Unlock()
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	c := newTestClient(t, ts.URL)
	_, err := c.MintBlobURL(strings.Repeat("a", 64), 300)
	if err == nil || !strings.Contains(err.Error(), "bloburl") {
		t.Fatalf("want explicit bloburl-capability error, got %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if mintHits != 0 {
		t.Fatal("MintBlobURL must not POST without the capability")
	}
}

// --- structured (JSON) docs (ext-5) ---

func TestCRDTJSONRoundTrip(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"status": "ok", "capabilities": []string{"crdtjson"}})
	})
	var gotPush map[string]any
	mux.HandleFunc("/v1/crdt/json/ops", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			json.NewDecoder(r.Body).Decode(&gotPush)
			json.NewEncoder(w).Encode(map[string]any{"doc": "d", "total": 3, "epoch": 1})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"doc": "d",
			"ops": []crdtjson.Op{{T: crdtjson.OpSet, Key: "k"}}, "total": 3, "epoch": 1})
	})
	mux.HandleFunc("/v1/crdt/json/doc", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"doc": "d",
			"json": json.RawMessage(`{"k":1}`), "total": 3, "epoch": 1})
	})
	mux.HandleFunc("/v1/crdt/json/list", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]DocInfo{{Name: "d", Ops: 3, Size: 7}})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	c := newTestClient(t, ts.URL)

	total, epoch, err := c.PushCRDTJSONOps("d",
		[]crdtjson.Op{{T: crdtjson.OpSet, Path: []any{"k"}, Value: json.RawMessage(`1`)}}, 0, true)
	if err != nil || total != 3 || epoch != 1 {
		t.Fatalf("push: %d %d %v", total, epoch, err)
	}
	if gotPush["epoch"] != float64(0) || gotPush["create_intermediate"] != true || gotPush["doc"] != "d" {
		t.Fatalf("push body wrong: %+v", gotPush)
	}
	ops, total, epoch, err := c.PullCRDTJSONOps("d", 2)
	if err != nil || len(ops) != 1 || ops[0].Key != "k" || total != 3 || epoch != 1 {
		t.Fatalf("pull: %+v %d %d %v", ops, total, epoch, err)
	}
	val, total, epoch, err := c.CRDTJSONDoc("d")
	if err != nil || string(val) != `{"k":1}` || total != 3 || epoch != 1 {
		t.Fatalf("doc: %s %d %d %v", val, total, epoch, err)
	}
	docs, err := c.CRDTJSONList()
	if err != nil || len(docs) != 1 || docs[0].Name != "d" {
		t.Fatalf("list: %+v %v", docs, err)
	}
}

func TestCRDTJSONFailsWithoutCapability(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	mux := http.NewServeMux()
	// Old daemon shape: no crdtjson in the capability list (it would 404 the
	// paths); every SDK method must fail EXPLICITLY before touching the wire.
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"status": "ok", "capabilities": []string{"channels"}})
	})
	mux.HandleFunc("/v1/crdt/json/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		http.NotFound(w, r)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	c := newTestClient(t, ts.URL)

	if _, _, err := c.PushCRDTJSONOps("d", nil, -1, false); err == nil || !strings.Contains(err.Error(), "crdtjson") {
		t.Fatalf("push: want explicit crdtjson error, got %v", err)
	}
	if _, _, _, err := c.PullCRDTJSONOps("d", 0); err == nil || !strings.Contains(err.Error(), "crdtjson") {
		t.Fatalf("pull: want explicit crdtjson error, got %v", err)
	}
	if _, _, _, err := c.CRDTJSONDoc("d"); err == nil || !strings.Contains(err.Error(), "crdtjson") {
		t.Fatalf("doc: want explicit crdtjson error, got %v", err)
	}
	if _, err := c.CRDTJSONList(); err == nil || !strings.Contains(err.Error(), "crdtjson") {
		t.Fatalf("list: want explicit crdtjson error, got %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 0 {
		t.Fatalf("SDK hit /v1/crdt/json/* %d times without the capability", hits)
	}
}

// A malicious or compromised coordd could answer a control-plane call with a
// multi-gigabyte body: the SDK buffers server responses with io.ReadAll, which
// is unbounded, so an oversized body would OOM the agent process. The read is
// now capped; this test proves the cap holds.

// endlessReader never returns EOF — the hostile-server shape, reduced to the
// thing actually under test. Driving this through a real HTTP server instead
// cost ~430 MB of peak RSS under -race for no extra coverage; the bound lives in
// readBounded, so that is what to point the test at.
type endlessReader struct{ n int64 }

func (e *endlessReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'A'
	}
	e.n += int64(len(p))
	return len(p), nil
}

// TestReadBoundedStopsAtCap: an endless body must yield exactly the cap and
// RETURN. Without the limit this call never terminates and the process grows
// until the kernel kills it.
func TestReadBoundedStopsAtCap(t *testing.T) {
	defer func(orig int64) { controlResponseCap = orig }(controlResponseCap)
	controlResponseCap = 1 << 20 // 1 MiB keeps the test instant; the logic is cap-independent

	src := &endlessReader{}
	done := make(chan []byte, 1)
	go func() { done <- readBounded(src) }()

	select {
	case got := <-done:
		if int64(len(got)) != controlResponseCap {
			t.Fatalf("readBounded returned %d bytes, want exactly the cap %d", len(got), controlResponseCap)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("readBounded never returned on an endless body — the read is unbounded")
	}

	// The SHIPPED cap is the 64 MiB constant, not whatever a test set. Guard it
	// so shrinking the cap for testability can never silently ship.
	if maxControlResponse != 64<<20 {
		t.Fatalf("shipped control-response cap changed to %d — intentional?", maxControlResponse)
	}
}

// TestControlResponseIsBounded proves the WIRING: a real control-plane call
// through do() actually goes via readBounded, so an oversized reply is truncated
// rather than buffered whole. Kept deliberately small — the unbounded-read
// property itself is proven above, without an HTTP server.
func TestControlResponseIsBounded(t *testing.T) {
	defer func(orig int64) { controlResponseCap = orig }(controlResponseCap)
	controlResponseCap = 64 << 10 // 64 KiB

	const serve = 1 << 20 // 1 MiB — 16x the cap, still trivial to move
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(bytes.Repeat([]byte("A"), serve))
	}))
	defer ts.Close()

	c, err := New(ts.URL, "tok", "A", "", true)
	if err != nil {
		t.Fatal(err)
	}
	// The body is not valid JSON, so this must fail to decode — the point is that
	// it FAILS rather than buffering the whole oversized reply.
	if _, err := c.Stats(); err == nil {
		t.Fatal("a garbage oversized response should not decode cleanly")
	}
}

// readBounded must not damage ordinary responses: anything under the cap is
// returned byte-for-byte, so the bound is invisible to every real deployment.
func TestReadBoundedPassesNormalPayloads(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 2<<20) // 2 MiB — a big but lawful response
	if got := readBounded(bytes.NewReader(payload)); !bytes.Equal(got, payload) {
		t.Fatalf("a lawful %d-byte response was altered (got %d bytes)", len(payload), len(got))
	}
	if got := readBounded(bytes.NewReader(nil)); len(got) != 0 {
		t.Fatalf("empty body should read as empty, got %d bytes", len(got))
	}
}

// TestUniformAPIErrorTaxonomy pins the SDK-suite interface contract (the
// acp-sdk-suite ticket, INTERFACE.md §4): EVERY server-reported failure — the
// blob put/get paths and the stream-open paths included, which used to return
// hand-rolled fmt.Errorf values — is a *APIError carrying the status, the
// server's message, and (when present) Current, so errors.As + the
// Conflict/Locked/OverQuota predicates work uniformly across all methods.
func TestUniformAPIErrorTaxonomy(t *testing.T) {
	mux := http.NewServeMux()
	fail := func(code int, body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			io.WriteString(w, body)
		}
	}
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"status":"ok","capabilities":["awareness"]}`)
	})
	mux.HandleFunc("/v1/blobs", fail(507, `{"error":"space blob quota exceeded"}`))
	mux.HandleFunc("/v1/blobs/", fail(404, `{"error":"no such blob"}`))
	mux.HandleFunc("/v1/events", fail(403, `{"error":"read outside grant scope"}`))
	mux.HandleFunc("/v1/awareness", fail(403, `{"error":"awareness barred for this grant"}`))
	ts := httptest.NewServer(mux)
	defer ts.Close()
	c := newTestClient(t, ts.URL)

	assertAPI := func(name string, err error, status int, wantMsg string) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s: expected an error", name)
		}
		ae, ok := err.(*APIError)
		if !ok {
			t.Fatalf("%s: got %T (%v), want *APIError — the taxonomy must be uniform", name, err, err)
		}
		if ae.Status != status {
			t.Fatalf("%s: status=%d want %d", name, ae.Status, status)
		}
		if !strings.Contains(ae.Message, wantMsg) {
			t.Fatalf("%s: message %q missing %q", name, ae.Message, wantMsg)
		}
	}

	_, _, err := c.PutBlob(strings.NewReader("x"))
	assertAPI("PutBlob", err, 507, "quota")
	if !err.(*APIError).OverQuota() {
		t.Fatal("PutBlob 507 must satisfy OverQuota()")
	}
	_, err = c.GetBlob("deadbeef")
	assertAPI("GetBlob", err, 404, "no such blob")
	assertAPI("Follow", c.Follow(0, func(wire.Event) error { return nil }), 403, "scope")
	f := &EventFilter{Actions: []string{"a.*"}}
	assertAPI("FollowFiltered", c.FollowFiltered(0, f, func(wire.Event) error { return nil }), 403, "scope")
	assertAPI("FollowAwareness", c.FollowAwareness(func(wire.AwarenessDelta) error { return nil }), 403, "barred")

	// Health: a non-200 is an *APIError too.
	bad := httptest.NewServer(http.HandlerFunc(fail(503, `{"error":"draining"}`)))
	defer bad.Close()
	cb := newTestClient(t, bad.URL)
	assertAPI("Health", cb.Health(), 503, "draining")

	// A non-JSON error body degrades to the raw text as the message.
	raw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
		io.WriteString(w, "bad gateway")
	}))
	defer raw.Close()
	cr := newTestClient(t, raw.URL)
	err = cr.Ack("m1")
	assertAPI("non-JSON body", err, 502, "bad gateway")
}

// TestAPIErrorPredicateMapping pins the code→predicate table itself: each
// predicate is true for EXACTLY its one status — a regression here would make
// retry logic misclassify errors (e.g. blind-retrying a persistent 507).
func TestAPIErrorPredicateMapping(t *testing.T) {
	cases := []struct {
		status                      int
		conflict, locked, overQuota bool
	}{
		{401, false, false, false},
		{403, false, false, false},
		{409, true, false, false},
		{423, false, true, false},
		{429, false, false, false},
		{500, false, false, false},
		{507, false, false, true},
	}
	for _, c := range cases {
		e := &APIError{Status: c.status, Message: "m"}
		if e.Conflict() != c.conflict || e.Locked() != c.locked || e.OverQuota() != c.overQuota {
			t.Fatalf("status %d: predicates conflict=%v locked=%v overQuota=%v, want %v/%v/%v",
				c.status, e.Conflict(), e.Locked(), e.OverQuota(), c.conflict, c.locked, c.overQuota)
		}
		want := fmt.Sprintf("acp %d: m", c.status)
		if e.Error() != want {
			t.Fatalf("status %d: Error()=%q want %q (the cross-SDK message format)", c.status, e.Error(), want)
		}
	}
}

// TestAPIErrorCurrentAndRawPreserved: apiErrorFrom must carry the 409 body's
// "current" member (the no-second-round-trip reconciliation contract) AND the
// raw bytes; a JSON body without an "error" field degrades to the raw text.
func TestAPIErrorCurrentAndRawPreserved(t *testing.T) {
	e := apiErrorFrom(409, []byte(`{"error":"conflict","current":{"version":59}}`))
	if e.Status != 409 || e.Message != "conflict" {
		t.Fatalf("basic parse: %+v", e)
	}
	cur, ok := e.Current.(map[string]any)
	if !ok || cur["version"] != float64(59) {
		t.Fatalf("current not preserved: %#v", e.Current)
	}
	if string(e.Raw) != `{"error":"conflict","current":{"version":59}}` {
		t.Fatalf("raw not preserved: %q", e.Raw)
	}
	// JSON body WITHOUT an "error" field: message falls back to the raw text.
	e = apiErrorFrom(502, []byte(`{"detail":"upstream sad"}`))
	if e.Message != `{"detail":"upstream sad"}` {
		t.Fatalf("no-error-field fallback: %q", e.Message)
	}
	// Whitespace-padded plain text is trimmed.
	e = apiErrorFrom(503, []byte("  draining \n"))
	if e.Message != "draining" {
		t.Fatalf("plain-text trim: %q", e.Message)
	}
	// Empty body: empty message, but status still speaks.
	e = apiErrorFrom(404, nil)
	if e.Status != 404 || e.Message != "" {
		t.Fatalf("empty body: %+v", e)
	}
}

// TestUniformAPIErrorOnPagedAndCapabilityPaths: doPaged (the pagination walk)
// and a capability-gated method with the capability PRESENT must surface
// server failures as *APIError too — the fix must hold on every transport
// path, not just do().
func TestUniformAPIErrorOnPagedAndCapabilityPaths(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true,"capabilities":["bloburl","crdtjson"]}`)
	})
	mux.HandleFunc("/v1/crdt/list", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		io.WriteString(w, `{"error":"rate limited"}`)
	})
	mux.HandleFunc("/v1/blobs/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		io.WriteString(w, `{"error":"blob not in manifest"}`)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	c := newTestClient(t, ts.URL)

	_, err := c.CRDTList() // doPaged path
	ae, ok := err.(*APIError)
	if !ok || ae.Status != 429 {
		t.Fatalf("doPaged: got %T (%v), want *APIError 429", err, err)
	}
	_, err = c.MintBlobURL("deadbeef", 60) // capability present -> server error path
	ae, ok = err.(*APIError)
	if !ok || ae.Status != 403 || !strings.Contains(ae.Message, "manifest") {
		t.Fatalf("MintBlobURL: got %T (%v), want *APIError 403", err, err)
	}
}

// TestFollowAwarenessRedirectHopKeepsAuthAndTaxonomy: the cluster-follower 307
// is re-requested MANUALLY with the full header set (Go's http.Client strips
// Authorization on cross-host redirects), and a failure AFTER the hop is still
// an *APIError. This pins the fix's interaction with the redirect path.
func TestFollowAwarenessRedirectHopKeepsAuthAndTaxonomy(t *testing.T) {
	var hopAuth, hopAgent string
	leader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hopAuth = r.Header.Get("Authorization")
		hopAgent = r.Header.Get("X-ACP-Agent")
		w.WriteHeader(403)
		io.WriteString(w, `{"error":"awareness barred"}`)
	}))
	defer leader.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true,"capabilities":["awareness"]}`)
	})
	mux.HandleFunc("/v1/awareness", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", leader.URL+"/v1/awareness?follow=true")
		w.WriteHeader(http.StatusTemporaryRedirect)
	})
	follower := httptest.NewServer(mux)
	defer follower.Close()

	c := newTestClient(t, follower.URL)
	err := c.FollowAwareness(func(wire.AwarenessDelta) error { return nil })
	ae, ok := err.(*APIError)
	if !ok || ae.Status != 403 || !strings.Contains(ae.Message, "barred") {
		t.Fatalf("post-hop failure: got %T (%v), want *APIError 403", err, err)
	}
	if hopAuth != "Bearer tok" {
		t.Fatalf("Authorization not re-applied on the manual hop: %q", hopAuth)
	}
	if hopAgent == "" {
		t.Fatalf("X-ACP-Agent not re-applied on the manual hop")
	}
}

// TestErrorsAsWorksOnFixedPaths: the documented errors.As pattern must succeed
// on the previously-plain-error paths (the whole point of the fix).
func TestErrorsAsWorksOnFixedPaths(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(507)
		io.WriteString(w, `{"error":"space blob quota exceeded"}`)
	}))
	defer ts.Close()
	c := newTestClient(t, ts.URL)
	_, _, err := c.PutBlob(strings.NewReader("x"))
	var ae *APIError
	if !errorsAs(err, &ae) || !ae.OverQuota() {
		t.Fatalf("errors.As must find *APIError with OverQuota on PutBlob: %T %v", err, err)
	}
}

// errorsAs avoids importing errors in a file that may not otherwise use it.
func errorsAs(err error, target **APIError) bool {
	for err != nil {
		if ae, ok := err.(*APIError); ok {
			*target = ae
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

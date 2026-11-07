package client

// G-WS3c (D38, spec block acp1-7-reads-never-create): the typed refusal for a workspace
// that does not exist, and what it means to a caller about to write. FAIL-SAFE: httptest
// on 127.0.0.1 only.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ab0t-com/acp/sdk/go/pkg/wire"
)

const spaceGoneBody = `{"error":"no such workspace \"x\" on this server (acp spaces lists yours; the first write to a new name creates it)","code":"space_not_found"}`

func spaceGone(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(404)
	w.Write([]byte(spaceGoneBody))
}

func answer(code int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		w.Write([]byte(body))
	}
}

func clientOf(t *testing.T, h http.Handler) *Client {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	c, err := New(ts.URL, "tok", "alice", "", true)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSpaceNotFoundIsTypedByItsCode(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"the refusal", 404, spaceGoneBody, true},
		{"a missing blob keeps its meaning", 404, `{"error":"blob not found"}`, false},
		{"another code", 404, `{"error":"x","code":"doc_not_found"}`, false},
		{"not json", 404, `no such workspace`, false},
		{"the code on another status", 403, `{"error":"x","code":"space_not_found"}`, false},
	} {
		e := apiErrorFrom(tc.status, []byte(tc.body))
		if e.SpaceNotFound() != tc.want || IsSpaceNotFound(e) != tc.want || IsSpaceNotFound(fmt.Errorf("read: %w", e)) != tc.want {
			t.Fatalf("%s: got %v", tc.name, e.SpaceNotFound())
		}
	}
	if IsSpaceNotFound(nil) || IsSpaceNotFound(errors.New(spaceGoneBody)) {
		t.Fatal("only a typed refusal counts")
	}
}

func TestSpaceNotFoundReadsAsTheServersSentence(t *testing.T) {
	e := apiErrorFrom(404, []byte(spaceGoneBody))
	if got := e.Error(); !strings.HasPrefix(got, `no such workspace "x" on this server`) {
		t.Fatalf("%q", got)
	}
	if got := apiErrorFrom(404, []byte(`{"error":"blob not found"}`)).Error(); got != "acp 404: blob not found" {
		t.Fatalf("every other error keeps its form: %q", got)
	}
}

func TestManifestForWrite(t *testing.T) {
	m, err := clientOf(t, http.HandlerFunc(spaceGone)).ManifestForWrite()
	if err != nil || m.Version != 0 || m.Entries == nil || len(m.Entries) != 0 {
		t.Fatalf("a new space is empty at version 0: %+v %v", m, err)
	}
	m, err = clientOf(t, answer(200, `{"version":7,"entries":{"a":{"hash":"h","size":1}}}`)).ManifestForWrite()
	if err != nil || m.Version != 7 || len(m.Entries) != 1 {
		t.Fatalf("an existing space is itself: %+v %v", m, err)
	}
	for _, code := range []int{403, 404, 500} { // a failure says nothing about what is there
		if _, err := clientOf(t, answer(code, `{"error":"no"}`)).ManifestForWrite(); err == nil {
			t.Fatalf("%d must stay an error", code)
		}
	}
	if _, err := clientOf(t, http.HandlerFunc(spaceGone)).Manifest(); !IsSpaceNotFound(err) {
		t.Fatalf("a reader is told: %v", err)
	}
}

func TestPullCRDTOpsForWrite(t *testing.T) {
	ops, total, epoch, err := clientOf(t, http.HandlerFunc(spaceGone)).PullCRDTOpsForWrite("d", 0)
	if err != nil || len(ops) != 0 || total != 0 || epoch != 0 {
		t.Fatalf("%v %d %d %v", ops, total, epoch, err)
	}
	if _, _, _, err := clientOf(t, answer(500, `{"error":"no"}`)).PullCRDTOpsForWrite("d", 0); err == nil {
		t.Fatal("a failure stays an error")
	}
	if _, _, _, err := clientOf(t, http.HandlerFunc(spaceGone)).PullCRDTOps("d", 0); !IsSpaceNotFound(err) {
		t.Fatalf("a reader is told: %v", err)
	}
}

func TestMissingBlobsOfANewSpaceIsAllOfThem(t *testing.T) {
	in := []string{"a", "b"}
	got, err := clientOf(t, http.HandlerFunc(spaceGone)).MissingBlobs(in)
	if err != nil || len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("%v %v", got, err)
	}
	if got[0] = "z"; in[0] != "a" {
		t.Fatal("the answer must not alias the caller's slice")
	}
	if _, err := clientOf(t, answer(404, `{"error":"no route"}`)).MissingBlobs(in); err == nil {
		t.Fatal("a bare 404 says nothing: an error, not all-missing")
	}
}

// D28 stays closed: only the typed refusal says what the mode is, and only to a writer.
func TestFSModeForWriteFailsClosed(t *testing.T) {
	daemon := func(config http.HandlerFunc, caps string) *Client {
		mux := http.NewServeMux()
		mux.HandleFunc("/v1/healthz", answer(200, `{"status":"ok","capabilities":[`+caps+`]}`))
		mux.HandleFunc("/v1/space/config", config)
		return clientOf(t, mux)
	}
	const fsmode = `"fsmode"`
	if m, err := daemon(spaceGone, fsmode).FSModeForWrite(); err != nil || m != "cas" {
		t.Fatalf("%q %v", m, err)
	}
	if m, err := daemon(answer(200, `{"fs_mode":"realtime"}`), fsmode).FSModeForWrite(); err != nil || m != "realtime" {
		t.Fatalf("%q %v", m, err)
	}
	if _, err := daemon(spaceGone, ``).FSModeForWrite(); !errors.Is(err, ErrNoFSMode) {
		t.Fatalf("too old stays typed: %v", err)
	}
	for _, code := range []int{401, 403, 404, 429, 500, 503} {
		if _, err := daemon(answer(code, `{"error":"no"}`), fsmode).FSModeForWrite(); err == nil || errors.Is(err, ErrNoFSMode) {
			t.Fatalf("%d must stay an unknown mode: %v", code, err)
		}
	}
	if _, err := daemon(spaceGone, fsmode).FSMode(); !IsSpaceNotFound(err) {
		t.Fatalf("a reader is told: %v", err)
	}
}

// inbox --wait on a name that does not exist answers at once, whatever the timeout.
func TestWaitForMailAnswersAtOnceOnAnUnknownSpace(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/whoami", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"agent": "alice", "role": "writer"})
	})
	mux.HandleFunc("/", spaceGone)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := clientOf(t, mux).WaitForMail(ctx, "")
		done <- err
	}()
	select {
	case err := <-done:
		if !IsSpaceNotFound(err) {
			t.Fatalf("%v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("it waited on a name that does not exist")
	}
}

// A long-lived subscription outlives the workspace's birth: it opens before the first
// write and delivers from the first event. FollowForever says so through OnRetry.
func TestFollowForeverKeepsTryingUntilTheWorkspaceExists(t *testing.T) {
	var opens atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		if opens.Add(1) < 3 {
			spaceGone(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Write([]byte(`{"seq":1,"action":"note","entity":"e"}` + "\n"))
	})
	got := make(chan uint64, 1)
	var retries atomic.Int64
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := clientOf(t, mux).FollowForever(1, func(e wire.Event) error {
		got <- e.Seq
		cancel()
		return nil
	}, &FollowForeverOptions{Context: ctx, MinBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond,
		OnRetry: func(err error, attempt int, next uint64) {
			if !IsSpaceNotFound(err) {
				t.Errorf("%v", err)
			}
			retries.Add(1)
		}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-got:
		if s != 1 || retries.Load() != 2 {
			t.Fatalf("seq %d after %d retries", s, retries.Load())
		}
	default:
		t.Fatal("no event")
	}
}

// The shared stream: a subscriber that starts before the first write gets that write and
// hears no error. The workspace's first event is seq 1, so nothing is missed.
func TestSubscribeBeforeTheWorkspaceExists(t *testing.T) {
	var made atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/stats", func(w http.ResponseWriter, r *http.Request) {
		if !made.Load() {
			spaceGone(w, r)
			return
		}
		w.Write([]byte(`{"events":1}`))
	})
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		if !made.Load() {
			spaceGone(w, r)
			return
		}
		if r.URL.Query().Get("from") != "1" {
			t.Errorf("the stream must start at the first event: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Write([]byte(`{"seq":1,"action":"note","entity":"e"}` + "\n"))
	})
	got := make(chan uint64, 1)
	errs := make(chan error, 8)
	unsub := clientOf(t, mux).Subscribe(func(e wire.Event) error {
		select {
		case got <- e.Seq:
		default:
		}
		return nil
	}, SubscribeOptions{OnError: func(err error) { errs <- err }})
	defer unsub()
	time.Sleep(100 * time.Millisecond) // it has met the 404 by now
	made.Store(true)
	select {
	case s := <-got:
		if s != 1 {
			t.Fatalf("seq %d", s)
		}
	case err := <-errs:
		t.Fatalf("a new workspace is not an error: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("the subscriber never got the first event")
	}
}

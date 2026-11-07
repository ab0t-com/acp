package client

// Text that is not valid UTF-8 is refused BEFORE anything is sent —
// encoding/json would silently send U+FFFD in its place. FAIL-SAFE: httptest on
// 127.0.0.1 only.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ab0t-com/acp/sdk/go/internal/crdtjson"
	"github.com/ab0t-com/acp/sdk/go/internal/wire"
)

// writeCounter is a daemon stand-in that answers every request and counts the ones
// that carry a body (anything but GET/HEAD).
func writeCounter(t *testing.T) (*httptest.Server, *int64) {
	var writes int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			atomic.AddInt64(&writes, 1)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/healthz" {
			w.Write([]byte(`{"protocol":"acp/1","capabilities":["awareness","batchevents","crdtjson","crdtjson-move","invites"]}`))
			return
		}
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(ts.Close)
	return ts, &writes
}

func wantInvalidText(t *testing.T, err error, field, label string) {
	t.Helper()
	var ite *InvalidTextError
	if !errors.Is(err, ErrInvalidText) || !errors.As(err, &ite) {
		t.Fatalf("%s: want an *InvalidTextError, got %v", label, err)
	}
	if !strings.Contains(ite.Field, field) {
		t.Fatalf("%s: the error must name the field %q, got %q", label, field, ite.Field)
	}
	if !strings.Contains(err.Error(), "nothing was sent") {
		t.Fatalf("%s: the error must say nothing was sent: %v", label, err)
	}
}

func TestInvalidUTF8TextIsRefusedBeforeSending(t *testing.T) {
	ts, writes := writeCounter(t)
	c := newTestClient(t, ts.URL)
	bad := "caf\xe9" // Latin-1 é: not UTF-8

	_, err := c.Send(wire.Message{To: "bob", Type: "inform", Body: bad})
	wantInvalidText(t, err, "body.body", "a mail body")
	_, err = c.Send(wire.Message{To: "bob", Type: "inform", Subject: "s\xff", Body: "ok"})
	wantInvalidText(t, err, "body.subject", "a mail subject")
	_, err = c.Append(wire.Event{Action: "note.add", Context: map[string]any{"note": bad}})
	wantInvalidText(t, err, "body.context.note", "an event note")
	_, err = c.Append(wire.Event{Action: "note.add", Context: map[string]any{"k\xff": "v"}})
	wantInvalidText(t, err, "(a key)", "a map key")
	_, err = c.LogBatch([]wire.Event{{Action: "a"}, {Action: "b", Entity: bad}})
	wantInvalidText(t, err, "[1].entity", "the second event of a batch")
	_, _, err = c.PushCRDTJSONOps("todo", []crdtjson.Op{{T: crdtjson.OpSet, Path: []any{"title"}, Value: json.RawMessage(`"` + bad + `"`)}}, -1, true)
	wantInvalidText(t, err, "body", "a JSON document value with bad bytes")
	_, _, err = c.PushCRDTJSONOps("todo", []crdtjson.Op{{T: crdtjson.OpSet, Path: []any{"title"}, Value: json.RawMessage(`"a\ud800b"`)}}, -1, true)
	wantInvalidText(t, err, "body", "a JSON document value with a lone surrogate escape")
	_, _, err = c.PushCRDTJSONOps("todo", []crdtjson.Op{{T: crdtjson.OpSet, Path: []any{bad}, Value: json.RawMessage(`1`)}}, -1, true)
	wantInvalidText(t, err, "body", "a JSON document path segment")
	_, err = c.SetAwareness(map[string]any{"status": bad}, 0, "")
	wantInvalidText(t, err, "body.status", "a presence state")
	if n := atomic.LoadInt64(writes); n != 0 {
		t.Fatalf("%d request(s) were sent with text that is not UTF-8", n)
	}

	// Valid text at the edges is sent unchanged.
	for _, ok := range []string{"café ☕ 😀", "\U0010ffff", "\ufffd is a real character", "tab\tnul\x00"} {
		if _, err := c.Send(wire.Message{To: "bob", Type: "inform", Body: ok}); err != nil {
			t.Fatalf("valid text %q refused: %v", ok, err)
		}
	}
	if _, _, err := c.PushCRDTJSONOps("todo", []crdtjson.Op{{T: crdtjson.OpSet, Path: []any{"title"}, Value: json.RawMessage(`"\ud83d\ude00 pair"`)}}, -1, true); err != nil {
		t.Fatalf("an escaped surrogate PAIR is valid JSON text: %v", err)
	}
	if n := atomic.LoadInt64(writes); n != 5 {
		t.Fatalf("the valid requests must be sent: %d", n)
	}
}

func TestJSONEscapesWellFormed(t *testing.T) {
	for _, c := range []struct {
		raw string
		ok  bool
	}{
		{`"plain"`, true},
		{`"\ud83d\ude00"`, true},  // a pair
		{`"\uD83D\uDE00"`, true},  // upper-case hex
		{`"\u00e9\n\"\\"`, true},  // ordinary escapes
		{`"\\ud800"`, true},       // an escaped backslash, then text "ud800"
		{`{"\ud800":1}`, false},   // a lone high surrogate in a key
		{`"\udc00"`, false},       // a lone low surrogate
		{`"\ud800x"`, false},      // high, then not an escape
		{`"\ud800\u0041"`, false}, // high, then not a low surrogate
		{`"\ud800`, false},        // cut short after a high surrogate
		{`["\ude00\ud83d"]`, false},
		{`\ud800`, true}, // outside a string: not text (the decoder refuses the JSON)
	} {
		if got := jsonEscapesWellFormed([]byte(c.raw)); got != c.ok {
			t.Fatalf("jsonEscapesWellFormed(%s) = %v, want %v", c.raw, got, c.ok)
		}
	}
}

// A request body that cannot be encoded is an error, never an empty request (the
// marshal error used to be dropped and the body sent empty).
func TestUnencodableBodyIsNeverSent(t *testing.T) {
	ts, writes := writeCounter(t)
	c := newTestClient(t, ts.URL)
	if err := c.do("POST", "/v1/anything", map[string]any{"bad": make(chan int)}, nil); err == nil {
		t.Fatal("an unencodable body must be an error")
	}
	if n := atomic.LoadInt64(writes); n != 0 {
		t.Fatalf("%d request(s) were sent", n)
	}
}

// Only "the daemon answered and has no fsmode capability" is typed as too old.
func TestFSModeTooOldIsTyped(t *testing.T) {
	serve := func(status int, body string) *Client {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			w.Write([]byte(body))
		}))
		t.Cleanup(ts.Close)
		return newTestClient(t, ts.URL)
	}
	if _, err := serve(200, `{"capabilities":[]}`).FSMode(); !errors.Is(err, ErrNoFSMode) {
		t.Fatalf("no capability must be ErrNoFSMode: %v", err)
	}
	if _, err := serve(503, ``).FSMode(); err == nil || errors.Is(err, ErrNoFSMode) {
		t.Fatalf("a failed probe says nothing about the mode: %v", err)
	}
	if _, err := serve(200, `{`).FSMode(); err == nil || errors.Is(err, ErrNoFSMode) {
		t.Fatalf("a garbled probe says nothing about the mode: %v", err)
	}
}

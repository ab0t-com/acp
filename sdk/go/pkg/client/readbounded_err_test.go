package client

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// G0.8 / F-11 (tickets/acp-data-loss-safety-20260911): readBounded used to DROP
// the body read error, so a connection dying mid-body surfaced only because a
// truncated JSON payload happened not to parse. The error is now returned and
// every buffered control call fails loudly on it.

type failingReader struct{ n int }

func (f *failingReader) Read(p []byte) (int, error) {
	if f.n > 0 {
		f.n--
		return copy(p, `{"seq":1,"action":"a"`), nil
	}
	return 0, errors.New("connection reset mid-body")
}

func TestReadBoundedSurfacesReadError(t *testing.T) {
	data, err := readBounded(&failingReader{n: 1})
	if err == nil {
		t.Fatal("readBounded must return the body read error")
	}
	if !strings.Contains(err.Error(), "connection reset mid-body") {
		t.Fatalf("read error not wrapped: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("the partial bytes read before the error are still returned")
	}
	// The best-effort form (error bodies) keeps the partial message.
	if got := readBoundedBestEffort(&failingReader{n: 1}); len(got) == 0 {
		t.Fatal("best-effort read should keep the partial body")
	}
}

// TestControlCallFailsOnTruncatedBody: a 200 whose body is cut short (declared
// Content-Length longer than what arrives) must be an error from do(), never a
// silently-accepted partial response.
func TestControlCallFailsOnTruncatedBody(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/manifest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "200")
		io.WriteString(w, `{"version":7,"entries":{}}`) // 26 bytes, then the handler returns
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	c := newTestClient(t, ts.URL)
	if _, err := c.Manifest(); err == nil {
		t.Fatal("a truncated manifest body must fail the call")
	}
}

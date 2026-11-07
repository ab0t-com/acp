package client

// D54 (R-C23): every one-shot call is bounded by default, a stream never is. FAIL-SAFE:
// httptest servers on 127.0.0.1 (clienttest.Stall).

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ab0t-com/acp/sdk/go/pkg/client/clienttest"
	"github.com/ab0t-com/acp/sdk/go/pkg/wire"
)

func TestDefaultTimeLimitsAreOn(t *testing.T) {
	c, err := New("http://127.0.0.1:1", "t", "a", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if c.limits.headers != DefaultHeaderWait || c.limits.stall != DefaultBodyStall || DefaultHeaderWait != 60*time.Second || DefaultBodyStall != 30*time.Second {
		t.Fatalf("defaults %+v", c.limits)
	}
	if c.WithSpace("s").limits != c.limits {
		t.Fatal("a copy of the client lost its limits")
	}
}

func TestASilentServerEndsTheCall(t *testing.T) {
	for _, body := range []bool{false, true} {
		s := clienttest.Stall(t, body)
		c, err := New(s.URL, "t", "a", "", true)
		if err != nil {
			t.Fatal(err)
		}
		c.SetTimeLimits(100*time.Millisecond, 100*time.Millisecond)
		done := make(chan error, 1)
		go func() { _, err := c.Manifest(); done <- err }()
		select {
		case err := <-done:
			var tl *TimeLimitError
			if !errors.As(err, &tl) {
				t.Fatalf("body stall %v: want a TimeLimitError, got %v", body, err)
			}
		case <-time.After(30 * time.Second):
			t.Fatalf("body stall %v: the call was still waiting", body)
		}
	}
}

// A stream waits by design: the limits never end it.
func TestAStreamIsNotBound(t *testing.T) {
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(ts.Close)
	t.Cleanup(func() { close(release) })
	c, err := New(ts.URL, "t", "a", "", true)
	if err != nil {
		t.Fatal(err)
	}
	c.SetTimeLimits(50*time.Millisecond, 50*time.Millisecond)
	done := make(chan error, 1)
	go func() { done <- c.Follow(0, func(wire.Event) error { return nil }) }()
	select {
	case err := <-done:
		t.Fatalf("a quiet stream was ended: %v", err)
	case <-time.After(500 * time.Millisecond): // ten limits: a stretched sleep only makes this weaker
	}
	_ = io.EOF
}

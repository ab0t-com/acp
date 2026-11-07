package client

// G-503c: the one retry rule for a 503 (acp-1 §6.3.1). Fail-safe: httptest only.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ab0t-com/acp/sdk/go/pkg/wire"
)

// stub503 answers the first `fail` requests with a 503 and then 200 {}.
type stub503 struct {
	fail       int32
	retryAfter string // "" = no header
	body       string
	calls      atomic.Int32
	bodies     chan string
}

func (s *stub503) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n := s.calls.Add(1)
	if s.bodies != nil {
		b, _ := io.ReadAll(r.Body)
		s.bodies <- string(b)
	}
	if n <= s.fail {
		if s.retryAfter != "" {
			w.Header().Set("Retry-After", s.retryAfter)
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, s.body)
		return
	}
	io.WriteString(w, `{"hash":"h","size":9}`) // what an upload answers; other calls ignore it
}

func client503(t *testing.T, s *stub503) *Client { return clientOf(t, s) }

const (
	marked   = `{"error":"no leader available","retry_safe":true}`
	unmarked = `{"error":"the cluster did not commit in time"}`
)

func TestRetrySafeWriteIsResentAndSucceeds(t *testing.T) {
	s := &stub503{fail: 1, retryAfter: "0", body: marked, bodies: make(chan string, 4)}
	c := client503(t, s)
	if err := c.do("POST", "/v1/mail", map[string]string{"text": "hi"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := s.calls.Load(); got != 2 {
		t.Fatalf("a marked write is sent again once: %d sends", got)
	}
	if a, b := <-s.bodies, <-s.bodies; a != b || a == "" {
		t.Fatalf("the resend carries the same body: %q then %q", a, b)
	}
}

func TestUnmarkedWriteIsSentOnce(t *testing.T) {
	s := &stub503{fail: 3, retryAfter: "0", body: unmarked}
	c := client503(t, s)
	err := c.do("POST", "/v1/mail", map[string]string{"text": "hi"}, nil)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 503 || ae.RetrySafe() {
		t.Fatalf("want the 503: %v", err)
	}
	if got := s.calls.Load(); got != 1 {
		t.Fatalf("an unmarked write may still commit: it was sent %d times, two mails would land", got)
	}
}

func TestReadIsResentOn503(t *testing.T) {
	s := &stub503{fail: 2, retryAfter: "0", body: unmarked}
	c := client503(t, s)
	if err := c.do("GET", "/v1/manifest", nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := s.calls.Load(); got != 3 {
		t.Fatalf("a read is safe to resend: %d sends", got)
	}
}

func TestWriteWithIdempotencyKeyIsResent(t *testing.T) {
	s := &stub503{fail: 1, retryAfter: "0", body: unmarked}
	c := client503(t, s)
	if _, err := c.SendIdem(wire.Message{To: "b"}, "key-1"); err != nil {
		t.Fatal(err)
	}
	if got := s.calls.Load(); got != 2 {
		t.Fatalf("the key makes the resend safe: %d sends", got)
	}
}

func TestA503WithoutRetryAfterIsNotResent(t *testing.T) {
	for _, m := range []string{"GET", "POST"} {
		s := &stub503{fail: 3, body: `{"error":"space_unavailable: ask the operator","code":"space_unavailable","retry_safe":true}`}
		c := client503(t, s)
		if err := c.do(m, "/v1/x", nil, nil); err == nil {
			t.Fatalf("%s: want the 503", m)
		}
		if got := s.calls.Load(); got != 1 {
			t.Fatalf("%s: no Retry-After, an operator has to act: %d sends", m, got)
		}
	}
}

func TestRetryIsBoundedAndKeepsTheServersSentence(t *testing.T) {
	s := &stub503{fail: 100, retryAfter: "0", body: marked}
	c := client503(t, s)
	err := c.do("POST", "/v1/mail", struct{}{}, nil)
	var ae *APIError
	if !errors.As(err, &ae) || !strings.Contains(ae.Error(), "no leader available") || !ae.RetrySafe() {
		t.Fatalf("the person sees the server's sentence: %v", err)
	}
	if got := s.calls.Load(); got != 3 {
		t.Fatalf("3 attempts in all, got %d", got)
	}
}

func TestRetryWaitsRetryAfterCappedAt10s(t *testing.T) {
	cases := []struct {
		name, v string
		want    time.Duration
		ok      bool
	}{
		{"absent", "", 0, false},
		{"seconds", "2", 2 * time.Second, true},
		{"zero", "0", 0, true},
		{"over the cap", "3600", 10 * time.Second, true},
		{"too big to multiply", "99999999999", 10 * time.Second, true},
		{"beyond an int", "99999999999999999999999", 10 * time.Second, true},
		{"negative", "-1", 0, false},
		{"junk", "soon", 0, false},
		{"a date in the past", "Wed, 21 Oct 2015 07:28:00 GMT", 0, true},
		{"a date far ahead", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat), 10 * time.Second, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := http.Header{}
			if c.v != "" {
				h.Set("Retry-After", c.v)
			}
			got, ok := retryAfterWait(h, retry503MaxWait)
			if ok != c.ok || got != c.want {
				t.Fatalf("got %v %v, want %v %v", got, ok, c.want, c.ok)
			}
		})
	}
	s := &stub503{fail: 1, retryAfter: "1", body: marked}
	c := client503(t, s)
	start := time.Now()
	if err := c.do("POST", "/v1/mail", struct{}{}, nil); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 900*time.Millisecond {
		t.Fatalf("Retry-After: 1 was not honoured: %v", d)
	}
}

func TestRetryStopsAtTheCallersContext(t *testing.T) {
	s := &stub503{fail: 100, retryAfter: "5", body: unmarked}
	c := client503(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req, err := c.req("GET", "/v1/manifest", nil)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	resp, err := c.send(req.WithContext(ctx))
	if err == nil {
		defer resp.Body.Close()
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("waited past the caller's deadline: %v", d)
	}
	if got := s.calls.Load(); got != 1 {
		t.Fatalf("no second send after the deadline: %d", got)
	}
}

func TestPutBlobResendsItsBytesOnAMarked503(t *testing.T) {
	s := &stub503{fail: 1, retryAfter: "0", body: marked, bodies: make(chan string, 4)}
	c := client503(t, s)
	if _, _, err := c.PutBlob(strings.NewReader("the bytes")); err != nil {
		t.Fatal(err)
	}
	if a, b := <-s.bodies, <-s.bodies; a != "the bytes" || b != "the bytes" {
		t.Fatalf("both sends carry the bytes: %q %q", a, b)
	}
}

// oneShot has no GetBody: its bytes cannot be sent twice.
type oneShot struct{ io.Reader }

func TestABodyThatCannotBeReplayedIsNotResent(t *testing.T) {
	s := &stub503{fail: 1, retryAfter: "0", body: marked}
	c := client503(t, s)
	if _, _, err := c.PutBlob(oneShot{strings.NewReader("stream")}); err == nil {
		t.Fatal("want the 503")
	}
	if got := s.calls.Load(); got != 1 {
		t.Fatalf("%d sends", got)
	}
}

func TestOther5xxAreNotRetried(t *testing.T) {
	for _, code := range []int{500, 502, 504} {
		var calls atomic.Int32
		c := clientOf(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(code)
			io.WriteString(w, marked)
		}))
		c.do("GET", "/v1/manifest", nil, nil)
		if calls.Load() != 1 {
			t.Fatalf("%d: the rule is about 503: %d sends", code, calls.Load())
		}
	}
}

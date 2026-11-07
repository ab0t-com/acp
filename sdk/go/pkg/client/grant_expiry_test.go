package client

// G-W15c (twin of the TS "T-W15 …" tests, D12, D18): a login with an end. A 401
// grant_expired says what to do, and nothing keeps retrying it: no reconnect can cure it.
// streamGap reads event_log. FAIL-SAFE: httptest on 127.0.0.1 only.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ab0t-com/acp/sdk/go/pkg/wire"
)

const endedBody = `{"error":"this login expired at 2026-11-02T10:00:00Z","code":"grant_expired"}`

func TestGrantExpiredSaysWhatToDo(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(endedBody))
	}))
	defer ts.Close()
	_, err := newTestClient(t, ts.URL).Whoami()
	if err == nil || !strings.Contains(err.Error(), "this login expired at 2026-11-02T10:00:00Z; ask an admin for a new login, retrying cannot help") {
		t.Fatalf("%v", err)
	}
	if !IsGrantExpired(err) || !IsGrantExpired(fmt.Errorf("wrapped: %w", err)) {
		t.Fatalf("%#v", err)
	}
	for _, e := range []*APIError{
		{Status: 401, Message: "invalid or missing bearer token"},
		{Status: 403, Raw: []byte(`{"code":"grant_expired"}`)},
		{Status: 401, Message: "grant_expired", Raw: []byte(`{"error":"grant_expired"}`)},
	} {
		if e.GrantExpired() {
			t.Fatalf("status and code, never the text: %+v", e)
		}
	}
	if IsGrantExpired(errors.New("grant_expired")) || IsGrantExpired(nil) {
		t.Fatal("not an answer from a server")
	}
}

func TestWhoamiAndRedeemCarryTheEnd(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"agent":"a","role":"writer","expires_at":"2026-12-01T00:00:00Z"}`))
	}))
	defer ts.Close()
	g, err := newTestClient(t, ts.URL).Whoami()
	if err != nil || g.ExpiresAt != "2026-12-01T00:00:00Z" {
		t.Fatalf("%+v %v", g, err)
	}
}

func TestFollowForeverStopsWhenTheLoginEnds(t *testing.T) {
	s, c := newStub(t)
	s.mu.Lock()
	s.refuse = endedBody
	s.mu.Unlock()
	retries := 0
	done := make(chan error, 1)
	go func() {
		done <- c.FollowForever(1, func(wire.Event) error { return nil }, &FollowForeverOptions{
			OnRetry:    func(error, int, uint64) { retries++ },
			MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond,
		})
	}()
	select {
	case err := <-done:
		if !IsGrantExpired(err) {
			t.Fatalf("the caller gets the reason: %v", err)
		}
		if retries != 0 {
			t.Fatalf("no retry: %d", retries)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("it kept reconnecting to a login that has ended")
	}
}

// Any other 401 is still the stream's to retry, as documented: a token can come back.
func TestFollowForeverStillRetriesAPlain401(t *testing.T) {
	s, c := newStub(t)
	s.mu.Lock()
	s.refuse = `{"error":"invalid or missing bearer token"}`
	s.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	retries := 0
	done := make(chan error, 1)
	go func() {
		done <- c.FollowForever(1, func(wire.Event) error { return nil }, &FollowForeverOptions{
			Context: ctx, MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond,
			OnRetry: func(error, int, uint64) { mu.Lock(); retries++; mu.Unlock() },
		})
	}()
	waitFor(t, "retries", func() bool { mu.Lock(); defer mu.Unlock(); return retries >= 3 })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSubscribeTellsEachSubscriberOnceWhenTheLoginEnds(t *testing.T) {
	t.Run("a running stream", func(t *testing.T) {
		s, c := newStub(t)
		s.append("a")
		var a, b countErrs
		subscribeT(t, c, func(wire.Event) error { return nil }, SubscribeOptions{From: from(1), OnError: a.add})
		subscribeT(t, c, func(wire.Event) error { return nil }, SubscribeOptions{From: from(1), OnError: b.add})
		waitFor(t, "stream open", func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.opens) > 0 })
		s.mu.Lock()
		s.refuse = endedBody
		s.mu.Unlock()
		s.kick()
		waitFor(t, "told", func() bool { return a.get() == 1 && b.get() == 1 })
		n := s.refused.Load()
		time.Sleep(50 * time.Millisecond)
		if s.refused.Load() != n {
			t.Fatal("the stream must stop: it went on asking")
		}
		if a.get() != 1 || b.get() != 1 || !IsGrantExpired(a.last()) {
			t.Fatalf("once each: %d %d %v", a.get(), b.get(), a.last())
		}
		// A later subscriber hears it too, without one more call to the server.
		var late countErrs
		subscribeT(t, c, func(wire.Event) error { return nil }, SubscribeOptions{OnError: late.add})
		waitFor(t, "late told", func() bool { return late.get() == 1 })
		if s.refused.Load() != n {
			t.Fatal("no call: the answer is known")
		}
	})
	t.Run("before any position is known: the head read is refused", func(t *testing.T) {
		old := retryPause
		retryPause = time.Millisecond
		defer func() { retryPause = old }()
		s, c := newStub(t)
		s.mu.Lock()
		s.refuse = endedBody
		s.mu.Unlock()
		var a countErrs
		subscribeT(t, c, func(wire.Event) error { return nil }, SubscribeOptions{OnError: a.add})
		waitFor(t, "told", func() bool { return a.get() == 1 })
		n := s.refused.Load()
		time.Sleep(50 * time.Millisecond)
		if s.refused.Load() != n || a.get() != 1 {
			t.Fatalf("no loop of reports: %d %d", s.refused.Load(), a.get())
		}
	})
	t.Run("a plain failure still reconnects", func(t *testing.T) {
		s, c := newStub(t)
		r := &recorder{}
		var a countErrs
		subscribeT(t, c, seqs(r), SubscribeOptions{From: from(1), OnError: a.add})
		s.append("a")
		waitFor(t, "first", func() bool { return r.len() == 1 })
		s.kick()
		s.append("b")
		waitFor(t, "second", func() bool { return r.len() == 2 })
		if a.get() != 0 {
			t.Fatal("not an error of the login")
		}
	})
}

type countErrs struct {
	mu  sync.Mutex
	n   int
	err error
}

func (c *countErrs) add(e error) { c.mu.Lock(); c.n++; c.err = e; c.mu.Unlock() }
func (c *countErrs) get() int    { c.mu.Lock(); defer c.mu.Unlock(); return c.n }
func (c *countErrs) last() error { c.mu.Lock(); defer c.mu.Unlock(); return c.err }

func TestStreamGapReadsEventLog(t *testing.T) {
	for _, tc := range []struct {
		name      string
		from      uint64
		configure func(s *stubLog)
		wantGap   string
		wantKnown bool
	}{
		{"a log never used is no gap, even from zero", 0, func(s *stubLog) { s.eventLog = "empty" }, "", true},
		{"empty counts as a known answer with no event_base_seq", 1, func(s *stubLog) { s.eventLog = "empty"; s.reportBase = false }, "", true},
		{"behind comes first: a restored history still resyncs", 5, func(s *stubLog) { s.eventLog = "empty" }, GapBehind, true},
		{"compacted to nothing keeps the old reading", 1, func(s *stubLog) {
			s.eventLog = "compacted"
			s.events = []wire.Event{{Seq: 1}, {Seq: 2}}
			s.keptFrom = 3
		}, GapCompacted, true},
		{"retained, nothing lost", 2, func(s *stubLog) {
			s.eventLog = "retained"
			s.events = []wire.Event{{Seq: 1}, {Seq: 2}}
		}, "", true},
		{"a server without the field: the old reading, the harmless gap", 0, func(s *stubLog) {}, GapCompacted, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, c := newStub(t)
			s.mu.Lock()
			tc.configure(s)
			s.mu.Unlock()
			gap, known := c.streamGap(context.Background(), tc.from)
			if known != tc.wantKnown {
				t.Fatalf("known=%v", known)
			}
			switch {
			case tc.wantGap == "" && gap != nil:
				t.Fatalf("spurious gap: %+v", gap)
			case tc.wantGap != "" && (gap == nil || gap.Reason != tc.wantGap):
				t.Fatalf("gap %+v, want %s", gap, tc.wantGap)
			}
		})
	}
}

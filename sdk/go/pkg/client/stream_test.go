package client

// FollowForever and Subscribe against an in-process stub daemon with a real
// event log: seqs climb, retention drops a prefix (keptFrom), restore(n) cuts
// the log back to n events, follow streams replay then live with the action
// filter, kick() drops open streams. Twins of the TS sdk's stream-gap tests.
// FAIL-SAFE: httptest on 127.0.0.1 only, and every stream and subscription is
// closed by t.Cleanup even when a test fails midway.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ab0t-com/acp/sdk/go/pkg/wire"
)

type stubFollower struct {
	ch   chan wire.Event
	done chan struct{}
}

type stubLog struct {
	mu         sync.Mutex
	events     []wire.Event
	keptFrom   uint64
	reportBase bool
	eventLog   string // the event_log member of /v1/stats; "" = a server without it
	refuse     string // a whole JSON body: every stats and events call answers 401 with it
	refused    atomic.Int64
	statsFail  bool
	stamp      int
	logID      string // D55: log_id in /v1/stats and X-ACP-Log-Id on the stream; "" = an older server
	followers  map[*stubFollower]bool
	opens      []string // from|actions per stream open
	stats      atomic.Int64
	reads      atomic.Int64  // plain (not follow) GET /v1/events
	beforeOpen func()        // run (under mu) as a stream request arrives
	holdPlain  chan struct{} // when set, a plain read waits until it is closed
	plainID    func() string // when set, the id a plain read's answer names
}

func (s *stubLog) append(action string) wire.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	seq := uint64(1)
	if n := len(s.events); n > 0 {
		seq = s.events[n-1].Seq + 1
	}
	s.stamp++ // each event its own time, as a server's: one restored and written again differs
	e := wire.Event{Seq: seq, Action: action, Entity: "x", Actor: "t", At: fmt.Sprintf("2026-11-06T00:00:%02d.%06dZ", s.stamp%60, s.stamp)}
	s.events = append(s.events, e)
	for f := range s.followers {
		f.ch <- e
	}
	return e
}

func (s *stubLog) compactTo(seq uint64) { s.mu.Lock(); s.keptFrom = seq; s.mu.Unlock() }

func (s *stubLog) restore(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = s.events[:n]
	if uint64(n)+1 < s.keptFrom {
		s.keptFrom = uint64(n) + 1
	}
}

func (s *stubLog) kick() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for f := range s.followers {
		delete(s.followers, f)
		close(f.done)
	}
}

func (s *stubLog) followerCount() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.followers) }

func (s *stubLog) openList() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.opens...)
}

func newStub(t *testing.T) (*stubLog, *Client) {
	t.Helper()
	s := &stubLog{keptFrom: 1, reportBase: true, followers: map[*stubFollower]bool{}}
	j := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		j(w, map[string]any{"protocol": "acp/1", "capabilities": []string{"channels"}})
	})
	mux.HandleFunc("/v1/stats", func(w http.ResponseWriter, r *http.Request) {
		s.stats.Add(1)
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.refuse != "" {
			s.refused.Add(1)
			w.WriteHeader(401)
			w.Write([]byte(s.refuse))
			return
		}
		if s.statsFail {
			w.WriteHeader(500)
			return
		}
		head, base := uint64(0), uint64(0)
		if n := len(s.events); n > 0 {
			head = s.events[n-1].Seq
		}
		for _, e := range s.events {
			if e.Seq >= s.keptFrom {
				base = e.Seq
				break
			}
		}
		out := map[string]any{"events": head}
		if s.reportBase {
			out["event_base_seq"] = base
		}
		if s.eventLog != "" {
			out["event_log"] = s.eventLog
		}
		if s.logID != "" {
			out["log_id"] = s.logID
		}
		j(w, out)
	})
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		from, _ := strconv.ParseUint(q.Get("from"), 10, 64)
		if q.Get("follow") != "true" { // the plain read: what LogPosition asks, past the head
			s.mu.Lock()
			hold := s.holdPlain
			s.mu.Unlock()
			if hold != nil {
				<-hold
			}
			s.mu.Lock()
			s.reads.Add(1)
			if s.refuse != "" {
				body := s.refuse
				s.mu.Unlock()
				s.refused.Add(1)
				w.WriteHeader(401)
				w.Write([]byte(body))
				return
			}
			head := uint64(0)
			if n := len(s.events); n > 0 {
				head = s.events[n-1].Seq
			}
			if id := s.logID; s.plainID != nil {
				if id = s.plainID(); id != "" {
					w.Header().Set(wire.HeaderLogID, id)
				}
			} else if id != "" {
				w.Header().Set(wire.HeaderLogID, id)
				w.Header().Set(wire.HeaderLogHead, strconv.FormatUint(head, 10))
			}
			var out []wire.Event
			for _, e := range s.events {
				if e.Seq >= from {
					out = append(out, e)
				}
			}
			s.mu.Unlock()
			j(w, append([]wire.Event{}, out...))
			return
		}
		actions := q.Get("actions")
		match := func(a string) bool {
			if actions == "" {
				return true
			}
			for _, p := range strings.Split(actions, ",") {
				if wire.MatchPattern(p, a) {
					return true
				}
			}
			return false
		}
		f := &stubFollower{ch: make(chan wire.Event, 256), done: make(chan struct{})}
		s.mu.Lock()
		if s.beforeOpen != nil {
			s.beforeOpen()
		}
		if s.refuse != "" {
			body := s.refuse
			s.mu.Unlock()
			s.refused.Add(1)
			w.WriteHeader(401)
			w.Write([]byte(body))
			return
		}
		s.opens = append(s.opens, fmt.Sprintf("%d|%s", from, actions))
		var backlog []wire.Event
		for _, e := range s.events {
			if e.Seq >= from && e.Seq >= s.keptFrom {
				backlog = append(backlog, e)
			}
		}
		s.followers[f] = true
		logID, head := s.logID, uint64(0)
		if n := len(s.events); n > 0 {
			head = s.events[n-1].Seq
		}
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			delete(s.followers, f)
			s.mu.Unlock()
		}()
		w.Header().Set("Content-Type", "application/x-ndjson")
		if logID != "" {
			w.Header().Set(wire.HeaderLogID, logID)
			w.Header().Set(wire.HeaderLogHead, strconv.FormatUint(head, 10))
		}
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		last := from - 1
		if from == 0 {
			last = 0
		}
		write := func(e wire.Event) {
			if e.Seq <= last {
				return // like coordd: never re-deliver below the declared resume point
			}
			last = e.Seq
			if match(e.Action) {
				json.NewEncoder(w).Encode(e)
				w.(http.Flusher).Flush()
			}
		}
		for _, e := range backlog {
			write(e)
		}
		for {
			select {
			case e := <-f.ch:
				write(e)
			case <-f.done:
				return
			case <-r.Context().Done():
				return
			}
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(func() { s.kick(); ts.CloseClientConnections(); ts.Close() })
	return s, newTestClient(t, ts.URL)
}

type recorder struct {
	mu  sync.Mutex
	log []string
}

func (r *recorder) add(format string, a ...any) {
	r.mu.Lock()
	r.log = append(r.log, fmt.Sprintf(format, a...))
	r.mu.Unlock()
}
func (r *recorder) len() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.log) }
func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.log...)
}

func waitFor(t *testing.T, label string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !cond(); time.Sleep(2 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", label)
		}
	}
}

// follow runs FollowForever until the test ends, recording events and gaps.
func follow(t *testing.T, c *Client, r *recorder, o FollowForeverOptions) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	o.Context, o.MinBackoff, o.MaxBackoff = ctx, 5*time.Millisecond, 20*time.Millisecond
	if o.OnResync == nil {
		o.OnResync = func(g StreamGap) error {
			r.add("resync %s from=%d resumeAt=%d", g.Reason, g.From, g.ResumeAt)
			return nil
		}
	}
	done := make(chan error, 1)
	go func() {
		done <- c.FollowForever(1, func(e wire.Event) error { r.add("ev %d", e.Seq); return nil }, &o)
	}()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			if err := <-done; err != nil {
				t.Errorf("a cancelled follow returns nil, got %v", err)
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func want(t *testing.T, r *recorder, w ...string) {
	t.Helper()
	if got := r.snapshot(); !reflect.DeepEqual(got, w) {
		t.Fatalf("got  %q\nwant %q", got, w)
	}
}

func TestFollowForeverResumesWithoutLossOrDuplicate(t *testing.T) {
	s, c := newStub(t)
	r := &recorder{}
	s.append("a")
	stop := follow(t, c, r, FollowForeverOptions{})
	waitFor(t, "first", func() bool { return r.len() == 1 })
	s.kick()
	s.append("a")
	waitFor(t, "after the drop", func() bool { return r.len() == 2 })
	stop()
	want(t, r, "ev 1", "ev 2")
}

func TestFollowForeverGapCallsOnResyncBeforeTheNextEvent(t *testing.T) {
	s, c := newStub(t)
	r := &recorder{}
	for i := 0; i < 3; i++ {
		s.append("a")
	}
	stop := follow(t, c, r, FollowForeverOptions{})
	waitFor(t, "first three", func() bool { return r.len() == 3 })
	s.kick()
	for i := 0; i < 3; i++ {
		s.append("a") // 4, 5, 6 while we reconnect...
	}
	s.compactTo(6) // ...and retention removes 4 and 5
	waitFor(t, "resync + 6", func() bool { return r.len() == 5 })
	stop()
	want(t, r, "ev 1", "ev 2", "ev 3", "resync compacted from=4 resumeAt=6", "ev 6")
}

func TestFollowForeverLogCompactedToNothingIsAGap(t *testing.T) {
	s, c := newStub(t)
	r := &recorder{}
	s.append("a")
	s.append("a")
	stop := follow(t, c, r, FollowForeverOptions{})
	waitFor(t, "first two", func() bool { return r.len() == 2 })
	s.kick()
	s.append("a") // 3
	s.compactTo(4)
	waitFor(t, "resync", func() bool { return r.len() == 3 })
	s.append("a") // 4
	waitFor(t, "event 4", func() bool { return r.len() == 4 })
	stop()
	want(t, r, "ev 1", "ev 2", "resync compacted from=3 resumeAt=4", "ev 4")
}

func TestFollowForeverFilteredSeqGapsAreNotAResync(t *testing.T) {
	s, c := newStub(t)
	r := &recorder{}
	s.append("a")
	s.append("noise")
	s.append("a")
	stop := follow(t, c, r, FollowForeverOptions{Filter: &EventFilter{Actions: []string{"a"}}})
	waitFor(t, "1 and 3", func() bool { return r.len() == 2 })
	s.kick()
	s.append("noise") // 4
	s.append("a")     // 5
	waitFor(t, "5", func() bool { return r.len() == 3 })
	s.kick()
	s.append("a") // 6
	s.append("a") // 7
	s.compactTo(7)
	waitFor(t, "resync + 7", func() bool { return r.len() == 5 })
	stop()
	want(t, r, "ev 1", "ev 3", "ev 5", "resync compacted from=6 resumeAt=7", "ev 7")
}

func TestFollowForeverHistoryRestoredEarlierResumesAtTheNewHead(t *testing.T) {
	s, c := newStub(t)
	r := &recorder{}
	for i := 0; i < 5; i++ {
		s.append("a")
	}
	stop := follow(t, c, r, FollowForeverOptions{OnResync: nil})
	waitFor(t, "five", func() bool { return r.len() == 5 })
	s.kick()
	s.restore(2)
	waitFor(t, "resync", func() bool { return r.len() == 6 })
	s.append("a") // seq 3 again: a NEW event a stream resuming at 6 would silently skip
	s.append("a") // 4
	waitFor(t, "3 and 4", func() bool { return r.len() == 8 })
	stop()
	want(t, r, "ev 1", "ev 2", "ev 3", "ev 4", "ev 5", "resync behind from=6 resumeAt=3", "ev 3", "ev 4")
}

func TestFollowForeverServerWithoutKeptRangeDetectsTheGapFromTheFirstEvent(t *testing.T) {
	s, c := newStub(t)
	s.reportBase = false
	r := &recorder{}
	s.append("a")
	stop := follow(t, c, r, FollowForeverOptions{})
	waitFor(t, "1", func() bool { return r.len() == 1 })
	s.kick()
	s.append("a")
	s.append("a")
	s.compactTo(3)
	waitFor(t, "resync + 3", func() bool { return r.len() == 3 })
	stop()
	want(t, r, "ev 1", "resync compacted from=2 resumeAt=3", "ev 3")
}

func TestFollowForeverStatsFailureIsAnUnknownGap(t *testing.T) {
	s, c := newStub(t)
	r := &recorder{}
	s.append("a")
	s.mu.Lock()
	s.statsFail = true
	s.mu.Unlock()
	stop := follow(t, c, r, FollowForeverOptions{})
	waitFor(t, "resync and event", func() bool { return r.len() == 2 })
	stop()
	want(t, r, "resync unknown from=1 resumeAt=1", "ev 1")
}

func TestFollowForeverCallerErrorsEndIt(t *testing.T) {
	s, c := newStub(t)
	s.append("a")
	s.compactTo(2)
	s.append("a")
	boom := errors.New("resync failed")
	err := c.FollowForever(1, func(wire.Event) error { return nil }, &FollowForeverOptions{
		OnResync: func(StreamGap) error { return boom }, MinBackoff: time.Millisecond})
	if !errors.Is(err, boom) {
		t.Fatalf("an OnResync error ends the follow: %v", err)
	}
	fnErr := errors.New("fn failed")
	err = c.FollowForever(0, func(wire.Event) error { return fnErr }, &FollowForeverOptions{MinBackoff: time.Millisecond})
	if !errors.Is(err, fnErr) {
		t.Fatalf("an fn error ends the follow: %v", err)
	}
}

// Without OnResync nothing extra is read, ever (D55: a changed log id comes on the stream's
// own answer, no stats read needed).
func TestFollowForeverWithoutOnResyncAsksForNothingExtra(t *testing.T) {
	s, c := newStub(t)
	s.append("a")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var seen atomic.Int64
	done := make(chan error, 1)
	go func() {
		done <- c.FollowForever(1, func(wire.Event) error { seen.Add(1); return nil },
			&FollowForeverOptions{Context: ctx, MinBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond})
	}()
	waitFor(t, "event", func() bool { return seen.Load() == 1 })
	s.kick()
	waitFor(t, "reconnect", func() bool { return len(s.openList()) >= 2 })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n := s.stats.Load(); n != 0 {
		t.Fatalf("without OnResync, %d stats reads: want none", n)
	}
}

func TestFollowForeverFromZeroIsNotAGap(t *testing.T) {
	s, c := newStub(t)
	r := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- c.FollowForever(0, func(e wire.Event) error { r.add("ev %d", e.Seq); return nil }, &FollowForeverOptions{
			Context: ctx, OnResync: func(g StreamGap) error { r.add("resync %+v", g); return nil }})
	}()
	waitFor(t, "open", func() bool { return s.followerCount() == 1 })
	s.append("a")
	waitFor(t, "event", func() bool { return r.len() >= 1 })
	cancel()
	<-done
	want(t, r, "ev 1")
}

func subscribeT(t *testing.T, c *Client, fn func(wire.Event) error, o SubscribeOptions) func() {
	t.Helper()
	u := c.Subscribe(fn, o)
	t.Cleanup(u)
	return u
}

func from(n uint64) *uint64 { return &n }

func seqs(r *recorder) func(e wire.Event) error {
	return func(e wire.Event) error { r.add("%d", e.Seq); return nil }
}

func TestSubscribeManySubscribersRideOneStream(t *testing.T) {
	s, c := newStub(t)
	chat, mail, all := &recorder{}, &recorder{}, &recorder{}
	u1 := subscribeT(t, c, seqs(chat), SubscribeOptions{Filter: &EventFilter{Actions: []string{"chat.msg"}}, From: from(1)})
	u2 := subscribeT(t, c, seqs(mail), SubscribeOptions{Filter: &EventFilter{Actions: []string{"chat.sent"}}, From: from(1)})
	s.append("chat.msg")  // 1
	s.append("chat.sent") // 2
	s.append("build")     // 3
	s.append("chat.msg")  // 4
	waitFor(t, "chat + mail", func() bool { return chat.len() == 2 && mail.len() == 1 })
	u3 := subscribeT(t, c, seqs(all), SubscribeOptions{}) // from now on
	waitFor(t, "a widened, unfiltered stream", func() bool {
		o := s.openList()
		return s.followerCount() == 1 && strings.HasSuffix(o[len(o)-1], "|")
	})
	s.append("chat.sent") // 5
	s.append("x")         // 6
	waitFor(t, "live after widening", func() bool { return all.len() == 2 && mail.len() == 2 })
	want(t, chat, "1", "4")
	want(t, mail, "2", "5")
	want(t, all, "5", "6")
	if n := s.followerCount(); n != 1 {
		t.Fatalf("one connection, however many subscribers: %d", n)
	}
	u1()
	u2()
	u3()
	waitFor(t, "the stream closes with the last subscriber", func() bool { return s.followerCount() == 0 })
}

func TestSubscribeEarlierFromGetsTheBacklogAndOthersNothingTwice(t *testing.T) {
	s, c := newStub(t)
	a, b := &recorder{}, &recorder{}
	for i := 0; i < 3; i++ {
		s.append("e")
	}
	subscribeT(t, c, seqs(a), SubscribeOptions{From: from(1)})
	waitFor(t, "a backlog", func() bool { return a.len() == 3 })
	s.append("e") // 4
	waitFor(t, "a live", func() bool { return a.len() == 4 })
	subscribeT(t, c, seqs(b), SubscribeOptions{From: from(2)})
	waitFor(t, "b backlog", func() bool { return b.len() == 3 })
	s.append("e") // 5
	waitFor(t, "both live", func() bool { return a.len() == 5 && b.len() == 4 })
	want(t, a, "1", "2", "3", "4", "5")
	want(t, b, "2", "3", "4", "5")
}

func TestSubscribeGapResyncsOnlyTheSubscribersItAffects(t *testing.T) {
	s, c := newStub(t)
	a, b, resyncs := &recorder{}, &recorder{}, &recorder{}
	rs := func(name string) func(StreamGap) error {
		return func(g StreamGap) error { resyncs.add("%s %d->%d", name, g.From, g.ResumeAt); return nil }
	}
	subscribeT(t, c, seqs(a), SubscribeOptions{From: from(1), OnResync: rs("a")})
	s.append("e") // 1
	s.append("e") // 2
	waitFor(t, "a 1,2", func() bool { return a.len() == 2 })
	s.kick()
	s.append("e") // 3, while reconnecting; kept
	waitFor(t, "a 3 after the reconnect", func() bool { return a.len() == 3 })
	subscribeT(t, c, seqs(b), SubscribeOptions{From: from(6), OnResync: rs("b")}) // a future point
	s.kick()
	s.append("e") // 4
	s.append("e") // 5
	s.append("e") // 6
	s.compactTo(6)
	waitFor(t, "6 to both", func() bool { return a.len() == 4 && b.len() == 1 })
	want(t, a, "1", "2", "3", "6")
	want(t, b, "6")
	want(t, resyncs, "a 4->6") // b's starting point was never lost
}

func TestSubscribeBehindGapMovesEverySubscriberBack(t *testing.T) {
	s, c := newStub(t)
	a, resyncs := &recorder{}, &recorder{}
	for i := 0; i < 4; i++ {
		s.append("e")
	}
	subscribeT(t, c, seqs(a), SubscribeOptions{From: from(1), OnResync: func(g StreamGap) error {
		resyncs.add("%s %d->%d", g.Reason, g.From, g.ResumeAt)
		return nil
	}})
	waitFor(t, "four", func() bool { return a.len() == 4 })
	s.kick()
	s.restore(1)
	waitFor(t, "resync", func() bool { return resyncs.len() == 1 })
	s.append("e") // 2 again
	waitFor(t, "the new 2", func() bool { return a.len() == 5 })
	want(t, a, "1", "2", "3", "4", "2")
	want(t, resyncs, "behind 5->2")
}

func TestSubscribeAFailingSubscriberStopsNothing(t *testing.T) {
	s, c := newStub(t)
	errs, ok := &recorder{}, &recorder{}
	subscribeT(t, c, func(wire.Event) error { return errors.New("subscriber bug") },
		SubscribeOptions{From: from(1), OnError: func(e error) { errs.add("%v", e) }})
	subscribeT(t, c, seqs(ok), SubscribeOptions{From: from(1)})
	s.append("e")
	s.append("e")
	waitFor(t, "the healthy subscriber", func() bool { return ok.len() == 2 })
	want(t, errs, "subscriber bug", "subscriber bug")
}

func TestSubscribeUnsubscribeFromInsideFnDoesNotDeadlock(t *testing.T) {
	s, c := newStub(t)
	got, other := &recorder{}, &recorder{}
	var u func()
	var ready sync.WaitGroup
	ready.Add(1)
	u = c.Subscribe(func(e wire.Event) error {
		ready.Wait()
		got.add("%d", e.Seq)
		u()
		u() // twice is harmless
		return nil
	}, SubscribeOptions{From: from(1)})
	t.Cleanup(u)
	ready.Done()
	subscribeT(t, c, seqs(other), SubscribeOptions{From: from(1)})
	s.append("e")
	s.append("e")
	waitFor(t, "the other subscriber", func() bool { return other.len() == 2 })
	want(t, got, "1")
}

func TestSubscribeFromNowOnSkipsTheBacklog(t *testing.T) {
	s, c := newStub(t)
	r := &recorder{}
	s.append("e")
	s.append("e")
	subscribeT(t, c, seqs(r), SubscribeOptions{})
	waitFor(t, "open", func() bool { return s.followerCount() == 1 })
	s.append("e") // 3
	waitFor(t, "live", func() bool { return r.len() == 1 })
	want(t, r, "3")
}

func TestSubscribeResubscribeAfterTheStreamClosed(t *testing.T) {
	s, c := newStub(t)
	a, b := &recorder{}, &recorder{}
	u := subscribeT(t, c, seqs(a), SubscribeOptions{From: from(1)})
	s.append("e")
	waitFor(t, "a", func() bool { return a.len() == 1 })
	u()
	waitFor(t, "closed", func() bool { return s.followerCount() == 0 })
	s.append("e") // 2
	subscribeT(t, c, seqs(b), SubscribeOptions{From: from(1)})
	waitFor(t, "b gets it all", func() bool { return b.len() == 2 })
	want(t, b, "1", "2")
}

func TestSubscribeConcurrentJoinsAndLeavesKeepOrderAndDropNothing(t *testing.T) {
	s, c := newStub(t)
	steady := &recorder{}
	subscribeT(t, c, seqs(steady), SubscribeOptions{From: from(1)})
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				u := c.Subscribe(func(wire.Event) error { return nil }, SubscribeOptions{Filter: &EventFilter{Actions: []string{"e", fmt.Sprintf("x%d", i)}}})
				time.Sleep(time.Millisecond)
				u()
			}
		}(i)
	}
	const n = 200
	for i := 0; i < n; i++ {
		s.append("e")
		if i%40 == 0 {
			time.Sleep(time.Millisecond)
		}
	}
	waitFor(t, "every event", func() bool { return steady.len() >= n })
	close(stop)
	wg.Wait()
	got := steady.snapshot()
	if len(got) != n {
		t.Fatalf("%d deliveries for %d events", len(got), n)
	}
	for i, v := range got {
		if v != strconv.Itoa(i+1) {
			t.Fatalf("position %d holds %s: reordered or repeated", i, v)
		}
	}
}

func (s *stubLog) setLog(id string) { s.mu.Lock(); s.logID = id; s.mu.Unlock() }

// D55 (R-C24): the log id tells a lagging node from a new log. A new id is a resync with the
// old id, the new id and the new head (never silent); the same id with a lower head is a
// lagging node: no resync, the stream waits at the next event not yet seen.
func TestFollowForeverReadsTheLogID(t *testing.T) {
	t.Run("a new log", func(t *testing.T) {
		s, c := newStub(t)
		s.setLog("sp.e1")
		r := &recorder{}
		for i := 0; i < 5; i++ {
			s.append("a")
		}
		var gap StreamGap
		stop := follow(t, c, r, FollowForeverOptions{OnResync: func(g StreamGap) error {
			gap = g
			r.add("resync %s from=%d resumeAt=%d", g.Reason, g.From, g.ResumeAt)
			return nil
		}})
		waitFor(t, "five", func() bool { return r.len() == 5 })
		s.kick()
		s.restore(2)
		s.setLog("sp.e2")
		waitFor(t, "resync", func() bool { return r.len() == 6 })
		s.append("a") // seq 3 of the new log
		waitFor(t, "3", func() bool { return r.len() == 7 })
		stop()
		want(t, r, "ev 1", "ev 2", "ev 3", "ev 4", "ev 5", "resync restored from=6 resumeAt=3", "ev 3")
		if gap.PreviousLogID != "sp.e1" || gap.LogID != "sp.e2" || gap.Head != 2 {
			t.Fatalf("the gap must carry both ids and the new head: %+v", gap)
		}
	})
	t.Run("the same log, behind: a lagging node", func(t *testing.T) {
		s, c := newStub(t)
		s.setLog("sp.e1")
		r := &recorder{}
		for i := 0; i < 5; i++ {
			s.append("a")
		}
		stop := follow(t, c, r, FollowForeverOptions{})
		waitFor(t, "five", func() bool { return r.len() == 5 })
		s.kick()
		s.mu.Lock()
		lag := s.events
		s.events = s.events[:2] // this node has applied up to 2 only
		s.mu.Unlock()
		waitFor(t, "a reopen", func() bool { return len(s.openList()) >= 2 })
		s.mu.Lock()
		s.events = lag // caught up
		s.mu.Unlock()
		s.append("a") // 6
		waitFor(t, "6", func() bool { return r.len() == 6 })
		stop()
		want(t, r, "ev 1", "ev 2", "ev 3", "ev 4", "ev 5", "ev 6")
		for _, o := range s.openList()[1:] {
			if o != "6|" {
				t.Fatalf("a lagging node: the stream must wait at 6, never go back: %v", s.openList())
			}
		}
	})
	t.Run("no OnResync: a LogChangedError", func(t *testing.T) {
		s, c := newStub(t)
		s.setLog("sp.e1")
		s.append("a")
		opened := make(chan struct{}, 8)
		done := make(chan error, 1)
		go func() {
			done <- c.FollowForever(1, func(wire.Event) error { return nil },
				&FollowForeverOptions{MinBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond, OnOpen: func() { opened <- struct{}{} }})
		}()
		<-opened // the first stream named sp.e1
		s.restore(0)
		s.append("a") // seq 1 again, another event: a restore
		s.setLog("sp.e2")
		s.kick()
		select {
		case err := <-done:
			var lc *LogChangedError
			if !errors.As(err, &lc) || lc.Gap.Reason != GapRestored || lc.Gap.PreviousLogID != "sp.e1" || lc.Gap.LogID != "sp.e2" || lc.Gap.ResumeAt != 2 {
				t.Fatalf("want a LogChangedError from sp.e1 to sp.e2 going on at 2, got %v", err)
			}
			if n := s.stats.Load(); n != 0 {
				t.Fatalf("without OnResync, %d stats reads: the stream's own answer says", n)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a new log was passed over in silence")
		}
	})
	// D57 Q1: a new id over the same history (a restart, a failover) skips nothing and tells
	// nothing: the event at the follow's position is the one it saw.
	for _, told := range []bool{true, false} {
		t.Run(fmt.Sprintf("a restart: the same history under a new id (OnResync %v)", told), func(t *testing.T) {
			s, c := newStub(t)
			s.setLog("sp.e1")
			r := &recorder{}
			for i := 0; i < 3; i++ {
				s.append("a")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			o := &FollowForeverOptions{Context: ctx, MinBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond}
			if told {
				o.OnResync = func(g StreamGap) error { r.add("resync %s", g.Reason); return nil }
			}
			done := make(chan error, 1)
			go func() { done <- c.FollowForever(1, func(e wire.Event) error { r.add("ev %d", e.Seq); return nil }, o) }()
			waitFor(t, "three", func() bool { return r.len() == 3 })
			s.kick()
			s.setLog("sp.e2")
			s.append("a") // 4, written between the restart and the reconnect
			s.append("a") // 5
			waitFor(t, "4 and 5", func() bool { return r.len() == 5 })
			cancel()
			if err := <-done; err != nil {
				t.Fatalf("a restart ended the follow: %v", err)
			}
			want(t, r, "ev 1", "ev 2", "ev 3", "ev 4", "ev 5")
		})
	}
	t.Run("the stats read fails: the stream's own id still says", func(t *testing.T) {
		s, c := newStub(t)
		s.setLog("sp.e1")
		s.append("a")
		r := &recorder{}
		stop := follow(t, c, r, FollowForeverOptions{OnResync: func(g StreamGap) error {
			r.add("resync %s %s>%s", g.Reason, g.PreviousLogID, g.LogID)
			return nil
		}})
		waitFor(t, "1", func() bool { return r.len() == 1 })
		s.restore(0)
		s.append("a") // seq 1 again, another event: a restore
		s.mu.Lock()
		s.statsFail, s.logID = true, "sp.e2"
		s.mu.Unlock()
		s.kick()
		waitFor(t, "resync", func() bool { return r.len() >= 2 })
		stop()
		if got := r.snapshot()[1]; got != "resync restored sp.e1>sp.e2" {
			t.Fatalf("with the stats read failing, the stream's header must still announce the new log: %q", r.snapshot())
		}
	})
}

// D55: a new log reaches every subscriber holding a position: OnResync with both ids, or,
// with no OnResync, OnError with a LogChangedError. Delivery goes on at the new log's events.
func TestSubscribeANewLogReachesEverySubscriber(t *testing.T) {
	s, c := newStub(t)
	s.setLog("sp.e1")
	a, b, told := &recorder{}, &recorder{}, &recorder{}
	subscribeT(t, c, seqs(a), SubscribeOptions{From: from(1), OnResync: func(g StreamGap) error {
		told.add("a %s %s>%s %d->%d", g.Reason, g.PreviousLogID, g.LogID, g.From, g.ResumeAt)
		return nil
	}})
	subscribeT(t, c, seqs(b), SubscribeOptions{From: from(1), OnError: func(err error) {
		var lc *LogChangedError
		if errors.As(err, &lc) {
			told.add("b error %s>%s", lc.Gap.PreviousLogID, lc.Gap.LogID)
		}
	}})
	for i := 0; i < 4; i++ {
		s.append("e")
	}
	waitFor(t, "1..4 to both", func() bool { return a.len() == 4 && b.len() == 4 })
	s.kick()
	s.restore(1)
	s.setLog("sp.e2")
	waitFor(t, "both told", func() bool { return told.len() == 2 })
	s.append("e") // 2 of the new log
	waitFor(t, "2 to both", func() bool { return a.len() == 5 && b.len() == 5 })
	got := told.snapshot()
	if len(got) != 2 || !((got[0] == "a restored sp.e1>sp.e2 5->2" && got[1] == "b error sp.e1>sp.e2") || (got[1] == "a restored sp.e1>sp.e2 5->2" && got[0] == "b error sp.e1>sp.e2")) {
		t.Fatalf("told: %q", got)
	}
	want(t, a, "1", "2", "3", "4", "2")
	want(t, b, "1", "2", "3", "4", "2")
}

// D55: ResumeCheck, the one read a follower makes per (re)connect, on its own.
func TestResumeCheckTellsALaggingNodeFromANewLog(t *testing.T) {
	s, c := newStub(t)
	for i := 0; i < 2; i++ {
		s.append("a")
	}
	ctx := context.Background()
	s.setLog("sp.e2")
	g, _, id := c.ResumeCheck(ctx, 6, "sp.e1")
	if id != "sp.e2" || g == nil || g.Reason != GapRestored || g.PreviousLogID != "sp.e1" || g.LogID != "sp.e2" || g.Head != 2 || g.ResumeAt != 3 {
		t.Fatalf("a new, shorter log: %+v (id %q), want restored sp.e1>sp.e2 head 2 resume at 3", g, id)
	}
	if g, _, _ := c.ResumeCheck(ctx, 2, "sp.e1"); g == nil || g.Reason != GapRestored || g.ResumeAt != 3 {
		t.Fatalf("a new log: %+v, want restored, going on at the new head (3)", g)
	}
	if g, _, _ := c.ResumeCheck(ctx, 6, "sp.e2"); g != nil {
		t.Fatalf("the same log, behind: a lagging node, no gap: %+v", g)
	}
	s.setLog("") // an older server
	if g, _, id := c.ResumeCheck(ctx, 6, "sp.e2"); id != "" || g == nil || g.Reason != GapBehind || g.ResumeAt != 3 {
		t.Fatalf("no id and a lower head: behind, as before D55: %+v (id %q)", g, id)
	}
}

// D55: a stream's own answer names its log and head (FollowOptions.OnStreamOpen), before OnOpen;
// a server that names none gives "" and -1.
func TestFollowOptionsOnStreamOpenNamesTheLog(t *testing.T) {
	for _, id := range []string{"sp.e1", ""} {
		s, c := newStub(t)
		s.setLog(id)
		s.append("a")
		s.append("a")
		ctx, cancel := context.WithCancel(context.Background())
		var got []string
		err := c.FollowFilteredWith(1, nil, &FollowOptions{Context: ctx,
			OnStreamOpen: func(o StreamOpen) { got = append(got, fmt.Sprintf("open %q %d", o.LogID, o.Head)) },
			OnOpen:       func() { got = append(got, "onopen") },
		}, func(e wire.Event) error { cancel(); return errors.New("done") })
		cancel()
		if err == nil {
			t.Fatal("the stream should end with fn's error")
		}
		head := 2
		if id == "" {
			head = -1
		}
		if want := []string{fmt.Sprintf("open %q %d", id, head), "onopen"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

// D55: the hub restarts its stream when the subscribers change; the log id goes across, so a
// log that changed in between is still told.
func TestSubscribeKeepsTheLogIDAcrossAStreamRestart(t *testing.T) {
	s, c := newStub(t)
	s.setLog("sp.e1")
	a, told := &recorder{}, &recorder{}
	subscribeT(t, c, seqs(a), SubscribeOptions{From: from(1), Filter: &EventFilter{Actions: []string{"a"}}, OnResync: func(g StreamGap) error {
		told.add("%s %s>%s", g.Reason, g.PreviousLogID, g.LogID)
		return nil
	}})
	s.append("a")
	s.append("a")
	waitFor(t, "1,2", func() bool { return a.len() == 2 })
	s.restore(0) // a restore: another history under e2 (the open stream stays on e1)
	s.append("a")
	s.setLog("sp.e2")
	opens := len(s.openList())
	subscribeT(t, c, seqs(&recorder{}), SubscribeOptions{Filter: &EventFilter{Actions: []string{"b"}}}) // a new union: the stream restarts
	waitFor(t, "the restart", func() bool { return len(s.openList()) > opens })
	waitFor(t, "told", func() bool { return told.len() == 1 })
	want(t, told, "restored sp.e1>sp.e2")
}

// D55 (R-C26): a subscriber "from now" gets its start and its log from one answer (LogPosition),
// so a log that changes before the hub's first stream opens is told, not taken as the same.
func TestSubscribeFromNowSeesALogChangeBeforeTheFirstStream(t *testing.T) {
	s, c := newStub(t)
	for i := 0; i < 5; i++ {
		s.append("a")
	}
	s.setLog("sp.e1")
	told := &recorder{}
	var once sync.Once
	s.mu.Lock()
	s.beforeOpen = func() { once.Do(func() { s.logID = "sp.e2"; s.events = s.events[:2] }) } // put back between the two requests
	s.mu.Unlock()
	subscribeT(t, c, seqs(&recorder{}), SubscribeOptions{OnResync: func(g StreamGap) error {
		told.add("%s %s>%s %d->%d", g.Reason, g.PreviousLogID, g.LogID, g.From, g.ResumeAt)
		return nil
	}})
	waitFor(t, "told", func() bool { return told.len() == 1 })
	want(t, told, "restored sp.e1>sp.e2 6->3")
	if n := s.stats.Load(); n > 1 { // the restored gap's kept-range check is the only stats read
		t.Fatalf("the start comes from the plain read, not stats: %d stats reads", n)
	}
}

// D57 Q1: a follow that starts at a kept position (nothing delivered yet) reads the event
// there from the first stream's log, so a restart before its first event skips nothing.
func TestFollowForeverFromAPositionAcrossARestart(t *testing.T) {
	s, c := newStub(t)
	s.setLog("sp.e1")
	for i := 0; i < 3; i++ {
		s.append("a")
	}
	r := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opened := make(chan struct{}, 8)
	done := make(chan error, 1)
	go func() {
		done <- c.FollowForever(4, func(e wire.Event) error { r.add("ev %d", e.Seq); return nil }, &FollowForeverOptions{
			Context: ctx, MinBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond, OnOpen: func() { opened <- struct{}{} },
			OnResync: func(g StreamGap) error { r.add("resync %s", g.Reason); return nil }})
	}()
	<-opened
	waitFor(t, "the event at 3 read", func() bool { return s.reads.Load() >= 1 })
	s.kick()
	s.setLog("sp.e2") // a restart: the same history
	s.append("a")     // 4
	waitFor(t, "4", func() bool { return r.len() >= 1 })
	cancel()
	<-done
	want(t, r, "ev 4")
}

// P2-1: histories that change at every connection (each restored, each shorter) never make a
// hot loop: a second restart with nothing delivered between waits its back-off.
func TestFollowForeverRestartsWithBackoff(t *testing.T) {
	s, c := newStub(t)
	s.setLog("sp.e0")
	for i := 0; i < 5; i++ {
		s.append("a")
	}
	var n atomic.Int64
	s.mu.Lock()
	s.beforeOpen = func() { // every stream another log and another history, its head 1 or 3 in turn
		k := n.Add(1)
		s.logID = fmt.Sprintf("sp.e%d", k)
		s.events = nil
		for i := 0; i < 1+2*int(k%2); i++ {
			s.stamp += 7
			s.events = append(s.events, wire.Event{Seq: uint64(i + 1), Action: "a", Entity: "x", Actor: "t", At: fmt.Sprintf("2026-11-06T00:01:%02d.%06dZ", s.stamp%60, s.stamp)})
		}
	}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	c.FollowForever(5, func(wire.Event) error { return nil }, &FollowForeverOptions{Context: ctx, MinBackoff: 50 * time.Millisecond, MaxBackoff: 100 * time.Millisecond,
		LogID: "sp.e0", LastEvent: &EventMark{Seq: 4, Hash: "the event at 4 that no later log holds"}, OnResync: func(StreamGap) error { return nil }})
	if k := n.Load(); k > 20 {
		t.Fatalf("%d stream opens in 0.6 s: a hot loop", k)
	}
}

// R-C28 P2-7: the hub restarts its stream (a subscriber joins) with the event it last
// delivered, so a new log id over the same history met in that restart is no restore.
func TestSubscribeRestartOverTheSameHistoryIsNoGap(t *testing.T) {
	s, c := newStub(t)
	s.setLog("sp.e1")
	a, told := &recorder{}, &recorder{}
	subscribeT(t, c, seqs(a), SubscribeOptions{From: from(1), Filter: &EventFilter{Actions: []string{"a"}}, OnResync: func(g StreamGap) error {
		told.add("%s", g.Reason)
		return nil
	}})
	s.append("a")
	s.append("a")
	waitFor(t, "1,2", func() bool { return a.len() == 2 })
	s.setLog("sp.e2") // a restart: the same history under a new id
	opens := len(s.openList())
	subscribeT(t, c, seqs(&recorder{}), SubscribeOptions{Filter: &EventFilter{Actions: []string{"b"}}})
	waitFor(t, "the restart", func() bool { return len(s.openList()) > opens })
	s.append("a")
	waitFor(t, "3", func() bool { return a.len() == 3 })
	if told.len() != 0 {
		t.Fatalf("a restart over the same history was told as a gap: %q", told.snapshot())
	}
}

// R-C28 P2-5: a lag (a node that names no log, its head below ours) keeps the event this
// follow saw, so the new id met next over the same history is no restore.
func TestFollowForeverALagKeepsItsEvent(t *testing.T) {
	s, c := newStub(t)
	s.setLog("sp.e1")
	for i := 0; i < 5; i++ {
		s.append("a")
	}
	r := &recorder{}
	stop := follow(t, c, r, FollowForeverOptions{OnResync: func(g StreamGap) error { r.add("resync %s", g.Reason); return nil }})
	waitFor(t, "five", func() bool { return r.len() == 5 })
	s.mu.Lock()
	all := s.events
	s.events, s.logID = all[:2], "" // a lagging node that names no log
	s.mu.Unlock()
	opens := len(s.openList())
	s.kick()
	// D60 GA-2 (model A-K3): no id while an id is held, with a lower head, waits: no gap,
	// no replay.
	waitFor(t, "the lagging open", func() bool { return len(s.openList()) > opens })
	s.mu.Lock()
	s.events, s.logID = all, "sp.e2" // caught up, under a new id: the same history
	s.mu.Unlock()
	s.kick()
	s.append("a") // 6
	waitFor(t, "6", func() bool {
		for _, l := range r.snapshot() {
			if l == "ev 6" {
				return true
			}
		}
		return false
	})
	stop()
	for _, l := range r.snapshot() {
		if l == "resync restored" || l == "resync behind" {
			t.Fatalf("the same history after a lag was told as a gap: %q", r.snapshot())
		}
	}
}

// R-C30 N3: the hub takes a new log id only once its follow checked it. A subscriber joining
// while the check of a restore is out restarts the stream, and the restore is still told.
func TestSubscribeTakesTheIDOnlyAfterTheCheck(t *testing.T) {
	s, c := newStub(t)
	s.setLog("sp.e1")
	a, told := &recorder{}, &recorder{}
	subscribeT(t, c, seqs(a), SubscribeOptions{From: from(1), Filter: &EventFilter{Actions: []string{"a"}}, OnResync: func(g StreamGap) error {
		told.add("%s", g.Reason)
		return nil
	}})
	s.append("a")
	s.append("a")
	waitFor(t, "1,2", func() bool { return a.len() == 2 })
	hold := make(chan struct{})
	s.mu.Lock()
	s.holdPlain = hold
	s.mu.Unlock()
	reads := s.reads.Load()
	s.restore(0)
	s.append("a") // another history
	s.setLog("sp.e2")
	s.kick() // the follow reopens on e2 and its check (a plain read) is held
	waitFor(t, "the check out", func() bool { return len(s.openList()) >= 2 })
	time.Sleep(20 * time.Millisecond)
	subscribeT(t, c, seqs(&recorder{}), SubscribeOptions{Filter: &EventFilter{Actions: []string{"b"}}}) // the hub restarts
	s.mu.Lock()
	s.holdPlain = nil
	s.mu.Unlock()
	close(hold)
	waitFor(t, "told", func() bool { return told.len() >= 1 })
	if got := told.snapshot(); got[0] != "restored" {
		t.Fatalf("told %q (reads %d)", got, reads)
	}
}

// R-C30 N4: events from a stream that names no log (an election) don't move the mark, so
// the next named id is checked at the last event of the checked log, and a restore served in
// that window is told.
func TestFollowForeverAnUnnamedStreamDoesNotMoveTheMark(t *testing.T) {
	s, c := newStub(t)
	s.setLog("sp.e1")
	for i := 0; i < 3; i++ {
		s.append("a")
	}
	r := &recorder{}
	stop := follow(t, c, r, FollowForeverOptions{OnResync: func(g StreamGap) error { r.add("resync %s", g.Reason); return nil }})
	waitFor(t, "three", func() bool { return r.len() == 3 })
	var once sync.Once
	s.mu.Lock()
	s.beforeOpen = func() { // at the next open: a restore, served in an election (no id)
		once.Do(func() {
			s.events = s.events[:1]
			for i := 2; i <= 4; i++ { // 2', 3', 4': another history, past the position
				s.stamp += 11
				s.events = append(s.events, wire.Event{Seq: uint64(i), Action: "a", Entity: "x", Actor: "t", At: fmt.Sprintf("2026-11-06T00:02:%02d.%06dZ", s.stamp%60, s.stamp)})
			}
			s.logID = ""
		})
	}
	s.mu.Unlock()
	s.kick()
	waitFor(t, "4 from the unnamed stream", func() bool {
		for _, l := range r.snapshot() {
			if l == "ev 4" {
				return true
			}
		}
		return false
	})
	s.setLog("sp.e2") // the new leader names the restored log
	s.kick()
	waitFor(t, "told", func() bool {
		for _, l := range r.snapshot() {
			if l == "resync restored" {
				return true
			}
		}
		return false
	})
	stop()
}

// R-C30 (D59): an event check answered by a node that names no log, or another, decides
// nothing: asked again, a restart over the same history is no restore.
func TestFollowForeverAnUnconfirmedCheckIsAskedAgain(t *testing.T) {
	s, c := newStub(t)
	s.setLog("sp.e1")
	for i := 0; i < 3; i++ {
		s.append("a")
	}
	r := &recorder{}
	stop := follow(t, c, r, FollowForeverOptions{OnResync: func(g StreamGap) error { r.add("resync %s", g.Reason); return nil }})
	waitFor(t, "three", func() bool { return r.len() == 3 })
	var answers atomic.Int64
	s.mu.Lock()
	s.plainID = func() string { // the first check reaches a node that names no log
		if answers.Add(1) == 1 {
			return ""
		}
		return "sp.e2"
	}
	s.logID = "sp.e2" // a restart: the same history
	s.mu.Unlock()
	s.kick()
	s.append("a") // 4
	waitFor(t, "4", func() bool { return r.len() >= 4 })
	stop()
	for _, l := range r.snapshot() {
		if strings.HasPrefix(l, "resync restored") {
			t.Fatalf("an unconfirmed check was taken for a restore: %q", r.snapshot())
		}
	}
}

// R-C30: the first reconnect after a stream ends is jittered, so the followers of a node that
// went away do not all come back in the same instant.
func TestReconnectAfterAStreamEndsIsJittered(t *testing.T) {
	min := 400 * time.Millisecond
	seen := map[time.Duration]bool{}
	for range 50 {
		d := backoff(0, min, time.Minute)
		if d < min/4 || d > 3*min/4 {
			t.Fatalf("%v outside [%v, %v]", d, min/4, 3*min/4)
		}
		seen[d] = true
	}
	if len(seen) < 10 {
		t.Fatalf("%d distinct waits in 50: not jittered", len(seen))
	}
}

// D60 DA-3 (model A-S12, A-S13, the Go hub twin): the hub's restart mark moves only on events
// of a stream that names the log it checked. A stream that names none delivers another
// history's events; a subscriber then joins (the hub restarts), and the next new id must be
// checked against the last checked event, so the restore is told, never gone on silently.
func TestSubscribeAnUnnamedStreamDoesNotMoveTheHubsMark(t *testing.T) {
	s, c := newStub(t)
	s.setLog("sp.e1")
	a, told := &recorder{}, &recorder{}
	subscribeT(t, c, seqs(a), SubscribeOptions{From: from(1), Filter: &EventFilter{Actions: []string{"a"}}, OnResync: func(g StreamGap) error {
		told.add("%s %d", g.Reason, g.From)
		return nil
	}})
	for i := 0; i < 3; i++ {
		s.append("a")
	}
	waitFor(t, "1-3", func() bool { return a.len() == 3 })
	s.mu.Lock()
	s.events, s.logID = s.events[:1], "" // restored to 1, during an election
	s.mu.Unlock()
	opens := len(s.openList())
	s.kick()
	waitFor(t, "the unnamed open", func() bool { return len(s.openList()) > opens })
	s.append("a") // 2, another history's, written again
	s.append("a")
	s.append("a") // 4: delivered on the stream that names no log
	waitFor(t, "the unnamed 4", func() bool { return a.len() == 4 })
	opens = len(s.openList())
	subscribeT(t, c, seqs(&recorder{}), SubscribeOptions{Filter: &EventFilter{Actions: []string{"b"}}}) // the hub restarts its stream
	waitFor(t, "the restart", func() bool { return len(s.openList()) > opens })
	s.setLog("sp.e2")
	s.kick()
	waitFor(t, "told", func() bool { return told.len() >= 1 })
	if got := told.snapshot(); !strings.HasPrefix(got[0], "restored") {
		t.Fatalf("told %q: the hub checked the new id against an unchecked event", got)
	}
}

package client

// testdata/follow-vectors.json (D60 GA-7): Model A of STATE-MODEL-restore-and-kept.md, one row
// per pair, run here by FollowForever with OnResync (eplus) and without (eminus) against a
// scripted stub server. Each stream open waits for the row's next open step, so every open
// answers exactly what the row says. FAIL-SAFE: httptest on 127.0.0.1 only; every follow is
// stopped and every stream closed by t.Cleanup.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ab0t-com/acp/sdk/go/pkg/wire"
)

type followVecStart struct {
	P uint64  `json:"p"`
	L string  `json:"L"`
	M *uint64 `json:"m"`
	B uint64  `json:"B"`
}

type followVecOpen struct {
	ID       string `json:"id"`
	Head     uint64 `json:"head"`
	PosFails bool   `json:"posFails"`
	StreamID string `json:"streamId"`
}

type followVecChk struct {
	Answer      string         `json:"answer"`
	At          *uint64        `json:"at"`
	Head        *uint64        `json:"head"`
	ReadsDuring string         `json:"readsDuring"`
	Expect      *followVecWant `json:"expect"`
}

type followVecStep struct {
	Open *followVecOpen `json:"open"`
	Chk  *followVecChk  `json:"chk"`
	Ev   *struct {
		From uint64 `json:"from"`
		To   uint64 `json:"to"`
	} `json:"ev"`
	End     bool `json:"end"`
	Restart *struct {
		Head uint64 `json:"head"`
	} `json:"restart"`
}

type followVecWant struct {
	EPlus *struct {
		Gaps []struct {
			Reason   string `json:"reason"`
			From     uint64 `json:"from"`
			ResumeAt uint64 `json:"resumeAt"`
		} `json:"gaps"`
		Delivered string `json:"delivered"`
	} `json:"eplus"`
	EMinus *struct {
		Delivered string `json:"delivered"`
		Stopped   bool   `json:"stopped"`
		From      uint64 `json:"from"`
	} `json:"eminus"`
	M  json.RawMessage `json:"m"`
	ID *string         `json:"id"`
}

type followVecRow struct {
	ID    string          `json:"id"`
	About string          `json:"about"`
	Start followVecStart  `json:"start"`
	Steps []followVecStep `json:"steps"`
	Want  followVecWant   `json:"want"`
}

// readFollowVectors finds acp/testdata by walking up (the SDK copy reads the same table).
func readFollowVectors(t *testing.T) []followVecRow {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		b, err := os.ReadFile(filepath.Join(dir, "testdata", "follow-vectors.json"))
		if err == nil {
			var doc struct {
				Rows []followVecRow `json:"rows"`
			}
			if err := json.Unmarshal(b, &doc); err != nil {
				t.Fatal(err)
			}
			if len(doc.Rows) < 58 {
				t.Fatalf("a lost table, not a passing one: %d rows", len(doc.Rows))
			}
			return doc.Rows
		}
		if m, merr := os.ReadFile(filepath.Join(dir, "go.mod")); merr == nil && strings.Contains(string(m), "module github.com/ab0t-com/acp\n") {
			t.Fatalf("testdata/follow-vectors.json not found under %s", dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("the table lives in the acp source tree (acp/testdata)")
		}
		dir = parent
	}
}

// vecEvent is the server's event at seq: replayed ones are of kind x.replay, new ones
// (an ev step) file.edited; "other" is another history's event there.
func vecEvent(seq uint64, action, hist string) wire.Event {
	sec := 0
	if hist != "h1" {
		sec = 30
	}
	return wire.Event{Seq: seq, Action: action, Entity: "x", Actor: "t", At: fmt.Sprintf("2026-11-06T00:00:%02d.%09dZ", sec, seq)}
}

// vecStub is the scripted server of one row.
type vecStub struct {
	mu       sync.Mutex
	log      []wire.Event
	ever     map[uint64]wire.Event // every event this history had at a seq, cut back or not
	id       string
	chk      *followVecChk // the answer to the next event check
	tokens   chan struct{} // one per open step: a stream open waits for one
	waiting  atomic.Int64  // stream opens waiting for a token
	live     []*vecStream
	activity atomic.Int64
}

type vecStream struct {
	from uint64
	ch   chan wire.Event
	done chan struct{}
}

func (s *vecStub) head() uint64 { return uint64(len(s.log)) }

func (s *vecStub) setHead(h uint64) {
	for uint64(len(s.log)) > h {
		s.log = s.log[:len(s.log)-1]
	}
	for uint64(len(s.log)) < h {
		seq := uint64(len(s.log)) + 1
		e, ok := s.ever[seq]
		if !ok {
			e = vecEvent(seq, "x.replay", "h1")
			s.ever[seq] = e
		}
		s.log = append(s.log, e)
	}
}

func newVecStub(t *testing.T, start followVecStart) (*vecStub, *Client) {
	t.Helper()
	s := &vecStub{tokens: make(chan struct{}, 8), ever: map[uint64]wire.Event{}}
	n := start.P
	if start.M != nil && *start.M > n {
		n = *start.M
	}
	s.setHead(n)
	s.id = start.L
	j := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/stats", func(w http.ResponseWriter, r *http.Request) {
		s.activity.Add(1)
		s.mu.Lock()
		out := map[string]any{"events": s.head(), "event_base_seq": 1}
		if s.id != "" {
			out["log_id"] = s.id
		}
		s.mu.Unlock()
		j(w, out)
	})
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		s.activity.Add(1)
		defer s.activity.Add(1)
		q := r.URL.Query()
		from, _ := strconv.ParseUint(q.Get("from"), 10, 64)
		if q.Get("follow") != "true" {
			s.mu.Lock()
			head, id := s.head(), s.id
			var e *wire.Event
			if from >= 1 && from <= head {
				ev := s.log[from-1]
				e = &ev
			}
			if c := s.chk; c != nil && from < pastTheHead {
				s.chk = nil
				switch c.Answer {
				case "same": // the event this history had there (another node may hold it still)
					if ev, ok := s.ever[from]; ok {
						e = &ev
					}
					if c.At != nil && *c.At != from && e != nil {
						o := vecEvent(from, e.Action, "h2")
						e = &o
					}
				case "other":
					if e != nil {
						o := vecEvent(from, e.Action, "h2")
						e = &o
					} else {
						o := vecEvent(from, "x.replay", "h2")
						e = &o
					}
				case "missing":
					e = nil
				case "fail":
					s.mu.Unlock()
					w.WriteHeader(500)
					return
				case "noid":
					id = ""
				case "otherid":
					id = "E3"
				}
				if c.Head != nil {
					head = *c.Head
				}
			}
			s.mu.Unlock()
			if id != "" {
				w.Header().Set(wire.HeaderLogID, id)
				w.Header().Set(wire.HeaderLogHead, strconv.FormatUint(head, 10))
			}
			out := []wire.Event{}
			if e != nil {
				out = append(out, *e)
			}
			j(w, out)
			return
		}
		s.waiting.Add(1)
		select {
		case <-s.tokens:
			s.waiting.Add(-1)
		case <-r.Context().Done():
			s.waiting.Add(-1)
			return
		}
		st := &vecStream{from: from, ch: make(chan wire.Event, 512), done: make(chan struct{})}
		s.mu.Lock()
		if s.id != "" {
			w.Header().Set(wire.HeaderLogID, s.id)
		}
		w.Header().Set(wire.HeaderLogHead, strconv.FormatUint(s.head(), 10))
		var replay []wire.Event
		for _, e := range s.log {
			if e.Seq >= from {
				replay = append(replay, e)
			}
		}
		s.live = append(s.live, st)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(200)
		fl, _ := w.(http.Flusher)
		enc := json.NewEncoder(w)
		fl.Flush()
		for _, e := range replay {
			enc.Encode(e)
		}
		fl.Flush()
		s.activity.Add(1)
		for {
			select {
			case e := <-st.ch:
				enc.Encode(e)
				fl.Flush()
				s.activity.Add(1)
			case <-st.done:
				return
			case <-r.Context().Done():
				return
			}
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	t.Cleanup(s.endAll)
	c, err := New(ts.URL, "", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	return s, c
}

func (s *vecStub) endAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.live {
		close(st.done)
	}
	s.live = nil
}

func (s *vecStub) append(from, to uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for seq := from; seq <= to; seq++ {
		e := vecEvent(seq, "file.edited", "h1")
		s.ever[seq] = e
		s.log = append(s.log, e)
		for _, st := range s.live {
			if seq >= st.from {
				st.ch <- e
			}
		}
	}
}

// vecFollower is one FollowForever over the stub, with what it was told.
type vecFollower struct {
	mu        sync.Mutex
	delivered []uint64
	gaps      []StreamGap
	gapsSince int // gaps told since the last token
	id        string
	stopped   error
	done      chan struct{}
	activity  atomic.Int64
}

func (f *vecFollower) snapshot() ([]uint64, []StreamGap, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uint64(nil), f.delivered...), append([]StreamGap(nil), f.gaps...), f.id
}

// settle waits until the stub and the follower are quiet, giving a token to a stream that
// opens again at once after a told gap (the same open step).
func settle(s *vecStub, f *vecFollower) {
	quiet, last := 0, int64(-1)
	for i := 0; i < 400 && quiet < 8; i++ {
		time.Sleep(5 * time.Millisecond)
		f.mu.Lock()
		restart := f.gapsSince > 0 && s.waiting.Load() > 0
		if restart {
			f.gapsSince = 0
		}
		f.mu.Unlock()
		if restart {
			s.tokens <- struct{}{}
		}
		now := s.activity.Load() + f.activity.Load()
		if now == last && !restart {
			quiet++
		} else {
			quiet, last = 0, now
		}
	}
}

func runFollowVecRow(t *testing.T, row followVecRow, plus bool) {
	s, c := newVecStub(t, row.Start)
	f := &vecFollower{id: row.Start.L}
	var lastMark *EventMark // what a caller keeps with its position: the last event delivered
	if row.Start.M != nil {
		s.mu.Lock()
		lastMark = &EventMark{Seq: *row.Start.M, Hash: EventHash(s.log[*row.Start.M-1])}
		s.mu.Unlock()
	}
	var cancel context.CancelFunc
	start := func(from uint64, logID string, last *EventMark) {
		ctx, stop := context.WithCancel(context.Background())
		cancel = stop
		done := make(chan struct{})
		f.mu.Lock()
		f.done = done
		f.mu.Unlock()
		opts := &FollowForeverOptions{Context: ctx, LogID: logID, LastEvent: last, MinBackoff: 2 * time.Millisecond, MaxBackoff: 4 * time.Millisecond,
			onLogChecked: func(id string) { f.mu.Lock(); f.id = id; f.mu.Unlock() }}
		if plus {
			opts.OnResync = func(g StreamGap) error {
				f.activity.Add(1)
				f.mu.Lock()
				f.gaps = append(f.gaps, g)
				f.gapsSince++
				f.mu.Unlock()
				return nil
			}
		}
		go func() {
			defer close(done)
			err := c.FollowForever(from, func(e wire.Event) error {
				f.activity.Add(1)
				f.mu.Lock()
				f.delivered = append(f.delivered, e.Seq)
				lastMark = &EventMark{Seq: e.Seq, Hash: EventHash(e)}
				f.mu.Unlock()
				return nil
			}, opts)
			f.mu.Lock()
			f.stopped = err
			f.mu.Unlock()
		}()
	}
	start(row.Start.P+1, row.Start.L, lastMark)
	t.Cleanup(func() {
		cancel()
		s.endAll()
		f.mu.Lock()
		d := f.done
		f.mu.Unlock()
		<-d
	})
	for i, st := range row.Steps {
		switch {
		case st.Open != nil:
			if st.Open.PosFails || st.Open.StreamID != "" {
				t.Skipf("%s: a mount-only step", row.ID)
			}
			s.mu.Lock()
			s.id = st.Open.ID
			s.setHead(st.Open.Head)
			if i+1 < len(row.Steps) && row.Steps[i+1].Chk != nil {
				s.chk = row.Steps[i+1].Chk
			}
			s.mu.Unlock()
			s.tokens <- struct{}{}
			f.mu.Lock()
			f.gapsSince = 0
			f.mu.Unlock()
			settle(s, f)
		case st.Chk != nil:
			if e := st.Chk.Expect; e != nil && e.ID != nil {
				if _, _, id := f.snapshot(); id != *e.ID {
					t.Fatalf("%s step %d: id %q, want %q", row.ID, i, id, *e.ID)
				}
			}
		case st.Ev != nil:
			s.mu.Lock()
			h := s.head()
			s.mu.Unlock()
			if st.Ev.From != h+1 {
				t.Fatalf("%s step %d: ev from %d over a log at %d", row.ID, i, st.Ev.From, h)
			}
			s.append(st.Ev.From, st.Ev.To)
			settle(s, f)
		case st.End:
			s.endAll()
			settle(s, f)
		case st.Restart != nil:
			// The caller stops its follow and starts it again from what it kept: its position
			// (the last seq delivered), the log id and the last event. The server's head is
			// elsewhere now; nothing may be skipped or taken from it.
			cancel()
			s.endAll()
			f.mu.Lock()
			d := f.done
			f.mu.Unlock()
			<-d
			got, _, id := f.snapshot()
			from := row.Start.P + 1
			if n := len(got); n > 0 {
				from = got[n-1] + 1
			}
			f.mu.Lock()
			last := lastMark
			f.mu.Unlock()
			s.mu.Lock()
			s.setHead(st.Restart.Head)
			s.mu.Unlock()
			start(from, id, last)
		}
	}
	got, gaps, id := f.snapshot()
	f.mu.Lock()
	stopped := f.stopped
	f.mu.Unlock()
	want := row.Want
	if plus {
		if d := ranges(got); d != want.EPlus.Delivered {
			t.Errorf("delivered %q, want %q", d, want.EPlus.Delivered)
		}
		var g, w []string
		for _, x := range gaps {
			g = append(g, fmt.Sprintf("%s %d->%d", x.Reason, x.From, x.ResumeAt))
		}
		for _, x := range want.EPlus.Gaps {
			w = append(w, fmt.Sprintf("%s %d->%d", x.Reason, x.From, x.ResumeAt))
		}
		if !slices.Equal(g, w) {
			t.Errorf("gaps %q, want %q", g, w)
		}
	} else {
		if d := ranges(got); d != want.EMinus.Delivered {
			t.Errorf("delivered %q, want %q", d, want.EMinus.Delivered)
		}
		var lc *LogChangedError
		if (stopped != nil && errors.As(stopped, &lc)) != want.EMinus.Stopped {
			t.Errorf("stopped %v, want %v", stopped, want.EMinus.Stopped)
		} else if lc != nil && lc.Gap.From != want.EMinus.From {
			t.Errorf("stopped with From %d, want %d", lc.Gap.From, want.EMinus.From)
		}
	}
	if want.ID != nil && id != *want.ID {
		t.Errorf("id %q, want %q", id, *want.ID)
	}
}

// ranges is seqs as "a-b,c" (sorted, unique runs).
func ranges(seqs []uint64) string {
	var out []string
	for i := 0; i < len(seqs); {
		j := i
		for j+1 < len(seqs) && seqs[j+1] == seqs[j]+1 {
			j++
		}
		if i == j {
			out = append(out, strconv.FormatUint(seqs[i], 10))
		} else {
			out = append(out, fmt.Sprintf("%d-%d", seqs[i], seqs[j]))
		}
		i = j + 1
	}
	return strings.Join(out, ",")
}

func TestFollowVectors(t *testing.T) {
	for _, row := range readFollowVectors(t) {
		for _, plus := range []bool{true, false} {
			kind := map[bool]string{true: "eplus", false: "eminus"}[plus]
			if (plus && row.Want.EPlus == nil) || (!plus && row.Want.EMinus == nil) {
				continue
			}
			t.Run(row.ID+"/"+kind, func(t *testing.T) {
				t.Parallel()
				runFollowVecRow(t, row, plus)
			})
		}
	}
}

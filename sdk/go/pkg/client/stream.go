package client

// FollowForever and Subscribe: the event stream with its resume work done for
// you. FollowForever reconnects by itself and says when the resume point is
// gone. Subscribe runs many subscribers over one shared stream. The TypeScript
// SDK has the same two (Client.followForever, Client.subscribe).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ab0t-com/acp/sdk/go/pkg/wire"
)

// The reasons a resume point can be gone.
const (
	// GapCompacted: history retention removed the events from From up to
	// Base-1; you will not see them.
	GapCompacted = "compacted"
	// GapBehind: the server's log head is below From-1 (its history was
	// restored to an earlier point); delivery restarts at Head+1.
	GapBehind = "behind"
	// GapUnknown: the kept range could not be read after a reconnect; treat it
	// as a gap.
	GapUnknown = "unknown"
	// GapRestored: the server's log id is not the one this position belongs to
	// (D55: a restart, a leader change, or a restored server; the id cannot tell
	// them apart, so every one is told). The events from From on may be other
	// events now: re-read what you derive from them. Delivery goes on at Head+1,
	// the new head (From when the head is unknown). The TypeScript SDK's
	// "restored" gap, the same rule.
	GapRestored = "restored"
)

// StreamGap says a follower's resume point is no longer in the server's history.
type StreamGap struct {
	From     uint64 // the seq the stream asked to resume from
	Reason   string // GapCompacted, GapBehind, GapRestored or GapUnknown
	Base     uint64 // the lowest seq the server still keeps (0 = unknown)
	Head     int64  // the server's log head at the check (-1 = unknown)
	ResumeAt uint64 // where delivery continues
	// GapRestored: the log id From belonged to, and the server's log id now.
	PreviousLogID, LogID string
}

// StreamOpen is what the server said when it accepted a stream: its log id ("" when it
// gives none) and its head when the stream began (-1 when unknown).
type StreamOpen struct {
	LogID string
	Head  int64
}

// FollowForeverOptions tunes FollowForever. A nil pointer means all defaults.
type FollowForeverOptions struct {
	// Context stops the follow: FollowForever then returns nil. nil = never.
	Context context.Context
	// Filter selects events, as in FollowFiltered. nil = every event.
	Filter *EventFilter
	// OnOpen fires each time the daemon accepts the stream.
	OnOpen func()
	// OnStreamOpen, when set, gets the log id and head the server's answer carried, each
	// time it accepts the stream: keep the log id with a position you store, and pass it
	// back as LogID when you resume from that position.
	OnStreamOpen func(StreamOpen)
	// LogID is the server's log id from belongs to (from an earlier OnStreamOpen), when you
	// resume a position you kept. "" = none known: the first stream's log is taken as it.
	LogID string
	// onLogChecked, when set, gets the stream's log id once this follow has checked it (the
	// hub keeps it across its restarts from here, never from the stream-open hook).
	onLogChecked func(id string)
	// onMark, when set, gets each event that moves this follow's mark: one from a stream that
	// names the log the follow has checked (the hub's restart mark, D60 DA-3).
	onMark func(e wire.Event)
	// LastEvent is the event your position follows (seq from-1) as its seq and EventHash,
	// kept with LogID when you resume a position you kept (the TypeScript SDK's lastEvent).
	// On a new log id the follow asks whether the event at its position is still that one
	// (D57): the same event is the same history (a restart or a failover), and the follow goes
	// on with no gap; another event, or none, is a restore. nil with a LogID the first stream
	// does not name = unknown: a new log id is then a restore.
	LastEvent *EventMark
	// OnRetry fires before each wait after a failed attempt (observability).
	OnRetry func(err error, attempt int, nextFrom uint64)
	// OnClose fires when the daemon closed a healthy stream cleanly.
	OnClose func(nextFrom uint64)
	// OnResync runs BEFORE the next event is delivered when the resume point is
	// gone, or the server's history was replaced (GapRestored: a new log id whose event
	// at this follow's position is another one, or none, D55/D57): re-read whatever
	// snapshot you derive from the events. Events that follow may already be in
	// what you re-read, so apply them idempotently. An error from it ends the
	// follow, like one from fn. With it set, every (re)connect reads the server's
	// kept range once. With OnResync nil, no kept range is read, and a replaced
	// history ends the follow with a *LogChangedError: the stream never goes on
	// silently on another history. A new log id over the same history (a restart, a
	// leader change) is neither: the follow goes on with no gap and no skip.
	OnResync func(StreamGap) error
	// MinBackoff and MaxBackoff bound the reconnect wait (defaults 500ms, 15s).
	MinBackoff, MaxBackoff time.Duration
}

// FollowForever streams events from `from` onward, reconnecting and resuming
// from the last delivered seq with exponential backoff. It runs until the
// Context ends (it then returns nil), fn or OnResync returns an error (it
// returns that error), or this login reaches its end (it returns that refusal,
// IsGrantExpired: no reconnect can cure it). Every other failure is the
// stream's and is retried, so watch OnRetry if another permanent refusal (a
// revoked login) matters to you. That includes a workspace that does not exist
// yet (IsSpaceNotFound): a follower may start before the first write, and it then
// delivers from the first event.
//
// A resume point can be gone: retention removed old events while you were
// away, and the server then streams only what it still keeps, silently. Set
// OnResync to be told.
func (c *Client) FollowForever(from uint64, fn func(wire.Event) error, opts *FollowForeverOptions) error {
	if opts == nil {
		opts = &FollowForeverOptions{}
	}
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	minWait, maxWait := opts.MinBackoff, opts.MaxBackoff
	if minWait <= 0 {
		minWait = 500 * time.Millisecond
	}
	if maxWait <= 0 {
		maxWait = 15 * time.Second
	}
	if from == 0 {
		from = 1 // seqs start at 1; 0 is "from the start"
	}
	next, attempt := from, 0
	unfiltered := opts.Filter.empty()
	logID := opts.LogID // the log the position is on (D55); "" until a server names one
	// The event at this follow's position (D57 Q1): the last one delivered, or the one at
	// from-1, by its digest. markEvent is kept whole and hashed only when a check needs it.
	markSeq, markDigest := from-1, ""
	if opts.LastEvent != nil {
		markSeq, markDigest = opts.LastEvent.Seq, opts.LastEvent.Hash
	}
	var markEvent *wire.Event
	digest := func() string {
		if markEvent != nil {
			markDigest, markEvent = EventHash(*markEvent), nil
		}
		return markDigest
	}
	baselineTried := false
	for {
		if ctx.Err() != nil {
			return nil
		}
		openedAt := next
		progressed, restart, baseKnown, firstSeen := false, false, true, false
		var callerErr, checkErr error
		conn, cancel := context.WithCancel(ctx) // one per connection: a "behind" gap restarts the stream
		streamLog, streamHead := "", int64(-1)
		err := c.followStream(next, opts.Filter, &FollowOptions{
			Context: conn,
			OnStreamOpen: func(o StreamOpen) {
				streamLog, streamHead = o.LogID, o.Head
				if opts.OnStreamOpen != nil {
					opts.OnStreamOpen(o)
				}
			},
			OnOpen: func() {
				if opts.OnOpen != nil {
					opts.OnOpen()
				}
				// The log this stream is on is the one its own answer names (D55); stats
				// never decide it (one rule with the TypeScript SDK).
				prev := logID
				newID := prev != "" && streamLog != "" && streamLog != prev
				if !newID && streamLog != "" && markSeq > 0 && digest() == "" && !baselineTried {
					// The position's event, from this same log, to check against later.
					baselineTried = true
					if e, id, err := c.EventAt(ctx, markSeq); err == nil && id == streamLog && e != nil {
						markDigest = EventHash(*e)
					}
				}
				restored := false
				if newID {
					same, err := c.continues(ctx, markSeq, digest(), streamLog, streamHead)
					if err != nil { // can't tell now: this connection ends, and the next asks again
						checkErr = err
						cancel()
						return
					}
					restored = !same
				}
				if streamLog != "" {
					logID = streamLog
					if opts.onLogChecked != nil {
						opts.onLogChecked(logID) // taken only now, after the check (R-C30 N3)
					}
				}
				if opts.OnResync == nil {
					if restored { // never silently on another history: say it, and stop
						callerErr = &LogChangedError{Gap: restoredGap(min(openedAt, markSeq+1), streamHead, prev, streamLog)}
						cancel()
					}
					return
				}
				var gap *StreamGap
				if restored {
					head := streamHead
					if head < 0 { // a server that sent no head: ask once
						if st, err := c.statsCtx(ctx); err == nil {
							if n, ok := st["events"].(float64); ok {
								head = int64(n)
							}
						}
					}
					g := restoredGap(min(openedAt, markSeq+1), head, prev, streamLog) // R-A9: from the last checked event on
					gap = &g
				} else {
					// Checked AFTER the stream opened, so a compaction just before the open
					// is caught. The check can only over-report (a needless re-read), never
					// miss a gap. No event is decoded until it returns.
					st, err := c.statsCtx(ctx)
					if err != nil {
						gap, baseKnown = &StreamGap{From: openedAt, Reason: GapUnknown, Head: -1, ResumeAt: openedAt}, false
					} else {
						// A lower head waits, with no gap and no replay, when this follow holds a
						// log the open does not contradict: the same id, none (unknown, R-A3), or a
						// new id just checked as the same history. The first id ever is not
						// known to hold this position: a lower head under it is "behind" (D60
						// GA-2, GA-5).
						gap, baseKnown = rangeGap(st, openedAt, prev != "" && (streamLog == "" || streamLog == prev || newID))
					}
				}
				if gap == nil || ctx.Err() != nil {
					return
				}
				if gap.ResumeAt != openedAt && (gap.Reason == GapBehind || gap.Reason == GapRestored) {
					next, restart = gap.ResumeAt, true
					if gap.Reason == GapRestored {
						// Another history: its event is the one before ResumeAt, read again. A
						// lag (GapBehind) keeps the event this follow saw: it is the same
						// history's, and a later new id is checked against it (R-C28 P2-5).
						markSeq, markEvent, markDigest, baselineTried = gap.ResumeAt-1, nil, "", false
					}
					cancel()
				}
				if callerErr = opts.OnResync(*gap); callerErr != nil {
					cancel()
				}
			},
		}, func(e wire.Event) error {
			if restart || callerErr != nil || checkErr != nil {
				return errStopStream
			}
			if !firstSeen {
				firstSeen = true
				// A server that does not report its kept range: an UNFILTERED stream has
				// no holes, so a first event past the resume point means the ones before
				// it are gone.
				if opts.OnResync != nil && !baseKnown && unfiltered && e.Seq > openedAt {
					if callerErr = opts.OnResync(StreamGap{From: openedAt, Reason: GapCompacted, Base: e.Seq, Head: -1, ResumeAt: e.Seq}); callerErr != nil {
						return errStopStream
					}
				}
			}
			next, progressed, attempt = e.Seq+1, true, 0
			// The mark moves only on a stream that names the log this follow has checked (R-C30
			// N4): events from a stream that names none (an election) may be another history's,
			// and the next named id is checked against the last event of a named one.
			if streamLog != "" && streamLog == logID {
				ev := e
				markSeq, markEvent, markDigest = e.Seq, &ev, ""
				if opts.onMark != nil {
					opts.onMark(e)
				}
			}
			if callerErr = fn(e); callerErr != nil {
				return errStopStream
			}
			return nil
		})
		cancel()
		switch {
		case callerErr != nil:
			return callerErr
		case ctx.Err() != nil:
			return nil
		case restart && (progressed || attempt == 0):
			attempt = 1 // resume at the server's head at once; a second restart with nothing between waits
			continue
		case checkErr != nil:
			attempt++
			if opts.OnRetry != nil {
				opts.OnRetry(checkErr, attempt, next)
			}
		case restart:
			attempt++
		case err == nil || errors.Is(err, io.EOF):
			if !progressed {
				attempt++
			}
			if opts.OnClose != nil {
				opts.OnClose(next)
			}
		case IsGrantExpired(err):
			return err
		default:
			attempt++
			if opts.OnRetry != nil {
				opts.OnRetry(err, attempt, next)
			}
		}
		if !sleepCtx(ctx, backoff(attempt, minWait, maxWait)) {
			return nil
		}
	}
}

// errStopStream ends one connection from inside the event callback.
var errStopStream = errors.New("stop")

func backoff(attempt int, minWait, maxWait time.Duration) time.Duration {
	if attempt == 0 {
		// Jittered (R-C30): every stream on a node ends at a leader change, and its
		// followers must not all come back in the same instant.
		return time.Duration(float64(minWait/2) * (0.5 + rand.Float64()))
	}
	shift := attempt
	if shift > 10 {
		shift = 10
	}
	wait := minWait << shift
	if wait > maxWait {
		wait = maxWait
	}
	return time.Duration(float64(wait) * (0.5 + rand.Float64()/2))
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// streamGap is ResumeCheck with no log id known.
func (c *Client) streamGap(ctx context.Context, from uint64) (*StreamGap, bool) {
	g, known, _ := c.ResumeCheck(ctx, from, "")
	return g, known
}

// LogChangedError ends a FollowForever with no OnResync when the server's log id changed
// (D55): events may have been replaced, and a follower that cannot re-read must not go on as
// if nothing happened. Re-read what you hold, then follow again from Gap.ResumeAt with
// LogID: Gap.LogID. Subscribe gives it to a subscriber with no OnResync, through OnError.
type LogChangedError struct{ Gap StreamGap }

func (e *LogChangedError) Error() string {
	return fmt.Sprintf("the server's event log changed (%s to %s: a restart, a leader change or a restore); events from %d may differ from what you saw: re-read what you hold, then follow again from %d with LogID %q", e.Gap.PreviousLogID, e.Gap.LogID, e.Gap.From, e.Gap.ResumeAt, e.Gap.LogID)
}

// ResumeCheck is what a follower resuming at from must know (D55). prevLog is the log id it
// was on ("" = none known). It reads GET /v1/stats once: the kept range (event_base_seq,
// events, event_log) and log_id. It returns the gap, if any; whether the base was known;
// and the log id the server reports now ("" from an older server, or one with no epoch yet).
//   - a new log id: GapRestored. Delivery goes on at the new head.
//   - the same log id and a lower head: a lagging node; no gap: wait for it.
//   - no log id, and a lower head: GapBehind, as before D55.
//
// A read that fails is an unknown gap: re-reading a snapshot is the safe direction.
func (c *Client) ResumeCheck(ctx context.Context, from uint64, prevLog string) (gap *StreamGap, baseKnown bool, logID string) {
	st, err := c.statsCtx(ctx)
	if err != nil {
		return &StreamGap{From: from, Reason: GapUnknown, Head: -1, ResumeAt: from}, false, ""
	}
	logID, _ = st["log_id"].(string)
	gap, baseKnown = gapOf(st, from, prevLog, logID)
	return gap, baseKnown, logID
}

// pastTheHead is a seq no log reaches: GET /v1/events from it answers nothing but its headers.
const pastTheHead = 1<<53 - 1

// LogPosition is where the space's event log is now, and which log that is, from ONE answer of
// the server (GET /v1/events past the head: its X-ACP-Log-Head and X-ACP-Log-Id), so the two
// always belong together (D55). A server that names no log gives logID "" and its head from
// GET /v1/stats; a workspace nothing has written to yet is at head 0. Start following at head+1
// with FollowForeverOptions.LogID = logID, and a change of log in between is told, never missed.
// The TypeScript SDK's logPosition().
func (c *Client) LogPosition(ctx context.Context) (head uint64, logID string, err error) {
	head, logID, err = c.logPosition(ctx)
	if IsSpaceNotFound(err) {
		return 0, "", nil
	}
	return head, logID, err
}

func (c *Client) logPosition(ctx context.Context) (uint64, string, error) {
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	r, err := c.req("GET", "/v1/events?from="+strconv.FormatUint(pastTheHead, 10)+"&limit=1", nil)
	if err != nil {
		return 0, "", err
	}
	resp, err := c.send(r.WithContext(rctx))
	if err != nil {
		return 0, "", err
	}
	if resp.StatusCode >= 300 {
		aerr := apiErrorFrom(resp.StatusCode, readBoundedBestEffort(resp.Body))
		resp.Body.Close()
		if !IsRouteMissing(aerr) {
			return 0, "", aerr // a server without the read (a stub, a proxy) still has stats
		}
	} else {
		id := resp.Header.Get(wire.HeaderLogID)
		h, herr := strconv.ParseUint(resp.Header.Get(wire.HeaderLogHead), 10, 64)
		resp.Body.Close()
		if id != "" && herr == nil {
			return h, id, nil
		}
	}
	st, err := c.statsCtx(ctx)
	if err != nil {
		return 0, "", err
	}
	n, ok := st["events"].(float64)
	if !ok || n < 0 {
		return 0, "", errors.New("stats: no events head in the server's response")
	}
	return uint64(n), "", nil
}

// EventAt reads the one event at seq and the log id that answer names ("" from a server that
// names none). The event is nil when the log holds none at seq (shorter, or compacted past
// it). Only the first event of the answer is read.
func (c *Client) EventAt(ctx context.Context, seq uint64) (*wire.Event, string, error) {
	e, id, _, err := c.EventAtHead(ctx, seq)
	return e, id, err
}

// EventAtHead is EventAt with the head of the log that answered (its X-ACP-Log-Head; -1 when
// the server sends none). A follower's event check uses it: a node whose head is below the
// event checked holds no event there because it is behind, which decides nothing (D60 GA-6).
func (c *Client) EventAtHead(ctx context.Context, seq uint64) (*wire.Event, string, int64, error) {
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// limit=1: a server that takes it sends one event and walks no further; one that does
	// not sends the tail, of which only the first is read (R-C28 P2-2).
	r, err := c.req("GET", "/v1/events?from="+strconv.FormatUint(seq, 10)+"&limit=1", nil)
	if err != nil {
		return nil, "", -1, err
	}
	resp, err := c.send(r.WithContext(rctx))
	if err != nil {
		return nil, "", -1, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, "", -1, apiErrorFrom(resp.StatusCode, readBoundedBestEffort(resp.Body))
	}
	id := resp.Header.Get(wire.HeaderLogID)
	head := int64(-1)
	if h, err := strconv.ParseInt(resp.Header.Get(wire.HeaderLogHead), 10, 64); err == nil {
		head = h
	}
	dec := json.NewDecoder(resp.Body)
	if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
		if err == nil {
			err = fmt.Errorf("events: the answer is not a list")
		}
		return nil, id, head, err
	}
	if !dec.More() {
		return nil, id, head, nil
	}
	var e wire.Event
	if err := dec.Decode(&e); err != nil {
		return nil, id, head, err
	}
	if e.Seq != seq {
		return nil, id, head, nil
	}
	return &e, id, head, nil
}

// continues says whether the log named now still holds, at seq, the event whose digest was
// kept (D57 Q1): the same history under a new id. Nothing seen yet (seq 0) continues; an
// unknown digest does not.
// openHead is the head the stream that named now opened at (-1 = unknown): an answer from a
// node of that log whose head is below both it and seq is behind, and decides nothing (D60
// GA-6, model A-C3b); one at or past the open's head with no event at seq is a shorter log.
func (c *Client) continues(ctx context.Context, seq uint64, digest, now string, openHead int64) (bool, error) {
	if seq == 0 {
		return true, nil
	}
	if digest == "" {
		return false, nil
	}
	e, id, head, err := c.EventAtHead(ctx, seq)
	if err != nil {
		return false, err
	}
	if id != now {
		// The answer came from a node that names no log, or another one (a balancer per
		// request, an election): it decides nothing; ask again (R-C30, D59).
		return false, &LogUnconfirmedError{Want: now, Got: id, Head: -1}
	}
	if e == nil && answerBehind(head, seq, openHead) {
		return false, &LogUnconfirmedError{Want: now, Got: id, Head: head, At: seq}
	}
	return e != nil && EventHash(*e) == digest, nil
}

// LogUnconfirmedError is an event check answered by a node that does not name the log the
// stream named: nothing is decided, and the check is asked again.
type LogUnconfirmedError struct {
	Want, Got string
	// Head and At: a node of the right log whose head (Head) is below the event checked (At)
	// answered: it is behind, and the check is asked again. Head is -1 otherwise.
	Head int64
	At   uint64
}

func (e *LogUnconfirmedError) Error() string {
	if e.Got == e.Want && e.Head >= 0 {
		return fmt.Sprintf("the event check was answered by a node of log %q at head %d, below the event %d checked: asking again", e.Got, e.Head, e.At)
	}
	return fmt.Sprintf("the event check was answered for log %q, not the stream's %q: asking again", e.Got, e.Want)
}

// answerBehind: an event check's answer (head) with no event at seq came from a node behind
// the one the stream opened on (openHead), short of seq: it decides nothing (D60 GA-6).
func answerBehind(head int64, seq uint64, openHead int64) bool {
	return head >= 0 && uint64(head) < seq && (openHead < 0 || head < openHead)
}

// restoredGap is the GapRestored gap for a position at from: delivery goes on at the new
// head (head+1), or at from when the head is unknown.
func restoredGap(from uint64, head int64, prev, now string) StreamGap {
	resume := from
	if head >= 0 {
		resume = uint64(head) + 1
	}
	return StreamGap{From: from, Reason: GapRestored, Head: head, ResumeAt: resume, PreviousLogID: prev, LogID: now}
}

func gapOf(st map[string]any, from uint64, prevLog, logID string) (gap *StreamGap, baseKnown bool) {
	head := int64(-1)
	if n, ok := st["events"].(float64); ok {
		head = int64(n)
	}
	if prevLog != "" && logID != "" && logID != prevLog {
		g := restoredGap(from, head, prevLog, logID)
		_, known := st["event_base_seq"].(float64)
		return &g, known
	}
	return rangeGap(st, from, logID != "" && logID == prevLog)
}

// rangeGap is the kept-range check for a stream at from. sameLog: the server named the log
// the position belongs to, so a lower head is a lagging node to wait for, not a gap.
func rangeGap(st map[string]any, from uint64, sameLog bool) (gap *StreamGap, baseKnown bool) {
	head := int64(-1)
	if n, ok := st["events"].(float64); ok {
		head = int64(n)
	}
	if head >= 0 && uint64(head)+1 < from && sameLog {
		_, known := st["event_base_seq"].(float64)
		return nil, known // the same log, behind us: a lagging node, catching up
	}
	base, baseKnown := st["event_base_seq"].(float64)
	// A log nothing was ever written to has nothing to miss, whatever the other two
	// say (a server older than v0.2.6 does not send it, and gets the formula below).
	empty := st["event_log"] == "empty"
	baseKnown = baseKnown || empty
	if head >= 0 && uint64(head)+1 < from {
		return &StreamGap{From: from, Reason: GapBehind, Head: head, ResumeAt: uint64(head) + 1}, baseKnown
	}
	if !baseKnown || empty {
		return nil, baseKnown
	}
	// The first seq still kept. A log compacted down to nothing reports 0: the
	// next event is then head+1.
	kept := uint64(base)
	if kept == 0 && head >= 0 {
		kept = uint64(head) + 1
	}
	if kept > from {
		return &StreamGap{From: from, Reason: GapCompacted, Base: kept, Head: head, ResumeAt: kept}, true
	}
	return nil, true
}

// statsCtx is Stats bounded by ctx and by 10 seconds: a stuck stats read must
// not hold the stream.
func (c *Client) statsCtx(ctx context.Context) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	r, err := c.req("GET", "/v1/stats", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.send(r.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, apiErrorFrom(resp.StatusCode, readBoundedBestEffort(resp.Body))
	}
	data, err := readBounded(resp.Body)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	return out, json.Unmarshal(data, &out)
}

// SubscribeOptions tunes one subscriber of Subscribe.
type SubscribeOptions struct {
	// Filter is this subscriber's own filter, matched locally. nil = every event.
	Filter *EventFilter
	// From is the first seq this subscriber wants. nil = from now on.
	From *uint64
	// OnResync runs before this subscriber's next event when its own resume
	// point is gone, or the server's log id changed (GapRestored, D55; see
	// FollowForeverOptions.OnResync). Its job, by default, is to re-read what the
	// subscriber derives from the events. With none set, a changed log id reaches
	// OnError (the process log with no OnError) as a *LogChangedError, and delivery
	// goes on at the new head: a subscriber is never moved on silently.
	OnResync func(StreamGap) error
	// OnError receives errors from fn and OnResync, which never stop the stream,
	// and once the refusal that does: this login reached its end (IsGrantExpired).
	// A subscriber that joins after that is told too. nil = the standard logger.
	OnError func(error)
}

// Subscribe adds a subscriber to the client's ONE shared event stream: many
// parts of a program (mail, chat, documents) share one connection, however many
// subscribers there are. Each has its own filter and starting point; a
// subscriber joining with an earlier From gets the backlog while the others get
// nothing twice. The stream reconnects and resumes by itself, and checks the
// server's kept history on every reconnect. It stops for good when this login
// reaches its end: see SubscribeOptions.OnError.
//
// fn runs in log order, one event at a time across all subscribers: hand long
// work off rather than doing it in fn. Unsubscribe may be called from anywhere,
// also from fn; a call already running when it is called runs to its end. The
// stream closes with the last subscriber.
func (c *Client) Subscribe(fn func(wire.Event) error, opts SubscribeOptions) (unsubscribe func()) {
	c.hubOnce.Do(func() { c.hub = &eventHub{c: c} })
	return c.hub.subscribe(fn, opts)
}

type hubSub struct {
	fn       func(wire.Event) error
	filter   *EventFilter
	onResync func(StreamGap) error
	onError  func(error)
	pos      uint64 // the next seq this subscriber needs
	hasPos   bool   // false until the stream position is known (joined "from now on")
	closed   bool
}

// eventHub is the shared stream. Every subscriber has a position, the next seq
// it needs. The hub follows ONE stream from the lowest position, with the union
// of the filters on the server side (a superset of what each wants; each
// re-filters locally). An event goes to a subscriber only if its seq is at or
// past that position, which then moves past it, so a stream restarted from an
// earlier point never delivers anything twice, and the union has no holes for
// anyone it covers.
type eventHub struct {
	c *Client

	mu        sync.Mutex
	subs      []*hubSub
	running   bool
	cancel    context.CancelFunc // stops the running stream; nil between streams
	next      uint64             // the seq after the last one the stream saw
	hasNext   bool
	streamKey string      // the union the running stream was opened with
	logID     string      // the log next belongs to (D55), kept across stream restarts
	lastEv    *wire.Event // the last event the stream delivered (D57: the restart's LastEvent)
	dirty     bool
	ended     error // the login ended: no stream will ever open again
}

func (h *eventHub) subscribe(fn func(wire.Event) error, opts SubscribeOptions) func() {
	s := &hubSub{fn: fn, onResync: opts.OnResync, onError: opts.OnError}
	if !opts.Filter.empty() {
		s.filter = opts.Filter
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case opts.From != nil:
		s.pos, s.hasPos = *opts.From, true
		if s.pos == 0 {
			s.pos = 1
		}
	case h.running && h.hasNext:
		s.pos, s.hasPos = h.next, true
	}
	h.subs = append(h.subs, s)
	if h.ended != nil {
		go h.report(s, h.ended) // never from inside this call: a caller may hold its own lock
	}
	h.reconsider()
	return func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if s.closed {
			return
		}
		s.closed = true
		for i, o := range h.subs {
			if o == s {
				h.subs = append(h.subs[:i:i], h.subs[i+1:]...)
				break
			}
		}
		h.reconsider()
	}
}

// reconsider starts, stops or restarts the stream so it covers exactly the
// current subscribers. Callers hold h.mu.
func (h *eventHub) reconsider() {
	if h.ended != nil {
		return
	}
	if len(h.subs) == 0 {
		if h.cancel != nil {
			h.cancel()
		}
		return
	}
	if !h.running {
		h.running = true
		go h.run()
		return
	}
	earlier := false
	for _, s := range h.subs {
		if !s.hasPos || (h.hasNext && s.pos < h.next) {
			earlier = true
		}
	}
	if _, key := h.union(); earlier || key != h.streamKey {
		h.dirty = true
		if h.cancel != nil {
			h.cancel() // the loop reopens from the lowest position with the new union
		}
	}
}

// union is the server-side filter covering every subscriber (nil = unfiltered)
// and a key to compare unions. A field a subscriber leaves open is open in the
// union, because a filter is an AND over its fields. Callers hold h.mu.
func (h *eventHub) union() (*EventFilter, string) {
	chs, acts := map[string]bool{}, map[string]bool{}
	anyCh, anyAct := false, false
	for _, s := range h.subs {
		if s.filter == nil || len(s.filter.Channels) == 0 {
			anyCh = true
		} else {
			for _, p := range s.filter.Channels {
				chs[p] = true
			}
		}
		if s.filter == nil || len(s.filter.Actions) == 0 {
			anyAct = true
		} else {
			for _, p := range s.filter.Actions {
				acts[p] = true
			}
		}
	}
	f := &EventFilter{}
	if !anyCh {
		f.Channels = sortedKeys(chs)
	}
	if !anyAct {
		f.Actions = sortedKeys(acts)
	}
	if f.empty() {
		return nil, "*"
	}
	return f, strings.Join(f.Channels, ",") + "\x00" + strings.Join(f.Actions, ",")
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// retryPause is how long the hub waits after a failure that is not the stream's
// own (reading the head when nobody has a position yet).
var retryPause = time.Second

func (h *eventHub) run() {
	for {
		h.mu.Lock()
		if len(h.subs) == 0 {
			h.running, h.hasNext, h.logID, h.lastEv = false, false, "", nil
			h.mu.Unlock()
			return
		}
		h.dirty = false
		need := false
		for _, s := range h.subs {
			need = need || !s.hasPos
		}
		if need {
			start, ok := h.next, h.hasNext
			if !ok {
				h.mu.Unlock()
				// The head and the log it belongs to, from one answer (D55): a change of log
				// before the stream opens is then told. A workspace that does not exist yet
				// is at head 0: its first event is seq 1, so nothing is missed.
				head, logID, err := h.c.LogPosition(context.Background())
				if IsGrantExpired(err) {
					h.end(err)
					return
				}
				if err != nil {
					h.mu.Lock()
					subs := append([]*hubSub(nil), h.subs...)
					h.mu.Unlock()
					for _, s := range subs {
						h.report(s, err)
					}
					time.Sleep(retryPause)
					continue
				}
				start = head + 1
				h.mu.Lock()
				if h.logID == "" {
					h.logID = logID
				}
			}
			for _, s := range h.subs {
				if !s.hasPos {
					s.pos, s.hasPos = start, true
				}
			}
		}
		if h.dirty || len(h.subs) == 0 {
			h.mu.Unlock()
			continue
		}
		filter, key := h.union()
		start := h.subs[0].pos
		for _, s := range h.subs {
			if s.pos < start {
				start = s.pos
			}
		}
		h.next, h.hasNext, h.streamKey = start, true, key
		logID := h.logID
		// The event the restarted stream follows, when the hub saw it: a new log id met in
		// the restart is then checked, not taken for a restore (R-C28 P2-7).
		var last *EventMark
		if h.lastEv != nil && h.lastEv.Seq+1 == start {
			last = &EventMark{Seq: h.lastEv.Seq, Hash: EventHash(*h.lastEv)}
		}
		ctx, cancel := context.WithCancel(context.Background())
		h.cancel = cancel
		h.mu.Unlock()

		// deliver and resync never return an error, so this ends when ctx does or
		// when the login does. The log id goes from one stream to the next (a new
		// subscriber restarts the stream), so a restore between them is still seen.
		err := h.c.FollowForever(start, h.deliver, &FollowForeverOptions{Context: ctx, Filter: filter, OnResync: h.resync, LogID: logID, LastEvent: last,
			onLogChecked: func(id string) { // only once the follow checked it (R-C30 N3)
				h.mu.Lock()
				h.logID = id
				h.mu.Unlock()
			},
			onMark: func(e wire.Event) { // only an event of a stream that names the checked log (D60 DA-3)
				h.mu.Lock()
				h.lastEv = &e
				h.mu.Unlock()
			}})
		cancel()
		h.mu.Lock()
		h.cancel = nil
		h.mu.Unlock()
		if err != nil {
			h.end(err)
			return
		}
	}
}

// end stops the hub for good and tells every subscriber once. It runs on the hub's
// goroutine, as its last act.
func (h *eventHub) end(err error) {
	h.mu.Lock()
	h.ended, h.running, h.hasNext, h.logID = err, false, false, ""
	subs := append([]*hubSub(nil), h.subs...)
	h.mu.Unlock()
	for _, s := range subs {
		h.report(s, err)
	}
}

func (h *eventHub) deliver(e wire.Event) error {
	h.mu.Lock()
	h.next = e.Seq + 1
	subs := append([]*hubSub(nil), h.subs...)
	h.mu.Unlock()
	for _, s := range subs {
		h.mu.Lock()
		due := !s.closed && s.hasPos && e.Seq >= s.pos
		if due {
			s.pos = e.Seq + 1
		}
		h.mu.Unlock()
		if !due || (s.filter != nil && !s.filter.Match(e)) {
			continue
		}
		if err := s.fn(e); err != nil {
			h.report(s, err)
		}
	}
	return nil
}

func (h *eventHub) resync(g StreamGap) error {
	h.mu.Lock()
	if g.Reason == GapBehind || g.Reason == GapRestored {
		h.next = g.ResumeAt
	}
	if g.Reason == GapRestored {
		h.lastEv = nil // the old history's event: never a mark on the new one
	}
	subs := append([]*hubSub(nil), h.subs...)
	h.mu.Unlock()
	for _, s := range subs {
		h.mu.Lock()
		hit := !s.closed && s.hasPos
		switch g.Reason {
		case GapCompacted:
			hit = hit && s.pos < g.ResumeAt
		case GapBehind:
			hit = hit && s.pos > g.ResumeAt
		case GapRestored:
			// Every subscriber with a position is told: what it saw may have been replaced
			// (D55); each goes on at the new head.
		}
		mine := g
		mine.From = s.pos
		if g.Reason == GapRestored && g.From < s.pos {
			mine.From = g.From // R-A9: from the last checked event on, what it saw may differ
		}
		if hit && g.Reason != GapUnknown {
			s.pos = g.ResumeAt
		}
		h.mu.Unlock()
		if hit && s.onResync == nil && g.Reason == GapRestored {
			h.report(s, &LogChangedError{Gap: mine}) // never silently (D55): OnError, or the log
		}
		if !hit || s.onResync == nil {
			continue
		}
		if err := s.onResync(mine); err != nil {
			h.report(s, err)
		}
	}
	return nil
}

func (h *eventHub) report(s *hubSub, err error) {
	if s.onError != nil {
		s.onError(err)
		return
	}
	log.Printf("acp: event subscriber error: %v", err)
}

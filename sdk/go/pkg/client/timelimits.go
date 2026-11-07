package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Time limits (D54, R-C23): a server that stops answering never holds a call for ever. Every
// one-shot call is bounded twice, by default: the wait for the response headers
// (DefaultHeaderWait), and a body that stops for DefaultBodyStall. Streams (Follow,
// FollowAwareness, Subscribe) are not bound: they wait by design. The mount keeps its own
// per-call bound on top (--io-timeout). The TypeScript SDK has the same defaults.
const (
	DefaultHeaderWait = 60 * time.Second
	DefaultBodyStall  = 30 * time.Second
)

type timeLimits struct{ headers, stall time.Duration }

// SetTimeLimits sets the wait for response headers and for a stalled body of every one-shot
// call; 0 turns that limit off. Set it before the first call.
func (c *Client) SetTimeLimits(headers, stall time.Duration) {
	c.limits = timeLimits{headers: headers, stall: stall}
}

// TimeLimitError is a call ended by a time limit: no answer, or a body that stopped. The
// server may still have acted on a write: read before trying it again.
type TimeLimitError struct {
	What  string // "no response headers" or "the response body stopped"
	After time.Duration
}

func (e *TimeLimitError) Error() string {
	return fmt.Sprintf("%s within %s: the server may be stuck or far away; a write may still have been applied, so read before sending it again (Client.SetTimeLimits changes the limit)", e.What, e.After)
}

// bounded wraps a request in the limits: it returns the request to send, and finish, which
// turns the answer of hc.Do into one whose body stalls end it and whose timeouts read as a
// TimeLimitError. wrote is called when the request is written (the header limit starts
// then, so an upload's own time is not counted), gotByte on the first response byte.
func (l timeLimits) bounded(req *http.Request) (out *http.Request, wrote, gotByte func(), finish func(*http.Response, error) (*http.Response, error)) {
	if l.headers <= 0 && l.stall <= 0 {
		return req, func() {}, func() {}, func(r *http.Response, err error) (*http.Response, error) { return r, err }
	}
	ctx, cancel := context.WithCancel(req.Context())
	var mu sync.Mutex
	var why *TimeLimitError
	expire := func(e *TimeLimitError) {
		mu.Lock()
		if why == nil {
			why = e
		}
		mu.Unlock()
		cancel()
	}
	var tmu sync.Mutex
	var headerTimer *time.Timer
	answered := false
	wrote = func() {
		tmu.Lock()
		defer tmu.Unlock()
		if l.headers > 0 && headerTimer == nil && !answered {
			headerTimer = time.AfterFunc(l.headers, func() { expire(&TimeLimitError{What: "no response headers", After: l.headers}) })
		}
	}
	gotByte = func() {
		tmu.Lock()
		defer tmu.Unlock()
		answered = true
		if headerTimer != nil {
			headerTimer.Stop()
		}
	}
	reason := func(err error) error {
		mu.Lock()
		defer mu.Unlock()
		if why != nil {
			return why
		}
		return err
	}
	finish = func(resp *http.Response, err error) (*http.Response, error) {
		gotByte()
		if err != nil {
			cancel()
			return nil, reason(err)
		}
		resp.Body = &stallBody{rc: resp.Body, stall: l.stall, expire: expire, cancel: cancel, reason: reason}
		return resp, nil
	}
	return req.WithContext(ctx), wrote, gotByte, finish
}

// stallBody ends a body read that gets no byte for stall, and frees the request's context
// on Close.
type stallBody struct {
	rc     io.ReadCloser
	stall  time.Duration
	expire func(*TimeLimitError)
	cancel context.CancelFunc
	reason func(error) error
	once   sync.Once
}

func (b *stallBody) Read(p []byte) (int, error) {
	var t *time.Timer
	if b.stall > 0 {
		t = time.AfterFunc(b.stall, func() { b.expire(&TimeLimitError{What: "the response body stopped", After: b.stall}) })
	}
	n, err := b.rc.Read(p)
	if t != nil {
		t.Stop()
	}
	if err != nil && err != io.EOF {
		err = b.reason(err)
	}
	return n, err
}

func (b *stallBody) Close() error {
	err := b.rc.Close()
	b.once.Do(b.cancel)
	return err
}

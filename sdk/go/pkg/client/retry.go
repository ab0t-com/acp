package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ab0t-com/acp/sdk/go/pkg/wire"
)

// The one retry rule for a 503 (acp-1 §6.3.1). Streams keep their own reconnect
// logic and an invite redemption its own loop; every other request goes through
// send.
const (
	retry503Attempts = 3 // sends in all, the first included
	retry503MaxWait  = 10 * time.Second
)

// RetrySafe reports a 503 the server marked `retry_safe`: it did nothing, so the
// same request can be sent again. Without the mark the outcome is unknown.
func (e *APIError) RetrySafe() bool {
	if e.Status != http.StatusServiceUnavailable {
		return false
	}
	return bodyRetrySafe(e.Raw)
}

func bodyRetrySafe(body []byte) bool {
	var b wire.ErrorResponse
	return json.Unmarshal(body, &b) == nil && b.RetrySafe
}

// resendable503 says whether a 503 may be sent again: the request is known safe
// (GET, HEAD, or an Idempotency-Key) or the server marked it. Anything else may
// still commit, and a second send would land it twice.
func resendable503(req *http.Request, body []byte) bool {
	return req.Method == http.MethodGet || req.Method == http.MethodHead ||
		req.Header.Get("Idempotency-Key") != "" || bodyRetrySafe(body)
}

// retryAfterWait reads Retry-After (seconds or an HTTP date), capped at limit.
// ok is false when the header is absent or unreadable: no invitation to return.
func retryAfterWait(h http.Header, limit time.Duration) (d time.Duration, ok bool) {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0, false
	}
	if n, err := strconv.ParseUint(v, 10, 64); err == nil {
		if n > uint64(limit/time.Second) {
			return limit, true
		}
		return time.Duration(n) * time.Second, true
	} else if errors.Is(err, strconv.ErrRange) {
		return limit, true
	}
	if t, err := http.ParseTime(v); err == nil {
		return min(max(0, time.Until(t)), limit), true
	}
	return 0, false
}

// send is hc.Do with that rule, and the one for a reset (resendableReset). It returns the
// last response with its body readable, so the person sees the server's own sentence after
// the last attempt. A body that cannot be read twice is not resent.
func (c *Client) send(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	for attempt := 1; ; attempt++ {
		resp, err := c.doResetOnce(c.hc, req)
		if err != nil || resp.StatusCode != http.StatusServiceUnavailable {
			return resp, err
		}
		body := readBoundedBestEffort(resp.Body)
		resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(body))
		wait, ok := retryAfterWait(resp.Header, retry503MaxWait)
		if !ok || attempt >= retry503Attempts || !resendable503(req, body) ||
			(req.Body != nil && req.GetBody == nil) {
			return resp, nil
		}
		if dl, has := ctx.Deadline(); has && time.Until(dl) <= wait {
			return resp, nil
		}
		select {
		case <-ctx.Done():
			return resp, nil
		case <-time.After(wait):
		}
		next := req.Clone(ctx)
		if req.GetBody != nil {
			if next.Body, err = req.GetBody(); err != nil {
				return resp, nil
			}
		}
		req = next
	}
}

// doResetOnce is hc.Do with the rule for a reset: a request that is safe to send again
// (resendableReset) and failed on a pooled connection before any answer byte goes once
// more, on a fresh connection, every idle one dropped first: they may be as stale as that
// one was. Every one-shot request of this package goes through it; a stream keeps its own
// reconnect logic.
func (c *Client) doResetOnce(hc *http.Client, req *http.Request) (*http.Response, error) {
	resp, reset, err := c.sendOnce(hc, req)
	if err == nil || !reset || !resendableReset(req) {
		return resp, err
	}
	c.closeIdle()
	next := req.Clone(req.Context())
	if req.GetBody != nil {
		if next.Body, err = req.GetBody(); err != nil {
			return nil, err
		}
	}
	resp, _, err = c.sendOnce(hc, next)
	return resp, err
}

// sendOnce is hc.Do. reset is true when the request failed on a connection taken from the
// pool, by a reset or a close, before any byte of an answer arrived: the server closed the
// socket while it sat idle, just as the client reused it, and never read the request.
func (c *Client) sendOnce(hc *http.Client, req *http.Request) (resp *http.Response, reset bool, err error) {
	var reused, answered atomic.Bool
	req, wrote, gotByte, finish := c.limits.bounded(req) // D54: no call waits for ever
	trace := &httptrace.ClientTrace{
		GotConn:              func(i httptrace.GotConnInfo) { reused.Store(i.Reused) },
		WroteRequest:         func(httptrace.WroteRequestInfo) { wrote() },
		GotFirstResponseByte: func() { answered.Store(true); gotByte() },
	}
	resp, err = hc.Do(req.WithContext(httptrace.WithClientTrace(req.Context(), trace)))
	resp, err = finish(resp, err)
	return resp, err != nil && reused.Load() && !answered.Load() && isConnReset(err), err
}

// resendableReset says whether a request that met a reset may go once more: it is known
// safe (GET, HEAD, or an Idempotency-Key) and its body can be read again. Any other write
// may have reached the server before the socket went, and a second send would land it
// twice. (A server that read nothing never marks a 503 retry_safe, so that mark does not
// apply here.)
func resendableReset(req *http.Request) bool {
	safe := req.Method == http.MethodGet || req.Method == http.MethodHead || req.Header.Get("Idempotency-Key") != ""
	return safe && (req.Body == nil || req.Body == http.NoBody || req.GetBody != nil)
}

// isConnReset is a connection the other end reset or closed. A timeout is not one: the
// server may be working on the request.
func isConnReset(err error) bool {
	var ne net.Error
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return false
	}
	return errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNABORTED) || errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed)
}

// closeIdle drops the pooled connections, so the next request dials a fresh one.
func (c *Client) closeIdle() {
	if c.tr != nil {
		c.tr.CloseIdleConnections()
		return
	}
	c.hc.CloseIdleConnections()
}

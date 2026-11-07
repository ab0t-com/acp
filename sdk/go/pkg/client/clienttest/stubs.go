package clienttest

// Servers that misbehave on purpose (R-G2 e), so a client feature can be tested against a
// connection the server drops, or a server that goes quiet, without a new stub each time.
// The Go twins of the TypeScript stubs (R-T17, R-T18). Fail-safe: 127.0.0.1 httptest
// servers closed by t.Cleanup.

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

type connKey struct{}

// ResetServer answers the first request on each connection and resets the connection
// (a TCP RST, no answer) when a later request arrives on it: a server that closed an idle
// socket just as the client reused it (R-C20). With Partial, it sends part of an answer
// and then closes (a FIN, so what it sent arrives first): a response byte has arrived.
type ResetServer struct {
	URL     string
	Partial bool
	calls   atomic.Int32
	resets  atomic.Int32
	mu      sync.Mutex
	seen    map[net.Conn]int
	answer  []byte
}

// Calls is the requests that reached the server; Resets, the ones it dropped.
func (s *ResetServer) Calls() int  { return int(s.calls.Load()) }
func (s *ResetServer) Resets() int { return int(s.resets.Load()) }

// ResetOnReuse starts a ResetServer, over HTTP/2 with TLS when h2, else HTTP/1.1.
// answer is the body of every answered request.
func ResetOnReuse(t testing.TB, h2, partial bool, answer string) *ResetServer {
	t.Helper()
	s := &ResetServer{Partial: partial, seen: map[net.Conn]int{}, answer: []byte(answer)}
	ts := httptest.NewUnstartedServer(http.HandlerFunc(s.serve))
	ts.Config.ConnContext = func(ctx context.Context, c net.Conn) context.Context {
		return context.WithValue(ctx, connKey{}, c)
	}
	if h2 {
		ts.EnableHTTP2 = true
		ts.StartTLS()
	} else {
		ts.Start()
	}
	t.Cleanup(ts.Close)
	s.URL = ts.URL
	return s
}

func (s *ResetServer) serve(w http.ResponseWriter, r *http.Request) {
	s.calls.Add(1)
	c := r.Context().Value(connKey{}).(net.Conn)
	s.mu.Lock()
	s.seen[c]++
	n := s.seen[c]
	s.mu.Unlock()
	if n == 1 {
		w.Write(s.answer)
		return
	}
	s.resets.Add(1)
	raw := c
	if tc, ok := c.(*tls.Conn); ok {
		raw = tc.NetConn()
	}
	if hj, ok := w.(http.Hijacker); ok && s.Partial {
		// HTTP/1.1: part of the status line, then the close.
		if conn, _, err := hj.Hijack(); err == nil {
			conn.Write([]byte("HTTP/1.1 200 O"))
			conn.Close()
		}
		return
	}
	if s.Partial {
		// HTTP/2: the headers and part of a body, then the stream is reset. The frames go out
		// in order on the one connection, so the client has the headers first: no sleep.
		w.WriteHeader(http.StatusOK)
		w.Write(s.answer[:len(s.answer)/2])
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	}
	raw.(*net.TCPConn).SetLinger(0)
	raw.Close()
}

// StallServer accepts every request and then goes quiet: with Body false it never sends
// the headers; with Body true it sends the headers and the first bytes of the body, then
// nothing. Release ends every stalled answer (and t.Cleanup does too).
type StallServer struct {
	URL     string
	Body    bool
	release chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

// Stall starts a StallServer.
func Stall(t testing.TB, body bool) *StallServer {
	t.Helper()
	s := &StallServer{Body: body, release: make(chan struct{})}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls.Add(1)
		if s.Body {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("x"))
			w.(http.Flusher).Flush()
		}
		select {
		case <-s.release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { s.Release(); ts.Close() })
	s.URL = ts.URL
	return s
}

// Calls is the requests that reached the server.
func (s *StallServer) Calls() int { return int(s.calls.Load()) }

// Release lets every stalled answer end.
func (s *StallServer) Release() { s.once.Do(func() { close(s.release) }) }

// StalledBody is a response body that gives one byte, then nothing until Release: a
// server that stops mid-body, for code that takes an io.ReadCloser and not a URL.
type StalledBody struct {
	first   bool
	release chan struct{}
	once    sync.Once
	closed  atomic.Bool
}

// NewStalledBody makes one.
func NewStalledBody() *StalledBody { return &StalledBody{release: make(chan struct{})} }

func (b *StalledBody) Read(p []byte) (int, error) {
	if !b.first {
		b.first = true
		return copy(p, "x"), nil
	}
	<-b.release
	return 0, io.ErrClosedPipe
}

// Close records the close; it does not end a read in progress (Release does).
func (b *StalledBody) Close() error { b.closed.Store(true); return nil }

// Closed reports whether Close was called.
func (b *StalledBody) Closed() bool { return b.closed.Load() }

// Release ends the read in progress, and every later one, with an error.
func (b *StalledBody) Release() { b.once.Do(func() { close(b.release) }) }

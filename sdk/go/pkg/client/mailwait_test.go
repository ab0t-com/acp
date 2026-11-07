package client

// WaitForMail must tell "nothing yet" (ErrNoMail) from "can't tell" (the daemon's event
// stream keeps dropping): the latter is an error (verifier finding, 2026-10-29).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ab0t-com/acp/sdk/go/pkg/wire"
)

func TestWaitForMailReportsALostStream(t *testing.T) {
	mux := http.NewServeMux()
	js := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/v1/whoami", func(w http.ResponseWriter, r *http.Request) { js(w, map[string]any{"agent": "me", "role": "writer"}) })
	mux.HandleFunc("/v1/stats", func(w http.ResponseWriter, r *http.Request) { js(w, map[string]any{"events": 3}) })
	mux.HandleFunc("/v1/mail", func(w http.ResponseWriter, r *http.Request) { js(w, []any{}) })
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack() // the stream dies before a byte is sent
		if err == nil {
			conn.Close()
		}
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	c, err := New(ts.URL, "tok", "me", "", true)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	_, err = c.WaitForMail(ctx, "")
	if err == nil || errors.Is(err, ErrNoMail) {
		t.Fatalf("a stream that never stays up must be an error, not \"no mail\": %v", err)
	}
}

// D55 (R-C24 class sweep): a server that comes back on another log (here a restore to an
// earlier point, whose new mail sits under seqs behind where the wait was) makes the wait read
// the inbox again, instead of waiting for seqs that may never carry the message.
func TestWaitForMailRereadsTheInboxOnAnotherLog(t *testing.T) {
	var mu sync.Mutex
	logID, inbox, kick := "sp.e1", []any{}, make(chan struct{})
	mux := http.NewServeMux()
	js := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/v1/whoami", func(w http.ResponseWriter, r *http.Request) { js(w, map[string]any{"agent": "me", "role": "writer"}) })
	mux.HandleFunc("/v1/stats", func(w http.ResponseWriter, r *http.Request) { js(w, map[string]any{"events": 3}) })
	mux.HandleFunc("/v1/mail", func(w http.ResponseWriter, r *http.Request) { mu.Lock(); defer mu.Unlock(); js(w, inbox) })
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		id, k := logID, kick
		mu.Unlock()
		w.Header().Set(wire.HeaderLogID, id)
		w.Header().Set(wire.HeaderLogHead, "1")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		select { // no chat.sent event ever comes on the stream
		case <-k:
		case <-r.Context().Done():
		}
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	c, err := New(ts.URL, "tok", "me", "", true)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		time.Sleep(100 * time.Millisecond)
		mu.Lock()
		logID, inbox = "sp.e2", []any{map[string]any{"id": "m1", "from": "bob", "to": "me", "body": "hi"}}
		close(kick)
		kick = make(chan struct{})
		mu.Unlock()
	}()
	msgs, err := c.WaitForMail(ctx, "")
	if err != nil || len(msgs) != 1 {
		t.Fatalf("on another log the inbox is read again: %v %v", msgs, err)
	}
}

// D55 (R-C26): the wait's head and log come from one answer. When the server's log changes
// between that read and the wait's first stream, the wait reads the inbox again.
func TestWaitForMailSeesALogChangeBeforeItsFirstStream(t *testing.T) {
	var mu sync.Mutex
	reads := 0
	mux := http.NewServeMux()
	js := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/v1/whoami", func(w http.ResponseWriter, r *http.Request) { js(w, map[string]any{"agent": "me", "role": "writer"}) })
	mux.HandleFunc("/v1/mail", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if reads++; reads == 1 {
			js(w, []any{})
			return
		}
		js(w, []any{map[string]any{"id": "m1", "from": "bob", "to": "me", "body": "hi"}}) // came after the put-back
	})
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("follow") != "true" { // the head read: the log before the put-back
			w.Header().Set(wire.HeaderLogID, "sp.e1")
			w.Header().Set(wire.HeaderLogHead, "9")
			js(w, []any{})
			return
		}
		w.Header().Set(wire.HeaderLogID, "sp.e2") // the stream: the log after it
		w.Header().Set(wire.HeaderLogHead, "2")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done() // the mail's event is under seq 10: never sent from 10
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	c, err := New(ts.URL, "tok", "me", "", true)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	msgs, err := c.WaitForMail(ctx, "")
	if err != nil || len(msgs) != 1 {
		t.Fatalf("a log changed before the first stream: the inbox is read again: %v %v", msgs, err)
	}
}

// D57 Q1 / P2-1: a stream under a new log id over the same history (a restart, a failover)
// does not send the wait back to the inbox: the event at its position is the one it saw.
// Two histories in turn (each stream another log, each event another one) send it back each
// time, but never in a hot loop.
func TestWaitForMailAcrossANewLogID(t *testing.T) {
	for _, c := range []struct {
		name     string
		restored bool
		maxReads int
	}{{"the same history", false, 1}, {"two histories in turn", true, 4}} {
		t.Run(c.name, func(t *testing.T) {
			var mu sync.Mutex
			opens, reads := 0, 0
			mux := http.NewServeMux()
			js := func(w http.ResponseWriter, v any) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(v)
			}
			mux.HandleFunc("/v1/whoami", func(w http.ResponseWriter, r *http.Request) { js(w, map[string]any{"agent": "me", "role": "writer"}) })
			mux.HandleFunc("/v1/mail", func(w http.ResponseWriter, r *http.Request) { mu.Lock(); reads++; mu.Unlock(); js(w, []any{}) })
			mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				n := opens // a stream: the n-th; a plain read: the log the last stream was on
				if r.URL.Query().Get("follow") == "true" {
					opens++
				} else if n > 0 {
					n--
				}
				mu.Unlock()
				id, at := fmt.Sprintf("sp.e%d", n%2), "2026-11-06T00:00:01Z"
				if c.restored {
					at = fmt.Sprintf("2026-11-06T00:00:%02dZ", n%60) // each log its own history
				}
				w.Header().Set(wire.HeaderLogID, id)
				w.Header().Set(wire.HeaderLogHead, "5")
				if r.URL.Query().Get("follow") != "true" {
					js(w, []any{map[string]any{"seq": 5, "actor": "carol", "action": "x", "entity": "y", "at": at}})
					return
				}
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				if n == 0 || c.restored {
					return // it drops: the next names another log (with two histories, every one does)
				}
				<-r.Context().Done()
			})
			ts := httptest.NewServer(mux)
			defer ts.Close()
			cl, err := New(ts.URL, "tok", "me", "", true)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3500*time.Millisecond)
			defer cancel()
			if _, err := cl.WaitForMail(ctx, ""); !errors.Is(err, ErrNoMail) {
				t.Fatalf("no mail came: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if reads > c.maxReads {
				t.Fatalf("%d inbox reads in 3.5 s, want at most %d", reads, c.maxReads)
			}
		})
	}
}

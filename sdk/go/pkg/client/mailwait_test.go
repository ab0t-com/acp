package client

// WaitForMail must tell "nothing yet" (ErrNoMail) from "can't tell" (the daemon's event
// stream keeps dropping): the latter is an error (verifier finding, 2026-10-29).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

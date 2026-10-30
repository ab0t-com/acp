package client

// Fixes for the independent review of EXT-40 invites (F1 redirects, F2 unknown-outcome
// retries). Servers are httptest on 127.0.0.1:0.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// F1 (review R5): the redeem never follows a redirect — not to plain http, not to another
// https host — and the error names the target without its query (or any secret).
func TestRedeemNeverFollowsARedirect(t *testing.T) {
	var leaked atomic.Value
	sink := func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		leaked.Store(string(b))
		w.WriteHeader(404)
	}
	plain := httptest.NewServer(http.HandlerFunc(sink))
	defer plain.Close()
	otherTLS := httptest.NewTLSServer(http.HandlerFunc(sink))
	defer otherTLS.Close()
	for _, target := range []string{plain.URL, otherTLS.URL} {
		for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect, http.StatusFound} {
			base, pin := inviteServer(t, func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target+"/steal?k=SECRET-IN-QUERY#frag", status)
			})
			c, _ := NewTLS(base, "", "", TLSOptions{Pin: pin})
			_, err := c.Redeem(context.Background(), RedeemRequest{ID: strings.Repeat("a", 32), Secret: "SECRET-SHOULD-NOT-LEAVE-TLS", RedeemKey: "redeem-key-0123456789"})
			if s, _ := leaked.Load().(string); s != "" {
				t.Fatalf("%d to %s: the invite secret was re-sent: %s", status, target, s)
			}
			var re *RedirectError
			if !errors.As(err, &re) || re.Status != status || re.Location != target+"/steal" || strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("%d to %s: want a RedirectError naming %s/steal, got %v", status, target, target, err)
			}
		}
	}
}

// F1 swept client-wide: an ordinary authenticated call never follows https -> http (Go's
// default would re-send Authorization to the same host in clear).
func TestClientRefusesAnHTTPSDowngradeRedirect(t *testing.T) {
	var sawAuth atomic.Value
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth.Store("hit:" + r.Header.Get("Authorization"))
		io.WriteString(w, `{"agent":"x","role":"writer"}`)
	}))
	defer plain.Close()
	base, pin := inviteServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+r.URL.Path, http.StatusTemporaryRedirect)
	})
	c, _ := NewTLS(base, "bearer-token-must-stay-in-tls", "x", TLSOptions{Pin: pin})
	_, err := c.Whoami()
	if s, _ := sawAuth.Load().(string); s != "" {
		t.Fatalf("the request followed the downgrade: %s", s)
	}
	var re *RedirectError
	if !errors.As(err, &re) || strings.Contains(err.Error(), "bearer-token") {
		t.Fatalf("want a RedirectError, got %v", err)
	}
}

// F2: an unknown commit outcome (502/503/504, or a 5xx with no EXT-40 reason) is retried
// with the SAME redeem key, honouring Retry-After; a 5xx that names a refusal is final.
func TestRedeemRetriesAnUnknownOutcome(t *testing.T) {
	oldB, oldW := redeemBackoff, redeemMaxWait
	redeemBackoff, redeemMaxWait = 10*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { redeemBackoff, redeemMaxWait = oldB, oldW })
	var mu sync.Mutex
	var keys []string
	answers := []func(w http.ResponseWriter){
		func(w http.ResponseWriter) {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(503)
			io.WriteString(w, `{"error":"raft commit timed out (cluster degraded; retry — commit status unknown)"}`)
		},
		func(w http.ResponseWriter) {
			w.WriteHeader(502)
			io.WriteString(w, `{"error":"forward to leader: EOF"}`)
		},
		func(w http.ResponseWriter) { w.WriteHeader(500); io.WriteString(w, `{"error":"internal"}`) },
		func(w http.ResponseWriter) {
			json.NewEncoder(w).Encode(Redeemed{Token: "t", Name: "codexB", Space: "demo", Role: "writer", Retry: true})
		},
	}
	base, pin := inviteServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req RedeemRequest
		json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		keys = append(keys, req.RedeemKey)
		n := len(keys)
		mu.Unlock()
		answers[n-1](w)
	})
	c, _ := NewTLS(base, "", "", TLSOptions{Pin: pin})
	out, err := c.Redeem(context.Background(), RedeemRequest{ID: strings.Repeat("a", 32), Secret: "s", RedeemKey: "same-key-0123456789"})
	if err != nil || out.Token != "t" || len(keys) != 4 {
		t.Fatalf("got %+v %v after %d calls", out, err, len(keys))
	}
	for _, k := range keys {
		if k != "same-key-0123456789" {
			t.Fatalf("every retry must carry the same redeem key: %q", keys)
		}
	}
	// Persistent 503: ErrNoResponse (outcome unknown), still carrying the daemon's answer.
	calls := 0
	base2, pin2 := inviteServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(503)
		io.WriteString(w, `{"error":"no leader available"}`)
	})
	c2, _ := NewTLS(base2, "", "", TLSOptions{Pin: pin2})
	_, err = c2.Redeem(context.Background(), RedeemRequest{ID: "x", Secret: "s", RedeemKey: "k"})
	var ae *APIError
	if !errors.Is(err, ErrNoResponse) || !errors.As(err, &ae) || ae.Status != 503 || calls != redeemAttempts {
		t.Fatalf("persistent 503: %v after %d calls", err, calls)
	}
	// A 5xx that names a refusal reason is a decided answer: final.
	calls = 0
	base3, pin3 := inviteServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(500)
		io.WriteString(w, `{"error":"x","reason":"invite_malformed"}`)
	})
	c3, _ := NewTLS(base3, "", "", TLSOptions{Pin: pin3})
	if _, err := c3.Redeem(context.Background(), RedeemRequest{ID: "x", Secret: "s", RedeemKey: "k"}); errors.Is(err, ErrNoResponse) || calls != 1 {
		t.Fatalf("a reasoned 5xx must be final: %v after %d calls", err, calls)
	}
}

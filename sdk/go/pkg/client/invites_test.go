package client

// EXT-40 invite methods against a local httptest TLS server (127.0.0.1:0, a cert
// minted per test). The daemon side is faked per the spec's wire shapes.

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func inviteServer(t *testing.T, h http.HandlerFunc) (base, pin string) {
	t.Helper()
	cert := selfSigned(t, []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	srv := httptest.NewUnstartedServer(h)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv.URL, pinOf(cert)
}

// The §5.5 retry rule from the client's side: a lost response is retried with
// the SAME redeem_key, and the redeem never carries a bearer or a space.
func TestRedeemRetriesWithSameKeyAfterLostResponse(t *testing.T) {
	var mu sync.Mutex
	var keys, auths, spaces []string
	base, pin := inviteServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req RedeemRequest
		json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		keys, auths, spaces = append(keys, req.RedeemKey), append(auths, r.Header.Get("Authorization")), append(spaces, r.Header.Get("X-ACP-Space"))
		n := len(keys)
		mu.Unlock()
		if n == 1 { // applied, but the response is lost
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		json.NewEncoder(w).Encode(Redeemed{Token: "t2", Name: "codexB", Space: "demo", Role: "writer", Retry: true})
	})
	c, err := NewTLS(base, "stale-token", "someone", TLSOptions{Pin: pin})
	if err != nil {
		t.Fatal(err)
	}
	c.SetSpace("other") // a stale profile's space must not ride along
	out, err := c.Redeem(context.Background(), RedeemRequest{ID: "0123456789abcdef0123456789abcdef", Secret: "s"})
	if err != nil || !out.Retry || out.Token != "t2" {
		t.Fatalf("redeem: %+v %v", out, err)
	}
	if len(keys) != 2 || keys[0] == "" || keys[0] != keys[1] {
		t.Fatalf("the retry must reuse the redeem_key: %q", keys)
	}
	for i := range auths {
		if auths[i] != "" || spaces[i] != "" {
			t.Fatalf("redeem sent Authorization %q / X-ACP-Space %q", auths[i], spaces[i])
		}
	}
}

// A daemon answer is final (no retry), and its EXT-40 reason is readable.
func TestRedeemRefusalIsFinal(t *testing.T) {
	calls := 0
	base, pin := inviteServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(410)
		io.WriteString(w, `{"error":"this invite was already used","reason":"invite_exhausted","redemptions":[{"name":"codexB","at":"2026-10-29T14:02:31Z"}]}`)
	})
	c, _ := NewTLS(base, "", "", TLSOptions{Pin: pin})
	_, err := c.Redeem(context.Background(), RedeemRequest{ID: "x", Secret: "s"})
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 410 || ae.Reason() != "invite_exhausted" || calls != 1 {
		t.Fatalf("got %v (calls %d)", err, calls)
	}
}

func TestRedeemGivesUpAfterBoundedAttempts(t *testing.T) {
	old := redeemAttempts
	redeemAttempts = 2
	t.Cleanup(func() { redeemAttempts = old })
	calls := 0
	base, pin := inviteServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
	})
	c, _ := NewTLS(base, "", "", TLSOptions{Pin: pin})
	_, err := c.Redeem(context.Background(), RedeemRequest{ID: "x", Secret: "s", RedeemKey: "k"})
	if !errors.Is(err, ErrNoResponse) || calls != 2 {
		t.Fatalf("got %v after %d calls", err, calls)
	}
}

func TestSupportsInvites(t *testing.T) {
	for body, want := range map[string]bool{
		`{"status":"ok","protocol":"acp/1","capabilities":["scopedtokens","invites"]}`: true,
		`{"status":"ok","protocol":"acp/1","capabilities":["scopedtokens"]}`:           false,
		`{"status":"ok","protocol":"acp/2","capabilities":["invites"]}`:                false,
		`{"status":"ok"}`: false,
	} {
		base, pin := inviteServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "" {
				t.Error("the probe must not send a token")
			}
			io.WriteString(w, body)
		})
		c, _ := NewTLS(base, "", "", TLSOptions{Pin: pin})
		if got, err := c.SupportsInvites(context.Background()); err != nil || got != want {
			t.Fatalf("%s: got %v %v", body, got, err)
		}
	}
	// A pin mismatch is an error (trust failed), not "unsupported".
	base, _ := inviteServer(t, func(w http.ResponseWriter, r *http.Request) { t.Error("request reached a server whose pin failed") })
	c, _ := NewTLS(base, "", "", TLSOptions{Pin: "sha256:" + strings.Repeat("00", 32)})
	if _, err := c.SupportsInvites(context.Background()); err == nil {
		t.Fatal("a pin mismatch must fail the probe")
	}
}

// The admin calls hit the EXT-40 paths with the bearer token.
func TestInviteAdminCalls(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	base, pin := inviteServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.RequestURI()+" "+r.Header.Get("Authorization"))
		mu.Unlock()
		switch {
		case r.Method == "POST":
			var req InviteRequest
			json.NewDecoder(r.Body).Decode(&req)
			if req.Space != "demo" || req.Name != "codexB" || req.TTLSec != 0 {
				t.Errorf("create body: %+v", req)
			}
			w.WriteHeader(201)
			io.WriteString(w, `{"invite":{"id":"0123456789abcdef0123456789abcdef","space":"demo","name":"codexB","role":"writer","uses":1,"uses_left":1,"status":"pending","created_by":{"name":"claudeA"},"redemptions":[]},"secret":"AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA"}`)
		case r.Method == "GET":
			io.WriteString(w, `{"invites":[{"id":"a","status":"exhausted","redemptions":[{"name":"codexB","fingerprint":"f1","at":"2026-10-29T14:02:31Z"}]}]}`)
		case r.URL.Path == "/v1/admin/invites/a/member":
			io.WriteString(w, `{"revoked":"f1"}`)
		default:
			io.WriteString(w, `{"revoked":"a"}`)
		}
	})
	c, _ := NewTLS(base, "adm", "claudeA", TLSOptions{Pin: pin})
	inv, secret, err := c.CreateInvite(InviteRequest{Space: "demo", Name: "codexB"})
	if err != nil || secret == "" || inv.CreatedBy.Name != "claudeA" || inv.UsesLeft != 1 {
		t.Fatalf("create: %+v %q %v", inv, secret, err)
	}
	invs, err := c.Invites("demo", "exhausted")
	if err != nil || len(invs) != 1 || invs[0].Redemptions[0].Fingerprint != "f1" {
		t.Fatalf("list: %+v %v", invs, err)
	}
	if fp, err := c.RemoveInviteMember("a"); err != nil || fp != "f1" {
		t.Fatalf("remove-member: %q %v", fp, err)
	}
	if err := c.RevokeInvite("a"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"POST /v1/admin/invites Bearer adm",
		"GET /v1/admin/invites?space=demo&status=exhausted Bearer adm",
		"DELETE /v1/admin/invites/a/member Bearer adm",
		"DELETE /v1/admin/invites/a Bearer adm",
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("call %d: %q, want %q", i, seen[i], want[i])
		}
	}
}

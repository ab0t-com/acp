package client

// R-C23 P2-17, P2-18, P2-19 (final client read). FAIL-SAFE: httptest on 127.0.0.1.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A redeem whose first try went out with its answer lost, then met a revoke: the 410 stands
// (no token after a revoke) and says plainly that the first try may have made the login.
func TestRedeemRevokedAfterALostAnswerSaysSo(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 { // the answer is lost
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGone)
		w.Write([]byte(`{"error":"this invite was revoked — ask for a new one","reason":"invite_revoked","code":"invite_revoked"}`))
	}))
	t.Cleanup(ts.Close)
	c, err := New(ts.URL, "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Redeem(context.Background(), RedeemRequest{Secret: "s", Name: "bob", RedeemKey: "k1"})
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 410 || ae.Reason() != "invite_revoked" ||
		!strings.Contains(ae.Message, "may have redeemed it") || !strings.Contains(ae.Message, "Ask the admin") {
		t.Fatalf("%v", err)
	}
}

// Every coordd answers /v1/healthz: a proxy's 502 is an error, not "an older server".
func TestSupportsInvitesTellsAnErrorFromAnOlderServer(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	t.Cleanup(ts.Close)
	c, err := New(ts.URL, "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := c.SupportsInvites(context.Background()); ok || err == nil {
		t.Fatalf("a 502 read as an answer: %v %v", ok, err)
	}
}

// A redeem with no answer at all hands back the key it used, so a re-run is a retry; a TLS
// trust refusal is not sent again.
func TestRedeemKeyComesBackAndTrustIsNotRetried(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
	}))
	t.Cleanup(ts.Close)
	c, err := New(ts.URL, "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if testing.Short() {
		t.Skip("four tries with backoff")
	}
	_, err = c.Redeem(context.Background(), RedeemRequest{Secret: "s", Name: "bob"})
	var ru *RedeemUnknownError
	if !errors.As(err, &ru) || ru.RedeemKey == "" || !errors.Is(err, ErrNoResponse) || strings.Contains(err.Error(), ru.RedeemKey) {
		t.Fatalf("the key comes back in the field, never in the text (a credential, R-C27 P2-15): %v", err)
	}

	var dials int32
	tlsSrv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	tlsSrv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			atomic.AddInt32(&dials, 1)
		}
	}
	tlsSrv.StartTLS()
	t.Cleanup(tlsSrv.Close)
	strict, err := New(tlsSrv.URL, "", "", "", false) // the system roots: the test certificate is not trusted
	if err != nil {
		t.Fatal(err)
	}
	if _, err := strict.Redeem(context.Background(), RedeemRequest{Secret: "s", Name: "bob", RedeemKey: "k"}); err == nil || !trustFailure(err) {
		t.Fatalf("want the trust failure: %v", err)
	}
	if n := atomic.LoadInt32(&dials); n != 1 {
		t.Fatalf("a trust failure was retried: %d connections", n)
	}
}

// R-C27 P2-15: a 2xx whose body is cut is a try that went out (the daemon applied it); a
// revoke after it gets the "may have made your login" words too.
func TestRedeemACutAnswerThenARevokeSaysSo(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 { // a 200 whose body dies mid-read
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(200)
			w.Write([]byte(`{"token":"`))
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGone)
		w.Write([]byte(`{"error":"this invite was revoked — ask for a new one","reason":"invite_revoked","code":"invite_revoked"}`))
	}))
	t.Cleanup(ts.Close)
	c, err := New(ts.URL, "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	old := redeemBackoff
	redeemBackoff = time.Millisecond
	t.Cleanup(func() { redeemBackoff = old })
	_, err = c.Redeem(context.Background(), RedeemRequest{Secret: "s", Name: "bob", RedeemKey: "k1k1k1k1k1k1k1k1"})
	var ae *APIError
	if !errors.As(err, &ae) || !strings.Contains(ae.Message, "may have redeemed it") {
		t.Fatalf("%v", err)
	}
}

// R-C27 P2-15: a redeem the caller's context ends hands the key back, as a lost answer does.
func TestRedeemEndedByTheCallerHandsTheKeyBack(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
	}))
	t.Cleanup(ts.Close)
	c, err := New(ts.URL, "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err = c.Redeem(ctx, RedeemRequest{Secret: "s", Name: "bob"})
	var ru *RedeemUnknownError
	if !errors.As(err, &ru) || ru.RedeemKey == "" || !errors.Is(err, ErrNoResponse) {
		t.Fatalf("the key must come back: %v", err)
	}
}

// R-C27 P2-15: a certificate the profile's pin refuses is a trust failure: not sent again,
// and said as itself, never as a lost connection.
func TestRedeemPinMismatchIsNotRetried(t *testing.T) {
	var dials int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			atomic.AddInt32(&dials, 1)
		}
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	c, err := NewTLS(srv.URL, "", "", TLSOptions{Pin: "sha256:" + strings.Repeat("00", 32)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Redeem(context.Background(), RedeemRequest{Secret: "s", Name: "bob"})
	if err == nil || !strings.Contains(err.Error(), "pin mismatch") || errors.Is(err, ErrNoResponse) {
		t.Fatalf("want the pin refusal itself: %v", err)
	}
	if n := atomic.LoadInt32(&dials); n != 1 {
		t.Fatalf("%d connections: a pin refusal is sent once", n)
	}
}

// R-C28 P0-10 (fix-introduced by R-C27 P2-15): a trust failure after a try whose answer was
// lost still hands the key back: that try may have redeemed.
func TestRedeemTrustFailureAfterALostAnswerKeepsTheKey(t *testing.T) {
	var swapped atomic.Bool
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		swapped.Store(true) // the answer is lost; from now on the server shows another certificate
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
	}))
	srv.StartTLS()
	t.Cleanup(srv.Close)
	first := srv.TLS.Certificates[0]
	second := anotherCert(t) // httptest serves one certificate for every server: make another
	srv.TLS.Certificates = nil
	srv.TLS.SessionTicketsDisabled = true // each try a full handshake: the new certificate is seen
	srv.TLS.GetCertificate = func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		if swapped.Load() {
			return &second, nil
		}
		return &first, nil
	}
	sum := sha256.Sum256(first.Certificate[0])
	c, err := NewTLS(srv.URL, "", "", TLSOptions{Pin: "sha256:" + hex.EncodeToString(sum[:])})
	if err != nil {
		t.Fatal(err)
	}
	old := redeemBackoff
	redeemBackoff = time.Millisecond
	t.Cleanup(func() { redeemBackoff = old })
	c.hc.CloseIdleConnections()
	_, err = c.Redeem(context.Background(), RedeemRequest{Secret: "s", Name: "bob", RedeemKey: "k1k1k1k1k1k1k1k1"})
	var ru *RedeemUnknownError
	if !errors.As(err, &ru) || ru.RedeemKey != "k1k1k1k1k1k1k1k1" || !strings.Contains(err.Error(), "pin mismatch") {
		t.Fatalf("the key must come back with the trust failure: %v", err)
	}
}

func anotherCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(7), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// R-C30: a 429 (or any refusal that says nothing of the invite) after a try whose answer was
// lost keeps the key: the outcome is still unknown.
func TestRedeemA429AfterALostAnswerKeepsTheKey(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"slow down","code":"rate_limited"}`))
	}))
	t.Cleanup(ts.Close)
	c, err := New(ts.URL, "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	old := redeemBackoff
	redeemBackoff = time.Millisecond
	t.Cleanup(func() { redeemBackoff = old })
	_, err = c.Redeem(context.Background(), RedeemRequest{Secret: "s", Name: "bob", RedeemKey: "k2k2k2k2k2k2k2k2"})
	var ru *RedeemUnknownError
	if !errors.As(err, &ru) || ru.RedeemKey != "k2k2k2k2k2k2k2k2" {
		t.Fatalf("the key must come back: %v", err)
	}
}

// R-C30: a redirect after a try whose answer was lost keeps the key too.
func TestRedeemARedirectAfterALostAnswerKeepsTheKey(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		http.Redirect(w, r, "https://elsewhere.invalid/v1/invites/redeem", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(ts.Close)
	c, err := New(ts.URL, "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	old := redeemBackoff
	redeemBackoff = time.Millisecond
	t.Cleanup(func() { redeemBackoff = old })
	_, err = c.Redeem(context.Background(), RedeemRequest{Secret: "s", Name: "bob", RedeemKey: "k3k3k3k3k3k3k3k3"})
	var ru *RedeemUnknownError
	if !errors.As(err, &ru) || ru.RedeemKey != "k3k3k3k3k3k3k3k3" {
		t.Fatalf("the key must come back: %v", err)
	}
}

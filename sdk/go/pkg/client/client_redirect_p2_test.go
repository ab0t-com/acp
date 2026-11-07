package client

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// F-11: Health had no redirect policy: it followed a redirect out of TLS with the token.
func TestHealthNeverFollowsARedirectOutOfTLS(t *testing.T) {
	var got string
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
	}))
	defer plain.Close()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/v1/healthz", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	c, err := NewTLS(srv.URL, "SECRET", "a", TLSOptions{Insecure: true})
	if err != nil {
		t.Fatal(err)
	}
	var re *RedirectError
	if err := c.Health(); !errors.As(err, &re) {
		t.Fatalf("got %v", err)
	}
	if got != "" {
		t.Fatalf("the plain server got %q", got)
	}
}

// Go keeps Authorization on a new port of one host: no call of ours may hop there.
func TestRedirectToAnotherHostKeepsTheToken(t *testing.T) {
	var got string
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
	}))
	defer other.Close()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	c, err := NewTLS(srv.URL, "SECRET", "a", TLSOptions{Insecure: true})
	if err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() error{
		"health":   c.Health,
		"manifest": func() error { _, err := c.Manifest(); return err },
	} {
		err := call()
		if err == nil || !strings.Contains(err.Error(), "another host") {
			t.Fatalf("%s: %v", name, err)
		}
		if got != "" {
			t.Fatalf("%s: the other host got %q", name, got)
		}
	}
}

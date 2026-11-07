package client

// G-INV2c (EXT-40 §5.7): redeeming an invite that adds a workspace to a login.
// Fail-safe: httptest only.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRedeemSendsTheTokenOnlyAsMember(t *testing.T) {
	var auth atomic.Value
	c := clientOf(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.Header.Get("Authorization"))
		io.WriteString(w, `{"added":true,"name":"b","space":"p2","role":"writer","retry":false}`)
	}))
	c.token = "the-members-token"
	red, err := c.Redeem(context.Background(), RedeemRequest{ID: "i", Secret: "s", AsMember: true})
	if err != nil {
		t.Fatal(err)
	}
	if !red.Added || red.Token != "" {
		t.Fatalf("%+v", red)
	}
	if got := auth.Load(); got != "Bearer the-members-token" {
		t.Fatalf("%q", got)
	}
	if _, err = c.Redeem(context.Background(), RedeemRequest{ID: "i", Secret: "s"}); err != nil {
		t.Fatal(err)
	}
	if got := auth.Load(); got != "" {
		t.Fatalf("a plain redeem carries no token, whatever the client holds: %q", got)
	}
}

func TestRedeemWithNoTokenAndNoAddIsABrokenAnswer(t *testing.T) {
	old := redeemAttempts
	redeemAttempts = 1
	t.Cleanup(func() { redeemAttempts = old })
	c := clientOf(t, answer(200, `{"name":"b","space":"p2"}`))
	_, err := c.Redeem(context.Background(), RedeemRequest{ID: "i", Secret: "s"})
	if !errors.Is(err, ErrNoResponse) || !strings.Contains(err.Error(), "no token") {
		t.Fatalf("%v", err)
	}
}

func TestRemoveInviteMemberDetailSaysWhatWent(t *testing.T) {
	c := clientOf(t, answer(200, `{"revoked":"fp","name":"b","space":"p2"}`))
	r, err := c.RemoveInviteMemberDetail("i")
	if err != nil || r.Revoked != "fp" || r.Name != "b" || r.Space != "p2" {
		t.Fatalf("%+v %v", r, err)
	}
	if fp, err := c.RemoveInviteMember("i"); err != nil || fp != "fp" {
		t.Fatalf("%q %v", fp, err)
	}
}

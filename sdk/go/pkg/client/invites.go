package client

// Agent invites (ACP/EXT-40): an admin creates a single-use, expiring invite for
// one space; a new agent redeems it, with no token of its own, for its own
// per-agent token. These types mirror the EXT-40 Appendix A wire shapes. They
// live here (not in pkg/wire) because only the SDK speaks them; the daemon
// keeps its own copy.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ab0t-com/acp/sdk/go/pkg/wire"
)

// InviteScope narrows the grant an invite mints (EXT-40 §4.4). Spaces appears
// only in responses: an invite is always confined to its one space.
type InviteScope struct {
	Spaces     []string `json:"spaces,omitempty"`
	PathPrefix []string `json:"path_prefix,omitempty"`
	ReadPrefix []string `json:"read_prefix,omitempty"`
}

// InviteRequest is POST /v1/admin/invites (EXT-40 §5.1). Zero values are
// omitted, so the daemon applies its defaults (writer, 24 h, one use).
type InviteRequest struct {
	Space      string       `json:"space"`
	Name       string       `json:"name,omitempty"`
	NamePrefix string       `json:"name_prefix,omitempty"`
	Role       string       `json:"role,omitempty"`
	Scope      *InviteScope `json:"scope,omitempty"`
	TTLSec     int          `json:"ttl_sec,omitempty"`
	Uses       int          `json:"uses,omitempty"`
	Note       string       `json:"note,omitempty"`
	// GrantTTLSec ends each joiner's login this many seconds after they join (ext-7
	// §4.1.2; 60 to 315360000). Zero: their login does not end. A daemon older than
	// v0.2.6 ignores it, so compare it with the Invite you get back.
	GrantTTLSec int `json:"grant_ttl_sec,omitempty"`
}

// InviteRedemption is one member an invite minted (names, times, fingerprints;
// never a token or hash).
type InviteRedemption struct {
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint,omitempty"`
	At          string `json:"at"`
	Retries     int    `json:"retries,omitempty"`
}

// Invite is the InviteView of EXT-40 Appendix A. It never carries the secret.
type Invite struct {
	ID         string       `json:"id"`
	Space      string       `json:"space"`
	Name       string       `json:"name,omitempty"`
	NamePrefix string       `json:"name_prefix,omitempty"`
	Role       string       `json:"role"`
	Scope      *InviteScope `json:"scope,omitempty"`
	Uses       int          `json:"uses"`
	UsesLeft   int          `json:"uses_left"`
	CreatedAt  string       `json:"created_at"`
	ExpiresAt  string       `json:"expires_at"`
	CreatedBy  struct {
		Name        string `json:"name"`
		Fingerprint string `json:"fingerprint,omitempty"`
	} `json:"created_by"`
	Note         string             `json:"note,omitempty"`
	RevokedAt    string             `json:"revoked_at,omitempty"`
	RevokeReason string             `json:"revoke_reason,omitempty"`
	Status       string             `json:"status"` // pending | exhausted | expired | revoked
	Redemptions  []InviteRedemption `json:"redemptions"`
	Servers      []string           `json:"servers,omitempty"`       // optional operator hint
	GrantTTLSec  int                `json:"grant_ttl_sec,omitempty"` // how long each joiner's login lasts, if it ends
	AddMember    bool               `json:"add_member,omitempty"`    // Name already exists: this adds Space to its login, and only its own token can redeem it
}

// CreateInvite creates a pending invite (admin). The secret is returned ONCE:
// the caller composes the invite code from it and must not keep it.
func (c *Client) CreateInvite(req InviteRequest) (inv Invite, secret string, err error) {
	var out struct {
		Invite Invite `json:"invite"`
		Secret string `json:"secret"`
	}
	if err := c.do("POST", "/v1/admin/invites", req, &out); err != nil {
		return Invite{}, "", err
	}
	if out.Secret == "" || out.Invite.ID == "" {
		return Invite{}, "", errors.New("the daemon's create-invite response has no id or secret")
	}
	return out.Invite, out.Secret, nil
}

// Invites lists invites (admin; a scoped admin sees only its spaces). space and
// status ("pending", "exhausted", "expired", "revoked") are optional filters.
func (c *Client) Invites(space, status string) ([]Invite, error) {
	q := url.Values{}
	if space != "" {
		q.Set("space", space)
	}
	if status != "" {
		q.Set("status", status)
	}
	path := "/v1/admin/invites"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out struct {
		Invites []Invite `json:"invites"`
	}
	return out.Invites, c.do("GET", path, nil, &out)
}

// RevokeInvite stops future redeems of an invite (admin; idempotent). Members
// it already let in keep their access: see RemoveInviteMember.
func (c *Client) RevokeInvite(id string) error {
	return c.do("DELETE", "/v1/admin/invites/"+url.PathEscape(id), nil, nil)
}

// RemoveInviteMember revokes the grant an invite minted (EXT-40 §5.6) and
// returns that grant's fingerprint. RemoveInviteMemberDetail also says whether
// only a workspace went.
func (c *Client) RemoveInviteMember(id string) (fingerprint string, err error) {
	r, err := c.RemoveInviteMemberDetail(id)
	return r.Revoked, err
}

// MemberRemoval is the answer to removing an invite's member. Space is set when
// the login holds other workspaces: it lost that one and keeps working in the
// rest (EXT-40 §5.7). Empty: the login ended.
type MemberRemoval struct {
	Revoked string `json:"revoked"`
	Name    string `json:"name"`
	Space   string `json:"space"`
}

// RemoveInviteMemberDetail is RemoveInviteMember with the whole answer.
func (c *Client) RemoveInviteMemberDetail(id string) (MemberRemoval, error) {
	return c.RemoveInviteMemberNamed(id, "")
}

// RemoveInviteMemberNamed removes the member of invite id that goes by name: the way to
// answer 409 member_ambiguous, when the invite let in more than one agent. "" names none.
func (c *Client) RemoveInviteMemberNamed(id, name string) (MemberRemoval, error) {
	var out MemberRemoval
	path := "/v1/admin/invites/" + url.PathEscape(id) + "/member"
	if name != "" {
		path += "?name=" + url.QueryEscape(name)
	}
	err := c.do("DELETE", path, nil, &out)
	return out, err
}

// noRedirect is c's HTTP client with redirects refused outright: the join
// probe and the redeem must talk to exactly the URL whose trust check passed
// (EXT-40 §8.5 step 2, §11.3). A followed 307/308 would re-POST the secret and
// redeem key to a URL that never had a trust check or capability probe —
// across hosts, or from https down to plain http (review F1).
func (c *Client) noRedirect() *http.Client {
	return &http.Client{Timeout: c.hc.Timeout, Transport: c.hc.Transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// redirected turns a 3xx answer into a *RedirectError (target redacted).
func redirected(resp *http.Response) error {
	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return nil
	}
	loc, _ := resp.Location()
	return &RedirectError{Status: resp.StatusCode, Location: redactedLocation(loc)}
}

// SupportsInvites probes GET /v1/healthz (unauthenticated) for protocol
// "acp/1" and the "invites" capability (EXT-40 §7). A joining client MUST see
// true here before it sends an invite secret. Errors are transport or trust
// failures (the TLS check happens on this request), or a *RedirectError: the
// capability must come from the very server that will receive the secret, so
// a redirect is never followed.
func (c *Client) SupportsInvites(ctx context.Context) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+"/v1/healthz", nil)
	if err != nil {
		return false, err
	}
	resp, err := c.doResetOnce(c.noRedirect(), req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if err := redirected(resp); err != nil {
		return false, err
	}
	data, _ := readBounded(resp.Body)
	if resp.StatusCode != 200 {
		// Every coordd answers /v1/healthz: another answer (a proxy's 502, a 5xx) is no
		// "older server", and saying so would send the person to update coordd (R-C23 P2-18).
		return false, apiErrorFrom(resp.StatusCode, data)
	}
	var h struct {
		Protocol     string   `json:"protocol"`
		Capabilities []string `json:"capabilities"`
	}
	if err := json.Unmarshal(data, &h); err != nil {
		return false, fmt.Errorf("the server's /v1/healthz answer could not be read (%v): is this address an ACP server?", err)
	}
	if h.Protocol != wire.ProtocolVersion {
		return false, nil
	}
	for _, s := range h.Capabilities {
		if s == "invites" {
			return true, nil
		}
	}
	return false, nil
}

// RedeemRequest is POST /v1/invites/redeem (EXT-40 §5.4).
type RedeemRequest struct {
	ID        string      `json:"id"`
	Secret    string      `json:"secret"`
	Name      string      `json:"name,omitempty"`
	RedeemKey string      `json:"redeem_key,omitempty"`
	Client    *ClientInfo `json:"client,omitempty"`
	// AsMember sends this client's own token with the redeem, for an invite that
	// adds a workspace to an existing member (EXT-40 §5.7): only that member's
	// token can redeem one. Set it only on a client built for the server the
	// invite's trust check passed for: it is the one redeem that carries a token.
	AsMember bool `json:"-"`
}

// ClientInfo optionally tells the daemon which harness and client version joined
// (shown in the member.joined event).
type ClientInfo struct {
	Harness string `json:"harness,omitempty"`
	Version string `json:"version,omitempty"`
}

// Redeemed is the redeem response. Token is the member's own token, shown once.
// Added: the invite added Space to the member's existing login, so Token is empty
// and the login in use keeps working.
type Redeemed struct {
	Added       bool        `json:"added,omitempty"`
	Token       string      `json:"token"`
	Fingerprint string      `json:"fingerprint"`
	Name        string      `json:"name"`
	Space       string      `json:"space"`
	Role        string      `json:"role"`
	Scope       InviteScope `json:"scope"`
	Inviter     struct {
		Name string `json:"name"`
	} `json:"inviter"`
	Retry bool `json:"retry"`
	// ExpiresAt is when this login ends (RFC 3339). Empty: it does not end.
	ExpiresAt string `json:"expires_at,omitempty"`
}

// NewRedeemKey returns a fresh retry key (192 bits, base64url): make ONE per
// join attempt and reuse it on every retry of that attempt (EXT-40 §5.5). It is
// a credential: never print or store it.
func NewRedeemKey() string {
	b := make([]byte, 24)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// ErrNoResponse wraps a redeem whose outcome is UNKNOWN: every attempt failed
// in transport, or the daemon answered with a 5xx that does not say the redeem
// was refused (502/503/504: a lost forward to the leader or a commit timeout —
// the leader may have applied it). Retrying with the SAME RedeemKey is safe (a
// retry within the window rotates the token, it never spends a second use).
var ErrNoResponse = errors.New("no response from the server")

// redeemAttempts bounds the same-request retries on an unknown outcome (§8.5
// step 3); redeemBackoff is the base pause between them (a test seam), and
// redeemMaxWait caps an honoured Retry-After.
var (
	redeemAttempts = 4
	redeemBackoff  = time.Second
	redeemMaxWait  = 10 * time.Second
)

// redeemOutcomeUnknown: a 5xx that carries no EXT-40 refusal reason. The
// daemon's invite refusals are decided by the apply plan and always name a
// reason; a 502/503/504 (or a bare 500) means the request may have committed.
func redeemOutcomeUnknown(status int, e *APIError) bool {
	return status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout ||
		(status >= 500 && e.Reason() == "")
}

// retryAfter parses a Retry-After in seconds (0 if absent), capped at redeemMaxWait.
func retryAfter(h http.Header) time.Duration {
	d, _ := retryAfterWait(h, redeemMaxWait)
	return d
}

// Redeem presents an invite (EXT-40 §5.4). It sends NO Authorization (unless
// r.AsMember, for an invite that adds a workspace to this client's own login)
// and NO X-ACP-Space header: the secret is the credential and the space comes
// from the invite. When the outcome is unknown (a transport failure with no response, or
// a 502/503/504 / reason-less 5xx) it retries the same request (same
// redeem_key) a bounded number of times, honouring Retry-After, and then
// returns an error wrapping ErrNoResponse. A daemon refusal (an *APIError with
// a Reason) is final, and so is a redirect (*RedirectError: never followed, the
// secret goes only to the URL whose trust check passed). The caller should
// have checked SupportsInvites on this client first, and should keep
// r.RedeemKey until the join succeeds or is refused, so a re-run is a retry.
func (c *Client) Redeem(ctx context.Context, r RedeemRequest) (Redeemed, error) {
	if r.RedeemKey == "" {
		r.RedeemKey = NewRedeemKey() // returned in a RedeemUnknownError, so a re-run can be a retry (R-C23 P2-19)
	}
	unanswered := false         // a redeem went out and its answer was lost
	body, err := marshalBody(r) // invalid UTF-8 text is refused before anything is sent
	if err != nil {
		return Redeemed{}, err
	}
	hc := c.noRedirect()
	var last error
	var wait time.Duration
	for attempt := 0; attempt < redeemAttempts; attempt++ {
		if attempt > 0 {
			if wait == 0 {
				wait = time.Duration(attempt) * redeemBackoff
			}
			select {
			case <-ctx.Done():
				// The tries so far went out with their answers lost: the key goes back, so a
				// re-run is a retry of the same redeem (R-C27 P2-15).
				return Redeemed{}, &RedeemUnknownError{RedeemKey: r.RedeemKey, Err: errors.Join(ErrNoResponse, last, ctx.Err())}
			case <-time.After(wait):
			}
			wait = 0
		}
		req, err := http.NewRequestWithContext(ctx, "POST", c.base+"/v1/invites/redeem", bytes.NewReader(body))
		if err != nil {
			return Redeemed{}, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(wire.HeaderProtocol, wire.ProtocolVersion)
		if r.AsMember {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		resp, err := hc.Do(req)
		if err != nil {
			if trustFailure(err) {
				// The TLS check refused: sending again can't help (R-C23 P2-19). If an earlier
				// try went out with its answer lost, the outcome is still unknown, and the key
				// goes back with it (R-C28 P0-10).
				if unanswered {
					return Redeemed{}, &RedeemUnknownError{RedeemKey: r.RedeemKey, Err: errors.Join(ErrNoResponse, last, err)}
				}
				return Redeemed{}, err
			}
			last, unanswered = err, true
			continue
		}
		if rerr := redirected(resp); rerr != nil {
			resp.Body.Close()
			if unanswered { // an earlier try's outcome is still unknown: the key goes back (R-C30)
				return Redeemed{}, &RedeemUnknownError{RedeemKey: r.RedeemKey, Err: errors.Join(ErrNoResponse, last, rerr)}
			}
			return Redeemed{}, rerr
		}
		data, rerr := readBounded(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			ae := apiErrorFrom(resp.StatusCode, data)
			if redeemOutcomeUnknown(resp.StatusCode, ae) {
				last, wait, unanswered = ae, retryAfter(resp.Header), true
				continue
			}
			if unanswered && ae.Reason() == "invite_revoked" {
				// D54 / R-C23 P2-17: an earlier try went out and its answer was lost, and the
				// invite was revoked since. No token is ever minted after a revoke, so the
				// answer stands; but that earlier try may have made the login.
				ae.Message = "this invite was revoked after your first try, whose answer was lost: that try may have redeemed it and made your login, and its token cannot be sent now. Ask the admin to give that login a token (or a new invite); a retry cannot help"
			}
			if unanswered && !strings.HasPrefix(ae.Reason(), "invite_") {
				// A refusal that says nothing of the invite (a 429, a proxy's 4xx) after a try
				// whose answer was lost: the outcome is still unknown, and the key goes back.
				return Redeemed{}, &RedeemUnknownError{RedeemKey: r.RedeemKey, Err: errors.Join(ErrNoResponse, last, ae)}
			}
			return Redeemed{}, ae
		}
		if rerr != nil { // the body died mid-read: the daemon applied it, we hold nothing
			last, unanswered = rerr, true
			continue
		}
		var out Redeemed
		if err := json.Unmarshal(data, &out); err != nil || (out.Token == "" && !out.Added) {
			last, unanswered = errors.New("the daemon's redeem response has no token"), true
			continue
		}
		return out, nil
	}
	return Redeemed{}, &RedeemUnknownError{RedeemKey: r.RedeemKey, Err: errors.Join(ErrNoResponse, last)}
}

// RedeemUnknownError is a redeem whose outcome is unknown: no answer came. Run Redeem again
// with RedeemKey (the one it used, made for you when you gave none), and the server treats
// it as a retry of the same redeem, never a second one. It matches ErrNoResponse.
type RedeemUnknownError struct {
	RedeemKey string
	Err       error
}

// Error never prints RedeemKey: it is a credential (R-C27 P2-15). Take it from the field.
func (e *RedeemUnknownError) Error() string {
	return e.Err.Error() + " (to retry, redeem again with the same redeem key: RedeemUnknownError.RedeemKey)"
}

func (e *RedeemUnknownError) Unwrap() error { return e.Err }

// trustFailure is a TLS trust refusal: the certificate is not trusted for this address.
func trustFailure(err error) bool {
	var ua x509.UnknownAuthorityError
	var he x509.HostnameError
	var ci x509.CertificateInvalidError
	return errors.As(err, &ua) || errors.As(err, &he) || errors.As(err, &ci) || errors.Is(err, errPinMismatch)
}

// errPinMismatch is the certificate check a profile's pin refused: a trust failure, which
// no retry can cure (R-C27 P2-15).
var errPinMismatch = errors.New("pin mismatch")

// Reason is the EXT-40 §9 machine-readable "reason" token of an error body
// ("" when the daemon sent none).
func (e *APIError) Reason() string {
	var b struct {
		Reason string `json:"reason"`
	}
	json.Unmarshal(e.Raw, &b)
	return b.Reason
}

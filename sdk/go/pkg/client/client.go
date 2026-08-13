// Package client is the Go SDK for talking to coordd. A harness embeds this to
// get the shared filesystem + comms line; the acp CLI is a thin wrapper over it.
//
// Transport: HTTPS to the pinned daemon cert (pass certPath), bearer token, and
// an agent id sent on every request. All methods are safe for concurrent use.
package client

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	// Public, externally-consumable type packages (ACP/EXT-23). These are
	// aliases of the canonical definitions, so the SDK's exported signatures name
	// types a customer's own module can import and construct.
	"github.com/ab0t-com/acp/sdk/go/pkg/crdt"
	"github.com/ab0t-com/acp/sdk/go/pkg/crdtjson"
	"github.com/ab0t-com/acp/sdk/go/pkg/wire"
)

type Client struct {
	base    string
	token   string
	agent   string
	space   string // isolated space; "" => daemon default
	session string // random per-process id (collision detection)
	hc      *http.Client

	capsMu       sync.Mutex
	caps         map[string]bool // healthz capability list; nil until first probe
	warnedNoChan bool            // one-time "no channels capability" warning fired
}

// New builds a client. certPath pins the daemon's self-signed cert (recommended).
// If certPath is "" and insecure is true, TLS verification is skipped (dev only).
func New(base, token, agent, certPath string, insecure bool) (*Client, error) {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	switch {
	case certPath != "":
		pem, err := os.ReadFile(certPath)
		if err != nil {
			return nil, fmt.Errorf("read cert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("cert %s: no certificates parsed", certPath)
		}
		tlsCfg.RootCAs = pool
	case insecure:
		tlsCfg.InsecureSkipVerify = true
	default:
		return nil, fmt.Errorf("provide --cert (pin daemon cert) or --insecure")
	}
	sess := make([]byte, 8)
	rand.Read(sess)
	return &Client{
		base: base, token: token, agent: agent, session: hex.EncodeToString(sess),
		hc: &http.Client{Timeout: 0, Transport: &http.Transport{TLSClientConfig: tlsCfg, ForceAttemptHTTP2: true}},
	}, nil
}

// SetSpace selects the isolated space (filesystem + channels) this client uses.
// Empty = the daemon's default space. Returns the client for chaining.
func (c *Client) SetSpace(space string) *Client { c.space = space; return c }

// Space reports the client's current space ("" = default).
func (c *Client) Space() string { return c.space }

func (c *Client) req(method, path string, body io.Reader) (*http.Request, error) {
	r, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Authorization", "Bearer "+c.token)
	r.Header.Set(wire.HeaderAgentID, c.agent)
	r.Header.Set(wire.HeaderProtocol, wire.ProtocolVersion)
	r.Header.Set(wire.HeaderSession, c.session)
	if c.space != "" {
		r.Header.Set(wire.HeaderSpace, c.space)
	}
	return r, nil
}

// do sends a JSON request and decodes a JSON response into out (may be nil).
// maxControlResponse bounds how much of a SERVER response the SDK will buffer
// into memory for a control-plane call. Every such call funnels through
// io.ReadAll, which is unbounded: a malicious or compromised coordd could
// answer any request with a multi-gigabyte body and OOM the agent process, so
// the read is capped. That is the client-side twin of the server's own "bound
// every value from untrusted input" rule.
//
// The cap applies ONLY to buffered control responses (JSON payloads and error
// bodies). It deliberately does NOT apply to:
//   - GetBlob, which returns the body as a STREAM so large files never land in
//     memory in the first place, and
//   - Follow/FollowFiltered, which decode a long-lived event stream event by
//     event and are meant to run indefinitely.
//
// 64 MiB is far above any legitimate control payload (a full event page at the
// default 50k retention is single-digit MB) and far below "kills the host".
const maxControlResponse = 64 << 20

// controlResponseCap is the live limit readBounded enforces. It exists as a var
// solely so tests can shrink it: proving the bound with the real 64 MiB value
// costs ~577 MB of peak RSS under -race (the race detector shadows every byte
// buffered), which is an absurd price for a unit test — especially in a suite
// whose whole subject is resource bounds. It is unexported and never written
// outside tests, so production behaviour is exactly maxControlResponse.
var controlResponseCap int64 = maxControlResponse

// readBounded buffers at most controlResponseCap bytes of an untrusted response
// body. A truncated body simply fails to parse, which surfaces as the normal
// decode error — the point is that the process cannot be made to allocate
// without limit.
func readBounded(r io.Reader) []byte {
	data, _ := io.ReadAll(io.LimitReader(r, controlResponseCap))
	return data
}

func (c *Client) do(method, path string, in, out any) error {
	return c.doH(method, path, nil, in, out)
}

// doH is do with extra request headers (e.g. Idempotency-Key, F-INV-36).
func (c *Client) doH(method, path string, headers map[string]string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
		_ = body
	}
	req, err := c.req(method, path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data := readBounded(resp.Body)
	if resp.StatusCode >= 300 {
		return apiErrorFrom(resp.StatusCode, data)
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// apiErrorFrom builds the uniform *APIError from a server response (the
// standard wire.ErrorResponse body when parseable; the raw body as the message
// otherwise). EVERY server-reported failure in this SDK funnels through it —
// control calls, blob puts/gets, and the stream open paths alike — so the
// documented taxonomy (Status + Conflict/Locked/OverQuota + Current) holds on
// every method, and errors.As(*APIError) never misses because a path
// hand-rolled a fmt.Errorf.
func apiErrorFrom(status int, body []byte) *APIError {
	var e wire.ErrorResponse
	json.Unmarshal(body, &e)
	msg := e.Error
	if msg == "" {
		msg = strings.TrimSpace(string(body))
	}
	return &APIError{Status: status, Message: msg, Current: e.Current, Raw: body}
}

// doPaged is do for a paginated GET: it decodes the body as usual and also
// returns the X-ACP-Next-Cursor header ("" when the walk is complete), which is
// how the daemon hands back the next page without changing the body's shape.
func (c *Client) doPaged(method, path string, out any) (string, error) {
	req, err := c.req(method, path, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data := readBounded(resp.Body)
	if resp.StatusCode >= 300 {
		return "", apiErrorFrom(resp.StatusCode, data)
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return "", err
		}
	}
	return resp.Header.Get("X-ACP-Next-Cursor"), nil
}

// APIError carries the daemon's status + (for 409s) the current server state.
type APIError struct {
	Status  int
	Message string
	Current any
	Raw     []byte
}

func (e *APIError) Error() string  { return fmt.Sprintf("acp %d: %s", e.Status, e.Message) }
func (e *APIError) Conflict() bool { return e.Status == http.StatusConflict }
func (e *APIError) Locked() bool   { return e.Status == http.StatusLocked } // 423: resource is leased

// OverQuota reports a PERSISTENT space-quota refusal (507 Insufficient
// Storage — ext-7 §5.2: max_events, max_docs, max_blob_bytes). The condition
// holds until compaction/GC frees room or an admin raises the quota, so a
// client MUST NOT blind-retry it (unlike a 429, which is transient).
func (e *APIError) OverQuota() bool { return e.Status == http.StatusInsufficientStorage }

// --- events ---

func (c *Client) Append(e wire.Event) (wire.Event, error) {
	var out wire.Event
	return out, c.do("POST", "/v1/events", e, &out)
}

// idemHeader builds the Idempotency-Key header map (F-INV-36), or nil for "".
func idemHeader(key string) map[string]string {
	if key == "" {
		return nil
	}
	return map[string]string{"Idempotency-Key": key}
}

// AppendIdem is Append with an Idempotency-Key so a retry (network timeout, 5xx
// during a leadership failover — the ambiguous-outcome window) REPLAYS the server's
// stored result instead of double-appending (F-INV-36 / T48). Reuse the SAME key on
// every retry of one logical append.
//
// KEY HYGIENE (S-F1-W2-7 / S-F2-W2-6): reuse a key ONLY for retries of THIS exact
// call. Do NOT reuse it for a different logical operation, or a different call
// SHAPE — in particular never share one key across AppendIdem and LogBatchIdem
// (both hit /v1/events): a colliding key replays the first call's stored result and
// the other call's write is silently dropped. In a SHARED-token deployment the key
// must also be UNGUESSABLE by other holders of the token — prefer a random UUID over
// a content-derived hash, or a co-tenant who predicts it can pre-seed the cache and
// substitute their result.
func (c *Client) AppendIdem(e wire.Event, idempotencyKey string) (wire.Event, error) {
	var out wire.Event
	return out, c.doH("POST", "/v1/events", idemHeader(idempotencyKey), e, &out)
}

// LogBatch appends events as ONE atomic batch (acp-ext-4): contiguous seqs,
// all-or-nothing, one array response. The daemon's capability list is probed
// once (then cached); a daemon without "batchevents" yields an explicit error —
// LogBatch never silently degrades to N single appends (the atomicity the
// caller asked for would be lost); the CALLER decides whether to fall back.
func (c *Client) LogBatch(events []wire.Event) ([]wire.Event, error) {
	ok, err := c.hasCapability("batchevents")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("daemon lacks batchevents capability (older coordd, or started with -max-batch 0)")
	}
	var out []wire.Event
	return out, c.do("POST", "/v1/events", events, &out)
}

// LogBatchIdem is LogBatch with an Idempotency-Key (F-INV-36): a retry of the same
// batch replays the stored result instead of appending the batch twice.
//
// KEY HYGIENE (S-F1-W2-7 / S-F2-W2-6): see AppendIdem — reuse a key ONLY for retries
// of THIS exact batch, never across a single AppendIdem and a batch, and make it
// unguessable in shared-token deployments.
func (c *Client) LogBatchIdem(events []wire.Event, idempotencyKey string) ([]wire.Event, error) {
	if ok, err := c.hasCapability("batchevents"); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("daemon lacks batchevents capability (older coordd, or started with -max-batch 0)")
	}
	var out []wire.Event
	return out, c.doH("POST", "/v1/events", idemHeader(idempotencyKey), events, &out)
}

// hasCapability probes GET /v1/healthz for the daemon's advertised capability
// list, once per client (cached). Daemons predating the capability mechanism
// simply have no list — every capability reads as absent.
func (c *Client) hasCapability(name string) (bool, error) {
	c.capsMu.Lock()
	defer c.capsMu.Unlock()
	if c.caps == nil {
		req, err := http.NewRequest("GET", c.base+"/v1/healthz", nil)
		if err != nil {
			return false, err
		}
		resp, err := c.hc.Do(req)
		if err != nil {
			return false, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return false, fmt.Errorf("healthz probe: %d", resp.StatusCode)
		}
		var m struct {
			Capabilities []string `json:"capabilities"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
			return false, fmt.Errorf("healthz probe: %w", err)
		}
		c.caps = map[string]bool{}
		for _, s := range m.Capabilities {
			c.caps[s] = true
		}
	}
	return c.caps[name], nil
}

func (c *Client) ReadEvents(from uint64) ([]wire.Event, error) {
	var out []wire.Event
	return out, c.do("GET", "/v1/events?from="+strconv.FormatUint(from, 10), nil, &out)
}

// Follow streams events from `from`, calling fn for each. Blocks until ctx-less
// stop: it returns when the connection closes; callers loop+reconnect with the
// last Seq for resilience.
func (c *Client) Follow(from uint64, fn func(wire.Event) error) error {
	req, err := c.req("GET", "/v1/events?follow=true&from="+strconv.FormatUint(from, 10), nil)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data := readBounded(resp.Body)
		return apiErrorFrom(resp.StatusCode, data) // uniform taxonomy on stream open too
	}
	dec := json.NewDecoder(resp.Body)
	for {
		var e wire.Event
		if err := dec.Decode(&e); err != nil {
			return err // EOF / connection drop — caller reconnects
		}
		if err := fn(e); err != nil {
			return err
		}
	}
}

// EventFilter selects events by channel and/or action patterns (acp-ext-1
// §4.3): each element is a literal or a trailing-".*" prefix pattern; within
// one field patterns OR-combine, and an event must match BOTH fields when both
// are set. The reserved channel pattern "_" matches only unlabeled events.
// Filtered streams see seq GAPS by design (they are a subsequence of the log);
// resume with from=<last-received-seq+1> and the SAME filter.
type EventFilter struct {
	Channels []string // patterns on Event.Channel
	Actions  []string // patterns on Event.Action
}

func (f *EventFilter) empty() bool {
	return f == nil || (len(f.Channels) == 0 && len(f.Actions) == 0)
}

// query renders the filter's query-string suffix ("" when empty).
func (f *EventFilter) query() string {
	if f.empty() {
		return ""
	}
	q := url.Values{}
	if len(f.Channels) > 0 {
		q.Set("channels", strings.Join(f.Channels, ","))
	}
	if len(f.Actions) > 0 {
		q.Set("actions", strings.Join(f.Actions, ","))
	}
	return "&" + q.Encode()
}

// Match applies the filter locally with the daemon's own matcher
// (wire.MatchPattern), so client- and server-side filtering can never diverge.
func (f *EventFilter) Match(e wire.Event) bool {
	if f.empty() {
		return true
	}
	if len(f.Channels) > 0 {
		ok := false
		for _, p := range f.Channels {
			if p == wire.UnlabeledPattern {
				if e.Channel == "" {
					ok = true
					break
				}
				continue
			}
			if wire.MatchPattern(p, e.Channel) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	for _, p := range f.Actions {
		if wire.MatchPattern(p, e.Action) {
			return true
		}
	}
	return len(f.Actions) == 0
}

// warnIfNoChannels fires a one-time advisory when the daemon does not
// advertise "channels" (ext-1 §4.5): an old daemon ignores filter parameters
// and returns EVERYTHING, which degrades bandwidth but not correctness — the
// filtered methods always re-filter locally with the same matcher. So this
// warns, never fails (contrast LogBatch, where degrading would forfeit
// atomicity and IS an error).
func (c *Client) warnIfNoChannels() {
	ok, err := c.hasCapability("channels")
	if err != nil || ok {
		return // probe failures surface on the real request; nothing to warn about
	}
	c.capsMu.Lock()
	warned := c.warnedNoChan
	c.warnedNoChan = true
	c.capsMu.Unlock()
	if !warned {
		log.Printf("acp: daemon lacks the channels capability; filtering client-side (the full stream crosses the wire)")
	}
}

// EventsFiltered reads events with a server-side channel/action filter and
// re-filters the result locally (harmless when the daemon already filtered;
// correctness-preserving when an old daemon returned everything). A nil/empty
// filter is exactly ReadEvents.
func (c *Client) EventsFiltered(from uint64, f *EventFilter) ([]wire.Event, error) {
	if f.empty() {
		return c.ReadEvents(from)
	}
	c.warnIfNoChannels()
	var out []wire.Event
	if err := c.do("GET", "/v1/events?from="+strconv.FormatUint(from, 10)+f.query(), nil, &out); err != nil {
		return nil, err
	}
	kept := out[:0]
	for _, e := range out {
		if f.Match(e) {
			kept = append(kept, e)
		}
	}
	return kept, nil
}

// FollowFiltered is Follow with a channel/action filter; fn sees only matching
// events (local re-filter covers old daemons). A nil/empty filter is exactly
// Follow. Callers resume with from=<last seq fn saw>+1 and the SAME filter.
func (c *Client) FollowFiltered(from uint64, f *EventFilter, fn func(wire.Event) error) error {
	if f.empty() {
		return c.Follow(from, fn)
	}
	c.warnIfNoChannels()
	req, err := c.req("GET", "/v1/events?follow=true&from="+strconv.FormatUint(from, 10)+f.query(), nil)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data := readBounded(resp.Body)
		return apiErrorFrom(resp.StatusCode, data) // uniform taxonomy on stream open too
	}
	dec := json.NewDecoder(resp.Body)
	for {
		var e wire.Event
		if err := dec.Decode(&e); err != nil {
			return err // EOF / connection drop — caller reconnects
		}
		if !f.Match(e) {
			continue // old daemon streaming unfiltered: drop here, same semantics
		}
		if err := fn(e); err != nil {
			return err
		}
	}
}

// ChannelInfo is one row of the daemon's derived channel registry (ext-1 §4.4).
type ChannelInfo struct {
	Name     string `json:"name"`
	Declared bool   `json:"declared"`
	LastSeq  uint64 `json:"last_seq"`
	LastAt   string `json:"last_at,omitempty"`
}

// Channels lists the space's channels (implicitly created by first use).
func (c *Client) Channels() ([]ChannelInfo, error) {
	var out []ChannelInfo
	return out, c.do("GET", "/v1/channels", nil, &out)
}

// --- mail ---

func (c *Client) Send(m wire.Message) (wire.Message, error) {
	var out wire.Message
	return out, c.do("POST", "/v1/mail", m, &out)
}

// SendIdem is Send with an Idempotency-Key (F-INV-36): reuse the same key on retry
// so an ambiguous failure (timeout / failover 5xx) replays instead of double-sending.
//
// KEY HYGIENE (S-F1-W2-7 / S-F2-W2-6): see AppendIdem — reuse a key ONLY for retries
// of THIS exact send, never for a different operation, and make it unguessable in
// shared-token deployments (a co-tenant who predicts it can substitute their result).
func (c *Client) SendIdem(m wire.Message, idempotencyKey string) (wire.Message, error) {
	var out wire.Message
	return out, c.doH("POST", "/v1/mail", idemHeader(idempotencyKey), m, &out)
}
func (c *Client) Inbox(unreadOnly bool) ([]wire.Message, error) {
	var out []wire.Message
	q := "/v1/mail"
	if unreadOnly {
		q += "?unread=true"
	}
	return out, c.do("GET", q, nil, &out)
}
func (c *Client) Ack(id string) error {
	return c.do("POST", "/v1/mail/ack", map[string]string{"id": id}, nil)
}
func (c *Client) Thread(id string) ([]wire.Message, error) {
	var out []wire.Message
	return out, c.do("GET", "/v1/mail/thread?id="+id, nil, &out)
}

// --- leases ---

func (c *Client) AcquireLease(resource string, ttlSec int) (wire.Lease, error) {
	return c.AcquireLeaseLabeled(resource, ttlSec, "")
}

// AcquireLeaseLabeled acquires with an OPTIONAL ext-7 §6 sub_scope label
// ("" = none). A sub_scope-pinned grant's label is server-forced either way;
// a mismatching label is 403.
func (c *Client) AcquireLeaseLabeled(resource string, ttlSec int, subScope string) (wire.Lease, error) {
	var out wire.Lease
	body := map[string]any{"resource": resource, "ttl_sec": ttlSec}
	if subScope != "" {
		body["sub_scope"] = subScope
	}
	err := c.do("POST", "/v1/lease/acquire", body, &out)
	return out, err
}
func (c *Client) RenewLease(resource string, token uint64, ttlSec int) (wire.Lease, error) {
	var out wire.Lease
	return out, c.do("POST", "/v1/lease/renew", map[string]any{"resource": resource, "token": token, "ttl_sec": ttlSec}, &out)
}
func (c *Client) ReleaseLease(resource string, token uint64) error {
	return c.do("POST", "/v1/lease/release", map[string]any{"resource": resource, "token": token}, nil)
}
func (c *Client) ListLeases() ([]wire.Lease, error) {
	var out []wire.Lease
	return out, c.do("GET", "/v1/lease", nil, &out)
}

// --- blobs / manifest / commit ---

func (c *Client) PutBlob(r io.Reader) (string, int64, error) {
	req, err := c.req("POST", "/v1/blobs", r)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	data := readBounded(resp.Body)
	if resp.StatusCode >= 300 {
		return "", 0, apiErrorFrom(resp.StatusCode, data) // uniform taxonomy (e.g. 507 OverQuota)
	}
	var out struct {
		Hash string `json:"hash"`
		Size int64  `json:"size"`
	}
	json.Unmarshal(data, &out)
	return out.Hash, out.Size, nil
}

func (c *Client) GetBlob(hash string) (io.ReadCloser, error) {
	req, err := c.req("GET", "/v1/blobs/"+hash, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		data := readBounded(resp.Body)
		resp.Body.Close()
		return nil, apiErrorFrom(resp.StatusCode, data) // uniform taxonomy (404, 403, ...)
	}
	return resp.Body, nil
}

// BlobURL is a minted blob capability URL (acp-ext-3): a short-lived,
// tokenless, signed URL granting read of exactly one blob. Treat it as a
// bearer secret for that blob — anyone holding it can fetch until expiry.
type BlobURL struct {
	URL     string `json:"url"`     // absolute (resolved against the client's base)
	Expires string `json:"expires"` // RFC3339
	Hash    string `json:"hash"`
	TTLSec  int    `json:"ttl_sec"` // effective (post-clamp) lifetime
}

// MintBlobURL mints a capability URL for hash (ttlSec 0 = daemon default; the
// daemon clamps to its configured bounds). The daemon's capability list is
// probed once (then cached); a daemon without "bloburl" yields an explicit
// error — there is NO safe fallback for handing out a URL (a bearer-token URL
// would leak the token; a proxied fetch is a different architecture), so the
// CALLER decides what to do instead. This is the LogBatch fail-explicit shape,
// not FollowFiltered's silent degrade. The blob must exist AND be referenced
// by the space manifest.
func (c *Client) MintBlobURL(hash string, ttlSec int) (BlobURL, error) {
	var out BlobURL
	ok, err := c.hasCapability("bloburl")
	if err != nil {
		return out, err
	}
	if !ok {
		return out, fmt.Errorf("daemon lacks the bloburl capability (older coordd, or started without -cap-key-file)")
	}
	if err := c.do("POST", "/v1/blobs/"+hash+"/url", map[string]int{"ttl_sec": ttlSec}, &out); err != nil {
		return out, err
	}
	// The daemon returns a root-relative URL (it cannot know its external
	// host); resolve it against this client's base so callers can hand the
	// result to an untrusted reader verbatim.
	if strings.HasPrefix(out.URL, "/") {
		out.URL = c.base + out.URL
	}
	return out, nil
}

func (c *Client) MissingBlobs(hashes []string) ([]string, error) {
	var out struct {
		Missing []string `json:"missing"`
	}
	err := c.do("POST", "/v1/blobs/has", map[string][]string{"hashes": hashes}, &out)
	return out.Missing, err
}

func (c *Client) Manifest() (wire.Manifest, error) {
	var out wire.Manifest
	return out, c.do("GET", "/v1/manifest", nil, &out)
}

func (c *Client) Commit(req wire.CommitRequest) (wire.Manifest, error) {
	var out wire.Manifest
	err := c.do("POST", "/v1/commit", req, &out)
	return out, err
}

// --- collaborative documents (CRDT) ---

// PushCRDTOps uploads ops for a collaborative document at the given epoch, and
// returns the new total and the doc's epoch. Pass epoch = -1 to bypass the
// compaction barrier. If the doc was compacted (epoch advanced), the call fails
// with a 409 APIError (Conflict) whose Current is the new epoch — resync first.
func (c *Client) PushCRDTOps(doc string, ops []crdt.Op, epoch int) (int, int, error) {
	body := map[string]any{"doc": doc, "ops": ops}
	if epoch >= 0 {
		body["epoch"] = epoch
	}
	var out struct {
		Total int `json:"total"`
		Epoch int `json:"epoch"`
	}
	err := c.do("POST", "/v1/crdt/ops", body, &out)
	return out.Total, out.Epoch, err
}

// PullCRDTOps fetches ops with index >= from, the doc's total op count, and its
// epoch. If the epoch differs from what the client last saw, the log was
// compacted: discard the local shadow and re-pull from 0 (the compacted log is a
// snapshot that reproduces full state).
func (c *Client) PullCRDTOps(doc string, from int) ([]crdt.Op, int, int, error) {
	var out struct {
		Ops   []crdt.Op `json:"ops"`
		Total int       `json:"total"`
		Epoch int       `json:"epoch"`
	}
	// F-INV-42: url-escape the doc name — a co-tenant can create a doc whose NAME
	// is a query-string-injection payload (e.g. "notes&from=999999"), which would
	// otherwise hijack the from= offset of any client that later pulls it.
	err := c.do("GET", "/v1/crdt/ops?doc="+url.QueryEscape(doc)+"&from="+strconv.Itoa(from), nil, &out)
	return out.Ops, out.Total, out.Epoch, err
}

// CRDTText returns the daemon's materialized text for a document.
func (c *Client) CRDTText(doc string) (string, int, error) {
	var out struct {
		Text  string `json:"text"`
		Total int    `json:"total"`
	}
	err := c.do("GET", "/v1/crdt/doc?doc="+doc, nil, &out)
	return out.Text, out.Total, err
}

// DocInfo summarizes a collaborative document.
type DocInfo struct {
	Name string `json:"name"`
	Ops  int    `json:"ops"`
	Size int    `json:"size"`
}

func (c *Client) CRDTList() ([]DocInfo, error) {
	return c.listPaged("/v1/crdt/list")
}

// maxListPages bounds the pagination walk so a server that kept returning a
// cursor could never spin the client forever (the client-side twin of the
// daemon's own page ceiling).
const maxListPages = 10000

// listPaged walks a paginated list endpoint to completion, following the
// X-ACP-Next-Cursor header. The daemon bounds every list response BY DEFAULT
// (F-INV-30) so a single bare GET can no longer force a whole-space fold;
// without this loop that bound would silently TRUNCATE the SDK's result to the
// first page — turning an availability fix into a correctness bug. Callers keep
// the same "every doc" contract, now paid for in bounded pages.
func (c *Client) listPaged(path string) ([]DocInfo, error) {
	var all []DocInfo
	cursor := ""
	for i := 0; ; i++ {
		if i > maxListPages {
			return all, fmt.Errorf("%s: pagination did not terminate after %d pages", path, maxListPages)
		}
		p := path
		if cursor != "" {
			p += "?cursor=" + url.QueryEscape(cursor)
		}
		var page []DocInfo
		next, err := c.doPaged("GET", p, &page)
		if err != nil {
			return all, err
		}
		all = append(all, page...)
		if next == "" {
			return all, nil
		}
		cursor = next
	}
}

// --- structured (JSON) collaborative documents (ext-5, capability "crdtjson") ---

// Every method below probes the daemon's capability list once (cached) and
// fails EXPLICITLY against a daemon without "crdtjson" (which would 404 the
// paths) — never a silent degrade; the CALLER decides how to fall back
// (LogBatch's contract). Ops may use the client-convenience forms (path,
// container-literal values, idx): the daemon canonicalizes and stamps
// everything server-side, so this SDK ships NO merge code.
func (c *Client) requireCRDTJSON() error {
	ok, err := c.hasCapability("crdtjson")
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("daemon lacks crdtjson capability (older coordd) — use a text doc or upgrade the daemon")
	}
	return nil
}

// PushCRDTJSONOps uploads structured-CRDT ops at the given epoch and returns
// the doc's new op total and epoch. Pass epoch = -1 to bypass the compaction
// barrier. createIntermediate auto-creates missing intermediate object keys on
// path-form ops; otherwise an unresolvable path is a 409 APIError whose
// Current is the doc's current materialized value (rebase and retry).
func (c *Client) PushCRDTJSONOps(doc string, ops []crdtjson.Op, epoch int, createIntermediate bool) (int, int, error) {
	if err := c.requireCRDTJSON(); err != nil {
		return 0, 0, err
	}
	body := map[string]any{"doc": doc, "ops": ops}
	if epoch >= 0 {
		body["epoch"] = epoch
	}
	if createIntermediate {
		body["create_intermediate"] = true
	}
	var out struct {
		Total int `json:"total"`
		Epoch int `json:"epoch"`
	}
	err := c.do("POST", "/v1/crdt/json/ops", body, &out)
	return out.Total, out.Epoch, err
}

// PullCRDTJSONOps fetches canonical ops with index >= from, plus the doc's
// total and epoch — the full-replay sync cursor, exactly like PullCRDTOps.
func (c *Client) PullCRDTJSONOps(doc string, from int) ([]crdtjson.Op, int, int, error) {
	if err := c.requireCRDTJSON(); err != nil {
		return nil, 0, 0, err
	}
	var out struct {
		Ops   []crdtjson.Op `json:"ops"`
		Total int           `json:"total"`
		Epoch int           `json:"epoch"`
	}
	err := c.do("GET", "/v1/crdt/json/ops?doc="+url.QueryEscape(doc)+"&from="+strconv.Itoa(from), nil, &out)
	return out.Ops, out.Total, out.Epoch, err
}

// CRDTJSONDoc returns the daemon-materialized JSON value (canonical bytes —
// sorted object keys, identical on every replica), its op total, and epoch.
func (c *Client) CRDTJSONDoc(doc string) (json.RawMessage, int, int, error) {
	if err := c.requireCRDTJSON(); err != nil {
		return nil, 0, 0, err
	}
	var out struct {
		JSON  json.RawMessage `json:"json"`
		Total int             `json:"total"`
		Epoch int             `json:"epoch"`
	}
	err := c.do("GET", "/v1/crdt/json/doc?doc="+doc, nil, &out)
	return out.JSON, out.Total, out.Epoch, err
}

// CRDTJSONList lists structured documents (a namespace separate from text docs).
func (c *Client) CRDTJSONList() ([]DocInfo, error) {
	if err := c.requireCRDTJSON(); err != nil {
		return nil, err
	}
	return c.listPaged("/v1/crdt/json/list")
}

// --- presence ---

func (c *Client) Beat(harness, host, status string) (wire.Agent, error) {
	var out wire.Agent
	return out, c.do("POST", "/v1/agents/beat", wire.Agent{Harness: harness, Host: host, Status: status}, &out)
}
func (c *Client) Agents() ([]wire.Agent, error) {
	var out []wire.Agent
	return out, c.do("GET", "/v1/agents", nil, &out)
}

// --- awareness (ext-9, capability "awareness"): the ephemeral tier ---

// Every awareness method gates on the daemon's capability list (probed once,
// cached) and degrades EXPLICITLY when "awareness" is absent — a conformant
// client MUST NOT poll a 404 endpoint (ext-9 §4.7). Never build
// correctness-bearing logic on awareness (§3.3): it is lossy display state;
// leases and the event log carry facts.
func (c *Client) requireAwareness() error {
	ok, err := c.hasCapability("awareness")
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("daemon lacks awareness capability (older coordd) — degrade explicitly (no cursors), do not poll")
	}
	return nil
}

// SetAwareness publishes the caller's own ephemeral state (cursor, status,
// ...) for the optional session discriminator ("" = the actor's single
// entry). state must marshal to a JSON object; nil state is an explicit
// leave (prefer ClearAwareness). ttlSec 0 adopts the daemon default (30s);
// re-send at ~TTL/3 to stay live. The daemon stamps actor from the token and
// updated/expires from its own clock.
func (c *Client) SetAwareness(state any, ttlSec int, session string) (wire.AwarenessEntry, error) {
	var out wire.AwarenessEntry
	if err := c.requireAwareness(); err != nil {
		return out, err
	}
	raw, err := json.Marshal(state) // nil → "null" → explicit leave
	if err != nil {
		return out, err
	}
	body := wire.AwarenessSet{State: raw, Session: session}
	if ttlSec != 0 {
		body.TTLSec = &ttlSec
	}
	return out, c.do("POST", "/v1/awareness", body, &out)
}

// ClearAwareness is the explicit leave (state:null — ext-9 §4.2): peers get
// the leave delta immediately instead of waiting out the TTL. Send it on
// orderly shutdown. Idempotent.
func (c *Client) ClearAwareness(session string) error {
	if err := c.requireAwareness(); err != nil {
		return err
	}
	var out wire.AwarenessEntry
	return c.do("POST", "/v1/awareness", wire.AwarenessSet{State: json.RawMessage("null"), Session: session}, &out)
}

// Awareness returns the space's live entries (ext-9 §4.3) — the re-priming
// read; follow deltas after it with FollowAwareness.
func (c *Client) Awareness() (wire.AwarenessSnapshot, error) {
	var out wire.AwarenessSnapshot
	if err := c.requireAwareness(); err != nil {
		return out, err
	}
	return out, c.do("GET", "/v1/awareness", nil, &out)
}

// FollowAwareness streams awareness deltas (ext-9 §4.4), calling fn for each.
// The stream opens with a synthetic "join" per live entry, so no separate
// snapshot is needed. Deltas carry NO seq and NO replay cursor: delivery is
// best-effort keep-latest, and on any disconnect the caller simply calls
// FollowAwareness again and re-primes from the fresh synthetic joins (unlike
// Follow, there is no from= to resume). Returns when the connection closes
// or fn errors.
func (c *Client) FollowAwareness(fn func(wire.AwarenessDelta) error) error {
	if err := c.requireAwareness(); err != nil {
		return err
	}
	req, err := c.req("GET", "/v1/awareness?follow=true", nil)
	if err != nil {
		return err
	}
	// Cluster: the awareness map is leader-served, and a follower answers the
	// follow with a 307 to the leader (it cannot proxy a live stream). Go's
	// http.Client silently STRIPS Authorization on a cross-host redirect, so
	// the hop must be taken manually with the full header set re-applied.
	hc := &http.Client{
		Transport:     c.hc.Transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	for hops := 0; resp.StatusCode == http.StatusTemporaryRedirect && hops < 3; hops++ {
		loc := resp.Header.Get("Location")
		resp.Body.Close()
		hop, err := http.NewRequest("GET", loc, nil)
		if err != nil {
			return err
		}
		hop.Header = req.Header.Clone()
		if resp, err = hc.Do(hop); err != nil {
			return err
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data := readBounded(resp.Body)
		return apiErrorFrom(resp.StatusCode, data) // uniform taxonomy on stream open too
	}
	dec := json.NewDecoder(resp.Body)
	for {
		var d wire.AwarenessDelta
		if err := dec.Decode(&d); err != nil {
			return err // EOF / connection drop — caller reconnects and re-primes
		}
		if err := fn(d); err != nil {
			return err
		}
	}
}

// SubscopeInfo is one ext-7 §6.4 discovery row.
type SubscopeInfo struct {
	Name    string `json:"name"`
	Events  uint64 `json:"events"`
	Limit   uint64 `json:"limit,omitempty"` // §5.5 configured max_events, when set
	LastSeq uint64 `json:"last_seq"`
	LastAt  string `json:"last_at,omitempty"`
}

// Subscopes lists the sub-scope labels seen in the space's retained log
// (ext-7 §6.4) plus any §5.5-configured-but-quiet labels. Events/mail/commits
// carry a label via the wire structs' SubScope field directly (e.g.
// Append(wire.Event{Action: "x", SubScope: "proj-42"})); leases via
// AcquireLeaseLabeled. NOTE §6.3: a sub-scope is NOT an isolation boundary —
// any reader of the space reads every sub-scope.
func (c *Client) Subscopes() ([]SubscopeInfo, error) {
	var out []SubscopeInfo
	return out, c.do("GET", "/v1/subscopes", nil, &out)
}

// Stats returns daemon counters for the client's space.
func (c *Client) Stats() (map[string]any, error) {
	var out map[string]any
	return out, c.do("GET", "/v1/stats", nil, &out)
}

// Spaces lists the spaces the daemon currently has open.
func (c *Client) Spaces() (map[string]any, error) {
	var out map[string]any
	return out, c.do("GET", "/v1/spaces", nil, &out)
}

// GC asks the daemon to delete unreferenced blobs older than graceSec seconds.
func (c *Client) GC(graceSec int) (int, int64, error) {
	var out struct {
		Removed int   `json:"removed"`
		Bytes   int64 `json:"bytes"`
	}
	err := c.do("POST", "/v1/admin/gc?grace_sec="+strconv.Itoa(graceSec), nil, &out)
	return out.Removed, out.Bytes, err
}

// Compact asks the daemon to compact a doc's op-log (or all docs if doc==""),
// reclaiming tombstone history. Returns ops dropped.
func (c *Client) Compact(doc string) (int, error) {
	var out struct {
		Dropped int `json:"dropped"`
	}
	path := "/v1/admin/compact"
	if doc != "" {
		path += "?doc=" + doc
	}
	err := c.do("POST", path, nil, &out)
	return out.Dropped, err
}

// Health pings the daemon.
func (c *Client) Health() error {
	req, _ := http.NewRequest("GET", c.base+"/v1/healthz", nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	c2 := &http.Client{Timeout: 10 * time.Second, Transport: c.hc.Transport}
	resp, err := c2.Do(req)
	if err != nil {
		return err
	}
	data := readBounded(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return apiErrorFrom(resp.StatusCode, data) // uniform taxonomy
	}
	return nil
}

package acpuri

// discovery.go — the bare-host bootstrap (ext-32 §3.8, amendment
// ext32-3.8-discovery-doc, D4 RESOLVED 2026-09-11). The Go twin of the
// discovery section of the TS SDK's uri.ts, kept at exact behavioural parity
// (same address grammar, same document validation order, same refusal codes,
// same result shape — the JSON tags are the TS property names).
//
// A DISCOVERY ADDRESS "acp://host[:port]/" names no resource (Parse refuses
// it: bare_host); Discover performs ONE anonymous GET of
// https://host:port/.well-known/acp — NO request headers of any kind, NO
// redirect ever followed, the body bounded — and validates the document
// FAIL-CLOSED: protocol must be "acp/1", the endpoint must be an https ORIGIN
// on the SAME host, the cert fingerprint is an advisory pin hint that never
// lowers TLS trust and must agree with the certificate that served it.

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ab0t-com/acp/sdk/go/pkg/wire"
)

// DiscoveryPath is the well-known path of the discovery document (RFC 8615 style).
const DiscoveryPath = "/.well-known/acp"

// MaxDiscoveryBytes is the maximum size of a discovery document; a larger body is refused.
const MaxDiscoveryBytes = 65536

// DiscoveryAddress is a DISCOVERY ADDRESS: the bare-host form
// `acp://host[:port]/` (no space, no path, no query) — or any valid resource
// URI, whose authority is used (Resource then carries the parsed URI).
type DiscoveryAddress struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	// Resource is nil for the bare-host form; the parsed URI when a resource address was given.
	Resource *URI `json:"resource"`
}

// DiscoveryDocument is the validated discovery document (the wire keys are
// snake_case; this is the SDK view, tagged with the TS property names).
type DiscoveryDocument struct {
	// Protocol is always "acp/1" (any other wire version is refused).
	Protocol string `json:"protocol"`
	// Endpoint is the https ORIGIN the acp/1 wire lives under (`<endpoint>/v1/...`), as published (trailing "/" stripped).
	Endpoint string `json:"endpoint"`
	// Capabilities are the capability strings, as GET /v1/healthz advertises them.
	Capabilities []string `json:"capabilities"`
	// Spaces are the published space names, or nil when the daemon does not disclose them (authenticate, then GET /v1/spaces).
	Spaces []string `json:"spaces"`
	// DefaultSpace is the space a first-contact client may OFFER (never auto-connect), or nil.
	DefaultSpace *string `json:"defaultSpace"`
	// CertFingerprint is "sha256:<64 hex>" of the endpoint's DER leaf certificate (an advisory pin hint), or nil.
	CertFingerprint *string `json:"certFingerprint"`
}

// Discovery is the result of discovery: the address fetched, the validated
// document, and what a Client needs.
type Discovery struct {
	// Host is the address's host (lowercased).
	Host string `json:"host"`
	// Port is the address's port (explicit or the 8443 default).
	Port int `json:"port"`
	// URL is the exact URL fetched: https://host:port/.well-known/acp
	URL string            `json:"url"`
	Doc DiscoveryDocument `json:"doc"`
	// EndpointHost is the endpoint's host — equal to Host by construction (host-match is enforced).
	EndpointHost string `json:"endpointHost"`
	// EndpointPort is the endpoint's port (the https default 443 when the origin omits it). MAY differ from Port.
	EndpointPort int `json:"endpointPort"`
	// BaseURL is "https://endpointHost:endpointPort" — exactly what client.New takes.
	BaseURL string `json:"baseUrl"`
	// PinVerified is true when the document's cert fingerprint was checked against
	// the certificate presented on the discovery connection and matched; nil when
	// not verified (no fingerprint in the document, or the presented certificate
	// was unknown). Never false — a mismatch is a refusal (discovery_pin_mismatch).
	PinVerified *bool `json:"pinVerified"`
	// IgnoredKeys lists the unknown top-level keys the resolver ignored (diagnostics only; "@…" keys are reserved and land here too), in document order.
	IgnoredKeys []string `json:"ignoredKeys"`
}

var (
	fingerprintRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	discoveryKeys = map[string]bool{"protocol": true, "endpoint": true, "capabilities": true, "spaces": true, "default_space": true, "cert_fingerprint": true}
)

// hostKey is the host identity for the endpoint host-match: ASCII case-fold, one trailing dot stripped.
func hostKey(h string) string {
	l := asciiLower(h)
	if strings.HasSuffix(l, ".") && !strings.HasPrefix(l, "[") {
		return l[:len(l)-1]
	}
	return l
}

// DiscoveryURL is the exact URL discovery fetches for an address.
func DiscoveryURL(host string, port int) string {
	return "https://" + host + ":" + strconv.Itoa(port) + DiscoveryPath
}

// ParseDiscoveryAddress parses a DISCOVERY ADDRESS. The bare-host form
// `acp://host[:port][/][#fragment]` takes NO query (a credential-shaped key is
// refused as raw_token, anything else as query); any other input must be a
// valid resource URI (every resource refusal applies first) and contributes
// its authority. Pure.
func ParseDiscoveryAddress(input string) (*DiscoveryAddress, error) {
	sp, err := splitURI(input)
	if err != nil {
		return nil, err
	}
	if sp.rawPath != "" && sp.rawPath != "/" {
		u, err := Parse(input)
		if err != nil {
			return nil, err
		}
		return &DiscoveryAddress{Host: u.Host, Port: u.Port, Resource: u}, nil
	}
	if sp.rawQuery != "" {
		for _, pair := range strings.Split(sp.rawQuery, "&") {
			if pair == "" {
				continue
			}
			rawK := pair
			if eq := strings.Index(pair, "="); eq >= 0 {
				rawK = pair[:eq]
			}
			k, err := pctDecode(rawK, "query key")
			if err != nil {
				return nil, err
			}
			if credentialKeys[asciiLower(k)] {
				return nil, errf(CodeRawToken, "query key %q looks like a raw credential — a token must never ride in an acp:// URI (discovery is anonymous)", k)
			}
		}
		return nil, errf(CodeQuery, "a discovery address (bare host) carries no query — use acp://host[:port]/")
	}
	return &DiscoveryAddress{Host: sp.host, Port: sp.port}, nil
}

func malformed(format string, args ...any) *Error {
	return errf(CodeDiscoveryMalformed, "discovery document: "+format, args...)
}

// jsonKeys returns the top-level keys of a JSON object in document order
// (encoding/json's map loses it; the TS Object.keys order is document order).
func jsonKeys(body []byte) ([]string, error) {
	dec := json.NewDecoder(strings.NewReader(string(body)))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("not an object")
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, ok := tok.(string)
		if !ok {
			return nil, errors.New("not a key")
		}
		keys = append(keys, k)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
	}
	if _, err := dec.Token(); err != nil { // the closing '}'
		return nil, err
	}
	return keys, nil
}

func optStringArray(o map[string]json.RawMessage, key string) ([]string, error) {
	raw, ok := o[key]
	if !ok || string(raw) == "null" {
		return nil, nil
	}
	var v []any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, malformed("%q must be an array of strings", key)
	}
	out := make([]string, 0, len(v))
	for _, e := range v {
		s, ok := e.(string)
		if !ok || s == "" {
			return nil, malformed("%q must contain only non-empty strings", key)
		}
		out = append(out, s)
	}
	return out, nil
}

func optString(o map[string]json.RawMessage, key string) (*string, error) {
	raw, ok := o[key]
	if !ok || string(raw) == "null" {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, malformed("%q must be a string", key)
	}
	return &s, nil
}

// ParseDiscoveryDocument validates a discovery document body against the
// address it was fetched for (§3.8 amendment (a)/(c)). PURE: no I/O.
// Fail-closed in a fixed order — size → UTF-8 → JSON object → protocol →
// endpoint (https, origin, host match) → capabilities → spaces →
// default_space → cert_fingerprint (format, then the pin check when
// presentedFingerprint — the SHA-256 of the DER leaf cert presented on the
// discovery connection, "" = unknown — is known). Never a best-effort result.
func ParseDiscoveryDocument(body []byte, address DiscoveryAddress, presentedFingerprint string) (*Discovery, error) {
	if len(body) > MaxDiscoveryBytes {
		return nil, malformed("body exceeds %d bytes", MaxDiscoveryBytes)
	}
	if !utf8.Valid(body) {
		return nil, malformed("body is not valid UTF-8")
	}
	var probe any
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil, malformed("body is not JSON")
	}
	if _, ok := probe.(map[string]any); !ok {
		return nil, malformed("body is not a JSON object")
	}
	var o map[string]json.RawMessage
	if err := json.Unmarshal(body, &o); err != nil {
		return nil, malformed("body is not a JSON object")
	}
	keys, err := jsonKeys(body)
	if err != nil {
		return nil, malformed("body is not a JSON object")
	}

	// protocol — the compatibility gate.
	protocol, err := optString(o, "protocol")
	if err != nil || protocol == nil || *protocol == "" {
		return nil, malformed(`"protocol" (string) is required`)
	}
	if *protocol != wire.ProtocolVersion {
		return nil, errf(CodeDiscoveryProtocol, "daemon speaks %q, this resolver speaks %q — refusing to talk a wire it does not know", *protocol, wire.ProtocolVersion)
	}

	// endpoint — an https ORIGIN on the SAME host.
	endpointRaw, err := optString(o, "endpoint")
	if err != nil || endpointRaw == nil || *endpointRaw == "" {
		return nil, malformed(`"endpoint" (string) is required`)
	}
	if strings.ContainsAny(*endpointRaw, "?#") {
		return nil, malformed(`"endpoint" must be an origin (no query or fragment)`)
	}
	eu, err := url.Parse(*endpointRaw)
	if err != nil || eu.Host == "" || !strings.Contains(*endpointRaw, "://") {
		return nil, malformed("\"endpoint\" %q is not a URL", *endpointRaw)
	}
	if eu.Scheme != "https" {
		return nil, malformed("\"endpoint\" must be https (acp:// implies TLS) — refusing %q, a downgrade", eu.Scheme)
	}
	if eu.User != nil {
		return nil, malformed(`"endpoint" must not carry userinfo`)
	}
	if p := eu.EscapedPath(); p != "" && p != "/" {
		if strings.TrimRight(p, "/") == "/v1" {
			return nil, malformed(`"endpoint" is the origin the /v1 wire lives under — do not append /v1`)
		}
		return nil, malformed(`"endpoint" must be an origin (no path)`)
	}
	endpointHost := asciiLower(eu.Hostname())
	if strings.Contains(endpointHost, ":") {
		endpointHost = "[" + endpointHost + "]"
	}
	if endpointHost == "" || !hostRE.MatchString(endpointHost) {
		return nil, malformed("\"endpoint\" host %q is invalid", eu.Hostname())
	}
	endpointPort := 443
	if p := eu.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return nil, malformed("\"endpoint\" port %q is invalid", p)
		}
		endpointPort = n
	}
	if hostKey(endpointHost) != hostKey(address.Host) {
		return nil, errf(CodeDiscoveryHostMismatch, "document points at %q but the address names %q — a discovery document may only describe its own host", endpointHost, address.Host)
	}
	endpoint := strings.TrimSuffix(*endpointRaw, "/")

	// capabilities — required, as healthz advertises them.
	capsRaw, ok := o["capabilities"]
	if !ok || string(capsRaw) == "null" {
		return nil, malformed(`"capabilities" (array of strings) is required`)
	}
	capabilities, err := optStringArray(o, "capabilities")
	if err != nil {
		return nil, err
	}
	if capabilities == nil {
		capabilities = []string{}
	}

	// spaces / default_space — optional; each a valid space name.
	spaces, err := optStringArray(o, "spaces")
	if err != nil {
		return nil, err
	}
	for _, s := range spaces {
		if !spaceRE.MatchString(s) {
			return nil, malformed("\"spaces\" entry %q violates [A-Za-z0-9_-]{1,64}", s)
		}
	}
	defaultSpace, err := optString(o, "default_space")
	if err != nil {
		return nil, err
	}
	if defaultSpace != nil && !spaceRE.MatchString(*defaultSpace) {
		return nil, malformed("\"default_space\" %q violates [A-Za-z0-9_-]{1,64}", *defaultSpace)
	}

	// cert_fingerprint — optional; format, then consistency with the presented certificate.
	certFingerprint, err := optString(o, "cert_fingerprint")
	if err != nil {
		return nil, err
	}
	var pinVerified *bool
	if certFingerprint != nil {
		if !fingerprintRE.MatchString(*certFingerprint) {
			return nil, malformed(`"cert_fingerprint" must be "sha256:" + 64 lowercase hex`)
		}
		if presentedFingerprint != "" {
			presented := asciiLower(presentedFingerprint)
			if !strings.HasPrefix(presented, "sha256:") {
				presented = "sha256:" + presented
			}
			if presented != *certFingerprint {
				return nil, errf(CodeDiscoveryPinMismatch, "document pins %s but the connection presented %s — a document that misdescribes its own transport is refused (a TLS-terminating gateway must omit the field or publish its own certificate)", *certFingerprint, presented)
			}
			v := true
			pinVerified = &v
		}
	}

	ignored := []string{}
	for _, k := range keys {
		if !discoveryKeys[k] {
			ignored = append(ignored, k)
		}
	}
	return &Discovery{
		Host:         address.Host,
		Port:         address.Port,
		URL:          DiscoveryURL(address.Host, address.Port),
		Doc:          DiscoveryDocument{Protocol: *protocol, Endpoint: endpoint, Capabilities: capabilities, Spaces: spaces, DefaultSpace: defaultSpace, CertFingerprint: certFingerprint},
		EndpointHost: endpointHost,
		EndpointPort: endpointPort,
		BaseURL:      "https://" + endpointHost + ":" + strconv.Itoa(endpointPort),
		PinVerified:  pinVerified,
		IgnoredKeys:  ignored,
	}, nil
}

// CertFingerprint is "sha256:<64 lowercase hex>" of a certificate's DER
// encoding — the cert_fingerprint format (§3.8 amendment (a)).
func CertFingerprint(cert *x509.Certificate) string {
	return CertFingerprintDER(cert.Raw)
}

// CertFingerprintDER is CertFingerprint over raw DER bytes.
func CertFingerprintDER(der []byte) string {
	sum := sha256.Sum256(der)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// DiscoverOptions configures Discover.
type DiscoverOptions struct {
	// HTTPClient performs the single anonymous GET (TLS trust/pinning is the
	// caller's: a pinned daemon cert goes in its Transport). nil = http.DefaultClient.
	// Its redirect policy is overridden: a redirect is never followed.
	HTTPClient *http.Client
}

// Discover discovers the daemon behind an address (§3.8 amendment (b)): ONE
// anonymous `GET https://host:port/.well-known/acp` — NO request headers of
// any kind (no Authorization, no X-ACP-Space, no X-ACP-Agent), NO redirect
// ever followed (any 3xx is refused), the body bounded to MaxDiscoveryBytes —
// then validates the document fail-closed with ParseDiscoveryDocument.
//
// TLS is verified by the http.Client exactly as for any request; nothing in
// the document can relax it. The leaf certificate the connection presented
// is taken from the response's TLS state, so a document whose
// cert_fingerprint disagrees with it is refused (discovery_pin_mismatch).
//
// 404 → discovery_unsupported (the daemon does not serve discovery — use a
// configured or user-supplied endpoint base); other non-200 → discovery_http.
func Discover(target string, opts DiscoverOptions) (*Discovery, error) {
	addr, err := ParseDiscoveryAddress(target)
	if err != nil {
		return nil, err
	}
	u := DiscoveryURL(addr.Host, addr.Port)
	hc := http.DefaultClient
	if opts.HTTPClient != nil {
		hc = opts.HTTPClient
	}
	// A shallow copy shares the Transport (and its TLS trust) but never follows
	// a redirect: the 3xx is returned as-is and refused below.
	c := *hc
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequest(http.MethodGet, u, nil) // deliberately NO headers: anonymous
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		loc := resp.Header.Get("Location")
		if loc != "" {
			loc = " to " + strconv.Quote(loc)
		}
		return nil, errf(CodeDiscoveryRedirect, "%s answered %d%s — a discovery document is never followed off its address (the endpoint field is the only way to point elsewhere)", u, resp.StatusCode, loc)
	case resp.StatusCode == http.StatusNotFound:
		return nil, errf(CodeDiscoveryUnsupported, "%s:%d does not serve %s (no \"acpuri\" bootstrap) — use a configured or user-supplied endpoint base", addr.Host, addr.Port, DiscoveryPath)
	case resp.StatusCode != http.StatusOK:
		msg := "discovery: " + u + " answered " + strconv.Itoa(resp.StatusCode)
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &e) == nil && e.Error != "" {
			msg += " (" + e.Error + ")"
		}
		return nil, errf(CodeDiscoveryHTTP, "%s", msg)
	}
	if resp.ContentLength > MaxDiscoveryBytes {
		return nil, malformed("body exceeds %d bytes", MaxDiscoveryBytes)
	}
	// Read one byte past the cap so an oversize chunked body is refused, never truncated.
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxDiscoveryBytes+1))
	if err != nil {
		return nil, err
	}
	presented := ""
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		presented = CertFingerprint(resp.TLS.PeerCertificates[0])
	}
	return ParseDiscoveryDocument(body, *addr, presented)
}

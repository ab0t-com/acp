// Package acpuri is the `acp://` URI scheme and its client-side RESOLVER
// (ext-32, draft 2026-08-21; open questions decided 2026-09-11 — see
// tickets/acp-first-slice-20260911/DECISION-acp-uri-scheme.md) — "the URL of
// the agent filesystem". It is the Go twin of the TypeScript SDK's uri.ts and
// is kept at exact behavioral parity with it (same grammar, same refusals,
// same error codes, same canonical strings, same wire mapping).
//
//	acp://<host>[:<port>]/<space>/<path>[?query][#fragment]
//
// This package is a PURE, deterministic mapping from an ACP URI to the frozen
// `acp/1` HTTPS wire: it adds no endpoint, no write path, and no capability.
// The space (first path segment) becomes the `X-ACP-Space` header — never a
// URL path component — so it stays the only hard isolation boundary.
//
// Grammar honored exactly as drafted (ext-32 §3.2–§3.7) plus the decisions:
//   - authority = host[:port], NO userinfo (a credential would leak there);
//     the DEFAULT PORT is 8443 (D3): a port-less URI means :8443, an explicit
//     port overrides, and the canonical form omits :8443 (as https omits :443);
//   - space is REQUIRED, matches acp-1 §7 ([A-Za-z0-9_-]{1,64});
//   - a leading "@<type>" segment after the space is the reserved RESOURCE-TYPE
//     dimension: bare (or "@fs") = the filesystem; "@doc"/"@log"/"@mail"/"@chan"
//     are reserved with no v1 resolution; ANY other "@type" is refused
//     (fail-closed — an old resolver must never mis-resolve a future type).
//     The sigil is checked on the RAW segment (§3.3 amendment 2026-09-11):
//     "%40" decodes to a literal "@" AFTER the check, so "%40doc/x" names the
//     file "@doc/x" and the canonical form keeps that leading "%40" encoded;
//   - query: `v=<manifest_version>` pins a read; `cp=<checkpoint>` is reserved
//     (ext-30) and refused until it resolves (D2) — a pin must never silently
//     read the current version; the SHAREABLE FILE LINK (D1) carries
//     `h=<blobhash>` plus the shipped blob-capability params (sp, e, k, s) —
//     fail-closed (partial/malformed = error, never an unsigned read) and
//     resolved to the tokenless `/v1/cap/blob/<hash>` fetch; unknown keys are
//     ignored; a credential-shaped key is refused outright;
//   - "#fragment" is client-only, excluded from identity, never sent;
//   - canonical form: lowercase scheme+host, default port omitted, unreserved
//     percent-escapes decoded, dot-segments resolved (escaping the space root
//     is refused), trailing "/" = collection, no empty segments;
//   - "acp://" IMPLIES TLS: resolution is always "https://", never cleartext;
//   - DISCOVERY (§3.8, D4 resolved 2026-09-11 — amendment ext32-3.8-discovery-doc):
//     a BARE HOST "acp://host[:port]/" names no resource (Parse refuses it:
//     bare_host) but is a DISCOVERY ADDRESS: Discover performs ONE anonymous
//     GET of https://host:port/.well-known/acp (no headers, no redirects,
//     ≤ 64 KiB) and validates the document fail-closed — protocol must be
//     "acp/1", the endpoint must be an https ORIGIN on the SAME host, the cert
//     fingerprint is an advisory pin hint that never lowers TLS trust and must
//     agree with the certificate that served the document (see discovery.go).
//
// THE IRONCLAD RULE (ext-32 §3.6): a raw bearer/space token NEVER appears in
// an `acp://` URI — not in userinfo, path, query, or fragment. Auth comes from
// a local PROFILE (this package's ProfileStore) or, for a shareable link, from
// the signed, scoped, expiring capability (the ONLY in-URL credential, and it
// grants one blob, in one space, until one deadline). Resolve produces
// requests whose URL carries no bearer token by construction (the token rides
// only in the Authorization header the Client sets).
package acpuri

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ab0t-com/acp/sdk/go/pkg/client"
	"github.com/ab0t-com/acp/sdk/go/pkg/wire"
)

// DefaultPort is the default port of the `acp` scheme (D3): a port-less URI means this port.
const DefaultPort = 8443

// ResourceKind is the resource kind a v1 (filesystem) ACP URI can denote.
type ResourceKind string

const (
	KindRoot       ResourceKind = "root"
	KindCollection ResourceKind = "collection"
	KindFile       ResourceKind = "file"
)

// Capability is the signed capability carried in the query of a SHAREABLE FILE
// LINK (D1): the shipped blob-capability-URL params verbatim (sp, e, k, s) plus
// `h`, the blob hash the signature covers. The link names exactly those bytes
// (version-pinned by construction); a living path-signed link is the
// capability-URL successor's job.
type Capability struct {
	// H is the blob hash (64 lowercase hex) the grant covers.
	H string `json:"h"`
	// SP is the signed space (must equal the URI's space — §3.10 binding).
	SP string `json:"sp"`
	// E is the expiry, unix seconds.
	E int64 `json:"e"`
	// K is the key id.
	K string `json:"k"`
	// S is the lowercase-hex HMAC-SHA256.
	S string `json:"s"`
}

// URI is a parsed, validated, normalized ACP URI. Its JSON form uses the TS
// SDK's field names so diagnostics compare across the two implementations.
type URI struct {
	// Host is the lowercased DNS host (or bracketed IPv6 literal).
	Host string `json:"host"`
	// Port is the port: explicit, or DefaultPort when the URI carried none.
	Port int `json:"port"`
	// Space is the space — byte-significant, the hard boundary.
	Space string `json:"space"`
	// Type is always "fs": only the filesystem type resolves in v1.
	Type string `json:"type"`
	// Path is the dot-segment-resolved path inside the space; "" for the space root. No leading/trailing slash.
	Path string       `json:"path"`
	Kind ResourceKind `json:"kind"`
	// Version is the `?v=` manifest-version pin, or nil (= current).
	Version *int64 `json:"version"`
	// Capability is the signed capability if the URI is the shareable file-link form, else nil.
	Capability *Capability `json:"capability"`
	// Fragment is the client-only sub-address; NEVER sent to the server; excluded
	// from identity. nil when the URI carried no "#"; non-nil (possibly empty)
	// when it did.
	Fragment *string `json:"fragment"`
	// IgnoredQuery lists the unknown query keys the resolver ignored (diagnostics only), in order of first appearance.
	IgnoredQuery []string `json:"ignoredQuery"`
}

// Code is a machine-readable reason a URI is refused. The set is identical to
// the TS SDK's ACPURIErrorCode.
type Code string

const (
	CodeScheme                        Code = "scheme"
	CodeAuthority                     Code = "authority"
	CodeUserinfo                      Code = "userinfo"
	CodeHost                          Code = "host"
	CodePort                          Code = "port"
	CodeSpaceRequired                 Code = "space_required"
	CodeSpaceInvalid                  Code = "space_invalid"
	CodeBareHost                      Code = "bare_host"
	CodeDiscoveryUnsupported          Code = "discovery_unsupported"
	CodeDiscoveryRedirect             Code = "discovery_redirect"
	CodeDiscoveryHTTP                 Code = "discovery_http"
	CodeDiscoveryMalformed            Code = "discovery_malformed"
	CodeDiscoveryProtocol             Code = "discovery_protocol"
	CodeDiscoveryHostMismatch         Code = "discovery_host_mismatch"
	CodeDiscoveryPinMismatch          Code = "discovery_pin_mismatch"
	CodeUnknownType                   Code = "unknown_type"
	CodeReservedType                  Code = "reserved_type"
	CodeReservedSigil                 Code = "reserved_sigil"
	CodeEmptySegment                  Code = "empty_segment"
	CodeTraversal                     Code = "traversal"
	CodeBackslash                     Code = "backslash"
	CodeEncodedSlash                  Code = "encoded_slash"
	CodeControlChar                   Code = "control_char"
	CodePercentEncoding               Code = "percent_encoding"
	CodeQuery                         Code = "query"
	CodeVersionInvalid                Code = "version_invalid"
	CodeCheckpointUnsupported         Code = "checkpoint_unsupported"
	CodePinConflict                   Code = "pin_conflict"
	CodeRawToken                      Code = "raw_token"
	CodeCapabilityMalformed           Code = "capability_malformed"
	CodeCapabilitySpaceMismatch       Code = "capability_space_mismatch"
	CodeCapabilityUnsupportedResource Code = "capability_unsupported_resource"
	CodeNoProfile                     Code = "no_profile"
)

// Error is a refusal. Every refusal is fail-closed: the URI resolves to
// nothing, never to a "best-effort" request.
type Error struct {
	Code Code
	Msg  string
}

func (e *Error) Error() string { return "acp uri: " + e.Msg }

func errf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// CodeOf returns the refusal code of err if it is an *Error (possibly wrapped), else "".
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// ReservedTypes are the reserved resource-type names (ext-32 §3.3). Only "fs" resolves in v1.
var ReservedTypes = []string{"fs", "doc", "log", "mail", "chan"}

var (
	// spaceRE is the acp-1 §7 space-name rule.
	spaceRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	// hostRE: DNS label chars, or a bracketed IPv6 literal.
	hostRE = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)*\.?|\[[0-9a-f:.]+\])$`)
	// hashRE: a content hash on the wire: 64 lowercase hex.
	hashRE    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	schemeRE  = regexp.MustCompile(`(?s)^([A-Za-z][A-Za-z0-9+.-]*)://(.*)$`)
	portRE    = regexp.MustCompile(`^[0-9]{1,5}$`)
	versionRE = regexp.MustCompile(`^(?:0|[1-9][0-9]*)$`)
	expiryRE  = regexp.MustCompile(`^[1-9][0-9]*$`)
	capPathRE = regexp.MustCompile(`^/v1/cap/blob/([0-9a-f]{64})$`)
)

// maxSafeInteger mirrors JavaScript's Number.MAX_SAFE_INTEGER so a numeric
// bound refused by the TS resolver is refused here too.
const maxSafeInteger = 1<<53 - 1

// credentialKeys are query keys that would carry a RAW credential. The
// ironclad rule is enforced structurally (no userinfo; only reserved keys are
// ever forwarded; the token lives only in the Authorization header) — this
// deny-list is defense in depth so a pasted "?token=…" fails loudly instead of
// being quietly dropped.
var credentialKeys = map[string]bool{
	"token":         true,
	"access_token":  true,
	"bearer":        true,
	"authorization": true,
	"auth":          true,
	"password":      true,
	"passwd":        true,
	"secret":        true,
	"api_key":       true,
	"apikey":        true,
	"acp_token":     true,
	"x-acp-token":   true,
}

// capKeys are the signed params of the shipped blob-capability URL; linkKeys is
// the whole shareable-link namespace (the signed params + the hash).
var (
	capKeys  = []string{"sp", "e", "k", "s"}
	linkKeys = []string{"h", "sp", "e", "k", "s"}
)

func isLinkKey(k string) bool {
	for _, lk := range linkKeys {
		if k == lk {
			return true
		}
	}
	return false
}

// asciiLower lowercases ASCII letters only. Non-ASCII octets are left as they
// are (and then fail the host grammar) — a deliberately narrower fold than the
// TS Unicode toLowerCase: Go never accepts a host the TS SDK would refuse.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func hexVal(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}

// pctDecode percent-decodes one component; refuses malformed escapes, invalid
// UTF-8 and control bytes.
func pctDecode(s, what string) (string, error) {
	if !strings.Contains(s, "%") {
		if !utf8.ValidString(s) {
			return "", errf(CodePercentEncoding, "%s: invalid UTF-8 in %q", what, s)
		}
		if err := assertNoControl(s, what); err != nil {
			return "", err
		}
		return s, nil
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '%' {
			out = append(out, c)
			continue
		}
		if i+2 >= len(s) || !isHex(s[i+1]) || !isHex(s[i+2]) {
			return "", errf(CodePercentEncoding, "%s: malformed percent-escape in %q", what, s)
		}
		out = append(out, hexVal(s[i+1])<<4|hexVal(s[i+2]))
		i += 2
	}
	if !utf8.Valid(out) {
		return "", errf(CodePercentEncoding, "%s: invalid UTF-8 in percent-escape %q", what, s)
	}
	dec := string(out)
	if err := assertNoControl(dec, what); err != nil {
		return "", err
	}
	return dec, nil
}

func assertNoControl(s, what string) error {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == 0x7f {
			return errf(CodeControlChar, "%s: control character U+%04x", what, c)
		}
	}
	return nil
}

// pctEncodeSegment is the canonical percent-encoding for a path segment:
// RFC3986 unreserved and the pchar sub-delims stay literal; everything else is
// %XX (uppercase hex). A LEADING "@" is always emitted as "%40": a bare "@"
// first octet is the reserved resource-type sigil (§3.3), so a literal
// "@"-named entry must stay encoded in the canonical form to remain distinct
// from the reserved syntax.
func pctEncodeSegment(seg string) string {
	var sb strings.Builder
	start := 0
	if len(seg) > 0 && seg[0] == '@' {
		sb.WriteString("%40")
		start = 1
	}
	for i := start; i < len(seg); i++ {
		b := seg[i]
		if (b >= '0' && b <= '9') || (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || strings.IndexByte("-._~!$&'()*+,;=:@", b) >= 0 {
			sb.WriteByte(b)
		} else {
			fmt.Fprintf(&sb, "%%%02X", b)
		}
	}
	return sb.String()
}

// pctEncodeQueryValue is JavaScript's encodeURIComponent: everything but
// A-Z a-z 0-9 - _ . ! ~ * ' ( ) is %XX (uppercase hex).
func pctEncodeQueryValue(v string) string {
	var sb strings.Builder
	for i := 0; i < len(v); i++ {
		b := v[i]
		if (b >= '0' && b <= '9') || (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || strings.IndexByte("-_.!~*'()", b) >= 0 {
			sb.WriteByte(b)
		} else {
			fmt.Fprintf(&sb, "%%%02X", b)
		}
	}
	return sb.String()
}

// Parse parses and validates an `acp://` URI into its normalized form (ext-32
// §3.2–§3.4, §3.7). Returns an *Error with a machine-readable Code on any
// refusal; a refusal is always fail-closed (never a "best-effort" resolution).
func Parse(input string) (*URI, error) {
	sp, err := splitURI(input)
	if err != nil {
		return nil, err
	}
	host, port, fragment, rawQuery, rawPath := sp.host, sp.port, sp.fragment, sp.rawQuery, sp.rawPath

	// Path: "/<space>[/@type][/segments...]"
	var rawSegs []string
	if rawPath != "" {
		rawSegs = strings.Split(rawPath[1:], "/")
	}
	if len(rawSegs) == 0 || (len(rawSegs) == 1 && rawSegs[0] == "") {
		return nil, errf(CodeBareHost, "a bare host names no resource (a URI needs a space: acp://host/<space>/...) — bootstrap a bare host with Discover / `acp uri discover` (§3.8)")
	}
	space, err := pctDecode(rawSegs[0], "space")
	if err != nil {
		return nil, err
	}
	if space == "" {
		return nil, errf(CodeSpaceRequired, "space (first path segment) is required")
	}
	if !spaceRE.MatchString(space) {
		return nil, errf(CodeSpaceInvalid, "space %q violates [A-Za-z0-9_-]{1,64}", space)
	}

	// The RAW (pre-percent-decoding) segments after the space. The "@" sigil is
	// recognised on the RAW segment (§3.3 amendment 2026-09-11): a bare "@" first
	// octet is reserved syntax, while "%40" decodes to a literal "@" that names a
	// real filesystem entry ("@doc/x" IS a legal manifest name — validPath bounds
	// shape, not charset — so it must stay addressable, as "%40doc/x").
	rawTail := rawSegs[1:]
	// A trailing empty segment means a trailing slash (a collection).
	trailingSlash := false
	if len(rawTail) > 0 && rawTail[len(rawTail)-1] == "" {
		trailingSlash = true
		rawTail = rawTail[:len(rawTail)-1]
	}
	// Resource-type dimension (§3.3): reserved bare "@" first octet, right after the space.
	if len(rawTail) > 0 && strings.HasPrefix(rawTail[0], "@") {
		typ := rawTail[0][1:]
		switch {
		case typ == "fs":
			rawTail = rawTail[1:]
		case isReservedType(typ):
			return nil, errf(CodeReservedType, "\"@%s\" is a reserved resource type with no v1 resolution", typ)
		default:
			return nil, errf(CodeUnknownType, "unknown resource type \"@%s\" — refusing to treat it as a path (fail-closed; a file named \"@%s\" is written \"%%40%s\")", typ, typ, typ)
		}
	}
	// Dot-segment resolution (RFC3986 §5.2.4) with the acp-1 §11.4 escape rule.
	stack := []string{}
	endsAsCollection := trailingSlash
	for i, raw := range rawTail {
		if strings.HasPrefix(raw, "@") {
			return nil, errf(CodeReservedSigil, "segment %q: a bare \"@\" first octet is the reserved resource-type sigil — write a literal \"@\" name as \"%%40%s\"", raw, raw[1:])
		}
		seg, err := pctDecode(raw, "path")
		if err != nil {
			return nil, err
		}
		last := i == len(rawTail)-1
		if seg == "" {
			return nil, errf(CodeEmptySegment, `empty path segment ("//") is not allowed`)
		}
		if seg == "." {
			if last {
				endsAsCollection = true
			}
			continue
		}
		if seg == ".." {
			if len(stack) == 0 {
				return nil, errf(CodeTraversal, "\"..\" would escape the space root of %q", space)
			}
			stack = stack[:len(stack)-1]
			if last {
				endsAsCollection = true
			}
			continue
		}
		if strings.Contains(seg, "\\") {
			return nil, errf(CodeBackslash, "segment %q: backslashes are not allowed (use /)", seg)
		}
		if strings.Contains(seg, "/") {
			// A percent-encoded "/" would decode to a separator the manifest path cannot
			// distinguish from a real one ("a%2F.." -> "a/.." after join): refuse, never re-split.
			return nil, errf(CodeEncodedSlash, "segment %q: a percent-encoded \"/\" is not a legal name octet", seg)
		}
		stack = append(stack, seg)
	}
	path := strings.Join(stack, "/")
	kind := KindFile
	switch {
	case path == "":
		kind = KindRoot
	case endsAsCollection:
		kind = KindCollection
	}

	// Query (§3.4).
	version, capability, ignored, err := parseQuery(rawQuery, space, kind)
	if err != nil {
		return nil, err
	}
	return &URI{Host: host, Port: port, Space: space, Type: "fs", Path: path, Kind: kind, Version: version, Capability: capability, Fragment: fragment, IgnoredQuery: ignored}, nil
}

// splitParts is the scheme/fragment/query/authority split shared by the
// resource parser and the discovery-address parser, so BOTH apply the identical
// refusals before anything else is looked at: not "acp" (no "acp+http"), an
// empty authority, userinfo (the ironclad rule), a malformed host/port.
type splitParts struct {
	host     string
	port     int
	fragment *string
	rawQuery string
	rawPath  string
}

func splitURI(input string) (*splitParts, error) {
	if input == "" {
		return nil, errf(CodeScheme, "empty URI")
	}
	m := schemeRE.FindStringSubmatch(input)
	if m == nil {
		return nil, errf(CodeScheme, "not an acp:// URI: %q", input)
	}
	scheme := asciiLower(m[1])
	if scheme != "acp" {
		if scheme == "acp+http" {
			return nil, errf(CodeScheme, `there is no "acp+http" form: acp:// implies TLS`)
		}
		return nil, errf(CodeScheme, "scheme %q is not \"acp\"", m[1])
	}
	rest := m[2]

	// Fragment: client-only, split off first (it may contain '?' and '/').
	var fragment *string
	if hash := strings.Index(rest, "#"); hash >= 0 {
		f, err := pctDecode(rest[hash+1:], "fragment")
		if err != nil {
			return nil, err
		}
		fragment = &f
		rest = rest[:hash]
	}
	// Query.
	rawQuery := ""
	if q := strings.Index(rest, "?"); q >= 0 {
		rawQuery = rest[q+1:]
		rest = rest[:q]
	}
	// Authority: up to the first '/'.
	authority, rawPath := rest, ""
	if slash := strings.Index(rest, "/"); slash >= 0 {
		authority, rawPath = rest[:slash], rest[slash:]
	}
	if authority == "" {
		return nil, errf(CodeAuthority, "missing authority (host[:port])")
	}
	if strings.Contains(authority, "@") {
		return nil, errf(CodeUserinfo, "userinfo (user:pass@host) is forbidden — a credential must never ride in the URI")
	}
	host, port, err := parseAuthority(authority)
	if err != nil {
		return nil, err
	}
	return &splitParts{host: host, port: port, fragment: fragment, rawQuery: rawQuery, rawPath: rawPath}, nil
}

func isReservedType(t string) bool {
	for _, r := range ReservedTypes {
		if r == t {
			return true
		}
	}
	return false
}

func parseAuthority(authority string) (string, int, error) {
	hostPart := authority
	var portPart *string
	if strings.HasPrefix(authority, "[") {
		close := strings.Index(authority, "]")
		if close < 0 {
			return "", 0, errf(CodeHost, "unterminated IPv6 literal in %q", authority)
		}
		hostPart = authority[:close+1]
		tail := authority[close+1:]
		if tail != "" {
			if !strings.HasPrefix(tail, ":") {
				return "", 0, errf(CodeHost, "garbage after IPv6 literal: %q", tail)
			}
			p := tail[1:]
			portPart = &p
		}
	} else if colon := strings.LastIndex(authority, ":"); colon >= 0 {
		hostPart = authority[:colon]
		p := authority[colon+1:]
		portPart = &p
	}
	host := asciiLower(hostPart)
	if host == "" || !hostRE.MatchString(host) {
		return "", 0, errf(CodeHost, "invalid host %q", hostPart)
	}
	port := DefaultPort
	if portPart != nil {
		if !portRE.MatchString(*portPart) {
			return "", 0, errf(CodePort, "invalid port %q", *portPart)
		}
		n, _ := strconv.Atoi(*portPart)
		if n < 1 || n > 65535 {
			return "", 0, errf(CodePort, "port %d out of range", n)
		}
		port = n
	}
	return host, port, nil
}

// safeInt parses a decimal string that already matched a digits-only regexp
// and reports whether it is a JavaScript-safe integer (parity with
// Number.isSafeInteger in the TS resolver).
func safeInt(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n > maxSafeInteger {
		return 0, false
	}
	return n, true
}

func parseQuery(rawQuery, space string, kind ResourceKind) (*int64, *Capability, []string, error) {
	seen := map[string][]string{}
	order := []string{} // keys in order of first appearance (the TS Map's iteration order)
	if rawQuery != "" {
		for _, pair := range strings.Split(rawQuery, "&") {
			if pair == "" {
				continue
			}
			rawK, rawV := pair, ""
			if eq := strings.Index(pair, "="); eq >= 0 {
				rawK, rawV = pair[:eq], pair[eq+1:]
			}
			k, err := pctDecode(rawK, "query key")
			if err != nil {
				return nil, nil, nil, err
			}
			v, err := pctDecode(rawV, "query value")
			if err != nil {
				return nil, nil, nil, err
			}
			if k == "" {
				return nil, nil, nil, errf(CodeQuery, "empty query key")
			}
			if _, ok := seen[k]; !ok {
				order = append(order, k)
			}
			seen[k] = append(seen[k], v)
		}
	}
	// The ironclad rule, defense-in-depth: a credential-shaped key is refused.
	for _, k := range order {
		if credentialKeys[asciiLower(k)] {
			return nil, nil, nil, errf(CodeRawToken, "query key %q looks like a raw credential — a token must never ride in an acp:// URI (use a profile or a signed capability link)", k)
		}
	}
	// Reserved: v= / cp= (mutually exclusive; cp has no v1 resolution — D2: refuse).
	var version *int64
	vs, hasV := seen["v"]
	if hasV {
		if len(vs) != 1 {
			return nil, nil, nil, errf(CodeVersionInvalid, `"v" must appear at most once`)
		}
		n, ok := safeInt(vs[0])
		if !versionRE.MatchString(vs[0]) || !ok {
			return nil, nil, nil, errf(CodeVersionInvalid, "v=%q is not a manifest version", vs[0])
		}
		version = &n
	}
	if _, hasCP := seen["cp"]; hasCP {
		if hasV {
			return nil, nil, nil, errf(CodePinConflict, `"v" and "cp" are mutually exclusive`)
		}
		return nil, nil, nil, errf(CodeCheckpointUnsupported, `"cp" (checkpoint pin) is reserved for space checkpoints (ext-30) and has no resolution yet — refusing rather than silently reading the current version`)
	}
	// The shareable-link namespace (D1): h + the signed params, all-or-nothing, fail-closed.
	var capability *Capability
	present := []string{}
	for _, k := range linkKeys {
		if _, ok := seen[k]; ok {
			present = append(present, k)
		}
	}
	if len(present) > 0 {
		for _, k := range linkKeys {
			if vals, ok := seen[k]; !ok || len(vals) != 1 {
				return nil, nil, nil, errf(CodeCapabilityMalformed, "a signed link needs each of h, sp, e, k, s exactly once (have: %s)", strings.Join(present, ","))
			}
		}
		h, sp, e, k, s := seen["h"][0], seen["sp"][0], seen["e"][0], seen["k"][0], seen["s"][0]
		if !hashRE.MatchString(h) {
			return nil, nil, nil, errf(CodeCapabilityMalformed, `link "h" (blob hash) must be 64 lowercase-hex bytes`)
		}
		en, ok := safeInt(e)
		if !expiryRE.MatchString(e) || !ok {
			return nil, nil, nil, errf(CodeCapabilityMalformed, "capability \"e\" (expiry) is not unix seconds: %q", e)
		}
		if k == "" {
			return nil, nil, nil, errf(CodeCapabilityMalformed, `capability "k" (key id) is empty`)
		}
		if !hashRE.MatchString(s) {
			return nil, nil, nil, errf(CodeCapabilityMalformed, `capability "s" must be 64 lowercase-hex bytes`)
		}
		if sp != space {
			return nil, nil, nil, errf(CodeCapabilitySpaceMismatch, "capability signed for space %q but the URI names %q — the URI's space binds and cannot be overridden", sp, space)
		}
		if kind != KindFile {
			return nil, nil, nil, errf(CodeCapabilityUnsupportedResource, "a signed link names ONE file (its blob); a collection/root link awaits the path-signed capability successor")
		}
		capability = &Capability{H: h, SP: sp, E: en, K: k, S: s}
	}
	ignored := []string{}
	for _, k := range order {
		if k != "v" && !isLinkKey(k) {
			ignored = append(ignored, k)
		}
	}
	return version, capability, ignored, nil
}

// capabilityQuery is the signed params in the daemon's own canonical (key-sorted) query order.
func capabilityQuery(c *Capability) string {
	return fmt.Sprintf("e=%d&k=%s&s=%s&sp=%s", c.E, pctEncodeQueryValue(c.K), c.S, pctEncodeQueryValue(c.SP))
}

// Canonical returns the canonical string (§3.7): two URIs naming the same
// resource canonicalize to byte-identical strings. The default port is omitted
// (D3); the fragment is excluded (not part of identity); reserved query keys
// sort ascending.
func Canonical(u *URI) string {
	var sb strings.Builder
	sb.WriteString("acp://")
	sb.WriteString(u.Host)
	if u.Port != DefaultPort {
		sb.WriteString(":")
		sb.WriteString(strconv.Itoa(u.Port))
	}
	sb.WriteString("/")
	sb.WriteString(u.Space)
	sb.WriteString("/")
	if u.Path != "" {
		segs := strings.Split(u.Path, "/")
		for i, s := range segs {
			if i > 0 {
				sb.WriteString("/")
			}
			sb.WriteString(pctEncodeSegment(s))
		}
		if u.Kind == KindCollection {
			sb.WriteString("/")
		}
	}
	qs := []string{}
	if u.Version != nil {
		qs = append(qs, "v="+strconv.FormatInt(*u.Version, 10))
	}
	if u.Capability != nil {
		qs = append(qs, "h="+u.Capability.H)
		qs = append(qs, strings.Split(capabilityQuery(u.Capability), "&")...)
	}
	sort.Strings(qs) // ascending by key: e, h, k, s, sp, v
	if len(qs) > 0 {
		sb.WriteString("?")
		sb.WriteString(strings.Join(qs, "&"))
	}
	return sb.String()
}

// CanonicalString parses s and returns its canonical form.
func CanonicalString(s string) (string, error) {
	u, err := Parse(s)
	if err != nil {
		return "", err
	}
	return Canonical(u), nil
}

// SameResource reports equality of resource identity (§3.7): canonical forms
// byte-equal, fragment excluded. Either input may be a string or a *URI; a
// string that does not parse is never the same resource as anything (and the
// refusal is returned).
func SameResource(a, b any) (bool, error) {
	ca, err := canonicalOf(a)
	if err != nil {
		return false, err
	}
	cb, err := canonicalOf(b)
	if err != nil {
		return false, err
	}
	return ca == cb, nil
}

func canonicalOf(v any) (string, error) {
	switch x := v.(type) {
	case *URI:
		return Canonical(x), nil
	case string:
		return CanonicalString(x)
	default:
		return "", errf(CodeScheme, "not a URI: %T", v)
	}
}

// Profile is a local credential profile (ext-32 §3.6 model 1). Mirrors the
// `acp config` profile shape (~/.acp/config.json): the credential lives HERE,
// never in a URI. Server is the daemon base ("https://host:port").
type Profile struct {
	Name   string
	Server string
	Token  string
	Agent  string
	// Space is the profile's preferred space (a tie-breaker when several profiles share a host).
	Space string
	// Default marks the caller's SELECTED profile (ACP_PROFILE, else the config's
	// default_profile). When several profiles share a host:port and NONE matches
	// the URI's space, Find prefers the default over an alphabetically-earlier one
	// (CL-1-adjacent: credential SELECTION correctness — the tie-break must honour
	// the operator's chosen default, not "whichever key sorted first").
	Default bool
	// Cert is the pinned daemon cert path (PEM); "" = system roots.
	Cert string
	// Insecure skips TLS verification when Cert is "" (dev only) — the Go SDK's
	// client.New flag; the TS Profile has no equivalent (its fetch trusts the platform roots).
	Insecure bool
}

// ProfileStore looks up the profile for a host + port and space.
type ProfileStore interface {
	Find(host string, port int, space string) *Profile
}

// ProfileServerHostPort parses a profile's Server into (host, port) (the https
// default 443 applies to the URL); refuses a non-https base. The host is
// returned in the same form Parse uses (lowercase; an IPv6 literal bracketed).
func ProfileServerHostPort(server string) (string, int, error) {
	u, err := url.Parse(server)
	if err != nil || u.Host == "" {
		return "", 0, errf(CodeNoProfile, "profile server %q is not a URL", server)
	}
	if u.Scheme != "https" {
		return "", 0, errf(CodeNoProfile, "profile server %q must be https (acp:// implies TLS)", server)
	}
	host := asciiLower(u.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	port := 443
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return "", 0, errf(CodeNoProfile, "profile server %q is not a URL", server)
		}
		port = n
	}
	return host, port, nil
}

// StaticProfileStore is an in-memory ProfileStore over a list of profiles: it
// matches by host and port (the URI's port — explicit or the 8443 default —
// must equal the profile server's), and prefers a profile whose Space equals
// the URI's space.
type StaticProfileStore struct {
	Profiles []Profile
}

// NewStaticProfileStore builds a StaticProfileStore.
func NewStaticProfileStore(profiles []Profile) *StaticProfileStore {
	return &StaticProfileStore{Profiles: profiles}
}

// Find implements ProfileStore.
func (s *StaticProfileStore) Find(host string, port int, space string) *Profile {
	h := asciiLower(host)
	var best *Profile
	for i := range s.Profiles {
		p := &s.Profiles[i]
		ph, pp, err := ProfileServerHostPort(p.Server)
		if err != nil {
			continue // a malformed profile never matches (it cannot be resolved to https)
		}
		if ph != h || pp != port {
			continue
		}
		if p.Space == space {
			return p // exact space preference wins immediately
		}
		// No exact-space match: prefer the operator's SELECTED (Default) profile
		// over an alphabetically-earlier one; otherwise keep the first match as a
		// deterministic fallback.
		if best == nil || (p.Default && !best.Default) {
			best = p
		}
	}
	return best
}

// WireRead is one acp/1 request the resolver maps a URI to (deterministic, credential-free).
type WireRead struct {
	Method string `json:"method"`
	// Path is the root-relative `/v1/...` path (+ the signed query for a
	// capability read) — NEVER contains the space as a path segment, never a bearer token.
	Path string `json:"path"`
	// Locate: for a profile file read, the blob GET's hash is looked up from the manifest at this path.
	Locate string `json:"locate,omitempty"`
	// Prefix: for a collection read, the manifest listing is filtered to this prefix (with trailing "/").
	Prefix string `json:"prefix,omitempty"`
	// Tokenless: for a capability read, the request carries NO Authorization header — the signature authorizes it.
	Tokenless bool `json:"tokenless,omitempty"`
}

// WireWrite is one acp/1 request a WRITE to the named resource maps to (§3.5:
// "PUT blob + POST commit (CAS)" for a CAS region). Descriptive and
// credential-free — a mount's write verbs execute it; the resolver only NAMES
// the target.
type WireWrite struct {
	Method string `json:"method"`
	// Path is the root-relative `/v1/...` path — NEVER contains the space.
	Path string `json:"path"`
	// Locate is the manifest path the commit Change targets.
	Locate string `json:"locate,omitempty"`
	Note   string `json:"note"`
}

// AuthModel says which model authorizes the requests.
type AuthModel string

const (
	// AuthProfile: the caller's local credential in the Authorization header.
	AuthProfile AuthModel = "profile"
	// AuthCapability: the link's own signed grant; tokenless.
	AuthCapability AuthModel = "capability"
)

// Resolved is the deterministic result of resolving a URI against the frozen acp/1 wire (§3.5).
type Resolved struct {
	URI       *URI   `json:"uri"`
	Canonical string `json:"canonical"`
	// BaseURL is "https://host:port" — TLS, always (the port is explicit here even when the canonical form omits it).
	BaseURL string    `json:"baseUrl"`
	Space   string    `json:"space"`
	Auth    AuthModel `json:"auth"`
	// Headers carries the space for profile reads (never the URL path). Empty for a capability read (the space is bound in the signature).
	Headers map[string]string `json:"headers"`
	// Reads is the request sequence the URI denotes.
	Reads []WireRead `json:"reads"`
	// Mode is the write surface: on the frozen wire every region is a CAS
	// region ("cas": PUT /v1/blobs then POST /v1/commit). A realtime region
	// (ext-31/ext-28) is not resolvable until those ship — the resolver never
	// probes an endpoint the frozen wire does not have.
	Mode string `json:"mode"`
	// Writes is the write surface a mutation of this resource maps to. A capability link is read-only: none.
	Writes []WireWrite `json:"writes"`
	// PinnedVersion is the `?v=` pin, or nil. A reader MUST refuse to serve any other version.
	PinnedVersion *int64 `json:"pinnedVersion"`
}

// Resolve maps a parsed URI to its acp/1 requests (§3.5). Pure: no I/O. A
// port-less URI resolves to the default port (D3). A signed file link (D1)
// resolves to the shipped tokenless capability fetch
// `GET /v1/cap/blob/<h>?e=&k=&s=&sp=`.
func Resolve(u *URI) *Resolved {
	r := &Resolved{
		URI:           u,
		Canonical:     Canonical(u),
		BaseURL:       "https://" + u.Host + ":" + strconv.Itoa(u.Port),
		Space:         u.Space,
		Mode:          "cas",
		PinnedVersion: u.Version,
		Headers:       map[string]string{},
		Reads:         []WireRead{},
		Writes:        []WireWrite{},
	}
	if u.Capability != nil {
		r.Auth = AuthCapability
		r.Reads = append(r.Reads, WireRead{Method: "GET", Path: "/v1/cap/blob/" + u.Capability.H + "?" + capabilityQuery(u.Capability), Tokenless: true})
		return r
	}
	r.Auth = AuthProfile
	r.Headers[wire.HeaderSpace] = u.Space
	switch u.Kind {
	case KindRoot:
		r.Reads = append(r.Reads, WireRead{Method: "GET", Path: "/v1/manifest"})
	case KindCollection:
		r.Reads = append(r.Reads, WireRead{Method: "GET", Path: "/v1/manifest", Prefix: u.Path + "/"})
	case KindFile:
		r.Reads = append(r.Reads,
			WireRead{Method: "GET", Path: "/v1/manifest", Locate: u.Path},
			WireRead{Method: "GET", Path: "/v1/blobs/<hash>", Locate: u.Path})
		r.Writes = append(r.Writes,
			WireWrite{Method: "POST", Path: "/v1/blobs", Note: "upload the bytes; returns {hash, size} (idempotent)"},
			WireWrite{Method: "POST", Path: "/v1/commit", Locate: u.Path, Note: "CAS on base_version; 409 = rebase, never last-writer-wins"})
	}
	// Collections/root have no write on the CAS wire: directories are implicit in manifest paths.
	return r
}

// ResolveString parses s and resolves it.
func ResolveString(s string) (*Resolved, error) {
	u, err := Parse(s)
	if err != nil {
		return nil, err
	}
	return Resolve(u), nil
}

// Lookup resolves a URI AND finds its credential in a profile store (model 1)
// WITHOUT building a client — pure, no I/O. The URI is a pure address; the
// profile is matched by the URI's host:port (space-preferred). A signed link is
// refused (it needs no profile); a missing profile, or one lacking a token or
// agent, is a no_profile refusal (never a silently unauthenticated result).
func Lookup(u *URI, profiles ProfileStore) (*Resolved, *Profile, error) {
	if u.Capability != nil {
		return nil, nil, errf(CodeCapabilityUnsupportedResource, "a signed link authorizes itself — read it with ReadCapability; it does not use a profile")
	}
	profile := profiles.Find(u.Host, u.Port, u.Space)
	if profile == nil {
		return nil, nil, errf(CodeNoProfile, "no profile for https://%s:%d — run `acp config set <profile> server=https://%s:%d token_file=...` (the URI itself never carries a credential)", u.Host, u.Port, u.Host, u.Port)
	}
	if profile.Token == "" || profile.Agent == "" {
		return nil, nil, errf(CodeNoProfile, "profile %q lacks a token or agent", profile.Name)
	}
	return Resolve(u), profile, nil
}

// ClientFor resolves a URI AND its credential from a profile store (model 1):
// the URI is a pure address; the token comes from local config keyed by
// host:port. Returns a ready *client.Client bound to the URI's space. The token
// is placed ONLY in the Client's Authorization header — never in any URL this
// SDK forms. A signed link needs no profile — use ReadCapability for it.
func ClientFor(u *URI, profiles ProfileStore) (*client.Client, *Resolved, *Profile, error) {
	resolved, profile, err := Lookup(u, profiles)
	if err != nil {
		return nil, nil, nil, err
	}
	c, err := client.New(resolved.BaseURL, profile.Token, profile.Agent, profile.Cert, profile.Insecure)
	if err != nil {
		return nil, nil, nil, err
	}
	c.SetSpace(resolved.Space)
	return c, resolved, profile, nil
}

// ReadOptions configures ReadCapability.
type ReadOptions struct {
	// HTTPClient performs the single tokenless GET (TLS trust/pinning is the
	// caller's: a pinned daemon cert goes in its Transport). nil = http.DefaultClient.
	HTTPClient *http.Client
}

// ReadCapability dereferences a SIGNED FILE LINK (D1): one tokenless GET of
// the shipped capability fetch path; no profile, no Authorization header. The
// daemon validates fail-closed (parse -> expiry 410 -> signature 403 ->
// existence 404) and streams the exact bytes the link was minted for. Errors
// surface as *client.APIError with the daemon's status (a leaked link EXPIRES;
// a tampered signature is refused). The caller closes the returned body.
func ReadCapability(u *URI, opts ReadOptions) (io.ReadCloser, error) {
	if u.Capability == nil {
		return nil, errf(CodeCapabilityMalformed, "not a signed link: it carries no h/sp/e/k/s — resolve it through a profile instead")
	}
	r := Resolve(u)
	hc := opts.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	req, err := http.NewRequest(http.MethodGet, r.BaseURL+r.Reads[0].Path, nil) // deliberately NO headers: tokenless
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		msg := fmt.Sprintf("capability fetch: %d", resp.StatusCode)
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &e) == nil && e.Error != "" {
			msg = e.Error
		}
		return nil, &client.APIError{Status: resp.StatusCode, Message: msg, Raw: body}
	}
	return resp.Body, nil
}

// LinkFromBlobURL turns a minted blob-capability URL (the daemon's
// `/v1/cap/blob/<hash>?sp=&e=&k=&s=` returned by Client.MintBlobURL) into a
// shareable `acp://` FILE LINK for the manifest path the blob is committed at
// (D1). The result is canonical.
func LinkFromBlobURL(blobURL, path string) (string, error) {
	u, err := url.Parse(blobURL)
	if err != nil {
		return "", errf(CodeCapabilityMalformed, "not a URL: %q", blobURL)
	}
	m := capPathRE.FindStringSubmatch(u.EscapedPath())
	if u.Scheme != "https" || m == nil {
		return "", errf(CodeCapabilityMalformed, "not a blob-capability URL (expected https://host/v1/cap/blob/<hash>?sp=&e=&k=&s=)")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", errf(CodeCapabilityMalformed, "blob-capability URL: malformed query")
	}
	// The ironclad rule applies to the INPUT too: a credential-shaped key riding on a
	// minted URL is refused, never silently dropped on the way into a shareable link.
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if credentialKeys[asciiLower(k)] {
			return "", errf(CodeRawToken, "blob-capability URL carries a credential-shaped key %q — refusing to build a link from it", k)
		}
	}
	get := func(k string) (string, error) {
		vals := q[k]
		if len(vals) != 1 {
			return "", errf(CodeCapabilityMalformed, "blob-capability URL: %q must appear exactly once", k)
		}
		return vals[0], nil
	}
	sp, err := get("sp")
	if err != nil {
		return "", err
	}
	e, err := get("e")
	if err != nil {
		return "", err
	}
	k, err := get("k")
	if err != nil {
		return "", err
	}
	s, err := get("s")
	if err != nil {
		return "", err
	}
	host := u.Hostname()
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	port := 443
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return "", errf(CodeCapabilityMalformed, "not a URL: %q", blobURL)
		}
		port = n
	}
	link, err := Format(Parts{Host: host, Port: port, Space: sp, Path: path, Kind: KindFile})
	if err != nil {
		return "", err
	}
	qs := []string{"h=" + m[1], "sp=" + pctEncodeQueryValue(sp), "e=" + pctEncodeQueryValue(e), "k=" + pctEncodeQueryValue(k), "s=" + pctEncodeQueryValue(s)}
	// Round-trip through the parser: a malformed capability is refused, never emitted.
	return CanonicalString(link + "?" + strings.Join(qs, "&"))
}

// Parts are the inputs of Format. By construction there is no field for a
// bearer credential — the type system enforces the ironclad rule. (A signed
// link is built by LinkFromBlobURL from a minted URL.)
type Parts struct {
	Host string
	// Port: 0 = omit (the default port applies).
	Port  int
	Space string
	Path  string
	// Kind: KindCollection appends the trailing slash; anything else names a file (or the root when Path is empty).
	Kind ResourceKind
	// Version: a `?v=` pin, or nil.
	Version *int64
}

// Format builds a canonical acp:// URI from parts. A leading/trailing "/" in
// Path is tolerated (normalized away); an INTERIOR empty segment ("a//b") is
// refused exactly as the parser refuses it — the builder never silently
// rewrites a spelling the grammar rejects.
func Format(p Parts) (string, error) {
	segs := strings.Split(p.Path, "/")
	if len(segs) > 0 && segs[0] == "" {
		segs = segs[1:]
	}
	if len(segs) > 0 && segs[len(segs)-1] == "" {
		segs = segs[:len(segs)-1]
	}
	for _, s := range segs {
		if s == "" {
			return "", errf(CodeEmptySegment, `path %q: empty path segment ("//") is not allowed`, p.Path)
		}
	}
	var sb strings.Builder
	sb.WriteString("acp://")
	sb.WriteString(asciiLower(p.Host))
	if p.Port != 0 {
		sb.WriteString(":")
		sb.WriteString(strconv.Itoa(p.Port))
	}
	sb.WriteString("/")
	sb.WriteString(p.Space)
	sb.WriteString("/")
	if len(segs) > 0 {
		for i, s := range segs {
			if i > 0 {
				sb.WriteString("/")
			}
			sb.WriteString(pctEncodeSegment(s))
		}
		if p.Kind == KindCollection {
			sb.WriteString("/")
		}
	}
	if p.Version != nil {
		sb.WriteString("?v=")
		sb.WriteString(strconv.FormatInt(*p.Version, 10))
	}
	// Round-trip through the parser so a malformed input is refused, never emitted.
	return CanonicalString(sb.String())
}

// The acp:// resolver (ext-32 + the 2026-09-11 decisions) — the parse table,
// canonicalization pairs (default port 8443 omitted — D3), the deterministic
// wire mapping, profile-based auth, the signed FILE LINK (h= + sp/e/k/s -> the
// tokenless /v1/cap/blob/ fetch — D1), and the MALICIOUS inputs the grammar
// must refuse fail-closed: a raw token anywhere in the URL, userinfo, an
// unknown or reserved "@type", traversal out of the space, empty segments, a
// backslash, a partial/mismatched capability, cp= (refused — D2).
//
// PARITY: this file is a row-for-row port of the TS SDK's
// acp/sdk/ts/test/uri.test.mjs (each Test* below names the TS test it
// mirrors). The "wire" tests use an httptest TLS server on a random loopback
// port with t.Cleanup — never the production daemon.
package acpuri

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/ab0t-com/acp/sdk/go/pkg/client"
)

var (
	sig = strings.Repeat("a", 64)
	hh  = strings.Repeat("b", 64)
	cap = "h=" + hh + "&sp=team&e=1782000300&k=k2&s=" + sig
)

// refuses asserts Parse refuses input with exactly the given code.
func refuses(t *testing.T, input string, code Code) *Error {
	t.Helper()
	_, err := Parse(input)
	if err == nil {
		t.Fatalf("%q: expected refusal %q, got acceptance", input, code)
	}
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("%q: expected *acpuri.Error, got %T: %v", input, err, err)
	}
	if e.Code != code {
		t.Fatalf("%q: expected code %q, got %q (%s)", input, code, e.Code, e.Msg)
	}
	if !strings.HasPrefix(e.Error(), "acp uri: ") {
		t.Fatalf("error text must be prefixed: %q", e.Error())
	}
	return e
}

func mustParse(t *testing.T, s string) *URI {
	t.Helper()
	u, err := Parse(s)
	if err != nil {
		t.Fatalf("%q: unexpected refusal: %v", s, err)
	}
	return u
}

func canon(t *testing.T, s string) string {
	t.Helper()
	c, err := CanonicalString(s)
	if err != nil {
		t.Fatalf("%q: %v", s, err)
	}
	return c
}

func same(t *testing.T, a, b any) bool {
	t.Helper()
	ok, err := SameResource(a, b)
	if err != nil {
		t.Fatalf("SameResource(%v, %v): %v", a, b, err)
	}
	return ok
}

func resolve(t *testing.T, s string) *Resolved {
	t.Helper()
	r, err := ResolveString(s)
	if err != nil {
		t.Fatalf("%q: %v", s, err)
	}
	return r
}

func i64(n int64) *int64 { return &n }

func eq(t *testing.T, got, want any, what string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s:\n got  %#v\n want %#v", what, got, want)
	}
}

// TS: "parse: the three shapes (file, collection, root)"
func TestParseThreeShapes(t *testing.T) {
	f := mustParse(t, "acp://coordd.example:8443/team/docs/spec.md")
	eq(t, []any{f.Host, f.Port, f.Space, f.Type, f.Path, f.Kind}, []any{"coordd.example", 8443, "team", "fs", "docs/spec.md", KindFile}, "file")
	c := mustParse(t, "acp://coordd.example:8443/team/docs/")
	eq(t, []any{c.Kind, c.Path}, []any{KindCollection, "docs"}, "collection")
	r := mustParse(t, "acp://coordd.example:8443/team/")
	eq(t, []any{r.Kind, r.Path}, []any{KindRoot, ""}, "root")
	// A space with no trailing slash is still the root.
	eq(t, mustParse(t, "acp://coordd.example:8443/team").Kind, KindRoot, "bare space")
}

// TS: "D3: the default port is 8443 — a port-less URI means :8443, an explicit port overrides"
func TestD3DefaultPort(t *testing.T) {
	if DefaultPort != 8443 {
		t.Fatal("DefaultPort must be 8443")
	}
	eq(t, mustParse(t, "acp://h.example/team/x").Port, 8443, "port-less")
	eq(t, mustParse(t, "acp://h.example:8443/team/x").Port, 8443, "explicit default")
	eq(t, mustParse(t, "acp://h.example:9443/team/x").Port, 9443, "explicit other")
	eq(t, resolve(t, "acp://h.example/team/x").BaseURL, "https://h.example:8443", "base default")
	eq(t, resolve(t, "acp://h.example:9443/team/x").BaseURL, "https://h.example:9443", "base other")
	// Canonical form omits :8443 (as https omits :443) and preserves any other port.
	eq(t, canon(t, "acp://h.example:8443/team/x"), "acp://h.example/team/x", "canon omit")
	eq(t, canon(t, "acp://h.example/team/x"), "acp://h.example/team/x", "canon bare")
	eq(t, canon(t, "acp://h.example:9443/team/x"), "acp://h.example:9443/team/x", "canon keep")
	if !same(t, "acp://h.example:8443/team/x", "acp://h.example/team/x") {
		t.Fatal(":8443 and port-less must be the same resource")
	}
	if same(t, "acp://h.example:9443/team/x", "acp://h.example/team/x") {
		t.Fatal(":9443 must differ from port-less")
	}
	// Resolution of the two spellings is identical (determinism across the default).
	eq(t, resolve(t, "acp://h.example:8443/team/docs/x.md"), resolve(t, "acp://h.example/team/docs/x.md"), "resolution across default")
}

// TS: "parse: the explicit @fs synonym canonicalizes to the bare form"
func TestParseFsSynonym(t *testing.T) {
	a := "acp://h.example:8443/team/@fs/docs/x.md"
	b := "acp://h.example/team/docs/x.md"
	eq(t, mustParse(t, a).Path, "docs/x.md", "path")
	eq(t, canon(t, a), b, "canonical")
	if !same(t, a, b) {
		t.Fatal("@fs must equal the bare form")
	}
	eq(t, mustParse(t, "acp://h.example:8443/team/@fs/").Kind, KindRoot, "@fs/ root")
	eq(t, mustParse(t, "acp://h.example:8443/team/@fs").Kind, KindRoot, "@fs root")
}

// TS: "parse: reserved @types refuse (no v1 resolution); unknown @types refuse (fail-closed)"
func TestParseReservedAndUnknownTypes(t *testing.T) {
	for _, typ := range []string{"doc", "log", "mail", "chan"} {
		refuses(t, "acp://h.example:8443/team/@"+typ+"/x", CodeReservedType)
	}
	refuses(t, "acp://h.example:8443/team/@future/x", CodeUnknownType)
	refuses(t, "acp://h.example:8443/team/@/x", CodeUnknownType)
	// A bare "@" later in the path is still the reserved sigil, never a path octet.
	refuses(t, "acp://h.example:8443/team/docs/@x", CodeReservedSigil)
}

// TS: "§3.3 amendment: the sigil is RAW — %40 names a literal '@' file; bare '@' is reserved syntax; canonical keeps %40"
func TestSigilIsRaw(t *testing.T) {
	// "@doc/x" is a legal manifest name; it is addressed as %40doc/x.
	lit := mustParse(t, "acp://h.example/team/%40doc/x")
	eq(t, []any{lit.Path, lit.Kind}, []any{"@doc/x", KindFile}, "literal @doc/x")
	eq(t, Canonical(lit), "acp://h.example/team/%40doc/x", "canonical keeps %40")
	eq(t, Resolve(lit).Reads[0].Locate, "@doc/x", "locate")
	// The reserved type and the literal file are DIFFERENT resources; the bare form is refused, not confused.
	refuses(t, "acp://h.example/team/@doc/x", CodeReservedType)
	if same(t, "acp://h.example/team/%40doc/x", "acp://h.example/team/%40fs/x") {
		t.Fatal("the literal files @doc/x and @fs/x must be different resources")
	}
	// "%40fs" is a file named "@fs", not the synonym; "@fs/%40doc/x" and "%40doc/x" are the same file.
	eq(t, mustParse(t, "acp://h.example/team/%40fs/x").Path, "@fs/x", "%40fs is a file")
	if !same(t, "acp://h.example/team/@fs/%40doc/x", "acp://h.example/team/%40doc/x") {
		t.Fatal("the @fs-prefixed and bare spellings of the literal @doc/x file must be the same resource")
	}
	// Deeper literal '@' names; a mid-segment '@' (never a sigil) stays literal and unencoded.
	eq(t, mustParse(t, "acp://h.example/team/docs/%40x/%40y.md").Path, "docs/@x/@y.md", "deep literal")
	eq(t, canon(t, "acp://h.example/team/docs/%40x/a%40b"), "acp://h.example/team/docs/%40x/a@b", "mid-segment @ literal")
	refuses(t, "acp://h.example/team/docs/@x", CodeReservedSigil)
	// A percent-encoded type name is not a type ("@f%73" is unknown syntax, refused; it is not "@fs").
	refuses(t, "acp://h.example/team/@f%73/x", CodeUnknownType)
	// The builder emits a leading '@' encoded, and it round-trips.
	built, err := Format(Parts{Host: "h.example", Space: "team", Path: "@doc/x"})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, built, "acp://h.example/team/%40doc/x", "format encodes leading @")
	eq(t, mustParse(t, built).Path, "@doc/x", "round-trip")
}

// TS: "parse: traversal out of the space root is refused; inner dot-segments resolve"
func TestParseTraversal(t *testing.T) {
	refuses(t, "acp://h.example:8443/team/../other/x", CodeTraversal)
	refuses(t, "acp://h.example:8443/team/docs/../../x", CodeTraversal)
	refuses(t, "acp://h.example:8443/team/@fs/../x", CodeTraversal)
	refuses(t, "acp://h.example:8443/team/%2e%2e/x", CodeTraversal) // percent-encoded ".."
	u := mustParse(t, "acp://h.example:8443/team/docs/./a/../spec.md")
	eq(t, []any{u.Path, u.Kind}, []any{"docs/spec.md", KindFile}, "inner dots")
	// A trailing "." or ".." denotes the parent collection (RFC3986 §5.2.4).
	p := mustParse(t, "acp://h.example:8443/team/docs/x/..")
	eq(t, []any{p.Kind, p.Path}, []any{KindCollection, "docs"}, "trailing ..")
}

// TS: "parse: empty segments, backslashes, control bytes, bad space names"
func TestParseSegmentsAndSpaceNames(t *testing.T) {
	refuses(t, "acp://h.example:8443/team/docs//x", CodeEmptySegment)
	refuses(t, "acp://h.example:8443/team/docs\\x", CodeBackslash)
	refuses(t, "acp://h.example:8443/team/docs%5Cx", CodeBackslash)
	refuses(t, "acp://h.example:8443/team/docs%00x", CodeControlChar)
	// A percent-encoded "/" is refused: it would decode into a separator ("a%2F.." -> "a/..").
	refuses(t, "acp://h.example:8443/team/docs/my%2Ffile", CodeEncodedSlash)
	refuses(t, "acp://h.example:8443/team/docs/a%2F..%2F..%2Fetc", CodeEncodedSlash)
	refuses(t, "acp://h.example:8443/te am/x", CodeSpaceInvalid)
	refuses(t, "acp://h.example:8443/te%20am/x", CodeSpaceInvalid)
	refuses(t, "acp://h.example:8443/"+strings.Repeat("a", 65)+"/x", CodeSpaceInvalid)
	refuses(t, "acp://h.example:8443/../x", CodeSpaceInvalid)
	refuses(t, "acp://h.example:8443//x", CodeSpaceRequired)
	// A bare host names no RESOURCE (D4.7): still refused here, with a code that
	// points at discovery — the bare form is consumed only by Discover.
	refuses(t, "acp://h.example:8443/", CodeBareHost)
	refuses(t, "acp://h.example:8443", CodeBareHost)
}

// TS: "parse: authority — no userinfo, valid host/port, IPv6 literal"
func TestParseAuthority(t *testing.T) {
	refuses(t, "acp://user:pass@h.example:8443/team/x", CodeUserinfo)
	refuses(t, "acp://tok-abc123@h.example:8443/team/x", CodeUserinfo)
	refuses(t, "acp://:8443/team/x", CodeHost)
	refuses(t, "acp://h.example:abc/team/x", CodePort)
	refuses(t, "acp://h.example:70000/team/x", CodePort)
	refuses(t, "acp://h.example:0/team/x", CodePort)
	refuses(t, "acp:///team/x", CodeAuthority)
	v6 := mustParse(t, "acp://[::1]:8443/team/x")
	eq(t, []any{v6.Host, v6.Port}, []any{"[::1]", 8443}, "ipv6")
	eq(t, mustParse(t, "acp://[::1]/team/x").Port, DefaultPort, "ipv6 default port")
	// Host is case-insensitive (lowercased); space and path are byte-significant.
	u := mustParse(t, "acp://H.Example:8443/Team/Docs/X.md")
	eq(t, []any{u.Host, u.Space, u.Path}, []any{"h.example", "Team", "Docs/X.md"}, "case")
}

// TS: "parse: scheme — case-insensitive 'acp', no acp+http, nothing else"
func TestParseScheme(t *testing.T) {
	eq(t, mustParse(t, "ACP://h.example:8443/team/x").Host, "h.example", "ACP upper")
	refuses(t, "acp+http://h.example:8443/team/x", CodeScheme)
	refuses(t, "https://h.example:8443/team/x", CodeScheme)
	refuses(t, "acp:/h.example/team/x", CodeScheme)
	refuses(t, "", CodeScheme)
}

// TS: "THE IRONCLAD RULE: a raw token anywhere in the URL is refused"
func TestIroncladRule(t *testing.T) {
	codes := map[string]Code{
		"acp://h.example:8443/team/x?token=abc":                   CodeRawToken,
		"acp://h.example:8443/team/x?access_token=abc":            CodeRawToken,
		"acp://h.example:8443/team/x?Authorization=Bearer%20abc":  CodeRawToken,
		"acp://h.example:8443/team/x?v=1&acp_token=abc":           CodeRawToken,
		"acp://h.example:8443/team/x?secret=abc":                  CodeRawToken,
		"acp://h.example:8443/team/x?api_key=abc":                 CodeRawToken,
		"acp://h.example:8443/team/x?" + cap + "&token=abc":       CodeRawToken, // even beside a valid signed link
		"acp://gate-deadbeef@h.example:8443/team/x":               CodeUserinfo,
		"acp://h.example:8443/team/x?bearer=abc":                  CodeRawToken,
		"acp://h.example:8443/team/x?auth=abc":                    CodeRawToken,
		"acp://h.example:8443/team/x?password=abc":                CodeRawToken,
		"acp://h.example:8443/team/x?passwd=abc":                  CodeRawToken,
		"acp://h.example:8443/team/x?apikey=abc":                  CodeRawToken,
		"acp://h.example:8443/team/x?x-acp-token=abc":             CodeRawToken,
		"acp://h.example:8443/team/x?TOKEN=abc":                   CodeRawToken, // case-folded key
		"acp://h.example:8443/team/x?%74oken=abc":                 CodeRawToken, // percent-encoded key spelling
		"acp://h.example:8443/team/x?token":                       CodeRawToken, // key with no "=" at all
		"acp://h.example:8443/team/x?foo=1&token=abc#frag":        CodeRawToken, // after an ignored key, before a fragment
		"acp://h.example:8443/team/docs/?token=abc":               CodeRawToken, // on a collection
		"acp://h.example:8443/team/?token=abc":                    CodeRawToken, // on the root
		"acp://h.example:8443/team/x?token=":                      CodeRawToken, // empty value is still the key
		"acp://user@h.example/team/x":                             CodeUserinfo,
		"acp://@h.example/team/x":                                 CodeUserinfo,
		"acp://h.example:8443@evil.example/team/x":                CodeUserinfo,
		"acp://tok%40h.example/team/x":                            CodeHost, // "%40" in the authority is not a host octet either
		"acp://h.example:8443/team/x?" + cap + "&Authorization=x": CodeRawToken,
	}
	keys := make([]string, 0, len(codes))
	for k := range codes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, input := range keys {
		refuses(t, input, codes[input])
	}
	// And the builder has no way to express one at all.
	built, err := Format(Parts{Host: "h.example", Port: 8443, Space: "team", Path: "docs/x.md"})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, built, "acp://h.example/team/docs/x.md", "built")
	if strings.Contains(built, "token") {
		t.Fatal("built URI must not contain a token")
	}
	// Parts is the whole builder input: by construction there is no credential field.
	for _, f := range reflect.VisibleFields(reflect.TypeOf(Parts{})) {
		if credentialKeys[asciiLower(f.Name)] {
			t.Fatalf("Parts must not have a credential-shaped field: %s", f.Name)
		}
	}
}

// TS: "query: v= pin, cp= refused (D2), mutual exclusion, unknown keys ignored"
func TestQueryPins(t *testing.T) {
	u := mustParse(t, "acp://h.example:8443/team/x?v=12&foo=bar")
	eq(t, u.Version, i64(12), "v=12")
	eq(t, u.IgnoredQuery, []string{"foo"}, "ignored")
	eq(t, mustParse(t, "acp://h.example:8443/team/x?v=0").Version, i64(0), "v=0")
	refuses(t, "acp://h.example:8443/team/x?v=abc", CodeVersionInvalid)
	refuses(t, "acp://h.example:8443/team/x?v=-1", CodeVersionInvalid)
	refuses(t, "acp://h.example:8443/team/x?v=01", CodeVersionInvalid)
	refuses(t, "acp://h.example:8443/team/x?v=1&v=2", CodeVersionInvalid)
	refuses(t, "acp://h.example:8443/team/x?v=9007199254740993", CodeVersionInvalid) // beyond the JS safe-integer bound (parity)
	refuses(t, "acp://h.example:8443/team/x?v=99999999999999999999", CodeVersionInvalid)
	refuses(t, "acp://h.example:8443/team/x?v=", CodeVersionInvalid)
	refuses(t, "acp://h.example:8443/team/x?cp=release-1", CodeCheckpointUnsupported)
	refuses(t, "acp://h.example:8443/team/x?v=1&cp=release-1", CodePinConflict)
	refuses(t, "acp://h.example:8443/team/x?=1", CodeQuery) // empty key
	refuses(t, "acp://h.example:8443/team/x?a%zz=1", CodePercentEncoding)
	refuses(t, "acp://h.example:8443/team/x?a=%00", CodeControlChar)
	eq(t, mustParse(t, "acp://h.example:8443/team/x?&&").IgnoredQuery, []string{}, "empty pairs skipped")
	eq(t, mustParse(t, "acp://h.example:8443/team/x?a=1&b=2&a=3").IgnoredQuery, []string{"a", "b"}, "first-appearance order, deduped")
}

// TS: "D1: the signed FILE LINK (h= + sp/e/k/s) is fail-closed and resolves to the tokenless capability fetch"
func TestD1SignedFileLink(t *testing.T) {
	ok := mustParse(t, "acp://h.example:8443/team/docs/x.md?"+cap)
	eq(t, ok.Capability, &Capability{H: hh, SP: "team", E: 1782000300, K: "k2", S: sig}, "capability")
	// All-or-nothing: partial / repeated / malformed parameters are an error, never an unsigned read.
	refuses(t, "acp://h.example:8443/team/x?sp=team&e=1&k=k2&s="+sig, CodeCapabilityMalformed) // no h
	refuses(t, "acp://h.example:8443/team/x?h="+hh, CodeCapabilityMalformed)                   // h without a signature
	refuses(t, "acp://h.example:8443/team/x?h="+hh+"&sp=team&e=1&k=k2", CodeCapabilityMalformed)
	refuses(t, "acp://h.example:8443/team/x?"+cap+"&s="+sig, CodeCapabilityMalformed)
	refuses(t, "acp://h.example:8443/team/x?"+cap+"&h="+hh, CodeCapabilityMalformed)
	refuses(t, "acp://h.example:8443/team/x?h="+hh+"&sp=team&e=soon&k=k2&s="+sig, CodeCapabilityMalformed)
	refuses(t, "acp://h.example:8443/team/x?h="+hh+"&sp=team&e=1&k=k2&s="+strings.Repeat("A", 64), CodeCapabilityMalformed)
	refuses(t, "acp://h.example:8443/team/x?h="+hh+"&sp=team&e=1&k=&s="+sig, CodeCapabilityMalformed)
	refuses(t, "acp://h.example:8443/team/x?h="+strings.Repeat("B", 64)+"&sp=team&e=1&k=k2&s="+sig, CodeCapabilityMalformed)
	refuses(t, "acp://h.example:8443/team/x?h="+hh[1:]+"&sp=team&e=1&k=k2&s="+sig, CodeCapabilityMalformed)
	// The URI's space binds; a capability for another space cannot override it.
	refuses(t, "acp://h.example:8443/team/x?h="+hh+"&sp=other&e=1&k=k2&s="+sig, CodeCapabilitySpaceMismatch)
	// A signed link names ONE file; a collection/root link awaits the path-signed successor.
	refuses(t, "acp://h.example:8443/team/docs/?"+cap, CodeCapabilityUnsupportedResource)
	refuses(t, "acp://h.example:8443/team/?"+cap, CodeCapabilityUnsupportedResource)
	// Resolution: ONE tokenless GET of the shipped capability path; the signed params in the daemon's key order.
	r := Resolve(ok)
	eq(t, r.Auth, AuthCapability, "auth")
	eq(t, r.BaseURL, "https://h.example:8443", "base")
	eq(t, r.Headers, map[string]string{}, "no headers")
	eq(t, r.Writes, []WireWrite{}, "no writes")
	eq(t, r.Reads, []WireRead{{Method: "GET", Path: "/v1/cap/blob/" + hh + "?e=1782000300&k=k2&s=" + sig + "&sp=team", Tokenless: true}}, "reads")
	if strings.Contains(r.Reads[0].Path, "/team/") {
		t.Fatal("the space is a signed param, never a path segment")
	}
	// Canonical form: ascending keys, default port omitted; the two spellings are the same resource.
	eq(t, r.Canonical, "acp://h.example/team/docs/x.md?e=1782000300&h="+hh+"&k=k2&s="+sig+"&sp=team", "canonical")
	if !same(t, "acp://H.EXAMPLE/team/docs/x.md?s="+sig+"&k=k2&e=1782000300&h="+hh+"&sp=team#L3", ok) {
		t.Fatal("reordered/cased/fragmented spelling must be the same resource")
	}
	// A signed link needs no profile — and a profile lookup for it is refused, not silently attempted.
	_, _, _, err := ClientFor(ok, NewStaticProfileStore(nil))
	if CodeOf(err) != CodeCapabilityUnsupportedResource {
		t.Fatalf("ClientFor on a signed link: want capability_unsupported_resource, got %v", err)
	}
}

// TS: "D1: readACPCapability dereferences a signed link with ONE tokenless request (no Authorization header)"
func TestD1ReadCapabilityTokenless(t *testing.T) {
	type seen struct {
		url     string
		headers http.Header
	}
	var got []seen
	var status int
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		got = append(got, seen{url: r.URL.String(), headers: r.Header.Clone()})
		if status != 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(map[string]string{"error": "stub " + strconv.Itoa(status)})
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte{1, 2, 3})
	})
	ts := httptest.NewTLSServer(mux)
	t.Cleanup(ts.Close)
	hc := ts.Client()
	uri := "acp://" + hostPortOf(t, ts.URL) + "/team/docs/x.md?" + cap + "#L1"

	body, err := ReadCapability(mustParse(t, uri), ReadOptions{HTTPClient: hc})
	if err != nil {
		t.Fatal(err)
	}
	bytes, _ := io.ReadAll(body)
	body.Close()
	eq(t, bytes, []byte{1, 2, 3}, "bytes")
	if len(got) != 1 {
		t.Fatalf("want exactly one request, got %d", len(got))
	}
	eq(t, got[0].url, "/v1/cap/blob/"+hh+"?e=1782000300&k=k2&s="+sig+"&sp=team", "request path")
	if got[0].headers.Get("Authorization") != "" || got[0].headers.Get("X-ACP-Space") != "" || got[0].headers.Get("X-ACP-Agent") != "" {
		t.Fatalf("tokenless: no auth/space/agent headers, got %v", got[0].headers)
	}
	if strings.Contains(got[0].url, "#") || strings.Contains(got[0].url, "L1") {
		t.Fatal("fragment never sent")
	}
	// Daemon refusals surface as APIError with the daemon's status (expired / bad signature / missing).
	for _, st := range []int{410, 403, 404} {
		status = st
		_, err := ReadCapability(mustParse(t, uri), ReadOptions{HTTPClient: hc})
		var ae *client.APIError
		if !errors.As(err, &ae) || ae.Status != st || !strings.Contains(ae.Message, "stub") {
			t.Fatalf("status %d: want APIError with the daemon's status and message, got %v", st, err)
		}
	}
	// Not a signed link -> refused before any request.
	n := len(got)
	_, err = ReadCapability(mustParse(t, "acp://"+hostPortOf(t, ts.URL)+"/team/docs/x.md"), ReadOptions{HTTPClient: hc})
	if CodeOf(err) != CodeCapabilityMalformed {
		t.Fatalf("want capability_malformed, got %v", err)
	}
	if len(got) != n {
		t.Fatal("a non-link must not produce a request")
	}
}

// hostPortOf turns an httptest URL ("https://127.0.0.1:PORT") into "127.0.0.1:PORT".
func hostPortOf(t *testing.T, tsURL string) string {
	t.Helper()
	u, err := url.Parse(tsURL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

// TS: "D1: acpLinkFromBlobURL turns a minted capability URL into a canonical signed acp:// link"
func TestD1LinkFromBlobURL(t *testing.T) {
	minted := "https://coordd.example:8443/v1/cap/blob/" + hh + "?e=1782000300&k=k2&s=" + sig + "&sp=team"
	link, err := LinkFromBlobURL(minted, "docs/spec.md")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, link, "acp://coordd.example/team/docs/spec.md?e=1782000300&h="+hh+"&k=k2&s="+sig+"&sp=team", "link")
	eq(t, resolve(t, link).Reads[0].Path, "/v1/cap/blob/"+hh+"?e=1782000300&k=k2&s="+sig+"&sp=team", "resolved path")
	// A non-default port is preserved; the path is validated like any other.
	l2, err := LinkFromBlobURL(strings.Replace(minted, ":8443", ":9443", 1), "a/b.txt")
	if err != nil || !strings.HasPrefix(l2, "acp://coordd.example:9443/team/a/b.txt?") {
		t.Fatalf("non-default port: %q %v", l2, err)
	}
	mustRefuseLink := func(blobURL, path string, why string) {
		t.Helper()
		_, err := LinkFromBlobURL(blobURL, path)
		var e *Error
		if !errors.As(err, &e) {
			t.Fatalf("%s: want *acpuri.Error, got %v", why, err)
		}
	}
	mustRefuseLink(minted, "../x", "traversal")
	mustRefuseLink("http://coordd.example:8443/v1/cap/blob/"+hh+"?e=1&k=k2&s="+sig+"&sp=team", "x", "cleartext")
	mustRefuseLink("https://coordd.example:8443/v1/blobs/"+hh, "x", "not the capability path")
	mustRefuseLink(minted+"&sp=team", "x", "repeated param")
	mustRefuseLink(minted+"&token=abc", "x", "a raw token never survives")
	if _, err := LinkFromBlobURL(minted+"&token=abc", "x"); CodeOf(err) != CodeRawToken {
		t.Fatalf("token on a minted URL must be raw_token, got %v", err)
	}
	mustRefuseLink("://not a url", "x", "not a URL")
	mustRefuseLink("https://coordd.example:8443/v1/cap/blob/"+hh+"?e=1&k=k2&s="+sig, "x", "missing sp")
	mustRefuseLink("https://coordd.example:8443/v1/cap/blob/"+strings.ToUpper(hh)+"?e=1&k=k2&s="+sig+"&sp=team", "x", "uppercase hash path")
}

// TS: "fragment: client-only, excluded from identity, never in a request"
func TestFragment(t *testing.T) {
	u := mustParse(t, "acp://h.example:8443/team/docs/x.md#L12")
	if u.Fragment == nil || *u.Fragment != "L12" {
		t.Fatalf("fragment: %v", u.Fragment)
	}
	eq(t, Canonical(u), "acp://h.example/team/docs/x.md", "canonical excludes fragment")
	if !same(t, "acp://h.example:8443/team/docs/x.md#L12", "acp://h.example:8443/team/docs/x.md#L99") {
		t.Fatal("fragments must not affect identity")
	}
	for _, rd := range Resolve(u).Reads {
		if strings.Contains(rd.Path, "L12") {
			t.Fatal("fragment leaked into a request path")
		}
	}
	// An empty fragment ("#") is present-but-empty; absent is nil.
	e := mustParse(t, "acp://h.example/team/x#")
	if e.Fragment == nil || *e.Fragment != "" {
		t.Fatal("'#' must yield an empty, non-nil fragment")
	}
	if mustParse(t, "acp://h.example/team/x").Fragment != nil {
		t.Fatal("no '#' must yield a nil fragment")
	}
	// A fragment may contain '?' and '/'; it is split off first.
	f := mustParse(t, "acp://h.example/team/x#a?b/c")
	eq(t, []any{*f.Fragment, f.Path, f.IgnoredQuery}, []any{"a?b/c", "x", []string{}}, "fragment with ? and /")
	refuses(t, "acp://h.example/team/x#%00", CodeControlChar)
}

// TS: "canonicalization: two spellings -> one string (percent-encoding, case, @fs, dot-segments, port, query order)"
func TestCanonicalizationPairs(t *testing.T) {
	pairs := [][2]string{
		{"acp://H.EXAMPLE:8443/team/docs/sp%65c.md", "acp://h.example/team/docs/spec.md"},
		{"acp://h.example:8443/team/@fs/docs/./spec.md", "acp://h.example/team/docs/spec.md"},
		{"acp://h.example/team/docs/a/../spec.md", "acp://h.example/team/docs/spec.md"},
		{"acp://h.example:9443/team/docs/my%20file.md", "acp://h.example:9443/team/docs/my%20file.md"},
		{"acp://h.example:8443/team/docs/%c3%a9.md", "acp://h.example/team/docs/%C3%A9.md"}, // hex uppercased
		{"acp://h.example:8443/team/docs/?foo=1&v=3", "acp://h.example/team/docs/?v=3"},
		{"acp://h.example:8443/team/x?s=" + sig + "&sp=team&k=k2&e=5&h=" + hh, "acp://h.example/team/x?e=5&h=" + hh + "&k=k2&s=" + sig + "&sp=team"},
		{"acp://h.example/team/docs/a%21b%24c", "acp://h.example/team/docs/a!b$c"},     // pchar sub-delims stay literal
		{"acp://h.example/team/docs/a%3Fb%23c", "acp://h.example/team/docs/a%3Fb%23c"}, // must-encode octets stay encoded
		{"acp://h.example/team/docs/é.md", "acp://h.example/team/docs/%C3%A9.md"},      // raw UTF-8 is encoded
		{"acp://h.example/team/docs/a%2Bb", "acp://h.example/team/docs/a+b"},
	}
	for _, p := range pairs {
		eq(t, canon(t, p[0]), p[1], p[0])
	}
	// Trailing slash is SIGNIFICANT: a file and a collection are different resources.
	if same(t, "acp://h.example:8443/team/docs", "acp://h.example:8443/team/docs/") {
		t.Fatal("file and collection must differ")
	}
}

// TS: "resolve: the write surface is named (CAS: PUT blob -> POST commit), never a new path"
func TestResolveWriteSurface(t *testing.T) {
	file := resolve(t, "acp://h.example:8443/team/docs/spec.md")
	eq(t, file.Mode, "cas", "mode")
	eq(t, file.Auth, AuthProfile, "auth")
	var ws [][3]string
	for _, w := range file.Writes {
		ws = append(ws, [3]string{w.Method, w.Path, w.Locate})
	}
	eq(t, ws, [][3]string{{"POST", "/v1/blobs", ""}, {"POST", "/v1/commit", "docs/spec.md"}}, "writes")
	for _, w := range file.Writes {
		if strings.Contains(w.Path, "/team") {
			t.Fatal("space must not be in the write URL path")
		}
	}
	// Collections and the root have no write on the CAS wire (directories are implicit).
	eq(t, resolve(t, "acp://h.example:8443/team/docs/").Writes, []WireWrite{}, "collection writes")
	eq(t, resolve(t, "acp://h.example:8443/team/").Writes, []WireWrite{}, "root writes")
}

// TS: "§3.10: the space binds from the first segment; a query/fragment cannot override it"
func TestSpaceBinds(t *testing.T) {
	r := resolve(t, "acp://h.example:8443/team/docs/x.md?space=other&X-ACP-Space=other#space=other")
	eq(t, r.Space, "team", "space")
	eq(t, r.Headers, map[string]string{"X-ACP-Space": "team"}, "headers")
	ig := append([]string{}, r.URI.IgnoredQuery...)
	sort.Strings(ig)
	eq(t, ig, []string{"X-ACP-Space", "space"}, "ignored")
	eq(t, r.Canonical, "acp://h.example/team/docs/x.md", "canonical")
}

// TS: "§3.9: parse/canonicalize/resolve are pure — no I/O of any kind"
//
// Go has no global fetch to replace; instead every step runs against an
// unresolvable host and must succeed synchronously — any network attempt
// would surface as an error (and ClientFor's client.New is documented to do
// no I/O when no cert file is given).
func TestPureNoIO(t *testing.T) {
	u := mustParse(t, "acp://h.example:8443/team/docs/x.md?v=3#frag")
	Canonical(u)
	Resolve(u)
	resolve(t, "acp://h.example/team/docs/x.md?"+cap)
	if _, err := LinkFromBlobURL("https://h.example:8443/v1/cap/blob/"+hh+"?e=1&k=k2&s="+sig+"&sp=team", "docs/x.md"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ClientFor(u, NewStaticProfileStore([]Profile{{Name: "p", Server: "https://h.example:8443", Token: "t", Agent: "a", Insecure: true}})); err != nil {
		t.Fatal(err)
	}
}

// TS: "resolve: deterministic wire mapping; the space rides in X-ACP-Space, never the URL path"
func TestResolveDeterministic(t *testing.T) {
	file := resolve(t, "acp://h.example:8443/team/docs/spec.md?v=7")
	eq(t, file.BaseURL, "https://h.example:8443", "base")
	eq(t, file.Headers, map[string]string{"X-ACP-Space": "team"}, "headers")
	eq(t, file.PinnedVersion, i64(7), "pin")
	eq(t, file.Reads, []WireRead{
		{Method: "GET", Path: "/v1/manifest", Locate: "docs/spec.md"},
		{Method: "GET", Path: "/v1/blobs/<hash>", Locate: "docs/spec.md"},
	}, "file reads")
	coll := resolve(t, "acp://h.example:8443/team/docs/")
	eq(t, coll.Reads, []WireRead{{Method: "GET", Path: "/v1/manifest", Prefix: "docs/"}}, "collection reads")
	root := resolve(t, "acp://h.example:8443/team/")
	eq(t, root.Reads, []WireRead{{Method: "GET", Path: "/v1/manifest"}}, "root reads")
	for _, r := range []*Resolved{file, coll, root} {
		if !strings.HasPrefix(r.BaseURL, "https://") {
			t.Fatal("acp:// implies TLS")
		}
		for _, rd := range r.Reads {
			if strings.Contains(rd.Path, "/team") {
				t.Fatalf("space leaked into the URL path: %s", rd.Path)
			}
			if !strings.HasPrefix(rd.Path, "/v1/") {
				t.Fatalf("not a /v1/ path: %s", rd.Path)
			}
		}
	}
	// Same canonical URI -> identical resolution (determinism).
	eq(t, resolve(t, "acp://H.example/team/@fs/docs/./spec.md?v=7"), file, "determinism")
}

// TS: "profile auth: the credential comes from the profile store, never from the URI"
func TestProfileAuthFromStore(t *testing.T) {
	store := NewStaticProfileStore([]Profile{
		{Name: "prod", Server: "https://h.example:8443", Token: "tok-prod", Agent: "me", Space: "other", Insecure: true},
		{Name: "team", Server: "https://h.example:8443", Token: "tok-team", Agent: "me", Space: "team", Insecure: true},
		{Name: "lab", Server: "https://lab.example:9443", Token: "tok-lab", Agent: "me", Insecure: true},
		{Name: "web", Server: "https://web.example", Token: "tok-web", Agent: "me", Insecure: true}, // https default :443
	})
	clientFor := func(s string) (*client.Client, *Resolved, *Profile) {
		t.Helper()
		c, r, p, err := ClientFor(mustParse(t, s), store)
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		return c, r, p
	}
	// Space preference picks the profile whose space matches the URI's.
	c, r, p := clientFor("acp://h.example:8443/team/docs/x.md")
	eq(t, p.Name, "team", "profile")
	eq(t, c.Space(), "team", "client space")
	eq(t, r.BaseURL, "https://h.example:8443", "base")
	eq(t, r.Canonical, "acp://h.example/team/docs/x.md", "canonical")
	// A port-less URI is :8443 and matches the :8443 profile (D3).
	_, _, p2 := clientFor("acp://h.example/team/docs/x.md")
	eq(t, p2.Name, "team", "port-less")
	// No space match: first host:port match wins.
	_, _, p3 := clientFor("acp://h.example:8443/zzz/x")
	eq(t, p3.Name, "prod", "first match")
	// The port is part of the identity: a non-default profile port needs the explicit port in the URI.
	_, r4, _ := clientFor("acp://lab.example:9443/s/x")
	eq(t, r4.BaseURL, "https://lab.example:9443", "lab base")
	for _, bad := range []string{"acp://lab.example/s/x", "acp://lab.example:1/s/x", "acp://web.example/s/x"} {
		_, _, _, err := ClientFor(mustParse(t, bad), store)
		if CodeOf(err) != CodeNoProfile {
			t.Fatalf("%s: want no_profile, got %v", bad, err)
		}
	}
	_, _, p5 := clientFor("acp://web.example:443/s/x")
	eq(t, p5.Name, "web", ":443 explicit")
	// The URI the resolver produces never contains the token, in any component.
	for _, s := range append([]string{r.Canonical, r.BaseURL}, readPaths(r)...) {
		if strings.Contains(s, "tok-team") {
			t.Fatalf("token leaked into %s", s)
		}
	}
	// A profile lacking a token or agent is refused at use (never a silently unauthenticated client).
	for _, bad := range []Profile{{Name: "nt", Server: "https://x.example:8443", Agent: "a"}, {Name: "na", Server: "https://x.example:8443", Token: "t"}} {
		_, _, _, err := ClientFor(mustParse(t, "acp://x.example/s/x"), NewStaticProfileStore([]Profile{bad}))
		if CodeOf(err) != CodeNoProfile || !strings.Contains(err.Error(), "lacks a token or agent") {
			t.Fatalf("%s: want no_profile/lacks, got %v", bad.Name, err)
		}
	}
	// A malformed profile server never matches (it cannot be resolved to https).
	for _, srv := range []string{"http://h.example:8443", "h.example:8443", "", "https://h.example:abc"} {
		if p := NewStaticProfileStore([]Profile{{Name: "m", Server: srv, Token: "t", Agent: "a"}}).Find("h.example", 8443, "s"); p != nil {
			t.Fatalf("server %q must never match", srv)
		}
	}
	// ProfileServerHostPort: https only, default 443, IPv6 bracketed, host lowercased.
	for _, c := range []struct {
		in   string
		host string
		port int
	}{{"https://H.Example", "h.example", 443}, {"https://h.example:8443", "h.example", 8443}, {"https://[::1]:9443", "[::1]", 9443}, {"https://h.example:443", "h.example", 443}} {
		h, p, err := ProfileServerHostPort(c.in)
		if err != nil || h != c.host || p != c.port {
			t.Fatalf("%s: got %q %d %v", c.in, h, p, err)
		}
	}
	if _, _, err := ProfileServerHostPort("http://h.example"); CodeOf(err) != CodeNoProfile {
		t.Fatalf("http profile server must be no_profile, got %v", err)
	}
}

func readPaths(r *Resolved) []string {
	out := []string{}
	for _, rd := range r.Reads {
		out = append(out, rd.Path)
	}
	return out
}

// TestProfileTieBreakHonoursDefault pins the CL-1-adjacent credential-selection
// fix: when several profiles share a host:port and NONE matches the URI's space,
// the SELECTED (Default) profile wins the tie — not whichever key sorted first.
// The pre-fix Find returned the first match, so an alphabetically-earlier
// non-default profile silently supplied the credential.
func TestProfileTieBreakHonoursDefault(t *testing.T) {
	// "aaa" sorts before "zzz"; "zzz" is the operator's selected default.
	store := NewStaticProfileStore([]Profile{
		{Name: "aaa", Server: "https://h.example:8443", Token: "tok-aaa", Agent: "me", Space: "one", Insecure: true},
		{Name: "zzz", Server: "https://h.example:8443", Token: "tok-zzz", Agent: "me", Space: "two", Insecure: true, Default: true},
	})
	// URI space "other" matches neither → the default (zzz) must win the tie.
	_, p := findVia(t, store, "acp://h.example:8443/other/x")
	eq(t, p.Name, "zzz", "default wins the no-space-match tie")
	// An EXACT space match still beats the default (the strongest signal).
	_, p2 := findVia(t, store, "acp://h.example:8443/one/x")
	eq(t, p2.Name, "aaa", "exact space match beats the default")
	// With no default set at all, the fallback stays deterministic (first match).
	plain := NewStaticProfileStore([]Profile{
		{Name: "aaa", Server: "https://h.example:8443", Token: "t", Agent: "me", Space: "one", Insecure: true},
		{Name: "bbb", Server: "https://h.example:8443", Token: "t", Agent: "me", Space: "two", Insecure: true},
	})
	_, p3 := findVia(t, plain, "acp://h.example:8443/other/x")
	eq(t, p3.Name, "aaa", "no default → first match")
}

func findVia(t *testing.T, store ProfileStore, s string) (*Resolved, *Profile) {
	t.Helper()
	r, p, err := Lookup(mustParse(t, s), store)
	if err != nil {
		t.Fatalf("%s: %v", s, err)
	}
	return r, p
}

// TS: "profile auth: the token rides ONLY in the Authorization header on the wire"
func TestProfileAuthTokenOnlyInHeader(t *testing.T) {
	type seen struct {
		url     string
		headers http.Header
	}
	var got []seen
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/manifest", func(w http.ResponseWriter, r *http.Request) {
		got = append(got, seen{url: r.URL.String(), headers: r.Header.Clone()})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"version": 3, "entries": map[string]any{}})
	})
	ts := httptest.NewTLSServer(mux)
	t.Cleanup(ts.Close)
	hp := hostPortOf(t, ts.URL)
	// Pinned-cert path: the profile's cert is the scratch server's own certificate.
	certFile := writeCertPEM(t, ts.Certificate())
	store := NewStaticProfileStore([]Profile{{Name: "t", Server: ts.URL, Token: "tok-secret", Agent: "me", Cert: certFile}})
	c, r, _, err := ClientFor(mustParse(t, "acp://"+hp+"/team/docs/"), store)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, r.BaseURL, ts.URL, "base is the scratch server")
	if _, err := c.Manifest(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want one request, got %d", len(got))
	}
	eq(t, got[0].url, "/v1/manifest", "url")
	if strings.Contains(got[0].url, "tok-secret") || strings.Contains(got[0].url, "team") {
		t.Fatalf("token/space leaked into the URL: %s", got[0].url)
	}
	eq(t, got[0].headers.Get("Authorization"), "Bearer tok-secret", "authorization header")
	eq(t, got[0].headers.Get("X-ACP-Space"), "team", "space header")
}

// writeCertPEM writes a certificate to a temp file (removed by t.TempDir cleanup).
func writeCertPEM(t *testing.T, cert *x509.Certificate) string {
	t.Helper()
	return writePEM(t, cert.Raw)
}

// TS: "formatACPURI round-trips through the parser and refuses malformed parts"
func TestFormatRoundTrip(t *testing.T) {
	mustFormat := func(p Parts) string {
		t.Helper()
		s, err := Format(p)
		if err != nil {
			t.Fatalf("%+v: %v", p, err)
		}
		return s
	}
	eq(t, mustFormat(Parts{Host: "H.example", Port: 8443, Space: "team", Path: "/docs/x y.md"}), "acp://h.example/team/docs/x%20y.md", "space in name")
	eq(t, mustFormat(Parts{Host: "h.example", Port: 9443, Space: "team", Path: "docs/x.md"}), "acp://h.example:9443/team/docs/x.md", "port kept")
	eq(t, mustFormat(Parts{Host: "h.example", Space: "team", Path: "docs", Kind: KindCollection, Version: i64(4)}), "acp://h.example/team/docs/?v=4", "collection + pin")
	eq(t, mustFormat(Parts{Host: "h.example", Space: "team"}), "acp://h.example/team/", "root")
	for _, bad := range []Parts{{Host: "h.example", Space: "te am"}, {Host: "h.example", Space: "team", Path: "../x"}, {Host: "h.example", Space: "team", Path: "a//b"}, {Host: "h.example", Space: "team", Path: "a\\b"}, {Host: "bad host", Space: "team"}} {
		if _, err := Format(bad); CodeOf(err) == "" {
			t.Fatalf("%+v: want refusal, got %v", bad, err)
		}
	}
}

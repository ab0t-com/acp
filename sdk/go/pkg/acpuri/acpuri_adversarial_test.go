// acp:// resolver — ADVERSARIAL / hardening vectors beyond the conformance
// table in acpuri_test.go: host/port edges, canonical idempotence, long inputs,
// a random-input fuzz (the parser may only ever return *Error — never panic,
// never a non-refusal on garbage), and the signed-link smuggling cases.
// PURE: no network, no files, no env.
//
// PARITY: a port of the TS SDK's acp/sdk/ts/test/uri-adversarial.test.mjs
// (each Test* names the TS test it mirrors) plus a native Go fuzz target.
package acpuri

import (
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

var (
	sigA = strings.Repeat("c", 64)
	hA   = strings.Repeat("d", 64)
	capA = "h=" + hA + "&sp=team&e=1782000300&k=k2&s=" + sigA
)

// writePEM writes DER bytes as a CERTIFICATE PEM file under t.TempDir().
func writePEM(t *testing.T, der []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cert.pem")
	if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// ---- 1. host / port edges ----

// TS: "host: case folds to lowercase; IDN/unicode, whitespace, and a doubled ':port' are refused"
func TestHostEdges(t *testing.T) {
	eq(t, mustParse(t, "acp://COORDD.Example.COM/team/x").Host, "coordd.example.com", "fold")
	eq(t, canon(t, "acp://COORDD.Example.COM:8443/team/x"), "acp://coordd.example.com/team/x", "canonical fold")
	refuses(t, "acp://bücher.example/team/x", CodeHost) // DECIDED: ASCII hosts only (no IDN mapping in v1)
	refuses(t, "acp://h.example%20/team/x", CodeHost)
	refuses(t, "acp://h example/team/x", CodeHost)
	refuses(t, "acp://h.example:8443:1/team/x", CodeHost)
	refuses(t, "acp://-h.example/team/x", CodeHost)
	refuses(t, "acp://h_x.example/team/x", CodeHost) // underscores are not hostname octets
	eq(t, mustParse(t, "acp://h.example./team/x").Host, "h.example.", "FQDN trailing dot is a distinct, legal host")
	// Go-only (documented residual): the fold is ASCII-only, so a non-ASCII letter whose
	// Unicode lowercase happens to be ASCII (U+212A KELVIN SIGN -> "k" in JS) is refused here.
	refuses(t, "acp://K.example/team/x", CodeHost)
	refuses(t, "acp://h.example\x00/team/x", CodeHost)
}

// TS: "IPv6: bracketed literals are ACCEPTED (lowercased); unbracketed/unterminated/garbage forms are refused"
func TestIPv6(t *testing.T) {
	// DECIDED: accept [v6] per RFC3986; it is byte-canonicalized by lowercasing only (no ::-compression).
	a := mustParse(t, "acp://[FE80::1]:9443/team/x")
	eq(t, []any{a.Host, a.Port}, []any{"[fe80::1]", 9443}, "v6")
	eq(t, canon(t, "acp://[FE80::1]:8443/team/x"), "acp://[fe80::1]/team/x", "v6 canonical")
	eq(t, mustParse(t, "acp://[::ffff:127.0.0.1]/team/x").Host, "[::ffff:127.0.0.1]", "v4-mapped")
	refuses(t, "acp://[::1/team/x", CodeHost)        // unterminated
	refuses(t, "acp://[::1]x:8443/team/x", CodeHost) // garbage after the literal
	refuses(t, "acp://[::1]:/team/x", CodePort)      // empty port after a literal
	refuses(t, "acp://[]/team/x", CodeHost)
	refuses(t, "acp://[zz::1]/team/x", CodeHost)
	refuses(t, "acp://::1/team/x", CodeHost) // unbracketed v6 is ambiguous with host:port — refused
}

// TS: "port: 1 and 65535 accepted; 0/65536/non-numeric/empty refused; leading zeros canonicalize"
func TestPortEdges(t *testing.T) {
	eq(t, mustParse(t, "acp://h.example:1/team/x").Port, 1, "1")
	eq(t, mustParse(t, "acp://h.example:65535/team/x").Port, 65535, "65535")
	refuses(t, "acp://h.example:0/team/x", CodePort)
	refuses(t, "acp://h.example:65536/team/x", CodePort)
	refuses(t, "acp://h.example:8443a/team/x", CodePort)
	refuses(t, "acp://h.example:/team/x", CodePort)
	refuses(t, "acp://h.example:-1/team/x", CodePort)
	refuses(t, "acp://h.example:8443 /team/x", CodePort)
	refuses(t, "acp://h.example:844300/team/x", CodePort) // 6 digits
	// DECIDED: leading zeros are legal RFC3986 DIGITs and canonicalize to the numeric port.
	eq(t, mustParse(t, "acp://h.example:08443/team/x").Port, DefaultPort, "08443")
	eq(t, canon(t, "acp://h.example:08443/team/x"), "acp://h.example/team/x", "08443 canonical")
	eq(t, canon(t, "acp://h.example:09443/team/x"), "acp://h.example:9443/team/x", "09443 canonical")
	if !same(t, "acp://h.example:08443/team/x", "acp://h.example/team/x") {
		t.Fatal("08443 must equal port-less")
	}
}

// ---- 1. canonical idempotence + equivalences ----

var validVectors = []string{
	"acp://h.example/team/",
	"acp://h.example:8443/team",
	"acp://H.EXAMPLE:08443/team/@fs/",
	"acp://h.example/team/docs/",
	"acp://h.example/team/@fs/docs/",
	"acp://h.example/team/docs/./x/../spec.md",
	"acp://h.example:9443/team/docs/my%20file.md?v=3#L1",
	"acp://h.example/team/docs/%c3%a9%2e%6dd",
	"acp://[FE80::1]:9443/team/a/b/c/d/e/",
	"acp://h.example/team/docs/x.md?" + capA,
	"acp://h.example:8443/team/docs/x.md?s=" + sigA + "&e=1782000300&h=" + hA + "&k=k2&sp=team&zzz=1#frag",
	"acp://h.example/team/x?foo=bar&baz=qux&v=0",
	"acp://h.example/team/%40doc/%40x/a%40b",
	"acp://h.example/team/@fs/%40fs/",
}

func strip(u *URI) URI {
	c := *u
	c.Fragment = nil
	c.IgnoredQuery = []string{}
	return c
}

// TS: "canonical form is idempotent and round-trips: canonical(canonical(x)) == canonical(x); parse(canonical(x)) == parse(x) modulo fragment/ignored keys"
func TestCanonicalIdempotent(t *testing.T) {
	for _, x := range validVectors {
		c1 := canon(t, x)
		c2 := canon(t, c1)
		eq(t, c2, c1, x)
		p1 := mustParse(t, x)
		p2 := mustParse(t, c1)
		eq(t, strip(p2), strip(p1), x)
		// Resolution is a function of identity: same canonical -> same requests.
		r1 := Resolve(p1)
		r2 := Resolve(p2)
		s1, s2 := strip(r1.URI), strip(r2.URI)
		r1.URI, r2.URI = &s1, &s2
		eq(t, r1, r2, x)
	}
}

// TS: "equivalences: @fs/ == bare for files, collections and the root; :8443 == port-less; trailing '.' == collection"
func TestEquivalences(t *testing.T) {
	eqv := [][2]string{
		{"acp://h.example/team/@fs/docs/", "acp://h.example:8443/team/docs/"},
		{"acp://h.example/team/@fs/docs/x.md", "acp://h.example/team/docs/x.md"},
		{"acp://h.example/team/@fs", "acp://h.example/team/"},
		{"acp://h.example/team/@fs/", "acp://h.example/team"},
		{"acp://h.example/team/docs/.", "acp://h.example/team/docs/"},
		{"acp://h.example/team/docs/x/..", "acp://h.example/team/docs/"},
		{"acp://h.example/team/docs/x/../", "acp://h.example/team/docs/"},
		{"acp://h.example/team/docs/./././x.md", "acp://h.example/team/docs/x.md"},
	}
	for _, p := range eqv {
		if !same(t, p[0], p[1]) {
			t.Fatalf("%s != %s", p[0], p[1])
		}
	}
	neq := [][2]string{
		{"acp://h.example/team/docs", "acp://h.example/team/docs/"},
		{"acp://h.example/team/x", "acp://h.example/Team/x"},
		{"acp://h.example/team/x", "acp://h.example/team/X"},
		{"acp://h.example/team/x", "acp://h.example:9443/team/x"},
		{"acp://h.example/team/x?v=1", "acp://h.example/team/x?v=2"},
		{"acp://h.example/team/x?v=1", "acp://h.example/team/x"},
		{"acp://h.example/team/x?" + capA, "acp://h.example/team/x?" + strings.Replace(capA, "e=1782000300", "e=1782000301", 1)},
	}
	for _, p := range neq {
		if same(t, p[0], p[1]) {
			t.Fatalf("%s == %s", p[0], p[1])
		}
	}
	// SameResource with a non-parsing input is a refusal, never "false by accident".
	if _, err := SameResource("acp://h.example/team/x?token=a", "acp://h.example/team/x"); CodeOf(err) != CodeRawToken {
		t.Fatalf("want raw_token, got %v", err)
	}
	if _, err := SameResource(42, "acp://h.example/team/x"); CodeOf(err) != CodeScheme {
		t.Fatalf("want scheme refusal for a non-URI type, got %v", err)
	}
}

// ---- 1. long / deep / hostile inputs ----

// TS: "very long paths parse and canonicalize; hostile bytes deep inside are still refused"
func TestLongDeepHostile(t *testing.T) {
	seg := strings.Repeat("s", 120)
	parts := make([]string, 200)
	for i := range parts {
		parts[i] = seg + fmt.Sprint(i)
	}
	deep := strings.Join(parts, "/")
	u := mustParse(t, "acp://h.example/team/"+deep+"/leaf.md")
	eq(t, len(strings.Split(u.Path, "/")), 201, "depth")
	c := Canonical(u)
	eq(t, canon(t, c), c, "idempotent")
	eq(t, Resolve(u).Reads[1].Locate, u.Path, "locate")
	// Two ".." 150 segments deep pop two segments (still inside the space); a NUL 190 segments in is still a control byte.
	mid := append(append(append([]string{}, parts[:150]...), "..", ".."), parts[150:]...)
	popped := mustParse(t, "acp://h.example/team/"+strings.Join(mid, "/")+"/x")
	ps := strings.Split(popped.Path, "/")
	eq(t, len(ps), 199, "popped depth")
	eq(t, ps[148], parts[150], "parts[148], parts[149] were popped")
	refuses(t, "acp://h.example/team/"+strings.Join(parts[:190], "/")+"/%00/x", CodeControlChar)
	refuses(t, "acp://h.example/team/"+strings.Join(parts[:190], "/")+"/a%2Fb", CodeEncodedSlash)
	refuses(t, "acp://h.example/team/"+strings.Join(parts[:190], "/")+"/@x", CodeReservedSigil)
	// 200 x ".." from a 100-deep path escapes the root (never wraps, never lands somewhere else).
	dots := strings.Repeat("../", 200)
	refuses(t, "acp://h.example/team/"+strings.Join(parts[:100], "/")+"/"+dots+"x", CodeTraversal)
	// A 10k-character single segment is fine (the daemon bounds names; the resolver bounds nothing it need not).
	eq(t, len(mustParse(t, "acp://h.example/team/"+strings.Repeat("a", 10_000)).Path), 10_000, "10k segment")
}

// mulberry32 is the TS test's deterministic PRNG, ported bit-for-bit so a
// failure is reproducible from the seed.
type mulberry32 struct{ s uint32 }

func (m *mulberry32) next() float64 {
	m.s += 0x6d2b79f5
	t := (m.s ^ (m.s >> 15)) * (1 | m.s)
	t = (t + (t^(t>>7))*(61|t)) ^ t
	return float64(t^(t>>14)) / 4294967296
}

var wireControlRE = regexp.MustCompile(`[\x00-\x1f\x7f]`)

// checkAccepted asserts the invariants every ACCEPTED input must satisfy.
func checkAccepted(t *testing.T, input string, u *URI) {
	t.Helper()
	c := Canonical(u)
	eq(t, canon(t, c), c, "idempotent canonical for "+input)
	r := Resolve(u)
	if !strings.HasPrefix(r.BaseURL, "https://") {
		t.Fatalf("%q: base not https: %s", input, r.BaseURL)
	}
	if strings.ContainsAny(r.BaseURL, "@ \t\r\n") {
		t.Fatalf("%q: credential/whitespace shape in base: %s", input, r.BaseURL)
	}
	for _, rd := range r.Reads {
		if !strings.HasPrefix(rd.Path, "/v1/") {
			t.Fatalf("%q: read path %s", input, rd.Path)
		}
		if strings.Contains(rd.Path, "/"+u.Space+"/") {
			t.Fatalf("%q: space leaked into the path", input)
		}
		if wireControlRE.MatchString(rd.Path) {
			t.Fatalf("%q: control byte reached the wire path", input)
		}
	}
}

// TS: "fuzz: 2000 random/mutated inputs — the parser either accepts or throws ACPURIError; never anything else"
func TestFuzzMutations(t *testing.T) {
	rnd := &mulberry32{s: 0x5eed_2026}
	alphabet := []rune("abcXYZ019.-_:/@?#%&=[]\\ \t\x00\x7f~!$'()*+,;é\U0001F600")
	seeds := append(append([]string{}, validVectors...), "acp://h.example/team/x", "acp://h.example/team/x?"+capA, "acp://user:pw@h.example/team/x", "acp://h.example:8443")
	accepted, refused := 0, 0
	for i := 0; i < 2000; i++ {
		input := []rune(seeds[int(rnd.next()*float64(len(seeds)))])
		mutations := 1 + int(rnd.next()*6)
		for m := 0; m < mutations; m++ {
			pos := int(rnd.next() * float64(len(input)+1))
			ch := alphabet[int(rnd.next()*float64(len(alphabet)))]
			op := rnd.next()
			switch {
			case op < 0.4:
				input = append(input[:pos:pos], append([]rune{ch}, input[pos:]...)...)
			case op < 0.7:
				if pos < len(input) {
					input = append(input[:pos:pos], input[pos+1:]...)
				}
			case op < 0.85:
				if pos < len(input) {
					input[pos] = ch
				} else {
					input = append(input, ch)
				}
			default:
				if len(input) > 0 {
					input = append(input, input[int(rnd.next()*float64(len(input))):]...)
				}
			}
		}
		s := string(input)
		u, err := parseNoPanic(t, s)
		if err == nil {
			accepted++
			checkAccepted(t, s, u)
			continue
		}
		var e *Error
		if !errors.As(err, &e) {
			t.Fatalf("%q: non-*Error refusal %T: %v", s, err, err)
		}
		refused++
	}
	if accepted <= 50 || refused <= 50 {
		t.Fatalf("fuzz mix too skewed to be meaningful: accepted=%d refused=%d", accepted, refused)
	}
	t.Logf("fuzz: accepted=%d refused=%d", accepted, refused)
}

func parseNoPanic(t *testing.T, s string) (u *URI, err error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%q: PANIC %v", s, r)
		}
	}()
	return Parse(s)
}

// FuzzParse is the native Go fuzz target (go test -fuzz=FuzzParse): Parse must
// never panic, and every accepted input must satisfy the wire invariants.
func FuzzParse(f *testing.F) {
	for _, s := range validVectors {
		f.Add(s)
	}
	f.Add("acp://user:pw@h.example/team/x")
	f.Add("acp://h.example/team/x?token=abc")
	f.Add("acp://h.example/team/x?" + capA)
	f.Add("acp://h.example:8443")
	f.Add("acp://h.example/team/docs/@x")
	f.Add("acp://h.example/team/%40doc/x")
	f.Fuzz(func(t *testing.T, s string) {
		u, err := Parse(s)
		if err != nil {
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("%q: non-*Error refusal %T", s, err)
			}
			return
		}
		checkAccepted(t, s, u)
		if strings.Contains(strings.ToLower(Canonical(u)), "token=") {
			t.Fatalf("%q: a token-shaped key survived into the canonical form", s)
		}
	})
}

// ---- 2. signed file link smuggling ----

// TS: "signed link: a raw credential smuggled beside a VALID capability is refused (query key or userinfo)"
func TestSignedLinkSmuggling(t *testing.T) {
	refuses(t, "acp://h.example/team/docs/x.md?"+capA+"&token=abc", CodeRawToken)
	refuses(t, "acp://h.example/team/docs/x.md?token=abc&"+capA, CodeRawToken)
	refuses(t, "acp://h.example/team/docs/x.md?"+capA+"&Bearer=abc", CodeRawToken)
	refuses(t, "acp://h.example/team/docs/x.md?"+capA+"&X-ACP-Token=abc", CodeRawToken)
	refuses(t, "acp://tok-abc@h.example/team/docs/x.md?"+capA, CodeUserinfo)
	refuses(t, "acp://h.example/team/docs/x.md?"+capA+"&cp=rel", CodeCheckpointUnsupported)
	// A capability cannot smuggle a second space through case or encoding of sp.
	refuses(t, "acp://h.example/team/docs/x.md?"+strings.Replace(capA, "sp=team", "sp=Team", 1), CodeCapabilitySpaceMismatch)
	refuses(t, "acp://h.example/team/docs/x.md?"+strings.Replace(capA, "sp=team", "sp=team%2F..", 1), CodeCapabilitySpaceMismatch)
	refuses(t, "acp://h.example/team/docs/x.md?"+capA+"&sp=team", CodeCapabilityMalformed) // duplicate, even if equal
}

// TS: "signed link: h/s shape edges — wrong length, uppercase, non-hex, and a tampered s still PARSES (the daemon verifies)"
func TestSignedLinkShapeEdges(t *testing.T) {
	refuses(t, "acp://h.example/team/x?h="+hA[:63]+"&sp=team&e=1&k=k2&s="+sigA, CodeCapabilityMalformed)
	refuses(t, "acp://h.example/team/x?h="+hA+"0&sp=team&e=1&k=k2&s="+sigA, CodeCapabilityMalformed)
	refuses(t, "acp://h.example/team/x?h="+strings.ToUpper(hA)+"&sp=team&e=1&k=k2&s="+sigA, CodeCapabilityMalformed)
	refuses(t, "acp://h.example/team/x?h="+strings.Repeat("g", 64)+"&sp=team&e=1&k=k2&s="+sigA, CodeCapabilityMalformed)
	refuses(t, "acp://h.example/team/x?h="+hA+"&sp=team&e=1&k=k2&s="+sigA[:63], CodeCapabilityMalformed)
	refuses(t, "acp://h.example/team/x?h="+hA+"&sp=team&e=1&k=k2&s="+strings.ToUpper(sigA), CodeCapabilityMalformed)
	refuses(t, "acp://h.example/team/x?h="+hA+"&sp=team&e=0&k=k2&s="+sigA, CodeCapabilityMalformed)
	refuses(t, "acp://h.example/team/x?h="+hA+"&sp=team&e=99999999999999999999&k=k2&s="+sigA, CodeCapabilityMalformed)
	refuses(t, "acp://h.example/team/x?h="+hA+"&sp=team&e=9007199254740993&k=k2&s="+sigA, CodeCapabilityMalformed) // > JS safe integer
	refuses(t, "acp://h.example/team/x?h="+hA+"&sp=team&e=1&k=k2&s=", CodeCapabilityMalformed)
	// A well-formed but WRONG signature is not detectable client-side (no key): it parses and resolves,
	// and the daemon refuses it with 403 (pinned live in the gate: "a tampered signature is refused").
	tampered := mustParse(t, "acp://h.example/team/x?h="+hA+"&sp=team&e=1&k=k2&s="+strings.Repeat("0", 64))
	eq(t, tampered.Capability.S, strings.Repeat("0", 64), "tampered s parses")
	eq(t, Resolve(tampered).Reads[0].Tokenless, true, "tokenless")
}

// TS: "signed link: mint -> acp link -> resolve is a BYTE-EXACT round trip back to the minted URL"
func TestSignedLinkRoundTrip(t *testing.T) {
	minted := "https://coordd.example:8443/v1/cap/blob/" + hA + "?e=1782000300&k=k2&s=" + sigA + "&sp=team"
	link, err := LinkFromBlobURL(minted, "docs/spec.md")
	if err != nil {
		t.Fatal(err)
	}
	r := resolve(t, link)
	eq(t, r.BaseURL+r.Reads[0].Path, minted, "byte-exact")
	// Same with a non-default port and a percent-encoded key id.
	minted2 := "https://coordd.example:9443/v1/cap/blob/" + hA + "?e=5&k=k%2F2&s=" + sigA + "&sp=team"
	l2, err := LinkFromBlobURL(minted2, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	r2 := resolve(t, l2)
	eq(t, r2.BaseURL+r2.Reads[0].Path, minted2, "byte-exact with encoded k")
	// The link is canonical already (idempotent), and re-deriving it from its own resolution is stable.
	eq(t, canon(t, link), link, "canonical link")
	l3, err := LinkFromBlobURL(r.BaseURL+r.Reads[0].Path, "docs/spec.md")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, l3, link, "re-derived")
	// The path is validated exactly like any other (the link cannot smuggle a traversal).
	for _, bad := range []string{"../x", "a/../../b", "a\\b", "a//b", "", "/"} {
		if _, err := LinkFromBlobURL(minted, bad); CodeOf(err) == "" {
			t.Fatalf("%q: want refusal, got %v", bad, err)
		}
	}
	// A literal "@doc/x" file is addressable: the builder encodes the leading "@" (§3.3 amendment).
	l4, err := LinkFromBlobURL(minted, "@doc/x")
	if err != nil || !strings.HasPrefix(l4, "acp://coordd.example/team/%40doc/x?") {
		t.Fatalf("@doc/x link: %q %v", l4, err)
	}
	eq(t, resolve(t, l4).URI.Path, "@doc/x", "@doc/x path")
	// An IPv6 minted URL round-trips too.
	minted6 := "https://[fe80::1]:9443/v1/cap/blob/" + hA + "?e=5&k=k2&s=" + sigA + "&sp=team"
	l6, err := LinkFromBlobURL(minted6, "x")
	if err != nil {
		t.Fatal(err)
	}
	r6 := resolve(t, l6)
	eq(t, r6.BaseURL+r6.Reads[0].Path, minted6, "ipv6 byte-exact")
}

// The exported error-code set is exactly the TS ACPURIErrorCode union (33 codes
// since the §3.8 discovery amendment: bare_host + the seven discovery_* codes).
func TestErrorCodeSetParity(t *testing.T) {
	want := []Code{"scheme", "authority", "userinfo", "host", "port", "space_required", "space_invalid", "bare_host", "discovery_unsupported", "discovery_redirect", "discovery_http", "discovery_malformed", "discovery_protocol", "discovery_host_mismatch", "discovery_pin_mismatch", "unknown_type", "reserved_type", "reserved_sigil", "empty_segment", "traversal", "backslash", "encoded_slash", "control_char", "percent_encoding", "query", "version_invalid", "checkpoint_unsupported", "pin_conflict", "raw_token", "capability_malformed", "capability_space_mismatch", "capability_unsupported_resource", "no_profile"}
	got := []Code{CodeScheme, CodeAuthority, CodeUserinfo, CodeHost, CodePort, CodeSpaceRequired, CodeSpaceInvalid, CodeBareHost, CodeDiscoveryUnsupported, CodeDiscoveryRedirect, CodeDiscoveryHTTP, CodeDiscoveryMalformed, CodeDiscoveryProtocol, CodeDiscoveryHostMismatch, CodeDiscoveryPinMismatch, CodeUnknownType, CodeReservedType, CodeReservedSigil, CodeEmptySegment, CodeTraversal, CodeBackslash, CodeEncodedSlash, CodeControlChar, CodePercentEncoding, CodeQuery, CodeVersionInvalid, CodeCheckpointUnsupported, CodePinConflict, CodeRawToken, CodeCapabilityMalformed, CodeCapabilitySpaceMismatch, CodeCapabilityUnsupportedResource, CodeNoProfile}
	if len(want) != 33 {
		t.Fatalf("code set size: %d", len(want))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("code set drifted from the TS union:\n got  %v\n want %v", got, want)
	}
	eq(t, ReservedTypes, []string{"fs", "doc", "log", "mail", "chan"}, "reserved types")
}

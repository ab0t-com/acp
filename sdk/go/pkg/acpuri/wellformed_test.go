// D26 (a) / U-URISURR — an acp:// address names TEXT: a segment that is not
// well-formed (invalid UTF-8 in Go; a lone UTF-16 surrogate in the TS twin) is
// REFUSED with percent_encoding, never encoded as U+FFFD (which names a
// different file). The string entry points (Parse, Format, LinkFromBlobURL, the
// discovery address) are pinned by the shared "wellformed/*" vectors in
// acp/testdata/acpuri-vectors.json; this file pins the *URI entry points a
// caller can hand a URI it built itself (clarification ext32-3.7-wellformed-text).
// Pure: no network (ReadCapability gets a transport that records and refuses).
package acpuri

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

// noNetwork records any request and fails it: a refused URI must send nothing.
type noNetwork struct{ seen int }

func (n *noNetwork) RoundTrip(*http.Request) (*http.Response, error) {
	n.seen++
	return nil, errors.New("no network in this test")
}

func wellFormedProfiles() ProfileStore {
	return NewStaticProfileStore([]Profile{{Name: "team", Server: "https://h.example:8443", Token: "t1", Agent: "a1", Space: "team"}})
}

// The ill-formed spellings a Go string can hold: a lone surrogate's
// generalized-UTF-8 bytes (high, low), a stray continuation byte, an overlong
// "/", a truncated sequence, and a 0xFF byte.
var illFormedText = map[string]string{
	"lone high surrogate (ED A0 80)": "a\xED\xA0\x80b",
	"lone low surrogate (ED B0 80)":  "\xED\xB0\x80",
	"stray continuation byte":        "docs/\x80.md",
	"overlong slash (C0 AF)":         "a\xC0\xAFb",
	"truncated sequence":             "x\xC3",
	"0xFF byte":                      "\xFF",
}

func TestWellFormedText_HandBuiltURIRefused(t *testing.T) {
	sig := strings.Repeat("c", 64)
	for name, bad := range illFormedText {
		cases := []struct {
			field string
			u     *URI
		}{
			{"path", &URI{Host: "h.example", Port: DefaultPort, Space: "team", Type: "fs", Path: bad, Kind: KindFile}},
			{"space", &URI{Host: "h.example", Port: DefaultPort, Space: "te" + bad, Type: "fs", Path: "x", Kind: KindFile}},
			{"capability k", &URI{Host: "h.example", Port: DefaultPort, Space: "team", Type: "fs", Path: "x", Kind: KindFile,
				Capability: &Capability{H: strings.Repeat("d", 64), SP: "team", E: 1782000300, K: "k" + bad, S: sig}}},
			{"capability sp", &URI{Host: "h.example", Port: DefaultPort, Space: "team", Type: "fs", Path: "x", Kind: KindFile,
				Capability: &Capability{H: strings.Repeat("d", 64), SP: "team" + bad, E: 1782000300, K: "k2", S: sig}}},
		}
		for _, c := range cases {
			label := name + " in " + c.field
			if _, _, err := Lookup(c.u, wellFormedProfiles()); CodeOf(err) != CodePercentEncoding {
				t.Errorf("%s: Lookup: got %v, want %s", label, err, CodePercentEncoding)
			}
			if cl, _, _, err := ClientFor(c.u, wellFormedProfiles()); cl != nil || CodeOf(err) != CodePercentEncoding {
				t.Errorf("%s: ClientFor: got client=%v err=%v, want %s and no client", label, cl != nil, err, CodePercentEncoding)
			}
			if same, err := SameResource(c.u, c.u); same || CodeOf(err) != CodePercentEncoding {
				t.Errorf("%s: SameResource(u, u): got %v, %v — want a %s refusal, never a match", label, same, err, CodePercentEncoding)
			}
			nn := &noNetwork{}
			if body, err := ReadCapability(c.u, ReadOptions{HTTPClient: &http.Client{Transport: nn}}); body != nil || CodeOf(err) != CodePercentEncoding {
				t.Errorf("%s: ReadCapability: got %v, want %s", label, err, CodePercentEncoding)
			}
			if nn.seen != 0 {
				t.Errorf("%s: ReadCapability sent %d request(s) for a refused URI", label, nn.seen)
			}
			// Canonical has no error result: it renders the bytes losslessly as %XX
			// escapes (never U+FFFD), and that output is itself refused by Parse.
			canon := Canonical(c.u)
			if strings.Contains(canon, "%EF%BF%BD") || strings.Contains(canon, "�") {
				t.Errorf("%s: Canonical rendered U+FFFD: %q", label, canon)
			}
			// ext32-3.7-wellformed-text: what cannot report a refusal emits a string every resolver refuses.
			if _, err := Parse(canon); CodeOf(err) != CodePercentEncoding {
				t.Errorf("%s: Parse(Canonical(u)) = %v, want %s", label, err, CodePercentEncoding)
			}
		}
	}
}

// Well-formed text at the edges stays addressable through every *URI entry point.
func TestWellFormedText_EdgeTextAccepted(t *testing.T) {
	for _, tc := range []struct{ path, canon string }{
		{"max-\U0010FFFF.txt", "acp://h.example/team/max-%F4%8F%BF%BF.txt"},
		{"r-�.txt", "acp://h.example/team/r-%EF%BF%BD.txt"},
		{"a/\U0001F600b", "acp://h.example/team/a/%F0%9F%98%80b"},
		{"文档/é.md", "acp://h.example/team/%E6%96%87%E6%A1%A3/%C3%A9.md"},
	} {
		u := &URI{Host: "h.example", Port: DefaultPort, Space: "team", Type: "fs", Path: tc.path, Kind: KindFile}
		if got := Canonical(u); got != tc.canon {
			t.Errorf("%q: Canonical = %q, want %q", tc.path, got, tc.canon)
		}
		if _, p, err := Lookup(u, wellFormedProfiles()); err != nil || p.Name != "team" {
			t.Errorf("%q: Lookup: %v", tc.path, err)
		}
		if same, err := SameResource(u, tc.canon); err != nil || !same {
			t.Errorf("%q: SameResource(u, %q) = %v, %v", tc.path, tc.canon, same, err)
		}
		f, err := Format(Parts{Host: "h.example", Space: "team", Path: tc.path})
		if err != nil || f != tc.canon {
			t.Errorf("%q: Format = %q, %v", tc.path, f, err)
		}
		back, err := Parse(f)
		if err != nil || back.Path != tc.path {
			t.Errorf("%q: round trip = %+v, %v", tc.path, back, err)
		}
	}
}

// TestURLIPLiteral_SameAnswerOnEveryToolchain: the bracket rule is this package's
// own (urlIPLiteralOK), because net/url's answer changes with the Go release —
// go1.23 and go1.25.0 accepted "[0:1]" and "[1::2::3]", go1.25.12 refuses them.
// This test runs in the main module AND in the sdk/go copy, which build with
// different toolchains. "&token=x" tells the steps apart: a URL that parses
// reaches the credential check (raw_token); one that does not is capability_malformed.
func TestURLIPLiteral_SameAnswerOnEveryToolchain(t *testing.T) {
	tail := "/v1/cap/blob/" + strings.Repeat("d", 64) + "?sp=team&e=1782000300&k=k2&s=" + strings.Repeat("c", 64) + "&token=x"
	valid := []string{"::", "::1", "1::", "1:2:3:4:5:6:7:8", "::ffff:1.2.3.4", "1:2:3:4:5:6:1.2.3.4", "FE80::1"}
	invalid := []string{
		"", ".", "0:1", ":1", ":::", "1:::2", "1::2::3", "12345::", "1:2:3:4:5:6:7", "1:2:3:4:5:6:7:8:9",
		"1:2:3:4:5:6:7::8", "1.2.3.4", "::1.2.3", "::1.2.3.04", "::ffff:256.1.1.1", "g::1", "fe80::1%25en0", "%25en0",
	}
	for _, h := range valid {
		if _, err := LinkFromBlobURL("https://["+h+"]:8443"+tail, "x"); CodeOf(err) != CodeRawToken {
			t.Errorf("[%s]: got %v, want %s (it is a URL)", h, err, CodeRawToken)
		}
		if !urlIPLiteralOK("https://[" + h + "]:8443") {
			t.Errorf("[%s]: urlIPLiteralOK = false", h)
		}
	}
	for _, h := range invalid {
		if _, err := LinkFromBlobURL("https://["+h+"]:8443"+tail, "x"); CodeOf(err) != CodeCapabilityMalformed {
			t.Errorf("[%s]: got %v, want %s (not a URL)", h, err, CodeCapabilityMalformed)
		}
		if _, _, err := ProfileServerHostPort("https://[" + h + "]:8443"); CodeOf(err) != CodeNoProfile {
			t.Errorf("[%s]: ProfileServerHostPort: got %v, want %s", h, err, CodeNoProfile)
		}
		body := []byte(`{"protocol":"acp/1","endpoint":"https://[` + h + `]:8443","capabilities":["acpuri"]}`)
		if _, err := ParseDiscoveryDocument(body, DiscoveryAddress{Host: "[" + h + "]", Port: 8443}, ""); CodeOf(err) != CodeDiscoveryMalformed {
			t.Errorf("[%s]: ParseDiscoveryDocument: got %v, want %s", h, err, CodeDiscoveryMalformed)
		}
	}
	for _, raw := range []string{"https://a[::1]:8443", "https://a]:8443", "https://[::1", "https://[[::1]]:8443"} {
		if urlIPLiteralOK(raw) {
			t.Errorf("%s: urlIPLiteralOK = true", raw)
		}
	}
}

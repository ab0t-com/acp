// acp:// DISCOVERY (ext-32 §3.8, amendment ext32-3.8-discovery-doc, D4
// RESOLVED 2026-09-11) — the bare-host bootstrap, tested AS AN ADVERSARY.
//
// PARITY: a row-for-row port of the TS SDK's acp/sdk/ts/test/uri-discovery.test.mjs
// (each Test* names the TS test it mirrors), plus the Go-native live proofs
// the TS fetch cannot make: the presented certificate is read from the
// connection (resp.TLS), so the pin check runs automatically, and a live
// redirect target proves the client never followed. FAIL-SAFE: every live
// check is an httptest TLS server on a random loopback port with t.Cleanup —
// never :8443, never /srv/acp, never ~/.acp.
package acpuri

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var (
	fpZero = "sha256:" + strings.Repeat("0", 64)
	fpOne  = "sha256:" + strings.Repeat("1", 64)
	addrH  = DiscoveryAddress{Host: "h.example", Port: 8443}
)

func goodDoc() map[string]any {
	return map[string]any{
		"protocol":     "acp/1",
		"endpoint":     "https://h.example:8443",
		"capabilities": []string{"acpuri", "channels", "crdtjson"},
	}
}

func docWith(kv ...any) map[string]any {
	d := goodDoc()
	for i := 0; i+1 < len(kv); i += 2 {
		d[kv[i].(string)] = kv[i+1]
	}
	return d
}

func docWithout(keys ...string) map[string]any {
	d := goodDoc()
	for _, k := range keys {
		delete(d, k)
	}
	return d
}

func bodyOf(t *testing.T, doc any) []byte {
	t.Helper()
	switch v := doc.(type) {
	case string:
		return []byte(v)
	case []byte:
		return v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
}

func refusesDoc(t *testing.T, doc any, code Code) *Error {
	t.Helper()
	return refusesDocAt(t, doc, code, addrH, "")
}

func refusesDocAt(t *testing.T, doc any, code Code, addr DiscoveryAddress, presented string) *Error {
	t.Helper()
	body := bodyOf(t, doc)
	_, err := ParseDiscoveryDocument(body, addr, presented)
	if err == nil {
		t.Fatalf("%.80s: expected refusal %q, got acceptance", body, code)
	}
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("%.80s: expected *acpuri.Error, got %T: %v", body, err, err)
	}
	if e.Code != code {
		t.Fatalf("%.80s: expected code %q, got %q (%s)", body, code, e.Code, e.Msg)
	}
	return e
}

func mustDoc(t *testing.T, doc any, addr DiscoveryAddress, presented string) *Discovery {
	t.Helper()
	d, err := ParseDiscoveryDocument(bodyOf(t, doc), addr, presented)
	if err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}
	return d
}

func refusesAddr(t *testing.T, input string, code Code) {
	t.Helper()
	_, err := ParseDiscoveryAddress(input)
	if CodeOf(err) != code {
		t.Fatalf("%q: expected %q, got %v", input, code, err)
	}
}

// TS: "discovery address: the bare-host form (default port 8443, explicit port, trailing slash, fragment ignored)"
func TestDiscoveryAddressBareHost(t *testing.T) {
	for in, want := range map[string]DiscoveryAddress{
		"acp://H.Example/":           {Host: "h.example", Port: 8443},
		"acp://h.example":            {Host: "h.example", Port: 8443},
		"acp://h.example:9443/":      {Host: "h.example", Port: 9443},
		"acp://[fe80::1]:9443/#frag": {Host: "[fe80::1]", Port: 9443},
	} {
		a, err := ParseDiscoveryAddress(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		eq(t, *a, want, in)
	}
	eq(t, DiscoveryURL("h.example", 8443), "https://h.example:8443/.well-known/acp", "url")
	eq(t, DiscoveryPath, "/.well-known/acp", "path")
}

// TS: "discovery address: a resource URI contributes its authority (every resource refusal still applies first)"
func TestDiscoveryAddressResource(t *testing.T) {
	a, err := ParseDiscoveryAddress("acp://h.example:9443/team/docs/x.md?v=3")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, a.Host, "h.example", "host")
	eq(t, a.Port, 9443, "port")
	eq(t, a.Resource.Space, "team", "space")
	eq(t, a.Resource.Path, "docs/x.md", "path")
	refusesAddr(t, "acp://h.example/team/x?token=abc", CodeRawToken)
	refusesAddr(t, "acp://tok@h.example/team/x", CodeUserinfo)
	refusesAddr(t, "acp://h.example/team/@doc/x", CodeReservedType)
	refusesAddr(t, "acp://h.example/team/../x", CodeTraversal)
}

// TS: "discovery address: the bare host takes NO query — a credential-shaped key is refused as such; the ironclad rule holds for discovery too"
func TestDiscoveryAddressNoQuery(t *testing.T) {
	refusesAddr(t, "acp://h.example/?token=abc", CodeRawToken)
	refusesAddr(t, "acp://h.example?TOKEN=abc", CodeRawToken)
	refusesAddr(t, "acp://h.example/?%74oken=abc", CodeRawToken)
	refusesAddr(t, "acp://h.example/?v=3", CodeQuery)
	refusesAddr(t, "acp://h.example/?x", CodeQuery)
	refusesAddr(t, "acp://user:pw@h.example/", CodeUserinfo)
	refusesAddr(t, "acp+http://h.example/", CodeScheme)
	refusesAddr(t, "https://h.example/", CodeScheme)
	refusesAddr(t, "acp://", CodeAuthority)
	refusesAddr(t, "acp://h.example:0/", CodePort)
	refusesAddr(t, "acp://h.example:99999/", CodePort)
	refusesAddr(t, "acp://h_x.example/", CodeHost)
	refusesAddr(t, "acp://h.example/#%00", CodeControlChar)
}

// TS: "document: the minimal valid doc; optional fields absent or null read as absent; unknown and @-keys are ignored and reported"
func TestDiscoveryDocMinimal(t *testing.T) {
	body := `{"protocol":"acp/1","endpoint":"https://h.example:8443","capabilities":["acpuri","channels","crdtjson"],"spaces":null,"default_space":null,"cert_fingerprint":null,"extra":1,"@type":"x"}`
	d := mustDoc(t, body, addrH, "")
	eq(t, d.Host, "h.example", "host")
	eq(t, d.Port, 8443, "port")
	eq(t, d.URL, "https://h.example:8443/.well-known/acp", "url")
	eq(t, d.Doc, DiscoveryDocument{Protocol: "acp/1", Endpoint: "https://h.example:8443", Capabilities: []string{"acpuri", "channels", "crdtjson"}}, "doc")
	eq(t, d.EndpointHost, "h.example", "endpointHost")
	eq(t, d.EndpointPort, 8443, "endpointPort")
	eq(t, d.BaseURL, "https://h.example:8443", "baseUrl")
	if d.PinVerified != nil {
		t.Fatal("pinVerified must be nil")
	}
	eq(t, d.IgnoredKeys, []string{"extra", "@type"}, "ignoredKeys in document order")
	d2 := mustDoc(t, docWith("capabilities", []string{}), addrH, "")
	eq(t, d2.Doc.Capabilities, []string{}, "empty capabilities")
	eq(t, d2.IgnoredKeys, []string{}, "no ignored keys")
	// The JSON form uses the TS property names (the CLI prints it; parity of diagnostics).
	js, _ := json.Marshal(d)
	for _, k := range []string{`"host"`, `"port"`, `"url"`, `"doc"`, `"endpointHost"`, `"endpointPort"`, `"baseUrl"`, `"pinVerified":null`, `"ignoredKeys"`, `"defaultSpace":null`, `"certFingerprint":null`, `"spaces":null`} {
		if !strings.Contains(string(js), k) {
			t.Fatalf("json lacks %s: %s", k, js)
		}
	}
}

// TS: "document: the full doc — spaces, default_space, cert_fingerprint (format only when the presented cert is unknown → pinVerified null)"
func TestDiscoveryDocFull(t *testing.T) {
	d := mustDoc(t, docWith("spaces", []string{"team", "docs_2"}, "default_space", "team", "cert_fingerprint", fpZero), addrH, "")
	eq(t, d.Doc.Spaces, []string{"team", "docs_2"}, "spaces")
	eq(t, *d.Doc.DefaultSpace, "team", "defaultSpace")
	eq(t, *d.Doc.CertFingerprint, fpZero, "certFingerprint")
	if d.PinVerified != nil {
		t.Fatal("the pin is NOT claimed verified when the presented certificate is unknown")
	}
}

// TS: "document: the endpoint may move the PORT on the same host (gateway on 443, API elsewhere), never the HOST"
func TestDiscoveryDocHostMatch(t *testing.T) {
	d := mustDoc(t, docWith("endpoint", "https://h.example"), addrH, "")
	eq(t, d.EndpointPort, 443, "default https port")
	eq(t, d.BaseURL, "https://h.example:443", "baseUrl")
	d2 := mustDoc(t, docWith("endpoint", "https://H.EXAMPLE.:9443/"), addrH, "")
	eq(t, d2.EndpointHost, "h.example.", "endpointHost")
	eq(t, d2.EndpointPort, 9443, "endpointPort")
	eq(t, d2.Doc.Endpoint, "https://H.EXAMPLE.:9443", "trailing slash stripped, otherwise as published")
	for _, ep := range []string{"https://other.example:8443", "https://api.h.example:8443", "https://example:8443", "https://www.h.example", "https://hh.example", "https://h.example.evil.example", "https://127.0.0.1:8443", "https://[::1]:8443"} {
		e := refusesDoc(t, docWith("endpoint", ep), CodeDiscoveryHostMismatch)
		if !strings.Contains(e.Msg, "may only describe its own host") {
			t.Fatalf("message: %s", e.Msg)
		}
	}
	a6 := DiscoveryAddress{Host: "[fe80::1]", Port: 8443}
	d6 := mustDoc(t, docWith("endpoint", "https://[FE80::1]:8443"), a6, "")
	eq(t, d6.EndpointHost, "[fe80::1]", "ipv6 bracketed lowercase")
	refusesDocAt(t, docWith("endpoint", "https://[fe80::2]:8443"), CodeDiscoveryHostMismatch, a6, "")
}

// TS: "document: DOWNGRADE attempts are refused — cleartext endpoint, a path (incl. /v1), userinfo, query, another wire version"
func TestDiscoveryDocDowngrade(t *testing.T) {
	e := refusesDoc(t, docWith("endpoint", "http://h.example:8443"), CodeDiscoveryMalformed)
	if !strings.Contains(e.Msg, "downgrade") {
		t.Fatalf("message: %s", e.Msg)
	}
	refusesDoc(t, docWith("endpoint", "ws://h.example:8443"), CodeDiscoveryMalformed)
	refusesDoc(t, docWith("endpoint", "acp://h.example:8443"), CodeDiscoveryMalformed)
	v1 := refusesDoc(t, docWith("endpoint", "https://h.example:8443/v1"), CodeDiscoveryMalformed)
	if !strings.Contains(v1.Msg, "do not append /v1") {
		t.Fatalf("message: %s", v1.Msg)
	}
	for _, ep := range []string{"https://h.example:8443/v1/", "https://h.example:8443/acp", "https://h.example:8443//", "https://u:p@h.example:8443", "https://h.example:8443?x=1", "https://h.example:8443#f", "h.example:8443", "", "https://h.example:0", "https://h.example:x"} {
		refusesDoc(t, docWith("endpoint", ep), CodeDiscoveryMalformed)
	}
	refusesDoc(t, docWith("endpoint", 8443), CodeDiscoveryMalformed)
	for _, p := range []any{"acp/2", "acp/0", "ACP/1", "acp/1 "} {
		refusesDoc(t, docWith("protocol", p), CodeDiscoveryProtocol)
	}
	for _, p := range []any{"", 1, nil, []string{"acp/1"}} {
		refusesDoc(t, docWith("protocol", p), CodeDiscoveryMalformed)
	}
}

// TS: "document: MALFORMED bodies are refused, never best-effort — not JSON, not an object, oversize, bad UTF-8, missing/mistyped fields"
func TestDiscoveryDocMalformed(t *testing.T) {
	for _, body := range []string{"", "<html>captive portal</html>", "null", "[]", `"acp/1"`, "{", "{}"} {
		refusesDoc(t, body, CodeDiscoveryMalformed)
	}
	refusesDoc(t, []byte{0x7b, 0xff, 0x7d}, CodeDiscoveryMalformed) // invalid UTF-8
	refusesDoc(t, docWithout("protocol"), CodeDiscoveryMalformed)
	refusesDoc(t, docWithout("endpoint"), CodeDiscoveryMalformed)
	refusesDoc(t, docWithout("capabilities"), CodeDiscoveryMalformed)
	refusesDoc(t, docWith("capabilities", nil), CodeDiscoveryMalformed)
	refusesDoc(t, docWith("capabilities", "acpuri"), CodeDiscoveryMalformed)
	refusesDoc(t, docWith("capabilities", []string{"acpuri", ""}), CodeDiscoveryMalformed)
	refusesDoc(t, docWith("capabilities", []int{1}), CodeDiscoveryMalformed)
	refusesDoc(t, docWith("spaces", "team"), CodeDiscoveryMalformed)
	refusesDoc(t, docWith("spaces", []string{"te am"}), CodeDiscoveryMalformed)
	refusesDoc(t, docWith("spaces", []string{"../x"}), CodeDiscoveryMalformed)
	refusesDoc(t, docWith("spaces", []string{strings.Repeat("a", 65)}), CodeDiscoveryMalformed)
	refusesDoc(t, docWith("default_space", []string{"team"}), CodeDiscoveryMalformed)
	refusesDoc(t, docWith("default_space", "te/am"), CodeDiscoveryMalformed)
	refusesDoc(t, docWith("default_space", ""), CodeDiscoveryMalformed)
	refusesDoc(t, docWith("cert_fingerprint", strings.Repeat("0", 64)), CodeDiscoveryMalformed) // needs the sha256: prefix
	refusesDoc(t, docWith("cert_fingerprint", "sha256:"+strings.Repeat("A", 64)), CodeDiscoveryMalformed)
	refusesDoc(t, docWith("cert_fingerprint", "sha256:"+strings.Repeat("0", 63)), CodeDiscoveryMalformed)
	refusesDoc(t, docWith("cert_fingerprint", "sha1:"+strings.Repeat("0", 40)), CodeDiscoveryMalformed)
	refusesDoc(t, docWith("cert_fingerprint", 5), CodeDiscoveryMalformed)
	// Oversize: exactly the cap is fine; one byte over is refused before parsing.
	pad := func(n int) []byte { return bodyOf(t, docWith("pad", strings.Repeat("x", n))) }
	atCap := pad(MaxDiscoveryBytes - len(pad(0)))
	eq(t, len(atCap), MaxDiscoveryBytes, "at cap")
	mustDoc(t, atCap, addrH, "")
	refusesDoc(t, []byte(strings.Replace(string(atCap), `"x`, `"xx`, 1)), CodeDiscoveryMalformed)
}

// TS: "document: the cert pin must AGREE with the certificate that served it — a lying pin is refused, a matching one is verified, never a downgrade"
func TestDiscoveryDocPin(t *testing.T) {
	d := mustDoc(t, docWith("cert_fingerprint", fpZero), addrH, fpZero)
	if d.PinVerified == nil || !*d.PinVerified {
		t.Fatal("pinVerified must be true")
	}
	// The presented value may be given without the prefix / in uppercase hex.
	d = mustDoc(t, docWith("cert_fingerprint", fpZero), addrH, strings.Repeat("0", 64))
	if d.PinVerified == nil || !*d.PinVerified {
		t.Fatal("pinVerified must be true (unprefixed presented)")
	}
	e := refusesDocAt(t, docWith("cert_fingerprint", fpZero), CodeDiscoveryPinMismatch, addrH, fpOne)
	if !strings.Contains(e.Msg, "misdescribes its own transport") {
		t.Fatalf("message: %s", e.Msg)
	}
	// No pin in the doc: nothing to verify, whatever the connection presented.
	if mustDoc(t, goodDoc(), addrH, fpOne).PinVerified != nil {
		t.Fatal("no pin -> nil")
	}
	// The helper produces the wire format.
	eq(t, CertFingerprintDER([]byte{1, 2, 3}), "sha256:039058c6f2c0cb492c533b0a4d14ef77cc0f78abccced5287d84a1a2011cfb81", "fingerprint")
}

// TS: "document: the validation ORDER is fixed (first failure wins), so a refusal is deterministic across implementations"
func TestDiscoveryDocOrder(t *testing.T) {
	refusesDoc(t, map[string]any{"protocol": "acp/9", "endpoint": "http://x", "capabilities": 1, "cert_fingerprint": "bad"}, CodeDiscoveryProtocol)
	refusesDoc(t, map[string]any{"protocol": "acp/1", "endpoint": "http://h.example", "capabilities": 1}, CodeDiscoveryMalformed)
	refusesDoc(t, map[string]any{"protocol": "acp/1", "endpoint": "https://other.example", "capabilities": 1}, CodeDiscoveryHostMismatch)
	refusesDoc(t, docWith("capabilities", 1, "spaces", 1), CodeDiscoveryMalformed)
	refusesDocAt(t, docWith("cert_fingerprint", fpZero, "spaces", 1), CodeDiscoveryMalformed, addrH, fpOne)
}

// discoveryDaemon is a stub daemon serving /.well-known/acp over TLS on a
// random loopback port, recording every request it sees.
type discoveryDaemon struct {
	ts     *httptest.Server
	got    []*http.Request
	status int
	body   func() []byte
	header http.Header
}

func newDiscoveryDaemon(t *testing.T) *discoveryDaemon {
	t.Helper()
	d := &discoveryDaemon{status: 200, header: http.Header{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		d.got = append(d.got, r.Clone(r.Context()))
		for k, v := range d.header {
			w.Header()[k] = v
		}
		if r.URL.Path != DiscoveryPath {
			http.Error(w, `{"error":"not found"}`, 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(d.status)
		if d.body != nil {
			w.Write(d.body())
		}
	})
	d.ts = httptest.NewTLSServer(mux)
	t.Cleanup(d.ts.Close)
	return d
}

func (d *discoveryDaemon) addr(t *testing.T) string { return "acp://" + hostPortOf(t, d.ts.URL) + "/" }

func (d *discoveryDaemon) fingerprint() string { return CertFingerprint(d.ts.Certificate()) }

func (d *discoveryDaemon) serve(doc map[string]any) {
	d.body = func() []byte { b, _ := json.Marshal(doc); return b }
}

// TS: "discoverACP: ONE anonymous GET of /.well-known/acp — no headers at all, no redirect followed; the parsed doc comes back"
// GO-NATIVE: the presented certificate comes from resp.TLS, so the pin check is automatic.
func TestDiscoverLiveAnonymousAndPinned(t *testing.T) {
	d := newDiscoveryDaemon(t)
	d.serve(map[string]any{"protocol": "acp/1", "endpoint": "https://127.0.0.1:9443", "capabilities": []string{"acpuri"}, "cert_fingerprint": d.fingerprint()})
	res, err := Discover(strings.TrimSuffix(d.addr(t), "/")+"/#x", DiscoverOptions{HTTPClient: d.ts.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.got) != 1 {
		t.Fatalf("want exactly one request, got %d", len(d.got))
	}
	r := d.got[0]
	eq(t, r.Method+" "+r.URL.String(), "GET /.well-known/acp", "request")
	for _, h := range []string{"Authorization", "X-ACP-Space", "X-ACP-Agent", "X-ACP-Protocol", "Cookie"} {
		if r.Header.Get(h) != "" {
			t.Fatalf("anonymous: no %s header, got %v", h, r.Header)
		}
	}
	eq(t, res.BaseURL, "https://127.0.0.1:9443", "baseUrl (port may move on the same host)")
	eq(t, res.URL, "https://"+hostPortOf(t, d.ts.URL)+"/.well-known/acp", "url")
	if res.PinVerified == nil || !*res.PinVerified {
		t.Fatal("the served certificate's fingerprint must verify automatically in Go")
	}
	// A resource address discovers ITS daemon (authority only).
	if _, err := Discover(strings.TrimSuffix(d.addr(t), "/")+"/team/docs/x.md", DiscoverOptions{HTTPClient: d.ts.Client()}); err != nil {
		t.Fatal(err)
	}
	eq(t, d.got[len(d.got)-1].URL.Path, DiscoveryPath, "resource address -> discovery path")
	// The daemon lying about its own certificate is refused on the real path.
	d.serve(map[string]any{"protocol": "acp/1", "endpoint": "https://127.0.0.1", "capabilities": []string{}, "cert_fingerprint": fpOne})
	if _, err := Discover(d.addr(t), DiscoverOptions{HTTPClient: d.ts.Client()}); CodeOf(err) != CodeDiscoveryPinMismatch {
		t.Fatalf("lying pin: %v", err)
	}
	// No pin in the doc: nil, not a fabricated true.
	d.serve(map[string]any{"protocol": "acp/1", "endpoint": "https://127.0.0.1", "capabilities": []string{}})
	res, err = Discover(d.addr(t), DiscoverOptions{HTTPClient: d.ts.Client()})
	if err != nil || res.PinVerified != nil {
		t.Fatalf("no pin: %v %v", err, res.PinVerified)
	}
}

// GO-NATIVE: a matching fingerprint NEVER relaxes TLS verification — a client
// without trust in the daemon's cert fails the handshake even though the
// document (which it never gets to read) carries the right pin.
func TestDiscoverPinIsNeverADowngrade(t *testing.T) {
	d := newDiscoveryDaemon(t)
	d.serve(map[string]any{"protocol": "acp/1", "endpoint": "https://127.0.0.1", "capabilities": []string{}, "cert_fingerprint": d.fingerprint()})
	_, err := Discover(d.addr(t), DiscoverOptions{HTTPClient: &http.Client{}}) // system roots only: the self-signed daemon is untrusted
	if err == nil || CodeOf(err) != "" {
		t.Fatalf("want a TLS verification error (not an acpuri refusal, not success), got %v", err)
	}
	if len(d.got) != 0 {
		t.Fatalf("the handshake must fail before any request is served, got %d", len(d.got))
	}
}

// TS: "discoverACP: address refusals happen BEFORE any request"
func TestDiscoverRefusesAddressBeforeRequest(t *testing.T) {
	d := newDiscoveryDaemon(t)
	d.serve(goodDoc())
	base := strings.TrimSuffix(d.addr(t), "/")
	for in, code := range map[string]Code{
		base + "/?token=abc":       CodeRawToken,
		"acp://tok@" + base[6:]:    CodeUserinfo,
		base + "/?v=1":             CodeQuery,
		"https://" + base[6:]:      CodeScheme,
		base + "/team/x?token=abc": CodeRawToken,
	} {
		if _, err := Discover(in, DiscoverOptions{HTTPClient: d.ts.Client()}); CodeOf(err) != code {
			t.Fatalf("%s: want %q, got %v", in, code, err)
		}
	}
	if len(d.got) != 0 {
		t.Fatalf("refused addresses must produce no request, got %d", len(d.got))
	}
}

// TS: "discoverACP: a REDIRECT is refused (never followed — a host redirect would move the bootstrap off the address)"
// GO-NATIVE: the redirect TARGET is a live server that must see zero requests.
func TestDiscoverRefusesRedirect(t *testing.T) {
	target := newDiscoveryDaemon(t)
	target.serve(goodDoc())
	d := newDiscoveryDaemon(t)
	for _, status := range []int{301, 302, 303, 307, 308} {
		d.status = status
		d.header = http.Header{"Location": []string{target.ts.URL + DiscoveryPath}}
		_, err := Discover(d.addr(t), DiscoverOptions{HTTPClient: d.ts.Client()})
		if CodeOf(err) != CodeDiscoveryRedirect || !strings.Contains(err.Error(), target.ts.URL) {
			t.Fatalf("%d: %v", status, err)
		}
	}
	if len(target.got) != 0 {
		t.Fatalf("the redirect target must never be contacted, got %d requests", len(target.got))
	}
	// The caller's own redirect policy cannot re-enable following.
	hc := d.ts.Client()
	hc.CheckRedirect = nil
	if _, err := Discover(d.addr(t), DiscoverOptions{HTTPClient: hc}); CodeOf(err) != CodeDiscoveryRedirect {
		t.Fatalf("with the default policy: %v", err)
	}
	if len(target.got) != 0 {
		t.Fatal("still never contacted")
	}
}

// TS: "discoverACP: 404 = the daemon does not serve discovery (discovery_unsupported); other statuses = discovery_http with the daemon's message"
func TestDiscoverStatuses(t *testing.T) {
	d := newDiscoveryDaemon(t)
	d.status = 404
	d.body = func() []byte { return []byte("not found") }
	_, err := Discover(d.addr(t), DiscoverOptions{HTTPClient: d.ts.Client()})
	if CodeOf(err) != CodeDiscoveryUnsupported || !strings.Contains(err.Error(), "configured or user-supplied endpoint") {
		t.Fatalf("404: %v", err)
	}
	for _, status := range []int{401, 403, 500, 503} {
		d.status = status
		d.body = func() []byte { return []byte(`{"error":"stub ` + http.StatusText(status) + `"}`) }
		_, err := Discover(d.addr(t), DiscoverOptions{HTTPClient: d.ts.Client()})
		if CodeOf(err) != CodeDiscoveryHTTP || !strings.Contains(err.Error(), "stub "+http.StatusText(status)) {
			t.Fatalf("%d: %v", status, err)
		}
	}
	d.status = 200
	d.body = func() []byte { return []byte("<html>") }
	if _, err := Discover(d.addr(t), DiscoverOptions{HTTPClient: d.ts.Client()}); CodeOf(err) != CodeDiscoveryMalformed {
		t.Fatalf("html: %v", err)
	}
	d.serve(docWith("endpoint", "https://evil.example"))
	if _, err := Discover(d.addr(t), DiscoverOptions{HTTPClient: d.ts.Client()}); CodeOf(err) != CodeDiscoveryHostMismatch {
		t.Fatalf("foreign endpoint: %v", err)
	}
}

// TS: "discoverACP: the body is BOUNDED — an oversize document is refused whether or not Content-Length says so"
func TestDiscoverBoundsBody(t *testing.T) {
	d := newDiscoveryDaemon(t)
	big := bodyOf(t, docWith("pad", strings.Repeat("x", MaxDiscoveryBytes)))
	d.body = func() []byte { return big } // net/http sets Content-Length for a small-enough single write; either way it is refused
	if _, err := Discover(d.addr(t), DiscoverOptions{HTTPClient: d.ts.Client()}); CodeOf(err) != CodeDiscoveryMalformed {
		t.Fatalf("oversize: %v", err)
	}
	// Chunked (no Content-Length): still refused once the cap is crossed.
	d.header = http.Header{"Transfer-Encoding": []string{"chunked"}}
	if _, err := Discover(d.addr(t), DiscoverOptions{HTTPClient: d.ts.Client()}); CodeOf(err) != CodeDiscoveryMalformed {
		t.Fatalf("oversize chunked: %v", err)
	}
	// Exactly at the cap is fine.
	d.header = http.Header{}
	pad := func(n int) []byte {
		return bodyOf(t, docWith("endpoint", "https://127.0.0.1", "pad", strings.Repeat("x", n)))
	}
	atCap := pad(MaxDiscoveryBytes - len(pad(0)))
	eq(t, len(atCap), MaxDiscoveryBytes, "at cap")
	d.body = func() []byte { return atCap }
	if _, err := Discover(d.addr(t), DiscoverOptions{HTTPClient: d.ts.Client()}); err != nil {
		t.Fatalf("at cap: %v", err)
	}
}

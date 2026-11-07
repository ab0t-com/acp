// Shared Go/TS test vectors for the acp:// resolver (ext-32).
//
// ONE table, acp/testdata/acpuri-vectors.json, is read by this file AND by the
// TypeScript SDK's sdk/ts/test/uri-vectors.test.mjs, so the two resolvers are
// pinned to the same expected answer for every address in it.
//
// The DIFFERENTIAL half: acp/testdata/acpuri-differential.json holds this
// resolver's FULL normalized result for every vector plus a few thousand
// generated inputs (seeded mutations of the vectors). TestDifferentialGolden
// proves the file is exactly what this Go code produces today; the TS suite
// proves the TS SDK produces exactly the same file. Together: Go and TS cannot
// silently disagree about any address in it. After an intended change:
//
//	ACPURI_UPDATE_DIFFERENTIAL=1 go test ./pkg/acpuri -run TestDifferentialGolden
//
// then run the TS suite; a TS failure is a real Go/TS disagreement to fix on the
// side that is wrong per ext-32. Pure: no network (discover_http vectors use an
// in-memory http.RoundTripper), no files written unless the update variable is set.
package acpuri

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

const (
	vectorsFile      = "acpuri-vectors.json"
	differentialFile = "acpuri-differential.json"
	updateEnv        = "ACPURI_UPDATE_DIFFERENTIAL"
	// generatedInputs is how many mutated inputs the differential file carries.
	generatedInputs = 1500
	// generatedURLInputs is how many mutated https URLs each URL-reading op (link,
	// discovery endpoint, profile server) contributes.
	generatedURLInputs = 300
	// genSeed seeds the mutator (mulberry32) so a regeneration is reproducible.
	genSeed = 0x5eed2026
)

type vectorFile struct {
	Codes   []string         `json:"codes"`
	Vectors []map[string]any `json:"vectors"`
}

// testdataDir finds acp/testdata by walking up from the package directory. It
// stops at the private acp module root (go.mod "module github.com/ab0t-com/acp"):
// inside that module a missing file is a FAILURE (never a silent skip). Only a
// published copy of this package with no acp tree above it skips.
func testdataDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "testdata", vectorsFile)); err == nil {
			return filepath.Join(dir, "testdata")
		}
		if b, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil && bytes.Contains(b, []byte("module github.com/ab0t-com/acp\n")) {
			t.Fatalf("%s not found under %s/testdata — the shared Go/TS vectors are missing", vectorsFile, dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skipf("%s lives in the acp source tree (acp/testdata); not present in this copy", vectorsFile)
		}
		dir = parent
	}
}

func loadVectors(t *testing.T) (*vectorFile, string) {
	t.Helper()
	dir := testdataDir(t)
	b, err := os.ReadFile(filepath.Join(dir, vectorsFile))
	if err != nil {
		t.Fatal(err)
	}
	var vf vectorFile
	if err := json.Unmarshal(b, &vf); err != nil {
		t.Fatalf("%s: %v", vectorsFile, err)
	}
	if len(vf.Vectors) == 0 {
		t.Fatalf("%s has no vectors", vectorsFile)
	}
	return &vf, dir
}

// ---------------------------------------------------------------------------
// The normalized result of one operation. The TS test builds the SAME shape
// (field names are the TS SDK's; refusals carry only the code — messages are
// worded per language and compared only for the no-echo rule).
// ---------------------------------------------------------------------------

// jsonValue round-trips v through JSON so it compares like a decoded vector.
func jsonValue(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		panic(err)
	}
	return out
}

func refusal(err error) (map[string]any, string) {
	code := CodeOf(err)
	if code == "" {
		// Not an *Error: an unexpected failure class. Surface it as its own
		// "code" so it can never compare equal to a real refusal.
		return map[string]any{"code": "!non-acpuri-error"}, err.Error()
	}
	return map[string]any{"code": string(code)}, err.Error()
}

func parseResult(input string) (map[string]any, string) {
	u, err := Parse(input)
	if err != nil {
		return refusal(err)
	}
	r := Resolve(u)
	resolved := jsonValue(r).(map[string]any)
	delete(resolved, "uri") // already reported as "uri"
	return map[string]any{"uri": jsonValue(u), "canonical": Canonical(u), "resolved": resolved}, ""
}

func discoveryAddressResult(input string) (map[string]any, string) {
	a, err := ParseDiscoveryAddress(input)
	if err != nil {
		return refusal(err)
	}
	var resource any
	if a.Resource != nil {
		resource = Canonical(a.Resource)
	}
	return map[string]any{"host": a.Host, "port": float64(a.Port), "resource": resource}, ""
}

func discoveryOut(d *Discovery) map[string]any {
	m := jsonValue(d).(map[string]any)
	doc := m["doc"].(map[string]any)
	delete(m, "doc")
	for k, v := range doc {
		m[k] = v
	}
	return m
}

func discoveryDocumentResult(v map[string]any) (map[string]any, string) {
	addr := v["address"].(map[string]any)
	presented := ""
	if p, ok := v["presented"].(string); ok {
		presented = p
	}
	d, err := ParseDiscoveryDocument([]byte(v["input"].(string)), DiscoveryAddress{Host: addr["host"].(string), Port: int(addr["port"].(float64))}, presented)
	if err != nil {
		return refusal(err)
	}
	return discoveryOut(d), ""
}

// cannedTransport answers every request with one canned response — no socket
// is ever opened. It records the request so the test can assert it was anonymous.
type cannedTransport struct {
	status int
	loc    string
	body   string
	seen   []*http.Request
}

func (c *cannedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.seen = append(c.seen, r)
	h := http.Header{}
	if c.loc != "" {
		h.Set("Location", c.loc)
	}
	return &http.Response{StatusCode: c.status, Header: h, Body: io.NopCloser(strings.NewReader(c.body)), ContentLength: int64(len(c.body)), Request: r}, nil
}

func discoverHTTPResult(t *testing.T, v map[string]any) (map[string]any, string) {
	resp := v["response"].(map[string]any)
	ct := &cannedTransport{status: int(resp["status"].(float64)), body: resp["body"].(string)}
	if loc, ok := resp["location"].(string); ok {
		ct.loc = loc
	}
	if pad, ok := resp["padSpaces"].(float64); ok {
		ct.body += strings.Repeat(" ", int(pad))
	}
	d, err := Discover(v["input"].(string), DiscoverOptions{HTTPClient: &http.Client{Transport: ct}})
	if t != nil {
		if len(ct.seen) != 1 {
			t.Errorf("%v: discovery made %d requests, want exactly 1 (no redirect followed)", v["id"], len(ct.seen))
		}
		for _, r := range ct.seen {
			for k := range r.Header {
				t.Errorf("%v: discovery sent header %q — it must be anonymous", v["id"], k)
			}
		}
	}
	if err != nil {
		return refusal(err)
	}
	return discoveryOut(d), ""
}

func lookupResult(v map[string]any) (map[string]any, string) {
	u, err := Parse(v["input"].(string))
	if err != nil {
		return refusal(err)
	}
	var ps []Profile
	for _, raw := range v["profiles"].([]any) {
		p := raw.(map[string]any)
		pr := Profile{Name: p["name"].(string), Server: p["server"].(string), Token: p["token"].(string), Agent: p["agent"].(string)}
		if s, ok := p["space"].(string); ok {
			pr.Space = s
		}
		if d, ok := p["default"].(bool); ok {
			pr.Default = d
		}
		ps = append(ps, pr)
	}
	r, p, err := Lookup(u, NewStaticProfileStore(ps))
	if err != nil {
		return refusal(err)
	}
	return map[string]any{"profile": p.Name, "canonical": r.Canonical}, ""
}

func linkResult(v map[string]any) (map[string]any, string) {
	s, err := LinkFromBlobURL(v["input"].(string), v["path"].(string))
	if err != nil {
		return refusal(err)
	}
	return map[string]any{"uri": s}, ""
}

func formatResult(v map[string]any) (map[string]any, string) {
	in := v["input"].(map[string]any)
	p := Parts{Host: in["host"].(string), Space: in["space"].(string)}
	if n, ok := in["port"].(float64); ok {
		p.Port = int(n)
	}
	if s, ok := in["path"].(string); ok {
		p.Path = s
	}
	if k, ok := in["kind"].(string); ok {
		p.Kind = ResourceKind(k)
	}
	if n, ok := in["version"].(float64); ok {
		ver := int64(n)
		p.Version = &ver
	}
	s, err := Format(p)
	if err != nil {
		return refusal(err)
	}
	return map[string]any{"uri": s}, ""
}

// illFormedRE matches the escapes of an "illFormed" vector (D26, clarification
// ext32-3.7-wellformed-text): <U+D800>..<U+DFFF> is one lone surrogate code
// point and <0xNN> one raw byte. JSON cannot carry either — a decoder turns them
// into U+FFFD, the very substitution these vectors pin as refused.
var illFormedRE = regexp.MustCompile(`<U\+(D[89A-F][0-9A-F]{2})>|<0x([0-9A-F]{2})>`)

// expandIllFormed turns the escapes into the octets a Go string holds: a lone
// surrogate as its generalized-UTF-8 bytes (U+D800 = ED A0 80 — exactly what
// the TS twin's builder emits for the same code unit), a raw byte as itself.
func expandIllFormed(s string) string {
	return illFormedRE.ReplaceAllStringFunc(s, func(m string) string {
		sub := illFormedRE.FindStringSubmatch(m)
		if sub[1] != "" {
			cp, _ := strconv.ParseUint(sub[1], 16, 32)
			return string([]byte{byte(0xE0 | cp>>12), byte(0x80 | (cp>>6)&0x3F), byte(0x80 | cp&0x3F)})
		}
		b, _ := strconv.ParseUint(sub[2], 16, 8)
		return string([]byte{byte(b)})
	})
}

// expandVector returns v with every string argument's escapes expanded when v
// is an "illFormed" vector (v itself is never modified: the case written to the
// differential keeps the escapes). It fails loudly if an "illFormed" vector
// expands to well-formed text — the notation would then test nothing.
func expandVector(v map[string]any) map[string]any {
	if v["illFormed"] != true {
		return v
	}
	malformed := false
	var walk func(x any) any
	walk = func(x any) any {
		switch y := x.(type) {
		case string:
			e := expandIllFormed(y)
			if !utf8.ValidString(e) {
				malformed = true
			}
			return e
		case map[string]any:
			out := map[string]any{}
			for k, val := range y {
				out[k] = walk(val)
			}
			return out
		case []any:
			out := make([]any, len(y))
			for i, val := range y {
				out[i] = walk(val)
			}
			return out
		}
		return x
	}
	out := map[string]any{}
	for k, val := range v {
		if k == "id" || k == "op" {
			out[k] = val
			continue
		}
		out[k] = walk(val)
	}
	if !malformed {
		panic(fmt.Sprintf("illFormed vector %v holds no ill-formed text after expansion", v["id"]))
	}
	return out
}

// run executes one vector/case and returns its normalized result and, for a
// refusal, the error message (for the no-echo check). t may be nil (generation).
func run(t *testing.T, v map[string]any) (map[string]any, string) {
	v = expandVector(v)
	switch v["op"] {
	case "parse":
		return parseResult(v["input"].(string))
	case "discovery_address":
		return discoveryAddressResult(v["input"].(string))
	case "discovery_document":
		return discoveryDocumentResult(v)
	case "discover_http":
		return discoverHTTPResult(t, v)
	case "lookup":
		return lookupResult(v)
	case "link":
		return linkResult(v)
	case "format":
		return formatResult(v)
	}
	panic(fmt.Sprintf("unknown op %v", v["op"]))
}

// subsetMismatch reports how got differs from the fields expect pins. Only
// "resolved" (and the parse "uri" fields, which sit at the top of expect) is
// matched as a subset; every other value must be equal in full.
func subsetMismatch(got, expect map[string]any) []string {
	var diffs []string
	if code, ok := expect["code"]; ok {
		if !reflect.DeepEqual(got["code"], code) {
			diffs = append(diffs, fmt.Sprintf("code: got %v, want %v (result %s)", got["code"], code, mustJSON(got)))
		}
		return diffs
	}
	if c, ok := got["code"]; ok {
		return []string{fmt.Sprintf("refused with %v, want success", c)}
	}
	// For parse, expect mixes ACPURI fields with canonical/resolved.
	view := map[string]any{}
	for k, v := range got {
		view[k] = v
	}
	if u, ok := got["uri"].(map[string]any); ok {
		if _, hasURI := expect["uri"]; !hasURI {
			for k, v := range u {
				view[k] = v
			}
		}
	}
	keys := make([]string, 0, len(expect))
	for k := range expect {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		want := expect[k]
		have, ok := view[k]
		if !ok {
			diffs = append(diffs, fmt.Sprintf("%s: missing from result", k))
			continue
		}
		if k == "resolved" {
			wm, hm := want.(map[string]any), have.(map[string]any)
			for rk, rv := range wm {
				if !reflect.DeepEqual(hm[rk], rv) {
					diffs = append(diffs, fmt.Sprintf("resolved.%s: got %s, want %s", rk, mustJSON(hm[rk]), mustJSON(rv)))
				}
			}
			continue
		}
		if !reflect.DeepEqual(have, want) {
			diffs = append(diffs, fmt.Sprintf("%s: got %s, want %s", k, mustJSON(have), mustJSON(want)))
		}
	}
	return diffs
}

func mustJSON(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return strings.TrimSuffix(b.String(), "\n")
}

// TestVectors runs every shared vector against the Go resolver: the expected
// result or refusal code, the sameAs/differentFrom identity rules (§3.7), and
// the no-echo rule for secrets (§3.6).
func TestVectors(t *testing.T) {
	vf, _ := loadVectors(t)
	ids := map[string]bool{}
	for _, v := range vf.Vectors {
		id, _ := v["id"].(string)
		if id == "" || ids[id] {
			t.Fatalf("vector id %q is empty or duplicated", id)
		}
		ids[id] = true
		t.Run(id, func(t *testing.T) {
			got, msg := run(t, v)
			expect := v["expect"].(map[string]any)
			for _, d := range subsetMismatch(got, expect) {
				t.Errorf("%s (%v): %s", id, v["input"], d)
			}
			if noEcho, ok := v["noEcho"].([]any); ok {
				if msg == "" {
					t.Errorf("noEcho on a vector that did not refuse")
				}
				for _, s := range noEcho {
					if strings.Contains(msg, s.(string)) {
						t.Errorf("refusal message echoes the secret %q: %s", s, msg)
					}
				}
			}
			if v["op"] != "parse" {
				return
			}
			canon, _ := got["canonical"].(string)
			if same, ok := v["sameAs"].([]any); ok {
				for _, s := range same {
					c, err := CanonicalString(s.(string))
					if err != nil || c != canon {
						t.Errorf("sameAs %q: canonical %q (err %v), want %q", s, c, err, canon)
					}
				}
			}
			if diff, ok := v["differentFrom"].([]any); ok {
				for _, s := range diff {
					c, err := CanonicalString(s.(string))
					if err != nil {
						t.Errorf("differentFrom %q must itself parse: %v", s, err)
					} else if c == canon {
						t.Errorf("differentFrom %q canonicalizes to the same %q", s, c)
					}
				}
			}
		})
	}
}

// codeConstants reads every `Code… Code = "…"` constant from acpuri.go's
// source, so a new refusal code cannot be added without a vector.
func codeConstants(t *testing.T) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "acpuri.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.CONST {
			continue
		}
		for _, s := range g.Specs {
			vs := s.(*ast.ValueSpec)
			if id, ok := vs.Type.(*ast.Ident); !ok || id.Name != "Code" {
				continue
			}
			for _, val := range vs.Values {
				lit := val.(*ast.BasicLit)
				c, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				codes = append(codes, c)
			}
		}
	}
	sort.Strings(codes)
	return codes
}

// TestVectorsCoverEveryCode: the vector file's code list equals the Go Code
// constants, and every code has at least one vector that refuses with it.
func TestVectorsCoverEveryCode(t *testing.T) {
	vf, _ := loadVectors(t)
	goCodes := codeConstants(t)
	if len(goCodes) < 26 {
		t.Fatalf("found only %d Code constants in acpuri.go — the source scan is broken", len(goCodes))
	}
	listed := append([]string(nil), vf.Codes...)
	sort.Strings(listed)
	if !reflect.DeepEqual(goCodes, listed) {
		t.Errorf("vector file codes %v != Go Code constants %v", listed, goCodes)
	}
	used := map[string]int{}
	for _, v := range vf.Vectors {
		if c, ok := v["expect"].(map[string]any)["code"].(string); ok {
			used[c]++
		}
	}
	for _, c := range goCodes {
		if used[c] == 0 {
			t.Errorf("no vector refuses with %q", c)
		}
	}
}

// ---------------------------------------------------------------------------
// Differential golden
// ---------------------------------------------------------------------------

type diffCase struct {
	// The case itself (a vector's op arguments, or a generated input).
	Case map[string]any `json:"case"`
	// Out is the Go resolver's normalized result. For a generated input it is
	// {"parse": …, "discovery_address": …}.
	Out map[string]any `json:"out"`
}

type diffFile struct {
	About     string     `json:"about"`
	Generated int        `json:"generated"`
	Seed      int        `json:"seed"`
	Cases     []diffCase `json:"cases"`
}

// caseOf strips the expectation fields from a vector, leaving what runs.
func caseOf(v map[string]any) map[string]any {
	c := map[string]any{}
	for k, val := range v {
		switch k {
		case "expect", "sameAs", "differentFrom", "noEcho", "note", "spec":
			continue
		}
		c[k] = val
	}
	return c
}

func runCase(c map[string]any) map[string]any {
	if c["op"] == "generated" {
		// Compact: the URI fields + canonical form (resolution is a pure function of
		// them, pinned per kind by the vectors), plus the discovery-address reading.
		p, _ := parseResult(c["input"].(string))
		delete(p, "resolved")
		d, _ := discoveryAddressResult(c["input"].(string))
		return map[string]any{"parse": p, "discovery_address": d}
	}
	out, _ := run(nil, c)
	return out
}

// vecPRNG (mulberry32) is a tiny, fully specified PRNG so a regeneration is reproducible
// regardless of the Go version's math/rand.
type vecPRNG uint32

func (m *vecPRNG) next() uint32 {
	*m += 0x6D2B79F5
	z := uint32(*m)
	z = (z ^ (z >> 15)) * (z | 1)
	z ^= z + (z^(z>>7))*(z|61)
	return z ^ (z >> 14)
}

func (m *vecPRNG) intn(n int) int { return int(m.next() % uint32(n)) }

// mutationTokens are the octets/escapes/segments the grammar is sensitive to.
var mutationTokens = []string{
	"/", ".", "..", "./", "../", "%", "%2e", "%2E", "%2f", "%2F", "%40", "@", "@fs", "@doc", "@x", "?", "#", "&", "=",
	"v=1", "v=01", "v=", "cp=x", "token=x", "TOKEN=x", "%74oken=x", ":", ":8443", ":0", ":65536", "[", "]", "[::1]",
	"\\", "%5C", "%5c", "%00", "%0A", "\x01", "\x7f", " ", "%20", "é", "%C3%A9", "%c3", "%C0%AF", "%FF", "%ED%A0%80",
	"K", "%E2%84%AA", "ı", "İ", "🙂", "文", "+", "%2B", ";", "h=" + strings.Repeat("d", 64), "h=D",
	"sp=team", "sp=other", "e=1", "e=0", "k=k2", "k=", "s=" + strings.Repeat("c", 64), "A", "z", "0", "-", "_", "~",
	"acp://", "ACP://", "acp+http://", "u@", "u:p@", "%zz", "\x00", "team", "Team", "x.md", "docs/", "//", "%25", "%2525",
}

// urlTokens add what matters to an https URL that feeds an address (a minted
// blob URL, a discovery endpoint, a profile's server): WHATWG-vs-net/url
// differences (tabs, "\\", IDN, IPv4 shorthand, percent-encoded hosts, ";", "+").
var urlTokens = append(append([]string(nil), mutationTokens...),
	"https://", "HTTPS://", "http://", "\t", "\n", "%2E", "%41", "xn--bcher-kva", "ü", "0x7f.1", "127.0.0.1", "[FE80::1]",
	"/v1", "/v1/cap/blob/", "#frag", "#%zz", "\u212aey", "to\u212aen=x", "pa\u017fsword=x", ":08443", "k=k+2", "k=%FF")

func mutate(r *vecPRNG, s string, tokens []string) string {
	rs := []rune(s)
	tok := []rune(tokens[r.intn(len(tokens))])
	// Mostly mutate AFTER the scheme so most inputs reach the authority/path/query
	// rules instead of all failing the scheme check.
	lo := 0
	for _, prefix := range []string{"acp://", "https://"} {
		if strings.HasPrefix(s, prefix) && r.intn(10) != 0 {
			lo = len(prefix)
		}
	}
	pos := func(extra int) int { return lo + r.intn(len(rs)-lo+extra) }
	switch r.intn(6) {
	case 0: // insert a token
		i := pos(1)
		rs = append(rs[:i:i], append(tok, rs[i:]...)...)
	case 1: // replace one rune with a token
		if len(rs) > lo {
			i := pos(0)
			rs = append(rs[:i:i], append(tok, rs[i+1:]...)...)
		}
	case 2: // delete a span
		if len(rs) > lo {
			i := pos(0)
			j := i + 1 + r.intn(4)
			if j > len(rs) {
				j = len(rs)
			}
			rs = append(rs[:i:i], rs[j:]...)
		}
	case 3: // truncate
		if len(rs) > lo {
			rs = rs[:pos(0)]
		}
	case 4: // ASCII case flip of one letter
		if len(rs) > lo {
			i := pos(0)
			c := rs[i]
			switch {
			case c >= 'a' && c <= 'z':
				rs[i] = c - 32
			case c >= 'A' && c <= 'Z':
				rs[i] = c + 32
			}
		}
	case 5: // append a token (often lands in the query/fragment)
		rs = append(rs, tok...)
	}
	return string(rs)
}

// generateInputs derives n distinct inputs from the vector corpus by 1–3 seeded mutations each.
func generateInputs(vf *vectorFile, n int) []string {
	var seeds []string
	for _, v := range vf.Vectors {
		if v["illFormed"] == true {
			continue // its escapes are notation, not an input (and generated inputs stay well-formed JSON text)
		}
		if s, ok := v["input"].(string); ok && (v["op"] == "parse" || v["op"] == "discovery_address" || v["op"] == "lookup") {
			seeds = append(seeds, s)
		}
		for _, k := range []string{"sameAs", "differentFrom"} {
			if l, ok := v[k].([]any); ok {
				for _, s := range l {
					seeds = append(seeds, s.(string))
				}
			}
		}
	}
	r := vecPRNG(genSeed)
	seen := map[string]bool{}
	for _, s := range seeds {
		seen[s] = true // vectors are already cases of their own
	}
	var out []string
	for len(out) < n {
		s := seeds[r.intn(len(seeds))]
		for k := 1 + r.intn(3); k > 0; k-- {
			s = mutate(&r, s, mutationTokens)
		}
		if seen[s] || len(s) > 512 {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func buildDifferential(vf *vectorFile) *diffFile {
	df := &diffFile{
		About: "GENERATED by `ACPURI_UPDATE_DIFFERENTIAL=1 go test ./pkg/acpuri -run TestDifferentialGolden` from acpuri-vectors.json. " +
			"Each case's out is the Go resolver's full normalized result; sdk/ts/test/uri-vectors.test.mjs requires the TS SDK to produce " +
			"exactly the same. Do not edit by hand.",
		Generated: generatedInputs,
		Seed:      genSeed,
	}
	for _, v := range vf.Vectors {
		c := caseOf(v)
		df.Cases = append(df.Cases, diffCase{Case: c, Out: runCase(c)})
	}
	for i, s := range generateInputs(vf, generatedInputs) {
		c := map[string]any{"id": fmt.Sprintf("generated/%04d", i), "op": "generated", "input": s}
		df.Cases = append(df.Cases, diffCase{Case: c, Out: runCase(c)})
	}
	// Generated https URLs through the three ops that read one: the shared
	// arguments of each op's vectors, with the URL argument mutated.
	r := vecPRNG(genSeed + 1)
	for _, g := range []struct{ op, field string }{{"link", "input"}, {"discovery_document", "endpoint"}, {"lookup", "server"}} {
		var bases []map[string]any
		for _, v := range vf.Vectors {
			if v["op"] == g.op && v["illFormed"] != true {
				bases = append(bases, caseOf(v))
			}
		}
		seen := map[string]bool{}
		for i := 0; i < generatedURLInputs; {
			c := jsonValue(bases[r.intn(len(bases))]).(map[string]any)
			var orig string
			switch g.op {
			case "link":
				orig = c["input"].(string)
			case "discovery_document":
				var doc map[string]any
				if json.Unmarshal([]byte(c["input"].(string)), &doc) != nil {
					continue
				}
				e, ok := doc["endpoint"].(string)
				if !ok {
					continue
				}
				orig = e
			case "lookup":
				orig = c["profiles"].([]any)[0].(map[string]any)["server"].(string)
			}
			m := orig
			for k := 1 + r.intn(2); k > 0; k-- {
				m = mutate(&r, m, urlTokens)
			}
			if m == orig || seen[m] || len(m) > 512 {
				continue
			}
			seen[m] = true
			switch g.op {
			case "link":
				c["input"] = m
			case "discovery_document":
				// A minimal document keeps the file small; only the endpoint varies.
				c["input"] = mustJSON(map[string]any{"protocol": "acp/1", "endpoint": m, "capabilities": []string{"acpuri"}})
				c["presented"] = nil
			case "lookup":
				c["profiles"].([]any)[0].(map[string]any)["server"] = m
			}
			c["id"] = fmt.Sprintf("generated-%s/%04d", g.op, i)
			df.Cases = append(df.Cases, diffCase{Case: c, Out: runCase(c)})
			i++
		}
	}
	return df
}

// encodeDifferential writes one case per line so a regeneration diffs readably.
func encodeDifferential(df *diffFile) []byte {
	var b bytes.Buffer
	head, _ := json.Marshal(struct {
		About     string `json:"about"`
		Generated int    `json:"generated"`
		Seed      int    `json:"seed"`
	}{df.About, df.Generated, df.Seed})
	b.Write(head[:len(head)-1])
	b.WriteString(",\n\"cases\": [\n")
	for i, c := range df.Cases {
		var line bytes.Buffer
		enc := json.NewEncoder(&line)
		enc.SetEscapeHTML(false) // keep '&' readable in URIs
		if err := enc.Encode(c); err != nil {
			panic(err)
		}
		b.Write(bytes.TrimSuffix(line.Bytes(), []byte("\n")))
		if i < len(df.Cases)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("]}\n")
	return b.Bytes()
}

// TestDifferentialGolden proves acpuri-differential.json is exactly what the Go
// resolver produces today for every vector (same arguments) and every generated
// input. The TS suite checks the TS SDK against the same file; together they
// make any Go/TS disagreement a test failure on one side.
func TestDifferentialGolden(t *testing.T) {
	vf, dir := loadVectors(t)
	path := filepath.Join(dir, differentialFile)
	if os.Getenv(updateEnv) == "1" {
		if err := os.WriteFile(path, encodeDifferential(buildDifferential(vf)), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v — generate it with %s=1", err, updateEnv)
	}
	var df diffFile
	if err := json.Unmarshal(b, &df); err != nil {
		t.Fatalf("%s: %v", differentialFile, err)
	}
	byID := map[string]diffCase{}
	generated := 0
	for _, c := range df.Cases {
		id := c.Case["id"].(string)
		if _, dup := byID[id]; dup {
			t.Fatalf("duplicate case id %q", id)
		}
		byID[id] = c
		if strings.HasPrefix(id, "generated") {
			generated++
		}
	}
	if generated < generatedInputs+3*generatedURLInputs {
		t.Errorf("only %d generated inputs, want %d", generated, generatedInputs+3*generatedURLInputs)
	}
	stale := 0
	for _, v := range vf.Vectors {
		want := jsonValue(caseOf(v))
		c, ok := byID[v["id"].(string)]
		if !ok || !reflect.DeepEqual(jsonValue(c.Case), want) {
			stale++
			if stale <= 5 {
				t.Errorf("vector %v is missing from or changed since %s — regenerate with %s=1", v["id"], differentialFile, updateEnv)
			}
		}
	}
	if len(byID) != len(vf.Vectors)+generated {
		t.Errorf("%s has %d cases; want %d vectors + %d generated (a removed vector is still in it — regenerate)", differentialFile, len(byID), len(vf.Vectors), generated)
	}
	mismatches := 0
	for _, c := range df.Cases {
		got := jsonValue(runCase(c.Case))
		if !reflect.DeepEqual(got, jsonValue(c.Out)) {
			mismatches++
			if mismatches <= 10 {
				t.Errorf("%v %q: Go now gives %s, file has %s", c.Case["id"], c.Case["input"], mustJSON(got), mustJSON(c.Out))
			}
		}
	}
	if mismatches > 0 {
		t.Errorf("%d case(s) differ from %s — if the Go change is intended, regenerate with %s=1 and run the TS suite", mismatches, differentialFile, updateEnv)
	}
}

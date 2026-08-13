package wire_test

import (
	"encoding/json"
	"reflect"
	"testing"

	intwire "github.com/ab0t-com/acp/sdk/go/internal/wire"
	"github.com/ab0t-com/acp/sdk/go/pkg/wire"
)

// Compile-time guards that the public types remain ALIASES of the internal
// canonical definitions (ACP/EXT-23 §3.2 "one source of truth"). If any public
// type were ever redefined as a distinct struct (a copy), these assignments
// would fail to compile — a copy is not assignable to its internal twin.
var (
	_ intwire.Event             = wire.Event{}
	_ wire.Event                = intwire.Event{}
	_ intwire.Message           = wire.Message{}
	_ intwire.Lease             = wire.Lease{}
	_ intwire.Manifest          = wire.Manifest{}
	_ intwire.ManifestEntry     = wire.ManifestEntry{}
	_ intwire.Change            = wire.Change{}
	_ intwire.CommitRequest     = wire.CommitRequest{}
	_ intwire.Agent             = wire.Agent{}
	_ intwire.ErrorResponse     = wire.ErrorResponse{}
	_ intwire.AwarenessSet      = wire.AwarenessSet{}
	_ intwire.AwarenessEntry    = wire.AwarenessEntry{}
	_ intwire.AwarenessSnapshot = wire.AwarenessSnapshot{}
	_ intwire.AwarenessDelta    = wire.AwarenessDelta{}
	_ intwire.AwarenessFrame    = wire.AwarenessFrame{}
)

// TestAliasIdentity confirms at runtime that each public type is the identical
// reflect.Type as its internal twin (a second, stronger statement of the
// compile-time guards above).
func TestAliasIdentity(t *testing.T) {
	cases := []struct {
		name          string
		pub, internal any
	}{
		{"Event", wire.Event{}, intwire.Event{}},
		{"Message", wire.Message{}, intwire.Message{}},
		{"Lease", wire.Lease{}, intwire.Lease{}},
		{"Manifest", wire.Manifest{}, intwire.Manifest{}},
		{"ManifestEntry", wire.ManifestEntry{}, intwire.ManifestEntry{}},
		{"Change", wire.Change{}, intwire.Change{}},
		{"CommitRequest", wire.CommitRequest{}, intwire.CommitRequest{}},
		{"Agent", wire.Agent{}, intwire.Agent{}},
		{"AwarenessEntry", wire.AwarenessEntry{}, intwire.AwarenessEntry{}},
		{"AwarenessSnapshot", wire.AwarenessSnapshot{}, intwire.AwarenessSnapshot{}},
		{"AwarenessDelta", wire.AwarenessDelta{}, intwire.AwarenessDelta{}},
	}
	for _, c := range cases {
		if got, want := reflect.TypeOf(c.pub), reflect.TypeOf(c.internal); got != want {
			t.Errorf("%s: public %v is not the internal type %v (not one source of truth)", c.name, got, want)
		}
	}
}

// TestConstantsMatch confirms the re-exported constants equal their canonical
// values (a client relying on them gets the real wire vocabulary).
func TestConstantsMatch(t *testing.T) {
	if wire.ProtocolVersion != intwire.ProtocolVersion {
		t.Errorf("ProtocolVersion %q != %q", wire.ProtocolVersion, intwire.ProtocolVersion)
	}
	if wire.HeaderAgentID != intwire.HeaderAgentID ||
		wire.HeaderProtocol != intwire.HeaderProtocol ||
		wire.HeaderSpace != intwire.HeaderSpace ||
		wire.HeaderSession != intwire.HeaderSession ||
		wire.DefaultSpace != intwire.DefaultSpace ||
		wire.UnlabeledPattern != intwire.UnlabeledPattern {
		t.Error("a re-exported header/constant does not match its canonical value")
	}
}

// TestJSONRoundTrip confirms a value built with the PUBLIC type marshals to the
// same wire JSON the daemon speaks and decodes back into the INTERNAL type
// unchanged — the actual client<->daemon compatibility the SDK depends on.
func TestJSONRoundTrip(t *testing.T) {
	pub := wire.CommitRequest{
		BaseVersion: 7,
		Actor:       "builder",
		Changes:     []wire.Change{{Path: "a.md", Hash: "deadbeef", Size: 5}},
		Note:        "n",
	}
	b, err := json.Marshal(pub)
	if err != nil {
		t.Fatal(err)
	}
	var in intwire.CommitRequest
	if err := json.Unmarshal(b, &in); err != nil {
		t.Fatal(err)
	}
	if in.BaseVersion != 7 || in.Actor != "builder" || len(in.Changes) != 1 || in.Changes[0].Path != "a.md" {
		t.Fatalf("round-trip mismatch: %+v", in)
	}
}

// TestMatchPatternReExport confirms the re-exported matcher behaves identically
// to the canonical one (client- and server-side filtering can never diverge).
func TestMatchPatternReExport(t *testing.T) {
	cases := []struct{ pat, s string }{
		{"build.*", "build.done"}, {"build.*", "deploy.done"}, {"exact", "exact"}, {"exact", "other"},
	}
	for _, c := range cases {
		if wire.MatchPattern(c.pat, c.s) != intwire.MatchPattern(c.pat, c.s) {
			t.Errorf("MatchPattern(%q,%q) diverged from canonical", c.pat, c.s)
		}
	}
}

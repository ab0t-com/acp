//go:build security_regression

package client

import (
	"os"
	"strings"
	"testing"
)

// TestVulnW2_SDKIdemKeyHygieneDocumented — S-F1-W2-7 + S-F2-W2-6 (HIGH/MED). F-INV-36
// shipped AppendIdem/LogBatchIdem/SendIdem as the sanctioned retry convention but with
// no guidance that a key must not be reused across SHAPES/operations (S-F2-W2-5) nor
// be unguessable in a shared-token deployment (S-F1-12). The fix that makes idempotency
// easy must also warn against the footgun it invites. This asserts each method carries
// the key-hygiene warning.
func TestVulnW2_SDKIdemKeyHygieneDocumented(t *testing.T) {
	src, err := os.ReadFile("client.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	for _, m := range []string{"func (c *Client) AppendIdem", "func (c *Client) LogBatchIdem", "func (c *Client) SendIdem"} {
		i := strings.Index(s, m)
		if i < 0 {
			t.Fatalf("could not find %s", m)
		}
		// The doc block is the ~800 bytes preceding the func signature.
		start := i - 900
		if start < 0 {
			start = 0
		}
		doc := s[start:i]
		if !strings.Contains(doc, "KEY HYGIENE") {
			t.Fatalf("%s has no KEY HYGIENE warning: the SDK's first-class idempotency methods invite key reuse across "+
				"shapes/operations (silent write-drop) and predictable keys in shared-token mode (co-tenant substitution) "+
				"with no caveat.", m)
		}
	}
}

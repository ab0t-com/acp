//go:build security_regression

package client

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// PROVES F-INV-42 (pkg/client builds the query via raw string concat, no url.QueryEscape). RED until fixed.
// Evidence: pkg/client/client.go:606 (`"/v1/crdt/ops?doc="+doc+"&from="+...`) and :686 (json twin); a co-tenant's
// doc name containing `&from=` injects/overrides the caller's from= offset.
func TestVuln_FINV42_PullCRDTOpsEscapesDocName(t *testing.T) {
	var gotDoc, gotFrom string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotDoc = r.URL.Query().Get("doc")
		gotFrom = r.URL.Query().Get("from")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ops":[],"total":0,"epoch":0}`))
	}))
	defer srv.Close()
	c, err := New(srv.URL, "tok", "A", "", true) // insecure: httptest is plain http (no cert to pin)
	if err != nil {
		t.Fatal(err)
	}
	// A malicious doc name whose bytes are a query-string injection payload.
	c.PullCRDTOps("x&from=999999", 0)
	if gotDoc != "x&from=999999" || gotFrom != "0" {
		t.Fatalf("PullCRDTOps did not url-escape the doc name: the server saw doc=%q from=%q (a co-tenant's doc name "+
			"`x&from=999999` injected the from= parameter, hijacking the caller's read offset). Expected doc=`x&from=999999`, "+
			"from=`0`. Use url.QueryEscape / url.Values.", gotDoc, gotFrom)
	}
}

// PROVES F-INV-36 (the reference SDK never sets/generates an Idempotency-Key). RED until fixed.
// Evidence: grep "Idempotency" pkg/client = 0 hits; Append/LogBatch/Send give no way to use the server's dedup (T48),
// so a naive retry-on-error/failover double-writes events/mail.
func TestVuln_FINV36_SDKSupportsIdempotencyKey(t *testing.T) {
	src, err := os.ReadFile("client.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "Idempotency") {
		t.Fatalf("pkg/client never sets or generates an Idempotency-Key: Append/LogBatch/Send cannot use the server's " +
			"replicated dedup (T48), so the natural retry-on-error/failover wrapper double-writes events/mail. The SDK " +
			"must send (or auto-generate) an Idempotency-Key for its non-CAS writes.")
	}
}

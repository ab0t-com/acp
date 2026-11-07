package client

// R-C20: a request that meets a connection the server closed while it sat idle in the
// pool. The server is clienttest.ResetOnReuse. Fail-safe: httptest servers on 127.0.0.1 only.

import (
	"context"
	"testing"

	"github.com/ab0t-com/acp/sdk/go/pkg/client/clienttest"
)

func resetRig(t *testing.T, h2, partial bool) (*Client, *clienttest.ResetServer) {
	t.Helper()
	s := clienttest.ResetOnReuse(t, h2, partial, `{"hash":"h","size":9}`)
	c, err := New(s.URL, "tok", "alice", "", true)
	if err != nil {
		t.Fatal(err)
	}
	return c, s
}

func TestResetOnAReusedConnectionIsRetriedOnce(t *testing.T) {
	for _, proto := range []struct {
		name string
		h2   bool
	}{{"http1", false}, {"http2", true}} {
		t.Run(proto.name, func(t *testing.T) {
			t.Run("a read goes once more, on a fresh connection", func(t *testing.T) {
				c, s := resetRig(t, proto.h2, false)
				for i := 0; i < 3; i++ {
					if err := c.do("GET", "/v1/fs/mode", nil, nil); err != nil {
						t.Fatalf("read %d: %v", i, err)
					}
				}
				if s.Resets() == 0 {
					t.Fatal("the rig never reset a reused connection: nothing was tested")
				}
			})
			t.Run("the health probes go once more too", func(t *testing.T) {
				c, s := resetRig(t, proto.h2, false)
				if err := c.do("GET", "/v1/fs/mode", nil, nil); err != nil {
					t.Fatal(err)
				}
				if err := c.Health(); err != nil {
					t.Fatalf("Health: %v", err)
				}
				if _, err := c.SupportsInvites(context.Background()); err != nil {
					t.Fatalf("SupportsInvites: %v", err)
				}
				if s.Resets() == 0 {
					t.Fatal("no reset met: nothing was tested")
				}
			})
			t.Run("a write with an Idempotency-Key goes once more", func(t *testing.T) {
				c, s := resetRig(t, proto.h2, false)
				if err := c.do("GET", "/v1/fs/mode", nil, nil); err != nil {
					t.Fatal(err)
				}
				if err := c.doH("POST", "/v1/mail", map[string]string{"Idempotency-Key": "k1"}, map[string]string{"text": "hi"}, nil); err != nil {
					t.Fatalf("a retry-safe write met a reset and was not sent again: %v", err)
				}
				if s.Resets() != 1 || s.Calls() != 3 {
					t.Fatalf("resets %d calls %d", s.Resets(), s.Calls())
				}
			})
			t.Run("a write that is not retry-safe is sent once", func(t *testing.T) {
				c, s := resetRig(t, proto.h2, false)
				if err := c.do("GET", "/v1/fs/mode", nil, nil); err != nil {
					t.Fatal(err)
				}
				err := c.do("POST", "/v1/mail", map[string]string{"text": "hi"}, nil)
				if err == nil {
					t.Fatal("the reset write reported success")
				}
				if got := s.Calls(); got != 2 {
					t.Fatalf("a write that may have landed was sent again: %d calls", got)
				}
			})
			t.Run("a read that got a response byte is not sent again", func(t *testing.T) {
				c, s := resetRig(t, proto.h2, true)
				if err := c.do("GET", "/v1/fs/mode", nil, nil); err != nil {
					t.Fatal(err)
				}
				_ = c.do("GET", "/v1/fs/mode", nil, nil) // its body is cut: an error or a short read
				if got := s.Calls(); got != 2 {
					t.Fatalf("an answered read was sent again: %d calls", got)
				}
			})
		})
	}
}

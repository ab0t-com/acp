package acpuri

// R-C22: a refused share link reads the same from an older server and from one that adds
// a generic next step (R-S18): the date after "expired", and the link's own next step.

import (
	"strings"
	"testing"
)

func TestLinkRefusalReadsBothServerStyles(t *testing.T) {
	u := &URI{Path: "draft.md", Capability: &Capability{E: 1793700000}}
	for _, c := range []struct {
		status    int
		msg, want string
	}{
		{410, "capability url expired", "capability url expired on 2026-11-03T"},
		{410, "capability url expired (ask for a new one)", "capability url expired on 2026-11-03T"},
		{410, "capability url expired: links are short-lived", "capability url expired on 2026-11-03T"},
		{403, "invalid capability signature", "invalid capability signature: ask whoever"},
		{403, "invalid capability signature (your login's role or scope does not allow this: ask an admin)", "invalid capability signature: ask whoever"},
	} {
		got := linkRefusal(u, c.status, c.msg)
		if !strings.HasPrefix(got, c.want) || !strings.HasSuffix(got, ": ask whoever sent it for a new one (they run: acp share draft.md)") ||
			strings.Contains(got, "ask an admin") || strings.Count(got, "ask") != 1 {
			t.Errorf("%d %q: %q", c.status, c.msg, got)
		}
	}
	if got := linkRefusal(u, 500, "boom (retry)"); got != "boom (retry)" {
		t.Errorf("a 500 keeps the server's words: %q", got)
	}
}

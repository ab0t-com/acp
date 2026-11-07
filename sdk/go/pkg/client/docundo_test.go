package client

// The undo calls for live documents speak the routes in openapi.json, tell a
// server without them from a real refusal, and keep generation/epoch 0 usable.
// FAIL-SAFE: httptest on 127.0.0.1 only.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type undoCall struct{ method, uri, body string }

func undoServer(t *testing.T, status int, reply string) (*Client, *[]undoCall) {
	var calls []undoCall
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		calls = append(calls, undoCall{r.Method, r.URL.RequestURI(), string(b)})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, reply)
	}))
	t.Cleanup(ts.Close)
	return newTestClient(t, ts.URL), &calls
}

func TestDocUndoCallsUseTheDocumentedRoutes(t *testing.T) {
	const trash = `{"trash":[{"doc":"a b.md","gen":0,"deleted_at":"2026-11-02T00:00:00Z","ops":7}]}`
	for name, tc := range map[string]struct {
		reply string
		call  func(c *Client) error
		want  undoCall
	}{
		"delete text": {`{"doc":"a","deleted":true}`, func(c *Client) error { _, e := c.DeleteDoc("a b.md"); return e },
			undoCall{"DELETE", "/v1/crdt/doc?doc=a+b.md", ""}},
		"delete json": {`{"doc":"a","deleted":true}`, func(c *Client) error { _, e := c.DeleteJSONDoc("a"); return e },
			undoCall{"DELETE", "/v1/crdt/json/doc?doc=a", ""}},
		"trash text": {trash, func(c *Client) error { _, e := c.DocTrash(); return e }, undoCall{"GET", "/v1/crdt/trash", ""}},
		"trash json": {trash, func(c *Client) error { _, e := c.JSONDocTrash(); return e }, undoCall{"GET", "/v1/crdt/json/trash", ""}},
		"restore text": {`{"restored":true}`, func(c *Client) error { return c.RestoreDoc("a", 0) },
			undoCall{"POST", "/v1/crdt/trash/restore", `{"doc":"a","gen":0}`}},
		"restore json": {`{"restored":true}`, func(c *Client) error { return c.RestoreJSONDoc("a", 3) },
			undoCall{"POST", "/v1/crdt/json/trash/restore", `{"doc":"a","gen":3}`}},
		"versions": {`{"doc":"a","versions":[]}`, func(c *Client) error { _, e := c.DocVersions("a/b"); return e },
			undoCall{"GET", "/v1/crdt/versions?doc=a%2Fb", ""}},
		"version at epoch 0": {`{"text":"x"}`, func(c *Client) error { _, e := c.DocVersionAt("a", 0, 0); return e },
			undoCall{"GET", "/v1/crdt/versions?doc=a&epoch=0", ""}},
		"version prefix": {`{"text":"x"}`, func(c *Client) error { _, e := c.DocVersionAt("a", 2, 5); return e },
			undoCall{"GET", "/v1/crdt/versions?doc=a&epoch=2&at=5", ""}},
	} {
		t.Run(name, func(t *testing.T) {
			c, calls := undoServer(t, 200, tc.reply)
			if err := tc.call(c); err != nil {
				t.Fatal(err)
			}
			var got *undoCall
			for i := range *calls {
				if !strings.HasPrefix((*calls)[i].uri, "/v1/healthz") {
					got = &(*calls)[i]
				}
			}
			if got == nil {
				t.Fatal("no call reached the server")
			}
			if got.method != tc.want.method || got.uri != tc.want.uri {
				t.Fatalf("got %s %s, want %s %s", got.method, got.uri, tc.want.method, tc.want.uri)
			}
			if tc.want.body != "" {
				var a, b any
				json.Unmarshal([]byte(got.body), &a)
				json.Unmarshal([]byte(tc.want.body), &b)
				ab, _ := json.Marshal(a)
				bb, _ := json.Marshal(b)
				if string(ab) != string(bb) {
					t.Fatalf("body %s, want %s", got.body, tc.want.body)
				}
			}
		})
	}
}

func TestDeleteDocReportsWhatCanComeBack(t *testing.T) {
	c, _ := undoServer(t, 200, `{"doc":"a","deleted":true,"trash":{"doc":"a","gen":4,"deleted_at":"t","expires_at":"u","ops":0}}`)
	e, err := c.DeleteDoc("a")
	if err != nil || e == nil || e.Gen != 4 || e.ExpiresAt != "u" {
		t.Fatalf("trash entry lost: %+v %v", e, err)
	}
	// A space with no history keeps no trash, and an older server says only deleted:true.
	c, _ = undoServer(t, 200, `{"doc":"a","deleted":true}`)
	if e, err := c.DeleteDoc("a"); err != nil || e != nil {
		t.Fatalf("no trash must read as nil: %+v %v", e, err)
	}
}

func TestDocUndoEmptyListsAreEmptyNotNil(t *testing.T) {
	c, _ := undoServer(t, 200, `{"trash":null,"versions":null}`)
	if got, err := c.DocTrash(); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("trash: %v %v", got, err)
	}
	if got, err := c.DocVersions("a"); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("versions: %v %v", got, err)
	}
}

// R-C22: a server without these routes answers in one of two styles, and both read as
// "no route": an older server's 404 with no code, and unknown_route from v0.2.6 (R-S18).
func TestDocUndoOldServerIsTyped(t *testing.T) {
	for style, body := range map[string]string{
		"older server, no code": `404 page not found`,
		"unknown_route":         `{"error":"not found: GET /v1/crdt/trash is not a route of this server (see /openapi.json)","code":"unknown_route"}`,
	} {
		c, _ := undoServer(t, 404, body)
		for name, call := range map[string]func() error{
			"trash":    func() error { _, e := c.DocTrash(); return e },
			"restore":  func() error { return c.RestoreDoc("a", 1) },
			"versions": func() error { _, e := c.DocVersions("a"); return e },
			"version":  func() error { _, e := c.DocVersionAt("a", 1, 0); return e },
		} {
			if err := call(); !errors.Is(err, ErrNoDocUndo) {
				t.Errorf("%s, %s: want ErrNoDocUndo, got %v", style, name, err)
			}
		}
	}
}

// RouteMissing is a 404 with no code or unknown_route, and nothing else: a coded 404 is
// the route's own answer, and keeps the server's sentence (its next step).
func TestRouteMissingReadsBothServerStyles(t *testing.T) {
	for _, c := range []struct {
		status  int
		body    string
		missing bool
	}{
		{404, `404 page not found`, true},
		{404, `{"error":"not found"}`, true},
		{404, `{"error":"not found: see /openapi.json","code":"unknown_route"}`, true},
		{404, `{"error":"no document d: acp docs list shows them","code":"doc_not_found"}`, false},
		{404, `{"error":"workspace x does not exist yet","code":"space_not_found"}`, false},
		{405, `{"error":"method not allowed"}`, false},
		{410, `{"error":"gone"}`, false},
	} {
		err := apiErrorFrom(c.status, []byte(c.body))
		if got := IsRouteMissing(err); got != c.missing {
			t.Errorf("%d %s: route missing %v, want %v", c.status, c.body, got, c.missing)
		}
		var body struct{ Error string }
		if json.Unmarshal([]byte(c.body), &body) == nil && !c.missing && !strings.Contains(err.Error(), body.Error) {
			t.Errorf("%s: the server's sentence is lost: %v", c.body, err)
		}
	}
	if IsRouteMissing(errors.New("dial tcp: refused")) || IsRouteMissing(nil) {
		t.Fatal("no answer is not a missing route")
	}
}

// A real refusal keeps its code: it must never read as "server too old".
func TestDocUndoRealRefusalKeepsItsCode(t *testing.T) {
	for _, code := range []string{"trash_not_found", "version_not_found"} {
		c, _ := undoServer(t, 404, `{"error":"none","code":"`+code+`"}`)
		var err error
		if code == "trash_not_found" {
			err = c.RestoreDoc("a", 1)
		} else {
			_, err = c.DocVersionAt("a", 1, 0)
		}
		var ae *APIError
		if errors.Is(err, ErrNoDocUndo) || !errors.As(err, &ae) || ae.Code() != code {
			t.Fatalf("%s: %v", code, err)
		}
	}
	c, _ := undoServer(t, 409, `{"error":"exists","code":"doc_exists"}`)
	var ae *APIError
	if err := c.RestoreDoc("a", 1); !errors.As(err, &ae) || ae.Code() != "doc_exists" || !ae.Conflict() {
		t.Fatalf("doc_exists: %v", err)
	}
}

func TestDocUndoRefusesBadArgumentsBeforeSending(t *testing.T) {
	c, calls := undoServer(t, 200, `{}`)
	if _, err := c.DeleteDoc(""); err == nil {
		t.Fatal("delete with no name")
	}
	if err := c.RestoreDoc("", 1); err == nil {
		t.Fatal("restore with no name")
	}
	if err := c.RestoreDoc("a", -1); err == nil {
		t.Fatal("restore with a negative generation")
	}
	if _, err := c.DocVersionAt("a", -1, 0); err == nil {
		t.Fatal("negative epoch")
	}
	if len(*calls) != 0 {
		t.Fatalf("nothing may be sent: %+v", *calls)
	}
}

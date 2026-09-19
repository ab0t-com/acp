package client

// Realtime-tree (ext-28) + filesystem-mode (ext-31) SDK surface, driven
// against the in-memory contract fake (clienttest.FSDaemon — a 127.0.0.1
// httptest server, closed by t.Cleanup; never a real coordd, :8443 or ~/.acp).
// Each test pins the CLIENT side of the frozen wire: the path + method each
// method hits, the typed decode of the daemon's bodies, and the refusal
// taxonomy (stable "code" -> typed helper) a CLI/bridge decides on.

import (
	"errors"
	"strings"
	"testing"

	"github.com/ab0t-com/acp/sdk/go/pkg/client/clienttest"
	"github.com/ab0t-com/acp/sdk/go/pkg/wire"
)

func fsClient(t *testing.T, d *clienttest.FSDaemon, token string) *Client {
	t.Helper()
	c, err := New(d.URL(), token, "tester-"+token, "", true)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func apiErr(t *testing.T, err error) *APIError {
	t.Helper()
	var ae *APIError
	if !errors.As(err, &ae) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	return ae
}

func TestFSModeReadAndFlipConfirmContract(t *testing.T) {
	d := clienttest.NewFSDaemon(t)
	admin, writer, reader := fsClient(t, d, "admin"), fsClient(t, d, "writer"), fsClient(t, d, "reader")

	// A new space is CAS; the realtime surfaces answer 409 wrong_fs_mode (no shadow state).
	if m, err := reader.FSMode(); err != nil || m != wire.FSModeCAS {
		t.Fatalf("FSMode on a new space: %q %v", m, err)
	}
	if _, err := reader.FSTree(); err == nil || !apiErr(t, err).WrongFSMode() {
		t.Fatalf("FSTree on a CAS space must be 409 wrong_fs_mode, got %v", err)
	}
	// The flip is admin-only (§5.3).
	if _, err := writer.SetFSMode(wire.FSModeRealtime, nil); err == nil || apiErr(t, err).Status != 403 {
		t.Fatalf("a writer must not flip: %v", err)
	}
	// An EMPTY region flips with no confirm and pins nothing.
	res, err := admin.SetFSMode(wire.FSModeRealtime, nil)
	if err != nil || res.FSMode != wire.FSModeRealtime || res.NoOp || res.Checkpoint != "" {
		t.Fatalf("empty flip: %+v %v", res, err)
	}
	// Same mode again: 200 no_op:true (R9 — never mistaken for "my flip applied").
	if res, err := admin.SetFSMode(wire.FSModeRealtime, nil); err != nil || !res.NoOp {
		t.Fatalf("same-mode flip must be marked no_op: %+v %v", res, err)
	}
	// Populate, then flip back: NON-empty needs the exact-count confirm.
	if _, err := writer.FSMkdir("docs"); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.FSCreate("docs/a.md", "h-a", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.FSCreate("docs/b.md", "h-b", ""); err != nil {
		t.Fatal(err)
	}
	_, err = admin.SetFSMode(wire.FSModeCAS, nil)
	ae := apiErr(t, err)
	if !ae.FSModeConfirmRequired() || ae.Code() != "fs_mode_confirm_required" {
		t.Fatalf("non-empty flip without confirm must be 428 fs_mode_confirm_required, got %v", err)
	}
	demand, ok := ae.FSModeDemand()
	if !ok || demand.Entries != 2 || demand.From != "realtime" || demand.To != "cas" || demand.Region != "/" {
		t.Fatalf("demand: %+v ok=%v", demand, ok)
	}
	// A wrong echo is refused 428 fs_mode_confirm_mismatch — no state change.
	_, err = admin.SetFSMode(wire.FSModeCAS, &wire.FSModeConfirm{Region: "/", From: "realtime", To: "cas", Entries: 1})
	if ae := apiErr(t, err); !ae.FSModeConfirmMismatch() {
		t.Fatalf("stale confirm must be 428 mismatch, got %v", err)
	}
	if m, _ := admin.FSMode(); m != wire.FSModeRealtime {
		t.Fatalf("a refused flip must not change the mode, got %q", m)
	}
	// The exact echo lands, with the mandatory pre-flip pin named.
	res, err = admin.SetFSMode(wire.FSModeCAS, &wire.FSModeConfirm{Region: "/", From: demand.From, To: demand.To, Entries: demand.Entries})
	if err != nil || res.FSMode != wire.FSModeCAS || res.Checkpoint == "" || res.Version == 0 {
		t.Fatalf("exact confirm must flip and pin: %+v %v", res, err)
	}
	// Bad mode string never reaches the wire.
	before := d.Calls["PUT /v1/space/config"]
	if _, err := admin.SetFSMode("fast", nil); err == nil || d.Calls["PUT /v1/space/config"] != before {
		t.Fatalf("an invalid mode must be refused client-side: %v", err)
	}
}

func TestFSOpsLifecycleAndBulkWall(t *testing.T) {
	d := clienttest.NewFSDaemon(t)
	d.SeedRealtime([]string{"src"}, map[string]string{"src/main.go": "h-main"})
	writer := fsClient(t, d, "writer")

	// PutBlob + create; a batch is ordered (mkdir then create inside it).
	hash, _, err := writer.PutBlob(strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := writer.FSOps([]wire.FSOp{{Op: "mkdir", Path: "docs"}, {Op: "create", Path: "docs/readme.md", Blob: hash}})
	if err != nil || res.Applied != 2 || len(res.Outstamped) != 0 {
		t.Fatalf("ordered batch: %+v %v", res, err)
	}
	tree, err := writer.FSTree()
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]wire.FSNode{}
	for _, n := range tree {
		paths[n.Path] = n
	}
	if n, ok := paths["docs/readme.md"]; !ok || n.Blob != hash || n.Size != 5 || n.Folder {
		t.Fatalf("created node missing/wrong: %+v (tree %+v)", n, tree)
	}
	// Move re-prefixes the subtree; ids are stable.
	id := paths["docs/readme.md"].NodeID
	if _, err := writer.FSMove("docs", "src/docs"); err != nil {
		t.Fatal(err)
	}
	tree, _ = writer.FSTree()
	found := false
	for _, n := range tree {
		if n.Path == "src/docs/readme.md" && n.NodeID == id {
			found = true
		}
	}
	if !found {
		t.Fatalf("move must carry the subtree with stable ids: %+v", tree)
	}
	// Unresolvable paths are 400 and the whole batch is refused atomically.
	if _, err := writer.FSMove("nope", "x"); err == nil || apiErr(t, err).Status != 400 {
		t.Fatalf("move of a missing path must be 400: %v", err)
	}
	// Delete = trash (recoverable); trash lists the ROOT by its origin; restore brings it back.
	if _, err := writer.FSDelete("src/docs"); err != nil {
		t.Fatal(err)
	}
	trash, err := writer.FSTrash()
	if err != nil || len(trash) != 1 || trash[0].Origin != "src/docs" || !trash[0].Folder {
		t.Fatalf("trash after a folder delete: %+v %v", trash, err)
	}
	if _, err := writer.FSRestore(trash[0].NodeID, "restored-docs"); err != nil {
		t.Fatal(err)
	}
	tree, _ = writer.FSTree()
	found = false
	for _, n := range tree {
		if n.Path == "restored-docs/readme.md" && n.NodeID == id {
			found = true
		}
	}
	if !found {
		t.Fatalf("restore to a new path must return the subtree: %+v", tree)
	}
	if tr, _ := writer.FSTrash(); len(tr) != 0 {
		t.Fatalf("restored root must leave the trash: %+v", tr)
	}
	// A restore of a non-restorable id is missing-shaped 400.
	if _, err := writer.FSRestore("999-fake", ""); err == nil || apiErr(t, err).Status != 400 {
		t.Fatalf("restore of an unknown id must be 400: %v", err)
	}

	// ext-34 wall: a single DELETE batch at the bulk minimum is 403 bulk_denied for
	// a plain writer — NOT confirmable, and the SDK never retries it.
	var many []string
	for i := 0; i < int(d.DelMin); i++ {
		p := "bulk" + string(rune('a'+i)) + ".txt"
		if _, err := writer.FSCreate(p, "h", ""); err != nil {
			t.Fatal(err)
		}
		many = append(many, p)
	}
	before := d.Calls["POST /v1/fs/ops"]
	_, err = writer.FSDelete(many...)
	ae := apiErr(t, err)
	if !ae.BulkDenied() || ae.Code() != "bulk_denied" {
		t.Fatalf("a bulk delete batch must be 403 bulk_denied for a plain writer, got %v", err)
	}
	if d.Calls["POST /v1/fs/ops"] != before+1 {
		t.Fatalf("bulk_denied must never be retried (calls %d -> %d)", before, d.Calls["POST /v1/fs/ops"])
	}
	if tree, _ := writer.FSTree(); len(tree) < len(many) {
		t.Fatalf("a refused batch must change nothing: %d nodes", len(tree))
	}
	// A grant that allows bulk passes the same batch.
	if res, err := fsClient(t, d, "bulkwriter").FSDelete(many...); err != nil || res.Applied != len(many) {
		t.Fatalf("bulk-allowed grant: %+v %v", res, err)
	}
	// Outstamped ops are REPORTED, never a silent 200.
	d.Outstamp = true
	res, err = writer.FSMkdir("late")
	if err != nil || len(res.Outstamped) != 1 || res.Warning == "" {
		t.Fatalf("outstamped ops must surface: %+v %v", res, err)
	}
	// Client-side argument checks never reach the wire.
	before = d.Calls["POST /v1/fs/ops"]
	if _, err := writer.FSOps(nil); err == nil {
		t.Fatal("empty batch must be refused")
	}
	if _, err := writer.FSCreate("x", "", ""); err == nil {
		t.Fatal("create needs blob or doc")
	}
	if _, err := writer.FSCreate("x", "h", "d"); err == nil {
		t.Fatal("create takes exactly one of blob/doc")
	}
	if d.Calls["POST /v1/fs/ops"] != before {
		t.Fatal("client-side refusals must not hit the daemon")
	}
	// A reader cannot write.
	if _, err := fsClient(t, d, "reader").FSMkdir("r"); err == nil || apiErr(t, err).Status != 403 {
		t.Fatalf("reader mkdir must be 403: %v", err)
	}
}

func TestFSTrashAtCheckpointAndRestoreToCAS(t *testing.T) {
	d := clienttest.NewFSDaemon(t)
	d.SeedRealtime([]string{"keep"}, map[string]string{"keep/k.md": "h-k", "old/x.md": "h-x", "old/sub/y.md": "h-y"})
	admin, writer := fsClient(t, d, "admin"), fsClient(t, d, "writer")
	if _, err := writer.FSDelete("old"); err != nil {
		t.Fatal(err)
	}
	// Flip realtime->cas with the exact confirm (1 live file, 1 trashed root).
	_, err := admin.SetFSMode(wire.FSModeCAS, nil)
	demand, _ := apiErr(t, err).FSModeDemand()
	if demand.Entries != 1 || demand.Trashed != 1 {
		t.Fatalf("demand %+v", demand)
	}
	res, err := admin.SetFSMode(wire.FSModeCAS, &wire.FSModeConfirm{Region: "/", From: "realtime", To: "cas", Entries: 1})
	if err != nil || res.Checkpoint == "" {
		t.Fatalf("flip: %+v %v", res, err)
	}
	// The realtime trash door is shut on a CAS space...
	if _, err := writer.FSTrash(); err == nil || !apiErr(t, err).WrongFSMode() {
		t.Fatalf("FSTrash on a CAS space must be 409 wrong_fs_mode: %v", err)
	}
	// ...the checkpoint door lists the pre-flip trashed FILES recursively (P2-4).
	trash, err := writer.FSTrashAtCheckpoint(res.Checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	origins := map[string]string{}
	for _, te := range trash {
		if !te.Folder {
			origins[te.Origin] = te.NodeID
		}
	}
	if len(origins) != 2 || origins["old/x.md"] == "" || origins["old/sub/y.md"] == "" {
		t.Fatalf("recursive pre-flip trash listing: %+v", trash)
	}
	if _, err := writer.FSTrashAtCheckpoint("auto/fsmode-realtime-to-cas-v999"); err == nil || apiErr(t, err).Status != 400 {
		t.Fatalf("unknown checkpoint must be 400: %v", err)
	}
	if _, err := writer.FSTrashAtCheckpoint(""); err == nil {
		t.Fatal("empty checkpoint must be refused client-side")
	}
	// Recover one file into the CAS manifest as a forward commit.
	rr, err := writer.FSTrashRestoreToCAS(origins["old/sub/y.md"], res.Checkpoint)
	if err != nil || rr.Restored != "old/sub/y.md" || rr.Version <= res.Version {
		t.Fatalf("restore to CAS: %+v %v", rr, err)
	}
	man, _ := writer.Manifest()
	if e, ok := man.Entries["old/sub/y.md"]; !ok || e.Hash != "h-y" {
		t.Fatalf("restored file must be in the manifest: %+v", man.Entries)
	}
	if _, err := writer.FSTrashRestoreToCAS("nope", res.Checkpoint); err == nil || apiErr(t, err).Status != 400 {
		t.Fatalf("unknown node must be 400: %v", err)
	}
}

func TestFSPruneIsAdminOnly(t *testing.T) {
	d := clienttest.NewFSDaemon(t)
	d.SeedRealtime(nil, map[string]string{"a.md": "h"})
	writer, admin := fsClient(t, d, "writer"), fsClient(t, d, "admin")
	if _, err := writer.FSDelete("a.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.FSPrune(); err == nil || apiErr(t, err).Status != 403 {
		t.Fatalf("prune must be admin-only: %v", err)
	}
	pr, err := admin.FSPrune()
	if err != nil || pr.Evicted != 1 || pr.IntegrityAnomalies != 0 {
		t.Fatalf("prune: %+v %v", pr, err)
	}
	if tr, _ := writer.FSTrash(); len(tr) != 0 {
		t.Fatalf("evicted record must be gone: %+v", tr)
	}
}

// TestFSCapabilityGate: an older daemon without crdtfs/fsmode yields ONE clear
// error before any /v1/fs request is sent.
func TestFSCapabilityGate(t *testing.T) {
	d := clienttest.NewFSDaemon(t)
	c := fsClient(t, d, "writer")
	c.caps = map[string]bool{"channels": true} // simulate a pre-ext-28 healthz
	if _, err := c.FSTree(); err == nil || !strings.Contains(err.Error(), "crdtfs") {
		t.Fatalf("missing crdtfs must be a clear client-side error: %v", err)
	}
	if _, err := c.FSMode(); err == nil || !strings.Contains(err.Error(), "fsmode") {
		t.Fatalf("missing fsmode must be a clear client-side error: %v", err)
	}
	if d.Calls["GET /v1/fs/tree"] != 0 || d.Calls["GET /v1/space/config"] != 0 {
		t.Fatal("a capability-refused call must not reach the daemon")
	}
}

package wire_test

// Realtime-tree (ext-28) + filesystem-mode (ext-31) public wire types: the
// field-name pins for the request/response rows the daemon keeps unexported
// (cmd/coordd/fs.go fsOpReq / the fsmode confirm block). The pins are the
// client's side of the frozen wire: if a field name here drifts from what the
// daemon decodes, the op is silently ignored — so they are asserted byte-for-byte
// against the daemon's documented JSON keys.
//
// FSNode/FSTrashEntry are STANDALONE client DTOs (they no longer alias
// internal/crdtfs.NodeView/TrashEntry — an alias made pkg/wire import the tree
// engine, which broke the carved-out sdk/go module). Their byte-identity with the
// daemon's canonical types is pinned at the daemon boundary in
// cmd/coordd/fs_wire_identity_test.go, the only side that imports both; pkg/wire
// (and thus the carve-out) stays free of internal/crdtfs.

import (
	"encoding/json"
	"testing"

	"github.com/ab0t-com/acp/sdk/go/pkg/wire"
)

// TestFSOpWireShape pins FSOp's JSON keys to the daemon's fsOpReq keys
// (op/path/to/node/blob/doc) and confirms empty fields are OMITTED (the daemon
// treats a present-but-empty "blob" and an absent one identically today, but a
// spare key is noise a future strict decoder could refuse).
func TestFSOpWireShape(t *testing.T) {
	b, err := json.Marshal(wire.FSOp{Op: "create", Path: "a/b.md", Blob: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"op":"create","path":"a/b.md","blob":"h1"}`; got != want {
		t.Fatalf("FSOp wire shape drifted: got %s want %s", got, want)
	}
	b, _ = json.Marshal(wire.FSOp{Op: "restore", Node: "7-alice", To: "docs/x.md"})
	if got, want := string(b), `{"op":"restore","to":"docs/x.md","node":"7-alice"}`; got != want {
		t.Fatalf("FSOp restore shape drifted: got %s want %s", got, want)
	}
	var m map[string]any
	json.Unmarshal(b, &m)
	for _, k := range []string{"path", "blob", "doc"} {
		if _, present := m[k]; present {
			t.Errorf("empty %q must be omitted", k)
		}
	}
}

// TestFSModeConfirmWireShape pins the confirm echo to the daemon's
// fsFlipConfirm keys (region/from/to/entries) — every key present, never
// omitted, because the daemon compares all four.
func TestFSModeConfirmWireShape(t *testing.T) {
	b, _ := json.Marshal(wire.FSModeConfirm{Region: "/", From: "cas", To: "realtime", Entries: 0})
	if got, want := string(b), `{"region":"/","from":"cas","to":"realtime","entries":0}`; got != want {
		t.Fatalf("FSModeConfirm wire shape drifted: got %s want %s", got, want)
	}
}

// TestFSResultsDecodeDaemonBodies decodes the daemon's documented 200 bodies
// (fs.go / fsmode.go renderers) into the public result types.
func TestFSResultsDecodeDaemonBodies(t *testing.T) {
	var ops wire.FSOpsResult
	if err := json.Unmarshal([]byte(`{"applied":2,"outstamped":["rename g.md (1-a)"],"warning":"w"}`), &ops); err != nil {
		t.Fatal(err)
	}
	if ops.Applied != 2 || len(ops.Outstamped) != 1 || ops.Warning == "" {
		t.Fatalf("FSOpsResult decode: %+v", ops)
	}
	var mode wire.FSModeResult
	json.Unmarshal([]byte(`{"fs_mode":"cas","checkpoint":"auto/fsmode-realtime-to-cas-v3","version":3}`), &mode)
	if mode.FSMode != "cas" || mode.Version != 3 || mode.Checkpoint == "" || mode.NoOp {
		t.Fatalf("FSModeResult decode: %+v", mode)
	}
	var demand struct {
		Code    string            `json:"code"`
		Confirm wire.FSModeDemand `json:"confirm"`
	}
	json.Unmarshal([]byte(`{"error":"x","code":"fs_mode_confirm_required","confirm":{"region":"/","from":"cas","to":"realtime","entries":4,"from_version":9,"leases_held":1}}`), &demand)
	if demand.Confirm.Entries != 4 || demand.Confirm.FromVersion != 9 || demand.Confirm.LeasesHeld != 1 || demand.Confirm.To != "realtime" {
		t.Fatalf("FSModeDemand decode: %+v", demand)
	}
	var pr wire.FSPruneResult
	json.Unmarshal([]byte(`{"evicted":3,"oldest_trashed_at":17,"history_bytes_over_cap":true,"integrity_anomalies":0}`), &pr)
	if pr.Evicted != 3 || pr.OldestTrashedAt != 17 || !pr.HistoryBytesOverCap {
		t.Fatalf("FSPruneResult decode: %+v", pr)
	}
	var tr wire.FSTrashRestoreResult
	json.Unmarshal([]byte(`{"restored":"docs/a.md","version":12}`), &tr)
	if tr.Restored != "docs/a.md" || tr.Version != 12 {
		t.Fatalf("FSTrashRestoreResult decode: %+v", tr)
	}
}

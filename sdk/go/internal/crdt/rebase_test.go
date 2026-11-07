package crdt

import "testing"

// rebuild returns a fresh replica holding exactly the server's compacted state.
func rebuild(replica string, server *RGA) *RGA {
	r := New(replica)
	for _, op := range server.SnapshotOps() {
		r.Apply(op)
	}
	return r
}

// TestRebaseReanchorsInsertAfterGCdOrigin (hunt A-5): alice types "XYZ" after "b"
// while bob deletes "b" and the server compacts it away. The typed run must survive
// the rebase (re-anchored where "b" was), never be dropped.
func TestRebaseReanchorsInsertAfterGCdOrigin(t *testing.T) {
	server := New("srv")
	for _, op := range New("seed").GenerateOps("abc") {
		server.Apply(op)
	}
	alice := Load(server.Snapshot())
	alice.replica = "alice"
	held := alice.Snapshot()
	ours := alice.GenerateOps("abXYZc")

	bob := Load(server.Snapshot())
	bob.replica = "bob"
	for _, op := range bob.GenerateOps("ac") { // bob deletes "b"
		server.Apply(op)
	}
	fresh := rebuild("alice", server) // compaction: "b" is gone, not a tombstone
	if fresh.Knows(ours[0].OriginLeft) {
		t.Fatal("setup: the origin survived the compaction")
	}
	out := Rebase(ours, held, fresh)
	if len(out) != len(ours) {
		t.Fatalf("A-5: %d of %d ops kept — an insert was dropped", len(out), len(ours))
	}
	if got := fresh.Text(); got != "aXYZc" {
		t.Fatalf("rebased text = %q, want %q", got, "aXYZc")
	}
	// Sending the rebased ops to the server converges to the same text.
	for _, op := range out {
		server.Apply(op)
	}
	if server.Text() != "aXYZc" || server.PendingLen() != 0 {
		t.Fatalf("server = %q (pending %d), want %q", server.Text(), server.PendingLen(), "aXYZc")
	}
}

func TestRebaseEdges(t *testing.T) {
	server := New("srv")
	for _, op := range New("seed").GenerateOps("hello") {
		server.Apply(op)
	}
	alice := Load(server.Snapshot())
	alice.replica = "alice"
	held := alice.Snapshot()
	ours := alice.GenerateOps("Xhello!") // insert at the very start + at the end

	// Ops already on the server (an earlier push that landed): not sent again (G-M5;
	// before v0.2.6 they were returned and re-sent as no-ops).
	for _, op := range ours {
		server.Apply(op)
	}
	fresh := rebuild("alice", server)
	if out := Rebase(ours, held, fresh); len(out) != 0 || fresh.Text() != "Xhello!" {
		t.Fatalf("re-sent landed ops: kept %d/%d, text %q", len(out), len(ours), fresh.Text())
	}

	// A delete of an element the server already lost is dropped (intent already true).
	srv2 := New("srv")
	for _, op := range New("seed").GenerateOps("abc") {
		srv2.Apply(op)
	}
	a2 := Load(srv2.Snapshot())
	a2.replica = "alice"
	h2 := a2.Snapshot()
	del := a2.GenerateOps("ac") // delete "b"
	b2 := Load(srv2.Snapshot())
	b2.replica = "bob"
	for _, op := range b2.GenerateOps("ac") {
		srv2.Apply(op)
	}
	f2 := rebuild("alice", srv2)
	if out := Rebase(del, h2, f2); len(out) != 0 || f2.Text() != "ac" {
		t.Fatalf("delete of a gone element: kept %d, text %q", len(out), f2.Text())
	}

	// Nothing to rebase.
	if out := Rebase(nil, h2, f2); len(out) != 0 {
		t.Fatalf("empty rebase returned %d ops", len(out))
	}
}

// TestRebaseSkipsOpsAlreadyPresent (G-M5, parity with the TS mergeDoc rebase): ops of
// ours the rebuilt state already holds — an insert that landed before the compaction, a
// delete whose target is already a tombstone — are not sent again; ops that never
// landed are kept (and may anchor on the landed ones). fresh is always rebuilt from the
// server alone, so "present" means "the server has it".
func TestRebaseSkipsOpsAlreadyPresent(t *testing.T) {
	server := New("srv")
	for _, op := range New("seed").GenerateOps("abc") {
		server.Apply(op)
	}
	alice := Load(server.Snapshot())
	alice.replica = "alice"
	ours := alice.GenerateOps("XabcYZ") // insert X (landed), delete nothing, insert YZ (lost)
	held := alice.Snapshot()
	var landed, lost []Op
	for _, op := range ours {
		if op.Type == OpInsert && op.Val == "X" {
			landed = append(landed, op)
		} else {
			lost = append(lost, op)
		}
	}
	for _, op := range landed {
		server.Apply(op)
	}
	// A teammate deletes "a" (a tombstone in the rebuilt state: no GC here).
	bob := Load(server.Snapshot())
	bob.replica = "bob"
	for _, op := range bob.GenerateOps("Xbc") {
		server.Apply(op)
	}
	fresh := Load(server.Snapshot()) // the server's state with its tombstones (no GC)
	fresh.replica = "alice"
	delA := Op{Type: OpDelete, ID: held.Els[1].ID} // our own delete of "a", already a tombstone
	out := Rebase(append(append([]Op{}, ours...), delA), held, fresh)
	if len(out) != len(lost) {
		t.Fatalf("G-M5: want only the %d ops that never landed, got %d: %+v", len(lost), len(out), out)
	}
	for i := range lost {
		if out[i].ID != lost[i].ID {
			t.Fatalf("G-M5: op %d: got %v, want %v", i, out[i].ID, lost[i].ID)
		}
	}
	if got := fresh.Text(); got != "XbcYZ" {
		t.Fatalf("rebased text %q, want %q", got, "XbcYZ")
	}
}

// The check itself: an element the log lacks is a new document; one in the push is not;
// and an id holding other text is a new document whose writer started over.
func TestInstanceGone(t *testing.T) {
	r := New("me")
	base := r.GenerateOps("ab")
	view := r.Snapshot()
	sameIDs := New("me").GenerateOps("xy")
	for _, c := range []struct {
		name            string
		pulled, sending []Op
		want            bool
	}{
		{"all in the log", base, nil, false},
		{"the rest in this push", base[:1], base[1:], false},
		{"a whole new log", nil, nil, true},
		{"part of the view in neither", base[:1], nil, true},
		{"the same ids with other text", sameIDs, nil, true},
	} {
		if got := InstanceGone(view, c.pulled, c.sending); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

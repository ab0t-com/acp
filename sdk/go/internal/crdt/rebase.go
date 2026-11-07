package crdt

// InstanceGone is true when the document at a name is no longer the one view was made
// on: pulled (its whole log, at the view's epoch or an older one) lacks an element of the
// view that isn't in sending. Within an instance nothing leaves a log except by a
// compaction, which moves the epoch up, so a missing element means the document was
// deleted or restored over: a new instance starts at epoch 0 again, and the wire carries
// no generation. An id is compared with its text, because a replica that starts a name
// over mints the same ids again; the first op of an id stands, as in a replica, and what
// the log holds outranks what is being sent. A refused push is the moment to ask: a 409
// looks the same for a compaction, and rebasing an edit onto a document that isn't there
// leaves a fragment of it. Across an epoch, InstanceGoneAt decides.
func InstanceGone(view State, pulled, sending []Op) bool {
	log, push := firstInserts(pulled), firstInserts(sending)
	for _, e := range view.Els {
		val, ok := log[e.ID]
		if !ok {
			val, ok = push[e.ID]
		}
		if !ok || val != e.Val {
			return true
		}
	}
	return false
}

// InstanceGoneAt is the one rule at any pair of epochs: view at viewEpoch, pulled (a whole
// log) at pulledEpoch. At the view's epoch or an older one it is InstanceGone. At a newer
// epoch a compaction may have collected tombstones, but it keeps the id of every element
// still visible. So the view is gone when none of its visible elements is in the log and
// at least one of them was there to find: what this push itself inserts says nothing of
// the log, and a view holding nothing else leans on nothing. One survivor is the same
// document. A teammate's delete-all followed by a compaction reads as gone too: a refusal
// the writer sees, nothing lost. The table every client reads is
// testdata/crdt-instance-vectors.json.
func InstanceGoneAt(view State, pulled, sending []Op, viewEpoch, pulledEpoch int) bool {
	if pulledEpoch <= viewEpoch {
		return InstanceGone(view, pulled, sending)
	}
	log, push := firstInserts(pulled), firstInserts(sending)
	asked := false
	for _, e := range view.Els {
		if e.Deleted {
			continue
		}
		val, ok := log[e.ID]
		if ok && val == e.Val {
			return false
		}
		if _, ours := push[e.ID]; ok || !ours {
			asked = true
		}
	}
	return asked
}

// firstInserts is what a replica would hold of ops: the first op of an id, its first rune.
func firstInserts(ops []Op) map[ID]string {
	held := make(map[ID]string, len(ops))
	for _, op := range ops {
		if op.Type != OpInsert {
			continue
		}
		if _, dup := held[op.ID]; !dup {
			held[op.ID] = string(runeOf(op.Val))
		}
	}
	return held
}

// Rebase carries ops derived against held (the replica state they were generated from,
// or any later state of it that still contains their anchors) onto fresh — a replica
// rebuilt from the server after a compaction (the epoch barrier) — applies them to
// fresh, and returns the ops to send at the new epoch.
//
// Element ids survive compaction, so ops replay as they are, except:
//   - an insert whose OriginLeft is gone from fresh (a tombstone the compaction
//     garbage-collected) is RE-ANCHORED to the nearest element before it in held that
//     still exists — where that position collapsed to — instead of being dropped (the
//     typed run would vanish locally and never reach the server, hunt A-5) or buffered
//     forever (F-INV-5);
//   - a delete of an element already gone is dropped (its intent is already true);
//   - an op fresh already holds is not returned (v0.2.6 G-M5, parity with the TS
//     mount): an insert whose id is present (an earlier push that landed), a delete
//     whose target is already a tombstone. Every caller rebuilds fresh from the
//     server's state alone, so "present" means "the server has it" — never a local op
//     still waiting to be sent (that would drop it from the pending set).
//
// The ops are never re-derived by diffing a text against fresh: that would emit a
// teammate's committed insert as our delete (CL-8.3/8.4). One implementation for the
// CLI and the MCP bridge (CL-55: the same rule written twice drifted); the Go mount
// carries a port of it (mount/pkg/view rebaseOnto) and the TS SDK its twin (mergeDoc).
func Rebase(ops []Op, held State, fresh *RGA) []Op {
	present := make(map[ID]bool, fresh.els.len())
	dead := map[ID]bool{}
	fresh.els.each(func(e *elem) {
		present[e.id] = true
		if e.deleted {
			dead[e.id] = true
		}
	})
	pos := make(map[ID]int, len(held.Els))
	for i, e := range held.Els {
		pos[e.ID] = i
	}
	out := make([]Op, 0, len(ops))
	for _, op := range ops {
		switch op.Type {
		case OpInsert:
			if present[op.ID] {
				continue // already there (landed before, or a duplicate in ops)
			}
			if !op.OriginLeft.IsZero() && !present[op.OriginLeft] {
				anchor := Zero
				if i, ok := pos[op.OriginLeft]; ok {
					for j := i - 1; j >= 0; j-- {
						if present[held.Els[j].ID] {
							anchor = held.Els[j].ID
							break
						}
					}
				}
				op.OriginLeft = anchor
			}
		case OpDelete:
			if !present[op.ID] || dead[op.ID] {
				continue // gone or already deleted: the intent is already true
			}
			dead[op.ID] = true
		}
		fresh.Apply(op)
		present[op.ID] = true
		out = append(out, op)
	}
	return out
}

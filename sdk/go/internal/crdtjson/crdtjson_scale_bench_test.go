package crdtjson

// crdtjson_scale_bench_test.go — STANDALONE scale/efficiency probe for the
// coordd v0.1.4 OOM investigation (tickets/crdtjson-scale-fix-20260724).
//
// It is READ-ONLY w.r.t. product code: it only exercises the exported Apply /
// Canonicalize / Materialize / SnapshotOps surface plus in-package internals to
// read binding counts. It answers two questions the incident dossier left open:
//
//   RETENTION  — how much resident heap does an overwrite-churned key hold, and
//                does it grow with edit history rather than live content?
//   CHURN      — how much does ONE Materialize()/read allocate as the placement
//                count P grows (i.e. is a read O(1) or O(P))?
//
// Run:
//   go test ./internal/crdtjson/ -run TestScaleReport -v
//   go test ./internal/crdtjson/ -run xxx -bench 'Materialize|Overwrite' -benchmem -benchtime=50x
//
// Nothing here is added to the default gate; it is an investigation artifact.
//
// SEEDING NOTE: the retention/churn seeders below apply canonical ops DIRECTLY
// (bypassing Canonicalize) so seeding is O(N) — this keeps P=10000 tractable.
// The resident structure is identical to what Canonicalize+Apply produces. A
// separate benchmark (BenchmarkWriteViaCanon) drives the REAL write path so the
// Canonicalize per-write full-doc clone cost (finding E7) is still measured.

import (
	"encoding/json"
	"fmt"
	"runtime"
	"testing"

	"github.com/ab0t-com/acp/sdk/go/internal/crdt"
)

func id(clock uint64) crdt.ID { return crdt.ID{Clock: clock, Replica: "p"} }

// seedViaSet applies n plain `set`s to root[key] DIRECTLY. Each binds the key to
// a NEW register nid; none is deleted, so all n bindings stay LIVE and LWW-max
// renders — the OR-map growth a status-poller produces. O(n).
func seedViaSet(d *Doc, key string, n int) {
	c := uint64(0)
	for i := 0; i < n; i++ {
		c++
		opID := id(c)
		c++
		nid := id(c)
		d.Apply(Op{T: OpSet, ID: opID, Target: Root, Key: key, NID: nid,
			Value: json.RawMessage(fmt.Sprintf(`%d`, i)), Now: int64(i + 1), Actor: "p"})
	}
}

// seedViaDelSet applies n (del,set) pairs to root[key] DIRECTLY. Each del records
// the prior binding tag into `removed` (a DEAD-but-retained binding, since ext-14
// removed the physical strip); each set appends a fresh live binding. After n
// rounds: 1 live binding + (n-1) dead-retained bindings + (n-1) removed tags +
// (n-1) orphan register nodes — the exact shape ext-14 stopped reclaiming. O(n).
func seedViaDelSet(d *Doc, key string, n int) {
	c := uint64(0)
	var prevTag crdt.ID
	for i := 0; i < n; i++ {
		if i > 0 {
			c++
			d.Apply(Op{T: OpDel, ID: id(c), Target: Root, Key: key,
				Tags: []crdt.ID{prevTag}, Now: int64(i + 1), Actor: "p"})
		}
		c++
		opID := id(c)
		c++
		nid := id(c)
		d.Apply(Op{T: OpSet, ID: opID, Target: Root, Key: key, NID: nid,
			Value: json.RawMessage(fmt.Sprintf(`%d`, i)), Now: int64(i + 1), Actor: "p"})
		prevTag = opID
	}
}

// bindingStats totals binding slices and removed-tag counts across the doc.
func (d *Doc) bindingStats() (bindings, removed int) {
	for _, n := range d.nodes {
		for _, bs := range n.bindings {
			bindings += len(bs)
		}
		removed += len(n.removed)
	}
	return
}

func heapAlloc() uint64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// TestScaleReport prints the retention + churn table used in DESIGN.md. It is a
// report, not a pass/fail assertion (the point is the numbers).
func TestScaleReport(t *testing.T) {
	t.Log("=== RETENTION: resident heap of one overwrite-churned key (del+set) ===")
	t.Log("N        bindings  removed   heapDelta(MB)  bytes/round   snapshotOps")
	for _, n := range []int{1000, 10000, 100000} {
		d := New()
		before := heapAlloc()
		seedViaDelSet(d, "k", n)
		after := heapAlloc()
		b, rm := d.bindingStats()
		snap := len(d.SnapshotOps())
		t.Logf("%-8d %-9d %-9d %-14.1f %-13.1f %d",
			n, b, rm, float64(after-before)/(1<<20), float64(after-before)/float64(n), snap)
		runtime.KeepAlive(d)
	}

	t.Log("")
	t.Log("=== CHURN: allocation of ONE Materialize() as live-binding count P grows (set) ===")
	t.Log("P        materialize B/op   allocs/op")
	for _, p := range []int{100, 1000, 10000} {
		d := New()
		seedViaSet(d, "k", p)
		_ = d.Materialize() // warm
		var m0, m1 runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&m0)
		const reps = 50
		for i := 0; i < reps; i++ {
			_ = d.Materialize()
		}
		runtime.ReadMemStats(&m1)
		t.Logf("%-8d %-18.0f %-11.0f", p,
			float64(m1.TotalAlloc-m0.TotalAlloc)/reps, float64(m1.Mallocs-m0.Mallocs)/reps)
		runtime.KeepAlive(d)
	}
}

// BenchmarkMaterialize_P measures read cost vs accumulated placement count. If
// reads were O(1) (memoized view, per RFC ext-14 §8.2) these would be flat; they
// are not — each read rebuilds resolveView over all P placements.
func BenchmarkMaterialize_P100(b *testing.B)   { benchMaterialize(b, 100) }
func BenchmarkMaterialize_P1000(b *testing.B)  { benchMaterialize(b, 1000) }
func BenchmarkMaterialize_P10000(b *testing.B) { benchMaterialize(b, 10000) }

func benchMaterialize(b *testing.B, p int) {
	d := New()
	seedViaSet(d, "k", p)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = d.Materialize()
	}
}

// BenchmarkWriteViaCanon drives the REAL write path (Canonicalize+Apply), which
// clones the whole doc per op (finding E7) AND calls resolveView per op via
// walkPath — so writes get slower as the doc's own history grows. This is O(n^2)
// in n by construction; keep n modest.
func BenchmarkWriteViaCanon(b *testing.B) {
	for i := 0; i < b.N; i++ {
		d := New()
		for j := 0; j < 500; j++ {
			ops, err := d.Canonicalize(
				[]Op{{T: OpSet, Path: []any{"k"}, Value: json.RawMessage(fmt.Sprintf(`%d`, j))}},
				"p", int64(j+1), true)
			if err != nil {
				b.Fatal(err)
			}
			for _, o := range ops {
				d.Apply(o)
			}
		}
		runtime.KeepAlive(d)
	}
}

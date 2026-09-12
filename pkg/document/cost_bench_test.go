package document_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

// Worst case for the append anchor: a long run of tombstones at the tail, which
// LastLiveCreatedAt walks back over on every append.
func BenchmarkArrayAppendOverTombstoneTail(b *testing.B) {
	const tail = 2000
	d := document.New("bench")
	require.NoError(b, d.Update(func(r *json.Object, _ *presence.Presence) error {
		a := r.SetNewArray("arr")
		a.AddString("keep")
		for i := range tail {
			a.AddString(fmt.Sprintf("t%d", i))
		}
		return nil
	}))
	require.NoError(b, d.Update(func(r *json.Object, _ *presence.Presence) error {
		a := r.GetArray("arr")
		for range tail {
			a.Delete(1)
		}
		return nil
	}))
	b.ResetTimer()
	for b.Loop() {
		require.NoError(b, d.Update(func(r *json.Object, _ *presence.Presence) error {
			r.GetArray("arr").AddString("x")
			return nil
		}))
	}
}

// Ordinary append with no tombstones, the common path.
func BenchmarkArrayAppendClean(b *testing.B) {
	d := document.New("bench")
	require.NoError(b, d.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewArray("arr").AddString("keep")
		return nil
	}))
	b.ResetTimer()
	for b.Loop() {
		require.NoError(b, d.Update(func(r *json.Object, _ *presence.Presence) error {
			r.GetArray("arr").AddString("x")
			return nil
		}))
	}
}

// Collection over an array that has been moved and deleted heavily: the path
// where the barrier is evaluated once per candidate.
func BenchmarkArrayMoveHeavyGC(b *testing.B) {
	const n = 500
	for b.Loop() {
		b.StopTimer()
		d1, _, a1, a2 := newReplicas(&testing.T{})
		require.NoError(b, d1.Update(func(r *json.Object, _ *presence.Presence) error {
			a := r.SetNewArray("arr")
			for i := range n {
				a.AddString(fmt.Sprintf("v%d", i))
			}
			return nil
		}))
		require.NoError(b, d1.Update(func(r *json.Object, _ *presence.Presence) error {
			a := r.GetArray("arr")
			for i := range n {
				a.MoveAfterByIndex((i+7)%n, i%n)
			}
			return nil
		}))
		require.NoError(b, d1.Update(func(r *json.Object, _ *presence.Presence) error {
			a := r.GetArray("arr")
			for range n / 2 {
				a.Delete(0)
			}
			return nil
		}))
		b.StartTimer()
		d1.GarbageCollect(helper.MaxVersionVector(a1, a2))
	}
}

// TestCostRetention reports DocSize after a full collection on three shapes.
// Run with -v; it asserts nothing, it prints what to compare.
func TestCostRetention(t *testing.T) {
	const n = 200

	report := func(name string, build func(*json.Array)) {
		d1, _, a1, a2 := newReplicas(t)
		require.NoError(t, d1.Update(func(r *json.Object, _ *presence.Presence) error {
			a := r.SetNewArray("arr")
			for i := range n {
				a.AddString(fmt.Sprintf("v%d", i))
			}
			return nil
		}))
		require.NoError(t, d1.Update(func(r *json.Object, _ *presence.Presence) error {
			build(r.GetArray("arr"))
			return nil
		}))
		d1.GarbageCollect(helper.MaxVersionVector(a1, a2))
		s := d1.DocSize()
		t.Logf("%-22s garbageLen=%3d live={d:%d m:%d} gc={d:%d m:%d} total=%d",
			name, d1.GarbageLen(), s.Live.Data, s.Live.Meta, s.GC.Data, s.GC.Meta,
			s.Live.Data+s.Live.Meta+s.GC.Data+s.GC.Meta)
	}

	report("insert only", func(a *json.Array) {})
	report("insert+delete", func(a *json.Array) {
		for range n / 2 {
			a.Delete(0)
		}
	})
	report("move heavy", func(a *json.Array) {
		for i := range n {
			a.MoveAfterByIndex((i+7)%n, i%n)
		}
	})
	report("move+delete", func(a *json.Array) {
		for i := range n {
			a.MoveAfterByIndex((i+7)%n, i%n)
		}
		for range n / 2 {
			a.Delete(0)
		}
	})
	report("append after delete", func(a *json.Array) {
		for range n / 2 {
			a.Delete(a.Len() - 1)
		}
		for i := range 50 {
			a.AddString(fmt.Sprintf("x%d", i))
		}
	})
}

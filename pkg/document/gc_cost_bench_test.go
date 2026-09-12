package document_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

// Worst case for Tree.PurgeBarrierAt: one parent with many children, a large
// fraction of them collected in a single pass.
func BenchmarkTreeWideGC(b *testing.B) {
	const kids = 4000
	for b.Loop() {
		b.StopTimer()
		d1, _, a1, a2 := newReplicas(&testing.T{})
		require.NoError(b, d1.Update(func(r *json.Object, _ *presence.Presence) error {
			r.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{{Type: "p"}}})
			return nil
		}))
		require.NoError(b, d1.Update(func(r *json.Object, _ *presence.Presence) error {
			for range kids {
				r.GetTree("t").Edit(1, 1, &json.TreeNode{Type: "text", Value: "x"}, 0)
			}
			return nil
		}))
		require.NoError(b, d1.Update(func(r *json.Object, _ *presence.Presence) error {
			r.GetTree("t").Edit(1, kids/2+1, nil, 0)
			return nil
		}))
		b.StartTimer()
		d1.GarbageCollect(helper.MaxVersionVector(a1, a2))
	}
}

// Ordinary text collection: the barrier here is one pointer hop.
func BenchmarkTextGC(b *testing.B) {
	const n = 3000
	for b.Loop() {
		b.StopTimer()
		d1, _, a1, a2 := newReplicas(&testing.T{})
		require.NoError(b, d1.Update(func(r *json.Object, _ *presence.Presence) error {
			t := r.SetNewText("t")
			for i := range n {
				t.Edit(i, i, "x")
			}
			return nil
		}))
		require.NoError(b, d1.Update(func(r *json.Object, _ *presence.Presence) error {
			t := r.GetText("t")
			for i := range n / 2 {
				t.Edit(i, i+1, "")
			}
			return nil
		}))
		b.StartTimer()
		d1.GarbageCollect(helper.MaxVersionVector(a1, a2))
	}
}

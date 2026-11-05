/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package document_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// deliverTo hands src's pending local changes to dst and acks them at src: the
// one-way half of a server round trip, which is what lets one replica run
// ahead while the other stays behind. exchangeInOrder cannot express that -- it
// acks every replica's pack whether or not anyone received it.
func deliverTo(t *testing.T, src, dst *document.Document) {
	t.Helper()

	// Through protobuf, as on the wire: handing a change's pointer to another
	// document lets the receiver rewrite its version vector in place.
	pb, err := converter.ToChangePack(src.CreateChangePack())
	require.NoError(t, err)
	pack, err := converter.FromChangePack(pb)
	require.NoError(t, err)

	require.NoError(t, dst.ApplyChangePack(change.NewPack(
		pack.DocumentKey, change.NewCheckpoint(0, 0), pack.Changes, time.InitialVersionVector, nil,
	)))

	var lastSeq uint32
	if n := len(pack.Changes); n > 0 {
		lastSeq = pack.Changes[n-1].ClientSeq()
	}
	require.NoError(t, src.ApplyChangePack(change.NewPack(
		pack.DocumentKey, change.NewCheckpoint(0, lastSeq), nil, time.InitialVersionVector, nil,
	)))
}

// minVectorOf returns the element-wise minimum of the replicas' version
// vectors, which is the watermark the server hands back for GC.
func minVectorOf(docs ...*document.Document) time.VersionVector {
	vv := docs[0].VersionVector().DeepCopy()
	for _, doc := range docs[1:] {
		other := doc.VersionVector()
		vv.Min(&other)
	}
	return vv
}

// TestTreeBornTombstonedInsertSurvivesUntilTheRemovalIsKnown covers #2327: a
// node inserted into a concurrently removed parent is born tombstoned on the
// receiving replica. Stamping it with the INSERTER's edit ticket made the
// minimum version vector cover it the moment the insert was delivered -- while
// the inserter, which has not pulled the removal yet, is still anchoring its
// next keystroke to it. The receiver purged the node and then failed to apply
// the inserter's next change, forever.
func TestTreeBornTombstonedInsertSurvivesUntilTheRemovalIsKnown(t *testing.T) {
	// d1 types; d2 removes the paragraph d1 is typing in.
	setup := func(t *testing.T) (*document.Document, *document.Document) {
		d1, d2, _, _ := newReplicas(t)

		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "r", Children: []json.TreeNode{
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}}},
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "cd"}}},
			}})
			return nil
		}))
		deliverTo(t, d1, d2)

		// d2 removes the first paragraph. d1 does not receive it.
		require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0}, []int{1}, nil, 0)
			return nil
		}))
		// d1 appends "x" to "ab" and ships it; d2 takes it born tombstoned.
		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 2}, []int{0, 2}, &json.TreeNode{Type: "text", Value: "x"}, 0)
			return nil
		}))
		deliverTo(t, d1, d2)

		return d1, d2
	}

	t.Run("the minimum vector does not cover it while the inserter is behind", func(t *testing.T) {
		d1, d2 := setup(t)

		// d1 has seen everything of its own and nothing of d2's removal, so the
		// watermark must not authorize purging anything inside that removal.
		before := d2.GarbageLen()
		assert.Equal(t, 0, d2.GarbageCollect(minVectorOf(d1, d2)))
		assert.Equal(t, before, d2.GarbageLen())
	})

	t.Run("the inserter's next change still applies after a GC pass", func(t *testing.T) {
		d1, d2 := setup(t)

		d2.GarbageCollect(minVectorOf(d1, d2))

		// d1, still unaware of the removal, types "y" right after "x".
		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 3}, []int{0, 3}, &json.TreeNode{Type: "text", Value: "y"}, 0)
			return nil
		}))
		assert.NoError(t, d2.ApplyChangePack(d1.CreateChangePack()))
	})

	t.Run("both replicas purge it once the removal is known to all", func(t *testing.T) {
		d1, d2 := setup(t)

		// d1 finally pulls the removal; now the watermark covers it.
		deliverTo(t, d2, d1)

		vv := minVectorOf(d1, d2)
		assert.Positive(t, d2.GarbageCollect(vv))
		assert.Positive(t, d1.GarbageCollect(vv))
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 0, d2.GarbageLen())
		assert.Equal(t, d1.Root().GetTree("t").ToXML(), d2.Root().GetTree("t").ToXML())
	})
}

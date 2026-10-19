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

// TestTreeInsertOffsetPastAPurge checks step 03's purged-gap rule on changes
// decoded from the wire, with the insert's left-sibling offset rewritten in
// the protobuf: only an offset inside a gap a purge left in the insertion is
// anchored after the piece before it; every other offset past that piece is
// out of range, as before the rule.
func TestTreeInsertOffsetPastAPurge(t *testing.T) {
	// replicas returns two synced replicas of <doc><p>hello</p></doc>, with
	// "ll" deleted and purged on both when purged is set.
	replicas := func(t *testing.T, purged bool) (*document.Document, *document.Document) {
		t.Helper()
		d1, d2, a1, a2 := newReplicas(t)
		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "hello"}}},
			}})
			return nil
		}))
		crossSync(t, d1, d2)
		if !purged {
			return d1, d2
		}

		// <doc>0<p>1hello6</p>7</doc>: "ll" is [3, 5).
		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(3, 5, nil, 0)
			return nil
		}))
		crossSync(t, d1, d2)
		vv := d1.VersionVector().DeepCopy()
		for _, actor := range []time.ActorID{a1, a2} {
			if v := d2.VersionVector().VersionOf(actor); v > vv.VersionOf(actor) {
				vv.Set(actor, v)
			}
		}
		assert.Positive(t, d1.GarbageCollect(vv))
		assert.Positive(t, d2.GarbageCollect(vv))
		require.Equal(t, "<doc><p>heo</p></doc>", treeXML(t, d2))
		return d1, d2
	}

	// send inserts "X" on d1 after the first two characters, rewrites the
	// left-sibling offset of both ends of the insert on the wire, and hands
	// the change to d2.
	send := func(t *testing.T, d1, d2 *document.Document, offset int32) error {
		t.Helper()
		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(3, 3, &json.TreeNode{Type: "text", Value: "X"}, 0)
			return nil
		}))
		pb, err := converter.ToChangePack(d1.CreateChangePack())
		require.NoError(t, err)
		edited := 0
		for _, c := range pb.Changes {
			for _, op := range c.Operations {
				if edit := op.GetTreeEdit(); edit != nil {
					edit.From.LeftSiblingId.Offset = offset
					edit.To.LeftSiblingId.Offset = offset
					edited++
				}
			}
		}
		require.Equal(t, 1, edited)
		pack, err := converter.FromChangePack(pb)
		require.NoError(t, err)
		return d2.ApplyChangePack(change.NewPack(
			pack.DocumentKey, change.NewCheckpoint(0, 0), pack.Changes, time.InitialVersionVector, nil,
		))
	}

	t.Run("an offset inside the purged gap anchors after the piece before it", func(t *testing.T) {
		for _, offset := range []int32{3, 4} {
			d1, d2 := replicas(t, true)
			require.NoError(t, send(t, d1, d2, offset), "offset %d", offset)
			assert.Equal(t, "<doc><p>heXo</p></doc>", treeXML(t, d2), "offset %d", offset)
		}
	})

	t.Run("an offset past the insertion's end is out of range", func(t *testing.T) {
		d1, d2 := replicas(t, true)
		assert.ErrorContains(t, send(t, d1, d2, 7), "out of range")
	})

	t.Run("an offset an unpurged insertion never had is out of range", func(t *testing.T) {
		d1, d2 := replicas(t, false)
		assert.ErrorContains(t, send(t, d1, d2, 9), "out of range")
	})
}

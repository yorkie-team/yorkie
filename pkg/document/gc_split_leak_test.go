/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/resource"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

// crossSync exchanges pending local changes between two in-process documents,
// mimicking a server round-trip without serialization. Delivery uses a neutral
// checkpoint (clientSeq 0) so the receiver's own pending local changes are not
// dropped, and an empty version vector so GC stays out of the exchange (GC is
// asserted separately below). Each sender then self-acks exactly the delivered
// changes so the next crossSync does not re-send them.
func crossSync(t *testing.T, d1, d2 *document.Document) {
	p1 := d1.CreateChangePack()
	p2 := d2.CreateChangePack()

	require.NoError(t, d2.ApplyChangePack(change.NewPack(
		p1.DocumentKey, change.NewCheckpoint(0, 0), p1.Changes, time.InitialVersionVector, nil,
	)))
	require.NoError(t, d1.ApplyChangePack(change.NewPack(
		p2.DocumentKey, change.NewCheckpoint(0, 0), p2.Changes, time.InitialVersionVector, nil,
	)))

	ack := func(p *change.Pack) *change.Pack {
		var lastSeq uint32
		if len(p.Changes) > 0 {
			lastSeq = p.Changes[len(p.Changes)-1].ClientSeq()
		}
		return change.NewPack(
			p.DocumentKey, change.NewCheckpoint(0, lastSeq), nil, time.InitialVersionVector, nil,
		)
	}
	require.NoError(t, d1.ApplyChangePack(ack(p1)))
	require.NoError(t, d2.ApplyChangePack(ack(p2)))
}

func newReplicas(t *testing.T) (*document.Document, *document.Document, time.ActorID, time.ActorID) {
	a1, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	a2, err := time.ActorIDFromHex("000000000000000000000002")
	require.NoError(t, err)

	d1 := document.New("test-doc")
	d2 := document.New("test-doc")
	d1.SetActor(a1)
	d2.SetActor(a2)
	return d1, d2, a1, a2
}

// TestTreeTombstoneSplitGC reproduces a GC leak where pieces split off an
// already-tombstoned tree node inherit removedAt without passing through
// remove(), so they never register a GC pair and can never be purged.
func TestTreeTombstoneSplitGC(t *testing.T) {
	t.Run("purges the same nodes on both replicas when a tombstone is split remotely", func(t *testing.T) {
		d1, d2, a1, a2 := newReplicas(t)

		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{
				Type: "doc",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "hello"}},
				}},
			})
			return nil
		}))
		crossSync(t, d1, d2)

		// Concurrent: d1 deletes "el" (splits the text live, at d1);
		// d2 deletes the whole <p> (tombstones it unsplit, at d2).
		// When d1's delete arrives at d2, it must split d2's tombstone,
		// creating born-dead pieces that never get GC pairs.
		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 4, nil, 0)
			return nil
		}))
		require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(0, 7, nil, 0)
			return nil
		}))
		crossSync(t, d1, d2)

		assert.Equal(t, "<doc></doc>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, d1.Root().GetTree("t").ToXML(), d2.Root().GetTree("t").ToXML())

		// Same full vector, same logical tombstones -> purge counts must match.
		// Before the fix the replica whose tombstone was split leaked the
		// born-dead pieces and purged fewer nodes than its peer.
		purged1 := d1.GarbageCollect(helper.MaxVersionVector(a1, a2))
		purged2 := d2.GarbageCollect(helper.MaxVersionVector(a1, a2))
		assert.Equal(t, purged1, purged2, "asymmetric purge: d1=%d d2=%d", purged1, purged2)

		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 0, d2.GarbageLen())

		// docSize must stay symmetric and gc must drain to zero on both.
		assert.Equal(t, d1.DocSize(), d2.DocSize())
		assert.Equal(t, resource.DataSize{}, d1.DocSize().GC)
	})
}

// TestTextTombstoneSplitGC reproduces the same class of leak in the Text CRDT:
// RGATreeSplitNode.split() copies removedAt into the new piece, so pieces split
// off an already-tombstoned text node are born removed and miss GC registration.
func TestTextTombstoneSplitGC(t *testing.T) {
	t.Run("purges pieces split off a tombstoned text node", func(t *testing.T) {
		d1, d2, a1, a2 := newReplicas(t)

		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("k").Edit(0, 0, "abcdef")
			return nil
		}))
		crossSync(t, d1, d2)

		// d1 tombstones the whole node; d2 concurrently deletes a middle slice.
		// When d2's delete arrives at d1, it splits d1's tombstone; the piece
		// after the deleted range is born dead and used to get no GC pair.
		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("k").Edit(0, 6, "")
			return nil
		}))
		require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("k").Edit(2, 4, "")
			return nil
		}))
		crossSync(t, d1, d2)

		assert.Equal(t, "", d1.Root().GetText("k").String())
		assert.Equal(t, "", d2.Root().GetText("k").String())

		purged1 := d1.GarbageCollect(helper.MaxVersionVector(a1, a2))
		purged2 := d2.GarbageCollect(helper.MaxVersionVector(a1, a2))
		assert.Equal(t, purged1, purged2, "asymmetric purge: d1=%d d2=%d", purged1, purged2)

		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 0, d2.GarbageLen())

		assert.Equal(t, d1.DocSize(), d2.DocSize())
		assert.Equal(t, resource.DataSize{}, d1.DocSize().GC)
	})
}

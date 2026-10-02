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

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

// The same-boundary walks (§7.5's empty-run advance and §7.8's retarget) both
// classify a split product by holdsKnownChild, which counts the children it
// still holds with the tombstones included. Purging one of those tombstones
// changes the answer, so Tree.splitChainBarriersAt defers the purge until the
// product's own createdAt is causally stable -- after which neither walk asks
// about it any more.
//
// These tests pin both halves of that: the deferral, and that it retires
// rather than retaining the tombstone for good.

// seedSplitChainReplicas returns two replicas holding
// <doc><p><span>abcde</span></p></doc>, in sync.
func seedSplitChainReplicas(t *testing.T) (*document.Document, *document.Document, time.ActorID, time.ActorID) {
	t.Helper()

	d1, d2, a1, a2 := newReplicas(t)
	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{
			Type: "doc",
			Children: []json.TreeNode{{
				Type: "p",
				Children: []json.TreeNode{{
					Type:     "span",
					Children: []json.TreeNode{{Type: "text", Value: "abcde"}},
				}},
			}},
		})
		return nil
	}))
	crossSync(t, d1, d2)
	require.Equal(t, `<doc><p><span>abcde</span></p></doc>`, d1.Root().GetTree("t").ToXML())
	require.Equal(t, `<doc><p><span>abcde</span></p></doc>`, d2.Root().GetTree("t").ToXML())

	return d1, d2, a1, a2
}

// TestTreeSplitChainGCBarrier drives the shape the barrier exists for: a
// tombstone whose removal every replica already knows about, which a local
// split has since moved into a product no other replica has seen yet. The
// removal is causally stable, so removedAt alone would authorise the purge;
// the product's createdAt is not, so a peer's concurrent split can still
// arrive and ask what that product holds.
func TestTreeSplitChainGCBarrier(t *testing.T) {
	t.Run("defers a tombstone a local split moved into an unseen product", func(t *testing.T) {
		d1, d2, a1, a2 := seedSplitChainReplicas(t)

		// d2 removes the last character. Delivering it one way and acking at
		// d2 leaves the removal covered by the element-wise min of the two
		// vectors, which is what a server would compute.
		require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 0, 4}, []int{0, 0, 5}, nil, 0)
			return nil
		}))
		oneWayDeliver(t, d2, d1)
		require.Equal(t, `<doc><p><span>abcd</span></p></doc>`, d1.Root().GetTree("t").ToXML())
		require.Positive(t, d1.GarbageLen(), "the removal should have registered a tombstone")

		minVV := time.MinVersionVector(d1.VersionVector(), d2.VersionVector())

		// d1 splits the span and does not push it. The right half is a fresh
		// product of d1's, carrying InsPrevID, and the tombstone goes with it.
		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 0, 3}, []int{0, 0, 3}, nil, 1)
			return nil
		}))
		require.Equal(t, `<doc><p><span>abc</span><span>d</span></p></doc>`, d1.Root().GetTree("t").ToXML())

		before := d1.GarbageLen()
		collected := d1.GarbageCollect(minVV)
		assert.Equal(t, 0, collected,
			"the tombstone sits under a split product minVV does not cover; its purge must wait")
		assert.Equal(t, before, d1.GarbageLen(), "the deferred tombstone must stay charged to GC")

		// And it is a deferral, not retention: once every actor is covered the
		// walks break before classifying the product, so the barrier retires.
		collected = d1.GarbageCollect(helper.MaxVersionVector(a1, a2))
		assert.Positive(t, collected, "the barrier must retire once the product is causally stable")
		assert.Equal(t, 0, d1.GarbageLen(), "tree garbage must still drain")
	})

	// The control: the identical removal and the identical minVV, with no
	// split in between. Nothing is deciding against the tombstone's place, so
	// the same vector that deferred above purges here -- which is what makes
	// the assertion above a test of the barrier and not of minVV.
	t.Run("purges the same tombstone under the same vector when no split intervenes", func(t *testing.T) {
		d1, d2, _, _ := seedSplitChainReplicas(t)

		require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 0, 4}, []int{0, 0, 5}, nil, 0)
			return nil
		}))
		oneWayDeliver(t, d2, d1)
		require.Positive(t, d1.GarbageLen())

		minVV := time.MinVersionVector(d1.VersionVector(), d2.VersionVector())

		collected := d1.GarbageCollect(minVV)
		assert.Positive(t, collected, "without a split chain the same vector authorises the purge")
		assert.Equal(t, 0, d1.GarbageLen())
	})

	// Replicas have to agree on the tree after one of them has collected. The
	// barrier is what keeps the §7.8 retarget reading the same children on
	// both sides while a concurrent split is still in flight.
	t.Run("replicas converge when one collects while a split is in flight", func(t *testing.T) {
		d1, d2, a1, a2 := seedSplitChainReplicas(t)

		require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 0, 4}, []int{0, 0, 5}, nil, 0)
			return nil
		}))
		oneWayDeliver(t, d2, d1)

		minVV := time.MinVersionVector(d1.VersionVector(), d2.VersionVector())

		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 0, 3}, []int{0, 0, 3}, nil, 1)
			return nil
		}))
		// d2 splits the same boundary concurrently, without having seen d1's.
		require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 0, 3}, []int{0, 0, 3}, nil, 1)
			return nil
		}))

		d1.GarbageCollect(minVV)

		crossSync(t, d1, d2)
		crossSync(t, d1, d2)

		assert.Equal(t, d1.Root().GetTree("t").ToXML(), d2.Root().GetTree("t").ToXML())
		assert.Equal(t, treeShape(t, d1), treeShape(t, d2))

		for _, doc := range []*document.Document{d1, d2} {
			doc.GarbageCollect(helper.MaxVersionVector(a1, a2))
			assert.Equal(t, 0, doc.GarbageLen(), "tree garbage must drain on both replicas")
		}
	})
}

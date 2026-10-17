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

package crdt

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// detachFixture builds <r><div><p>ab</p></div></r> with div and p removed and
// the text live under them, the shape an identity restore leaves.
func detachFixture(t *testing.T) (*Tree, *TreeNode, *TreeNode, *TreeNode, *time.Ticket) {
	t.Helper()
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	lamport := int64(0)
	nextTicket := func() *time.Ticket {
		lamport++
		return time.NewTicket(lamport, 0, actor)
	}
	root := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "r", nil)
	div := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "div", nil)
	p := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "p", nil)
	text := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "text", nil, "ab")
	require.NoError(t, root.Append(div))
	require.NoError(t, div.Append(p))
	require.NoError(t, p.Append(text))
	tree := NewTree(root, nextTicket())
	removedAt := nextTicket()
	div.remove(removedAt)
	p.remove(removedAt)
	return tree, div, p, text, removedAt
}

// TestCollectHoldsAnElementWithALiveDescendant pins the GC hold behind the
// detached-node invariant. div and p are removed with live text under them,
// the shape an identity restore leaves. Neither is purged while the text is
// live: a purge would detach bytes Live still holds while a rebuild no
// longer counts them. Once the text is removed too, all three go.
func TestCollectHoldsAnElementWithALiveDescendant(t *testing.T) {
	tree, div, p, text, removedAt := detachFixture(t)
	root := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
	for _, node := range []*TreeNode{div, p} {
		size := node.DataSize()
		root.RegisterGCPair(GCPair{Parent: tree, Child: node, GCOnlySize: &size})
	}

	vector := time.NewVersionVector()
	vector.Set(removedAt.ActorID(), removedAt.Lamport()+1)
	n, err := root.GarbageCollect(vector)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "held while the text under them is live")
	assert.Equal(t, "<r></r>", tree.ToXML())
	assert.Len(t, tree.Root().Children(true), 1, "div is still in the tree")

	text.remove(time.NewTicket(removedAt.Lamport()+1, 0, removedAt.ActorID()))
	size := text.DataSize()
	root.RegisterGCPair(GCPair{Parent: tree, Child: text, GCOnlySize: &size})
	n, err = root.GarbageCollect(vector)
	require.NoError(t, err)
	assert.Equal(t, 3, n, "released once nothing under them is live")
	assert.Empty(t, tree.Root().Children(true))
}

// TestPurgeInsideADetachedSubtreeWalksNothing pins the detached mark: a node
// a purge already took out with its ancestor is purged later, through its
// own GC pair, without walking anything.
func TestPurgeInsideADetachedSubtreeWalksNothing(t *testing.T) {
	tree, div, p, text, removedAt := detachFixture(t)
	text.remove(removedAt)

	walked, err := tree.purge(div)
	require.NoError(t, err)
	assert.Equal(t, 3, walked)
	assert.True(t, p.detached)
	assert.True(t, text.detached)

	walked, err = tree.purge(p)
	require.NoError(t, err)
	assert.Equal(t, 0, walked, "already detached: nothing to walk")
}

// TestCollectWalksADeletedSubtreeOnce pins the cost of collecting a deleted
// subtree whose nodes each carry a GC pair of their own: a chain of nested
// removed elements, purged top-down, the order that made each purge walk
// everything still under it. Each node is walked once in all, by the first
// purge that detaches it, so the pass is linear in the depth.
func TestCollectWalksADeletedSubtreeOnce(t *testing.T) {
	const depth = 2000

	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	lamport := int64(0)
	nextTicket := func() *time.Ticket {
		lamport++
		return time.NewTicket(lamport, 0, actor)
	}
	root := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "r", nil)
	nodes := make([]*TreeNode, depth)
	parent := root
	for i := range nodes {
		nodes[i] = NewTreeNode(NewTreeNodeID(nextTicket(), 0), "div", nil)
		require.NoError(t, parent.Append(nodes[i]))
		parent = nodes[i]
	}
	tree := NewTree(root, nextTicket())
	removedAt := nextTicket()
	for _, node := range nodes {
		node.remove(removedAt)
	}

	visits := 0
	for _, node := range nodes {
		walked, err := tree.purge(node)
		require.NoError(t, err)
		visits += walked
	}
	assert.Equal(t, depth, visits, "every node is walked exactly once")
	assert.Equal(t, 1, tree.NodeMapByID.Len(), "only the root is left registered")
	assert.Equal(t, "<r></r>", tree.ToXML())
}

// TestRestoreLeavesADetachedNodeATombstone pins Restore's two detached
// guards. div is purged while p and the text under it are still tombstones
// whose own removals are not yet collectable, so they stay registered,
// detached. A restore naming either, by element span or by text span, must
// not revive it: a detached node is off the ledger and out of the tree, and
// reviving it would charge Live for bytes no rebuild holds.
func TestRestoreLeavesADetachedNodeATombstone(t *testing.T) {
	tree, div, p, text, removedAt := detachFixture(t)
	text.remove(removedAt)
	_, err := tree.purge(div)
	require.NoError(t, err)
	require.True(t, p.detached)
	require.Same(t, p, tree.findFloorNode(p.id), "p is still registered")

	untombstoned, recreated, _, _, _, err := tree.Restore([]*TreeRestoreSpan{
		{ID: p.id, NodeType: "p", Length: 1, ParentID: div.id},
		{ID: text.id, NodeType: "text", IsText: true, Length: 2, Value: "ab", ParentID: p.id},
	}, false, nil)
	require.NoError(t, err)
	assert.Empty(t, untombstoned)
	assert.Empty(t, recreated)
	assert.True(t, p.IsRemoved())
	assert.True(t, text.IsRemoved())
}

// TestLiveHoldComposesWithTheBarrier pins that the live-descendant hold only
// ever delays a purge removedAt and GCBarrier allow. div is removed with live
// text under it and a sibling after it whose creation the collection vector
// does not cover. Neither the barrier nor the hold alone lets div go; with
// the barrier covered the hold still keeps it; with the text removed, the
// text goes and div waits for the barrier, then goes, and the ledger then
// holds nothing for either.
func TestLiveHoldComposesWithTheBarrier(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	peer, err := time.ActorIDFromHex("0000000000000000000000dd")
	require.NoError(t, err)
	lamport := int64(0)
	nextTicket := func() *time.Ticket {
		lamport++
		return time.NewTicket(lamport, 0, actor)
	}
	root := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "r", nil)
	div := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "div", nil)
	text := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "text", nil, "ab")
	after := NewTreeNode(NewTreeNodeID(time.NewTicket(50, 0, peer), 0), "p", nil)
	require.NoError(t, root.Append(div, after))
	require.NoError(t, div.Append(text))
	tree := NewTree(root, nextTicket())
	removedAt := nextTicket()
	div.remove(removedAt)

	doc := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
	size := div.DataSize()
	doc.RegisterGCPair(GCPair{Parent: tree, Child: div, GCOnlySize: &size})
	collect := func(peerSeen int64) int {
		vector := time.NewVersionVector()
		vector.Set(actor, lamport)
		vector.Set(peer, peerSeen)
		n, err := doc.GarbageCollect(vector)
		require.NoError(t, err)
		return n
	}

	assert.Equal(t, 0, collect(0), "the barrier and the hold both keep div")
	assert.Equal(t, 0, collect(50), "barrier covered: the live text still holds div")

	text.remove(nextTicket())
	textSize := text.DataSize()
	doc.RegisterGCPair(GCPair{Parent: tree, Child: text, GCOnlySize: &textSize})
	assert.Equal(t, 1, collect(0), "text removed: it goes, while the barrier keeps div")
	assert.Same(t, div, tree.Root().Children(true)[0])
	assert.Equal(t, 1, collect(50), "both allow: div goes")
	assert.Equal(t, 0, doc.GarbageLen())
	gc := doc.DocSize().GC
	assert.Zero(t, (&gc).Total())
}

// TestRetombstoneLeavesADetachedNodeAlone pins Retombstone's detached guard.
// A peer's split cut <r><div><p>ab</p></div></r> into two divs, the product
// div' and p' carrying "b"; then div, p and "a" were deleted and div purged,
// leaving p registered and detached. A retombstone naming p, from a change
// that did not know the split, must neither refresh p's span off the
// detached subtree nor run the §4.1 cascade from it: p's InsNextID still
// reaches p', which is attached and live.
func TestRetombstoneLeavesADetachedNodeAlone(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	peer, err := time.ActorIDFromHex("0000000000000000000000ff")
	require.NoError(t, err)
	lamport := int64(0)
	nextTicket := func() *time.Ticket {
		lamport++
		return time.NewTicket(lamport, 0, actor)
	}
	root := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "r", nil)
	div := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "div", nil)
	p := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "p", nil)
	require.NoError(t, p.Append(NewTreeNode(NewTreeNodeID(nextTicket(), 0), "text", nil, "ab")))
	require.NoError(t, div.Append(p))
	require.NoError(t, root.Append(div))
	tree := NewTree(root, nextTicket())
	unaware := time.NewVersionVector()
	unaware.Set(actor, lamport)

	peerLamport := lamport
	peerTicket := func() *time.Ticket {
		peerLamport++
		return time.NewTicket(peerLamport, 0, peer)
	}
	_, _, err = tree.EditT(3, 3, nil, 2, peerTicket(), peerTicket)
	require.NoError(t, err)
	require.Equal(t, "<r><div><p>a</p></div><div><p>b</p></div></r>", tree.ToXML())
	product := tree.Root().Children()[1].Children()[0]

	lamport = peerLamport
	_, _, err = tree.EditT(0, 5, nil, 0, nextTicket(), nextTicket)
	require.NoError(t, err)
	require.NoError(t, tree.Purge(div))
	require.True(t, p.detached)
	require.Equal(t, "<r><div><p>b</p></div></r>", tree.ToXML())

	span := &TreeRestoreSpan{ID: p.id, NodeType: "p", ParentID: div.id}
	pairs, _, edits, spans, err := tree.Retombstone([]*TreeRestoreSpan{span}, nextTicket(), true, unaware)
	require.NoError(t, err)
	assert.Empty(t, pairs)
	assert.Empty(t, edits)
	require.Len(t, spans, 1)
	assert.Equal(t, *span, *spans[0], "the span keeps the anchors it came with")
	assert.False(t, product.IsRemoved(), "the attached split product stays live")
	assert.Equal(t, "<r><div><p>b</p></div></r>", tree.ToXML())
}

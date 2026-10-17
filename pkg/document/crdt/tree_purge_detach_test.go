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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/index"
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

// TestCollectReleasesALiveDescendantWithItsAncestor pins the release behind
// the detached-node invariant. div and p are removed with live text under
// them, the shape a concurrent insert or identity restore leaves. Once the
// collection vector covers their removal, the text is tombstoned and goes
// with them: a purge that left it live would detach bytes Live still holds
// while a rebuild no longer counts them, and holding div for it instead
// would keep div for as long as a node no one can see stays live.
func TestCollectReleasesALiveDescendantWithItsAncestor(t *testing.T) {
	tree, div, p, text, removedAt := detachFixture(t)
	root := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
	base := root.DocSize().Live
	root.docSize.Live.Add(text.DataSize())
	for _, node := range []*TreeNode{div, p} {
		size := node.DataSize()
		root.RegisterGCPair(GCPair{Parent: tree, Child: node, GCOnlySize: &size})
	}

	vector := time.NewVersionVector()
	vector.Set(removedAt.ActorID(), removedAt.Lamport())
	n, err := root.GarbageCollect(vector)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "div and p go, with the text under them")
	assert.Empty(t, tree.Root().Children(true))
	assert.True(t, text.IsRemoved())
	assert.True(t, text.detached)
	assert.Same(t, removedAt, text.RemovedAt(), "tombstoned with the ancestor's removal")
	assert.Same(t, text, tree.findFloorNode(text.id), "the text still resolves, as main leaves a detached node")
	assert.Equal(t, len(tree.Nodes()), tree.NodeLen(), "NodeMapByID holds only what is reachable from the root")
	assert.Equal(t, 0, root.GarbageLen())
	live, gc := root.DocSize().Live, root.DocSize().GC
	assert.Equal(t, base, live, "the text's bytes left Live with it")
	assert.Zero(t, (&gc).Total())
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

// TestRestoreLeavesADetachedNodeATombstone pins how Restore resolves a
// detached node. div is purged while p and the text under it are still
// tombstones whose own removals are not yet collectable, so they stay
// registered, detached. A restore naming them resolves them as a replica
// that has purged them does, or a copy of the attached tree: the detached
// nodes themselves stay tombstones, and each span is recreated if its
// parent is there. The restoring change does not know div's removal, so
// only that resolution, not knownRemovalAbove, keeps p and the text from
// being revived in place.
func TestRestoreLeavesADetachedNodeATombstone(t *testing.T) {
	other, err := time.ActorIDFromHex("0000000000000000000000ee")
	require.NoError(t, err)
	unaware := time.NewVersionVector()
	unaware.Set(other, 1)

	setup := func(t *testing.T) (*Tree, *TreeNode, *TreeNode, *TreeNode) {
		tree, div, p, text, removedAt := detachFixture(t)
		text.remove(removedAt)
		_, err := tree.purge(div)
		require.NoError(t, err)
		require.True(t, p.detached)
		require.Same(t, p, tree.findFloorNode(p.id), "p is still registered")
		return tree, div, p, text
	}
	spanOf := func(node, parent *TreeNode) *TreeRestoreSpan {
		if node.IsText() {
			return &TreeRestoreSpan{ID: node.id, NodeType: "text", IsText: true, Length: 2, Value: "ab", ParentID: parent.id}
		}
		return &TreeRestoreSpan{ID: node.id, NodeType: node.Type(), Length: 1, ParentID: parent.id}
	}

	t.Run("under a purged parent nothing comes back", func(t *testing.T) {
		tree, div, p, text := setup(t)
		untombstoned, recreated, _, _, _, err := tree.Restore([]*TreeRestoreSpan{
			spanOf(p, div), spanOf(text, p),
		}, false, unaware)
		require.NoError(t, err)
		assert.Empty(t, untombstoned)
		assert.Empty(t, recreated)
		assert.True(t, p.IsRemoved())
		assert.True(t, text.IsRemoved())
		assert.Equal(t, "<r></r>", tree.ToXML())
	})

	t.Run("with the purged parent restored first, all three are recreated", func(t *testing.T) {
		tree, div, p, text := setup(t)
		spans := []*TreeRestoreSpan{spanOf(div, tree.Root()), spanOf(p, div), spanOf(text, p)}
		copied, err := tree.DeepCopy()
		require.NoError(t, err)

		untombstoned, recreated, _, _, _, err := tree.Restore(spans, false, unaware)
		require.NoError(t, err)
		assert.Empty(t, untombstoned, "nothing detached is revived in place")
		assert.Len(t, recreated, 3)
		assert.True(t, p.IsRemoved())
		assert.True(t, text.IsRemoved())
		assert.Equal(t, "<r><div><p>ab</p></div></r>", tree.ToXML())

		// A copy of the attached tree, which holds no detached node, ends
		// the same.
		_, _, _, _, _, err = copied.(*Tree).Restore(spans, false, unaware)
		require.NoError(t, err)
		assert.Equal(t, tree.ToXML(), copied.(*Tree).ToXML())
	})
}

// TestLiveReleaseWaitsForTheBarrier pins that the release runs only once
// removedAt and GCBarrier both let a purge go. div is removed with live text
// under it and a sibling after it whose creation the collection vector does
// not cover. While the barrier keeps div, the text stays live and charged
// to Live; once it is covered, div goes and the text with it.
func TestLiveReleaseWaitsForTheBarrier(t *testing.T) {
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
	base := doc.DocSize().Live
	doc.docSize.Live.Add(text.DataSize())
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

	assert.Equal(t, 0, collect(0), "the barrier keeps div")
	assert.False(t, text.IsRemoved(), "and the text under it live")
	live := doc.DocSize().Live
	live.Sub(base)
	assert.Equal(t, text.DataSize(), live)

	assert.Equal(t, 1, collect(50), "barrier covered: div goes")
	assert.True(t, text.IsRemoved())
	assert.True(t, text.detached)
	assert.Equal(t, 0, doc.GarbageLen())
	live, gc := doc.DocSize().Live, doc.DocSize().GC
	assert.Equal(t, base, live)
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

	// The span names the root as p's parent, which a refresh off the
	// detached p would rewrite to div.
	span := &TreeRestoreSpan{ID: p.id, NodeType: "p", ParentID: tree.Root().id}
	pairs, _, edits, spans, err := tree.Retombstone([]*TreeRestoreSpan{span}, nextTicket(), true, unaware)
	require.NoError(t, err)
	assert.Empty(t, pairs)
	assert.Empty(t, edits)
	require.Len(t, spans, 1)
	assert.Equal(t, *span, *spans[0], "the span keeps the anchors it came with")
	assert.Equal(t, tree.Root().id, spans[0].ParentID)
	assert.False(t, product.IsRemoved(), "the attached split product stays live")
	assert.Equal(t, "<r><div><p>b</p></div></r>", tree.ToXML())
}

// TestRestoreRecreatesNothingUnderADetachedParent pins recreateParent's
// detached check. div is purged with p under it, and the text under p was
// purged on its own, so p is registered and detached while the text is
// gone. A restore naming the text under p must not recreate it there: the
// node would be charged to GC for bytes no rebuild holds, and a replica that
// had purged p too would have nowhere to put it.
func TestRestoreRecreatesNothingUnderADetachedParent(t *testing.T) {
	tree, div, p, text, removedAt := detachFixture(t)
	text.remove(removedAt)
	require.NoError(t, tree.Purge(text))
	require.NoError(t, tree.Purge(div))
	require.True(t, p.detached)

	untombstoned, recreated, _, _, _, err := tree.Restore([]*TreeRestoreSpan{
		{ID: text.id, NodeType: "text", IsText: true, Length: 2, Value: "ab", ParentID: p.id},
	}, false, nil)
	require.NoError(t, err)
	assert.Empty(t, untombstoned)
	assert.Empty(t, recreated)
	assert.Empty(t, p.Children(true), "nothing is placed under the detached p")
	assert.Empty(t, tree.drainPendingGCPairs(), "and nothing is booked to GC")
}

// TestRecreateGoesNowhereDetachedThroughASplitHome pins the detached check
// on rung (a)'s split home. A peer's split carried "b" of <p>ab</p> into p'
// under a new div', which was then deleted and purged, leaving p' and "b"
// registered and detached. Recreating the purged "a" under p, from a change
// that did not know the split, must not follow "b" into p': it would land
// in a subtree nothing references and be booked to GC for bytes no rebuild
// holds. It falls through to the other rungs and lands under p.
func TestRecreateGoesNowhereDetachedThroughASplitHome(t *testing.T) {
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
	insertion := nextTicket()
	require.NoError(t, p.Append(NewTreeNode(NewTreeNodeID(insertion, 0), "text", nil, "ab")))
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
	product := tree.Root().Children()[1]
	productP := product.Children()[0]

	lamport = peerLamport
	_, _, err = tree.EditT(5, 10, nil, 0, nextTicket(), nextTicket)
	require.NoError(t, err)
	_, err = tree.purge(product)
	require.NoError(t, err)
	require.True(t, productP.detached)
	_, _, err = tree.EditT(2, 3, nil, 0, nextTicket(), nextTicket)
	require.NoError(t, err)
	require.NoError(t, tree.Purge(p.Children(true)[0]))
	require.Equal(t, "<r><div><p></p></div></r>", tree.ToXML())

	_, recreated, _, _, _, err := tree.Restore([]*TreeRestoreSpan{{
		ID: NewTreeNodeID(insertion, 0), NodeType: "text", IsText: true, Length: 1, Value: "a", ParentID: p.id,
	}}, false, unaware)
	require.NoError(t, err)
	require.Len(t, recreated, 1)
	assert.Same(t, p, recreated[0].Index.Parent.Value, "recreated under p, not the detached p'")
	assert.Equal(t, "<r><div><p>a</p></div></r>", tree.ToXML())
	assert.Empty(t, tree.drainPendingGCPairs(), "and nothing is booked to GC")
}

// TestEditUnderADetachedParentNamingAReleasedNode pins the shape a late
// change of the yorkie#2110 race can take: an insert whose parent is a
// detached tombstone, p, and whose left anchor is a node a collection
// released, the text. Released nodes stay registered, as main leaves its
// detached nodes, so the position resolves and the insert applies as on
// main. The content is born tombstoned under the removed p and booked to GC
// only, so Live is untouched.
func TestEditUnderADetachedParentNamingAReleasedNode(t *testing.T) {
	tree, div, p, text, removedAt := detachFixture(t)
	root := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
	root.docSize.Live.Add(text.DataSize())
	size := div.DataSize()
	root.RegisterGCPair(GCPair{Parent: tree, Child: div, GCOnlySize: &size})
	vector := time.NewVersionVector()
	vector.Set(removedAt.ActorID(), removedAt.Lamport())
	_, err := root.GarbageCollect(vector)
	require.NoError(t, err)
	require.True(t, p.detached)
	require.True(t, text.IsRemoved(), "released")
	live := root.DocSize().Live

	peer, err := time.ActorIDFromHex("0000000000000000000000ee")
	require.NoError(t, err)
	lamport := int64(100)
	ticket := func() *time.Ticket {
		lamport++
		return time.NewTicket(lamport, 0, peer)
	}
	at := ticket()
	pos := &TreePos{ParentID: p.id, LeftSiblingID: NewTreeNodeID(text.id.CreatedAt, text.Length())}
	content := NewTreeNode(NewTreeNodeID(at, 0), "text", nil, "X")
	pairs, diff, _, err := tree.Edit(pos, pos, []*TreeNode{content}, 0, at, ticket, nil, false)
	require.NoError(t, err, "the position resolves and the insert applies")
	assert.True(t, content.IsRemoved(), "born tombstoned under the removed p")
	assert.True(t, content.detached, "and detached with it, so no restore revives it")
	assert.Zero(t, diff.Total(), "nothing charged to Live")
	require.Len(t, pairs, 1)
	assert.NotNil(t, pairs[0].GCOnlySize, "booked to GC only")
	assert.Equal(t, live, root.DocSize().Live)
	assert.Equal(t, "<r></r>", tree.ToXML())
}

// releasedFixture builds <r><div><p>ab</p></div><q>cd</q></r> with div
// removed while p and its text are live. With purge set, div is purged,
// releasing p and the text: tombstoned, detached and still registered.
func releasedFixture(t *testing.T, purge bool) (*Tree, *TreeNode, *TreeNode, *TreeNode, func() *time.Ticket) {
	t.Helper()
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	lamport := int64(0)
	next := func() *time.Ticket {
		lamport++
		return time.NewTicket(lamport, 0, actor)
	}
	root := NewTreeNode(NewTreeNodeID(next(), 0), "r", nil)
	div := NewTreeNode(NewTreeNodeID(next(), 0), "div", nil)
	p := NewTreeNode(NewTreeNodeID(next(), 0), "p", nil)
	ab := NewTreeNode(NewTreeNodeID(next(), 0), "text", nil, "ab")
	q := NewTreeNode(NewTreeNodeID(next(), 0), "q", nil)
	cd := NewTreeNode(NewTreeNodeID(next(), 0), "text", nil, "cd")
	require.NoError(t, root.Append(div, q))
	require.NoError(t, div.Append(p))
	require.NoError(t, p.Append(ab))
	require.NoError(t, q.Append(cd))
	tree := NewTree(root, next())
	div.remove(next())
	if purge {
		_, err = tree.PurgeReleasing(div)
		require.NoError(t, err)
		require.True(t, p.detached)
	}
	return tree, div, p, ab, next
}

// TestSplitsInADetachedSubtreeStayDetached pins that a piece split off a
// detached node is detached too. A crafted insert or split in the middle of
// the released "ab" cuts p and the text; every node under the purged div
// must still be a detached tombstone, and a restore that does not know
// div's removal revives none of them in place. A split product left
// unmarked would be revived there, live in a subtree no rebuild holds, and
// charged to Live (yorkie#2244).
func TestSplitsInADetachedSubtreeStayDetached(t *testing.T) {
	for _, tc := range []struct {
		name       string
		splitLevel int
		insert     bool
	}{
		{"an insert in the middle of the text", 0, true},
		{"a split in the middle of the paragraph", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree, div, p, ab, next := releasedFixture(t, true)
			mid := NewTreePos(p.id, NewTreeNodeID(ab.id.CreatedAt, 1))
			var contents []*TreeNode
			if tc.insert {
				contents = []*TreeNode{NewTreeNode(NewTreeNodeID(next(), 0), "text", nil, "XYZ")}
			}
			known := time.NewVersionVector()
			known.Set(ab.id.CreatedAt.ActorID(), 1000)
			_, _, _, err := tree.Edit(mid, mid, contents, tc.splitLevel, next(), next, known, false)
			require.NoError(t, err)

			var nodes []*TreeNode
			index.TraverseNode(div.Index, func(n *index.Node[*TreeNode], _ int) {
				nodes = append(nodes, n.Value)
			})
			require.Greater(t, len(nodes), 3, "the op cut something under div")
			for _, n := range nodes {
				assert.True(t, n.detached, "%s is detached", n.Type())
				assert.True(t, n.IsRemoved(), "%s is a tombstone", n.Type())
			}

			other, err := time.ActorIDFromHex("0000000000000000000000ee")
			require.NoError(t, err)
			unaware := time.NewVersionVector()
			unaware.Set(other, 1)
			var spans []*TreeRestoreSpan
			for _, n := range nodes {
				if n == div {
					continue
				}
				if n.IsText() {
					spans = append(spans, &TreeRestoreSpan{
						ID: n.id, NodeType: "text", IsText: true, Length: n.Length(), Value: n.Value,
						ParentID: n.Index.Parent.Value.id,
					})
				} else {
					spans = append(spans, &TreeRestoreSpan{
						ID: n.id, NodeType: n.Type(), Length: 1, ParentID: n.Index.Parent.Value.id,
					})
				}
			}
			untombstoned, _, _, _, _, err := tree.Restore(spans, false, unaware)
			require.NoError(t, err)
			assert.Empty(t, untombstoned, "nothing in the detached subtree is revived")
			assert.Equal(t, "<r><q>cd</q></r>", tree.ToXML())
		})
	}
}

// TestStyleWithAnEndInADetachedSubtree pins how a Style or RemoveStyle
// resolves an end inside a detached subtree: at the place the subtree was
// purged from, as for an edit. On <r><x>12</x><div><p>ab</p></div><q>cd</q></r>
// with div removed and p live under it, a style from the start of the root
// to the end of "ab" styles x, and no attached node after div, on the
// replica that purged div and on the one that did not. The purged replica
// writes nothing into the detached subtree; once the other collects too,
// both hold the same attributes on every node, and a removal of the style
// clears x on both.
func TestStyleWithAnEndInADetachedSubtree(t *testing.T) {
	attached := []string{"r", "x", "12", "q", "cd"}
	attrsOf := func(n map[string]*TreeNode, names []string) string {
		var sb strings.Builder
		for _, name := range names {
			sb.WriteString(name + "=" + n[name].Attributes() + ";")
		}
		return sb.String()
	}
	purged, pdoc, pn, pnext := halfDetachedFixture(t, true)
	kept, kdoc, kn, knext := halfDetachedFixture(t, false)
	type replica struct {
		tree *Tree
		doc  *Root
		n    map[string]*TreeNode
		next func() *time.Ticket
	}
	replicas := []replica{{purged, pdoc, pn, pnext}, {kept, kdoc, kn, knext}}
	rangeOf := func(r replica) (*TreePos, *TreePos, time.VersionVector) {
		vv := time.NewVersionVector()
		vv.Set(r.n["r"].id.CreatedAt.ActorID(), 1000)
		return NewTreePos(r.n["r"].id, r.n["r"].id),
			NewTreePos(r.n["p"].id, NewTreeNodeID(r.n["ab"].id.CreatedAt, 2)), vv
	}

	for _, r := range replicas {
		from, to, vv := rangeOf(r)
		actor := r.n["r"].id.CreatedAt.ActorID()
		_, _, _, err := r.tree.Style(from, to, map[string]string{"k": "v"}, time.NewTicket(500, 0, actor), vv)
		require.NoError(t, err)
		assert.Equal(t, `<r><x k="v">12</x><q>cd</q></r>`, r.tree.ToXML())
	}
	assert.Equal(t, attrsOf(pn, attached), attrsOf(kn, attached), "the same attached nodes are styled")
	assert.Equal(t, `r=;x= k="v";12=;q=;cd=;`, attrsOf(pn, attached))
	assert.Equal(t, "div=;p=;ab=;", attrsOf(pn, []string{"div", "p", "ab"}),
		"nothing in the detached subtree is styled")

	for _, r := range replicas {
		from, to, vv := rangeOf(r)
		actor := r.n["r"].id.CreatedAt.ActorID()
		_, _, _, err := r.tree.RemoveStyle(from, to, []string{"k"}, time.NewTicket(600, 0, actor), vv)
		require.NoError(t, err)
		assert.Equal(t, "<r><x>12</x><q>cd</q></r>", r.tree.ToXML())
		_, err = r.doc.GarbageCollect(vv)
		require.NoError(t, err)
	}
	assert.Equal(t, attrsOf(pn, attached), attrsOf(kn, attached))
	assert.Equal(t, treeShapeOf(purged), treeShapeOf(kept), "the same tree once both collect")
}

// TestEditWithOneEndInADetachedSubtree pins how a range with exactly one end
// in a detached subtree resolves: at the place the subtree was purged from,
// where a replica that has not purged it counts the subtree from. The tree
// is <r><x>12</x><div><p>ab</p></div><q>cd</q></r> with div removed and p
// live under it; one replica purges div, releasing p and "ab", the other
// does not. A delete from the start of x to the end of "ab", and one from
// the start of "ab" to the end of q, must remove the same attached content
// on both, and the two must hold the same tree once both collect.
func TestEditWithOneEndInADetachedSubtree(t *testing.T) {
	for _, tc := range []struct {
		name     string
		from, to func(n map[string]*TreeNode) *TreePos
		want     string
	}{
		{
			"from the start of x to the end of ab",
			func(n map[string]*TreeNode) *TreePos { return NewTreePos(n["r"].id, n["r"].id) },
			func(n map[string]*TreeNode) *TreePos {
				return NewTreePos(n["p"].id, NewTreeNodeID(n["ab"].id.CreatedAt, 2))
			},
			"<r><q>cd</q></r>",
		},
		{
			"from the start of ab to the end of q",
			func(n map[string]*TreeNode) *TreePos { return NewTreePos(n["p"].id, n["p"].id) },
			func(n map[string]*TreeNode) *TreePos { return NewTreePos(n["r"].id, n["q"].id) },
			"<r><x>12</x></r>",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			purged, pdoc, pn, pnext := halfDetachedFixture(t, true)
			kept, kdoc, kn, knext := halfDetachedFixture(t, false)
			for _, r := range []struct {
				tree  *Tree
				doc   *Root
				n     map[string]*TreeNode
				next  func() *time.Ticket
				label string
			}{{purged, pdoc, pn, pnext, "purged"}, {kept, kdoc, kn, knext, "kept"}} {
				actor := r.n["r"].id.CreatedAt.ActorID()
				vv := time.NewVersionVector()
				vv.Set(actor, 1000)
				at := time.NewTicket(500, 0, actor)
				pairs, _, _, err := r.tree.Edit(tc.from(r.n), tc.to(r.n), nil, 0, at, r.next, vv, false)
				require.NoError(t, err, r.label)
				assert.Equal(t, tc.want, r.tree.ToXML(), r.label)
				for _, pair := range pairs {
					r.doc.RegisterGCPair(pair)
				}
				_, err = r.doc.GarbageCollect(vv)
				require.NoError(t, err, r.label)
			}
			assert.Equal(t, treeShapeOf(purged), treeShapeOf(kept), "the same tree once both collect")
			assert.Equal(t, tc.want, purged.ToXML())
		})
	}
}

// halfDetachedFixture builds <r><x>12</x><div><p>ab</p></div><q>cd</q></r>
// with div removed and p live under it, div's GC pair booked on a Root.
// With purge set, a collection takes div out, releasing p and "ab".
func halfDetachedFixture(t *testing.T, purge bool) (*Tree, *Root, map[string]*TreeNode, func() *time.Ticket) {
	t.Helper()
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	lamport := int64(0)
	next := func() *time.Ticket {
		lamport++
		return time.NewTicket(lamport, 0, actor)
	}
	n := map[string]*TreeNode{}
	n["r"] = NewTreeNode(NewTreeNodeID(next(), 0), "r", nil)
	n["x"] = NewTreeNode(NewTreeNodeID(next(), 0), "x", nil)
	n["12"] = NewTreeNode(NewTreeNodeID(next(), 0), "text", nil, "12")
	n["div"] = NewTreeNode(NewTreeNodeID(next(), 0), "div", nil)
	n["p"] = NewTreeNode(NewTreeNodeID(next(), 0), "p", nil)
	n["ab"] = NewTreeNode(NewTreeNodeID(next(), 0), "text", nil, "ab")
	n["q"] = NewTreeNode(NewTreeNodeID(next(), 0), "q", nil)
	n["cd"] = NewTreeNode(NewTreeNodeID(next(), 0), "text", nil, "cd")
	require.NoError(t, n["r"].Append(n["x"], n["div"], n["q"]))
	require.NoError(t, n["x"].Append(n["12"]))
	require.NoError(t, n["div"].Append(n["p"]))
	require.NoError(t, n["p"].Append(n["ab"]))
	require.NoError(t, n["q"].Append(n["cd"]))
	tree := NewTree(n["r"], next())
	n["div"].remove(next())
	doc := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
	size := n["div"].DataSize()
	doc.RegisterGCPair(GCPair{Parent: tree, Child: n["div"], GCOnlySize: &size})
	if purge {
		vector := time.NewVersionVector()
		vector.Set(actor, lamport)
		_, err := doc.GarbageCollect(vector)
		require.NoError(t, err)
		require.True(t, n["p"].detached)
	}
	return tree, doc, n, next
}

// treeShapeOf renders tree with tombstones, the attached tree only.
func treeShapeOf(tree *Tree) string {
	var sb strings.Builder
	var walk func(n *TreeNode)
	walk = func(n *TreeNode) {
		sb.WriteString(n.Type())
		if n.IsText() {
			sb.WriteString(":" + n.Value)
		}
		if n.IsRemoved() {
			sb.WriteString("x")
		}
		sb.WriteString("[")
		for _, c := range n.Index.Children(true) {
			walk(c.Value)
		}
		sb.WriteString("]")
	}
	walk(tree.Root())
	return sb.String()
}

// TestPurgedSlotOverPurgeOrders pins purgedSlot over every order a replica
// can purge in. The tree is <r><l>LL</l><top><p>ab</p></top><s>x</s><q>cd</q></r>
// with top, p, "ab", s and "x" removed. A replica that purges nothing
// resolves "ab" to the place top sits, before s; every purge order must
// resolve it to the same index: past a sibling purged after (s), up
// through a parent purged after its child (p, then top), and both.
func TestPurgedSlotOverPurgeOrders(t *testing.T) {
	build := func(t *testing.T) (*Tree, map[string]*TreeNode) {
		actor, err := time.ActorIDFromHex("000000000000000000000001")
		require.NoError(t, err)
		lamport := int64(0)
		next := func() *time.Ticket {
			lamport++
			return time.NewTicket(lamport, 0, actor)
		}
		n := map[string]*TreeNode{}
		mk := func(name, typ string, value ...string) *TreeNode {
			n[name] = NewTreeNode(NewTreeNodeID(next(), 0), typ, nil, value...)
			return n[name]
		}
		root := mk("r", "r")
		require.NoError(t, root.Append(mk("l", "l"), mk("top", "top"), mk("s", "s"), mk("q", "q")))
		require.NoError(t, n["l"].Append(mk("LL", "text", "LL")))
		require.NoError(t, n["top"].Append(mk("p", "p")))
		require.NoError(t, n["p"].Append(mk("ab", "text", "ab")))
		require.NoError(t, n["s"].Append(mk("x", "text", "x")))
		require.NoError(t, n["q"].Append(mk("cd", "text", "cd")))
		tree := NewTree(root, next())
		removedAt := next()
		for _, name := range []string{"top", "p", "ab", "s", "x"} {
			n[name].remove(removedAt)
		}
		return tree, n
	}

	unpurged, un := build(t)
	want, err := unpurged.ToIndex(un["ab"], un["ab"])
	require.NoError(t, err)
	require.Equal(t, 4, want, "after <l>LL</l>, where top sits")

	for _, order := range [][]string{
		{"top"}, {"p"}, {"s"}, {"p", "top"}, {"top", "p"}, {"top", "s"}, {"s", "top"},
		{"p", "top", "s"}, {"s", "p", "top"}, {"p", "s", "top"}, {"top", "s", "p"},
	} {
		tree, n := build(t)
		for _, name := range order {
			require.NoError(t, tree.Purge(n[name]))
		}
		got, err := tree.ToIndex(n["ab"], n["ab"])
		require.NoError(t, err, "purged %v", order)
		assert.Equal(t, want, got, "purged %v", order)
	}
}

// TestToTreePosReportsAPurgeWithoutASlot pins the guard behind #2008 now
// that every purge records where it took a node from: a detached node with
// no recorded purge above it fails the lookup with ErrNodeNotFound instead
// of dereferencing a nil parent.
func TestToTreePosReportsAPurgeWithoutASlot(t *testing.T) {
	tree, div, p, text, removedAt := detachFixture(t)
	text.remove(removedAt)
	require.NoError(t, tree.Purge(div))
	require.True(t, p.detached)
	div.purgedFrom = nil

	assert.NotPanics(t, func() {
		_, err := tree.ToIndex(p, text)
		assert.ErrorIs(t, err, ErrNodeNotFound)
		_, err = tree.ToIndex(p, text, true)
		assert.ErrorIs(t, err, ErrNodeNotFound)
	})
}

// TestEditMergingOutOfADetachedSubtree pins the one shape where a range with
// an end in a detached subtree does not converge: a delete that merges. From
// the start of the root to the middle of the released "ab", the replica that
// has not purged div merges div and p into the root, bringing the hidden "b"
// into view, while the replica that purged div holds nothing to move. No
// honest client names a position inside a removed element, so only a late
// or crafted op reaches it, and main diverges the same way.
func TestEditMergingOutOfADetachedSubtree(t *testing.T) {
	results := map[bool]string{}
	for _, purge := range []bool{true, false} {
		tree, _, n, next := halfDetachedFixture(t, purge)
		actor := n["r"].id.CreatedAt.ActorID()
		vv := time.NewVersionVector()
		vv.Set(actor, 1000)
		from := NewTreePos(n["r"].id, n["r"].id)
		to := NewTreePos(n["p"].id, NewTreeNodeID(n["ab"].id.CreatedAt, 1))
		_, _, _, err := tree.Edit(from, to, nil, 0, time.NewTicket(500, 0, actor), next, vv, false)
		require.NoError(t, err)
		results[purge] = tree.ToXML()
	}
	assert.Equal(t, "<r><q>cd</q></r>", results[true], "purged: nothing to move")
	assert.Equal(t, "<r><q>cd</q>b</r>", results[false], "not purged: the merge moves b into view")
}

// TestFindFloorNodeReadsBothIndexes pins that a lookup answers from
// NodeMapByID and detachedByID together, with the floor across both: the
// piece of an insertion that is still attached for an offset inside it,
// and the detached piece for an offset inside that one.
func TestFindFloorNodeReadsBothIndexes(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	root := NewTreeNode(NewTreeNodeID(time.NewTicket(1, 0, actor), 0), "r", nil)
	tree := NewTree(root, time.NewTicket(2, 0, actor))
	insertion := time.NewTicket(3, 0, actor)
	attached := NewTreeNode(NewTreeNodeID(insertion, 0), "text", nil, "ab")
	detached := NewTreeNode(NewTreeNodeID(insertion, 2), "text", nil, "cd")
	detached.detached = true
	tree.putNode(attached)
	tree.putNode(detached)
	require.Equal(t, 2, tree.NodeLen(), "the detached piece is not in NodeMapByID")

	assert.Same(t, attached, tree.findFloorNode(NewTreeNodeID(insertion, 1)))
	assert.Same(t, detached, tree.findFloorNode(NewTreeNodeID(insertion, 3)))
	assert.Same(t, detached, tree.findFloorNode(NewTreeNodeID(insertion, 2)))
	assert.Nil(t, tree.findFloorNode(NewTreeNodeID(time.NewTicket(4, 0, actor), 0)))
}

// TestADetachedNodeCarriesNoIndexedLineage pins that a node placed in a
// detached subtree is not indexed by the source its MergedFrom names, so
// no purge has to take a stamp it gained after detachment back out.
func TestADetachedNodeCarriesNoIndexedLineage(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	root := NewTreeNode(NewTreeNodeID(time.NewTicket(1, 0, actor), 0), "r", nil)
	tree := NewTree(root, time.NewTicket(2, 0, actor))
	source := NewTreeNodeID(time.NewTicket(3, 0, actor), 0)
	node := NewTreeNode(NewTreeNodeID(time.NewTicket(4, 0, actor), 0), "p", nil)
	node.MergedFrom = source
	node.detached = true
	tree.putNode(node)
	assert.Empty(t, tree.mergeStamps[source.toIDString()])
}

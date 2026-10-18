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
	gotime "time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/resource"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/index"
)

// keeperActor stamps the merge keepDetached records, at a lamport no test
// vector covers unless it names keeperActor.
var keeperActor = func() time.ActorID {
	actor, err := time.ActorIDFromHex("0000000000000000000000cc")
	if err != nil {
		panic(err)
	}
	return actor
}()

// keepDetached stamps node with a merge at a ticket of keeperActor that a
// collection's vector has not seen, so a subtree holding node stays after a
// purge takes it out, as one holding a node a concurrent merge moved in
// stays (see Root.dropDetached), for the tests of what resolves against a
// detached subtree. Only the drop reads MergedAt without MergedFrom, so
// nothing else the tests check sees the stamp. A vector that names
// keeperActor at keptUntil lets the subtree go.
func keepDetached(node *TreeNode) {
	node.MergedAt = time.NewTicket(keptUntil, 0, keeperActor)
}

// keptUntil is the lamport of keepDetached's stamp.
const keptUntil = 1000

// detachFixture builds <r><div><p>ab</p></div></r> with div and p removed and
// the text live under them, the shape an identity restore leaves. p is
// stamped (see keepDetached), so the subtree stays after a purge.
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
	keepDetached(p)
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
// would keep div for as long as a node no one can see stays live. Its
// bytes move to GC, where the replica holds them until a collection that
// has seen the subtree whole, stamp included (see keepDetached), lets it go.
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
	assert.Equal(t, 1, root.GarbageLen(), "the text is held, released")
	live, gc := root.DocSize().Live, root.DocSize().GC
	assert.Equal(t, base, live, "the text's bytes left Live with it")
	assert.Equal(t, text.DataSize(), gc, "for GC, as the tombstone it is now")

	vector.Set(keeperActor, keptUntil)
	_, err = root.GarbageCollect(vector)
	require.NoError(t, err)
	assert.Nil(t, tree.DetachedNode(text.id), "a collection that has seen p's stamp lets it go")
	assert.Equal(t, 0, root.GarbageLen())
	gc = root.DocSize().GC
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
		// The attached tree alone, as a snapshot holds it: no detached node.
		rootCopy, err := tree.Root().DeepCopy()
		require.NoError(t, err)
		attachedOnly := NewTree(rootCopy, tree.createdAt)

		untombstoned, recreated, _, _, _, err := tree.Restore(spans, false, unaware)
		require.NoError(t, err)
		assert.Empty(t, untombstoned, "nothing detached is revived in place")
		assert.Len(t, recreated, 3)
		assert.True(t, p.IsRemoved())
		assert.True(t, text.IsRemoved())
		assert.Equal(t, "<r><div><p>ab</p></div></r>", tree.ToXML())

		// A copy of the attached tree, which holds no detached node, ends
		// the same.
		_, _, _, _, _, err = attachedOnly.Restore(spans, false, unaware)
		require.NoError(t, err)
		assert.Equal(t, tree.ToXML(), attachedOnly.ToXML())
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

	assert.Equal(t, 2, collect(50), "barrier covered: div goes, and the text it released")
	assert.True(t, text.IsRemoved())
	assert.True(t, text.detached)
	assert.Nil(t, tree.DetachedNode(text.id), "the vector has seen the subtree whole: one call lets it go")
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
		_, err = tree.purgeChild(div)
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
	keepDetached(n["p"])
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

// TestPurgedSlotIsAChildOfItsParent pins that the sibling purgedSlot
// returns is a child of the parent it returns, or nil for the end. A merge
// that moved the sibling the subtree was purged in front of out of the
// parent leaves the recorded one under another parent.
func TestPurgedSlotIsAChildOfItsParent(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	lamport := int64(0)
	next := func() *time.Ticket {
		lamport++
		return time.NewTicket(lamport, 0, actor)
	}
	mk := func(typ string, value ...string) *TreeNode {
		return NewTreeNode(NewTreeNodeID(next(), 0), typ, nil, value...)
	}
	root, p, gone, s, q := mk("r"), mk("p"), mk("text", "ab"), mk("text", "cd"), mk("q")
	require.NoError(t, root.Append(p, q))
	require.NoError(t, p.Append(gone, s))
	tree := NewTree(root, next())
	gone.remove(next())
	require.NoError(t, tree.Purge(gone))
	require.Same(t, s, gone.purgedBefore)

	require.NoError(t, p.Index.RemoveChild(s.Index))
	require.NoError(t, q.Append(s))
	parent, before, ok := tree.purgedSlot(gone)
	require.True(t, ok)
	assert.Same(t, p, parent)
	assert.Nil(t, before, "s now sits under q")
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

// TestADetachedNodeCarriesIndexedLineage pins that a node placed in a
// detached subtree is indexed by the source its MergedFrom names, as it would
// be on a replica where the subtree is still attached, so a restore of that
// source decides its pointer alike on both.
func TestADetachedNodeCarriesIndexedLineage(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	root := NewTreeNode(NewTreeNodeID(time.NewTicket(1, 0, actor), 0), "r", nil)
	tree := NewTree(root, time.NewTicket(2, 0, actor))
	source := NewTreeNodeID(time.NewTicket(3, 0, actor), 0)
	node := NewTreeNode(NewTreeNodeID(time.NewTicket(4, 0, actor), 0), "p", nil)
	node.MergedFrom = source
	node.detached = true
	tree.putNode(node)
	_, stamped := tree.mergeStamps[source.toIDString()][node]
	assert.True(t, stamped)
}

// TestRangeBetweenTwoDetachedSubtrees pins a range whose two ends sit in two
// different purged subtrees with attached content between them. In
// <r><a><pa>ab</pa></a><m>MM</m><b><pb>cd</pb></b></r>, with a and b
// removed while pa and pb are live, one replica purges a and b and the
// other does not. A delete and a style from the end of "ab" to the end of
// "cd" each resolve their ends where the subtrees sit, so both reach m on
// both replicas, and the two agree once both collect.
func TestRangeBetweenTwoDetachedSubtrees(t *testing.T) {
	build := func(t *testing.T, purge bool) (*Tree, *Root, map[string]*TreeNode, func() *time.Ticket) {
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
		require.NoError(t, root.Append(mk("a", "a"), mk("m", "m"), mk("b", "b")))
		require.NoError(t, n["a"].Append(mk("pa", "pa")))
		require.NoError(t, n["pa"].Append(mk("ab", "text", "ab")))
		require.NoError(t, n["m"].Append(mk("MM", "text", "MM")))
		require.NoError(t, n["b"].Append(mk("pb", "pb")))
		require.NoError(t, n["pb"].Append(mk("cd", "text", "cd")))
		keepDetached(n["pa"])
		keepDetached(n["pb"])
		tree := NewTree(root, next())
		doc := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
		removedAt := next()
		for _, name := range []string{"a", "b"} {
			n[name].remove(removedAt)
			size := n[name].DataSize()
			doc.RegisterGCPair(GCPair{Parent: tree, Child: n[name], GCOnlySize: &size})
		}
		if purge {
			vector := time.NewVersionVector()
			vector.Set(actor, lamport)
			_, err := doc.GarbageCollect(vector)
			require.NoError(t, err)
			require.True(t, n["pa"].detached)
			require.True(t, n["pb"].detached)
		}
		return tree, doc, n, next
	}

	for _, style := range []bool{false, true} {
		var shapes, attrs []string
		for _, purge := range []bool{true, false} {
			tree, doc, n, next := build(t, purge)
			actor := n["r"].id.CreatedAt.ActorID()
			vv := time.NewVersionVector()
			vv.Set(actor, 1000)
			from := NewTreePos(n["pa"].id, NewTreeNodeID(n["ab"].id.CreatedAt, 2))
			to := NewTreePos(n["pb"].id, NewTreeNodeID(n["cd"].id.CreatedAt, 2))
			at := time.NewTicket(500, 0, actor)
			if style {
				_, _, _, err := tree.Style(from, to, map[string]string{"k": "v"}, at, vv)
				require.NoError(t, err)
				assert.Equal(t, `<r><m k="v">MM</m></r>`, tree.ToXML(), "purged %v", purge)
			} else {
				pairs, _, _, err := tree.Edit(from, to, nil, 0, at, next, vv, false)
				require.NoError(t, err)
				assert.Equal(t, "<r></r>", tree.ToXML(), "purged %v", purge)
				for _, pair := range pairs {
					doc.RegisterGCPair(pair)
				}
			}
			_, err := doc.GarbageCollect(vv)
			require.NoError(t, err)
			shapes = append(shapes, treeShapeOf(tree))
			attrs = append(attrs, n["m"].Attributes())
		}
		assert.Equal(t, shapes[0], shapes[1], "style %v: the same tree once both collect", style)
		assert.Equal(t, attrs[0], attrs[1], "style %v: the same attributes on m", style)
	}
}

// TestSplitMovedAcrossTheDetachedBoundary pins §7.4's move of an empty split
// product next to its InsNext sibling when the two sit on different sides of
// a purge. n, in a, and its earlier split product s, in b, are linked; one of
// a and b is purged. Splitting n at its end moves the new product before s,
// and the product takes s's side: detached and a tombstone inside a purged
// subtree, attached outside one, and indexed where its side says, so
// NodeMapByID still holds exactly what is reachable from the root.
func TestSplitMovedAcrossTheDetachedBoundary(t *testing.T) {
	for _, purged := range []string{"a", "b"} {
		t.Run("purged "+purged, func(t *testing.T) {
			actor, err := time.ActorIDFromHex("000000000000000000000001")
			require.NoError(t, err)
			lamport := int64(0)
			next := func() *time.Ticket {
				lamport++
				return time.NewTicket(lamport, 0, actor)
			}
			root := NewTreeNode(NewTreeNodeID(next(), 0), "r", nil)
			a := NewTreeNode(NewTreeNodeID(next(), 0), "a", nil)
			b := NewTreeNode(NewTreeNodeID(next(), 0), "b", nil)
			n := NewTreeNode(NewTreeNodeID(next(), 0), "p", nil)
			x := NewTreeNode(NewTreeNodeID(next(), 0), "text", nil, "x")
			s := NewTreeNode(NewTreeNodeID(next(), 0), "p", nil)
			require.NoError(t, root.Append(a, b))
			require.NoError(t, a.Append(n))
			require.NoError(t, n.Append(x))
			require.NoError(t, b.Append(s))
			n.InsNextID, s.InsPrevID = s.id, n.id
			tree := NewTree(root, next())
			gone := map[string]*TreeNode{"a": a, "b": b}[purged]
			removedAt := next()
			gone.remove(removedAt)
			if purged == "a" {
				n.remove(removedAt)
				x.remove(removedAt)
			} else {
				s.remove(removedAt)
			}
			_, err = tree.purgeChild(gone)
			require.NoError(t, err)

			known := time.NewVersionVector()
			known.Set(actor, 1000)
			_, err = n.Split(tree, 1, next, known)
			require.NoError(t, err)
			product := s.Index.Parent.Children(true)[0].Value
			require.NotSame(t, s, product, "the product sits before s")
			assert.Equal(t, s.detached, product.detached, "the product takes s's side")
			if product.detached {
				assert.True(t, product.IsRemoved(), "a detached product is a tombstone")
				assert.True(t, product.released, "released, as the purge released what it found there")
				// Its pair books what Rederive derives for it: its size.
				var booked *resource.DataSize
				for _, pair := range tree.drainPendingGCPairs() {
					if pair.Child == product {
						booked = pair.GCOnlySize
					}
				}
				require.NotNil(t, booked)
				assert.Equal(t, product.DataSize(), *booked)
			}
			assert.Equal(t, len(tree.Nodes()), tree.NodeLen(), "NodeMapByID holds only what is reachable")
			assert.Same(t, product, tree.findFloorNode(product.id))
		})
	}
}

// TestSplitOutOfSightMovesOutATombstone pins the empty product §7.4 moves
// out of a node under a removed ancestor. In <r><top><p>ab</p></top><q><p>cd</p></q></r>
// with top removed while p is live, the second paragraph is p's earlier
// split product. A late split at the end of p cuts an empty product that
// §7.4 moves next to it, into q. One replica purged top, so p is released
// there and the product is a tombstone; the other did not, and the product
// must be born a tombstone there too, or it shows as an empty paragraph on
// that replica alone.
func TestSplitOutOfSightMovesOutATombstone(t *testing.T) {
	var xmls, shapes []string
	for _, purge := range []bool{true, false} {
		actor, err := time.ActorIDFromHex("000000000000000000000001")
		require.NoError(t, err)
		lamport := int64(0)
		next := func() *time.Ticket {
			lamport++
			return time.NewTicket(lamport, 0, actor)
		}
		root := NewTreeNode(NewTreeNodeID(next(), 0), "r", nil)
		top := NewTreeNode(NewTreeNodeID(next(), 0), "top", nil)
		p := NewTreeNode(NewTreeNodeID(next(), 0), "p", nil)
		ab := NewTreeNode(NewTreeNodeID(next(), 0), "text", nil, "ab")
		q := NewTreeNode(NewTreeNodeID(next(), 0), "q", nil)
		p2 := NewTreeNode(NewTreeNodeID(p.id.CreatedAt, 1), "p", nil)
		cd := NewTreeNode(NewTreeNodeID(next(), 0), "text", nil, "cd")
		p.InsNextID, p2.InsPrevID = p2.id, p.id
		require.NoError(t, root.Append(top, q))
		require.NoError(t, top.Append(p))
		require.NoError(t, p.Append(ab))
		require.NoError(t, q.Append(p2))
		require.NoError(t, p2.Append(cd))
		tree := NewTree(root, next())
		top.remove(next())
		if purge {
			_, err := tree.purgeChild(top)
			require.NoError(t, err)
		}

		vv := time.NewVersionVector()
		vv.Set(actor, 1000)
		pos := NewTreePos(p.id, NewTreeNodeID(ab.id.CreatedAt, 2))
		_, diff, _, err := tree.Edit(pos, pos, nil, 1, next(), next, vv, false)
		require.NoError(t, err)
		assert.Zero(t, diff.Total(), "purged %v: nothing charged to Live", purge)
		require.Len(t, q.Children(true), 2, "purged %v: the product moved into q", purge)
		assert.True(t, q.Children(true)[0].IsRemoved(), "purged %v: as a tombstone", purge)
		xmls = append(xmls, tree.ToXML())
		if !purge {
			_, err := tree.purgeChild(top)
			require.NoError(t, err)
		}
		shapes = append(shapes, treeShapeOf(tree))
	}
	assert.Equal(t, "<r><q><p>cd</p></q></r>", xmls[0])
	assert.Equal(t, xmls[0], xmls[1], "both replicas show the same tree")
	assert.Equal(t, shapes[0], shapes[1], "and hold the same once both collect")
}

// TestSplitOutOfSightKeepsTheLedger pins the ledger of the §7.4 product
// born a tombstone: the size the split books has to include the removal
// ticket the tombstone carries, or GC drifts from a rebuild by one ticket.
// The fixture is TestSplitOutOfSightMovesOutATombstone's, on a Root.
func TestSplitOutOfSightKeepsTheLedger(t *testing.T) {
	for _, purge := range []bool{true, false} {
		actor, err := time.ActorIDFromHex("000000000000000000000001")
		require.NoError(t, err)
		lamport := int64(0)
		next := func() *time.Ticket {
			lamport++
			return time.NewTicket(lamport, 0, actor)
		}
		doc := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
		root := NewTreeNode(NewTreeNodeID(next(), 0), "r", nil)
		top := NewTreeNode(NewTreeNodeID(next(), 0), "top", nil)
		p := NewTreeNode(NewTreeNodeID(next(), 0), "p", nil)
		ab := NewTreeNode(NewTreeNodeID(next(), 0), "text", nil, "ab")
		q := NewTreeNode(NewTreeNodeID(next(), 0), "q", nil)
		p2 := NewTreeNode(NewTreeNodeID(p.id.CreatedAt, 1), "p", nil)
		cd := NewTreeNode(NewTreeNodeID(next(), 0), "text", nil, "cd")
		p.InsNextID, p2.InsPrevID = p2.id, p.id
		require.NoError(t, root.Append(top, q))
		require.NoError(t, top.Append(p))
		require.NoError(t, p.Append(ab))
		keepDetached(p)
		require.NoError(t, q.Append(p2))
		require.NoError(t, p2.Append(cd))
		top.remove(next())
		tree := NewTree(root, next())
		doc.Object().Set("t", tree)
		doc.RegisterElement(tree, doc.Object())
		vv := time.NewVersionVector()
		vv.Set(actor, 1000)
		if purge {
			_, err := doc.GarbageCollect(vv)
			require.NoError(t, err)
			require.True(t, p.detached)
		}

		pos := NewTreePos(p.id, NewTreeNodeID(ab.id.CreatedAt, 2))
		pairs, diff, _, err := tree.Edit(pos, pos, nil, 1, next(), next, vv, false)
		require.NoError(t, err)
		for _, pair := range pairs {
			doc.RegisterGCPair(pair)
		}
		doc.Acc(diff)
		rebuilt, err := doc.Rederive()
		require.NoError(t, err)
		assert.Equal(t, rebuilt.DocSize(), doc.DocSize(), "purged %v: after the split", purge)

		_, err = doc.GarbageCollect(vv)
		require.NoError(t, err)
		rebuilt, err = doc.Rederive()
		require.NoError(t, err)
		assert.Equal(t, rebuilt.DocSize(), doc.DocSize(), "purged %v: after collecting", purge)
		assert.Equal(t, rebuilt.GarbageLen(), doc.GarbageLen(), "purged %v", purge)
	}
}

// TestToIndexUnderARemovedAncestorKeepsItsIndex pins that only the range an
// operation acts on resolves a live parent under a removed ancestor from the
// topmost removed one. ToIndex, which reports positions to users, still
// counts the parent where it sits, as it always has.
func TestToIndexUnderARemovedAncestorKeepsItsIndex(t *testing.T) {
	tree, _, n, _ := halfDetachedFixture(t, false)
	p, div := n["p"], n["div"]

	reported, err := tree.ToIndex(p, p)
	require.NoError(t, err)
	where, err := tree.IndexTree.IndexOf(&index.TreePos[*TreeNode]{Node: p.Index, Offset: 0}, false)
	require.NoError(t, err)
	assert.Equal(t, where, reported, "ToIndex counts p where it sits")

	ranged, err := tree.rangeIndex(p, p, false)
	require.NoError(t, err)
	offset, err := tree.Root().Index.FindOffset(div.Index, false)
	require.NoError(t, err)
	at, err := tree.IndexTree.IndexOf(&index.TreePos[*TreeNode]{Node: tree.Root().Index, Offset: offset}, false)
	require.NoError(t, err)
	assert.Equal(t, at, ranged, "a range end counts from div's place")
	assert.NotEqual(t, reported, ranged, "the two resolutions differ here")
}

// TestDropGCPairGivesBackThePurgeAmount pins Root.DropGCPair's accounting:
// it takes out of GC what purging the child would, its size, for an
// ordinary pair and for one booked with GCOnlySize alike, leaves a pair
// registered under the key for another child alone, and is a no-op when
// nothing is registered. TestRestoreAfterDetachKeepsTheLedger pins why the
// GCOnlySize case is the size too: a split piece's bytes are in GC under its
// sibling's charge by the time the piece is forgotten.
func TestDropGCPairGivesBackThePurgeAmount(t *testing.T) {
	tree, _, p, text, removedAt := detachFixture(t)
	text.remove(removedAt)
	doc := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
	base := doc.DocSize()

	doc.docSize.Live.Add(text.DataSize())
	doc.RegisterGCPair(GCPair{Parent: tree, Child: text})
	doc.DropGCPair(GCPair{Parent: tree, Child: text})
	assert.Equal(t, 0, doc.GarbageLen())
	assert.Equal(t, base.GC, doc.DocSize().GC, "an ordinary pair gives back the child's size")

	// Booked with less than its size, as a piece split off a removed node
	// is: the drop still gives back the size, which is what is held for it.
	split := resource.DataSize{Data: 0, Meta: 3}
	require.NotEqual(t, p.DataSize(), split)
	doc.RegisterGCPair(GCPair{Parent: tree, Child: p, GCOnlySize: &split})
	doc.DropGCPair(GCPair{Parent: tree, Child: p})
	assert.Equal(t, 0, doc.GarbageLen())
	want := base.GC
	want.Add(split)
	want.Sub(p.DataSize())
	assert.Equal(t, want, doc.DocSize().GC, "a GCOnlySize pair gives back the child's size, not its booking")

	booked := resource.DataSize{Data: 7, Meta: 3}
	other := NewTreeNode(p.id, "p", nil)
	other.remove(removedAt)
	doc.RegisterGCPair(GCPair{Parent: tree, Child: p, GCOnlySize: &booked})
	doc.DropGCPair(GCPair{Parent: tree, Child: other})
	assert.Equal(t, 1, doc.GarbageLen(), "another child's pair under the key stays")

	doc.DropGCPair(GCPair{Parent: tree, Child: text})
	assert.Equal(t, 1, doc.GarbageLen(), "dropping an unregistered pair does nothing")
}

// TestRelinkRecreatedTextSkipsDetachedPieces pins that a recreated attached
// piece is spliced into its insertion's chain past detached pieces, as on a
// replica that purged them and joined their neighbours: "a" is attached,
// "b" detached, and the recreated "c" links to "a" and leaves "b" alone.
func TestRelinkRecreatedTextSkipsDetachedPieces(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	root := NewTreeNode(NewTreeNodeID(time.NewTicket(1, 0, actor), 0), "r", nil)
	p := NewTreeNode(NewTreeNodeID(time.NewTicket(2, 0, actor), 0), "p", nil)
	require.NoError(t, root.Append(p))
	tree := NewTree(root, time.NewTicket(3, 0, actor))
	insertion := time.NewTicket(4, 0, actor)
	a := NewTreeNode(NewTreeNodeID(insertion, 0), "text", nil, "a")
	b := NewTreeNode(NewTreeNodeID(insertion, 1), "text", nil, "b")
	c := NewTreeNode(NewTreeNodeID(insertion, 2), "text", nil, "c")
	require.NoError(t, p.Append(a, c))
	b.detached = true
	b.remove(time.NewTicket(5, 0, actor))
	a.InsNextID, b.InsPrevID = b.id, a.id
	tree.putNode(a)
	tree.putNode(b)
	tree.putNode(c)

	tree.relinkRecreatedText(c)
	assert.Equal(t, c.id, a.InsNextID)
	assert.Equal(t, a.id, c.InsPrevID)
	assert.Nil(t, c.InsNextID)
	assert.Equal(t, a.id, b.InsPrevID, "the detached piece is left alone")
}

// TestSplitIntoADetachedParentWithoutARemoval pins the §7.4 fallback: a
// detached destination always carries a removal, since a purge takes out
// only a tombstone. Built by hand without one, the split still applies -- an
// error would fail every later replay of a change the server already stored
// -- and the product it moves there is tombstoned with its own creation
// ticket, so no live node sits in a detached subtree.
func TestSplitIntoADetachedParentWithoutARemoval(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	lamport := int64(0)
	next := func() *time.Ticket {
		lamport++
		return time.NewTicket(lamport, 0, actor)
	}
	root := NewTreeNode(NewTreeNodeID(next(), 0), "r", nil)
	a := NewTreeNode(NewTreeNodeID(next(), 0), "a", nil)
	b := NewTreeNode(NewTreeNodeID(next(), 0), "b", nil)
	n := NewTreeNode(NewTreeNodeID(next(), 0), "p", nil)
	x := NewTreeNode(NewTreeNodeID(next(), 0), "text", nil, "x")
	s := NewTreeNode(NewTreeNodeID(next(), 0), "p", nil)
	require.NoError(t, root.Append(a, b))
	require.NoError(t, a.Append(n))
	require.NoError(t, n.Append(x))
	require.NoError(t, b.Append(s))
	n.InsNextID, s.InsPrevID = s.id, n.id
	tree := NewTree(root, next())
	b.detached, s.detached = true, true

	known := time.NewVersionVector()
	known.Set(actor, 1000)
	inB := len(b.Index.Children(true))
	_, err = n.Split(tree, 1, next, known)
	require.NoError(t, err)

	require.Len(t, b.Index.Children(true), inB+1, "the product moved into b")
	product := b.Index.Children(true)[0].Value
	assert.True(t, product.detached, "it takes b's side")
	require.NotNil(t, product.removedAt, "a detached node is a tombstone")
	assert.Equal(t, product.id.CreatedAt, product.removedAt, "tombstoned with its own creation ticket")
	assert.Same(t, product, tree.DetachedNode(product.id))
}

// TestPurgeOfALiveNodeChangesNothing pins that a purge only takes out a
// tombstone: asked to purge a live node, Purge and purgeChild leave the
// tree, the indexes and every node as they were, and report nothing
// released.
func TestPurgeOfALiveNodeChangesNothing(t *testing.T) {
	tree, div, p, text, _ := detachFixture(t)
	div.removedAt, p.removedAt = nil, nil
	div.Index.UpdateDescendantsLength()
	tree.Root().Index.UpdateDescendantsLength()
	before := tree.ToXML()

	require.NoError(t, tree.Purge(div))
	result, err := tree.purgeChild(div)
	require.NoError(t, err)
	assert.False(t, result.Purged, "not purged, so its pair stays")
	assert.Zero(t, result.Released.Total(), "nothing released")
	assert.Equal(t, before, tree.ToXML())
	assert.Same(t, tree.Root(), div.Index.Parent.Value, "div stays in the tree")
	for _, n := range []*TreeNode{div, p, text} {
		assert.False(t, n.detached, "%s is not detached", n.Type())
		assert.False(t, n.IsRemoved(), "%s stays live", n.Type())
	}
	assert.Equal(t, len(tree.Nodes()), tree.NodeLen())
}

// TestTakeForgottenReportsTheLastRestoreAlone pins that each Restore starts
// the forgotten list afresh: a caller that never takes the first Restore's
// list does not leave its nodes for a later caller to drop pairs for.
func TestTakeForgottenReportsTheLastRestoreAlone(t *testing.T) {
	tree, div, p, text, removedAt := detachFixture(t)
	text.remove(removedAt)
	require.NoError(t, tree.Purge(div))
	other, err := time.ActorIDFromHex("0000000000000000000000ee")
	require.NoError(t, err)
	unaware := time.NewVersionVector()
	unaware.Set(other, 1)

	_, _, _, _, _, err = tree.Restore([]*TreeRestoreSpan{
		{ID: p.id, NodeType: "p", Length: 1, ParentID: div.id},
	}, false, unaware)
	require.NoError(t, err)
	_, _, _, _, _, err = tree.Restore([]*TreeRestoreSpan{
		{ID: text.id, NodeType: "text", IsText: true, Length: 2, Value: "ab", ParentID: p.id},
	}, false, unaware)
	require.NoError(t, err)

	forgotten := tree.TakeForgotten()
	require.Len(t, forgotten, 1, "only the second Restore's")
	assert.Same(t, text, forgotten[0])
	assert.Empty(t, tree.TakeForgotten(), "and taking it clears it")
}

// TestRelinkRecreatedTextLeavesADetachedSuccessor pins the successor side of
// the relink: a recreated attached piece with no surviving predecessor does
// not link to a detached piece at its exact end offset, as a replica that
// purged that piece has nothing there to link to.
func TestRelinkRecreatedTextLeavesADetachedSuccessor(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	root := NewTreeNode(NewTreeNodeID(time.NewTicket(1, 0, actor), 0), "r", nil)
	p := NewTreeNode(NewTreeNodeID(time.NewTicket(2, 0, actor), 0), "p", nil)
	require.NoError(t, root.Append(p))
	tree := NewTree(root, time.NewTicket(3, 0, actor))
	insertion := time.NewTicket(4, 0, actor)
	a := NewTreeNode(NewTreeNodeID(insertion, 0), "text", nil, "a")
	b := NewTreeNode(NewTreeNodeID(insertion, 1), "text", nil, "b")
	require.NoError(t, p.Append(a))
	b.detached = true
	b.remove(time.NewTicket(5, 0, actor))
	tree.putNode(a)
	tree.putNode(b)

	tree.relinkRecreatedText(a)
	assert.Nil(t, a.InsNextID, "the recreated piece does not link to the detached one")
	assert.Nil(t, b.InsPrevID, "and the detached one is left alone")
}

// TestRegisterGCPairCancelsAGCOnlyPairByItsBooking pins the RegisterGCPair
// branch DropGCPair's doc sets itself against: registering a pair booked with
// GCOnlySize a second time cancels it and gives back GCOnlySize, the amount
// the first registration put in, rather than the child's size.
func TestRegisterGCPairCancelsAGCOnlyPairByItsBooking(t *testing.T) {
	tree, _, p, _, _ := detachFixture(t)
	doc := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
	base := doc.DocSize()

	booked := resource.DataSize{Data: 7, Meta: 3}
	doc.RegisterGCPair(GCPair{Parent: tree, Child: p, GCOnlySize: &booked})
	gc := doc.DocSize().GC
	gc.Sub(base.GC)
	assert.Equal(t, booked, gc, "registration adds the booking")

	doc.RegisterGCPair(GCPair{Parent: tree, Child: p, GCOnlySize: &booked})
	assert.Equal(t, 0, doc.GarbageLen())
	assert.Equal(t, base.GC, doc.DocSize().GC, "cancelling gives the booking back")
}

// TestMergeIntoADetachedParentTakesItsSide pins the Phase 6 merge into a
// destination in a subtree a purge took out. In
// <r><x>12</x><div><p>ab</p></div><q>cd</q></r>, with div removed while p is
// live, a delete from the end of "ab" to the start of q merges q's "cd" into
// p. Where div is purged, p is detached and "cd" takes its side: detached,
// a tombstone, out of Live and indexed with the detached nodes. Where div is
// not, "cd" moves under the live p, out of sight, until that replica's own
// collection releases it. Both show the same tree, the ledger equals a
// rebuild on each, and the two hold the same once both collect.
func TestMergeIntoADetachedParentTakesItsSide(t *testing.T) {
	var xmls, shapes []string
	for _, purge := range []bool{true, false} {
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
		require.NoError(t, root.Append(mk("x", "x"), mk("div", "div"), mk("q", "q")))
		require.NoError(t, n["x"].Append(mk("12", "text", "12")))
		require.NoError(t, n["div"].Append(mk("p", "p")))
		require.NoError(t, n["p"].Append(mk("ab", "text", "ab")))
		keepDetached(n["p"])
		require.NoError(t, n["q"].Append(mk("cd", "text", "cd")))
		n["div"].remove(next())
		tree := NewTree(root, next())
		doc := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
		doc.Object().Set("t", tree)
		doc.RegisterElement(tree, doc.Object())
		vv := time.NewVersionVector()
		vv.Set(actor, 1000)
		if purge {
			_, err := doc.GarbageCollect(vv)
			require.NoError(t, err)
			require.True(t, n["p"].detached)
		}

		from := NewTreePos(n["p"].id, NewTreeNodeID(n["ab"].id.CreatedAt, 2))
		to := NewTreePos(n["q"].id, n["q"].id)
		pairs, diff, _, err := tree.Edit(from, to, nil, 0, time.NewTicket(500, 0, actor), next, vv, false)
		require.NoError(t, err)
		for _, pair := range pairs {
			doc.RegisterGCPair(pair)
		}
		doc.Acc(diff)
		assert.Same(t, n["p"], n["cd"].Index.Parent.Value, "purged %v: cd merged into p", purge)
		if purge {
			assert.True(t, n["cd"].detached, "cd takes the detached side")
			assert.True(t, n["cd"].IsRemoved(), "and is released")
			_, stamped := tree.mergeStamps[n["q"].id.toIDString()][n["cd"]]
			assert.True(t, stamped, "and keeps its merge stamp indexed, as an unpurged replica does")
			assert.Equal(t, len(tree.Nodes()), tree.NodeLen(), "NodeMapByID holds only what is reachable")
		}
		rebuilt, err := doc.Rederive()
		require.NoError(t, err)
		assert.Equal(t, rebuilt.DocSize().Live, doc.DocSize().Live, "purged %v: Live equals a rebuild", purge)
		xmls = append(xmls, tree.ToXML())

		_, err = doc.GarbageCollect(vv)
		require.NoError(t, err)
		rebuilt, err = doc.Rederive()
		require.NoError(t, err)
		assert.Equal(t, rebuilt.DocSize(), doc.DocSize(), "purged %v: after collecting", purge)
		shapes = append(shapes, treeShapeOf(tree))
	}
	assert.Equal(t, "<r><x>12</x></r>", xmls[0])
	assert.Equal(t, xmls[0], xmls[1], "both replicas show the same tree")
	assert.Equal(t, shapes[0], shapes[1], "and hold the same once both collect")
}

// TestInsertCopyOfADetachedNodeIsDropped pins that the duplicate-content
// check sees detached nodes: content from an older change carrying the ID of
// a released node is a copy of a node that already exists, so the edit drops
// it rather than put a second node under one ID, as it did while main kept
// detached nodes in NodeMapByID.
func TestInsertCopyOfADetachedNodeIsDropped(t *testing.T) {
	tree, _, p, ab, next := releasedFixture(t, true)
	require.True(t, ab.detached)
	copied := NewTreeNode(ab.id, "text", nil, "ab")
	at := next()
	known := time.NewVersionVector()
	known.Set(at.ActorID(), at.Lamport())
	pos := NewTreePos(tree.Root().id, tree.Root().id)
	_, _, _, err := tree.Edit(pos, pos, []*TreeNode{copied}, 0, at, next, known, false)
	require.NoError(t, err)
	assert.Equal(t, "<r><q>cd</q></r>", tree.ToXML(), "the copy is dropped")
	assert.Nil(t, copied.Index.Parent)
	assert.Same(t, ab, tree.findFloorNode(ab.id), "the ID still names the released node")
	assert.Same(t, p, ab.Index.Parent.Value)
}

// TestCollectLeavesAPairOfALiveNode pins that a collection neither purges
// nor books anything for a pair whose child is live: the pair stays, GC and
// Live keep their sizes, and the node stays in the tree.
func TestCollectLeavesAPairOfALiveNode(t *testing.T) {
	tree, div, _, _, removedAt := detachFixture(t)
	div.removedAt = nil
	doc := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
	size := div.DataSize()
	doc.RegisterGCPair(GCPair{Parent: tree, Child: div, GCOnlySize: &size})
	before := doc.DocSize()

	vector := time.NewVersionVector()
	vector.Set(removedAt.ActorID(), removedAt.Lamport())
	n, err := doc.GarbageCollect(vector)
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.Equal(t, 1, doc.GarbageLen(), "the pair stays")
	assert.Equal(t, before, doc.DocSize())
	assert.Same(t, tree.Root(), div.Index.Parent.Value)
}

// TestRelinkRecreatedTextNeverPointsAtItself pins the self-link guard: when
// the surviving predecessor still points at the recreated piece's own ID,
// left by an earlier node under it, the recreated piece does not take that
// as its successor.
func TestRelinkRecreatedTextNeverPointsAtItself(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	root := NewTreeNode(NewTreeNodeID(time.NewTicket(1, 0, actor), 0), "r", nil)
	p := NewTreeNode(NewTreeNodeID(time.NewTicket(2, 0, actor), 0), "p", nil)
	require.NoError(t, root.Append(p))
	tree := NewTree(root, time.NewTicket(3, 0, actor))
	insertion := time.NewTicket(4, 0, actor)
	a := NewTreeNode(NewTreeNodeID(insertion, 0), "text", nil, "a")
	b := NewTreeNode(NewTreeNodeID(insertion, 1), "text", nil, "b")
	require.NoError(t, p.Append(a, b))
	a.InsNextID = b.id
	tree.putNode(a)
	tree.putNode(b)

	tree.relinkRecreatedText(b)
	assert.Equal(t, b.id, a.InsNextID)
	assert.Equal(t, a.id, b.InsPrevID)
	assert.Nil(t, b.InsNextID, "b does not point at itself")
}

// TestInsertSubtreeUnderADetachedParent pins that every node of an inserted
// subtree, not only its root, takes the detached side of the parent it is
// placed under: detached, a tombstone, indexed with the detached nodes,
// booked to GC alone and charging nothing to Live.
func TestInsertSubtreeUnderADetachedParent(t *testing.T) {
	tree, _, p, ab, next := releasedFixture(t, true)
	at := next()
	element := NewTreeNode(NewTreeNodeID(at, 0), "b", nil)
	text := NewTreeNode(NewTreeNodeID(at, 1), "text", nil, "xy")
	require.NoError(t, element.Append(text))
	known := time.NewVersionVector()
	known.Set(at.ActorID(), at.Lamport())
	pos := NewTreePos(p.id, NewTreeNodeID(ab.id.CreatedAt, ab.Length()))

	pairs, diff, _, err := tree.Edit(pos, pos, []*TreeNode{element}, 0, at, next, known, false)
	require.NoError(t, err)
	assert.Zero(t, diff.Total(), "nothing charged to Live")
	require.Len(t, pairs, 2, "one pair per node")
	for _, pair := range pairs {
		assert.NotNil(t, pair.GCOnlySize, "booked to GC alone")
	}
	for _, n := range []*TreeNode{element, text} {
		assert.True(t, n.detached, "%s is detached", n.Type())
		assert.True(t, n.IsRemoved(), "%s is a tombstone", n.Type())
		assert.Same(t, n, tree.DetachedNode(n.id), "%s is indexed with the detached nodes", n.Type())
	}
	assert.Equal(t, len(tree.Nodes()), tree.NodeLen())
	assert.Equal(t, "<r><q>cd</q></r>", tree.ToXML())
}

// TestMergeIntoADetachedParentWithoutARemoval pins mergeNodes' twin of the
// §7.4 fallback: built by hand without a removal, a merge into a detached
// destination still applies, and what it moves there is tombstoned with the
// edit's ticket and taken off Live, so no live node sits in a detached
// subtree.
func TestMergeIntoADetachedParentWithoutARemoval(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	lamport := int64(0)
	next := func() *time.Ticket {
		lamport++
		return time.NewTicket(lamport, 0, actor)
	}
	root := NewTreeNode(NewTreeNodeID(next(), 0), "r", nil)
	dest := NewTreeNode(NewTreeNodeID(next(), 0), "p", nil)
	source := NewTreeNode(NewTreeNodeID(next(), 0), "p", nil)
	moved := NewTreeNode(NewTreeNodeID(next(), 0), "text", nil, "cd")
	require.NoError(t, root.Append(dest, source))
	require.NoError(t, source.Append(moved))
	tree := NewTree(root, next())
	dest.detached = true

	size := moved.DataSize()
	editedAt := next()
	_, released, err := tree.mergeNodes(dest, []*TreeNode{moved}, editedAt)
	require.NoError(t, err)
	assert.Equal(t, size, released, "its bytes leave Live")
	assert.Same(t, dest, moved.Index.Parent.Value, "it moved into dest")
	assert.True(t, moved.detached, "it takes dest's side")
	assert.Equal(t, editedAt, moved.removedAt, "tombstoned with the edit's ticket")
	assert.Same(t, moved, tree.DetachedNode(moved.id))
}

// TestSplitOfAReleasedTextKeepsTheLedger pins the ledger of a split inside
// a released subtree. A purge releases the live "ab" under the removed div,
// taking its bytes off Live and booking nothing. A late insert in the middle
// of "ab" splits it; the piece split off is released too, so it books no
// pair: one booked for its split overhead alone would give back the piece's
// whole size at its purge, which GC never held. After everything is
// collected, GC and Live equal a rebuild and nothing is left pending.
func TestSplitOfAReleasedTextKeepsTheLedger(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	removedAt := time.NewTicket(10, 0, actor)
	root := NewTreeNode(NewTreeNodeID(time.NewTicket(1, 0, actor), 0), "r", nil)
	div := NewTreeNode(NewTreeNodeID(time.NewTicket(2, 0, actor), 0), "div", nil)
	p := NewTreeNode(NewTreeNodeID(time.NewTicket(3, 0, actor), 0), "p", nil)
	text := NewTreeNode(NewTreeNodeID(time.NewTicket(4, 0, actor), 0), "text", nil, "ab")
	require.NoError(t, root.Append(div))
	require.NoError(t, div.Append(p))
	require.NoError(t, p.Append(text))
	keepDetached(p)
	div.remove(removedAt)
	tree := NewTree(root, time.NewTicket(5, 0, actor))
	doc := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
	doc.Object().Set("t", tree)
	doc.RegisterElement(tree, doc.Object())
	all := time.NewVersionVector()
	all.Set(removedAt.ActorID(), time.MaxLamport)
	_, err = doc.GarbageCollect(all)
	require.NoError(t, err)
	require.True(t, text.released, "the text is released with div")

	at := time.NewTicket(removedAt.Lamport()+10, 0, removedAt.ActorID())
	issue := func() *time.Ticket { return at }
	pos := NewTreePos(p.id, NewTreeNodeID(text.id.CreatedAt, 1))
	content := NewTreeNode(NewTreeNodeID(at, 0), "text", nil, "X")
	pairs, diff, _, err := tree.Edit(pos, pos, []*TreeNode{content}, 0, at, issue, all, false)
	require.NoError(t, err)
	require.Len(t, p.Children(true), 3, "ab was split around the insert")
	assert.True(t, p.Children(true)[2].released, "the piece split off is released too")
	for _, pair := range pairs {
		doc.RegisterGCPair(pair)
	}
	doc.Acc(diff)

	all.Set(keeperActor, keptUntil)
	_, err = doc.GarbageCollect(all)
	require.NoError(t, err)
	rebuilt, err := doc.Rederive()
	require.NoError(t, err)
	assert.Equal(t, rebuilt.DocSize(), doc.DocSize(), "GC and Live equal a rebuild")
	assert.Equal(t, 0, doc.GarbageLen(), "nothing is left pending")
}

// TestPutNodeKeepsALiveDetachedNodeOverATombstone pins that putNode's
// duplicate-ID rule holds for the detached index as for NodeMapByID: a live
// node keeps its ID over a tombstone put after it under the same ID.
func TestPutNodeKeepsALiveDetachedNodeOverATombstone(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	root := NewTreeNode(NewTreeNodeID(time.NewTicket(1, 0, actor), 0), "r", nil)
	tree := NewTree(root, time.NewTicket(2, 0, actor))
	id := NewTreeNodeID(time.NewTicket(3, 0, actor), 0)
	live := NewTreeNode(id, "p", nil)
	live.detached = true
	tomb := NewTreeNode(id, "p", nil)
	tomb.detached = true
	tomb.remove(time.NewTicket(4, 0, actor))

	tree.putNode(live)
	tree.putNode(tomb)
	assert.Same(t, live, tree.DetachedNode(id), "the live node keeps the ID")
}

// TestSplitOfAMergedInReleasedTextKeepsTheLedger pins the released mark a
// merge into a detached parent leaves: "cd", merged into the detached p, is
// released with it, so a later split of "cd" books nothing for the piece
// cut off, and after everything is collected GC and Live equal a rebuild
// with nothing left pending.
func TestSplitOfAMergedInReleasedTextKeepsTheLedger(t *testing.T) {
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
	require.NoError(t, root.Append(mk("div", "div"), mk("q", "q")))
	require.NoError(t, n["div"].Append(mk("p", "p")))
	require.NoError(t, n["p"].Append(mk("ab", "text", "ab")))
	keepDetached(n["p"])
	require.NoError(t, n["q"].Append(mk("cd", "text", "cd")))
	n["div"].remove(next())
	tree := NewTree(root, next())
	doc := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
	doc.Object().Set("t", tree)
	doc.RegisterElement(tree, doc.Object())
	all := time.NewVersionVector()
	all.Set(actor, time.MaxLamport)
	_, err = doc.GarbageCollect(all)
	require.NoError(t, err)
	require.True(t, n["p"].detached)

	apply := func(from, to *TreePos, contents ...*TreeNode) {
		t.Helper()
		at := next()
		pairs, diff, _, err := tree.Edit(from, to, contents, 0, at, next, all, false)
		require.NoError(t, err)
		for _, pair := range pairs {
			doc.RegisterGCPair(pair)
		}
		doc.Acc(diff)
	}
	// Merge q's "cd" into the detached p, then split "cd" with an insert.
	apply(NewTreePos(n["p"].id, NewTreeNodeID(n["ab"].id.CreatedAt, 2)), NewTreePos(n["q"].id, n["q"].id))
	assert.True(t, n["cd"].released, "cd is released into p")
	mid := NewTreePos(n["p"].id, NewTreeNodeID(n["cd"].id.CreatedAt, 1))
	apply(mid, mid, NewTreeNode(NewTreeNodeID(time.NewTicket(lamport+100, 0, actor), 0), "text", nil, "X"))

	all.Set(keeperActor, keptUntil)
	_, err = doc.GarbageCollect(all)
	require.NoError(t, err)
	rebuilt, err := doc.Rederive()
	require.NoError(t, err)
	assert.Equal(t, rebuilt.DocSize(), doc.DocSize(), "GC and Live equal a rebuild")
	assert.Equal(t, 0, doc.GarbageLen(), "nothing is left pending")
}

// TestPurgedSlotEndsOnACycle pins that resolving a position through purge
// slots terminates when the recorded slots loop: a and b are detached roots,
// each recorded as purged from the other, and the lookup fails with
// ErrNodeNotFound instead of spinning. The same goes for a loop among the
// recorded siblings.
func TestPurgedSlotEndsOnACycle(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	root := NewTreeNode(NewTreeNodeID(time.NewTicket(1, 0, actor), 0), "r", nil)
	tree := NewTree(root, time.NewTicket(2, 0, actor))
	removedAt := time.NewTicket(3, 0, actor)
	node := func(lamport int64) *TreeNode {
		n := NewTreeNode(NewTreeNodeID(time.NewTicket(lamport, 0, actor), 0), "p", nil)
		n.remove(removedAt)
		n.detached = true
		return n
	}
	finish := func(name string, fn func()) {
		t.Helper()
		done := make(chan struct{})
		go func() {
			defer close(done)
			fn()
		}()
		select {
		case <-done:
		case <-gotime.After(10 * gotime.Second):
			t.Fatalf("%s did not terminate on a cycle of purge slots", name)
		}
	}

	a, b := node(4), node(5)
	a.purgedFrom, b.purgedFrom = b, a
	finish("parents", func() {
		_, err := tree.ToIndex(a, a)
		assert.ErrorIs(t, err, ErrNodeNotFound)
	})

	parent := node(6)
	parent.detached = false
	parent.removedAt = nil
	require.NoError(t, root.Append(parent))
	c, x, y := node(7), node(8), node(9)
	c.purgedFrom, c.purgedBefore = parent, x
	x.purgedFrom, x.purgedBefore = parent, y
	y.purgedFrom, y.purgedBefore = parent, x
	finish("siblings", func() {
		_, err := tree.ToIndex(c, c)
		assert.ErrorIs(t, err, ErrNodeNotFound)
	})
}

// TestDeleteCascadeSkipsADetachedSplitProduct pins the forward §4.1 cascade
// against a split product a purge took out. A peer's split cut
// <r><div><p>ab</p></div></r> into div and div', the product p' carrying
// "b"; div' was then removed while p' stayed live under it. One replica
// purges div', releasing p' and "b"; the other does not. A delete of div
// that did not know the split cascades to p'. The purged replica walks past
// the detached p', with nothing to remove there; the other removes the
// still-live p'. Both show the same tree, keep a ledger equal to a rebuild,
// and hold the same once both collect.
func TestDeleteCascadeSkipsADetachedSplitProduct(t *testing.T) {
	var xmls, shapes []string
	for _, purge := range []bool{true, false} {
		actor, err := time.ActorIDFromHex("000000000000000000000001")
		require.NoError(t, err)
		peer, err := time.ActorIDFromHex("0000000000000000000000ff")
		require.NoError(t, err)
		lamport := int64(0)
		next := func() *time.Ticket {
			lamport++
			return time.NewTicket(lamport, 0, actor)
		}
		root := NewTreeNode(NewTreeNodeID(next(), 0), "r", nil)
		div := NewTreeNode(NewTreeNodeID(next(), 0), "div", nil)
		p := NewTreeNode(NewTreeNodeID(next(), 0), "p", nil)
		require.NoError(t, p.Append(NewTreeNode(NewTreeNodeID(next(), 0), "text", nil, "ab")))
		require.NoError(t, div.Append(p))
		require.NoError(t, root.Append(div))
		tree := NewTree(root, next())
		unaware := time.NewVersionVector()
		unaware.Set(actor, lamport+100)

		peerLamport := lamport
		peerTicket := func() *time.Ticket {
			peerLamport++
			return time.NewTicket(peerLamport, 0, peer)
		}
		_, _, err = tree.EditT(3, 3, nil, 2, peerTicket(), peerTicket)
		require.NoError(t, err)
		product := tree.Root().Children()[1]
		productP := product.Children()[0]
		product.remove(peerTicket())

		doc := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
		doc.Object().Set("t", tree)
		doc.RegisterElement(tree, doc.Object())
		all := time.NewVersionVector()
		all.Set(actor, time.MaxLamport)
		all.Set(peer, time.MaxLamport)
		if purge {
			_, err := doc.GarbageCollect(all)
			require.NoError(t, err)
			require.True(t, productP.detached)
		}

		from := NewTreePos(root.id, root.id)
		to := NewTreePos(root.id, div.id)
		at := time.NewTicket(lamport+50, 0, actor)
		before := productP.removedAt
		pairs, diff, _, err := tree.Edit(from, to, nil, 0, at, next, unaware, false)
		if purge {
			assert.Same(t, before, productP.removedAt, "the detached product keeps its removal")
		}
		require.NoError(t, err)
		for _, pair := range pairs {
			doc.RegisterGCPair(pair)
		}
		doc.Acc(diff)
		rebuilt, err := doc.Rederive()
		require.NoError(t, err)
		assert.Equal(t, rebuilt.DocSize(), doc.DocSize(), "purged %v: after the delete", purge)
		xmls = append(xmls, tree.ToXML())

		_, err = doc.GarbageCollect(all)
		require.NoError(t, err)
		rebuilt, err = doc.Rederive()
		require.NoError(t, err)
		assert.Equal(t, rebuilt.DocSize(), doc.DocSize(), "purged %v: after collecting", purge)
		assert.Equal(t, 0, doc.GarbageLen(), "purged %v", purge)
		shapes = append(shapes, treeShapeOf(tree))
	}
	assert.Equal(t, "<r></r>", xmls[0])
	assert.Equal(t, xmls[0], xmls[1])
	assert.Equal(t, shapes[0], shapes[1])
}

// TestDeepCopyKeepsASlotThroughAnUnregisteredParent pins that a DeepCopy
// carries the purge slots of what it copies even through a node no longer
// registered. In <r><l>LL</l><top><p><d1>x</d1></p></top><q></q></r>, d1 is
// purged from p while x's own pair is pending; then top is purged, and p by
// its own pair, which unregisters it, though d1 still records p as the
// parent it was purged from. The copy resolves x where the source does.
func TestDeepCopyKeepsASlotThroughAnUnregisteredParent(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	lamport := int64(0)
	next := func() *time.Ticket { lamport++; return time.NewTicket(lamport, 0, actor) }
	mk := func(typ string, val ...string) *TreeNode {
		return NewTreeNode(NewTreeNodeID(next(), 0), typ, nil, val...)
	}
	root, l, ll := mk("r"), mk("l"), mk("text", "LL")
	top, p, d1, x, q := mk("top"), mk("p"), mk("d1"), mk("text", "x"), mk("q")
	require.NoError(t, root.Append(l))
	require.NoError(t, l.Append(ll))
	require.NoError(t, root.Append(top))
	require.NoError(t, top.Append(p))
	require.NoError(t, p.Append(d1))
	require.NoError(t, d1.Append(x))
	require.NoError(t, root.Append(q))
	tree := NewTree(root, next())
	rm := next()
	for _, n := range []*TreeNode{top, p, d1, x} {
		n.remove(rm)
	}
	for _, n := range []*TreeNode{d1, top, p} {
		_, err = tree.purgeChild(n)
		require.NoError(t, err)
	}
	require.Nil(t, tree.DetachedNode(p.id), "p left the registry with its own purge")
	require.Same(t, x, tree.DetachedNode(x.id))
	want, wantErr := tree.ToIndex(x, x)
	// The copy the document's clone gets: Root.DeepCopy adds the detached
	// subtrees to the value copy.
	cp, err := tree.DeepCopy()
	require.NoError(t, err)
	ct := cp.(*Tree)
	_, err = ct.copyDetachedFrom(tree)
	require.NoError(t, err)
	cx := ct.DetachedNode(x.id)
	require.NotNil(t, cx)
	got, gotErr := ct.ToIndex(cx, cx)
	assert.Equal(t, wantErr == nil, gotErr == nil)
	assert.Equal(t, want, got)
}

// TestDeepCopyKeepsASlotThroughAnUnregisteredSibling pins the same for a
// recorded sibling: in <r><l>LL</l><top>ab</top><s></s><q>cd</q></r>, top is
// purged while ab's pair is pending, with s as the sibling it sat before;
// s is purged next, which unregisters it. The slot of ab passes s to q on
// the source, and on the copy too.
func TestDeepCopyKeepsASlotThroughAnUnregisteredSibling(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	lamport := int64(0)
	next := func() *time.Ticket { lamport++; return time.NewTicket(lamport, 0, actor) }
	mk := func(typ string, val ...string) *TreeNode {
		return NewTreeNode(NewTreeNodeID(next(), 0), typ, nil, val...)
	}
	root, l, ll := mk("r"), mk("l"), mk("text", "LL")
	top, ab, s, q, cd := mk("top"), mk("text", "ab"), mk("s"), mk("q"), mk("text", "cd")
	require.NoError(t, root.Append(l))
	require.NoError(t, l.Append(ll))
	require.NoError(t, root.Append(top))
	require.NoError(t, top.Append(ab))
	require.NoError(t, root.Append(s))
	require.NoError(t, root.Append(q))
	require.NoError(t, q.Append(cd))
	tree := NewTree(root, next())
	rm := next()
	for _, n := range []*TreeNode{top, ab, s} {
		n.remove(rm)
	}
	for _, n := range []*TreeNode{top, s} {
		_, err = tree.purgeChild(n)
		require.NoError(t, err)
	}
	want, err := tree.ToIndex(ab, ab)
	require.NoError(t, err)
	require.Equal(t, 4, want, "after <l>LL</l>, where top sat")
	// The copy the document's clone gets: Root.DeepCopy adds the detached
	// subtrees to the value copy.
	cp, err := tree.DeepCopy()
	require.NoError(t, err)
	ct := cp.(*Tree)
	_, err = ct.copyDetachedFrom(tree)
	require.NoError(t, err)
	cab := ct.DetachedNode(ab.id)
	require.NotNil(t, cab)
	got, err := ct.ToIndex(cab, cab)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

// TestDeepCopyBooksADetachedTombstonesPendingPair pins the pending pair of
// a detached tombstone against a ledger derived from the content: div is
// purged while p, removed later by a change the collection does not cover,
// keeps its own pair pending. Rederive, which derives the pairs from the
// nodes, p's included, and not from the root's map, reports the GC and
// GarbageLen the root does, as does the DeepCopy the clone is, and a reload
// of the attached tree alone reports less by exactly p. All settle alike
// once p's removal is covered.
func TestDeepCopyBooksADetachedTombstonesPendingPair(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	peer, err := time.ActorIDFromHex("0000000000000000000000ee")
	require.NoError(t, err)
	root := NewTreeNode(NewTreeNodeID(time.NewTicket(1, 0, actor), 0), "r", nil)
	div := NewTreeNode(NewTreeNodeID(time.NewTicket(2, 0, actor), 0), "div", nil)
	p := NewTreeNode(NewTreeNodeID(time.NewTicket(3, 0, actor), 0), "p", nil)
	require.NoError(t, root.Append(div))
	require.NoError(t, div.Append(p))
	div.remove(time.NewTicket(10, 0, actor))
	p.remove(time.NewTicket(20, 0, peer))
	keepDetached(p)
	tree := NewTree(root, time.NewTicket(4, 0, actor))
	doc := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
	doc.Object().Set("t", tree)
	doc.RegisterElement(tree, doc.Object())

	divOnly := time.NewVersionVector()
	divOnly.Set(actor, 10)
	_, err = doc.GarbageCollect(divOnly)
	require.NoError(t, err)
	require.True(t, p.detached)
	require.Equal(t, 1, doc.GarbageLen(), "p's pair is pending")

	rederived, err := doc.Rederive()
	require.NoError(t, err)
	assert.Equal(t, doc.DocSize(), rederived.DocSize(), "derived from p, the root's ledger")
	assert.Equal(t, doc.GarbageLen(), rederived.GarbageLen())
	object, err := doc.Object().DeepCopy()
	require.NoError(t, err)
	reloaded := NewRoot(object.(*Object))
	pending := doc.DocSize().GC
	pending.Sub(reloaded.DocSize().GC)
	assert.Equal(t, p.DataSize(), pending, "a reload lacks p's pair, and only that")
	copied, err := doc.DeepCopy()
	require.NoError(t, err)
	assert.Equal(t, doc.DocSize(), copied.DocSize(), "the clone books p's pending pair")
	assert.Equal(t, doc.GarbageLen(), copied.GarbageLen())

	all := time.NewVersionVector()
	all.Set(actor, time.MaxLamport)
	all.Set(peer, time.MaxLamport)
	all.Set(keeperActor, keptUntil)
	for _, r := range []*Root{doc, copied} {
		_, err = r.GarbageCollect(all)
		require.NoError(t, err)
	}
	assert.Equal(t, doc.DocSize(), copied.DocSize())
	assert.Equal(t, 0, copied.GarbageLen())
}

// TestDissolutionKeepsTheSourcePointerWhetherOrNotPurged pins that a
// restore's decision to drop a revived source's mergedInto does not depend
// on GC. s was merged into dst, whose child c carries the merge's stamp;
// dst sits in x, removed. One replica purges x, detaching dst and c; the
// other does not. A restore of s that did not know the merge keeps c's
// stamp, so both keep s's pointer.
func TestDissolutionKeepsTheSourcePointerWhetherOrNotPurged(t *testing.T) {
	var kept []bool
	for _, purge := range []bool{true, false} {
		actor, err := time.ActorIDFromHex("000000000000000000000001")
		require.NoError(t, err)
		peer, err := time.ActorIDFromHex("0000000000000000000000ee")
		require.NoError(t, err)
		root := NewTreeNode(NewTreeNodeID(time.NewTicket(1, 0, actor), 0), "r", nil)
		s := NewTreeNode(NewTreeNodeID(time.NewTicket(2, 0, actor), 0), "p", nil)
		x := NewTreeNode(NewTreeNodeID(time.NewTicket(3, 0, actor), 0), "div", nil)
		dst := NewTreeNode(NewTreeNodeID(time.NewTicket(4, 0, actor), 0), "p", nil)
		c := NewTreeNode(NewTreeNodeID(time.NewTicket(5, 0, actor), 0), "text", nil, "c")
		require.NoError(t, root.Append(s, x))
		require.NoError(t, x.Append(dst))
		require.NoError(t, dst.Append(c))
		merge := time.NewTicket(50, 0, peer)
		s.remove(merge)
		c.MergedFrom, c.MergedAt = s.id, merge
		x.remove(time.NewTicket(60, 0, actor))
		tree := NewTree(root, time.NewTicket(6, 0, actor))
		s.mergedInto = dst.id
		if purge {
			_, err := tree.purgeChild(x)
			require.NoError(t, err)
			require.True(t, c.detached)
		}

		unaware := time.NewVersionVector()
		unaware.Set(actor, 100)
		_, _, _, _, _, err = tree.Restore([]*TreeRestoreSpan{
			{ID: s.id, NodeType: "p", Length: 1, ParentID: root.id},
		}, false, unaware)
		require.NoError(t, err)
		require.False(t, s.IsRemoved())
		assert.Equal(t, s.id, c.MergedFrom, "purged %v: the unknown merge's stamp stays", purge)
		kept = append(kept, s.mergedInto != nil)
	}
	assert.Equal(t, kept[1], kept[0], "the pointer is kept or dropped alike, purged or not")
	assert.True(t, kept[0])
}

// TestDissolutionKeepsTheSourcePointerOnTheClone pins the same decision
// between a root and its clone, Root.DeepCopy, which runs every change
// first: the clone indexes the stamps of the detached subtrees it copies, so
// a restore of s that did not know the merge keeps s's pointer on both. It
// also pins that a detached node a restore forgets leaves the index.
func TestDissolutionKeepsTheSourcePointerOnTheClone(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	peer, err := time.ActorIDFromHex("0000000000000000000000ee")
	require.NoError(t, err)
	root := NewTreeNode(NewTreeNodeID(time.NewTicket(1, 0, actor), 0), "r", nil)
	s := NewTreeNode(NewTreeNodeID(time.NewTicket(2, 0, actor), 0), "p", nil)
	x := NewTreeNode(NewTreeNodeID(time.NewTicket(3, 0, actor), 0), "div", nil)
	dst := NewTreeNode(NewTreeNodeID(time.NewTicket(4, 0, actor), 0), "p", nil)
	c := NewTreeNode(NewTreeNodeID(time.NewTicket(5, 0, actor), 0), "text", nil, "c")
	require.NoError(t, root.Append(s, x))
	require.NoError(t, x.Append(dst))
	require.NoError(t, dst.Append(c))
	merge := time.NewTicket(50, 0, peer)
	s.remove(merge)
	c.MergedFrom, c.MergedAt = s.id, merge
	x.remove(time.NewTicket(60, 0, actor))
	tree := NewTree(root, time.NewTicket(6, 0, actor))
	s.mergedInto = dst.id
	doc := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
	doc.Object().Set("t", tree)
	doc.RegisterElement(tree, doc.Object())
	_, err = tree.purgeChild(x)
	require.NoError(t, err)

	copied, err := doc.DeepCopy()
	require.NoError(t, err)
	clone := copied.Object().Get("t").(*Tree)

	unaware := time.NewVersionVector()
	unaware.Set(actor, 100)
	var kept []bool
	for _, tr := range []*Tree{clone, tree} {
		_, _, _, _, _, err = tr.Restore([]*TreeRestoreSpan{
			{ID: s.id, NodeType: "p", Length: 1, ParentID: root.id},
		}, false, unaware)
		require.NoError(t, err)
		revived := tr.findFloorNode(s.id)
		require.NotNil(t, revived)
		require.False(t, revived.IsRemoved())
		kept = append(kept, revived.mergedInto != nil)
	}
	assert.Equal(t, []bool{true, true}, kept, "the clone keeps the pointer the root keeps")

	tree.forgetDetached(c)
	_, stamped := tree.mergeStamps[s.id.toIDString()][c]
	assert.False(t, stamped, "a forgotten node leaves the index")
}

// TestDeepCopyBooksTheRootsOwnTreePairs pins that the root's copy holds the
// tree pairs the root holds. A, attached, and D, detached by the purge of x,
// share an ID, so the root holds one pair under that key, A's. Booking D's
// pair on the copy through RegisterGCPair cancelled A's, and the copy held
// neither, collecting less than the root.
func TestDeepCopyBooksTheRootsOwnTreePairs(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	tk := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }
	shared := NewTreeNodeID(tk(4), 0)
	root := NewTreeNode(NewTreeNodeID(tk(1), 0), "r", nil)
	x := NewTreeNode(NewTreeNodeID(tk(2), 0), "div", nil)
	d := NewTreeNode(shared, "p", nil)
	a := NewTreeNode(shared, "p", nil)
	require.NoError(t, root.Append(x, a))
	require.NoError(t, x.Append(d))
	x.remove(tk(10))
	a.remove(tk(20))
	keepDetached(d)
	// D, live, holds the ID, so the purge registers it as detached.
	tree := NewTree(root, tk(5))
	d.remove(tk(20))

	// The scan books D and A under one key, and the second cancels the
	// first; A's pair is then booked again, as a later removal of A would.
	doc := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
	doc.Object().Set("t", tree)
	doc.RegisterElement(tree, doc.Object())
	size := a.DataSize()
	doc.RegisterGCPair(GCPair{Parent: tree, Child: a, GCOnlySize: &size})
	vector := time.NewVersionVector()
	vector.Set(actor, 15)
	_, err = doc.GarbageCollect(vector)
	require.NoError(t, err)
	require.True(t, d.detached)
	require.Same(t, d, tree.DetachedNode(shared))
	require.Equal(t, 1, doc.GarbageLen())

	copied, err := doc.DeepCopy()
	require.NoError(t, err)
	assert.Equal(t, doc.GarbageLen(), copied.GarbageLen())
	assert.Equal(t, doc.DocSize(), copied.DocSize())
	ct := copied.Object().Get("t").(*Tree)
	for _, pair := range copied.gcNodePairMap {
		assert.Same(t, ct.Root().Children(true)[0], pair.Child, "the attached A's copy, as on the root")
	}
}

// TestReleaseTicketDoesNotDependOnPurgeOrder pins the removal a released
// node is tombstoned with. z is live under y, removed, under x, removed
// earlier; a collection that covers both purges their pairs in Go's map
// order. Whichever goes first, z takes y's removal, the one that hides it,
// so every replica and every run stores the same tombstone.
func TestReleaseTicketDoesNotDependOnPurgeOrder(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	tk := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }
	for range 32 {
		root := NewTreeNode(NewTreeNodeID(tk(1), 0), "r", nil)
		x := NewTreeNode(NewTreeNodeID(tk(2), 0), "div", nil)
		y := NewTreeNode(NewTreeNodeID(tk(3), 0), "p", nil)
		z := NewTreeNode(NewTreeNodeID(tk(4), 0), "text", nil, "z")
		require.NoError(t, root.Append(x))
		require.NoError(t, x.Append(y))
		require.NoError(t, y.Append(z))
		x.remove(tk(10))
		y.remove(tk(20))
		tree := NewTree(root, tk(5))
		doc := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
		doc.Object().Set("t", tree)
		doc.RegisterElement(tree, doc.Object())

		vector := time.NewVersionVector()
		vector.Set(actor, 30)
		_, err = doc.GarbageCollect(vector)
		require.NoError(t, err)
		require.True(t, z.released)
		require.Equal(t, tk(20).Key(), z.RemovedAt().Key(), "y's removal, whichever purge came first")
	}
}

// TestALaterCollectionDropsADetachedSubtree pins the bound on the detached
// state a purge leaves. x, removed, holds y, live, and t, a tombstone whose
// own removal comes later. A collection at v purges x, releasing y and
// leaving t detached with its pair pending, and keeps them, as y is stamped
// merged in by a change v has not seen (see keepDetached). The next, which
// has seen it, lets the subtree go with t's pair, on the root and on a copy
// made in between alike, and the ledger is what one derived from the
// content reports; a position naming y then fails to resolve, as on a
// replica that keeps no detached nodes.
func TestALaterCollectionDropsADetachedSubtree(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	tk := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }
	root := NewTreeNode(NewTreeNodeID(tk(1), 0), "r", nil)
	x := NewTreeNode(NewTreeNodeID(tk(2), 0), "div", nil)
	y := NewTreeNode(NewTreeNodeID(tk(3), 0), "p", nil)
	tomb := NewTreeNode(NewTreeNodeID(tk(4), 0), "q", nil)
	require.NoError(t, root.Append(x))
	require.NoError(t, x.Append(y, tomb))
	x.remove(tk(10))
	tomb.remove(tk(30))
	keepDetached(y)
	tree := NewTree(root, tk(5))
	doc := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
	doc.Object().Set("t", tree)
	doc.RegisterElement(tree, doc.Object())

	at := func(lamport int64) time.VersionVector {
		v := time.NewVersionVector()
		v.Set(actor, lamport)
		return v
	}
	_, err = doc.GarbageCollect(at(20))
	require.NoError(t, err)
	require.Same(t, y, tree.DetachedNode(y.id))
	require.Same(t, tomb, tree.DetachedNode(tomb.id))
	require.Equal(t, 2, doc.GarbageLen(), "y is held, released, and t's pair is pending")

	copied, err := doc.DeepCopy()
	require.NoError(t, err)
	ct := copied.Object().Get("t").(*Tree)
	require.NotNil(t, ct.DetachedNode(y.id), "the copy carries the subtree")
	seen := at(20)
	seen.Set(keeperActor, keptUntil)
	for _, r := range []*Root{copied, doc} {
		_, err = r.GarbageCollect(seen)
		require.NoError(t, err)
	}
	for _, tr := range []*Tree{tree, ct} {
		assert.Nil(t, tr.DetachedNode(y.id))
		assert.Nil(t, tr.DetachedNode(tomb.id))
		assert.Zero(t, tr.detachedByID.Len())
	}
	assert.Equal(t, 0, doc.GarbageLen(), "t's pair goes with it")
	assert.Equal(t, doc.DocSize(), copied.DocSize())
	assert.Equal(t, 0, copied.GarbageLen())
	rederived, err := doc.Rederive()
	require.NoError(t, err)
	assert.Equal(t, rederived.DocSize(), doc.DocSize())
	assert.Empty(t, doc.detachedTrees)
	assert.Empty(t, copied.detachedTrees)
}

// TestADetachedSubtreeStaysUntilItsNodesAreSeen pins that a collection
// keeps a detached subtree holding a node its vector has not seen created,
// or merged in. A change that has not seen a split which carried a node
// into the subtree, or a merge that moved one there, is concurrent with it
// and can still name the node. c, created by one peer at 40, and m, merged
// in by another at 41, sit under x, purged at v1. Each later vector has
// seen one of the two and keeps the subtree; the one that has seen both
// lets it go.
func TestADetachedSubtreeStaysUntilItsNodesAreSeen(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	splitter, err := time.ActorIDFromHex("0000000000000000000000ee")
	require.NoError(t, err)
	merger, err := time.ActorIDFromHex("0000000000000000000000ff")
	require.NoError(t, err)
	tk := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }
	root := NewTreeNode(NewTreeNodeID(tk(1), 0), "r", nil)
	x := NewTreeNode(NewTreeNodeID(tk(2), 0), "div", nil)
	c := NewTreeNode(NewTreeNodeID(time.NewTicket(40, 0, splitter), 0), "p", nil)
	m := NewTreeNode(NewTreeNodeID(tk(6), 0), "p", nil)
	m.MergedFrom, m.MergedAt = c.id, time.NewTicket(41, 0, merger)
	require.NoError(t, root.Append(x))
	require.NoError(t, x.Append(c, m))
	x.remove(tk(10))
	tree := NewTree(root, tk(5))
	doc := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
	doc.Object().Set("t", tree)
	doc.RegisterElement(tree, doc.Object())

	at := func(own, split, merge int64) time.VersionVector {
		v := time.NewVersionVector()
		v.Set(actor, own)
		v.Set(splitter, split)
		v.Set(merger, merge)
		return v
	}
	_, err = doc.GarbageCollect(at(20, 0, 0))
	require.NoError(t, err)
	require.Same(t, c, tree.DetachedNode(c.id))
	_, err = doc.GarbageCollect(at(21, 40, 0))
	require.NoError(t, err)
	assert.Same(t, c, tree.DetachedNode(c.id), "kept while m's merge is unseen")
	_, err = doc.GarbageCollect(at(22, 0, 41))
	require.NoError(t, err)
	assert.Same(t, c, tree.DetachedNode(c.id), "kept while c's creation is unseen")
	_, err = doc.GarbageCollect(at(23, 40, 41))
	require.NoError(t, err)
	assert.Nil(t, tree.DetachedNode(c.id))
	assert.Nil(t, tree.DetachedNode(m.id))
}

// TestDroppingASubtreeUnlinksItsSplitSiblings pins that a drop takes its
// nodes out of the split chains attached nodes keep, as a purge of each
// node would. a, attached, was split; its product b sits under x, removed
// and purged. Left in a's InsNextID, b's ID would resolve, once b is gone,
// to the floor of that ID: another piece of the same insertion, or nothing.
func TestDroppingASubtreeUnlinksItsSplitSiblings(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	tk := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }
	root := NewTreeNode(NewTreeNodeID(tk(1), 0), "r", nil)
	a := NewTreeNode(NewTreeNodeID(tk(2), 0), "p", nil)
	x := NewTreeNode(NewTreeNodeID(tk(3), 0), "div", nil)
	b := NewTreeNode(NewTreeNodeID(tk(4), 0), "p", nil)
	c := NewTreeNode(NewTreeNodeID(tk(5), 0), "p", nil)
	require.NoError(t, root.Append(a, x, c))
	require.NoError(t, x.Append(b))
	a.InsNextID, b.InsPrevID = b.id, a.id
	b.InsNextID, c.InsPrevID = c.id, b.id
	x.remove(tk(10))
	keepDetached(b)
	tree := NewTree(root, tk(6))
	doc := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
	doc.Object().Set("t", tree)
	doc.RegisterElement(tree, doc.Object())

	at := func(lamport int64) time.VersionVector {
		v := time.NewVersionVector()
		v.Set(actor, lamport)
		return v
	}
	_, err = doc.GarbageCollect(at(20))
	require.NoError(t, err)
	require.Same(t, b, tree.DetachedNode(b.id))
	require.Equal(t, b.id, a.InsNextID, "a detached node stays in the chain")

	seen := at(25)
	seen.Set(keeperActor, keptUntil)
	_, err = doc.GarbageCollect(seen)
	require.NoError(t, err)
	require.Nil(t, tree.DetachedNode(b.id))
	assert.Equal(t, c.id, a.InsNextID, "a skips the dropped b")
	assert.Equal(t, a.id, c.InsPrevID)
}

// TestDroppingASubtreeDropsItsAttributePairs pins the pairs a dropped node's
// removed attributes hold. p, live under x, has had k removed by a peer at
// 50; the purge of x releases p and leaves the pair of k pending, and the
// drop at a vector that has seen p created, and not k removed, lets p go.
// Its attribute's pair goes with it, so the root's GC is what its copy and
// a reload of its object, neither of which holds p, report.
func TestDroppingASubtreeDropsItsAttributePairs(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	peer, err := time.ActorIDFromHex("0000000000000000000000ee")
	require.NoError(t, err)
	tk := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }
	root := NewTreeNode(NewTreeNodeID(tk(1), 0), "r", nil)
	x := NewTreeNode(NewTreeNodeID(tk(2), 0), "div", nil)
	p := NewTreeNode(NewTreeNodeID(tk(3), 0), "p", nil)
	require.NoError(t, root.Append(x))
	require.NoError(t, x.Append(p))
	p.SetAttr("k", "v", tk(4))
	p.RemoveAttr("k", time.NewTicket(50, 0, peer))
	x.remove(tk(10))
	keepDetached(p)
	tree := NewTree(root, tk(5))
	doc := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
	doc.Object().Set("t", tree)
	doc.RegisterElement(tree, doc.Object())

	at := func(own int64) time.VersionVector {
		v := time.NewVersionVector()
		v.Set(actor, own)
		v.Set(peer, 0)
		return v
	}
	_, err = doc.GarbageCollect(at(20))
	require.NoError(t, err)
	require.Same(t, p, tree.DetachedNode(p.id))
	require.Equal(t, 2, doc.GarbageLen(), "p is held, released, and k's pair is pending")

	seen := at(25)
	seen.Set(keeperActor, keptUntil)
	_, err = doc.GarbageCollect(seen)
	require.NoError(t, err)
	require.Nil(t, tree.DetachedNode(p.id))
	copied, err := doc.DeepCopy()
	require.NoError(t, err)
	object, err := doc.Object().DeepCopy()
	require.NoError(t, err)
	reloaded := NewRoot(object.(*Object))
	for _, other := range []*Root{copied, reloaded} {
		assert.Equal(t, other.DocSize().GC, doc.DocSize().GC)
		assert.Equal(t, other.GarbageLen(), doc.GarbageLen())
	}
	assert.Equal(t, 0, doc.GarbageLen())
}

// TestDroppingABornDeadSplitPieceKeepsTheLedger pins DropGCPair on the pair
// of a piece split off a tombstone, booked for the split's overhead alone:
// the bytes it took from its sibling stay in GC under the sibling's charge.
// "abcd", removed by a peer at 50, is split in two, and its paragraph and
// div, removed earlier, are purged at a vector that has not seen the text's
// removal, which leaves both pieces detached, tombstones with their pairs
// pending. The next collection drops them; what each drop gives back adds
// up to what the two pairs booked, so GC is what a reload reports.
func TestDroppingABornDeadSplitPieceKeepsTheLedger(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	peer, err := time.ActorIDFromHex("0000000000000000000000ee")
	require.NoError(t, err)
	lamport := int64(0)
	tk := func() *time.Ticket {
		lamport++
		return time.NewTicket(lamport, 0, actor)
	}
	root := NewTreeNode(NewTreeNodeID(tk(), 0), "r", nil)
	x := NewTreeNode(NewTreeNodeID(tk(), 0), "div", nil)
	p := NewTreeNode(NewTreeNodeID(tk(), 0), "p", nil)
	text := NewTreeNode(NewTreeNodeID(tk(), 0), "text", nil, "abcd")
	require.NoError(t, root.Append(x))
	require.NoError(t, x.Append(p))
	require.NoError(t, p.Append(text))
	removedAt := tk()
	x.remove(removedAt)
	p.remove(removedAt)
	text.remove(time.NewTicket(50, 0, peer))
	keepDetached(p)
	tree := NewTree(root, tk())
	doc := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
	doc.Object().Set("t", tree)
	doc.RegisterElement(tree, doc.Object())

	_, err = text.Split(tree, 2, tk, nil)
	require.NoError(t, err)
	pairs := tree.drainPendingGCPairs()
	require.Len(t, pairs, 1, "the born-dead piece books its overhead")
	require.NotNil(t, pairs[0].GCOnlySize)
	require.NotEqual(t, pairs[0].Child.DataSize(), *pairs[0].GCOnlySize)
	doc.RegisterGCPair(pairs[0])
	piece := pairs[0].Child.(*TreeNode)

	at := func(own int64) time.VersionVector {
		v := time.NewVersionVector()
		v.Set(actor, own)
		v.Set(peer, 0)
		return v
	}
	_, err = doc.GarbageCollect(at(lamport))
	require.NoError(t, err)
	require.Same(t, piece, tree.DetachedNode(piece.id))
	require.Equal(t, 2, doc.GarbageLen(), "both pieces' pairs are pending")

	seen := at(lamport)
	seen.Set(keeperActor, keptUntil)
	_, err = doc.GarbageCollect(seen)
	require.NoError(t, err)
	require.Nil(t, tree.DetachedNode(piece.id))
	object, err := doc.Object().DeepCopy()
	require.NoError(t, err)
	reloaded := NewRoot(object.(*Object))
	assert.Equal(t, reloaded.DocSize(), doc.DocSize())
	assert.Equal(t, 0, doc.GarbageLen())
}

// TestDeepCopyBooksAPairWhoseNodeLeftTheTree pins the copy's ledger for a
// pair the root still holds for a node no longer in its tree: p, a leaf,
// is purged while the pair of its attribute k, removed later by a peer,
// stays pending. The copy has no node for p, so it books the pair for a
// copy of p alone; dropping it would leave the clone, whose size the
// client's MaxSizeLimit reads, holding less GC than the root.
func TestDeepCopyBooksAPairWhoseNodeLeftTheTree(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	peer, err := time.ActorIDFromHex("0000000000000000000000ee")
	require.NoError(t, err)
	tk := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }
	root := NewTreeNode(NewTreeNodeID(tk(1), 0), "r", nil)
	p := NewTreeNode(NewTreeNodeID(tk(2), 0), "p", nil)
	require.NoError(t, root.Append(p))
	p.SetAttr("k", "v", tk(3))
	p.RemoveAttr("k", time.NewTicket(50, 0, peer))
	p.remove(tk(10))
	tree := NewTree(root, tk(4))
	doc := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
	doc.Object().Set("t", tree)
	doc.RegisterElement(tree, doc.Object())

	vector := time.NewVersionVector()
	vector.Set(actor, 20)
	vector.Set(peer, 0)
	_, err = doc.GarbageCollect(vector)
	require.NoError(t, err)
	require.Empty(t, tree.Root().Children(true), "p is purged")
	require.Equal(t, 1, doc.GarbageLen(), "k's pair is pending")

	copied, err := doc.DeepCopy()
	require.NoError(t, err)
	assert.Equal(t, doc.DocSize(), copied.DocSize())
	assert.Equal(t, doc.GarbageLen(), copied.GarbageLen())

	vector.Set(peer, 50)
	for _, r := range []*Root{doc, copied} {
		_, err = r.GarbageCollect(vector)
		require.NoError(t, err)
	}
	assert.Equal(t, doc.DocSize(), copied.DocSize())
	assert.Equal(t, 0, copied.GarbageLen())
}

// TestCollectingATreeLetsItsDetachedSubtreesGo pins that a Tree element the
// collection deregisters leaves detachedTrees, and does not stay reachable
// through it with the subtrees it holds.
func TestCollectingATreeLetsItsDetachedSubtreesGo(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	peer, err := time.ActorIDFromHex("0000000000000000000000ee")
	require.NoError(t, err)
	tk := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }
	root := NewTreeNode(NewTreeNodeID(tk(1), 0), "r", nil)
	x := NewTreeNode(NewTreeNodeID(tk(2), 0), "div", nil)
	a := NewTreeNode(NewTreeNodeID(time.NewTicket(40, 0, peer), 0), "p", nil)
	require.NoError(t, root.Append(x))
	require.NoError(t, x.Append(a))
	x.remove(tk(10))
	tree := NewTree(root, tk(3))
	doc := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
	doc.Object().Set("t", tree)
	doc.RegisterElement(tree, doc.Object())

	vector := time.NewVersionVector()
	vector.Set(actor, 20)
	vector.Set(peer, 0)
	_, err = doc.GarbageCollect(vector)
	require.NoError(t, err)
	require.Contains(t, doc.detachedTrees, tree)

	removed := doc.Object().Delete("t", tk(21))
	require.Same(t, tree, removed)
	doc.RegisterRemovedElementPair(doc.Object(), tree)
	vector.Set(actor, 30)
	_, err = doc.GarbageCollect(vector)
	require.NoError(t, err)
	require.Nil(t, doc.FindByCreatedAt(tree.CreatedAt()), "the tree is collected")
	assert.NotContains(t, doc.detachedTrees, tree)
}

// TestCopyDetachedFromATreeOfAnotherShape pins that copyDetachedFrom, which
// pairs a source's nodes with a copy's by walking both in lockstep, fails
// with ErrTreeCopyShape for a tree that is not a copy of the source in
// shape, rather than indexing past the end of the shorter child list, and
// that Root.DeepCopy fails with it rather than handing back a clone without
// the detached state its root holds. The root's element map is made to
// name a tree of another shape than the one its object holds, which only a
// broken invariant gives.
func TestCopyDetachedFromATreeOfAnotherShape(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	tk := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }
	srcRoot := NewTreeNode(NewTreeNodeID(tk(1), 0), "r", nil)
	require.NoError(t, srcRoot.Append(
		NewTreeNode(NewTreeNodeID(tk(2), 0), "p", nil),
		NewTreeNode(NewTreeNodeID(tk(3), 0), "p", nil),
	))
	src := NewTree(srcRoot, tk(4))
	other := NewTree(NewTreeNode(NewTreeNodeID(tk(1), 0), "r", nil), tk(4))

	assert.NotPanics(t, func() {
		_, err := other.copyDetachedFrom(src)
		assert.ErrorIs(t, err, ErrTreeCopyShape)
	})

	doc := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
	doc.Object().Set("t", other)
	doc.RegisterElement(other, doc.Object())
	doc.elementMap[other.CreatedAt().Key()] = src
	_, err = doc.DeepCopy()
	assert.ErrorIs(t, err, ErrTreeCopyShape)
}

// TestDropDetachedStopsAtTheAttachedTree pins that a drop walks down from
// the tops purges recorded, never up: a node marked detached under an
// attached parent, which only a tree breaking that invariant holds, is no
// top, so a drop does not reach the attached tree and let it go.
func TestDropDetachedStopsAtTheAttachedTree(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	tk := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }
	root := NewTreeNode(NewTreeNodeID(tk(1), 0), "r", nil)
	p := NewTreeNode(NewTreeNodeID(tk(2), 0), "p", nil)
	stray := NewTreeNode(NewTreeNodeID(tk(3), 0), "q", nil)
	require.NoError(t, root.Append(p))
	require.NoError(t, p.Append(stray))
	tree := NewTree(root, tk(4))
	stray.detached = true
	tree.moveToDetached(stray)

	vector := time.NewVersionVector()
	vector.Set(actor, 10)
	assert.Empty(t, tree.dropDetached(vector, nil))
	assert.Same(t, p, tree.findFloorNode(p.id), "the attached tree stays")
	assert.Same(t, root, tree.findFloorNode(root.id))
}

// releaseFixture builds a root holding a tree with the given nodes under
// its root, books it, and returns both.
func releaseFixture(t *testing.T, root *TreeNode, createdAt *time.Ticket) (*Root, *Tree) {
	t.Helper()
	tree := NewTree(root, createdAt)
	doc := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
	doc.Object().Set("t", tree)
	doc.RegisterElement(tree, doc.Object())
	return doc, tree
}

// TestCollectingATreeDropsItsReleasedPairs pins the pairs of the nodes a
// purge released when the Tree element itself is collected before their
// subtree is let go. a, created by a peer the vector has not seen, keeps
// x's subtree; collect leaves a released node's pair to the drop, which a
// tree out of the document does not get, so deregistering the tree drops
// them: GC is what one derived from the content reports, and nothing is
// left.
func TestCollectingATreeDropsItsReleasedPairs(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	peer, err := time.ActorIDFromHex("0000000000000000000000ee")
	require.NoError(t, err)
	tk := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }
	root := NewTreeNode(NewTreeNodeID(tk(1), 0), "r", nil)
	x := NewTreeNode(NewTreeNodeID(tk(2), 0), "div", nil)
	a := NewTreeNode(NewTreeNodeID(time.NewTicket(40, 0, peer), 0), "p", nil)
	b := NewTreeNode(NewTreeNodeID(tk(3), 0), "p", nil)
	require.NoError(t, root.Append(x))
	require.NoError(t, x.Append(a, b))
	x.remove(tk(10))
	doc, tree := releaseFixture(t, root, tk(4))

	vector := time.NewVersionVector()
	vector.Set(actor, 20)
	vector.Set(peer, 0)
	_, err = doc.GarbageCollect(vector)
	require.NoError(t, err)
	require.True(t, a.released)
	require.Equal(t, 2, doc.GarbageLen(), "a and b are held, released")

	doc.Object().Delete("t", tk(21))
	doc.RegisterRemovedElementPair(doc.Object(), tree)
	vector.Set(actor, 30)
	_, err = doc.GarbageCollect(vector)
	require.NoError(t, err)
	require.Nil(t, doc.FindByCreatedAt(tree.CreatedAt()))
	assert.Equal(t, 0, doc.GarbageLen())
	rederived, err := doc.Rederive()
	require.NoError(t, err)
	assert.Equal(t, rederived.DocSize(), doc.DocSize())
	gc := doc.DocSize().GC
	assert.Zero(t, (&gc).Total())
}

// TestDroppingReachesAReleasedNodeAnotherHoldsTheIDOf pins the drop's reach
// for a released node detachedByID does not index: b shares its ID with a,
// attached and live, which holds it. The purge of x releases b and books
// its pair, and the tops of the detached subtrees, not detachedByID, are
// what the drop walks, so the next collection lets b go with its pair.
func TestDroppingReachesAReleasedNodeAnotherHoldsTheIDOf(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	tk := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }
	shared := NewTreeNodeID(tk(3), 0)
	root := NewTreeNode(NewTreeNodeID(tk(1), 0), "r", nil)
	x := NewTreeNode(NewTreeNodeID(tk(2), 0), "div", nil)
	b := NewTreeNode(shared, "p", nil)
	a := NewTreeNode(shared, "p", nil)
	require.NoError(t, root.Append(x, a))
	require.NoError(t, x.Append(b))
	x.remove(tk(10))
	keepDetached(b)
	doc, tree := releaseFixture(t, root, tk(4))
	require.Same(t, a, tree.findFloorNode(shared), "a holds the ID")

	vector := time.NewVersionVector()
	vector.Set(actor, 20)
	_, err = doc.GarbageCollect(vector)
	require.NoError(t, err)
	require.True(t, b.released)
	require.Nil(t, tree.DetachedNode(shared), "b is not indexed")
	require.Equal(t, 1, doc.GarbageLen(), "b's pair is booked")
	copied, err := doc.DeepCopy()
	require.NoError(t, err)
	assert.Equal(t, doc.DocSize(), copied.DocSize())

	vector.Set(keeperActor, keptUntil)
	n, err := doc.GarbageCollect(vector)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "the count takes in what the drop let go")
	assert.Equal(t, 0, doc.GarbageLen(), "the drop reached b")
	gc := doc.DocSize().GC
	assert.Zero(t, (&gc).Total())
	assert.Empty(t, doc.detachedTrees)
}

// TestARetainedBookingUnderATakenKeyIsKept pins bookRetained when the key
// of a released node's pair is held by another node's: b, live under x,
// shares its ID with a, an attached tombstone whose own pair is pending.
// The purge has taken b's bytes off Live, so they go to GC whatever the
// key, and the drop gives them back; a's pair stays, as it is a's.
func TestARetainedBookingUnderATakenKeyIsKept(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	peer, err := time.ActorIDFromHex("0000000000000000000000ee")
	require.NoError(t, err)
	tk := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }
	shared := NewTreeNodeID(tk(3), 0)
	root := NewTreeNode(NewTreeNodeID(tk(1), 0), "r", nil)
	x := NewTreeNode(NewTreeNodeID(tk(2), 0), "div", nil)
	b := NewTreeNode(shared, "p", nil)
	a := NewTreeNode(shared, "p", nil)
	require.NoError(t, root.Append(x, a))
	require.NoError(t, x.Append(b))
	x.remove(tk(10))
	a.remove(time.NewTicket(50, 0, peer))
	keepDetached(b)
	doc, _ := releaseFixture(t, root, tk(4))
	require.Equal(t, 2, doc.GarbageLen(), "x and a")

	vector := time.NewVersionVector()
	vector.Set(actor, 20)
	vector.Set(peer, 0)
	live := doc.DocSize().Live
	_, err = doc.GarbageCollect(vector)
	require.NoError(t, err)
	require.True(t, b.released)
	held := a.DataSize()
	held.Add(b.DataSize())
	assert.Equal(t, held, doc.DocSize().GC, "b's bytes are in GC though a holds the key")
	released := live
	released.Sub(doc.DocSize().Live)
	assert.Equal(t, b.DataSize().Data, released.Data, "and off Live")

	copied, err := doc.DeepCopy()
	require.NoError(t, err)
	assert.Equal(t, doc.DocSize(), copied.DocSize(), "the clone holds b's bytes too")
	assert.Len(t, copied.retainedOff, 1)
	rederived, err := doc.Rederive()
	require.NoError(t, err)
	assert.Equal(t, doc.DocSize(), rederived.DocSize())

	vector.Set(keeperActor, keptUntil)
	for _, r := range []*Root{doc, copied} {
		_, err = r.GarbageCollect(vector)
		require.NoError(t, err)
	}
	assert.Equal(t, doc.DocSize(), copied.DocSize())
	assert.Equal(t, a.DataSize(), doc.DocSize().GC, "the drop gives b's back, a's pair stays")
	assert.Equal(t, 1, doc.GarbageLen())
	assert.Empty(t, doc.retainedOff)
}

// TestDroppingAZeroSizedAttributePairKeepsGC pins DropGCPair on an
// attribute pair booked at zero: k, live on p when p was removed, is
// removed afterwards, and its bytes are inside p's charge. The drop of
// p's subtree gives back p's size, which no longer counts k, and k's, which
// together are what was booked, so GC ends at zero and not below.
func TestDroppingAZeroSizedAttributePairKeepsGC(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	peer, err := time.ActorIDFromHex("0000000000000000000000ee")
	require.NoError(t, err)
	tk := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }
	root := NewTreeNode(NewTreeNodeID(tk(1), 0), "r", nil)
	x := NewTreeNode(NewTreeNodeID(tk(2), 0), "div", nil)
	p := NewTreeNode(NewTreeNodeID(tk(3), 0), "p", nil)
	require.NoError(t, root.Append(x))
	require.NoError(t, x.Append(p))
	p.SetAttr("k", "value", tk(4))
	x.remove(tk(10))
	p.remove(time.NewTicket(50, 0, peer))
	keepDetached(p)
	doc, tree := releaseFixture(t, root, tk(5))

	// As RemoveStyle books it on a removed node: the dropped value leaves
	// GC, and the tombstone's pair carries nothing.
	removal := p.RemoveAttr("k", time.NewTicket(51, 0, peer))
	doc.docSize.GC.Sub(removal.ValueDropped)
	for _, rht := range removal.GCNodes {
		doc.RegisterGCPair(attrGCPair(p, rht, true, false))
	}
	require.Equal(t, 3, doc.GarbageLen(), "x, p and k")

	vector := time.NewVersionVector()
	vector.Set(actor, 20)
	vector.Set(peer, 0)
	_, err = doc.GarbageCollect(vector)
	require.NoError(t, err)
	require.Same(t, p, tree.DetachedNode(p.id))
	vector.Set(keeperActor, keptUntil)
	_, err = doc.GarbageCollect(vector)
	require.NoError(t, err)
	require.Nil(t, tree.DetachedNode(p.id))
	gc := doc.DocSize().GC
	assert.GreaterOrEqual(t, gc.Data, 0)
	assert.GreaterOrEqual(t, gc.Meta, 0)
	assert.Zero(t, (&gc).Total())
	assert.Equal(t, 0, doc.GarbageLen())
}

// TestPutNodeKeepsAnAttachedNodesID pins putNode's detached branch to the
// rule moveToDetached keeps: a node placed in a detached subtree under an ID
// an attached node holds is not registered with the detached nodes, so the
// lookup keeps answering with the attached one.
func TestPutNodeKeepsAnAttachedNodesID(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	tk := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }
	shared := NewTreeNodeID(tk(3), 0)
	root := NewTreeNode(NewTreeNodeID(tk(1), 0), "r", nil)
	a := NewTreeNode(shared, "p", nil)
	require.NoError(t, root.Append(a))
	tree := NewTree(root, tk(4))

	n := NewTreeNode(shared, "p", nil)
	n.remove(tk(5))
	n.detached = true
	tree.putNode(n)
	assert.Nil(t, tree.DetachedNode(shared))
	assert.Same(t, a, tree.findFloorNode(shared))
}

// TestCopyCarriesATopNoIndexedNodeHangsUnder pins that a copy of the root
// carries every detached subtree a purge recorded, including one with no
// node detachedByID indexes: b, the only node under x, shares its ID with
// a, attached, which holds it. A copy that found its subtrees through
// detachedByID alone would book b's pair for a copy of b, with no top to
// drop it by, and hold more GC than the root once the root drops it. Both,
// and a ledger derived from the content, agree before the drop, which the
// next collection makes, and after.
func TestCopyCarriesATopNoIndexedNodeHangsUnder(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	peer, err := time.ActorIDFromHex("0000000000000000000000ee")
	require.NoError(t, err)
	tk := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }
	shared := NewTreeNodeID(time.NewTicket(40, 0, peer), 0)
	root := NewTreeNode(NewTreeNodeID(tk(1), 0), "r", nil)
	x := NewTreeNode(NewTreeNodeID(tk(2), 0), "div", nil)
	b := NewTreeNode(shared, "p", nil)
	a := NewTreeNode(shared, "p", nil)
	require.NoError(t, root.Append(x, a))
	require.NoError(t, x.Append(b))
	x.remove(tk(10))
	keepDetached(b)
	doc, tree := releaseFixture(t, root, tk(4))
	require.Same(t, a, tree.findFloorNode(shared))

	vector := time.NewVersionVector()
	vector.Set(actor, 20)
	vector.Set(peer, 40)
	_, err = doc.GarbageCollect(vector)
	require.NoError(t, err)
	require.True(t, b.released)
	require.Zero(t, tree.detachedByID.Len(), "no node under x is indexed")

	copied, err := doc.DeepCopy()
	require.NoError(t, err)
	check := func(when string) {
		t.Helper()
		assert.Equal(t, doc.DocSize(), copied.DocSize(), when)
		assert.Equal(t, doc.GarbageLen(), copied.GarbageLen(), when)
		rederived, err := doc.Rederive()
		require.NoError(t, err)
		assert.Equal(t, doc.DocSize(), rederived.DocSize(), when)
		assert.Equal(t, doc.GarbageLen(), rederived.GarbageLen(), when)
	}
	check("before the drop")
	require.Equal(t, 1, doc.GarbageLen())

	vector.Set(keeperActor, keptUntil)
	for _, r := range []*Root{doc, copied} {
		_, err = r.GarbageCollect(vector)
		require.NoError(t, err)
	}
	require.Equal(t, 0, doc.GarbageLen(), "the root drops b")
	check("after the drop")
}

// TestAForgedTicketDoesNotKeepADetachedSubtree pins the drop against a
// ticket no change this replica applied carries: m, under x, is stamped
// merged in by a phantom actor at a lamport no vector will reach. Every
// honest node of the subtree is covered, and the replica's own version
// vector, which knows every honest ticket of a node it holds, does not
// know m's, so it counts as seen and the subtree goes. Trusting it would
// keep the subtree, and its bytes in GC, for good.
func TestAForgedTicketDoesNotKeepADetachedSubtree(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	phantom, err := time.ActorIDFromHex("0000000000000000000000ab")
	require.NoError(t, err)
	tk := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }
	root := NewTreeNode(NewTreeNodeID(tk(1), 0), "r", nil)
	x := NewTreeNode(NewTreeNodeID(tk(2), 0), "div", nil)
	m := NewTreeNode(NewTreeNodeID(tk(3), 0), "p", nil)
	m.MergedFrom, m.MergedAt = x.id, time.NewTicket(time.MaxLamport-1, 0, phantom)
	require.NoError(t, root.Append(x))
	require.NoError(t, x.Append(m))
	x.remove(tk(10))
	doc, tree := releaseFixture(t, root, tk(4))

	vector := time.NewVersionVector()
	vector.Set(actor, 20)
	known := time.NewVersionVector()
	known.Set(actor, 30)
	_, err = doc.GarbageCollect(vector)
	require.NoError(t, err)
	require.Same(t, m, tree.DetachedNode(m.id), "trusted, the forged merge keeps it")

	_, err = doc.GarbageCollectKnown(vector, known)
	require.NoError(t, err)
	assert.Nil(t, tree.DetachedNode(m.id), "known does not know it: it counts as seen")
	assert.Equal(t, 0, doc.GarbageLen())
}

// TestDeepCopyFailsForATreeItCannotFind pins that Root.DeepCopy fails when
// the copy of its object holds no element for a Tree the root's element map
// holds, which only a broken invariant gives, rather than handing back a
// clone without that tree's detached state.
func TestDeepCopyFailsForATreeItCannotFind(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	tk := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }
	doc := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
	stray := NewTree(NewTreeNode(NewTreeNodeID(tk(1), 0), "r", nil), tk(2))
	doc.elementMap[stray.CreatedAt().Key()] = stray

	_, err = doc.DeepCopy()
	assert.ErrorIs(t, err, ErrTreeCopyShape)
}

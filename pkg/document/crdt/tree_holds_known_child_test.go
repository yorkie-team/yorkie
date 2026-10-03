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

// holdsKnownChild is what orderSameBoundarySplit (§7.8) reads to decide that a
// chain node holds the right half, so its answer decides where a concurrent
// same-boundary split lands. These pin that answer directly: the end-to-end
// suites reach it only through a split that happens to produce one shape.
//
// A child a merge relocated here counts like any other, as it does in
// yorkie-js-sdk#1435; a cyclic parent/child graph is walked once per node
// rather than forever. See holdsKnownChild's comment, and
// docs/design/concurrent-merge-split.md for the shared rule.

// knownChildFixture builds <r><p></p></r> and hands out tickets for the editor
// (whose edits the version vector covers) and for a concurrent peer (whose do
// not).
type knownChildFixture struct {
	tree *Tree
	p    *TreeNode

	// editorTicket issues a ticket the version vector below covers.
	editorTicket func() *time.Ticket
	// peerTicket issues a ticket it does not.
	peerTicket func() *time.Ticket
	// vv knows everything the editor did and nothing the peer did.
	vv time.VersionVector

	// peer is the actor peerTicket issues for.
	peer time.ActorID
}

func newKnownChildFixture(t *testing.T) *knownChildFixture {
	t.Helper()

	editor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	peer, err := time.ActorIDFromHex("000000000000000000000002")
	require.NoError(t, err)

	lamport := int64(0)
	ticket := func(actor time.ActorID) *time.Ticket {
		lamport++
		return time.NewTicket(lamport, 0, actor)
	}
	editorTicket := func() *time.Ticket { return ticket(editor) }
	peerTicket := func() *time.Ticket { return ticket(peer) }

	root := NewTreeNode(NewTreeNodeID(editorTicket(), 0), "r", nil)
	p := NewTreeNode(NewTreeNodeID(editorTicket(), 0), "p", nil)
	require.NoError(t, root.Append(p))

	f := &knownChildFixture{
		tree:         NewTree(root, editorTicket()),
		p:            p,
		editorTicket: editorTicket,
		peerTicket:   peerTicket,
		vv:           time.VersionVector{editor: time.MaxLamport},
		peer:         peer,
	}

	return f
}

// namesPeer returns the fixture's vector extended with an entry for the peer
// at lamport 0 -- the shape time.MinVersionVector produces for an actor some
// attached client's vector does not carry. PurgeHeldBack only waits on actors
// the collecting vector names, so a barrier test has to use this one.
func (f *knownChildFixture) namesPeer() time.VersionVector {
	vv := f.vv.DeepCopy()
	vv.Set(f.peer, 0)

	return vv
}

// linkAsSplitProduct marks node the way SplitElement does, so PurgeHeldBack
// sees a node orderSameBoundarySplit's InsNextID walk could land on.
func linkAsSplitProduct(prev, node *TreeNode) {
	prev.InsNextID = node.id
	node.InsPrevID = prev.id
}

// appendText hangs a text child off p, created by the given actor's ticket.
func (f *knownChildFixture) appendText(t *testing.T, parent *TreeNode, createdAt *time.Ticket) *TreeNode {
	t.Helper()

	child := NewTreeNode(NewTreeNodeID(createdAt, 0), "text", nil, "ab")
	require.NoError(t, parent.Append(child))

	return child
}

// appendElement hangs an element child off parent, created by the given
// actor's ticket.
func (f *knownChildFixture) appendElement(t *testing.T, parent *TreeNode, createdAt *time.Ticket) *TreeNode {
	t.Helper()

	child := NewTreeNode(NewTreeNodeID(createdAt, 0), "span", nil)
	require.NoError(t, parent.Append(child))

	return child
}

// holds asks the §7.8 marker about node, with a descent set of its own.
func (f *knownChildFixture) holds(node *TreeNode) bool {
	return f.tree.holdsKnownChild(node, f.vv, &nodeSet{})
}

func TestTreeHoldsKnownChild(t *testing.T) {
	t.Run("reports nothing for a childless node", func(t *testing.T) {
		f := newKnownChildFixture(t)
		assert.False(t, f.holds(f.p))
	})

	t.Run("counts a child the editor knew", func(t *testing.T) {
		f := newKnownChildFixture(t)
		f.appendText(t, f.p, f.editorTicket())

		assert.True(t, f.holds(f.p))
	})

	t.Run("ignores a child a peer inserted concurrently", func(t *testing.T) {
		f := newKnownChildFixture(t)
		f.appendText(t, f.p, f.peerTicket())

		assert.False(t, f.holds(f.p))
	})

	// A multi-level split hides the marker one level down: the outer product
	// holds a single unknown element, and only below it sits the known text.
	t.Run("descends past an unknown element child", func(t *testing.T) {
		f := newKnownChildFixture(t)
		span := f.appendElement(t, f.p, f.peerTicket())
		f.appendText(t, span, f.editorTicket())

		assert.True(t, f.holds(f.p))
	})

	// The descent terminates on a node with nothing known anywhere below it,
	// which is the answer §7.8 walks on from -- the "false" direction of the
	// case above.
	t.Run("descends past unknown element children and reports nothing", func(t *testing.T) {
		f := newKnownChildFixture(t)
		span := f.appendElement(t, f.p, f.peerTicket())
		inner := f.appendElement(t, span, f.peerTicket())
		f.appendText(t, inner, f.peerTicket())

		assert.False(t, f.holds(f.p))
	})

	// Counting tombstones is what keeps the answer the same whether or not
	// this replica has applied a concurrent removal yet.
	t.Run("counts a child that has since been removed", func(t *testing.T) {
		f := newKnownChildFixture(t)
		child := f.appendText(t, f.p, f.editorTicket())
		child.remove(f.peerTicket())
		require.True(t, child.IsRemoved())

		assert.True(t, f.holds(f.p))
	})

	// §6.1/§6.3 relocate children keeping their original createdAt, and the
	// marker reads them as it finds them, whether or not the editor knew the
	// merge. A skip was tried in two forms (MergedFrom presence, and MergedAt
	// scoped to the editor's vector) and diverged on more scripts than it
	// fixed; yorkie-js-sdk#1435 has no skip either, and the two SDKs have to
	// answer alike.
	t.Run("counts a merge-moved child whoever's merge it was", func(t *testing.T) {
		for _, known := range []bool{true, false} {
			f := newKnownChildFixture(t)
			span := f.appendElement(t, f.p, f.peerTicket())
			mergedAt := f.peerTicket()
			if known {
				mergedAt = f.editorTicket()
			}
			span.MergedFrom = NewTreeNodeID(f.peerTicket(), 0)
			span.MergedAt = mergedAt
			f.appendText(t, span, f.editorTicket())

			assert.True(t, f.holds(f.p), "merge known to the editor: %v", known)
		}
	})

	// An empty vector reads as "knows everything" in time.TicketKnown, which
	// would make every child a marker. §7.8 returns before it can get here,
	// but the helper answers for itself.
	t.Run("reports nothing for an empty version vector", func(t *testing.T) {
		f := newKnownChildFixture(t)
		f.appendText(t, f.p, f.editorTicket())

		assert.False(t, f.tree.holdsKnownChild(f.p, time.VersionVector{}, &nodeSet{}))
	})

	// A parent/child cycle has no test here because it cannot be built:
	// index.MoveChild has no ancestor check, but closing the loop through it
	// hangs inside MoveChild's own ancestor length update, before this reader
	// ever runs. The node set is insurance for a graph that arrives some other
	// way -- the descent is over physical pointers, which this file elsewhere
	// treats as peer-shaped (see insNextWalker).
	//
	// The descent budget is shared across one §7.8 chain walk, so a subtree
	// already proven to hold nothing known is not walked a second time.
	t.Run("does not descend the same subtree twice", func(t *testing.T) {
		f := newKnownChildFixture(t)
		span := f.appendElement(t, f.p, f.peerTicket())
		f.appendText(t, span, f.peerTicket())

		descended := &nodeSet{}
		assert.False(t, f.tree.holdsKnownChild(f.p, f.vv, descended))
		assert.Len(t, descended.seen, 2, "p and span; text children are not descended")

		// The tree does not change during one chain walk; this does it anyway,
		// to show the second ask is answered without going below span again.
		f.appendText(t, span, f.editorTicket())
		assert.False(t, f.tree.holdsKnownChild(f.p, f.vv, descended),
			"span was already descended; re-walking it would have found the known text")
	})

	// The risky direction of sharing the budget: a node the walk already
	// descended *through* can come back as a chain node of its own, and the
	// cache holds the answer to a different question ("nothing known below
	// this subtree"), not to the one §7.8 asks of a chain sibling. The entry
	// node's own children decide, every time.
	t.Run("re-asks a node an earlier descent passed through", func(t *testing.T) {
		f := newKnownChildFixture(t)
		span := f.appendElement(t, f.p, f.peerTicket())
		f.appendText(t, span, f.peerTicket())

		descended := &nodeSet{}
		require.False(t, f.tree.holdsKnownChild(f.p, f.vv, descended))
		require.Contains(t, descended.seen, span, "the descent went through span")

		// span now holds a child the editor knew. Asked as a chain node of its
		// own it is a marker; the cached "nothing known below p" must not
		// answer for it.
		f.appendText(t, span, f.editorTicket())

		assert.True(t, f.tree.holdsKnownChild(span, f.vv, descended))
	})
}

// PurgeHeldBack is the GC half of holdsKnownChild's argument: a tombstone stays
// linked while an ancestor orderSameBoundarySplit could land on is outside the
// collecting vector, because a split that has not arrived yet could reach that
// ancestor as an unknown chain sibling and count the tombstone below it.
//
// It waits on that ancestor only, not on every ancestor: a min version vector
// carries 0 for any actor some attached client's vector lacks and names no
// actor that has detached, so a barrier keyed on ancestry alone would gate
// nearly all tree garbage behind tickets that may never be covered.
func TestTreePurgeHeldBack(t *testing.T) {
	// splitProductFixture hangs an unknown split product off p and removes a
	// child of it -- the shape §7.8 descends into.
	splitProduct := func(t *testing.T) (*knownChildFixture, *TreeNode, *TreeNode) {
		t.Helper()

		f := newKnownChildFixture(t)
		span := f.appendElement(t, f.p, f.peerTicket())
		linkAsSplitProduct(f.p, span)
		child := f.appendText(t, span, f.editorTicket())
		child.remove(f.editorTicket())

		return f, span, child
	}

	t.Run("holds a tombstone below a split product the vector does not cover", func(t *testing.T) {
		f, _, child := splitProduct(t)

		assert.True(t, f.tree.PurgeHeldBack(child, f.namesPeer()),
			"the product is the peer's, behind the vector's entry for it")
	})

	t.Run("lets a tombstone go once the split product is covered", func(t *testing.T) {
		f := newKnownChildFixture(t)
		span := f.appendElement(t, f.p, f.editorTicket())
		linkAsSplitProduct(f.p, span)
		child := f.appendText(t, span, f.editorTicket())
		child.remove(f.editorTicket())

		assert.False(t, f.tree.PurgeHeldBack(child, f.namesPeer()))
	})

	// §7.8 never descends into a node outside an InsNextID chain, so an
	// ordinary element -- which is what almost every tombstone sits under --
	// is no reason to wait.
	t.Run("does not wait on an ancestor outside any split chain", func(t *testing.T) {
		f := newKnownChildFixture(t)
		span := f.appendElement(t, f.p, f.peerTicket())
		child := f.appendText(t, span, f.editorTicket())
		child.remove(f.editorTicket())

		assert.False(t, f.tree.PurgeHeldBack(child, f.namesPeer()))
	})

	// §7.8 breaks on a removed chain sibling before it descends, so one cannot
	// be the node whose answer a purge would change.
	t.Run("does not wait on a removed split product", func(t *testing.T) {
		f, span, child := splitProduct(t)
		span.remove(f.peerTicket())
		require.True(t, span.IsRemoved())

		assert.False(t, f.tree.PurgeHeldBack(child, f.namesPeer()))
	})

	// The collecting vector naming no entry for the actor means the actor is
	// not one this collection is synced against: either it detached (and
	// pushed before its entry went) or the ticket was forged. Reading it as
	// uncovered pinned the subtree for the life of the document.
	t.Run("does not wait on an actor the vector does not name", func(t *testing.T) {
		f, _, child := splitProduct(t)

		assert.False(t, f.tree.PurgeHeldBack(child, f.vv),
			"f.vv names the editor only; the peer's entry is gone, not pending")
	})

	// time.InitialTicket carries an actor no vector names; reading it as
	// uncovered would hold every tombstone in such a tree forever.
	t.Run("does not wait on a lamport-0 ancestor", func(t *testing.T) {
		f := newKnownChildFixture(t)
		root := NewTreeNode(NewTreeNodeID(time.InitialTicket, 0), "r", nil)
		child := f.appendText(t, root, f.editorTicket())
		child.remove(f.editorTicket())

		assert.False(t, f.tree.PurgeHeldBack(child, f.namesPeer()))
	})

	// The climb is bounded, so a GC pass stays O(tombstones) over parent
	// pointers a peer controls however deep -- or however cyclic -- they are.
	t.Run("gives up past the climb bound instead of walking on", func(t *testing.T) {
		f := newKnownChildFixture(t)

		// The uncovered split product sits above the bound, out of reach.
		marker := f.appendElement(t, f.p, f.peerTicket())
		linkAsSplitProduct(f.p, marker)

		deepest := marker
		for range maxHeldBackClimb + 2 {
			deepest = f.appendElement(t, deepest, f.editorTicket())
		}

		child := f.appendText(t, deepest, f.editorTicket())
		child.remove(f.editorTicket())

		assert.False(t, f.tree.PurgeHeldBack(child, f.namesPeer()))
	})
}

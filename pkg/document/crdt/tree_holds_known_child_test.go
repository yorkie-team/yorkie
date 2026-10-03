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
// A child a merge relocated here is not evidence and is skipped with its
// subtree; a cyclic parent/child graph is walked once per node rather than
// forever. See holdsKnownChild's comment, and
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
	}

	return f
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

	// §6.1/§6.3 relocate children keeping their original createdAt, so a
	// concurrent merge can hand an otherwise-empty same-boundary product a
	// child the editor knew long after the split that produced it. Whether it
	// has arrived yet differs per replica, so it is not a marker.
	t.Run("ignores a child a concurrent merge moved in", func(t *testing.T) {
		f := newKnownChildFixture(t)
		child := f.appendText(t, f.p, f.editorTicket())
		mergedAt := f.peerTicket()
		child.MergedFrom = NewTreeNodeID(f.peerTicket(), 0)
		child.MergedAt = mergedAt
		require.False(t, time.TicketKnown(f.vv, mergedAt),
			"the merge has to be one the editor did not know, or there is nothing to skip")

		assert.False(t, f.holds(f.p),
			"a merge put this child here; it never marked the right half")
	})

	// MergedFrom is read as presence, with no ticket comparison: a merge the
	// editor did know moves the child just the same, and skipping it only
	// falls the walk back to the one that ran before this check existed.
	t.Run("ignores a child a merge the editor knew moved in", func(t *testing.T) {
		f := newKnownChildFixture(t)
		child := f.appendText(t, f.p, f.editorTicket())
		child.MergedFrom = NewTreeNodeID(f.editorTicket(), 0)
		child.MergedAt = f.editorTicket()

		assert.False(t, f.holds(f.p))
	})

	// The skip covers what rode in under the moved child too: those arrived
	// with the merge as well, and carry no MergedFrom of their own.
	t.Run("ignores what a merge-moved child brought with it", func(t *testing.T) {
		f := newKnownChildFixture(t)
		span := f.appendElement(t, f.p, f.peerTicket())
		span.MergedFrom = NewTreeNodeID(f.peerTicket(), 0)
		f.appendText(t, span, f.editorTicket())

		assert.False(t, f.holds(f.p))
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
		assert.False(t, f.tree.holdsKnownChild(f.p, f.vv, descended))
	})
}

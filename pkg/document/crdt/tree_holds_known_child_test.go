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
// The merge-moved case is pinned as the known limitation it is -- a child a
// concurrent merge relocated is counted like any other. See holdsKnownChild's
// comment for why MergedAt is not a ticket this reader can trust, and
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

func TestTreeHoldsKnownChild(t *testing.T) {
	t.Run("reports nothing for a childless node", func(t *testing.T) {
		f := newKnownChildFixture(t)
		assert.False(t, f.tree.holdsKnownChild(f.p, f.vv))
	})

	t.Run("counts a child the editor knew", func(t *testing.T) {
		f := newKnownChildFixture(t)
		f.appendText(t, f.p, f.editorTicket())

		assert.True(t, f.tree.holdsKnownChild(f.p, f.vv))
	})

	t.Run("ignores a child a peer inserted concurrently", func(t *testing.T) {
		f := newKnownChildFixture(t)
		f.appendText(t, f.p, f.peerTicket())

		assert.False(t, f.tree.holdsKnownChild(f.p, f.vv))
	})

	// A multi-level split hides the marker one level down: the outer product
	// holds a single unknown element, and only below it sits the known text.
	t.Run("descends past an unknown element child", func(t *testing.T) {
		f := newKnownChildFixture(t)
		span := f.appendElement(t, f.p, f.peerTicket())
		f.appendText(t, span, f.editorTicket())

		assert.True(t, f.tree.holdsKnownChild(f.p, f.vv))
	})

	// The descent terminates on a node with nothing known anywhere below it,
	// which is the answer §7.8 walks on from -- the "false" direction of the
	// case above.
	t.Run("descends past unknown element children and reports nothing", func(t *testing.T) {
		f := newKnownChildFixture(t)
		span := f.appendElement(t, f.p, f.peerTicket())
		inner := f.appendElement(t, span, f.peerTicket())
		f.appendText(t, inner, f.peerTicket())

		assert.False(t, f.tree.holdsKnownChild(f.p, f.vv))
	})

	// Counting tombstones is what keeps the answer the same whether or not
	// this replica has applied a concurrent removal yet.
	t.Run("counts a child that has since been removed", func(t *testing.T) {
		f := newKnownChildFixture(t)
		child := f.appendText(t, f.p, f.editorTicket())
		child.remove(f.peerTicket())
		require.True(t, child.IsRemoved())

		assert.True(t, f.tree.holdsKnownChild(f.p, f.vv))
	})

	// KNOWN LIMITATION, pinned rather than fixed: §6.1/§6.3 relocate children
	// keeping their original createdAt, so a concurrent merge can hand an
	// otherwise-empty same-boundary product a child the editor knew long after
	// the split that produced it -- and the walk then stops at a node that
	// never held the right half. MergedAt is not a ticket that can tell the
	// two apart here (see holdsKnownChild), and the rule is replicated, so the
	// answer stays "counted" until Go and yorkie-js-sdk move together against
	// a reproducer.
	t.Run("counts a child a concurrent merge moved in", func(t *testing.T) {
		f := newKnownChildFixture(t)
		child := f.appendText(t, f.p, f.editorTicket())
		mergedAt := f.peerTicket()
		child.MergedFrom = NewTreeNodeID(f.peerTicket(), 0)
		child.MergedAt = mergedAt
		require.False(t, time.TicketKnown(f.vv, mergedAt),
			"the merge has to be one the editor did not know, or there is nothing to skip")

		assert.True(t, f.tree.holdsKnownChild(f.p, f.vv),
			"a merge-moved child counts like any other; see the comment above")
	})

	// The same child, moved by a merge the editor did know. Both readings
	// agree here, so this one pins the half of the rule that is not in doubt.
	t.Run("counts a child a merge the editor knew moved in", func(t *testing.T) {
		f := newKnownChildFixture(t)
		child := f.appendText(t, f.p, f.editorTicket())
		child.MergedFrom = NewTreeNodeID(f.editorTicket(), 0)
		child.MergedAt = f.editorTicket()

		assert.True(t, f.tree.holdsKnownChild(f.p, f.vv))
	})
}

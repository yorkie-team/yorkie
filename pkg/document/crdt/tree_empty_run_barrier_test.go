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

// emptyRunReachesActor is the second reader splitChainBarriersAt stands for.
// These pin which of its branches a causally stable createdAt retires and
// which it does not -- the question the barrier's doc comment answers in
// prose.

// emptyRunFixture builds <r><a></a><b></b><c></c></r> with the chain
// a -> b -> c, every node registered in NodeMapByID so the chain walk can
// resolve it. The editor owns b; a and c belong to a peer the version vector
// does not cover.
type emptyRunFixture struct {
	tree        *Tree
	a, b, c     *TreeNode
	editor      time.ActorID
	peerTicket  func() *time.Ticket
	otherTicket func() *time.Ticket
	// vv covers everything the editor did -- including b, the node the walk
	// short-circuits on -- and everything a third actor did, which is how a
	// node becomes "one the editor knows" without being one it made. It covers
	// nothing the peer did.
	vv time.VersionVector
}

func newEmptyRunFixture(t *testing.T) *emptyRunFixture {
	t.Helper()

	editor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	peer, err := time.ActorIDFromHex("000000000000000000000002")
	require.NoError(t, err)
	other, err := time.ActorIDFromHex("000000000000000000000003")
	require.NoError(t, err)

	lamport := int64(0)
	ticket := func(actor time.ActorID) *time.Ticket {
		lamport++
		return time.NewTicket(lamport, 0, actor)
	}
	peerTicket := func() *time.Ticket { return ticket(peer) }
	otherTicket := func() *time.Ticket { return ticket(other) }

	element := func(createdAt *time.Ticket) *TreeNode {
		return NewTreeNode(NewTreeNodeID(createdAt, 0), "p", nil)
	}

	root := NewTreeNode(NewTreeNodeID(ticket(editor), 0), "r", nil)
	a := element(peerTicket())
	b := element(ticket(editor))
	c := element(peerTicket())
	require.NoError(t, root.Append(a, b, c))

	a.InsNextID = b.id
	b.InsPrevID = a.id
	b.InsNextID = c.id
	c.InsPrevID = b.id

	return &emptyRunFixture{
		tree:        NewTree(root, ticket(editor)),
		a:           a,
		b:           b,
		c:           c,
		editor:      editor,
		peerTicket:  peerTicket,
		otherTicket: otherTicket,
		vv: time.VersionVector{
			editor: time.MaxLamport,
			other:  time.MaxLamport,
		},
	}
}

func TestTreeEmptyRunReachesActorBarrier(t *testing.T) {
	// The count leg, which the InsNextID barrier does retire: once the walk's
	// start node is one the editor knows, the answer is false whether or not
	// it still holds children, so purging a counted tombstone under it cannot
	// change where the split lands.
	t.Run("answers the same for a known node whatever it holds", func(t *testing.T) {
		f := newEmptyRunFixture(t)
		// The start node has to be known to the editor WITHOUT being the
		// editor's own: a node the editor made returns at the actor-ID branch
		// (tree.go emptyRunReachesActor) before either branch this subtest is
		// about is reached. A third actor the version vector covers is how
		// causal stability makes a peer's node "known".
		known := NewTreeNode(NewTreeNodeID(f.otherTicket(), 0), "p", nil)
		require.NoError(t, f.tree.IndexTree.Root().Value.Append(known))
		f.tree.putNode(known)
		known.InsNextID = f.b.id
		require.NotEqual(t, f.editor, known.id.CreatedAt.ActorID(),
			"the actor-ID branch must not short-circuit this walk")
		require.True(t, time.TicketKnown(f.vv, known.id.CreatedAt))

		// The control: the same walk under a vector that does NOT cover the
		// start node runs on through the chain and reaches the editor's own
		// b. Every false below is therefore a branch deciding, not a walk
		// that had nowhere to go.
		narrow := time.VersionVector{f.editor: time.MaxLamport}
		require.Empty(t, known.Index.Children(true))
		require.True(t, f.tree.emptyRunReachesActor(known, f.editor, narrow),
			"without coverage of the start node the walk answers true")

		// Empty and known: the version-vector branch decides.
		empty := f.tree.emptyRunReachesActor(known, f.editor, f.vv)

		// Holding a child: the count branch is reached before the version
		// vector, and the narrow vector shows it deciding on its own.
		child := NewTreeNode(NewTreeNodeID(f.peerTicket(), 0), "text", nil, "ab")
		require.NoError(t, known.Append(child))
		require.NotEmpty(t, known.Index.Children(true))

		assert.False(t, empty)
		assert.False(t, f.tree.emptyRunReachesActor(known, f.editor, narrow),
			"the count branch alone answers false once a child is held")
		assert.False(t, f.tree.emptyRunReachesActor(known, f.editor, f.vv),
			"a known node answers false through the count branch and through "+
				"the version-vector branch alike")
	})

	// The actor-ID leg, which NO ticket retires: it is tested before the
	// version vector, and an actor always knows its own tickets, so causal
	// stability never stops it from firing. Purging b relinks the chain
	// across it (Tree.Purge), and the answer flips -- the residual recorded
	// in splitChainBarriersAt and in
	// docs/design/concurrent-merge-split.md. Pinned, not fixed: closing it
	// means making the walk purge-invariant, a replicated rule that moves in
	// Go and yorkie-js-sdk together.
	t.Run("flips when a chain node the editor owns is purged", func(t *testing.T) {
		f := newEmptyRunFixture(t)
		f.b.remove(f.peerTicket())
		require.True(t, f.b.IsRemoved())

		assert.True(t, f.tree.emptyRunReachesActor(f.a, f.editor, f.vv),
			"the walk reaches the editor's own product through the empty run")
		require.True(t, time.TicketKnown(f.vv, f.b.id.CreatedAt),
			"b is as causally stable as a barrier can make it, and the "+
				"answer above still came from the actor-ID branch")

		require.NoError(t, f.tree.Purge(f.b))
		require.Equal(t, f.c.id, f.a.InsNextID, "Purge relinks the chain across b")

		assert.False(t, f.tree.emptyRunReachesActor(f.a, f.editor, f.vv),
			"same walk, same version vector, different answer: the residual")
	})
}

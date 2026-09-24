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

// mergeLineageFixture builds <r><p1></p1><p2>cd</p2><p3></p3></r>, the shape
// every case below forges a merge lineage over: p1 is the merge destination,
// p2 holds the child that carries MergedFrom, and p3 is the unrelated live
// element a forged pointer tries to name.
type mergeLineageFixture struct {
	tree               *Tree
	p1, p2, p3, text   *TreeNode
	nextTicket         func() *time.Ticket
	forgedIDNamingNone func(*TreeNode) *TreeNodeID
}

func newMergeLineageFixture(t *testing.T) *mergeLineageFixture {
	t.Helper()

	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)

	lamport := int64(0)
	nextTicket := func() *time.Ticket {
		lamport++
		return time.NewTicket(lamport, 0, actor)
	}

	root := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "r", nil)
	p1 := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "p", nil)
	p2 := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "p", nil)
	p3 := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "p", nil)
	text := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "text", nil, "cd")
	require.NoError(t, root.Append(p1, p2, p3))
	require.NoError(t, p2.Append(text))

	return &mergeLineageFixture{
		tree:       NewTree(root, nextTicket()),
		p1:         p1,
		p2:         p2,
		p3:         p3,
		text:       text,
		nextTicket: nextTicket,
		// Same CreatedAt, an offset the node never had: the floor lookup
		// answers with the node, an exact-ID lookup does not.
		forgedIDNamingNone: func(node *TreeNode) *TreeNodeID {
			return NewTreeNodeID(node.id.CreatedAt, node.id.Offset+9)
		},
	}
}

// TestFindMergeNodeRequiresExactElement pins what a merge pointer has to name.
//
// MergedFrom survives on an element payload (Set/Add/ArraySet keep the lineage
// a reverse-of-Remove legitimately carries), so both ends of a merge relation
// can be client-supplied. A floor match is not enough: it would let a made-up
// offset name a node the merge never touched.
func TestFindMergeNodeRequiresExactElement(t *testing.T) {
	f := newMergeLineageFixture(t)

	assert.Same(t, f.p3, f.tree.findMergeNode(f.p3.id),
		"the exact ID of an element is the one shape a merge records")
	assert.Nil(t, f.tree.findMergeNode(f.forgedIDNamingNone(f.p3)),
		"an offset the node never had must not resolve to it")
	assert.Nil(t, f.tree.findMergeNode(f.text.id),
		"a text node holds no children, so it is neither end of a merge")
	assert.Nil(t, f.tree.findMergeNode(nil))
}

// TestRebuildMergeStateRejectsForgedSource pins the decode boundary: a
// MergedFrom that only floors onto a live element plants no forwarding pointer
// on it, while the genuine one still rebuilds.
func TestRebuildMergeStateRejectsForgedSource(t *testing.T) {
	t.Run("forged source", func(t *testing.T) {
		f := newMergeLineageFixture(t)
		f.text.MergedFrom = f.forgedIDNamingNone(f.p3)
		f.text.MergedAt = f.nextTicket()

		// Re-read exactly as the converter re-reads an element payload.
		NewTree(f.tree.Root(), f.nextTicket())

		assert.Nil(t, f.p3.mergedInto,
			"a later delete would follow this pointer and tombstone p3's children")
	})

	t.Run("genuine source", func(t *testing.T) {
		f := newMergeLineageFixture(t)
		f.text.MergedFrom = f.p3.id
		f.text.MergedAt = f.nextTicket()

		NewTree(f.tree.Root(), f.nextTicket())

		require.NotNil(t, f.p3.mergedInto)
		assert.True(t, f.p3.mergedInto.Equal(f.p2.id),
			"the lineage an element payload legitimately carries still rebuilds")
	})
}

// TestMergeNodesRejectsForgedSource pins that the check holds for the whole
// lifetime of the field, not only at the decode that first saw it.
//
// MergedFrom is retained on the node inside the live document, and mergeNodes
// re-derives the forwarding pointer from it whenever an ordinary, innocent
// merge moves that node again. Resolved by floor there, the forged offset
// would plant on p3 the pointer the decode refused to.
func TestMergeNodesRejectsForgedSource(t *testing.T) {
	f := newMergeLineageFixture(t)
	f.text.MergedFrom = f.forgedIDNamingNone(f.p3)
	f.text.MergedAt = f.nextTicket()

	// An ordinary merge of p2 into p1 moves the child that carries the forged
	// lineage; the child keeps it, because MergedFrom is stamped only on a
	// first move.
	require.NoError(t, f.tree.mergeNodes(f.p1, []*TreeNode{f.text}, f.nextTicket()))

	assert.Nil(t, f.p3.mergedInto,
		"an ordinary merge must not plant a forwarding pointer on an unrelated node")
	assert.Same(t, f.p1, f.text.Index.Parent.Value,
		"the merge itself still moves the child")
}

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

package converter_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"

	"github.com/yorkie-team/yorkie/api/converter"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// boundaryTree returns <r><p>a</p><p></p><p></p></r>: the second paragraph
// is a split product anchored before a, and the third is anchored at the end
// of the family and nested under the second.
func boundaryTree(t *testing.T) (*crdt.Tree, []*crdt.TreeNode) {
	t.Helper()

	actor := time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	ticket := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }
	root := crdt.NewTreeNode(crdt.NewTreeNodeID(ticket(1), 0), "r", nil)
	first := crdt.NewTreeNode(crdt.NewTreeNodeID(ticket(2), 0), "p", nil)
	text := crdt.NewTreeNode(crdt.NewTreeNodeID(ticket(3), 0), "text", nil, "a")
	second := crdt.NewTreeNode(crdt.NewTreeNodeID(ticket(4), 0), "p", nil)
	third := crdt.NewTreeNode(crdt.NewTreeNodeID(ticket(5), 0), "p", nil)
	require.NoError(t, first.Append(text))
	require.NoError(t, root.Append(first, second, third))
	second.BoundaryAnchor = text.ID()
	third.BoundaryAtEnd = true
	third.BoundaryNest = second.ID()
	return crdt.NewTree(root, ticket(1)), []*crdt.TreeNode{first, text, second, third}
}

// TestTreeBoundaryRecordSnapshotRoundTrip checks that a tree snapshot carries
// the boundary records of its split products, which later splits read for as
// long as the products live.
func TestTreeBoundaryRecordSnapshotRoundTrip(t *testing.T) {
	tree, nodes := boundaryTree(t)
	bytes, err := converter.TreeToBytes(tree)
	require.NoError(t, err)
	decoded, err := converter.BytesToTree(bytes)
	require.NoError(t, err)

	got := decoded.Boundaries()
	require.Len(t, got, 2)
	assert.True(t, got[0].Product.Equal(nodes[2].ID()))
	require.NotNil(t, got[0].Anchor)
	assert.True(t, got[0].Anchor.Equal(nodes[1].ID()))
	assert.Nil(t, got[0].Nest)
	assert.True(t, got[1].Product.Equal(nodes[3].ID()))
	assert.Nil(t, got[1].Anchor, "anchored at the end of the family")
	require.NotNil(t, got[1].Nest)
	assert.True(t, got[1].Nest.Equal(nodes[2].ID()))
}

// TestTreeBoundaryRecordNamingNoNode checks that a record naming a node the
// tree does not hold is ignored, and one without a product is rejected.
func TestTreeBoundaryRecordNamingNoNode(t *testing.T) {
	tree, _ := boundaryTree(t)
	pb := converter.ToTreeNodes(tree.Root())
	actor := time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	missing := &api.TreeNodeID{CreatedAt: converter.ToTimeTicket(time.NewTicket(99, 0, actor))}
	elem := func(bs ...*api.TreeBoundary) []byte {
		bytes, err := proto.Marshal(&api.JSONElement{Body: &api.JSONElement_Tree_{Tree: &api.JSONElement_Tree{
			Nodes:      pb,
			CreatedAt:  converter.ToTimeTicket(tree.CreatedAt()),
			Boundaries: bs,
		}}})
		require.NoError(t, err)
		return bytes
	}

	decoded, err := converter.BytesToTree(elem(&api.TreeBoundary{Product: missing}))
	require.NoError(t, err)
	assert.Empty(t, decoded.Boundaries())

	_, err = converter.BytesToTree(elem(&api.TreeBoundary{}))
	assert.Error(t, err)
}

// TestTreeMergeTicketsSnapshotRoundTrip checks that a tree snapshot carries
// the merge tickets of a node that two merges removed, which later splits
// read to tell whether they knew the node was merged away, and drops one
// naming no node.
func TestTreeMergeTicketsSnapshotRoundTrip(t *testing.T) {
	tree, nodes := boundaryTree(t)
	actor := time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}
	tickets := []*time.Ticket{time.NewTicket(7, 0, actor), time.NewTicket(9, 0, actor)}
	tree.RestoreMerges([]crdt.TreeMerge{
		{Source: nodes[3].ID(), Tickets: []*time.Ticket{tickets[1], tickets[0]}},
		{Source: crdt.NewTreeNodeID(time.NewTicket(99, 0, actor), 0), Tickets: tickets},
	})
	bytes, err := converter.TreeToBytes(tree)
	require.NoError(t, err)
	decoded, err := converter.BytesToTree(bytes)
	require.NoError(t, err)

	got := decoded.Merges()
	require.Len(t, got, 1)
	assert.True(t, got[0].Source.Equal(nodes[3].ID()))
	require.Len(t, got[0].Tickets, 2)
	assert.Equal(t, 0, got[0].Tickets[0].Compare(tickets[0]), "sorted")
	assert.Equal(t, 0, got[0].Tickets[1].Compare(tickets[1]))
}

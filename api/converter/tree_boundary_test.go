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

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// boundaryNodes returns <r><p>a</p><p></p></r> with the second paragraph
// recorded as a split product anchored before a and nested under the first.
func boundaryNodes(t *testing.T, ticket func(int64) *time.Ticket) (*crdt.TreeNode, *crdt.TreeNode) {
	t.Helper()

	root := crdt.NewTreeNode(crdt.NewTreeNodeID(ticket(1), 0), "r", nil)
	first := crdt.NewTreeNode(crdt.NewTreeNodeID(ticket(2), 0), "p", nil)
	text := crdt.NewTreeNode(crdt.NewTreeNodeID(ticket(3), 0), "text", nil, "a")
	product := crdt.NewTreeNode(crdt.NewTreeNodeID(ticket(4), 0), "p", nil)
	require.NoError(t, first.Append(text))
	require.NoError(t, root.Append(first, product))
	product.BoundaryAnchor = text.ID()
	product.BoundaryAtEnd = true
	product.BoundaryNest = first.ID()
	return root, product
}

// TestTreeBoundaryRecordSnapshotRoundTrip checks that a snapshot carries the
// boundary record of a split product, which later splits read for as long as
// the node lives.
func TestTreeBoundaryRecordSnapshotRoundTrip(t *testing.T) {
	actor := time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	ticket := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }
	root, product := boundaryNodes(t, ticket)

	decoded, err := converter.FromTreeNodes(converter.ToTreeNodes(root))
	require.NoError(t, err)
	got := decoded.Index.Children(true)[1].Value

	require.NotNil(t, got.BoundaryAnchor)
	assert.True(t, got.BoundaryAnchor.Equal(product.BoundaryAnchor))
	assert.True(t, got.BoundaryAtEnd)
	require.NotNil(t, got.BoundaryNest)
	assert.True(t, got.BoundaryNest.Equal(product.BoundaryNest))

	first := decoded.Index.Children(true)[0].Value
	assert.Nil(t, first.BoundaryAnchor, "a node without a record decodes without one")
	assert.False(t, first.BoundaryAtEnd)
	assert.Nil(t, first.BoundaryNest)
}

// TestTreeEditContentDropsBoundaryRecord checks that operation content cannot
// carry a boundary record. Content is freshly created by the editing client,
// so it is never a split product, and the split rules read the record as
// trusted state.
func TestTreeEditContentDropsBoundaryRecord(t *testing.T) {
	actor := time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	ticket := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }
	_, product := boundaryNodes(t, ticket)
	require.NoError(t, product.Index.Parent.RemoveChild(product.Index))

	pos := crdt.NewTreePos(crdt.NewTreeNodeID(ticket(1), 0), crdt.NewTreeNodeID(ticket(1), 0))
	pbOps, err := converter.ToOperations([]operations.Operation{
		operations.NewTreeEdit(ticket(1), pos, pos, []*crdt.TreeNode{product}, 0, ticket(5)),
	})
	require.NoError(t, err)

	decoded, err := converter.FromOperations(pbOps)
	require.NoError(t, err)
	edit, ok := decoded[0].(*operations.TreeEdit)
	require.True(t, ok)
	content := edit.Contents()[0]
	assert.Nil(t, content.BoundaryAnchor)
	assert.False(t, content.BoundaryAtEnd)
	assert.Nil(t, content.BoundaryNest)
}

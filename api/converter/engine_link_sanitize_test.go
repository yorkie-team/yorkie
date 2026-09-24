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

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// A Set/Add/SetByIndex payload carries a whole element as bytes, decoded by
// the same BytesToObject/BytesToTree that reads a server-built snapshot. Its
// tree nodes are freshly created by the editing client, so none of them can be
// a split product or a merge survivor — but the wire format carries
// InsPrevID/InsNextID and MergedFrom/MergedAt anyway and the tree follows them
// as trusted structural pointers, up to and including a chain that loops back
// on itself. Drop them on the way in, as FromTreeNodesWhenEdit does for
// operation content.
func TestSetElementDropsEngineOnlyLinks(t *testing.T) {
	actor := time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	ticket := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }

	build := func() *crdt.Tree {
		root := crdt.NewTreeNode(crdt.NewTreeNodeID(ticket(1), 0), "r", nil)
		tree := crdt.NewTree(root, ticket(1))
		_, _, err := tree.EditT(0, 0, []*crdt.TreeNode{
			crdt.NewTreeNode(crdt.NewTreeNodeID(ticket(2), 0), "p", nil),
		}, 0, ticket(3), func() *time.Ticket { return ticket(3) })
		assert.NoError(t, err)
		_, _, err = tree.EditT(1, 1, []*crdt.TreeNode{
			crdt.NewTreeNode(crdt.NewTreeNodeID(ticket(4), 0), "p", nil),
		}, 0, ticket(5), func() *time.Ticket { return ticket(5) })
		assert.NoError(t, err)
		return tree
	}

	for _, tc := range []struct {
		name string
		elem func(*crdt.Tree) crdt.Element
	}{
		{"tree", func(tree *crdt.Tree) crdt.Element { return tree }},
		{"tree nested in an object", func(tree *crdt.Tree) crdt.Element {
			obj := crdt.NewObject(crdt.NewElementRHT(), ticket(6))
			obj.Set("t", tree)
			return obj
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree := build()

			// Poison the two paragraphs into a cycle, and point each one's
			// merge lineage at the other, the way a crafted payload would.
			var paragraphs []*crdt.TreeNode
			for _, node := range tree.Nodes() {
				if node.Type() == "p" {
					paragraphs = append(paragraphs, node)
				}
			}
			assert.Len(t, paragraphs, 2)
			paragraphs[0].InsNextID = paragraphs[1].ID()
			paragraphs[0].InsPrevID = paragraphs[1].ID()
			paragraphs[1].InsNextID = paragraphs[0].ID()
			paragraphs[1].InsPrevID = paragraphs[0].ID()
			paragraphs[0].MergedFrom = paragraphs[1].ID()
			paragraphs[0].MergedAt = ticket(8)
			paragraphs[1].MergedFrom = paragraphs[0].ID()
			paragraphs[1].MergedAt = ticket(8)

			pbOps, err := converter.ToOperations([]operations.Operation{
				operations.NewSet(ticket(1), "k", tc.elem(tree), ticket(7)),
			})
			assert.NoError(t, err)

			decoded, err := converter.FromOperations(pbOps)
			assert.NoError(t, err)
			assert.Len(t, decoded, 1)

			set, ok := decoded[0].(*operations.Set)
			assert.True(t, ok)

			var decodedTree *crdt.Tree
			switch value := set.Value().(type) {
			case *crdt.Tree:
				decodedTree = value
			case *crdt.Object:
				decodedTree, ok = value.Get("t").(*crdt.Tree)
				assert.True(t, ok)
			}
			assert.NotNil(t, decodedTree)

			for _, node := range decodedTree.Nodes() {
				assert.Nil(t, node.InsNextID, "InsNextID should not survive decode")
				assert.Nil(t, node.InsPrevID, "InsPrevID should not survive decode")
				assert.Nil(t, node.MergedFrom, "MergedFrom should not survive decode")
				assert.Nil(t, node.MergedAt, "MergedAt should not survive decode")
			}
		})
	}
}

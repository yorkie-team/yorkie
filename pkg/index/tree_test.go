/*
 * Copyright 2023 The Yorkie Authors. All rights reserved.
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

package index_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestIndexTree is a test for IndexTree.
func TestIndexTree(t *testing.T) {
	t.Run("find position from the given offset", func(t *testing.T) {
		//    0   1 2 3 4 5 6    7   8 9  10 11 12 13    14
		// <r> <p> h e l l o </p> <p> w  o  r  l  d  </p>  </r>
		tree := helper.BuildIndexTree(&crdt.JSONTreeNode{
			Type: "r",
			Children: []crdt.JSONTreeNode{
				{Type: "p", Children: []crdt.JSONTreeNode{{Type: "text", Value: "hello"}}},
				{Type: "p", Children: []crdt.JSONTreeNode{{Type: "text", Value: "world"}}},
			},
		})

		pos := tree.FindTreePos(0)
		assert.Equal(t, "r", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 0, pos.Offset)
		pos = tree.FindTreePos(1)
		assert.Equal(t, "text.hello", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 0, pos.Offset)
		pos = tree.FindTreePos(6)
		assert.Equal(t, "text.hello", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 5, pos.Offset)
		pos = tree.FindTreePos(6, false)
		assert.Equal(t, "p", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 1, pos.Offset)
		pos = tree.FindTreePos(7)
		assert.Equal(t, "r", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 1, pos.Offset)
		pos = tree.FindTreePos(8)
		assert.Equal(t, "text.world", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 0, pos.Offset)
		pos = tree.FindTreePos(13)
		assert.Equal(t, "text.world", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 5, pos.Offset)
		pos = tree.FindTreePos(14)
		assert.Equal(t, "r", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 2, pos.Offset)
	})

	t.Run("find right node from the given offset in postorder traversal test", func(t *testing.T) {
		//       0   1 2 3    4   6 7     8
		// <root> <p> a b </p> <p> c d</p> </root>
		tree := helper.BuildIndexTree(&crdt.JSONTreeNode{
			Type: "root",
			Children: []crdt.JSONTreeNode{
				{Type: "p", Children: []crdt.JSONTreeNode{{Type: "text", Value: "ab"}}},
				{Type: "p", Children: []crdt.JSONTreeNode{{Type: "text", Value: "cd"}}},
			},
		})

		// postorder traversal: "ab", <b>, "cd", <p>, <root>
		assert.Equal(t, "text", tree.FindPostorderRight(tree.FindTreePos(0)).Type())
		assert.Equal(t, "text", tree.FindPostorderRight(tree.FindTreePos(1)).Type())
		assert.Equal(t, "p", tree.FindPostorderRight(tree.FindTreePos(3)).Type())
		assert.Equal(t, "text", tree.FindPostorderRight(tree.FindTreePos(4)).Type())
		assert.Equal(t, "text", tree.FindPostorderRight(tree.FindTreePos(5)).Type())
		assert.Equal(t, "p", tree.FindPostorderRight(tree.FindTreePos(7)).Type())
		assert.Equal(t, "root", tree.FindPostorderRight(tree.FindTreePos(8)).Type())
	})

	t.Run("find common ancestor of two given nodes test", func(t *testing.T) {
		tree := helper.BuildIndexTree(&crdt.JSONTreeNode{
			Type: "root",
			Children: []crdt.JSONTreeNode{{
				Type: "p",
				Children: []crdt.JSONTreeNode{
					{Type: "b", Children: []crdt.JSONTreeNode{{Type: "text", Value: "ab"}}},
					{Type: "b", Children: []crdt.JSONTreeNode{{Type: "text", Value: "cd"}}},
				}},
			},
		})

		nodeAB := tree.FindTreePos(3, true).Node
		nodeCD := tree.FindTreePos(7, true).Node

		assert.Equal(t, "text.ab", helper.ToDiagnostic(nodeAB.Value))
		assert.Equal(t, "text.cd", helper.ToDiagnostic(nodeCD.Value))
		assert.Equal(t, "p", tree.FindCommonAncestor(nodeAB, nodeCD).Type())
	})

	t.Run("traverse nodes between two given positions test", func(t *testing.T) {
		//       0   1 2 3    4   5 6 7 8    9   10 11 12   13
		// <root> <p> a b </p> <p> c d e </p> <p>  f  g  </p>  </root>
		tree := helper.BuildIndexTree(&crdt.JSONTreeNode{
			Type: "root",
			Children: []crdt.JSONTreeNode{{
				Type: "p",
				Children: []crdt.JSONTreeNode{
					{Type: "text", Children: []crdt.JSONTreeNode{}, Value: "a"},
					{Type: "text", Children: []crdt.JSONTreeNode{}, Value: "b"},
				}},
				{Type: "p", Children: []crdt.JSONTreeNode{{Type: "text", Value: "cde"}}},
				{Type: "p", Children: []crdt.JSONTreeNode{{Type: "text", Value: "fg"}}},
			},
		})

		helper.NodesBetweenEqual(t, tree, 2, 11, []string{"text.b", "p", "text.cde", "p", "text.fg", "p"})
		helper.NodesBetweenEqual(t, tree, 2, 6, []string{"text.b", "p", "text.cde", "p"})
		helper.NodesBetweenEqual(t, tree, 0, 1, []string{"p"})
		helper.NodesBetweenEqual(t, tree, 3, 4, []string{"p"})
		helper.NodesBetweenEqual(t, tree, 3, 5, []string{"p", "p"})
	})

	t.Run("find index of the given node test", func(t *testing.T) {
		//       0   1 2 3    4   5 6 7 8    9   10 11 12   13
		// <root> <p> a b </p> <p> c d e </p> <p>  f  g  </p>  </root>
		tree := helper.BuildIndexTree(&crdt.JSONTreeNode{
			Type: "root",
			Children: []crdt.JSONTreeNode{{
				Type: "p",
				Children: []crdt.JSONTreeNode{
					{Type: "text", Children: []crdt.JSONTreeNode{}, Value: "a"},
					{Type: "text", Children: []crdt.JSONTreeNode{}, Value: "b"},
				}},
				{Type: "p", Children: []crdt.JSONTreeNode{{Type: "text", Value: "cde"}}},
				{Type: "p", Children: []crdt.JSONTreeNode{{Type: "text", Value: "fg"}}},
			},
		})

		pos := tree.FindTreePos(0, true)
		assert.Equal(t, "root", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 0, pos.Offset)
		assert.Equal(t, 0, tree.IndexOf(pos.Node))

		pos = tree.FindTreePos(1, true)
		assert.Equal(t, "text.a", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 0, pos.Offset)
		assert.Equal(t, 1, tree.IndexOf(pos.Node))

		pos = tree.FindTreePos(3, true)
		assert.Equal(t, "text.b", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 1, pos.Offset)
		assert.Equal(t, 2, tree.IndexOf(pos.Node))

		pos = tree.FindTreePos(4, true)
		assert.Equal(t, "root", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 1, pos.Offset)
		assert.Equal(t, 0, tree.IndexOf(pos.Node))

		pos = tree.FindTreePos(10, true)
		assert.Equal(t, "text.fg", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 0, pos.Offset)
		assert.Equal(t, 10, tree.IndexOf(pos.Node))

		firstP := tree.Root().Children()[0]
		assert.Equal(t, "p", helper.ToDiagnostic(firstP.Value))
		assert.Equal(t, 0, tree.IndexOf(firstP))

		secondP := tree.Root().Children()[1]
		assert.Equal(t, "p", helper.ToDiagnostic(secondP.Value))
		assert.Equal(t, 4, tree.IndexOf(secondP))

		thirdP := tree.Root().Children()[2]
		assert.Equal(t, "p", helper.ToDiagnostic(thirdP.Value))
		assert.Equal(t, 9, tree.IndexOf(thirdP))
	})

	t.Run("find treePos from given path test", func(t *testing.T) {
		//       0   1 2 3    4   5 6 7 8    9   10 11 12   13
		// <root> <p> a b </p> <p> c d e </p> <p>  f  g  </p>  </root>
		tree := helper.BuildIndexTree(&crdt.JSONTreeNode{
			Type: "root",
			Children: []crdt.JSONTreeNode{{
				Type: "p",
				Children: []crdt.JSONTreeNode{
					{Type: "text", Children: []crdt.JSONTreeNode{}, Value: "a"},
					{Type: "text", Children: []crdt.JSONTreeNode{}, Value: "b"},
				}},
				{Type: "p", Children: []crdt.JSONTreeNode{{Type: "text", Value: "cde"}}},
				{Type: "p", Children: []crdt.JSONTreeNode{{Type: "text", Value: "fg"}}},
			},
		})

		pos := tree.PathToTreePos([]int{0})
		assert.Equal(t, "root", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 0, pos.Offset)

		pos = tree.PathToTreePos([]int{0, 0})
		assert.Equal(t, "p", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 0, pos.Offset)

		pos = tree.PathToTreePos([]int{0, 0, 0})
		assert.Equal(t, "text.a", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 0, pos.Offset)

		pos = tree.PathToTreePos([]int{0, 0, 1})
		assert.Equal(t, "text.a", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 1, pos.Offset)

		pos = tree.PathToTreePos([]int{0, 1, 0})
		assert.Equal(t, "text.b", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 0, pos.Offset)

		pos = tree.PathToTreePos([]int{0, 1, 1})
		assert.Equal(t, "text.b", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 1, pos.Offset)

		pos = tree.PathToTreePos([]int{1})
		assert.Equal(t, "p", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 0, pos.Offset)

		pos = tree.PathToTreePos([]int{1, 0})
		assert.Equal(t, "p", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 0, pos.Offset)

		pos = tree.PathToTreePos([]int{1, 0, 0})
		assert.Equal(t, "text.cde", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 0, pos.Offset)

		pos = tree.PathToTreePos([]int{1, 0, 1})
		assert.Equal(t, "text.cde", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 1, pos.Offset)

		pos = tree.PathToTreePos([]int{1, 0, 2})
		assert.Equal(t, "text.cde", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 2, pos.Offset)

		pos = tree.PathToTreePos([]int{1, 0, 3})
		assert.Equal(t, "text.cde", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 3, pos.Offset)

		pos = tree.PathToTreePos([]int{2})
		assert.Equal(t, "p", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 1, pos.Offset)

		pos = tree.PathToTreePos([]int{2, 0, 0})
		assert.Equal(t, "text.fg", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 0, pos.Offset)

		pos = tree.PathToTreePos([]int{2, 0, 1})
		assert.Equal(t, "text.fg", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 1, pos.Offset)

		pos = tree.PathToTreePos([]int{2, 0, 2})
		assert.Equal(t, "text.fg", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 2, pos.Offset)

		pos = tree.PathToTreePos([]int{3})
		assert.Equal(t, "p", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 2, pos.Offset)
	})

	t.Run("find path from given treePos test", func(t *testing.T) {
		//       0   1 2 3    4   5 6 7 8    9   10 11 12   13
		// <root> <p> a b </p> <p> c d e </p> <p>  f  g  </p>  </root>
		tree := helper.BuildIndexTree(&crdt.JSONTreeNode{
			Type: "root",
			Children: []crdt.JSONTreeNode{{
				Type: "p",
				Children: []crdt.JSONTreeNode{
					{Type: "text", Children: []crdt.JSONTreeNode{}, Value: "a"},
					{Type: "text", Children: []crdt.JSONTreeNode{}, Value: "b"},
				}},
				{Type: "p", Children: []crdt.JSONTreeNode{{Type: "text", Value: "cde"}}},
				{Type: "p", Children: []crdt.JSONTreeNode{{Type: "text", Value: "fg"}}},
			},
		})

		pos := tree.FindTreePos(0)
		assert.Equal(t, []int{0}, tree.TreePosToPath(pos))

		pos = tree.FindTreePos(1)
		assert.Equal(t, []int{0, 0, 0}, tree.TreePosToPath(pos))

		pos = tree.FindTreePos(2)
		assert.Equal(t, []int{0, 0, 1}, tree.TreePosToPath(pos))

		pos = tree.FindTreePos(3)
		assert.Equal(t, []int{0, 1, 1}, tree.TreePosToPath(pos))

		pos = tree.FindTreePos(4)
		assert.Equal(t, []int{1}, tree.TreePosToPath(pos))

		pos = tree.FindTreePos(5)
		assert.Equal(t, []int{1, 0, 0}, tree.TreePosToPath(pos))

		pos = tree.FindTreePos(6)
		assert.Equal(t, []int{1, 0, 1}, tree.TreePosToPath(pos))

		pos = tree.FindTreePos(7)
		assert.Equal(t, []int{1, 0, 2}, tree.TreePosToPath(pos))

		pos = tree.FindTreePos(8)
		assert.Equal(t, []int{1, 0, 3}, tree.TreePosToPath(pos))

		pos = tree.FindTreePos(9)
		assert.Equal(t, []int{2}, tree.TreePosToPath(pos))

		pos = tree.FindTreePos(10)
		assert.Equal(t, []int{2, 0, 0}, tree.TreePosToPath(pos))

		pos = tree.FindTreePos(11)
		assert.Equal(t, []int{2, 0, 1}, tree.TreePosToPath(pos))

		pos = tree.FindTreePos(12)
		assert.Equal(t, []int{2, 0, 2}, tree.TreePosToPath(pos))

		pos = tree.FindTreePos(13)
		assert.Equal(t, []int{3}, tree.TreePosToPath(pos))
	})
}

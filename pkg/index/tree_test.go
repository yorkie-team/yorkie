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

	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestIndexTree is a test for IndexTree.
func TestIndexTree(t *testing.T) {
	t.Run("find position from the given offset", func(t *testing.T) {
		//    0   1 2 3 4 5 6    7   8 9  10 11 12 13    14
		// <r> <p> h e l l o </p> <p> w  o  r  l  d  </p>  </r>
		tree := helper.BuildIndexTree(&json.TreeNode{
			Type: "r",
			Children: []json.TreeNode{
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "hello"}}},
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "world"}}},
			},
		})

		pos, err := tree.FindTreePos(0)
		assert.Equal(t, "r", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 0, pos.Offset)
		assert.NoError(t, err)
		pos, err = tree.FindTreePos(1)
		assert.Equal(t, "text.hello", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 0, pos.Offset)
		assert.NoError(t, err)
		pos, err = tree.FindTreePos(6)
		assert.Equal(t, "text.hello", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 5, pos.Offset)
		assert.NoError(t, err)
		pos, err = tree.FindTreePos(6, false)
		assert.Equal(t, "p", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 1, pos.Offset)
		assert.NoError(t, err)
		pos, err = tree.FindTreePos(7)
		assert.Equal(t, "r", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 1, pos.Offset)
		assert.NoError(t, err)
		pos, err = tree.FindTreePos(8)
		assert.Equal(t, "text.world", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 0, pos.Offset)
		assert.NoError(t, err)
		pos, err = tree.FindTreePos(13)
		assert.Equal(t, "text.world", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 5, pos.Offset)
		assert.NoError(t, err)
		pos, err = tree.FindTreePos(14)
		assert.Equal(t, "r", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 2, pos.Offset)
		assert.NoError(t, err)
	})

	t.Run("find right node from the given offset in postorder traversal test", func(t *testing.T) {
		//       0   1 2 3    4   6 7     8
		// <root> <p> a b </p> <p> c d</p> </root>
		tree := helper.BuildIndexTree(&json.TreeNode{
			Type: "root",
			Children: []json.TreeNode{
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}}},
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "cd"}}},
			},
		})

		// postorder traversal: "ab", <p>, "cd", <p>, <root>

		treePos, posErr := tree.FindTreePos(0)
		assert.NoError(t, posErr)
		right, rightErr := tree.FindPostorderRight(treePos)
		assert.NoError(t, rightErr)
		assert.Equal(t, "text", right.Type())

		treePos, posErr = tree.FindTreePos(1)
		assert.NoError(t, posErr)
		right, rightErr = tree.FindPostorderRight(treePos)
		assert.NoError(t, rightErr)
		assert.Equal(t, "text", right.Type())

		treePos, posErr = tree.FindTreePos(3)
		assert.NoError(t, posErr)
		right, rightErr = tree.FindPostorderRight(treePos)
		assert.NoError(t, rightErr)
		assert.Equal(t, "p", right.Type())

		treePos, posErr = tree.FindTreePos(4)
		assert.NoError(t, posErr)
		right, rightErr = tree.FindPostorderRight(treePos)
		assert.NoError(t, rightErr)
		assert.Equal(t, "text", right.Type())

		treePos, posErr = tree.FindTreePos(5)
		assert.NoError(t, posErr)
		right, rightErr = tree.FindPostorderRight(treePos)
		assert.NoError(t, rightErr)
		assert.Equal(t, "text", right.Type())

		treePos, posErr = tree.FindTreePos(7)
		assert.NoError(t, posErr)
		right, rightErr = tree.FindPostorderRight(treePos)
		assert.NoError(t, rightErr)
		assert.Equal(t, "p", right.Type())

		treePos, posErr = tree.FindTreePos(8)
		assert.NoError(t, posErr)
		right, rightErr = tree.FindPostorderRight(treePos)
		assert.NoError(t, rightErr)
		assert.Equal(t, "root", right.Type())
	})

	t.Run("find common ancestor of two given nodes test", func(t *testing.T) {
		tree := helper.BuildIndexTree(&json.TreeNode{
			Type: "root",
			Children: []json.TreeNode{{
				Type: "p",
				Children: []json.TreeNode{
					{Type: "b", Children: []json.TreeNode{{Type: "text", Value: "ab"}}},
					{Type: "b", Children: []json.TreeNode{{Type: "text", Value: "cd"}}},
				}},
			},
		})

		treePosAB, _ := tree.FindTreePos(3, true)
		nodeAB := treePosAB.Node

		treePosCD, _ := tree.FindTreePos(7, true)
		nodeCD := treePosCD.Node

		assert.Equal(t, "text.ab", helper.ToDiagnostic(nodeAB.Value))
		assert.Equal(t, "text.cd", helper.ToDiagnostic(nodeCD.Value))
		assert.Equal(t, "p", tree.FindCommonAncestor(nodeAB, nodeCD).Type())
	})

	t.Run("traverse nodes between two given positions test", func(t *testing.T) {
		//       0   1 2 3    4   5 6 7 8    9   10 11 12   13
		// <root> <p> a b </p> <p> c d e </p> <p>  f  g  </p>  </root>
		tree := helper.BuildIndexTree(&json.TreeNode{
			Type: "root",
			Children: []json.TreeNode{{
				Type: "p",
				Children: []json.TreeNode{
					{Type: "text", Children: []json.TreeNode{}, Value: "a"},
					{Type: "text", Children: []json.TreeNode{}, Value: "b"},
				}},
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "cde"}}},
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "fg"}}},
			},
		})
		//        0   1 2 3    4   5 6 7 8    9   10 11 12    13
		// `<root> <p> a b </p> <p> c d e </p> <p>  f  g  </p> </root>`

		helper.NodesBetweenEqual(t, tree, 2, 11, []string{"text.b:All", "p:Closing",
			"text.cde:All", "p:All", "text.fg:All", "p:Opening"})
		helper.NodesBetweenEqual(t, tree, 2, 6, []string{"text.b:All", "p:Closing", "text.cde:All", "p:Opening"})
		helper.NodesBetweenEqual(t, tree, 0, 1, []string{"p:Opening"})
		helper.NodesBetweenEqual(t, tree, 3, 4, []string{"p:Closing"})
		helper.NodesBetweenEqual(t, tree, 3, 5, []string{"p:Closing", "p:Opening"})
	})

	t.Run("find index of the given node test", func(t *testing.T) {
		//       0   1 2 3    4   5 6 7 8    9   10 11 12   13
		// <root> <p> a b </p> <p> c d e </p> <p>  f  g  </p>  </root>
		tree := helper.BuildIndexTree(&json.TreeNode{
			Type: "root",
			Children: []json.TreeNode{{
				Type: "p",
				Children: []json.TreeNode{
					{Type: "text", Children: []json.TreeNode{}, Value: "a"},
					{Type: "text", Children: []json.TreeNode{}, Value: "b"},
				}},
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "cde"}}},
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "fg"}}},
			},
		})

		pos, posErr := tree.FindTreePos(0, true)
		assert.NoError(t, posErr)
		assert.Equal(t, "root", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 0, pos.Offset)
		index, indexErr := tree.IndexOf(pos)
		assert.NoError(t, indexErr)
		assert.Equal(t, 0, index)

		pos, posErr = tree.FindTreePos(1, true)
		assert.NoError(t, posErr)
		assert.Equal(t, "text.a", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 0, pos.Offset)
		index, indexErr = tree.IndexOf(pos)
		assert.NoError(t, indexErr)
		assert.Equal(t, 1, index)

		pos, posErr = tree.FindTreePos(3, true)
		assert.NoError(t, posErr)
		assert.Equal(t, "text.b", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 1, pos.Offset)
		index, indexErr = tree.IndexOf(pos)
		assert.NoError(t, indexErr)
		assert.Equal(t, 3, index)

		pos, posErr = tree.FindTreePos(4, true)
		assert.NoError(t, posErr)
		assert.Equal(t, "root", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 1, pos.Offset)
		index, indexErr = tree.IndexOf(pos)
		assert.NoError(t, indexErr)
		assert.Equal(t, 4, index)

		pos, posErr = tree.FindTreePos(10, true)
		assert.NoError(t, posErr)
		assert.Equal(t, "text.fg", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 0, pos.Offset)
		index, indexErr = tree.IndexOf(pos)
		assert.NoError(t, indexErr)
		assert.Equal(t, 10, index)
	})

	t.Run("find treePos from given path test", func(t *testing.T) {
		//       0   1 2 3    4   5 6 7 8    9   10 11 12   13
		// <root> <p> a b </p> <p> c d e </p> <p>  f  g  </p>  </root>
		tree := helper.BuildIndexTree(&json.TreeNode{
			Type: "root",
			Children: []json.TreeNode{{
				Type: "rc",
				Children: []json.TreeNode{
					{Type: "text", Children: []json.TreeNode{}, Value: "a"},
					{Type: "text", Children: []json.TreeNode{}, Value: "b"},
				}},
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "cde"}}},
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "fg"}}},
			},
		})

		pos, err := tree.PathToTreePos([]int{0})
		assert.NoError(t, err)
		assert.Equal(t, "root", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 0, pos.Offset)

		pos, err = tree.PathToTreePos([]int{0, 0})
		assert.NoError(t, err)
		assert.Equal(t, "text.a", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 0, pos.Offset)

		pos, err = tree.PathToTreePos([]int{0, 1})
		assert.NoError(t, err)
		assert.Equal(t, "text.a", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 1, pos.Offset)

		pos, err = tree.PathToTreePos([]int{0, 2})
		assert.NoError(t, err)
		assert.Equal(t, "text.b", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 1, pos.Offset)

		pos, err = tree.PathToTreePos([]int{1})
		assert.NoError(t, err)
		assert.Equal(t, "root", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 1, pos.Offset)

		pos, err = tree.PathToTreePos([]int{1, 0})
		assert.NoError(t, err)
		assert.Equal(t, "text.cde", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 0, pos.Offset)

		pos, err = tree.PathToTreePos([]int{1, 1})
		assert.NoError(t, err)
		assert.Equal(t, "text.cde", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 1, pos.Offset)

		pos, err = tree.PathToTreePos([]int{1, 2})
		assert.NoError(t, err)
		assert.Equal(t, "text.cde", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 2, pos.Offset)

		pos, err = tree.PathToTreePos([]int{1, 3})
		assert.NoError(t, err)
		assert.Equal(t, "text.cde", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 3, pos.Offset)

		pos, err = tree.PathToTreePos([]int{2})
		assert.NoError(t, err)
		assert.Equal(t, "root", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 2, pos.Offset)

		pos, err = tree.PathToTreePos([]int{2, 0})
		assert.NoError(t, err)
		assert.Equal(t, "text.fg", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 0, pos.Offset)

		pos, err = tree.PathToTreePos([]int{2, 1})
		assert.NoError(t, err)
		assert.Equal(t, "text.fg", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 1, pos.Offset)

		pos, err = tree.PathToTreePos([]int{2, 2})
		assert.NoError(t, err)
		assert.Equal(t, "text.fg", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 2, pos.Offset)

		pos, err = tree.PathToTreePos([]int{3})
		assert.NoError(t, err)
		assert.Equal(t, "root", helper.ToDiagnostic(pos.Node.Value))
		assert.Equal(t, 3, pos.Offset)
	})

	t.Run("find path from given treePos test", func(t *testing.T) {
		//       0  1  2    3 4 5 6 7     8   9 10 11 12 13  14 15  16
		// <root><tc><p><tn> A B C D </tn><tn> E  F G  H </tn><p></tc></root>
		tree := helper.BuildIndexTree(&json.TreeNode{
			Type: "root",
			Children: []json.TreeNode{{
				Type: "tc",
				Children: []json.TreeNode{
					{
						Type: "p", Children: []json.TreeNode{
							{Type: "tn", Children: []json.TreeNode{{Type: "text", Value: "ABCD"}}},
							{Type: "tn", Children: []json.TreeNode{{Type: "text", Value: "EFGH"}}},
						},
					},
				}},
			}},
		)

		//       0  1  2    3 4 5 6 7     8   9 10 11 12 13  14 15  16
		// <root><tc><p><tn> A B C D </tn><tn> E  F G  H </tn><p></tc></root>
		pos, posErr := tree.FindTreePos(0)
		assert.NoError(t, posErr)
		path, pathErr := tree.TreePosToPath(pos)
		assert.NoError(t, pathErr)
		assert.Equal(t, []int{0}, path)

		pos, posErr = tree.FindTreePos(1)
		assert.NoError(t, posErr)
		path, pathErr = tree.TreePosToPath(pos)
		assert.NoError(t, pathErr)
		assert.Equal(t, []int{0, 0}, path)

		pos, posErr = tree.FindTreePos(2)
		assert.NoError(t, posErr)
		path, pathErr = tree.TreePosToPath(pos)
		assert.NoError(t, pathErr)
		assert.Equal(t, []int{0, 0, 0}, path)

		pos, posErr = tree.FindTreePos(3)
		assert.NoError(t, posErr)
		path, pathErr = tree.TreePosToPath(pos)
		assert.NoError(t, pathErr)
		assert.Equal(t, []int{0, 0, 0, 0}, path)

		pos, posErr = tree.FindTreePos(4)
		assert.NoError(t, posErr)
		path, pathErr = tree.TreePosToPath(pos)
		assert.NoError(t, pathErr)
		assert.Equal(t, []int{0, 0, 0, 1}, path)

		pos, posErr = tree.FindTreePos(5)
		assert.NoError(t, posErr)
		path, pathErr = tree.TreePosToPath(pos)
		assert.NoError(t, pathErr)
		assert.Equal(t, []int{0, 0, 0, 2}, path)

		pos, posErr = tree.FindTreePos(6)
		assert.NoError(t, posErr)
		path, pathErr = tree.TreePosToPath(pos)
		assert.NoError(t, pathErr)
		assert.Equal(t, []int{0, 0, 0, 3}, path)

		pos, posErr = tree.FindTreePos(7)
		assert.NoError(t, posErr)
		path, pathErr = tree.TreePosToPath(pos)
		assert.NoError(t, pathErr)
		assert.Equal(t, []int{0, 0, 0, 4}, path)

		pos, posErr = tree.FindTreePos(8)
		assert.NoError(t, posErr)
		path, pathErr = tree.TreePosToPath(pos)
		assert.NoError(t, pathErr)
		assert.Equal(t, []int{0, 0, 1}, path)

		pos, posErr = tree.FindTreePos(9)
		assert.NoError(t, posErr)
		path, pathErr = tree.TreePosToPath(pos)
		assert.NoError(t, pathErr)
		assert.Equal(t, []int{0, 0, 1, 0}, path)

		pos, posErr = tree.FindTreePos(10)
		assert.NoError(t, posErr)
		path, pathErr = tree.TreePosToPath(pos)
		assert.NoError(t, pathErr)
		assert.Equal(t, []int{0, 0, 1, 1}, path)

		pos, posErr = tree.FindTreePos(11)
		assert.NoError(t, posErr)
		path, pathErr = tree.TreePosToPath(pos)
		assert.NoError(t, pathErr)
		assert.Equal(t, []int{0, 0, 1, 2}, path)

		pos, posErr = tree.FindTreePos(12)
		assert.NoError(t, posErr)
		path, pathErr = tree.TreePosToPath(pos)
		assert.NoError(t, pathErr)
		assert.Equal(t, []int{0, 0, 1, 3}, path)

		pos, posErr = tree.FindTreePos(13)
		assert.NoError(t, posErr)
		path, pathErr = tree.TreePosToPath(pos)
		assert.NoError(t, pathErr)
		assert.Equal(t, []int{0, 0, 1, 4}, path)

		pos, posErr = tree.FindTreePos(14)
		assert.NoError(t, posErr)
		path, pathErr = tree.TreePosToPath(pos)
		assert.NoError(t, pathErr)
		assert.Equal(t, []int{0, 0, 2}, path)

		pos, posErr = tree.FindTreePos(15)
		assert.NoError(t, posErr)
		path, pathErr = tree.TreePosToPath(pos)
		assert.NoError(t, pathErr)
		assert.Equal(t, []int{0, 1}, path)

		pos, posErr = tree.FindTreePos(16)
		assert.NoError(t, posErr)
		path, pathErr = tree.TreePosToPath(pos)
		assert.NoError(t, pathErr)
		assert.Equal(t, []int{1}, path)
	})
}

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

package crdt_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestTreeNode(t *testing.T) {
	t.Run("inline node test", func(t *testing.T) {
		node := crdt.NewTreeNode(crdt.DummyTreePos, "text", "hello")
		assert.Equal(t, crdt.DummyTreePos, node.Pos)
		assert.Equal(t, "text", node.Type())
		assert.Equal(t, "hello", node.Value)
		assert.Equal(t, 5, node.Len())
		assert.Equal(t, true, node.IsInline())
		assert.Equal(t, false, node.IsRemoved())
	})

	t.Run("block node test", func(t *testing.T) {
		para := crdt.NewTreeNode(crdt.DummyTreePos, "p")
		para.Append(crdt.NewTreeNode(crdt.DummyTreePos, "text", "helloyorkie"))
		assert.Equal(t, "<p>helloyorkie</p>", crdt.ToXML(para))
		assert.Equal(t, 11, para.Len())
		assert.Equal(t, false, para.IsInline())

		left := para.Child(0)
		right := left.Split(5)
		assert.Equal(t, "<p>helloyorkie</p>", crdt.ToXML(para))
		assert.Equal(t, 11, para.Len())

		assert.Equal(t, "hello", left.Value)
		assert.Equal(t, "yorkie", right.Value)
		assert.Equal(t, &crdt.TreePos{CreatedAt: time.InitialTicket, Offset: 0}, left.Pos)
		assert.Equal(t, &crdt.TreePos{CreatedAt: time.InitialTicket, Offset: 5}, right.Pos)
	})
}

func TestTree(t *testing.T) {
	t.Run("insert nodes with Edit test", func(t *testing.T) {
		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)
		//       0
		// <root> </root>
		tree := crdt.NewTree(crdt.NewTreeNode(helper.IssuePos(ctx), "r"), helper.IssueTime(ctx))
		assert.Equal(t, 0, tree.Root().Len())
		assert.Equal(t, "<r></r>", tree.ToXML())
		helper.ListEqual(t, tree, []string{"r"})

		//           1
		// <root> <p> </p> </root>
		tree.EditByIndex(0, 0, crdt.NewTreeNode(helper.IssuePos(ctx), "p"), helper.IssueTime(ctx))
		assert.Equal(t, "<r><p></p></r>", tree.ToXML())
		helper.ListEqual(t, tree, []string{"p", "r"})
		assert.Equal(t, 2, tree.Root().Len())

		//           1
		// <root> <p> h e l l o </p> </root>
		tree.EditByIndex(
			1, 1,
			crdt.NewTreeNode(helper.IssuePos(ctx), "text", "hello"),
			helper.IssueTime(ctx),
		)
		assert.Equal(t, "<r><p>hello</p></r>", tree.ToXML())
		helper.ListEqual(t, tree, []string{"text.hello", "p", "r"})
		assert.Equal(t, 7, tree.Root().Len())

		//       0   1 2 3 4 5 6    7   8 9  10 11 12 13    14
		// <root> <p> h e l l o </p> <p> w  o  r  l  d  </p>  </root>
		p := crdt.NewTreeNode(helper.IssuePos(ctx), "p")
		p.InsertAt(crdt.NewTreeNode(helper.IssuePos(ctx), "text", "world"), 0)
		tree.EditByIndex(7, 7, p, helper.IssueTime(ctx))
		assert.Equal(t, "<r><p>hello</p><p>world</p></r>", tree.ToXML())
		helper.ListEqual(t, tree, []string{"text.hello", "p", "text.world", "p", "r"})
		assert.Equal(t, 14, tree.Root().Len())

		//       0   1 2 3 4 5 6 7    8   9 10 11 12 13 14    15
		// <root> <p> h e l l o ! </p> <p> w  o  r  l  d  </p>  </root>
		tree.EditByIndex(
			6, 6,
			crdt.NewTreeNode(helper.IssuePos(ctx), "text", "!"),
			helper.IssueTime(ctx),
		)
		assert.Equal(t, "<r><p>hello!</p><p>world</p></r>", tree.ToXML())
		helper.ListEqual(t, tree, []string{"text.hello", "text.!", "p", "text.world", "p", "r"})
		assert.Equal(t, crdt.TreeNodeForTest{
			Type: "r",
			Children: []crdt.TreeNodeForTest{
				{
					Type: "p",
					Children: []crdt.TreeNodeForTest{
						{Type: "text", Value: "hello", Size: 5, IsRemoved: false},
						{Type: "text", Value: "!", Size: 1, IsRemoved: false},
					},
					Size:      6,
					IsRemoved: false,
				},
				{
					Type: "p",
					Children: []crdt.TreeNodeForTest{
						{Type: "text", Value: "world", Size: 5, IsRemoved: false},
					},
					Size:      5,
					IsRemoved: false,
				},
			},
			Size:      15,
			IsRemoved: false,
		}, tree.Structure())

		//       0   1 2 3 4 5 6 7 8    9   10 11 12 13 14 15    16
		// <root> <p> h e l l o ~ ! </p> <p>  w  o  r  l  d  </p>  </root>
		tree.EditByIndex(
			6, 6,
			crdt.NewTreeNode(helper.IssuePos(ctx), "text", "~"),
			helper.IssueTime(ctx),
		)
		assert.Equal(t, "<r><p>hello~!</p><p>world</p></r>", tree.ToXML())
		helper.ListEqual(t, tree, []string{"text.hello", "text.~", "text.!", "p", "text.world", "p", "r"})
	})

	t.Run("delete inline nodes with Edit test", func(t *testing.T) {
		// 01. Create a tree with 2 paragraphs.
		//       0   1 2 3    4   5 6 7    8
		// <root> <p> a b </p> <p> c d </p> </root>

		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := crdt.NewTree(crdt.NewTreeNode(helper.IssuePos(ctx), "root"), helper.IssueTime(ctx))
		tree.EditByIndex(0, 0, crdt.NewTreeNode(helper.IssuePos(ctx), "p"), helper.IssueTime(ctx))
		tree.EditByIndex(1, 1, crdt.NewTreeNode(helper.IssuePos(ctx), "text", "ab"), helper.IssueTime(ctx))
		tree.EditByIndex(4, 4, crdt.NewTreeNode(helper.IssuePos(ctx), "p"), helper.IssueTime(ctx))
		tree.EditByIndex(5, 5, crdt.NewTreeNode(helper.IssuePos(ctx), "text", "cd"), helper.IssueTime(ctx))
		assert.Equal(t, "<root><p>ab</p><p>cd</p></root>", tree.ToXML())
		helper.ListEqual(t, tree, []string{"text.ab", "p", "text.cd", "p", "root"})

		structure := tree.Structure()
		assert.Equal(t, 8, structure.Size)
		assert.Equal(t, 2, structure.Children[0].Size)
		assert.Equal(t, 2, structure.Children[0].Children[0].Size)

		// 02. Delete b from the first paragraph.
		// 	     0   1 2    3   4 5 6    7
		// <root> <p> a </p> <p> c d </p> </root>
		tree.EditByIndex(2, 3, nil, helper.IssueTime(ctx))
		assert.Equal(t, "<root><p>a</p><p>cd</p></root>", tree.ToXML())
		helper.ListEqual(t, tree, []string{"text.a", "p", "text.cd", "p", "root"})

		structure = tree.Structure()
		assert.Equal(t, 7, structure.Size)
		assert.Equal(t, 1, structure.Children[0].Size)
		assert.Equal(t, 1, structure.Children[0].Children[0].Size)
	})

	t.Run("delete nodes between block nodes test", func(t *testing.T) {
		// 01. Create a tree with 2 paragraphs.
		//       0   1 2 3    4   5 6 7    8
		// <root> <p> a b </p> <p> c d </p> </root>

		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := crdt.NewTree(crdt.NewTreeNode(helper.IssuePos(ctx), "root"), helper.IssueTime(ctx))
		tree.EditByIndex(0, 0, crdt.NewTreeNode(helper.IssuePos(ctx), "p"), helper.IssueTime(ctx))
		tree.EditByIndex(1, 1, crdt.NewTreeNode(helper.IssuePos(ctx), "text", "ab"), helper.IssueTime(ctx))
		tree.EditByIndex(4, 4, crdt.NewTreeNode(helper.IssuePos(ctx), "p"), helper.IssueTime(ctx))
		tree.EditByIndex(5, 5, crdt.NewTreeNode(helper.IssuePos(ctx), "text", "cd"), helper.IssueTime(ctx))
		assert.Equal(t, "<root><p>ab</p><p>cd</p></root>", tree.ToXML())
		helper.ListEqual(t, tree, []string{"text.ab", "p", "text.cd", "p", "root"})

		// 02. delete b, c and first paragraph.
		//       0   1 2 3    4
		// <root> <p> a d </p> </root>
		tree.EditByIndex(2, 6, nil, helper.IssueTime(ctx))
		assert.Equal(t, "<root><p>ad</p></root>", tree.ToXML())
		helper.ListEqual(t, tree, []string{"text.a", "text.d", "p", "root"})

		structure := tree.Structure()
		assert.Equal(t, 4, structure.Size)
		assert.Equal(t, 2, structure.Children[0].Size)
		assert.Equal(t, 1, structure.Children[0].Children[0].Size)
		assert.Equal(t, 1, structure.Children[0].Children[1].Size)

		// 03. insert a new text node at the start of the first paragraph.
		tree.EditByIndex(1, 1, crdt.NewTreeNode(helper.IssuePos(ctx), "text", "@"), helper.IssueTime(ctx))
		assert.Equal(t, "<root><p>@ad</p></root>", tree.ToXML())
	})

	t.Run("merge different levels with Edit", func(t *testing.T) {
		// 01. Edit between two block nodes in the same hierarchy.
		//       0   1   2   3 4 5    6    7    8
		// <root> <p> <b> <i> a b </i> </b> </p> </root>
		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := crdt.NewTree(crdt.NewTreeNode(helper.IssuePos(ctx), "root"), helper.IssueTime(ctx))
		tree.EditByIndex(0, 0, crdt.NewTreeNode(helper.IssuePos(ctx), "p"), helper.IssueTime(ctx))
		tree.EditByIndex(1, 1, crdt.NewTreeNode(helper.IssuePos(ctx), "b"), helper.IssueTime(ctx))
		tree.EditByIndex(2, 2, crdt.NewTreeNode(helper.IssuePos(ctx), "i"), helper.IssueTime(ctx))
		tree.EditByIndex(3, 3, crdt.NewTreeNode(helper.IssuePos(ctx), "text", "ab"), helper.IssueTime(ctx))
		assert.Equal(t, "<root><p><b><i>ab</i></b></p></root>", tree.ToXML())
		tree.EditByIndex(5, 6, nil, helper.IssueTime(ctx))
		assert.Equal(t, "<root><p><b>ab</b></p></root>", tree.ToXML())

		// 02. Edit between two block nodes in same hierarchy.
		tree = crdt.NewTree(crdt.NewTreeNode(helper.IssuePos(ctx), "root"), helper.IssueTime(ctx))
		tree.EditByIndex(0, 0, crdt.NewTreeNode(helper.IssuePos(ctx), "p"), helper.IssueTime(ctx))
		tree.EditByIndex(1, 1, crdt.NewTreeNode(helper.IssuePos(ctx), "b"), helper.IssueTime(ctx))
		tree.EditByIndex(2, 2, crdt.NewTreeNode(helper.IssuePos(ctx), "i"), helper.IssueTime(ctx))
		tree.EditByIndex(3, 3, crdt.NewTreeNode(helper.IssuePos(ctx), "text", "ab"), helper.IssueTime(ctx))
		assert.Equal(t, "<root><p><b><i>ab</i></b></p></root>", tree.ToXML())
		tree.EditByIndex(6, 7, nil, helper.IssueTime(ctx))
		assert.Equal(t, "<root><p><i>ab</i></p></root>", tree.ToXML())

		// 03. Edit between inline and block node in same hierarchy.
		tree = crdt.NewTree(crdt.NewTreeNode(helper.IssuePos(ctx), "root"), helper.IssueTime(ctx))
		tree.EditByIndex(0, 0, crdt.NewTreeNode(helper.IssuePos(ctx), "p"), helper.IssueTime(ctx))
		tree.EditByIndex(1, 1, crdt.NewTreeNode(helper.IssuePos(ctx), "b"), helper.IssueTime(ctx))
		tree.EditByIndex(2, 2, crdt.NewTreeNode(helper.IssuePos(ctx), "i"), helper.IssueTime(ctx))
		tree.EditByIndex(3, 3, crdt.NewTreeNode(helper.IssuePos(ctx), "text", "ab"), helper.IssueTime(ctx))
		assert.Equal(t, "<root><p><b><i>ab</i></b></p></root>", tree.ToXML())
		tree.EditByIndex(4, 6, nil, helper.IssueTime(ctx))
		assert.Equal(t, "<root><p><b>a</b></p></root>", tree.ToXML())

		// 04. Edit between inline and block node in same hierarchy.
		tree = crdt.NewTree(crdt.NewTreeNode(helper.IssuePos(ctx), "root"), helper.IssueTime(ctx))
		tree.EditByIndex(0, 0, crdt.NewTreeNode(helper.IssuePos(ctx), "p"), helper.IssueTime(ctx))
		tree.EditByIndex(1, 1, crdt.NewTreeNode(helper.IssuePos(ctx), "b"), helper.IssueTime(ctx))
		tree.EditByIndex(2, 2, crdt.NewTreeNode(helper.IssuePos(ctx), "i"), helper.IssueTime(ctx))
		tree.EditByIndex(3, 3, crdt.NewTreeNode(helper.IssuePos(ctx), "text", "ab"), helper.IssueTime(ctx))
		assert.Equal(t, "<root><p><b><i>ab</i></b></p></root>", tree.ToXML())
		tree.EditByIndex(5, 7, nil, helper.IssueTime(ctx))
		assert.Equal(t, "<root><p>ab</p></root>", tree.ToXML())

		// 05. Edit between inline and block node in same hierarchy.
		tree = crdt.NewTree(crdt.NewTreeNode(helper.IssuePos(ctx), "root"), helper.IssueTime(ctx))
		tree.EditByIndex(0, 0, crdt.NewTreeNode(helper.IssuePos(ctx), "p"), helper.IssueTime(ctx))
		tree.EditByIndex(1, 1, crdt.NewTreeNode(helper.IssuePos(ctx), "b"), helper.IssueTime(ctx))
		tree.EditByIndex(2, 2, crdt.NewTreeNode(helper.IssuePos(ctx), "i"), helper.IssueTime(ctx))
		tree.EditByIndex(3, 3, crdt.NewTreeNode(helper.IssuePos(ctx), "text", "ab"), helper.IssueTime(ctx))
		assert.Equal(t, "<root><p><b><i>ab</i></b></p></root>", tree.ToXML())
		tree.EditByIndex(4, 7, nil, helper.IssueTime(ctx))
		assert.Equal(t, "<root><p>a</p></root>", tree.ToXML())

		// 06. Edit between inline and block node in same hierarchy.
		tree = crdt.NewTree(crdt.NewTreeNode(helper.IssuePos(ctx), "root"), helper.IssueTime(ctx))
		tree.EditByIndex(0, 0, crdt.NewTreeNode(helper.IssuePos(ctx), "p"), helper.IssueTime(ctx))
		tree.EditByIndex(1, 1, crdt.NewTreeNode(helper.IssuePos(ctx), "b"), helper.IssueTime(ctx))
		tree.EditByIndex(2, 2, crdt.NewTreeNode(helper.IssuePos(ctx), "i"), helper.IssueTime(ctx))
		tree.EditByIndex(3, 3, crdt.NewTreeNode(helper.IssuePos(ctx), "text", "ab"), helper.IssueTime(ctx))
		assert.Equal(t, "<root><p><b><i>ab</i></b></p></root>", tree.ToXML())
		tree.EditByIndex(3, 7, nil, helper.IssueTime(ctx))
		assert.Equal(t, "<root><p></p></root>", tree.ToXML())

		// 07. Edit between inline and block node in same hierarchy.
		tree = crdt.NewTree(crdt.NewTreeNode(helper.IssuePos(ctx), "root"), helper.IssueTime(ctx))
		tree.EditByIndex(0, 0, crdt.NewTreeNode(helper.IssuePos(ctx), "p"), helper.IssueTime(ctx))
		tree.EditByIndex(1, 1, crdt.NewTreeNode(helper.IssuePos(ctx), "text", "ab"), helper.IssueTime(ctx))
		tree.EditByIndex(4, 4, crdt.NewTreeNode(helper.IssuePos(ctx), "p"), helper.IssueTime(ctx))
		tree.EditByIndex(5, 5, crdt.NewTreeNode(helper.IssuePos(ctx), "b"), helper.IssueTime(ctx))
		tree.EditByIndex(6, 6, crdt.NewTreeNode(helper.IssuePos(ctx), "text", "cd"), helper.IssueTime(ctx))
		tree.EditByIndex(10, 10, crdt.NewTreeNode(helper.IssuePos(ctx), "p"), helper.IssueTime(ctx))
		tree.EditByIndex(11, 11, crdt.NewTreeNode(helper.IssuePos(ctx), "text", "ef"), helper.IssueTime(ctx))
		assert.Equal(t, "<root><p>ab</p><p><b>cd</b></p><p>ef</p></root>", tree.ToXML())
		tree.EditByIndex(9, 10, nil, helper.IssueTime(ctx))
		assert.Equal(t, "<root><p>ab</p><b>cd</b><p>ef</p></root>", tree.ToXML())
	})
}

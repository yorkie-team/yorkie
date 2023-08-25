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

var (
	dummyTreeNodeID = &crdt.TreeNodeID{
		CreatedAt: time.InitialTicket,
		Offset:    0,
	}
)

func TestTreeNode(t *testing.T) {
	t.Run("text node test", func(t *testing.T) {
		node := crdt.NewTreeNode(dummyTreeNodeID, "text", nil, "hello")
		assert.Equal(t, dummyTreeNodeID, node.ID)
		assert.Equal(t, "text", node.Type())
		assert.Equal(t, "hello", node.Value)
		assert.Equal(t, 5, node.Len())
		assert.Equal(t, true, node.IsText())
		assert.Equal(t, false, node.IsRemoved())
	})

	t.Run("element node test", func(t *testing.T) {
		para := crdt.NewTreeNode(dummyTreeNodeID, "p", nil)
		err := para.Append(crdt.NewTreeNode(dummyTreeNodeID, "text", nil, "helloyorkie"))
		assert.NoError(t, err)
		assert.Equal(t, "<p>helloyorkie</p>", crdt.ToXML(para))
		assert.Equal(t, 11, para.Len())
		assert.Equal(t, false, para.IsText())

		left, err := para.Child(0)
		assert.NoError(t, err)
		right, err := left.Split(5, 0)
		assert.NoError(t, err)
		assert.Equal(t, "<p>helloyorkie</p>", crdt.ToXML(para))
		assert.Equal(t, 11, para.Len())

		assert.Equal(t, "hello", left.Value)
		assert.Equal(t, "yorkie", right.Value)
		assert.Equal(t, &crdt.TreeNodeID{CreatedAt: time.InitialTicket, Offset: 0}, left.ID)
		assert.Equal(t, &crdt.TreeNodeID{CreatedAt: time.InitialTicket, Offset: 5}, right.ID)
	})

	t.Run("element node with attributes test", func(t *testing.T) {
		attrs := crdt.NewRHT()
		attrs.Set("font-weight", "bold", time.InitialTicket)
		node := crdt.NewTreeNode(dummyTreeNodeID, "span", attrs)
		err := node.Append(crdt.NewTreeNode(dummyTreeNodeID, "text", nil, "helloyorkie"))
		assert.NoError(t, err)
		assert.Equal(t, `<span font-weight="bold">helloyorkie</span>`, crdt.ToXML(node))
	})

	t.Run("UTF-16 code unit test", func(t *testing.T) {
		tests := []struct {
			length int
			value  string
		}{
			{4, "abcd"},
			{6, "우리나라한글"},
			{8, "अनुच्छेद"},
			{10, "Ĺo͂řȩm̅"},
			{12, "🌷🎁💩😜👍🏳"},
		}
		for _, test := range tests {
			para := crdt.NewTreeNode(dummyTreeNodeID, "p", nil)
			err := para.Append(crdt.NewTreeNode(dummyTreeNodeID, "text", nil, test.value))
			assert.NoError(t, err)
			left, err := para.Child(0)
			assert.NoError(t, err)
			assert.Equal(t, test.length, left.Len())
			right, err := left.Split(2, 0)
			assert.NoError(t, err)
			assert.Equal(t, test.length-2, right.Len())
		}
	})
}

func TestTree(t *testing.T) {
	t.Run("insert nodes with Edit test", func(t *testing.T) {
		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)
		//       0
		// <root> </root>
		tree := crdt.NewTree(crdt.NewTreeNode(helper.IssuePos(ctx), "r", nil), helper.IssueTime(ctx))
		assert.Equal(t, 0, tree.Root().Len())
		assert.Equal(t, "<r></r>", tree.ToXML())

		//           1
		// <root> <p> </p> </root>
		_, err := tree.EditByIndex(0, 0, nil,
			[]*crdt.TreeNode{crdt.NewTreeNode(helper.IssuePos(ctx), "p", nil)}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p></p></r>", tree.ToXML())
		assert.Equal(t, 2, tree.Root().Len())

		//           1
		// <root> <p> h e l l o </p> </root>
		_, err = tree.EditByIndex(
			1, 1, nil,
			[]*crdt.TreeNode{crdt.NewTreeNode(helper.IssuePos(ctx), "text", nil, "hello")},
			helper.IssueTime(ctx),
		)
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>hello</p></r>", tree.ToXML())
		assert.Equal(t, 7, tree.Root().Len())

		//       0   1 2 3 4 5 6    7   8 9  10 11 12 13    14
		// <root> <p> h e l l o </p> <p> w  o  r  l  d  </p>  </root>
		p := crdt.NewTreeNode(helper.IssuePos(ctx), "p", nil)
		err = p.InsertAt(crdt.NewTreeNode(helper.IssuePos(ctx), "text", nil, "world"), 0)
		assert.NoError(t, err)
		_, err = tree.EditByIndex(7, 7, nil, []*crdt.TreeNode{p}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>hello</p><p>world</p></r>", tree.ToXML())
		assert.Equal(t, 14, tree.Root().Len())

		//       0   1 2 3 4 5 6 7    8   9 10 11 12 13 14    15
		// <root> <p> h e l l o ! </p> <p> w  o  r  l  d  </p>  </root>
		_, err = tree.EditByIndex(
			6, 6, nil,
			[]*crdt.TreeNode{crdt.NewTreeNode(helper.IssuePos(ctx), "text", nil, "!")},
			helper.IssueTime(ctx),
		)
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>hello!</p><p>world</p></r>", tree.ToXML())
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
		_, err = tree.EditByIndex(
			6, 6, nil,
			[]*crdt.TreeNode{crdt.NewTreeNode(helper.IssuePos(ctx), "text", nil, "~")},
			helper.IssueTime(ctx),
		)
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>hello~!</p><p>world</p></r>", tree.ToXML())
	})

	t.Run("delete text nodes with Edit test", func(t *testing.T) {
		// 01. Create a tree with 2 paragraphs.
		//       0   1 2 3    4   5 6 7    8
		// <root> <p> a b </p> <p> c d </p> </root>

		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := crdt.NewTree(crdt.NewTreeNode(helper.IssuePos(ctx),
			"root", nil), helper.IssueTime(ctx))
		_, err := tree.EditByIndex(0, 0, nil, []*crdt.TreeNode{crdt.NewTreeNode(helper.IssuePos(ctx),
			"p", nil)}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		_, err = tree.EditByIndex(1, 1, nil, []*crdt.TreeNode{crdt.NewTreeNode(helper.IssuePos(ctx),
			"text", nil, "ab")}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		_, err = tree.EditByIndex(4, 4, nil, []*crdt.TreeNode{crdt.NewTreeNode(helper.IssuePos(ctx),
			"p", nil)}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		_, err = tree.EditByIndex(5, 5, nil, []*crdt.TreeNode{crdt.NewTreeNode(helper.IssuePos(ctx),
			"text", nil, "cd")}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p>ab</p><p>cd</p></root>", tree.ToXML())

		structure := tree.Structure()
		assert.Equal(t, 8, structure.Size)
		assert.Equal(t, 2, structure.Children[0].Size)
		assert.Equal(t, 2, structure.Children[0].Children[0].Size)

		// 02. Delete b from the first paragraph.
		// 	     0   1 2    3   4 5 6    7
		// <root> <p> a </p> <p> c d </p> </root>
		_, err = tree.EditByIndex(2, 3, nil, nil, helper.IssueTime(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p>a</p><p>cd</p></root>", tree.ToXML())

		structure = tree.Structure()
		assert.Equal(t, 7, structure.Size)
		assert.Equal(t, 1, structure.Children[0].Size)
		assert.Equal(t, 1, structure.Children[0].Children[0].Size)
	})

	t.Run("delete nodes between element nodes test", func(t *testing.T) {
		// 01. Create a tree with 2 paragraphs.
		//       0   1 2 3    4   5 6 7    8
		// <root> <p> a b </p> <p> c d </p> </root>

		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := crdt.NewTree(crdt.NewTreeNode(helper.IssuePos(ctx), "root", nil), helper.IssueTime(ctx))
		_, err := tree.EditByIndex(0, 0, nil, []*crdt.TreeNode{crdt.NewTreeNode(helper.IssuePos(ctx),
			"p", nil)}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		_, err = tree.EditByIndex(1, 1, nil, []*crdt.TreeNode{crdt.NewTreeNode(helper.IssuePos(ctx),
			"text", nil, "ab")}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		_, err = tree.EditByIndex(4, 4, nil, []*crdt.TreeNode{crdt.NewTreeNode(helper.IssuePos(ctx),
			"p", nil)}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		_, err = tree.EditByIndex(5, 5, nil, []*crdt.TreeNode{crdt.NewTreeNode(helper.IssuePos(ctx),
			"text", nil, "cd")}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p>ab</p><p>cd</p></root>", tree.ToXML())

		// 02. delete b, c and first paragraph.
		//       0   1 2 3    4
		// <root> <p> a d </p> </root>
		_, err = tree.EditByIndex(2, 6, nil, nil, helper.IssueTime(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p>a</p><p>d</p></root>", tree.ToXML())

		// TODO(sejongk): Use the below assertions after implementing Tree.Move.
		// assert.Equal(t, "<root><p>ad</p></root>", tree.ToXML())

		// structure := tree.Structure()
		// assert.Equal(t, 4, structure.Size)
		// assert.Equal(t, 2, structure.Children[0].Size)
		// assert.Equal(t, 1, structure.Children[0].Children[0].Size)
		// assert.Equal(t, 1, structure.Children[0].Children[1].Size)

		// // 03. insert a new text node at the start of the first paragraph.
		// _, err = tree.EditByIndex(1, 1, nil, []*crdt.TreeNode{crdt.NewTreeNode(helper.IssuePos(ctx),
		// 	"text", nil, "@")}, helper.IssueTime(ctx))
		// assert.NoError(t, err)
		// assert.Equal(t, "<root><p>@ad</p></root>", tree.ToXML())
	})

	t.Run("style node with element attributes test", func(t *testing.T) {
		// 01. style attributes to an element node.
		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := crdt.NewTree(crdt.NewTreeNode(helper.IssuePos(ctx), "root", nil), helper.IssueTime(ctx))
		_, err := tree.EditByIndex(0, 0, nil, []*crdt.TreeNode{crdt.NewTreeNode(helper.IssuePos(ctx),
			"p", nil)}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		_, err = tree.EditByIndex(1, 1, nil, []*crdt.TreeNode{crdt.NewTreeNode(helper.IssuePos(ctx),
			"text", nil, "ab")}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		_, err = tree.EditByIndex(4, 4, nil, []*crdt.TreeNode{crdt.NewTreeNode(helper.IssuePos(ctx),
			"p", nil)}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		_, err = tree.EditByIndex(5, 5, nil, []*crdt.TreeNode{crdt.NewTreeNode(helper.IssuePos(ctx),
			"text", nil, "cd")}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p>ab</p><p>cd</p></root>", tree.ToXML())

		// style attributes with opening tag
		err = tree.StyleByIndex(0, 1, map[string]string{"weight": "bold"}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		assert.Equal(t, `<root><p weight="bold">ab</p><p>cd</p></root>`, tree.ToXML())

		// style attributes with closing tag
		err = tree.StyleByIndex(3, 4, map[string]string{"color": "red"}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		assert.Equal(t, `<root><p color="red" weight="bold">ab</p><p>cd</p></root>`, tree.ToXML())

		// style attributes with the whole
		err = tree.StyleByIndex(0, 4, map[string]string{"size": "small"}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		assert.Equal(t, `<root><p color="red" size="small" weight="bold">ab</p><p>cd</p></root>`, tree.ToXML())

		// 02. style attributes to elements.
		err = tree.StyleByIndex(0, 5, map[string]string{"style": "italic"}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		assert.Equal(t, `<root><p color="red" size="small" style="italic" weight="bold">ab</p>`+
			`<p style="italic">cd</p></root>`, tree.ToXML())

		// 03. Ignore styling attributes to text nodes.
		err = tree.StyleByIndex(1, 3, map[string]string{"bold": "true"}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		assert.Equal(t, `<root><p color="red" size="small" style="italic" weight="bold">ab</p>`+
			`<p style="italic">cd</p></root>`, tree.ToXML())
	})

	t.Run("can find the closest TreePos when parentNode or leftSiblingNode does not exist", func(t *testing.T) {
		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)
		//       0
		// <root> </root>
		tree := crdt.NewTree(crdt.NewTreeNode(helper.IssuePos(ctx), "r", nil), helper.IssueTime(ctx))
		assert.Equal(t, 0, tree.Root().Len())
		assert.Equal(t, "<r></r>", tree.ToXML())

		//       0   1 2 3    4
		// <root> <p> a b </p> </root>
		pNode := crdt.NewTreeNode(helper.IssuePos(ctx), "p", nil)
		textNode := crdt.NewTreeNode(helper.IssuePos(ctx), "text", nil, "ab")

		_, err := tree.EditByIndex(0, 0, nil, []*crdt.TreeNode{pNode}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		_, err = tree.EditByIndex(1, 1, nil, []*crdt.TreeNode{textNode}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>ab</p></r>", tree.ToXML())

		// Find the closest index.TreePos when leftSiblingNode in crdt.TreePos is removed.
		//       0   1    2
		// <root> <p> </p> </root>
		_, err = tree.EditByIndex(1, 3, nil, nil, helper.IssueTime(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p></p></r>", tree.ToXML())

		treePos := crdt.NewTreePos(pNode.ID, textNode.ID)

		parent, leftSibling, err := tree.FindTreeNodesWithSplitText(treePos, helper.IssueTime(ctx))
		assert.NoError(t, err)
		idx, err := tree.ToIndex(parent.Value, leftSibling.Value)
		assert.NoError(t, err)
		assert.Equal(t, 1, idx)

		// Find the closest index.TreePos when parentNode in crdt.TreePos is removed.
		//       0
		// <root> </root>
		_, err = tree.EditByIndex(0, 2, nil, nil, helper.IssueTime(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r></r>", tree.ToXML())

		treePos = crdt.NewTreePos(pNode.ID, textNode.ID)
		parent, leftSibling, err = tree.FindTreeNodesWithSplitText(treePos, helper.IssueTime(ctx))
		assert.NoError(t, err)
		idx, err = tree.ToIndex(parent.Value, leftSibling.Value)
		assert.NoError(t, err)
		assert.Equal(t, 0, idx)
	})

	t.Run("delete nodes in a multi-level range test", func(t *testing.T) {
		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := crdt.NewTree(crdt.NewTreeNode(helper.IssuePos(ctx), "root", nil), helper.IssueTime(ctx))
		_, err := tree.EditByIndex(0, 0, nil, []*crdt.TreeNode{crdt.NewTreeNode(helper.IssuePos(ctx),
			"p", nil)}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		_, err = tree.EditByIndex(1, 1, nil, []*crdt.TreeNode{crdt.NewTreeNode(helper.IssuePos(ctx),
			"text", nil, "ab")}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		_, err = tree.EditByIndex(3, 3, nil, []*crdt.TreeNode{crdt.NewTreeNode(helper.IssuePos(ctx),
			"p", nil)}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		_, err = tree.EditByIndex(4, 4, nil, []*crdt.TreeNode{crdt.NewTreeNode(helper.IssuePos(ctx),
			"text", nil, "x")}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		_, err = tree.EditByIndex(7, 7, nil, []*crdt.TreeNode{crdt.NewTreeNode(helper.IssuePos(ctx),
			"p", nil)}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		_, err = tree.EditByIndex(8, 8, nil, []*crdt.TreeNode{crdt.NewTreeNode(helper.IssuePos(ctx),
			"p", nil)}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		_, err = tree.EditByIndex(9, 9, nil, []*crdt.TreeNode{crdt.NewTreeNode(helper.IssuePos(ctx),
			"text", nil, "cd")}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		_, err = tree.EditByIndex(13, 13, nil, []*crdt.TreeNode{crdt.NewTreeNode(helper.IssuePos(ctx),
			"p", nil)}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		_, err = tree.EditByIndex(14, 14, nil, []*crdt.TreeNode{crdt.NewTreeNode(helper.IssuePos(ctx),
			"p", nil)}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		_, err = tree.EditByIndex(15, 15, nil, []*crdt.TreeNode{crdt.NewTreeNode(helper.IssuePos(ctx),
			"text", nil, "y")}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		_, err = tree.EditByIndex(17, 17, nil, []*crdt.TreeNode{crdt.NewTreeNode(helper.IssuePos(ctx),
			"text", nil, "ef")}, helper.IssueTime(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p>ab<p>x</p></p><p><p>cd</p></p><p><p>y</p>ef</p></root>", tree.ToXML())

		_, err = tree.EditByIndex(2, 18, nil, nil, helper.IssueTime(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p>a</p><p>f</p></root>", tree.ToXML())

		// TODO(sejongk): Use the below assertion after implementing Tree.Move.
		// assert.Equal(t, "<root><p>af</p></root>", tree.ToXML())
	})
}

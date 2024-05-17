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

	"github.com/yorkie-team/yorkie/pkg/document/change"
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

func createHelloTree(t *testing.T, ctx *change.Context) *crdt.Tree {
	// TODO(raararaara): This test should be generalized. e.g) createTree(ctx, "<r><p>hello</p></r>")
	// https://pkg.go.dev/encoding/xml#Unmarshal
	tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "r", nil), helper.TimeT(ctx))
	err := tree.EditT(0, 0, []*crdt.TreeNode{
		crdt.NewTreeNode(helper.PosT(ctx), "p", nil),
	}, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
	assert.NoError(t, err)

	err = tree.EditT(1, 1, []*crdt.TreeNode{
		crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "hello"),
	}, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
	assert.NoError(t, err)
	assert.Equal(t, "<r><p>hello</p></r>", tree.ToXML())
	assert.Equal(t, 7, tree.Root().Len())

	return tree
}

func TestTreeNode(t *testing.T) {
	t.Run("text node test", func(t *testing.T) {
		node := crdt.NewTreeNode(dummyTreeNodeID, "text", nil, "hello")
		assert.Equal(t, dummyTreeNodeID, node.ID())
		assert.Equal(t, "text", node.Type())
		assert.Equal(t, "hello", node.Value)
		assert.Equal(t, 5, node.Len())
		assert.Equal(t, true, node.IsText())
		assert.Equal(t, false, node.IsRemoved())
	})

	t.Run("element node test", func(t *testing.T) {
		root := crdt.NewTreeNode(dummyTreeNodeID, "r", nil)
		para := crdt.NewTreeNode(dummyTreeNodeID, "p", nil)
		assert.NoError(t, root.Append(para))
		err := para.Append(crdt.NewTreeNode(dummyTreeNodeID, "text", nil, "helloyorkie"))
		assert.NoError(t, err)
		assert.Equal(t, "<p>helloyorkie</p>", crdt.ToXML(para))
		assert.Equal(t, 11, para.Len())
		assert.Equal(t, false, para.IsText())

		left, err := para.Child(0)
		assert.NoError(t, err)
		right, err := left.SplitText(5, 0)
		assert.NoError(t, err)
		assert.Equal(t, "<p>helloyorkie</p>", crdt.ToXML(para))
		assert.Equal(t, 11, para.Len())

		assert.Equal(t, "hello", left.Value)
		assert.Equal(t, "yorkie", right.Value)
		assert.Equal(t, &crdt.TreeNodeID{CreatedAt: time.InitialTicket, Offset: 0}, left.ID())
		assert.Equal(t, &crdt.TreeNodeID{CreatedAt: time.InitialTicket, Offset: 5}, right.ID())

		split, err := para.SplitElement(1, func() *time.Ticket {
			return time.InitialTicket
		})
		assert.NoError(t, err)
		assert.Equal(t, "<p>hello</p>", crdt.ToXML(para))
		assert.Equal(t, "<p>yorkie</p>", crdt.ToXML(split))
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
			right, err := left.SplitText(2, 0)
			assert.NoError(t, err)
			assert.Equal(t, test.length-2, right.Len())
		}
	})

	t.Run("deepcopy test with deletion", func(t *testing.T) {
		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := createHelloTree(t, ctx)

		// To make tree have a deletion to check length modification.
		err := tree.EditT(4, 5, nil, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>helo</p></r>", tree.ToXML())
		assert.Equal(t, 6, tree.Root().Len())

		clone, err := tree.Root().DeepCopy()
		assert.NoError(t, err)
		helper.AssertEqualTreeNode(t, tree.Root(), clone)
	})

	t.Run("deepcopy test with split", func(t *testing.T) {
		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := createHelloTree(t, ctx)

		// To make tree have split text nodes.
		err := tree.EditT(3, 3, nil, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>hello</p></r>", tree.ToXML())

		clone, err := tree.Root().DeepCopy()
		assert.NoError(t, err)
		helper.AssertEqualTreeNode(t, tree.Root(), clone)
	})

	t.Run("ToXML test", func(t *testing.T) {
		node := crdt.NewTreeNode(dummyTreeNodeID, "text", nil, "hello")
		assert.Equal(t, "hello", crdt.ToXML(node))

		para := crdt.NewTreeNode(dummyTreeNodeID, "p", nil)
		assert.NoError(t, para.Append(node))
		assert.Equal(t, "<p>hello</p>", crdt.ToXML(para))

		elemWithAttrs := crdt.NewTreeNode(dummyTreeNodeID, "p", nil)
		assert.NoError(t, elemWithAttrs.Append(node))
		elemWithAttrs.SetAttr("e", "\"true\"", time.MaxTicket)
		assert.Equal(t, `<p e="\"true\"">hello</p>`, crdt.ToXML(elemWithAttrs))

		elemWithAttrs.SetAttr("b", "t", time.MaxTicket)
		assert.Equal(t, `<p b="t" e="\"true\"">hello</p>`, crdt.ToXML(elemWithAttrs))
	})
}

func TestTreeEdit(t *testing.T) {
	t.Run("insert nodes with Edit test", func(t *testing.T) {
		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)
		//       0
		// <root> </root>
		tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "r", nil), helper.TimeT(ctx))
		assert.Equal(t, 0, tree.Root().Len())
		assert.Equal(t, "<r></r>", tree.ToXML())

		//           1
		// <root> <p> </p> </root>
		err := tree.EditT(0, 0, []*crdt.TreeNode{crdt.NewTreeNode(helper.
			PosT(ctx), "p", nil)}, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p></p></r>", tree.ToXML())
		assert.Equal(t, 2, tree.Root().Len())

		//           1
		// <root> <p> h e l l o </p> </root>
		err = tree.EditT(1, 1, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "hello"),
		}, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>hello</p></r>", tree.ToXML())
		assert.Equal(t, 7, tree.Root().Len())

		//       0   1 2 3 4 5 6    7   8 9  10 11 12 13    14
		// <root> <p> h e l l o </p> <p> w  o  r  l  d  </p>  </root>
		p := crdt.NewTreeNode(helper.PosT(ctx), "p", nil)
		err = p.InsertAt(crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "world"), 0)
		assert.NoError(t, err)
		err = tree.EditT(7, 7, []*crdt.TreeNode{p}, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>hello</p><p>world</p></r>", tree.ToXML())
		assert.Equal(t, 14, tree.Root().Len())

		//       0   1 2 3 4 5 6 7    8   9 10 11 12 13 14    15
		// <root> <p> h e l l o ! </p> <p> w  o  r  l  d  </p>  </root>
		err = tree.EditT(6, 6, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "!"),
		}, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
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
		}, tree.ToTreeNodeForTest())

		//       0   1 2 3 4 5 6 7 8    9   10 11 12 13 14 15    16
		// <root> <p> h e l l o ~ ! </p> <p>  w  o  r  l  d  </p>  </root>
		err = tree.EditT(6, 6, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "~"),
		}, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>hello~!</p><p>world</p></r>", tree.ToXML())
	})

	t.Run("delete text nodes with Edit test", func(t *testing.T) {
		// 01. Create a tree with 2 paragraphs.
		//       0   1 2 3    4   5 6 7    8
		// <root> <p> a b </p> <p> c d </p> </root>

		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "root", nil), helper.TimeT(ctx))
		err := tree.EditT(0, 0, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(1, 1, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "ab"),
		}, 0,
			helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(4, 4, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "p", nil),
		}, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(5, 5, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "cd"),
		}, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p>ab</p><p>cd</p></root>", tree.ToXML())

		node := tree.ToTreeNodeForTest()
		assert.Equal(t, 8, node.Size)
		assert.Equal(t, 2, node.Children[0].Size)
		assert.Equal(t, 2, node.Children[0].Children[0].Size)

		// 02. Delete b from the second paragraph.
		// 	     0   1 2    3   4 5 6    7
		// <root> <p> a </p> <p> c d </p> </root>
		err = tree.EditT(2, 3, nil, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p>a</p><p>cd</p></root>", tree.ToXML())

		node = tree.ToTreeNodeForTest()
		assert.Equal(t, 7, node.Size)
		assert.Equal(t, 1, node.Children[0].Size)
		assert.Equal(t, 1, node.Children[0].Children[0].Size)
	})

	t.Run("delete nodes between element nodes test", func(t *testing.T) {
		// 01. Create a tree with 2 paragraphs.
		//       0   1 2 3    4   5 6 7    8
		// <root> <p> a b </p> <p> c d </p> </root>

		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "root", nil), helper.TimeT(ctx))
		err := tree.EditT(0, 0, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(1, 1, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "ab"),
		}, 0,
			helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(4, 4, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "p", nil),
		}, 0,
			helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(5, 5, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "cd"),
		}, 0,
			helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p>ab</p><p>cd</p></root>", tree.ToXML())

		// 02. delete b, c and the second paragraph.
		//       0   1 2 3    4
		// <root> <p> a d </p> </root>
		err = tree.EditT(2, 6, nil, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p>ad</p></root>", tree.ToXML())

		node := tree.ToTreeNodeForTest()
		assert.Equal(t, 4, node.Size)
		assert.Equal(t, 2, node.Children[0].Size)
		assert.Equal(t, 1, node.Children[0].Children[0].Size)
		assert.Equal(t, 1, node.Children[0].Children[1].Size)

		// 03. insert a new text node at the start of the first paragraph.
		err = tree.EditT(1, 1, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "@"),
		}, 0,
			helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p>@ad</p></root>", tree.ToXML())
	})

	t.Run("delete nodes between element nodes in different levels test", func(t *testing.T) {
		// 01. Create a tree with 2 paragraphs.
		//       0   1   2 3 4    5    6   7 8 9    10
		// <root> <p> <b> a b </b> </p> <p> c d </p>  </root>

		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "root", nil), helper.TimeT(ctx))
		err := tree.EditT(0, 0, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(1, 1, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "b", nil)}, 0,
			helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(2, 2, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "ab"),
		}, 0,
			helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(6, 6, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(7, 7, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "cd"),
		}, 0,
			helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p><b>ab</b></p><p>cd</p></root>", tree.ToXML())

		// 02. delete b, c and the second paragraph.
		//       0   1   2 3 4    5
		// <root> <p> <b> a d </b> </root>
		err = tree.EditT(3, 8, nil, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p><b>ad</b></p></root>", tree.ToXML())
	})

	t.Run("style node with element attributes test", func(t *testing.T) {
		// 01. style attributes to an element node.
		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "root", nil), helper.TimeT(ctx))
		err := tree.EditT(0, 0, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(1, 1, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "ab"),
		}, 0,
			helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(4, 4, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "p", nil),
		}, 0,
			helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(5, 5, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "cd"),
		}, 0,
			helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p>ab</p><p>cd</p></root>", tree.ToXML())

		// style attributes with opening tag
		_, _, err = tree.StyleByIndex(0, 1, map[string]string{"weight": "bold"}, helper.TimeT(ctx), nil)
		assert.NoError(t, err)
		assert.Equal(t, `<root><p weight="bold">ab</p><p>cd</p></root>`, tree.ToXML())

		// style attributes with closing tag
		_, _, err = tree.StyleByIndex(3, 4, map[string]string{"color": "red"}, helper.TimeT(ctx), nil)
		assert.NoError(t, err)
		assert.Equal(t, `<root><p color="red" weight="bold">ab</p><p>cd</p></root>`, tree.ToXML())

		// style attributes with the whole
		_, _, err = tree.StyleByIndex(0, 4, map[string]string{"size": "small"}, helper.TimeT(ctx), nil)
		assert.NoError(t, err)
		assert.Equal(t, `<root><p color="red" size="small" weight="bold">ab</p><p>cd</p></root>`, tree.ToXML())

		// 02. style attributes to elements.
		_, _, err = tree.StyleByIndex(0, 5, map[string]string{"style": "italic"}, helper.TimeT(ctx), nil)
		assert.NoError(t, err)
		assert.Equal(t, `<root><p color="red" size="small" style="italic" weight="bold">ab</p>`+
			`<p style="italic">cd</p></root>`, tree.ToXML())

		// 03. Ignore styling attributes to text nodes.
		_, _, err = tree.StyleByIndex(1, 3, map[string]string{"bold": "true"}, helper.TimeT(ctx), nil)
		assert.NoError(t, err)
		assert.Equal(t, `<root><p color="red" size="small" style="italic" weight="bold">ab</p>`+
			`<p style="italic">cd</p></root>`, tree.ToXML())
	})

	t.Run("can find the closest TreePos when parentNode or leftSiblingNode does not exist", func(t *testing.T) {
		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)
		//       0
		// <root> </root>
		tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "r", nil), helper.TimeT(ctx))
		assert.Equal(t, 0, tree.Root().Len())
		assert.Equal(t, "<r></r>", tree.ToXML())

		//       0   1 2 3    4
		// <root> <p> a b </p> </root>
		pNode := crdt.NewTreeNode(helper.PosT(ctx), "p", nil)
		textNode := crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "ab")

		err := tree.EditT(0, 0, []*crdt.TreeNode{pNode}, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(1, 1, []*crdt.TreeNode{textNode}, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>ab</p></r>", tree.ToXML())

		// Find the closest index.TreePos when leftSiblingNode in crdt.TreePos is removed.
		//       0   1    2
		// <root> <p> </p> </root>
		err = tree.EditT(1, 3, nil, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p></p></r>", tree.ToXML())

		treePos := crdt.NewTreePos(pNode.ID(), textNode.ID())

		parent, leftSibling, err := tree.FindTreeNodesWithSplitText(treePos, helper.TimeT(ctx))
		assert.NoError(t, err)
		idx, err := tree.ToIndex(parent, leftSibling)
		assert.NoError(t, err)
		assert.Equal(t, 1, idx)

		// Find the closest index.TreePos when parentNode in crdt.TreePos is removed.
		//       0
		// <root> </root>
		err = tree.EditT(0, 2, nil, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r></r>", tree.ToXML())

		treePos = crdt.NewTreePos(pNode.ID(), textNode.ID())
		parent, leftSibling, err = tree.FindTreeNodesWithSplitText(treePos, helper.TimeT(ctx))
		assert.NoError(t, err)
		idx, err = tree.ToIndex(parent, leftSibling)
		assert.NoError(t, err)
		assert.Equal(t, 0, idx)
	})

	t.Run("marshal test", func(t *testing.T) {
		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "root", nil), helper.TimeT(ctx))
		err := tree.EditT(0, 0, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "p", nil),
		}, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(1, 1, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, `"Hello" \n i'm yorkie!`),
		}, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)

		assert.Equal(t, `<root><p>"Hello" \n i'm yorkie!</p></root>`, tree.ToXML())
		assert.Equal(
			t,
			`{"type":"root","children":[{"type":"p","children":[{"type":"text","value":"\"Hello\" \\n i'm yorkie!"}]}]}`,
			tree.Marshal(),
		)
	})
}

func TestTreeSplit(t *testing.T) {
	t.Run("split text nodes test", func(t *testing.T) {
		ctx := helper.TextChangeContext(helper.TestRoot())
		expectedInitial := crdt.TreeNodeForTest{
			Type: "r",
			Children: []crdt.TreeNodeForTest{{
				Type:      "p",
				Children:  []crdt.TreeNodeForTest{{Type: "text", Value: "helloworld", Size: 10, IsRemoved: false}},
				Size:      10,
				IsRemoved: false,
			}},
			Size:      12,
			IsRemoved: false,
		}

		tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "r", nil), helper.TimeT(ctx))
		err := tree.EditT(0, 0, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(1, 1, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "helloworld"),
		}, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>helloworld</p></r>", tree.ToXML())
		assert.Equal(t, 12, tree.Root().Len())
		assert.Equal(t, tree.ToTreeNodeForTest(), expectedInitial)

		// 01. Split left side of 'helloworld'.
		err = tree.EditT(1, 1, nil, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, tree.ToTreeNodeForTest(), expectedInitial)

		// 02. Split right side of 'helloworld'.
		err = tree.EditT(11, 11, nil, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, tree.ToTreeNodeForTest(), expectedInitial)

		// 03. Split 'helloworld' into 'hello' and 'world'.
		err = tree.EditT(6, 6, nil, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, tree.ToTreeNodeForTest(), crdt.TreeNodeForTest{
			Type: "r",
			Children: []crdt.TreeNodeForTest{{
				Type: "p",
				Children: []crdt.TreeNodeForTest{
					{Type: "text", Value: "hello", Size: 5, IsRemoved: false},
					{Type: "text", Value: "world", Size: 5, IsRemoved: false},
				},
				Size:      10,
				IsRemoved: false,
			}},
			Size:      12,
			IsRemoved: false,
		})
	})

	t.Run("split element nodes level 1", func(t *testing.T) {
		//       0   1 2 3    4
		// <root> <p> a b </p> </root>
		ctx := helper.TextChangeContext(helper.TestRoot())

		// 01. Split position 1.
		tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "r", nil), helper.TimeT(ctx))
		err := tree.EditT(0, 0, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(1, 1, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "ab"),
		}, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>ab</p></r>", tree.ToXML())
		assert.Equal(t, 4, tree.Root().Len())
		err = tree.EditT(1, 1, nil, 1, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p></p><p>ab</p></r>", tree.ToXML())
		assert.Equal(t, 6, tree.Root().Len())

		// 02. Split position 2.
		tree = crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "r", nil), helper.TimeT(ctx))
		err = tree.EditT(0, 0, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "p", nil),
		}, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(1, 1, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "ab"),
		}, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>ab</p></r>", tree.ToXML())
		assert.Equal(t, 4, tree.Root().Len())
		err = tree.EditT(2, 2, nil, 1, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>a</p><p>b</p></r>", tree.ToXML())
		assert.Equal(t, 6, tree.Root().Len())

		// 03. Split position 3.
		tree = crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "r", nil), helper.TimeT(ctx))
		err = tree.EditT(0, 0, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "p", nil),
		}, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(1, 1, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "ab"),
		}, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>ab</p></r>", tree.ToXML())
		assert.Equal(t, 4, tree.Root().Len())
		err = tree.EditT(3, 3, nil, 1, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>ab</p><p></p></r>", tree.ToXML())
		assert.Equal(t, 6, tree.Root().Len())
	})

	t.Run("split element nodes multi-level", func(t *testing.T) {
		//       0   1   2 3 4    5    6
		// <root> <p> <b> a b </b> </p> </root>
		ctx := helper.TextChangeContext(helper.TestRoot())

		// 01. Split nodes level 1.
		tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "r", nil), helper.TimeT(ctx))
		err := tree.EditT(0, 0, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(1, 1, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "b", nil)}, 0,
			helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(2, 2, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "ab"),
		}, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p><b>ab</b></p></r>", tree.ToXML())
		assert.Equal(t, 6, tree.Root().Len())
		err = tree.EditT(3, 3, nil, 1, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p><b>a</b><b>b</b></p></r>", tree.ToXML())
		assert.Equal(t, 8, tree.Root().Len())

		// 02. Split nodes level 2.
		tree = crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "r", nil), helper.TimeT(ctx))
		err = tree.EditT(0, 0, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(1, 1, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "b", nil)}, 0,
			helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(2, 2, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "ab"),
		}, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p><b>ab</b></p></r>", tree.ToXML())
		assert.Equal(t, 6, tree.Root().Len())
		err = tree.EditT(3, 3, nil, 2, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p><b>a</b></p><p><b>b</b></p></r>", tree.ToXML())
		assert.Equal(t, 10, tree.Root().Len())
	})

	t.Run("split and merge element nodes by edit", func(t *testing.T) {
		ctx := helper.TextChangeContext(helper.TestRoot())

		tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "r", nil), helper.TimeT(ctx))
		err := tree.EditT(0, 0, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(1, 1, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "abcd"),
		}, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>abcd</p></r>", tree.ToXML())

		//       0   1 2 3    4   5 6 7    8
		// <root> <p> a b </p> <p> c d </p> </root>
		err = tree.EditT(3, 3, nil, 1, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>ab</p><p>cd</p></r>", tree.ToXML())
		assert.Equal(t, 8, tree.Root().Len())

		err = tree.EditT(3, 5, nil, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>abcd</p></r>", tree.ToXML())
		assert.Equal(t, 6, tree.Root().Len())
	})
}

func TestTreeMerge(t *testing.T) {
	t.Run("delete nodes in a multi-level range test", func(t *testing.T) {
		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "root", nil), helper.TimeT(ctx))
		err := tree.EditT(0, 0, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(1, 1, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "ab"),
		}, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(3, 3, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(4, 4, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "x"),
		}, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(7, 7, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(8, 8, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(9, 9, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "cd"),
		}, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(13, 13, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(14, 14, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(15, 15, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "y"),
		}, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		err = tree.EditT(17, 17, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "ef"),
		}, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p>ab<p>x</p></p><p><p>cd</p></p><p><p>y</p>ef</p></root>", tree.ToXML())

		err = tree.EditT(2, 18, nil, 0, helper.TimeT(ctx), issueTimeTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p>af</p></root>", tree.ToXML())
	})
}

func issueTimeTicket(change *change.Context) func() *time.Ticket {
	return func() *time.Ticket {
		return helper.TimeT(change)
	}
}

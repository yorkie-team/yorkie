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
	"github.com/stretchr/testify/require"

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
	_, _, err := tree.EditT(0, 0, []*crdt.TreeNode{
		crdt.NewTreeNode(helper.PosT(ctx), "p", nil),
	}, 0, helper.TimeT(ctx), issueTicket(ctx))
	assert.NoError(t, err)

	_, _, err = tree.EditT(1, 1, []*crdt.TreeNode{
		crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "hello"),
	}, 0, helper.TimeT(ctx), issueTicket(ctx))
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
		right, _, err := left.SplitText(5, 0)
		assert.NoError(t, err)
		assert.Equal(t, "<p>helloyorkie</p>", crdt.ToXML(para))
		assert.Equal(t, 11, para.Len())

		assert.Equal(t, "hello", left.Value)
		assert.Equal(t, "yorkie", right.Value)
		assert.Equal(t, &crdt.TreeNodeID{CreatedAt: time.InitialTicket, Offset: 0}, left.ID())
		assert.Equal(t, &crdt.TreeNodeID{CreatedAt: time.InitialTicket, Offset: 5}, right.ID())

		split, _, err := para.SplitElement(1, func() *time.Ticket {
			return time.InitialTicket
		}, nil)
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

	t.Run("split element should copy attributes", func(t *testing.T) {
		attrs := crdt.NewRHT()
		attrs.Set("bold", "true", time.InitialTicket)

		root := crdt.NewTreeNode(dummyTreeNodeID, "r", nil)
		para := crdt.NewTreeNode(dummyTreeNodeID, "p", attrs)
		assert.NoError(t, root.Append(para))
		assert.NoError(t, para.Append(crdt.NewTreeNode(dummyTreeNodeID, "text", nil, "helloworld")))
		assert.Equal(t, `<r><p bold="true">helloworld</p></r>`, crdt.ToXML(root))

		// split text node
		left, err := para.Child(0)
		assert.NoError(t, err)
		_, _, err = left.SplitText(5, 0)
		assert.NoError(t, err)

		// split element node
		split, _, err := para.SplitElement(1, func() *time.Ticket {
			return time.InitialTicket
		}, nil)
		assert.NoError(t, err)
		assert.Equal(t, `<p bold="true">hello</p>`, crdt.ToXML(para))
		assert.Equal(t, `<p bold="true">world</p>`, crdt.ToXML(split))
	})

	t.Run("split element should copy merge stamp", func(t *testing.T) {
		root := crdt.NewTreeNode(dummyTreeNodeID, "r", nil)
		para := crdt.NewTreeNode(dummyTreeNodeID, "p", nil)
		assert.NoError(t, root.Append(para))
		assert.NoError(t, para.Append(crdt.NewTreeNode(dummyTreeNodeID, "text", nil, "helloworld")))

		// The paragraph was previously moved by a merge.
		mergedFrom := &crdt.TreeNodeID{CreatedAt: time.InitialTicket, Offset: 7}
		para.MergedFrom = mergedFrom
		para.MergedAt = time.InitialTicket

		left, err := para.Child(0)
		assert.NoError(t, err)
		_, _, err = left.SplitText(5, 0)
		assert.NoError(t, err)

		split, _, err := para.SplitElement(1, func() *time.Ticket {
			return time.InitialTicket
		}, nil)
		assert.NoError(t, err)

		// The split product holds the other half of the same moved node,
		// so it must carry the same merge stamp (as SplitText does).
		assert.NotNil(t, split.MergedFrom)
		assert.True(t, split.MergedFrom.Equal(mergedFrom))
		assert.Equal(t, time.InitialTicket, split.MergedAt)
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
			right, _, err := left.SplitText(2, 0)
			assert.NoError(t, err)
			assert.Equal(t, test.length-2, right.Len())
		}
	})

	t.Run("deepcopy test with deletion", func(t *testing.T) {
		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := createHelloTree(t, ctx)

		// To make tree have a deletion to check length modification.
		_, _, err := tree.EditT(4, 5, nil, 0, helper.TimeT(ctx), issueTicket(ctx))
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
		_, _, err := tree.EditT(3, 3, nil, 0, helper.TimeT(ctx), issueTicket(ctx))
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
		_, _, err := tree.EditT(0, 0, []*crdt.TreeNode{crdt.NewTreeNode(helper.
			PosT(ctx), "p", nil)}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p></p></r>", tree.ToXML())
		assert.Equal(t, 2, tree.Root().Len())

		//           1
		// <root> <p> h e l l o </p> </root>
		_, _, err = tree.EditT(1, 1, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "hello"),
		}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>hello</p></r>", tree.ToXML())
		assert.Equal(t, 7, tree.Root().Len())

		//       0   1 2 3 4 5 6    7   8 9  10 11 12 13    14
		// <root> <p> h e l l o </p> <p> w  o  r  l  d  </p>  </root>
		p := crdt.NewTreeNode(helper.PosT(ctx), "p", nil)
		err = p.InsertAt(crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "world"), 0)
		assert.NoError(t, err)
		_, _, err = tree.EditT(7, 7, []*crdt.TreeNode{p}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>hello</p><p>world</p></r>", tree.ToXML())
		assert.Equal(t, 14, tree.Root().Len())

		//       0   1 2 3 4 5 6 7    8   9 10 11 12 13 14    15
		// <root> <p> h e l l o ! </p> <p> w  o  r  l  d  </p>  </root>
		_, _, err = tree.EditT(6, 6, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "!"),
		}, 0, helper.TimeT(ctx), issueTicket(ctx))
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
		_, _, err = tree.EditT(6, 6, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "~"),
		}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>hello~!</p><p>world</p></r>", tree.ToXML())
	})

	t.Run("delete text nodes with Edit test", func(t *testing.T) {
		// 01. Create a tree with 2 paragraphs.
		//       0   1 2 3    4   5 6 7    8
		// <root> <p> a b </p> <p> c d </p> </root>

		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "root", nil), helper.TimeT(ctx))
		_, _, err := tree.EditT(0, 0, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(1, 1, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "ab"),
		}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(4, 4, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "p", nil),
		}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(5, 5, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "cd"),
		}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p>ab</p><p>cd</p></root>", tree.ToXML())

		node := tree.ToTreeNodeForTest()
		assert.Equal(t, 8, node.Size)
		assert.Equal(t, 2, node.Children[0].Size)
		assert.Equal(t, 2, node.Children[0].Children[0].Size)

		// 02. Delete b from the second paragraph.
		// 	     0   1 2    3   4 5 6    7
		// <root> <p> a </p> <p> c d </p> </root>
		_, _, err = tree.EditT(2, 3, nil, 0, helper.TimeT(ctx), issueTicket(ctx))
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
		_, _, err := tree.EditT(0, 0, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(1, 1, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "ab"),
		}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(4, 4, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "p", nil),
		}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(5, 5, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "cd"),
		}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p>ab</p><p>cd</p></root>", tree.ToXML())

		// 02. delete b, c and the second paragraph.
		//       0   1 2 3    4
		// <root> <p> a d </p> </root>
		_, _, err = tree.EditT(2, 6, nil, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p>ad</p></root>", tree.ToXML())

		node := tree.ToTreeNodeForTest()
		assert.Equal(t, 4, node.Size)
		assert.Equal(t, 2, node.Children[0].Size)
		assert.Equal(t, 1, node.Children[0].Children[0].Size)
		assert.Equal(t, 1, node.Children[0].Children[1].Size)

		// 03. insert a new text node at the start of the first paragraph.
		_, _, err = tree.EditT(1, 1, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "@"),
		}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p>@ad</p></root>", tree.ToXML())
	})

	t.Run("merge moves an element tombstone with correct length accounting", func(t *testing.T) {
		// 01. Create <root><p>ab</p><p><b></b>cd</p></root>.
		//       0   1 2 3    4   5   6    7 8 9    10
		// <root> <p> a b </p> <p> <b> </b> c d </p>  </root>
		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "root", nil), helper.TimeT(ctx))
		_, _, err := tree.EditT(0, 0, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(1, 1, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "ab"),
		}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(4, 4, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(5, 5, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "b", nil)}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(7, 7, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "cd"),
		}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p>ab</p><p><b></b>cd</p></root>", tree.ToXML())

		// 02. Delete b, the second paragraph's open tag, the <b></b>
		// element and c, merging the paragraph. The <b></b> element becomes
		// a tombstone moved into the first paragraph. Its padding must not
		// inflate the visible length of the (surviving) first paragraph.
		_, _, err = tree.EditT(2, 8, nil, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p>ad</p></root>", tree.ToXML())

		node := tree.ToTreeNodeForTest()
		assert.Equal(t, 4, node.Size)
		assert.Equal(t, 2, node.Children[0].Size)
	})

	t.Run("resolves a range boundary right after the merge-source tombstone", func(t *testing.T) {
		// 01. Create <root><p>ab</p><p>cd</p></root>.
		//       0   1 2 3    4   5 6 7     8
		// <root> <p> a b </p> <p> c d </p>  </root>
		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "root", nil), helper.TimeT(ctx))
		_, _, err := tree.EditT(0, 0, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(1, 1, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "ab"),
		}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		p2Node := crdt.NewTreeNode(helper.PosT(ctx), "p", nil)
		_, _, err = tree.EditT(4, 4, []*crdt.TreeNode{p2Node}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(5, 5, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "cd"),
		}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p>ab</p><p>cd</p></root>", tree.ToXML())

		// 02. Capture the leftmost position inside the second paragraph,
		// then merge the second paragraph into the first.
		pos := crdt.NewTreePos(p2Node.ID(), p2Node.ID())
		_, _, err = tree.EditT(3, 5, nil, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p>abcd</p></root>", tree.ToXML())

		// 03. An insert boundary resolves into the merge target before the
		// moved children, while a range boundary resolves right after the
		// merge-source tombstone, leaving the moved children outside.
		insertParent, insertLeft, _, err := tree.FindTreeNodesWithSplitText(pos, helper.TimeT(ctx))
		assert.NoError(t, err)
		idx, err := tree.ToIndex(insertParent, insertLeft)
		assert.NoError(t, err)
		assert.Equal(t, 3, idx)

		rangeParent, rangeLeft, _, err := tree.FindTreeNodesWithSplitText(
			pos, helper.TimeT(ctx), crdt.BoundaryRange)
		assert.NoError(t, err)
		assert.True(t, rangeLeft.IsRemoved())
		idx, err = tree.ToIndex(rangeParent, rangeLeft)
		assert.NoError(t, err)
		assert.Equal(t, 6, idx)
	})

	t.Run("stamps an insert declared inside a merged-away parent", func(t *testing.T) {
		// 01. Create <root><p>ab</p><p>cd</p></root> and capture the
		// leftmost position inside the second paragraph before the merge.
		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "root", nil), helper.TimeT(ctx))
		_, _, err := tree.EditT(0, 0, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(1, 1, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "ab"),
		}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		p2Node := crdt.NewTreeNode(helper.PosT(ctx), "p", nil)
		_, _, err = tree.EditT(4, 4, []*crdt.TreeNode{p2Node}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(5, 5, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "cd"),
		}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		pos := crdt.NewTreePos(p2Node.ID(), p2Node.ID())

		// 02. Merge the second paragraph into the first, then apply an
		// insert that still declares the merged-away paragraph as parent.
		// A later delete LWW-overwrites the tombstone first, so the merge
		// ticket is only recoverable from the moved sibling.
		mergeTicket := helper.TimeT(ctx)
		_, _, err = tree.EditT(3, 5, nil, 0, mergeTicket, issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p>abcd</p></root>", tree.ToXML())
		p2Node.SetRemovedAt(helper.TimeT(ctx))
		content := crdt.NewTreeNode(helper.PosT(ctx), "b", nil)
		_, _, _, err = tree.Edit(pos, pos, []*crdt.TreeNode{content}, 0,
			helper.TimeT(ctx), issueTicket(ctx), nil)
		assert.NoError(t, err)

		// 03. The content lands in the merge target but is stamped as
		// merged-from the declared parent, like a merge-moved child,
		// carrying the moved sibling's merge ticket.
		assert.NotNil(t, content.MergedFrom)
		assert.True(t, content.MergedFrom.Equal(p2Node.ID()))
		assert.Equal(t, mergeTicket, content.MergedAt)
	})

	t.Run("delete nodes between element nodes in different levels test", func(t *testing.T) {
		// 01. Create a tree with 2 paragraphs.
		//       0   1   2 3 4    5    6   7 8 9    10
		// <root> <p> <b> a b </b> </p> <p> c d </p>  </root>

		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "root", nil), helper.TimeT(ctx))
		_, _, err := tree.EditT(0, 0, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(1, 1, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "b", nil)}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(2, 2, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "ab"),
		}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(6, 6, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(7, 7, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "cd"),
		}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p><b>ab</b></p><p>cd</p></root>", tree.ToXML())

		// 02. delete b, c and the second paragraph.
		//       0   1   2 3 4    5
		// <root> <p> <b> a d </b> </root>
		_, _, err = tree.EditT(3, 8, nil, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p><b>ad</b></p></root>", tree.ToXML())
	})

	t.Run("style node with element attributes test", func(t *testing.T) {
		// 01. style attributes to an element node.
		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "root", nil), helper.TimeT(ctx))
		_, _, err := tree.EditT(0, 0, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(1, 1, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "ab"),
		}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(4, 4, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "p", nil),
		}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(5, 5, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "cd"),
		}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
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

		_, _, err := tree.EditT(0, 0, []*crdt.TreeNode{pNode}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(1, 1, []*crdt.TreeNode{textNode}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>ab</p></r>", tree.ToXML())

		// Find the closest index.TreePos when leftSiblingNode in crdt.TreePos is removed.
		//       0   1    2
		// <root> <p> </p> </root>
		_, _, err = tree.EditT(1, 3, nil, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p></p></r>", tree.ToXML())

		treePos := crdt.NewTreePos(pNode.ID(), textNode.ID())

		parent, leftSibling, _, err := tree.FindTreeNodesWithSplitText(treePos, helper.TimeT(ctx))
		assert.NoError(t, err)
		idx, err := tree.ToIndex(parent, leftSibling)
		assert.NoError(t, err)
		assert.Equal(t, 1, idx)

		// Find the closest index.TreePos when parentNode in crdt.TreePos is removed.
		//       0
		// <root> </root>
		_, _, err = tree.EditT(0, 2, nil, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r></r>", tree.ToXML())

		treePos = crdt.NewTreePos(pNode.ID(), textNode.ID())
		parent, leftSibling, _, err = tree.FindTreeNodesWithSplitText(treePos, helper.TimeT(ctx))
		assert.NoError(t, err)
		idx, err = tree.ToIndex(parent, leftSibling)
		assert.NoError(t, err)
		assert.Equal(t, 0, idx)
	})

	t.Run("length update after GC test", func(t *testing.T) {
		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)
		//       0
		// <root> </root>
		tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "r", nil), helper.TimeT(ctx))
		assert.Equal(t, 0, tree.Root().Len())
		assert.Equal(t, "<r></r>", tree.ToXML())

		//       0   1 2 3    4   5 6 7    8   9 10 11    12
		// <root> <b> a b </b> <i> c d </i> <a> e  f  </a>  </root>
		pNode1 := crdt.NewTreeNode(helper.PosT(ctx), "b", nil)
		textNode1 := crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "ab")
		pNode2 := crdt.NewTreeNode(helper.PosT(ctx), "i", nil)
		textNode2 := crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "cd")
		pNode3 := crdt.NewTreeNode(helper.PosT(ctx), "a", nil)
		textNode3 := crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "ef")

		_, _, err := tree.EditT(0, 0, []*crdt.TreeNode{pNode1}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(1, 1, []*crdt.TreeNode{textNode1}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(4, 4, []*crdt.TreeNode{pNode2}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(5, 5, []*crdt.TreeNode{textNode2}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(8, 8, []*crdt.TreeNode{pNode3}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(9, 9, []*crdt.TreeNode{textNode3}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)

		assert.Equal(t, "<r><b>ab</b><i>cd</i><a>ef</a></r>", tree.ToXML())
		assert.Equal(t, tree.Root().Index.VisibleLength, 12)
		assert.Equal(t, tree.Root().Index.TotalLength, 12)

		//       0   1 2 3    4   5 6 7    8
		// <root> <b> a b </b> <a> e f </a> </root>
		gcpairs, _, err := tree.EditT(4, 8, nil, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		registerGCPairs(root, gcpairs)

		assert.Equal(t, 2, root.GarbageLen())
		assert.Equal(t, "<r><b>ab</b><a>ef</a></r>", tree.ToXML())
		assert.Equal(t, tree.Root().Index.VisibleLength, 8)
		assert.Equal(t, tree.Root().Index.TotalLength, 12)

		n, err := root.GarbageCollect(helper.MaxVersionVector())
		assert.NoError(t, err)
		assert.Equal(t, 2, n)
		assert.Equal(t, 0, root.GarbageLen())

		assert.Equal(t, "<r><b>ab</b><a>ef</a></r>", tree.ToXML())
		assert.Equal(t, tree.Root().Index.VisibleLength, 8)
		assert.Equal(t, tree.Root().Index.TotalLength, 8)

		gcpairs, _, err = tree.EditT(5, 7, nil, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		registerGCPairs(root, gcpairs)

		assert.Equal(t, 1, root.GarbageLen())
		assert.Equal(t, "<r><b>ab</b><a></a></r>", tree.ToXML())
		assert.Equal(t, tree.Root().Index.VisibleLength, 6)
		assert.Equal(t, tree.Root().Index.TotalLength, 8)
	})

	t.Run("marshal test", func(t *testing.T) {
		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "root", nil), helper.TimeT(ctx))
		_, _, err := tree.EditT(0, 0, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "p", nil),
		}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(1, 1, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, `"Hello" \n i'm yorkie!`),
		}, 0, helper.TimeT(ctx), issueTicket(ctx))
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
		_, _, err := tree.EditT(0, 0, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(1, 1, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "helloworld"),
		}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>helloworld</p></r>", tree.ToXML())
		assert.Equal(t, 12, tree.Root().Len())
		assert.Equal(t, tree.ToTreeNodeForTest(), expectedInitial)

		// 01. Split left side of 'helloworld'.
		_, _, err = tree.EditT(1, 1, nil, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, tree.ToTreeNodeForTest(), expectedInitial)

		// 02. Split right side of 'helloworld'.
		_, _, err = tree.EditT(11, 11, nil, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, tree.ToTreeNodeForTest(), expectedInitial)

		// 03. Split 'helloworld' into 'hello' and 'world'.
		_, _, err = tree.EditT(6, 6, nil, 0, helper.TimeT(ctx), issueTicket(ctx))
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
		_, _, err := tree.EditT(0, 0, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(1, 1, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "ab"),
		}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>ab</p></r>", tree.ToXML())
		assert.Equal(t, 4, tree.Root().Len())
		_, _, err = tree.EditT(1, 1, nil, 1, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p></p><p>ab</p></r>", tree.ToXML())
		assert.Equal(t, 6, tree.Root().Len())

		// 02. Split position 2.
		tree = crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "r", nil), helper.TimeT(ctx))
		_, _, err = tree.EditT(0, 0, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "p", nil),
		}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(1, 1, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "ab"),
		}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>ab</p></r>", tree.ToXML())
		assert.Equal(t, 4, tree.Root().Len())
		_, _, err = tree.EditT(2, 2, nil, 1, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>a</p><p>b</p></r>", tree.ToXML())
		assert.Equal(t, 6, tree.Root().Len())

		// 03. Split position 3.
		tree = crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "r", nil), helper.TimeT(ctx))
		_, _, err = tree.EditT(0, 0, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "p", nil),
		}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(1, 1, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "ab"),
		}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>ab</p></r>", tree.ToXML())
		assert.Equal(t, 4, tree.Root().Len())
		_, _, err = tree.EditT(3, 3, nil, 1, helper.TimeT(ctx), issueTicket(ctx))
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
		_, _, err := tree.EditT(0, 0, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(1, 1, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "b", nil)}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(2, 2, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "ab"),
		}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p><b>ab</b></p></r>", tree.ToXML())
		assert.Equal(t, 6, tree.Root().Len())
		_, _, err = tree.EditT(3, 3, nil, 1, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p><b>a</b><b>b</b></p></r>", tree.ToXML())
		assert.Equal(t, 8, tree.Root().Len())

		// 02. Split nodes level 2.
		tree = crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "r", nil), helper.TimeT(ctx))
		_, _, err = tree.EditT(0, 0, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(1, 1, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "b", nil)}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(2, 2, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "ab"),
		}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p><b>ab</b></p></r>", tree.ToXML())
		assert.Equal(t, 6, tree.Root().Len())
		_, _, err = tree.EditT(3, 3, nil, 2, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p><b>a</b></p><p><b>b</b></p></r>", tree.ToXML())
		assert.Equal(t, 10, tree.Root().Len())
	})

	t.Run("split and merge element nodes by edit", func(t *testing.T) {
		ctx := helper.TextChangeContext(helper.TestRoot())

		tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "r", nil), helper.TimeT(ctx))
		_, _, err := tree.EditT(0, 0, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(1, 1, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "abcd"),
		}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>abcd</p></r>", tree.ToXML())

		//       0   1 2 3    4   5 6 7    8
		// <root> <p> a b </p> <p> c d </p> </root>
		_, _, err = tree.EditT(3, 3, nil, 1, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>ab</p><p>cd</p></r>", tree.ToXML())
		assert.Equal(t, 8, tree.Root().Len())

		_, _, err = tree.EditT(3, 5, nil, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>abcd</p></r>", tree.ToXML())
		assert.Equal(t, 6, tree.Root().Len())
	})
}

func TestTreeMerge(t *testing.T) {
	t.Run("delete nodes in a multi-level range test", func(t *testing.T) {
		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "root", nil), helper.TimeT(ctx))
		_, _, err := tree.EditT(0, 0, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(1, 1, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "ab"),
		}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(3, 3, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(4, 4, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "x"),
		}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(7, 7, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(8, 8, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(9, 9, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "cd"),
		}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(13, 13, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(14, 14, []*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)}, 0,
			helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(15, 15, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "y"),
		}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(17, 17, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "ef"),
		}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p>ab<p>x</p></p><p><p>cd</p></p><p><p>y</p>ef</p></root>", tree.ToXML())

		_, _, err = tree.EditT(2, 18, nil, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p>af</p></root>", tree.ToXML())
	})

	// Regression for the MergedAt immutability invariant: the merge
	// ticket recorded on a moved child must reflect the merge operation
	// itself, not the source parent's removedAt (which is mutable under
	// LWW and can be overwritten by a later concurrent tombstone).
	t.Run("MergedAt is captured at merge time, independent of source removedAt", func(t *testing.T) {
		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "root", nil), helper.TimeT(ctx))

		// Build <root><p>a</p><p>b</p></root>.
		_, _, err := tree.EditT(0, 0,
			[]*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)},
			0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(1, 1,
			[]*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "a")},
			0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(3, 3,
			[]*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "p", nil)},
			0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		_, _, err = tree.EditT(4, 4,
			[]*crdt.TreeNode{crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "b")},
			0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p>a</p><p>b</p></root>", tree.ToXML())

		// Capture the merge ticket explicitly so we can assert on it later.
		mergeTicket := helper.TimeT(ctx)
		_, _, err = tree.EditT(2, 4, nil, 0, mergeTicket, issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<root><p>ab</p></root>", tree.ToXML())

		// Locate the moved child (text node "b") now living under the
		// first <p>. It carries MergedFrom pointing at the tombstoned
		// second <p>, and MergedAt must equal the merge ticket.
		var moved *crdt.TreeNode
		for _, child := range tree.Root().Children(true) {
			for _, grand := range child.Children(true) {
				if grand.MergedFrom != nil {
					moved = grand
					break
				}
			}
		}
		assert.NotNil(t, moved, "moved child with MergedFrom should exist")
		assert.NotNil(t, moved.MergedAt, "MergedAt should be set at merge time")
		assert.True(t, moved.MergedAt.Compare(mergeTicket) == 0,
			"MergedAt should equal the exact merge ticket (immutable witness)")
	})
}

func issueTicket(change *change.Context) func() *time.Ticket {
	return func() *time.Ticket {
		return helper.TimeT(change)
	}
}

func TestTreeEditReturnsRemoved(t *testing.T) {
	// Deleting a subtree must report the nodes it newly tombstoned, and a
	// descendant that was ALREADY tombstoned before this edit must land only
	// in the pre-tombstoned set — never in the removed contents — so a
	// later undo never resurrects a deletion the user already made
	// independently of this edit.
	ctx := helper.TextChangeContext(helper.TestRoot())
	tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "r", nil), helper.TimeT(ctx))

	_, _, err := tree.EditT(0, 0, []*crdt.TreeNode{
		crdt.NewTreeNode(helper.PosT(ctx), "p", nil),
	}, 0, helper.TimeT(ctx), issueTicket(ctx))
	require.NoError(t, err)
	_, _, err = tree.EditT(1, 1, []*crdt.TreeNode{
		crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "abcde"),
	}, 0, helper.TimeT(ctx), issueTicket(ctx))
	require.NoError(t, err)
	require.Equal(t, "<r><p>abcde</p></r>", tree.ToXML())

	// Delete "bc" on its own first: this is the pre-existing tombstone the
	// later whole-subtree delete must not resurrect. Call Edit directly (not
	// EditT) so the "bc" node can be identified from its own return value,
	// rather than reached through package-internal lookups.
	fromBC, err := tree.FindPos(2)
	require.NoError(t, err)
	toBC, err := tree.FindPos(4)
	require.NoError(t, err)
	_, _, bcInfo, err := tree.Edit(
		fromBC, toBC, nil, 0, helper.TimeT(ctx), issueTicket(ctx), nil,
	)
	require.NoError(t, err)
	require.Empty(t, bcInfo.PreTombstoned)
	require.Len(t, bcInfo.Removed, 1)
	bcNode := bcInfo.Removed[0]
	require.Equal(t, "bc", bcNode.String())
	require.Equal(t, "<r><p>ade</p></r>", tree.ToXML())

	// Now delete the whole <p> subtree: the live "a" and "de" pieces plus
	// the element boundary are newly tombstoned by THIS edit; the
	// already-tombstoned "bc" piece must be excluded from removed and
	// appear only in preTombstoned.
	fromP, err := tree.FindPos(0)
	require.NoError(t, err)
	toP, err := tree.FindPos(5)
	require.NoError(t, err)
	outerAt := helper.TimeT(ctx)
	_, _, info, err := tree.Edit(
		fromP, toP, nil, 0, outerAt, issueTicket(ctx), nil,
	)
	removed, preTombstoned := info.Removed, info.PreTombstoned
	require.NoError(t, err)
	require.Equal(t, "<r></r>", tree.ToXML())

	// preTombstoned names exactly the pre-existing "bc" tombstone.
	require.Len(t, preTombstoned, 1)
	_, ok := preTombstoned[bcNode.IDString()]
	require.True(t, ok, "the pre-existing tombstone must be recorded by identity")

	// removed names exactly the <p> element and the two live text pieces
	// this edit itself transitioned to tombstoned — assert identity, not
	// just count: every entry must actually be tombstoned by outerAt, and
	// none may be the pre-existing "bc" tombstone.
	require.Len(t, removed, 3)
	var sawP, sawA, sawDE bool
	for _, node := range removed {
		require.NotEqual(t, bcNode.IDString(), node.IDString(),
			"an already-tombstoned descendant must not appear in removed")
		require.NotNil(t, node.RemovedAt())
		require.Zero(t, node.RemovedAt().Compare(outerAt),
			"every removed entry must be tombstoned by this edit's own ticket")
		switch {
		case node.Type() == "p":
			sawP = true
		case node.String() == "a":
			sawA = true
		case node.String() == "de":
			sawDE = true
		}
	}
	assert.True(t, sawP, "removed must name the <p> element")
	assert.True(t, sawA, "removed must name the live \"a\" text piece")
	assert.True(t, sawDE, "removed must name the live \"de\" text piece")
}

func TestTreeStyleReturnsPrevAttr(t *testing.T) {
	// Styling a range that already carries one attribute, with a call that
	// touches that attribute plus a brand-new one, must report the prior
	// value for the existing key and Existed: false for the new one, sorted
	// by key so the result is deterministic regardless of map order.
	ctx := helper.TextChangeContext(helper.TestRoot())
	tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "root", nil), helper.TimeT(ctx))
	_, _, err := tree.EditT(0, 0, []*crdt.TreeNode{
		crdt.NewTreeNode(helper.PosT(ctx), "p", nil),
	}, 0, helper.TimeT(ctx), issueTicket(ctx))
	require.NoError(t, err)
	_, _, err = tree.EditT(1, 1, []*crdt.TreeNode{
		crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "ab"),
	}, 0, helper.TimeT(ctx), issueTicket(ctx))
	require.NoError(t, err)

	fromPos, err := tree.FindPos(0)
	require.NoError(t, err)
	toPos, err := tree.FindPos(1)
	require.NoError(t, err)

	_, _, _, err = tree.Style(fromPos, toPos, map[string]string{"bold": "1"}, helper.TimeT(ctx), nil)
	require.NoError(t, err)

	_, _, prevAttrs, err := tree.Style(
		fromPos, toPos, map[string]string{"bold": "2", "italic": "1"}, helper.TimeT(ctx), nil,
	)
	require.NoError(t, err)
	require.Equal(t, []crdt.PrevAttr{
		{Key: "bold", Value: "1", Existed: true},
		{Key: "italic", Existed: false},
	}, prevAttrs)

	// Assert identity: the captured "before" value must match what the
	// element actually held immediately before this call, and the element's
	// current state must match what this call just set.
	pNode := tree.Root().Children()[0]
	require.Equal(t, "2", pNode.Attrs.Get("bold"))
	require.Equal(t, "1", pNode.Attrs.Get("italic"))
}

func TestTreeRemoveStyleReturnsPrevAttr(t *testing.T) {
	// RemoveStyle must report the value each removed key held immediately
	// before the call, omitting any key that was already absent, sorted by
	// key regardless of the order attributesToRemove was given in.
	ctx := helper.TextChangeContext(helper.TestRoot())
	tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "root", nil), helper.TimeT(ctx))
	_, _, err := tree.EditT(0, 0, []*crdt.TreeNode{
		crdt.NewTreeNode(helper.PosT(ctx), "p", nil),
	}, 0, helper.TimeT(ctx), issueTicket(ctx))
	require.NoError(t, err)
	_, _, err = tree.EditT(1, 1, []*crdt.TreeNode{
		crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "ab"),
	}, 0, helper.TimeT(ctx), issueTicket(ctx))
	require.NoError(t, err)

	fromPos, err := tree.FindPos(0)
	require.NoError(t, err)
	toPos, err := tree.FindPos(1)
	require.NoError(t, err)

	_, _, _, err = tree.Style(fromPos, toPos, map[string]string{"bold": "1"}, helper.TimeT(ctx), nil)
	require.NoError(t, err)

	_, _, prevAttrs, err := tree.RemoveStyle(fromPos, toPos, []string{"italic", "bold"}, helper.TimeT(ctx), nil)
	require.NoError(t, err)
	require.Equal(t, []crdt.PrevAttr{
		{Key: "bold", Value: "1", Existed: true},
	}, prevAttrs)

	pNode := tree.Root().Children()[0]
	require.False(t, pNode.Attrs.Has("bold"), "the removed key must actually be gone")
}

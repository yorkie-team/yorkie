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
	initialTreePos = &crdt.TreePos{
		CreatedAt: time.InitialTicket,
		Offset:    0,
	}
)

func TestTreeNode(t *testing.T) {
	t.Run("inline node test", func(t *testing.T) {
		node := crdt.NewTreeNode(initialTreePos, "text", "hello")
		assert.Equal(t, initialTreePos, node.Pos)
		assert.Equal(t, "text", node.Type())
		assert.Equal(t, "hello", node.Value)
		assert.Equal(t, 5, node.Len())
		assert.Equal(t, true, node.IsInline())
		assert.Equal(t, false, node.IsRemoved())
	})

	t.Run("block node test", func(t *testing.T) {
		para := crdt.NewTreeNode(initialTreePos, "p")
		assert.NoError(t, para.Append(crdt.NewTreeNode(initialTreePos, "text", "helloyorkie")))
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
	t.Run("insert nodes with edit test", func(t *testing.T) {
		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)
		//       0
		// <root> </root>
		tree := crdt.NewTree(crdt.NewTreeNode(helper.IssuePos(ctx), "r"), helper.IssueTime(ctx))
		assert.Equal(t, 0, tree.GetRoot().Len())
		assert.Equal(t, "<r></r>", tree.ToXML())
		helper.ListEqual(t, tree, []string{"r"})

		//           1
		// <root> <p> </p> </root>
		tree.EditByOffset(0, 0, crdt.NewTreeNode(helper.IssuePos(ctx), "p"), helper.IssueTime(ctx))
		assert.Equal(t, "<r><p></p></r>", tree.ToXML())
		helper.ListEqual(t, tree, []string{"p", "r"})
		assert.Equal(t, 2, tree.GetRoot().Len())

		//           1
		// <root> <p> h e l l o </p> </root>
		tree.EditByOffset(
			1, 1,
			crdt.NewTreeNode(helper.IssuePos(ctx), "text", "hello"),
			helper.IssueTime(ctx),
		)
		assert.Equal(t, "<r><p>hello</p></r>", tree.ToXML())
		helper.ListEqual(t, tree, []string{"text.hello", "p", "r"})
		assert.Equal(t, 7, tree.GetRoot().Len())

		//       0   1 2 3 4 5 6    7   8 9  10 11 12 13    14
		// <root> <p> h e l l o </p> <p> w  o  r  l  d  </p>  </root>
		p := crdt.NewTreeNode(helper.IssuePos(ctx), "p")
		p.InsertAt(crdt.NewTreeNode(helper.IssuePos(ctx), "text", "world"), 0)
		tree.EditByOffset(7, 7, p, helper.IssueTime(ctx))
		assert.Equal(t, "<r><p>hello</p><p>world</p></r>", tree.ToXML())
		helper.ListEqual(t, tree, []string{"text.hello", "p", "text.world", "p", "r"})
		assert.Equal(t, 14, tree.GetRoot().Len())

		//       0   1 2 3 4 5 6 7    8   9 10 11 12 13 14    15
		// <root> <p> h e l l o ! </p> <p> w  o  r  l  d  </p>  </root>
		tree.EditByOffset(
			6, 6,
			crdt.NewTreeNode(helper.IssuePos(ctx), "text", "!"),
			helper.IssueTime(ctx),
		)
		assert.Equal(t, "<r><p>hello!</p><p>world</p></r>", tree.ToXML())
		helper.ListEqual(t, tree, []string{"text.hello", "text.!", "p", "text.world", "p", "r"})
		// TODO(krapie): add tree JSON deep equal

		//       0   1 2 3 4 5 6 7 8    9   10 11 12 13 14 15    16
		// <root> <p> h e l l o ~ ! </p> <p>  w  o  r  l  d  </p>  </root>
		tree.EditByOffset(
			6, 6,
			crdt.NewTreeNode(helper.IssuePos(ctx), "text", "~"),
			helper.IssueTime(ctx),
		)
		assert.Equal(t, "<r><p>hello~!</p><p>world</p></r>", tree.ToXML())
		helper.ListEqual(t, tree, []string{"text.hello", "text.~", "text.!", "p", "text.world", "p", "r"})
	})
}

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

package crdt_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestTreeEditAtElementTextBoundary checks that the index right after an
// element and right before a text sibling (index 8 below) resolves to the
// start of that text.
//
// FindPos used to anchor that index on the text itself at offset 0. A text
// left sibling means "after its first N characters", and with N = 0 there is
// nothing to split off, so the whole text became the left sibling and the edit
// landed after it. FindPos now anchors on what precedes the text instead.
func TestTreeEditAtElementTextBoundary(t *testing.T) {
	t.Run("insert lands before the text", func(t *testing.T) {
		tree, ctx := newBoldHelloWorldTree(t)

		editT(t, tree, ctx, 8, 8, textNode(ctx, "|"))

		assert.Equal(t, "<r><p><b>hello</b>| world</p></r>", tree.ToXML())
	})

	t.Run("delete from the boundary removes the text", func(t *testing.T) {
		tree, ctx := newBoldHelloWorldTree(t)

		editT(t, tree, ctx, 8, 14, nil)

		assert.Equal(t, "<r><p><b>hello</b></p></r>", tree.ToXML())
	})

	t.Run("a tombstone between the element and the text is skipped", func(t *testing.T) {
		// <r><p><b>hello</b>[x removed] world</p></r>: the previous visible
		// sibling of " world" is still <b>.
		tree, ctx := newBoldHelloWorldTree(t)
		editT(t, tree, ctx, 8, 8, textNode(ctx, "x"))
		editT(t, tree, ctx, 8, 9, nil)
		require.Equal(t, "<r><p><b>hello</b> world</p></r>", tree.ToXML())

		editT(t, tree, ctx, 8, 8, textNode(ctx, "|"))

		assert.Equal(t, "<r><p><b>hello</b>| world</p></r>", tree.ToXML())
	})

	t.Run("a deleted head of the same text is skipped like a separate tombstone", func(t *testing.T) {
		// Deleting " w" splits " world" into a tombstoned head and a live
		// "orld" whose InsPrev is that head. The insert still anchors on <b>,
		// before the tombstone, exactly as it does when the tombstone is a
		// separate node (the case above) and as Text does for its own
		// tombstones: which kind of tombstone sits there is not visible to
		// the user, so it must not change where the edit lands.
		tree, ctx := newBoldHelloWorldTree(t)
		editT(t, tree, ctx, 8, 10, nil)
		require.Equal(t, `<r><p><b>hello</b>orld</p></r>`, tree.ToXML())

		editT(t, tree, ctx, 8, 8, textNode(ctx, "|"))

		assert.Equal(t, "<r><p><b>hello</b>|orld</p></r>", tree.ToXML())
		assert.Equal(t, []string{"b", `"|"`, `~" w"~`, `"orld"`}, childrenOfP(tree))

		separate, ctx := newBoldHelloWorldTree(t)
		editT(t, separate, ctx, 8, 8, textNode(ctx, "x"))
		editT(t, separate, ctx, 8, 9, nil)
		editT(t, separate, ctx, 8, 8, textNode(ctx, "|"))
		assert.Equal(t, []string{"b", `"|"`, `~"x"~`, `" world"`}, childrenOfP(separate))
	})

	t.Run("a text that is the first visible child anchors on the parent", func(t *testing.T) {
		// <r><p>[<b>hello</b> removed] world</p></r>: " world" is now the
		// first visible child even though a tombstone precedes it.
		tree, ctx := newBoldHelloWorldTree(t)
		editT(t, tree, ctx, 1, 8, nil)
		require.Equal(t, "<r><p> world</p></r>", tree.ToXML())

		pos, err := tree.FindPos(1)
		require.NoError(t, err)
		assert.True(t, pos.LeftSiblingID.Equal(pos.ParentID))

		editT(t, tree, ctx, 1, 1, textNode(ctx, "|"))
		assert.Equal(t, "<r><p>| world</p></r>", tree.ToXML())
	})

	t.Run("a split element before the text is the anchor itself", func(t *testing.T) {
		// Splitting <b> at "hel|lo" gives the right half an InsPrev, the
		// shape ToTreeNodes redirects for text halves. It must not redirect
		// here: element ids always carry offset 0, so the anchor stays on
		// the right half instead of moving one sibling to the left.
		//
		//	    0   1   2 3 4 5    6   7 8 9    10 11 ...
		//	<r> <p> <b> h e l </b> <b> l o </b>    w ...
		tree, ctx := newBoldHelloWorldTree(t)
		_, _, err := tree.EditT(5, 5, nil, 1, helper.TimeT(ctx), issueTicket(ctx))
		require.NoError(t, err)
		require.Equal(t, "<r><p><b>hel</b><b>lo</b> world</p></r>", tree.ToXML())

		rightHalf := tree.Root().Index.Children()[0].Children()[1].Value
		require.NotNil(t, rightHalf.InsPrevID)
		pos, err := tree.FindPos(10)
		require.NoError(t, err)
		assert.True(t, pos.LeftSiblingID.Equal(rightHalf.ID()))

		editT(t, tree, ctx, 10, 10, textNode(ctx, "|"))
		assert.Equal(t, "<r><p><b>hel</b><b>lo</b>| world</p></r>", tree.ToXML())
	})

	t.Run("a boundary between two texts resolves to the end of the left one", func(t *testing.T) {
		// FindPos relies on this: a text reached at offset 0 never has a text
		// right before it, so the previous visible sibling is an element.
		tree, ctx := newBoldHelloWorldTree(t)
		editT(t, tree, ctx, 14, 14, textNode(ctx, "!"))
		require.Equal(t, "<r><p><b>hello</b> world!</p></r>", tree.ToXML())

		world := tree.Root().Index.Children()[0].Children()[1].Value
		require.Equal(t, " world", world.Value)
		pos, err := tree.FindPos(14)
		require.NoError(t, err)
		assert.Equal(t, 0, pos.LeftSiblingID.CreatedAt.Compare(world.ID().CreatedAt))
		assert.Equal(t, world.ID().Offset+len(" world"), pos.LeftSiblingID.Offset)
	})

	t.Run("splitting at the boundary moves the text to the new paragraph", func(t *testing.T) {
		tree, ctx := newBoldHelloWorldTree(t)

		_, _, err := tree.EditT(8, 8, nil, 1, helper.TimeT(ctx), issueTicket(ctx))
		require.NoError(t, err)

		assert.Equal(t, "<r><p><b>hello</b></p><p> world</p></r>", tree.ToXML())
	})
}

// childrenOfP lists the children of the first <p>, tombstones included, as
// `b`, `"text"` or `~"removed text"~`.
func childrenOfP(tree *crdt.Tree) []string {
	var children []string
	for _, child := range tree.Root().Index.Children()[0].Children(true) {
		node := child.Value
		name := node.Type()
		if node.IsText() {
			name = `"` + node.Value + `"`
		}
		if node.IsRemoved() {
			name = "~" + name + "~"
		}
		children = append(children, name)
	}
	return children
}

// newBoldHelloWorldTree builds the tree below and returns it with its context.
//
//	    0   1   2 3 4 5 6 7    8 9 10 11 12 13 14    15
//	<r> <p> <b> h e l l o </b>   w  o  r  l  d   </p>  </r>
func newBoldHelloWorldTree(t *testing.T) (*crdt.Tree, *change.Context) {
	t.Helper()

	ctx := helper.TextChangeContext(helper.TestRoot())
	tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "r", nil), helper.TimeT(ctx))

	for _, step := range []struct {
		index int
		node  *crdt.TreeNode
	}{
		{0, crdt.NewTreeNode(helper.PosT(ctx), "p", nil)},
		{1, crdt.NewTreeNode(helper.PosT(ctx), "b", nil)},
		{2, textNode(ctx, "hello")},
		{8, textNode(ctx, " world")}, // <p> has no text after </b> yet.
	} {
		editT(t, tree, ctx, step.index, step.index, step.node)
	}
	require.Equal(t, "<r><p><b>hello</b> world</p></r>", tree.ToXML())

	return tree, ctx
}

// editT replaces [from, to) with node, or deletes it when node is nil.
func editT(t *testing.T, tree *crdt.Tree, ctx *change.Context, from, to int, node *crdt.TreeNode) {
	t.Helper()

	var contents []*crdt.TreeNode
	if node != nil {
		contents = []*crdt.TreeNode{node}
	}
	_, _, err := tree.EditT(from, to, contents, 0, helper.TimeT(ctx), issueTicket(ctx))
	require.NoError(t, err)
}

func textNode(ctx *change.Context, value string) *crdt.TreeNode {
	return crdt.NewTreeNode(helper.PosT(ctx), "text", nil, value)
}

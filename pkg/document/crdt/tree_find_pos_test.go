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

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

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestFindTreeNodesWithSplitTextSplitOffset pins every side of step 03's
// range guard: an offset inside the floor piece still splits it; one past its
// end that a purge of the pieces between explains -- a later piece of the
// insertion survives past it -- anchors after the piece; and any other offset
// past the piece still fails with ErrSplitOutOfRange, a purge of the
// insertion's tail included, since nothing tells that purge from an offset
// the insertion never had.
func TestFindTreeNodesWithSplitTextSplitOffset(t *testing.T) {
	t.Run("an offset inside the floor piece splits it", func(t *testing.T) {
		tree, ctx := newBoldHelloWorldTree(t)
		world := tree.Root().Index.Children()[0].Children()[1].Value
		require.Equal(t, " world", world.Value)

		// " world" is one piece; offset 3 names the boundary inside it.
		pos := crdt.NewTreePos(
			tree.Root().Index.Children()[0].Value.ID(),
			crdt.NewTreeNodeID(world.ID().CreatedAt, world.ID().Offset+3),
		)
		parent, left, _, err := tree.FindTreeNodesWithSplitText(pos, helper.TimeT(ctx))
		require.NoError(t, err)
		assert.Equal(t, "p", parent.Type())
		assert.Equal(t, " wo", left.Value, "the piece is split at the offset")
		assert.Equal(t, []string{"b", `" wo"`, `"rld"`}, childrenOfP(tree))
	})

	t.Run("an offset past purged pieces anchors after the floor piece", func(t *testing.T) {
		// Delete "or" and purge it, leaving " w" and "ld" as the surviving
		// pieces of the insertion while positions a peer declared still name
		// offsets inside the range that is gone.
		tree, ctx := newBoldHelloWorldTree(t)
		world := tree.Root().Index.Children()[0].Children()[1].Value
		createdAt, baseOffset := world.ID().CreatedAt, world.ID().Offset
		editT(t, tree, ctx, 10, 12, nil)
		require.Equal(t, "<r><p><b>hello</b> wld</p></r>", tree.ToXML())

		gap := tree.Root().Index.Children()[0].Children(true)[2].Value
		require.True(t, gap.IsRemoved())
		require.NoError(t, tree.Purge(gap))
		require.Equal(t, []string{"b", `" w"`, `"ld"`}, childrenOfP(tree))

		// Offset 3 is right after the purged "o": past the end of the " w"
		// piece the lookup now answers with, before the surviving "ld".
		pos := crdt.NewTreePos(
			tree.Root().Index.Children()[0].Value.ID(),
			crdt.NewTreeNodeID(createdAt, baseOffset+3),
		)
		parent, left, _, err := tree.FindTreeNodesWithSplitText(pos, helper.TimeT(ctx))
		require.NoError(t, err, "a position after purged pieces is not out of range")
		assert.Equal(t, "p", parent.Type())
		assert.Equal(t, " w", left.Value, "it anchors after the surviving piece")
		assert.Equal(t, []string{"b", `" w"`, `"ld"`}, childrenOfP(tree), "nothing was split")
	})

	t.Run("a gap the chain does not link both ways is out of range", func(t *testing.T) {
		// The same purge of "or", but the surviving "ld" no longer points back
		// at " w": the gap is not one Purge left between two neighbours.
		tree, ctx := newBoldHelloWorldTree(t)
		world := tree.Root().Index.Children()[0].Children()[1].Value
		createdAt, baseOffset := world.ID().CreatedAt, world.ID().Offset
		editT(t, tree, ctx, 10, 12, nil)
		pieces := tree.Root().Index.Children()[0].Children(true)
		gap, tail := pieces[2].Value, pieces[3].Value
		require.NoError(t, tree.Purge(gap))
		tail.InsPrevID = nil

		pos := crdt.NewTreePos(
			tree.Root().Index.Children()[0].Value.ID(),
			crdt.NewTreeNodeID(createdAt, baseOffset+3),
		)
		_, _, _, err := tree.FindTreeNodesWithSplitText(pos, helper.TimeT(ctx))
		assert.ErrorIs(t, err, crdt.ErrSplitOutOfRange)
	})

	t.Run("an offset past the last surviving piece is out of range", func(t *testing.T) {
		tree, ctx := newBoldHelloWorldTree(t)
		world := tree.Root().Index.Children()[0].Children()[1].Value
		createdAt, baseOffset := world.ID().CreatedAt, world.ID().Offset
		p := tree.Root().Index.Children()[0].Value.ID()

		pos := crdt.NewTreePos(p, crdt.NewTreeNodeID(createdAt, baseOffset+9))
		_, _, _, err := tree.FindTreeNodesWithSplitText(pos, helper.TimeT(ctx))
		assert.ErrorIs(t, err, crdt.ErrSplitOutOfRange, "an offset the insertion never had")

		// Delete "rld" and purge it: the tail is gone with nothing after it,
		// so an offset inside it reads exactly like one the insertion never
		// had.
		editT(t, tree, ctx, 11, 14, nil)
		tail := tree.Root().Index.Children()[0].Children(true)[2].Value
		require.True(t, tail.IsRemoved())
		require.NoError(t, tree.Purge(tail))

		pos = crdt.NewTreePos(p, crdt.NewTreeNodeID(createdAt, baseOffset+5))
		_, _, _, err = tree.FindTreeNodesWithSplitText(pos, helper.TimeT(ctx))
		assert.ErrorIs(t, err, crdt.ErrSplitOutOfRange, "a purged tail is not told apart")
	})
}

// TestFindTreeNodesWithSplitTextSplitProductByMode pins that step 04 passes
// over a split product of the left element only for an insert position. An
// insert after the element lands between it and the product (§1.3, §7.4). A
// range boundary after the element -- a style range's, or the end of a
// non-empty Edit range -- was drawn after the whole element its client saw,
// unsplit, so it goes past the product as the walk always has.
func TestFindTreeNodesWithSplitTextSplitProductByMode(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode crdt.PosBoundary
		want string
	}{
		{"an insert stays before the product", crdt.BoundaryInsert, "left"},
		{"a range boundary goes past it", crdt.BoundaryRange, "product"},
		{"an edit's range end goes past it", crdt.BoundaryEditEnd, "product"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := helper.TextChangeContext(helper.TestRoot())
			tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "r", nil), helper.TimeT(ctx))
			editT(t, tree, ctx, 0, 0, crdt.NewTreeNode(helper.PosT(ctx), "p", nil))
			editT(t, tree, ctx, 1, 1, textNode(ctx, "ab"))
			left := tree.Root().Children()[0]

			// The position after <p>ab</p> is declared before a concurrent
			// split cuts the paragraph in two.
			editedAt := helper.TimeT(ctx)
			pos := crdt.NewTreePos(tree.Root().ID(), left.ID())
			_, _, err := tree.EditT(2, 2, nil, 1, helper.TimeT(ctx), issueTicket(ctx))
			require.NoError(t, err)
			require.Equal(t, "<r><p>a</p><p>b</p></r>", tree.ToXML())
			product := tree.Root().Children()[1]

			_, anchor, _, err := tree.FindTreeNodesWithSplitText(pos, editedAt, tc.mode)
			require.NoError(t, err)
			want := map[string]*crdt.TreeNode{"left": left, "product": product}[tc.want]
			assert.Same(t, want, anchor)
		})
	}
}

// TestTreeEditRangeStartsAfterAConcurrentlySplitElement pins the Edit end of
// §1.3: a non-empty range is not an insert position at either end. A delete
// of the second paragraph, declared from right after the first one, meets a
// concurrent split of that first paragraph. Its start passes over the split
// product, as the walk always has, so the product -- content the deleting
// client saw inside the first paragraph -- stays out of the range.
func TestTreeEditRangeStartsAfterAConcurrentlySplitElement(t *testing.T) {
	ctx := helper.TextChangeContext(helper.TestRoot())
	tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "r", nil), helper.TimeT(ctx))
	editT(t, tree, ctx, 0, 0, crdt.NewTreeNode(helper.PosT(ctx), "p", nil))
	editT(t, tree, ctx, 1, 1, textNode(ctx, "ab"))
	editT(t, tree, ctx, 4, 4, crdt.NewTreeNode(helper.PosT(ctx), "p", nil))
	editT(t, tree, ctx, 5, 5, textNode(ctx, "cd"))
	require.Equal(t, "<r><p>ab</p><p>cd</p></r>", tree.ToXML())

	// <r>0<p>1ab3</p>4<p>5cd7</p>8</r>: the delete covers [4, 8).
	editedAt := helper.TimeT(ctx)
	from, err := tree.FindPos(4)
	require.NoError(t, err)
	to, err := tree.FindPos(8)
	require.NoError(t, err)

	_, _, err = tree.EditT(2, 2, nil, 1, helper.TimeT(ctx), issueTicket(ctx))
	require.NoError(t, err)
	require.Equal(t, "<r><p>a</p><p>b</p><p>cd</p></r>", tree.ToXML())

	_, _, _, err = tree.Edit(from, to, nil, 0, editedAt, issueTicket(ctx), nil, false)
	require.NoError(t, err)
	assert.Equal(t, "<r><p>a</p><p>b</p></r>", tree.ToXML(),
		"the split product is not inside the range")
}

// TestFindTreeNodesWithSplitTextManySplitProducts runs step 04 past a long
// run of split products of the left element, each split off the previous one
// at one anchor, as repeated Enters by a peer leave them. Each product is a
// sibling the walk passes and a link of the chain every other product's
// lineage goes through; the memoized test answers the whole run in one pass
// over the chain instead of one chain walk per product.
func TestFindTreeNodesWithSplitTextManySplitProducts(t *testing.T) {
	const products = 2000

	ctx := helper.TextChangeContext(helper.TestRoot())
	tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "r", nil), helper.TimeT(ctx))
	editT(t, tree, ctx, 0, 0, crdt.NewTreeNode(helper.PosT(ctx), "p", nil))
	editT(t, tree, ctx, 1, 1, textNode(ctx, "ab"))
	left := tree.Root().Children()[0]
	editedAt := helper.TimeT(ctx)
	pos := crdt.NewTreePos(tree.Root().ID(), left.ID())

	// The first Enter splits <p>ab</p> after "a"; each one after it splits
	// the newest product right before "b", leaving an empty product behind,
	// so every product is split off the one before it: <r>0<p>1a2</p>3, then
	// one empty <p></p> per earlier product, then <p>b</p>.
	for k := range products {
		at := 2
		if k > 0 {
			at = 3 + 2*(k-1) + 1
		}
		_, _, err := tree.EditT(at, at, nil, 1, helper.TimeT(ctx), issueTicket(ctx))
		require.NoError(t, err)
	}
	last := tree.Root().Children()[products]
	require.Equal(t, "<p>b</p>", crdt.ToXML(last))
	require.Len(t, tree.Root().Children(), products+1)

	_, anchor, _, err := tree.FindTreeNodesWithSplitText(pos, editedAt, crdt.BoundaryInsert)
	require.NoError(t, err)
	assert.Same(t, left, anchor, "every product is passed over")
}

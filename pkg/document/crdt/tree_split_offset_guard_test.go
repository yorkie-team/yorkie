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

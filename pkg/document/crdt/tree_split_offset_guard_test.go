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

// TestFindTreeNodesWithSplitTextSplitOffset pins both sides of step 03's
// range guard: an offset inside the floor piece still splits it, and one past
// its end -- which only a purge of the pieces between can produce -- anchors
// after the piece instead of failing with ErrSplitOutOfRange.
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

	t.Run("an offset past the floor piece anchors after it", func(t *testing.T) {
		// Delete "rld" and purge it, leaving " wo" as the floor piece of the
		// insertion while positions a peer declared still name offsets inside
		// the range that is gone.
		tree, ctx := newBoldHelloWorldTree(t)
		world := tree.Root().Index.Children()[0].Children()[1].Value
		createdAt, baseOffset := world.ID().CreatedAt, world.ID().Offset
		editT(t, tree, ctx, 11, 14, nil)
		require.Equal(t, "<r><p><b>hello</b> wo</p></r>", tree.ToXML())

		tail := tree.Root().Index.Children()[0].Children(true)[2].Value
		require.True(t, tail.IsRemoved())
		require.NoError(t, tree.Purge(tail))
		require.Equal(t, []string{"b", `" wo"`}, childrenOfP(tree))

		// Offset 5 is the last character of the purged tail: past the end of
		// the " wo" piece the floor lookup now answers with.
		pos := crdt.NewTreePos(
			tree.Root().Index.Children()[0].Value.ID(),
			crdt.NewTreeNodeID(createdAt, baseOffset+5),
		)
		parent, left, _, err := tree.FindTreeNodesWithSplitText(pos, helper.TimeT(ctx))
		require.NoError(t, err, "a position after purged pieces is not out of range")
		assert.Equal(t, "p", parent.Type())
		assert.Equal(t, " wo", left.Value, "it anchors after the surviving piece")
		assert.Equal(t, []string{"b", `" wo"`}, childrenOfP(tree), "nothing was split")
	})
}

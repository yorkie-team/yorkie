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
)

// TestRestoreRelinksRecreatedTextPiece pins the InsPrevID/InsNextID chain a
// recreated text piece is spliced back into. Purge unlinks the piece and
// joins its neighbours to each other; a recreated piece left out of the chain
// is invisible to the walks that follow it, and the next split of a neighbour
// hands the right half an InsNextID that skips it.
func TestRestoreRelinksRecreatedTextPiece(t *testing.T) {
	tree, ctx := newBoldHelloWorldTree(t)

	// Delete the "r" out of the middle of " world", splitting the insertion
	// into three pieces so the tombstone in the middle has a neighbour on
	// BOTH sides of the chain.
	editT(t, tree, ctx, 11, 12, nil)
	require.Equal(t, []string{"b", `" wo"`, `~"r"~`, `"ld"`}, childrenOfP(tree))

	pieces := tree.Root().Index.Children()[0].Children(true)
	head, gap, tail := pieces[1].Value, pieces[2].Value, pieces[3].Value
	require.Equal(t, " wo", head.Value)
	require.Equal(t, "r", gap.Value)
	require.Equal(t, "ld", tail.Value)
	gapID := gap.ID()

	// Purge the tombstone: the chain closes over the gap.
	require.NoError(t, tree.Purge(gap))
	require.True(t, head.InsNextID.Equal(tail.ID()), "purge joined the neighbours")
	require.True(t, tail.InsPrevID.Equal(head.ID()), "purge joined the neighbours")

	// An undo of the second delete recreates the purged piece by identity.
	p := tree.Root().Index.Children()[0].Value
	span := &crdt.TreeRestoreSpan{
		ID:            gapID,
		NodeType:      "text",
		IsText:        true,
		Length:        1,
		Value:         "r",
		ParentID:      p.ID(),
		LeftSiblingID: crdt.NewTreeNodeID(head.ID().CreatedAt, head.ID().Offset+head.Length()-1),
	}
	_, recreated, _, _, _, err := tree.Restore([]*crdt.TreeRestoreSpan{span}, false)
	require.NoError(t, err)
	require.Len(t, recreated, 1)
	assert.Equal(t, []string{"b", `" wo"`, `"r"`, `"ld"`}, childrenOfP(tree))

	back := recreated[0]
	require.NotNil(t, back.InsPrevID)
	require.NotNil(t, back.InsNextID)
	assert.True(t, back.InsPrevID.Equal(head.ID()), "the piece points back at its predecessor")
	assert.True(t, back.InsNextID.Equal(tail.ID()), "and forward at its successor")
	require.NotNil(t, head.InsNextID)
	assert.True(t, head.InsNextID.Equal(gapID), "the predecessor no longer skips it")
	require.NotNil(t, tail.InsPrevID)
	assert.True(t, tail.InsPrevID.Equal(gapID), "and neither does the successor")
}

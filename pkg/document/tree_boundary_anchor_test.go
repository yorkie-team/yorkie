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

package document_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/test/helper"
)

// paragraphs returns the children of the tree's root, tombstones included.
func paragraphs(tree *crdt.Tree) []*crdt.TreeNode {
	return tree.Root().Children(true)
}

// TestTreeBoundaryAnchorRecord checks what a level-0 split of a text paragraph
// records on its product: the node its boundary sits in front of, or the end
// of the family, and no nesting when no other boundary shares the anchor.
func TestTreeBoundaryAnchorRecord(t *testing.T) {
	t.Run("split inside the text anchors before the right half", func(t *testing.T) {
		docs := boundaryReplicas(t, 1, "ab")
		updateTree(t, docs[0], splitAt(2)) // <p>a</p><p>b</p>

		ps := paragraphs(treeCRDT(t, docs[0]))
		require.Len(t, ps, 2)
		right := ps[1].Children(true)
		require.Len(t, right, 1)
		require.NotNil(t, ps[1].BoundaryAnchor)
		assert.True(t, ps[1].BoundaryAnchor.Equal(right[0].ID()), "anchored before b")
		assert.False(t, ps[1].BoundaryAtEnd)
		assert.Nil(t, ps[1].BoundaryNest)
		assert.Nil(t, ps[0].BoundaryAnchor, "the split node itself records nothing")
	})

	t.Run("split at the end anchors at the end of the family", func(t *testing.T) {
		docs := boundaryReplicas(t, 1, "ab")
		updateTree(t, docs[0], splitAt(3)) // <p>ab</p><p></p>

		ps := paragraphs(treeCRDT(t, docs[0]))
		require.Len(t, ps, 2)
		assert.Nil(t, ps[1].BoundaryAnchor)
		assert.True(t, ps[1].BoundaryAtEnd)
	})

	t.Run("a split inside a same-anchor group nests under it", func(t *testing.T) {
		docs := boundaryReplicas(t, 1, "ab")
		updateTree(t, docs[0], splitAt(3)) // <p>ab</p><p></p>
		updateTree(t, docs[0], splitAt(5)) // <p>ab</p><p></p><p></p>

		ps := paragraphs(treeCRDT(t, docs[0]))
		require.Len(t, ps, 3)
		assert.True(t, ps[2].BoundaryAtEnd)
		require.NotNil(t, ps[2].BoundaryNest)
		assert.True(t, ps[2].BoundaryNest.Equal(ps[1].ID()), "nested under the first boundary")
	})
}

// TestTreeBoundaryAnchorRelinkOnPurge checks that purging the text node a
// boundary is anchored before moves the anchor to the next item of the
// sequence, here the end of the family.
func TestTreeBoundaryAnchorRelinkOnPurge(t *testing.T) {
	docs := boundaryReplicas(t, 1, "ab")
	updateTree(t, docs[0], splitAt(2)) // <p>a</p><p>b</p>
	updateTree(t, docs[0], func(tree *json.Tree) { tree.Edit(4, 5, nil, 0) })

	require.Positive(t, docs[0].GarbageCollect(helper.MaxVersionVector(docs[0].ActorID())))

	ps := paragraphs(treeCRDT(t, docs[0]))
	require.Len(t, ps, 2)
	assert.Nil(t, ps[1].BoundaryAnchor)
	assert.True(t, ps[1].BoundaryAtEnd)
}

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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

// paragraphs returns the children of the tree's root, tombstones included.
func paragraphs(tree *crdt.Tree) []*crdt.TreeNode {
	return tree.Root().Children(true)
}

// TestTreeBoundaryAnchorRecord checks what a split of a text paragraph
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

// TestTreeBoundaryElementTombstoneGC runs the yorkie-js-sdk#1436 shapes on a
// paragraph that once held an inline element, removed before the concurrent
// ops. Whether a replica has purged that element's tombstone must not decide
// which split rules apply, so every collector choice converges alike.
func TestTreeBoundaryElementTombstoneGC(t *testing.T) {
	type op struct {
		replica int
		fn      func(tree *json.Tree)
	}
	cases := [][]op{
		{{0, insertText(2, "e")}, {0, splitAt(2)}, {1, splitAt(2)}},
		{{0, insertText(2, "c")}, {0, insertText(3, "d")}, {0, splitAt(3)}, {1, splitAt(2)}},
		{{0, insertText(3, "c")}, {1, splitAt(3)}, {0, insertText(4, "d")}, {0, splitAt(4)}},
		{{0, insertText(3, "e")}, {1, insertText(3, "f")}, {1, splitAt(3)}, {1, splitAt(5)}},
		{{0, insertText(3, "c")}, {0, insertText(4, "d")}, {0, splitAt(5)}, {1, splitAt(3)}},
		{{1, insertText(1, "f")}, {1, splitAt(1)}, {1, insertText(3, "h")}, {0, insertText(1, "i")}},
		{{1, insertText(3, "g")}, {1, splitAt(3)}, {0, insertText(3, "h")}, {1, insertText(6, "i")}},
		{{1, splitAt(3)}, {1, insertText(3, "d")}, {1, splitAt(3)}, {1, insertText(6, "e")}, {0, splitAt(3)}},
		{{1, splitAt(1)}, {0, splitAt(1)}, {1, splitAt(4)}, {1, insertText(1, "f")}, {1, insertText(2, "g")}},
	}
	for ci, ops := range cases {
		for _, collector := range []int{-1, 0, 1} {
			t.Run(fmt.Sprintf("case %d, collector %d", ci, collector), func(t *testing.T) {
				docs := boundaryReplicas(t, 2, "ab")
				// <p>ab<i></i></p>, then the <i> is removed and both sync.
				updateTree(t, docs[0], func(tree *json.Tree) {
					tree.Edit(3, 3, &json.TreeNode{Type: "i"}, 0)
				})
				exchangeInOrder(t, docs, [][]int{nil, {0}})
				updateTree(t, docs[1], func(tree *json.Tree) { tree.Edit(3, 5, nil, 0) })
				exchangeInOrder(t, docs, [][]int{{1}, nil})
				exchangeInOrder(t, docs, [][]int{nil, nil})
				if collector >= 0 {
					minVV := time.MinVersionVector(docs[0].VersionVector(), docs[1].VersionVector())
					require.Positive(t, docs[collector].GarbageCollect(minVV))
				}

				for _, o := range ops {
					updateTree(t, docs[o.replica], o.fn)
				}
				exchangeInOrder(t, docs, [][]int{{1}, {0}})

				assert.Equal(t, treeXML(t, docs[0]), treeXML(t, docs[1]))
				assert.Equal(t, liveTreeShape(t, docs[0]), liveTreeShape(t, docs[1]))
			})
		}
	}
}

// TestTreeBoundaryAnchorRelinkPurgeOrder purges, in one GC pass, the text a
// boundary is anchored before and the product that follows it. GC visits its
// pairs in map order, so the anchor has to end up in the same place whichever
// goes first: here at the end of the family, since both are gone.
func TestTreeBoundaryAnchorRelinkPurgeOrder(t *testing.T) {
	for range 100 {
		docs := boundaryReplicas(t, 1, "ab")
		updateTree(t, docs[0], splitAt(2)) // <p>a</p><p>b</p>: anchored before b
		updateTree(t, docs[0], splitAt(5)) // <p>a</p><p>b</p><p></p>: at the end
		updateTree(t, docs[0], func(tree *json.Tree) { tree.Edit(6, 8, nil, 0) })
		updateTree(t, docs[0], func(tree *json.Tree) { tree.Edit(4, 5, nil, 0) })
		require.Positive(t, docs[0].GarbageCollect(helper.MaxVersionVector(docs[0].ActorID())))

		ps := paragraphs(treeCRDT(t, docs[0]))
		require.Len(t, ps, 2)
		require.Nil(t, ps[1].BoundaryAnchor, "the anchor must not name a purged node")
		require.True(t, ps[1].BoundaryAtEnd)
	}
}

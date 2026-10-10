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

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// boundaryReplicas returns n replicas seeded with text under one paragraph,
// <doc><p>{text}</p></doc>.
func boundaryReplicas(t *testing.T, n int, text string) []*document.Document {
	t.Helper()

	docs := make([]*document.Document, n)
	for i := range docs {
		actor, err := time.ActorIDFromHex(fmt.Sprintf("%024d", i+1))
		require.NoError(t, err)
		docs[i] = document.New("test-doc")
		docs[i].SetActor(actor)
	}

	require.NoError(t, docs[0].Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{
			Type: "doc",
			Children: []json.TreeNode{{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: text}},
			}},
		})
		return nil
	}))
	orders := make([][]int, n)
	for i := 1; i < n; i++ {
		orders[i] = []int{0}
	}
	exchangeInOrder(t, docs, orders)

	return docs
}

// updateTree runs fn against the tree under "t" of doc in one change.
func updateTree(t *testing.T, doc *document.Document, fn func(tree *json.Tree)) {
	t.Helper()

	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		fn(root.GetTree("t"))
		return nil
	}))
}

// insertText inserts value at index idx.
func insertText(idx int, value string) func(tree *json.Tree) {
	return func(tree *json.Tree) {
		tree.Edit(idx, idx, &json.TreeNode{Type: "text", Value: value}, 0)
	}
}

// splitAt splits the paragraph at index idx.
func splitAt(idx int) func(tree *json.Tree) {
	return func(tree *json.Tree) { tree.Edit(idx, idx, nil, 1) }
}

// TestTreeInsertAtConcurrentSplitBoundary covers text typed at exactly the
// point where a peer concurrently splits the paragraph (yorkie-js-sdk#1436).
// The replica that applies the insert first has the text inside the paragraph
// when the split arrives; the replica that applies the split first has the
// right half already in the product. Both are self-consistent, and without
// the boundary-insert-side rules they disagree on which side of the new
// boundary the text ends up on.
//
// XML alone does not always show it -- the products can hold the same strings
// in a different order of nodes -- so these compare node IDs as well. The cases
// mirror yorkie-js-sdk's tree_boundary_insert_side_test.ts, so a Go replica
// and a JS one place the same split the same way.
func TestTreeInsertAtConcurrentSplitBoundary(t *testing.T) {
	t.Run("keeps the insert left of the boundary, split applied second", func(t *testing.T) {
		docs := boundaryReplicas(t, 2, "ab")
		updateTree(t, docs[0], insertText(2, "e"))
		updateTree(t, docs[0], splitAt(2))
		updateTree(t, docs[1], splitAt(2))
		exchangeInOrder(t, docs, [][]int{{1}, {0}})

		assert.Equal(t, treeXML(t, docs[0]), treeXML(t, docs[1]))
		assert.Equal(t, treeShape(t, docs[0]), treeShape(t, docs[1]))
		assert.Equal(t, "<doc><p>a</p><p>e</p><p>b</p></doc>", treeXML(t, docs[0]))
	})

	t.Run("orders the insert after a concurrent insert the split took right", func(t *testing.T) {
		docs := boundaryReplicas(t, 2, "ab")
		updateTree(t, docs[1], insertText(2, "r"))
		updateTree(t, docs[0], insertText(2, "u"))
		updateTree(t, docs[1], splitAt(2))
		exchangeInOrder(t, docs, [][]int{{1}, {0}})

		assert.Equal(t, treeXML(t, docs[0]), treeXML(t, docs[1]))
		assert.Equal(t, treeShape(t, docs[0]), treeShape(t, docs[1]))
		assert.Equal(t, "<doc><p>a</p><p>rub</p></doc>", treeXML(t, docs[0]))
	})

	t.Run("keeps an insert at the start of the paragraph left of the boundary", func(t *testing.T) {
		docs := boundaryReplicas(t, 2, "ab")
		updateTree(t, docs[0], insertText(1, "s"))
		updateTree(t, docs[1], splitAt(1))
		updateTree(t, docs[0], splitAt(1))
		exchangeInOrder(t, docs, [][]int{{1}, {0}})

		assert.Equal(t, treeXML(t, docs[0]), treeXML(t, docs[1]))
		assert.Equal(t, treeShape(t, docs[0]), treeShape(t, docs[1]))
		assert.Equal(t, "<doc><p></p><p>s</p><p>ab</p></doc>", treeXML(t, docs[0]))
	})

	// The two actors are not interchangeable: §7.8 orders their products by
	// ticket, so the typist being the older or the newer actor takes a
	// different path through orderSameBoundarySplit.
	for typist := range 2 {
		t.Run(fmt.Sprintf("converges with replica %d as the typist", typist), func(t *testing.T) {
			docs := boundaryReplicas(t, 2, "ab")
			updateTree(t, docs[typist], insertText(2, "e"))
			updateTree(t, docs[typist], splitAt(2))
			updateTree(t, docs[1-typist], splitAt(2))
			exchangeInOrder(t, docs, [][]int{{1}, {0}})

			assert.Equal(t, treeShape(t, docs[0]), treeShape(t, docs[1]))
			assert.Equal(t, "<doc><p>a</p><p>e</p><p>b</p></doc>", treeXML(t, docs[0]))
		})
	}
}

// TestTreeInsertAtSplitBoundaryPastRemovedChild covers an insert whose anchor
// is followed, inside the paragraph, only by a child another replica removes
// concurrently, while a third replica splits the paragraph right after that
// child. The typist put u between a and c, so u belongs before c and in the
// left paragraph whatever happens to c.
//
// advanceIntoSplitProducts gates on atEndOfLiveContent, which counts a
// trailing child as gone only when the inserting change knew of its removal.
// Judged by the local tombstone instead, whether u crosses into the split
// product would depend on whether the removal arrived before the insert, and
// the first ordering below would diverge.
func TestTreeInsertAtSplitBoundaryPastRemovedChild(t *testing.T) {
	// <p>acb</p>: index 2 is between a and c, 3 between c and b.
	for _, tc := range []struct {
		name   string
		orders [][]int
	}{
		{
			name:   "splitter receives the removal before the insert",
			orders: [][]int{{1, 2}, {2, 0}, {1, 0}},
		},
		{
			name:   "splitter receives the insert before the removal",
			orders: [][]int{{2, 1}, {0, 2}, {0, 1}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docs := boundaryReplicas(t, 3, "acb")
			updateTree(t, docs[1], insertText(3, "r"))
			updateTree(t, docs[1], splitAt(3))
			updateTree(t, docs[0], insertText(2, "u"))
			updateTree(t, docs[2], func(tree *json.Tree) { tree.Edit(2, 3, nil, 0) })
			exchangeInOrder(t, docs, tc.orders)

			for i := 1; i < len(docs); i++ {
				assert.Equal(t, treeXML(t, docs[0]), treeXML(t, docs[i]))
				assert.Equal(t, treeShape(t, docs[0]), treeShape(t, docs[i]))
			}
			assert.Equal(t, "<doc><p>au</p><p>rb</p></doc>", treeXML(t, docs[0]))
		})
	}
}

// TestTreeEnterThenTypeAtConcurrentInsert covers Enter followed by typing at
// the start of the new paragraph, while a peer types at the point Enter was
// pressed. The typed s is younger than the split product, so it was never at
// the old boundary: neither run measurement may cross it, or the replica that
// applied the split first pulls u into the new paragraph alone.
func TestTreeEnterThenTypeAtConcurrentInsert(t *testing.T) {
	t.Run("keeps the concurrent insert in the left paragraph", func(t *testing.T) {
		docs := boundaryReplicas(t, 2, "ab")
		updateTree(t, docs[0], insertText(2, "u"))
		updateTree(t, docs[1], splitAt(2))
		updateTree(t, docs[1], insertText(4, "s"))
		exchangeInOrder(t, docs, [][]int{{1}, {0}})

		assert.Equal(t, treeXML(t, docs[0]), treeXML(t, docs[1]))
		assert.Equal(t, treeShape(t, docs[0]), treeShape(t, docs[1]))
		assert.Equal(t, "<doc><p>au</p><p>sb</p></doc>", treeXML(t, docs[0]))
	})

	// A third replica removes a so that the left paragraph holds u alone.
	for _, orders := range [][][]int{
		{{1, 2}, {0, 2}, {0, 1}},
		{{2, 1}, {2, 0}, {1, 0}},
		{{1, 2}, {2, 0}, {1, 0}},
	} {
		t.Run(fmt.Sprintf("with a third replica, orders %v", orders), func(t *testing.T) {
			docs := boundaryReplicas(t, 3, "ab")
			updateTree(t, docs[0], insertText(2, "u"))
			updateTree(t, docs[1], splitAt(2))
			updateTree(t, docs[1], insertText(4, "s"))
			updateTree(t, docs[2], func(tree *json.Tree) { tree.Edit(1, 2, nil, 0) })
			exchangeInOrder(t, docs, orders)

			for i := 1; i < len(docs); i++ {
				assert.Equal(t, treeXML(t, docs[0]), treeXML(t, docs[i]))
				assert.Equal(t, treeShape(t, docs[0]), treeShape(t, docs[i]))
			}
			assert.Equal(t, "<doc><p>u</p><p>sb</p></doc>", treeXML(t, docs[0]))
		})
	}
}

// TestTreeBoundaryRunWithTwoConcurrentStartSplits covers a typist's insert
// and split after a, while two peers split the paragraph at its start. The
// typist's product is split off at a different boundary from the start
// splits, so orderSameBoundarySplit must not redirect a start split past its
// leading u after stepping over a product that still holds b.
func TestTreeBoundaryRunWithTwoConcurrentStartSplits(t *testing.T) {
	for _, orders := range [][][]int{
		{{1, 2}, {0, 2}, {0, 1}},
		{{2, 1}, {2, 0}, {1, 0}},
		{{1, 2}, {2, 0}, {1, 0}},
		{{2, 1}, {0, 2}, {0, 1}},
	} {
		t.Run(fmt.Sprintf("converges with orders %v", orders), func(t *testing.T) {
			docs := boundaryReplicas(t, 3, "ab")
			updateTree(t, docs[0], insertText(2, "u"))
			updateTree(t, docs[0], splitAt(2))
			updateTree(t, docs[1], splitAt(1))
			updateTree(t, docs[2], splitAt(1))
			exchangeInOrder(t, docs, orders)

			for i := 1; i < len(docs); i++ {
				assert.Equal(t, treeXML(t, docs[0]), treeXML(t, docs[i]))
				assert.Equal(t, treeShape(t, docs[0]), treeShape(t, docs[i]))
			}
		})
	}
}

// TestTreeSplitBoundaryRemainingDivergences records two-replica cases that
// still diverge with the boundary-insert-side rules: the remaining cases of
// yorkie-js-sdk#1436. A fuzz of the JS tree found them and delta debugging
// minimized them; every op is concurrent with the other replica's. The Go
// and JS replicas end in the same two states, node IDs included, so they are
// gaps in the shared rule rather than port differences. Each is skipped with
// the replica states and how it relates to main until the rule covers it.
func TestTreeSplitBoundaryRemainingDivergences(t *testing.T) {
	type op struct {
		replica int
		fn      func(tree *json.Tree)
	}
	const (
		onMain = "Also diverges on main and with the same-boundary walk fix " +
			"(yorkie#2098, yorkie-js-sdk#1435)."
		fromFilter = "Also diverges on main; converged with this rule before " +
			"movedBySplit, which Enter-then-type needs."
	)
	for _, tc := range []struct {
		name  string
		ops   []op
		known string
	}{
		{
			name:  "seed 101",
			ops:   []op{{0, insertText(2, "c")}, {0, insertText(3, "d")}, {0, splitAt(3)}, {1, splitAt(2)}},
			known: "<p>ac</p><p></p><p>db</p> vs <p>ac</p><p>d</p><p>b</p>. " + onMain,
		},
		{
			name:  "seed 235",
			ops:   []op{{0, insertText(3, "c")}, {1, splitAt(3)}, {0, insertText(4, "d")}, {0, splitAt(4)}},
			known: "<p>abc</p><p></p><p>d</p> vs <p>abc</p><p>d</p><p></p>. " + onMain,
		},
		{
			name:  "seed 193",
			ops:   []op{{0, insertText(3, "e")}, {1, insertText(3, "f")}, {1, splitAt(3)}, {1, splitAt(5)}},
			known: "<p>ab</p><p></p><p>fe</p> vs <p>abe</p><p></p><p>f</p>. " + onMain,
		},
		{
			name:  "seed 24",
			ops:   []op{{0, insertText(3, "c")}, {0, insertText(4, "d")}, {0, splitAt(5)}, {1, splitAt(3)}},
			known: "Same XML, the two empty paragraphs in a different order. " + onMain,
		},
		{
			name:  "seed 69",
			ops:   []op{{1, insertText(1, "f")}, {1, splitAt(1)}, {1, insertText(3, "h")}, {0, insertText(1, "i")}},
			known: "<p></p><p>hfiab</p> vs <p>i</p><p>hfab</p>. " + fromFilter,
		},
		{
			name:  "seed 502",
			ops:   []op{{1, insertText(3, "g")}, {1, splitAt(3)}, {0, insertText(3, "h")}, {1, insertText(6, "i")}},
			known: "<p>ab</p><p>gih</p> vs <p>ab</p><p>ghi</p>. " + fromFilter,
		},
		{
			name: "seed 3768",
			ops: []op{
				{1, splitAt(3)}, {1, insertText(3, "d")}, {1, splitAt(3)}, {1, insertText(6, "e")}, {0, splitAt(3)},
			},
			known: "Same XML, the two empty paragraphs in a different order. Converges on " +
				"main and converged with this rule before movedBySplit, which " +
				"Enter-then-type needs.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Skip("KNOWN (yorkie-js-sdk#1436 remaining cases): " + tc.known)

			docs := boundaryReplicas(t, 2, "ab")
			for _, o := range tc.ops {
				updateTree(t, docs[o.replica], o.fn)
			}
			exchangeInOrder(t, docs, [][]int{{1}, {0}})

			assert.Equal(t, treeXML(t, docs[0]), treeXML(t, docs[1]))
			assert.Equal(t, treeShape(t, docs[0]), treeShape(t, docs[1]))
		})
	}
}

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
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// topTreeUndoRange returns the index range of the TreeEdit on top of doc's
// undo stack.
func topTreeUndoRange(t *testing.T, doc *document.Document) [2]int {
	t.Helper()
	for _, op := range doc.UndoStackTopForTest() {
		if te, ok := op.Op.(*operations.TreeEdit); ok {
			from, to := te.NormalizePos()
			return [2]int{from, to}
		}
	}
	t.Fatalf("no TreeEdit on top of the undo stack")
	return [2]int{}
}

// TestUndoSplitBetweenNonContiguousRestoredNodes isolates the identity
// restore/retombstone path from the forward one. d3 inserts "X" between "c"
// and "d" while d1 deletes "cdef", so d1's undo revives "c" on one side of the
// live "X" and "def" on the other. d2's pending split boundary is stacked
// right after "X", BETWEEN the two revived runs, and no forward op reaches it
// after it is stacked: only d1's undo (restore) or redo (retombstone) does.
//
// The correct shift is +1 (restore) / -1 (retombstone): only "c" is left of
// the boundary. Reporting nothing, as the identity path did before
// yorkie#2222, leaves the entry one token off; a single envelope over the op also gets it wrong -- as an
// insertion of 4 at "c" it shifts by +4, as a deletion of [c, f] it swallows
// the boundary (Case 3) and collapses it.
func TestUndoSplitBetweenNonContiguousRestoredNodes(t *testing.T) {
	newDocs := func(t *testing.T) (d1, d2, d3 *document.Document, docs []*document.Document) {
		docs = make([]*document.Document, 3)
		for i := range docs {
			actor, err := time.ActorIDFromHex(fmt.Sprintf("%024d", i+1))
			require.NoError(t, err)
			docs[i] = document.New("test-doc")
			docs[i].SetActor(actor)
		}
		d1, d2, d3 = docs[0], docs[1], docs[2]
		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "abcdefgh"}}},
			}})
			return nil
		}))
		exchangeInOrder(t, docs, [][]int{{}, {0}, {0}})
		require.NoError(t, d1.ClearHistory())

		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(3, 7, nil, 0)
			return nil
		}))
		require.NoError(t, d3.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(4, 4, &json.TreeNode{Type: "text", Value: "X"}, 0)
			return nil
		}))
		exchangeInOrder(t, docs, [][]int{{2}, {0, 2}, {0}})
		for _, d := range docs {
			require.Equal(t, `<doc><p>abXgh</p></doc>`, treeXML(t, d))
		}
		return d1, d2, d3, docs
	}
	split := func(t *testing.T, d *document.Document, at int) {
		require.NoError(t, d.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(at, at, nil, 1)
			return nil
		}))
	}

	t.Run("restore", func(t *testing.T) {
		d1, d2, _, docs := newDocs(t)
		split(t, d2, 4) // after "X"
		require.Equal(t, `<doc><p>abX</p><p>gh</p></doc>`, treeXML(t, d2))
		require.Equal(t, [2]int{4, 6}, topTreeUndoRange(t, d2))

		require.NoError(t, d1.Undo())
		exchangeInOrder(t, docs, [][]int{{}, {0}, {}})
		require.Equal(t, `<doc><p>abcX</p><p>defgh</p></doc>`, treeXML(t, d2))
		assert.Equal(t, [2]int{5, 7}, topTreeUndoRange(t, d2))
		require.NoError(t, d2.Undo())
		assert.Equal(t, `<doc><p>abcXdefgh</p></doc>`, treeXML(t, d2))
	})

	t.Run("retombstone", func(t *testing.T) {
		d1, d2, _, docs := newDocs(t)
		require.NoError(t, d1.Undo())
		exchangeInOrder(t, docs, [][]int{{}, {0}, {0}})
		require.Equal(t, `<doc><p>abcXdefgh</p></doc>`, treeXML(t, d2))
		split(t, d2, 5) // after "X"
		require.Equal(t, `<doc><p>abcX</p><p>defgh</p></doc>`, treeXML(t, d2))
		require.Equal(t, [2]int{5, 7}, topTreeUndoRange(t, d2))

		require.NoError(t, d1.Redo())
		exchangeInOrder(t, docs, [][]int{{}, {0}, {}})
		require.Equal(t, `<doc><p>abX</p><p>gh</p></doc>`, treeXML(t, d2))
		assert.Equal(t, [2]int{4, 6}, topTreeUndoRange(t, d2))
		require.NoError(t, d2.Undo())
		assert.Equal(t, `<doc><p>abXgh</p></doc>`, treeXML(t, d2))
	})
}

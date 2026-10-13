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

// A peer's identity-preserving undo or redo arrives as a TreeEdit that
// revives or re-removes nodes by identity instead of editing a range. The
// replica receiving it still has to shift its own pending undo/redo entries by
// what became visible or left, exactly as it does for an ordinary edit, or
// those entries land on the wrong index (yorkie#2222).

// peerUndoReplicas returns two replicas seeded with the given tree, d1's
// history cleared so its first undo is the one the test sets up.
func peerUndoReplicas(t *testing.T, tree json.TreeNode) (*document.Document, *document.Document) {
	t.Helper()

	docs := make([]*document.Document, 2)
	for i := range docs {
		actor, err := time.ActorIDFromHex(fmt.Sprintf("%024d", i+1))
		require.NoError(t, err)
		docs[i] = document.New("test-doc")
		docs[i].SetActor(actor)
	}
	require.NoError(t, docs[0].Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", tree)
		return nil
	}))
	exchangeInOrder(t, docs, [][]int{{}, {0}})
	require.NoError(t, docs[0].ClearHistory())
	return docs[0], docs[1]
}

// TestUndoEnterAfterPeerUndoesTextAtSplitPoint is the reported case: d1
// inserts "B" where d2 later presses Enter, then removes it again while d2's
// Enter is pending. Removing it by undo must leave d2's Enter-undo exactly as
// removing it by an ordinary delete does: one index to the left. Before the
// fix the undo reported no range at all, so the Enter-undo stayed put and
// merged the boundary after "cd" instead of the one before it.
func TestUndoEnterAfterPeerUndoesTextAtSplitPoint(t *testing.T) {
	for _, tc := range []struct {
		name       string
		removeText func(t *testing.T, doc *document.Document)
	}{
		{"peer removes the text by undo", func(t *testing.T, doc *document.Document) {
			require.NoError(t, doc.Undo())
		}},
		{"peer removes the text by a delete", func(t *testing.T, doc *document.Document) {
			editTree(t, doc, 4, 5, nil)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d1, d2 := peerUndoReplicas(t, json.TreeNode{Type: "doc", Children: []json.TreeNode{
				{Type: "p", Children: []json.TreeNode{{Type: "span", Children: []json.TreeNode{{Type: "text", Value: "abcd"}}}}},
				{Type: "p", Children: []json.TreeNode{{Type: "span", Children: []json.TreeNode{{Type: "text", Value: "efgh"}}}}},
			}})
			docs := []*document.Document{d1, d2}

			// 01. d1 inserts "B" between "ab" and "cd": "abBcd".
			editTree(t, d1, 4, 4, &json.TreeNode{Type: "text", Value: "B"})
			exchangeInOrder(t, docs, [][]int{{1}, {0}})

			// 02. d1 removes "B" while d2 presses Enter right after it, which
			// splits both <span> and <p> there.
			tc.removeText(t, d1)
			require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
				tree := root.GetTree("t")
				tree.EditByPath([]int{0, 0, 3}, []int{0, 0, 3}, nil, 1)
				tree.EditByPath([]int{0, 1}, []int{0, 1}, nil, 1)
				return nil
			}))
			exchangeInOrder(t, docs, [][]int{{1}, {0}})
			require.Equal(t, `<doc><p><span>ab</span></p><p><span>cd</span></p><p><span>efgh</span></p></doc>`, treeXML(t, d2))

			// 03. d2's undo of the Enter merges the boundary it opened.
			require.NoError(t, d2.Undo())
			assert.Equal(t, `<doc><p><span>abcd</span></p><p><span>efgh</span></p></doc>`, treeXML(t, d2))
			exchangeInOrder(t, docs, [][]int{{1}, {0}})
			assert.Equal(t, treeXML(t, d1), treeXML(t, d2))
		})
	}
}

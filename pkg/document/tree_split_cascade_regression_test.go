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

// Regression scenarios for the §4.1 split-sibling cascade, mirrored in
// yorkie-js-sdk's test/unit/document/tree_split_cascade_regression_test.ts.
// Each one loses text that nobody deleted if the cascade also runs when the
// delete lost the LWW of the element -- a tempting extra step for
// yorkie-js-sdk#1408, left out on purpose.

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// regressionFlush takes doc's pending changes through protobuf and acks them.
func regressionFlush(t *testing.T, doc *document.Document) *change.Pack {
	t.Helper()
	pb, err := converter.ToChangePack(doc.CreateChangePack())
	require.NoError(t, err)
	pack, err := converter.FromChangePack(pb)
	require.NoError(t, err)
	var lastSeq uint32
	if n := len(pack.Changes); n > 0 {
		lastSeq = pack.Changes[n-1].ClientSeq()
	}
	require.NoError(t, doc.ApplyChangePack(change.NewPack(
		pack.DocumentKey, change.NewCheckpoint(0, lastSeq), nil, time.InitialVersionVector, nil,
	)))
	return pack
}

func regressionReceive(t *testing.T, doc *document.Document, pack *change.Pack) {
	t.Helper()
	require.NoError(t, doc.ApplyChangePack(change.NewPack(
		pack.DocumentKey, change.NewCheckpoint(0, 0), pack.Changes, time.InitialVersionVector, nil,
	)))
}

func regressionEdit(t *testing.T, doc *document.Document, fn func(tree *json.Tree)) {
	t.Helper()
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		fn(root.GetTree("t"))
		return nil
	}))
}

// regressionStart returns n synced replicas holding
// <p><span>abc</span><span>de</span></p><p><span>fg</span></p>.
func regressionStart(t *testing.T, n int) []*document.Document {
	t.Helper()
	docs := make([]*document.Document, n)
	for i := range docs {
		actor, err := time.ActorIDFromHex(fmt.Sprintf("%024x", i+1))
		require.NoError(t, err)
		docs[i] = document.New("doc")
		docs[i].SetActor(actor)
	}
	require.NoError(t, docs[0].Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{
			Type: "doc",
			Children: []json.TreeNode{
				{Type: "p", Children: []json.TreeNode{
					{Type: "span", Children: []json.TreeNode{{Type: "text", Value: "abc"}}},
					{Type: "span", Children: []json.TreeNode{{Type: "text", Value: "de"}}},
				}},
				{Type: "p", Children: []json.TreeNode{
					{Type: "span", Children: []json.TreeNode{{Type: "text", Value: "fg"}}},
				}},
			},
		})
		return nil
	}))
	init := regressionFlush(t, docs[0])
	for _, d := range docs[1:] {
		regressionReceive(t, d, init)
	}
	return docs
}

var regressionTags = regexp.MustCompile(`<[^>]*>`)

func regressionText(t *testing.T, doc *document.Document) string {
	return regressionTags.ReplaceAllString(treeXML(t, doc), "")
}

func regressionEnterAtSpanStart(tree *json.Tree) {
	tree.EditByPath([]int{0, 1, 0}, []int{0, 1, 0}, nil, 1)
	tree.EditByPath([]int{0, 2}, []int{0, 2}, nil, 1)
	tree.EditByPath([]int{0, 1}, []int{0, 2}, nil, 0)
}

func regressionSplitAt0DropLeft(tree *json.Tree) {
	tree.EditByPath([]int{0, 1, 0}, []int{0, 1, 0}, nil, 1)
	tree.EditByPath([]int{0, 1}, []int{0, 2}, nil, 0)
}

// TestTreeSplitSiblingCascadeKeepsText checks that the cascade never takes
// text the deleter did not delete.
func TestTreeSplitSiblingCascadeKeepsText(t *testing.T) {
	// r1 deletes the styled span and undoes it; r2 (newer ticket) presses
	// Enter right before it. Both replicas keep "de". Cascading on a lost LWW
	// would let r1's delete tombstone r2's split product holding "de" with
	// r1's ticket; r1's undo restores only what r1 knew.
	for name, op := range map[string]func(*json.Tree){
		"Enter at span start":    regressionEnterAtSpanStart,
		"split at 0 + drop left": regressionSplitAt0DropLeft,
	} {
		t.Run("delete span + undo vs "+name, func(t *testing.T) {
			docs := regressionStart(t, 2)
			regressionEdit(t, docs[0], func(tree *json.Tree) { tree.EditByPath([]int{0, 1}, []int{0, 2}, nil, 0) })
			require.NoError(t, docs[0].Undo())
			regressionEdit(t, docs[1], op)
			p1, p2 := regressionFlush(t, docs[0]), regressionFlush(t, docs[1])
			regressionReceive(t, docs[1], p1)
			regressionReceive(t, docs[0], p2)
			assert.Contains(t, regressionText(t, docs[0]), "de", "d1 lost de: %s", treeXML(t, docs[0]))
			assert.Contains(t, regressionText(t, docs[1]), "de", "d2 lost de: %s", treeXML(t, docs[1]))
		})
	}

	// r1 does the #1408 shape; r2 (newer ticket) does the same and types "y"
	// at the start of its new span. Nobody deletes "de" or "y". Cascading on
	// a lost LWW would tombstone r2's product with "y" in it on r2 as well.
	// r1 does not show "y": on r1, r2's product is split off a span r1 has
	// already deleted, so it is born tombstoned. That is not the cascade.
	t.Run("Enter + type vs Enter keeps the typed y on the typist replica", func(t *testing.T) {
		docs := regressionStart(t, 2)
		regressionEdit(t, docs[0], regressionSplitAt0DropLeft)
		regressionEdit(t, docs[1], func(tree *json.Tree) {
			regressionSplitAt0DropLeft(tree)
			tree.EditByPath([]int{0, 1, 0}, []int{0, 1, 0}, &json.TreeNode{Type: "text", Value: "y"}, 0)
		})
		p1, p2 := regressionFlush(t, docs[0]), regressionFlush(t, docs[1])
		regressionReceive(t, docs[1], p1)
		regressionReceive(t, docs[0], p2)
		assert.Contains(t, regressionText(t, docs[0]), "de", "d1: %s", treeXML(t, docs[0]))
		assert.Contains(t, regressionText(t, docs[1]), "yde", "d2: %s", treeXML(t, docs[1]))
	})

	// No undo. r1 splits the second span in the middle with level 2 (Enter in
	// the middle of a styled run): <p>abc,d</p><p>e</p>. r2 concurrently
	// deletes "c" and "d" across the span boundary, merging the second span
	// into the first. r3 has seen r1 and does the #1408 shape on the "d"
	// span. Nobody deletes "e". Cascading on a lost LWW would let the
	// merge-turned-delete on r3 run through the whole chain, including r1's
	// level-2 product in the next paragraph that holds "e".
	t.Run("Enter (level 2) vs cross-span merge vs split-at-0+drop", func(t *testing.T) {
		for _, rev := range []bool{false, true} {
			docs := regressionStart(t, 3)
			regressionEdit(t, docs[0], func(tree *json.Tree) { tree.EditByPath([]int{0, 1, 1}, []int{0, 1, 1}, nil, 2) })
			p1 := regressionFlush(t, docs[0])
			regressionEdit(t, docs[1], func(tree *json.Tree) { tree.EditByPath([]int{0, 0, 2}, []int{0, 1, 1}, nil, 0) })
			p2 := regressionFlush(t, docs[1])
			regressionReceive(t, docs[2], p1)
			regressionEdit(t, docs[2], regressionSplitAt0DropLeft)
			p3 := regressionFlush(t, docs[2])
			if rev {
				regressionReceive(t, docs[0], p3)
				regressionReceive(t, docs[0], p2)
			} else {
				regressionReceive(t, docs[0], p2)
				regressionReceive(t, docs[0], p3)
			}
			regressionReceive(t, docs[1], p1)
			regressionReceive(t, docs[1], p3)
			regressionReceive(t, docs[2], p2)
			for i, d := range docs {
				assert.Contains(t, regressionText(t, d), "e", "d%d (rev=%v) lost e: %s", i+1, rev, treeXML(t, d))
			}
		}
	})

	// Same family, convergence: the replicas agree; cascading on a lost LWW
	// would leave r2 without "e".
	t.Run("delete span + undo vs split-at-0+drop vs split-in-middle+drop converge", func(t *testing.T) {
		docs := regressionStart(t, 3)
		regressionEdit(t, docs[0], func(tree *json.Tree) { tree.EditByPath([]int{0, 1}, []int{0, 2}, nil, 0) })
		require.NoError(t, docs[0].Undo())
		p1 := regressionFlush(t, docs[0])
		regressionEdit(t, docs[1], regressionSplitAt0DropLeft)
		p2 := regressionFlush(t, docs[1])
		regressionReceive(t, docs[2], p1)
		regressionEdit(t, docs[2], func(tree *json.Tree) {
			tree.EditByPath([]int{0, 1, 1}, []int{0, 1, 1}, nil, 1)
			tree.EditByPath([]int{0, 1}, []int{0, 2}, nil, 0)
		})
		p3 := regressionFlush(t, docs[2])
		regressionReceive(t, docs[0], p2)
		regressionReceive(t, docs[0], p3)
		regressionReceive(t, docs[1], p1)
		regressionReceive(t, docs[1], p3)
		regressionReceive(t, docs[2], p2)
		assert.Equal(t, treeXML(t, docs[0]), treeXML(t, docs[1]), "d2 diverged from d1")
		assert.Equal(t, treeXML(t, docs[0]), treeXML(t, docs[2]), "d3 diverged from d1")
	})
}

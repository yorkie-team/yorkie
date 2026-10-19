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
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
)

// TestTreeReviveDissolvesMergeSource checks that deleting an element an undo
// revived does not erase the text a concurrent merge had moved out of it
// (#2189).
//
// A merge source is a tombstone: §1.1's insert redirect, §6.2's propagation
// and resolveMergeTarget all read `mergedInto` only on a removed node. Before
// the fix a revive left that pointer on the element it made live again, and
// left the `MergedFrom`/`MergedAt` stamps on the children the merge had moved,
// so deleting the revived -- and now empty -- element followed the stale
// pointer and tombstoned text no edit of it ever covered. Go landed on
// <r><p>ab</p></r> where yorkie-js-sdk, which refuses a live source
// (yorkie-js-sdk#1406), lands on <r><p>abcd</p></r>.
func TestTreeReviveDissolvesMergeSource(t *testing.T) {
	docs := twoParagraphReplicas(t, 2)

	// Step 1: d0 deletes p2 whole (Edit(4, 8)) while d1 merges p2 into p1
	// (Edit(3, 5) removes p1's closing token and p2's opening token, moving cd
	// under p1). The two land apart, on the order each applied: d0 tombstoned
	// cd before the merge reached it, so the merge relocates a tombstone, while
	// on d1 the §6.2 skip keeps cd -- d0's own position named p2, so the
	// concurrent merge put the children where d0's edit would have. Pinned as
	// the starting state of step 2, not as an endorsement: the asymmetry is
	// pre-existing and outside #2189, and d0's own undo repairs it below.
	editRangeOnEach(t, docs, [][2]int{{4, 8}, {3, 5}})
	require.Equal(t, "<r><p>ab</p></r>", treeXML(t, docs[0]))
	require.Equal(t, "<r><p>abcd</p></r>", treeXML(t, docs[1]))

	// Step 2: d0 undoes its own delete. The reverse revives p2 by identity and
	// restores the cd its edit had removed -- now under p1, where the merge
	// moved it -- so the replicas reconverge. p2 comes back empty, which is the
	// state this test is about: a live element still carrying the merge lineage
	// it had as a tombstone.
	require.True(t, docs[0].CanUndo())
	require.NoError(t, docs[0].Undo())
	exchangeInOrder(t, docs, [][]int{{1}, {0}})
	require.Equal(t, "<r><p>abcd</p><p></p></r>", treeXML(t, docs[0]))
	require.Equal(t, "<r><p>abcd</p><p></p></r>", treeXML(t, docs[1]))

	// Step 3: d1 deletes the now-empty paragraph whole. Over
	// <r>0<p>1abcd5</p>6<p>7</p>8</r> that is Edit(6, 8). p2 is live, so it is
	// nobody's merge source: a plain delete of an empty element, and cd -- which
	// no longer names it -- stays.
	require.NoError(t, docs[1].Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Edit(6, 8, nil, 0)
		return nil
	}))
	require.Equal(t, "<r><p>abcd</p></r>", treeXML(t, docs[1]),
		"deleting a revived element must not erase what a merge moved out of it")

	exchangeInOrder(t, docs, [][]int{{1}, {0}})
	assert.Equal(t, "<r><p>abcd</p></r>", treeXML(t, docs[0]))
	assert.Equal(t, "<r><p>abcd</p></r>", treeXML(t, docs[1]))
	assert.Equal(t, treeShape(t, docs[0]), treeShape(t, docs[1]))
	assertNoDuplicateTreeIDs(t, docs[0], "after deleting the revived paragraph")
	assertNoDuplicateTreeIDs(t, docs[1], "after deleting the revived paragraph")
}

// TestTreeReviveConcurrentWithMergeKeepsTheSameLineage checks that the
// lineage a revive dissolves does not depend on the order a concurrent merge
// of the same element arrives in.
//
// d0 deletes p2 and undoes the delete while d1, which saw neither, merges p2
// into p1. d1 applies its merge and then the revive; d0 applies the revive and
// then the merge, whose Phase 6 stamps cd after the dissolution has run. The
// revive only dissolves the lineage of merges its own change knew, so d1 keeps
// the stamps d0's merge writes after it. The two still end apart on whether p2
// is live -- a revive by identity clears a concurrent removal on the replica
// that applies it second, as on main -- so only the lineage is compared here.
func TestTreeReviveConcurrentWithMergeKeepsTheSameLineage(t *testing.T) {
	docs := twoParagraphReplicas(t, 2)

	require.NoError(t, docs[0].Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Edit(4, 8, nil, 0)
		return nil
	}))
	require.NoError(t, docs[0].Undo())
	require.NoError(t, docs[1].Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Edit(3, 5, nil, 0)
		return nil
	}))
	exchangeInOrder(t, docs, [][]int{{1}, {0}})

	lineage := func(doc *document.Document) []string {
		var stamps []string
		var walk func(n *crdt.TreeNode)
		walk = func(n *crdt.TreeNode) {
			if n.MergedFrom != nil {
				stamps = append(stamps, fmt.Sprintf("%s<-%s@%s",
					n.IDString(), n.MergedFrom.CreatedAt.ToTestString(), n.MergedAt.ToTestString()))
			}
			for _, child := range n.Children(true) {
				walk(child)
			}
		}
		walk(treeCRDT(t, doc).Root())
		return stamps
	}
	require.Len(t, lineage(docs[0]), 1, "the merge stamped cd after the revive")
	assert.Equal(t, lineage(docs[0]), lineage(docs[1]),
		"the revive must not clear a concurrent merge's stamps on one replica only")
}

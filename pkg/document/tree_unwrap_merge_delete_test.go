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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// twoParagraphReplicas returns n replicas seeded with
// <r><p>ab</p><p>cd</p></r>, whose token indexes are
//
//	 0   1 2   3    4   5 6   7    8
//	<r> <p> a b </p> <p> c d </p> </r>
func twoParagraphReplicas(t *testing.T, n int) []*document.Document {
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
			Type: "r",
			Children: []json.TreeNode{{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "ab"}},
			}, {
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "cd"}},
			}},
		})
		return nil
	}))
	exchangeInOrder(t, docs, [][]int{{}, {0}}[:n])

	return docs
}

// liveTreeShape renders only the live nodes under "t", with their IDs: it is
// treeShape minus the tombstones. Where two replicas agree on every visible
// node's identity and order but not on where a tombstone sits among them, this
// is the part of the convergence that does hold.
func liveTreeShape(t *testing.T, doc *document.Document) string {
	t.Helper()

	var walk func(n *crdt.TreeNode) string
	walk = func(n *crdt.TreeNode) string {
		if n.IsText() {
			return fmt.Sprintf("%s%q", n.IDString(), n.Value)
		}
		var children []string
		for _, child := range n.Children() {
			children = append(children, walk(child))
		}
		return fmt.Sprintf("%s#%s[%s]", n.Type(), n.IDString(), strings.Join(children, ","))
	}

	return walk(treeCRDT(t, doc).Root())
}

// editRangeOnEach applies one content-less range delete per replica and
// exchanges the two changes, so each replica sees the other's edit second.
func editRangeOnEach(t *testing.T, docs []*document.Document, ranges [][2]int) {
	t.Helper()

	for i, doc := range docs {
		from, to := ranges[i][0], ranges[i][1]
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(from, to, nil, 0)
			return nil
		}))
	}
	exchangeInOrder(t, docs, [][]int{{1}, {0}})
}

// TestTreeUnwrapAndMergeDelete checks that an unwrap -- a content-less Edit
// over a paragraph's opening token, which hoists its children into the root
// -- converges with a concurrent delete that covered the same paragraph
// whole (#1956).
//
// §6.2 propagates a delete to the children a concurrent merge moved out of
// the deleted node, but it used to skip that whenever the children had
// landed in this edit's own merge destination. An unwrap moves them into the
// root, and a delete that ends at the next paragraph's opening token merges
// into the root too, so the skip fired and the hoisted text stayed alive on
// the replica that unwrapped first.
func TestTreeUnwrapAndMergeDelete(t *testing.T) {
	t.Run("unwrap of a paragraph a concurrent delete covers whole", func(t *testing.T) {
		docs := twoParagraphReplicas(t, 2)

		// d1 unwraps p1: Edit(0, 1) removes only its opening token, hoisting
		// ab into the root. d2 deletes p1 whole -- ab included -- and p2's
		// opening token: Edit(0, 5).
		editRangeOnEach(t, docs, [][2]int{{0, 1}, {0, 5}})

		// d2's delete covered ab, so it is gone on both replicas.
		assert.Equal(t, "<r>cd</r>", treeXML(t, docs[0]))
		assert.Equal(t, "<r>cd</r>", treeXML(t, docs[1]))

		// The replicas agree on every live node and on their order, but not
		// on where the tombstoned ab sits among them: mergeNodes appends
		// merge-moved children to the end of the destination, so each replica
		// orders them by arrival -- d1 ends with [ab(x), cd] under the root
		// and d2 with [cd, ab(x)].
		//
		// Pinned rather than left unasserted. Fixing it means placing merged
		// children at the merge source's position instead of appending, which
		// changes the outcome of every unwrap and would have to land in the JS
		// SDK at the same moment or the two ports would stop converging with
		// each other outright. Out of scope here; see the task's known
		// limitations. If this NotEqual ever fails, the limitation is gone.
		assert.Equal(t, liveTreeShape(t, docs[0]), liveTreeShape(t, docs[1]))
		assert.NotEqual(t, treeShape(t, docs[0]), treeShape(t, docs[1]),
			"tombstone ordering now converges: replace this with the "+
				"treeShape equality the other subtests assert")
	})

	t.Run("same unwrap on both replicas keeps the hoisted children", func(t *testing.T) {
		docs := twoParagraphReplicas(t, 2)

		// The skip §6.2 still needs: both replicas run the same unwrap, so
		// each sees the other's merge already done and must not read it as a
		// delete of the children it moved itself. Edit(4, 5) removes p2's
		// opening token, hoisting cd into the root after p1.
		//
		// The last paragraph, deliberately: unwrapping p1 instead drags in a
		// separate, pre-existing §1.1 problem, where the redirect of the
		// second unwrap's to-position onto the hoisted ab -- which mergeNodes
		// appended after p2 -- widens that range over the untouched p2 and
		// destroys cd. Both replicas agree on that loss, before this change
		// and after, but asserting it here would bless it as the expected
		// result of an unwrap and hide what this subtest is actually for.
		editRangeOnEach(t, docs, [][2]int{{4, 5}, {4, 5}})

		// Without the skip the replica that unwrapped first tombstones the cd
		// it hoisted itself, leaving <r><p>ab</p></r> against
		// <r><p>ab</p>cd</r>.
		assert.Equal(t, "<r><p>ab</p>cd</r>", treeXML(t, docs[0]))
		assert.Equal(t, "<r><p>ab</p>cd</r>", treeXML(t, docs[1]))
		assert.Equal(t, treeShape(t, docs[0]), treeShape(t, docs[1]))
	})

	t.Run("unwrap of a paragraph a concurrent merge emptied", func(t *testing.T) {
		docs := twoParagraphReplicas(t, 2)

		// The other skip §6.2 needs: d1 merges p2 into p1 (Edit(1, 5) removes
		// ab, p1's closing token and p2's opening token), while d2 unwraps p2
		// by removing its closing token (Edit(7, 8)). d2's range starts
		// inside p2, so it deletes nothing p2 held -- cd survives.
		editRangeOnEach(t, docs, [][2]int{{1, 5}, {7, 8}})

		assert.Equal(t, "<r><p>cd</p></r>", treeXML(t, docs[0]))
		assert.Equal(t, treeShape(t, docs[0]), treeShape(t, docs[1]))
	})
}

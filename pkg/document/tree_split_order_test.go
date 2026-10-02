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

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// treeShape renders the tree under "t" with every node's ID, tombstones
// included. XML cannot tell two empty <p>s apart; this can, so it is what the
// convergence checks below compare.
func treeShape(t *testing.T, doc *document.Document) string {
	t.Helper()

	var walk func(n *crdt.TreeNode) string
	walk = func(n *crdt.TreeNode) string {
		removed := ""
		if n.RemovedAt() != nil {
			removed = "x"
		}
		if n.IsText() {
			return fmt.Sprintf("%s%s%q", n.IDString(), removed, n.Value)
		}
		var children []string
		for _, child := range n.Children(true) {
			children = append(children, walk(child))
		}
		return fmt.Sprintf("%s#%s%s[%s]", n.Type(), n.IDString(), removed, strings.Join(children, ","))
	}

	return walk(treeCRDT(t, doc).Root())
}

// splitReplicas returns n replicas seeded with <doc><p><span>abcde</span></p></doc>.
func splitReplicas(t *testing.T, n int) []*document.Document {
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
				Type: "p",
				Children: []json.TreeNode{{
					Type:     "span",
					Children: []json.TreeNode{{Type: "text", Value: "abcde"}},
				}},
			}},
		})
		return nil
	}))
	exchangeInOrder(t, docs, [][]int{{}, {0}, {0}}[:n])

	return docs
}

// exchangeInOrder hands every replica's pending changes to the others. orders[i]
// lists, in arrival order, whose changes replica i receives, so each replica
// can see the concurrent changes in a different order.
func exchangeInOrder(t *testing.T, docs []*document.Document, orders [][]int) {
	t.Helper()

	// Through protobuf, as on the wire: handing a change's pointer to another
	// document lets the receiver rewrite its version vector in place.
	packs := make([]*change.Pack, len(docs))
	for i, doc := range docs {
		pb, err := converter.ToChangePack(doc.CreateChangePack())
		require.NoError(t, err)
		packs[i], err = converter.FromChangePack(pb)
		require.NoError(t, err)
	}
	for i, doc := range docs {
		for _, j := range orders[i] {
			if j == i {
				continue
			}
			require.NoError(t, doc.ApplyChangePack(change.NewPack(
				packs[j].DocumentKey, change.NewCheckpoint(0, 0), packs[j].Changes, time.InitialVersionVector, nil,
			)))
		}
	}
	for i, doc := range docs {
		var lastSeq uint32
		if n := len(packs[i].Changes); n > 0 {
			lastSeq = packs[i].Changes[n-1].ClientSeq()
		}
		require.NoError(t, doc.ApplyChangePack(change.NewPack(
			packs[i].DocumentKey, change.NewCheckpoint(0, lastSeq), nil, time.InitialVersionVector, nil,
		)))
	}
}

// TestTreeSameBoundarySplitOrder checks that concurrent element splits of one
// node at one boundary end up in the same order on every replica, whatever
// order the replicas apply them in.
//
// The products of those splits are placed directly after the node they split,
// so without an ordering rule they sit in arrival order. XML hides it -- all
// but the last product are empty -- until a position-based operation lands on
// the difference: a range delete over the root then leaves an empty node on
// one replica for good, and an empty node between two halves carries
// different attributes on each replica.
func TestTreeSameBoundarySplitOrder(t *testing.T) {
	type edit func(tree *json.Tree)

	cases := []struct {
		name string
		op   edit
	}{
		{"paragraph split", func(tree *json.Tree) { tree.EditByPath([]int{0, 1}, []int{0, 1}, nil, 1) }},
		{"span split", func(tree *json.Tree) { tree.EditByPath([]int{0, 0, 3}, []int{0, 0, 3}, nil, 1) }},
		{"span and paragraph split in one edit", func(tree *json.Tree) {
			tree.EditByPath([]int{0, 0, 3}, []int{0, 0, 3}, nil, 2)
		}},
		{"span split, then paragraph split in a second edit", func(tree *json.Tree) {
			tree.EditByPath([]int{0, 0, 3}, []int{0, 0, 3}, nil, 1)
			tree.EditByPath([]int{0, 1}, []int{0, 1}, nil, 1)
		}},
		{"the same at the end of the text", func(tree *json.Tree) {
			tree.EditByPath([]int{0, 0, 5}, []int{0, 0, 5}, nil, 1)
			tree.EditByPath([]int{0, 1}, []int{0, 1}, nil, 1)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name+": two replicas, and a range delete afterwards", func(t *testing.T) {
			docs := splitReplicas(t, 2)
			for _, doc := range docs {
				require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
					tc.op(root.GetTree("t"))
					return nil
				}))
			}
			exchangeInOrder(t, docs, [][]int{{1}, {0}})

			assert.Equal(t, treeXML(t, docs[0]), treeXML(t, docs[1]))
			assert.Equal(t, treeShape(t, docs[0]), treeShape(t, docs[1]))

			require.NoError(t, docs[0].Update(func(root *json.Object, p *presence.Presence) error {
				tree := root.GetTree("t")
				tree.EditByPath([]int{0}, []int{len(treeCRDT(t, docs[0]).Root().Children())}, nil, 0)
				return nil
			}))
			exchangeInOrder(t, docs, [][]int{{1}, {0}})

			assert.Equal(t, "<doc></doc>", treeXML(t, docs[0]))
			assert.Equal(t, "<doc></doc>", treeXML(t, docs[1]))
		})

		t.Run(tc.name+": three replicas, each in a different arrival order", func(t *testing.T) {
			docs := splitReplicas(t, 3)
			for _, doc := range docs {
				require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
					tc.op(root.GetTree("t"))
					return nil
				}))
			}
			exchangeInOrder(t, docs, [][]int{{2, 1}, {0, 2}, {1, 0}})

			shape := treeShape(t, docs[0])
			assert.Equal(t, shape, treeShape(t, docs[1]))
			assert.Equal(t, shape, treeShape(t, docs[2]))
			assert.Contains(t, treeXML(t, docs[0]), "abc")
		})
	}

	t.Run("the empty node between two halves carries the same attributes everywhere", func(t *testing.T) {
		docs := splitReplicas(t, 2)
		require.NoError(t, docs[0].Update(func(root *json.Object, p *presence.Presence) error {
			tree := root.GetTree("t")
			tree.EditByPath([]int{0, 0, 3}, []int{0, 0, 3}, nil, 1)
			tree.StyleByPath([]int{0, 0}, []int{0, 1}, map[string]string{"bold": "true"})
			return nil
		}))
		require.NoError(t, docs[1].Update(func(root *json.Object, p *presence.Presence) error {
			tree := root.GetTree("t")
			tree.EditByPath([]int{0, 0, 3}, []int{0, 0, 3}, nil, 1)
			tree.StyleByPath([]int{0, 1}, []int{0, 2}, map[string]string{"italic": "true"})
			return nil
		}))
		exchangeInOrder(t, docs, [][]int{{1}, {0}})

		assert.Equal(t, treeXML(t, docs[0]), treeXML(t, docs[1]))
	})
}

// TestTreeSameBoundarySplitAfterOlderSplit covers a same-boundary split that
// meets an older, already-known split product of one of the two actors.
func TestTreeSameBoundarySplitAfterOlderSplit(t *testing.T) {
	cases := []struct {
		name  string
		known string // why the case is skipped; empty when it converges
		a, b  func(tree *json.Tree)
	}{
		{
			name: "the older actor splits the span again",
			a:    func(tree *json.Tree) { tree.EditByPath([]int{0, 0, 3}, []int{0, 0, 3}, nil, 1) },
			b:    func(tree *json.Tree) { tree.EditByPath([]int{0, 0, 3}, []int{0, 0, 3}, nil, 1) },
		},
		{
			name: "the older actor splits the paragraph",
			known: "KNOWN: the other actor's empty product lands before the paragraph boundary on the replica " +
				"that applied it first (the edit's position advance passes it) and after it on the other (§7.4 " +
				"re-parents it next to the older product). Diverges on main too.",
			a: func(tree *json.Tree) { tree.EditByPath([]int{0, 1}, []int{0, 1}, nil, 1) },
			b: func(tree *json.Tree) { tree.EditByPath([]int{0, 0, 3}, []int{0, 0, 3}, nil, 1) },
		},
		{
			name:  "the older actor presses Enter as two edits",
			known: "KNOWN: same shape as the paragraph case. Diverges on main too.",
			a: func(tree *json.Tree) {
				tree.EditByPath([]int{0, 0, 3}, []int{0, 0, 3}, nil, 1)
				tree.EditByPath([]int{0, 1}, []int{0, 1}, nil, 1)
			},
			b: func(tree *json.Tree) { tree.EditByPath([]int{0, 0, 3}, []int{0, 0, 3}, nil, 1) },
		},
	}

	for _, tc := range cases {
		for older := range 2 {
			t.Run(fmt.Sprintf("%s, older split by replica %d", tc.name, older), func(t *testing.T) {
				if tc.known != "" {
					t.Skip(tc.known)
				}

				docs := splitReplicas(t, 2)
				require.NoError(t, docs[older].Update(func(root *json.Object, p *presence.Presence) error {
					root.GetTree("t").EditByPath([]int{0, 0, 3}, []int{0, 0, 3}, nil, 1)
					return nil
				}))
				exchangeInOrder(t, docs, [][]int{{1}, {0}})

				require.NoError(t, docs[older].Update(func(root *json.Object, p *presence.Presence) error {
					tc.a(root.GetTree("t"))
					return nil
				}))
				require.NoError(t, docs[1-older].Update(func(root *json.Object, p *presence.Presence) error {
					tc.b(root.GetTree("t"))
					return nil
				}))
				exchangeInOrder(t, docs, [][]int{{1}, {0}})

				assert.Equal(t, treeXML(t, docs[0]), treeXML(t, docs[1]))
				assert.Equal(t, treeShape(t, docs[0]), treeShape(t, docs[1]))
			})
		}
	}
}

// paragraphReplicas returns n replicas seeded with <doc><p>{text}</p></doc>,
// for scripts written in index positions.
func paragraphReplicas(t *testing.T, n int, text string) []*document.Document {
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
	exchangeInOrder(t, docs, [][]int{{}, {0}, {0}}[:n])

	return docs
}

// splitStep is one edit by one replica: a split at index (content == "" and
// to == 0), an insert of content there, or a delete of the range index..to.
type splitStep struct {
	replica int
	index   int
	content string
	to      int
}

func applySplitSteps(t *testing.T, docs []*document.Document, steps []splitStep) {
	t.Helper()
	for _, s := range steps {
		require.NoError(t, docs[s.replica].Update(func(root *json.Object, p *presence.Presence) error {
			tree := root.GetTree("t")
			switch {
			case s.to > 0:
				tree.Edit(s.index, s.to, nil, 0)
			case s.content != "":
				tree.Edit(s.index, s.index, &json.TreeNode{Type: "text", Value: s.content}, 0)
			default:
				tree.Edit(s.index, s.index, nil, 1)
			}
			return nil
		}))
	}
}

// A split that follows the concurrent same-boundary splits above, made by one
// of the two actors before it has seen the other's split (yorkie-js-sdk#1433).
//
// The §7.8 ordering walks the InsNextID chain to find the node that holds the
// right half. The follow-up split is in that chain too, but it cut the right
// half at a *later* boundary, so the walk must stop at the node that holds the
// content rather than go on to the follow-up's empty product.
func TestTreeFurtherSplitAfterSameBoundarySplits(t *testing.T) {
	split := func(replica, index int) splitStep { return splitStep{replica: replica, index: index} }
	insert := func(replica, index int, s string) splitStep {
		return splitStep{replica: replica, index: index, content: s}
	}
	del := func(replica, from, to int) splitStep { return splitStep{replica: replica, index: from, to: to} }

	cases := []struct {
		name  string
		text  string
		steps []splitStep
	}{
		// The three-operation minimum from the issue: both split "a|b", then
		// the newer actor splits again at the end of its right piece.
		{`the newer actor splits again at the end of "b"`, "ab", []splitStep{split(0, 2), split(1, 2), split(1, 5)}},
		// Converged before the fix; pinned so the asymmetry does not come back.
		{`the older actor splits again at the end of "b"`, "ab", []splitStep{split(0, 2), split(1, 2), split(0, 5)}},
		{"with an insert ahead of the newer actor's splits", "ab",
			[]splitStep{split(0, 2), insert(1, 1, "ㅂ"), split(1, 3), split(1, 6)}},
		// The follow-up split at offset 0 of the right piece is a same-boundary
		// split as well, so the walk has to pass its empty product.
		{`the newer actor splits again at the start of "b"`, "ab", []splitStep{split(0, 2), split(1, 2), split(1, 4)}},
		// Text typed into the empty right piece is not the right half: a split
		// after it is still a same-boundary split, and the walk has to go on.
		{"the newer actor types into its empty right piece, then splits", "ab",
			[]splitStep{split(0, 2), split(1, 2), insert(1, 4, "x"), split(1, 5)}},
		{"the same with an empty right half", "a",
			[]splitStep{split(0, 2), split(1, 2), insert(1, 4, "x"), split(1, 5)}},
		{"typed text and the right half on either side of the follow-up split", "ab",
			[]splitStep{split(0, 2), split(1, 2), insert(1, 4, "x"), split(1, 5), insert(1, 7, "y")}},
		// The right half deleted before the follow-up split, so the stopping
		// node's only known child is a tombstone.
		{`the newer actor deletes "b", then splits at the end of the piece`, "ab",
			[]splitStep{split(0, 2), split(1, 2), del(1, 4, 5), split(1, 4)}},
		{`the same, deleting "b" before its own same-boundary split`, "abc",
			[]splitStep{split(0, 2), split(1, 2), del(1, 4, 5), split(1, 5)}},
		// Delta-debugged minima of a split-only fuzz over <p>abcdef</p>.
		{"two follow-up splits", "abcdef", []splitStep{split(1, 3), split(0, 3), split(1, 3), split(1, 10)}},
		{"a follow-up split in the left piece", "abcdef", []splitStep{split(1, 5), split(0, 3), split(1, 3)}},
		{"a follow-up split at the start, then in the right piece", "abcdef",
			[]splitStep{split(1, 1), split(1, 4), split(0, 1)}},
		{"a follow-up split in the middle of the right piece", "abcdef",
			[]splitStep{split(0, 4), split(1, 4), split(1, 7)}},
	}

	for _, tc := range cases {
		t.Run(tc.name+": two replicas", func(t *testing.T) {
			docs := paragraphReplicas(t, 2, tc.text)
			applySplitSteps(t, docs, tc.steps)
			exchangeInOrder(t, docs, [][]int{{1}, {0}})

			assert.Equal(t, docs[0].Root().GetTree("t").ToXML(), docs[1].Root().GetTree("t").ToXML())
			assert.Equal(t, treeShape(t, docs[0]), treeShape(t, docs[1]))
		})
	}

	// The flat cases cannot reach the multi-level shape: a text split keeps
	// the original createdAt, so the right half's text child is known by
	// itself. Split <p><span>abcde</span></p> at both levels and the outer
	// right-half product holds a freshly ticketed <span> instead, with the
	// known text one level further down -- which is why holdsKnownChild
	// descends.
	t.Run("the same shape nested one level deeper", func(t *testing.T) {
		docs := splitReplicas(t, 2)
		for _, doc := range docs {
			require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetTree("t").EditByPath([]int{0, 0, 3}, []int{0, 0, 3}, nil, 2)
				return nil
			}))
		}
		require.NoError(t, docs[1].Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(11, 11, nil, 2)
			return nil
		}))
		exchangeInOrder(t, docs, [][]int{{1}, {0}})

		assert.Equal(t, docs[0].Root().GetTree("t").ToXML(), docs[1].Root().GetTree("t").ToXML())
		assert.Equal(t, treeShape(t, docs[0]), treeShape(t, docs[1]))
		assert.Contains(t, docs[0].Root().GetTree("t").ToXML(), "abc")
		assert.Contains(t, docs[0].Root().GetTree("t").ToXML(), "de")
	})

	t.Run("a third replica agrees in both arrival orders", func(t *testing.T) {
		steps := []splitStep{split(0, 2), split(1, 2), split(1, 5)}
		for _, order := range [][]int{{0, 1}, {1, 0}} {
			docs := paragraphReplicas(t, 3, "ab")
			applySplitSteps(t, docs, steps)
			exchangeInOrder(t, docs, [][]int{{1}, {0}, order})

			shape := treeShape(t, docs[0])
			assert.Equal(t, shape, treeShape(t, docs[1]))
			assert.Equal(t, shape, treeShape(t, docs[2]))
		}
	})
}

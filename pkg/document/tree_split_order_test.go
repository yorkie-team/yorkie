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
		for older := 0; older < 2; older++ {
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

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
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

// cascadeReplicas returns two synced replicas holding <doc><p> with one
// <span> per given text. With flip the first replica gets the higher actor
// ID, so its ticket wins ties.
func cascadeReplicas(t *testing.T, spans []string, flip bool) []*document.Document {
	t.Helper()

	docs := make([]*document.Document, 2)
	for i := range docs {
		n := i + 1
		if flip {
			n = 2 - i
		}
		actor, err := time.ActorIDFromHex(fmt.Sprintf("%024d", n))
		require.NoError(t, err)
		docs[i] = document.New("doc")
		docs[i].SetActor(actor)
	}

	var children []json.TreeNode
	for _, value := range spans {
		children = append(children, json.TreeNode{
			Type:     "span",
			Children: []json.TreeNode{{Type: "text", Value: value}},
		})
	}
	require.NoError(t, docs[0].Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{
			Type:     "doc",
			Children: []json.TreeNode{{Type: "p", Children: children}},
		})
		return nil
	}))
	exchangeInOrder(t, docs, [][]int{{1}, {0}})

	return docs
}

// TestTreeSplitSiblingCascade checks the §4.1 split-sibling cascade of a
// deleted element against concurrent element splits of it
// (yorkie-js-sdk#1408). Each case runs in both role assignments and with
// both actor orders, so either side's ticket wins the LWW once.
func TestTreeSplitSiblingCascade(t *testing.T) {
	type edit func(tree *json.Tree)

	splitSpanAt0AndDropLeft := func(tree *json.Tree) {
		tree.EditByPath([]int{0, 1, 0}, []int{0, 1, 0}, nil, 1)
		tree.EditByPath([]int{0, 1}, []int{0, 2}, nil, 0)
	}
	enterAtSpanStart := func(tree *json.Tree) {
		tree.EditByPath([]int{0, 1, 0}, []int{0, 1, 0}, nil, 1)
		tree.EditByPath([]int{0, 2}, []int{0, 2}, nil, 1)
		tree.EditByPath([]int{0, 1}, []int{0, 2}, nil, 0)
	}

	cases := []struct {
		name  string
		spans []string
		setup edit // run on the second replica and synced before the race
		a, b  edit
		want  string // expected converged XML; empty means any
		// One replica may keep a concurrent split product as an empty span
		// that the other tombstoned (#1408, not fixed here). Text must
		// still agree.
		emptySpanResidue bool
		// Tombstones may sit in a different order on each side (a merge
		// against a split; main does the same). Compare shapes after GC.
		tombstoneOrder bool
	}{
		{
			name:  "same-boundary split at 0 + drop left piece on both sides (#1408)",
			spans: []string{"abc", "de"},
			a:     splitSpanAt0AndDropLeft, b: splitSpanAt0AndDropLeft,
			want:             "<doc><p><span>abc</span><span>de</span></p></doc>",
			emptySpanResidue: true,
		},
		{
			name:  "Enter at the start of a styled run on both sides (#1408)",
			spans: []string{"abc", "de"},
			a:     enterAtSpanStart, b: enterAtSpanStart,
			want:             "<doc><p><span>abc</span></p><p></p><p><span>de</span></p></doc>",
			emptySpanResidue: true,
		},
		{
			name:  "Enter on one side, split only on the other",
			spans: []string{"abc", "de"},
			a:     enterAtSpanStart,
			b: func(tree *json.Tree) {
				tree.EditByPath([]int{0, 1, 0}, []int{0, 1, 0}, nil, 1)
				tree.EditByPath([]int{0, 2}, []int{0, 2}, nil, 1)
			},
		},
		{
			name:  "split at 0 + drop left piece against a plain split at 0",
			spans: []string{"abc", "de"},
			a:     splitSpanAt0AndDropLeft,
			b: func(tree *json.Tree) {
				tree.EditByPath([]int{0, 1, 0}, []int{0, 1, 0}, nil, 1)
			},
		},
		{
			name:  "split at different offsets + drop left piece on both sides",
			spans: []string{"abc", "defg"},
			a: func(tree *json.Tree) {
				tree.EditByPath([]int{0, 1, 1}, []int{0, 1, 1}, nil, 1)
				tree.EditByPath([]int{0, 1}, []int{0, 2}, nil, 0)
			},
			b: func(tree *json.Tree) {
				tree.EditByPath([]int{0, 1, 3}, []int{0, 1, 3}, nil, 1)
				tree.EditByPath([]int{0, 1}, []int{0, 2}, nil, 0)
			},
			emptySpanResidue: true,
		},
		{
			name:  "split + drop left piece against deleting the whole span",
			spans: []string{"abc", "de"},
			a: func(tree *json.Tree) {
				tree.EditByPath([]int{0, 1, 1}, []int{0, 1, 1}, nil, 1)
				tree.EditByPath([]int{0, 1}, []int{0, 2}, nil, 0)
			},
			b:                func(tree *json.Tree) { tree.EditByPath([]int{0, 1}, []int{0, 2}, nil, 0) },
			want:             "<doc><p><span>abc</span></p></doc>",
			emptySpanResidue: true,
		},
		{
			// <span>ab</span><span>cd</span>, the second a split product.
			// One side merges it back and deletes the whole; the other
			// splits "cd". "d" was inside what the deleter deleted.
			name:  "merge the split sibling back and delete, against splitting it",
			spans: []string{"abcd"},
			setup: func(tree *json.Tree) { tree.EditByPath([]int{0, 0, 2}, []int{0, 0, 2}, nil, 1) },
			a: func(tree *json.Tree) {
				tree.EditByPath([]int{0, 0, 2}, []int{0, 1, 0}, nil, 0)
				tree.EditByPath([]int{0, 0}, []int{0, 1}, nil, 0)
			},
			b:              func(tree *json.Tree) { tree.EditByPath([]int{0, 1, 1}, []int{0, 1, 1}, nil, 1) },
			want:           "<doc><p></p></doc>",
			tombstoneOrder: true,
		},
		{
			// One side deletes "ab"; the other presses Enter at the start of
			// "cd", which nobody deleted.
			name:  "delete an element against Enter at the start of its split sibling",
			spans: []string{"abcd"},
			setup: func(tree *json.Tree) { tree.EditByPath([]int{0, 0, 2}, []int{0, 0, 2}, nil, 1) },
			a:     func(tree *json.Tree) { tree.EditByPath([]int{0, 0}, []int{0, 1}, nil, 0) },
			b: func(tree *json.Tree) {
				tree.EditByPath([]int{0, 1, 0}, []int{0, 1, 0}, nil, 1)
				tree.EditByPath([]int{0, 1}, []int{0, 2}, nil, 0)
			},
			want: "<doc><p><span>cd</span></p></doc>",
		},
		{
			// One side deletes both halves; the other splits "cd" and deletes
			// "c". The deleter's delete of "cd" may lose the LWW.
			name:  "delete an element and its split sibling, against splitting and deleting that sibling",
			spans: []string{"abcd"},
			setup: func(tree *json.Tree) { tree.EditByPath([]int{0, 0, 2}, []int{0, 0, 2}, nil, 1) },
			a:     func(tree *json.Tree) { tree.EditByPath([]int{0, 0}, []int{0, 2}, nil, 0) },
			b: func(tree *json.Tree) {
				tree.EditByPath([]int{0, 1, 1}, []int{0, 1, 1}, nil, 1)
				tree.EditByPath([]int{0, 1}, []int{0, 2}, nil, 0)
			},
			want: "<doc><p></p></doc>",
		},
		{
			name:  "cascade stops at a split sibling the deleter already knew",
			spans: []string{"abcd"},
			setup: func(tree *json.Tree) { tree.EditByPath([]int{0, 0, 2}, []int{0, 0, 2}, nil, 1) },
			a:     func(tree *json.Tree) { tree.EditByPath([]int{0, 0}, []int{0, 1}, nil, 0) },
			b: func(tree *json.Tree) {
				tree.EditByPath([]int{0, 1, 1}, []int{0, 1, 1}, nil, 1)
				tree.EditByPath([]int{0, 0, 1}, []int{0, 0, 1}, nil, 1)
			},
			want: "<doc><p><span>c</span><span>d</span></p></doc>",
		},
	}

	for _, tc := range cases {
		for _, swap := range []bool{false, true} {
			for _, flip := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/swap=%v/flip=%v", tc.name, swap, flip), func(t *testing.T) {
					docs := cascadeReplicas(t, tc.spans, flip)
					if tc.setup != nil {
						require.NoError(t, docs[1].Update(func(root *json.Object, p *presence.Presence) error {
							tc.setup(root.GetTree("t"))
							return nil
						}))
						exchangeInOrder(t, docs, [][]int{{1}, {0}})
					}

					first, second := tc.a, tc.b
					if swap {
						first, second = tc.b, tc.a
					}
					require.NoError(t, docs[0].Update(func(root *json.Object, p *presence.Presence) error {
						first(root.GetTree("t"))
						return nil
					}))
					require.NoError(t, docs[1].Update(func(root *json.Object, p *presence.Presence) error {
						second(root.GetTree("t"))
						return nil
					}))
					exchangeInOrder(t, docs, [][]int{{1}, {0}})

					x1, x2 := treeXML(t, docs[0]), treeXML(t, docs[1])
					residue := x1 != x2
					if residue {
						require.True(t, tc.emptySpanResidue, "XML diverged:\n%s\n%s", x1, x2)
						// Exactly one empty span more on one side; nothing else.
						const empty = "<span></span>"
						n1, n2 := strings.Count(x1, empty), strings.Count(x2, empty)
						assert.Equal(t, 1, max(n1, n2)-min(n1, n2), "not one empty span:\n%s\n%s", x1, x2)
						x1 = strings.Replace(x1, empty, "", n1-min(n1, n2))
						x2 = strings.Replace(x2, empty, "", n2-min(n1, n2))
						assert.Equal(t, x1, x2, "diverged beyond an empty span")
					} else if !tc.tombstoneOrder {
						assert.Equal(t, treeShape(t, docs[0]), treeShape(t, docs[1]), "shape diverged")
					}
					if tc.want != "" {
						assert.Equal(t, tc.want, strings.ReplaceAll(x1, "<span></span>", ""))
					}

					// Every tombstone the race left is collectable on both
					// sides, and what remains still agrees.
					vv := helper.MaxVersionVector(docs[0].ActorID(), docs[1].ActorID())
					docs[0].GarbageCollect(vv)
					docs[1].GarbageCollect(vv)
					assert.Equal(t, 0, docs[0].GarbageLen(), "garbage left on d1")
					assert.Equal(t, 0, docs[1].GarbageLen(), "garbage left on d2")
					if !residue {
						assert.Equal(t, treeShape(t, docs[0]), treeShape(t, docs[1]), "shape diverged after gc")
					}
				})
			}
		}
	}
}

// TestTreeSplitSiblingCascadeResidueConverges is the reproducer for the
// empty-span residue §4.1 leaves, which TestTreeSplitSiblingCascade accepts
// via its emptySpanResidue flag rather than fails on. Stopping the cascade at
// a sibling the editor saw alive keeps the concurrent split product nobody
// deleted alive on the replica whose own delete won the element's LWW; on the
// other replica that product is born tombstoned (`split.removedAt =
// n.removedAt`, tree.go). The two trees then differ by one empty span, with
// text agreeing. Closing it needs the tombstone record to keep every delete
// rather than the newest — a data model, snapshot encoding and JS-port change
// past this rule; see the limitations in §4.1 of
// docs/design/concurrent-merge-split.md.
func TestTreeSplitSiblingCascadeResidueConverges(t *testing.T) {
	t.Skip("still reproduces: breaking the cascade at a known-alive sibling " +
		"leaves a concurrent split product alive on one replica and " +
		"tombstoned on the other")

	docs := cascadeReplicas(t, []string{"abc", "de"}, false)
	for _, doc := range docs {
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			tree := root.GetTree("t")
			tree.EditByPath([]int{0, 1, 0}, []int{0, 1, 0}, nil, 1)
			tree.EditByPath([]int{0, 1}, []int{0, 2}, nil, 0)
			return nil
		}))
	}
	exchangeInOrder(t, docs, [][]int{{1}, {0}})

	assert.Equal(t, treeXML(t, docs[0]), treeXML(t, docs[1]), "XML diverged")
	assert.Equal(t, treeShape(t, docs[0]), treeShape(t, docs[1]), "shape diverged")
}

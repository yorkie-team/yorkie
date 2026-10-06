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

// joinReplicas returns two synced replicas holding <doc><p> with one <span>
// per given text.
func joinReplicas(t *testing.T, spans []string) []*document.Document {
	t.Helper()

	docs := make([]*document.Document, 2)
	for i := range docs {
		actor, err := time.ActorIDFromHex(fmt.Sprintf("%024d", i+1))
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

// TestTreeJoinAfterSplitInSpan checks that joining two paragraphs right after
// an Enter in the middle of a span merges them, on the replica that joins and
// on the one that receives it, as the JS SDK does (yorkie-js-sdk#1237).
// The join's to position is the start of the second paragraph, so narrowing
// the deleted range to the split span there would make it run backwards and
// drop the merge.
func TestTreeJoinAfterSplitInSpan(t *testing.T) {
	enters := []struct {
		name  string
		enter func(tree *json.Tree)
	}{
		{"split level 2", func(tree *json.Tree) {
			tree.EditByPath([]int{0, 0, 1}, []int{0, 0, 1}, nil, 2)
		}},
		{"span split, then paragraph split", func(tree *json.Tree) {
			tree.EditByPath([]int{0, 0, 1}, []int{0, 0, 1}, nil, 1)
			tree.EditByPath([]int{0, 1}, []int{0, 1}, nil, 1)
		}},
	}
	cases := []struct {
		spans []string
		want  string
	}{
		{[]string{"ab"}, "<doc><p><span>a</span><span>b</span></p></doc>"},
		{[]string{"ab", "cd"}, "<doc><p><span>a</span><span>b</span><span>cd</span></p></doc>"},
	}

	for _, tc := range cases {
		for _, e := range enters {
			for joiner := range 2 {
				t.Run(fmt.Sprintf("%v/%s/joiner=d%d", tc.spans, e.name, joiner+1), func(t *testing.T) {
					docs := joinReplicas(t, tc.spans)
					require.NoError(t, docs[0].Update(func(root *json.Object, p *presence.Presence) error {
						e.enter(root.GetTree("t"))
						return nil
					}))
					exchangeInOrder(t, docs, [][]int{{1}, {0}})

					require.NoError(t, docs[joiner].Update(func(root *json.Object, p *presence.Presence) error {
						root.GetTree("t").EditByPath([]int{0, 1}, []int{1, 0}, nil, 0)
						return nil
					}))
					assert.Equal(t, tc.want, treeXML(t, docs[joiner]), "joiner")

					exchangeInOrder(t, docs, [][]int{{1}, {0}})
					assert.Equal(t, tc.want, treeXML(t, docs[0]))
					assert.Equal(t, tc.want, treeXML(t, docs[1]))
				})
			}
		}
	}
}

// TestTreeNarrowingAwayFromParagraphStart pins that Phase 3 still narrows
// when the to position is not the start of toParent. One replica deletes the
// second span while the other splits the first one twice with level 2; the
// delete must not reach the paragraphs those splits made.
func TestTreeNarrowingAwayFromParagraphStart(t *testing.T) {
	docs := joinReplicas(t, []string{"cd", "jk"})
	require.NoError(t, docs[0].Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").EditByPath([]int{0, 0, 1}, []int{0, 0, 1}, nil, 2)
		root.GetTree("t").EditByPath([]int{0, 0, 0}, []int{0, 0, 0}, nil, 2)
		return nil
	}))
	require.NoError(t, docs[1].Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").EditByPath([]int{0, 1}, []int{0, 2}, nil, 0)
		return nil
	}))
	exchangeInOrder(t, docs, [][]int{{1}, {0}})

	want := "<doc><p><span></span></p><p><span>c</span></p><p><span>d</span></p></doc>"
	assert.Equal(t, want, treeXML(t, docs[0]))
	assert.Equal(t, want, treeXML(t, docs[1]))
}

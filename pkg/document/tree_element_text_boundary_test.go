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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
)

// An index right after an element and right before a text sibling anchors on
// the element (FindPos). On a replica that has split that element
// concurrently, the anchor then has to move past the split half the editing
// change did not know, or the edit lands between the two halves instead of
// before the text. These cases run the anchor through the wire to a replica
// that applies the split first, and check both replicas agree.
//
//	    0   1   2 3 4 5 6 7    8 9 10 11 12 13 14    15
//	<r> <p> <b> h e l l o </b>   w  o  r  l  d   </p>  </r>
func TestTreeElementTextBoundaryUnderConcurrentSplit(t *testing.T) {
	tests := []struct {
		name string
		edit func(tree *json.Tree)
		want string
	}{
		{
			name: "insert before the text",
			edit: func(tree *json.Tree) {
				tree.Edit(8, 8, &json.TreeNode{Type: "text", Value: "|"}, 0)
			},
			want: "<r><p><b>hel</b><b>lo</b>| world</p></r>",
		},
		{
			name: "delete the text from the boundary",
			edit: func(tree *json.Tree) {
				tree.Edit(8, 14, nil, 0)
			},
			want: "<r><p><b>hel</b><b>lo</b></p></r>",
		},
		{
			name: "split the paragraph at the boundary",
			edit: func(tree *json.Tree) {
				tree.Edit(8, 8, nil, 1)
			},
			want: "<r><p><b>hel</b><b>lo</b></p><p> world</p></r>",
		},
		{
			name: "style a range that ends at the boundary",
			edit: func(tree *json.Tree) {
				tree.Style(1, 8, map[string]string{"k": "v"})
			},
			want: `<r><p><b k="v">hel</b><b k="v">lo</b> world</p></r>`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			seed := newActor(t, "000000000000000000000009")
			require.NoError(t, seed.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewTree("t", json.TreeNode{
					Type: "r",
					Children: []json.TreeNode{{
						Type: "p",
						Children: []json.TreeNode{
							{Type: "b", Children: []json.TreeNode{{Type: "text", Value: "hello"}}},
							{Type: "text", Value: " world"},
						},
					}},
				})
				return nil
			}))
			base := grab(t, seed)

			editor := newActor(t, "00000000000000000000000a")
			splitter := newActor(t, "00000000000000000000000b")
			feed(t, editor, base)
			feed(t, splitter, base)

			require.NoError(t, editor.Update(func(root *json.Object, p *presence.Presence) error {
				tc.edit(root.GetTree("t"))
				return nil
			}))
			require.NoError(t, splitter.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetTree("t").Edit(5, 5, nil, 1) // <b>hel</b><b>lo</b>
				return nil
			}))

			edits, splits := grab(t, editor), grab(t, splitter)
			feed(t, editor, splits)
			feed(t, splitter, edits)

			assert.Equal(t, tc.want, editor.Root().GetTree("t").ToXML())
			assert.Equal(t, tc.want, splitter.Root().GetTree("t").ToXML())
		})
	}
}

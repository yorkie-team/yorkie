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

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
)

// TestTreeSplitAfterOriginInsert pins a multi-level split racing an insert
// placed by origin right after the element the split cuts. The insert went
// after <p>abcd</p>, so it belongs after that element's last piece, cd. A
// replica that applies the insert after the split places it there by origin;
// one that applies the split after the insert used to leave the insert on the
// left of the cut, and the two diverged.
func TestTreeSplitAfterOriginInsert(t *testing.T) {
	want := `<r><p><p><p italic="a">ab</p></p><p><p italic="a">cd</p><i>z</i>` +
		`<p italic="a">efgh</p></p><p italic="a">ijkl</p></p></r>`

	for _, splitterFirst := range []bool{true, false} {
		d1, d2, _, _ := newReplicas(t)
		require.NoError(t, d1.Update(func(root *json.Object, _ *presence.Presence) error {
			italic := map[string]string{"italic": "a"}
			root.SetNewTree("t", json.TreeNode{Type: "r", Children: []json.TreeNode{{
				Type: "p",
				Children: []json.TreeNode{
					{Type: "p", Children: []json.TreeNode{
						{Type: "p", Attributes: italic, Children: []json.TreeNode{{Type: "text", Value: "abcd"}}},
						{Type: "p", Attributes: italic, Children: []json.TreeNode{{Type: "text", Value: "efgh"}}},
					}},
					{Type: "p", Attributes: italic, Children: []json.TreeNode{{Type: "text", Value: "ijkl"}}},
				},
			}}})
			return nil
		}))
		crossSync(t, d1, d2)

		splitter, inserter := d1, d2
		if !splitterFirst {
			splitter, inserter = d2, d1
		}
		// Split "ab|cd" two levels up, and insert <i> right after <p>abcd</p>.
		require.NoError(t, splitter.Update(func(root *json.Object, _ *presence.Presence) error {
			root.GetTree("t").Edit(5, 5, nil, 2)
			return nil
		}))
		require.NoError(t, inserter.Update(func(root *json.Object, _ *presence.Presence) error {
			root.GetTree("t").Edit(8, 8, &json.TreeNode{
				Type:     "i",
				Children: []json.TreeNode{{Type: "text", Value: "z"}},
			}, 0)
			return nil
		}))
		crossSync(t, d1, d2)

		require.Equal(t, want, d1.Root().GetTree("t").ToXML())
		require.Equal(t, want, d2.Root().GetTree("t").ToXML())
	}
}

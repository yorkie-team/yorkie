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

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestTreeEditAfterNonBMPText covers yorkie#2027: a caret edit inside a text
// node that holds characters outside the BMP. SplitText recorded the left
// piece's length in runes while the rest of the tree counts UTF-16 code
// units, so the edit's second resolution of the same anchor re-split the left
// piece past its recorded end and failed. On the server that failure repeats
// on every replay of the stored history, so the document can no longer be
// rebuilt for a snapshot or compaction.
func TestTreeEditAfterNonBMPText(t *testing.T) {
	// "즐거운 한가위 🇰🇷ㅇㄹ" is 12 runes but 14 UTF-16 code units: the flag
	// is two regional indicators, each a surrogate pair.
	const text = "즐거운 한가위 🇰🇷ㅇㄹ"

	t.Run("a caret edit right after the flag applies and replays", func(t *testing.T) {
		d1 := document.New("test-doc")
		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{
				Type: "r",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: text}},
				}},
			})
			return nil
		}))

		// Offset 12 is right after the flag, in UTF-16 code units.
		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 12}, []int{0, 12}, &json.TreeNode{Type: "text", Value: "!"}, 0)
			return nil
		}))
		assert.Equal(t, "<r><p>즐거운 한가위 🇰🇷!ㅇㄹ</p></r>", treeXML(t, d1))

		// Replay the whole history on a fresh document, as the server does to
		// build a snapshot. Through protobuf, as it is stored.
		pb, err := converter.ToChangePack(d1.CreateChangePack())
		require.NoError(t, err)
		pack, err := converter.FromChangePack(pb)
		require.NoError(t, err)

		d2 := document.New("test-doc")
		require.NoError(t, d2.ApplyChangePack(change.NewPack(
			pack.DocumentKey, change.NewCheckpoint(0, 0), pack.Changes, time.InitialVersionVector, nil,
		)))
		assert.Equal(t, treeXML(t, d1), treeXML(t, d2))
	})

	// Offsets 9 and 11 fall between the two code units of a surrogate pair. An
	// editor never places a caret there, but a replica whose strings can hold a
	// lone surrogate can, so such an edit can already sit in a stored history.
	// The edit has to apply and replay rather than fail, and it must not decode
	// the flag into U+FFFD on either side of the split.
	t.Run("a caret edit inside the flag applies, replays and keeps the flag", func(t *testing.T) {
		for _, tc := range []struct {
			offset int
			want   string
		}{
			// The split moved forward to the end of the pair it landed in, so
			// the "!" goes after that regional indicator. Both indicators
			// survive as themselves, and no text is dropped: the caret edit
			// stays a caret edit.
			{offset: 9, want: "<r><p>즐거운 한가위 🇰!🇷ㅇㄹ</p></r>"},
			{offset: 11, want: "<r><p>즐거운 한가위 🇰🇷!ㅇㄹ</p></r>"},
		} {
			offset := tc.offset
			d1 := document.New("test-doc")
			require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewTree("t", json.TreeNode{
					Type: "r",
					Children: []json.TreeNode{{
						Type:     "p",
						Children: []json.TreeNode{{Type: "text", Value: text}},
					}},
				})
				return nil
			}))
			require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetTree("t").EditByPath([]int{0, offset}, []int{0, offset},
					&json.TreeNode{Type: "text", Value: "!"}, 0)
				return nil
			}), "offset %d", offset)

			assert.Equal(t, tc.want, treeXML(t, d1), "offset %d", offset)
			assert.NotContains(t, treeXML(t, d1), "�", "offset %d", offset)

			pb, err := converter.ToChangePack(d1.CreateChangePack())
			require.NoError(t, err, "offset %d", offset)
			pack, err := converter.FromChangePack(pb)
			require.NoError(t, err, "offset %d", offset)

			d2 := document.New("test-doc")
			require.NoError(t, d2.ApplyChangePack(change.NewPack(
				pack.DocumentKey, change.NewCheckpoint(0, 0), pack.Changes, time.InitialVersionVector, nil,
			)), "offset %d", offset)
			assert.Equal(t, treeXML(t, d1), treeXML(t, d2), "offset %d", offset)
		}
	})

	t.Run("edits at every offset keep the length in UTF-16 code units", func(t *testing.T) {
		for offset := 1; offset < 14; offset++ {
			doc := document.New("test-doc")
			require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewTree("t", json.TreeNode{
					Type: "r",
					Children: []json.TreeNode{{
						Type:     "p",
						Children: []json.TreeNode{{Type: "text", Value: text}},
					}},
				})
				return nil
			}))
			require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetTree("t").EditByPath([]int{0, offset}, []int{0, offset}, &json.TreeNode{Type: "text", Value: "!"}, 0)
				return nil
			}), "offset %d", offset)

			p := treeCRDT(t, doc).Root().Index.Children()[0]
			assert.Equal(t, 15, p.Len(), "offset %d", offset)
			for _, child := range p.Children() {
				assert.Equal(t, child.Value.Length(), child.Len(), "offset %d: %q", offset, child.Value.Value)
			}
		}
	})
}

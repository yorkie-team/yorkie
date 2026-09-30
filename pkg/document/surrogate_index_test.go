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

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestRejectMidSurrogatePairIndexes verifies that Text and Tree operations
// reject indexes that split a UTF-16 surrogate pair.
func TestRejectMidSurrogatePairIndexes(t *testing.T) {
	t.Run("Text.Edit", func(t *testing.T) {
		doc := document.New("text-mid-surrogate")

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("text").Edit(0, 0, "😀x")
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, `{"text":[{"val":"😀x"}]}`, doc.Marshal())

		assert.PanicsWithError(
			t,
			crdt.ErrInvalidUTF16Index.Error(),
			func() {
				_ = doc.Update(func(root *json.Object, p *presence.Presence) error {
					root.GetText("text").Edit(1, 1, "y")
					return nil
				})
			},
		)

		assert.Equal(t, `{"text":[{"val":"😀x"}]}`, doc.Marshal())
	})

	t.Run("Tree.Edit", func(t *testing.T) {
		doc := document.New("tree-mid-surrogate")

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("tree", json.TreeNode{
				Type: "r",
				Children: []json.TreeNode{
					{
						Type: "p",
						Children: []json.TreeNode{
							{
								Type:  "text",
								Value: "😀x",
							},
						},
					},
				},
			})
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, "<r><p>😀x</p></r>", doc.Root().GetTree("tree").ToXML())

		assert.PanicsWithError(
			t,
			crdt.ErrInvalidUTF16Index.Error(),
			func() {
				_ = doc.Update(func(root *json.Object, p *presence.Presence) error {
					root.GetTree("tree").Edit(2, 2, &json.TreeNode{
						Type:  "text",
						Value: "y",
					}, 0)
					return nil
				})
			},
		)

		assert.Equal(t, "<r><p>😀x</p></r>", doc.Root().GetTree("tree").ToXML())
	})

	t.Run("Text.Style", func(t *testing.T) {
		doc := document.New("text-style-mid-surrogate")

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("text").Edit(0, 0, "😀x")
			return nil
		})
		require.NoError(t, err)

		expected := doc.Marshal()

		assert.PanicsWithError(
			t,
			crdt.ErrInvalidUTF16Index.Error(),
			func() {
				_ = doc.Update(func(root *json.Object, p *presence.Presence) error {
					root.GetText("text").Style(
						0,
						1,
						map[string]string{"bold": "true"},
					)
					return nil
				})
			},
		)

		assert.Equal(t, expected, doc.Marshal())
	})

	t.Run("Tree.Style", func(t *testing.T) {
		doc := document.New("tree-style-mid-surrogate")

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("tree", json.TreeNode{
				Type: "r",
				Children: []json.TreeNode{
					{
						Type: "p",
						Children: []json.TreeNode{
							{
								Type:  "text",
								Value: "😀x",
							},
						},
					},
				},
			})
			return nil
		})
		require.NoError(t, err)

		expected := doc.Marshal()

		assert.PanicsWithError(
			t,
			crdt.ErrInvalidUTF16Index.Error(),
			func() {
				_ = doc.Update(func(root *json.Object, p *presence.Presence) error {
					root.GetTree("tree").Style(
						1,
						2,
						map[string]string{"bold": "true"},
					)
					return nil
				})
			},
		)

		assert.Equal(t, expected, doc.Marshal())
	})

	t.Run("valid UTF-16 boundaries remain editable", func(t *testing.T) {
		tests := []struct {
			name     string
			index    int
			expected string
		}{
			{
				name:     "before surrogate pair",
				index:    0,
				expected: `{"text":[{"val":"y"},{"val":"😀x"}]}`,
			},
			{
				name:     "after surrogate pair",
				index:    2,
				expected: `{"text":[{"val":"😀"},{"val":"y"},{"val":"x"}]}`,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				doc := document.New(helper.TestKey(t))

				err := doc.Update(func(
					root *json.Object,
					p *presence.Presence,
				) error {
					root.SetNewText("text").Edit(0, 0, "😀x")
					return nil
				})
				require.NoError(t, err)

				assert.NotPanics(t, func() {
					err = doc.Update(func(
						root *json.Object,
						p *presence.Presence,
					) error {
						root.GetText("text").Edit(tc.index, tc.index, "y")
						return nil
					})
				})
				require.NoError(t, err)
				assert.Equal(t, tc.expected, doc.Marshal())
			})
		}
	})
}

// //go:build integration

/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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

package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestTree(t *testing.T) {

	t.Run("tree", func(t *testing.T) {
		d1 := document.New(helper.TestDocKey(t))
		err := d1.Update(func(root *json.Object) error {
			// 01. Create a tree and insert a paragraph.
			root.SetNewTree("t").Edit(0, 0, &crdt.JSONTreeNode{
				Type:     "p",
				Children: []crdt.JSONTreeNode{},
			})
			assert.Equal(t, "<root><p></p></root>", root.GetTree("t").ToXML())
			assert.Equal(t, `{"t":{"type":"root","children":[{"type":"p","children":[]}]}}`, root.Marshal())

			// 02. Create a text into the paragraph.
			root.GetTree("t").Edit(1, 1, &crdt.JSONTreeNode{
				Type:  "text",
				Value: "AB",
			})
			assert.Equal(t, "<root><p>AB</p></root>", root.GetTree("t").ToXML())
			assert.Equal(
				t,
				`{"t":{"type":"root","children":[{"type":"p","children":[{"type":"text","value":"AB"}]}]}}`,
				root.Marshal(),
			)

			// 03. Insert a text into the paragraph.
			root.GetTree("t").Edit(3, 3, &crdt.JSONTreeNode{
				Type:  "text",
				Value: "CD",
			})
			assert.Equal(t, "<root><p>ABCD</p></root>", root.GetTree("t").ToXML())

			// TODO(krapie): consider other options to avoid line over
			text1 := `{"t":{"type":"root","children":[{"type":"p","children"`
			text2 := `:[{"type":"text","value":"AB"},{"type":"text","value":"CD"}]}]}}`
			assert.Equal(
				t,
				text1+text2,
				root.Marshal(),
			)

			// 04. Replace ABCD with Yorkie
			root.GetTree("t").Edit(1, 5, &crdt.JSONTreeNode{
				Type:  "text",
				Value: "Yorkie",
			})
			assert.Equal(t, "<root><p>Yorkie</p></root>", root.GetTree("t").ToXML())
			assert.Equal(
				t,
				`{"t":{"type":"root","children":[{"type":"p","children":[{"type":"text","value":"Yorkie"}]}]}}`,
				root.Marshal(),
			)

			return nil
		})
		assert.NoError(t, err)
	})

	t.Run("created from JSON test", func(t *testing.T) {
		d1 := document.New(helper.TestDocKey(t))
		err := d1.Update(func(root *json.Object) error {
			root.SetNewTree("t", &crdt.JSONTreeNode{
				Type: "doc",
				Children: []crdt.JSONTreeNode{{
					Type:     "p",
					Children: []crdt.JSONTreeNode{{Type: "text", Value: "ab"}},
				}, {
					Type: "ng",
					Children: []crdt.JSONTreeNode{
						{Type: "note", Children: []crdt.JSONTreeNode{{Type: "text", Value: "cd"}}},
						{Type: "note", Children: []crdt.JSONTreeNode{{Type: "text", Value: "ef"}}},
					},
				}, {
					Type:     "bp",
					Children: []crdt.JSONTreeNode{{Type: "text", Value: "gh"}},
				}},
			})
			assert.Equal(t, "<doc><p>ab</p><ng><note>cd</note><note>ef</note></ng><bp>gh</bp></doc>", root.GetTree("t").ToXML())
			assert.Equal(t, 18, root.GetTree("t").Len())
			// TODO(krapie): add listEqual test later
			return nil
		})
		assert.NoError(t, err)
	})

	t.Run("edit its content test", func(t *testing.T) {
		d1 := document.New(helper.TestDocKey(t))
		err := d1.Update(func(root *json.Object) error {
			root.SetNewTree("t", &crdt.JSONTreeNode{
				Type: "doc",
				Children: []crdt.JSONTreeNode{{
					Type:     "p",
					Children: []crdt.JSONTreeNode{{Type: "text", Value: "ab"}},
				}},
			})
			assert.Equal(t, "<doc><p>ab</p></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(1, 1, &crdt.JSONTreeNode{
				Type:  "text",
				Value: "X",
			})
			assert.Equal(t, "<doc><p>Xab</p></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(1, 2, nil)
			assert.Equal(t, "<doc><p>ab</p></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(2, 2, &crdt.JSONTreeNode{
				Type:  "text",
				Value: "X",
			})
			assert.Equal(t, "<doc><p>aXb</p></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(2, 3, nil)
			assert.Equal(t, "<doc><p>ab</p></doc>", root.GetTree("t").ToXML())

			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, "<doc><p>ab</p></doc>", d1.Root().GetTree("t").ToXML())

		err = d1.Update(func(root *json.Object) error {
			root.SetNewTree("t", &crdt.JSONTreeNode{
				Type: "doc",
				Children: []crdt.JSONTreeNode{{
					Type:     "p",
					Children: []crdt.JSONTreeNode{{Type: "text", Value: "ab"}},
				}},
			})
			assert.Equal(t, "<doc><p>ab</p></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(3, 3, &crdt.JSONTreeNode{
				Type:  "text",
				Value: "X",
			})
			assert.Equal(t, "<doc><p>abX</p></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(3, 4, nil)
			assert.Equal(t, "<doc><p>ab</p></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(2, 3, nil)
			assert.Equal(t, "<doc><p>a</p></doc>", root.GetTree("t").ToXML())

			return nil
		})
		assert.NoError(t, err)
	})

	t.Run("subscribed by handler test", func(t *testing.T) {
		// TODO(krapie): add this test case later
	})

	t.Run("insert inline content to the same position(left) concurrently test", func(t *testing.T) {
		// TODO(krapie): add this test after implementing API
	})
}

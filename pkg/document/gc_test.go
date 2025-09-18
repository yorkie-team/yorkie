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

package document_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestTreeGC(t *testing.T) {
	type opCode int
	const (
		NoOp opCode = iota
		Style
		RemoveStyle
		DeleteNode
		GC
	)

	type operation struct {
		code opCode
		key  string
		val  string
	}

	type step struct {
		op         operation
		garbageLen int
		expectXML  string
	}

	tests := []struct {
		desc  string
		steps []step
	}{
		{
			desc: "style-style test",
			steps: []step{
				{operation{Style, "b", "t"}, 0, `<r><p b="t"></p></r>`},
				{operation{Style, "b", "f"}, 0, `<r><p b="f"></p></r>`},
			},
		},
		{
			desc: "style-remove test",
			steps: []step{
				{operation{Style, "b", "t"}, 0, `<r><p b="t"></p></r>`},
				{operation{RemoveStyle, "b", ""}, 1, `<r><p></p></r>`},
			},
		},
		{
			desc: "remove-style test",
			steps: []step{
				{operation{RemoveStyle, "b", ""}, 1, `<r><p></p></r>`},
				{operation{Style, "b", "t"}, 0, `<r><p b="t"></p></r>`},
			},
		},
		{
			desc: "remove-remove test",
			steps: []step{
				{operation{RemoveStyle, "b", ""}, 1, `<r><p></p></r>`},
				{operation{RemoveStyle, "b", ""}, 1, `<r><p></p></r>`},
			},
		},
		{
			desc: "style-delete test",
			steps: []step{
				{operation{Style, "b", "t"}, 0, `<r><p b="t"></p></r>`},
				{operation{DeleteNode, "", ""}, 1, `<r></r>`},
			},
		},
		{
			desc: "remove-delete test",
			steps: []step{
				{operation{RemoveStyle, "b", ""}, 1, `<r><p></p></r>`},
				{operation{DeleteNode, "b", "t"}, 2, `<r></r>`},
			},
		},
		{
			desc: "remove-gc-delete test",
			steps: []step{
				{operation{RemoveStyle, "b", ""}, 1, `<r><p></p></r>`},
				{operation{GC, "", ""}, 0, `<r><p></p></r>`},
				{operation{DeleteNode, "b", "t"}, 1, `<r></r>`},
			},
		},
	}

	for i, tc := range tests {
		t.Run(fmt.Sprintf("%d. %s", i+1, tc.desc), func(t *testing.T) {
			// 01. Initial: <r><p></p></r>
			doc := document.New("doc")
			err := doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewTree("t", json.TreeNode{
					Type:     "r",
					Children: []json.TreeNode{{Type: "p"}},
				})
				return nil
			})
			assert.NoError(t, err)
			assert.Equal(t, "<r><p></p></r>", doc.Root().GetTree("t").ToXML())
			assert.Equal(t, 0, doc.GarbageLen())

			// 02. Run test steps
			for _, s := range tc.steps {
				assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
					if s.op.code == RemoveStyle {
						root.GetTree("t").RemoveStyle(0, 1, []string{s.op.key})
					} else if s.op.code == Style {
						root.GetTree("t").Style(0, 1, map[string]string{s.op.key: s.op.val})
					} else if s.op.code == DeleteNode {
						root.GetTree("t").Edit(0, 2, nil, 0)
					} else if s.op.code == GC {
						doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID()))
					}
					return nil
				}))
				assert.Equal(t, s.expectXML, doc.Root().GetTree("t").ToXML())
				assert.Equal(t, s.garbageLen, doc.GarbageLen())
			}

			// 03. Garbage collect
			doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID()))
			assert.Equal(t, 0, doc.GarbageLen())
		})
	}
}

func TestTextGC(t *testing.T) {
	type opCode int
	const (
		NoOp opCode = iota
		Style
		DeleteNode
		GC
	)

	type operation struct {
		code opCode
		key  string
		val  string
	}

	type step struct {
		op         operation
		garbageLen int
		expectXML  string
	}

	tests := []struct {
		desc  string
		steps []step
	}{
		{
			desc: "style-style test",
			steps: []step{
				{operation{Style, "b", "t"}, 0, `[{"attrs":{"b":"t"},"val":"AB"}]`},
				{operation{Style, "b", "f"}, 0, `[{"attrs":{"b":"f"},"val":"AB"}]`},
			},
		},
		{
			desc: "style-delete test",
			steps: []step{
				{operation{Style, "b", "t"}, 0, `[{"attrs":{"b":"t"},"val":"AB"}]`},
				{operation{DeleteNode, "", ""}, 1, `[]`},
			},
		},
	}

	for i, tc := range tests {
		t.Run(fmt.Sprintf("%d. %s", i+1, tc.desc), func(t *testing.T) {
			doc := document.New("doc")
			err := doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewText("t").Edit(0, 0, "AB")
				return nil
			})
			assert.NoError(t, err)
			assert.Equal(t, `[{"val":"AB"}]`, doc.Root().GetText("t").Marshal())
			assert.Equal(t, 0, doc.GarbageLen())

			// 02. Run test steps
			for _, s := range tc.steps {
				assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
					if s.op.code == Style {
						root.GetText("t").Style(0, 2, map[string]string{s.op.key: s.op.val})
					} else if s.op.code == DeleteNode {
						root.GetText("t").Edit(0, 2, "")
					} else if s.op.code == GC {
						doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID()))
					}
					return nil
				}))
				assert.Equal(t, s.expectXML, doc.Root().GetText("t").Marshal())
				assert.Equal(t, s.garbageLen, doc.GarbageLen())
			}

			// 03. Garbage collect
			doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID()))
			assert.Equal(t, 0, doc.GarbageLen())
		})
	}
}

func TestTextGCSize(t *testing.T) {
	t.Run("should update gc size correctly after text garbage collection", func(t *testing.T) {
		doc := document.New("test-doc")

		// Initial state
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("text")
			return nil
		})
		assert.NoError(t, err)

		initialSize := doc.DocSize()
		assert.Equal(t, 0, initialSize.GC.Data)
		assert.Equal(t, 0, initialSize.GC.Meta)

		// Add and then remove text to create garbage
		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(0, 0, "Hello world")
			return nil
		})
		assert.NoError(t, err)

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(6, 11, "")
			return nil
		})
		assert.NoError(t, err)

		sizeBeforeGC := doc.DocSize()
		assert.Equal(t, 10, sizeBeforeGC.GC.Data)
		assert.Equal(t, 48, sizeBeforeGC.GC.Meta)
		assert.Equal(t, 1, doc.GarbageLen())

		// Perform garbage collection
		collected := doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID()))
		assert.Equal(t, 1, collected)

		// Verify gc size is properly reset after collection
		sizeAfterGC := doc.DocSize()
		assert.Equal(t, 0, sizeAfterGC.GC.Data)
		assert.Equal(t, 0, sizeAfterGC.GC.Meta)
		assert.Equal(t, 0, doc.GarbageLen())
	})

	t.Run("should update gc size correctly after multiple text operations and gc", func(t *testing.T) {
		doc := document.New("test-doc")

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("text")
			return nil
		})
		assert.NoError(t, err)

		// Create multiple text segments and then remove some
		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(0, 0, "ABC")
			return nil
		})
		assert.NoError(t, err)

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(1, 2, "X")
			return nil
		})
		assert.NoError(t, err)

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(2, 3, "")
			return nil
		})
		assert.NoError(t, err)

		sizeBeforeGC := doc.DocSize()
		garbageLen := doc.GarbageLen()

		assert.Equal(t, 4, sizeBeforeGC.GC.Data)
		assert.Equal(t, 96, sizeBeforeGC.GC.Meta)
		assert.Equal(t, 2, garbageLen) // B and C should be garbage

		// Perform garbage collection
		collected := doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID()))
		assert.Equal(t, garbageLen, collected)

		// Verify all gc size is cleared
		sizeAfterGC := doc.DocSize()
		assert.Equal(t, 0, sizeAfterGC.GC.Data)
		assert.Equal(t, 0, sizeAfterGC.GC.Meta)
		assert.Equal(t, 0, doc.GarbageLen())
	})
}

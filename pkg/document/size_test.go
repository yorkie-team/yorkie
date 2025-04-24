/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/resource"
)

func TestDocumentSize(t *testing.T) {
	t.Run("text test", func(t *testing.T) {
		doc := document.New("doc")

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("text")

			return nil
		}))
		assert.Equal(t, doc.DocSize().Live, resource.DataSize{Data: 0, Meta: 72})
		assert.Equal(t, doc.DocSize().GC, resource.DataSize{Data: 0, Meta: 0})

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(0, 0, "helloworld")

			return nil
		}))
		assert.Equal(t, doc.DocSize().Live, resource.DataSize{Data: 20, Meta: 96})
		assert.Equal(t, doc.DocSize().GC, resource.DataSize{Data: 0, Meta: 0})

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(5, 5, " ")

			return nil
		}))
		assert.Equal(t, doc.DocSize().Live, resource.DataSize{Data: 22, Meta: 144})
		assert.Equal(t, doc.DocSize().GC, resource.DataSize{Data: 0, Meta: 0})

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(6, 11, "")

			return nil
		}))
		assert.Equal(t, doc.DocSize().Live, resource.DataSize{Data: 12, Meta: 120})
		assert.Equal(t, doc.DocSize().GC, resource.DataSize{Data: 10, Meta: 48})

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Style(0, 5, map[string]string{"bold": "true"})

			return nil
		}))
		assert.Equal(t, doc.DocSize().Live, resource.DataSize{Data: 28, Meta: 144})
		assert.Equal(t, doc.DocSize().GC, resource.DataSize{Data: 10, Meta: 48})
	})

	t.Run("tree test", func(t *testing.T) {
		doc := document.New("doc")

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("tree", &json.TreeNode{
				Type:     "doc",
				Children: []json.TreeNode{{Type: "p", Children: []json.TreeNode{}}},
			})
			return nil
		}))
		assert.Equal(t, `<doc><p></p></doc>`, doc.Root().GetTree("tree").ToXML())
		assert.Equal(t, doc.DocSize().Live, resource.DataSize{Data: 0, Meta: 120})
		assert.Equal(t, doc.DocSize().GC, resource.DataSize{Data: 0, Meta: 0})

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("tree").Edit(1, 1, &json.TreeNode{Type: "text", Value: "helloworld"}, 0)

			return nil
		}))
		assert.Equal(t, doc.DocSize().Live, resource.DataSize{Data: 20, Meta: 144})
		assert.Equal(t, doc.DocSize().GC, resource.DataSize{Data: 0, Meta: 0})

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("tree").Edit(1, 7, &json.TreeNode{Type: "text", Value: "w"}, 0)
			return nil
		}))
		assert.Equal(t, `<doc><p>world</p></doc>`, doc.Root().GetTree("tree").ToXML())
		assert.Equal(t, doc.DocSize().Live, resource.DataSize{Data: 10, Meta: 168})
		assert.Equal(t, doc.DocSize().GC, resource.DataSize{Data: 12, Meta: 48})

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("tree").Edit(7, 7, &json.TreeNode{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "abcd"}},
			}, 0)
			return nil
		}))
		assert.Equal(t, `<doc><p>world</p><p>abcd</p></doc>`, doc.Root().GetTree("tree").ToXML())
		assert.Equal(t, doc.DocSize().Live, resource.DataSize{Data: 18, Meta: 216})
		assert.Equal(t, doc.DocSize().GC, resource.DataSize{Data: 12, Meta: 48})

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("tree").Edit(7, 13, nil, 0)
			return nil
		}))
		assert.Equal(t, `<doc><p>world</p></doc>`, doc.Root().GetTree("tree").ToXML())
		assert.Equal(t, doc.DocSize().Live, resource.DataSize{Data: 10, Meta: 168})
		assert.Equal(t, doc.DocSize().GC, resource.DataSize{Data: 20, Meta: 144})

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("tree").Style(0, 7, map[string]string{"bold": "true"})
			return nil
		}))
		assert.Equal(t, `<doc><p bold="true">world</p></doc>`, doc.Root().GetTree("tree").ToXML())
		assert.Equal(t, doc.DocSize().Live, resource.DataSize{Data: 26, Meta: 192})
		assert.Equal(t, doc.DocSize().GC, resource.DataSize{Data: 20, Meta: 144})

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("tree").RemoveStyle(0, 7, []string{"bold"})
			return nil
		}))
		assert.Equal(t, `<doc><p>world</p></doc>`, doc.Root().GetTree("tree").ToXML())
		assert.Equal(t, doc.DocSize().Live, resource.DataSize{Data: 10, Meta: 168})
		assert.Equal(t, doc.DocSize().GC, resource.DataSize{Data: 36, Meta: 168})
	})

}

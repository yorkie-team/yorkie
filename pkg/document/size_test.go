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
	"math"
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/resource"
)

var (
	dummyTreeNodeID = &crdt.TreeNodeID{
		CreatedAt: time.InitialTicket,
		Offset:    0,
	}
)

func TestTreeNodeSize(t *testing.T) {
	t.Run("split tree node test", func(t *testing.T) {
		root := crdt.NewTreeNode(dummyTreeNodeID, "r", nil)
		para := crdt.NewTreeNode(dummyTreeNodeID, "p", nil)
		assert.NoError(t, root.Append(para))
		assert.NoError(t, para.Append(crdt.NewTreeNode(dummyTreeNodeID, "text", nil, "helloworld")))

		// split text node
		left, err := para.Child(0)
		assert.NoError(t, err)
		right, diff, err := left.SplitText(5, 0)
		assert.NoError(t, err)
		assert.Equal(t, resource.DataSize{Data: 0, Meta: 24}, diff)
		assert.Equal(t, resource.DataSize{Data: 10, Meta: 24}, left.DataSize())
		assert.Equal(t, resource.DataSize{Data: 10, Meta: 24}, right.DataSize())

		// split element node
		right, diff, err = para.SplitElement(1, func() *time.Ticket {
			return time.InitialTicket
		})
		assert.NoError(t, err)
		assert.Equal(t, resource.DataSize{Data: 0, Meta: 24}, diff)
		assert.Equal(t, "<p>hello</p>", crdt.ToXML(para))
		assert.Equal(t, "<p>world</p>", crdt.ToXML(right))
	})

	t.Run("split tree node with attribute test", func(t *testing.T) {
		t.Skip("TODO(raararaara): We need to check if the attributes are copied correctly when splitting elements.")
		attributes := crdt.NewRHT()
		attributes.Set("bold", "true", time.InitialTicket)

		root := crdt.NewTreeNode(dummyTreeNodeID, "r", nil)
		para := crdt.NewTreeNode(dummyTreeNodeID, "p", attributes)
		assert.NoError(t, root.Append(para))
		assert.NoError(t, para.Append(crdt.NewTreeNode(dummyTreeNodeID, "text", nil, "helloworld")))
		assert.Equal(t, `<r><p bold="true">helloworld</p></r>`, crdt.ToXML(root))

		// split text node
		left, err := para.Child(0)
		assert.NoError(t, err)
		right, diff, err := left.SplitText(5, 0)
		assert.NoError(t, err)
		assert.Equal(t, resource.DataSize{Data: 0, Meta: 24}, diff)
		assert.Equal(t, resource.DataSize{Data: 10, Meta: 24}, left.DataSize())
		assert.Equal(t, resource.DataSize{Data: 10, Meta: 24}, right.DataSize())

		// split element node
		right, diff, err = para.SplitElement(1, func() *time.Ticket {
			return time.InitialTicket
		})
		assert.NoError(t, err)
		assert.Equal(t, resource.DataSize{Data: 16, Meta: 48}, diff)
		assert.Equal(t, `<p bold="true">hello</p>`, crdt.ToXML(para))
		assert.Equal(t, `<p bold="true">world</p>`, crdt.ToXML(right))
	})
}

func TestDocumentSize(t *testing.T) {
	t.Run("primitive and object test", func(t *testing.T) {
		doc := document.New("doc")
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNull("k0")
			return nil
		}))
		assert.Equal(t, resource.DataSize{Data: 8, Meta: 72}, doc.DocSize().Live)

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetBool("k1", true)
			return nil
		}))
		assert.Equal(t, resource.DataSize{Data: 12, Meta: 120}, doc.DocSize().Live)

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetInteger("k2", 2147483647)
			return nil
		}))
		assert.Equal(t, resource.DataSize{Data: 16, Meta: 168}, doc.DocSize().Live)

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetLong("k3", math.MaxInt64)
			return nil
		}))
		assert.Equal(t, resource.DataSize{Data: 24, Meta: 216}, doc.DocSize().Live)

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetDouble("k4", 1.79)
			return nil
		}))
		assert.Equal(t, resource.DataSize{Data: 32, Meta: 264}, doc.DocSize().Live)

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetString("k5", "4")
			return nil
		}))
		assert.Equal(t, resource.DataSize{Data: 34, Meta: 312}, doc.DocSize().Live)

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetBytes("k6", []byte{65, 66})
			return nil
		}))
		assert.Equal(t, resource.DataSize{Data: 36, Meta: 360}, doc.DocSize().Live)

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetDate("k7", gotime.Now())
			return nil
		}))
		assert.Equal(t, resource.DataSize{Data: 44, Meta: 408}, doc.DocSize().Live)
	})

	t.Run("array test", func(t *testing.T) {
		doc := document.New("doc")

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("arr")
			return nil
		}))
		assert.Equal(t, resource.DataSize{Data: 0, Meta: 72}, doc.DocSize().Live)

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetArray("arr").AddString("a")
			return nil
		}))
		assert.Equal(t, resource.DataSize{Data: 2, Meta: 96}, doc.DocSize().Live)
		assert.Equal(t, resource.DataSize{Data: 0, Meta: 0}, doc.DocSize().GC)

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetArray("arr").Delete(0)
			return nil
		}))
		assert.Equal(t, resource.DataSize{Data: 0, Meta: 72}, doc.DocSize().Live)
		assert.Equal(t, resource.DataSize{Data: 2, Meta: 48}, doc.DocSize().GC)
	})

	t.Run("counter test", func(t *testing.T) {
		doc := document.New("doc")

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewCounter("counter", crdt.IntegerCnt, 0)
			return nil
		}))
		assert.Equal(t, resource.DataSize{Data: 4, Meta: 72}, doc.DocSize().Live)
	})

	t.Run("text test", func(t *testing.T) {
		doc := document.New("doc")

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("text")
			return nil
		}))
		assert.Equal(t, resource.DataSize{Data: 0, Meta: 72}, doc.DocSize().Live)
		assert.Equal(t, resource.DataSize{Data: 0, Meta: 0}, doc.DocSize().GC)

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(0, 0, "helloworld")
			return nil
		}))
		assert.Equal(t, resource.DataSize{Data: 20, Meta: 96}, doc.DocSize().Live)
		assert.Equal(t, resource.DataSize{Data: 0, Meta: 0}, doc.DocSize().GC)

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(5, 5, " ")
			return nil
		}))
		assert.Equal(t, resource.DataSize{Data: 22, Meta: 144}, doc.DocSize().Live)
		assert.Equal(t, resource.DataSize{Data: 0, Meta: 0}, doc.DocSize().GC)

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(6, 11, "")
			return nil
		}))
		assert.Equal(t, resource.DataSize{Data: 12, Meta: 120}, doc.DocSize().Live)
		assert.Equal(t, resource.DataSize{Data: 10, Meta: 48}, doc.DocSize().GC)

		clone, err := doc.InternalDocument().DeepCopy()
		assert.NoError(t, err)
		assert.Equal(t, doc.DocSize(), clone.DocSize())

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Style(0, 5, map[string]string{"bold": "true"})
			return nil
		}))
		assert.Equal(t, `{"text":[{"attrs":{"bold":"true"},"val":"hello"},{"val":" "}]}`, doc.Marshal())
		assert.Equal(t, resource.DataSize{Data: 28, Meta: 144}, doc.DocSize().Live)
		assert.Equal(t, resource.DataSize{Data: 10, Meta: 48}, doc.DocSize().GC)

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(1, 1, "")
			return nil
		}))
		assert.Equal(t,
			`{"text":[{"attrs":{"bold":"true"},"val":"h"},{"attrs":{"bold":"true"},"val":"ello"},{"val":" "}]}`,
			doc.Marshal(),
		)
		assert.Equal(t, resource.DataSize{Data: 44, Meta: 192}, doc.DocSize().Live)
		assert.Equal(t, resource.DataSize{Data: 10, Meta: 48}, doc.DocSize().GC)
	})

	t.Run("tree test", func(t *testing.T) {
		doc := document.New("doc")

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("tree", json.TreeNode{
				Type:     "doc",
				Children: []json.TreeNode{{Type: "p", Children: []json.TreeNode{}}},
			})
			return nil
		}))
		assert.Equal(t, `<doc><p></p></doc>`, doc.Root().GetTree("tree").ToXML())
		assert.Equal(t, resource.DataSize{Data: 0, Meta: 120}, doc.DocSize().Live)
		assert.Equal(t, resource.DataSize{Data: 0, Meta: 0}, doc.DocSize().GC)

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("tree").Edit(1, 1, &json.TreeNode{Type: "text", Value: "helloworld"}, 0)

			return nil
		}))
		assert.Equal(t, resource.DataSize{Data: 20, Meta: 144}, doc.DocSize().Live)
		assert.Equal(t, resource.DataSize{Data: 0, Meta: 0}, doc.DocSize().GC)

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("tree").Edit(1, 7, &json.TreeNode{Type: "text", Value: "w"}, 0)
			return nil
		}))
		assert.Equal(t, `<doc><p>world</p></doc>`, doc.Root().GetTree("tree").ToXML())
		assert.Equal(t, resource.DataSize{Data: 10, Meta: 168}, doc.DocSize().Live)
		assert.Equal(t, resource.DataSize{Data: 12, Meta: 48}, doc.DocSize().GC)

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("tree").Edit(7, 7, &json.TreeNode{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "abcd"}},
			}, 0)
			return nil
		}))
		assert.Equal(t, `<doc><p>world</p><p>abcd</p></doc>`, doc.Root().GetTree("tree").ToXML())
		assert.Equal(t, resource.DataSize{Data: 18, Meta: 216}, doc.DocSize().Live)
		assert.Equal(t, resource.DataSize{Data: 12, Meta: 48}, doc.DocSize().GC)

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("tree").Edit(7, 13, nil, 0)
			return nil
		}))
		assert.Equal(t, `<doc><p>world</p></doc>`, doc.Root().GetTree("tree").ToXML())
		assert.Equal(t, resource.DataSize{Data: 10, Meta: 168}, doc.DocSize().Live)
		assert.Equal(t, resource.DataSize{Data: 20, Meta: 144}, doc.DocSize().GC)

		clone, err := doc.InternalDocument().DeepCopy()
		assert.NoError(t, err)
		assert.Equal(t, doc.DocSize(), clone.DocSize())

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("tree").Style(0, 7, map[string]string{"bold": "true"})
			return nil
		}))
		assert.Equal(t, `<doc><p bold="true">world</p></doc>`, doc.Root().GetTree("tree").ToXML())
		assert.Equal(t, resource.DataSize{Data: 26, Meta: 192}, doc.DocSize().Live)
		assert.Equal(t, resource.DataSize{Data: 20, Meta: 144}, doc.DocSize().GC)

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("tree").RemoveStyle(0, 7, []string{"bold"})
			return nil
		}))
		assert.Equal(t, `<doc><p>world</p></doc>`, doc.Root().GetTree("tree").ToXML())
		assert.Equal(t, resource.DataSize{Data: 10, Meta: 168}, doc.DocSize().Live)
		assert.Equal(t, resource.DataSize{Data: 36, Meta: 168}, doc.DocSize().GC)
	})

	t.Run("gc test", func(t *testing.T) {
		doc := document.New("doc")

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetInteger("num", 1)
			root.SetString("str", "hello")
			return nil
		}))
		assert.Equal(t, resource.DataSize{Data: 14, Meta: 120}, doc.DocSize().Live)

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("num")
			return nil
		}))
		assert.Equal(t, resource.DataSize{Data: 10, Meta: 72}, doc.DocSize().Live)
		assert.Equal(t, resource.DataSize{Data: 4, Meta: 72}, doc.DocSize().GC)
	})

	t.Run("deep copy test", func(t *testing.T) {
		doc := document.New("doc")

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewCounter("counter", crdt.IntegerCnt, 0)
			return nil
		}))
		clone, err := doc.InternalDocument().DeepCopy()
		assert.NoError(t, err)
		assert.Equal(t, doc.DocSize(), clone.DocSize())
	})

	t.Run("deep copy for nested element test", func(t *testing.T) {
		doc := document.New("doc")

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("arr")
			return nil
		}))
		assert.Equal(t, resource.DataSize{Data: 0, Meta: 72}, doc.DocSize().Live)

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetArray("arr").AddNewCounter(crdt.IntegerCnt, 0)
			return nil
		}))

		clone, err := doc.InternalDocument().DeepCopy()
		assert.NoError(t, err)
		assert.Equal(t, doc.DocSize(), clone.DocSize())
	})
}

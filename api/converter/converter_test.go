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

package converter_test

import (
	"math"
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/converter"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/innerpresence"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestConverter(t *testing.T) {
	t.Run("snapshot simple test", func(t *testing.T) {
		doc := document.New("d1")
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("k1").Edit(0, 0, "A")
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[{"val":"A"}]}`, doc.Marshal())

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("k1").Edit(0, 0, "B")
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[{"val":"B"}]}`, doc.Marshal())

		bytes, err := converter.ObjectToBytes(doc.RootObject())
		assert.NoError(t, err)

		obj, err := converter.BytesToObject(bytes)
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[{"val":"B"}]}`, obj.Marshal())
	})

	t.Run("root snapshot test", func(t *testing.T) {
		doc := document.New("d1")

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			// an object and primitive types
			root.SetNewObject("k1").
				SetNull("k1.0").
				SetBool("k1.1", true).
				SetInteger("k1.2", 2147483647).
				SetLong("k1.3", 9223372036854775807).
				SetDouble("1.4", 1.79).
				SetString("k1.5", "4").
				SetBytes("k1.6", []byte{65, 66}).
				SetDate("k1.7", gotime.Now()).
				Delete("k1.5")

			// an array
			root.SetNewArray("k2").
				AddNull().
				AddBool(true).
				AddInteger(1).
				AddLong(2).
				AddDouble(3.0).
				AddString("4").
				AddBytes([]byte{65}).
				AddDate(gotime.Now()).
				Delete(4)

			// plain text
			root.SetNewText("k3").
				Edit(0, 0, "ㅎ").
				Edit(0, 1, "하").
				Edit(0, 1, "한").
				Edit(0, 1, "하").
				Edit(1, 1, "느").
				Edit(1, 2, "늘").
				Edit(2, 2, "구름").
				Edit(2, 3, "뭉게구")

			// rich text
			root.SetNewText("k4").
				Edit(0, 0, "Hello world", nil).
				Edit(6, 11, "sky", nil).
				Style(0, 5, map[string]string{"b": "1"})

			// a counter
			root.SetNewCounter("k5", crdt.LongCnt, 0).
				Increase(10).
				Increase(math.MaxInt64)

			// tree
			root.SetNewTree("k5").
				Edit(0, 0, &json.TreeNode{
					Type: "p",
					Children: []json.TreeNode{{
						Type:  "text",
						Value: "Hello world",
					}},
				}, 0)

			return nil
		})
		assert.NoError(t, err)

		bytes, err := converter.ObjectToBytes(doc.RootObject())
		assert.NoError(t, err)

		obj, err := converter.BytesToObject(bytes)
		assert.NoError(t, err)
		assert.Equal(t, doc.Marshal(), obj.Marshal())
	})

	t.Run("change pack test", func(t *testing.T) {
		d1 := document.New("d1")

		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
			// an object and primitive types
			root.SetNewObject("k1").
				SetBool("k1.1", true).
				SetInteger("k1.2", 2147483647).
				SetLong("k1.3", 9223372036854775807).
				SetDouble("1.4", 1.79).
				SetString("k1.5", "4").
				SetBytes("k1.6", []byte{65, 66}).
				SetDate("k1.7", gotime.Now()).
				Delete("k1.5")

			// an array
			root.SetNewArray("k2").
				AddBool(true).
				AddInteger(1).
				AddLong(2).
				AddDouble(3.0).
				AddString("4").
				AddBytes([]byte{65}).
				AddDate(gotime.Now()).
				Delete(4)

			nextCreatedAt := root.GetArray("k2").Get(0).CreatedAt()
			targetCreatedAt := root.GetArray("k2").Get(1).CreatedAt()
			root.GetArray("k2").MoveBefore(nextCreatedAt, targetCreatedAt)

			// plain text
			root.SetNewText("k3").
				Edit(0, 0, "ㅎ").
				Edit(0, 1, "하").
				Edit(0, 1, "한").
				Edit(0, 1, "하").
				Edit(1, 1, "느").
				Edit(1, 2, "늘")

			// rich text
			root.SetNewText("k3").
				Edit(0, 0, "Hello World", nil).
				Style(0, 5, map[string]string{"b": "1"})

			// counter
			root.SetNewCounter("k4", crdt.IntegerCnt, 0).Increase(5)

			return nil
		})
		assert.NoError(t, err)

		pbPack, err := converter.ToChangePack(d1.CreateChangePack())
		assert.NoError(t, err)

		pack, err := converter.FromChangePack(pbPack)
		assert.NoError(t, err)
		pack.MinSyncedTicket = time.MaxTicket

		d2 := document.New("d1")
		err = d2.ApplyChangePack(pack)
		assert.NoError(t, err)

		assert.Equal(t, d1.Marshal(), d2.Marshal())
	})

	t.Run("change pack error test", func(t *testing.T) {
		_, err := converter.FromChangePack(nil)
		assert.ErrorIs(t, err, converter.ErrPackRequired)

		_, err = converter.FromChangePack(&api.ChangePack{})
		assert.ErrorIs(t, err, converter.ErrCheckpointRequired)
	})

	t.Run("tree converting test", func(t *testing.T) {
		root := helper.BuildTreeNode(&json.TreeNode{
			Type: "r",
			Children: []json.TreeNode{
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "hello"}}},
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "world"}}},
			},
		})

		pbNodes := converter.ToTreeNodes(root)
		clone, err := converter.FromTreeNodes(pbNodes)
		assert.NoError(t, err)
		assert.Equal(t, crdt.ToTreeNodeForTest(root), crdt.ToTreeNodeForTest(clone))
		assert.Equal(t, crdt.ToXML(root), crdt.ToXML(clone))
	})

	t.Run("tree converting to bytes test", func(t *testing.T) {
		root := helper.BuildTreeNode(&json.TreeNode{
			Type: "r",
			Children: []json.TreeNode{
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "hello"}}},
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "world"}}},
			},
		})

		tree := crdt.NewTree(root, time.InitialTicket)
		bytes, err := converter.TreeToBytes(tree)
		assert.NoError(t, err)
		clone, err := converter.BytesToTree(bytes)
		assert.NoError(t, err)

		assert.Equal(t, tree.ToXML(), clone.ToXML())
	})

	t.Run("array converting to bytes test", func(t *testing.T) {
		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)

		treeList := crdt.NewRGATreeList()
		arr := crdt.NewArray(treeList, ctx.IssueTimeTicket())
		primitive, _ := crdt.NewPrimitive("1", ctx.IssueTimeTicket())
		_ = arr.Add(primitive)
		primitive, _ = crdt.NewPrimitive("2", ctx.IssueTimeTicket())
		_ = arr.Add(primitive)
		primitive, _ = crdt.NewPrimitive("3", ctx.IssueTimeTicket())
		_ = arr.Add(primitive)

		bytes, err := converter.ArrayToBytes(arr)
		assert.NoError(t, err)
		clone, err := converter.BytesToArray(bytes)
		assert.NoError(t, err)

		assert.Equal(t, `["1","2","3"]`, arr.Marshal())
		assert.Equal(t, `["1","2","3"]`, clone.Marshal())
	})

	t.Run("empty presence converting test", func(t *testing.T) {
		change, err := innerpresence.NewChangeFromJSON(`{"ChangeType":"put","Presence":{}}`)
		assert.NoError(t, err)

		pbChange := converter.ToPresenceChange(change)
		clone := converter.FromPresenceChange(pbChange)
		assert.Equal(t, change, clone)
	})

	t.Run("properly encode and decode tree test", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "r",
				Children: []json.TreeNode{
					{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "12"}}},
					{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "34"}}},
				},
			})
			assert.Equal(t, "<r><p>12</p><p>34</p></r>", root.GetTree("t").ToXML())
			root.GetTree("t").EditByPath([]int{0, 1}, []int{1, 1}, nil, 0)

			root.GetTree("t").Style(0, 1, map[string]string{"b": "t", "i": "t"})
			assert.Equal(t, `<r><p b="t" i="t">14</p></r>`, root.GetTree("t").ToXML())

			root.GetTree("t").RemoveStyle(0, 1, []string{"i"})
			return nil
		}))
		assert.Equal(t, `<r><p b="t">14</p></r>`, doc.Root().GetTree("t").ToXML())

		bytes, err := converter.ObjectToBytes(doc.RootObject())
		assert.NoError(t, err)
		obj, err := converter.BytesToObject(bytes)
		assert.NoError(t, err)

		assert.Equal(t, obj.Get("t").(*crdt.Tree).NodeLen(), doc.Root().GetTree("t").NodeLen())
		assert.Equal(t, obj.Get("t").(*crdt.Tree).Root().Len(), doc.Root().GetTree("t").Len())
		assert.Equal(t, obj.Get("t").(*crdt.Tree).ToXML(), doc.Root().GetTree("t").ToXML())
	})
}

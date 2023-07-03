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

package crdt_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

func registerElementHasRemovedNodes(fromPos, toPos *crdt.RGATreeSplitNodePos, root *crdt.Root, text crdt.GCElement) {
	if !fromPos.Equal(toPos) {
		root.RegisterElementHasRemovedNodes(text)
	}
}

func TestRoot(t *testing.T) {
	t.Run("garbage collection for array test", func(t *testing.T) {
		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)
		array := crdt.NewArray(crdt.NewRGATreeList(), ctx.IssueTimeTicket())

		array.Add(crdt.NewPrimitive(0, ctx.IssueTimeTicket()))
		array.Add(crdt.NewPrimitive(1, ctx.IssueTimeTicket()))
		array.Add(crdt.NewPrimitive(2, ctx.IssueTimeTicket()))
		assert.Equal(t, "[0,1,2]", array.Marshal())

		targetElement, _ := array.Get(1)
		array.DeleteByCreatedAt(targetElement.CreatedAt(), ctx.IssueTimeTicket())
		root.RegisterRemovedElementPair(array, targetElement)
		assert.Equal(t, "[0,2]", array.Marshal())
		assert.Equal(t, 1, root.GarbageLen())

		n, err := root.GarbageCollect(time.MaxTicket)
		assert.NoError(t, err)
		assert.Equal(t, 1, n)
		assert.Equal(t, 0, root.GarbageLen())
	})

	t.Run("garbage collection for text test", func(t *testing.T) {
		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)
		text := crdt.NewText(crdt.NewRGATreeSplit(crdt.InitialTextNode()), ctx.IssueTimeTicket())

		fromPos, toPos, _ := text.CreateRange(0, 0)
		_, _, err := text.Edit(fromPos, toPos, nil, "Hello World", nil, ctx.IssueTimeTicket())
		assert.NoError(t, err)
		registerElementHasRemovedNodes(fromPos, toPos, root, text)
		assert.Equal(t, "Hello World", text.String())
		assert.Equal(t, 0, root.GarbageLen())

		fromPos, toPos, _ = text.CreateRange(5, 10)
		_, _, err = text.Edit(fromPos, toPos, nil, "Yorkie", nil, ctx.IssueTimeTicket())
		assert.NoError(t, err)
		registerElementHasRemovedNodes(fromPos, toPos, root, text)
		assert.Equal(t, "HelloYorkied", text.String())
		assert.Equal(t, 1, root.GarbageLen())

		fromPos, toPos, _ = text.CreateRange(0, 5)
		_, _, err = text.Edit(fromPos, toPos, nil, "", nil, ctx.IssueTimeTicket())
		assert.NoError(t, err)
		registerElementHasRemovedNodes(fromPos, toPos, root, text)
		assert.Equal(t, "Yorkied", text.String())
		assert.Equal(t, 2, root.GarbageLen())

		fromPos, toPos, _ = text.CreateRange(6, 7)
		_, _, err = text.Edit(fromPos, toPos, nil, "", nil, ctx.IssueTimeTicket())
		assert.NoError(t, err)
		registerElementHasRemovedNodes(fromPos, toPos, root, text)
		assert.Equal(t, "Yorkie", text.String())
		assert.Equal(t, 3, root.GarbageLen())

		// It contains code marked tombstone.
		// After calling the garbage collector, the node will be removed.
		nodeLen := len(text.Nodes())
		assert.Equal(t, 4, nodeLen)

		n, err := root.GarbageCollect(time.MaxTicket)
		assert.NoError(t, err)
		assert.Equal(t, 3, n)
		assert.Equal(t, 0, root.GarbageLen())
		nodeLen = len(text.Nodes())
		assert.Equal(t, 1, nodeLen)
	})

	t.Run("garbage collection for fragments of text", func(t *testing.T) {
		type test struct {
			from    int
			to      int
			content string
			want    string
			garbage int
		}

		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)
		text := crdt.NewText(crdt.NewRGATreeSplit(crdt.InitialTextNode()), ctx.IssueTimeTicket())

		tests := []test{
			{from: 0, to: 0, content: "Yorkie", want: "Yorkie", garbage: 0},
			{from: 4, to: 5, content: "", want: "Yorke", garbage: 1},
			{from: 2, to: 3, content: "", want: "Yoke", garbage: 2},
			{from: 0, to: 1, content: "", want: "oke", garbage: 3},
		}

		for _, tc := range tests {
			fromPos, toPos, _ := text.CreateRange(tc.from, tc.to)
			_, _, err := text.Edit(fromPos, toPos, nil, tc.content, nil, ctx.IssueTimeTicket())
			assert.NoError(t, err)
			registerElementHasRemovedNodes(fromPos, toPos, root, text)
			assert.Equal(t, tc.want, text.String())
			assert.Equal(t, tc.garbage, root.GarbageLen())
		}

		n, err := root.GarbageCollect(time.MaxTicket)
		assert.NoError(t, err)
		assert.Equal(t, 3, n)
		assert.Equal(t, 0, root.GarbageLen())
	})

	t.Run("garbage collection for rich text test", func(t *testing.T) {
		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)
		text := crdt.NewText(crdt.NewRGATreeSplit(crdt.InitialTextNode()), ctx.IssueTimeTicket())

		fromPos, toPos, _ := text.CreateRange(0, 0)
		_, _, err := text.Edit(fromPos, toPos, nil, "Hello World", nil, ctx.IssueTimeTicket())
		assert.NoError(t, err)
		registerElementHasRemovedNodes(fromPos, toPos, root, text)
		assert.Equal(t, `[{"val":"Hello World"}]`, text.Marshal())
		assert.Equal(t, 0, root.GarbageLen())

		fromPos, toPos, _ = text.CreateRange(6, 11)
		_, _, err = text.Edit(fromPos, toPos, nil, "Yorkie", nil, ctx.IssueTimeTicket())
		assert.NoError(t, err)
		registerElementHasRemovedNodes(fromPos, toPos, root, text)
		assert.Equal(t, `[{"val":"Hello "},{"val":"Yorkie"}]`, text.Marshal())
		assert.Equal(t, 1, root.GarbageLen())

		fromPos, toPos, _ = text.CreateRange(0, 6)
		_, _, err = text.Edit(fromPos, toPos, nil, "", nil, ctx.IssueTimeTicket())
		assert.NoError(t, err)
		registerElementHasRemovedNodes(fromPos, toPos, root, text)
		assert.Equal(t, `[{"val":"Yorkie"}]`, text.Marshal())
		assert.Equal(t, 2, root.GarbageLen())

		// It contains code marked tombstone.
		// After calling the garbage collector, the node will be removed.
		nodeLen := len(text.Nodes())
		assert.Equal(t, 3, nodeLen)

		garbageLen, err := root.GarbageCollect(time.MaxTicket)
		assert.NoError(t, err)
		assert.Equal(t, 2, garbageLen)
		assert.Equal(t, 0, root.GarbageLen())

		nodeLen = len(text.Nodes())
		assert.Equal(t, 1, nodeLen)
	})

	t.Run("garbage collection for container test", func(t *testing.T) {
		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)

		obj := root.Object()
		obj.Set("1", crdt.NewPrimitive(1, ctx.IssueTimeTicket()))
		arr := crdt.NewArray(crdt.NewRGATreeList(), ctx.IssueTimeTicket()).
			Add(crdt.NewPrimitive(1, ctx.IssueTimeTicket())).
			Add(crdt.NewPrimitive(2, ctx.IssueTimeTicket())).
			Add(crdt.NewPrimitive(3, ctx.IssueTimeTicket()))
		obj.Set("2", arr)
		obj.Set("3", crdt.NewPrimitive(3, ctx.IssueTimeTicket()))
		assert.Equal(t, `{"1":1,"2":[1,2,3],"3":3}`, root.Object().Marshal())

		deleted := obj.Delete("2", ctx.IssueTimeTicket())
		root.RegisterRemovedElementPair(obj, deleted)
		assert.Equal(t, `{"1":1,"3":3}`, obj.Marshal())
		assert.Equal(t, 4, root.GarbageLen())

		n, err := root.GarbageCollect(time.MaxTicket)
		assert.NoError(t, err)
		assert.Equal(t, 4, n)
		assert.Equal(t, 0, root.GarbageLen())

		deleted = obj.Delete("3", ctx.IssueTimeTicket())
		root.RegisterRemovedElementPair(obj, deleted)
		assert.Equal(t, `{"1":1}`, obj.Marshal())
		assert.Equal(t, 1, root.GarbageLen())

		n, err = root.GarbageCollect(time.MaxTicket)
		assert.NoError(t, err)
		assert.Equal(t, 1, n)
		assert.Equal(t, 0, root.GarbageLen())
	})
}

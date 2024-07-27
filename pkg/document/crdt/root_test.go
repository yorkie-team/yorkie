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

func registerGCPairs(root *crdt.Root, pairs []crdt.GCPair) {
	for _, pair := range pairs {
		root.RegisterGCPair(pair)
	}
}

func TestRoot(t *testing.T) {
	t.Run("garbage collection for array test", func(t *testing.T) {
		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)
		elements := crdt.NewRGATreeList()
		array := crdt.NewArray(elements, ctx.IssueTimeTicket())

		for _, v := range []int{0, 1, 2} {
			primitive, err := crdt.NewPrimitive(v, ctx.IssueTimeTicket())
			assert.NoError(t, err)
			err = array.Add(primitive)
			assert.NoError(t, err)
		}
		assert.Equal(t, "[0,1,2]", array.Marshal())

		targetElement, _ := array.Get(1)
		_, err := array.DeleteByCreatedAt(targetElement.CreatedAt(), ctx.IssueTimeTicket())
		assert.NoError(t, err)
		root.RegisterRemovedElementPair(array, targetElement)
		assert.Equal(t, "[0,2]", array.Marshal())
		assert.Equal(t, 1, root.GarbageLen())

		n, err := root.GarbageCollect(time.MaxTicket)
		assert.NoError(t, err)
		assert.Equal(t, 1, n)
		assert.Equal(t, 0, root.GarbageLen())
	})

	t.Run("garbage collection for text test", func(t *testing.T) {
		steps := []struct {
			from    int
			to      int
			content string
			want    string
			garbage int
		}{
			{0, 0, "Hi World", `[0:0:00:0 {} ""][0:2:00:0 {} "Hi World"]`, 0},
			{2, 7, "Earth", `[0:0:00:0 {} ""][0:2:00:0 {} "Hi"][0:3:00:0 {} "Earth"]{0:2:00:2 {} " Worl"}[0:2:00:7 {} "d"]`, 1},
			{0, 2, "", `[0:0:00:0 {} ""]{0:2:00:0 {} "Hi"}[0:3:00:0 {} "Earth"]{0:2:00:2 {} " Worl"}[0:2:00:7 {} "d"]`, 2},
			{5, 6, "", `[0:0:00:0 {} ""]{0:2:00:0 {} "Hi"}[0:3:00:0 {} "Earth"]{0:2:00:2 {} " Worl"}{0:2:00:7 {} "d"}`, 3},
		}

		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)
		text := crdt.NewText(crdt.NewRGATreeSplit(crdt.InitialTextNode()), ctx.IssueTimeTicket())

		for _, step := range steps {
			fromPos, toPos, _ := text.CreateRange(step.from, step.to)
			_, _, pairs, err := text.Edit(fromPos, toPos, nil, step.content, nil, ctx.IssueTimeTicket())
			assert.NoError(t, err)
			registerGCPairs(root, pairs)
			assert.Equal(t, step.want, text.ToTestString())
			assert.Equal(t, step.garbage, root.GarbageLen())
		}

		// It contains code marked tombstone.
		// After calling the garbage collector, the node will be removed.
		assert.Equal(t, 4, len(text.Nodes()))

		n, err := root.GarbageCollect(time.MaxTicket)
		assert.NoError(t, err)
		assert.Equal(t, 3, n)
		assert.Equal(t, 0, root.GarbageLen())
		assert.Equal(t, 1, len(text.Nodes()))
	})

	t.Run("garbage collection for fragments of text", func(t *testing.T) {
		steps := []struct {
			from    int
			to      int
			content string
			want    string
			garbage int
		}{
			{from: 0, to: 0, content: "Yorkie", want: "Yorkie", garbage: 0},
			{from: 4, to: 5, content: "", want: "Yorke", garbage: 1},
			{from: 2, to: 3, content: "", want: "Yoke", garbage: 2},
			{from: 0, to: 1, content: "", want: "oke", garbage: 3},
		}

		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)
		text := crdt.NewText(crdt.NewRGATreeSplit(crdt.InitialTextNode()), ctx.IssueTimeTicket())

		for _, tc := range steps {
			fromPos, toPos, _ := text.CreateRange(tc.from, tc.to)
			_, _, pairs, err := text.Edit(fromPos, toPos, nil, tc.content, nil, ctx.IssueTimeTicket())
			assert.NoError(t, err)
			registerGCPairs(root, pairs)
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
		_, _, pairs, err := text.Edit(fromPos, toPos, nil, "Hello World", nil, ctx.IssueTimeTicket())
		assert.NoError(t, err)
		registerGCPairs(root, pairs)
		assert.Equal(t, `[{"val":"Hello World"}]`, text.Marshal())
		assert.Equal(t, 0, root.GarbageLen())

		fromPos, toPos, _ = text.CreateRange(6, 11)
		_, _, pairs, err = text.Edit(fromPos, toPos, nil, "Yorkie", nil, ctx.IssueTimeTicket())
		assert.NoError(t, err)
		registerGCPairs(root, pairs)
		assert.Equal(t, `[{"val":"Hello "},{"val":"Yorkie"}]`, text.Marshal())
		assert.Equal(t, 1, root.GarbageLen())

		fromPos, toPos, _ = text.CreateRange(0, 6)
		_, _, pairs, err = text.Edit(fromPos, toPos, nil, "", nil, ctx.IssueTimeTicket())
		assert.NoError(t, err)
		registerGCPairs(root, pairs)
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
		var err error
		primitive, err := crdt.NewPrimitive(1, ctx.IssueTimeTicket())
		assert.NoError(t, err)
		obj.Set("1", primitive)
		elements := crdt.NewRGATreeList()
		arr := crdt.NewArray(elements, ctx.IssueTimeTicket())
		for _, v := range []int{1, 2, 3} {
			primitive, err := crdt.NewPrimitive(v, ctx.IssueTimeTicket())
			assert.NoError(t, err)
			err = arr.Add(primitive)
			assert.NoError(t, err)
		}

		obj.Set("2", arr)
		primitive, err = crdt.NewPrimitive(3, ctx.IssueTimeTicket())
		assert.NoError(t, err)
		obj.Set("3", primitive)
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

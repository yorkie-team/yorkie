//go:build integration

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

package integration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/yson"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestObjectWithYSON(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	t.Run("object.set with yson test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		assert.NoError(t, c2.Attach(ctx, d2))

		// 01. set new object with json literal. 10 elements will be created.
		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewObject("obj", yson.ParseObject(`{
				"str": "v",
				"arr": [Int(1), "str"],
				"obj": {"key3": 42.2},
				"cnt": Counter(Long(0)),
				"txt": Text(),
				"tree": Tree({"type": "doc", "children": [{"type": "p", "children": [{"type": "text", "value": "ab"}]}]})
			}`))
			return nil
		})
		assert.NoError(t, err)

		// 02. set new object with same value by calling function.
		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewObject("obj").SetString("str", "v")
			root.GetObject("obj").SetNewArray("arr").AddInteger(1).AddString("str")
			root.GetObject("obj").SetNewObject("obj").SetDouble("key3", 42.2)
			root.GetObject("obj").SetNewCounter("cnt", crdt.LongCnt, 0)
			root.GetObject("obj").SetNewText("txt")
			root.GetObject("obj").SetNewTree("tree", json.TreeNode{
				Type: "doc",
				Children: []json.TreeNode{{
					Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}},
				}},
			})
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"obj":{"arr":[1,"str"],"cnt":0,"obj":{"key3":42.200000},"str":"v","tree":{"type":"doc","children":[{"type":"p","children":[{"type":"text","value":"ab"}]}]},"txt":[]}}`, d1.Marshal())
		assert.Equal(t, d2.Marshal(), d1.Marshal())

		// 03. remove the object and check the number of tombstones.
		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("obj")
			return nil
		})
		assert.NoError(t, err)
		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("obj")
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, 10, d1.GarbageLen())
		assert.Equal(t, 10, d1.GarbageCollect(helper.MaxVersionVector(d1.ActorID())))
		assert.Equal(t, 10, d2.GarbageLen())
		assert.Equal(t, 10, d2.GarbageCollect(helper.MaxVersionVector(d2.ActorID())))
	})

	t.Run("object.set with yson sync test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		assert.NoError(t, c2.Attach(ctx, d2))

		// 01. Sync set new object from d1 to d2
		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewObject("shape", yson.ParseObject(`{"color":"black"}`))
			return nil
		})
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		// 02. Sync overwrite object from d2 to d1
		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewObject("shape", yson.ParseObject(`{"color":"yellow"}`))
			return nil
		})
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		// 03. Sync delete object from d2 to d1
		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetObject("shape").Delete("color")
			return nil
		})
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("object.set with json literal array type test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
			// 01. set nested array in object with YSON
			root.SetNewObject("obj", yson.ParseObject(`{
				"array": [Int(1), Int(2), Int(3), [Int(7), Int(8)]]
			}`))
			assert.Equal(t, `{"obj":{"array":[1,2,3,[7,8]]}}`, root.Marshal())

			// 02. delete nested array in object
			root.GetObject("obj").GetArray("array").Delete(3)
			assert.Equal(t, `{"obj":{"array":[1,2,3]}}`, root.Marshal())
			return nil
		})
		assert.NoError(t, err)

		// 03. garbage collect (3 elements: array, 1, 2)
		assert.Equal(t, 3, d1.GarbageLen())
		assert.Equal(t, 3, d1.GarbageCollect(helper.MaxVersionVector(d1.ActorID())))
	})

	t.Run("object.set with yson counter type test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
			// 01. set two type of counter in object with YSON
			root.SetNewObject("obj", yson.ParseObject(`{
				"cntLong": Counter(Long(0)),
				"cntInt": Counter(Int(0))
			}`))
			assert.Equal(t, `{"obj":{"cntInt":0,"cntLong":0}}`, root.Marshal())

			// 02. increase and decrease counter
			root.GetObject("obj").GetCounter("cntLong").Increase(1)
			root.GetObject("obj").GetCounter("cntInt").Increase(-1)
			assert.Equal(t, `{"obj":{"cntInt":-1,"cntLong":1}}`, root.Marshal())
			return nil
		})
		assert.NoError(t, err)

		// 03. is it also applied to the document
		assert.Equal(t, `{"obj":{"cntInt":-1,"cntLong":1}}`, d1.Marshal())
	})

	t.Run("object.set with yson text type test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
			// 01. set text in object with YSON
			root.SetNewObject("obj", yson.ParseObject(`{"txt": Text()}`))
			assert.Equal(t, `{"obj":{"txt":[]}}`, root.Marshal())

			// 02. edit text
			root.GetObject("obj").GetText("txt").Edit(0, 0, "ABCD")
			assert.Equal(t, `{"obj":{"txt":[{"val":"ABCD"}]}}`, root.Marshal())
			return nil
		})
		assert.NoError(t, err)

		// 03. is it also applied to the document
		assert.Equal(t, `{"obj":{"txt":[{"val":"ABCD"}]}}`, d1.Marshal())
	})

	t.Run("object.set with yson primitive type test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
			// TODO(hackerwins): yson.ParseObject uses json.Unmarshal internally,
			// so it loses the precision of the long type. max safe integer is 2^53.
			root.SetNewObject("obj", yson.ParseObject(`{
				"nill": null,
				"bool": true,
				"long": Long(9007199254740992),
				"int": Int(32),
				"double": 1.79,
				"bytes": BinData("AQID"),
				"date": Date("2022-03-02T09:10:00Z")
			}`))
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"obj":{"bool":true,"bytes":"AQID","date":"2022-03-02T09:10:00Z","double":1.790000,"int":32,"long":9007199254740992,"nill":null}}`, d1.Marshal())
	})
}

//go:build integration

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
	"context"
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestObject(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	t.Run("causal object.set/delete test", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewObject("k1").
				SetString("k1.1", "v1").
				SetString("k1.2", "v2").
				SetString("k1.3", "v3")
			root.SetNewObject("k2").
				SetString("k2.1", "v4").
				SetString("k2.2", "v5").
				SetString("k2.3", "v6")
			return nil
		}, "nested update by c1")
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("k1")
			root.GetObject("k2").Delete("k2.2")
			return nil
		}, "nested update by c1")
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("concurrent object set/delete simple test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewObject("k1")
			return nil
		}, "set v1 by c1")
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":{}}`, d1.Marshal())
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("k1")
			root.SetString("k1", "v1")
			return nil
		}, "delete and set v1 by c1")
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":"v1"}`, d1.Marshal())

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("k1")
			root.SetString("k1", "v2")
			return nil
		}, "delete and set v2 by c2")
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":"v2"}`, d2.Marshal())
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("concurrent object.set test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		// 01. concurrent set on same key
		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetString("k1", "v1")
			return nil
		}, "set k1 by c1")
		assert.NoError(t, err)
		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetString("k1", "v2")
			return nil
		}, "set k1 by c2")
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		// 02. concurrent set between ancestor descendant
		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewObject("k2")
			return nil
		}, "set k2 by c1")
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetString("k2", "v2")
			return nil
		}, "set k2 by c1")
		assert.NoError(t, err)
		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetObject("k2").SetNewObject("k2.1").SetString("k2.1.1", "v2")
			return nil
		}, "set k2.1.1 by c2")
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		// 03. concurrent set between independent keys
		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetString("k3", "v3")
			return nil
		}, "set k3 by c1")
		assert.NoError(t, err)
		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetString("k4", "v4")
			return nil
		}, "set k4 by c2")
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("object.set with json literal test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		assert.NoError(t, c2.Attach(ctx, d2))

		// 01. set new object with json literal. 10 elements will be created.
		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewObject("obj", map[string]interface{}{ // 1: object
				"str": "v",                                  // 1: string
				"arr": []interface{}{1, "str"},              // 3: array, 1, "str"
				"obj": map[string]interface{}{"key3": 42.2}, // 2: object, 42.2
				"cnt": json.NewCounter(0, crdt.LongCnt),     // 1: counter
				"txt": json.NewText(),                       // 1: text
				"tree": json.NewTree(&json.TreeNode{ // 1: tree
					Type: "doc",
					Children: []json.TreeNode{{
						Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}},
					}},
				}),
			})
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
			root.GetObject("obj").SetNewTree("tree", &json.TreeNode{
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

		// 03. remove the shape object and check the number of tombstones.
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
		assert.Equal(t, 10, d1.GarbageCollect(time.MaxTicket))
		assert.Equal(t, 10, d2.GarbageLen())
		assert.Equal(t, 10, d2.GarbageCollect(time.MaxTicket))
	})

	t.Run("Set new object to json object literal sync test", func(t *testing.T) {

		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		d2 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {

			data := map[string]interface{}{
				"color": "black",
			}

			root.SetNewObject("shape", data)
			return nil
		})

		err = c1.Sync(ctx)
		assert.NoError(t, err)
		err = c2.Sync(ctx)
		assert.NoError(t, err)

		assert.Equal(t, `{"shape":{"color":"black"}}`, d1.Marshal())
		assert.Equal(t, `{"shape":{"color":"black"}}`, d2.Marshal())

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			data := map[string]interface{}{
				"color": "yellow",
			}
			root.SetNewObject("shape", data)
			return nil
		})

		err = c2.Sync(ctx)
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		assert.Equal(t, `{"shape":{"color":"yellow"}}`, d1.Marshal())
		assert.Equal(t, `{"shape":{"color":"yellow"}}`, d2.Marshal())

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetObject("shape").Delete("color")
			return nil
		})

		err = c2.Sync(ctx)
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		assert.Equal(t, 1, d1.GarbageLen())
		assert.Equal(t, 1, d1.GarbageCollect(time.MaxTicket))

	})

	t.Run("Set new object to json object literal set/delete test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			json := map[string]interface{}{
				"key1": "value1",
				"key2": "value2",
				"key3": "value3",
			}
			root.SetNewObject("obj", json)
			root.SetNewObject("obj2", json)
			return nil
		})

		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("obj")
			root.GetObject("obj2").Delete("key1")
			return nil
		})
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("Json object literal array type test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		array2 := []interface{}{7, 8}

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			json := map[string]interface{}{
				"array": []interface{}{1, 2, 3, array2},
			}
			root.SetNewObject("obj", json)
			assert.Equal(t, `{"obj":{"array":[1,2,3,[7,8]]}}`, root.Marshal())

			root.GetObject("obj").GetArray("array").Delete(3)
			assert.Equal(t, `{"obj":{"array":[1,2,3]}}`, root.Marshal())
			return nil
		})

		assert.NoError(t, err)
		assert.Equal(t, 3, d1.GarbageLen())
		assert.Equal(t, 3, d1.GarbageCollect(time.MaxTicket))

	})

	t.Run("Json object literal counter type test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			json := map[string]interface{}{
				"counterLong": json.NewCounter(0, crdt.LongCnt),
				"counterInt":  json.NewCounter(0, crdt.IntegerCnt),
			}
			root.SetNewObject("obj", json)

			root.GetObject("obj").GetCounter("counterLong").Increase(3)
			root.GetObject("obj").GetCounter("counterInt").Increase(-2)
			return nil
		})

		assert.NoError(t, err)
		assert.Equal(t, `{"obj":{"counterInt":-2,"counterLong":3}}`, d1.Marshal())
	})

	t.Run("Json object literal text type test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			json := map[string]interface{}{
				"text": json.NewText(),
			}
			root.SetNewObject("obj", json)
			root.GetObject("obj").GetText("text").Edit(0, 0, "ABCD")
			return nil
		})

		assert.NoError(t, err)
		assert.Equal(t, `{"obj":{"text":[{"val":"ABCD"}]}}`, d1.Marshal())
	})

	t.Run("Json object literal primitive type test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		var testInt int32 = 32

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			json := map[string]interface{}{
				"nill":   nil,
				"bool":   true,
				"long":   9223372036854775807,
				"int":    testInt,
				"double": 1.79,
				"bytes":  []byte{65, 66},
				"date":   gotime.Date(2022, 3, 2, 9, 10, 0, 0, gotime.UTC),
			}
			root.SetNewObject("obj", json)
			return nil
		})

		assert.NoError(t, err)
		assert.Equal(t, `{"obj":{"bool":true,"bytes":"AB","date":"2022-03-02T09:10:00Z","double":1.790000,"int":32,"long":9223372036854775807,"nill":null}}`, d1.Marshal())
	})

}

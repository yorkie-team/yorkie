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

	t.Run("Set new object with json literal test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		d2 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			data := map[string]interface{}{ // 1 : object
				"key":  "value",                    // 1 : string
				"key2": []interface{}{1, 2, "str"}, // 4 : array, 1, 2, "str"
				"obj": map[string]interface{}{ // 2 : object, double
					"key3": 42.2,
				},
				"cnt": json.CreateCounter(crdt.LongCnt, 0), // 1
				"txt": json.NewText(nil, nil),              // 1
			}
			root.SetNewObject("shape", data)
			root.GetObject("shape").SetString("key", "changed") // 1 tombstone
			root.GetObject("shape").GetText("txt").Edit(0, 0, "ABCD")
			root.GetObject("shape").GetCounter("cnt").Increase(7)
			return nil
		})
		assert.NoError(t, err)

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewObject("shape").
				SetString("key", "value").
				SetNewArray("key2")

			root.GetObject("shape").GetArray("key2").AddInteger(1, 2).AddString("str")

			root.GetObject("shape").SetNewObject("obj").
				SetDouble("key3", 42.2)

			root.GetObject("shape").SetNewCounter("cnt", crdt.LongCnt, 0)
			root.GetObject("shape").GetCounter("cnt").Increase(7)
			root.GetObject("shape").SetString("key", "changed")

			root.GetObject("shape").SetNewText("txt").Edit(0, 0, "ABCD")
			return nil
		})
		assert.NoError(t, err)

		assert.Equal(t, `{"shape":{"cnt":7,"key":"changed","key2":[1,2,"str"],"obj":{"key3":42.200000},"txt":[{"val":"ABCD"}]}}`, d1.Marshal())
		assert.Equal(t, d2.Marshal(), d1.Marshal()) // d1 should be same with d2

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("shape")
			return nil
		})

		assert.NoError(t, err)

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("shape")
			return nil
		})

		assert.NoError(t, err)

		assert.Equal(t, 11, d1.GarbageLen())
		assert.Equal(t, 11, d1.GarbageCollect(time.MaxTicket))

		assert.Equal(t, 11, d2.GarbageLen())
		assert.Equal(t, 11, d2.GarbageCollect(time.MaxTicket))

	})

	t.Run("Nested object tree test", func(t *testing.T) {

		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)

		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {

			data := map[string]interface{}{
				"tree": json.CreateTree(&json.TreeNode{
					Type: "doc",
					Children: []json.TreeNode{{
						Type:     "p",
						Children: []json.TreeNode{{Type: "text", Value: "ab"}},
					}, {
						Type: "ng",
						Children: []json.TreeNode{
							{Type: "note", Children: []json.TreeNode{{Type: "text", Value: "cd"}}},
							{Type: "note", Children: []json.TreeNode{{Type: "text", Value: "ef"}}},
						},
					}, {
						Type:     "bp",
						Children: []json.TreeNode{{Type: "text", Value: "gh"}},
					}},
				}),
			}

			root.SetNewObject("shape", data)
			assert.Equal(t, "<doc><p>ab</p><ng><note>cd</note><note>ef</note></ng><bp>gh</bp></doc>", root.GetObject("shape").GetTree("tree").ToXML())
			assert.Equal(t, 18, root.GetObject("shape").GetTree("tree").Len())

			return nil
		})

		assert.NoError(t, err)

		err = c1.Sync(ctx)
		assert.NoError(t, err)
		err = c2.Sync(ctx)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("shape")
			return nil
		})
		assert.NoError(t, err)

		assert.Equal(t, 2, d1.GarbageLen())
		assert.Equal(t, 2, d1.GarbageCollect(time.MaxTicket))
	})

	t.Run("Nested object sync test", func(t *testing.T) {

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

	t.Run("Nested object set/delete test", func(t *testing.T) {
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

	t.Run("Nested object nil type test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			json := map[string]interface{}{
				"emptry": nil,
			}
			root.SetNewObject("obj", json)
			return nil
		})

		assert.NoError(t, err)
		assert.Equal(t, `{"obj":{"emptry":null}}`, d1.Marshal())
	})

	t.Run("Nested object array type test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			json := map[string]interface{}{
				"array": []interface{}{1, 2, 3},
			}
			root.SetNewObject("obj", json)
			root.GetObject("obj").GetArray("array").AddInteger(4, 5)
			return nil
		})

		assert.NoError(t, err)
		assert.Equal(t, `{"obj":{"array":[1,2,3,4,5]}}`, d1.Marshal())
	})

	t.Run("Nested object counter type test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			json := map[string]interface{}{
				"counter": json.CreateCounter(crdt.LongCnt, 0),
			}
			root.SetNewObject("obj", json)
			root.GetObject("obj").GetCounter("counter").Increase(3)
			return nil
		})

		assert.NoError(t, err)
		assert.Equal(t, `{"obj":{"counter":3}}`, d1.Marshal())
	})

	t.Run("Nested object text type test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			json := map[string]interface{}{
				"text": json.CreateText(),
			}
			root.SetNewObject("obj", json)
			root.GetObject("obj").GetText("text").Edit(0, 0, "ABCD")
			return nil
		})

		assert.NoError(t, err)
		assert.Equal(t, `{"obj":{"text":[{"val":"ABCD"}]}}`, d1.Marshal())
	})

	t.Run("Nested object primitive type test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		var testInt int32 = 32

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			json := map[string]interface{}{
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
		assert.Equal(t, `{"obj":{"bool":true,"bytes":"AB","date":"2022-03-02T09:10:00Z","double":1.790000,"int":32,"long":9223372036854775807}}`, d1.Marshal())
	})

}

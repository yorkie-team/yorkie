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

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestGarbageCollection(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	t.Run("garbage collection for container type test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetInteger("1", 1)
			root.SetNewArray("2").AddInteger(1, 2, 3)
			root.SetInteger("3", 3)
			return nil
		}, "sets 1,2,3")
		assert.NoError(t, err)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 0, d2.GarbageLen())

		// (0, 0) -> (1, 0): syncedseqs:(0, 0)
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		// (1, 0) -> (1, 1): syncedseqs:(0, 0)
		err = c2.Sync(ctx)
		assert.NoError(t, err)

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("2")
			return nil
		}, "removes 2")
		assert.NoError(t, err)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 4, d2.GarbageLen())

		// (1, 1) -> (1, 2): syncedseqs:(0, 1)
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 4, d2.GarbageLen())

		// (1, 2) -> (2, 2): syncedseqs:(1, 1)
		err = c1.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 4, d1.GarbageLen())
		assert.Equal(t, 4, d2.GarbageLen())

		// (2, 2) -> (2, 2): syncedseqs:(1, 2)
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 4, d1.GarbageLen())
		assert.Equal(t, 4, d2.GarbageLen())

		// (2, 2) -> (2, 2): syncedseqs:(2, 2): meet GC condition
		err = c1.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 4, d2.GarbageLen())

		// (2, 2) -> (2, 2): syncedseqs:(2, 2): meet GC condition
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 0, d2.GarbageLen())
	})

	t.Run("garbage collection for text type test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("text").
				Edit(0, 0, "Hello world")
			root.SetNewText("richText").
				Edit(0, 0, "Hello world", nil)
			return nil
		}, "sets test and richText")
		assert.NoError(t, err)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 0, d2.GarbageLen())

		// (0, 0) -> (1, 0): syncedseqs:(0, 0)
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		// (1, 0) -> (1, 1): syncedseqs:(0, 0)
		err = c2.Sync(ctx)
		assert.NoError(t, err)

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").
				Edit(0, 1, "a").
				Edit(1, 2, "b")
			root.GetText("richText").
				Edit(0, 1, "a", map[string]string{"b": "1"})
			return nil
		}, "edit text type elements")
		assert.NoError(t, err)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 3, d2.GarbageLen())

		// (1, 1) -> (1, 2): syncedseqs:(0, 1)
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 3, d2.GarbageLen())

		// (1, 2) -> (2, 2): syncedseqs:(1, 1)
		err = c1.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 3, d1.GarbageLen())
		assert.Equal(t, 3, d2.GarbageLen())

		// (2, 2) -> (2, 2): syncedseqs:(1, 2)
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 3, d1.GarbageLen())
		assert.Equal(t, 3, d2.GarbageLen())

		// (2, 2) -> (2, 2): syncedseqs:(2, 2): meet GC condition
		err = c1.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 3, d2.GarbageLen())

		// (2, 2) -> (2, 2): syncedseqs:(2, 2): meet GC condition
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 0, d2.GarbageLen())
	})

	t.Run("garbage collection for tree type test", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "doc",
				Children: []json.TreeNode{{
					Type: "p", Children: []json.TreeNode{{
						Type: "tn", Children: []json.TreeNode{{
							Type: "text", Value: "a",
						}, {
							Type: "text", Value: "b",
						}},
					}, {
						Type: "tn", Children: []json.TreeNode{{
							Type: "text", Value: "cd",
						}},
					}},
				}},
			})
			assert.Equal(t, `<doc><p><tn>ab</tn><tn>cd</tn></p></doc>`, root.GetTree("t").ToXML())

			return nil
		})
		assert.NoError(t, err)

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 0, 0}, []int{0, 0, 2}, &json.TreeNode{Type: "text", Value: "gh"}, 0)
			assert.Equal(t, `<doc><p><tn>gh</tn><tn>cd</tn></p></doc>`, root.GetTree("t").ToXML())
			return nil
		})

		// [text(a), text(b)]
		assert.NoError(t, err)
		assert.Equal(t, doc.GarbageLen(), 2)
		assert.Equal(t, doc.GarbageCollect(time.MaxTicket), 2)
		assert.Equal(t, doc.GarbageLen(), 0)

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 0, 0}, []int{0, 0, 2}, &json.TreeNode{Type: "text", Value: "cv"}, 0)
			assert.Equal(t, `<doc><p><tn>cv</tn><tn>cd</tn></p></doc>`, root.GetTree("t").ToXML())
			return nil
		})

		// [text(gh)]
		assert.NoError(t, err)
		assert.Equal(t, doc.GarbageLen(), 1)
		assert.Equal(t, doc.GarbageCollect(time.MaxTicket), 1)
		assert.Equal(t, doc.GarbageLen(), 0)

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0}, []int{1}, &json.TreeNode{
				Type: "p", Children: []json.TreeNode{{
					Type: "tn", Children: []json.TreeNode{{
						Type: "text", Value: "ab",
					}},
				}}}, 0)
			assert.Equal(t, `<doc><p><tn>ab</tn></p></doc>`, root.GetTree("t").ToXML())
			return nil
		})

		// [p, tn, tn, text(cv), text(cd)]
		assert.NoError(t, err)
		assert.Equal(t, doc.GarbageLen(), 5)
		assert.Equal(t, doc.GarbageCollect(time.MaxTicket), 5)
		assert.Equal(t, doc.GarbageLen(), 0)
	})

	t.Run("garbage collection for tree type test (multi clients)", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "doc",
				Children: []json.TreeNode{{
					Type: "p",
					Children: []json.TreeNode{{
						Type:     "tn",
						Children: []json.TreeNode{{Type: "text", Value: "a"}, {Type: "text", Value: "b"}},
					}, {
						Type:     "tn",
						Children: []json.TreeNode{{Type: "text", Value: "cd"}},
					}},
				}},
			})
			assert.Equal(t, `<doc><p><tn>ab</tn><tn>cd</tn></p></doc>`, root.GetTree("t").ToXML())
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 0, d2.GarbageLen())

		// (0, 0) -> (1, 0): syncedseqs:(0, 0)
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		// (1, 0) -> (1, 1): syncedseqs:(0, 0)
		err = c2.Sync(ctx)
		assert.NoError(t, err)

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 0, 0}, []int{0, 0, 2}, &json.TreeNode{Type: "text", Value: "gh"}, 0)
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, d1.GarbageLen(), 0)
		assert.Equal(t, d2.GarbageLen(), 2)

		// (1, 1) -> (1, 2): syncedseqs:(0, 1)
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, d1.GarbageLen(), 0)
		assert.Equal(t, d2.GarbageLen(), 2)

		// (1, 2) -> (2, 2): syncedseqs:(1, 1)
		err = c1.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, d1.GarbageLen(), 2)
		assert.Equal(t, d2.GarbageLen(), 2)

		// (2, 2) -> (2, 2): syncedseqs:(1, 2)
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, d1.GarbageLen(), 2)
		assert.Equal(t, d2.GarbageLen(), 2)

		// (2, 2) -> (2, 2): syncedseqs:(2, 2): meet GC condition
		err = c1.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, d1.GarbageLen(), 0)
		assert.Equal(t, d2.GarbageLen(), 2)

		// (2, 2) -> (2, 2): syncedseqs:(2, 2): meet GC condition
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, d1.GarbageLen(), 0)
		assert.Equal(t, d2.GarbageLen(), 0)
	})

	t.Run("garbage collection with detached document test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetInteger("1", 1)
			root.SetNewArray("2").AddInteger(1, 2, 3)
			root.SetInteger("3", 3)
			root.SetNewText("4").Edit(0, 0, "Hi")
			root.SetNewText("5").Edit(0, 0, "Hi", nil)
			return nil
		}, "sets 1,2,3,4,5")
		assert.NoError(t, err)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 0, d2.GarbageLen())

		// (0, 0) -> (1, 0): syncedseqs:(0, 0)
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		// (1, 0) -> (1, 1): syncedseqs:(0, 0)
		err = c2.Sync(ctx)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("2")
			root.GetText("4").Edit(0, 1, "h")
			root.GetText("5").Edit(0, 1, "h", map[string]string{"b": "1"})
			return nil
		}, "removes 2 and edit text type elements")
		assert.NoError(t, err)
		assert.Equal(t, 6, d1.GarbageLen())
		assert.Equal(t, 0, d2.GarbageLen())

		// (1, 1) -> (2, 1): syncedseqs:(1, 0)
		err = c1.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 6, d1.GarbageLen())
		assert.Equal(t, 0, d2.GarbageLen())

		err = c2.Detach(ctx, d2)
		assert.NoError(t, err)

		// (2, 1) -> (2, 2): syncedseqs:(1, x)
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 6, d1.GarbageLen())
		assert.Equal(t, 6, d2.GarbageLen())

		// (2, 2) -> (2, 2): syncedseqs:(2, x): meet GC condition
		err = c1.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 6, d2.GarbageLen())
	})

	t.Run("GarbageLen should return the actual number of elements garbage-collected", func(t *testing.T) {
		ctx := context.Background()
		docKey := helper.TestDocKey(t)
		d1 := document.New(docKey)
		d2 := document.New(docKey)

		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewObject("point").
				SetInteger("point.x", 0).
				SetInteger("point.y", 0)
			return nil
		})
		err = c1.Sync(ctx)
		assert.NoError(t, err)
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("point")
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, 3, d1.GarbageLen())

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetObject("point").Delete("point.x")
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, 1, d2.GarbageLen())

		err = c1.Sync(ctx)
		assert.NoError(t, err)
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		assert.Equal(t, 3, d1.GarbageLen())
		assert.Equal(t, 3, d2.GarbageLen())

		assert.Equal(t, d1.GarbageCollect(time.MaxTicket), 3)
		assert.Equal(t, d2.GarbageCollect(time.MaxTicket), 3)
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
				"cnt": json.NewTempCounter(crdt.LongCnt, 0), // 1
				"txt": json.NewText(nil, nil),               // 1
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

		assert.Equal(t, d1.Marshal(), `{"shape":{"cnt":7,"key":"changed","key2":[1,2,"str"],"obj":{"key3":42.200000},"txt":[{"val":"ABCD"}]}}`)
		assert.Equal(t, d1.Marshal(), d2.Marshal())

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

		assert.Equal(t, d1.GarbageLen(), 11)
		assert.Equal(t, d1.GarbageCollect(time.MaxTicket), 11)

		assert.Equal(t, d2.GarbageLen(), 11)
		assert.Equal(t, d2.GarbageCollect(time.MaxTicket), 11)

	})

	t.Run("Nested object tree test", func(t *testing.T) {

		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {

			data := map[string]interface{}{
				"tree": &json.TreeNode{
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
				},
			}

			root.SetNewObject("shape", data)
			assert.Equal(t, "<doc><p>ab</p><ng><note>cd</note><note>ef</note></ng><bp>gh</bp></doc>", root.GetObject("shape").GetTree("tree").ToXML())
			assert.Equal(t, 18, root.GetObject("shape").GetTree("tree").Len())

			return nil
		})

		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("shape")
			return nil
		})
		assert.NoError(t, err)

		assert.Equal(t, d1.GarbageLen(), 2)
		assert.Equal(t, d1.GarbageCollect(time.MaxTicket), 2)
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

		assert.Equal(t, d1.GarbageLen(), 1)
		assert.Equal(t, d1.GarbageCollect(time.MaxTicket), 1)

	})

}

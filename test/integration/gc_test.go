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
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server"
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

	t.Run("deregister nested object gc test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			json := map[string]interface{}{
				"array": []interface{}{'a', 'b', 'c'},
			}

			root.SetNewObject("obj", json)
			root.Delete("obj")
			return nil
		})
		assert.NoError(t, err)

		assert.Equal(t, 5, d1.GarbageLen())
		assert.Equal(t, 5, d1.GarbageCollect(time.MaxTicket))
	})

	t.Run("Should work properly when there are multiple nodes to be collected in text type", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("text").Edit(0, 0, "z")
			return nil
		})
		assert.NoError(t, err)
		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(0, 1, "a")
			return nil
		})
		assert.NoError(t, err)
		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(1, 1, "b")
			return nil
		})
		assert.NoError(t, err)
		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(2, 2, "d")
			return nil
		})
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)
		err = c2.Sync(ctx)
		assert.NoError(t, err)

		assert.Equal(t, `{"text":[{"val":"a"},{"val":"b"},{"val":"d"}]}`, d1.Marshal())
		assert.Equal(t, `{"text":[{"val":"a"},{"val":"b"},{"val":"d"}]}`, d2.Marshal())
		assert.Equal(t, 1, d1.GarbageLen()) // z

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(2, 2, "c")
			return nil
		})
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, `{"text":[{"val":"a"},{"val":"b"},{"val":"c"},{"val":"d"}]}`, d1.Marshal())
		assert.Equal(t, `{"text":[{"val":"a"},{"val":"b"},{"val":"c"},{"val":"d"}]}`, d2.Marshal())

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(1, 3, "")
			return nil
		})
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, `{"text":[{"val":"a"},{"val":"d"}]}`, d1.Marshal())
		assert.Equal(t, 2, d1.GarbageLen()) // b,c

		err = c2.Sync(ctx)
		assert.NoError(t, err)
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, `{"text":[{"val":"a"},{"val":"d"}]}`, d2.Marshal())
		assert.Equal(t, 0, d1.GarbageLen())
	})

	t.Run("Should work properly when there are multiple nodes to be collected in tree type", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("tree", &json.TreeNode{
				Type: "r",
				Children: []json.TreeNode{{
					Type: "text", Value: "z",
				}},
			})
			return nil
		})
		assert.NoError(t, err)
		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("tree").EditByPath([]int{0}, []int{1}, &json.TreeNode{Type: "text", Value: "a"}, 0)
			return nil
		})
		assert.NoError(t, err)
		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("tree").EditByPath([]int{1}, []int{1}, &json.TreeNode{Type: "text", Value: "b"}, 0)
			return nil
		})
		assert.NoError(t, err)
		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("tree").EditByPath([]int{2}, []int{2}, &json.TreeNode{Type: "text", Value: "d"}, 0)
			return nil
		})
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)
		err = c2.Sync(ctx)
		assert.NoError(t, err)

		assert.Equal(t, `<r>abd</r>`, d1.Root().GetTree("tree").ToXML())
		assert.Equal(t, `<r>abd</r>`, d2.Root().GetTree("tree").ToXML())
		assert.Equal(t, 1, d1.GarbageLen()) // z

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("tree").EditByPath([]int{2}, []int{2}, &json.TreeNode{Type: "text", Value: "c"}, 0)
			return nil
		})
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, `<r>abcd</r>`, d1.Root().GetTree("tree").ToXML())
		assert.Equal(t, `<r>abcd</r>`, d2.Root().GetTree("tree").ToXML())

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("tree").EditByPath([]int{1}, []int{3}, nil, 0)
			return nil
		})
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, `<r>ad</r>`, d1.Root().GetTree("tree").ToXML())
		assert.Equal(t, 2, d1.GarbageLen()) // b,c

		err = c2.Sync(ctx)
		assert.NoError(t, err)
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, `<r>ad</r>`, d2.Root().GetTree("tree").ToXML())
		assert.Equal(t, 0, d1.GarbageLen())
	})

	t.Run("Should not collect the garbage if the DisableGC is true", func(t *testing.T) {
		// 01. Create a new server with SnapshotDisableGC set to true
		conf := helper.TestConfig()
		conf.Backend.SnapshotDisableGC = true
		conf.Backend.SnapshotThreshold = 10
		conf.Backend.SnapshotInterval = 10
		testServer, err := server.New(conf)
		assert.NoError(t, err)
		assert.NoError(t, testServer.Start())
		defer func() {
			assert.NoError(t, testServer.Shutdown(true))
		}()

		ctx := context.Background()
		c1, err := client.Dial(testServer.RPCAddr())
		assert.NoError(t, err)
		assert.NoError(t, c1.Activate(ctx))
		defer func() {
			assert.NoError(t, c1.Deactivate(ctx))
			assert.NoError(t, c1.Close())
		}()

		// 02. Create a document and update it to check if the garbage is collected
		d1 := document.New(helper.TestDocKey(t), document.WithDisableGC())
		assert.NoError(t, c1.Attach(ctx, d1))
		defer func() {
			assert.NoError(t, c1.Detach(ctx, d1))
		}()

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("text").Edit(0, 0, "-")
			return nil
		}))
		for i := 0; i < int(conf.Backend.SnapshotInterval); i++ {
			assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetText("text").Edit(0, 1, strconv.Itoa(i))
				return nil
			}))
		}
		assert.Equal(t, int(conf.Backend.SnapshotInterval), d1.GarbageLen())
		assert.NoError(t, c1.Sync(ctx))

		// 03. Check if the garbage is collected after the snapshot interval
		c2, err := client.Dial(testServer.RPCAddr())
		assert.NoError(t, err)
		assert.NoError(t, c2.Activate(ctx))
		defer func() {
			assert.NoError(t, c2.Deactivate(ctx))
			assert.NoError(t, c2.Close())
		}()

		d2 := document.New(helper.TestDocKey(t), document.WithDisableGC())
		assert.NoError(t, c2.Attach(ctx, d2))
		defer func() {
			assert.NoError(t, c2.Detach(ctx, d2))
		}()
		assert.Equal(t, int(conf.Backend.SnapshotInterval), d2.GarbageLen())
	})

	t.Run("simple tree test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "r",
				Children: []json.TreeNode{
					{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "A"}}},
					{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "B"}}},
					{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "C"}}},
				},
			})
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `<r><p>A</p><p>B</p><p>C</p></r>`, d1.Root().GetTree("t").ToXML())
		assert.Equal(t, 0, d1.GarbageLen())

		err = c1.Sync(ctx)
		assert.NoError(t, err)
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, `<r><p>ABC</p></r>`, d2.Root().GetTree("t").ToXML())
		assert.Equal(t, 0, d2.GarbageLen())

		// 01. c1.op
		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Style(0, 6, map[string]string{"1": "k1"})

			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `<r><p 1="k1">ABC</p></r>`, d1.Root().GetTree("t").ToXML())
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 0, d2.GarbageLen())

		//02. c2.op
		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").RemoveStyle(0, 5, []string{"1"})

			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `<r><p>ABC</p></r>`, d2.Root().GetTree("t").ToXML())
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 1, d2.GarbageLen())

		// 03. c1.sync
		err = c1.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, `<r><p 1="k1">ABC</p></r>`, d1.Root().GetTree("t").ToXML())
		assert.Equal(t, `<r><p>ABC</p></r>`, d2.Root().GetTree("t").ToXML())
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 1, d2.GarbageLen())
		// 04. c2.sync
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, `<r><p 1="k1">ABC</p></r>`, d1.Root().GetTree("t").ToXML())
		assert.Equal(t, `<r><p>ABC</p></r>`, d2.Root().GetTree("t").ToXML())
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 1, d2.GarbageLen())
		// 05. c1.sync
		err = c1.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, `<r><p>ABC</p></r>`, d1.Root().GetTree("t").ToXML())
		assert.Equal(t, `<r><p>ABC</p></r>`, d2.Root().GetTree("t").ToXML())
		assert.Equal(t, 1, d1.GarbageLen())
		assert.Equal(t, 1, d2.GarbageLen())

		// 06. c2.sync
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 1, d1.GarbageLen())
		assert.Equal(t, 1, d2.GarbageLen())

		// 07. c1.sync
		err = c1.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 1, d2.GarbageLen())
	})
}

func TestConcurrentTreeGC(t *testing.T) {
	//       0   1 2    3   4 5    6   7 8    9
	// <root> <p> a </p> <p> b </p> <p> c </p> </root>
	// 0,3 : |----------|
	// 3,6 :            |----------|
	// 6,9 :                       |----------|

	initialState := json.TreeNode{
		Type: "root",
		Children: []json.TreeNode{
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "a"}}, Attributes: map[string]string{"color": "red"}},
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "b"}}, Attributes: map[string]string{"color": "red"}},
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "c"}}, Attributes: map[string]string{"color": "red"}},
		},
	}
	initialXML := `<root><p color="red">a</p><p color="red">b</p><p color="red">c</p></root>`

	content := &json.TreeNode{Type: "p", Attributes: map[string]string{"italic": "true"}, Children: []json.TreeNode{{Type: "text", Value: `d`}}}

	ranges := []twoRangesType{
		// equal: <p>b</p> - <p>b</p>
		makeTwoRanges(3, 3, 6, 3, -1, 6, `equal`),
		// equal multiple: <p>a</p><p>b</p><p>c</p> - <p>a</p><p>b</p><p>c</p>
		makeTwoRanges(0, 3, 9, 0, 3, 9, `equal multiple`),
		// A contains B: <p>a</p><p>b</p><p>c</p> - <p>b</p>
		makeTwoRanges(0, 3, 9, 3, -1, 6, `A contains B`),
		// B contains A: <p>b</p> - <p>a</p><p>b</p><p>c</p>
		makeTwoRanges(3, 3, 6, 0, -1, 9, `B contains A`),
		// intersect: <p>a</p><p>b</p> - <p>b</p><p>c</p>
		makeTwoRanges(0, 3, 6, 3, -1, 9, `intersect`),
	}

	editOperations := []operationInterface{
		editOperationType{RangeFront, EditUpdate, content, 0, `insertFront`},
		editOperationType{RangeMiddle, EditUpdate, content, 0, `insertMiddle`},
		editOperationType{RangeBack, EditUpdate, content, 0, `insertBack`},
		editOperationType{RangeAll, EditUpdate, nil, 0, `delete`},
		editOperationType{RangeAll, EditUpdate, content, 0, `replace`},
		editOperationType{RangeAll, MergeUpdate, nil, 0, `merge`},
	}

	styleOperations := []operationInterface{
		styleOperationType{RangeAll, StyleRemove, "color", "", `remove-bold`},
		styleOperationType{RangeAll, StyleSet, "bold", "aa", `set-bold-aa`},
	}

	type step struct {
		garbageLen []int
		expectXML  []string
	}

	type test struct {
		desc     string
		interval twoRangesType
		op1      operationInterface
		op2      operationInterface
		//steps []step
	}

	var tests []test

	// 00. Compose tests
	for _, interval := range ranges {
		for _, op1 := range editOperations {
			for _, op2 := range styleOperations {
				tests = append(tests, test{
					desc:     interval.desc + "(" + op1.getDesc() + "," + op2.getDesc() + ")",
					interval: interval,
					op1:      op1,
					op2:      op2,
					//steps : []step,
				})
			}
		}
	}

	c1, err := client.Dial(defaultServer.RPCAddr())
	assert.NoError(t, err)
	assert.NoError(t, c1.Activate(context.Background()))

	c2, err := client.Dial(defaultServer.RPCAddr())
	assert.NoError(t, err)
	assert.NoError(t, c2.Activate(context.Background()))

	defer func() {
		assert.NoError(t, c1.Deactivate(context.Background()))
		assert.NoError(t, c1.Close())
		assert.NoError(t, c2.Deactivate(context.Background()))
		assert.NoError(t, c2.Close())
	}()

	for i, tc := range tests {
		t.Run(fmt.Sprintf("%d. %s", i+1, tc.desc), func(t *testing.T) {
			ctx := context.Background()
			d1 := document.New(helper.TestDocKey(t))
			assert.NoError(t, c1.Attach(ctx, d1))

			d2 := document.New(helper.TestDocKey(t))
			assert.NoError(t, c2.Attach(ctx, d2))

			assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewTree("t", &initialState)
				return nil
			}))
			assert.NoError(t, c1.Sync(ctx))
			assert.NoError(t, c2.Sync(ctx))
			assert.Equal(t, initialXML, d1.Root().GetTree("t").ToXML())
			assert.Equal(t, initialXML, d2.Root().GetTree("t").ToXML())
			fmt.Printf("0. initial state: %d %d\n", d1.GarbageLen(), d2.GarbageLen())

			tc.op1.run(t, d1, 0, tc.interval)
			fmt.Printf("1. c1.op: %d, %s,  %d, %s\n", d1.GarbageLen(), d1.Root().GetTree("t").ToXML(),
				d2.GarbageLen(), d2.Root().GetTree("t").ToXML())

			tc.op2.run(t, d2, 1, tc.interval)
			fmt.Printf("2. c2.op: %d %d\n", d1.GarbageLen(), d2.GarbageLen())

			assert.NoError(t, c1.Sync(ctx))
			fmt.Printf("3. c1.sync: %d %d\n", d1.GarbageLen(), d2.GarbageLen())

			assert.NoError(t, c2.Sync(ctx))
			fmt.Printf("4. c2.sync: %d %d\n", d1.GarbageLen(), d2.GarbageLen())

			assert.NoError(t, c1.Sync(ctx))
			fmt.Printf("5. c1.sync: %d %d\n", d1.GarbageLen(), d2.GarbageLen())

			assert.NoError(t, c2.Sync(ctx))
			fmt.Printf("6. c2.sync: %d %d\n", d1.GarbageLen(), d2.GarbageLen())

			assert.NoError(t, c1.Sync(ctx))
			fmt.Printf("7. c1.sync: %d %d\n", d1.GarbageLen(), d2.GarbageLen())

			assert.NoError(t, c2.Sync(ctx))
			fmt.Printf("8. c2.sync: %d %d\n", d1.GarbageLen(), d2.GarbageLen())
		})
	}
}

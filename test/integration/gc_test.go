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
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
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

		// push A[A:2] pull [B:1] => A:{ A:[A:2,B:1], B[B:1] }
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		// pull A[A:2] -> B:{ A:[A:2], B:[A:2,B:1] }
		err = c2.Sync(ctx)
		assert.NoError(t, err)

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("2")
			return nil
		}, "removes 2")
		assert.NoError(t, err)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 4, d2.GarbageLen())

		// push B[A:2,B:4] -> B: { A[A:2], B[A:2,B:4] }
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 4, d2.GarbageLen())

		// pull B[A:2, B:4] -> A: { A[A:2,B:4], B[A:2,B:4] } -> minSyncedVector : [2, 4]
		err = c1.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 0, d1.GarbageLen()) // gc collected garbage
		assert.Equal(t, 4, d2.GarbageLen())

		// B: { A[A:2], B[A:2,B:4] }
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 4, d2.GarbageLen())

		// A: { A[A:2,B:4], B[A:2,B:4] }
		err = c1.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 4, d2.GarbageLen())

		// B: { A[A:2], B[A:2,B:4] } -> minSyncedVector : [2, 0]
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 4, d2.GarbageLen())
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

		// push A[A:2]
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		// pull A[A:2] -> B: { A[A:2], B[A:2,B:1] }
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

		// push B[A:2,B:4] -> B: { A[A:2], B[A:2,B:4] }
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 3, d2.GarbageLen())

		// pull B[A:2,B:4] -> A: {A[A:2,B:4], B[A:2,B:4]} : meet GC condition
		err = c1.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 3, d2.GarbageLen())

		// B: { A[A:2], B[A:2,B:4] }
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 3, d2.GarbageLen())

		// A: { A[A:2,B:4], B[A:2,B:4] }
		err = c1.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 3, d2.GarbageLen())

		// B: { A[A:2], B[A:2,B:4] }
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 3, d2.GarbageLen())
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
		// A: {[A:3]}
		assert.NoError(t, err)
		assert.Equal(t, 2, doc.GarbageLen())
		assert.Equal(t, 2, doc.GarbageCollect(helper.MaxVectorClock(doc.ActorID())))
		assert.Equal(t, 0, doc.GarbageLen())

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 0, 0}, []int{0, 0, 2}, &json.TreeNode{Type: "text", Value: "cv"}, 0)
			assert.Equal(t, `<doc><p><tn>cv</tn><tn>cd</tn></p></doc>`, root.GetTree("t").ToXML())
			return nil
		})

		// [text(gh)]
		// A: {[A:4]}
		assert.NoError(t, err)
		assert.Equal(t, 1, doc.GarbageLen())
		assert.Equal(t, 1, doc.GarbageCollect(helper.MaxVectorClock(doc.ActorID())))
		assert.Equal(t, 0, doc.GarbageLen())

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
		// A: {[A:5]}
		assert.NoError(t, err)
		assert.Equal(t, 5, doc.GarbageLen())
		assert.Equal(t, 5, doc.GarbageCollect(helper.MaxVectorClock(doc.ActorID())))
		assert.Equal(t, 0, doc.GarbageLen())
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

		// push A[A:2] pull [B:1] => A: { A:[A:2,B:1], B[B:1] }
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		// pull A[A:2] -> B: { A:[A:2], B:[A:2,B:1] }
		err = c2.Sync(ctx)
		assert.NoError(t, err)

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 0, 0}, []int{0, 0, 2}, &json.TreeNode{Type: "text", Value: "gh"}, 0)
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, d1.GarbageLen(), 0)
		assert.Equal(t, d2.GarbageLen(), 2)

		// push B[A:2,B:4] -> B: { A[A:2], B[A:2,B:4] }
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, d1.GarbageLen(), 0)
		assert.Equal(t, d2.GarbageLen(), 2)

		// pull B[A:2,B:4] -> A: { A[A:2,B:4], B[A:2,B:4] } : meet GC condition
		err = c1.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, d1.GarbageLen(), 0)
		assert.Equal(t, d2.GarbageLen(), 2)

		// B: { A[A:2], B[A:2,B:4] }
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, d1.GarbageLen(), 0)
		assert.Equal(t, d2.GarbageLen(), 2)

		// A: { A[A:2,B:4], B[A:2,B:4] }
		err = c1.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, d1.GarbageLen(), 0)
		assert.Equal(t, d2.GarbageLen(), 2)

		// B: { A[A:2], B[A:2,B:4] }
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, d1.GarbageLen(), 0)
		assert.Equal(t, d2.GarbageLen(), 2)
	})

	t.Run("garbage collection with detached document test", func(t *testing.T) {
		t.Skip("skipping test now, because it is not implemented yet")
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

		// push A[A:2] -> A: { A:[A:2] }
		err = c1.Sync(ctx)
		assert.NoError(t, err)
		// push B[B:1], pull A[A:2] -> B: { A:[A:2], B:[A:2,B:1] }
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		// A@3 : delete point
		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("point")
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, 3, d1.GarbageLen())

		// B@4 : delete point.x
		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetObject("point").Delete("point.x")
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, 1, d2.GarbageLen())

		// push A[A:3], pull B[B:1] -> A: { A:[A:3,B:1], B:[B:1] }
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		// push B[A:2,B:4], pull A[A:3] -> B: { A:[A:3], B:[A:3,B:4] } -> B GC 3@A
		err = c2.Sync(ctx)
		assert.NoError(t, err)

		// pull B[A:2,B:4] -> A: { A:[A:3,B:4], B:[A:2,B:4] } -> A GC 4@B
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		assert.Equal(t, 2, d1.GarbageLen()) // 3@A remains
		assert.Equal(t, 0, d2.GarbageLen())

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewObject("point2")
			return nil
		})
		assert.NoError(t, err)

		// push B[A:3,B:6] -> B: { A:[A:3], B:[A:3,B:6] }
		err = c2.Sync(ctx)
		assert.NoError(t, err)

		// pull B[A:3,B:6] -> A: { A:[A:3,B:6], B:[A:3,B:6] } -> A GC 3@A
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 0, d2.GarbageLen())
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
		assert.Equal(t, 5, d1.GarbageCollect(helper.MaxVectorClock(d1.ActorID())))
	})

	// gc_test.go
	t.Run("should not garbage collection", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("text").Edit(0, 0, "a").Edit(1, 1, "b").Edit(2, 2, "c")
			return nil
		}, "sets text")
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)
		err = c2.Sync(ctx)
		assert.NoError(t, err)

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(2, 2, "c")
			return nil
		}, "insert c")
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(1, 3, "")
			return nil
		}, "delete bd")
		assert.NoError(t, err)
		assert.Equal(t, 2, d1.GarbageLen())
		assert.Equal(t, 0, d2.GarbageLen())

		err = c2.Sync(ctx)
		assert.NoError(t, err)
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 2, d1.GarbageLen())
		assert.Equal(t, 0, d2.GarbageLen())

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(2, 2, "1")
			return nil
		}, "insert 1")
		assert.NoError(t, err)

		err = c2.Sync(ctx) // GC purge 4@A node
		assert.NoError(t, err)
		err = c2.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 2, d1.GarbageLen())
		assert.Equal(t, 0, d2.GarbageLen())

		err = c1.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 2, d1.GarbageLen())
		assert.Equal(t, 0, d2.GarbageLen())
	})

	t.Run("should not garbage collection int Text delete", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("text").Edit(0, 0, "a").Edit(1, 1, "b")
			return nil
		})

		// push A[A:2] -> A: { A:[A:2] }
		c1.Sync(ctx)
		c1.Sync(ctx)

		// push B[A:1,B:1], pull A[A:2] -> B: { A:[A:2], B:[A:2,B:1] }
		c2.Sync(ctx)
		c2.Sync(ctx)

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(2, 2, "d")
			return nil
		})

		// push B[A:2,B:4] -> B: { A:[A:2], B:[A:2,B:4] }
		c2.Sync(ctx)
		c2.Sync(ctx)

		// pull B[A:2,B:4] -> A: { A:[A:2,B:4], B:[A:2,B:4] } -> minSyncedVector : [2, 4]
		c1.Sync(ctx)
		c1.Sync(ctx)

		// 5@A : insert 'c'
		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(2, 2, "c")
			return nil
		})

		// 5@B : delete "bcd"
		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(2, 2, "1").Edit(1, 4, "")
			return nil
		})

		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 3, d2.GarbageLen())

		// push B[A:2,B:5] -> B: { A:[A:2], B:[A:2,B:5] }
		c2.Sync(ctx)
		c2.Sync(ctx)

		// push A[A:5,B:4], pull B[A:2,B:5] -> A: { A:[A:5,B:5], B:[A:2,B:5] } -> minSyncedVector : [2, 5]
		c1.Sync(ctx)
		c1.Sync(ctx)

		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 3, d2.GarbageLen())

		// pull A[A:5,B:4] -> B: { A:[A:5,B:4], B:[A:5,B:5] } -> minSyncedVector : [5, 4]
		c2.Sync(ctx)
		c2.Sync(ctx)

		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 3, d2.GarbageLen())
	})
}

// func TestVectorClock(t *testing.T) {
// 	t.Run("Delete actor's vector clock who has detaced", func(t *testing.T) {
// 		clients := activeClients(t, 2)
// 		c1, c2 := clients[0], clients[1]

// 		ctx := context.Background()
// 		d1 := document.New(helper.TestDocKey(t))
// 		err := c1.Attach(ctx, d1)
// 		assert.NoError(t, err)

// 		d2 := document.New(helper.TestDocKey(t))
// 		err = c2.Attach(ctx, d2)
// 		assert.NoError(t, err)

// 		err = c1.Detach(ctx, d1)
// 		assert.NoError(t, err)

// 		c2.Sync(ctx)

// 		assert.Equal(t, time.SyncedVectorMap{
// 			c2.ID().String(): time.VectorClock{
// 				c2.ID().String(): 1,
// 				c1.ID().String(): 2,
// 			},
// 		}, d2.InternalDocument().SyncedVectorMap())
// 	})
// }

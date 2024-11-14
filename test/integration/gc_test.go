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

type versionInfo struct {
	actorID *time.ActorID
	lamport int64
}

func checkVV(vector time.VersionVector, versions ...versionInfo) bool {
	if len(vector) != len(versions) {
		return false
	}

	for _, version := range versions {
		actorID := version.actorID
		lamport := version.lamport

		if vector.VersionOf(actorID) != lamport {
			return false
		}
	}

	return true
}

func versionOf(id *time.ActorID, lamport int64) versionInfo {
	return versionInfo{
		actorID: id,
		lamport: lamport,
	}
}

func TestGarbageCollection(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	t.Run("garbage collection for container type test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		// d1.vv = [c1:1], minvv = [c1:1], db.vv = {c1: [c1:1]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 1)), true)

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		// d2.vv = [c1: 1, c2:2], minvv = [c1:0, c2:0], db.vv = {c1: [c1:1], c2: [c2:1]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 1), versionOf(d2.ActorID(), 2)), true)

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetInteger("1", 1)
			root.SetNewArray("2").AddInteger(1, 2, 3)
			root.SetInteger("3", 3)
			return nil
		}, "sets 1,2,3"))

		// d1.vv = [c1: 2]
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 2)), true)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 0, d2.GarbageLen())

		assert.NoError(t, c1.Sync(ctx))
		// d1.vv = [c1:3, c2:1], minvv = [c1:0, c2:0], db.vv = {c1: [c1:2], c2: [c2:1]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 3), versionOf(d2.ActorID(), 1)), true)

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv = [c1:2, c2:3], minvv = [c1:1, c2:0], db.vv = {c1: [c1:2], c2: [c1:1, c2:2]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 2), versionOf(d2.ActorID(), 3)), true)

		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("2")
			return nil
		}, "removes 2"))
		// d2.vv = [c1:2, c2:4]
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 2), versionOf(d2.ActorID(), 4)), true)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 4, d2.GarbageLen())

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv = [c1: 2, c2:4], minvv = [c1: 2, c2:0], db.vv = {c1: [c1:2], c2: [c1:2, c2:4]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 2), versionOf(d2.ActorID(), 4)), true)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 4, d2.GarbageLen())

		assert.NoError(t, c1.Sync(ctx))
		// d1.vv = [c1: 5, c2:4], minvv = [c1: 2, c2:1], db.vv = {c1: [c1:3, c2:1], c2: [c1:2, c2:4]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 5), versionOf(d2.ActorID(), 4)), true)
		assert.Equal(t, 4, d1.GarbageLen())
		assert.Equal(t, 4, d2.GarbageLen())

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv = [c1: 2, c2:4], minvv = [c1: 2, c2:1], db.vv = {c1: [c1:3, c2:1], c2: [c1:2, c2:4]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 2), versionOf(d2.ActorID(), 4)), true)
		assert.Equal(t, 4, d1.GarbageLen())
		assert.Equal(t, 4, d2.GarbageLen())

		assert.NoError(t, c1.Sync(ctx))
		// d1.vv = [c1: 5, c2:4], minvv = [c1: 2, c2:4], db.vv = {c1: [c1:5, c2:4], c2: [c1:2, c2:4]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 5), versionOf(d2.ActorID(), 4)), true)
		// node removedAt = 4@c2, minVV[c2] = 4 meet GC condition
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 4, d2.GarbageLen())

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv = [c1: 2, c2:4], minvv = [c1: 2, c2:4], db.vv = {c1: [c1:5 c2:4], c2: [c1:2, c2:4]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 2), versionOf(d2.ActorID(), 4)), true)
		assert.Equal(t, 0, d1.GarbageLen())
		// node removedAt = 4@c2, minVV[c2] = 4 meet GC condition
		assert.Equal(t, 0, d2.GarbageLen())
	})

	t.Run("garbage collection for text type test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		// d1.vv = [c1:1], minvv = [c1:1], db.vv {c1: [c1:1]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 1)), true)

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		// d2.vv = [c1: 1, c2:2], minvv = [c1: 0, c2:0], db.vv = {c1: [c1:1], c2: [c2:1]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 1), versionOf(d2.ActorID(), 2)), true)

		assert.NoError(t,
			d1.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewText("text").
					Edit(0, 0, "Hello world")
				root.SetNewText("richText").
					Edit(0, 0, "Hello world", nil)
				return nil
			}, "sets test and richText"),
		)
		// d1.vv = [c1:2]
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 2)), true)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 0, d2.GarbageLen())

		assert.NoError(t, c1.Sync(ctx))
		// d1.vv = [c1:3, c2:1], minvv = [c1:0, c2:0], db.vv = {c1: [c1:2], c2: [c2:1]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 3), versionOf(d2.ActorID(), 1)), true)

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv = [c1:2, c2:3], minvv = [c1:1, c2:0], db.vv = {c1: [c1:2], c2: [c1:1 c2:2]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 2), versionOf(d2.ActorID(), 3)), true)

		assert.NoError(t,
			d2.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetText("text").
					Edit(0, 1, "a").
					Edit(1, 2, "b")
				root.GetText("richText").
					Edit(0, 1, "a", map[string]string{"b": "1"})
				return nil
			}, "edit text type elements"),
		)
		// d2.vv = [c1:2, c2:4]
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 2), versionOf(d2.ActorID(), 4)), true)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 3, d2.GarbageLen())

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv = [c1:2, c2:4], minvv = [c1:2, c2:0], db.vv = {c1: [c1:2], c2: [c1:2 c2:4]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 2), versionOf(d2.ActorID(), 4)), true)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 3, d2.GarbageLen())

		assert.NoError(t, c1.Sync(ctx))
		// d1.vv = [c1:5, c2:4], minvv = [c1:2, c2:1], db.vv = {c1: [c1:3, c2:1], c2: [c1:2 c2:4]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 5), versionOf(d2.ActorID(), 4)), true)
		assert.Equal(t, 3, d1.GarbageLen())
		assert.Equal(t, 3, d2.GarbageLen())

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv = [c1:2, c2:4], minvv = [c1:2, c2:0], db.vv = {c1: [c1:3, c2:1], c2: [c1:2 c2:4]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 2), versionOf(d2.ActorID(), 4)), true)
		assert.Equal(t, 3, d1.GarbageLen())
		assert.Equal(t, 3, d2.GarbageLen())

		assert.NoError(t, c1.Sync(ctx))
		// d1.vv = [c1:5, c2:4], minvv = [c1:2, c2:4], db.vv = {c1: [c1:5, c2:4], c2: [c1:2 c2:4]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 5), versionOf(d2.ActorID(), 4)), true)
		// node removedAt = 4@c2, minVV[c2] = 4 meet GC condition
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 3, d2.GarbageLen())

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv = [c1:2, c2:4], minvv = [c1:2, c2:4], db.vv = {c1: [c1:5, c2:4], c2: [c1:2 c2:4]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 2), versionOf(d2.ActorID(), 4)), true)
		assert.Equal(t, 0, d1.GarbageLen())
		// node removedAt = 4@c2, minVV[c2] = 4 meet GC condition
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
		assert.Equal(t, checkVV(doc.VersionVector(), versionOf(doc.ActorID(), 1)), true)
		assert.NoError(t, err)

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 0, 0}, []int{0, 0, 2}, &json.TreeNode{Type: "text", Value: "gh"}, 0)
			assert.Equal(t, `<doc><p><tn>gh</tn><tn>cd</tn></p></doc>`, root.GetTree("t").ToXML())
			return nil
		})

		// [text(a), text(b)]
		assert.Equal(t, checkVV(doc.VersionVector(), versionOf(doc.ActorID(), 2)), true)
		assert.NoError(t, err)
		assert.Equal(t, doc.GarbageLen(), 2)
		assert.Equal(t, doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID())), 2)
		assert.Equal(t, doc.GarbageLen(), 0)

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 0, 0}, []int{0, 0, 2}, &json.TreeNode{Type: "text", Value: "cv"}, 0)
			assert.Equal(t, `<doc><p><tn>cv</tn><tn>cd</tn></p></doc>`, root.GetTree("t").ToXML())
			return nil
		})

		// [text(gh)]
		assert.Equal(t, checkVV(doc.VersionVector(), versionOf(doc.ActorID(), 3)), true)
		assert.NoError(t, err)
		assert.Equal(t, doc.GarbageLen(), 1)
		assert.Equal(t, doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID())), 1)
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
		assert.Equal(t, checkVV(doc.VersionVector(), versionOf(doc.ActorID(), 4)), true)
		assert.NoError(t, err)
		assert.Equal(t, doc.GarbageLen(), 5)
		assert.Equal(t, doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID())), 5)
		assert.Equal(t, doc.GarbageLen(), 0)
	})

	t.Run("garbage collection for tree type test (multi clients)", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		// d1.vv = [c1:1], minvv = [c1:1], db.vv {c1: [c1:1]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 1)), true)

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		// d2.vv = [c1:1, c2:2], minvv = [c1:0, c2:0], db.vv {c1: [c1:1], c2: [c2:1]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 1), versionOf(d2.ActorID(), 2)), true)

		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
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
		//d1.vv = [c1:2]
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 2)), true)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 0, d2.GarbageLen())

		assert.NoError(t, c1.Sync(ctx))
		// d1.vv = [c1:3, c2:1], minvv = [c1:0, c2:0], db.vv {c1: [c1:2], c2: [c2:1]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 3), versionOf(d2.ActorID(), 1)), true)

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv = [c1:2, c2:3], minvv = [c1:1, c2:0], db.vv {c1: [c1:2], c2: [c1:1, c2:2]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 2), versionOf(d2.ActorID(), 3)), true)

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 0, 0}, []int{0, 0, 2}, &json.TreeNode{Type: "text", Value: "gh"}, 0)
			return nil
		})
		// d2.vv = [c1:2, c2:4]
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 2), versionOf(d2.ActorID(), 4)), true)
		assert.NoError(t, err)
		assert.Equal(t, d1.GarbageLen(), 0)
		assert.Equal(t, d2.GarbageLen(), 2)

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv = [c1:2, c2:4], minvv = [c1:2, c2:0], db.vv {c1: [c1:2], c2: [c1:2, c2:4]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 2), versionOf(d2.ActorID(), 4)), true)
		assert.NoError(t, err)
		assert.Equal(t, d1.GarbageLen(), 0)
		assert.Equal(t, d2.GarbageLen(), 2)

		assert.NoError(t, c1.Sync(ctx))
		// d1.vv = [c1:5, c2:4], minvv = [c1:2, c2:1], db.vv {c1: [c1:3, c2:1], c2: [c1:2, c2:4]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 5), versionOf(d2.ActorID(), 4)), true)
		assert.NoError(t, err)
		assert.Equal(t, d1.GarbageLen(), 2)
		assert.Equal(t, d2.GarbageLen(), 2)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 0, 0}, []int{0, 0, 0}, &json.TreeNode{Type: "text", Value: "g"}, 0)
			return nil
		})
		//d1.vv = [c1:6, c2:4]
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 6), versionOf(d2.ActorID(), 4)), true)

		assert.NoError(t, c1.Sync(ctx))
		// d1.vv = [c1:6, c2:4], minvv = [c1:2, c2:4], db.vv {c1: [c1:6, c2:4], c2: [c1:2, c2:4]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 6), versionOf(d2.ActorID(), 4)), true)
		assert.NoError(t, err)
		// node removedAt = 4@c2, minVV[c2] = 4 meet GC condition
		assert.Equal(t, d1.GarbageLen(), 0)
		assert.Equal(t, d2.GarbageLen(), 2)

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv = [c1:6, c2:7], minvv = [c1:2, c2:4], db.vv {c1: [c1:6, c2:4], c2: [c1:2, c2:4]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 6), versionOf(d2.ActorID(), 7)), true)
		assert.NoError(t, err)
		assert.Equal(t, d1.GarbageLen(), 0)
		// node removedAt = 4@c2, minVV[c2] = 4 meet GC condition
		assert.Equal(t, d2.GarbageLen(), 0)
	})

	t.Run("garbage collection with detached document test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		// d1.vv = [c1:1], minvv = [c1:1], db.vv {c1: [c1:1]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 1)), true)

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		// d2.vv = [c1:1, c2:2], minvv = [c1:0, c2:0], db.vv {c1: [c1:1], c2: [c2:1]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 1), versionOf(d2.ActorID(), 2)), true)

		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetInteger("1", 1)
			root.SetNewArray("2").AddInteger(1, 2, 3)
			root.SetInteger("3", 3)
			root.SetNewText("4").Edit(0, 0, "Hi")
			root.SetNewText("5").Edit(0, 0, "Hi", nil)
			return nil
		}, "sets 1,2,3,4,5")
		// d1.vv = [c1:2]
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 2)), true)
		assert.NoError(t, err)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 0, d2.GarbageLen())

		assert.NoError(t, c1.Sync(ctx))
		// d1.vv = [c1:3, c2:1], minvv = [c1:0, c2:0], db.vv {c1: [c1:2], c2: [c2:1]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 3), versionOf(d2.ActorID(), 1)), true)

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv = [c1:2, c2:3], minvv = [c1:1, c2:0], db.vv {c1: [c1:2], c2: [c1:1, c2:2]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 2), versionOf(d2.ActorID(), 3)), true)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("2")
			root.GetText("4").Edit(0, 1, "h")
			root.GetText("5").Edit(0, 1, "h", map[string]string{"b": "1"})
			return nil
		}, "removes 2 and edit text type elements")
		//d1.vv = [c1:4, c2:1]
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 4), versionOf(d2.ActorID(), 1)), true)
		assert.NoError(t, err)
		assert.Equal(t, 6, d1.GarbageLen())
		assert.Equal(t, 0, d2.GarbageLen())

		assert.NoError(t, c1.Sync(ctx))
		// d2.vv = [c1:4, c2:1], minvv = [c1:1, c2:1], db.vv {c1: [c1:4, c2:1], c2: [c1:1, c2:2]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 4), versionOf(d2.ActorID(), 1)), true)
		assert.Equal(t, 6, d1.GarbageLen())
		assert.Equal(t, 0, d2.GarbageLen())

		assert.NoError(t, c2.Detach(ctx, d2))
		// minvv = [c1:1], db.vv {c1: [c1:4]}

		assert.NoError(t, c1.Sync(ctx))
		// remove c2 lamport from d1.vv after GC
		// d1.vv = [c1:5], minvv = [c1:4], db.vv {c1: [c1:4]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 5)), true)
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 6, d2.GarbageLen())
	})

	t.Run("GarbageLen should return the actual number of elements garbage-collected", func(t *testing.T) {
		ctx := context.Background()
		docKey := helper.TestDocKey(t)
		d1 := document.New(docKey)
		d2 := document.New(docKey)

		assert.NoError(t, c1.Attach(ctx, d1))
		// d1.vv = [c1:1], minvv = [c1:1], db.vv {c1: [c1:1]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 1)), true)

		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewObject("point").
				SetInteger("point.x", 0).
				SetInteger("point.y", 0)
			return nil
		})
		// d1.vv = [c1:2]
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 2)), true)

		assert.NoError(t, c1.Sync(ctx))
		// d1.vv = [c1:2], minvv = [c1:2], db.vv {c1: [c1:2]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 2)), true)

		assert.NoError(t, c2.Attach(ctx, d2))
		// d1.vv = [c1:2, c2:3], minvv = [c1:0, c2:0],  db.vv {c1: [c1:2], [c2:1]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 2), versionOf(d2.ActorID(), 3)), true)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("point")
			return nil
		})
		// d1.vv = [c1:3]
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 3)), true)
		assert.NoError(t, err)
		assert.Equal(t, 3, d1.GarbageLen())

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetObject("point").Delete("point.x")
			return nil
		})
		// d2.vv = [c1:2, c2:4]
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 2), versionOf(d2.ActorID(), 4)), true)
		assert.NoError(t, err)
		assert.Equal(t, 1, d2.GarbageLen())

		assert.NoError(t, c1.Sync(ctx))
		// d1.vv = [c1:4, c2:1], minvv = [c1:0, c2:0], db.vv {c1: [c1:3], c2: [c2:1]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 4), versionOf(d2.ActorID(), 1)), true)

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv = [c1:3, c2:5], minvv = [c1:2, c2:0], db.vv {c1: [c1:3], c2: [c1:2, c2:4]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 3), versionOf(d2.ActorID(), 5)), true)

		assert.Equal(t, 3, d1.GarbageLen())
		assert.Equal(t, 3, d2.GarbageLen())
		assert.NoError(t, c1.Sync(ctx))
		// d1.vv = [c1:5, c2:4], minvv = [c1:2, c2:1], db.vv {c1: [c1:4, c2:1], c2: [c1:2, c2:4]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 5), versionOf(d2.ActorID(), 4)), true)

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv = [c1:3, c2:5], minvv = [c1:3, c2:0], db.vv {c1: [c1:3], c2: [c1:3, c2:5]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 3), versionOf(d2.ActorID(), 5)), true)
		// node removedAt = 3@c1, minVV[c1] = 3 meet GC condition
		assert.Equal(t, 3, d1.GarbageLen())
		assert.Equal(t, 0, d2.GarbageLen())
		assert.Equal(t, d1.GarbageCollect(helper.MaxVersionVector(d1.ActorID(), d2.ActorID())), 3)
		assert.Equal(t, d2.GarbageCollect(helper.MaxVersionVector(d1.ActorID(), d2.ActorID())), 0)
	})

	t.Run("deregister nested object gc test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))

		assert.NoError(t, c1.Attach(ctx, d1))
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 1)), true)

		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
			json := map[string]interface{}{
				"array": []interface{}{'a', 'b', 'c'},
			}

			root.SetNewObject("obj", json)
			root.Delete("obj")
			return nil
		})
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 2)), true)
		assert.NoError(t, err)

		assert.Equal(t, 5, d1.GarbageLen())
		assert.Equal(t, 5, d1.GarbageCollect(helper.MaxVersionVector(d1.ActorID())))
	})

	t.Run("Should work properly when there are multiple nodes to be collected in text type", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))

		assert.NoError(t, c1.Attach(ctx, d1))
		// d1.vv = [c1:1], minvv = [c1:1], db.vv {c1: [c1:1]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 1)), true)

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		// d1.vv = [c1:1, c2:2], minvv = [c1:0, c2:0], db.vv {c1: [c1:1], c2:[c2:1]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 1), versionOf(d2.ActorID(), 2)), true)

		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("text").Edit(0, 0, "z")
			return nil
		})
		// d1.vv = [c1:2]
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 2)), true)
		assert.NoError(t, err)

		// "z" revmoedAt 3@c1
		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(0, 1, "a")
			return nil
		})
		// d1.vv = [c1:3]
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 3)), true)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(1, 1, "b")
			return nil
		})
		// d1.vv = [c1:4]
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 4)), true)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(2, 2, "d")
			return nil
		})
		// d1.vv = [c1:5]
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 5)), true)
		assert.NoError(t, err)

		assert.NoError(t, c1.Sync(ctx))
		// d1.vv = [c1:6, c2:1], minvv = [c1:0, c2:0], db.vv {c1: [c1:5], c2:[c2:1]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 6), versionOf(d2.ActorID(), 1)), true)

		assert.NoError(t, c2.Sync(ctx))
		err = c2.Sync(ctx)
		// d2.vv = [c1:5, c2:6], minvv = [c1:1, c2:0], db.vv {c1: [c1:5], c2:[c1:1, c2:2]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 5), versionOf(d2.ActorID(), 6)), true)

		assert.Equal(t, `{"text":[{"val":"a"},{"val":"b"},{"val":"d"}]}`, d1.Marshal())
		assert.Equal(t, `{"text":[{"val":"a"},{"val":"b"},{"val":"d"}]}`, d2.Marshal())
		assert.Equal(t, 1, d1.GarbageLen()) // z

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(2, 2, "c")
			return nil
		})
		// d1.vv = [c1:7, c2:1]
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 7), versionOf(d2.ActorID(), 1)), true)
		assert.NoError(t, err)

		assert.NoError(t, c1.Sync(ctx))
		// d1.vv = [c1:7, c2:1], minvv = [c1:1, c2:1], db.vv {c1: [c1:7, c2:1], c2:[c1:1, c2:2]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 7), versionOf(d2.ActorID(), 1)), true)

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv = [c1:7, c2:8], minvv = [c1:6, c2:1], db.vv {c1: [c1:7, c2:1], c2:[c1:6, c2:7]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 7), versionOf(d2.ActorID(), 8)), true)
		assert.Equal(t, `{"text":[{"val":"a"},{"val":"b"},{"val":"c"},{"val":"d"}]}`, d1.Marshal())
		assert.Equal(t, `{"text":[{"val":"a"},{"val":"b"},{"val":"c"},{"val":"d"}]}`, d2.Marshal())

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(1, 3, "")
			return nil
		})
		//d1.vv = [c1:8, c2:1]
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 8), versionOf(d2.ActorID(), 1)), true)
		assert.NoError(t, err)

		assert.NoError(t, c1.Sync(ctx))
		// d1.vv = [c1:8, c2:1], minvv = [c1:6, c2:1], db.vv {c1: [c1:8, c2:1], c2:[c1:6, c2:7]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 8), versionOf(d2.ActorID(), 1)), true)
		// "z"'s removedAt = 6@c1, minvv[c1] =6 meet GC Condition
		assert.Equal(t, `{"text":[{"val":"a"},{"val":"d"}]}`, d1.Marshal())
		assert.Equal(t, 2, d1.GarbageLen()) // a,b,c

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(0, 0, "a")
			return nil
		})
		// d2.vv = [c1:7, c2:9]
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 7), versionOf(d2.ActorID(), 9)), true)
		assert.NoError(t, err)

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv = [c1:8, c2:10], minvv = [c1:7, c2:1], db.vv {c1: [c1:8, c2:1], c2:[c1:7, c2:9]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 8), versionOf(d2.ActorID(), 10)), true)

		assert.NoError(t, c1.Sync(ctx))
		// d1.vv = [c1:10, c2:9], minvv = [c1:7, c2:1], db.vv {c1: [c1:8, c2:1], c2:[c1:7, c2:9]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 10), versionOf(d2.ActorID(), 9)), true)

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv = [c1:8, c2:10], minvv = [c1:7, c2:1], db.vv {c1: [c1:8, c2:1], c2:[c1:8, c2:10]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 8), versionOf(d2.ActorID(), 10)), true)

		assert.NoError(t, c1.Sync(ctx))
		// d2.vv = [c1:10, c2:9], minvv = [c1:8, c2:9], db.vv {c1: [c1:10, c2:9], c2:[c1:8, c2:10]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 10), versionOf(d2.ActorID(), 9)), true)

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv = [c1:8, c2:10], minvv = [c1:8, c2:9], db.vv {c1: [c1:10, c2:9], c2:[c1:8, c2:10]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 8), versionOf(d2.ActorID(), 10)), true)
		assert.Equal(t, `{"text":[{"val":"a"},{"val":"a"},{"val":"d"}]}`, d1.Marshal())
		assert.Equal(t, `{"text":[{"val":"a"},{"val":"a"},{"val":"d"}]}`, d2.Marshal())
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 0, d2.GarbageLen())
	})

	t.Run("Should work properly when there are multiple nodes to be collected in tree type", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))

		assert.NoError(t, c1.Attach(ctx, d1))
		// d1.vv = [c1:1], minvv = [c1:1], db.vv {c1: [c1:1]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 1)), true)

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		// d2.vv = [c1:1, c2:1], minvv = [c1:0, c2:0], db.vv {c1: [c1:1], c2: [c2:1]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 1), versionOf(d2.ActorID(), 2)), true)

		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("tree", &json.TreeNode{
				Type: "r",
				Children: []json.TreeNode{{
					Type: "text", Value: "z",
				}},
			})
			return nil
		})
		// d1.vv =[c1:2]
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 2)), true)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("tree").EditByPath([]int{0}, []int{1}, &json.TreeNode{Type: "text", Value: "a"}, 0)
			return nil
		})
		// d1.vv =[c1:3]
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 3)), true)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("tree").EditByPath([]int{1}, []int{1}, &json.TreeNode{Type: "text", Value: "b"}, 0)
			return nil
		})
		// d1.vv =[c1:4]
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 4)), true)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("tree").EditByPath([]int{2}, []int{2}, &json.TreeNode{Type: "text", Value: "d"}, 0)
			return nil
		})
		// d1.vv =[c1:5]
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 5)), true)
		assert.NoError(t, err)

		assert.NoError(t, c1.Sync(ctx))
		// d1.vv = [c1:6, c2:1], minvv = [c1:0, c2:0], db.vv {c1: [c1:5], c2: [c2:1]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 6), versionOf(d2.ActorID(), 1)), true)

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv = [c1:5, c2:6], minvv = [c1:1, c2:0], db.vv {c1: [c1:5], c2: [c1:1, c2:2]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 5), versionOf(d2.ActorID(), 6)), true)

		assert.Equal(t, `<r>abd</r>`, d1.Root().GetTree("tree").ToXML())
		assert.Equal(t, `<r>abd</r>`, d2.Root().GetTree("tree").ToXML())
		assert.Equal(t, 1, d1.GarbageLen()) // z

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("tree").EditByPath([]int{2}, []int{2}, &json.TreeNode{Type: "text", Value: "c"}, 0)
			return nil
		})
		//d1.vv =[c1:7, c2:1]
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 7), versionOf(d2.ActorID(), 1)), true)
		assert.NoError(t, err)

		assert.NoError(t, c1.Sync(ctx))
		// d1.vv =[c1:7, c2:1], minvv = [c1:1, c2:1], db.vv {c1: [c1:7, c2:1], c2: [c1:1, c2:2]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 7), versionOf(d2.ActorID(), 1)), true)

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv =[c1:7, c2:8], minvv =[c1:5, c2:1], db.vv {c1: [c1:7, c2:1], c2: [c1:5, c2:6]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 7), versionOf(d2.ActorID(), 8)), true)
		// "z" removedAt = 3@c1, minvv[c1] =5 meet GC condition
		assert.Equal(t, d2.GarbageLen(), 0)

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv =[c1:7, c2:8], minvv =[c1:7, c2:1], db.vv {c1: [c1:7, c2:1], c2: [c1:7, c2:8]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 7), versionOf(d2.ActorID(), 8)), true)

		assert.Equal(t, `<r>abcd</r>`, d1.Root().GetTree("tree").ToXML())
		assert.Equal(t, `<r>abcd</r>`, d2.Root().GetTree("tree").ToXML())

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("tree").EditByPath([]int{1}, []int{3}, nil, 0)
			return nil
		})
		// d1.vv = [c1:8, c2:1]
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 8), versionOf(d2.ActorID(), 1)), true)
		assert.NoError(t, err)

		assert.NoError(t, c1.Sync(ctx))
		// d1.vv = [c1:8, c2:1], minvv = [c1:7, c2:1], db.vv {c1: [c1:8, c2:1], c2: [c1:7, c2:8]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 8), versionOf(d2.ActorID(), 1)), true)
		assert.Equal(t, `<r>ad</r>`, d1.Root().GetTree("tree").ToXML())
		assert.Equal(t, 2, d1.GarbageLen()) // b,c

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv =[c1:8, c2:9], minvv =[c1:7, c2:1], db.vv {c1: [c1:8, c2:1], c2: [c1:7, c2:8]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 8), versionOf(d2.ActorID(), 9)), true)

		assert.NoError(t, c1.Sync(ctx))
		// d1.vv =[c1:8, c2:1], minvv =[c1:7, c2:1], db.vv {c1: [c1:8, c2:1], c2: [c1:7, c2:8]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 8), versionOf(d2.ActorID(), 1)), true)
		assert.Equal(t, `<r>ad</r>`, d2.Root().GetTree("tree").ToXML())
		assert.Equal(t, 2, d1.GarbageLen())

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv =[c1:8, c2:9], minvv =[c1:8, c2:1], db.vv {c1: [c1:8, c2:1], c2: [c1:8, c2:9]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 8), versionOf(d2.ActorID(), 9)), true)

		assert.NoError(t, c1.Sync(ctx))
		// d1.vv =[c1:8, c2:1], minvv =[c1:8, c2:1], db.vv {c1: [c1:8, c2:1], c2: [c1:8, c2:9]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 8), versionOf(d2.ActorID(), 1)), true)
		// "b", "c" removedAt = 8@c1, minvv[c1] = 8 meet GC condition
		assert.Equal(t, 0, d1.GarbageLen())

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv =[c1:8, c2:9], minvv =[c1:8, c2:1], db.vv {c1: [c1:8, c2:1], c2: [c1:8, c2:9]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 8), versionOf(d2.ActorID(), 9)), true)
		// "b", "c" removedAt = 8@c1, minvv[c1] = 8 meet GC condition
		assert.NoError(t, err)
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
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 2)), true)
		for i := 0; i < int(conf.Backend.SnapshotInterval); i++ {
			assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetText("text").Edit(0, 1, strconv.Itoa(i))
				return nil
			}))
			assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), int64(2+i+1))), true)
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

	t.Run("concurrent garbage collection test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		// d2.vv =[c1:1], minvv =[c1:1], db.vv {c1: [c1:1]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 1)), true)

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		// d2.vv =[c1:1, c2:1], minvv =[c1:0, c2:0], db.vv {c1: [c1:1], c2: [c2:1]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 1), versionOf(d2.ActorID(), 2)), true)

		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("text").Edit(0, 0, "a").Edit(1, 1, "b").Edit(2, 2, "c")
			return nil
		}, "sets text")
		//d1.vv = [c1:2]
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 2)), true)
		assert.NoError(t, err)

		assert.NoError(t, c1.Sync(ctx))
		// d1.vv =[c1:3, c2:1], minvv =[c1:0, c2:0], db.vv {c1: [c1:2], c2: [c2:1]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 3), versionOf(d2.ActorID(), 1)), true)

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv =[c1:2, c2:3], minvv =[c1:0, c2:0], db.vv {c1: [c1:2], c2: [c1:1, c2:2]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 2), versionOf(d2.ActorID(), 3)), true)

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(2, 2, "c")
			return nil
		}, "insert c")
		//d2.vv =[c1:2, c2:4]
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 2), versionOf(d2.ActorID(), 4)), true)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(1, 3, "")
			return nil
		}, "delete bd")
		//d1.vv = [c1:4, c2:1]
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 4), versionOf(d2.ActorID(), 1)), true)
		assert.NoError(t, err)
		assert.Equal(t, 2, d1.GarbageLen())
		assert.Equal(t, 0, d2.GarbageLen())

		assert.NoError(t, c1.Sync(ctx))
		// d1.vv = [c1:5, c2:1], minvv = [c1:1, c2:1], db.vv {c1: [c1:4, c2:1], c2: [c1:1, c2:2]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 4), versionOf(d2.ActorID(), 1)), true)

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv =[c1:4, c2:5], minvv = [c1:2, c2:1], db.vv {c1: [c1:4, c2:1], c2: [c1:2, c2:4]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 4), versionOf(d2.ActorID(), 5)), true)
		assert.Equal(t, 2, d1.GarbageLen())
		assert.Equal(t, 2, d2.GarbageLen())

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(2, 2, "1")
			return nil
		}, "insert 1")
		//d2.vv =[c1:4, c2:6]
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 4), versionOf(d2.ActorID(), 6)), true)
		assert.NoError(t, err)

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv =[c1:4, c2:6], minvv = [c1:4, c2:1], db.vv {c1: [c1:4, c2:1], c2: [c1:4, c2:6]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 4), versionOf(d2.ActorID(), 6)), true)
		// "b", "c" removedAt = 4@c1, minvv[c1] = 4 meet GC condition
		assert.Equal(t, 2, d1.GarbageLen())
		assert.Equal(t, 0, d2.GarbageLen())

		assert.NoError(t, c1.Sync(ctx))
		// d1.vv = [c1:7, c2:6], minvv = [c1:4, c2:1], db.vv {c1: [c1:5, c2:1], c2: [c1:4, c2:6]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 7), versionOf(d2.ActorID(), 6)), true)
		// "b", "c" removedAt = 4@c1, minvv[c1] = 4 meet GC condition
		assert.Equal(t, 0, d1.GarbageLen())
		assert.Equal(t, 0, d2.GarbageLen())
	})

	t.Run("concurrent garbage collection test(with pushonly)", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))

		assert.NoError(t, c1.Attach(ctx, d1))
		// d2.vv =[c1:1], minvv =[c1:1], db.vv {c1: [c1:1]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 1)), true)

		d2 := document.New(helper.TestDocKey(t))

		assert.NoError(t, c2.Attach(ctx, d2))
		// d2.vv =[c1:1, c2:2], minvv =[c1:0, c2:0], db.vv {c1: [c1:1], c2: [c2:1]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 1), versionOf(d2.ActorID(), 2)), true)

		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("text").Edit(0, 0, "a").Edit(1, 1, "b")
			return nil
		}, "insert ab")
		// d1/vv = [c1:2]
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 2)), true)
		assert.NoError(t, err)

		assert.NoError(t, c1.Sync(ctx))
		// d1.vv = [c1:3, c2:1], minvv = [c1:0, c2:0], db.vv {c1: [c1:2], c2: [c2:1]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 3), versionOf(d2.ActorID(), 1)), true)

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv = [c1:2, c2:3], minvv = [c1:1, c2:0], db.vv {c1: [c1:2], c2: [c1:1, c2:2]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 2), versionOf(d2.ActorID(), 3)), true)

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(2, 2, "d")
			return nil
		}, "insert d")
		//d2.vv = [c1:2, c2:4]
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 2), versionOf(d2.ActorID(), 4)), true)
		assert.NoError(t, err)

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv = [c1:2, c2:4], minvv = [c1:2, c2:0], db.vv {c1: [c1:2], c2: [c1:2, c2:4]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 2), versionOf(d2.ActorID(), 4)), true)

		assert.NoError(t, c1.Sync(ctx))
		// d1.vv = [c1:5, c2:4], minvv = [c1:2, c2:1], db.vv {c1: [c1:3, c2:1], c2: [c1:2, c2:4]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 5), versionOf(d2.ActorID(), 4)), true)

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(2, 2, "c")
			return nil
		}, "insert c")
		//d2.vv = [c1:2, c2:5]
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 2), versionOf(d2.ActorID(), 5)), true)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(1, 3, "")
			return nil
		}, "remove ac")
		//c1.vv = [c1:6, c2:4]
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 6), versionOf(d2.ActorID(), 4)), true)
		assert.NoError(t, err)

		// sync pushonly
		assert.NoError(t, c2.Sync(ctx, client.WithDocKey(d2.Key()).WithPushOnly()))
		// d2.vv = [c1:2, c2:5], minvv = [c1:2, c2:1], db.vv {c1: [c1:3, c2:1], c2: [c1:2, c2:5]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 2), versionOf(d2.ActorID(), 5)), true)

		assert.NoError(t, c1.Sync(ctx))
		// d1.vv = [c1:7, c2:5], minvv = [c1:2, c2:4], db.vv {c1: [c1:6, c2:4], c2: [c1:2, c2:5]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 7), versionOf(d2.ActorID(), 5)), true)

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(2, 2, "1")
			return nil
		}, "insert 1 (pushonly)")
		//d2.vv = [c1:2, c2:6]
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 2), versionOf(d2.ActorID(), 6)), true)
		assert.NoError(t, err)

		assert.NoError(t, c2.Sync(ctx, client.WithDocKey(d2.Key()).WithPushOnly()))
		// d2.vv = [c1:2, c2:6], minvv = [c1:2, c2:4], db.vv {c1: [c1:6, c2:4], c2: [c1:2, c2:6]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 2), versionOf(d2.ActorID(), 6)), true)

		assert.NoError(t, c1.Sync(ctx))
		// d1.vv = [c1:8, c2:6], minvv = [c1:2, c2:5], db.vv {c1: [c1:7, c2:5], c2: [c1:2, c2:6]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 8), versionOf(d2.ActorID(), 6)), true)
		assert.Equal(t, d1.GarbageLen(), 2)
		assert.Equal(t, d2.GarbageLen(), 0)

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(2, 2, "2")
			return nil
		}, "insert 2 (pushonly)")
		//c2.vv = [c1:2, c2:7]
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 2), versionOf(d2.ActorID(), 7)), true)
		assert.NoError(t, err)

		assert.NoError(t, c2.Sync(ctx, client.WithDocKey(d2.Key()).WithPushOnly()))
		// d2.vv = [c1:2, c2:7], minvv = [c1:2, c2:5], db.vv {c1: [c1:7, c2:5], c2: [c1:2, c2:7]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 2), versionOf(d2.ActorID(), 7)), true)

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv = [c1:6, c2:8], minvv = [c1:2, c2:5], db.vv {c1: [c1:7, c2:5], c2: [c1:2, c2:7]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 6), versionOf(d2.ActorID(), 8)), true)

		assert.NoError(t, c1.Sync(ctx))
		// d1.vv = [c1:9, c2:7]], minvv = [c1:2, c2:6], db.vv {c1: [c1:8, c2:6], c2: [c1:2, c2:7]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 9), versionOf(d2.ActorID(), 7)), true)
		assert.Equal(t, d1.GarbageLen(), 2)
		assert.Equal(t, d2.GarbageLen(), 2)

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv = [c1:6, c2:8], minvv = [c1:6, c2:6], db.vv {c1: [c1:8, c2:6], c2: [c1:6, c2:8]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 6), versionOf(d2.ActorID(), 8)), true)
		// removedAt = 6@c1, minvv[c1] = 6, meet GC condition
		assert.Equal(t, d1.GarbageLen(), 2)
		assert.Equal(t, d2.GarbageLen(), 0)

		assert.NoError(t, c1.Sync(ctx))
		// d2.vv = [c1:9, c2:7], minvv = [c1:6, c2:7], db.vv {c1: [c1:9, c2:7], c2: [c1:6, c2:8]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 9), versionOf(d2.ActorID(), 7)), true)
		// removedAt = 6@c1, minvv[c1] = 6, meet GC condition
		assert.Equal(t, d1.GarbageLen(), 0)
		assert.Equal(t, d2.GarbageLen(), 0)
		assert.Equal(t, d1.Marshal(), `{"text":[{"val":"a"},{"val":"2"},{"val":"1"},{"val":"c"}]}`)
		assert.Equal(t, d2.Marshal(), `{"text":[{"val":"a"},{"val":"2"},{"val":"1"},{"val":"c"}]}`)
	})

	t.Run("gc doesn't run when detached client's version vector remains in db", func(t *testing.T) {
		clients := activeClients(t, 3)
		c1, c2, c3 := clients[0], clients[1], clients[2]
		defer deactivateAndCloseClients(t, clients)

		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))

		assert.NoError(t, c1.Attach(ctx, d1))

		d2 := document.New(helper.TestDocKey(t))
		d3 := document.New(helper.TestDocKey(t))

		assert.NoError(t, c2.Attach(ctx, d2))
		assert.NoError(t, c3.Attach(ctx, d3))

		// assume c3 is detached from now
		// c3 is detached but its version vector still remains in db

		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("text").Edit(0, 0, "a").Edit(1, 1, "b").Edit(2, 2, "c")
			return nil
		}, "sets text")
		assert.NoError(t, err)
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))

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

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.Equal(t, 2, d1.GarbageLen())
		assert.Equal(t, 2, d2.GarbageLen())

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(2, 2, "1")
			return nil
		}, "insert 1")
		assert.NoError(t, err)
		assert.NoError(t, c2.Sync(ctx))
		assert.Equal(t, 2, d1.GarbageLen())
		assert.Equal(t, 2, d2.GarbageLen())

		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, 2, d1.GarbageLen())
		assert.Equal(t, 2, d2.GarbageLen())

		i := 0
		for i < 100 {
			// min version vector stays the same as when c3 detached
			// thus no GC runs because c3's version vector remains in db
			i++
			assert.NoError(t, c2.Sync(ctx))
			assert.Equal(t, 2, d1.GarbageLen())
			assert.Equal(t, 2, d2.GarbageLen())

			assert.NoError(t, c1.Sync(ctx))
			assert.Equal(t, 2, d1.GarbageLen())
			assert.Equal(t, 2, d2.GarbageLen())
		}
	})

	t.Run("gc targeting nodes made by deactivated client", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		// d2.vv =[c1:1], minvv =[c1:1], db.vv {c1: [c1:1]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 1)), true)

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		// d2.vv =[c1:1, c2:1], minvv =[c1:0, c2:0], db.vv {c1: [c1:1], c2: [c2:1]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 1), versionOf(d2.ActorID(), 2)), true)

		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("text").Edit(0, 0, "a").Edit(1, 1, "b").Edit(2, 2, "c")
			return nil
		}, "sets text")
		//d1.vv = [c1:2]
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 2)), true)
		assert.NoError(t, err)

		assert.NoError(t, c1.Sync(ctx))
		// d1.vv =[c1:3, c2:1], minvv =[c1:0, c2:0], db.vv {c1: [c1:2], c2: [c2:1]}
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 3), versionOf(d2.ActorID(), 1)), true)

		assert.NoError(t, c2.Sync(ctx))
		// d2.vv =[c1:2, c2:3], minvv =[c1:0, c2:0], db.vv {c1: [c1:2], c2: [c1:1, c2:2]}
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 2), versionOf(d2.ActorID(), 3)), true)

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(2, 2, "c")
			return nil
		}, "insert c")
		//d2.vv =[c1:2, c2:4]
		assert.Equal(t, checkVV(d2.VersionVector(), versionOf(d1.ActorID(), 2), versionOf(d2.ActorID(), 4)), true)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(1, 3, "")
			return nil
		}, "delete bd")
		//d1.vv = [c1:4, c2:1]
		assert.Equal(t, checkVV(d1.VersionVector(), versionOf(d1.ActorID(), 4), versionOf(d2.ActorID(), 1)), true)
		assert.NoError(t, err)
		assert.Equal(t, 2, d1.GarbageLen())
		assert.Equal(t, 0, d2.GarbageLen())

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Deactivate(ctx))

		assert.Equal(t, d2.GarbageLen(), 2)
		assert.Equal(t, len(d2.VersionVector()), 2)

		assert.NoError(t, c2.Sync(ctx))
		assert.Equal(t, d2.GarbageLen(), 0)
		assert.Equal(t, len(d2.VersionVector()), 1)
	})
}

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

func TestObject(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	t.Run("causal object.set/delete test", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.TestKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		d2 := document.New(helper.TestKey(t))
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
		d1 := document.New(helper.TestKey(t))
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

		d2 := document.New(helper.TestKey(t))
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
		d1 := document.New(helper.TestKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		d2 := document.New(helper.TestKey(t))
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

	t.Run("object set/remove undo/redo test", func(t *testing.T) {
		ctx := context.Background()
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, doc))
		defer func() { assert.NoError(t, c1.Detach(ctx, doc)) }()

		// set over a new key
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetString("k1", "v1")
			return nil
		}))
		assert.Equal(t, `{"k1":"v1"}`, doc.Marshal())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, `{}`, doc.Marshal())

		assert.NoError(t, doc.Redo())
		assert.Equal(t, `{"k1":"v1"}`, doc.Marshal())

		// set over an existing key
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetString("k1", "v2")
			return nil
		}))
		assert.Equal(t, `{"k1":"v2"}`, doc.Marshal())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, `{"k1":"v1"}`, doc.Marshal())

		assert.NoError(t, doc.Redo())
		assert.Equal(t, `{"k1":"v2"}`, doc.Marshal())

		// remove
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("k1")
			return nil
		}))
		assert.Equal(t, `{}`, doc.Marshal())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, `{"k1":"v2"}`, doc.Marshal())

		assert.NoError(t, doc.Redo())
		assert.Equal(t, `{}`, doc.Marshal())
	})

	t.Run("reverse set operation is skipped when an ancestor was removed concurrently", func(t *testing.T) {
		// Test scenario:
		// c1: set shape.circle.point to {x: 0, y: 0}
		// c1: set shape.circle.point.x to 1 (SET over an existing key)
		// c2: delete shape (two levels above the SET's target object)
		// c1: undo (no changes, since shape -- and therefore point -- is gone)
		ctx := context.Background()
		d1 := document.New(helper.TestKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewObject("shape").
				SetNewObject("circle").
				SetNewObject("point").
				SetInteger("x", 0).
				SetInteger("y", 0)
			return nil
		})
		assert.NoError(t, err)
		assert.NoError(t, c1.Sync(ctx))

		d2 := document.New(helper.TestKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetObject("shape").GetObject("circle").GetObject("point").SetInteger("x", 1)
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(
			t,
			`{"shape":{"circle":{"point":{"x":1,"y":0}}}}`,
			d1.Marshal(),
		)
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("shape")
			return nil
		})
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c2, d2}, {c1, d1}})
		assert.Equal(t, `{}`, d1.Marshal())

		assert.NoError(t, d1.Undo())
		assert.Equal(t, `{}`, d1.Marshal())
		assert.False(t, d1.CanRedo())
	})

	t.Run("reverse remove operation is skipped when an ancestor was removed concurrently", func(t *testing.T) {
		// Test scenario:
		// c1: set shape.circle.color to 'red'
		// c1: set shape.circle.point to {x: 0, y: 0} (a brand new key, so its
		//     undo reverse is a Remove targeting point, two levels below shape)
		// c2: delete shape
		// c1: undo (no changes, since shape -- and therefore point -- is gone)
		ctx := context.Background()
		d1 := document.New(helper.TestKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewObject("shape").SetNewObject("circle").SetString("color", "red")
			return nil
		})
		assert.NoError(t, err)
		assert.NoError(t, c1.Sync(ctx))

		d2 := document.New(helper.TestKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetObject("shape").GetObject("circle").
				SetNewObject("point").SetInteger("x", 0).SetInteger("y", 0)
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(
			t,
			`{"shape":{"circle":{"color":"red","point":{"x":0,"y":0}}}}`,
			d1.Marshal(),
		)
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("shape")
			return nil
		})
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c2, d2}, {c1, d1}})
		assert.Equal(t, `{}`, d1.Marshal())

		assert.NoError(t, d1.Undo())
		assert.Equal(t, `{}`, d1.Marshal())
		assert.False(t, d1.CanRedo())
	})
}

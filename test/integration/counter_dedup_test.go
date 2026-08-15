//go:build integration

/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
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

func TestCounterDedup(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	t.Run("dedup counter ignores duplicate actors across clients", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		// c1 creates a dedup counter and adds "user-1".
		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewDedupCounter("uv").Add("user-1")
			return nil
		}, "c1 creates dedup counter and adds user-1")
		assert.NoError(t, err)
		assert.Equal(t, `{"uv":1}`, d1.Marshal())

		err = c1.Sync(ctx)
		assert.NoError(t, err)

		d2 := document.New(helper.TestKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		// c2 adds same "user-1" — should be ignored after sync.
		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetCounter("uv").Add("user-1")
			return nil
		}, "c2 adds duplicate user-1")
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, `{"uv":1}`, d1.Marshal())
		assert.Equal(t, `{"uv":1}`, d2.Marshal())
	})

	t.Run("dedup counter counts distinct actors", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		// c1 creates a dedup counter and adds "user-1" and "user-2".
		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			counter := root.SetNewDedupCounter("uv")
			counter.Add("user-1")
			counter.Add("user-2")
			return nil
		}, "c1 creates dedup counter and adds user-1 and user-2")
		assert.NoError(t, err)
		assert.Equal(t, `{"uv":2}`, d1.Marshal())

		err = c1.Sync(ctx)
		assert.NoError(t, err)

		d2 := document.New(helper.TestKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		// c2 adds a new "user-3" — should be counted.
		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetCounter("uv").Add("user-3")
			return nil
		}, "c2 adds new user-3")
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, `{"uv":3}`, d1.Marshal())
		assert.Equal(t, `{"uv":3}`, d2.Marshal())
	})

	t.Run("dedup counter has no undo test", func(t *testing.T) {
		// A dedup counter cannot be undone: HyperLogLog cannot remove an
		// actor once added, so Increase produces no reverse operation for
		// it. The counter's own creation is a Set, though, and that is
		// undoable; the two are pushed as separate Updates here so the
		// dedup Increase's own (lack of) undo entry can be observed on its
		// own, without the creation's reverse masking it.
		ctx := context.Background()
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, doc))
		defer func() { assert.NoError(t, c1.Detach(ctx, doc)) }()

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewDedupCounter("uv")
			return nil
		}))
		assert.Equal(t, `{"uv":0}`, doc.Marshal())
		assert.True(t, doc.CanUndo())

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetCounter("uv").Add("user-1")
			return nil
		}))
		assert.Equal(t, `{"uv":1}`, doc.Marshal())

		// The dedup Increase contributed no reverse, so undo reaches past
		// it to the counter's own creation.
		assert.NoError(t, doc.Undo())
		assert.Equal(t, `{}`, doc.Marshal())
	})
}

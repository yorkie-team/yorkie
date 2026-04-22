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

func TestVVCleanupAfterDetach(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	t.Run("detached client actor is removed from VV after sync", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		d2 := document.New(helper.TestKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		// Both clients make edits so both actors appear in VVs.
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetInteger("a", 1)
			return nil
		}, "c1 sets a"))

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))

		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetInteger("b", 2)
			return nil
		}, "c2 sets b"))

		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))

		// Verify both actors are in d1's VV before detach.
		_, hasC2Before := d1.VersionVector().Get(d2.ActorID())
		assert.True(t, hasC2Before, "c2 should be in d1's VV before detach")

		// c2 detaches.
		assert.NoError(t, c2.Detach(ctx, d2))

		// c1 syncs — should receive detached_actors and remove c2 from VV.
		assert.NoError(t, c1.Sync(ctx))

		_, hasC2After := d1.VersionVector().Get(d2.ActorID())
		assert.False(t, hasC2After, "c2 should be removed from d1's VV after detach + sync")

		// c1 can still make edits and sync correctly.
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetInteger("c", 3)
			return nil
		}, "c1 sets c after cleanup"))
		assert.NoError(t, c1.Sync(ctx))

		assert.Equal(t, `{"a":1,"b":2,"c":3}`, d1.Marshal())

		assert.NoError(t, c1.Detach(ctx, d1))
	})

	t.Run("VV cleanup with three clients and one detach", func(t *testing.T) {
		clients3 := activeClients(t, 3)
		c1, c2, c3 := clients3[0], clients3[1], clients3[2]
		defer deactivateAndCloseClients(t, clients3)

		ctx := context.Background()
		d1 := document.New(helper.TestKey(t))
		d2 := document.New(helper.TestKey(t))
		d3 := document.New(helper.TestKey(t))

		assert.NoError(t, c1.Attach(ctx, d1))
		assert.NoError(t, c2.Attach(ctx, d2))
		assert.NoError(t, c3.Attach(ctx, d3))

		// All clients make edits and sync.
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetInteger("x", 1)
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c3.Sync(ctx))

		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetInteger("y", 2)
			return nil
		}))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c3.Sync(ctx))

		// c3 detaches.
		assert.NoError(t, c3.Detach(ctx, d3))

		// c1 and c2 sync — both should eventually remove c3 from their VVs.
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))

		_, hasC3InD1 := d1.VersionVector().Get(d3.ActorID())
		assert.False(t, hasC3InD1, "c3 should be removed from d1's VV")

		_, hasC3InD2 := d2.VersionVector().Get(d3.ActorID())
		assert.False(t, hasC3InD2, "c3 should be removed from d2's VV")

		// Remaining clients can still collaborate.
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetInteger("z", 3)
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.Equal(t, d1.Marshal(), d2.Marshal())

		assert.NoError(t, c1.Detach(ctx, d1))
		assert.NoError(t, c2.Detach(ctx, d2))
	})
}

func TestGCAfterVVCleanup(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	t.Run("GC works correctly after VV cleanup with augmented minVV", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		d2 := document.New(helper.TestKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		// c1 creates elements and syncs.
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetInteger("1", 1)
			root.SetNewArray("2").AddInteger(1, 2, 3)
			root.SetInteger("3", 3)
			return nil
		}, "sets 1,2,3"))

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))

		// c1 deletes an element to create garbage.
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("2")
			return nil
		}, "deletes 2"))
		assert.Equal(t, 4, d1.GarbageLen())

		assert.NoError(t, c1.Sync(ctx))

		// c2 syncs to get the delete, then detaches.
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c2.Detach(ctx, d2))

		// c1 syncs — receives detached_actors, VV cleanup happens.
		assert.NoError(t, c1.Sync(ctx))

		_, hasC2 := d1.VersionVector().Get(d2.ActorID())
		assert.False(t, hasC2, "c2 should be removed from d1's VV after detach")

		// c1 syncs again — GC should work with augmented minVV.
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, 0, d1.GarbageLen(), "garbage should be collected after VV cleanup")

		assert.NoError(t, c1.Detach(ctx, d1))
	})

	t.Run("GC with text type after VV cleanup", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		d2 := document.New(helper.TestKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		// c1 creates text and both sync.
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("text").Edit(0, 0, "abc")
			return nil
		}, "sets text"))

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))

		// c1 deletes text content to create garbage.
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(0, 3, "")
			return nil
		}, "deletes text content"))
		assert.Equal(t, 1, d1.GarbageLen())

		assert.NoError(t, c1.Sync(ctx))

		// c2 syncs to get the delete, then detaches.
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c2.Detach(ctx, d2))

		// c1 syncs — receives detached_actors.
		assert.NoError(t, c1.Sync(ctx))

		// c1 syncs again — GC should collect the garbage nodes.
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, 0, d1.GarbageLen(), "text garbage should be collected after VV cleanup")

		assert.NoError(t, c1.Detach(ctx, d1))
	})
}

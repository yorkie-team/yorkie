//go:build integration

/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
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
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestPushOnlyGarbageCollection ports yorkie-js-sdk's pushonly_gc_test.ts.
//
// A push-only client pushes but does not pull, and every reply still carries
// the server's minimum version vector. Collecting with it would purge
// tombstones while the remote changes anchored on them are exactly what the
// client has not pulled yet, leaving it unable to apply them once it resumes.
func TestPushOnlyGarbageCollection(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	t.Run("keep a tombstone a deferred remote change anchors on", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestKey(t))
		d2 := document.New(helper.TestKey(t))
		require.NoError(t, c1.Attach(ctx, d1))
		require.NoError(t, c2.Attach(ctx, d2))
		pushOnly := client.WithKey(d2.Key()).WithPushOnly()

		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{
				Type: "doc",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "ab"}},
				}},
			})
			return nil
		}))
		require.NoError(t, c1.Sync(ctx))
		require.NoError(t, c2.Sync(ctx))

		// c2 composes from here on: it pushes while pulling nothing.
		require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 2, &json.TreeNode{Type: "text", Value: "X"}, 0)
			return nil
		}))
		require.NoError(t, c2.Sync(ctx, pushOnly))
		require.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<doc><p>aXb</p></doc>", d1.Root().GetTree("t").ToXML())

		// c2 replaces its "X": the node becomes a tombstone on both sides.
		require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 3, &json.TreeNode{Type: "text", Value: "x"}, 0)
			return nil
		}))
		require.NoError(t, c2.Sync(ctx, pushOnly))

		// c1 still sees "X" live and inserts right after it, an edit anchored
		// on the node c2 just removed. The first sync pushes the insert and
		// pulls the removal; the second reports a version vector that covers
		// the removal, so the server's minimum vector now does too.
		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(3, 3, &json.TreeNode{Type: "text", Value: "Y"}, 0)
			return nil
		}))
		require.NoError(t, c1.Sync(ctx))
		require.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<doc><p>axYb</p></doc>", d1.Root().GetTree("t").ToXML())

		// c2 keeps composing. The reply to this push carries a minimum vector
		// under which "X" is collectable, while the insert anchored on it is
		// still waiting on the server for c2 to pull.
		require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 1, &json.TreeNode{Type: "text", Value: "z"}, 0)
			return nil
		}))
		garbage := d2.GarbageLen()
		require.Positive(t, garbage)
		require.NoError(t, c2.Sync(ctx, pushOnly))
		assert.False(t, d2.HasLocalChanges())
		assert.Equal(t, garbage, d2.GarbageLen(), "the push-only reply must not collect")

		// The composition ends: c2 pulls the deferred insert.
		require.NoError(t, c2.Sync(ctx))
		require.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<doc><p>zaxYb</p></doc>", d2.Root().GetTree("t").ToXML())
		assert.Equal(t, d1.Root().GetTree("t").ToXML(), d2.Root().GetTree("t").ToXML())
	})
}

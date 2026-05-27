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

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestDisableGCOnAttach(t *testing.T) {
	t.Run("opt-out client writes no versionvectors row", func(t *testing.T) {
		clients := activeClients(t, 1)
		defer deactivateAndCloseClients(t, clients)
		c1 := clients[0]

		ctx := context.Background()
		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1, client.WithDisableGC()))
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewCounter("counter", 0).Increase(1)
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))

		// Inspect server-side state to confirm the wire contract: no row
		// must exist in the versionvectors table for this client.
		be := defaultServer.Backend()
		project, err := defaultServer.DefaultProject(ctx)
		assert.NoError(t, err)
		docInfo, err := be.DB.FindDocInfoByKey(ctx, project.ID, d1.Key())
		assert.NoError(t, err)

		minVV, err := be.DB.GetMinVersionVector(ctx, docInfo.RefKey(), time.NewVersionVector())
		assert.NoError(t, err)
		assert.Len(t, minVV, 0,
			"opt-out client must not write to the versionvectors table")
	})

	t.Run("mixed opt-in and opt-out clients converge on counter value", func(t *testing.T) {
		clients := activeClients(t, 2)
		defer deactivateAndCloseClients(t, clients)
		c1, c2 := clients[0], clients[1]

		ctx := context.Background()
		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewCounter("counter", 0)
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))

		d2 := document.New(helper.TestKey(t))
		assert.NoError(t, c2.Attach(ctx, d2, client.WithDisableGC()))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetCounter("counter").Increase(1)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetCounter("counter").Increase(1)
			return nil
		}))

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))

		assert.Equal(t, "2", d1.Root().GetCounter("counter").Marshal())
		assert.Equal(t, "2", d2.Root().GetCounter("counter").Marshal())
	})

	t.Run("re-attach without WithDisableGC restores normal GC participation", func(t *testing.T) {
		clients := activeClients(t, 1)
		defer deactivateAndCloseClients(t, clients)
		c1 := clients[0]

		ctx := context.Background()

		// First attach with opt-out.
		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1, client.WithDisableGC()))
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewCounter("counter", 0).Increase(1)
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c1.Detach(ctx, d1))

		// Re-attach without opt-out. The flag must reset to false; subsequent
		// PushPulls participate in minVV tracking normally.
		d2 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d2))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetCounter("counter").Increase(1)
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "2", d2.Root().GetCounter("counter").Marshal())
	})
}

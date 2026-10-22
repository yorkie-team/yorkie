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
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestDeactivateClearsPresence covers #2290: a client that is deactivated
// without detaching is detached by the server, which writes a presence-clear
// change on its behalf. That change used to be stamped with the client's
// per-session ID while the client had recorded its presence under its
// StableActorID, so the clear removed nothing and the departed client's
// presence stayed in the document — in every peer's memory and in every
// snapshot built from it.
func TestDeactivateClearsPresence(t *testing.T) {
	ctx := context.Background()

	t.Run("deactivate without detach clears the client's presence", func(t *testing.T) {
		clis := activeClients(t, 2)
		departing, peer := clis[0], clis[1]
		defer func() {
			// departing is deactivated by the test itself; Deactivate is a no-op
			// the second time round.
			deactivateAndCloseClients(t, clis)
		}()

		docKey := helper.TestKey(t)

		d1 := document.New(docKey)
		require.NoError(t, departing.Attach(ctx, d1, client.WithPresence(presence.Data{"name": "departing"})))

		d2 := document.New(docKey)
		require.NoError(t, peer.Attach(ctx, d2, client.WithPresence(presence.Data{"name": "peer"})))
		require.NoError(t, peer.Sync(ctx))

		// The peer sees the departing client's presence while it is still active.
		departingID := departing.ID().String()
		require.Equal(t, presence.Data{"name": "departing"}, d2.AllPresences()[departingID])

		// Deactivate without detaching: the server detaches on the client's
		// behalf and writes the clear change.
		require.NoError(t, departing.Deactivate(ctx))
		require.NoError(t, peer.Sync(ctx))

		assert.NotContains(t, d2.AllPresences(), departingID,
			"the departed client's presence should be cleared from the peer's document")

		// A client attaching afterwards replays the document from the server —
		// through its changes or a snapshot of them — and must not pick the
		// stale presence back up.
		latecomers := activeClients(t, 1)
		defer deactivateAndCloseClients(t, latecomers)
		latecomer := latecomers[0]

		d3 := document.New(docKey)
		require.NoError(t, latecomer.Attach(ctx, d3, client.WithPresence(presence.Data{"name": "latecomer"})))
		defer func() { assert.NoError(t, latecomer.Detach(ctx, d3)) }()

		assert.NotContains(t, d3.AllPresences(), departingID,
			"a client attaching after the deactivation should not see the departed client's presence")
	})
}

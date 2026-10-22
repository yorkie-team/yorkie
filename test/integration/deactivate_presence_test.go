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
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/presence/inner"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestDeactivateClearsPresence covers #2290: a client that is deactivated
// without detaching is detached by the server, which writes a presence-clear
// change on its behalf. Applying that change deletes the presence recorded
// under the actor it is stamped with, and the two SDK generations record
// theirs under different actors — the Go SDK under its per-session ID, the JS
// SDK under the StableActorID of the same ActivateClient response. A clear
// stamped with only one of them removes nothing for the other, and the
// departed client's presence stays in the document — in every peer's memory
// and in every snapshot built from it. Both subtests below drive one of the
// two identities.
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

	// The Go client stamps the per-session ID the subtest above asserts on, so
	// the stable-actor identity the JS SDK stamps needs raw RPC to reproduce:
	// attach with a presence change carried under the ActivateClient response's
	// actor_id rather than its client_id.
	t.Run("deactivate clears a presence recorded under the stable actor", func(t *testing.T) {
		clis := activeClients(t, 1)
		defer deactivateAndCloseClients(t, clis)
		peer := clis[0]

		docKey := helper.TestKey(t)
		raw := v1connect.NewYorkieServiceClient(http.DefaultClient, "http://"+defaultServer.RPCAddr())
		withKey := func(req connect.AnyRequest) {
			req.Header().Add(types.ShardKey, "/"+docKey.String())
		}

		activateReq := connect.NewRequest(&api.ActivateClientRequest{ClientKey: t.Name()})
		withKey(activateReq)
		activated, err := raw.ActivateClient(ctx, activateReq)
		require.NoError(t, err)
		require.NotEqual(t, activated.Msg.ClientId, activated.Msg.ActorId)
		stableActor, err := time.ActorIDFromHex(activated.Msg.ActorId)
		require.NoError(t, err)

		pack, err := converter.ToChangePack(change.NewPack(
			docKey,
			change.NewCheckpoint(0, 1),
			[]*change.Change{change.New(
				change.NewID(1, 0, 1, stableActor, nil),
				"",
				nil,
				&inner.Change{ChangeType: inner.Put, Presence: inner.Presence{"name": "stable"}},
			)},
			nil,
			nil,
		))
		require.NoError(t, err)
		attachReq := connect.NewRequest(&api.AttachDocumentRequest{
			ClientId:   activated.Msg.ClientId,
			ChangePack: pack,
		})
		withKey(attachReq)
		_, err = raw.AttachDocument(ctx, attachReq)
		require.NoError(t, err)

		d := document.New(docKey)
		require.NoError(t, peer.Attach(ctx, d, client.WithPresence(presence.Data{"name": "peer"})))
		require.Equal(t, presence.Data{"name": "stable"}, d.AllPresences()[stableActor.String()])

		deactivateReq := connect.NewRequest(&api.DeactivateClientRequest{
			ClientId:    activated.Msg.ClientId,
			Synchronous: true,
		})
		withKey(deactivateReq)
		_, err = raw.DeactivateClient(ctx, deactivateReq)
		require.NoError(t, err)
		require.NoError(t, peer.Sync(ctx))

		assert.NotContains(t, d.AllPresences(), stableActor.String(),
			"a presence recorded under the stable actor should be cleared too")
	})
}

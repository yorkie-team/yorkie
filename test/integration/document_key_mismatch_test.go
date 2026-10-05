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
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server/rpc"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestChangePackKeyMismatch checks that PushPullChanges, DetachDocument and
// RemoveDocument reject a request whose change pack names another document
// than the one its DocumentId targets. The auth webhook is asked about the
// pack's key, while the request is applied to the DocumentId's document.
func TestChangePackKeyMismatch(t *testing.T) {
	ctx := context.Background()
	raw := v1connect.NewYorkieServiceClient(http.DefaultClient, "http://"+defaultServer.RPCAddr())
	withKey := func(req connect.AnyRequest, k key.Key) {
		req.Header().Add(types.ShardKey, "/"+k.String())
	}

	// setup activates a raw client and attaches it to a target and another
	// document. It returns the client, the target's ID and a pack that names
	// the other document but sets {"x":1} on whichever document applies it.
	setup := func(t *testing.T, target, other key.Key) (string, string, *api.ChangePack) {
		activateReq := connect.NewRequest(&api.ActivateClientRequest{ClientKey: t.Name()})
		withKey(activateReq, target)
		activated, err := raw.ActivateClient(ctx, activateReq)
		require.NoError(t, err)
		clientID := activated.Msg.ClientId
		actorID, err := time.ActorIDFromHex(clientID)
		require.NoError(t, err)

		attach := func(k key.Key) string {
			req := connect.NewRequest(&api.AttachDocumentRequest{
				ClientId:   clientID,
				ChangePack: &api.ChangePack{DocumentKey: k.String(), Checkpoint: &api.Checkpoint{}},
			})
			withKey(req, k)
			res, err := raw.AttachDocument(ctx, req)
			require.NoError(t, err)
			return res.Msg.DocumentId
		}
		targetID := attach(target)
		attach(other)

		ticket := time.NewTicket(1, 1, actorID)
		value, err := crdt.NewPrimitive(1, ticket)
		require.NoError(t, err)
		vv := time.NewVersionVector()
		vv.Set(actorID, 1)
		pack, err := converter.ToChangePack(change.NewPack(
			other,
			change.NewCheckpoint(0, 1),
			[]*change.Change{change.New(
				change.NewID(1, 0, 1, actorID, vv),
				"",
				[]operations.Operation{operations.NewSet(time.InitialTicket, "x", value, ticket)},
				nil,
			)},
			nil,
			nil,
		))
		require.NoError(t, err)
		return clientID, targetID, pack
	}

	// assertRejected checks the error and that the target is untouched.
	assertRejected := func(t *testing.T, err error, target key.Key) {
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		assert.ErrorContains(t, err, rpc.ErrDocumentKeyMismatch.Error())

		cli := activeClients(t, 1)
		defer deactivateAndCloseClients(t, cli)
		doc := document.New(target)
		require.NoError(t, cli[0].Attach(ctx, doc))
		assert.Equal(t, `{}`, doc.Marshal())
	}

	t.Run("PushPullChanges", func(t *testing.T) {
		target, other := helper.TestKey(t, 1), helper.TestKey(t, 2)
		clientID, targetID, pack := setup(t, target, other)
		req := connect.NewRequest(&api.PushPullChangesRequest{
			ClientId: clientID, DocumentId: targetID, ChangePack: pack,
		})
		withKey(req, target)
		_, err := raw.PushPullChanges(ctx, req)
		assertRejected(t, err, target)
	})

	t.Run("DetachDocument", func(t *testing.T) {
		target, other := helper.TestKey(t, 1), helper.TestKey(t, 2)
		clientID, targetID, pack := setup(t, target, other)
		req := connect.NewRequest(&api.DetachDocumentRequest{
			ClientId: clientID, DocumentId: targetID, ChangePack: pack,
		})
		withKey(req, target)
		_, err := raw.DetachDocument(ctx, req)
		assertRejected(t, err, target)
	})

	t.Run("RemoveDocument", func(t *testing.T) {
		target, other := helper.TestKey(t, 1), helper.TestKey(t, 2)
		clientID, targetID, pack := setup(t, target, other)
		pack.IsRemoved = true
		req := connect.NewRequest(&api.RemoveDocumentRequest{
			ClientId: clientID, DocumentId: targetID, ChangePack: pack,
		})
		withKey(req, target)
		_, err := raw.RemoveDocument(ctx, req)
		assertRejected(t, err, target)
	})
}

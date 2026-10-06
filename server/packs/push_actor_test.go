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

package packs_test

import (
	"context"
	"encoding/hex"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/documents"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestPushForeignActor pins that the actor of a pushed change is not a
// precondition of the push: `logForeignActors` warns when a change names an
// actor the pushing client row does not hold, but the push still lands. The
// server can only compare against the client row the request names in
// client_id, which is not a credential, so a rejection would refuse honest
// writers (an SDK that pushes pre-attach changes under the initial actor)
// without stopping a caller that holds a victim's identifier. See #2120, whose
// fix needs an authenticated client identity (#2114).
func TestPushForeignActor(t *testing.T) {
	ctx := context.Background()

	activate := func(t *testing.T, clientKey string) *api.ActivateClientResponse {
		res, err := testClient.ActivateClient(ctx, connect.NewRequest(&api.ActivateClientRequest{
			ClientKey: clientKey,
		}))
		require.NoError(t, err)
		require.NotEmpty(t, res.Msg.ActorId)
		require.NotEqual(t, res.Msg.ClientId, res.Msg.ActorId)
		return res.Msg
	}
	actorBytes := func(t *testing.T, hexID string) []byte {
		b, err := hex.DecodeString(hexID)
		require.NoError(t, err)
		return b
	}
	packWith := func(docKey string, clientSeq uint32, actor []byte) *api.ChangePack {
		return &api.ChangePack{
			DocumentKey: docKey,
			Checkpoint:  &api.Checkpoint{ClientSeq: clientSeq},
			Changes: []*api.Change{{
				Id: &api.ChangeID{ClientSeq: clientSeq, Lamport: int64(clientSeq), ActorId: actor},
			}},
		}
	}

	initialActor := time.InitialActorID.Bytes()

	t.Run("push under another actor is accepted", func(t *testing.T) {
		pusher := activate(t, helper.TestKey(t, 1).String())
		other := activate(t, helper.TestKey(t, 2).String())
		docKey := helper.TestKey(t).String()

		attached, err := testClient.AttachDocument(ctx, connect.NewRequest(&api.AttachDocumentRequest{
			ClientId:   pusher.ClientId,
			ChangePack: &api.ChangePack{DocumentKey: docKey, Checkpoint: &api.Checkpoint{}},
		}))
		require.NoError(t, err)

		docID := types.ID(attached.Msg.DocumentId)
		docRefKey := types.DocRefKey{ProjectID: database.DefaultProjectID, DocID: docID}
		docInfo, err := documents.FindDocInfoByRefKey(ctx, testBackend, docRefKey)
		require.NoError(t, err)

		push := func(clientSeq uint32, actor []byte) error {
			_, err := testClient.PushPullChanges(ctx, connect.NewRequest(&api.PushPullChangesRequest{
				ClientId:   pusher.ClientId,
				DocumentId: attached.Msg.DocumentId,
				ChangePack: packWith(docKey, clientSeq, actor),
			}))
			return err
		}

		// Another client's session id and stable actor, the initial actor and
		// the pusher's own two identities all land; only the first three are
		// logged.
		for i, actor := range [][]byte{
			actorBytes(t, other.ClientId),
			actorBytes(t, other.ActorId),
			initialActor,
			actorBytes(t, pusher.ClientId),
			actorBytes(t, pusher.ActorId),
		} {
			assert.NoError(t, push(uint32(i+1), actor))
		}

		docInfoAfter, err := documents.FindDocInfoByRefKey(ctx, testBackend, docRefKey)
		require.NoError(t, err)
		assert.Equal(t, docInfo.ServerSeq+5, docInfoAfter.ServerSeq)
	})

	t.Run("attach under another actor is accepted", func(t *testing.T) {
		attacher := activate(t, helper.TestKey(t, 1).String())
		other := activate(t, helper.TestKey(t, 2).String())

		attach := func(docKey string, actor []byte) error {
			_, err := testClient.AttachDocument(ctx, connect.NewRequest(&api.AttachDocumentRequest{
				ClientId:   attacher.ClientId,
				ChangePack: packWith(docKey, 1, actor),
			}))
			return err
		}

		// A pre-attach change keeps the initial actor in a client that skipped
		// SetActor, so refusing it would be a new wire precondition.
		assert.NoError(t, attach(helper.TestKey(t, 3).String(), initialActor))
		assert.NoError(t, attach(helper.TestKey(t, 4).String(), actorBytes(t, other.ActorId)))
		assert.NoError(t, attach(helper.TestKey(t, 5).String(), actorBytes(t, attacher.ClientId)))
		assert.NoError(t, attach(helper.TestKey(t, 6).String(), actorBytes(t, attacher.ActorId)))
	})

	// Why a push-side compare cannot be an authorization boundary:
	// StableActorID is derived from (project, client key) with no unique index,
	// so every session of one key stamps the same actor and the compare cannot
	// tell two of them apart.
	t.Run("sessions sharing a client key share one actor", func(t *testing.T) {
		clientKey := helper.TestKey(t, 1).String()
		first := activate(t, clientKey)
		second := activate(t, clientKey)
		require.NotEqual(t, first.ClientId, second.ClientId)
		require.Equal(t, first.ActorId, second.ActorId)

		docKey := helper.TestKey(t).String()
		emptyPack := func() *api.ChangePack {
			return &api.ChangePack{DocumentKey: docKey, Checkpoint: &api.Checkpoint{}}
		}
		attached, err := testClient.AttachDocument(ctx, connect.NewRequest(&api.AttachDocumentRequest{
			ClientId:   first.ClientId,
			ChangePack: emptyPack(),
		}))
		require.NoError(t, err)
		_, err = testClient.AttachDocument(ctx, connect.NewRequest(&api.AttachDocumentRequest{
			ClientId:   second.ClientId,
			ChangePack: emptyPack(),
		}))
		require.NoError(t, err)

		_, err = testClient.PushPullChanges(ctx, connect.NewRequest(&api.PushPullChangesRequest{
			ClientId:   second.ClientId,
			DocumentId: attached.Msg.DocumentId,
			ChangePack: packWith(docKey, 1, actorBytes(t, first.ActorId)),
		}))
		assert.NoError(t, err)
	})
}

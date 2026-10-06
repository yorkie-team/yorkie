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

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/documents"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestPushForeignActor pins the push-path actor gate over the wire:
// `validateChangeActors` refuses a change whose ID names an actor the pushing
// client row does not hold, with the ErrActorMismatch the Watch path already
// returns for the same compare, while the pusher's own two identities and the
// initial actor still land. A collaborator reads a peer's actor off the wire
// but not the client_id behind it, so this is what stops it from pushing a
// change the peer's own pull would then drop as a self-echo (#2120). What the
// compare cannot separate — two sessions of one client key, or a caller
// holding a victim's identifier — is pinned in the last subtest and needs an
// authenticated client identity (#2114).
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

	t.Run("push under another actor is refused", func(t *testing.T) {
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

		// Another client's session id and either of its actors are refused.
		// Nothing is stored, so the checkpoint does not move and the accepted
		// pushes below still start at clientSeq 1.
		for _, actor := range [][]byte{
			actorBytes(t, other.ClientId),
			actorBytes(t, other.ActorId),
		} {
			err := push(1, actor)
			assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
			assert.Equal(t, "ErrActorMismatch", converter.ErrorCodeOf(err))
		}

		// The initial actor and the pusher's own two identities land.
		for i, actor := range [][]byte{
			initialActor,
			actorBytes(t, pusher.ClientId),
			actorBytes(t, pusher.ActorId),
		} {
			assert.NoError(t, push(uint32(i+1), actor))
		}

		docInfoAfter, err := documents.FindDocInfoByRefKey(ctx, testBackend, docRefKey)
		require.NoError(t, err)
		assert.Equal(t, docInfo.ServerSeq+3, docInfoAfter.ServerSeq)
	})

	t.Run("attach under another actor is refused", func(t *testing.T) {
		attacher := activate(t, helper.TestKey(t, 1).String())
		other := activate(t, helper.TestKey(t, 2).String())

		attach := func(docKey string, actor []byte) error {
			_, err := testClient.AttachDocument(ctx, connect.NewRequest(&api.AttachDocumentRequest{
				ClientId:   attacher.ClientId,
				ChangePack: packWith(docKey, 1, actor),
			}))
			return err
		}

		// The attach pack goes through the same gate: another client's actor is
		// refused there too.
		err := attach(helper.TestKey(t, 4).String(), actorBytes(t, other.ActorId))
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
		assert.Equal(t, "ErrActorMismatch", converter.ErrorCodeOf(err))

		// A pre-attach change keeps the initial actor in a client that skipped
		// SetActor, so refusing it would be a new wire precondition.
		assert.NoError(t, attach(helper.TestKey(t, 3).String(), initialActor))
		assert.NoError(t, attach(helper.TestKey(t, 5).String(), actorBytes(t, attacher.ClientId)))
		assert.NoError(t, attach(helper.TestKey(t, 6).String(), actorBytes(t, attacher.ActorId)))
	})

	// The limit of the compare: StableActorID is derived from (project, client
	// key) with no unique index, so every session of one key stamps the same
	// actor and the gate cannot tell two of them apart. One session's change
	// still lands in the other's pull dedup; closing that needs an
	// authenticated client identity (#2114).
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

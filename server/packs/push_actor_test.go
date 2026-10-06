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
	"github.com/yorkie-team/yorkie/server/clients"
	"github.com/yorkie-team/yorkie/server/documents"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestPushActorCheck verifies that the server refuses a pushed change whose ID
// actor is not the pushing client's own, on push and on attach, and accepts
// the two actors a client may stamp: its session id (old SDKs, the Go client)
// and its stable actor (new SDKs). See #2120.
func TestPushActorCheck(t *testing.T) {
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
	assertActorMismatch := func(t *testing.T, err error) {
		t.Helper()
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
		assert.Equal(t, "ErrActorMismatch", converter.ErrorCodeOf(err))
	}

	initialActor := time.InitialActorID.Bytes()

	t.Run("push with another actor is rejected", func(t *testing.T) {
		attacker := activate(t, helper.TestKey(t, 1).String())
		victim := activate(t, helper.TestKey(t, 2).String())
		docKey := helper.TestKey(t).String()

		attached, err := testClient.AttachDocument(ctx, connect.NewRequest(&api.AttachDocumentRequest{
			ClientId:   attacker.ClientId,
			ChangePack: &api.ChangePack{DocumentKey: docKey, Checkpoint: &api.Checkpoint{}},
		}))
		require.NoError(t, err)

		docID := types.ID(attached.Msg.DocumentId)
		docRefKey := types.DocRefKey{ProjectID: database.DefaultProjectID, DocID: docID}
		clientRefKey := types.ClientRefKey{
			ProjectID: database.DefaultProjectID,
			ClientID:  types.ID(attacker.ClientId),
		}
		docInfo, err := documents.FindDocInfoByRefKey(ctx, testBackend, docRefKey)
		require.NoError(t, err)
		clientInfo, err := clients.FindActiveClientInfo(ctx, testBackend, clientRefKey)
		require.NoError(t, err)

		push := func(clientSeq uint32, actor []byte) error {
			_, err := testClient.PushPullChanges(ctx, connect.NewRequest(&api.PushPullChangesRequest{
				ClientId:   attacker.ClientId,
				DocumentId: attached.Msg.DocumentId,
				ChangePack: packWith(docKey, clientSeq, actor),
			}))
			return err
		}

		// 01. The victim's session id, its stable actor and the initial actor
		// are refused, and nothing is stored.
		for _, forged := range [][]byte{
			actorBytes(t, victim.ClientId),
			actorBytes(t, victim.ActorId),
			initialActor,
		} {
			assertActorMismatch(t, push(1, forged))
			assertRejectedPushPullUnchanged(
				t, ctx, docRefKey, clientRefKey, docID,
				docInfo.ServerSeq, clientInfo.Checkpoint(docID),
			)
		}

		// 02. The attacker's own session id and stable actor are accepted.
		assert.NoError(t, push(1, actorBytes(t, attacker.ClientId)))
		assert.NoError(t, push(2, actorBytes(t, attacker.ActorId)))

		docInfoAfter, err := documents.FindDocInfoByRefKey(ctx, testBackend, docRefKey)
		require.NoError(t, err)
		assert.Equal(t, docInfo.ServerSeq+2, docInfoAfter.ServerSeq)
	})

	t.Run("already pushed changes are not checked", func(t *testing.T) {
		cli := activate(t, helper.TestKey(t, 1).String())
		other := activate(t, helper.TestKey(t, 2).String())
		docKey := helper.TestKey(t).String()

		attached, err := testClient.AttachDocument(ctx, connect.NewRequest(&api.AttachDocumentRequest{
			ClientId:   cli.ClientId,
			ChangePack: packWith(docKey, 1, actorBytes(t, cli.ActorId)),
		}))
		require.NoError(t, err)

		// clientSeq 1 is already stored, so pushPack skips it whatever its
		// actor says; only clientSeq 2 is stored and checked.
		pack := packWith(docKey, 2, actorBytes(t, cli.ActorId))
		pack.Changes = append([]*api.Change{{
			Id: &api.ChangeID{ClientSeq: 1, Lamport: 1, ActorId: actorBytes(t, other.ActorId)},
		}}, pack.Changes...)
		_, err = testClient.PushPullChanges(ctx, connect.NewRequest(&api.PushPullChangesRequest{
			ClientId:   cli.ClientId,
			DocumentId: attached.Msg.DocumentId,
			ChangePack: pack,
		}))
		assert.NoError(t, err)
	})

	t.Run("attach with another actor is rejected", func(t *testing.T) {
		attacker := activate(t, helper.TestKey(t, 1).String())
		victim := activate(t, helper.TestKey(t, 2).String())

		attach := func(docKey string, actor []byte) error {
			_, err := testClient.AttachDocument(ctx, connect.NewRequest(&api.AttachDocumentRequest{
				ClientId:   attacker.ClientId,
				ChangePack: packWith(docKey, 1, actor),
			}))
			return err
		}

		// A pre-attach change keeps the initial actor only in a client that
		// skipped SetActor; both SDKs stamp their own actor before the attach.
		assertActorMismatch(t, attach(helper.TestKey(t, 3).String(), actorBytes(t, victim.ActorId)))
		assertActorMismatch(t, attach(helper.TestKey(t, 4).String(), initialActor))

		assert.NoError(t, attach(helper.TestKey(t, 5).String(), actorBytes(t, attacker.ClientId)))
		assert.NoError(t, attach(helper.TestKey(t, 6).String(), actorBytes(t, attacker.ActorId)))
	})

	// This pins the limit of the check rather than a guarantee: StableActorID
	// is derived from (project, client key) with no unique index, so every
	// session of one key stamps the same actor and the check cannot tell two
	// of them apart. A change one session stores under the shared actor is
	// still dropped by the other session's pull dedup as its own echo, so a
	// caller that holds a victim's client key keeps the #2120 hole. Closing it
	// needs an authenticated client identity (#2114), not a push-side compare.
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

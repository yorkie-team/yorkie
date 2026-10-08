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
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestPresencePatch drives a presence patch through the RPC surface: the server
// advertises the capability, folds the patch into a full put, and refuses a
// patch it has no base for.
func TestPresencePatch(t *testing.T) {
	ctx := context.Background()

	activate := func(t *testing.T, suffix string) (string, []byte) {
		resp, err := testClient.ActivateClient(ctx, connect.NewRequest(&api.ActivateClientRequest{
			ClientKey: helper.TestKey(t).String() + suffix,
		}))
		require.NoError(t, err)
		actorID, err := hex.DecodeString(resp.Msg.ClientId)
		require.NoError(t, err)
		return resp.Msg.ClientId, actorID
	}

	presenceChange := func(actorID []byte, clientSeq uint32, pc *api.PresenceChange) *api.Change {
		return &api.Change{
			Id:             &api.ChangeID{ClientSeq: clientSeq, Lamport: int64(clientSeq), ActorId: actorID},
			PresenceChange: pc,
		}
	}

	t.Run("a patch is folded into the full presence peers pull", func(t *testing.T) {
		docKey := helper.TestKey(t).String()
		clientID, actorID := activate(t, "-a")

		attachResp, err := testClient.AttachDocument(ctx, connect.NewRequest(&api.AttachDocumentRequest{
			ClientId: clientID,
			ChangePack: &api.ChangePack{
				DocumentKey: docKey,
				Checkpoint:  &api.Checkpoint{ClientSeq: 1},
				Changes: []*api.Change{presenceChange(actorID, 1, &api.PresenceChange{
					Type:     api.PresenceChange_CHANGE_TYPE_PUT,
					Presence: &api.Presence{Data: map[string]string{"name": "a", "selection": "x"}},
				})},
			},
		}))
		require.NoError(t, err)
		assert.True(t, api.HasCapability(attachResp.Msg.ChangePack.Capabilities, api.CapPresencePatch))
		docID := attachResp.Msg.DocumentId

		pushResp, err := testClient.PushPullChanges(ctx, connect.NewRequest(&api.PushPullChangesRequest{
			ClientId:   clientID,
			DocumentId: docID,
			ChangePack: &api.ChangePack{
				DocumentKey: docKey,
				Checkpoint:  &api.Checkpoint{ServerSeq: attachResp.Msg.ChangePack.Checkpoint.ServerSeq, ClientSeq: 2},
				Changes: []*api.Change{presenceChange(actorID, 2, &api.PresenceChange{
					Type:        api.PresenceChange_CHANGE_TYPE_PATCH,
					Presence:    &api.Presence{Data: map[string]string{"cursor": "1"}},
					RemovedKeys: []string{"selection"},
				})},
			},
		}))
		require.NoError(t, err)
		assert.True(t, api.HasCapability(pushResp.Msg.ChangePack.Capabilities, api.CapPresencePatch))

		peerID, peerActorID := activate(t, "-b")
		peerResp, err := testClient.AttachDocument(ctx, connect.NewRequest(&api.AttachDocumentRequest{
			ClientId: peerID,
			ChangePack: &api.ChangePack{
				DocumentKey: docKey,
				Checkpoint:  &api.Checkpoint{ClientSeq: 1},
				Changes: []*api.Change{presenceChange(peerActorID, 1, &api.PresenceChange{
					Type:     api.PresenceChange_CHANGE_TYPE_PUT,
					Presence: &api.Presence{Data: map[string]string{"name": "b"}},
				})},
			},
		}))
		require.NoError(t, err)

		var pulled []*api.PresenceChange
		for _, c := range peerResp.Msg.ChangePack.Changes {
			if hex.EncodeToString(c.Id.ActorId) == clientID && c.PresenceChange != nil {
				pulled = append(pulled, c.PresenceChange)
			}
		}
		require.Len(t, pulled, 2)
		assert.Equal(t, api.PresenceChange_CHANGE_TYPE_PUT, pulled[1].Type)
		assert.Equal(t, map[string]string{"name": "a", "cursor": "1"}, pulled[1].Presence.Data)
		assert.Empty(t, pulled[1].RemovedKeys)
	})

	// The point of the feature is that a client keeps patching: the base
	// commit stores after one push has to be the base the next push folds
	// onto, which only holds if it is keyed to the checkpoint the pull left
	// the client at. A second patch is where that contract is exercised
	// against the real pushPack and pullPack rather than a model of them.
	t.Run("consecutive patches from one client each fold onto the last", func(t *testing.T) {
		docKey := helper.TestKey(t).String()
		clientID, actorID := activate(t, "-e")

		attachResp, err := testClient.AttachDocument(ctx, connect.NewRequest(&api.AttachDocumentRequest{
			ClientId: clientID,
			ChangePack: &api.ChangePack{
				DocumentKey: docKey,
				Checkpoint:  &api.Checkpoint{ClientSeq: 1},
				Changes: []*api.Change{presenceChange(actorID, 1, &api.PresenceChange{
					Type:     api.PresenceChange_CHANGE_TYPE_PUT,
					Presence: &api.Presence{Data: map[string]string{"name": "a", "selection": "x"}},
				})},
			},
		}))
		require.NoError(t, err)
		docID := attachResp.Msg.DocumentId
		serverSeq := attachResp.Msg.ChangePack.Checkpoint.ServerSeq

		push := func(clientSeq uint32, pc *api.PresenceChange) *api.ChangePack {
			resp, err := testClient.PushPullChanges(ctx, connect.NewRequest(&api.PushPullChangesRequest{
				ClientId:   clientID,
				DocumentId: docID,
				ChangePack: &api.ChangePack{
					DocumentKey: docKey,
					Checkpoint:  &api.Checkpoint{ServerSeq: serverSeq, ClientSeq: clientSeq},
					Changes:     []*api.Change{presenceChange(actorID, clientSeq, pc)},
				},
			}))
			require.NoError(t, err)
			serverSeq = resp.Msg.ChangePack.Checkpoint.ServerSeq
			return resp.Msg.ChangePack
		}

		push(2, &api.PresenceChange{
			Type:        api.PresenceChange_CHANGE_TYPE_PATCH,
			Presence:    &api.Presence{Data: map[string]string{"cursor": "1"}},
			RemovedKeys: []string{"selection"},
		})
		// The second patch has to fold onto the presence the first one
		// produced: it only sets "cursor", so "name" survives from the put
		// and "selection" stays removed.
		push(3, &api.PresenceChange{
			Type:     api.PresenceChange_CHANGE_TYPE_PATCH,
			Presence: &api.Presence{Data: map[string]string{"cursor": "2"}},
		})

		peerID, peerActorID := activate(t, "-f")
		peerResp, err := testClient.AttachDocument(ctx, connect.NewRequest(&api.AttachDocumentRequest{
			ClientId: peerID,
			ChangePack: &api.ChangePack{
				DocumentKey: docKey,
				Checkpoint:  &api.Checkpoint{ClientSeq: 1},
				Changes: []*api.Change{presenceChange(peerActorID, 1, &api.PresenceChange{
					Type:     api.PresenceChange_CHANGE_TYPE_PUT,
					Presence: &api.Presence{Data: map[string]string{"name": "b"}},
				})},
			},
		}))
		require.NoError(t, err)

		var pulled []*api.PresenceChange
		for _, c := range peerResp.Msg.ChangePack.Changes {
			if hex.EncodeToString(c.Id.ActorId) == clientID && c.PresenceChange != nil {
				pulled = append(pulled, c.PresenceChange)
			}
		}
		require.Len(t, pulled, 3)
		for _, pc := range pulled {
			assert.Equal(t, api.PresenceChange_CHANGE_TYPE_PUT, pc.Type)
			assert.Empty(t, pc.RemovedKeys)
		}
		assert.Equal(t, map[string]string{"name": "a", "cursor": "1"}, pulled[1].Presence.Data)
		assert.Equal(t, map[string]string{"name": "a", "cursor": "2"}, pulled[2].Presence.Data)
	})

	// A detach zeroes the client's checkpoint to what a fresh attach seeds, so
	// a base kept past it would fold the next attachment's first patch onto
	// presence from the previous one instead of refusing it.
	t.Run("a base does not survive a detach and re-attach", func(t *testing.T) {
		docKey := helper.TestKey(t).String()
		clientID, actorID := activate(t, "-g")

		attachResp, err := testClient.AttachDocument(ctx, connect.NewRequest(&api.AttachDocumentRequest{
			ClientId: clientID,
			ChangePack: &api.ChangePack{
				DocumentKey: docKey,
				Checkpoint:  &api.Checkpoint{ClientSeq: 1},
				Changes: []*api.Change{presenceChange(actorID, 1, &api.PresenceChange{
					Type:     api.PresenceChange_CHANGE_TYPE_PUT,
					Presence: &api.Presence{Data: map[string]string{"name": "a"}},
				})},
			},
		}))
		require.NoError(t, err)

		// Detach without a clear: nothing in the pack drops the base, so only
		// the detach itself can.
		_, err = testClient.DetachDocument(ctx, connect.NewRequest(&api.DetachDocumentRequest{
			ClientId:   clientID,
			DocumentId: attachResp.Msg.DocumentId,
			ChangePack: &api.ChangePack{
				DocumentKey: docKey,
				Checkpoint:  &api.Checkpoint{ServerSeq: attachResp.Msg.ChangePack.Checkpoint.ServerSeq, ClientSeq: 1},
			},
		}))
		require.NoError(t, err)

		_, err = testClient.AttachDocument(ctx, connect.NewRequest(&api.AttachDocumentRequest{
			ClientId: clientID,
			ChangePack: &api.ChangePack{
				DocumentKey: docKey,
				Checkpoint:  &api.Checkpoint{ClientSeq: 1},
				Changes: []*api.Change{presenceChange(actorID, 1, &api.PresenceChange{
					Type:     api.PresenceChange_CHANGE_TYPE_PATCH,
					Presence: &api.Presence{Data: map[string]string{"cursor": "1"}},
				})},
			},
		}))
		require.Error(t, err)
		assert.Equal(t, "ErrPresenceBaseUnavailable", converter.ErrorCodeOf(err))
	})

	// The detach pack ends with a clear, so a pending patch the server has no
	// base for must not fail it.
	t.Run("a detach carrying a patch without a base goes through", func(t *testing.T) {
		docKey := helper.TestKey(t).String()
		clientID, actorID := activate(t, "-d")

		attachResp, err := testClient.AttachDocument(ctx, connect.NewRequest(&api.AttachDocumentRequest{
			ClientId:   clientID,
			ChangePack: &api.ChangePack{DocumentKey: docKey, Checkpoint: &api.Checkpoint{}},
		}))
		require.NoError(t, err)

		_, err = testClient.DetachDocument(ctx, connect.NewRequest(&api.DetachDocumentRequest{
			ClientId:   clientID,
			DocumentId: attachResp.Msg.DocumentId,
			ChangePack: &api.ChangePack{
				DocumentKey: docKey,
				Checkpoint:  &api.Checkpoint{ServerSeq: attachResp.Msg.ChangePack.Checkpoint.ServerSeq, ClientSeq: 2},
				Changes: []*api.Change{
					presenceChange(actorID, 1, &api.PresenceChange{
						Type:     api.PresenceChange_CHANGE_TYPE_PATCH,
						Presence: &api.Presence{Data: map[string]string{"cursor": "1"}},
					}),
					presenceChange(actorID, 2, &api.PresenceChange{Type: api.PresenceChange_CHANGE_TYPE_CLEAR}),
				},
			},
		}))
		assert.NoError(t, err)
	})

	t.Run("a patch without a base is refused", func(t *testing.T) {
		docKey := helper.TestKey(t).String()
		clientID, actorID := activate(t, "-c")

		_, err := testClient.AttachDocument(ctx, connect.NewRequest(&api.AttachDocumentRequest{
			ClientId: clientID,
			ChangePack: &api.ChangePack{
				DocumentKey: docKey,
				Checkpoint:  &api.Checkpoint{ClientSeq: 1},
				Changes: []*api.Change{presenceChange(actorID, 1, &api.PresenceChange{
					Type:     api.PresenceChange_CHANGE_TYPE_PATCH,
					Presence: &api.Presence{Data: map[string]string{"cursor": "1"}},
				})},
			},
		}))
		require.Error(t, err)
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
		assert.Equal(t, "ErrPresenceBaseUnavailable", converter.ErrorCodeOf(err))
	})
}

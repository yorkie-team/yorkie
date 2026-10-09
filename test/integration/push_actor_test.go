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
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestPushActorCheck covers #2120: a change pushed under another client's
// actor used to be stored and then dropped on that client's pull as its own
// echo, so the victim never converged. `PushPull` now refuses it — see the
// first subtest — and the honest flows of the Go client keep working.
func TestPushActorCheck(t *testing.T) {
	ctx := context.Background()

	// Reproducer for #2120: a second client pushes a change stamped with the
	// victim's actor, at a clientSeq the victim's checkpoint already covers,
	// which is what made the victim's pull drop it as its own echo.
	t.Run("change stamped with the victim's actor is refused", func(t *testing.T) {
		clis := activeClients(t, 1)
		defer deactivateAndCloseClients(t, clis)
		victim := clis[0]

		docKey := helper.TestKey(t)
		doc := document.New(docKey)
		require.NoError(t, victim.Attach(ctx, doc))

		raw := v1connect.NewYorkieServiceClient(http.DefaultClient, "http://"+defaultServer.RPCAddr())
		withKey := func(req connect.AnyRequest) {
			req.Header().Add(types.ShardKey, "/"+docKey.String())
		}
		activateReq := connect.NewRequest(&api.ActivateClientRequest{ClientKey: t.Name()})
		withKey(activateReq)
		activated, err := raw.ActivateClient(ctx, activateReq)
		require.NoError(t, err)
		attachReq := connect.NewRequest(&api.AttachDocumentRequest{
			ClientId:   activated.Msg.ClientId,
			ChangePack: &api.ChangePack{DocumentKey: docKey.String(), Checkpoint: &api.Checkpoint{}},
		})
		withKey(attachReq)
		attached, err := raw.AttachDocument(ctx, attachReq)
		require.NoError(t, err)

		// push sends {"x":1} as clientSeq 1 under the given actor. clientSeq 1
		// is at or below the victim's checkpoint, which is what made the pull
		// filter drop it as the victim's own echo.
		push := func(actor time.ActorID) error {
			ticket := time.NewTicket(1, 1, actor)
			value, err := crdt.NewPrimitive(1, ticket)
			require.NoError(t, err)
			vv := time.NewVersionVector()
			vv.Set(actor, 1)
			pb, err := converter.ToChangePack(change.NewPack(
				docKey,
				change.NewCheckpoint(0, 1),
				[]*change.Change{change.New(
					change.NewID(1, 0, 1, actor, vv),
					"",
					[]operations.Operation{operations.NewSet(time.InitialTicket, "x", value, ticket)},
					nil,
				)},
				nil,
				nil,
			))
			require.NoError(t, err)
			req := connect.NewRequest(&api.PushPullChangesRequest{
				ClientId: activated.Msg.ClientId, DocumentId: attached.Msg.DocumentId, ChangePack: pb,
			})
			withKey(req)
			_, err = raw.PushPullChanges(ctx, req)
			return err
		}

		err = push(victim.ID())
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
		assert.Equal(t, "ErrActorMismatch", converter.ErrorCodeOf(err))

		// The same change under the pusher's own actor reaches the victim.
		own, err := time.ActorIDFromHex(activated.Msg.ClientId)
		require.NoError(t, err)
		require.NoError(t, push(own))
		require.NoError(t, victim.Sync(ctx))
		assert.Equal(t, `{"x":1}`, doc.Marshal())
	})

	t.Run("honest flows still converge", func(t *testing.T) {
		clis := activeClients(t, 2)
		defer deactivateAndCloseClients(t, clis)
		c1, c2 := clis[0], clis[1]
		docKey := helper.TestKey(t)

		set := func(d *document.Document, k string, v int) {
			require.NoError(t, d.Update(func(r *json.Object, _ *presence.Presence) error {
				r.SetInteger(k, v)
				return nil
			}))
		}

		// 01. Both clients edit before attaching, then push and pull.
		d1 := document.New(docKey)
		set(d1, "a", 1)
		require.NoError(t, c1.Attach(ctx, d1))
		d2 := document.New(docKey)
		set(d2, "b", 2)
		require.NoError(t, c2.Attach(ctx, d2))
		set(d1, "c", 3)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		// 02. c1 reactivates, which detaches d1 and takes a new session id, and
		// attaches a fresh document of the key with a pre-attach edit. (A
		// re-attach under the same actor is not used: the pull filter skips
		// the actor's earlier changes, a limitation of the pre-attach
		// re-issue design unrelated to this check.)
		require.NoError(t, c1.Deactivate(ctx))
		require.NoError(t, c1.Activate(ctx))
		d1 = document.New(docKey)
		set(d1, "e", 5)
		require.NoError(t, c1.Attach(ctx, d1))
		set(d1, "f", 6)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, `{"a":1,"b":2,"c":3,"e":5,"f":6}`, d2.Marshal())
	})
}

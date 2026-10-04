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

// TestPushedPayloadValidation pins the push boundary at the RPC layer: a
// change whose element payload no replica can produce is refused with
// InvalidArgument by every RPC that takes a client's changes, nothing of it
// reaches the document, and the client can still push an honest change under
// the same client seq.
func TestPushedPayloadValidation(t *testing.T) {
	ctx := context.Background()
	clients := activeClients(t, 1)
	c1 := clients[0]
	defer deactivateAndCloseClients(t, clients)

	docKey := helper.TestKey(t)
	d1 := document.New(docKey)
	require.NoError(t, c1.Attach(ctx, d1))

	raw := v1connect.NewYorkieServiceClient(http.DefaultClient, "http://"+defaultServer.RPCAddr())
	withKey := func(req connect.AnyRequest, k string) {
		req.Header().Add(types.ShardKey, "/"+k)
	}

	activateReq := connect.NewRequest(&api.ActivateClientRequest{ClientKey: t.Name()})
	withKey(activateReq, t.Name())
	activated, err := raw.ActivateClient(ctx, activateReq)
	require.NoError(t, err)
	actorID, err := time.ActorIDFromHex(activated.Msg.ClientId)
	require.NoError(t, err)

	rootCreatedAt := d1.RootObject().CreatedAt()

	// newPack builds a pack of one change at client seq 1 that sets "k" to an
	// object; crafted gives the object's member a removedAt that does not
	// follow its own createdAt, which no replica emits.
	newPack := func(t *testing.T, crafted bool) *api.ChangePack {
		t.Helper()
		lamport := int64(10)
		ticket := time.NewTicket(lamport, 1, actorID)
		obj := crdt.NewObject(crdt.NewElementRHT(), ticket)
		member, err := crdt.NewPrimitive("v", time.NewTicket(lamport, 2, actorID))
		require.NoError(t, err)
		obj.Set("m", member)
		if crafted {
			member.SetRemovedAt(member.CreatedAt())
		}
		vv := time.NewVersionVector()
		vv.Set(actorID, lamport)
		c := change.New(
			change.NewID(1, 0, lamport, actorID, vv),
			"",
			[]operations.Operation{operations.NewSet(rootCreatedAt, "k", obj, ticket)},
			nil,
		)
		pbPack, err := converter.ToChangePack(change.NewPack(
			docKey, change.NewCheckpoint(0, 1), []*change.Change{c}, nil, nil,
		))
		require.NoError(t, err)
		return pbPack
	}

	// 01. Attach carrying the crafted change is refused.
	attachReq := connect.NewRequest(&api.AttachDocumentRequest{
		ClientId:   activated.Msg.ClientId,
		ChangePack: newPack(t, true),
	})
	withKey(attachReq, docKey.String())
	_, err = raw.AttachDocument(ctx, attachReq)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "attach: %v", err)

	// 02. An empty attach goes through.
	attachReq = connect.NewRequest(&api.AttachDocumentRequest{
		ClientId: activated.Msg.ClientId,
		ChangePack: &api.ChangePack{
			DocumentKey: docKey.String(),
			Checkpoint:  &api.Checkpoint{},
		},
	})
	withKey(attachReq, docKey.String())
	attached, err := raw.AttachDocument(ctx, attachReq)
	require.NoError(t, err)

	// 03. PushPull and Detach carrying the crafted change are refused.
	pushReq := connect.NewRequest(&api.PushPullChangesRequest{
		ClientId:   activated.Msg.ClientId,
		DocumentId: attached.Msg.DocumentId,
		ChangePack: newPack(t, true),
	})
	withKey(pushReq, docKey.String())
	_, err = raw.PushPullChanges(ctx, pushReq)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "push-pull: %v", err)

	detachReq := connect.NewRequest(&api.DetachDocumentRequest{
		ClientId:   activated.Msg.ClientId,
		DocumentId: attached.Msg.DocumentId,
		ChangePack: newPack(t, true),
	})
	withKey(detachReq, docKey.String())
	_, err = raw.DetachDocument(ctx, detachReq)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "detach: %v", err)

	// 04. Nothing of it reached the document.
	require.NoError(t, c1.Sync(ctx))
	assert.Equal(t, `{}`, d1.Marshal())

	// 05. The checkpoint did not move, so the honest change at the same
	// client seq goes through and reaches the other client.
	pushReq = connect.NewRequest(&api.PushPullChangesRequest{
		ClientId:   activated.Msg.ClientId,
		DocumentId: attached.Msg.DocumentId,
		ChangePack: newPack(t, false),
	})
	withKey(pushReq, docKey.String())
	_, err = raw.PushPullChanges(ctx, pushReq)
	require.NoError(t, err)

	require.NoError(t, c1.Sync(ctx))
	assert.Equal(t, `{"k":{"m":"v"}}`, d1.Marshal())

	// 06. A document set by two clients before attach -- the shape
	// BenchmarkRPC's "attach large document" drives -- attaches through the
	// boundary from both sides.
	pre1, pre2 := document.New(helper.TestKey(t)+"-pre"), document.New(helper.TestKey(t)+"-pre")
	for _, d := range []*document.Document{pre1, pre2} {
		require.NoError(t, d.Update(func(r *json.Object, _ *presence.Presence) error {
			r.SetNewText("k1").Edit(0, 0, "abc")
			return nil
		}))
	}
	others := activeClients(t, 2)
	defer deactivateAndCloseClients(t, others)
	require.NoError(t, others[0].Attach(ctx, pre1))
	require.NoError(t, others[1].Attach(ctx, pre2))
	require.NoError(t, others[0].Sync(ctx))
	assert.Equal(t, pre1.Marshal(), pre2.Marshal())
}

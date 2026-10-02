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

// TestCounterDoubleDeltaFromJS pushes Counter increases carrying a Double
// delta, which is what a JS client sends for `counter.increase(1.5)`, and
// checks that a Go client and the server's own replay can apply them.
func TestCounterDoubleDeltaFromJS(t *testing.T) {
	ctx := context.Background()
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	// 01. c1 creates a counter.
	docKey := helper.TestKey(t)
	d1 := document.New(docKey)
	require.NoError(t, c1.Attach(ctx, d1))
	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewCounter("cnt", 10)
		return nil
	}))
	require.NoError(t, c1.Sync(ctx))
	cntCreatedAt := d1.Root().GetCounter("cnt").CreatedAt()

	// 02. A raw RPC client pushes Double deltas, more of them than the
	// snapshot threshold so that a later attach makes the server replay them.
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

	attachReq := connect.NewRequest(&api.AttachDocumentRequest{
		ClientId: activated.Msg.ClientId,
		ChangePack: &api.ChangePack{
			DocumentKey: docKey.String(),
			Checkpoint:  &api.Checkpoint{},
		},
	})
	withKey(attachReq, docKey.String())
	attached, err := raw.AttachDocument(ctx, attachReq)
	require.NoError(t, err)

	n := int(helper.SnapshotThreshold) + 1
	var changes []*change.Change
	for i := 1; i <= n; i++ {
		lamport := int64(100 + i)
		ticket := time.NewTicket(lamport, 1, actorID)
		delta, err := crdt.NewPrimitive(1.5, ticket)
		require.NoError(t, err)
		vv := time.NewVersionVector()
		vv.Set(actorID, lamport)
		changes = append(changes, change.New(
			change.NewID(uint32(i), 0, lamport, actorID, vv),
			"",
			[]operations.Operation{operations.NewIncrease(cntCreatedAt, delta, ticket)},
			nil,
		))
	}
	pbPack, err := converter.ToChangePack(change.NewPack(
		docKey, change.NewCheckpoint(0, uint32(n)), changes, nil, nil,
	))
	require.NoError(t, err)
	pushReq := connect.NewRequest(&api.PushPullChangesRequest{
		ClientId:   activated.Msg.ClientId,
		DocumentId: attached.Msg.DocumentId,
		ChangePack: pbPack,
	})
	withKey(pushReq, docKey.String())
	_, err = raw.PushPullChanges(ctx, pushReq)
	require.NoError(t, err)

	// Each 1.5 is truncated to 1, as in JS: 10 + 11 = 21.
	want := `{"cnt":21}`

	// 03. c1 applies them as remote changes.
	assert.NoError(t, c1.Sync(ctx))
	assert.Equal(t, want, d1.Marshal())

	// 04. c2 attaches; the server builds the document by replaying the
	// change log (pullSnapshot), which executes every Double delta.
	d2 := document.New(docKey)
	assert.NoError(t, c2.Attach(ctx, d2))
	assert.Equal(t, want, d2.Marshal())
}

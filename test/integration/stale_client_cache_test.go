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
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestStaleClientCache covers a push admitted on a node whose client cache
// has not seen another node's deactivation yet. The RPC gate reads the cached
// client, which still says activated and attached; the push must still be
// refused before its changes reach the document, and the client row must keep
// what the deactivation wrote.
func TestStaleClientCache(t *testing.T) {
	ctx := context.Background()

	// A second node on the same database. defaultServer is the first.
	nodeB, err := server.New(helper.TestConfig())
	require.NoError(t, err)
	require.NoError(t, nodeB.Start())
	defer func() { assert.NoError(t, nodeB.Shutdown(true)) }()

	c1, err := client.Dial(defaultServer.RPCAddr())
	require.NoError(t, err)
	// Close tries to deactivate c1 on the server, which the second node has
	// already done, so its error is expected.
	defer func() { _ = c1.Close() }()
	require.NoError(t, c1.Activate(ctx))

	// c1 attaches and pushes through the first node, which caches c1 as
	// activated with the document attached.
	d1 := document.New(helper.TestKey(t))
	require.NoError(t, c1.Attach(ctx, d1))
	require.NoError(t, d1.Update(func(r *json.Object, p *presence.Presence) error {
		r.SetString("k1", "v1")
		return nil
	}))
	require.NoError(t, c1.Sync(ctx))

	// The second node deactivates c1, as its housekeeping would. Nothing
	// tells the first node.
	require.NoError(t, nodeB.DeactivateClient(ctx, c1))

	// Count the stored changes in the database itself: each node answers
	// from caches this scenario leaves stale on purpose.
	project, err := defaultServer.DefaultProject(ctx)
	require.NoError(t, err)
	docInfo, err := nodeB.Backend().DB.FindDocInfoByKey(ctx, project.ID, d1.Key())
	require.NoError(t, err)
	before, err := helper.CountChangesWithDocID(helper.TestDBName(), docInfo.ID)
	require.NoError(t, err)

	// c1 does not know either, and pushes again through the first node.
	require.NoError(t, d1.Update(func(r *json.Object, p *presence.Presence) error {
		r.SetString("k2", "v2")
		return nil
	}))
	// Push twice. The detach moved the document's server seq, so without the
	// client check the first push would stop at the first node's stale
	// document cache (ErrConflictOnUpdate, which drops the entry) and the
	// second would land.
	for range 2 {
		err = c1.Sync(ctx)
	}
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	assert.Equal(t, "ErrClientNotActivated", converter.ErrorCodeOf(err))

	// The refused change did not reach the document.
	after, err := helper.CountChangesWithDocID(helper.TestDBName(), docInfo.ID)
	require.NoError(t, err)
	assert.Equal(t, before, after)

	// The client row still holds the deactivation and the detach.
	stored, err := nodeB.Backend().DB.FindClientInfoByRefKey(ctx, types.ClientRefKey{
		ProjectID: project.ID,
		ClientID:  types.IDFromActorID(c1.ID()),
	}, true)
	require.NoError(t, err)
	assert.Equal(t, database.ClientDeactivated, stored.Status)
	for docID, docInfo := range stored.Documents {
		assert.NotEqual(t, database.DocumentAttached, docInfo.Status, docID)
	}
}

// TestStaleClientCacheWatch covers the read side of the same staleness: a
// watch stream and a broadcast admitted on a node whose client cache has not
// seen another node's deactivation yet. Neither writes the document, so the
// conditional write-back in PushPull never sees them; they must be refused on
// the client row the database holds, not on the cached copy.
func TestStaleClientCacheWatch(t *testing.T) {
	ctx := context.Background()

	// A second node on the same database. defaultServer is the first.
	nodeB, err := server.New(helper.TestConfig())
	require.NoError(t, err)
	require.NoError(t, nodeB.Start())
	defer func() { assert.NoError(t, nodeB.Shutdown(true)) }()

	c1, err := client.Dial(defaultServer.RPCAddr())
	require.NoError(t, err)
	// Close tries to deactivate c1 on the server, which the second node has
	// already done, so its error is expected.
	defer func() { _ = c1.Close() }()
	require.NoError(t, c1.Activate(ctx))

	// The attach runs through the first node, which caches c1 as activated
	// with the document attached.
	d1 := document.New(helper.TestKey(t))
	require.NoError(t, c1.Attach(ctx, d1))

	project, err := defaultServer.DefaultProject(ctx)
	require.NoError(t, err)
	docInfo, err := nodeB.Backend().DB.FindDocInfoByKey(ctx, project.ID, d1.Key())
	require.NoError(t, err)

	// The second node deactivates c1. Nothing tells the first node.
	require.NoError(t, nodeB.DeactivateClient(ctx, c1))

	raw := v1connect.NewYorkieServiceClient(http.DefaultClient, "http://"+defaultServer.RPCAddr())
	withKey := func(req connect.AnyRequest, k string) {
		req.Header().Add(types.ShardKey, "/"+k)
	}

	// A watch opened through the first node is refused: the deactivated client
	// must not be handed the document's events or its peer presence.
	watchReq := connect.NewRequest(&api.WatchRequest{
		ClientId: c1.ID().String(),
		Resources: []*api.ResourceDescriptor{{
			Resource: &api.ResourceDescriptor_Document{
				Document: &api.DocumentDescriptor{DocumentId: docInfo.ID.String()},
			},
		}},
	})
	withKey(watchReq, d1.Key().String())
	stream, err := raw.Watch(ctx, watchReq)
	require.NoError(t, err)
	assert.False(t, stream.Receive())
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(stream.Err()))
	assert.Equal(t, "ErrClientNotActivated", converter.ErrorCodeOf(stream.Err()))
	assert.NoError(t, stream.Close())

	// So is a broadcast, which would otherwise publish arbitrary payloads to
	// every subscriber of the channel.
	broadcastReq := connect.NewRequest(&api.BroadcastRequest{
		ClientId:   c1.ID().String(),
		ChannelKey: d1.Key().String(),
		Topic:      "topic",
		Payload:    []byte(`"payload"`),
	})
	withKey(broadcastReq, d1.Key().String())
	_, err = raw.Broadcast(ctx, broadcastReq)
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	assert.Equal(t, "ErrClientNotActivated", converter.ErrorCodeOf(err))
}

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
	"strings"
	"testing"
	gotime "time"

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
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestDocumentSizeGate pushes past MaxSizePerDocument over raw RPC calls,
// which skip the SDK's own size check, and expects the server to refuse.
func TestDocumentSizeGate(t *testing.T) {
	ctx := context.Background()
	svr, err := server.New(helper.TestConfig())
	require.NoError(t, err)
	require.NoError(t, svr.Start())
	defer func() { assert.NoError(t, svr.Shutdown(true)) }()

	adminCli := helper.CreateAdminCli(t, svr.RPCAddr())
	defer func() { adminCli.Close() }()

	// A snapshot after every change, so the size the gate reads catches up
	// with each push.
	sizeLimit := 1024
	snapshotInterval := int64(1)
	project, err := adminCli.CreateProject(ctx, "size-gate-test")
	require.NoError(t, err)
	project, err = adminCli.UpdateProject(ctx, project.ID.String(), &types.UpdatableProjectFields{
		MaxSizePerDocument: &sizeLimit,
		SnapshotInterval:   &snapshotInterval,
	})
	require.NoError(t, err)

	docKey := helper.TestKey(t)
	raw := v1connect.NewYorkieServiceClient(http.DefaultClient, "http://"+svr.RPCAddr())
	withHeaders := func(req connect.AnyRequest, shard string) {
		req.Header().Set(types.APIKeyKey, project.PublicKey)
		req.Header().Set(types.ShardKey, project.PublicKey+"/"+shard)
	}

	activateReq := connect.NewRequest(&api.ActivateClientRequest{ClientKey: t.Name()})
	withHeaders(activateReq, t.Name())
	activated, err := raw.ActivateClient(ctx, activateReq)
	require.NoError(t, err)
	actorID, err := time.ActorIDFromHex(activated.Msg.ClientId)
	require.NoError(t, err)

	attachReq := connect.NewRequest(&api.AttachDocumentRequest{
		ClientId:   activated.Msg.ClientId,
		ChangePack: &api.ChangePack{DocumentKey: docKey.String(), Checkpoint: &api.Checkpoint{}},
	})
	withHeaders(attachReq, docKey.String())
	attached, err := raw.AttachDocument(ctx, attachReq)
	require.NoError(t, err)
	refKey := types.DocRefKey{ProjectID: project.ID, DocID: types.ID(attached.Msg.DocumentId)}

	// The raw client's view of the document. document.New sets no size
	// limit, so nothing on this side stops an update.
	doc := document.New(docKey)
	doc.SetActor(actorID)
	push := func() error {
		pbPack, err := converter.ToChangePack(doc.CreateChangePack())
		require.NoError(t, err)
		req := connect.NewRequest(&api.PushPullChangesRequest{
			ClientId:   activated.Msg.ClientId,
			DocumentId: attached.Msg.DocumentId,
			ChangePack: pbPack,
		})
		withHeaders(req, docKey.String())
		res, err := raw.PushPullChanges(ctx, req)
		if err != nil {
			return err
		}
		resPack, err := converter.FromChangePack(res.Msg.ChangePack)
		require.NoError(t, err)
		return doc.ApplyChangePack(resPack)
	}
	liveSize := func() int64 {
		info, err := svr.Backend().DB.FindClosestSnapshotInfo(ctx, refKey, change.MaxCheckpoint.ServerSeq, false)
		require.NoError(t, err)
		return info.LiveSize
	}
	serverSeq := func() int64 {
		info, err := svr.Backend().DB.FindDocInfoByRefKey(ctx, refKey)
		require.NoError(t, err)
		return info.ServerSeq
	}

	t.Run("growth past the quota is refused", func(t *testing.T) {
		// No snapshot yet, so the size is unknown and the push goes through.
		require.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetString("big", strings.Repeat("a", 4*sizeLimit))
			return nil
		}))
		require.NoError(t, push())
		assert.Eventually(t, func() bool {
			return liveSize() > int64(sizeLimit)
		}, 5*gotime.Second, 50*gotime.Millisecond)

		before := serverSeq()
		require.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetString("more", "x")
			return nil
		}))
		err := push()
		assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))
		assert.Equal(t, "ErrDocumentSizeExceedsLimit", converter.ErrorCodeOf(err))
		assert.Equal(t, before, serverSeq())
	})

	t.Run("a remove-only pack on an over-quota document is admitted", func(t *testing.T) {
		cli, err := client.Dial(svr.RPCAddr(), client.WithAPIKey(project.PublicKey))
		require.NoError(t, err)
		defer func() { assert.NoError(t, cli.Close()) }()
		require.NoError(t, cli.Activate(ctx))
		defer func() { assert.NoError(t, cli.Deactivate(ctx)) }()

		d2 := document.New(docKey)
		require.NoError(t, cli.Attach(ctx, d2))
		// The SDK's own check compares Live+GC, which a deletion does not
		// shrink; lift it to reach the server's gate.
		d2.SetMaxSizeLimit(0)

		before := serverSeq()
		require.NoError(t, d2.Update(func(r *json.Object, p *presence.Presence) error {
			r.Delete("big")
			return nil
		}))
		require.NoError(t, cli.Sync(ctx))
		assert.Equal(t, before+1, serverSeq())

		// Once a snapshot measures the smaller document, growth is admitted
		// again: the gate does not wedge a document that went over.
		assert.Eventually(t, func() bool {
			return liveSize() <= int64(sizeLimit)
		}, 5*gotime.Second, 50*gotime.Millisecond)
		require.NoError(t, d2.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetString("small", "y")
			return nil
		}))
		require.NoError(t, cli.Sync(ctx))
		assert.Equal(t, `{"small":"y"}`, d2.Marshal())
		require.NoError(t, cli.Detach(ctx, d2))
	})

	t.Run("detach goes through without the refused changes", func(t *testing.T) {
		// The raw client still holds the change refused above. Grow the
		// document over the quota again so the detach meets the gate.
		cli, err := client.Dial(svr.RPCAddr(), client.WithAPIKey(project.PublicKey))
		require.NoError(t, err)
		defer func() { assert.NoError(t, cli.Close()) }()
		require.NoError(t, cli.Activate(ctx))
		defer func() { assert.NoError(t, cli.Deactivate(ctx)) }()
		d3 := document.New(docKey)
		require.NoError(t, cli.Attach(ctx, d3))
		d3.SetMaxSizeLimit(0)
		require.NoError(t, d3.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetString("big", strings.Repeat("b", 4*sizeLimit))
			return nil
		}))
		require.NoError(t, cli.Sync(ctx))
		assert.Eventually(t, func() bool {
			return liveSize() > int64(sizeLimit)
		}, 5*gotime.Second, 50*gotime.Millisecond)

		before := serverSeq()
		pbPack, err := converter.ToChangePack(doc.CreateChangePack())
		require.NoError(t, err)
		req := connect.NewRequest(&api.DetachDocumentRequest{
			ClientId:   activated.Msg.ClientId,
			DocumentId: attached.Msg.DocumentId,
			ChangePack: pbPack,
		})
		withHeaders(req, docKey.String())
		_, err = raw.DetachDocument(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, before, serverSeq())

		require.NoError(t, cli.Sync(ctx))
		assert.NotContains(t, d3.Marshal(), `"more"`)
		require.NoError(t, cli.Detach(ctx, d3))
	})
}

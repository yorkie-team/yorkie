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
	gotime "time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/cluster"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/rpc"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestChangePackKeyMismatch checks that PushPullChanges, DetachDocument and
// RemoveDocument reject a request whose change pack names another document
// than the one its DocumentId targets, and still accept the same request with
// the target's own key. The auth webhook is asked about the pack's key, while
// the request is applied to the DocumentId's document. The cluster service's
// DetachDocument, CompactDocument and PurgeDocument take the same ID/key pair
// and must reject it the same way.
func TestChangePackKeyMismatch(t *testing.T) {
	ctx := context.Background()
	raw := v1connect.NewYorkieServiceClient(http.DefaultClient, "http://"+defaultServer.RPCAddr())
	withKey := func(req connect.AnyRequest, k key.Key) {
		req.Header().Add(types.ShardKey, "/"+k.String())
	}

	// attached is a raw client attached to a target and another document.
	type attached struct {
		clientID string
		actorID  time.ActorID
		targetID string
	}
	setup := func(t *testing.T, target, other key.Key) attached {
		activateReq := connect.NewRequest(&api.ActivateClientRequest{ClientKey: t.Name()})
		withKey(activateReq, target)
		activated, err := raw.ActivateClient(ctx, activateReq)
		require.NoError(t, err)
		actorID, err := time.ActorIDFromHex(activated.Msg.ClientId)
		require.NoError(t, err)

		attach := func(k key.Key) string {
			req := connect.NewRequest(&api.AttachDocumentRequest{
				ClientId:   activated.Msg.ClientId,
				ChangePack: &api.ChangePack{DocumentKey: k.String(), Checkpoint: &api.Checkpoint{}},
			})
			withKey(req, k)
			res, err := raw.AttachDocument(ctx, req)
			require.NoError(t, err)
			return res.Msg.DocumentId
		}
		a := attached{clientID: activated.Msg.ClientId, actorID: actorID, targetID: attach(target)}
		attach(other)
		return a
	}

	// packFor builds a pack that names k and sets {"x":1} on whichever
	// document applies it.
	packFor := func(t *testing.T, a attached, k key.Key) *api.ChangePack {
		ticket := time.NewTicket(1, 1, a.actorID)
		value, err := crdt.NewPrimitive(1, ticket)
		require.NoError(t, err)
		vv := time.NewVersionVector()
		vv.Set(a.actorID, 1)
		pb, err := converter.ToChangePack(change.NewPack(
			k,
			change.NewCheckpoint(0, 1),
			[]*change.Change{change.New(
				change.NewID(1, 0, 1, a.actorID, vv),
				"",
				[]operations.Operation{operations.NewSet(time.InitialTicket, "x", value, ticket)},
				nil,
			)},
			nil,
			nil,
		))
		require.NoError(t, err)
		return pb
	}

	assertMismatch := func(t *testing.T, err error) {
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		assert.ErrorContains(t, err, rpc.ErrDocumentKeyMismatch.Error())
	}

	// contentOf attaches a fresh client to k and returns its content.
	contentOf := func(t *testing.T, k key.Key) string {
		cli := activeClients(t, 1)
		defer deactivateAndCloseClients(t, cli)
		doc := document.New(k)
		require.NoError(t, cli[0].Attach(ctx, doc))
		return doc.Marshal()
	}

	t.Run("PushPullChanges", func(t *testing.T) {
		target, other := helper.TestKey(t, 1), helper.TestKey(t, 2)
		a := setup(t, target, other)
		push := func(k key.Key) error {
			req := connect.NewRequest(&api.PushPullChangesRequest{
				ClientId: a.clientID, DocumentId: a.targetID, ChangePack: packFor(t, a, k),
			})
			withKey(req, target)
			_, err := raw.PushPullChanges(ctx, req)
			return err
		}

		assertMismatch(t, push(other))
		assert.Equal(t, `{}`, contentOf(t, target))

		assert.NoError(t, push(target))
		assert.Equal(t, `{"x":1}`, contentOf(t, target))
	})

	t.Run("DetachDocument", func(t *testing.T) {
		target, other := helper.TestKey(t, 1), helper.TestKey(t, 2)
		a := setup(t, target, other)
		detach := func(k key.Key) error {
			req := connect.NewRequest(&api.DetachDocumentRequest{
				ClientId: a.clientID, DocumentId: a.targetID, ChangePack: packFor(t, a, k),
			})
			withKey(req, target)
			_, err := raw.DetachDocument(ctx, req)
			return err
		}

		assertMismatch(t, detach(other))
		assert.Equal(t, `{}`, contentOf(t, target))

		assert.NoError(t, detach(target))
		assert.Equal(t, `{"x":1}`, contentOf(t, target))
	})

	t.Run("RemoveDocument", func(t *testing.T) {
		target, other := helper.TestKey(t, 1), helper.TestKey(t, 2)
		a := setup(t, target, other)
		remove := func(k key.Key) error {
			pb := packFor(t, a, k)
			pb.IsRemoved = true
			req := connect.NewRequest(&api.RemoveDocumentRequest{
				ClientId: a.clientID, DocumentId: a.targetID, ChangePack: pb,
			})
			withKey(req, target)
			_, err := raw.RemoveDocument(ctx, req)
			return err
		}

		// A removal that went through would leave the client detached from
		// the target, so the second remove would fail.
		assertMismatch(t, remove(other))
		assert.NoError(t, remove(target))
	})

	t.Run("cluster DetachDocument", func(t *testing.T) {
		target, other := helper.TestKey(t, 1), helper.TestKey(t, 2)
		a := setup(t, target, other)
		actorID, err := time.ActorIDFromHex(a.clientID)
		require.NoError(t, err)

		adminCli := helper.CreateAdminCli(t, defaultServer.RPCAddr())
		defer adminCli.Close()
		project, err := adminCli.GetProject(ctx, "default")
		require.NoError(t, err)
		clusterCli, err := cluster.Dial(defaultServer.RPCAddr(), cluster.WithRPCTimeout(10*gotime.Second))
		require.NoError(t, err)
		defer clusterCli.Close()

		err = clusterCli.DetachDocument(ctx, project, actorID, types.ID(a.targetID), other)
		assert.ErrorContains(t, err, rpc.ErrDocumentKeyMismatch.Error())
		assert.NoError(t, clusterCli.DetachDocument(ctx, project, actorID, types.ID(a.targetID), target))
	})

	// clusterSetup stores {"x":1} in a fresh target document, detaches it,
	// optionally removes it, and returns a cluster client and the target's
	// DocInfo.
	clusterSetup := func(t *testing.T, target key.Key, remove bool) (
		*cluster.Client, *types.Project, *database.DocInfo,
	) {
		cli := activeClients(t, 1)
		defer deactivateAndCloseClients(t, cli)
		doc := document.New(target)
		require.NoError(t, cli[0].Attach(ctx, doc))
		require.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetInteger("x", 1)
			return nil
		}))
		require.NoError(t, cli[0].Sync(ctx))

		project, err := defaultServer.DefaultProject(ctx)
		require.NoError(t, err)
		db := defaultServer.Backend().DB
		info, err := db.FindDocInfoByKey(ctx, project.ID, target)
		require.NoError(t, err)

		if remove {
			require.NoError(t, cli[0].Remove(ctx, doc))
		} else {
			require.NoError(t, cli[0].Detach(ctx, doc))
		}
		info, err = db.FindDocInfoByRefKey(ctx, info.RefKey())
		require.NoError(t, err)

		clusterCli, err := cluster.Dial(defaultServer.RPCAddr(), cluster.WithRPCTimeout(10*gotime.Second))
		require.NoError(t, err)
		t.Cleanup(clusterCli.Close)
		return clusterCli, project, info
	}

	// mismatched returns a copy of info that keeps its ID but names k.
	mismatched := func(info *database.DocInfo, k key.Key) *database.DocInfo {
		m := *info
		m.Key = k
		return &m
	}

	t.Run("cluster CompactDocument", func(t *testing.T) {
		target, other := helper.TestKey(t, 1), helper.TestKey(t, 2)
		clusterCli, project, info := clusterSetup(t, target, false)

		_, err := clusterCli.CompactDocument(ctx, project, mismatched(info, other), true)
		assert.ErrorContains(t, err, rpc.ErrDocumentKeyMismatch.Error())

		compacted, err := clusterCli.CompactDocument(ctx, project, info, true)
		assert.NoError(t, err)
		assert.True(t, compacted)
		assert.Equal(t, `{"x":1}`, contentOf(t, target))
	})

	t.Run("cluster PurgeDocument", func(t *testing.T) {
		target, other := helper.TestKey(t, 1), helper.TestKey(t, 2)
		clusterCli, project, info := clusterSetup(t, target, true)
		count := func() int64 {
			n, err := helper.CountChangesWithDocID(helper.TestDBName(), info.ID)
			require.NoError(t, err)
			return n
		}

		err := clusterCli.PurgeDocument(ctx, project, mismatched(info, other))
		assert.ErrorContains(t, err, rpc.ErrDocumentKeyMismatch.Error())
		assert.Positive(t, count())

		assert.NoError(t, clusterCli.PurgeDocument(ctx, project, info))
		assert.Zero(t, count())
	})
}

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

	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/test/helper"
)

// unknownProjectID is a well-formed ObjectID that names no project.
const unknownProjectID = "000000000000000000000000"

// TestClusterServiceProjectTrust checks that the ClusterService handlers whose
// requests still carry a whole Project message read nothing from it but the
// ID, and resolve the stored project themselves. ClusterService shares the
// public RPC port and its interceptor admits every request while
// ClusterSecret is empty — the default — so whatever these handlers take off
// the wire, an unauthenticated caller chooses.
func TestClusterServiceProjectTrust(t *testing.T) {
	ctx := context.Background()
	raw := v1connect.NewClusterServiceClient(http.DefaultClient, "http://"+defaultServer.RPCAddr())

	project, err := defaultServer.DefaultProject(ctx)
	require.NoError(t, err)
	// The forged-body check below turns on the stored flag being off.
	require.False(t, project.RemoveOnDetach)

	withKey := func(req connect.AnyRequest, k key.Key) connect.AnyRequest {
		req.Header().Add(types.ShardKey, project.PublicKey+"/"+k.String())
		return req
	}

	t.Run("GetDocument without a project", func(t *testing.T) {
		docKey := helper.TestKey(t)
		req := connect.NewRequest(&api.ClusterServiceGetDocumentRequest{
			DocumentKey: docKey.String(),
		})
		withKey(req, docKey)

		// A handler panic reaches the caller as CodeInternal "internal error"
		// (connect.WithRecover in server/rpc/server.go). Any other error is
		// the proof that the missing project was handled rather than
		// dereferenced.
		_, err := raw.GetDocument(ctx, req)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "internal error")
	})

	t.Run("GetDocument resolves the project the ID names", func(t *testing.T) {
		docKey := helper.TestKey(t)
		cli := activeClients(t, 1)
		defer deactivateAndCloseClients(t, cli)
		doc := document.New(docKey)
		require.NoError(t, cli[0].Attach(ctx, doc))

		// A body that names a project nobody has fails on the project, not on
		// the document: the handler never reaches the document lookup with an
		// ID it took on trust.
		req := connect.NewRequest(&api.ClusterServiceGetDocumentRequest{
			Project:     &api.Project{Id: unknownProjectID, Name: "forged"},
			DocumentKey: docKey.String(),
		})
		withKey(req, docKey)
		_, err := raw.GetDocument(ctx, req)
		assert.ErrorContains(t, err, database.ErrProjectNotFound.Error())

		// The real ID with a forged body still resolves the stored project.
		req = connect.NewRequest(&api.ClusterServiceGetDocumentRequest{
			Project:     &api.Project{Id: project.ID.String(), Name: "forged", SecretKey: "forged"},
			DocumentKey: docKey.String(),
		})
		withKey(req, docKey)
		res, err := raw.GetDocument(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, docKey.String(), res.Msg.Document.Key)
	})

	t.Run("DetachDocument ignores a forged project body", func(t *testing.T) {
		docKey := helper.TestKey(t)
		cli := activeClients(t, 1)
		defer deactivateAndCloseClients(t, cli)
		doc := document.New(docKey)
		require.NoError(t, cli[0].Attach(ctx, doc))

		db := defaultServer.Backend().DB
		info, err := db.FindDocInfoByKey(ctx, project.ID, docKey)
		require.NoError(t, err)

		// RemoveOnDetach is off on the stored project, and this is the only
		// client attached, so a handler that believed the wire would remove
		// the document here.
		req := connect.NewRequest(&api.ClusterServiceDetachDocumentRequest{
			Project: &api.Project{
				Id:             project.ID.String(),
				Name:           "forged",
				SecretKey:      "forged",
				RemoveOnDetach: true,
			},
			ClientId:    cli[0].ID().String(),
			DocumentId:  info.ID.String(),
			DocumentKey: docKey.String(),
		})
		withKey(req, docKey)
		_, err = raw.DetachDocument(ctx, req)
		require.NoError(t, err)

		info, err = db.FindDocInfoByRefKey(ctx, info.RefKey())
		require.NoError(t, err)
		assert.True(t, info.RemovedAt.IsZero(), "a forged RemoveOnDetach removed the document")
	})
}

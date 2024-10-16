/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
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

package rpc

import (
	"context"

	"connectrpc.com/connect"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/clients"
	"github.com/yorkie-team/yorkie/server/documents"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/packs"
)

type systemServer struct {
	backend *backend.Backend
}

// newSystemServer creates a new instance of systemServer.
func newSystemServer(backend *backend.Backend) *systemServer {
	return &systemServer{
		backend: backend,
	}
}

// DetachDocument detaches the given document from the given client.
func (s *systemServer) DetachDocument(
	ctx context.Context,
	req *connect.Request[api.SystemServiceDetachDocumentRequest],
) (*connect.Response[api.SystemServiceDetachDocumentResponse], error) {
	actorID, err := time.ActorIDFromHex(req.Msg.ClientId)
	if err != nil {
		return nil, err
	}

	documentSummary := converter.FromDocumentSummary(req.Msg.DocumentSummary)
	project := converter.FromProject(req.Msg.Project)

	locker, err := s.backend.Coordinator.NewLocker(ctx, packs.PushPullKey(project.ID, documentSummary.Key))
	if err != nil {
		return nil, err
	}

	if err := locker.Lock(ctx); err != nil {
		return nil, err
	}
	defer func() {
		if err := locker.Unlock(ctx); err != nil {
			logging.DefaultLogger().Error(err)
		}
	}()

	clientInfo, err := clients.FindActiveClientInfo(ctx, s.backend.DB, types.ClientRefKey{
		ProjectID: project.ID,
		ClientID:  types.IDFromActorID(actorID),
	})
	if err != nil {
		return nil, err
	}

	docRefKey := types.DocRefKey{
		ProjectID: project.ID,
		DocID:     documentSummary.ID,
	}

	docInfo, err := documents.FindDocInfoByRefKey(ctx, s.backend, docRefKey)
	if err != nil {
		return nil, err
	}

	doc, err := packs.BuildDocForCheckpoint(ctx, s.backend, docInfo, clientInfo.Checkpoint(documentSummary.ID), actorID)
	if err != nil {
		return nil, err
	}

	if err := doc.Update(func(root *json.Object, p *presence.Presence) error {
		p.Clear()
		return nil
	}); err != nil {
		return nil, err
	}

	if _, err := packs.PushPull(
		ctx,
		s.backend,
		project,
		clientInfo,
		docInfo,
		doc.CreateChangePack(),
		packs.PushPullOptions{
			Mode:   types.SyncModePushPull,
			Status: document.StatusDetached,
		},
	); err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.SystemServiceDetachDocumentResponse{}), nil
}

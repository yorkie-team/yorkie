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
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/innerpresence"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/clients"
	"github.com/yorkie-team/yorkie/server/documents"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/packs"
)

// clusterServer is a server that provides the internal Yorkie cluster service.
// This service is used for communication between nodes in the Yorkie cluster.
type clusterServer struct {
	backend *backend.Backend
}

// newClusterServer creates a new instance of clusterServer.
func newClusterServer(backend *backend.Backend) *clusterServer {
	return &clusterServer{
		backend: backend,
	}
}

// DetachDocument detaches the given document from the given client.
func (s *clusterServer) DetachDocument(
	ctx context.Context,
	req *connect.Request[api.ClusterServiceDetachDocumentRequest],
) (*connect.Response[api.ClusterServiceDetachDocumentResponse], error) {
	actorID, err := time.ActorIDFromHex(req.Msg.ClientId)
	if err != nil {
		return nil, err
	}

	summary := converter.FromDocumentSummary(req.Msg.DocumentSummary)
	project := converter.FromProject(req.Msg.Project)

	locker, err := s.backend.Locker.NewLocker(ctx, packs.PushPullKey(project.ID, summary.Key))
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

	clientInfo, err := clients.FindActiveClientInfo(ctx, s.backend, types.ClientRefKey{
		ProjectID: project.ID,
		ClientID:  types.IDFromActorID(actorID),
	})
	if err != nil {
		return nil, err
	}

	docRefKey := types.DocRefKey{
		ProjectID: project.ID,
		DocID:     summary.ID,
	}

	docInfo, err := documents.FindDocInfoByRefKey(ctx, s.backend, docRefKey)
	if err != nil {
		return nil, err
	}

	// 01. Create changePack with presence clear change
	cp := clientInfo.Checkpoint(summary.ID)
	latestChangeInfo, err := s.backend.DB.FindLatestChangeInfoByActor(
		ctx,
		docRefKey,
		types.ID(req.Msg.ClientId),
		cp.ServerSeq,
	)
	if err != nil {
		return nil, err
	}
	changeCtx := change.NewContext(
		change.NewID(cp.ClientSeq, cp.ServerSeq, latestChangeInfo.Lamport, actorID, latestChangeInfo.VersionVector).Next(),
		"",
		nil,
	)
	p := presence.New(changeCtx, innerpresence.NewPresence())
	p.Clear()

	changes := []*change.Change{changeCtx.ToChange()}
	pack := change.NewPack(docInfo.Key, cp, changes, nil, nil)

	// 02. PushPull with the created ChangePack.
	if _, err := packs.PushPull(
		ctx,
		s.backend,
		project,
		clientInfo,
		docInfo,
		pack,
		packs.PushPullOptions{
			Mode:   types.SyncModePushOnly,
			Status: document.StatusDetached,
		},
	); err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.ClusterServiceDetachDocumentResponse{}), nil
}

// CompactDocument compacts the given document.
func (s *clusterServer) CompactDocument(
	ctx context.Context,
	req *connect.Request[api.ClusterServiceCompactDocumentRequest],
) (*connect.Response[api.ClusterServiceCompactDocumentResponse], error) {
	// 1. Find docInfo
	docId := types.ID(req.Msg.DocumentId)
	projectId := types.ID(req.Msg.ProjectId)
	docInfo, err := documents.FindDocInfoByRefKey(ctx, s.backend, types.DocRefKey{
		ProjectID: projectId,
		DocID:     docId,
	})
	if err != nil {
		return nil, err
	}

	// TODO(chacha912): Should we use SnapshotKey as well?
	locker, err := s.backend.Locker.NewLocker(ctx, packs.PushPullKey(projectId, docInfo.Key))
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

	// 2. Build the final state of the current document
	doc, err := packs.BuildInternalDocForServerSeq(ctx, s.backend, docInfo, docInfo.ServerSeq)
	if err != nil {
		logging.DefaultLogger().Errorf("Document %s failed to apply changes: %v\n", docInfo.ID, err)
		return nil, err
	}

	// 3. Convert doc to jsonStruct
	jsonStruct, err := converter.ToJSONStruct(doc.RootObject())
	if err != nil {
		return nil, err
	}

	// 4. Build new document with jsonStruct and create changepack
	newDoc := document.New(docInfo.Key)
	err = newDoc.Update(func(root *json.Object, p *presence.Presence) error {
		if objStruct, ok := jsonStruct.(*converter.JSONObjectStruct); ok {
			for key, value := range objStruct.Value {
				if err := converter.SetObjFromJsonStruct(root, key, *value); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if newDoc.Marshal() != doc.Marshal() {
		logging.DefaultLogger().Errorf("Document %s content mismatch after rebuild: %v\n", docInfo.ID, err)
		return nil, err
	}
	changes := newDoc.CreateChangePack().Changes

	// 5. Store compacted changes and delete previous data
	err = s.backend.DB.CompactChangeInfos(
		ctx,
		projectId,
		docInfo,
		docInfo.ServerSeq,
		changes,
	)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.ClusterServiceCompactDocumentResponse{}), nil
}

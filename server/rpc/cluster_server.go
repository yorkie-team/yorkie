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
	"fmt"

	"connectrpc.com/connect"
	"go.uber.org/zap"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/innerpresence"
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

	clientInfo, err := clients.FindActiveClientInfo(ctx, s.backend, types.ClientRefKey{
		ProjectID: project.ID,
		ClientID:  types.IDFromActorID(actorID),
	})
	if err != nil {
		return nil, err
	}

	// 01. Create changePack with presence clear change
	refKey := types.DocRefKey{ProjectID: project.ID, DocID: summary.ID}
	cp := clientInfo.Checkpoint(summary.ID)
	latestChangeInfo, err := s.backend.DB.FindLatestChangeInfoByActor(
		ctx,
		refKey,
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
	p := presence.New(changeCtx, innerpresence.New())
	p.Clear()

	changes := []*change.Change{changeCtx.ToChange()}
	pack := change.NewPack(summary.Key, cp, changes, nil, nil)

	// 02. Push the changePack to the document
	locker := s.backend.Lockers.Locker(packs.DocEditKey(project.ID, summary.Key))
	defer func() {
		if err := locker.Unlock(); err != nil {
			logging.DefaultLogger().Error(err)
		}
	}()

	docInfo, err := documents.FindDocInfoByRefKey(ctx, s.backend, refKey)
	if err != nil {
		return nil, err
	}

	if _, err := packs.PushPull(ctx, s.backend, project, clientInfo, docInfo, pack, packs.PushPullOptions{
		Mode:   types.SyncModePushOnly,
		Status: document.StatusDetached,
	}); err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.ClusterServiceDetachDocumentResponse{}), nil
}

// CompactDocument compacts the given document.
func (s *clusterServer) CompactDocument(
	ctx context.Context,
	req *connect.Request[api.ClusterServiceCompactDocumentRequest],
) (*connect.Response[api.ClusterServiceCompactDocumentResponse], error) {
	docId := types.ID(req.Msg.DocumentId)
	projectId := types.ID(req.Msg.ProjectId)
	docInfo, err := documents.FindDocInfoByRefKey(ctx, s.backend, types.DocRefKey{
		ProjectID: projectId,
		DocID:     docId,
	})
	if err != nil {
		return nil, err
	}

	locker := s.backend.Lockers.Locker(packs.DocEditKey(projectId, docInfo.Key))
	defer func() {
		if err := locker.Unlock(); err != nil {
			logging.DefaultLogger().Error(err)
		}
	}()

	if err := packs.Compact(ctx, s.backend, projectId, docInfo); err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.ClusterServiceCompactDocumentResponse{}), nil
}

// PurgeDocument purges the given document.
func (s *clusterServer) PurgeDocument(
	ctx context.Context,
	req *connect.Request[api.ClusterServicePurgeDocumentRequest],
) (*connect.Response[api.ClusterServicePurgeDocumentResponse], error) {
	projectID := types.ID(req.Msg.ProjectId)
	docID := types.ID(req.Msg.DocumentId)

	refKey := types.DocRefKey{ProjectID: projectID, DocID: docID}
	docInfo, err := documents.FindDocInfoByRefKey(ctx, s.backend, refKey)
	if err != nil {
		return nil, err
	}

	if !docInfo.IsRemoved() {
		return nil, fmt.Errorf("purge document %s: %w", docID, documents.ErrDocumentNotRemoved)
	}

	locker := s.backend.Lockers.Locker(packs.DocEditKey(projectID, docInfo.Key))
	defer func() {
		if err := locker.Unlock(); err != nil {
			logging.DefaultLogger().Error(err)
		}
	}()

	counts, err := s.backend.DB.PurgeDocument(ctx, refKey)
	if err != nil {
		logging.From(ctx).Error("failed to purge document", zap.Error(err))
		return nil, err
	}

	logging.From(ctx).Infow(fmt.Sprintf(
		"purged document internals [project_id=%s doc_id=%s]",
		projectID, docID,
	),
		"changes", counts["changes"],
		"snapshots", counts["snapshots"],
		"versionvectors", counts["versionvectors"],
	)

	return connect.NewResponse(&api.ClusterServicePurgeDocumentResponse{}), nil
}

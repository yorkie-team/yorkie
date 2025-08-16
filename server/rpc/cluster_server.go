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
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/clients"
	"github.com/yorkie-team/yorkie/server/documents"
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
	project := converter.FromProject(req.Msg.Project)
	actorID, err := time.ActorIDFromHex(req.Msg.ClientId)
	if err != nil {
		return nil, err
	}
	docID := types.ID(req.Msg.DocumentId)
	docKey := key.Key(req.Msg.DocumentKey)

	locker := s.backend.Lockers.Locker(packs.DocPullKey(actorID, key.Key(req.Msg.DocumentKey)))
	defer locker.Unlock()

	clientInfo, err := clients.FindActiveClientInfo(ctx, s.backend, types.ClientRefKey{
		ProjectID: project.ID,
		ClientID:  types.IDFromActorID(actorID),
	})
	if err != nil {
		return nil, err
	}

	// 01. Create request pack with presence clear change.
	refKey := types.DocRefKey{ProjectID: project.ID, DocID: docID}
	cp := clientInfo.Checkpoint(docID)
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
	pack := change.NewPack(docKey, cp, []*change.Change{changeCtx.ToChange()}, nil, nil)

	// 02. Push the changePack to the document
	docLocker := s.backend.Lockers.LockerWithRLock(packs.DocKey(project.ID, pack.DocumentKey))
	defer docLocker.RUnlock()
	if project.HasAttachmentLimit() {
		locker := s.backend.Lockers.Locker(documents.DocAttachmentKey(refKey))
		defer locker.Unlock()
	}

	if _, err := packs.PushPull(ctx, s.backend, project, clientInfo, refKey, pack, packs.PushPullOptions{
		Mode:   types.SyncModePushOnly,
		Status: document.StatusDetached,
	}); err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.ClusterServiceDetachDocumentResponse{}), nil
}

// CompactDocument unnecessary data from the document and its metadata to reduce
// storage usage.
func (s *clusterServer) CompactDocument(
	ctx context.Context,
	req *connect.Request[api.ClusterServiceCompactDocumentRequest],
) (*connect.Response[api.ClusterServiceCompactDocumentResponse], error) {
	docId := types.ID(req.Msg.DocumentId)
	projectID := types.ID(req.Msg.ProjectId)
	docKey := key.Key(req.Msg.DocumentKey)

	// NOTE(hackerwins): This locker is used to prevent concurrent compaction
	// requests for the same document. And it is also used to prevent
	// concurrent push/pull requests while compaction is in progress.
	locker := s.backend.Lockers.Locker(packs.DocKey(projectID, docKey))
	defer locker.Unlock()

	docInfo, err := documents.FindDocInfoByRefKey(ctx, s.backend, types.DocRefKey{
		ProjectID: projectID,
		DocID:     docId,
	})
	if err != nil {
		return nil, err
	}

	if err := packs.Compact(ctx, s.backend, projectID, docInfo); err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.ClusterServiceCompactDocumentResponse{}), nil
}

// PurgeDocument purges the given document and its metadata from the database.
func (s *clusterServer) PurgeDocument(
	ctx context.Context,
	req *connect.Request[api.ClusterServicePurgeDocumentRequest],
) (*connect.Response[api.ClusterServicePurgeDocumentResponse], error) {
	projectID := types.ID(req.Msg.ProjectId)
	docID := types.ID(req.Msg.DocumentId)
	docKey := key.Key(req.Msg.DocumentKey)

	// NOTE(hackerwins): This locker is used to prevent concurrent compaction
	// requests for the same document. And it is also used to prevent
	// concurrent push/pull requests while compaction is in progress.
	locker := s.backend.Lockers.Locker(packs.DocKey(projectID, docKey))
	defer locker.Unlock()

	docInfo, err := documents.FindDocInfoByRefKey(ctx, s.backend, types.DocRefKey{
		ProjectID: projectID,
		DocID:     docID,
	})
	if err != nil {
		return nil, err
	}

	if err := packs.Purge(ctx, s.backend, projectID, docInfo); err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.ClusterServicePurgeDocumentResponse{}), nil
}

// GetDocument gets the document for a single document.
func (s *clusterServer) GetDocument(
	ctx context.Context,
	req *connect.Request[api.ClusterServiceGetDocumentRequest],
) (*connect.Response[api.ClusterServiceGetDocumentResponse], error) {
	project := converter.FromProject(req.Msg.Project)
	docInfo, err := documents.FindDocInfoByKey(
		ctx,
		s.backend,
		project,
		key.Key(req.Msg.DocumentKey),
	)
	if err != nil {
		return nil, err
	}

	summary := &types.DocumentSummary{
		ID:         docInfo.ID,
		Key:        docInfo.Key,
		CreatedAt:  docInfo.CreatedAt,
		AccessedAt: docInfo.AccessedAt,
		UpdatedAt:  docInfo.UpdatedAt,
		SchemaKey:  docInfo.Schema,
		Root:       "",
		Presences:  nil,
	}

	// If root or presences are requested, we need to build the internal document
	if req.Msg.IncludeRoot || req.Msg.IncludePresences {
		doc, err := packs.BuildInternalDocForServerSeq(ctx, s.backend, docInfo, docInfo.ServerSeq)
		if err != nil {
			return nil, err
		}

		// Set root if requested
		if req.Msg.IncludeRoot {
			summary.Root = doc.Marshal()
		}

		// Set presences if requested
		if req.Msg.IncludePresences {
			summary.Presences = make(map[string]innerpresence.Presence)
			clientIDs := s.backend.PubSub.ClientIDs(docInfo.RefKey())
			presences := doc.AllPresences()

			for _, id := range clientIDs {
				if presence, ok := presences[id.String()]; ok {
					summary.Presences[id.String()] = presence
				}
			}
		}
	}

	return connect.NewResponse(&api.ClusterServiceGetDocumentResponse{
		Document: converter.ToDocumentSummary(summary),
	}), nil
}

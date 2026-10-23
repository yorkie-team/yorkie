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
	"errors"

	"connectrpc.com/connect"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/types/events"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/server/clients"
	"github.com/yorkie-team/yorkie/server/documents"
	"github.com/yorkie-team/yorkie/server/packs"
	"github.com/yorkie-team/yorkie/server/rpc/auth"
)

// clusterServer is a server that provides the internal Yorkie cluster service.
// This service is used for communication between nodes in the Yorkie cluster.
type clusterServer struct {
	backend *backend.Backend
	watches *watchRegistry
}

// newClusterServer creates a new instance of clusterServer.
func newClusterServer(backend *backend.Backend, watches *watchRegistry) *clusterServer {
	return &clusterServer{
		backend: backend,
		watches: watches,
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
	}, true)
	if err != nil {
		return nil, err
	}

	// 01. Create request pack with presence clear changes.
	//
	// Applying a clear runs presences.Delete(change.ID().ActorID()), so it only
	// removes the entry under the actor the client itself stamped into its
	// changes. The server cannot tell which of the client's two identities that
	// is: ActivateClient derives a StableActorID for every client row, but the
	// response carries both it and the per-session ID and the SDKs pick
	// differently — the JS SDK stamps the stable actor, the Go SDK stamps the
	// session ID (client.Activate stores ActivateClientResponse.client_id). So
	// neither ClientInfo field identifies the stamping actor on its own, and
	// stamping the wrong one deletes nothing: the departed client's presence
	// stays in the document and in every snapshot of it (#2290).
	//
	// Clear both, as ClientInfo.IsOwnActor accepts both on the read side. A
	// clear stamped with an identity that holds no presence deletes nothing and
	// reconciles to no event on any replica (InternalDocument.applyChanges), so
	// a redundant one is inert. The session ID is this row's alone, so its clear
	// is always safe; it goes first so its change keeps the clientSeq that a
	// single clear used to take.
	//
	// The stable actor is not this row's alone: it is
	// DeriveActorID(project_id, client_key), which every session of one client
	// key shares by design (docs/design/offline-resumable-attach.md), and so
	// they share the single presence entry keyed under it. Clearing it while a
	// sibling session is still attached to this document would delete presence
	// that session still owns, so that clear is skipped while one remains.
	refKey := types.DocRefKey{ProjectID: project.ID, DocID: docID}
	actorIDs := []time.ActorID{actorID}
	ownActorID, err := clientInfo.OwnActorID()
	if err != nil {
		return nil, err
	}
	if ownActorID != actorID {
		shared, err := s.hasAttachedSibling(ctx, refKey, clientInfo)
		if err != nil {
			return nil, err
		}
		if !shared {
			actorIDs = append(actorIDs, ownActorID)
		}
	}

	// The clears carry no clocks, and there is nothing to inherit from the
	// client's latest change: Context.ToChange rebuilds a presence-only change's
	// ID as prevID.Next(true), which keeps the actor and the clientSeq and drops
	// the lamport and the version vector (change/id.go). Every SDK-emitted
	// presence change is stamped the same way, and a presence change that did
	// carry a vector would put a liveness entry back under an actor that is
	// leaving.
	//
	// Each clear takes the next clientSeq: a context built from clientSeq n
	// emits a change at n+1, and pushPack drops anything at or below the
	// checkpoint.
	cp := clientInfo.Checkpoint(docID)
	changes := make([]*change.Change, 0, len(actorIDs))
	clientSeq := cp.ClientSeq
	for _, id := range actorIDs {
		changeCtx := change.NewContext(
			change.NewID(clientSeq, cp.ServerSeq, time.InitialLamport, id, nil),
			"",
			nil,
		)
		p := presence.New(changeCtx, presence.NewData())
		p.Clear()
		changes = append(changes, changeCtx.ToChange())
		clientSeq++
	}
	pack := change.NewPack(docKey, cp, changes, nil, nil)

	// 02. Push the changePack to the document
	docLocker := s.backend.Lockers.LockerWithRLock(packs.DocKey(project.ID, pack.DocumentKey))
	defer docLocker.RUnlock()
	if project.HasAttachmentLimit() || project.RemoveOnDetach {
		locker := s.backend.Lockers.Locker(documents.DocAttachmentKey(refKey))
		defer locker.Unlock()
	}

	// NOTE(hackerwins): If the project does not have an attachment limit,
	// removing the document by RemoveOnDetach does not guarantee that
	// the document is not attached to the client.
	var status = document.StatusDetached
	if project.RemoveOnDetach {
		isAttached, err := documents.IsDocumentAttachedOrAttaching(ctx, s.backend, refKey, clientInfo.ID)
		if err != nil {
			return nil, err
		}

		if !isAttached {
			pack.IsRemoved = true
			status = document.StatusRemoved
		}
	}

	docInfo, err := documents.FindDocInfoByRefKey(ctx, s.backend, refKey)
	if err != nil {
		return nil, err
	}
	if docInfo.Key != docKey {
		return nil, ErrDocumentKeyMismatch
	}

	if _, err := packs.PushPull(ctx, s.backend, project, clientInfo, refKey, pack, packs.PushPullOptions{
		Mode:            types.SyncModePushOnly,
		Status:          status,
		DisablePresence: docInfo.DisablePresence,
	}); err != nil {
		return nil, err
	}

	// NOTE: No cache invalidation is broadcast here. This handler is only
	// reached from clients.Deactivate, which detaches every attached document
	// of one client and then broadcasts a single invalidation for the whole
	// client row — covering all of them, and on its failure paths too (see
	// the deferred InvalidateCachedClient there). Broadcasting per document as
	// well would multiply one deactivation into (documents+1) cluster-wide
	// fan-outs, and housekeeping runs up to DeactivateConcurrency of those at
	// once. A future caller that detaches a single document through this
	// handler must broadcast the invalidation itself.
	return connect.NewResponse(&api.ClusterServiceDetachDocumentResponse{}), nil
}

// hasAttachedSibling reports whether another session of the same client key is
// still attached to the given document, i.e. whether another activated client
// row holds the same StableActorID. Because that actor is derived from
// (project, client key) alone, such a row keys its presence under the very
// actor a detach of this row would otherwise clear.
//
// Rows written before StableActorID existed leave it empty; an empty value is
// no identity and never matches, mirroring ClientInfo.IsOwnActor.
func (s *clusterServer) hasAttachedSibling(
	ctx context.Context,
	refKey types.DocRefKey,
	clientInfo *database.ClientInfo,
) (bool, error) {
	if clientInfo.StableActorID == "" {
		return false, nil
	}

	infos, err := s.backend.DB.FindAttachedClientInfosByRefKey(ctx, refKey)
	if err != nil {
		return false, err
	}

	for _, info := range infos {
		if info.ID != clientInfo.ID && info.StableActorID == clientInfo.StableActorID {
			return true, nil
		}
	}

	return false, nil
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
	if docInfo.Key != docKey {
		return nil, ErrDocumentKeyMismatch
	}

	if err := packs.Compact(ctx, s.backend, projectID, docInfo, req.Msg.Force); err != nil {
		// If the document is attached, we don't return an error.
		// Because compaction is a best-effort operation.
		if errors.Is(err, packs.ErrDocumentAttached) {
			return connect.NewResponse(&api.ClusterServiceCompactDocumentResponse{
				Compacted: false,
			}), nil
		}

		return nil, err
	}

	return connect.NewResponse(&api.ClusterServiceCompactDocumentResponse{
		Compacted: true,
	}), nil
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
	if docInfo.Key != docKey {
		return nil, ErrDocumentKeyMismatch
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
			summary.DocSize = doc.DocSize()
		}

		// Set presences if requested
		if req.Msg.IncludePresences {
			summary.Presences = make(map[string]presence.Data)
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

// ListChannels lists channels for the given project.
func (s *clusterServer) ListChannels(
	ctx context.Context,
	req *connect.Request[api.ClusterServiceListChannelsRequest],
) (*connect.Response[api.ClusterServiceListChannelsResponse], error) {
	projectID := types.ID(req.Msg.ProjectId)
	limit := int(req.Msg.Limit)
	query := req.Msg.Query

	results := s.backend.Channel.List(projectID, query, limit)

	var channels []*api.ChannelSummary
	for _, result := range results {
		channels = append(channels, &api.ChannelSummary{
			Key:          result.Key.ChannelKey.String(),
			SessionCount: int32(result.Sessions),
		})
	}

	return connect.NewResponse(&api.ClusterServiceListChannelsResponse{
		Channels: channels,
	}), nil
}

// GetChannels gets multiple channels in a single RPC call.
func (s *clusterServer) GetChannels(
	ctx context.Context,
	req *connect.Request[api.ClusterServiceGetChannelsRequest],
) (*connect.Response[api.ClusterServiceGetChannelsResponse], error) {
	projectID := types.ID(req.Msg.ProjectId)
	includeSubPath := req.Msg.IncludeSubPath

	var channels []*api.ChannelSummary
	for _, channelKeyStr := range req.Msg.ChannelKeys {
		channelKey := key.Key(channelKeyStr)
		sessionCount := s.backend.Channel.SessionCount(
			types.ChannelRefKey{
				ProjectID:  projectID,
				ChannelKey: channelKey,
			},
			includeSubPath,
		)
		channels = append(channels, &api.ChannelSummary{
			Key:          channelKeyStr,
			SessionCount: int32(sessionCount),
		})
	}

	return connect.NewResponse(&api.ClusterServiceGetChannelsResponse{
		Channels: channels,
	}), nil
}

// Broadcast broadcasts an event to clients watching the given channel.
func (s *clusterServer) Broadcast(
	ctx context.Context,
	req *connect.Request[api.ClusterServiceBroadcastRequest],
) (*connect.Response[api.ClusterServiceBroadcastResponse], error) {
	channelKey := key.Key(req.Msg.ChannelKey)
	if err := channelKey.Validate(); err != nil {
		return nil, err
	}

	s.backend.PubSub.PublishChannel(ctx, events.ChannelEvent{
		Type: events.ChannelBroadcast,
		Key: types.ChannelRefKey{
			ProjectID:  types.ID(req.Msg.ProjectId),
			ChannelKey: channelKey,
		},
		Publisher: time.InitialActorID,
		Topic:     req.Msg.Topic,
		Payload:   req.Msg.Payload,
	})

	return connect.NewResponse(&api.ClusterServiceBroadcastResponse{}), nil
}

// GetChannelCount gets the channel count for the given project.
func (s *clusterServer) GetChannelCount(
	ctx context.Context,
	req *connect.Request[api.ClusterServiceGetChannelCountRequest],
) (*connect.Response[api.ClusterServiceGetChannelCountResponse], error) {
	projectID := types.ID(req.Msg.ProjectId)
	count := s.backend.Channel.Count(projectID)
	return connect.NewResponse(&api.ClusterServiceGetChannelCountResponse{
		ChannelCount: int32(count),
	}), nil
}

// InvalidateCache invalidates the cache of the given type and key.
func (s *clusterServer) InvalidateCache(
	ctx context.Context,
	req *connect.Request[api.InvalidateCacheRequest],
) (*connect.Response[api.InvalidateCacheResponse], error) {
	cacheType := converter.FromCacheType(req.Msg.CacheType)
	if mongoClient, ok := s.backend.DB.(*mongo.Client); ok {
		mongoClient.InvalidateCache(cacheType, req.Msg.Key)
	}

	return connect.NewResponse(&api.InvalidateCacheResponse{
		Success: true,
	}), nil
}

// RevalidateAccess drops the project's cached auth webhook decisions on this
// node and verifies again the Watch streams it serves for the given keys,
// closing the ones that no longer pass.
//
// Dropping first and snapshotting after covers every stream: one registered
// before the snapshot is verified here, and one registered after it reads no
// cached decision, so its admission asks the webhook itself. A verification
// that was already in flight when the drop ran cannot put its answer back in
// the cache either, since the answer predates the drop (see
// auth.DropCachedDecisions).
func (s *clusterServer) RevalidateAccess(
	ctx context.Context,
	req *connect.Request[api.ClusterServiceRevalidateAccessRequest],
) (*connect.Response[api.ClusterServiceRevalidateAccessResponse], error) {
	projectID := types.ID(req.Msg.ProjectId)
	info, err := s.backend.DB.FindProjectInfoByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	project := info.ToProject()

	auth.DropCachedDecisions(s.backend, project, req.Msg.Keys)
	closed, err := s.watches.revalidate(ctx, projectID, req.Msg.Keys, func(
		ctx context.Context,
		token string,
		access *types.AccessInfo,
	) error {
		return auth.RecheckAccess(ctx, s.backend, project, token, access)
	})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.ClusterServiceRevalidateAccessResponse{
		ClosedStreams: int32(closed),
	}), nil
}

/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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
	gotime "time"

	"connectrpc.com/connect"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/types/events"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/messagebroker"
	"github.com/yorkie-team/yorkie/server/backend/pubsub"
	"github.com/yorkie-team/yorkie/server/clients"
	"github.com/yorkie-team/yorkie/server/documents"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/packs"
	"github.com/yorkie-team/yorkie/server/projects"
	"github.com/yorkie-team/yorkie/server/rpc/auth"
	"github.com/yorkie-team/yorkie/server/schemas"
)

type yorkieServer struct {
	backend    *backend.Backend
	serviceCtx context.Context
}

// newYorkieServer creates a new instance of yorkieServer
func newYorkieServer(serviceCtx context.Context, be *backend.Backend) *yorkieServer {
	return &yorkieServer{
		backend:    be,
		serviceCtx: serviceCtx,
	}
}

// ActivateClient activates the given client.
func (s *yorkieServer) ActivateClient(
	ctx context.Context,
	req *connect.Request[api.ActivateClientRequest],
) (*connect.Response[api.ActivateClientResponse], error) {
	if req.Msg.ClientKey == "" {
		return nil, clients.ErrInvalidClientKey
	}

	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method: types.ActivateClient,
	}); err != nil {
		return nil, err
	}

	project := projects.From(ctx)
	cli, err := clients.Activate(ctx, s.backend, project, req.Msg.ClientKey, req.Msg.Metadata)
	if err != nil {
		return nil, err
	}

	if userID, exist := req.Msg.Metadata["userID"]; exist && userID != "" {
		if err := s.backend.MsgBroker.Produce(
			ctx,
			messagebroker.UserEventMessage{
				UserID:    userID,
				Timestamp: gotime.Now(),
				EventType: events.ClientActivatedEvent,
				ProjectID: project.ID.String(),
				UserAgent: req.Header().Get("x-yorkie-user-agent"),
			},
		); err != nil {
			logging.From(ctx).Error(err)
		}
	}

	return connect.NewResponse(&api.ActivateClientResponse{
		ClientId: cli.ID.String(),
	}), nil
}

// DeactivateClient deactivates the given client.
func (s *yorkieServer) DeactivateClient(
	ctx context.Context,
	req *connect.Request[api.DeactivateClientRequest],
) (*connect.Response[api.DeactivateClientResponse], error) {
	actorID, err := time.ActorIDFromHex(req.Msg.ClientId)
	if err != nil {
		return nil, err
	}

	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method: types.DeactivateClient,
	}); err != nil {
		return nil, err
	}

	project := projects.From(ctx)
	_, err = clients.Deactivate(ctx, s.backend, project, types.ClientRefKey{
		ProjectID: project.ID,
		ClientID:  types.IDFromActorID(actorID),
	})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.DeactivateClientResponse{}), nil
}

// AttachDocument attaches the given document to the client.
func (s *yorkieServer) AttachDocument(
	ctx context.Context,
	req *connect.Request[api.AttachDocumentRequest],
) (*connect.Response[api.AttachDocumentResponse], error) {
	// 01. Validate the request and verify access
	actorID, err := time.ActorIDFromHex(req.Msg.ClientId)
	if err != nil {
		return nil, err
	}

	pack, err := converter.FromChangePack(req.Msg.ChangePack)
	if err != nil {
		return nil, err
	}
	if err := pack.DocumentKey.Validate(); err != nil {
		return nil, err
	}

	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method:     types.AttachDocument,
		Attributes: auth.AccessAttributes(pack),
	}); err != nil {
		return nil, err
	}

	project := projects.From(ctx)

	docLocker := s.backend.Lockers.LockerWithRLock(packs.DocKey(project.ID, pack.DocumentKey))
	defer docLocker.RUnlock()
	locker := s.backend.Lockers.Locker(packs.DocPullKey(actorID, pack.DocumentKey))
	defer locker.Unlock()

	clientInfo, err := clients.FindActiveClientInfo(ctx, s.backend, types.ClientRefKey{
		ProjectID: project.ID,
		ClientID:  types.IDFromActorID(actorID),
	})
	if err != nil {
		return nil, err
	}

	// 02. Ensure the document exists and is attached to the client.
	docInfo, err := documents.FindOrCreateDocInfo(ctx, s.backend, clientInfo, pack.DocumentKey)
	if err != nil {
		return nil, err
	}

	clientInfo, err = clients.AttachDocument(ctx, s.backend, clientInfo, docInfo, pack.IsAttached())
	if err != nil {
		return nil, err
	}

	docKey := types.DocRefKey{ProjectID: project.ID, DocID: docInfo.ID}
	schemaName, schemaVersion, err := converter.FromSchemaKey(docInfo.Schema)
	if err != nil {
		return nil, err
	}
	schema, err := schemas.GetSchema(ctx, s.backend, project.ID, schemaName, schemaVersion)
	if err != nil {
		return nil, err
	}
	if project.HasAttachmentLimit() || (docInfo.Schema == "" && req.Msg.SchemaKey != "") {
		locker := s.backend.Lockers.Locker(documents.DocAttachmentKey(docKey))
		defer locker.Unlock()

		count, err := documents.FindAttachedClientCount(ctx, s.backend, docKey)
		if err != nil {
			return nil, err
		}

		if err := project.IsAttachmentLimitExceeded(count); err != nil {
			return nil, err
		}

		if count == 0 {
			schemaName, schemaVersion, err = converter.FromSchemaKey(req.Msg.SchemaKey)
			if err != nil {
				return nil, err
			}
			schema, err = schemas.GetSchema(ctx, s.backend, project.ID, schemaName, schemaVersion)
			if err != nil {
				return nil, err
			}
			if err := documents.UpdateDocInfoSchema(ctx, s.backend, docInfo.RefKey(), req.Msg.SchemaKey); err != nil {
				return nil, err
			}
		}
	}

	// 03. Push/Pull between the client and server.
	pulled, err := packs.PushPull(ctx, s.backend, project, clientInfo, docKey, pack, packs.PushPullOptions{
		Mode:   types.SyncModePushPull,
		Status: document.StatusAttached,
	})
	if err != nil {
		return nil, err
	}

	pbChangePack, err := pulled.ToPBChangePack()
	if err != nil {
		return nil, err
	}

	response := &api.AttachDocumentResponse{
		ChangePack:         pbChangePack,
		DocumentId:         docInfo.ID.String(),
		MaxSizePerDocument: int32(project.MaxSizePerDocument),
	}
	if schema != nil {
		response.SchemaRules = converter.ToRules(schema.Rules)
	}
	return connect.NewResponse(response), nil
}

// DetachDocument detaches the given document to the client.
func (s *yorkieServer) DetachDocument(
	ctx context.Context,
	req *connect.Request[api.DetachDocumentRequest],
) (*connect.Response[api.DetachDocumentResponse], error) {
	// 01. Validate the request and verify access
	actorID, err := time.ActorIDFromHex(req.Msg.ClientId)
	if err != nil {
		return nil, err
	}

	pack, err := converter.FromChangePack(req.Msg.ChangePack)
	if err != nil {
		return nil, err
	}
	docID, err := converter.FromDocumentID(req.Msg.DocumentId)
	if err != nil {
		return nil, err
	}

	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method:     types.DetachDocument,
		Attributes: auth.AccessAttributes(pack),
	}); err != nil {
		return nil, err
	}

	project := projects.From(ctx)

	docLocker := s.backend.Lockers.LockerWithRLock(packs.DocKey(project.ID, pack.DocumentKey))
	defer docLocker.RUnlock()
	locker := s.backend.Lockers.Locker(packs.DocPullKey(actorID, pack.DocumentKey))
	defer locker.Unlock()

	clientInfo, err := clients.FindActiveClientInfo(ctx, s.backend, types.ClientRefKey{
		ProjectID: project.ID,
		ClientID:  types.IDFromActorID(actorID),
	})
	if err != nil {
		return nil, err
	}

	// 02. Set the document status if it is not attached.
	docKey := types.DocRefKey{ProjectID: project.ID, DocID: docID}
	if project.HasAttachmentLimit() {
		locker := s.backend.Lockers.Locker(documents.DocAttachmentKey(docKey))
		defer locker.Unlock()
	}

	// NOTE(hackerwins): If the project does not have an attachment limit,
	// removing the document by removeIfNotAttached does not guarantee that
	// the document is not attached to the client.
	var status document.StatusType
	if req.Msg.RemoveIfNotAttached {
		isAttached, err := documents.IsDocumentAttached(ctx, s.backend, docKey, clientInfo.ID)
		if err != nil {
			return nil, err
		}

		if !isAttached {
			pack.IsRemoved = true
			status = document.StatusRemoved
		}
	} else {
		status = document.StatusDetached
	}

	// 03. Push/Pull between the client and server.
	pulled, err := packs.PushPull(ctx, s.backend, project, clientInfo, docKey, pack, packs.PushPullOptions{
		Mode:   types.SyncModePushPull,
		Status: status,
	})
	if err != nil {
		return nil, err
	}

	pbChangePack, err := pulled.ToPBChangePack()
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.DetachDocumentResponse{
		ChangePack: pbChangePack,
	}), nil
}

// PushPullChanges stores the changes sent by the client and delivers the changes
// accumulated in the server to the client.
func (s *yorkieServer) PushPullChanges(
	ctx context.Context,
	req *connect.Request[api.PushPullChangesRequest],
) (*connect.Response[api.PushPullChangesResponse], error) {
	// 01. Validate the request and verify access
	actorID, err := time.ActorIDFromHex(req.Msg.ClientId)
	if err != nil {
		return nil, err
	}

	pack, err := converter.FromChangePack(req.Msg.ChangePack)
	if err != nil {
		return nil, err
	}
	docID, err := converter.FromDocumentID(req.Msg.DocumentId)
	if err != nil {
		return nil, err
	}
	syncMode := types.SyncModePushPull
	if req.Msg.PushOnly {
		syncMode = types.SyncModePushOnly
	}

	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method:     types.PushPull,
		Attributes: auth.AccessAttributes(pack),
	}); err != nil {
		return nil, err
	}

	project := projects.From(ctx)

	docLocker := s.backend.Lockers.LockerWithRLock(packs.DocKey(project.ID, pack.DocumentKey))
	defer docLocker.RUnlock()
	locker := s.backend.Lockers.Locker(packs.DocPullKey(actorID, pack.DocumentKey))
	defer locker.Unlock()
	clientInfo, err := clients.FindActiveClientInfo(ctx, s.backend, types.ClientRefKey{
		ProjectID: project.ID,
		ClientID:  types.IDFromActorID(actorID),
	})
	if err != nil {
		return nil, err
	}

	// 02. Ensure the document attached to the client.
	if err := clientInfo.EnsureDocumentAttached(docID); err != nil {
		return nil, err
	}

	// 03. Push/Pull between the client and server.
	docKey := types.DocRefKey{ProjectID: project.ID, DocID: docID}
	pulled, err := packs.PushPull(ctx, s.backend, project, clientInfo, docKey, pack, packs.PushPullOptions{
		Mode:   syncMode,
		Status: document.StatusAttached,
	})
	if err != nil {
		return nil, err
	}

	pbChangePack, err := pulled.ToPBChangePack()
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.PushPullChangesResponse{
		ChangePack: pbChangePack,
	}), nil
}

// RemoveDocument removes the given document.
func (s *yorkieServer) RemoveDocument(
	ctx context.Context,
	req *connect.Request[api.RemoveDocumentRequest],
) (*connect.Response[api.RemoveDocumentResponse], error) {
	// 01. Validate the request and verify access
	actorID, err := time.ActorIDFromHex(req.Msg.ClientId)
	if err != nil {
		return nil, err
	}

	pack, err := converter.FromChangePack(req.Msg.ChangePack)
	if err != nil {
		return nil, err
	}
	docID, err := converter.FromDocumentID(req.Msg.DocumentId)
	if err != nil {
		return nil, err
	}

	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method:     types.RemoveDocument,
		Attributes: auth.AccessAttributes(pack),
	}); err != nil {
		return nil, err
	}

	project := projects.From(ctx)

	docLocker := s.backend.Lockers.LockerWithRLock(packs.DocKey(project.ID, pack.DocumentKey))
	defer docLocker.RUnlock()
	locker := s.backend.Lockers.Locker(packs.DocPullKey(actorID, pack.DocumentKey))
	defer locker.Unlock()
	clientInfo, err := clients.FindActiveClientInfo(ctx, s.backend, types.ClientRefKey{
		ProjectID: project.ID,
		ClientID:  types.IDFromActorID(actorID),
	})
	if err != nil {
		return nil, err
	}

	docKey := types.DocRefKey{ProjectID: project.ID, DocID: docID}
	if project.HasAttachmentLimit() {
		locker := s.backend.Lockers.Locker(documents.DocAttachmentKey(docKey))
		defer locker.Unlock()
	}

	// 02. Push/Pull between the client and server.
	pulled, err := packs.PushPull(ctx, s.backend, project, clientInfo, docKey, pack, packs.PushPullOptions{
		Mode:   types.SyncModePushPull,
		Status: document.StatusRemoved,
	})
	if err != nil {
		return nil, err
	}

	pbChangePack, err := pulled.ToPBChangePack()
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.RemoveDocumentResponse{
		ChangePack: pbChangePack,
	}), nil
}

// WatchDocument connects the stream to deliver events from the given documents
// to the requesting client.
func (s *yorkieServer) WatchDocument(
	ctx context.Context,
	req *connect.Request[api.WatchDocumentRequest],
	stream *connect.ServerStream[api.WatchDocumentResponse],
) error {
	clientID, err := time.ActorIDFromHex(req.Msg.ClientId)
	if err != nil {
		return err
	}

	project := projects.From(ctx)
	docID, err := converter.FromDocumentID(req.Msg.DocumentId)
	if err != nil {
		return err
	}

	if _, err = clients.FindActiveClientInfo(ctx, s.backend, types.ClientRefKey{
		ProjectID: project.ID,
		ClientID:  types.IDFromActorID(clientID),
	}); err != nil {
		return err
	}

	docKey := types.DocRefKey{ProjectID: project.ID, DocID: docID}
	docInfo, err := documents.FindDocInfoByRefKey(ctx, s.backend, docKey)
	if err != nil {
		return nil
	}

	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method:     types.WatchDocuments,
		Attributes: types.NewAccessAttributes([]key.Key{docInfo.Key}, types.Read),
	}); err != nil {
		return err
	}

	locker := s.backend.Lockers.Locker(documents.DocWatchStreamKey(clientID, docInfo.Key))
	defer locker.Unlock()
	subscription, clientIDs, err := s.watchDoc(ctx, clientID, docKey, project.MaxSubscribersPerDocument)
	if err != nil {
		return err
	}

	s.backend.Metrics.AddWatchDocumentConnections(s.backend.Config.Hostname, project)
	defer func() {
		if err := s.unwatchDoc(ctx, subscription, docKey); err != nil {
			logging.From(ctx).Error(err)
		} else {
			s.backend.Metrics.RemoveWatchDocumentConnections(s.backend.Config.Hostname, project)
		}
	}()

	var pbClientIDs []string
	for _, id := range clientIDs {
		pbClientIDs = append(pbClientIDs, id.String())
	}
	if err := stream.Send(&api.WatchDocumentResponse{
		Body: &api.WatchDocumentResponse_Initialization_{
			Initialization: &api.WatchDocumentResponse_Initialization{
				ClientIds: pbClientIDs,
			},
		},
	}); err != nil {
		return err
	}

	for {
		select {
		case <-s.serviceCtx.Done():
			return context.Canceled
		case <-ctx.Done():
			return context.Canceled
		case event := <-subscription.Events():
			eventType, err := converter.ToDocEventType(event.Type)
			if err != nil {
				return err
			}

			response := &api.WatchDocumentResponse{
				Body: &api.WatchDocumentResponse_Event{
					Event: &api.DocEvent{
						Type:      eventType,
						Publisher: event.Publisher.String(),
						Body: &api.DocEventBody{
							Topic:   event.Body.Topic,
							Payload: event.Body.Payload,
						},
					},
				},
			}
			if err := stream.Send(response); err != nil {
				return err
			}
			s.backend.Metrics.AddWatchDocumentEventPayloadBytes(
				s.backend.Config.Hostname,
				project,
				event.Type,
				event.Body.PayloadLen(),
			)
		}
	}
}

func (s *yorkieServer) watchDoc(
	ctx context.Context,
	clientID time.ActorID,
	docKey types.DocRefKey,
	limit int,
) (*pubsub.Subscription, []time.ActorID, error) {
	sub, clientIDs, err := s.backend.PubSub.Subscribe(ctx, clientID, docKey, limit)
	if err != nil {
		return nil, nil, err
	}

	s.backend.PubSub.Publish(ctx, sub.Subscriber(), events.DocEvent{
		Type:      events.DocWatched,
		Publisher: sub.Subscriber(),
		DocRefKey: docKey,
	})
	s.backend.Metrics.AddWatchDocumentEventPayloadBytes(
		s.backend.Config.Hostname,
		projects.From(ctx),
		events.DocWatched,
		0,
	)

	return sub, clientIDs, nil
}

func (s *yorkieServer) unwatchDoc(
	ctx context.Context,
	sub *pubsub.Subscription,
	docKey types.DocRefKey,
) error {
	s.backend.PubSub.Unsubscribe(ctx, docKey, sub)
	s.backend.PubSub.Publish(ctx, sub.Subscriber(), events.DocEvent{
		Type:      events.DocUnwatched,
		Publisher: sub.Subscriber(),
		DocRefKey: docKey,
	})
	s.backend.Metrics.AddWatchDocumentEventPayloadBytes(
		s.backend.Config.Hostname,
		projects.From(ctx),
		events.DocUnwatched,
		0,
	)

	return nil
}

// Broadcast sends the given payload to all clients watching the document.
func (s *yorkieServer) Broadcast(
	ctx context.Context,
	req *connect.Request[api.BroadcastRequest],
) (*connect.Response[api.BroadcastResponse], error) {
	clientID, err := time.ActorIDFromHex(req.Msg.ClientId)
	if err != nil {
		return nil, err
	}

	project := projects.From(ctx)
	docID, err := converter.FromDocumentID(req.Msg.DocumentId)
	if err != nil {
		return nil, err
	}

	docKey := types.DocRefKey{ProjectID: project.ID, DocID: docID}
	docInfo, err := documents.FindDocInfoByRefKey(ctx, s.backend, docKey)
	if err != nil {
		return nil, err
	}

	// TODO(sejongk): It seems better to use a separate auth attributes for broadcast later
	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method:     types.Broadcast,
		Attributes: types.NewAccessAttributes([]key.Key{docInfo.Key}, types.Read),
	}); err != nil {
		return nil, err
	}

	if _, err = clients.FindActiveClientInfo(ctx, s.backend, types.ClientRefKey{
		ProjectID: project.ID,
		ClientID:  types.IDFromActorID(clientID),
	}); err != nil {
		return nil, err
	}

	s.backend.PubSub.Publish(ctx, clientID, events.DocEvent{
		Type:      events.DocBroadcast,
		Publisher: clientID,
		DocRefKey: docKey,
		Body: events.DocEventBody{
			Topic:   req.Msg.Topic,
			Payload: req.Msg.Payload,
		},
	})
	s.backend.Metrics.AddWatchDocumentEventPayloadBytes(
		s.backend.Config.Hostname,
		project,
		events.DocBroadcast,
		len(req.Msg.Payload),
	)

	return connect.NewResponse(&api.BroadcastResponse{}), nil
}

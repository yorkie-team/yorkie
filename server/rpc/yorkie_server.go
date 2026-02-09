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
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/messaging"
	"github.com/yorkie-team/yorkie/server/backend/pubsub"
	"github.com/yorkie-team/yorkie/server/clients"
	"github.com/yorkie-team/yorkie/server/documents"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/packs"
	"github.com/yorkie-team/yorkie/server/projects"
	"github.com/yorkie-team/yorkie/server/revisions"
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

	if err := s.backend.MsgBroker.Produce(
		ctx,
		messaging.ClientEventMessage{
			ProjectID: project.ID.String(),
			ClientID:  cli.ID.String(),
			Timestamp: gotime.Now(),
			EventType: events.ClientActivatedEvent,
		},
	); err != nil {
		logging.From(ctx).Errorf("failed to produce client event: %v", err)
	}

	if userID, exist := req.Msg.Metadata["userID"]; exist && userID != "" {
		if err := s.backend.MsgBroker.Produce(
			ctx,
			messaging.UserEventMessage{
				UserID:    userID,
				Timestamp: gotime.Now(),
				EventType: events.ClientActivatedEvent,
				ProjectID: project.ID.String(),
				UserAgent: req.Header().Get("x-yorkie-user-agent"),
			},
		); err != nil {
			logging.From(ctx).Errorf("failed to produce user event: %v", err)
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

	if req.Msg.Synchronous {
		if _, err := clients.Deactivate(ctx, s.backend, project, types.ClientRefKey{
			ProjectID: project.ID,
			ClientID:  types.IDFromActorID(actorID),
		}); err != nil {
			return nil, err
		}

		if err := s.backend.MsgBroker.Produce(
			ctx,
			messaging.ClientEventMessage{
				ProjectID: project.ID.String(),
				ClientID:  actorID.String(),
				Timestamp: gotime.Now(),
				EventType: events.ClientDeactivatedEvent,
			},
		); err != nil {
			logging.From(ctx).Errorf("failed to produce client deactivation event: %v", err)
		}

		return connect.NewResponse(&api.DeactivateClientResponse{}), nil
	}

	// Use DeactivateAsync to handle cases where browser window is closed
	// and the context might be cancelled before deactivation completes
	if err = clients.DeactivateAsync(s.backend, project, types.ClientRefKey{
		ProjectID: project.ID,
		ClientID:  types.IDFromActorID(actorID),
	}); err != nil {
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
	if project.HasAttachmentLimit() || project.RemoveOnDetach || (docInfo.Schema == "" && req.Msg.SchemaKey != "") {
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

	if err := s.backend.MsgBroker.Produce(
		ctx,
		messaging.DocumentEventMessage{
			ProjectID:   project.ID.String(),
			DocumentKey: docInfo.Key.String(),
			ActorID:     actorID.String(),
			Timestamp:   gotime.Now(),
			EventType:   events.DocAttached,
		},
	); err != nil {
		logging.From(ctx).Errorf("failed to produce document event: %v", err)
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

// AttachChannel attaches the given channel to the client.
func (s *yorkieServer) AttachChannel(
	ctx context.Context,
	req *connect.Request[api.AttachChannelRequest],
) (*connect.Response[api.AttachChannelResponse], error) {
	// 01. Validate the request and verify access
	actorID, err := time.ActorIDFromHex(req.Msg.ClientId)
	if err != nil {
		return nil, err
	}
	channelKey := key.Key(req.Msg.ChannelKey)
	if err := channelKey.Validate(); err != nil {
		return nil, err
	}
	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method:     types.AttachChannel,
		Attributes: types.NewAccessAttributes([]key.Key{channelKey}, types.ReadWrite),
	}); err != nil {
		return nil, err
	}

	project := projects.From(ctx)
	_, err = clients.FindActiveClientInfo(ctx, s.backend, types.ClientRefKey{
		ProjectID: project.ID,
		ClientID:  types.IDFromActorID(actorID),
	})
	if err != nil {
		return nil, err
	}

	// 02. Create ChannelRefKey and attach to channel
	refKey := types.ChannelRefKey{
		ProjectID:  project.ID,
		ChannelKey: channelKey,
	}

	sessionID, sessionCount, err := s.backend.Channel.Attach(ctx, refKey, actorID)
	if err != nil {
		return nil, err
	}

	response := &api.AttachChannelResponse{
		SessionId:    sessionID.String(),
		SessionCount: sessionCount,
	}
	return connect.NewResponse(response), nil
}

// DetachChannel detaches the given channel from the client.
func (s *yorkieServer) DetachChannel(
	ctx context.Context,
	req *connect.Request[api.DetachChannelRequest],
) (*connect.Response[api.DetachChannelResponse], error) {
	// 01. Validate the request and verify access
	actorID, err := time.ActorIDFromHex(req.Msg.ClientId)
	if err != nil {
		return nil, err
	}
	channelKey := key.Key(req.Msg.ChannelKey)
	if err := channelKey.Validate(); err != nil {
		return nil, err
	}
	sessionID := types.ID(req.Msg.SessionId)
	if err := sessionID.Validate(); err != nil {
		return nil, err
	}
	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method:     types.DetachChannel,
		Attributes: types.NewAccessAttributes([]key.Key{channelKey}, types.ReadWrite),
	}); err != nil {
		return nil, err
	}

	project := projects.From(ctx)
	_, err = clients.FindActiveClientInfo(ctx, s.backend, types.ClientRefKey{
		ProjectID: project.ID,
		ClientID:  types.IDFromActorID(actorID),
	})
	if err != nil {
		return nil, err
	}

	// 02. Detach using presence ID
	sessionCount, err := s.backend.Channel.Detach(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	response := &api.DetachChannelResponse{
		SessionCount: sessionCount,
	}
	return connect.NewResponse(response), nil
}

// RefreshChannel refreshes the TTL of the given channel.
func (s *yorkieServer) RefreshChannel(
	ctx context.Context,
	req *connect.Request[api.RefreshChannelRequest],
) (*connect.Response[api.RefreshChannelResponse], error) {
	// 01. Validate the request and verify access
	actorID, err := time.ActorIDFromHex(req.Msg.ClientId)
	if err != nil {
		return nil, err
	}
	channelKey := key.Key(req.Msg.ChannelKey)
	if err := channelKey.Validate(); err != nil {
		return nil, err
	}
	sessionID := types.ID(req.Msg.SessionId)
	if err := sessionID.Validate(); err != nil {
		return nil, err
	}
	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method:     types.RefreshChannel,
		Attributes: types.NewAccessAttributes([]key.Key{channelKey}, types.ReadWrite),
	}); err != nil {
		return nil, err
	}

	project := projects.From(ctx)
	_, err = clients.FindActiveClientInfo(ctx, s.backend, types.ClientRefKey{
		ProjectID: project.ID,
		ClientID:  types.IDFromActorID(actorID),
	})
	if err != nil {
		return nil, err
	}

	// 02. Refresh presence using presence ID
	if err := s.backend.Channel.Refresh(ctx, sessionID); err != nil {
		return nil, err
	}

	// 03. Get current count from backend
	refKey := types.ChannelRefKey{
		ProjectID:  project.ID,
		ChannelKey: channelKey,
	}
	sessionCount := s.backend.Channel.SessionCount(refKey, false)

	response := &api.RefreshChannelResponse{
		SessionCount: sessionCount,
	}
	return connect.NewResponse(response), nil
}

// taggedEvent wraps a doc or channel event for fan-in multiplexing.
type taggedEvent struct {
	docEvent     *events.DocEvent
	channelEvent *events.ChannelEvent
	docID        string
	channelKey   string
}

// docSub holds a document subscription with its identifiers.
type docSub struct {
	docID  types.ID
	docKey types.DocRefKey
	sub    *pubsub.DocSubscription
}

// channelSub holds a channel subscription with its identifiers.
type channelSub struct {
	channelKey key.Key
	refKey     types.ChannelRefKey
	sub        *pubsub.ChannelSubscription
}

// Watch connects a unified stream to deliver events from documents and channels
// to the requesting client.
func (s *yorkieServer) Watch(
	ctx context.Context,
	req *connect.Request[api.WatchRequest],
	stream *connect.ServerStream[api.WatchResponse],
) error {
	clientID, err := time.ActorIDFromHex(req.Msg.ClientId)
	if err != nil {
		return err
	}

	project := projects.From(ctx)
	if _, err = clients.FindActiveClientInfo(ctx, s.backend, types.ClientRefKey{
		ProjectID: project.ID,
		ClientID:  types.IDFromActorID(clientID),
	}); err != nil {
		return err
	}

	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method: types.Watch,
	}); err != nil {
		return err
	}

	docSubs, channelSubs, resourceInits, err := s.subscribeResources(ctx, req.Msg, clientID, project)
	if err != nil {
		return err
	}

	s.backend.Metrics.AddWatchDocumentConnections(s.backend.Config.Hostname, project)
	defer func() {
		for _, ds := range docSubs {
			if err := s.unwatchDoc(ctx, ds.sub, ds.docKey); err != nil {
				logging.From(ctx).Error(err)
			}
		}
		for _, cs := range channelSubs {
			s.backend.PubSub.UnsubscribeChannel(ctx, cs.refKey, cs.sub)
		}
		s.backend.Metrics.RemoveWatchDocumentConnections(s.backend.Config.Hostname, project)
	}()

	if err := stream.Send(&api.WatchResponse{
		Body: &api.WatchResponse_Initialization{
			Initialization: &api.WatchInitialization{
				ResourceInits: resourceInits,
			},
		},
	}); err != nil {
		return err
	}

	return s.streamMergedEvents(ctx, stream, project, docSubs, channelSubs)
}

// subscribeResources subscribes to each document and channel resource in the request.
func (s *yorkieServer) subscribeResources(
	ctx context.Context,
	req *api.WatchRequest,
	clientID time.ActorID,
	project *types.Project,
) ([]docSub, []channelSub, []*api.ResourceInit, error) {
	var docSubs []docSub
	var channelSubs []channelSub
	var resourceInits []*api.ResourceInit

	for _, res := range req.Resources {
		switch desc := res.Resource.(type) {
		case *api.ResourceDescriptor_Document:
			ds, ri, err := s.subscribeDocument(ctx, desc, clientID, project)
			if err != nil {
				return nil, nil, nil, err
			}
			docSubs = append(docSubs, *ds)
			resourceInits = append(resourceInits, ri)

		case *api.ResourceDescriptor_Channel:
			cs, ri, err := s.subscribeChannel(ctx, desc, clientID, project)
			if err != nil {
				return nil, nil, nil, err
			}
			channelSubs = append(channelSubs, *cs)
			resourceInits = append(resourceInits, ri)
		}
	}

	return docSubs, channelSubs, resourceInits, nil
}

// subscribeDocument subscribes to a single document and returns its subscription and init data.
func (s *yorkieServer) subscribeDocument(
	ctx context.Context,
	desc *api.ResourceDescriptor_Document,
	clientID time.ActorID,
	project *types.Project,
) (*docSub, *api.ResourceInit, error) {
	docID, err := converter.FromDocumentID(desc.Document.DocumentId)
	if err != nil {
		return nil, nil, err
	}
	dk := types.DocRefKey{ProjectID: project.ID, DocID: docID}
	docInfo, err := documents.FindDocInfoByRefKey(ctx, s.backend, dk)
	if err != nil {
		return nil, nil, err
	}

	locker := s.backend.Lockers.Locker(documents.DocWatchStreamKey(clientID, docInfo.Key))
	defer locker.Unlock()

	sub, clientIDs, err := s.watchDoc(ctx, clientID, dk, project.MaxSubscribersPerDocument)
	if err != nil {
		return nil, nil, err
	}

	var pbClientIDs []string
	for _, id := range clientIDs {
		pbClientIDs = append(pbClientIDs, id.String())
	}

	return &docSub{docID: docID, docKey: dk, sub: sub}, &api.ResourceInit{
		Init: &api.ResourceInit_DocumentInit{
			DocumentInit: &api.DocumentInit{
				DocumentId: docID.String(),
				ClientIds:  pbClientIDs,
			},
		},
	}, nil
}

// subscribeChannel subscribes to a single channel and returns its subscription and init data.
func (s *yorkieServer) subscribeChannel(
	ctx context.Context,
	desc *api.ResourceDescriptor_Channel,
	clientID time.ActorID,
	project *types.Project,
) (*channelSub, *api.ResourceInit, error) {
	channelKey := key.Key(desc.Channel.ChannelKey)
	if err := channelKey.Validate(); err != nil {
		return nil, nil, err
	}
	refKey := types.ChannelRefKey{
		ProjectID:  project.ID,
		ChannelKey: channelKey,
	}
	sub, _, err := s.backend.PubSub.SubscribeChannel(ctx, clientID, refKey)
	if err != nil {
		return nil, nil, err
	}

	sessionCount := s.backend.Channel.SessionCount(refKey, false)
	return &channelSub{channelKey: channelKey, refKey: refKey, sub: sub}, &api.ResourceInit{
		Init: &api.ResourceInit_ChannelInit{
			ChannelInit: &api.ChannelInit{
				ChannelKey:   string(channelKey),
				SessionCount: sessionCount,
				Seq:          0,
			},
		},
	}, nil
}

// streamMergedEvents fans in events from all subscriptions and streams them.
func (s *yorkieServer) streamMergedEvents(
	ctx context.Context,
	stream *connect.ServerStream[api.WatchResponse],
	project *types.Project,
	docSubs []docSub,
	channelSubs []channelSub,
) error {
	merged := make(chan taggedEvent, len(docSubs)+len(channelSubs))
	done := make(chan struct{})
	defer close(done)

	for _, ds := range docSubs {
		go func(ds docSub) {
			for {
				select {
				case <-done:
					return
				case event, ok := <-ds.sub.Events():
					if !ok {
						return
					}
					e := event
					merged <- taggedEvent{docEvent: &e, docID: ds.docID.String()}
				}
			}
		}(ds)
	}
	for _, cs := range channelSubs {
		go func(cs channelSub) {
			for {
				select {
				case <-done:
					return
				case event, ok := <-cs.sub.Events():
					if !ok {
						return
					}
					e := event
					merged <- taggedEvent{channelEvent: &e, channelKey: string(cs.channelKey)}
				}
			}
		}(cs)
	}

	for {
		select {
		case <-s.serviceCtx.Done():
			return context.Canceled
		case <-ctx.Done():
			return context.Canceled
		case te := <-merged:
			resp, err := s.convertTaggedEvent(te)
			if err != nil {
				logging.From(ctx).Errorf("failed to convert event: %v", err)
				continue
			}
			if resp == nil {
				continue
			}

			if te.docEvent != nil {
				s.backend.Metrics.AddWatchDocumentEventPayloadBytes(
					s.backend.Config.Hostname,
					project,
					te.docEvent.Type,
					te.docEvent.Body.PayloadLen(),
				)
			}

			if err := stream.Send(resp); err != nil {
				return err
			}
		}
	}
}

// convertTaggedEvent converts a tagged event into a WatchResponse.
// Returns nil response for events that should be skipped.
func (s *yorkieServer) convertTaggedEvent(te taggedEvent) (*api.WatchResponse, error) {
	if te.docEvent != nil {
		eventType, err := converter.ToDocEventType(te.docEvent.Type)
		if err != nil {
			return nil, err
		}
		return &api.WatchResponse{
			Body: &api.WatchResponse_Event{
				Event: &api.WatchEvent{
					Event: &api.WatchEvent_DocEvent{
						DocEvent: &api.DocWatchEvent{
							DocumentId: te.docID,
							Event: &api.DocEvent{
								Type:      eventType,
								Publisher: te.docEvent.Actor.String(),
								Body: &api.DocEventBody{
									Topic:   te.docEvent.Body.Topic,
									Payload: te.docEvent.Body.Payload,
								},
							},
						},
					},
				},
			},
		}, nil
	}

	if te.channelEvent != nil {
		return convertChannelEvent(te.channelKey, te.channelEvent)
	}

	return nil, nil
}

// convertChannelEvent converts a channel event into a WatchResponse.
func convertChannelEvent(channelKey string, event *events.ChannelEvent) (*api.WatchResponse, error) {
	if event.Type == events.ChannelBroadcast {
		return &api.WatchResponse{
			Body: &api.WatchResponse_Event{
				Event: &api.WatchEvent{
					Event: &api.WatchEvent_ChannelEvent{
						ChannelEvent: &api.ChannelWatchEvent{
							ChannelKey: channelKey,
							Event: &api.ChannelEvent{
								Type:      api.ChannelEvent_TYPE_BROADCAST,
								Publisher: event.Publisher.String(),
								Topic:     event.Topic,
								Payload:   event.Payload,
							},
						},
					},
				},
			},
		}, nil
	}

	if event.Seq > 0 {
		return &api.WatchResponse{
			Body: &api.WatchResponse_Event{
				Event: &api.WatchEvent{
					Event: &api.WatchEvent_ChannelEvent{
						ChannelEvent: &api.ChannelWatchEvent{
							ChannelKey: channelKey,
							Event: &api.ChannelEvent{
								Type:         api.ChannelEvent_TYPE_PRESENCE,
								SessionCount: event.SessionCount,
								Seq:          event.Seq,
							},
						},
					},
				},
			},
		}, nil
	}

	// Skip initial event with Seq 0
	return nil, nil
}

// Broadcast broadcasts a message to all clients watching the presence.
func (s *yorkieServer) Broadcast(
	ctx context.Context,
	req *connect.Request[api.BroadcastRequest],
) (*connect.Response[api.BroadcastResponse], error) {
	// 01. Validate the request
	actorID, err := time.ActorIDFromHex(req.Msg.ClientId)
	if err != nil {
		return nil, err
	}

	channelKey := key.Key(req.Msg.ChannelKey)
	if err := channelKey.Validate(); err != nil {
		return nil, err
	}

	// 02. Verify access
	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method:     types.Broadcast,
		Attributes: types.NewAccessAttributes([]key.Key{channelKey}, types.Read),
	}); err != nil {
		return nil, err
	}

	project := projects.From(ctx)

	// 03. Verify active client
	if _, err = clients.FindActiveClientInfo(ctx, s.backend, types.ClientRefKey{
		ProjectID: project.ID,
		ClientID:  types.IDFromActorID(actorID),
	}); err != nil {
		return nil, err
	}

	// 04. Publish broadcast event
	refKey := types.ChannelRefKey{
		ProjectID:  project.ID,
		ChannelKey: channelKey,
	}

	s.backend.PubSub.PublishChannel(ctx, events.ChannelEvent{
		Type:      events.ChannelBroadcast,
		Key:       refKey,
		Publisher: actorID,
		Topic:     req.Msg.Topic,
		Payload:   req.Msg.Payload,
	})

	return connect.NewResponse(&api.BroadcastResponse{}), nil
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
	if project.HasAttachmentLimit() || project.RemoveOnDetach {
		locker := s.backend.Lockers.Locker(documents.DocAttachmentKey(docKey))
		defer locker.Unlock()
	}

	// NOTE(hackerwins): If the project does not have an attachment limit,
	// removing the document by RemoveOnDetach does not guarantee that
	// the document is not attached to the client.
	var status document.StatusType = document.StatusDetached
	if project.RemoveOnDetach {
		isAttached, err := documents.IsDocumentAttachedOrAttaching(ctx, s.backend, docKey, clientInfo.ID)
		if err != nil {
			return nil, err
		}

		if !isAttached {
			pack.IsRemoved = true
			status = document.StatusRemoved
		}
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

// CreateRevision creates a new revision for the given document.
func (s *yorkieServer) CreateRevision(
	ctx context.Context,
	req *connect.Request[api.CreateRevisionRequest],
) (*connect.Response[api.CreateRevisionResponse], error) {
	docID, err := converter.FromDocumentID(req.Msg.DocumentId)
	if err != nil {
		return nil, err
	}

	project := projects.From(ctx)
	docKey := types.DocRefKey{
		ProjectID: project.ID,
		DocID:     docID,
	}

	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method: types.CreateRevision,
	}); err != nil {
		return nil, err
	}

	revision, err := revisions.Create(
		ctx,
		s.backend,
		docKey,
		req.Msg.Label,
		req.Msg.Description,
	)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.CreateRevisionResponse{
		Revision: converter.ToRevisionSummary(revision),
	}), nil
}

// ListRevisions returns all revisions for the given document.
func (s *yorkieServer) ListRevisions(
	ctx context.Context,
	req *connect.Request[api.ListRevisionsRequest],
) (*connect.Response[api.ListRevisionsResponse], error) {
	clientID, err := time.ActorIDFromHex(req.Msg.ClientId)
	if err != nil {
		return nil, err
	}

	docID, err := converter.FromDocumentID(req.Msg.DocumentId)
	if err != nil {
		return nil, err
	}

	paging := types.Paging[int]{
		Offset:    int(req.Msg.Offset),
		PageSize:  int(req.Msg.PageSize),
		IsForward: req.Msg.IsForward,
	}

	project := projects.From(ctx)
	docKey := types.DocRefKey{ProjectID: project.ID, DocID: docID}
	docInfo, err := documents.FindDocInfoByRefKey(ctx, s.backend, docKey)
	if err != nil {
		return nil, err
	}

	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method:     types.ListRevisions,
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

	summaries, err := revisions.List(ctx, s.backend, docKey, paging, false)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.ListRevisionsResponse{
		Revisions: converter.ToRevisionSummaries(summaries),
	}), nil
}

// GetRevision returns a specific revision with its full snapshot data.
func (s *yorkieServer) GetRevision(
	ctx context.Context,
	req *connect.Request[api.GetRevisionRequest],
) (*connect.Response[api.GetRevisionResponse], error) {
	clientID, err := time.ActorIDFromHex(req.Msg.ClientId)
	if err != nil {
		return nil, err
	}

	docID, err := converter.FromDocumentID(req.Msg.DocumentId)
	if err != nil {
		return nil, err
	}

	revisionID := types.ID(req.Msg.RevisionId)
	project := projects.From(ctx)
	docKey := types.DocRefKey{ProjectID: project.ID, DocID: docID}
	docInfo, err := documents.FindDocInfoByRefKey(ctx, s.backend, docKey)
	if err != nil {
		return nil, err
	}

	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method:     types.GetRevision,
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

	// Get the revision with full snapshot
	revision, err := revisions.Get(ctx, s.backend, revisionID)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.GetRevisionResponse{
		Revision: converter.ToRevisionSummary(revision),
	}), nil
}

// RestoreRevision restores a document to a specific revision.
func (s *yorkieServer) RestoreRevision(
	ctx context.Context,
	req *connect.Request[api.RestoreRevisionRequest],
) (*connect.Response[api.RestoreRevisionResponse], error) {
	clientID, err := time.ActorIDFromHex(req.Msg.ClientId)
	if err != nil {
		return nil, err
	}

	docID, err := converter.FromDocumentID(req.Msg.DocumentId)
	if err != nil {
		return nil, err
	}

	revisionID := types.ID(req.Msg.RevisionId)
	project := projects.From(ctx)
	docKey := types.DocRefKey{ProjectID: project.ID, DocID: docID}
	docInfo, err := documents.FindDocInfoByRefKey(ctx, s.backend, docKey)
	if err != nil {
		return nil, err
	}

	locker := s.backend.Lockers.LockerWithRLock(packs.DocKey(project.ID, docInfo.Key))
	defer locker.RUnlock()

	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method:     types.RestoreRevision,
		Attributes: types.NewAccessAttributes([]key.Key{docInfo.Key}, types.ReadWrite),
	}); err != nil {
		return nil, err
	}

	if _, err = clients.FindActiveClientInfo(ctx, s.backend, types.ClientRefKey{
		ProjectID: project.ID,
		ClientID:  types.IDFromActorID(clientID),
	}); err != nil {
		return nil, err
	}

	if err := revisions.Restore(ctx, s.backend, project, revisionID); err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.RestoreRevisionResponse{}), nil
}

func (s *yorkieServer) watchDoc(
	ctx context.Context,
	clientID time.ActorID,
	docKey types.DocRefKey,
	limit int,
) (*pubsub.DocSubscription, []time.ActorID, error) {
	sub, clientIDs, err := s.backend.PubSub.Subscribe(ctx, clientID, docKey, limit)
	if err != nil {
		return nil, nil, err
	}

	s.backend.PubSub.Publish(ctx, sub.Subscriber(), events.DocEvent{
		Type:  events.DocWatched,
		Actor: sub.Subscriber(),
		Key:   docKey,
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
	sub *pubsub.DocSubscription,
	docKey types.DocRefKey,
) error {
	s.backend.PubSub.Unsubscribe(ctx, docKey, sub)
	s.backend.PubSub.Publish(ctx, sub.Subscriber(), events.DocEvent{
		Type:  events.DocUnwatched,
		Actor: sub.Subscriber(),
		Key:   docKey,
	})
	s.backend.Metrics.AddWatchDocumentEventPayloadBytes(
		s.backend.Config.Hostname,
		projects.From(ctx),
		events.DocUnwatched,
		0,
	)

	return nil
}

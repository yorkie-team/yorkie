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
	"fmt"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/sync"
	"github.com/yorkie-team/yorkie/server/clients"
	"github.com/yorkie-team/yorkie/server/documents"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/packs"
	"github.com/yorkie-team/yorkie/server/projects"
	"github.com/yorkie-team/yorkie/server/rpc/auth"
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
	req *api.ActivateClientRequest,
) (*api.ActivateClientResponse, error) {
	if req.ClientKey == "" {
		return nil, clients.ErrInvalidClientKey
	}

	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method: types.ActivateClient,
	}); err != nil {
		return nil, fmt.Errorf("verify access: %w", err)
	}

	cli, err := clients.Activate(ctx, s.backend.DB, projects.From(ctx), req.ClientKey)
	if err != nil {
		return nil, fmt.Errorf("activate client: %w", err)
	}

	pbClientID, err := cli.ID.Bytes()
	if err != nil {
		return nil, fmt.Errorf("encode client id: %w", err)
	}

	return &api.ActivateClientResponse{
		ClientKey: cli.Key,
		ClientId:  pbClientID,
	}, nil
}

// DeactivateClient deactivates the given client.
func (s *yorkieServer) DeactivateClient(
	ctx context.Context,
	req *api.DeactivateClientRequest,
) (*api.DeactivateClientResponse, error) {
	actorID, err := time.ActorIDFromBytes(req.ClientId)
	if err != nil {
		return nil, fmt.Errorf("parsing client id: %w", err)
	}

	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method: types.DeactivateClient,
	}); err != nil {
		return nil, fmt.Errorf("verify access: %w", err)
	}

	cli, err := clients.Deactivate(
		ctx,
		s.backend.DB,
		projects.From(ctx).ID,
		types.IDFromActorID(actorID),
	)
	if err != nil {
		return nil, fmt.Errorf("deactivate client: %w", err)
	}

	pbClientID, err := cli.ID.Bytes()
	if err != nil {
		return nil, fmt.Errorf("convert client id to bytes: %w", err)
	}

	return &api.DeactivateClientResponse{
		ClientId: pbClientID,
	}, nil
}

// AttachDocument attaches the given document to the client.
func (s *yorkieServer) AttachDocument(
	ctx context.Context,
	req *api.AttachDocumentRequest,
) (*api.AttachDocumentResponse, error) {
	actorID, err := time.ActorIDFromBytes(req.ClientId)
	if err != nil {
		return nil, fmt.Errorf("parsing client id: %w", err)
	}

	pack, err := converter.FromChangePack(req.ChangePack)
	if err != nil {
		return nil, fmt.Errorf("convert from change pack: %w", err)
	}

	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method:     types.AttachDocument,
		Attributes: auth.AccessAttributes(pack),
	}); err != nil {
		return nil, fmt.Errorf("verifying access: %w", err)
	}

	if pack.HasChanges() {
		locker, err := s.backend.Coordinator.NewLocker(
			ctx,
			packs.PushPullKey(projects.From(ctx).ID, pack.DocumentKey),
		)
		if err != nil {
			return nil, fmt.Errorf("new locker: %w", err)
		}

		if err := locker.Lock(ctx); err != nil {
			return nil, err
		}
		defer func() {
			if err := locker.Unlock(ctx); err != nil {
				logging.DefaultLogger().Error(err)
			}
		}()
	}

	clientInfo, err := clients.FindClientInfo(
		ctx,
		s.backend.DB,
		projects.From(ctx),
		actorID,
	)
	if err != nil {
		return nil, fmt.Errorf("find client info: %w", err)
	}
	docInfo, err := documents.FindDocInfoByKeyAndOwner(
		ctx,
		s.backend,
		projects.From(ctx),
		clientInfo,
		pack.DocumentKey,
		true,
	)
	if err != nil {
		return nil, err
	}

	if err := clientInfo.AttachDocument(docInfo.ID); err != nil {
		return nil, fmt.Errorf("attach document: %w", err)
	}

	pulled, err := packs.PushPull(ctx, s.backend, projects.From(ctx), clientInfo, docInfo, pack)
	if err != nil {
		return nil, fmt.Errorf("push pull pack: %w", err)
	}

	pbChangePack, err := pulled.ToPBChangePack()
	if err != nil {
		return nil, err
	}

	return &api.AttachDocumentResponse{
		ChangePack: pbChangePack,
	}, nil
}

// DetachDocument detaches the given document to the client.
func (s *yorkieServer) DetachDocument(
	ctx context.Context,
	req *api.DetachDocumentRequest,
) (*api.DetachDocumentResponse, error) {
	actorID, err := time.ActorIDFromBytes(req.ClientId)
	if err != nil {
		return nil, fmt.Errorf("parse client id: %w", err)
	}

	pack, err := converter.FromChangePack(req.ChangePack)
	if err != nil {
		return nil, fmt.Errorf("convert from change pack: %w", err)
	}

	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method:     types.DetachDocument,
		Attributes: auth.AccessAttributes(pack),
	}); err != nil {
		return nil, err
	}

	if pack.HasChanges() {
		locker, err := s.backend.Coordinator.NewLocker(
			ctx,
			packs.PushPullKey(projects.From(ctx).ID, pack.DocumentKey),
		)
		if err != nil {
			return nil, fmt.Errorf("new locker: %w", err)
		}

		if err := locker.Lock(ctx); err != nil {
			return nil, fmt.Errorf("lock: %w", err)
		}
		defer func() {
			if err := locker.Unlock(ctx); err != nil {
				logging.DefaultLogger().Error(err)
			}
		}()
	}

	clientInfo, err := clients.FindClientInfo(
		ctx,
		s.backend.DB,
		projects.From(ctx),
		actorID,
	)
	if err != nil {
		return nil, fmt.Errorf("find client info: %w", err)
	}
	docInfo, err := documents.FindDocInfoByKeyAndOwner(
		ctx,
		s.backend,
		projects.From(ctx),
		clientInfo,
		pack.DocumentKey,
		false,
	)
	if err != nil {
		return nil, err
	}

	if err := clientInfo.EnsureDocumentAttached(docInfo.ID); err != nil {
		return nil, fmt.Errorf("ensure document attached: %w", err)
	}
	if err := clientInfo.DetachDocument(docInfo.ID); err != nil {
		return nil, fmt.Errorf("detach document: %w", err)
	}

	pulled, err := packs.PushPull(ctx, s.backend, projects.From(ctx), clientInfo, docInfo, pack)
	if err != nil {
		return nil, fmt.Errorf("push pull pack: %w", err)
	}

	pbChangePack, err := pulled.ToPBChangePack()
	if err != nil {
		return nil, fmt.Errorf("convert change pack to pb: %w", err)
	}

	return &api.DetachDocumentResponse{
		ChangePack: pbChangePack,
	}, nil
}

// PushPull stores the changes sent by the client and delivers the changes
// accumulated in the server to the client.
func (s *yorkieServer) PushPull(
	ctx context.Context,
	req *api.PushPullRequest,
) (*api.PushPullResponse, error) {
	actorID, err := time.ActorIDFromBytes(req.ClientId)
	if err != nil {
		return nil, err
	}

	pack, err := converter.FromChangePack(req.ChangePack)
	if err != nil {
		return nil, fmt.Errorf("convert change pack: %w", err)
	}

	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method:     types.PushPull,
		Attributes: auth.AccessAttributes(pack),
	}); err != nil {
		return nil, err
	}

	if pack.HasChanges() {
		locker, err := s.backend.Coordinator.NewLocker(
			ctx,
			packs.PushPullKey(projects.From(ctx).ID, pack.DocumentKey),
		)
		if err != nil {
			return nil, fmt.Errorf("new locker: %w", err)
		}

		if err := locker.Lock(ctx); err != nil {
			return nil, fmt.Errorf("lock: %w", err)
		}
		defer func() {
			if err := locker.Unlock(ctx); err != nil {
				logging.DefaultLogger().Error(err)
			}
		}()
	}

	clientInfo, err := clients.FindClientInfo(
		ctx,
		s.backend.DB,
		projects.From(ctx),
		actorID,
	)
	if err != nil {
		return nil, err
	}
	docInfo, err := documents.FindDocInfoByKeyAndOwner(
		ctx,
		s.backend,
		projects.From(ctx),
		clientInfo,
		pack.DocumentKey,
		false,
	)
	if err != nil {
		return nil, fmt.Errorf("find document info: %w", err)
	}

	if err := clientInfo.EnsureDocumentAttached(docInfo.ID); err != nil {
		return nil, fmt.Errorf("ensure document attached: %w", err)
	}

	pulled, err := packs.PushPull(ctx, s.backend, projects.From(ctx), clientInfo, docInfo, pack)
	if err != nil {
		return nil, fmt.Errorf("push pull pack: %w", err)
	}

	pbChangePack, err := pulled.ToPBChangePack()
	if err != nil {
		return nil, fmt.Errorf("convert change pack to protobuf: %w", err)
	}

	return &api.PushPullResponse{
		ChangePack: pbChangePack,
	}, nil
}

// WatchDocuments connects the stream to deliver events from the given documents
// to the requesting client.
func (s *yorkieServer) WatchDocuments(
	req *api.WatchDocumentsRequest,
	stream api.YorkieService_WatchDocumentsServer,
) error {
	cli, err := converter.FromClient(req.Client)
	if err != nil {
		return fmt.Errorf("convert client: %w", err)
	}
	docKeys := converter.FromDocumentKeys(req.DocumentKeys)

	var attrs []types.AccessAttribute
	for _, k := range docKeys {
		attrs = append(attrs, types.AccessAttribute{
			Key:  k.String(),
			Verb: types.Read,
		})
	}

	if err := auth.VerifyAccess(stream.Context(), s.backend, &types.AccessInfo{
		Method:     types.WatchDocuments,
		Attributes: attrs,
	}); err != nil {
		return err
	}

	if _, err = clients.FindClientInfo(
		stream.Context(),
		s.backend.DB,
		projects.From(stream.Context()),
		cli.ID,
	); err != nil {
		return err
	}

	subscription, peersMap, err := s.watchDocs(
		stream.Context(),
		*cli,
		docKeys,
	)
	if err != nil {
		logging.From(stream.Context()).Error(err)
		return err
	}

	if err := stream.Send(&api.WatchDocumentsResponse{
		Body: &api.WatchDocumentsResponse_Initialization_{
			Initialization: &api.WatchDocumentsResponse_Initialization{
				PeersMapByDoc: converter.ToClientsMap(peersMap),
			},
		},
	}); err != nil {
		s.unwatchDocs(docKeys, subscription)
		return fmt.Errorf("sending initialization: %w", err)
	}

	for {
		select {
		case <-s.serviceCtx.Done():
			s.unwatchDocs(docKeys, subscription)
			return nil
		case <-stream.Context().Done():
			s.unwatchDocs(docKeys, subscription)
			return nil
		case event := <-subscription.Events():
			eventType, err := converter.ToDocEventType(event.Type)
			if err != nil {
				return fmt.Errorf("converting event type: %w", err)
			}

			if err := stream.Send(&api.WatchDocumentsResponse{
				Body: &api.WatchDocumentsResponse_Event{
					Event: &api.DocEvent{
						Type:         eventType,
						Publisher:    converter.ToClient(event.Publisher),
						DocumentKeys: converter.ToDocumentKeys(event.DocumentKeys),
					},
				},
			}); err != nil {
				s.unwatchDocs(docKeys, subscription)
				return fmt.Errorf("sending watch document event to client: %w", err)
			}
		}
	}
}

// UpdatePresence updates the presence of the given client.
func (s *yorkieServer) UpdatePresence(
	ctx context.Context,
	req *api.UpdatePresenceRequest,
) (*api.UpdatePresenceResponse, error) {
	cli, err := converter.FromClient(req.Client)
	if err != nil {
		return nil, fmt.Errorf("converting client: %w", err)
	}
	keys := converter.FromDocumentKeys(req.DocumentKeys)

	docEvent, err := s.backend.Coordinator.UpdatePresence(ctx, cli, keys)
	if err != nil {
		return nil, fmt.Errorf("update presence: %w", err)
	}

	s.backend.Coordinator.Publish(ctx, docEvent.Publisher.ID, *docEvent)

	return &api.UpdatePresenceResponse{}, nil
}

func (s *yorkieServer) watchDocs(
	ctx context.Context,
	client types.Client,
	docKeys []key.Key,
) (*sync.Subscription, map[string][]types.Client, error) {
	subscription, peersMap, err := s.backend.Coordinator.Subscribe(
		ctx,
		client,
		docKeys,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("subscribe: %w", err)
	}

	s.backend.Coordinator.Publish(
		ctx,
		subscription.Subscriber().ID,
		sync.DocEvent{
			Type:         types.DocumentsWatchedEvent,
			Publisher:    subscription.Subscriber(),
			DocumentKeys: docKeys,
		},
	)

	return subscription, peersMap, nil
}

func (s *yorkieServer) unwatchDocs(
	docKeys []key.Key,
	subscription *sync.Subscription,
) {
	ctx := context.Background()
	_ = s.backend.Coordinator.Unsubscribe(ctx, docKeys, subscription)
	s.backend.Coordinator.Publish(
		ctx,
		subscription.Subscriber().ID,
		sync.DocEvent{
			Type:         types.DocumentsUnwatchedEvent,
			Publisher:    subscription.Subscriber(),
			DocumentKeys: docKeys,
		},
	)
}

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
		return nil, err
	}

	cli, err := clients.Activate(ctx, s.backend.DB, projects.From(ctx), req.ClientKey)
	if err != nil {
		return nil, err
	}

	pbClientID, err := cli.ID.Bytes()
	if err != nil {
		return nil, err
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
		return nil, err
	}

	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method: types.DeactivateClient,
	}); err != nil {
		return nil, err
	}

	cli, err := clients.Deactivate(
		ctx,
		s.backend.DB,
		projects.From(ctx).ID,
		types.IDFromActorID(actorID),
	)
	if err != nil {
		return nil, err
	}

	pbClientID, err := cli.ID.Bytes()
	if err != nil {
		return nil, err
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
		return nil, err
	}

	pack, err := converter.FromChangePack(req.ChangePack)
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

	if pack.HasChanges() {
		locker, err := s.backend.Coordinator.NewLocker(
			ctx,
			packs.PushPullKey(projects.From(ctx).ID, pack.DocumentKey),
		)
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
		true,
	)

	if err != nil {
		return nil, err
	}

	if err := clientInfo.AttachDocument(docInfo.ID); err != nil {
		return nil, err
	}

	pulled, err := packs.PushPull(ctx, s.backend, projects.From(ctx), clientInfo, docInfo, pack)
	if err != nil {
		return nil, err
	}

	pbChangePack, err := pulled.ToPBChangePack()
	if err != nil {
		return nil, err
	}

	return &api.AttachDocumentResponse{
		ChangePack: pbChangePack,
		DocumentId: docInfo.ID.String(),
	}, nil
}

// DetachDocument detaches the given document to the client.
func (s *yorkieServer) DetachDocument(
	ctx context.Context,
	req *api.DetachDocumentRequest,
) (*api.DetachDocumentResponse, error) {
	actorID, err := time.ActorIDFromBytes(req.ClientId)
	if err != nil {
		return nil, err
	}

	pack, err := converter.FromChangePack(req.ChangePack)
	if err != nil {
		return nil, err
	}
	docID := types.ID(req.DocumentId)
	if err := docID.Validate(); err != nil {
		return nil, err
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
	docInfo, err := documents.FindDocInfo(
		ctx,
		s.backend,
		projects.From(ctx),
		docID,
	)
	if err != nil {
		return nil, err
	}

	if err := clientInfo.DetachDocument(docInfo.ID); err != nil {
		return nil, err
	}

	pulled, err := packs.PushPull(ctx, s.backend, projects.From(ctx), clientInfo, docInfo, pack)
	if err != nil {
		return nil, err
	}

	pbChangePack, err := pulled.ToPBChangePack()
	if err != nil {
		return nil, err
	}

	return &api.DetachDocumentResponse{
		ChangePack: pbChangePack,
	}, nil
}

// PushPullChanges stores the changes sent by the client and delivers the changes
// accumulated in the server to the client.
func (s *yorkieServer) PushPullChanges(
	ctx context.Context,
	req *api.PushPullChangesRequest,
) (*api.PushPullChangesResponse, error) {
	actorID, err := time.ActorIDFromBytes(req.ClientId)
	if err != nil {
		return nil, err
	}

	pack, err := converter.FromChangePack(req.ChangePack)
	if err != nil {
		return nil, err
	}
	docID := types.ID(req.DocumentId)
	if err := docID.Validate(); err != nil {
		return nil, err
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
	docInfo, err := documents.FindDocInfo(
		ctx,
		s.backend,
		projects.From(ctx),
		docID,
	)
	if err != nil {
		return nil, err
	}

	if err := clientInfo.EnsureDocumentAttached(docInfo.ID); err != nil {
		return nil, err
	}

	pulled, err := packs.PushPull(ctx, s.backend, projects.From(ctx), clientInfo, docInfo, pack)
	if err != nil {
		return nil, err
	}

	pbChangePack, err := pulled.ToPBChangePack()
	if err != nil {
		return nil, err
	}

	return &api.PushPullChangesResponse{
		ChangePack: pbChangePack,
	}, nil
}

// WatchDocument connects the stream to deliver events from the given documents
// to the requesting client.
func (s *yorkieServer) WatchDocument(
	req *api.WatchDocumentRequest,
	stream api.YorkieService_WatchDocumentServer,
) error {
	cli, err := converter.FromClient(req.Client)
	if err != nil {
		return err
	}
	docID, err := converter.FromDocumentID(req.DocumentId)
	if err != nil {
		return err
	}

	docInfo, err := documents.FindDocInfo(
		stream.Context(),
		s.backend,
		projects.From(stream.Context()),
		docID,
	)
	if err != nil {
		return nil
	}

	if err := auth.VerifyAccess(stream.Context(), s.backend, &types.AccessInfo{
		Method:     types.WatchDocuments,
		Attributes: types.NewAccessAttributes([]key.Key{docInfo.Key}, types.Read),
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

	subscription, peersMap, err := s.watchDoc(stream.Context(), *cli, docID)
	if err != nil {
		logging.From(stream.Context()).Error(err)
		return err
	}
	defer func() {
		s.unwatchDoc(subscription, docID)
	}()

	if err := stream.Send(&api.WatchDocumentResponse{
		Body: &api.WatchDocumentResponse_Initialization_{
			Initialization: &api.WatchDocumentResponse_Initialization{
				Peers: converter.ToClients(peersMap),
			},
		},
	}); err != nil {
		return err
	}

	for {
		select {
		case <-s.serviceCtx.Done():
			return nil
		case <-stream.Context().Done():
			return nil
		case event := <-subscription.Events():
			eventType, err := converter.ToDocEventType(event.Type)
			if err != nil {
				return err
			}

			if err := stream.Send(&api.WatchDocumentResponse{
				Body: &api.WatchDocumentResponse_Event{
					Event: &api.DocEvent{
						Type:       eventType,
						Publisher:  converter.ToClient(event.Publisher),
						DocumentId: event.DocumentID.String(),
					},
				},
			}); err != nil {
				return err
			}
		}
	}
}

// RemoveDocument removes the given document.
func (s *yorkieServer) RemoveDocument(
	ctx context.Context,
	req *api.RemoveDocumentRequest,
) (*api.RemoveDocumentResponse, error) {
	actorID, err := time.ActorIDFromBytes(req.ClientId)
	if err != nil {
		return nil, err
	}

	pack, err := converter.FromChangePack(req.ChangePack)
	if err != nil {
		return nil, err
	}
	docID := types.ID(req.DocumentId)
	if err := docID.Validate(); err != nil {
		return nil, err
	}

	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method:     types.RemoveDocument,
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
	docInfo, err := documents.FindDocInfo(
		ctx,
		s.backend,
		projects.From(ctx),
		docID,
	)
	if err != nil {
		return nil, err
	}

	if err := clientInfo.RemoveDocument(docInfo.ID); err != nil {
		return nil, err
	}

	pulled, err := packs.PushPull(ctx, s.backend, projects.From(ctx), clientInfo, docInfo, pack)
	if err != nil {
		return nil, err
	}

	pbChangePack, err := pulled.ToPBChangePack()
	if err != nil {
		return nil, err
	}

	return &api.RemoveDocumentResponse{
		ChangePack: pbChangePack,
	}, nil
}

// UpdatePresence updates the presence of the given client.
func (s *yorkieServer) UpdatePresence(
	ctx context.Context,
	req *api.UpdatePresenceRequest,
) (*api.UpdatePresenceResponse, error) {
	cli, err := converter.FromClient(req.Client)
	if err != nil {
		return nil, err
	}
	documentID, err := converter.FromDocumentID(req.DocumentId)
	if err != nil {
		return nil, err
	}

	_, err = documents.FindDocInfo(
		ctx,
		s.backend,
		projects.From(ctx),
		documentID,
	)
	if err != nil {
		return nil, err
	}

	if err = s.backend.Coordinator.UpdatePresence(ctx, cli, documentID); err != nil {
		return nil, err
	}

	s.backend.Coordinator.Publish(ctx, cli.ID, sync.DocEvent{
		Type:       types.DocumentsWatchedEvent,
		Publisher:  *cli,
		DocumentID: documentID,
	})

	return &api.UpdatePresenceResponse{}, nil
}

func (s *yorkieServer) watchDoc(
	ctx context.Context,
	client types.Client,
	documentID types.ID,
) (*sync.Subscription, []types.Client, error) {
	subscription, peers, err := s.backend.Coordinator.Subscribe(
		ctx,
		client,
		documentID,
	)
	if err != nil {
		logging.From(ctx).Error(err)
		return nil, nil, err
	}

	s.backend.Coordinator.Publish(
		ctx,
		subscription.Subscriber().ID,
		sync.DocEvent{
			Type:       types.DocumentsWatchedEvent,
			Publisher:  subscription.Subscriber(),
			DocumentID: documentID,
		},
	)

	return subscription, peers, nil
}

func (s *yorkieServer) unwatchDoc(
	subscription *sync.Subscription,
	documentID types.ID,
) {
	ctx := context.Background()
	_ = s.backend.Coordinator.Unsubscribe(ctx, documentID, subscription)
	s.backend.Coordinator.Publish(
		ctx,
		subscription.Subscriber().ID,
		sync.DocEvent{
			Type:       types.DocumentsUnwatchedEvent,
			Publisher:  subscription.Subscriber(),
			DocumentID: documentID,
		},
	)
}

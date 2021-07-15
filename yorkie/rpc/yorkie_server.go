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

	"github.com/yorkie-team/yorkie/api"
	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/internal/log"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/yorkie/auth"
	"github.com/yorkie-team/yorkie/yorkie/backend"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync"
	"github.com/yorkie-team/yorkie/yorkie/clients"
	"github.com/yorkie-team/yorkie/yorkie/packs"
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

	client, err := clients.Activate(ctx, s.backend, req.ClientKey)
	if err != nil {
		return nil, err
	}

	return &api.ActivateClientResponse{
		ClientKey: client.Key,
		ClientId:  client.ID.Bytes(),
	}, nil
}

// DeactivateClient deactivates the given client.
func (s *yorkieServer) DeactivateClient(
	ctx context.Context,
	req *api.DeactivateClientRequest,
) (*api.DeactivateClientResponse, error) {
	if len(req.ClientId) == 0 {
		return nil, clients.ErrInvalidClientID
	}

	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method: types.DeactivateClient,
	}); err != nil {
		return nil, err
	}

	client, err := clients.Deactivate(ctx, s.backend, req.ClientId)
	if err != nil {
		return nil, err
	}

	return &api.DeactivateClientResponse{
		ClientId: client.ID.Bytes(),
	}, nil
}

// AttachDocument attaches the given document to the client.
func (s *yorkieServer) AttachDocument(
	ctx context.Context,
	req *api.AttachDocumentRequest,
) (*api.AttachDocumentResponse, error) {
	pack, err := converter.FromChangePack(req.ChangePack)
	if err != nil {
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
			packs.NewPushPullKey(pack.DocumentKey),
		)
		if err != nil {
			return nil, err
		}

		if err := locker.Lock(ctx); err != nil {
			return nil, err
		}
		defer func() {
			if err := locker.Unlock(ctx); err != nil {
				log.Logger.Error(err)
			}
		}()
	}

	clientInfo, docInfo, err := clients.FindClientAndDocument(
		ctx,
		s.backend,
		req.ClientId,
		pack,
		true,
	)
	if err != nil {
		return nil, err
	}
	if err := clientInfo.AttachDocument(docInfo.ID); err != nil {
		return nil, err
	}

	pulled, err := packs.PushPull(ctx, s.backend, clientInfo, docInfo, pack)
	if err != nil {
		return nil, err
	}

	pbChangePack, err := converter.ToChangePack(pulled)
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
	pack, err := converter.FromChangePack(req.ChangePack)
	if err != nil {
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
			packs.NewPushPullKey(pack.DocumentKey),
		)
		if err != nil {
			return nil, err
		}

		if err := locker.Lock(ctx); err != nil {
			return nil, err
		}
		defer func() {
			if err := locker.Unlock(ctx); err != nil {
				log.Logger.Error(err)
			}
		}()
	}

	clientInfo, docInfo, err := clients.FindClientAndDocument(
		ctx,
		s.backend,
		req.ClientId,
		pack,
		false,
	)
	if err != nil {
		return nil, err
	}
	if err := clientInfo.EnsureDocumentAttached(docInfo.ID); err != nil {
		return nil, err
	}
	if err := clientInfo.DetachDocument(docInfo.ID); err != nil {
		return nil, err
	}

	pulled, err := packs.PushPull(ctx, s.backend, clientInfo, docInfo, pack)
	if err != nil {
		return nil, err
	}

	pbChangePack, err := converter.ToChangePack(pulled)
	if err != nil {
		return nil, err
	}

	return &api.DetachDocumentResponse{
		ChangePack: pbChangePack,
	}, nil
}

// PushPull stores the changes sent by the client and delivers the changes
// accumulated in the agent to the client.
func (s *yorkieServer) PushPull(
	ctx context.Context,
	req *api.PushPullRequest,
) (*api.PushPullResponse, error) {
	start := gotime.Now()
	pack, err := converter.FromChangePack(req.ChangePack)
	if err != nil {
		return nil, err
	}

	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method:     types.PushPull,
		Attributes: auth.AccessAttributes(pack),
	}); err != nil {
		return nil, err
	}

	if pack.HasChanges() {
		s.backend.Metrics.SetPushPullReceivedChanges(len(pack.Changes))

		locker, err := s.backend.Coordinator.NewLocker(
			ctx,
			packs.NewPushPullKey(pack.DocumentKey),
		)
		if err != nil {
			return nil, err
		}

		if err := locker.Lock(ctx); err != nil {
			log.Logger.Error(err)
			return nil, err
		}
		defer func() {
			if err := locker.Unlock(ctx); err != nil {
				log.Logger.Error(err)
			}
		}()
	}

	clientInfo, docInfo, err := clients.FindClientAndDocument(
		ctx,
		s.backend,
		req.ClientId,
		pack,
		false,
	)
	if err != nil {
		return nil, err
	}
	if err := clientInfo.EnsureDocumentAttached(docInfo.ID); err != nil {
		return nil, err
	}

	pulled, err := packs.PushPull(ctx, s.backend, clientInfo, docInfo, pack)
	if err != nil {
		return nil, err
	}

	pbChangePack, err := converter.ToChangePack(pulled)
	if err != nil {
		return nil, err
	}

	s.backend.Metrics.SetPushPullSentChanges(len(pbChangePack.Changes))
	s.backend.Metrics.ObservePushPullResponseSeconds(gotime.Since(start).Seconds())

	return &api.PushPullResponse{
		ChangePack: pbChangePack,
	}, nil
}

// WatchDocuments connects the stream to deliver events from the given documents
// to the requesting client.
func (s *yorkieServer) WatchDocuments(
	req *api.WatchDocumentsRequest,
	stream api.Yorkie_WatchDocumentsServer,
) error {
	client, err := converter.FromClient(req.Client)
	if err != nil {
		return err
	}
	docKeys := converter.FromDocumentKeys(req.DocumentKeys)

	var attrs []types.AccessAttribute
	for _, k := range docKeys {
		attrs = append(attrs, types.AccessAttribute{
			Key:  k.BSONKey(),
			Verb: types.Read,
		})
	}
	if err := auth.VerifyAccess(stream.Context(), s.backend, &types.AccessInfo{
		Method:     types.WatchDocuments,
		Attributes: attrs,
	}); err != nil {
		return err
	}

	subscription, peersMap, err := s.watchDocs(
		stream.Context(),
		*client,
		docKeys,
	)
	if err != nil {
		log.Logger.Error(err)
		return err
	}

	if err := stream.Send(&api.WatchDocumentsResponse{
		Body: &api.WatchDocumentsResponse_Initialization_{
			Initialization: &api.WatchDocumentsResponse_Initialization{
				PeersMapByDoc: converter.ToClientsMap(peersMap),
			},
		},
	}); err != nil {
		log.Logger.Error(err)
		s.unwatchDocs(docKeys, subscription)
		return err
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
				return err
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
				log.Logger.Error(err)
				s.unwatchDocs(docKeys, subscription)
				return err
			}
		}
	}
}

func (s *yorkieServer) watchDocs(
	ctx context.Context,
	client types.Client,
	docKeys []*key.Key,
) (*sync.Subscription, map[string][]types.Client, error) {
	subscription, peersMap, err := s.backend.Coordinator.Subscribe(
		ctx,
		client,
		docKeys,
	)
	if err != nil {
		log.Logger.Error(err)
		return nil, nil, err
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
	docKeys []*key.Key,
	subscription *sync.Subscription,
) {
	ctx := context.Background()
	s.backend.Coordinator.Unsubscribe(ctx, docKeys, subscription)

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

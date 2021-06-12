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
	"net"
	gotime "time"

	grpcmiddleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpcprometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

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
	"github.com/yorkie-team/yorkie/yorkie/rpc/interceptors"
)

// Config is the configuration for creating a Server instance.
type Config struct {
	Port     int
	CertFile string
	KeyFile  string
}

// Server is a normal server that processes the logic requested by the client.
type Server struct {
	conf       *Config
	grpcServer *grpc.Server
	backend    *backend.Backend
}

// NewServer creates a new instance of Server.
func NewServer(conf *Config, be *backend.Backend) (*Server, error) {
	authInterceptor := interceptors.NewAuthInterceptor(be.Config.AuthorizationWebhookURL)
	defaultInterceptor := interceptors.NewDefaultInterceptor()

	opts := []grpc.ServerOption{
		grpc.UnaryInterceptor(grpcmiddleware.ChainUnaryServer(
			authInterceptor.Unary(),
			defaultInterceptor.Unary(),
			grpcprometheus.UnaryServerInterceptor,
		)),
		grpc.StreamInterceptor(grpcmiddleware.ChainStreamServer(
			authInterceptor.Stream(),
			defaultInterceptor.Stream(),
			grpcprometheus.StreamServerInterceptor,
		)),
	}

	if conf.CertFile != "" && conf.KeyFile != "" {
		creds, err := credentials.NewServerTLSFromFile(conf.CertFile, conf.KeyFile)
		if err != nil {
			return nil, errors.WithStack(err)
		}
		opts = append(opts, grpc.Creds(creds))
	}

	grpcServer := grpc.NewServer(opts...)
	healthServer := health.NewServer()
	healthpb.RegisterHealthServer(grpcServer, healthServer)

	rpcServer := &Server{
		conf:       conf,
		grpcServer: grpcServer,
		backend:    be,
	}
	api.RegisterYorkieServer(rpcServer.grpcServer, rpcServer)
	api.RegisterClusterServer(rpcServer.grpcServer, rpcServer)
	grpcprometheus.Register(rpcServer.grpcServer)

	return rpcServer, nil
}

// Start starts this server by opening the rpc port.
func (s *Server) Start() error {
	return s.listenAndServeGRPC()
}

// Shutdown shuts down this server.
func (s *Server) Shutdown(graceful bool) {
	if graceful {
		s.grpcServer.GracefulStop()
	} else {
		s.grpcServer.Stop()
	}
}

// ActivateClient activates the given client.
func (s *Server) ActivateClient(
	ctx context.Context,
	req *api.ActivateClientRequest,
) (*api.ActivateClientResponse, error) {
	if req.ClientKey == "" {
		log.Logger.Error(clients.ErrInvalidClientKey)
		return nil, clients.ErrInvalidClientKey
	}

	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method: types.ActivateClient,
	}); err != nil {
		log.Logger.Errorf("%+v", err)
		return nil, err
	}

	client, err := clients.Activate(ctx, s.backend, req.ClientKey)
	if err != nil {
		log.Logger.Errorf("%+v", err)
		return nil, err
	}

	return &api.ActivateClientResponse{
		ClientKey: client.Key,
		ClientId:  client.ID.Bytes(),
	}, nil
}

// DeactivateClient deactivates the given client.
func (s *Server) DeactivateClient(
	ctx context.Context,
	req *api.DeactivateClientRequest,
) (*api.DeactivateClientResponse, error) {
	if len(req.ClientId) == 0 {
		return nil, clients.ErrInvalidClientID
	}

	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method: types.DeactivateClient,
	}); err != nil {
		log.Logger.Errorf("%+v", err)
		return nil, err
	}

	client, err := clients.Deactivate(ctx, s.backend, req.ClientId)
	if err != nil {
		log.Logger.Errorf("%+v", err)
		return nil, err
	}

	return &api.DeactivateClientResponse{
		ClientId: client.ID.Bytes(),
	}, nil
}

// AttachDocument attaches the given document to the client.
func (s *Server) AttachDocument(
	ctx context.Context,
	req *api.AttachDocumentRequest,
) (*api.AttachDocumentResponse, error) {
	pack, err := converter.FromChangePack(req.ChangePack)
	if err != nil {
		log.Logger.Errorf("%+v", err)
		return nil, err
	}

	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method:     types.AttachDocument,
		Attributes: auth.AccessAttributes(pack),
	}); err != nil {
		log.Logger.Errorf("%+v", err)
		return nil, err
	}

	if pack.HasChanges() {
		locker, err := s.backend.Coordinator.NewLocker(
			ctx,
			packs.NewPushPullKey(pack.DocumentKey),
		)
		if err != nil {
			log.Logger.Errorf("%+v", err)
			return nil, err
		}

		if err := locker.Lock(ctx); err != nil {
			log.Logger.Errorf("%+v", err)
			return nil, err
		}
		defer func() {
			if err := locker.Unlock(ctx); err != nil {
				log.Logger.Errorf("%+v", err)
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
		log.Logger.Errorf("%+v", err)
		return nil, err
	}
	if err := clientInfo.AttachDocument(docInfo.ID); err != nil {
		log.Logger.Errorf("%+v", err)
		return nil, err
	}

	pulled, err := packs.PushPull(ctx, s.backend, clientInfo, docInfo, pack)
	if err != nil {
		log.Logger.Errorf("%+v", err)
		return nil, err
	}

	pbChangePack, err := converter.ToChangePack(pulled)
	if err != nil {
		log.Logger.Errorf("%+v", err)
		return nil, err
	}

	return &api.AttachDocumentResponse{
		ChangePack: pbChangePack,
	}, nil
}

// DetachDocument detaches the given document to the client.
func (s *Server) DetachDocument(
	ctx context.Context,
	req *api.DetachDocumentRequest,
) (*api.DetachDocumentResponse, error) {
	pack, err := converter.FromChangePack(req.ChangePack)
	if err != nil {
		log.Logger.Errorf("%+v", err)
		return nil, err
	}

	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method:     types.DetachDocument,
		Attributes: auth.AccessAttributes(pack),
	}); err != nil {
		log.Logger.Errorf("%+v", err)
		return nil, err
	}

	if pack.HasChanges() {
		locker, err := s.backend.Coordinator.NewLocker(
			ctx,
			packs.NewPushPullKey(pack.DocumentKey),
		)
		if err != nil {
			log.Logger.Errorf("%+v", err)
			return nil, err
		}

		if err := locker.Lock(ctx); err != nil {
			log.Logger.Errorf("%+v", err)
			return nil, err
		}
		defer func() {
			if err := locker.Unlock(ctx); err != nil {
				log.Logger.Errorf("%+v", err)
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
		log.Logger.Errorf("%+v", err)
		return nil, err
	}
	if err := clientInfo.EnsureDocumentAttached(docInfo.ID); err != nil {
		log.Logger.Errorf("%+v", err)
		return nil, err
	}
	if err := clientInfo.DetachDocument(docInfo.ID); err != nil {
		log.Logger.Errorf("%+v", err)
		return nil, err
	}

	pulled, err := packs.PushPull(ctx, s.backend, clientInfo, docInfo, pack)
	if err != nil {
		log.Logger.Errorf("%+v", err)
		return nil, err
	}

	pbChangePack, err := converter.ToChangePack(pulled)
	if err != nil {
		log.Logger.Errorf("%+v", err)
		return nil, err
	}

	return &api.DetachDocumentResponse{
		ChangePack: pbChangePack,
	}, nil
}

// PushPull stores the changes sent by the client and delivers the changes
// accumulated in the agent to the client.
func (s *Server) PushPull(
	ctx context.Context,
	req *api.PushPullRequest,
) (*api.PushPullResponse, error) {
	start := gotime.Now()
	pack, err := converter.FromChangePack(req.ChangePack)
	if err != nil {
		log.Logger.Errorf("%+v", err)
		return nil, err
	}

	if err := auth.VerifyAccess(ctx, s.backend, &types.AccessInfo{
		Method:     types.PushPull,
		Attributes: auth.AccessAttributes(pack),
	}); err != nil {
		log.Logger.Errorf("%+v", err)
		return nil, err
	}

	if pack.HasChanges() {
		s.backend.Metrics.SetPushPullReceivedChanges(len(pack.Changes))

		locker, err := s.backend.Coordinator.NewLocker(
			ctx,
			packs.NewPushPullKey(pack.DocumentKey),
		)
		if err != nil {
			log.Logger.Errorf("%+v", err)
			return nil, err
		}

		if err := locker.Lock(ctx); err != nil {
			log.Logger.Errorf("%+v", err)
			return nil, err
		}
		defer func() {
			if err := locker.Unlock(ctx); err != nil {
				log.Logger.Errorf("%+v", err)
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
		log.Logger.Errorf("%+v", err)
		return nil, err
	}
	if err := clientInfo.EnsureDocumentAttached(docInfo.ID); err != nil {
		log.Logger.Errorf("%+v", err)
		return nil, err
	}

	pulled, err := packs.PushPull(ctx, s.backend, clientInfo, docInfo, pack)
	if err != nil {
		log.Logger.Errorf("%+v", err)
		return nil, err
	}

	pbChangePack, err := converter.ToChangePack(pulled)
	if err != nil {
		log.Logger.Errorf("%+v", err)
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
func (s *Server) WatchDocuments(
	req *api.WatchDocumentsRequest,
	stream api.Yorkie_WatchDocumentsServer,
) error {
	client, err := converter.FromClient(req.Client)
	if err != nil {
		log.Logger.Errorf("%+v", err)
		return err
	}
	docKeys := converter.FromDocumentKeys(req.DocumentKeys)

	subscription, peersMap, err := s.watchDocs(
		stream.Context(),
		*client,
		docKeys,
	)
	if err != nil {
		log.Logger.Errorf("%+v", err)
		return err
	}

	if err := stream.Send(&api.WatchDocumentsResponse{
		Body: &api.WatchDocumentsResponse_Initialization_{
			Initialization: &api.WatchDocumentsResponse_Initialization{
				PeersMapByDoc: converter.ToClientsMap(peersMap),
			},
		},
	}); err != nil {
		log.Logger.Errorf("%+v", err)
		s.unwatchDocs(docKeys, subscription)
		return err
	}

	for {
		select {
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
				log.Logger.Errorf("%+v", err)
				s.unwatchDocs(docKeys, subscription)
				return err
			}
		}
	}
}

func (s *Server) listenAndServeGRPC() error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.conf.Port))
	if err != nil {
		return errors.WithStack(err)
	}

	go func() {
		log.Logger.Infof("serving API on %d", s.conf.Port)

		if err := s.grpcServer.Serve(lis); err != nil {
			if err != grpc.ErrServerStopped {
				log.Logger.Error(err)
			}
		}
	}()

	return nil
}

func (s *Server) watchDocs(
	ctx context.Context,
	client types.Client,
	docKeys []*key.Key,
) (*sync.Subscription, map[string][]types.Client, error) {
	subscription, peersMap, err := s.backend.Coordinator.Subscribe(
		client,
		docKeys,
	)
	if err != nil {
		log.Logger.Errorf("%+v", err)
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

func (s *Server) unwatchDocs(
	docKeys []*key.Key,
	subscription *sync.Subscription,
) {
	s.backend.Coordinator.Unsubscribe(docKeys, subscription)

	s.backend.Coordinator.Publish(
		context.Background(),
		subscription.Subscriber().ID,
		sync.DocEvent{
			Type:         types.DocumentsUnwatchedEvent,
			Publisher:    subscription.Subscriber(),
			DocumentKeys: docKeys,
		},
	)
}

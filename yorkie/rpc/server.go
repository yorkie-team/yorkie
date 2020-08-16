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

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	"github.com/yorkie-team/yorkie/api"
	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/log"
	pkgTypes "github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/yorkie/backend"
	"github.com/yorkie-team/yorkie/yorkie/backend/mongo"
	"github.com/yorkie-team/yorkie/yorkie/clients"
	"github.com/yorkie-team/yorkie/yorkie/packs"
	"github.com/yorkie-team/yorkie/yorkie/pubsub"
	"github.com/yorkie-team/yorkie/yorkie/types"
)

type fieldViolation struct {
	field       string
	description string
}

type Config struct {
	Port     int
	CertFile string
	KeyFile  string
}

type Server struct {
	conf       *Config
	grpcServer *grpc.Server
	backend    *backend.Backend
}

// NewServer creates a new instance of Server.
func NewServer(conf *Config, be *backend.Backend) (*Server, error) {
	opts := []grpc.ServerOption{
		grpc.UnaryInterceptor(unaryInterceptor),
		grpc.StreamInterceptor(streamInterceptor),
	}

	if conf.CertFile != "" && conf.KeyFile != "" {
		creds, err := credentials.NewServerTLSFromFile(conf.CertFile, conf.KeyFile)
		if err != nil {
			log.Logger.Error(err)
			return nil, err
		}
		opts = append(opts, grpc.Creds(creds))
	}

	rpcServer := &Server{
		conf:       conf,
		grpcServer: grpc.NewServer(opts...),
		backend:    be,
	}
	api.RegisterYorkieServer(rpcServer.grpcServer, rpcServer)

	return rpcServer, nil
}

func (s *Server) Start() error {
	return s.listenAndServeGRPC()
}

func (s *Server) Shutdown(graceful bool) {
	if graceful {
		s.grpcServer.GracefulStop()
	} else {
		s.grpcServer.Stop()
	}
}

func (s *Server) ActivateClient(
	ctx context.Context,
	req *api.ActivateClientRequest,
) (*api.ActivateClientResponse, error) {
	if req.ClientKey == "" {
		return nil, toStatusError(
			codes.InvalidArgument,
			"invalid client key",
			[]fieldViolation{{
				field:       "client_key",
				description: "the client key must not be empty",
			}},
		)
	}
	client, err := clients.Activate(ctx, s.backend, req.ClientKey)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &api.ActivateClientResponse{
		ClientKey: client.Key,
		ClientId:  client.ID.Hex(),
	}, nil
}

func (s *Server) DeactivateClient(
	ctx context.Context,
	req *api.DeactivateClientRequest,
) (*api.DeactivateClientResponse, error) {
	if req.ClientId == "" {
		return nil, toStatusError(
			codes.InvalidArgument,
			"invalid client ID",
			[]fieldViolation{{
				field:       "client_id",
				description: "the client ID must not be empty",
			}},
		)
	}

	client, err := clients.Deactivate(ctx, s.backend, req.ClientId)
	if err != nil {
		if err == mongo.ErrClientNotFound {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &api.DeactivateClientResponse{
		ClientId: client.ID.Hex(),
	}, nil
}

func (s *Server) AttachDocument(
	ctx context.Context,
	req *api.AttachDocumentRequest,
) (*api.AttachDocumentResponse, error) {
	pack, err := converter.FromChangePack(req.ChangePack)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// if pack.HasChanges() {
	lockKey := fmt.Sprintf("pushpull-%s", pack.DocumentKey.BSONKey())
	if err := s.backend.MutexMap.Lock(lockKey); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	defer func() {
		if err := s.backend.MutexMap.Unlock(lockKey); err != nil {
			log.Logger.Error(err)
		}
	}()
	// }

	clientInfo, docInfo, err := clients.FindClientAndDocument(ctx, s.backend, req.ClientId, pack, true)
	if err != nil {
		if err == mongo.ErrClientNotFound || err == mongo.ErrDocumentNotFound {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := clientInfo.AttachDocument(docInfo.ID); err != nil {
		if err == types.ErrClientNotActivated {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		if err == types.ErrDocumentAlreadyAttached {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	pulled, err := packs.PushPull(ctx, s.backend, clientInfo, docInfo, pack)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &api.AttachDocumentResponse{
		ChangePack: converter.ToChangePack(pulled),
	}, nil
}

func (s *Server) DetachDocument(
	ctx context.Context,
	req *api.DetachDocumentRequest,
) (*api.DetachDocumentResponse, error) {
	pack, err := converter.FromChangePack(req.ChangePack)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// if pack.HasChanges() {
	lockKey := fmt.Sprintf("pushpull-%s", pack.DocumentKey.BSONKey())
	if err := s.backend.MutexMap.Lock(lockKey); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	defer func() {
		if err := s.backend.MutexMap.Unlock(lockKey); err != nil {
			log.Logger.Error(err)
		}
	}()
	// }

	clientInfo, docInfo, err := clients.FindClientAndDocument(ctx, s.backend, req.ClientId, pack, false)
	if err != nil {
		if err == mongo.ErrClientNotFound || err == mongo.ErrDocumentNotFound {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := clientInfo.CheckDocumentAttached(docInfo.ID.Hex()); err != nil {
		if err == types.ErrClientNotActivated || err == types.ErrDocumentNotAttached {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := clientInfo.DetachDocument(docInfo.ID); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	pulled, err := packs.PushPull(ctx, s.backend, clientInfo, docInfo, pack)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &api.DetachDocumentResponse{
		ChangePack: converter.ToChangePack(pulled),
	}, nil
}

func (s *Server) PushPull(
	ctx context.Context,
	req *api.PushPullRequest,
) (*api.PushPullResponse, error) {
	pack, err := converter.FromChangePack(req.ChangePack)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// TODO uncomment write lock condition. We need $max operation on client.
	// if pack.HasChanges() {
	lockKey := fmt.Sprintf("pushpull-%s", pack.DocumentKey.BSONKey())
	if err := s.backend.MutexMap.Lock(lockKey); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	defer func() {
		if err := s.backend.MutexMap.Unlock(lockKey); err != nil {
			log.Logger.Error(err)
		}
	}()
	// }

	clientInfo, docInfo, err := clients.FindClientAndDocument(ctx, s.backend, req.ClientId, pack, false)
	if err != nil {
		if err == mongo.ErrClientNotFound || err == mongo.ErrDocumentNotFound {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := clientInfo.CheckDocumentAttached(docInfo.ID.Hex()); err != nil {
		if err == types.ErrClientNotActivated || err == types.ErrDocumentNotAttached {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	pulled, err := packs.PushPull(ctx, s.backend, clientInfo, docInfo, pack)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &api.PushPullResponse{
		ChangePack: converter.ToChangePack(pulled),
	}, nil
}

func (s *Server) WatchDocuments(
	req *api.WatchDocumentsRequest,
	stream api.Yorkie_WatchDocumentsServer,
) error {
	var docKeys []string
	for _, docKey := range converter.FromDocumentKeys(req.DocumentKeys) {
		docKeys = append(docKeys, docKey.BSONKey())
	}

	subscription, peersMap, err := s.watchDocs(req.ClientId, docKeys)
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
		case <-stream.Context().Done():
			s.unwatchDocs(docKeys, subscription)
			return nil
		case event := <-subscription.Events():
			k, err := key.FromBSONKey(event.DocKey)
			if err != nil {
				log.Logger.Error(err)
				s.unwatchDocs(docKeys, subscription)
				return err
			}

			if err := stream.Send(&api.WatchDocumentsResponse{
				Body: &api.WatchDocumentsResponse_Event_{
					Event: &api.WatchDocumentsResponse_Event{
						ClientId:     event.Publisher.String(),
						EventType:    converter.ToEventType(event.Type),
						DocumentKeys: converter.ToDocumentKeys(k),
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

func (s *Server) listenAndServeGRPC() error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.conf.Port))
	if err != nil {
		log.Logger.Error(err)
		return err
	}

	go func() {
		log.Logger.Infof("serving API on %d", s.conf.Port)

		if err := s.grpcServer.Serve(lis); err != nil {
			log.Logger.Error(err)
		}
	}()

	return nil
}

func (s *Server) watchDocs(
	clientID string,
	docKeys []string,
) (*pubsub.Subscription, map[string][]string, error) {
	subscription, peersMap, err := s.backend.PubSub.Subscribe(
		time.ActorIDFromHex(clientID),
		docKeys,
	)
	if err != nil {
		log.Logger.Error(err)
		return nil, nil, err
	}

	for _, docKey := range docKeys {
		s.backend.PubSub.Publish(
			subscription.Subscriber(),
			docKey,
			pubsub.DocEvent{
				Type:      pkgTypes.DocumentsWatchedEvent,
				DocKey:    docKey,
				Publisher: subscription.Subscriber(),
			},
		)
	}

	return subscription, peersMap, nil
}

func (s *Server) unwatchDocs(docKeys []string, subscription *pubsub.Subscription) {
	s.backend.PubSub.Unsubscribe(docKeys, subscription)

	for _, docKey := range docKeys {
		s.backend.PubSub.Publish(
			subscription.Subscriber(),
			docKey,
			pubsub.DocEvent{
				Type:      pkgTypes.DocumentsUnwatchedEvent,
				DocKey:    docKey,
				Publisher: subscription.Subscriber(),
			},
		)
	}
}

func toStatusError(code codes.Code, msg string, violations []fieldViolation) error {
	br := &errdetails.BadRequest{}

	for _, violation := range violations {
		br.FieldViolations = append(br.FieldViolations, &errdetails.BadRequest_FieldViolation{
			Field:       violation.field,
			Description: violation.description,
		})
	}

	st, err := status.New(code, msg).WithDetails(br)
	if err != nil {
		// If this errored, it will always error/ here, so better panic so we can figure
		// out why than have this silently passing.
		panic(fmt.Sprintf("unexpected error attaching metadata: %violation", err))
	}
	return st.Err()
}

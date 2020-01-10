package api

import (
	"context"
	"fmt"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hackerwins/yorkie/api"
	"github.com/hackerwins/yorkie/api/converter"
	"github.com/hackerwins/yorkie/pkg/document/key"
	"github.com/hackerwins/yorkie/pkg/document/time"
	"github.com/hackerwins/yorkie/pkg/log"
	"github.com/hackerwins/yorkie/yorkie/backend"
	"github.com/hackerwins/yorkie/yorkie/clients"
	"github.com/hackerwins/yorkie/yorkie/packs"
	"github.com/hackerwins/yorkie/yorkie/pubsub"
)

type RPCServer struct {
	port       int
	grpcServer *grpc.Server
	backend    *backend.Backend
}

func NewRPCServer(port int, be *backend.Backend) (*RPCServer, error) {
	opts := []grpc.ServerOption{
		grpc.UnaryInterceptor(unaryInterceptor),
		grpc.StreamInterceptor(streamInterceptor),
	}

	rpcServer := &RPCServer{
		port:       port,
		grpcServer: grpc.NewServer(opts...),
		backend:    be,
	}
	api.RegisterYorkieServer(rpcServer.grpcServer, rpcServer)

	return rpcServer, nil
}

func (s *RPCServer) Start() error {
	return s.listenAndServeGRPC()
}

func (s *RPCServer) Shutdown(graceful bool) {
	if graceful {
		s.grpcServer.GracefulStop()
	} else {
		s.grpcServer.Stop()
	}
}

func (s *RPCServer) ActivateClient(
	ctx context.Context,
	req *api.ActivateClientRequest,
) (*api.ActivateClientResponse, error) {
	client, err := clients.Activate(ctx, s.backend, req.ClientKey)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &api.ActivateClientResponse{
		ClientKey: client.Key,
		ClientId:  client.ID.Hex(),
	}, nil
}

func (s *RPCServer) DeactivateClient(
	ctx context.Context,
	req *api.DeactivateClientRequest,
) (*api.DeactivateClientResponse, error) {
	client, err := clients.Deactivate(ctx, s.backend, req.ClientId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &api.DeactivateClientResponse{
		ClientId: client.ID.Hex(),
	}, nil
}

func (s *RPCServer) AttachDocument(
	ctx context.Context,
	req *api.AttachDocumentRequest,
) (*api.AttachDocumentResponse, error) {
	pack, err := converter.FromChangePack(req.ChangePack)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// if pack.HasChanges() {
		if err := s.backend.Lock(pack.DocumentKey.BSONKey()); err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		defer func() {
			if err := s.backend.Unlock(pack.DocumentKey.BSONKey()); err != nil {
				log.Logger.Error(err)
			}
		}()
	// }

	clientInfo, docInfo, err := clients.FindClientAndDocument(ctx, s.backend, req.ClientId, pack)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := clientInfo.AttachDocument(docInfo.ID, pack.Checkpoint); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	pulled, err := packs.PushPull(ctx, s.backend, clientInfo, docInfo, pack)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	if pack.HasChanges() {
		s.backend.Publish(
			time.ActorIDFromHex(req.ClientId),
			pack.DocumentKey.BSONKey(),
			pubsub.Event{Value: pack.DocumentKey.BSONKey()},
		)
	}

	return &api.AttachDocumentResponse{
		ChangePack: converter.ToChangePack(pulled),
	}, nil
}

func (s *RPCServer) DetachDocument(
	ctx context.Context,
	req *api.DetachDocumentRequest,
) (*api.DetachDocumentResponse, error) {
	pack, err := converter.FromChangePack(req.ChangePack)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// if pack.HasChanges() {
		if err := s.backend.Lock(pack.DocumentKey.BSONKey()); err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		defer func() {
			if err := s.backend.Unlock(pack.DocumentKey.BSONKey()); err != nil {
				log.Logger.Error(err)
			}
		}()
	// }

	clientInfo, docInfo, err := clients.FindClientAndDocument(ctx, s.backend, req.ClientId, pack)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := clientInfo.CheckDocumentAttached(docInfo.ID.Hex()); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	pulled, err := packs.PushPull(ctx, s.backend, clientInfo, docInfo, pack)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	if pack.HasChanges() {
		s.backend.Publish(
			time.ActorIDFromHex(req.ClientId),
			pack.DocumentKey.BSONKey(),
			pubsub.Event{Value: pack.DocumentKey.BSONKey()},
		)
	}

	return &api.DetachDocumentResponse{
		ChangePack: converter.ToChangePack(pulled),
	}, nil
}

func (s *RPCServer) PushPull(
	ctx context.Context,
	req *api.PushPullRequest,
) (*api.PushPullResponse, error) {
	pack, err := converter.FromChangePack(req.ChangePack)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// TODO uncomment write lock condition. We need $max operation on client.
	// if pack.HasChanges() {
		if err := s.backend.Lock(pack.DocumentKey.BSONKey()); err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		defer func() {
			if err := s.backend.Unlock(pack.DocumentKey.BSONKey()); err != nil {
				log.Logger.Error(err)
			}
		}()
	// }

	clientInfo, docInfo, err := clients.FindClientAndDocument(ctx, s.backend, req.ClientId, pack)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := clientInfo.CheckDocumentAttached(docInfo.ID.Hex()); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	pulled, err := packs.PushPull(ctx, s.backend, clientInfo, docInfo, pack)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	if pack.HasChanges() {
		s.backend.Publish(
			time.ActorIDFromHex(req.ClientId),
			pack.DocumentKey.BSONKey(),
			pubsub.Event{Value: pack.DocumentKey.BSONKey()},
		)
	}

	return &api.PushPullResponse{
		ChangePack: converter.ToChangePack(pulled),
	}, nil
}

func (s *RPCServer) listenAndServeGRPC() error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		log.Logger.Error(err)
		return err
	}

	go func() {
		log.Logger.Infof("serving API on %d", s.port)

		if err := s.grpcServer.Serve(lis); err != nil {
			log.Logger.Error(err)
		}
	}()

	return nil
}

func (s *RPCServer) WatchDocuments(
	req *api.WatchDocumentsRequest,
	stream api.Yorkie_WatchDocumentsServer,
) error {
	var docKeys []string
	for _, docKey := range converter.FromDocumentKeys(req.DocumentKeys) {
		docKeys = append(docKeys, docKey.BSONKey())
	}

	subscription, err := s.backend.Subscribe(
		time.ActorIDFromHex(req.ClientId),
		docKeys,
	)
	if err != nil {
		log.Logger.Error(err)
		return err
	}

	for {
		select {
		case <-stream.Context().Done():
			s.backend.Unsubscribe(docKeys, subscription)
			return nil
		case event := <-subscription.Events():
			k, err := key.FromBSONKey(event.Value)
			if err != nil {
				s.backend.Unsubscribe(docKeys, subscription)
				log.Logger.Error(err)
				return err
			}

			if err := stream.Send(&api.WatchDocumentsResponse{
				ClientId:     req.ClientId,
				DocumentKeys: converter.ToDocumentKeys(k),
			}); err != nil {
				s.backend.Unsubscribe(docKeys, subscription)
				log.Logger.Error(err)
				return err
			}
		}
	}
}

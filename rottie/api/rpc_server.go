package api

import (
	"context"
	"fmt"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hackerwins/rottie/api"
	"github.com/hackerwins/rottie/api/converter"
	"github.com/hackerwins/rottie/pkg/log"
	"github.com/hackerwins/rottie/rottie/backend"
	"github.com/hackerwins/rottie/rottie/clients"
	"github.com/hackerwins/rottie/rottie/packs"
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
	api.RegisterRottieServer(rpcServer.grpcServer, rpcServer)

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
	pack := converter.FromChangePack(req.ChangePack)
	clientInfo, docInfo, err := clients.AttachDocument(ctx, s.backend, req.ClientId, pack)
	if err != nil {
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

func (s *RPCServer) DetachDocument(
	ctx context.Context,
	req *api.DetachDocumentRequest,
) (*api.DetachDocumentResponse, error) {
	pack := converter.FromChangePack(req.ChangePack)
	clientInfo, docInfo, err := clients.DetachDocument(ctx, s.backend, req.ClientId, pack)
	if err != nil {
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

func (s *RPCServer) PushPull(
	ctx context.Context,
	req *api.PushPullRequest,
) (*api.PushPullResponse, error) {
	pack := converter.FromChangePack(req.ChangePack)
	clientInfo, docInfo, err := clients.PushPullDocument(ctx, s.backend, req.ClientId, pack)
	if err != nil {
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

package api

import (
	"context"
	"fmt"
	"net"

	"google.golang.org/grpc"

	"github.com/hackerwins/rottie/api"
	"github.com/hackerwins/rottie/pkg/log"
)

const rpcPort = 1101

type RPCServer struct {
	grpcServer *grpc.Server
}

func NewRPCServer() (*RPCServer, error) {
	opts := []grpc.ServerOption{
		grpc.UnaryInterceptor(unaryInterceptor),
		grpc.StreamInterceptor(streamInterceptor),
	}

	rpcServer := &RPCServer{
		grpcServer: grpc.NewServer(opts...),
	}
	api.RegisterRottieServer(rpcServer.grpcServer, rpcServer)

	return rpcServer, nil
}

func (s *RPCServer) Start() error {
	return s.listenAndServeGRPC()
}

func (s *RPCServer) Hello(
	ctx context.Context,
	req *api.HelloRequest,
) (*api.HelloResponse, error) {
	log.Logger.Info(req.One)

	return &api.HelloResponse{}, nil
}

func (s *RPCServer) listenAndServeGRPC() error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", +rpcPort))
	if err != nil {
		log.Logger.Error(err)
		return err
	}

	go func() {
		log.Logger.Infof("serving API on %d", rpcPort)

		if err := s.grpcServer.Serve(lis); err != nil {
			log.Logger.Error(err)
		}
	}()

	return nil
}

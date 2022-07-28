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
	"github.com/yorkie-team/yorkie/gen/go/yorkie/v1"
	"math"
	"net"

	grpcmiddleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/grpchelper"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/rpc/interceptors"
)

// Server is a normal server that processes the logic requested by the client.
type Server struct {
	conf                *Config
	grpcServer          *grpc.Server
	yorkieServiceCancel context.CancelFunc
}

// NewServer creates a new instance of Server.
func NewServer(conf *Config, be *backend.Backend) (*Server, error) {
	loggingInterceptor := grpchelper.NewLoggingInterceptor()
	contextInterceptor := interceptors.NewContextInterceptor(be)
	defaultInterceptor := interceptors.NewDefaultInterceptor()

	opts := []grpc.ServerOption{
		grpc.UnaryInterceptor(grpcmiddleware.ChainUnaryServer(
			loggingInterceptor.Unary(),
			be.Metrics.ServerMetrics().UnaryServerInterceptor(),
			contextInterceptor.Unary(),
			defaultInterceptor.Unary(),
		)),
		grpc.StreamInterceptor(grpcmiddleware.ChainStreamServer(
			loggingInterceptor.Stream(),
			be.Metrics.ServerMetrics().StreamServerInterceptor(),
			contextInterceptor.Stream(),
			defaultInterceptor.Stream(),
		)),
	}

	if conf.CertFile != "" && conf.KeyFile != "" {
		creds, err := credentials.NewServerTLSFromFile(conf.CertFile, conf.KeyFile)
		if err != nil {
			logging.DefaultLogger().Error(err)
			return nil, err
		}
		opts = append(opts, grpc.Creds(creds))
	}

	opts = append(opts, grpc.MaxRecvMsgSize(int(conf.MaxRequestBytes)))
	opts = append(opts, grpc.MaxSendMsgSize(math.MaxInt32))
	opts = append(opts, grpc.MaxConcurrentStreams(math.MaxUint32))

	yorkieServiceCtx, yorkieServiceCancel := context.WithCancel(context.Background())

	grpcServer := grpc.NewServer(opts...)
	healthpb.RegisterHealthServer(grpcServer, health.NewServer())
	v1.RegisterYorkieServer(grpcServer, newYorkieServer(yorkieServiceCtx, be))
	be.Metrics.RegisterGRPCServer(grpcServer)

	return &Server{
		conf:                conf,
		grpcServer:          grpcServer,
		yorkieServiceCancel: yorkieServiceCancel,
	}, nil
}

// Start starts this server by opening the rpc port.
func (s *Server) Start() error {
	return s.listenAndServeGRPC()
}

// Shutdown shuts down this server.
func (s *Server) Shutdown(graceful bool) {
	s.yorkieServiceCancel()

	if graceful {
		s.grpcServer.GracefulStop()
	} else {
		s.grpcServer.Stop()
	}
}

func (s *Server) listenAndServeGRPC() error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.conf.Port))
	if err != nil {
		logging.DefaultLogger().Error(err)
		return err
	}

	go func() {
		logging.DefaultLogger().Infof("serving RPC on %d", s.conf.Port)

		if err := s.grpcServer.Serve(lis); err != nil {
			if err != grpc.ErrServerStopped {
				logging.DefaultLogger().Error(err)
			}
		}
	}()

	return nil
}

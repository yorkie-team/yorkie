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

// Package rpc provides the rpc server which is responsible for handling
// requests from the client.
package rpc

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/grpchealth"
	"github.com/rs/cors"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/rpc/auth"
	"github.com/yorkie-team/yorkie/server/rpc/connecthelper"
	"github.com/yorkie-team/yorkie/server/rpc/interceptors"
)

// Server is a normal server that processes the logic requested by the client.
type Server struct {
	conf                *Config
	httpServer          *http.Server
	yorkieServiceCancel context.CancelFunc
	tokenManager        *auth.TokenManager
}

// NewServer creates a new instance of Server.
func NewServer(conf *Config, be *backend.Backend) (*Server, error) {
	tokenManager := auth.NewTokenManager(
		be.Config.SecretKey,
		be.Config.ParseAdminTokenDuration(),
	)

	//loggingInterceptor := grpchelper.NewLoggingInterceptor()
	//adminAuthInterceptor := interceptors.NewAdminAuthInterceptor(be, tokenManager)
	//defaultInterceptor := interceptors.NewDefaultInterceptor()

	//opts := []grpc.ServerOption{
	//	grpc.UnaryInterceptor(grpcmiddleware.ChainUnaryServer(
	//		loggingInterceptor.Unary(),
	//		be.Metrics.ServerMetrics().UnaryServerInterceptor(),
	//		adminAuthInterceptor.Unary(),
	//		contextInterceptor.Unary(),
	//		defaultInterceptor.Unary(),
	//	)),
	//	grpc.StreamInterceptor(grpcmiddleware.ChainStreamServer(
	//		loggingInterceptor.Stream(),
	//		be.Metrics.ServerMetrics().StreamServerInterceptor(),
	//		adminAuthInterceptor.Stream(),
	//		contextInterceptor.Stream(),
	//		defaultInterceptor.Stream(),
	//	)),
	//}

	interceptor := connect.WithInterceptors(
		connecthelper.NewLoggingInterceptor(),
		// TODO(krapie): update prometehus metrics server to http server
		// be.Metrics.ServerMetrics()
		interceptors.NewAdminAuthInterceptor(be, tokenManager),
		interceptors.NewContextInterceptor(be),
		interceptors.NewDefaultInterceptor(),
	)

	//if conf.CertFile != "" && conf.KeyFile != "" {
	//	creds, err := credentials.NewServerTLSFromFile(conf.CertFile, conf.KeyFile)
	//	if err != nil {
	//		return nil, fmt.Errorf("load TLS cert: %w", err)
	//	}
	//	opts = append(opts, grpc.Creds(creds))
	//}
	//
	//maxConnectionAge, err := time.ParseDuration(conf.MaxConnectionAge)
	//if err != nil {
	//	return nil, fmt.Errorf("parse max connection age: %w", err)
	//}
	//
	//maxConnectionAgeGrace, err := time.ParseDuration(conf.MaxConnectionAgeGrace)
	//if err != nil {
	//	return nil, fmt.Errorf("parse max connection age grace: %w", err)
	//}
	//
	//opts = append(opts, grpc.MaxRecvMsgSize(int(conf.MaxRequestBytes)))
	//opts = append(opts, grpc.MaxSendMsgSize(math.MaxInt32))
	//opts = append(opts, grpc.MaxConcurrentStreams(math.MaxUint32))
	//opts = append(opts, grpc.KeepaliveParams(keepalive.ServerParameters{
	//	MaxConnectionAge:      maxConnectionAge,
	//	MaxConnectionAgeGrace: maxConnectionAgeGrace,
	//}))

	yorkieServiceCtx, yorkieServiceCancel := context.WithCancel(context.Background())

	mux := http.NewServeMux()
	mux.Handle(v1connect.NewYorkieServiceHandler(
		newYorkieServer(yorkieServiceCtx, be),
		interceptor,
	))
	mux.Handle(v1connect.NewAdminServiceHandler(
		newAdminServer(be, tokenManager),
		interceptor,
	))

	checker := grpchealth.NewStaticChecker(
		grpchealth.HealthV1ServiceName,
		v1connect.YorkieServiceName,
		v1connect.AdminServiceName,
	)
	mux.Handle(grpchealth.NewHandler(checker))

	httpServer := &http.Server{
		Addr: fmt.Sprintf(":%d", conf.Port),
		Handler: h2c.NewHandler(
			newCORS().Handler(mux),
			&http2.Server{},
		),
	}

	//grpcServer := grpc.NewServer(opts...)
	//healthpb.RegisterHealthServer(grpcServer, health.NewServer())
	//api.RegisterYorkieServiceServer(grpcServer, newYorkieServer(yorkieServiceCtx, be))
	//api.RegisterAdminServiceServer(grpcServer, newAdminServer(be, tokenManager))
	//be.Metrics.RegisterGRPCServer(grpcServer)

	return &Server{
		conf:                conf,
		httpServer:          httpServer,
		yorkieServiceCancel: yorkieServiceCancel,
	}, nil
}

// Start starts this server by opening the rpc port.
func (s *Server) Start() error {
	return s.listenAndServe()
}

func (s *Server) listenAndServe() error {
	go func() {
		logging.DefaultLogger().Infof(fmt.Sprintf("serving rpc on %d", s.conf.Port))
		if err := s.httpServer.ListenAndServe(); err != http.ErrServerClosed {
			logging.DefaultLogger().Errorf("HTTP server ListenAndServe: %v", err)
		}
	}()
	return nil
}

// Shutdown shuts down this server.
func (s *Server) Shutdown(graceful bool) {
	s.yorkieServiceCancel()

	if graceful {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err := s.httpServer.Shutdown(ctx)
		if err != nil {
			return
		}
	} else {
		// TODO(krapie): find a way to shutdown http server immediately
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		err := s.httpServer.Shutdown(ctx)
		if err != nil {
			return
		}
	}
}

//func (s *Server) listenAndServeGRPC() error {
//	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.conf.Port))
//	if err != nil {
//		return fmt.Errorf("listen port %d: %w", s.conf.Port, err)
//	}
//
//	go func() {
//		logging.DefaultLogger().Infof("serving RPC on %d", s.conf.Port)
//
//		if err := s.grpcServer.Serve(lis); err != nil {
//			if err != grpc.ErrServerStopped {
//				logging.DefaultLogger().Error(err)
//			}
//		}
//	}()
//
//	return nil
//}

func newCORS() *cors.Cors {
	// To let web developers play with the demo service from browsers, we need a
	// very permissive CORS setup.
	return cors.New(cors.Options{
		AllowedMethods: []string{
			http.MethodHead,
			http.MethodGet,
			http.MethodPost,
			http.MethodPut,
			http.MethodPatch,
			http.MethodDelete,
		},
		AllowOriginFunc: func(origin string) bool {
			// Allow all origins, which effectively disables CORS.
			return true
		},
		AllowedHeaders: []string{"*"},
		ExposedHeaders: []string{
			// Content-Type is in the default safelist.
			"Accept",
			"Accept-Encoding",
			"Accept-Post",
			"Connect-Accept-Encoding",
			"Connect-Content-Encoding",
			"Content-Encoding",
			"Grpc-Accept-Encoding",
			"Grpc-Encoding",
			"Grpc-Message",
			"Grpc-Status",
			"Grpc-Status-Details-Bin",
			"X-Custom-Header",
			"Connect-Protocol-Version",
		},
		// Let browsers cache CORS information for longer, which reduces the number
		// of preflight requests. Any changes to ExposedHeaders won't take effect
		// until the cached data expires. FF caps this value at 24h, and modern
		// Chrome caps it at 2h.
		MaxAge: int(2 * time.Hour / time.Second),
	})
}

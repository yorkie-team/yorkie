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
	serverMux           *http.ServeMux
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

	interceptor := connect.WithInterceptors(
		connecthelper.NewLoggingInterceptor(),
		interceptors.NewAdminAuthInterceptor(be, tokenManager),
		interceptors.NewContextInterceptor(be),
		interceptors.NewDefaultInterceptor(),
	)

	// TODO(krapie): find corresponding http/net server configurations that matches with gRPC server options

	yorkieServiceCtx, yorkieServiceCancel := context.WithCancel(context.Background())

	serverMux := http.NewServeMux()
	serverMux.Handle(v1connect.NewYorkieServiceHandler(
		newYorkieServer(yorkieServiceCtx, be),
		interceptor,
	))
	serverMux.Handle(v1connect.NewAdminServiceHandler(
		newAdminServer(be, tokenManager),
		interceptor,
	))

	checker := grpchealth.NewStaticChecker(
		grpchealth.HealthV1ServiceName,
		v1connect.YorkieServiceName,
		v1connect.AdminServiceName,
	)
	serverMux.Handle(grpchealth.NewHandler(checker))

	return &Server{
		conf:                conf,
		serverMux:           serverMux,
		httpServer:          &http.Server{Addr: fmt.Sprintf(":%d", conf.Port)},
		yorkieServiceCancel: yorkieServiceCancel,
	}, nil
}

// Start starts this server by opening the rpc port.
func (s *Server) Start() error {
	return s.listenAndServe()
}

// Shutdown shuts down this server.
func (s *Server) Shutdown(graceful bool) {
	s.yorkieServiceCancel()

	if graceful {
		if err := s.httpServer.Shutdown(context.Background()); err != nil {
			logging.DefaultLogger().Error("HTTP server Shutdown: %v", err)
		}
		return
	}

	if err := s.httpServer.Close(); err != nil {
		logging.DefaultLogger().Error("HTTP server close: %v", err)
	}
}

func (s *Server) listenAndServe() error {
	go func() {
		logging.DefaultLogger().Infof(fmt.Sprintf("serving RPC on %d", s.conf.Port))
		s.httpServer.Handler = h2c.NewHandler(
			newCORS().Handler(s.serverMux),
			&http2.Server{},
		)
		if s.conf.CertFile != "" && s.conf.KeyFile != "" {
			if err := s.httpServer.ListenAndServeTLS(s.conf.CertFile, s.conf.KeyFile); err != http.ErrServerClosed {
				logging.DefaultLogger().Errorf("HTTP server ListenAndServeTLS: %v", err)
			}
			return
		}
		if err := s.httpServer.ListenAndServe(); err != http.ErrServerClosed {
			logging.DefaultLogger().Errorf("HTTP server ListenAndServe: %v", err)
		}
		return
	}()
	return nil
}

func newCORS() *cors.Cors {
	return cors.New(cors.Options{
		AllowedMethods: []string{
			http.MethodOptions,
			http.MethodGet,
			http.MethodPost,
			http.MethodPut,
			http.MethodDelete,
		},
		AllowOriginFunc: func(origin string) bool {
			return true
		},
		AllowedHeaders: []string{
			"Grpc-Timeout",
			"Content-Type",
			"Keep-Alive",
			"User-Agent",
			"Cache-Control",
			"Content-Type",
			"Content-Transfer-Encoding",
			"Custom-Header-1",
			"Connect-Protocol-Version",
			"X-Accept-Content-Transfer-Encoding",
			"X-Accept-Response-Streaming",
			"X-User-Agent",
			"X-Yorkie-User-Agent",
			"X-Grpc-Web",
			"Authorization",
			"X-API-Key",
			"X-Shard-Key",
		},
		MaxAge: int(1728 * time.Second),
		ExposedHeaders: []string{
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
			"Custom-Header-1",
		},
		AllowCredentials: true,
	})
}

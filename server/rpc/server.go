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
	"errors"
	"fmt"
	"math"
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
	"github.com/yorkie-team/yorkie/server/rpc/httphealth"
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

	opts := []connect.HandlerOption{
		connect.WithInterceptors(
			interceptors.NewAdminServiceInterceptor(be, tokenManager),
			interceptors.NewYorkieServiceInterceptor(be),
			interceptors.NewDefaultInterceptor(),
		),
	}

	healthChecker := grpchealth.NewStaticChecker(
		grpchealth.HealthV1ServiceName,
		v1connect.YorkieServiceName,
		v1connect.AdminServiceName,
	)

	yorkieServiceCtx, yorkieServiceCancel := context.WithCancel(context.Background())
	mux := http.NewServeMux()
	mux.Handle(v1connect.NewYorkieServiceHandler(newYorkieServer(yorkieServiceCtx, be), opts...))
	mux.Handle(v1connect.NewAdminServiceHandler(newAdminServer(be, tokenManager), opts...))
	mux.Handle(grpchealth.NewHandler(healthChecker))
	mux.Handle(httphealth.NewHandler(healthChecker))
	// TODO(hackerwins): We need to provide proper http server configuration.
	return &Server{
		conf: conf,
		httpServer: &http.Server{
			Addr: fmt.Sprintf(":%d", conf.Port),
			Handler: h2c.NewHandler(newCORS().Handler(mux),
				&http2.Server{
					MaxConcurrentStreams: math.MaxUint32,
				},
			),
		},
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

		if s.conf.CertFile != "" && s.conf.KeyFile != "" {
			if err := s.httpServer.ListenAndServeTLS(s.conf.CertFile, s.conf.KeyFile); !errors.Is(err, http.ErrServerClosed) {
				logging.DefaultLogger().Errorf("HTTP server ListenAndServeTLS: %v", err)
			}
			return
		}

		if err := s.httpServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
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
			// TODO(hackerwins): We need to provide a way to configure allow origins in the dashboard.
			return true
		},
		AllowedHeaders: []string{"*"},
		ExposedHeaders: []string{"*"},
		// MaxAge indicates how long (in seconds) the results of a preflight request
		// can be cached. FF caps this value at 24h, and modern Chrome caps it at 2h.
		MaxAge:           int(2 * time.Hour / time.Second),
		AllowCredentials: true,
	})
}

/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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

package prometheus

import (
	"context"
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/yorkie-team/yorkie/pkg/log"
)

// Config is the configuration for creating a Server instance.
type Config struct {
	Port int
}

// Server provides application-specific and Go metrics.
type Server struct {
	conf          *Config
	metrics       *Metrics
	metricsServer *http.Server
}

// NewServer creates an instance of Server.
func NewServer(conf *Config, met *Metrics) (*Server, error) {
	if conf == nil {
		return nil, nil
	}
	return &Server{
		conf:    conf,
		metrics: met,
		metricsServer: &http.Server{
			Addr: fmt.Sprintf(":%d", conf.Port),
		},
	}, nil
}

func (s *Server) listenAndServe() error {
	go func() {
		log.Logger.Infof(fmt.Sprintf("serving Metrics on %d", s.conf.Port))
		serveMux := http.NewServeMux()
		serveMux.Handle("/metrics", promhttp.HandlerFor(s.metrics.Registry(), promhttp.HandlerOpts{}))
		s.metricsServer.Handler = serveMux
		if err := s.metricsServer.ListenAndServe(); err != http.ErrServerClosed {
			log.Logger.Error("HTTP server ListenAndServe: %v", err)
		}
	}()
	return nil
}

// Start registers application-specific metrics and starts the HTTP server.
func (s *Server) Start() error {
	return s.listenAndServe()
}

// Shutdown closes the server.
func (s *Server) Shutdown(graceful bool) {
	if graceful {
		if err := s.metricsServer.Shutdown(context.Background()); err != nil {
			log.Logger.Error("HTTP server Shutdown: %v", err)
		}
		return
	}

	if err := s.metricsServer.Close(); err != nil {
		log.Logger.Error("HTTP server Close: %v", err)
	}
}

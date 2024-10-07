/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
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

package system

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/logging"
)

// DetachRequest is TODO.
type DetachRequest struct {
	DocID         string                 `json:"docID"`
	ClientInfo    database.ClientInfo    `json:"clientInfo"`
	ClientDocInfo database.ClientDocInfo `json:"clientDocInfo"`
	Project       types.Project          `json:"project"`
}

// Server is a server that processes the logic related to server-to-server requests.
type Server struct {
	conf       *Config
	be         *backend.Backend
	serveMux   *http.ServeMux
	httpServer *http.Server
	service    *systemService
}

// NewServer creates an instance of Server.
func NewServer(conf *Config, be *backend.Backend) *Server {
	serveMux := http.NewServeMux()

	ctx := context.Background()

	s := &Server{
		conf:       conf,
		be:         be,
		serveMux:   serveMux,
		httpServer: &http.Server{Addr: fmt.Sprintf(":%d", conf.Port)},
		service:    newSystemService(ctx, be),
	}

	serveMux.HandleFunc("/detach", s.detach)

	return s
}

// Handle is... do I need this?
func (s *Server) Handle(pattern string, handler http.Handler) {
	s.serveMux.Handle(pattern, handler)
}

// Start starts the server.
func (s *Server) Start() error {
	return s.listenAndServe()
}

func (s *Server) detach(w http.ResponseWriter, r *http.Request) {
	var req DetachRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		fmt.Println("JSON decode error:", err)
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	success, err := s.service.DetachDocument(req.DocID, req.ClientInfo, req.ClientDocInfo, req.Project)
	if err != nil {
		fmt.Println("Error occurs at system.DetachDocument:", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	_, err = fmt.Fprintln(w, success)
	if err != nil {
		return
	}

	//w.Header().Set("Content-Type", "application/json")
	//if err := json.NewEncoder(w).Encode(map[string]interface{}{"success": success}); err != nil {
	//	return
	//}
}

func (s *Server) listenAndServe() error {
	go func() {
		logging.DefaultLogger().Infof(fmt.Sprintf("serving system server on %d", s.conf.Port))
		s.httpServer.Handler = s.serveMux
		if err := s.httpServer.ListenAndServe(); err != http.ErrServerClosed {
			logging.DefaultLogger().Errorf("HTTP server ListenAndServe: %v", err)
		}
	}()
	return nil
}

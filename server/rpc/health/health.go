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

// Package health uses http GET to provide a health check for the server.
package health

import (
	"encoding/json"
	"net/http"

	"connectrpc.com/grpchealth"
)

// CheckResponse represents the response structure for health checks.
type CheckResponse struct {
	Status string `json:"status"`
}

// NewHTTPHealthCheckHandler creates a new HTTP handler for health checks.
func NewHTTPHealthCheckHandler(checker grpchealth.Checker) (string, http.Handler) {
	const serviceName = "/healthz/"
	check := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var checkRequest grpchealth.CheckRequest
		service := r.URL.Query().Get("service")
		if service != "" {
			checkRequest.Service = service
		}
		checkResponse, err := checker.Check(r.Context(), &checkRequest)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		resp, err := json.Marshal(CheckResponse{checkResponse.Status.String()})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(resp); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	return serviceName, check
}

//go:build integration

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

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"connectrpc.com/grpchealth"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/server/rpc/httphealth"
)

var services = []string{
	grpchealth.HealthV1ServiceName,
	v1connect.YorkieServiceName,
	v1connect.AdminServiceName,
}

func TestRPCHealthCheck(t *testing.T) {
	conn, err := grpc.Dial(
		defaultServer.RPCAddr(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	assert.NoError(t, err)
	defer func() {
		assert.NoError(t, conn.Close())
	}()
	cli := healthpb.NewHealthClient(conn)

	// check default service
	t.Run("Service: default", func(t *testing.T) {
		resp, err := cli.Check(context.Background(), &healthpb.HealthCheckRequest{})
		assert.NoError(t, err)
		assert.Equal(t, resp.Status, healthpb.HealthCheckResponse_SERVING)
	})

	// check all services
	for _, s := range services {
		service := s
		t.Run("Service: "+service, func(t *testing.T) {
			resp, err := cli.Check(context.Background(), &healthpb.HealthCheckRequest{
				Service: service,
			})
			assert.NoError(t, err)
			assert.Equal(t, resp.Status, healthpb.HealthCheckResponse_SERVING)
		})
	}

	// check unknown service
	t.Run("Service: unknown", func(t *testing.T) {
		_, err := cli.Check(context.Background(), &healthpb.HealthCheckRequest{
			Service: "unknown",
		})
		assert.Error(t, err)
	})
}

func TestHTTPHealthCheck(t *testing.T) {
	// check default service
	t.Run("Service: default", func(t *testing.T) {
		resp, err := http.Get("http://" + defaultServer.RPCAddr() + "/healthz/")
		assert.NoError(t, err)
		defer func() {
			assert.NoError(t, resp.Body.Close())
		}()
		assert.Equal(t, resp.StatusCode, http.StatusOK)

		var healthResp httphealth.CheckResponse
		err = json.NewDecoder(resp.Body).Decode(&healthResp)
		assert.NoError(t, err)
		assert.Equal(t, healthResp.Status, grpchealth.StatusServing.String())
	})

	// check all services
	for _, s := range services {
		service := s
		t.Run("Service: "+service, func(t *testing.T) {
			url := "http://" + defaultServer.RPCAddr() + "/healthz/?service=" + service
			resp, err := http.Get(url)
			assert.NoError(t, err)
			defer func() {
				assert.NoError(t, resp.Body.Close())
			}()
			assert.Equal(t, resp.StatusCode, http.StatusOK)

			var healthResp httphealth.CheckResponse
			err = json.NewDecoder(resp.Body).Decode(&healthResp)
			assert.NoError(t, err)
			assert.Equal(t, healthResp.Status, grpchealth.StatusServing.String())
		})
	}

	// check unknown service
	t.Run("Service: unknown", func(t *testing.T) {
		resp, err := http.Get("http://" + defaultServer.RPCAddr() + "/healthz/?service=unknown")
		assert.NoError(t, err)
		defer func() {
			assert.NoError(t, resp.Body.Close())
		}()
		assert.Equal(t, resp.StatusCode, http.StatusNotFound)
	})
}

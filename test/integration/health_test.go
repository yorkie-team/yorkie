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
	"github.com/yorkie-team/yorkie/server/rpc/health"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestRPCHealthCheck(t *testing.T) {
	// use gRPC health check
	conn, err := grpc.Dial(
		defaultServer.RPCAddr(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	assert.NoError(t, err)
	defer func() {
		assert.NoError(t, conn.Close())
	}()

	cli := healthpb.NewHealthClient(conn)
	resp, err := cli.Check(context.Background(), &healthpb.HealthCheckRequest{})
	assert.NoError(t, err)
	assert.Equal(t, resp.Status, healthpb.HealthCheckResponse_SERVING)
}

func TestHTTPHealthCheck(t *testing.T) {
	// use HTTP health check
	resp, err := http.Get("http://" + defaultServer.RPCAddr() + "/healthz/")
	defer func() {
		assert.NoError(t, resp.Body.Close())
	}()
	assert.NoError(t, err)
	assert.Equal(t, resp.StatusCode, 200)

	var healthResp health.HealthCheckResponse
	err = json.NewDecoder(resp.Body).Decode(&healthResp)
	assert.NoError(t, err)
	assert.Equal(t, healthResp.Status, grpchealth.StatusServing.String())
}

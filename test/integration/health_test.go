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
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
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

	// use gRPC health check for unknown service
	_, err = cli.Check(context.Background(), &healthpb.HealthCheckRequest{
		Service: "unknown",
	})
	assert.Error(t, err)
}

func TestRPCHealthCheckYorkie(t *testing.T) {
	// use gRPC health check for Yorkie
	conn, err := grpc.Dial(
		defaultServer.RPCAddr(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	assert.NoError(t, err)
	defer func() {
		assert.NoError(t, conn.Close())
	}()

	cli := healthpb.NewHealthClient(conn)
	resp, err := cli.Check(context.Background(), &healthpb.HealthCheckRequest{
		Service: v1connect.YorkieServiceName,
	})
	assert.NoError(t, err)
	assert.Equal(t, resp.Status, healthpb.HealthCheckResponse_SERVING)
}

func TestRPCHealthCheckAdmin(t *testing.T) {
	// use gRPC health check for Admin
	conn, err := grpc.Dial(
		defaultServer.RPCAddr(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	assert.NoError(t, err)
	defer func() {
		assert.NoError(t, conn.Close())
	}()

	cli := healthpb.NewHealthClient(conn)
	resp, err := cli.Check(context.Background(), &healthpb.HealthCheckRequest{
		Service: v1connect.AdminServiceName,
	})
	assert.NoError(t, err)
	assert.Equal(t, resp.Status, healthpb.HealthCheckResponse_SERVING)
}

func TestRPCHealthCheckHealthService(t *testing.T) {
	// use gRPC health check for health service
	conn, err := grpc.Dial(
		defaultServer.RPCAddr(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	assert.NoError(t, err)
	defer func() {
		assert.NoError(t, conn.Close())
	}()

	cli := healthpb.NewHealthClient(conn)
	resp, err := cli.Check(context.Background(), &healthpb.HealthCheckRequest{
		Service: grpchealth.HealthV1ServiceName,
	})
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

	var healthResp health.CheckResponse
	err = json.NewDecoder(resp.Body).Decode(&healthResp)
	assert.NoError(t, err)
	assert.Equal(t, healthResp.Status, grpchealth.StatusServing.String())

	// use HTTP health check for unknown service
	resp, err = http.Get("http://" + defaultServer.RPCAddr() + "/healthz/?service=unknown")
	defer func() {
		assert.NoError(t, resp.Body.Close())
	}()
	assert.NoError(t, err)
	assert.Equal(t, resp.StatusCode, 404)
}

func TestHTTPHealthCheckYorkie(t *testing.T) {
	// use HTTP health check for Yorkie
	resp, err := http.Get("http://" + defaultServer.RPCAddr() + "/healthz/?service=" + v1connect.YorkieServiceName)
	defer func() {
		assert.NoError(t, resp.Body.Close())
	}()
	assert.NoError(t, err)
	assert.Equal(t, resp.StatusCode, 200)

	var healthResp health.CheckResponse
	err = json.NewDecoder(resp.Body).Decode(&healthResp)
	assert.NoError(t, err)
	assert.Equal(t, healthResp.Status, grpchealth.StatusServing.String())
}

func TestHTTPHealthCheckAdmin(t *testing.T) {
	// use HTTP health check for Admin
	resp, err := http.Get("http://" + defaultServer.RPCAddr() + "/healthz/?service=" + v1connect.AdminServiceName)
	defer func() {
		assert.NoError(t, resp.Body.Close())
	}()
	assert.NoError(t, err)
	assert.Equal(t, resp.StatusCode, 200)

	var healthResp health.CheckResponse
	err = json.NewDecoder(resp.Body).Decode(&healthResp)
	assert.NoError(t, err)
	assert.Equal(t, healthResp.Status, grpchealth.StatusServing.String())
}

func TestHTTPHealthCheckHealthService(t *testing.T) {
	// use HTTP health check for health service
	resp, err := http.Get("http://" + defaultServer.RPCAddr() + "/healthz/?service=" + grpchealth.HealthV1ServiceName)
	defer func() {
		assert.NoError(t, resp.Body.Close())
	}()
	assert.NoError(t, err)
	assert.Equal(t, resp.StatusCode, 200)

	var healthResp health.CheckResponse
	err = json.NewDecoder(resp.Body).Decode(&healthResp)
	assert.NoError(t, err)
	assert.Equal(t, healthResp.Status, grpchealth.StatusServing.String())
}

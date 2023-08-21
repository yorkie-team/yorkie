//go:build amd64

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

package client_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/rs/xid"
	"github.com/stretchr/testify/assert"
	monkey "github.com/undefinedlabs/go-mpatch"
	"golang.org/x/net/nettest"
	"google.golang.org/grpc"
	grpcmetadata "google.golang.org/grpc/metadata"

	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/client"
)

type testYorkieServer struct {
	grpcServer   *grpc.Server
	yorkieServer *api.UnimplementedYorkieServiceServer
}

// dialTestYorkieServer creates a new instance of testYorkieServer and
// dials it with LocalListener.
func dialTestYorkieServer(t *testing.T) (*testYorkieServer, string) {
	yorkieServer := &api.UnimplementedYorkieServiceServer{}
	grpcServer := grpc.NewServer()
	api.RegisterYorkieServiceServer(grpcServer, yorkieServer)

	testYorkieServer := &testYorkieServer{
		grpcServer:   grpcServer,
		yorkieServer: yorkieServer,
	}

	return testYorkieServer, testYorkieServer.listenAndServe(t)
}

func (s *testYorkieServer) listenAndServe(t *testing.T) string {
	lis, err := nettest.NewLocalListener("tcp")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	go func() {
		if err := s.grpcServer.Serve(lis); err != nil {
			if err != grpc.ErrServerStopped {
				t.Error(err)
			}
		}
	}()

	return lis.Addr().String()
}

func (s *testYorkieServer) Stop() {
	s.grpcServer.Stop()
}

func TestClient(t *testing.T) {
	t.Run("create instance test", func(t *testing.T) {
		_, err := client.New(
			client.WithToken(xid.New().String()),
		)
		assert.NoError(t, err)
	})

	t.Run("x-shard-key test", func(t *testing.T) {
		dummyID := types.ID("000000000000000000000000")

		testServer, addr := dialTestYorkieServer(t)
		defer testServer.Stop()

		cli, err := client.Dial(addr, client.WithAPIKey("dummy-api-key"))
		assert.NoError(t, err)

		var patch *monkey.Patch
		patch, err = monkey.PatchInstanceMethodByName(
			reflect.TypeOf(testServer.yorkieServer),
			"ActivateClient",
			func(
				m *api.UnimplementedYorkieServiceServer,
				ctx context.Context,
				req *api.ActivateClientRequest,
			) (*api.ActivateClientResponse, error) {
				assert.NoError(t, patch.Unpatch())
				defer func() {
					assert.NoError(t, patch.Patch())
				}()

				data, _ := grpcmetadata.FromIncomingContext(ctx)
				assert.Equal(t, "dummy-api-key", data[types.ShardKey][0])

				return &api.ActivateClientResponse{
					ClientId: dummyID.String(),
				}, nil
			},
		)
		assert.NoError(t, err)
		assert.NoError(t, cli.Activate(context.Background()))
	})
}

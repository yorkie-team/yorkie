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

	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/client"
)

type testYorkieServer struct {
	grpcServer *grpc.Server
}

func (t *testYorkieServer) ActivateClient(ctx context.Context, request *api.ActivateClientRequest) (*api.ActivateClientResponse, error) {
	panic("implement me")
}

func (t *testYorkieServer) DeactivateClient(ctx context.Context, request *api.DeactivateClientRequest) (*api.DeactivateClientResponse, error) {
	panic("implement me")
}

func (t *testYorkieServer) UpdatePresence(ctx context.Context, request *api.UpdatePresenceRequest) (*api.UpdatePresenceResponse, error) {
	panic("implement me")
}

func (t *testYorkieServer) AttachDocument(ctx context.Context, request *api.AttachDocumentRequest) (*api.AttachDocumentResponse, error) {
	panic("implement me")
}

func (t *testYorkieServer) DetachDocument(ctx context.Context, request *api.DetachDocumentRequest) (*api.DetachDocumentResponse, error) {
	panic("implement me")
}

func (t *testYorkieServer) RemoveDocument(ctx context.Context, request *api.RemoveDocumentRequest) (*api.RemoveDocumentResponse, error) {
	panic("implement me")
}

func (t *testYorkieServer) PushPullChanges(ctx context.Context, request *api.PushPullChangesRequest) (*api.PushPullChangesResponse, error) {
	panic("implement me")
}

func (t *testYorkieServer) WatchDocument(request *api.WatchDocumentRequest, server api.YorkieService_WatchDocumentServer) error {
	panic("implement me")
}

// newYorkieServer creates a new instance of yorkieServer.
func dialTestYorkieServer(t *testing.T) (*testYorkieServer, string) {
	testYorkieServer := &testYorkieServer{}
	grpcServer := grpc.NewServer()
	api.RegisterYorkieServiceServer(grpcServer, testYorkieServer)
	testYorkieServer.grpcServer = grpcServer

	addr := testYorkieServer.listenAndServe(t)
	return testYorkieServer, addr
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
		presence := types.Presence{"Name": "ClientName"}
		cli, err := client.New(
			client.WithToken(xid.New().String()),
			client.WithPresence(presence),
		)
		assert.NoError(t, err)
		assert.Equal(t, presence, cli.Presence())
	})

	// t.Run("x-shard-key test", func(t *testing.T) {
	// 	testServer, addr := dialTestYorkieServer(t)
	// 	defer testServer.Stop()

	// 	cli, err := client.Dial(addr, client.WithAPIKey("dummy-api-key"))
	// 	assert.NoError(t, err)

	// 	var patch *monkey.Patch
	// 	patch, err = monkey.PatchInstanceMethodByName(reflect.TypeOf(testServer), "ActivateClient", func(m *testYorkieServer, ctx context.Context, req *api.ActivateClientRequest) (*api.ActivateClientResponse, error) {
	// 		patch.Unpatch()
	// 		defer patch.Patch()
	// 		t.Log("ActivateClient called")
	// 		return &api.ActivateClientResponse{}, nil
	// 	})
	// 	if err != nil {
	// 		t.Fatal(err)
	// 	}

	// 	ctx := context.Background()
	// 	err = cli.Activate(ctx)
	// })
}

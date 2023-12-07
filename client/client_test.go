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
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"connectrpc.com/connect"
	"github.com/rs/xid"
	"github.com/stretchr/testify/assert"
	monkey "github.com/undefinedlabs/go-mpatch"

	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/client"
)

type testYorkieServer struct {
	httpServer   *httptest.Server
	yorkieServer *v1connect.UnimplementedYorkieServiceHandler
}

// dialTestYorkieServer creates a new instance of testYorkieServer and
// dials it with LocalListener.
func dialTestYorkieServer() (*testYorkieServer, string) {
	yorkieServer := &v1connect.UnimplementedYorkieServiceHandler{}
	mux := http.NewServeMux()
	mux.Handle(v1connect.NewYorkieServiceHandler(yorkieServer))
	httpServer := httptest.NewUnstartedServer(mux)

	testYorkieServer := &testYorkieServer{
		httpServer:   httpServer,
		yorkieServer: yorkieServer,
	}

	return testYorkieServer, testYorkieServer.listenAndServe()
}

func (s *testYorkieServer) listenAndServe() string {
	s.httpServer.Start()

	return s.httpServer.URL
}

func (s *testYorkieServer) Stop() {
	s.httpServer.Close()
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

		testServer, addr := dialTestYorkieServer()
		defer testServer.Stop()

		cli, err := client.Dial(addr, client.WithAPIKey("dummy-api-key"))
		assert.NoError(t, err)

		var patch *monkey.Patch
		patch, err = monkey.PatchInstanceMethodByName(
			reflect.TypeOf(testServer.yorkieServer),
			"ActivateClient",
			func(
				m *v1connect.UnimplementedYorkieServiceHandler,
				ctx context.Context,
				req *connect.Request[api.ActivateClientRequest],
			) (*connect.Response[api.ActivateClientResponse], error) {
				assert.NoError(t, patch.Unpatch())
				defer func() {
					assert.NoError(t, patch.Patch())
				}()

				assert.Equal(t, "dummy-api-key", req.Header().Get(types.ShardKey))

				return connect.NewResponse(&api.ActivateClientResponse{
					ClientId: dummyID.String(),
				}), nil
			},
		)
		assert.NoError(t, err)
		assert.NoError(t, cli.Activate(context.Background()))
	})
}

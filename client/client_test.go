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
	"github.com/yorkie-team/yorkie/pkg/attachable"
	"github.com/yorkie-team/yorkie/pkg/channel"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/yson"
	"github.com/yorkie-team/yorkie/pkg/key"
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
			reflect.TypeFor[*v1connect.UnimplementedYorkieServiceHandler](),
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

				assert.Equal(t, "dummy-api-key/"+cli.Key(), req.Header().Get(types.ShardKey))

				return connect.NewResponse(&api.ActivateClientResponse{
					ClientId: dummyID.String(),
				}), nil
			},
		)
		assert.NoError(t, err)
		assert.NoError(t, cli.Activate(context.Background()))
	})
}

// activatingYorkieServer answers ActivateClient and leaves every other
// method unimplemented, so a client can reach the checks Attach runs before
// its first RPC. It is a real handler rather than a patch of the
// unimplemented one: a patch installed here would collide with the one
// TestClient leaves on the same method.
type activatingYorkieServer struct {
	v1connect.UnimplementedYorkieServiceHandler
}

func (s *activatingYorkieServer) ActivateClient(
	_ context.Context,
	_ *connect.Request[api.ActivateClientRequest],
) (*connect.Response[api.ActivateClientResponse], error) {
	dummyID := types.ID("000000000000000000000000")
	return connect.NewResponse(&api.ActivateClientResponse{
		ClientId: dummyID.String(),
	}), nil
}

func TestAttachInitialRoot(t *testing.T) {
	// A dedup counter built in Go never passes the YSON text decoder, so its
	// register payload is unchecked until json.Object.SetYSONElement panics
	// on it. Attach must reject the root instead, before any RPC.
	t.Run("malformed dedup counter payload test", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.Handle(v1connect.NewYorkieServiceHandler(&activatingYorkieServer{}))
		httpServer := httptest.NewServer(mux)
		defer httpServer.Close()

		cli, err := client.Dial(httpServer.URL)
		assert.NoError(t, err)
		assert.NoError(t, cli.Activate(context.Background()))

		doc := document.New(key.Key("test-doc"))
		// AttachDocument is left unimplemented: a root that reaches the RPC
		// fails there instead, so only the validation error proves the guard.
		err = cli.Attach(context.Background(), doc, client.WithInitialRoot(yson.Object{
			"uv": yson.Counter{
				Type:      crdt.IntegerDedupCnt,
				Value:     int32(15),
				Registers: make([]byte, 4),
			},
		}))
		assert.ErrorIs(t, err, yson.ErrInvalidYSON)
		assert.Equal(t, attachable.StatusDetached, doc.Status())
	})

	// The same guard covers every other value SetYSONElement panics on, not
	// the dedup payload alone.
	t.Run("unsupported initial root value test", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.Handle(v1connect.NewYorkieServiceHandler(&activatingYorkieServer{}))
		httpServer := httptest.NewServer(mux)
		defer httpServer.Close()

		cli, err := client.Dial(httpServer.URL)
		assert.NoError(t, err)
		assert.NoError(t, cli.Activate(context.Background()))

		for _, value := range []any{
			1,
			yson.Counter{Type: crdt.IntegerCnt, Value: "one"},
			yson.Tree{Root: yson.TreeNode{
				Type:     "doc",
				Children: []yson.TreeNode{{Type: yson.TextNodeType}},
			}},
		} {
			doc := document.New(key.Key("test-doc"))
			err = cli.Attach(context.Background(), doc, client.WithInitialRoot(yson.Object{
				"k": value,
			}))
			assert.ErrorIs(t, err, yson.ErrInvalidYSON)
			assert.Equal(t, attachable.StatusDetached, doc.Status())
		}
	})

	// The apply step skips a key the document already holds, so the guard
	// must skip it too rather than refuse an attach over a value never read.
	t.Run("value under an existing key test", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.Handle(v1connect.NewYorkieServiceHandler(&activatingYorkieServer{}))
		httpServer := httptest.NewServer(mux)
		defer httpServer.Close()

		cli, err := client.Dial(httpServer.URL)
		assert.NoError(t, err)
		assert.NoError(t, cli.Activate(context.Background()))

		doc := document.New(key.Key("test-doc"))
		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetString("uv", "already here")
			return nil
		}))

		// AttachDocument is left unimplemented, so reaching the RPC -- any
		// error but the validation one -- is what shows the guard passed.
		err = cli.Attach(context.Background(), doc, client.WithInitialRoot(yson.Object{
			"uv": yson.Counter{
				Type:      crdt.IntegerDedupCnt,
				Value:     int32(15),
				Registers: make([]byte, 4),
			},
		}))
		assert.Error(t, err)
		assert.NotErrorIs(t, err, yson.ErrInvalidYSON)
	})
}

func TestAttachableInterfaceCompatibility(t *testing.T) {
	t.Run("Document implements Attachable", func(t *testing.T) {
		docKey := key.Key("test-doc")
		doc := document.New(docKey)

		// Ensure Document implements Attachable
		var _ attachable.Attachable = doc

		assert.Equal(t, "test-doc", doc.Key().String())
		assert.Equal(t, attachable.TypeDocument, doc.Type())
		assert.Equal(t, attachable.StatusDetached, doc.Status())
		assert.False(t, doc.IsAttached())
	})

	t.Run("Presence Counter implements Attachable", func(t *testing.T) {
		channelKey := key.Key("test-presence")
		counter, err := channel.New(channelKey)
		assert.NoError(t, err)

		// Ensure Presence Counter implements Attachable
		var _ attachable.Attachable = counter

		assert.Equal(t, "test-presence", counter.Key().String())
		assert.Equal(t, attachable.TypeChannel, counter.Type())
		assert.Equal(t, attachable.StatusDetached, counter.Status())
		assert.False(t, counter.IsAttached())
	})

	t.Run("Status changes work correctly", func(t *testing.T) {
		docKey := key.Key("status-doc")
		doc := document.New(docKey)

		channelKey := key.Key("status-presence")
		counter, err := channel.New(channelKey)
		assert.NoError(t, err)

		resources := []attachable.Attachable{doc, counter}

		for _, resource := range resources {
			// Test status transitions
			assert.Equal(t, attachable.StatusDetached, resource.Status())
			assert.False(t, resource.IsAttached())

			resource.SetStatus(attachable.StatusAttached)
			assert.Equal(t, attachable.StatusAttached, resource.Status())
			assert.True(t, resource.IsAttached())

			resource.SetStatus(attachable.StatusRemoved)
			assert.Equal(t, attachable.StatusRemoved, resource.Status())
			assert.False(t, resource.IsAttached())

			resource.SetStatus(attachable.StatusDetached)
			assert.Equal(t, attachable.StatusDetached, resource.Status())
			assert.False(t, resource.IsAttached())
		}
	})
}

/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/key"
)

// countingWatchServer counts Watch calls, so a test can tell a reconnect
// attempt that reached the server from one that did not.
type countingWatchServer struct {
	*watchInitServer
	watches atomic.Int32
}

func (s *countingWatchServer) Watch(
	ctx context.Context,
	req *connect.Request[api.WatchRequest],
	stream *connect.ServerStream[api.WatchResponse],
) error {
	s.watches.Add(1)
	return s.watchInitServer.Watch(ctx, req, stream)
}

func (s *countingWatchServer) DeactivateClient(
	_ context.Context,
	_ *connect.Request[api.DeactivateClientRequest],
) (*connect.Response[api.DeactivateClientResponse], error) {
	return connect.NewResponse(&api.DeactivateClientResponse{}), nil
}

// inFlightSyncServer holds PushPullChanges open until the test releases it, so
// a user-initiated Sync can be parked mid-flight -- past pushPullChanges'
// status guard and holding the attachment's syncMu -- while Deactivate runs.
type inFlightSyncServer struct {
	*watchInitServer

	entered chan struct{}
	finish  chan struct{}
}

func (s *inFlightSyncServer) PushPullChanges(
	_ context.Context,
	req *connect.Request[api.PushPullChangesRequest],
) (*connect.Response[api.PushPullChangesResponse], error) {
	select {
	case s.entered <- struct{}{}:
	default:
	}
	<-s.finish

	pack := req.Msg.ChangePack
	return connect.NewResponse(&api.PushPullChangesResponse{
		ChangePack: &api.ChangePack{
			DocumentKey:   pack.DocumentKey,
			Checkpoint:    pack.Checkpoint,
			VersionVector: pack.VersionVector,
		},
	}), nil
}

func (s *inFlightSyncServer) DeactivateClient(
	_ context.Context,
	_ *connect.Request[api.DeactivateClientRequest],
) (*connect.Response[api.DeactivateClientResponse], error) {
	return connect.NewResponse(&api.DeactivateClientResponse{}), nil
}

// TestDeactivateWaitsForInFlightSync pins that Deactivate retires an
// attachment's event pump only under that attachment's syncMu. Stopping the
// sync loop is not enough: Client.Sync is callable straight from a user
// goroutine, and a pump retired under one would leave its ApplyChangePack
// blocked forever on the document's capacity-one event channel, holding the
// document's event mutex and taking every other publisher down with it.
//
// The sync loop is given a duration longer than the test so its own
// needSync -- which takes syncMu for reading -- cannot be what blocks
// Deactivate here.
func TestDeactivateWaitsForInFlightSync(t *testing.T) {
	srv := &inFlightSyncServer{
		watchInitServer: &watchInitServer{
			firstResponse: &api.WatchResponse{
				Body: &api.WatchResponse_Initialization{
					Initialization: &api.WatchInitialization{},
				},
			},
			release: make(chan struct{}),
		},
		entered: make(chan struct{}, 1),
		finish:  make(chan struct{}),
	}
	mux := http.NewServeMux()
	mux.Handle(v1connect.NewYorkieServiceHandler(srv))
	httpServer := httptest.NewServer(mux)
	t.Cleanup(func() {
		close(srv.release)
		httpServer.Close()
	})
	// Registered after the server cleanup, so it runs before it: a failure
	// below must not leave the blocked handler holding the server shutdown.
	releaseSync := sync.OnceFunc(func() { close(srv.finish) })
	t.Cleanup(releaseSync)

	cli, err := client.Dial(httpServer.URL, client.WithSyncLoopDuration(time.Minute))
	assert.NoError(t, err)
	assert.NoError(t, cli.Activate(context.Background()))

	doc := document.New(key.Key("deactivate-inflight-sync"))
	assert.NoError(t, cli.Attach(context.Background(), doc, client.WithRealtimeSync()))

	syncDone := make(chan error, 1)
	go func() { syncDone <- cli.Sync(context.Background()) }()

	select {
	case <-srv.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("sync did not reach the server")
	}

	deactivateDone := make(chan error, 1)
	go func() { deactivateDone <- cli.Deactivate(context.Background()) }()

	select {
	case <-deactivateDone:
		t.Fatal("Deactivate retired the pipeline under an in-flight Sync")
	case <-time.After(200 * time.Millisecond):
	}

	releaseSync()

	select {
	case err := <-syncDone:
		assert.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("in-flight sync did not finish")
	}
	select {
	case err := <-deactivateDone:
		assert.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("deactivate did not finish")
	}
}

// TestDetachEndsWatchStreamQuietly pins that the client's own teardown is not
// treated as a lost stream. Detach and Deactivate cancel the watch context, so
// the stream reader's Receive ends with context.Canceled; that must end the
// reader without a reconnect attempt and without a "re-establish watch
// stream" warning on every Detach.
func TestDetachEndsWatchStreamQuietly(t *testing.T) {
	for _, tc := range []struct {
		name     string
		teardown func(cli *client.Client, doc *document.Document) error
	}{
		{"detach", func(cli *client.Client, doc *document.Document) error {
			return cli.Detach(context.Background(), doc)
		}},
		{"deactivate", func(cli *client.Client, _ *document.Document) error {
			return cli.Deactivate(context.Background())
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := &countingWatchServer{watchInitServer: &watchInitServer{
				firstResponse: &api.WatchResponse{
					Body: &api.WatchResponse_Initialization{
						Initialization: &api.WatchInitialization{},
					},
				},
				release: make(chan struct{}),
			}}
			mux := http.NewServeMux()
			mux.Handle(v1connect.NewYorkieServiceHandler(srv))
			httpServer := httptest.NewServer(mux)
			t.Cleanup(func() {
				close(srv.release)
				httpServer.Close()
			})

			core, logs := observer.New(zap.DebugLevel)
			cli, err := client.Dial(httpServer.URL, client.WithLogger(zap.New(core)))
			assert.NoError(t, err)
			assert.NoError(t, cli.Activate(context.Background()))

			doc := document.New(key.Key("watch-teardown-" + tc.name))
			assert.NoError(t, cli.Attach(context.Background(), doc, client.WithRealtimeSync()))
			rch, _, err := cli.WatchStream(doc)
			assert.NoError(t, err)

			assert.NoError(t, tc.teardown(cli, doc))

			for resp := range rch {
				assert.NoError(t, resp.Err)
			}
			assert.Equal(t, int32(1), srv.watches.Load())
			assert.Zero(t, logs.FilterMessageSnippet("re-establish watch stream").Len())
		})
	}
}

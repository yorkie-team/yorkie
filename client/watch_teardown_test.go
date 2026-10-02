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
	"errors"
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

// failingDeactivateServer fails the first DeactivateClient and serves every
// later one, so a test can watch what a failed deactivation leaves behind.
type failingDeactivateServer struct {
	*watchInitServer
	deactivates atomic.Int32
}

func (s *failingDeactivateServer) DeactivateClient(
	_ context.Context,
	_ *connect.Request[api.DeactivateClientRequest],
) (*connect.Response[api.DeactivateClientResponse], error) {
	if s.deactivates.Add(1) == 1 {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("deactivate rejected"))
	}
	return connect.NewResponse(&api.DeactivateClientResponse{}), nil
}

// TestDeactivateRestoresStatusOnFailure pins that a failed DeactivateClient
// does not wedge the client in the deactivating state. The server-side session
// outlived the call, so IsActive has to keep reporting it, the guards spelled
// `!= statusActivated` have to keep letting the client through, and a retry
// has to be able to finish the deactivation.
func TestDeactivateRestoresStatusOnFailure(t *testing.T) {
	srv := &failingDeactivateServer{watchInitServer: &watchInitServer{
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

	ctx := context.Background()
	cli, err := client.Dial(httpServer.URL, client.WithSyncLoopDuration(time.Minute))
	assert.NoError(t, err)
	assert.NoError(t, cli.Activate(ctx))

	assert.Error(t, cli.Deactivate(ctx))
	assert.True(t, cli.IsActive())

	// Every entry point guarded on the activated status stays open.
	doc := document.New(key.Key("deactivate-failure-retry"))
	assert.NoError(t, cli.Attach(ctx, doc))
	assert.NoError(t, cli.Detach(ctx, doc))

	assert.NoError(t, cli.Deactivate(ctx))
	assert.False(t, cli.IsActive())
}

// blockingDeactivateServer holds DeactivateClient open until the test releases
// it, so a deactivation can be parked mid-flight while another call runs.
type blockingDeactivateServer struct {
	*watchInitServer

	entered chan struct{}
	finish  chan struct{}
}

func (s *blockingDeactivateServer) DeactivateClient(
	_ context.Context,
	_ *connect.Request[api.DeactivateClientRequest],
) (*connect.Response[api.DeactivateClientResponse], error) {
	select {
	case s.entered <- struct{}{}:
	default:
	}
	<-s.finish

	return connect.NewResponse(&api.DeactivateClientResponse{}), nil
}

// TestActivateRejectedWhileDeactivating pins that Activate refuses to lay a
// new session over one being ended. The deactivation has already retired the
// watch pipelines and is about to drop the attachments, so activating here
// would hand the client a new ID while the ended session's attachments are
// still registered, leaving the sync loop to push resources the new ID never
// attached over pipelines that no longer deliver.
func TestActivateRejectedWhileDeactivating(t *testing.T) {
	srv := &blockingDeactivateServer{
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
	releaseDeactivate := sync.OnceFunc(func() { close(srv.finish) })
	t.Cleanup(releaseDeactivate)

	ctx := context.Background()
	cli, err := client.Dial(httpServer.URL, client.WithSyncLoopDuration(time.Minute))
	assert.NoError(t, err)
	assert.NoError(t, cli.Activate(ctx))

	doc := document.New(key.Key("activate-while-deactivating"))
	assert.NoError(t, cli.Attach(ctx, doc, client.WithRealtimeSync()))
	firstID := cli.ID()

	deactivateDone := make(chan error, 1)
	go func() { deactivateDone <- cli.Deactivate(ctx) }()

	select {
	case <-srv.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("deactivate did not reach the server")
	}

	assert.ErrorIs(t, cli.Activate(ctx), client.ErrDeactivating)
	assert.Equal(t, firstID, cli.ID())

	releaseDeactivate()
	select {
	case err := <-deactivateDone:
		assert.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("deactivate did not finish")
	}
	assert.False(t, cli.IsActive())
}

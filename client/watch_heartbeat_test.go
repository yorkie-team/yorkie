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
	"sync/atomic"
	"testing"
	gotime "time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"

	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/channel"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/key"
)

// watchIdleServer advertises a heartbeat interval on every Watch stream and
// then holds the stream open and silent, which is what a half-open socket
// looks like from the client: established, answered once, and never heard
// from again. The client's idle watchdog is the only thing that can end such
// a stream.
type watchIdleServer struct {
	*watchInitServer

	// intervalMs is the heartbeat interval advertised in the initialization
	// response. The client times a stream out at a multiple of it.
	intervalMs int64
	// rejectCall is the ordinal of the Watch call to reject outright, 0 for
	// none. It stands for the handshake that fails because whatever took the
	// first stream down -- a rolling server, a network still settling -- is
	// still true a moment later.
	rejectCall int32

	calls atomic.Int32
	// requestedMs records the heartbeat interval the last Watch request asked
	// for, which is what the server would clamp and honor.
	requestedMs atomic.Int64
}

func (s *watchIdleServer) Watch(
	ctx context.Context,
	req *connect.Request[api.WatchRequest],
	stream *connect.ServerStream[api.WatchResponse],
) error {
	s.requestedMs.Store(req.Msg.HeartbeatIntervalMs)

	if s.calls.Add(1) == s.rejectCall {
		return connect.NewError(connect.CodeUnavailable, errors.New("watch rejected"))
	}

	if err := stream.Send(&api.WatchResponse{
		Body: &api.WatchResponse_Initialization{
			Initialization: &api.WatchInitialization{HeartbeatIntervalMs: s.intervalMs},
		},
	}); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
	case <-s.release:
	}
	return nil
}

// AttachChannel answers a channel attach, so a channel Watch can be opened
// against this server too.
func (s *watchIdleServer) AttachChannel(
	_ context.Context,
	_ *connect.Request[api.AttachChannelRequest],
) (*connect.Response[api.AttachChannelResponse], error) {
	return connect.NewResponse(&api.AttachChannelResponse{
		SessionId:    "000000000000000000000003",
		SessionCount: 1,
	}), nil
}

// dialWatchIdleServer starts the given server and returns an activated client
// pointed at it.
func dialWatchIdleServer(t *testing.T, srv *watchIdleServer) *client.Client {
	t.Helper()

	mux := http.NewServeMux()
	mux.Handle(v1connect.NewYorkieServiceHandler(srv))
	httpServer := httptest.NewServer(mux)

	cli, err := client.Dial(httpServer.URL)
	assert.NoError(t, err)
	assert.NoError(t, cli.Activate(context.Background()))

	t.Cleanup(func() {
		select {
		case <-srv.release:
		default:
			close(srv.release)
		}
		httpServer.Close()
	})

	return cli
}

func newWatchIdleServer(intervalMs int64, rejectCall int32) *watchIdleServer {
	return &watchIdleServer{
		watchInitServer: &watchInitServer{release: make(chan struct{})},
		intervalMs:      intervalMs,
		rejectCall:      rejectCall,
	}
}

// TestWatchRequestCarriesHeartbeatInterval pins that the client asks for the
// heartbeat it wants, on both Watch requests it builds: the document's and
// the channel's. The server heartbeats only the streams that ask, so a client
// that sent nothing here would silently get no heartbeat and leave its idle
// watchdog disarmed -- the very failure the request field exists to fix.
func TestWatchRequestCarriesHeartbeatInterval(t *testing.T) {
	watchDocument := func(t *testing.T, cli *client.Client) {
		doc := document.New(key.Key("watch-heartbeat-request"))
		assert.NoError(t, cli.Attach(context.Background(), doc, client.WithRealtimeSync()))
		_, _, err := cli.WatchStream(doc)
		assert.NoError(t, err)
	}
	watchChannel := func(t *testing.T, cli *client.Client) {
		ch, err := channel.New(key.Key("watch-heartbeat-request"))
		assert.NoError(t, err)
		assert.NoError(t, cli.Attach(context.Background(), ch))
		_, closeWatch, err := cli.WatchChannel(context.Background(), ch)
		assert.NoError(t, err)
		t.Cleanup(closeWatch)
	}

	requested := func(
		t *testing.T,
		watch func(*testing.T, *client.Client),
		opts ...client.Option,
	) int64 {
		t.Helper()

		srv := newWatchIdleServer(100, 0)
		mux := http.NewServeMux()
		mux.Handle(v1connect.NewYorkieServiceHandler(srv))
		httpServer := httptest.NewServer(mux)
		t.Cleanup(func() {
			select {
			case <-srv.release:
			default:
				close(srv.release)
			}
			httpServer.Close()
		})

		cli, err := client.Dial(httpServer.URL, opts...)
		assert.NoError(t, err)
		assert.NoError(t, cli.Activate(context.Background()))

		watch(t, cli)

		assert.Eventually(t, func() bool {
			return srv.calls.Load() >= 1
		}, 10*gotime.Second, 20*gotime.Millisecond, "the client never opened a watch stream")

		return srv.requestedMs.Load()
	}

	for _, path := range []struct {
		name  string
		watch func(*testing.T, *client.Client)
	}{
		{"document", watchDocument},
		{"channel", watchChannel},
	} {
		t.Run(path.name, func(t *testing.T) {
			t.Run("an unconfigured client asks for the default", func(t *testing.T) {
				assert.Equal(t, int64(20_000), requested(t, path.watch))
			})

			t.Run("a configured interval is what is asked for", func(t *testing.T) {
				assert.Equal(t, int64(7_000),
					requested(t, path.watch, client.WithWatchHeartbeatInterval(7*gotime.Second)))
			})

			t.Run("opting out asks for nothing", func(t *testing.T) {
				// Which is byte for byte what a client older than the field
				// sends, and the server answers both the same way.
				assert.Zero(t, requested(t, path.watch,
					client.WithWatchHeartbeatInterval(client.NoWatchHeartbeat)))
			})
		})
	}
}

// TestWatchStreamIdleTimeout pins the client's idle watchdog end to end: a
// server that advertises a heartbeat interval and then says nothing has its
// stream timed out, the timeout reaches the consumer named as
// ErrWatchStreamIdle rather than as a nil error or a bare cancellation, and
// the watch reconnects instead of being retired.
func TestWatchStreamIdleTimeout(t *testing.T) {
	srv := newWatchIdleServer(100, 0)
	cli := dialWatchIdleServer(t, srv)

	doc := document.New(key.Key("watch-stream-idle"))
	assert.NoError(t, cli.Attach(context.Background(), doc, client.WithRealtimeSync()))

	rch, _, err := cli.WatchStream(doc)
	assert.NoError(t, err)

	select {
	case resp, ok := <-rch:
		assert.True(t, ok, "a timed-out stream must report the timeout, not close the channel")
		assert.ErrorIs(t, resp.Err, client.ErrWatchStreamIdle,
			"a stream the watchdog cancelled must be named, not reported as nothing at all")
	case <-gotime.After(10 * gotime.Second):
		t.Fatal("a silent stream was not timed out")
	}

	assert.Eventually(t, func() bool {
		return srv.calls.Load() >= 2
	}, 10*gotime.Second, 20*gotime.Millisecond, "the client must re-establish the stream it timed out")

	assert.NoError(t, cli.Detach(context.Background(), doc))
}

// TestWatchReconnectRetriesAfterFailedHandshake pins that a reconnect is not
// single-shot. The handshake that follows a lost stream is the one most
// likely to fail -- the condition that took the stream down is usually still
// true -- and a client that gave up there would leave the document without
// realtime for good over an outage that lasted a moment.
func TestWatchReconnectRetriesAfterFailedHandshake(t *testing.T) {
	srv := newWatchIdleServer(100, 2)
	cli := dialWatchIdleServer(t, srv)

	doc := document.New(key.Key("watch-reconnect-retry"))
	assert.NoError(t, cli.Attach(context.Background(), doc, client.WithRealtimeSync()))

	rch, _, err := cli.WatchStream(doc)
	assert.NoError(t, err)

	// Stream 1 goes idle, the reconnect of call 2 is rejected, and the retry
	// of call 3 is what proves the pipeline survived the rejection.
	assert.Eventually(t, func() bool {
		return srv.calls.Load() >= 3
	}, 10*gotime.Second, 20*gotime.Millisecond, "a rejected reconnect must be retried")

	select {
	case _, ok := <-rch:
		assert.True(t, ok, "a reconnect that failed once must not retire the pipeline")
	default:
	}

	assert.NoError(t, cli.Detach(context.Background(), doc))
}

// TestWatchStreamWithoutHeartbeatIsNotTimedOut pins the other half of the
// gate: a server that advertises no heartbeat -- one older than the feature,
// or one with it turned off, which is the default -- leaves the watchdog
// unarmed. Timing such a stream out would reconnect on every quiet document.
func TestWatchStreamWithoutHeartbeatIsNotTimedOut(t *testing.T) {
	srv := newWatchIdleServer(0, 0)
	cli := dialWatchIdleServer(t, srv)

	doc := document.New(key.Key("watch-no-heartbeat"))
	assert.NoError(t, cli.Attach(context.Background(), doc, client.WithRealtimeSync()))

	rch, _, err := cli.WatchStream(doc)
	assert.NoError(t, err)

	select {
	case resp, ok := <-rch:
		t.Fatalf("a quiet stream with no heartbeat was disturbed: %v (open: %v)", resp.Err, ok)
	case <-gotime.After(2 * gotime.Second):
	}
	assert.Equal(t, int32(1), srv.calls.Load(), "a stream with no heartbeat must not be re-established")

	assert.NoError(t, cli.Detach(context.Background(), doc))
}

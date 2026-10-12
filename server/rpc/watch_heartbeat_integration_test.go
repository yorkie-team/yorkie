//go:build integration

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

package rpc_test

import (
	"context"
	"testing"
	gotime "time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"

	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/test/helper"
)

// openWatchAskingFor opens a Watch stream through the real RPC server, asking
// for requestedMs milliseconds of heartbeat on the request itself. It returns
// the stream and the initialization the server answered with.
func openWatchAskingFor(
	ctx context.Context,
	t *testing.T,
	requestedMs int64,
) (*connect.ServerStreamForClient[api.WatchResponse], *api.WatchInitialization) {
	t.Helper()

	// Unique per subtest, so neither the client nor the document is shared
	// with a stream configured differently.
	docKey := helper.TestKey(t).String()

	activateResp, err := testClient.ActivateClient(
		ctx,
		connect.NewRequest(&api.ActivateClientRequest{ClientKey: docKey}),
	)
	assert.NoError(t, err)

	resPack, err := testClient.AttachDocument(
		ctx,
		connect.NewRequest(&api.AttachDocumentRequest{
			ClientId: activateResp.Msg.ClientId,
			ChangePack: &api.ChangePack{
				DocumentKey: docKey,
				Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
			},
		}),
	)
	assert.NoError(t, err)

	stream, err := testClient.Watch(ctx, connect.NewRequest(&api.WatchRequest{
		ClientId:            activateResp.Msg.ClientId,
		HeartbeatIntervalMs: requestedMs,
		Resources: []*api.ResourceDescriptor{{
			Resource: &api.ResourceDescriptor_Document{
				Document: &api.DocumentDescriptor{DocumentId: resPack.Msg.DocumentId},
			},
		}},
	}))
	assert.NoError(t, err)
	// The stream is ended by the test's own deadline, so closing it afterwards
	// reports that cancellation and there is nothing to assert on.
	t.Cleanup(func() { _ = stream.Close() })

	assert.True(t, stream.Receive(), "the stream sent no initialization: %v", stream.Err())
	init := stream.Msg().GetInitialization()
	assert.NotNil(t, init, "the first response of a Watch stream must be the initialization")

	return stream, init
}

// awaitHeartbeat drains the stream until a heartbeat arrives or the stream
// ends, and reports whether one arrived. Other bodies are ignored: a live
// document can deliver events, and only the heartbeat is under test here.
func awaitHeartbeat(stream *connect.ServerStreamForClient[api.WatchResponse]) bool {
	for stream.Receive() {
		if stream.Msg().GetHeartbeat() != nil {
			return true
		}
	}
	return false
}

// TestWatchHeartbeatInterval pins the server half of the opt-in end to end:
// what a stream asks for on the WatchRequest is what the server clamps,
// advertises on the initialization, and then actually sends on the wire. Each
// of those three is read from the same resolved value, so a break anywhere in
// the wiring -- reading the wrong request field, advertising the configured
// bound instead of the effective interval, or passing the unresolved request
// to the event loop -- either disarms every client's idle watchdog or pushes
// heartbeats at a client that never asked for one and cannot parse them.
//
// The server under test is configured with helper.WatchHeartbeatIntervalMin
// of 1s, so the intervals asked for below are due well inside a test's
// lifetime. The maximum is left at the server's own default: lowering it
// would clamp every other suite's watch streams down with it.
func TestWatchHeartbeatInterval(t *testing.T) {
	t.Run("an asked-for interval is advertised and heartbeats", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*gotime.Second)
		defer cancel()

		// Inside the server's bounds, so it is honored as asked.
		stream, init := openWatchAskingFor(ctx, t, 1_500)
		assert.Equal(t, int64(1_500), init.GetHeartbeatIntervalMs())
		assert.True(t, awaitHeartbeat(stream),
			"a stream that asked for a heartbeat received none: %v", stream.Err())
	})

	t.Run("an interval outside the bounds is clamped before it is advertised", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*gotime.Second)
		defer cancel()

		// A client asking for a heartbeat every 10ms must not set the
		// server's load, and must be told the interval it really gets.
		stream, init := openWatchAskingFor(ctx, t, 10)
		assert.Equal(t, int64(1_000), init.GetHeartbeatIntervalMs())
		assert.True(t, awaitHeartbeat(stream),
			"a clamped stream received no heartbeat: %v", stream.Err())
	})

	t.Run("a stream that did not ask is left alone", func(t *testing.T) {
		// Which is byte for byte what a client older than the field sends,
		// and such a client ends its watch on the first heartbeat it sees.
		ctx, cancel := context.WithTimeout(context.Background(), 5*gotime.Second)
		defer cancel()

		stream, init := openWatchAskingFor(ctx, t, 0)
		assert.Zero(t, init.GetHeartbeatIntervalMs(),
			"advertising an interval to a stream that gets no heartbeat would arm its watchdog against nothing")

		// Several times the shortest interval the server would ever resolve
		// passes before the deadline closes the stream, so a heartbeat would
		// have been sent by now.
		assert.False(t, awaitHeartbeat(stream), "a stream that did not ask was sent a heartbeat")
	})
}

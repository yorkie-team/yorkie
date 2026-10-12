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

package rpc

import (
	"context"
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types/events"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend"
)

// TestWatchInitializationAdvertisesHeartbeatInterval verifies that the first
// response of a Watch stream carries the interval the server settled on for
// that stream, in milliseconds. A stream that advertised 0 -- or seconds where
// the field is milliseconds -- would leave the client's idle watchdog unarmed
// or armed a thousand times too long, and the client has nothing else to go on.
func TestWatchInitializationAdvertisesHeartbeatInterval(t *testing.T) {
	inits := []*api.ResourceInit{{
		Init: &api.ResourceInit_ChannelInit{
			ChannelInit: &api.ChannelInit{SessionCount: 1, Seq: 1},
		},
	}}

	advertise := func(t *testing.T, interval gotime.Duration) *api.WatchInitialization {
		t.Helper()
		s := newYorkieServer(context.Background(), &backend.Backend{
			Config: &backend.Config{},
		}, nil)

		var sent *api.WatchResponse
		assert.NoError(t, s.sendWatchInitialization(func(resp *api.WatchResponse) error {
			sent = resp
			return nil
		}, inits, interval))

		return sent.GetInitialization()
	}

	t.Run("the effective interval is advertised in milliseconds", func(t *testing.T) {
		init := advertise(t, 30*gotime.Second)
		assert.NotNil(t, init)
		assert.Equal(t, int64(30_000), init.GetHeartbeatIntervalMs())
		assert.Len(t, init.GetResourceInits(), 1)
	})

	t.Run("a stream without a heartbeat advertises zero", func(t *testing.T) {
		// Which is what tells a client not to apply an idle timeout at all.
		assert.Zero(t, advertise(t, 0).GetHeartbeatIntervalMs())
	})
}

// TestResolveHeartbeatInterval verifies the server honors what a stream asked
// for, within its own bounds. The opt-out cases are the load-bearing ones: a
// client older than the heartbeat leaves the field at 0, and must be served as
// if the feature did not exist, or an upgraded server would cut it off.
func TestResolveHeartbeatInterval(t *testing.T) {
	serverWith := func(cfg *backend.Config) *yorkieServer {
		return newYorkieServer(context.Background(), &backend.Backend{Config: cfg}, nil)
	}

	t.Run("defaults bound what a stream may ask for", func(t *testing.T) {
		s := serverWith(&backend.Config{})
		assert.Equal(t, backend.DefaultWatchHeartbeatIntervalMin, s.heartbeat.Min)
		assert.Equal(t, backend.DefaultWatchHeartbeatIntervalMax, s.heartbeat.Max)

		// An unset field, which is every client older than it.
		assert.Zero(t, s.resolveHeartbeatInterval(0))
		// A negative one cannot arm a timer, so it reads as opting out too.
		assert.Zero(t, s.resolveHeartbeatInterval(-1))

		// Honored as asked, between the bounds.
		assert.Equal(t, 20*gotime.Second, s.resolveHeartbeatInterval(20_000))
		// Clamped up: one client cannot set the server's heartbeat load.
		assert.Equal(t, 5*gotime.Second, s.resolveHeartbeatInterval(1_000))
		// Clamped down.
		assert.Equal(t, 60*gotime.Second, s.resolveHeartbeatInterval(600_000))
		// The bounds themselves are allowed through unchanged.
		assert.Equal(t, 5*gotime.Second, s.resolveHeartbeatInterval(5_000))
		assert.Equal(t, 60*gotime.Second, s.resolveHeartbeatInterval(60_000))
	})

	t.Run("a zero maximum turns the heartbeat off server-wide", func(t *testing.T) {
		s := serverWith(&backend.Config{WatchHeartbeatIntervalMax: "0s"})
		assert.Zero(t, s.resolveHeartbeatInterval(20_000))
	})

	t.Run("the deprecated single interval is ignored", func(t *testing.T) {
		// "0s" must not silence clients that asked: it is what every config
		// copied from the old sample carries.
		off := serverWith(&backend.Config{WatchHeartbeatInterval: "0s"})
		assert.Equal(t, 20*gotime.Second, off.resolveHeartbeatInterval(20_000))

		// A non-zero one must not reach a stream that did not ask: a client
		// older than the heartbeat ends its watch on the first one.
		on := serverWith(&backend.Config{WatchHeartbeatInterval: "30s"})
		assert.Zero(t, on.resolveHeartbeatInterval(0))
		assert.Zero(t, on.resolveHeartbeatInterval(-1))
		assert.Equal(t, 20*gotime.Second, on.resolveHeartbeatInterval(20_000))
	})

	t.Run("configured bounds replace the defaults", func(t *testing.T) {
		s := serverWith(&backend.Config{
			WatchHeartbeatIntervalMin: "10s",
			WatchHeartbeatIntervalMax: "30s",
		})
		assert.Equal(t, 10*gotime.Second, s.resolveHeartbeatInterval(1_000))
		assert.Equal(t, 30*gotime.Second, s.resolveHeartbeatInterval(90_000))
	})
}

// TestStreamMergedEventsSendsHeartbeat verifies that a stream with nothing to
// deliver still sends something, so a client can tell it from a half-open one.
func TestStreamMergedEventsSendsHeartbeat(t *testing.T) {
	s := &yorkieServer{serviceCtx: context.Background()}
	cs := newChannelSub("idle")

	ctx := t.Context()

	sent := make(chan *api.WatchResponse, 4)
	go func() {
		_ = s.streamMergedEvents(
			ctx,
			func(resp *api.WatchResponse) error {
				sent <- resp
				return nil
			},
			nil,
			nil,
			[]channelSub{cs},
			10*gotime.Millisecond,
		)
	}()

	// Two in a row: the first proves the ticker fired, the second that it was
	// re-armed rather than firing once and going quiet.
	for i := range 2 {
		select {
		case resp := <-sent:
			assert.NotNil(t, resp.GetHeartbeat(), "response %d was not a heartbeat", i)
		case <-gotime.After(5 * gotime.Second):
			t.Fatalf("an idle stream sent no heartbeat (%d received)", i)
		}
	}
}

// TestStreamMergedEventsWithoutHeartbeat verifies that a zero interval sends
// no heartbeats, so an operator can turn them off for clients that would read
// an unknown response as a protocol error.
func TestStreamMergedEventsWithoutHeartbeat(t *testing.T) {
	s := &yorkieServer{serviceCtx: context.Background()}
	cs := newChannelSub("quiet")

	ctx := t.Context()

	sent := make(chan *api.WatchResponse, 4)
	go func() {
		_ = s.streamMergedEvents(
			ctx,
			func(resp *api.WatchResponse) error {
				sent <- resp
				return nil
			},
			nil,
			nil,
			[]channelSub{cs},
			0,
		)
	}()

	// An event still gets through; only the heartbeat is off.
	cs.sub.Events() <- events.ChannelEvent{
		Type:         events.ChannelPresenceChanged,
		Publisher:    time.InitialActorID,
		SessionCount: 1,
		Seq:          1,
	}

	select {
	case resp := <-sent:
		assert.Nil(t, resp.GetHeartbeat())
		assert.NotNil(t, resp.GetEvent())
	case <-gotime.After(5 * gotime.Second):
		t.Fatal("an event was not delivered with the heartbeat disabled")
	}

	select {
	case resp := <-sent:
		t.Fatalf("a heartbeat-free stream sent %v", resp)
	case <-gotime.After(100 * gotime.Millisecond):
	}
}

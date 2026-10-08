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
)

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

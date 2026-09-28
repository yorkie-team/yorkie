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
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/pubsub"
)

// streamResp stands in for the generated response types streamEvents is
// instantiated with. Only its pointer-ness matters here.
type streamResp struct {
	seq int64
}

// TestStreamEventsSkipsNilResponses pins the skip contract of streamEvents.
//
// The deprecated WatchChannel handler returns (nil, nil) from its convert
// function for the initial Seq 0 event. Resp is instantiated with a pointer
// type, so that nil is a typed nil pointer rather than a nil interface: a
// guard written as `any(resp) == nil` never fires and the client receives an
// empty response with no body instead of nothing at all.
func TestStreamEventsSkipsNilResponses(t *testing.T) {
	sub := pubsub.NewChannelSubscription(time.InitialActorID)

	// The event that must be skipped, then one that must be delivered.
	sub.Events() <- events.ChannelEvent{Publisher: time.InitialActorID, Seq: 0}
	sub.Events() <- events.ChannelEvent{
		Type:         events.ChannelPresenceChanged,
		Publisher:    time.InitialActorID,
		SessionCount: 1,
		Seq:          1,
	}
	sub.Close()

	var sent []*streamResp
	errCh := make(chan error, 1)
	go func() {
		errCh <- streamEvents(
			context.Background(),
			context.Background(),
			sub,
			func(resp *streamResp) error {
				sent = append(sent, resp)
				return nil
			},
			func(event events.ChannelEvent) (*streamResp, error) {
				if event.Seq == 0 {
					return nil, nil
				}
				return &streamResp{seq: event.Seq}, nil
			},
			nil,
		)
	}()

	select {
	case err := <-errCh:
		// A subscription that pruned itself ends the stream retriably; a clean
		// end would leave the SDK waiting on a stream it never reconnects.
		assert.ErrorIs(t, err, ErrSubscriptionsClosed)
	case <-gotime.After(5 * gotime.Second):
		t.Fatal("streamEvents did not return after its subscription closed")
	}

	assert.Len(t, sent, 1)
	assert.Equal(t, int64(1), sent[0].seq)
}

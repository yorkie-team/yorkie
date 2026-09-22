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

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"

	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server/backend/pubsub"
	"github.com/yorkie-team/yorkie/server/rpc/connecthelper"
)

// runStreamMergedEvents starts streamMergedEvents for the given channel
// subscriptions and returns a channel carrying its return value. Events are
// discarded; these tests only exercise the lifecycle of the merged channel.
func runStreamMergedEvents(subs []channelSub) <-chan error {
	s := &yorkieServer{serviceCtx: context.Background()}

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.streamMergedEvents(
			context.Background(),
			func(*api.WatchResponse) error { return nil },
			nil,
			nil,
			subs,
		)
	}()

	return errCh
}

func newChannelSub(name string) channelSub {
	return channelSub{
		channelKey: key.Key(name),
		sub:        pubsub.NewChannelSubscription(time.InitialActorID),
	}
}

// TestStreamMergedEventsEndsWhenSubscriptionsClose verifies that the Watch
// handler returns once no subscription can deliver another event.
//
// A Subscription closes its own event channel after too many consecutive
// Publish failures (pubsub.Subscription.Publish), which is the only cleanup
// path when the stream handler never unsubscribes. Each fan-in goroutine in
// streamMergedEvents then returns. If nothing closes the merged channel at
// that point, the handler blocks on it forever: the stream stays open, the
// active-stream gauge is never decremented, and the deferred unsubscribe
// never runs.
func TestStreamMergedEventsEndsWhenSubscriptionsClose(t *testing.T) {
	t.Run("returns when its only subscription closes", func(t *testing.T) {
		sub := newChannelSub("single")
		errCh := runStreamMergedEvents([]channelSub{sub})

		sub.sub.Close()

		select {
		case err := <-errCh:
			assert.NoError(t, err)
		case <-gotime.After(5 * gotime.Second):
			t.Fatal("streamMergedEvents did not return after its only " +
				"subscription closed; the stream is a zombie")
		}
	})

	t.Run("waits for the last subscription", func(t *testing.T) {
		first, second := newChannelSub("first"), newChannelSub("second")
		errCh := runStreamMergedEvents([]channelSub{first, second})

		first.sub.Close()

		// One live subscription remains, so the stream must stay open.
		select {
		case err := <-errCh:
			t.Fatalf("streamMergedEvents returned (%v) while a subscription "+
				"was still live", err)
		case <-gotime.After(200 * gotime.Millisecond):
		}

		second.sub.Close()

		select {
		case err := <-errCh:
			assert.NoError(t, err)
		case <-gotime.After(5 * gotime.Second):
			t.Fatal("streamMergedEvents did not return after the last " +
				"subscription closed; the stream is a zombie")
		}
	})
}

// TestSubscribeResourcesRejectsStreamWithoutSubscription verifies that a Watch
// request which would subscribe to nothing is rejected rather than accepted
// into a stream that can never deliver an event.
//
// An empty Resources list is one spelling of that; a descriptor whose resource
// oneof is unset is the other. The type switch in subscribeResources has no
// case for the latter, so skipping it would subscribe to nothing while the
// request looked well formed — and since streamMergedEvents now ends a stream
// that holds no subscription, the client would see the stream close right
// after initialization and retry.
func TestSubscribeResourcesRejectsStreamWithoutSubscription(t *testing.T) {
	s := &yorkieServer{serviceCtx: context.Background()}

	subscribe := func(resources []*api.ResourceDescriptor) error {
		_, _, _, err := s.subscribeResources(
			context.Background(),
			&api.WatchRequest{Resources: resources},
			time.InitialActorID,
			nil,
		)
		return err
	}

	t.Run("no resource at all", func(t *testing.T) {
		err := subscribe(nil)

		assert.ErrorIs(t, err, ErrNoResources)
		assert.Equal(t, connect.CodeInvalidArgument.String(), connecthelper.CodeOf(err))
	})

	t.Run("a descriptor naming no resource", func(t *testing.T) {
		err := subscribe([]*api.ResourceDescriptor{{}})

		assert.ErrorIs(t, err, ErrUnsupportedResource)
		assert.Equal(t, connect.CodeInvalidArgument.String(), connecthelper.CodeOf(err))
	})

	// Such a descriptor is rejected rather than skipped even when the request
	// also carries a valid one: a client that asked to watch two resources and
	// silently watches one has no way to learn which.
	t.Run("a descriptor naming no resource beside a valid one", func(t *testing.T) {
		err := subscribe([]*api.ResourceDescriptor{
			{},
			{Resource: &api.ResourceDescriptor_Document{
				Document: &api.DocumentDescriptor{DocumentId: "000000000000000000000000"},
			}},
		})

		assert.ErrorIs(t, err, ErrUnsupportedResource)
	})
}

// TestStreamMergedEventsEndsWithoutSubscriptions pins the reason
// subscribeResources rejects a request that would subscribe to nothing: such a
// stream is already over when it starts.
func TestStreamMergedEventsEndsWithoutSubscriptions(t *testing.T) {
	errCh := runStreamMergedEvents(nil)

	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-gotime.After(5 * gotime.Second):
		t.Fatal("streamMergedEvents did not return with no subscription to read")
	}
}

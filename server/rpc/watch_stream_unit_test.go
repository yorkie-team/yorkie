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

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/types/events"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server/backend/pubsub"
	"github.com/yorkie-team/yorkie/server/rpc/connecthelper"
)

// runStreamMergedEvents starts streamMergedEvents for the given subscriptions
// and returns a channel carrying its return value. Events are discarded; these
// tests only exercise the lifecycle of the merged channel.
func runStreamMergedEvents(docSubs []docSub, channelSubs []channelSub) <-chan error {
	s := &yorkieServer{serviceCtx: context.Background()}

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.streamMergedEvents(
			context.Background(),
			func(*api.WatchResponse) error { return nil },
			nil,
			docSubs,
			channelSubs,
		)
	}()

	return errCh
}

func newDocSub(id string) docSub {
	return docSub{
		docID: types.ID(id),
		sub:   pubsub.NewDocSubscription(time.InitialActorID),
	}
}

func newChannelSub(name string) channelSub {
	return channelSub{
		channelKey: key.Key(name),
		sub:        pubsub.NewChannelSubscription(time.InitialActorID),
	}
}

// assertEndedBySelfPrune asserts that streamMergedEvents reported the stream as
// retriably broken rather than as a clean end. A clean end is indistinguishable
// from a completed stream to the SDK, which then never reconnects; see
// ErrSubscriptionsClosed.
func assertEndedBySelfPrune(t *testing.T, errCh <-chan error) {
	t.Helper()

	select {
	case err := <-errCh:
		assert.ErrorIs(t, err, ErrSubscriptionsClosed)
		assert.Equal(t, connect.CodeUnavailable.String(), connecthelper.CodeOf(err))
	case <-gotime.After(5 * gotime.Second):
		t.Fatal("streamMergedEvents did not return after its subscriptions " +
			"closed; the stream is a zombie")
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
	// Documents are the dominant case, and their fan-in loop is a separate
	// copy of the one channels use, so both are covered here.
	t.Run("returns when its only document subscription closes", func(t *testing.T) {
		sub := newDocSub("000000000000000000000000")
		errCh := runStreamMergedEvents([]docSub{sub}, nil)

		sub.sub.Close()

		assertEndedBySelfPrune(t, errCh)
	})

	t.Run("returns when its only channel subscription closes", func(t *testing.T) {
		sub := newChannelSub("single")
		errCh := runStreamMergedEvents(nil, []channelSub{sub})

		sub.sub.Close()

		assertEndedBySelfPrune(t, errCh)
	})

	t.Run("waits for the last subscription across both kinds", func(t *testing.T) {
		ds := newDocSub("000000000000000000000000")
		cs := newChannelSub("channel")
		errCh := runStreamMergedEvents([]docSub{ds}, []channelSub{cs})

		ds.sub.Close()

		// One live subscription remains, so the stream must stay open.
		select {
		case err := <-errCh:
			t.Fatalf("streamMergedEvents returned (%v) while a subscription "+
				"was still live", err)
		case <-gotime.After(200 * gotime.Millisecond):
		}

		cs.sub.Close()

		assertEndedBySelfPrune(t, errCh)
	})
}

// TestStreamMergedEventsDeliversQueuedEvents verifies that events already
// buffered in the merged channel reach the client before the close is
// reported. The fan-in goroutines send through merged and only then return, so
// closing merged must not race ahead of the events it already carries.
func TestStreamMergedEventsDeliversQueuedEvents(t *testing.T) {
	s := &yorkieServer{serviceCtx: context.Background()}
	cs := newChannelSub("queued")

	// Queue an event, then close the subscription so the fan-in drains it and
	// returns without any further publisher running.
	cs.sub.Events() <- events.ChannelEvent{
		Type:         events.ChannelPresenceChanged,
		Publisher:    time.InitialActorID,
		SessionCount: 1,
		Seq:          1,
	}
	cs.sub.Close()

	sent := make(chan *api.WatchResponse, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.streamMergedEvents(
			context.Background(),
			func(resp *api.WatchResponse) error {
				sent <- resp
				return nil
			},
			nil,
			nil,
			[]channelSub{cs},
		)
	}()

	select {
	case resp := <-sent:
		event := resp.GetEvent().GetChannelEvent()
		assert.Equal(t, "queued", event.GetChannelKey())
		assert.Equal(t, int64(1), event.GetEvent().GetSessionCount())
	case <-gotime.After(5 * gotime.Second):
		t.Fatal("a queued event was dropped when the subscription closed")
	}

	assertEndedBySelfPrune(t, errCh)
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
	errCh := runStreamMergedEvents(nil, nil)

	assertEndedBySelfPrune(t, errCh)
}

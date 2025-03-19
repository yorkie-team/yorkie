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

package pubsub_test

import (
	"context"
	"fmt"
	gosync "sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/types/events"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/pubsub"
)

func TestPubSub(t *testing.T) {
	idA, err := time.ActorIDFromBytes([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	assert.NoError(t, err)
	idB, err := time.ActorIDFromBytes([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1})
	assert.NoError(t, err)

	t.Run("publish subscribe test", func(t *testing.T) {
		pubSub := pubsub.New()
		refKey := types.DocRefKey{
			ProjectID: types.ID("000000000000000000000000"),
			DocID:     types.ID("000000000000000000000000"),
		}
		docEvent := events.DocEvent{
			Type:      events.DocWatchedEvent,
			Publisher: idB,
			DocRefKey: refKey,
		}

		ctx := context.Background()
		// subscribe the documents by actorA (using basic Subscribe without limit)
		subA, _, err := pubSub.Subscribe(ctx, idA, refKey, 0)
		assert.NoError(t, err)
		defer func() {
			pubSub.Unsubscribe(ctx, refKey, subA)
		}()

		var wg gosync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := <-subA.Events()
			assert.Equal(t, e, docEvent)
		}()

		// publish the event to the documents by actorB
		pubSub.Publish(ctx, idB, docEvent)
		wg.Wait()
	})

	t.Run("max subscribers per document limit exceeded test", func(t *testing.T) {
		pubSub := pubsub.New()
		refKey := types.DocRefKey{
			ProjectID: types.ID("000000000000000000000000"),
			DocID:     types.ID("000000000000000000000000"),
		}

		ctx := context.Background()
		limit := 2

		// subscribe the documents by actorA
		subA, _, err := pubSub.Subscribe(ctx, idA, refKey, limit)
		assert.NoError(t, err)
		defer func() {
			pubSub.Unsubscribe(ctx, refKey, subA)
		}()

		// subscribe the documents by actorB
		subB, _, err := pubSub.Subscribe(ctx, idB, refKey, limit)
		assert.NoError(t, err)
		defer func() {
			pubSub.Unsubscribe(ctx, refKey, subB)
		}()

		// third subscription should fail due to limit
		_, _, err = pubSub.Subscribe(ctx, idB, refKey, limit)
		assert.Error(t, err)
		assert.ErrorIs(t, err, pubsub.ErrTooManySubscribers)
		assert.Equal(t, err.Error(), fmt.Sprintf("%d subscribers allowed per document: subscription limit exceeded", limit))
	})

	t.Run("max subscribers per document limit exceeded concurrent test with multiple actors", func(t *testing.T) {
		pubSub := pubsub.New()
		refKey := types.DocRefKey{
			ProjectID: types.ID("000000000000000000000000"),
			DocID:     types.ID("000000000000000000000000"),
		}

		ctx := context.Background()
		var successCount, failCount atomic.Int32
		limitCount := 5000
		concurrency := limitCount * 2

		// Create multiple different ActorIDs
		actors := make([]*time.ActorID, concurrency)
		for i := range concurrency {
			bytes := make([]byte, 12)
			bytes[11] = byte(i) // Use last byte for uniqueness
			actorID, err := time.ActorIDFromBytes(bytes)
			assert.NoError(t, err)
			actors[i] = actorID
		}

		// Try to subscribe concurrently with different ActorIDs
		var wg gosync.WaitGroup
		subscriptions := make([]*pubsub.Subscription, concurrency)
		for i := range concurrency {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				subs, _, err := pubSub.Subscribe(ctx, actors[idx], refKey, limitCount)
				if err == nil {
					successCount.Add(1)
					subscriptions[idx] = subs
				} else {
					failCount.Add(1)
					assert.ErrorIs(t, err, pubsub.ErrTooManySubscribers)
				}
			}(i)
		}
		wg.Wait()
		defer func() {
			for _, sub := range subscriptions {
				if sub != nil {
					pubSub.Unsubscribe(ctx, refKey, sub)
				}
			}
		}()

		successLen := int(successCount.Load())
		failLen := int(failCount.Load())

		// We expect exactly limitCount successful subscriptions
		assert.Equal(t, limitCount, successLen)
		// And the rest should have failed
		assert.Equal(t, concurrency-limitCount, failLen)
		// Total should equal concurrency
		assert.Equal(t, concurrency, successLen+failLen)
	})
}

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
	gosync "sync"
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
		// subscribe the documents by actorA
		subA, _, err := pubSub.Subscribe(ctx, idA, refKey)
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

	t.Run("connection limit exceeded test", func(t *testing.T) {
		pubSub := pubsub.New()
		refKey := types.DocRefKey{
			ProjectID: types.ID("000000000000000000000000"),
			DocID:     types.ID("000000000000000000000000"),
		}

		ctx := context.Background()

		// check connection limit exceeded
		err = pubSub.IsConnectionLimitExceeded(2, refKey)
		assert.NoError(t, err)

		// subscribe the documents by actorA
		subA, _, err := pubSub.Subscribe(ctx, idA, refKey)
		assert.NoError(t, err)
		defer func() {
			pubSub.Unsubscribe(ctx, refKey, subA)
		}()

		// check connection limit exceeded
		err = pubSub.IsConnectionLimitExceeded(2, refKey)
		assert.NoError(t, err)

		// subscribe the documents by actorB
		subB, _, err := pubSub.Subscribe(ctx, idB, refKey)
		assert.NoError(t, err)
		defer func() {
			pubSub.Unsubscribe(ctx, refKey, subB)
		}()

		// check connection limit exceeded
		err = pubSub.IsConnectionLimitExceeded(2, refKey)
		assert.Error(t, err)
		assert.Equal(t, err.Error(), "2 stream connections allowed per document: connection limit exceeded")
	})
}

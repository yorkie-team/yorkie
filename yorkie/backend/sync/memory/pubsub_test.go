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

package memory_test

import (
	gosync "sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync/memory"
)

func TestPubSub(t *testing.T) {
	actorA := types.Client{ID: &time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}}
	actorB := types.Client{ID: &time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}}

	t.Run("subscribe publish test", func(t *testing.T) {
		pubsub := memory.NewPubSub()
		event := sync.DocEvent{
			Type:      types.DocumentsWatchedEvent,
			DocKey:    t.Name(),
			Publisher: actorB,
		}

		// subscribe the topic by actorA
		subA, _, err := pubsub.Subscribe(actorA, []string{t.Name()})
		assert.NoError(t, err)
		defer func() {
			pubsub.Unsubscribe([]string{t.Name()}, subA)
		}()

		var wg gosync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := <-subA.Events()
			assert.Equal(t, e, event)
		}()

		// publish the event to the topic by actorB
		pubsub.Publish(actorB.ID, t.Name(), event)
		wg.Wait()
	})

	t.Run("subscriptions map test", func(t *testing.T) {
		pubsub := memory.NewPubSub()

		for i := 0; i < 5; i++ {
			_, subs, err := pubsub.Subscribe(actorA, []string{t.Name()})
			assert.NoError(t, err)
			assert.Len(t, subs[t.Name()], i+1)
		}
	})

	t.Run("update subscriber test", func(t *testing.T) {
		pubsub := memory.NewPubSub()

		// empty topic error
		_, err := pubsub.UpdateSubscriber(
			actorA,
			[]string{},
		)
		assert.ErrorIs(t, sync.ErrEmptyTopics, err)

		// when there is no topic subscribed
		updatedTopics, err := pubsub.UpdateSubscriber(
			actorA,
			[]string{"ab"},
		)
		assert.NoError(t, err)
		assert.Empty(t, updatedTopics)

		// update metadata
		actor := types.Client{
			ID:       &time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2},
			Metadata: map[string]string{"name": "actor"},
		}

		topics := []string{t.Name()}
		sub, _, err := pubsub.Subscribe(actor, topics)
		assert.NoError(t, err)

		actor.Metadata = map[string]string{"name": "Yorkie"}
		updatedTopics, err = pubsub.UpdateSubscriber(
			actor,
			[]string{t.Name()},
		)
		assert.NoError(t, err)
		assert.Equal(t, updatedTopics, topics)
		assert.Equal(t, actor.Metadata, sub.Subscriber().Metadata)
	})
}

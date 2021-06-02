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

	t.Run("publish subscribe test", func(t *testing.T) {
		pubSub := memory.NewPubSub()
		event := sync.DocEvent{
			Type:      types.DocumentsWatchedEvent,
			DocKey:    t.Name(),
			Publisher: actorB,
		}

		// subscribe the topic by actorA
		subA, _, err := pubSub.Subscribe(actorA, []string{t.Name()})
		assert.NoError(t, err)
		defer func() {
			pubSub.Unsubscribe([]string{t.Name()}, subA)
		}()

		var wg gosync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := <-subA.Events()
			assert.Equal(t, e, event)
		}()

		// publish the event to the topic by actorB
		pubSub.Publish(actorB.ID, t.Name(), event)
		wg.Wait()
	})

	t.Run("subscriptions map test", func(t *testing.T) {
		pubSub := memory.NewPubSub()

		for i := 0; i < 5; i++ {
			_, subs, err := pubSub.Subscribe(actorA, []string{t.Name()})
			assert.NoError(t, err)
			assert.Len(t, subs[t.Name()], i+1)
		}
	})
}

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
	"context"
	gosync "sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/sync"
	"github.com/yorkie-team/yorkie/server/backend/sync/memory"
)

func TestPubSub(t *testing.T) {
	idA, err := time.ActorIDFromBytes([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	assert.NoError(t, err)
	idB, err := time.ActorIDFromBytes([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1})
	assert.NoError(t, err)

	t.Run("publish subscribe test", func(t *testing.T) {
		pubSub := memory.NewPubSub()
		id := types.ID(t.Name() + "id")
		docEvent := sync.DocEvent{
			Type:       types.DocumentWatchedEvent,
			Publisher:  idB,
			DocumentID: id,
		}

		ctx := context.Background()
		// subscribe the documents by actorA
		subA, err := pubSub.Subscribe(ctx, idA, id)
		assert.NoError(t, err)
		defer func() {
			pubSub.Unsubscribe(ctx, id, subA)
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
}

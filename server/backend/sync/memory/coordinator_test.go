/*
 * Copyright 2023 The Yorkie Authors. All rights reserved.
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
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/sync/memory"
)

func TestCoordinator(t *testing.T) {
	t.Run("subscriptions map test", func(t *testing.T) {
		coordinator := memory.NewCoordinator(nil)
		docID := types.ID(t.Name() + "id")
		ctx := context.Background()

		for i := 0; i < 5; i++ {
			id, err := time.ActorIDFromBytes([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, byte(i)})
			assert.NoError(t, err)

			_, peers, err := coordinator.Subscribe(ctx, types.Client{ID: id}, docID)
			assert.NoError(t, err)
			assert.Len(t, peers, i+1)
		}
	})
}

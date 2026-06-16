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

package change_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/presence/inner"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func TestChange(t *testing.T) {
	t.Run("HasOperations distinguishes presence-only changes", func(t *testing.T) {
		id := change.NewID(0, 0, 0, time.InitialActorID, time.NewVersionVector())

		presenceOnly := change.New(id, "", nil, &inner.Change{
			ChangeType: inner.Put,
			Presence:   inner.Presence{"foo": "bar"},
		})
		assert.False(t, presenceOnly.HasOperations())
		assert.NotNil(t, presenceOnly.PresenceChange())

		empty := change.New(id, "", nil, nil)
		assert.False(t, empty.HasOperations())
		assert.Nil(t, empty.PresenceChange())
	})

	t.Run("SetPresenceChange(nil) preserves operations", func(t *testing.T) {
		id := change.NewID(0, 0, 0, time.InitialActorID, time.NewVersionVector())
		rmOp := operations.NewRemove(time.InitialTicket, time.InitialTicket, time.InitialTicket)

		c := change.New(id, "", []operations.Operation{rmOp}, &inner.Change{
			ChangeType: inner.Put,
			Presence:   inner.Presence{"x": "y"},
		})
		assert.True(t, c.HasOperations())
		assert.NotNil(t, c.PresenceChange())

		c.SetPresenceChange(nil)
		assert.True(t, c.HasOperations())
		assert.Nil(t, c.PresenceChange())
		assert.Len(t, c.Operations(), 1)
	})
}

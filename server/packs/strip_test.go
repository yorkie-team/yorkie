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

package packs

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/presence/inner"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func newID() change.ID {
	return change.NewID(0, 0, 0, time.InitialActorID, time.NewVersionVector())
}

func newPresenceChange() *inner.Change {
	return &inner.Change{
		ChangeType: inner.Put,
		Presence:   inner.Presence{"k": "v"},
	}
}

func newRemoveOp() operations.Operation {
	return operations.NewRemove(time.InitialTicket, time.InitialTicket, time.InitialTicket)
}

func TestStripPresenceChanges(t *testing.T) {
	t.Run("empty input is returned as-is", func(t *testing.T) {
		out := stripPresenceChanges(nil)
		assert.Empty(t, out)

		out = stripPresenceChanges([]*change.Change{})
		assert.Empty(t, out)
	})

	t.Run("presence-only changes drop in full", func(t *testing.T) {
		changes := []*change.Change{
			change.New(newID(), "", nil, newPresenceChange()),
			change.New(newID(), "", nil, newPresenceChange()),
		}
		out := stripPresenceChanges(changes)
		assert.Empty(t, out)
	})

	t.Run("mixed changes keep operations and lose presence", func(t *testing.T) {
		c := change.New(newID(), "", []operations.Operation{newRemoveOp()}, newPresenceChange())
		out := stripPresenceChanges([]*change.Change{c})

		assert.Len(t, out, 1)
		assert.True(t, out[0].HasOperations())
		assert.Nil(t, out[0].PresenceChange())
	})

	t.Run("ops-only changes pass through untouched", func(t *testing.T) {
		c := change.New(newID(), "", []operations.Operation{newRemoveOp()}, nil)
		out := stripPresenceChanges([]*change.Change{c})

		assert.Len(t, out, 1)
		assert.True(t, out[0].HasOperations())
		assert.Nil(t, out[0].PresenceChange())
	})

	t.Run("mixed batch keeps only the survivor changes", func(t *testing.T) {
		presenceOnly := change.New(newID(), "", nil, newPresenceChange())
		mixed := change.New(newID(), "", []operations.Operation{newRemoveOp()}, newPresenceChange())
		opsOnly := change.New(newID(), "", []operations.Operation{newRemoveOp()}, nil)

		out := stripPresenceChanges([]*change.Change{presenceOnly, mixed, opsOnly})
		assert.Len(t, out, 2)
		for _, c := range out {
			assert.True(t, c.HasOperations())
			assert.Nil(t, c.PresenceChange())
		}
	})
}

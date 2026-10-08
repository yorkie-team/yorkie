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

// presenceChangeOf builds a change that only moves presence, the shape a
// cursor update takes: a server_seq, no operations.
func presenceChangeOf(clientSeq uint32) *change.Change {
	id := change.NewID(clientSeq, 0, int64(clientSeq), time.InitialActorID, time.NewVersionVector())
	return change.New(id, "", nil, &inner.Change{
		ChangeType: inner.Put,
		Presence:   inner.Presence{"cursor": "1"},
	})
}

// operationChangeOf builds a change carrying one operation.
func operationChangeOf(clientSeq uint32) *change.Change {
	id := change.NewID(clientSeq, 0, int64(clientSeq), time.InitialActorID, time.NewVersionVector())
	executedAt := time.NewTicket(int64(clientSeq), 0, time.InitialActorID)
	ops := []operations.Operation{operations.NewRemove(time.InitialTicket, time.InitialTicket, executedAt)}
	return change.New(id, "", ops, nil)
}

// TestHasOperations pins the gate that keeps auto revisions off presence
// traffic: a snapshot interval filled only with presence changes records no
// revision, because such a revision would duplicate the previous one (#2152).
func TestHasOperations(t *testing.T) {
	t.Run("no change at all", func(t *testing.T) {
		assert.False(t, hasOperations(nil))
	})

	t.Run("presence-only changes", func(t *testing.T) {
		assert.False(t, hasOperations([]*change.Change{
			presenceChangeOf(1),
			presenceChangeOf(2),
		}))
	})

	t.Run("one operation among presence changes", func(t *testing.T) {
		assert.True(t, hasOperations([]*change.Change{
			presenceChangeOf(1),
			operationChangeOf(2),
			presenceChangeOf(3),
		}))
	})
}

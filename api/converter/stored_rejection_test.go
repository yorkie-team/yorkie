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

package converter_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// A rejection added at the wire boundary applies retroactively to every change
// already persisted under the looser rules, and a stored change that cannot be
// decoded makes its document permanently unloadable -- the read path has no
// other source for it, and every client's pull fails with it. The stored path
// therefore drops what the wire path refuses.
func TestStoredOperationsSurviveWireRejections(t *testing.T) {
	actor := time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	ticket := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }

	// A payload arriving with a removedAt that does not follow its createdAt:
	// ErrInvalidElementTicket on the wire.
	member, err := crdt.NewPrimitive("v", ticket(2))
	assert.NoError(t, err)
	member.SetRemovedAt(ticket(2))

	obj := crdt.NewObject(crdt.NewElementRHT(), ticket(1))
	obj.Set("a", member)

	pbOps, err := converter.ToOperations([]operations.Operation{
		operations.NewSet(ticket(1), "k", obj, ticket(9)),
	})
	assert.NoError(t, err)

	_, err = converter.FromOperations(pbOps)
	assert.ErrorIs(t, err, converter.ErrInvalidElementTicket)

	ops, err := converter.FromStoredOperations(pbOps)
	assert.NoError(t, err)
	assert.Empty(t, ops)

	assert.Empty(t, converter.SanitizeStoredOperations(pbOps))
}

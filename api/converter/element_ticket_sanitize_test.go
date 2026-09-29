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

// An element payload carries createdAt, movedAt and removedAt as three
// independently decoded tickets, and nothing downstream re-derives them:
// Change.SetActor rewrites only the operation's own executedAt. A triple no
// replica could have issued is therefore a crafted payload, and the converter
// is where it stops -- before the CRDT has to survive it.
func TestSetElementRejectsImpossibleTickets(t *testing.T) {
	actor := time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	ticket := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }

	// obj holds one member created at lamport 2, so obj.Set leaves its
	// movedAt equal to its createdAt.
	build := func(poison func(*crdt.Primitive)) *crdt.Object {
		obj := crdt.NewObject(crdt.NewElementRHT(), ticket(1))
		member, err := crdt.NewPrimitive("v", ticket(2))
		assert.NoError(t, err)
		obj.Set("a", member)
		poison(member)
		return obj
	}

	decode := func(elem crdt.Element) error {
		pbOps, err := converter.ToOperations([]operations.Operation{
			operations.NewSet(ticket(1), "k", elem, ticket(9)),
		})
		assert.NoError(t, err)

		_, err = converter.FromOperations(pbOps)
		return err
	}

	for _, tc := range []struct {
		name   string
		poison func(*crdt.Primitive)
	}{{
		// Element.Remove refuses a ticket that does not follow createdAt, and
		// so does DeleteByCreatedAt: an element that arrives with this triple
		// can never be tombstoned, moved to GC or purged.
		name:   "removed_at does not follow created_at",
		poison: func(p *crdt.Primitive) { p.SetRemovedAt(ticket(2)) },
	}} {
		t.Run(tc.name, func(t *testing.T) {
			err := decode(build(tc.poison))
			assert.ErrorIs(t, err, converter.ErrInvalidElementTicket)
		})
	}

	t.Run("tickets a replica can issue survive", func(t *testing.T) {
		assert.NoError(t, decode(build(func(p *crdt.Primitive) {
			p.SetRemovedAt(ticket(3))
		})))
	})

	// Undo re-identifies the value of an Add/ArraySet reverse with a freshly
	// issued createdAt and leaves the copy's older movedAt alone
	// (Document.executeUndoRedo), so a movedAt preceding createdAt is a shape
	// replicas really emit -- the boundary must let it through.
	t.Run("moved_at preceding created_at survives", func(t *testing.T) {
		assert.NoError(t, decode(build(func(p *crdt.Primitive) {
			p.SetMovedAt(ticket(1))
		})))
	})
}

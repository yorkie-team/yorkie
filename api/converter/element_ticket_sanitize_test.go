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

	// ElementRHT anchors both the LWW comparison and the eviction on an
	// occupant's positionedAt, but Element.Remove only accepts a ticket after
	// its createdAt. A member whose movedAt precedes its createdAt loses the key
	// to any later Set whose ticket falls between the two without being
	// tombstoned: it stays live, unreachable by key and charged to Live. No
	// replica places an object member that way -- a member is positioned by
	// the Set that won its key, whose ticket is never older than the value.
	t.Run("an object member whose moved_at precedes created_at", func(t *testing.T) {
		err := decode(build(func(p *crdt.Primitive) { p.SetMovedAt(ticket(1)) }))
		assert.ErrorIs(t, err, converter.ErrInvalidElementTicket)
	})

	// Undo re-identifies the value of an Add/ArraySet reverse with a freshly
	// issued createdAt and leaves the copy's older movedAt alone
	// (Document.executeUndoRedo). That element lives in an array, and a payload
	// can carry it nested once its container is restored, so an array element
	// with a movedAt preceding its createdAt is a shape replicas really emit --
	// the boundary must let it through.
	t.Run("an array element whose moved_at precedes created_at survives", func(t *testing.T) {
		arr := crdt.NewArray(crdt.NewRGATreeList(), ticket(1))
		elem, err := crdt.NewPrimitive("v", ticket(3))
		assert.NoError(t, err)
		assert.NoError(t, arr.Add(elem))
		elem.SetMovedAt(ticket(2))
		assert.NoError(t, decode(arr))
	})

	// The value of a Set is positioned at the Set's executedAt when it wins,
	// so a value created after the ticket that places it is the other way to
	// an occupant positioned before its own createdAt. The json layer issues
	// one ticket for both, and an undo restores an older value under a newer
	// ticket; neither yields a value newer than its Set.
	t.Run("a set value created after the set", func(t *testing.T) {
		value, err := crdt.NewPrimitive("v", ticket(9))
		assert.NoError(t, err)
		pbOps, err := converter.ToOperations([]operations.Operation{
			operations.NewSet(ticket(1), "k", value, ticket(5)),
		})
		assert.NoError(t, err)
		_, err = converter.FromOperations(pbOps)
		assert.ErrorIs(t, err, converter.ErrInvalidElementTicket)
	})
}

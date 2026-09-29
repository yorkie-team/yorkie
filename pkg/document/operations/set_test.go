/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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

package operations_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/resource"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func TestSet(t *testing.T) {
	t.Run("LWW loser should be registered in gcElementPairMap", func(t *testing.T) {
		// Setup: create root with an empty object.
		root := crdt.NewRoot(crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket))

		actorA, _ := time.ActorIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaa")
		actorB, _ := time.ActorIDFromHex("bbbbbbbbbbbbbbbbbbbbbbbb")

		// actorB > actorA, so actorB wins LWW when lamport is equal.
		ticketA := time.NewTicket(1, 0, actorA)
		ticketB := time.NewTicket(1, 0, actorB)

		// First Set: actorA sets "key" = 1.
		valueA, err := crdt.NewPrimitive(1, ticketA)
		assert.NoError(t, err)
		setA := operations.NewSet(time.InitialTicket, "key", valueA, ticketA)
		_, err = setA.Execute(root, operations.OpSourceRemote, time.NewVersionVector())
		assert.NoError(t, err)
		assert.Equal(t, 0, root.GarbageLen())

		// Second Set: actorB sets "key" = 2 (actorB wins, actorA loses).
		valueB, err := crdt.NewPrimitive(2, ticketB)
		assert.NoError(t, err)
		setB := operations.NewSet(time.InitialTicket, "key", valueB, ticketB)
		_, err = setB.Execute(root, operations.OpSourceRemote, time.NewVersionVector())
		assert.NoError(t, err)
		// valueA should be registered as garbage (it was removed by the winner).
		assert.Equal(t, 1, root.GarbageLen())
		assert.Equal(t, `{"key":2}`, root.Object().Marshal())

		// Third Set: actorA sets "key" = 3 (actorA loses to actorB's value).
		ticketA2 := time.NewTicket(1, 1, actorA)
		valueA2, err := crdt.NewPrimitive(3, ticketA2)
		assert.NoError(t, err)
		setA2 := operations.NewSet(time.InitialTicket, "key", valueA2, ticketA2)
		_, err = setA2.Execute(root, operations.OpSourceRemote, time.NewVersionVector())
		assert.NoError(t, err)
		// valueA2 should also be registered as garbage (it lost LWW to valueB).
		// Before the fix, this was 1 because the LWW loser was not registered.
		assert.Equal(t, 2, root.GarbageLen())
		assert.Equal(t, `{"key":2}`, root.Object().Marshal())
	})

	t.Run("a refused loser is not booked into the root", func(t *testing.T) {
		// A value whose createdAt does not follow the ticket that beat it
		// cannot be tombstoned, so the object refuses it. Booking it anyway
		// would charge docSize.Live for an element in no container and point
		// elementMap at it, with nothing able to collect either.
		root := crdt.NewRoot(crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket))
		actorA, _ := time.ActorIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaa")
		actorB, _ := time.ActorIDFromHex("bbbbbbbbbbbbbbbbbbbbbbbb")

		occupant, err := crdt.NewPrimitive(1, time.NewTicket(6, 0, actorA))
		assert.NoError(t, err)
		_, err = operations.NewSet(
			time.InitialTicket, "key", occupant, time.NewTicket(6, 0, actorA),
		).Execute(root, operations.OpSourceRemote, time.NewVersionVector())
		assert.NoError(t, err)

		before := root.DocSize()
		elements := root.ElementMapLen()

		// createdAt is ahead of the executedAt that loses the comparison,
		// which only a crafted or replayed change can produce.
		crafted, err := crdt.NewPrimitive(2, time.NewTicket(time.MaxLamport, 0, actorB))
		assert.NoError(t, err)
		result, err := operations.NewSet(
			time.InitialTicket, "key", crafted, time.NewTicket(5, 0, actorB),
		).Execute(root, operations.OpSourceLocal, time.NewVersionVector())
		assert.NoError(t, err)

		assert.False(t, result.Observable, "a refused Set reported a change")
		assert.Nil(t, result.Reverse, "a refused Set produced a reverse")
		assert.Equal(t, `{"key":1}`, root.Object().Marshal())
		assert.Equal(t, before, root.DocSize())
		assert.Equal(t, elements, root.ElementMapLen())
		assert.Nil(t, root.FindByCreatedAt(crafted.CreatedAt()),
			"a refused value took over its createdAt's elementMap slot")
		assert.Equal(t, 0, root.GarbageLen())
	})

	t.Run("a pre-removed value does not take a live element's slot", func(t *testing.T) {
		// A createdAt identifies an element in the whole document, not in one
		// object: Root.elementMap is keyed by it. ElementRHT refuses a loser
		// whose createdAt one of its own live nodes answers to, but the live
		// copy here sits in another container, so only Set.Execute can see the
		// collision. The value arrives already removed, which is what lets it
		// skip the tombstoning in the losing branch and be indexed anyway.
		root := crdt.NewRoot(crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket))
		actorA, _ := time.ActorIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaa")
		ticket := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actorA) }
		vector := time.NewVersionVector()

		inner := crdt.NewObject(crdt.NewElementRHT(), ticket(1))
		_, err := operations.NewSet(
			time.InitialTicket, "inner", inner, ticket(1),
		).Execute(root, operations.OpSourceRemote, vector)
		assert.NoError(t, err)

		member, err := crdt.NewPrimitive(1, ticket(2))
		assert.NoError(t, err)
		_, err = operations.NewSet(
			inner.CreatedAt(), "x", member, ticket(2),
		).Execute(root, operations.OpSourceRemote, vector)
		assert.NoError(t, err)
		live := root.FindByCreatedAt(ticket(2))
		assert.NotNil(t, live)

		occupant, err := crdt.NewPrimitive(9, ticket(6))
		assert.NoError(t, err)
		_, err = operations.NewSet(
			time.InitialTicket, "key", occupant, ticket(6),
		).Execute(root, operations.OpSourceRemote, vector)
		assert.NoError(t, err)

		before := root.DocSize()
		elements := root.ElementMapLen()

		crafted, err := crdt.NewPrimitive(2, ticket(2))
		assert.NoError(t, err)
		crafted.SetRemovedAt(ticket(3))
		result, err := operations.NewSet(
			time.InitialTicket, "key", crafted, ticket(5),
		).Execute(root, operations.OpSourceRemote, vector)
		assert.NoError(t, err)

		assert.False(t, result.Observable, "a refused Set reported a change")
		assert.Same(t, live, root.FindByCreatedAt(ticket(2)),
			"a pre-removed value took the live element's elementMap slot")
		assert.Equal(t, `{"inner":{"x":1},"key":9}`, root.Object().Marshal())
		assert.Equal(t, before, root.DocSize())
		assert.Equal(t, elements, root.ElementMapLen())
		assert.Equal(t, 0, root.GarbageLen())
	})

	t.Run("a pre-removed value is judged the same before and after collection", func(t *testing.T) {
		// The guard above looks the value's createdAt up in Root.elementMap. A
		// tombstone leaves that map when it is purged, and each replica and the
		// server collect on their own schedule, so a guard that also fired on a
		// tombstone would skip the Set on a replica still holding it and apply
		// it on one that had collected it.
		actorA, _ := time.ActorIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaa")
		ticket := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actorA) }

		apply := func(collect bool) (bool, resource.DataSize) {
			root := crdt.NewRoot(crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket))
			vector := time.NewVersionVector()

			inner := crdt.NewObject(crdt.NewElementRHT(), ticket(1))
			_, err := operations.NewSet(
				time.InitialTicket, "inner", inner, ticket(1),
			).Execute(root, operations.OpSourceRemote, vector)
			assert.NoError(t, err)
			member, err := crdt.NewPrimitive(1, ticket(2))
			assert.NoError(t, err)
			_, err = operations.NewSet(
				inner.CreatedAt(), "x", member, ticket(2),
			).Execute(root, operations.OpSourceRemote, vector)
			assert.NoError(t, err)
			_, err = operations.NewRemove(
				inner.CreatedAt(), ticket(2), ticket(3),
			).Execute(root, operations.OpSourceRemote, vector)
			assert.NoError(t, err)

			if collect {
				max := time.NewVersionVector()
				max.Set(actorA, time.MaxLamport)
				n, err := root.GarbageCollect(max)
				assert.NoError(t, err)
				assert.Equal(t, 1, n)
			}

			before := root.DocSize().Live
			crafted, err := crdt.NewPrimitive(2, ticket(2))
			assert.NoError(t, err)
			crafted.SetRemovedAt(ticket(4))
			result, err := operations.NewSet(
				time.InitialTicket, "key", crafted, ticket(5),
			).Execute(root, operations.OpSourceRemote, vector)
			assert.NoError(t, err)
			after := root.DocSize().Live
			return result.Observable, resource.DataSize{
				Data: after.Data - before.Data,
				Meta: after.Meta - before.Meta,
			}
		}

		heldApplied, heldDelta := apply(false)
		collectedApplied, collectedDelta := apply(true)
		assert.Equal(t, collectedApplied, heldApplied,
			"the Set applied on one replica and was skipped on the other")
		assert.Equal(t, collectedDelta, heldDelta)
	})

	t.Run("a Set retires only its own object's collection entry", func(t *testing.T) {
		// The entry retired for a restore has to be the tombstone this Set
		// displaced. An entry another container registered under the same
		// createdAt still resolves to its own tombstone, so retiring it would
		// release a charge the document still carries and leave a tombstone
		// nothing can collect.
		root := crdt.NewRoot(crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket))
		actorA, _ := time.ActorIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaa")
		ticket := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actorA) }
		vector := time.NewVersionVector()

		inner := crdt.NewObject(crdt.NewElementRHT(), ticket(1))
		_, err := operations.NewSet(
			time.InitialTicket, "inner", inner, ticket(1),
		).Execute(root, operations.OpSourceRemote, vector)
		assert.NoError(t, err)

		member, err := crdt.NewPrimitive(1, ticket(2))
		assert.NoError(t, err)
		_, err = operations.NewSet(
			inner.CreatedAt(), "x", member, ticket(2),
		).Execute(root, operations.OpSourceRemote, vector)
		assert.NoError(t, err)

		_, err = operations.NewRemove(
			inner.CreatedAt(), ticket(2), ticket(3),
		).Execute(root, operations.OpSourceRemote, vector)
		assert.NoError(t, err)
		assert.Equal(t, 1, root.GarbageLen())
		gcSize := root.DocSize().GC

		// A Set on the root object carrying the same createdAt. It wins its
		// own key, but the collection entry under that createdAt belongs to
		// inner and nothing has displaced the tombstone it names.
		value, err := crdt.NewPrimitive(2, ticket(2))
		assert.NoError(t, err)
		_, err = operations.NewSet(
			time.InitialTicket, "key", value, ticket(7),
		).Execute(root, operations.OpSourceRemote, vector)
		assert.NoError(t, err)

		assert.Equal(t, 1, root.GarbageLen(),
			"another container's collection entry was retired")
		assert.Equal(t, gcSize, root.DocSize().GC)
	})

	t.Run("re-applying a Set leaves the tombstone collectable", func(t *testing.T) {
		// The same Set applied twice around a Remove: the duplicate carries
		// the tombstone's createdAt, so it loses and is refused. Retiring the
		// tombstone's collection entry for it would leave a member that is
		// still in the object uncollectable and charged to nothing.
		root := crdt.NewRoot(crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket))
		actorA, _ := time.ActorIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaa")
		ticket := time.NewTicket(1, 0, actorA)

		value, err := crdt.NewPrimitive(1, ticket)
		assert.NoError(t, err)
		set := operations.NewSet(time.InitialTicket, "key", value, ticket)
		_, err = set.Execute(root, operations.OpSourceRemote, time.NewVersionVector())
		assert.NoError(t, err)

		_, err = operations.NewRemove(
			time.InitialTicket, ticket, time.NewTicket(2, 0, actorA),
		).Execute(root, operations.OpSourceRemote, time.NewVersionVector())
		assert.NoError(t, err)
		assert.Equal(t, 1, root.GarbageLen())
		gcSize := root.DocSize().GC

		// The duplicate ties the tombstone's positionedAt, so it loses.
		_, err = set.Execute(root, operations.OpSourceRemote, time.NewVersionVector())
		assert.NoError(t, err)

		assert.Equal(t, 1, root.GarbageLen(), "the tombstone's collection entry was retired")
		assert.Equal(t, gcSize, root.DocSize().GC)

		vector := time.NewVersionVector()
		vector.Set(actorA, time.MaxLamport)
		n, err := root.GarbageCollect(vector)
		assert.NoError(t, err)
		assert.Equal(t, 1, n)
		assert.Equal(t, 0, root.GarbageLen())
	})
}

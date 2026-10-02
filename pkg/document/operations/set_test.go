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

	t.Run("a Set may not take the createdAt of a live element elsewhere", func(t *testing.T) {
		// The value of a pushed Set carries its createdAt straight from the
		// wire (fromSet). A createdAt naming a live element in another
		// container would re-point Root.elementMap at the pushed value, so
		// every later operation addressed at the victim resolves to the
		// attacker's element instead -- permanently, since the server rebuilds
		// its snapshots by replaying the same change log.
		actor, _ := time.ActorIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaa")
		root := crdt.NewRoot(crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket))

		nested := crdt.NewObject(crdt.NewElementRHT(), time.NewTicket(1, 0, actor))
		setNested := operations.NewSet(time.InitialTicket, "nested", nested, nested.CreatedAt())
		_, err := setNested.Execute(root, operations.OpSourceRemote, time.NewVersionVector())
		assert.NoError(t, err)

		victim, err := crdt.NewPrimitive("victim", time.NewTicket(2, 0, actor))
		assert.NoError(t, err)
		setVictim := operations.NewSet(nested.CreatedAt(), "victim", victim, victim.CreatedAt())
		_, err = setVictim.Execute(root, operations.OpSourceRemote, time.NewVersionVector())
		assert.NoError(t, err)

		// A Set on the root object whose value reuses the nested member's
		// createdAt, with a newer executedAt so it wins every LWW comparison.
		forged, err := crdt.NewPrimitive("forged", victim.CreatedAt())
		assert.NoError(t, err)
		setForged := operations.NewSet(time.InitialTicket, "stolen", forged, time.NewTicket(9, 0, actor))
		_, err = setForged.Execute(root, operations.OpSourceRemote, time.NewVersionVector())
		assert.ErrorIs(t, err, operations.ErrOperationSkipped)

		// Set.Execute registers a deep copy, so identity is checked by value.
		assert.Equal(t, `"victim"`, root.FindByCreatedAt(victim.CreatedAt()).Marshal(),
			"the forged value took the victim's identity")
		assert.Equal(t, `{"nested":{"victim":"victim"}}`, root.Object().Marshal())
	})

	t.Run("a Set may still restore a tombstone under its own createdAt", func(t *testing.T) {
		// The guard above must not reach undo of a Remove, which re-inserts
		// the removed element under its original createdAt.
		actor, _ := time.ActorIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaa")
		root := crdt.NewRoot(crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket))

		value, err := crdt.NewPrimitive("v", time.NewTicket(1, 0, actor))
		assert.NoError(t, err)
		set := operations.NewSet(time.InitialTicket, "key", value, value.CreatedAt())
		_, err = set.Execute(root, operations.OpSourceRemote, time.NewVersionVector())
		assert.NoError(t, err)

		remove := operations.NewRemove(time.InitialTicket, value.CreatedAt(), time.NewTicket(2, 0, actor))
		_, err = remove.Execute(root, operations.OpSourceRemote, time.NewVersionVector())
		assert.NoError(t, err)
		assert.Equal(t, `{}`, root.Object().Marshal())

		restored, err := crdt.NewPrimitive("v", value.CreatedAt())
		assert.NoError(t, err)
		restore := operations.NewSet(time.InitialTicket, "key", restored, time.NewTicket(3, 0, actor))
		_, err = restore.Execute(root, operations.OpSourceRemote, time.NewVersionVector())
		assert.NoError(t, err)
		assert.Equal(t, `{"key":"v"}`, root.Object().Marshal())
	})
}

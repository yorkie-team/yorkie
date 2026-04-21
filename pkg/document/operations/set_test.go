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
		err = setA.Execute(root, time.NewVersionVector())
		assert.NoError(t, err)
		assert.Equal(t, 0, root.GarbageLen())

		// Second Set: actorB sets "key" = 2 (actorB wins, actorA loses).
		valueB, err := crdt.NewPrimitive(2, ticketB)
		assert.NoError(t, err)
		setB := operations.NewSet(time.InitialTicket, "key", valueB, ticketB)
		err = setB.Execute(root, time.NewVersionVector())
		assert.NoError(t, err)
		// valueA should be registered as garbage (it was removed by the winner).
		assert.Equal(t, 1, root.GarbageLen())
		assert.Equal(t, `{"key":2}`, root.Object().Marshal())

		// Third Set: actorA sets "key" = 3 (actorA loses to actorB's value).
		ticketA2 := time.NewTicket(1, 1, actorA)
		valueA2, err := crdt.NewPrimitive(3, ticketA2)
		assert.NoError(t, err)
		setA2 := operations.NewSet(time.InitialTicket, "key", valueA2, ticketA2)
		err = setA2.Execute(root, time.NewVersionVector())
		assert.NoError(t, err)
		// valueA2 should also be registered as garbage (it lost LWW to valueB).
		// Before the fix, this was 1 because the LWW loser was not registered.
		assert.Equal(t, 2, root.GarbageLen())
		assert.Equal(t, `{"key":2}`, root.Object().Marshal())
	})
}

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

package crdt_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func TestElementRHT(t *testing.T) {
	t.Run("should not produce duplicate keys on concurrent set with earlier timestamp", func(t *testing.T) {
		rht := crdt.NewElementRHT()

		actorA := time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
		actorB := time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}

		// Client A sets "color" at lamport=2 (wins LWW)
		ticketA := time.NewTicket(2, 0, actorA)
		valueA, err := crdt.NewPrimitive("red", ticketA)
		assert.NoError(t, err)
		rht.Set("color", valueA)

		// Client B's operation arrives with earlier timestamp lamport=1 (loses LWW)
		ticketB := time.NewTicket(1, 0, actorB)
		valueB, err := crdt.NewPrimitive("blue", ticketB)
		assert.NoError(t, err)
		rht.Set("color", valueB)

		// Verify via Object: Members() should have exactly one "color" key
		obj := crdt.NewObject(rht, time.InitialTicket)
		members := obj.Members()
		assert.Len(t, members, 1)
		assert.Equal(t, `"red"`, members["color"].Marshal())

		// Also verify via RHTNodes: only one non-removed node with key "color"
		nonRemovedKeys := make(map[string]int)
		for _, node := range obj.RHTNodes() {
			if node.Element().RemovedAt() == nil {
				nonRemovedKeys[node.Key()]++
			}
		}
		assert.Equal(t, 1, nonRemovedKeys["color"], "should have exactly one non-removed node for 'color'")
	})

	t.Run("should handle multiple concurrent sets on the same key", func(t *testing.T) {
		rht := crdt.NewElementRHT()

		actor1 := time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
		actor2 := time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}
		actor3 := time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 3}

		// Set initial value at lamport=3 (wins)
		ticket1 := time.NewTicket(3, 0, actor1)
		value1, err := crdt.NewPrimitive("first", ticket1)
		assert.NoError(t, err)
		rht.Set("key", value1)

		// Late-arriving operation at lamport=1
		ticket2 := time.NewTicket(1, 0, actor2)
		value2, err := crdt.NewPrimitive("second", ticket2)
		assert.NoError(t, err)
		rht.Set("key", value2)

		// Another late-arriving operation at lamport=2
		ticket3 := time.NewTicket(2, 0, actor3)
		value3, err := crdt.NewPrimitive("third", ticket3)
		assert.NoError(t, err)
		rht.Set("key", value3)

		obj := crdt.NewObject(rht, time.InitialTicket)

		// Members should have exactly one "key"
		members := obj.Members()
		assert.Len(t, members, 1)
		assert.Equal(t, `"first"`, members["key"].Marshal())

		// Only one non-removed node with key "key"
		nonRemovedCount := 0
		for _, node := range obj.RHTNodes() {
			if node.Element().RemovedAt() == nil {
				nonRemovedCount++
			}
		}
		assert.Equal(t, 1, nonRemovedCount, "should have exactly one non-removed node")
	})
}

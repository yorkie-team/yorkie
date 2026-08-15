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

	t.Run("restore via SetWithExecutedAt converges regardless of apply order", func(t *testing.T) {
		// Regression test: SetWithExecutedAt's LWW tie-break used to compare
		// against the current occupant's createdAt instead of its
		// positionedAt (movedAt, falling back to createdAt). A value
		// restored by undo/redo keeps its original createdAt but is given a
		// fresh movedAt via its executedAt ticket; comparing createdAt let a
		// third write with a ticket between the restored value's original
		// createdAt and its new movedAt win on one replica but not the
		// other, diverging the two.
		//
		// V1@t1 is created, then overwritten by V2@t5. V1 is then restored
		// under its original createdAt (t1) but a fresh executedAt (t9), as
		// undo/redo does. A concurrent write V3@t7 -- with a ticket between
		// V1's old createdAt and its new positionedAt -- must lose to the
		// restored V1 on both replicas, however the two events are ordered.
		actorA := time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
		actorB := time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}

		t1 := time.NewTicket(1, 0, actorA)
		t5 := time.NewTicket(5, 0, actorA)
		t7 := time.NewTicket(7, 0, actorB)
		t9 := time.NewTicket(9, 0, actorA)

		newReplica := func() *crdt.ElementRHT {
			rht := crdt.NewElementRHT()
			v1, err := crdt.NewPrimitive("v1", t1)
			assert.NoError(t, err)
			rht.Set("key", v1)
			v2, err := crdt.NewPrimitive("v2", t5)
			assert.NoError(t, err)
			rht.Set("key", v2)
			return rht
		}

		// Replica A: the restore (V1 under executedAt=t9) is applied before
		// the concurrent V3@t7 write.
		replicaA := newReplica()
		v1RestoredA, err := crdt.NewPrimitive("v1", t1)
		assert.NoError(t, err)
		replicaA.SetWithExecutedAt("key", v1RestoredA, t9)
		v3A, err := crdt.NewPrimitive("v3", t7)
		assert.NoError(t, err)
		replicaA.SetWithExecutedAt("key", v3A, t7)

		// Replica B: the same two writes, opposite order.
		replicaB := newReplica()
		v3B, err := crdt.NewPrimitive("v3", t7)
		assert.NoError(t, err)
		replicaB.SetWithExecutedAt("key", v3B, t7)
		v1RestoredB, err := crdt.NewPrimitive("v1", t1)
		assert.NoError(t, err)
		replicaB.SetWithExecutedAt("key", v1RestoredB, t9)

		assert.Equal(t, `"v1"`, replicaA.Get("key").Marshal())
		assert.Equal(t, replicaA.Get("key").Marshal(), replicaB.Get("key").Marshal())
	})
}

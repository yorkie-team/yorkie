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

func TestElementRHTNodeOrder(t *testing.T) {
	// The identity of a Nodes() result, as a comparable value.
	order := func(nodes []*crdt.ElementRHTNode) []string {
		out := make([]string, 0, len(nodes))
		for _, node := range nodes {
			out = append(out, node.Key()+"@"+node.Element().CreatedAt().Key())
		}
		return out
	}

	// A key whose occupant was re-placed under a ticket newer than its own
	// createdAt -- the shape undo/redo leaves -- so PositionedAt and createdAt
	// disagree and the order is observable. The members here are distinct
	// elements, not literal restores; only the disagreement matters.
	build := func(t *testing.T) *crdt.ElementRHT {
		t.Helper()
		rht := crdt.NewElementRHT()

		actorA := time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
		actorB := time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}

		for i, spec := range []struct {
			key        string
			lamport    int64
			actor      time.ActorID
			executedAt int64
		}{
			{"a", 1, actorA, 1},
			{"a", 3, actorB, 3},
			{"b", 2, actorA, 2},
			{"b", 5, actorB, 5},
			{"a", 1, actorA, 6}, // re-placed: older createdAt, newer executedAt
		} {
			ticket := time.NewTicket(spec.lamport, uint32(i), spec.actor)
			value, err := crdt.NewPrimitive("v", ticket)
			assert.NoError(t, err)
			rht.SetWithExecutedAt(spec.key, value, time.NewTicket(spec.executedAt, uint32(i), spec.actor))
		}
		return rht
	}

	t.Run("returns nodes in ascending PositionedAt order", func(t *testing.T) {
		nodes := build(t).Nodes()
		assert.Len(t, nodes, 5)
		for i := 1; i < len(nodes); i++ {
			prev := crdt.PositionedAt(nodes[i-1].Element())
			curr := crdt.PositionedAt(nodes[i].Element())
			assert.LessOrEqual(t, prev.Compare(curr), 0,
				"node %d is positioned before node %d", i, i-1)
		}
	})

	t.Run("returns the same order on every call", func(t *testing.T) {
		// The property the ordering exists for: ranging the underlying Go map
		// gave a different answer each time, and api/converter emits an
		// object's members from here.
		rht := build(t)
		first := order(rht.Nodes())
		for i := range 50 {
			assert.Equal(t, first, order(rht.Nodes()),
				"call %d returned a different order", i)
		}
	})

	t.Run("breaks a PositionedAt tie by createdAt", func(t *testing.T) {
		// Two elements cannot share a PositionedAt in practice -- tickets are
		// unique per operation -- but the comparator must still be a total
		// order, or sort.Slice leaves the result unstable.
		rht := crdt.NewElementRHT()
		actor := time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
		shared := time.NewTicket(9, 0, actor)
		for i := range 3 {
			value, err := crdt.NewPrimitive("v", time.NewTicket(int64(i+1), 0, actor))
			assert.NoError(t, err)
			value.SetMovedAt(shared)
			rht.SetWithExecutedAt("k", value, shared)
		}

		first := order(rht.Nodes())
		for i := range 20 {
			assert.Equal(t, first, order(rht.Nodes()),
				"call %d returned a different order", i)
		}
	})
}

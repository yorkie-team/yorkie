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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/resource"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
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

	t.Run("a Set may still restore a tombstone under its own createdAt", func(t *testing.T) {
		// Undo of a Remove re-inserts the removed element under its original
		// createdAt; the tombstone in that slot must not make it a refusal.
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

	t.Run("a refused loser is reported as skipped and changes nothing", func(t *testing.T) {
		// Two restores of one value under its createdAt; the newer one holds
		// the key, so the older one loses while a live copy answers to its
		// createdAt. ElementRHT refuses it, and Set.Execute must report the
		// decline as ErrOperationSkipped -- so Change.Execute keeps it out of
		// the executed list and the reverse operations -- and touch no Root
		// bookkeeping.
		actor, _ := time.ActorIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaa")
		root := crdt.NewRoot(crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket))

		original, err := crdt.NewPrimitive("c", time.NewTicket(1, 0, actor))
		require.NoError(t, err)
		_, err = operations.NewSet(time.InitialTicket, "k", original, original.CreatedAt()).
			Execute(root, operations.OpSourceRemote, time.NewVersionVector())
		require.NoError(t, err)
		_, err = operations.NewSet(time.InitialTicket, "k", original, time.NewTicket(9, 0, actor)).
			Execute(root, operations.OpSourceRemote, time.NewVersionVector())
		require.NoError(t, err)

		garbage, size := root.GarbageLen(), root.DocSize()
		result, err := operations.NewSet(time.InitialTicket, "k", original, time.NewTicket(5, 0, actor)).
			Execute(root, operations.OpSourceUndoRedo, time.NewVersionVector())
		assert.ErrorIs(t, err, operations.ErrOperationSkipped)
		assert.Nil(t, result.Reverse)
		assert.False(t, result.Observable)
		assert.Equal(t, garbage, root.GarbageLen())
		assert.Equal(t, size, root.DocSize())
		assert.Equal(t, `{"k":"c"}`, root.Object().Marshal())
	})

	t.Run("a refused local Set fails rather than skips", func(t *testing.T) {
		// The same refusal, reached as a local edit. json.Object.setInternal
		// has by then taken the value into the clone through crdt.Object.Set,
		// which never declines, so a skip here would be swallowed by
		// Change.Execute and leave Document.Update with a clone holding a
		// member the root refused and no error to drop the clone on.
		actor, _ := time.ActorIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaa")
		root := crdt.NewRoot(crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket))

		original, err := crdt.NewPrimitive("c", time.NewTicket(1, 0, actor))
		require.NoError(t, err)
		_, err = operations.NewSet(time.InitialTicket, "k", original, original.CreatedAt()).
			Execute(root, operations.OpSourceRemote, time.NewVersionVector())
		require.NoError(t, err)
		_, err = operations.NewSet(time.InitialTicket, "k", original, time.NewTicket(9, 0, actor)).
			Execute(root, operations.OpSourceRemote, time.NewVersionVector())
		require.NoError(t, err)

		garbage, size := root.GarbageLen(), root.DocSize()
		_, err = operations.NewSet(time.InitialTicket, "k", original, time.NewTicket(5, 0, actor)).
			Execute(root, operations.OpSourceLocal, time.NewVersionVector())
		assert.ErrorIs(t, err, operations.ErrRefusedLocalSet)
		assert.NotErrorIs(t, err, operations.ErrOperationSkipped,
			"a local refusal reported as a skip is swallowed by Change.Execute")
		assert.Equal(t, garbage, root.GarbageLen())
		assert.Equal(t, size, root.DocSize())
		assert.Equal(t, `{"k":"c"}`, root.Object().Marshal())
	})
}

// TestSetConcurrentRestoresConverge applies two restores of one value under
// its original createdAt, and a concurrent Set of the same key, in every
// delivery order. Every order has to reach the same content, the same
// garbage, the same Live size and the same GC data size -- before collection,
// not only after -- and stay collectable and rebuildable, with the same
// docSize once collected.
//
// GC.Meta is left out before collection on purpose: a value that won its key
// and was then evicted carries a movedAt ticket, while the same value losing
// on arrival never gets one. That holds for any two concurrent Sets of one
// key, predates the restore handling and goes away with collection.
//
// Root's collection entries are keyed by
// createdAt, so a restore that evicts an older copy of itself must not book
// that copy under the createdAt it now answers to: the next eviction would
// overwrite the entry and leave the copy's GC charge behind for good, on the
// replicas that met the restores in that order and on no other.
func TestSetConcurrentRestoresConverge(t *testing.T) {
	actorA, _ := time.ActorIDFromHex("000000000000000000000001")
	actorB, _ := time.ActorIDFromHex("000000000000000000000002")
	actorC, _ := time.ActorIDFromHex("000000000000000000000003")

	primitive := func(v string, createdAt *time.Ticket) crdt.Element {
		p, err := crdt.NewPrimitive(v, createdAt)
		require.NoError(t, err)
		return p
	}
	// An object value, so that the restored copies share descendants too.
	object := func(createdAt *time.Ticket) crdt.Element {
		members := crdt.NewElementRHT()
		members.Set("n", primitive("1", time.NewTicket(createdAt.Lamport(), 1, createdAt.ActorID())))
		return crdt.NewObject(members, createdAt)
	}

	permutations := func(n int) [][]int {
		var out [][]int
		var walk func(prefix []int, rest []int)
		walk = func(prefix []int, rest []int) {
			if len(rest) == 0 {
				out = append(out, append([]int(nil), prefix...))
				return
			}
			for i := range rest {
				next := append(append([]int(nil), rest[:i]...), rest[i+1:]...)
				walk(append(prefix, rest[i]), next)
			}
		}
		all := make([]int, n)
		for i := range all {
			all[i] = i
		}
		walk(nil, all)
		return out
	}

	for _, tc := range []struct {
		name     string
		original func(*time.Ticket) crdt.Element
		xLamport int64
	}{
		{"primitive, Set between the restores", func(c *time.Ticket) crdt.Element { return primitive("c", c) }, 5},
		{"primitive, Set after both restores", func(c *time.Ticket) crdt.Element { return primitive("c", c) }, 7},
		{"object, Set between the restores", object, 5},
		{"object, Set after both restores", object, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := tc.original(time.NewTicket(1, 0, actorA))
			concurrent := []operations.Operation{
				operations.NewSet(time.InitialTicket, "k", original, time.NewTicket(4, 0, actorA)),
				operations.NewSet(time.InitialTicket, "k", original, time.NewTicket(6, 0, actorB)),
				operations.NewSet(time.InitialTicket, "k",
					primitive("x", time.NewTicket(tc.xLamport, 0, actorC)), time.NewTicket(tc.xLamport, 0, actorC)),
			}

			type state struct {
				marshal string
				garbage int
				live    string
				gcData  int
			}
			var want *state
			var wantCollected *resource.DocSize
			for _, order := range permutations(len(concurrent)) {
				name := fmt.Sprint(order)
				root := crdt.NewRoot(crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket))
				for _, op := range []operations.Operation{
					operations.NewSet(time.InitialTicket, "k", original, original.CreatedAt()),
					operations.NewSet(time.InitialTicket, "k",
						primitive("a", time.NewTicket(2, 0, actorA)), time.NewTicket(2, 0, actorA)),
					operations.NewSet(time.InitialTicket, "k",
						primitive("b", time.NewTicket(3, 0, actorB)), time.NewTicket(3, 0, actorB)),
				} {
					_, err := op.Execute(root, operations.OpSourceRemote, time.NewVersionVector())
					require.NoError(t, err)
				}
				for _, i := range order {
					_, err := concurrent[i].Execute(root, operations.OpSourceRemote, time.NewVersionVector())
					if err != nil {
						require.ErrorIs(t, err, operations.ErrOperationSkipped, name)
					}
				}

				got := &state{
					root.Object().Marshal(), root.GarbageLen(),
					fmt.Sprint(root.DocSize().Live), root.DocSize().GC.Data,
				}
				if want == nil {
					want = got
				}
				assert.Equal(t, *want, *got, "order %s before collection", name)

				rebuilt, err := root.DeepCopy()
				require.NoError(t, err)
				assert.Equal(t, root.GarbageLen(), rebuilt.GarbageLen(), "order %s rebuilt garbage", name)
				assert.Equal(t, root.DocSize(), rebuilt.DocSize(), "order %s rebuilt size", name)

				_, err = root.GarbageCollect(helper.MaxVersionVector(actorA, actorB, actorC))
				require.NoError(t, err)
				assert.Equal(t, 0, root.GarbageLen(), "order %s garbage after collection", name)
				assert.Equal(t, want.marshal, root.Object().Marshal(), "order %s after collection", name)
				assert.Zero(t, root.DocSize().GC, "order %s GC size after collection", name)
				collected := root.DocSize()
				if wantCollected == nil {
					wantCollected = &collected
				}
				assert.Equal(t, *wantCollected, collected, "order %s size after collection", name)
				rebuilt, err = root.DeepCopy()
				require.NoError(t, err, "order %s rebuild after collection", name)
				assert.Equal(t, root.DocSize(), rebuilt.DocSize(), "order %s rebuilt size after collection", name)
			}
		})
	}
}

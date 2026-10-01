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

package operations_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// Root.elementMap is keyed by createdAt for the whole document, and
// RegisterElement hands the slot to whatever was registered last. An array has
// no LWW guard of its own, so an Add or an ArraySet carrying the createdAt of
// a live element elsewhere would strand that element: every later operation,
// collection and snapshot resolving its createdAt would find the pushed value
// instead. The push boundary exempts array elements from the ticket rules on
// purpose (undo really re-identifies them), so this guard has to hold at
// execution time.
func TestElementSlotGuard(t *testing.T) {
	actor, err := time.ActorIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaa")
	assert.NoError(t, err)
	ticket := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }

	// build returns a root holding {"live": "v"} and an empty array at "arr".
	build := func(t *testing.T) (*crdt.Root, *crdt.Array, *time.Ticket) {
		t.Helper()

		root := crdt.NewRoot(crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket))
		live, err := crdt.NewPrimitive("v", ticket(1))
		assert.NoError(t, err)
		set := operations.NewSet(time.InitialTicket, "live", live, ticket(1))
		_, err = set.Execute(root, operations.OpSourceRemote, time.NewVersionVector())
		assert.NoError(t, err)

		arr := crdt.NewArray(crdt.NewRGATreeList(), ticket(2))
		setArr := operations.NewSet(time.InitialTicket, "arr", arr, ticket(2))
		_, err = setArr.Execute(root, operations.OpSourceRemote, time.NewVersionVector())
		assert.NoError(t, err)

		// The Set registered a DeepCopy, so reach the copy the root holds.
		registered, ok := root.FindByCreatedAt(ticket(2)).(*crdt.Array)
		assert.True(t, ok)
		return root, registered, ticket(1)
	}

	t.Run("an Add may not take a live element's slot", func(t *testing.T) {
		root, arr, liveAt := build(t)

		hijacker, err := crdt.NewPrimitive("hijacked", liveAt)
		assert.NoError(t, err)
		add := operations.NewAdd(arr.CreatedAt(), time.InitialTicket, hijacker, ticket(3))
		res, err := add.Execute(root, operations.OpSourceRemote, time.NewVersionVector())
		assert.NoError(t, err)
		assert.False(t, res.Observable)

		assert.Equal(t, `{"arr":[],"live":"v"}`, root.Object().Marshal())
		assert.Equal(t, "v", root.FindByCreatedAt(liveAt).(*crdt.Primitive).Value())
	})

	t.Run("an ArraySet may not take a live element's slot", func(t *testing.T) {
		root, arr, liveAt := build(t)

		// Give the array one element for the ArraySet to replace.
		victim, err := crdt.NewPrimitive("a", ticket(3))
		assert.NoError(t, err)
		add := operations.NewAdd(arr.CreatedAt(), time.InitialTicket, victim, ticket(3))
		_, err = add.Execute(root, operations.OpSourceRemote, time.NewVersionVector())
		assert.NoError(t, err)

		hijacker, err := crdt.NewPrimitive("hijacked", liveAt)
		assert.NoError(t, err)
		arraySet := operations.NewArraySet(arr.CreatedAt(), ticket(3), hijacker, ticket(4))
		res, err := arraySet.Execute(root, operations.OpSourceRemote, time.NewVersionVector())
		assert.NoError(t, err)
		assert.False(t, res.Observable)

		// Neither the insert nor the deletion the ArraySet would have made ran.
		assert.Equal(t, `{"arr":["a"],"live":"v"}`, root.Object().Marshal())
		assert.Equal(t, "v", root.FindByCreatedAt(liveAt).(*crdt.Primitive).Value())
	})

	// A Set that wins its key re-points the same slot with nothing between it
	// and elementMap, so the guard cannot be conditional on the value arriving
	// already removed.
	t.Run("a Set may not take a live element's slot", func(t *testing.T) {
		root, _, liveAt := build(t)

		hijacker, err := crdt.NewPrimitive("hijacked", liveAt)
		assert.NoError(t, err)
		set := operations.NewSet(time.InitialTicket, "other", hijacker, ticket(5))
		res, err := set.Execute(root, operations.OpSourceRemote, time.NewVersionVector())
		assert.NoError(t, err)
		assert.False(t, res.Observable)

		assert.Equal(t, `{"arr":[],"live":"v"}`, root.Object().Marshal())
		assert.Equal(t, "v", root.FindByCreatedAt(liveAt).(*crdt.Primitive).Value())
	})

	// A tombstone in the slot is not protected: each replica collects on its
	// own schedule, so a guard that fired on one would skip the operation only
	// where the tombstone had not been purged yet.
	t.Run("a tombstone's slot may be taken", func(t *testing.T) {
		root, arr, liveAt := build(t)

		remove := operations.NewRemove(time.InitialTicket, liveAt, ticket(6))
		_, err := remove.Execute(root, operations.OpSourceRemote, time.NewVersionVector())
		assert.NoError(t, err)

		restored, err := crdt.NewPrimitive("v", liveAt)
		assert.NoError(t, err)
		add := operations.NewAdd(arr.CreatedAt(), time.InitialTicket, restored, ticket(7))
		res, err := add.Execute(root, operations.OpSourceRemote, time.NewVersionVector())
		assert.NoError(t, err)
		assert.True(t, res.Observable)
		assert.Equal(t, `{"arr":["v"]}`, root.Object().Marshal())
	})
}

// Increase decodes its value with the generic element reader, which accepts
// any type a crafted payload names. The operation must refuse a non-numeric
// value rather than assert on it: a stored change replayed into a snapshot
// never passes the push boundary that rejects one. Off the local path the
// refusal is a no-op, so that replay cannot fail on it (see skipUnlessLocal).
func TestIncreaseRefusesNonPrimitive(t *testing.T) {
	actor, err := time.ActorIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaa")
	assert.NoError(t, err)
	ticket := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }

	root := crdt.NewRoot(crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket))
	counter, err := crdt.NewCounter(crdt.LongCnt, int64(0), ticket(1))
	assert.NoError(t, err)
	set := operations.NewSet(time.InitialTicket, "cnt", counter, ticket(1))
	_, err = set.Execute(root, operations.OpSourceRemote, time.NewVersionVector())
	assert.NoError(t, err)

	increase := operations.NewIncrease(
		ticket(1),
		crdt.NewObject(crdt.NewElementRHT(), ticket(2)),
		ticket(3),
	)
	res, err := increase.Execute(root, operations.OpSourceRemote, time.NewVersionVector())
	assert.NoError(t, err)
	assert.False(t, res.Observable)
	assert.Equal(t, `{"cnt":0}`, root.Object().Marshal())

	_, err = increase.Execute(root, operations.OpSourceLocal, time.NewVersionVector())
	assert.ErrorIs(t, err, operations.ErrNotApplicableDataType)
}

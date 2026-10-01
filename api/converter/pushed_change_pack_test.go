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
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// pushedActor is the actor every pack below is pushed by.
var pushedActor = time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}

func pushedTicket(lamport int64) *time.Ticket {
	return time.NewTicket(lamport, 0, pushedActor)
}

// pushedPack wraps the given operations in the smallest ChangePack a client
// can push: one change, by one actor, at the first checkpoint.
func pushedPack(t *testing.T, ops ...operations.Operation) *api.ChangePack {
	t.Helper()

	pbOps, err := converter.ToOperations(ops)
	assert.NoError(t, err)

	return &api.ChangePack{
		DocumentKey: "d1",
		Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 1},
		Changes: []*api.Change{{
			Id: &api.ChangeID{
				ClientSeq: 1,
				Lamport:   1,
				ActorId:   pushedActor[:],
			},
			Operations: pbOps,
		}},
	}
}

// poisonedObject is an object whose single member carries a removedAt that
// does not follow its createdAt -- the shape no replica can issue and
// ValidatePushedOperations rejects.
func poisonedObject(t *testing.T) *crdt.Object {
	t.Helper()

	obj := crdt.NewObject(crdt.NewElementRHT(), pushedTicket(1))
	member, err := crdt.NewPrimitive("v", pushedTicket(2))
	assert.NoError(t, err)
	obj.Set("a", member)
	member.SetRemovedAt(pushedTicket(2))
	return obj
}

// FromPushedChangePack is the function the four client-facing RPC handlers
// actually call, so the rules have to reach a pack rather than a bare
// operation list.
func TestFromPushedChangePack(t *testing.T) {
	t.Run("a pack a replica can send decodes", func(t *testing.T) {
		value, err := crdt.NewPrimitive("v", pushedTicket(2))
		assert.NoError(t, err)

		pack, err := converter.FromPushedChangePack(
			pushedPack(t, operations.NewSet(pushedTicket(1), "k", value, pushedTicket(2))),
		)
		assert.NoError(t, err)
		assert.Len(t, pack.Changes, 1)
		assert.Len(t, pack.Changes[0].Operations(), 1)
	})

	t.Run("a pack carrying an impossible member fails", func(t *testing.T) {
		_, err := converter.FromPushedChangePack(
			pushedPack(t, operations.NewSet(pushedTicket(1), "k", poisonedObject(t), pushedTicket(9))),
		)
		assert.ErrorIs(t, err, converter.ErrInvalidElementTicket)
	})

	// The Add and ArraySet branches of the validator carry the same
	// object-member rules through their own payloads; only the array elements
	// themselves are exempt.
	t.Run("an Add carrying an impossible member fails", func(t *testing.T) {
		_, err := converter.FromPushedChangePack(pushedPack(t, operations.NewAdd(
			pushedTicket(1), pushedTicket(1), poisonedObject(t), pushedTicket(9),
		)))
		assert.ErrorIs(t, err, converter.ErrInvalidElementTicket)
	})

	t.Run("an ArraySet carrying an impossible member fails", func(t *testing.T) {
		_, err := converter.FromPushedChangePack(pushedPack(t, operations.NewArraySet(
			pushedTicket(1), pushedTicket(2), poisonedObject(t), pushedTicket(9),
		)))
		assert.ErrorIs(t, err, converter.ErrInvalidElementTicket)
	})

	// An array element may carry tickets an object member may not, because
	// undo really emits them; an Add of one must still get through.
	t.Run("an Add of a legitimately re-identified element decodes", func(t *testing.T) {
		arr := crdt.NewArray(crdt.NewRGATreeList(), pushedTicket(1))
		elem, err := crdt.NewPrimitive("v", pushedTicket(3))
		assert.NoError(t, err)
		assert.NoError(t, arr.Add(elem))
		elem.SetMovedAt(pushedTicket(2))

		_, err = converter.FromPushedChangePack(pushedPack(t, operations.NewAdd(
			pushedTicket(1), pushedTicket(1), arr, pushedTicket(9),
		)))
		assert.NoError(t, err)
	})

	// Increase carries a JSONElementSimple the decoder will happily fill with
	// any element type, and Increase.Execute wants a Primitive. The boundary
	// rejects the rest rather than letting the operation meet them.
	t.Run("an Increase of a container fails", func(t *testing.T) {
		_, err := converter.FromPushedChangePack(pushedPack(t, operations.NewIncrease(
			pushedTicket(1),
			crdt.NewObject(crdt.NewElementRHT(), pushedTicket(2)),
			pushedTicket(9),
		)))
		assert.ErrorIs(t, err, converter.ErrInvalidElementTicket)
	})

	t.Run("an ordinary Increase decodes", func(t *testing.T) {
		delta, err := crdt.NewPrimitive(int32(1), pushedTicket(2))
		assert.NoError(t, err)

		_, err = converter.FromPushedChangePack(pushedPack(t, operations.NewIncrease(
			pushedTicket(1), delta, pushedTicket(9),
		)))
		assert.NoError(t, err)
	})
}

// A client does not discard a change a push rejected, so a rejection with no
// way out is permanent. Leaving is that way out: the pack is dropped rather
// than failed, the detach proceeds, and re-attaching rebuilds the document
// from the server's snapshot.
func TestFromLeavingChangePack(t *testing.T) {
	t.Run("a pack a replica can send is kept", func(t *testing.T) {
		value, err := crdt.NewPrimitive("v", pushedTicket(2))
		assert.NoError(t, err)

		pack, err := converter.FromLeavingChangePack(
			pushedPack(t, operations.NewSet(pushedTicket(1), "k", value, pushedTicket(2))),
		)
		assert.NoError(t, err)
		assert.Len(t, pack.Changes, 1)
	})

	t.Run("a pack carrying an impossible member leaves without it", func(t *testing.T) {
		pack, err := converter.FromLeavingChangePack(
			pushedPack(t, operations.NewSet(pushedTicket(1), "k", poisonedObject(t), pushedTicket(9))),
		)
		assert.NoError(t, err)
		assert.Empty(t, pack.Changes)
		assert.Equal(t, "d1", pack.DocumentKey.String())
	})

	// A pack that cannot be decoded at all is still an error: there is no
	// checkpoint or document key to act on, so there is nothing to let through.
	t.Run("an undecodable pack still fails", func(t *testing.T) {
		_, err := converter.FromLeavingChangePack(&api.ChangePack{DocumentKey: "d1"})
		assert.Error(t, err)
	})
}

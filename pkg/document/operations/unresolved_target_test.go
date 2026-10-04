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
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestUnresolvedTargetIsSkipped covers the createdAts no rebuild can carry.
// An orphaned subtree -- a refused copy, or a tombstone a restore displaced --
// is addressable only on the Root that adopted it: a snapshot encodes what the
// tree reaches and nothing else, so a replica or server seeded from one
// answers for strictly fewer createdAts than the replica that applied the
// changes in order. An operation a peer addressed into such a subtree has to
// be dropped there rather than abort the whole change, which on the server
// would fail forever -- it replays the same log on every rebuild.
func TestUnresolvedTargetIsSkipped(t *testing.T) {
	actor, err := time.ActorIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaa")
	require.NoError(t, err)

	value, err := crdt.NewPrimitive("x", time.NewTicket(11, 0, actor))
	require.NoError(t, err)
	vanished := time.NewTicket(10, 0, actor)
	op := operations.NewSet(vanished, "k", value, value.CreatedAt())

	for _, source := range []operations.OpSource{
		operations.OpSourceRemote,
		operations.OpSourceReplay,
		operations.OpSourceUndoRedo,
	} {
		root := crdt.NewRoot(crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket))
		_, err := op.Execute(root, source, time.NewVersionVector())
		assert.ErrorIs(t, err, operations.ErrOperationSkipped)
	}

	// A local operation was built against this very root a moment ago, so an
	// unresolved target there is a bug, not a replica that saw history in
	// another order.
	root := crdt.NewRoot(crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket))
	_, err = op.Execute(root, operations.OpSourceLocal, time.NewVersionVector())
	assert.ErrorIs(t, err, operations.ErrNotApplicableDataType)
}

// TestSetRejectsIdentityHeldLiveElsewhere covers the push boundary. The
// payload's createdAt arrives verbatim off the wire, and it now decides
// control flow: a value whose createdAt another element already answers to is
// refused by the object and left addressable at no charge. A ticket naming a
// live element this Set is not restoring must therefore be rejected before
// any map is touched.
func TestSetRejectsIdentityHeldLiveElsewhere(t *testing.T) {
	actor, err := time.ActorIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaa")
	require.NoError(t, err)

	obj := crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket)
	root := crdt.NewRoot(obj)

	liveAt := time.NewTicket(1, 0, actor)
	live, err := crdt.NewPrimitive("live", liveAt)
	require.NoError(t, err)
	_, err = operations.NewSet(time.InitialTicket, "a", live, liveAt).
		Execute(root, operations.OpSourceRemote, time.NewVersionVector())
	require.NoError(t, err)

	// A crafted payload reusing the live element's createdAt under another
	// key.
	forged, err := crdt.NewPrimitive("forged", liveAt)
	require.NoError(t, err)
	_, err = operations.NewSet(time.InitialTicket, "b", forged, time.NewTicket(2, 0, actor)).
		Execute(root, operations.OpSourceRemote, time.NewVersionVector())
	assert.ErrorIs(t, err, operations.ErrInUseElementIdentity)
	assert.Equal(t, `{"a":"live"}`, root.Object().Marshal())

	// A restore under a tombstoned createdAt is the shape Set exists to
	// tolerate, and still passes.
	_, err = operations.NewRemove(time.InitialTicket, liveAt, time.NewTicket(3, 0, actor)).
		Execute(root, operations.OpSourceRemote, time.NewVersionVector())
	require.NoError(t, err)
	restored, err := crdt.NewPrimitive("live", liveAt)
	require.NoError(t, err)
	_, err = operations.NewSet(time.InitialTicket, "a", restored, time.NewTicket(4, 0, actor)).
		Execute(root, operations.OpSourceRemote, time.NewVersionVector())
	require.NoError(t, err)
	assert.Equal(t, `{"a":"live"}`, root.Object().Marshal())
}

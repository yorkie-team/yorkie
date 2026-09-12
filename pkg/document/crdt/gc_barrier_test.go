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

package crdt

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// A purge can be held back by more than one ticket, and the set is NOT
// reducible to its maximum. Ticket order is (lamport, actorID) and total;
// coverage by a version vector is decided per actor, so the ticket that sorts
// later can be the one that is covered.
//
// This is not hypothetical: it is what a concurrent move and a concurrent
// assignment on the same element produce -- the move's ticket and the
// assignment's share a lamport and differ only in actor -- and taking the
// maximum let seed 60 of the array fuzz collect a slot that was still named.
func TestBarrierCoverageIsPerActor(t *testing.T) {
	actorB, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	actorC, err := time.ActorIDFromHex("000000000000000000000002")
	require.NoError(t, err)

	fromB := time.NewTicket(3, 1, actorB)
	fromC := time.NewTicket(3, 1, actorC)
	require.True(t, fromC.After(fromB), "the C ticket has to be the later of the two")

	vector := time.NewVersionVector()
	vector.Set(actorB, 1)
	vector.Set(actorC, 3)

	require.True(t, vector.EqualToOrAfter(fromC), "the later ticket is covered")
	require.False(t, vector.EqualToOrAfter(fromB), "the earlier one is not")

	require.False(t, covers(vector, PurgeBarrier{fromB, fromC}),
		"a barrier is covered only when every ticket in it is")
	require.True(t, covers(vector, PurgeBarrier{nil, fromC}),
		"nil entries are no barrier at all")
	require.True(t, covers(vector, PurgeBarrier{}))
}

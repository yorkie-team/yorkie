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

// A tombstone in an RGATreeList is two things at once, and collection has to
// respect both. gc_rga_barrier_test.go covers the first: it is a stopping point
// for the forward skip that decides where a concurrent insert lands. The three
// tests here cover the second: it is also a POSITION that later operations name
// as their anchor.
//
// The distinction matters because the two have different gates. A stopping
// point is safe to unlink once its successor is causally stable, which is a
// statement about tickets. An anchor is safe to unlink only once no replica can
// still name it, which is a statement about which replica holds what -- and
// "every replica knows this node was removed" (what minVV covering removedAt
// says) is strictly weaker than "no replica still uses it as a position".
//
// Every vector that authorises a purge below is the element-wise min over the
// replicas' own push-time vectors, as UpdateMinVersionVector computes it.
// helper.MaxVersionVector appears only in the closing assertion of each test,
// to show that nothing is retained forever.

package document_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

// arrLive counts the position nodes still linked in "arr", dead ones included.
func arrPositionCount(t *testing.T, d *document.Document) int {
	t.Helper()

	arr, ok := d.RootObject().Get("arr").(*crdt.Array)
	require.True(t, ok)
	return len(arr.AllRGANodes())
}

// TestAppendAnchorsOnTailTombstone pins that the physical tail survives
// collection while any replica can still append behind it.
//
// RGATreeList.Add takes a.last as its prevCreatedAt -- the last PHYSICAL
// position node, tombstones included -- so an append issued long after a delete
// legitimately names the deleted node. Gating that node's purge on minVV
// covering its removedAt is not enough: that says every replica KNOWS about the
// delete, not that every replica has acted on it. B still holds the tombstone,
// so B's next append still anchors there, and A cannot apply it.
//
// No concurrency and no move is involved; the append strictly follows the
// delete.
func TestAppendAnchorsOnTailTombstone(t *testing.T) {
	dA, dB, aA, aB := newReplicas(t)
	rows := map[string]time.VersionVector{}

	require.NoError(t, dA.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewArray("arr").AddString("a").AddString("b").AddString("c")
		return nil
	}))
	vpull(t, dB, vpush(t, dA, rows))
	_ = vpush(t, dB, rows)

	// B deletes the LAST element and pushes it; A pulls and pushes, so both
	// stored rows cover the removal.
	require.NoError(t, dB.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").Delete(2)
		return nil
	}))
	vpull(t, dA, vpush(t, dB, rows))
	_ = vpush(t, dA, rows)

	minVV := serverMinVV(rows)
	removedAt := removedAtOf(t, dA, "c")
	require.True(t, minVV.EqualToOrAfter(removedAt),
		"the scenario needs a vector that genuinely covers the removal: minVV=%s removedAt=%s",
		minVV.Marshal(), removedAt.Key())

	dA.GarbageCollect(minVV)
	t.Log(dumpRGA(t, dA, "A after GC"))
	require.Contains(t, dumpRGA(t, dA, "A"), `"c"`,
		"A must keep its physical tail: B has not collected and still anchors appends there")

	// B appends. Add anchors on a.last, which on B is still the tombstone.
	require.NoError(t, dB.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").AddString("x")
		return nil
	}))
	fromB := vpush(t, dB, rows)
	vpull(t, dA, fromB)

	require.Equal(t, `["a","b","x"]`, dB.Root().GetArray("arr").Marshal())
	require.Equal(t, dB.Root().GetArray("arr").Marshal(), dA.Root().GetArray("arr").Marshal())

	// Nothing is retained forever: once the append is stable the tombstone has
	// a stable successor and both replicas let it go.
	_ = vpush(t, dA, rows)
	_ = vpush(t, dB, rows)
	dA.GarbageCollect(helper.MaxVersionVector(aA, aB))
	dB.GarbageCollect(helper.MaxVersionVector(aA, aB))
	require.NotContains(t, dumpRGA(t, dA, "A"), `"c"`, "the tombstone is retained forever")
	require.NotContains(t, dumpRGA(t, dB, "B"), `"c"`, "the tombstone is retained forever")
	require.Equal(t, arrPositionCount(t, dA), arrPositionCount(t, dB))
}

// TestArraySetAnchorsOnAbandonedInsertSlot pins the second anchor shape, which
// fails silently rather than loudly.
//
// RGATreeList.insertAfter resolves prevCreatedAt through nodeMapByCreatedAt
// FIRST and falls back to elementMapByCreatedAt. A move leaves the element's
// insert-created slot in nodeMapByCreatedAt under the element's own createdAt,
// and every later ArraySet on that element anchors there. Purging the slot
// deletes the nodeMapByCreatedAt entry, so the ArraySet does not fail -- it
// falls through to the element's CURRENT position node, a different place in
// the list, and the replicas order the replacement differently forever.
//
// The successor barrier alone authorises exactly this purge: the slot's
// successor is the element's own moved-to node, which minVV covers.
func TestArraySetAnchorsOnAbandonedInsertSlot(t *testing.T) {
	dA, dB, aA, aB := newReplicas(t)
	rows := map[string]time.VersionVector{}

	require.NoError(t, dA.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewArray("arr").AddString("a").AddString("b")
		return nil
	}))
	vpull(t, dB, vpush(t, dA, rows))
	_ = vpush(t, dB, rows)

	// B self-moves "a": a new position node is stamped with the move ticket and
	// the insert-created slot becomes a dead position node.
	require.NoError(t, dB.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").MoveAfterByIndex(0, 0)
		return nil
	}))
	vpull(t, dA, vpush(t, dB, rows))
	_ = vpush(t, dA, rows)
	t.Log(dumpRGA(t, dA, "A after move"))

	// B replaces "a" with "z" (ArraySet) and does NOT push.
	require.NoError(t, dB.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").SetString(0, "z")
		return nil
	}))
	t.Log(dumpRGA(t, dB, "B after set"))

	// A inserts after "a" with a ticket newer than B's unseen set, and collects
	// with a vector that covers the move but not the set.
	require.NoError(t, dA.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").InsertStringAfter(0, "q")
		return nil
	}))
	minVV := serverMinVV(rows)
	require.True(t, minVV.EqualToOrAfter(movedPositionTicket(t, dA)),
		"the scenario needs a vector that covers the move: minVV=%s", minVV.Marshal())
	dA.GarbageCollect(minVV)
	t.Log(dumpRGA(t, dA, "A after GC"))

	// Deliver everything both ways.
	fromB := vpush(t, dB, rows)
	fromA := vpush(t, dA, rows)
	vpull(t, dA, fromB)
	vpull(t, dB, fromA)

	t.Log(dumpRGA(t, dA, "A final"))
	t.Log(dumpRGA(t, dB, "B final"))
	require.Equal(t,
		dB.Root().GetArray("arr").Marshal(),
		dA.Root().GetArray("arr").Marshal(),
		"replicas diverged after collecting the dead slot an ArraySet still anchors on")

	// The slot is not retained forever: once the element it names is itself
	// collected, its key is free and the slot goes with the next pass.
	_ = vpush(t, dA, rows)
	_ = vpush(t, dB, rows)
	dA.GarbageCollect(helper.MaxVersionVector(aA, aB))
	dB.GarbageCollect(helper.MaxVersionVector(aA, aB))
	require.Equal(t, arrPositionCount(t, dA), arrPositionCount(t, dB))
	require.Equal(t, 0, dA.GarbageLen(), "A retains collectable garbage")
	require.Equal(t, 0, dB.GarbageLen(), "B retains collectable garbage")
}

// TestLosingMoveSlotIsLiveElsewhere pins the third anchor shape: a position
// node that is dead HERE and live THERE.
//
// When a move arrives that an already-recorded move beats, MoveAfter still
// builds the position node -- operations that reference it have to find
// something -- and marks it dead. But the replica that issued the losing move
// applied it while it was still the winner, so on that replica the very same
// node holds the element and later moves and inserts anchor on it.
//
// Stamping the node's removedAt with the losing move's own executedAt makes a
// vector that covers only the loser authorise the purge, which is a purge of a
// node another replica is actively using. The ticket that decides the node is
// dead is the WINNER's.
func TestLosingMoveSlotIsLiveElsewhere(t *testing.T) {
	dA, dB, aA, aB := newReplicas(t)
	rows := map[string]time.VersionVector{}

	require.NoError(t, dA.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewArray("arr").AddString("a").AddString("b").AddString("c")
		return nil
	}))
	vpull(t, dB, vpush(t, dA, rows))
	_ = vpush(t, dB, rows)

	// Concurrent moves of the same element. B burns a ticket first so its move
	// carries the later lamport and wins the LWW.
	require.NoError(t, dA.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").MoveAfterByIndex(1, 0)
		return nil
	}))
	require.NoError(t, dB.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetInteger("filler", 1)
		r.GetArray("arr").MoveAfterByIndex(0, 0)
		return nil
	}))

	// Both push. A's row and B's row both end up covering A's move; only B's
	// covers B's winning move, because A never pulls it.
	loserMove := vpush(t, dA, rows)
	winnerMove := vpush(t, dB, rows)
	vpull(t, dB, loserMove)
	_ = vpush(t, dB, rows)
	t.Log(dumpRGA(t, dB, "B after filing the losing move"))

	// A, which has not heard from B, still has "a" sitting in the slot its own
	// move built, so its next move ANCHORS on that slot: A's array reads
	// ["b","a","c"] and moving "c" after "a" names "a"'s position.
	require.Equal(t, `["b","a","c"]`, dA.Root().GetArray("arr").Marshal())
	require.NoError(t, dA.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").MoveAfterByIndex(1, 2)
		return nil
	}))
	secondMove := vpush(t, dA, rows)

	// B collects. minVV covers A's first move -- both rows have it -- but not
	// B's own winning move, which A has never seen.
	minVV := serverMinVV(rows)
	require.False(t, minVV.EqualToOrAfter(movedPositionTicket(t, dB)),
		"the scenario needs a vector that does NOT cover the winning move: minVV=%s", minVV.Marshal())
	before := dumpRGA(t, dB, "B before GC")
	dB.GarbageCollect(minVV)
	t.Log(before)
	t.Log(dumpRGA(t, dB, "B after GC"))

	// B now applies A's second move, which names the slot it just considered
	// collecting.
	vpull(t, dB, secondMove)
	vpull(t, dA, winnerMove)

	t.Log(dumpRGA(t, dA, "A final"))
	t.Log(dumpRGA(t, dB, "B final"))
	require.Equal(t,
		dA.Root().GetArray("arr").Marshal(),
		dB.Root().GetArray("arr").Marshal())

	_ = vpush(t, dA, rows)
	_ = vpush(t, dB, rows)
	dA.GarbageCollect(helper.MaxVersionVector(aA, aB))
	dB.GarbageCollect(helper.MaxVersionVector(aA, aB))
	require.Equal(t, arrPositionCount(t, dA), arrPositionCount(t, dB),
		"the replicas retain different amounts of structure")
}

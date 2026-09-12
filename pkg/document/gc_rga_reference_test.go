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

package document_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// The barrier tests next door are about what collection must not UNLINK. These
// are about what an operation is allowed to NAME.
//
// A version vector authorises a purge by saying every replica has seen the
// removal. It does not say no replica will name the node again, and three
// places named one: an append anchors on the physical tail, an ArraySet anchors
// on the element's first position slot, and a move that lost LWW recorded its
// own ticket as the slot's removal rather than the winner's. Each is reachable
// through the ordinary push/pull path, and each leaves a replica that collected
// resolving an anchor differently from one that did not -- or failing to
// resolve it at all, which is worse, because the server replays the same log to
// rebuild the document.

// TestAppendAfterCollectedTail: B deletes the last element and A collects the
// tombstone. B then appends, with no concurrency at all -- the append strictly
// follows the delete. The anchor an append names has to be a node A still has.
func TestAppendAfterCollectedTail(t *testing.T) {
	dA, dB, _, _ := newReplicas(t)
	rows := map[string]time.VersionVector{}

	require.NoError(t, dA.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewArray("arr").AddString("a").AddString("b").AddString("c")
		return nil
	}))
	base := vpush(t, dA, rows)
	vpull(t, dB, base)
	_ = vpush(t, dB, rows)

	// B deletes the LAST element.
	require.NoError(t, dB.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").Delete(2)
		return nil
	}))
	del := vpush(t, dB, rows)
	vpull(t, dA, del)
	_ = vpush(t, dA, rows)

	// A collects: every row now covers the delete.
	dA.GarbageCollect(serverMinVV(rows))
	require.NotContains(t, dumpRGA(t, dA, "A"), `"c"`, "A should have collected the tombstone")

	// B appends. It has not collected, so its physical tail is still the
	// tombstone A has just unlinked.
	require.NoError(t, dB.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").AddString("x")
		return nil
	}))
	add := vpush(t, dB, rows)
	vpull(t, dA, add)

	require.Equal(t, `["a","b","x"]`, dA.Root().GetArray("arr").Marshal())
	require.Equal(t, `["a","b","x"]`, dB.Root().GetArray("arr").Marshal())
}

// TestArraySetAnchorSlotSurvivesCollection: an element is moved, so its first
// position slot goes dead, and then an ArraySet on that element is issued
// concurrently with an insert. ArraySet resolves its anchor through
// nodeMapByCreatedAt, which is that dead slot -- on purpose, so the assignment
// lands where the element was written rather than where a concurrent move has
// since put it. Collecting the slot does not make the assignment fail, it makes
// it silently anchor somewhere else.
func TestArraySetAnchorSlotSurvivesCollection(t *testing.T) {
	dA, dB, _, _ := newReplicas(t)
	rows := map[string]time.VersionVector{}

	require.NoError(t, dA.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewArray("arr").AddString("a").AddString("b")
		return nil
	}))
	base := vpush(t, dA, rows)
	vpull(t, dB, base)
	_ = vpush(t, dB, rows)

	// B moves "a" onto itself: a new position node takes the element and the
	// insert-created slot is left dead.
	require.NoError(t, dB.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").MoveAfterByIndex(0, 0)
		return nil
	}))
	move := vpush(t, dB, rows)
	vpull(t, dA, move)
	_ = vpush(t, dA, rows)

	// B assigns over "a" and does NOT push. The operation names "a", which
	// resolves to the dead slot.
	require.NoError(t, dB.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").SetString(0, "z")
		return nil
	}))

	// A, meanwhile, inserts after "a" with a later ticket, and collects. Every
	// row covers the move; none covers B's unpushed assignment.
	for i := range 4 {
		require.NoError(t, dA.Update(func(r *json.Object, _ *presence.Presence) error {
			r.SetInteger(fmt.Sprintf("f%d", i), i)
			return nil
		}))
	}
	require.NoError(t, dA.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").InsertStringAfter(0, "q")
		return nil
	}))
	fromA := vpush(t, dA, rows)
	dA.GarbageCollect(serverMinVV(rows))

	fromB := vpush(t, dB, rows)
	vpull(t, dA, fromB)
	vpull(t, dB, fromA)

	require.Equal(t,
		dB.Root().GetArray("arr").Marshal(),
		dA.Root().GetArray("arr").Marshal(),
		"the replica that collected resolved the assignment's anchor elsewhere")
}

// TestLosingMoveSlotNotCollectedEarly: two replicas move the same element
// concurrently. On the replica that applies the winner first, the loser's slot
// is born dead, and recording the LOSING ticket as its removal makes it
// collectible as soon as every replica has merely SEEN that losing move -- while
// the replica that applied the moves the other way round still has the element
// parked on that slot and is still handing it out as an anchor.
func TestLosingMoveSlotNotCollectedEarly(t *testing.T) {
	dA, dB, _, _ := newReplicas(t)
	rows := map[string]time.VersionVector{}

	require.NoError(t, dA.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewArray("arr").AddString("a").AddString("b").AddString("c")
		return nil
	}))
	base := vpush(t, dA, rows)
	vpull(t, dB, base)
	_ = vpush(t, dB, rows)

	// A moves "a" after "b" (m1). B concurrently moves "a" after "c" (m2),
	// with the later ticket, so m2 wins wherever both are known.
	require.NoError(t, dA.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").MoveAfterByIndex(1, 0)
		return nil
	}))
	m1 := vpush(t, dA, rows)

	for i := range 3 {
		require.NoError(t, dB.Update(func(r *json.Object, _ *presence.Presence) error {
			r.SetInteger(fmt.Sprintf("f%d", i), i)
			return nil
		}))
	}
	require.NoError(t, dB.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").MoveAfterByIndex(2, 0)
		return nil
	}))
	m2 := vpush(t, dB, rows)

	// B applies m1 second, so on B the slot m1 would have filled is born dead.
	vpull(t, dB, m1)
	_ = vpush(t, dB, rows)

	// A has not pulled m2 yet: on A the element is parked on m1's slot, and an
	// insert after "a" therefore names it.
	require.NoError(t, dA.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").InsertStringAfter(1, "s")
		return nil
	}))
	ins := vpush(t, dA, rows)

	// Every row now covers m1, so B collects -- and must not take the slot
	// A's insert names with it.
	dB.GarbageCollect(serverMinVV(rows))

	vpull(t, dB, ins)
	vpull(t, dA, m2)

	require.Equal(t,
		dB.Root().GetArray("arr").Marshal(),
		dA.Root().GetArray("arr").Marshal(),
		"the replica that collected the losing move's slot lost the insert anchored on it")
}

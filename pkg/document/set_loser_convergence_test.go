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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestSetLoserAgainstTombstoneConverges pins that a Set losing LWW to a key
// whose occupant is already a tombstone is accounted for the same way on
// every replica, whichever order the changes arrive in.
//
// d1 sets k and removes it; d2 concurrently sets k under an older ticket. On
// d2 its own value is the occupant when d1's newer Set arrives, so the
// ordinary eviction tombstones it. On d1 the occupant is already a tombstone
// when d2's older Set arrives, and ElementRHT used to mark the loser removed
// only when the occupant was live -- leaving it live in the created-at index,
// never registered as garbage, and charged to Live. The two replicas showed
// the same JSON but disagreed on docSize and GarbageLen, and the server,
// which replays the change log, is just another replica.
func TestSetLoserAgainstTombstoneConverges(t *testing.T) {
	actor1, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	actor2, err := time.ActorIDFromHex("000000000000000000000002")
	require.NoError(t, err)

	d1 := document.New("set-loser")
	d1.SetActor(actor1)
	d2 := document.New("set-loser")
	d2.SetActor(actor2)

	// Advance d1's lamport so its Set of k is newer than d2's.
	for range 3 {
		require.NoError(t, d1.Update(func(r *json.Object, _ *presence.Presence) error {
			r.SetString("pad", "x")
			return nil
		}))
	}
	require.NoError(t, d1.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetInteger("k", 1)
		return nil
	}))
	require.NoError(t, d1.Update(func(r *json.Object, _ *presence.Presence) error {
		r.Delete("k")
		return nil
	}))

	require.NoError(t, d2.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetInteger("k", 2)
		return nil
	}))

	deliverChanges(t, d1, d2)
	deliverChanges(t, d2, d1)

	assert.Equal(t, `{"pad":"x"}`, d1.Marshal())
	assert.Equal(t, `{"pad":"x"}`, d2.Marshal())

	// Four garbage elements on both: the two overwritten pads, the removed
	// k, and the losing k.
	assert.Equal(t, 4, d2.GarbageLen())
	assert.Equal(t, d2.GarbageLen(), d1.GarbageLen(),
		"the replica that saw the tombstone first did not book the loser")
	assert.Equal(t, d2.DocSize().Live, d1.DocSize().Live,
		"the replica that saw the tombstone first kept the loser in Live")
	// GC.Meta is compared up to one ticket on purpose. On d2 the losing k
	// first won and was stamped with movedAt; on d1 it lost on arrival and
	// never was. That is a separate, pre-existing difference in the elements
	// themselves (the JS SDK shares it), not in how they are accounted.
	assert.Equal(t, d2.DocSize().GC.Data, d1.DocSize().GC.Data)
	assert.Equal(t, d2.DocSize().GC.Meta, d1.DocSize().GC.Meta+time.TicketSize,
		"the loser's size did not reach GC on the replica that saw the tombstone first")
	assertRebuildsSame(t, d1, "tombstone first")
	assertRebuildsSame(t, d2, "loser first")

	vector := helper.MaxVersionVector(actor1, actor2)
	d1.GarbageCollect(vector)
	d2.GarbageCollect(vector)

	assert.Equal(t, 0, d1.GarbageLen())
	assert.Equal(t, 0, d2.GarbageLen())
	assert.Equal(t, d2.DocSize(), d1.DocSize())
}

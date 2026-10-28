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
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestLosingUndoRestoreConverges pins that an undo restoring a replaced value
// ends the same on every replica when a concurrent, newer Set takes the key.
//
// d1 sets k to C, then to D, and undoes -- restoring a copy of C under C's
// own createdAt, where C's tombstone already answers. d2 concurrently sets k
// to E with a newer ticket. On d1 the restore wins and E then evicts it, so C
// ends up removed at E's ticket. On d2 E arrives first and the restore loses:
// it must still be tombstoned at E's ticket and take C's index slot, not be
// refused because a tombstone holds that createdAt, or the replicas disagree
// on C's removedAt and on everything that follows from it.
func TestLosingUndoRestoreConverges(t *testing.T) {
	actor1, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	actor2, err := time.ActorIDFromHex("000000000000000000000002")
	require.NoError(t, err)

	d1 := document.New("undo-restore-loser")
	d1.SetActor(actor1)
	d2 := document.New("undo-restore-loser")
	d2.SetActor(actor2)

	require.NoError(t, d1.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetString("k", "C")
		return nil
	}))
	require.NoError(t, d1.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetString("k", "D")
		return nil
	}))
	deliverChanges(t, d1, d2)

	// Advance d2's lamport so its Set of k is newer than d1's undo.
	for range 3 {
		require.NoError(t, d2.Update(func(r *json.Object, _ *presence.Presence) error {
			r.SetString("pad", "x")
			return nil
		}))
	}
	require.NoError(t, d2.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetString("k", "E")
		return nil
	}))

	require.NoError(t, d1.Undo())

	deliverChanges(t, d1, d2)
	deliverChanges(t, d2, d1)

	assert.Equal(t, `{"k":"E","pad":"x"}`, d1.Marshal())
	assert.Equal(t, d1.Marshal(), d2.Marshal())
	assert.Equal(t, d1.GarbageLen(), d2.GarbageLen())
	assert.Equal(t, d1.DocSize(), d2.DocSize())
	// The replicas must agree on C's removedAt: it decides when C becomes
	// collectable, and the snapshot the server builds carries it to every
	// client that attaches. D is left out on purpose -- whichever of the undo
	// and E evicts it first stamps its removedAt, so the two replicas differ
	// there by delivery order. That predates the refusal and is not what this
	// pins.
	assert.Equal(t, memberRemovedAt(t, d1.RootObject(), "C"), memberRemovedAt(t, d2.RootObject(), "C"),
		"the replicas disagree on the restored value's removedAt")
	assertRebuildsSame(t, d1, "restore won first")
	assertRebuildsSame(t, d2, "restore lost on arrival")

	vector := helper.MaxVersionVector(actor1, actor2)
	d1.GarbageCollect(vector)
	d2.GarbageCollect(vector)
	assert.Equal(t, 0, d1.GarbageLen())
	assert.Equal(t, 0, d2.GarbageLen())
	assert.Equal(t, d1.DocSize(), d2.DocSize())
}

// memberRemovedAt returns the removedAt of the one member of obj whose value
// is v, tombstones included. It fails the test unless exactly one node holds
// v and that node is removed, so a comparison of two replicas cannot pass by
// both missing the member.
func memberRemovedAt(t *testing.T, obj *crdt.Object, v string) string {
	t.Helper()

	var found []crdt.Element
	for _, node := range obj.RHTNodes() {
		if node.Element().Marshal() == `"`+v+`"` {
			found = append(found, node.Element())
		}
	}
	require.Len(t, found, 1, "nodes holding %q", v)
	require.NotNil(t, found[0].RemovedAt(), "%q is not removed", v)
	return found[0].RemovedAt().Key()
}

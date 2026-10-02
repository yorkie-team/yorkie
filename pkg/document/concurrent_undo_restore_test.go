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

// TestConcurrentUndoRestoresSameValue pins that two replicas undoing their
// concurrent overwrites of the same key converge, and stay collectable.
//
// d1 and d2 each overwrite k, whose value C every replica holds, and each
// undo. Both undos restore a copy of C under C's own createdAt, with
// different executedAt tickets. d3's Set of k to X lands between those two
// tickets, so each replica meets the restores and X in a different order.
// Every order has to end with the newest restore holding k, and with one node
// per createdAt so that the document can still be rebuilt from its own
// content after collection.
func TestConcurrentUndoRestoresSameValue(t *testing.T) {
	actor1, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	actor2, err := time.ActorIDFromHex("000000000000000000000003")
	require.NoError(t, err)
	actor3, err := time.ActorIDFromHex("000000000000000000000002")
	require.NoError(t, err)

	newDoc := func(actor time.ActorID) *document.Document {
		d := document.New("concurrent-undo-restore")
		d.SetActor(actor)
		return d
	}
	d1, d2, d3 := newDoc(actor1), newDoc(actor2), newDoc(actor3)
	set := func(d *document.Document, k, v string) {
		t.Helper()
		require.NoError(t, d.Update(func(r *json.Object, _ *presence.Presence) error {
			r.SetString(k, v)
			return nil
		}))
	}

	set(d1, "k", "C")
	broadcastChanges(t, d1, d2, d3)

	// d1 advances its clock, then overwrites k and undoes it.
	set(d1, "p1", "1")
	set(d1, "p1", "2")
	set(d1, "p1", "3")
	set(d1, "k", "A")
	require.NoError(t, d1.Undo())

	// d2 overwrites k early, learns d3's unrelated edits, then undoes, so
	// its restore is issued with a ticket newer than d1's.
	set(d2, "k", "B")
	for _, v := range []string{"a", "b", "c", "d", "e", "f"} {
		set(d3, "p3", v)
	}
	broadcastChanges(t, d3, d1, d2)
	require.NoError(t, d2.Undo())

	// d3's Set falls between the two restore tickets.
	set(d3, "k", "X")

	broadcastChanges(t, d1, d2, d3)
	broadcastChanges(t, d2, d1, d3)
	broadcastChanges(t, d3, d1, d2)

	const converged = `{"k":"C","p1":"3","p3":"f"}`
	for i, d := range []*document.Document{d1, d2, d3} {
		assert.Equal(t, converged, d.Marshal(), "d%d before collection", i+1)
	}

	vector := helper.MaxVersionVector(actor1, actor2, actor3)
	for i, d := range []*document.Document{d1, d2, d3} {
		d.GarbageCollect(vector)
		assert.Equal(t, 0, d.GarbageLen(), "d%d garbage after collection", i+1)
		assert.Equal(t, converged, d.Marshal(), "d%d after collection", i+1)
		assert.Equal(t, d1.DocSize(), d.DocSize(), "d%d size after collection", i+1)
		assertRebuildsSame(t, d, "after collection")
	}
}

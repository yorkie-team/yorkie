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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// pushChanges hands from's pending changes to every peer the way the server
// takes them: encoded, then decoded through the push boundary. A rejection
// fails the test, since every history below is one a replica really emits.
func pushChanges(t *testing.T, from *document.Document, tos ...*document.Document) {
	t.Helper()

	pack := from.CreateChangePack()
	pbPack, err := converter.ToChangePack(pack)
	require.NoError(t, err)
	pushed, err := converter.FromPushedChangePack(pbPack)
	require.NoError(t, err, "the push boundary rejected a change a replica emitted")

	for _, to := range tos {
		require.NoError(t, to.ApplyChangePack(change.NewPack(
			pushed.DocumentKey,
			change.NewCheckpoint(0, 0),
			pushed.Changes,
			time.InitialVersionVector,
			nil,
		)))
	}
	require.NoError(t, from.ApplyChangePack(change.NewPack(
		pack.DocumentKey,
		pack.Checkpoint,
		nil,
		time.InitialVersionVector,
		nil,
	)))
}

func actorForTest(t *testing.T, hex string) time.ActorID {
	t.Helper()
	actor, err := time.ActorIDFromHex(hex)
	require.NoError(t, err)
	return actor
}

// TestPushBoundaryAcceptsReplicaHistories pins that ValidatePushedOperations
// rejects nothing a replica emits. Each history drives the shapes its rules
// sit next to through FromPushedChangePack: restored values with old
// createdAts, re-identified array values with older movedAts, displaced
// tombstones nested in restored containers, and tickets issued before attach.
func TestPushBoundaryAcceptsReplicaHistories(t *testing.T) {
	update := func(t *testing.T, d *document.Document, fn func(r *json.Object)) {
		t.Helper()
		require.NoError(t, d.Update(func(r *json.Object, _ *presence.Presence) error {
			fn(r)
			return nil
		}))
	}

	t.Run("object overwrite, removal, undo and redo", func(t *testing.T) {
		d1, d2 := document.New("pushed"), document.New("pushed")
		update(t, d1, func(r *json.Object) {
			o := r.SetNewObject("o")
			o.SetString("a", "1")
			o.SetNewObject("n").SetInteger("x", 1)
		})
		update(t, d1, func(r *json.Object) { r.GetObject("o").SetString("a", "2") })
		update(t, d1, func(r *json.Object) { r.GetObject("o").Delete("n") })
		update(t, d1, func(r *json.Object) { r.SetString("o", "replaced") })
		pushChanges(t, d1, d2)

		for range 4 {
			require.NoError(t, d1.Undo())
			pushChanges(t, d1, d2)
		}
		for range 4 {
			require.NoError(t, d1.Redo())
			pushChanges(t, d1, d2)
		}
		assert.Equal(t, d1.Marshal(), d2.Marshal())
	})

	t.Run("array add, move, set and delete, undo and redo", func(t *testing.T) {
		d1, d2 := document.New("pushed"), document.New("pushed")
		update(t, d1, func(r *json.Object) {
			arr := r.SetNewArray("arr")
			arr.AddInteger(1, 2, 3)
			arr.AddNewObject().SetString("k", "v")
		})
		update(t, d1, func(r *json.Object) { r.GetArray("arr").MoveAfterByIndex(2, 0) })
		update(t, d1, func(r *json.Object) { r.GetArray("arr").SetInteger(1, 20) })
		update(t, d1, func(r *json.Object) { r.GetArray("arr").Delete(0) })
		update(t, d1, func(r *json.Object) { r.GetArray("arr").Delete(2) })
		pushChanges(t, d1, d2)

		for range 5 {
			require.NoError(t, d1.Undo())
			pushChanges(t, d1, d2)
		}
		for range 5 {
			require.NoError(t, d1.Redo())
			pushChanges(t, d1, d2)
		}
		assert.Equal(t, d1.Marshal(), d2.Marshal())
	})

	t.Run("concurrent removals of one array element, both undone, then copied whole", func(t *testing.T) {
		// Each undo restores the element as a copy re-identified at its root
		// only, so the array ends up holding two live copies and a tombstone
		// whose descendants share every createdAt. Removing and restoring the
		// array then sends all three in one payload.
		d1, d2 := document.New("pushed"), document.New("pushed")
		d1.SetActor(actorForTest(t, "000000000000000000000001"))
		d2.SetActor(actorForTest(t, "000000000000000000000002"))
		update(t, d1, func(r *json.Object) {
			r.SetNewArray("arr").AddNewObject().SetNewObject("n").SetString("m", "v")
		})
		pushChanges(t, d1, d2)

		update(t, d1, func(r *json.Object) { r.GetArray("arr").Delete(0) })
		update(t, d2, func(r *json.Object) { r.GetArray("arr").Delete(0) })
		require.NoError(t, d1.Undo())
		require.NoError(t, d2.Undo())
		pushChanges(t, d1, d2)
		pushChanges(t, d2, d1)
		assert.Equal(t, d1.Marshal(), d2.Marshal())

		update(t, d1, func(r *json.Object) { r.Delete("arr") })
		pushChanges(t, d1, d2)
		require.NoError(t, d1.Undo())
		pushChanges(t, d1, d2)
		assert.Equal(t, d1.Marshal(), d2.Marshal())
	})

	t.Run("array set redo after a peer removed the target", func(t *testing.T) {
		d1, d2 := document.New("pushed"), document.New("pushed")
		update(t, d1, func(r *json.Object) { r.SetNewArray("arr").AddInteger(1) })
		pushChanges(t, d1, d2)
		update(t, d1, func(r *json.Object) { r.GetArray("arr").SetInteger(0, 2) })
		pushChanges(t, d1, d2)
		update(t, d2, func(r *json.Object) { r.GetArray("arr").Delete(0) })
		pushChanges(t, d2, d1)

		require.NoError(t, d1.Undo())
		pushChanges(t, d1, d2)
		// The redo re-inserts the removed value under a fresh createdAt with
		// its older removedAt -- the shape the ArraySet exemption is for. The
		// replicas disagree afterwards on main; fixing the reverse is a
		// separate change (undo/redo semantics), so only acceptance is pinned.
		require.NoError(t, d1.Redo())
		var reinserted bool
		for _, c := range d1.CreateChangePack().Changes {
			for _, op := range c.Operations() {
				if set, ok := op.(*operations.ArraySet); ok && set.Value().RemovedAt() != nil {
					reinserted = !set.Value().RemovedAt().After(set.Value().CreatedAt())
				}
			}
		}
		require.True(t, reinserted, "the redo no longer carries the exempt shape")
		pushChanges(t, d1, d2)
	})

	t.Run("concurrent undos restoring the same value", func(t *testing.T) {
		// The history of TestConcurrentUndoRestoresSameValue in #2100: two
		// restores of one value under one createdAt, and a Set between them.
		newDoc := func(hex string) *document.Document {
			d := document.New("pushed")
			d.SetActor(actorForTest(t, hex))
			return d
		}
		d1 := newDoc("000000000000000000000001")
		d2 := newDoc("000000000000000000000003")
		d3 := newDoc("000000000000000000000002")
		set := func(d *document.Document, k, v string) {
			update(t, d, func(r *json.Object) { r.SetString(k, v) })
		}

		set(d1, "k", "C")
		pushChanges(t, d1, d2, d3)
		set(d1, "p1", "1")
		set(d1, "p1", "2")
		set(d1, "k", "A")
		require.NoError(t, d1.Undo())
		set(d2, "k", "B")
		for _, v := range []string{"a", "b", "c", "d"} {
			set(d3, "p3", v)
		}
		pushChanges(t, d3, d1, d2)
		require.NoError(t, d2.Undo())
		set(d3, "k", "X")

		pushChanges(t, d1, d2, d3)
		pushChanges(t, d2, d1, d3)
		pushChanges(t, d3, d1, d2)
		assert.Equal(t, d1.Marshal(), d2.Marshal())
		assert.Equal(t, d1.Marshal(), d3.Marshal())
	})

	t.Run("two clients set the same key before attach", func(t *testing.T) {
		// Both texts are created under InitialActorID at the same lamport, so
		// they share one createdAt; attaching rewrites only the operations'
		// executedAt. This is what BenchmarkRPC's "attach large document"
		// does with two 10 MB texts.
		const size = 4096
		d1, d2 := document.New("pushed"), document.New("pushed")
		for _, d := range []*document.Document{d1, d2} {
			update(t, d, func(r *json.Object) { r.SetNewText("k1").Edit(0, 0, strings.Repeat("a", size)) })
		}
		setValueCreatedAt := func(d *document.Document) string {
			for _, c := range d.CreateChangePack().Changes {
				for _, op := range c.Operations() {
					if set, ok := op.(*operations.Set); ok {
						return set.Value().CreatedAt().Key()
					}
				}
			}
			return ""
		}
		require.Equal(t, setValueCreatedAt(d1), setValueCreatedAt(d2))
		d1.SetActor(actorForTest(t, "000000000000000000000001"))
		d2.SetActor(actorForTest(t, "000000000000000000000002"))

		pushChanges(t, d1, d2)
		pushChanges(t, d2, d1)

		assert.Equal(t, d1.Marshal(), d2.Marshal())
		for i, d := range []*document.Document{d1, d2} {
			assert.Less(t, d.DocSize().Live.Data, 3*size,
				"d%d holds more than both texts after a pre-attach collision", i+1)
		}
	})
}

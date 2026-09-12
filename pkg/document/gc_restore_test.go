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
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

// deliverChanges pushes the sender's pending changes into the receiver,
// mimicking a server round-trip. The receiver applies them with
// OpSourceRemote, which is also how the server itself replays a change log to
// build a snapshot -- the path that decides what every later joiner sees.
//
// The ack half matters as much as the push. CreateChangePack does not clear
// localChanges; the server's response does, by returning a checkpoint that
// ApplyChangePack walks the queue against. Without it the sender re-pushes
// every change it has ever made on the next call, and the receiver applies
// change 1 twice -- which no real peer does: pushPull drops a change whose
// clientSeq is at or below the stored checkpoint on the way in, and drops a
// client's own changes on the way back out, precisely because operations are
// not idempotent. Replaying one twice diverges the replicas on its own --
// with no undo involved at all, see
// docs/tasks/active/20260911-duplicate-change-application-not-idempotent-todo.md
// -- and would mask the defect these tests are about.
func deliverChanges(t *testing.T, from, to *document.Document) {
	t.Helper()
	pack := from.CreateChangePack()
	require.NoError(t, to.ApplyChangePack(change.NewPack(
		pack.DocumentKey,
		change.NewCheckpoint(0, 0),
		pack.Changes,
		time.InitialVersionVector,
		nil,
	)))
	require.NoError(t, from.ApplyChangePack(change.NewPack(
		pack.DocumentKey,
		pack.Checkpoint,
		nil,
		time.InitialVersionVector,
		nil,
	)))
}

// TestUndoneObjectRemoveSurvivesCollection pins that a member restored by
// undoing its removal is still there after collection, on every replica.
//
// Undoing the removal of an object member restores it under its original
// createdAt. `ElementRHT.SetWithExecutedAt` re-keys `nodeMapByCreatedAt` onto
// the restored node, so the entry left in `gcElementPairMap` by the removal
// now resolves, through that index, to the live member -- and `purge` deletes
// it. The replica that performed the undo is spared because `Set.Execute`
// deregisters the stale entry, but it only does so for `OpSourceUndoRedo`, so
// every peer and the server keep it.
func TestUndoneObjectRemoveSurvivesCollection(t *testing.T) {
	d1 := document.New("restore-gc")
	d2 := document.New("restore-gc")

	require.NoError(t, d1.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewObject("obj").SetInteger("k", 1)
		r.SetInteger("keep", 0)
		return nil
	}))
	deliverChanges(t, d1, d2)

	require.NoError(t, d1.Update(func(r *json.Object, _ *presence.Presence) error {
		r.Delete("obj")
		return nil
	}, "remove obj"))
	require.NoError(t, d1.Undo())
	deliverChanges(t, d1, d2)

	assert.Equal(t, `{"keep":0,"obj":{"k":1}}`, d1.Marshal())
	assert.Equal(t, `{"keep":0,"obj":{"k":1}}`, d2.Marshal())

	vector := helper.MaxVersionVector(d1.ActorID(), d2.ActorID())
	d1.GarbageCollect(vector)
	d2.GarbageCollect(vector)

	assert.Equal(t, `{"keep":0,"obj":{"k":1}}`, d1.Marshal())
	assert.Equal(t, `{"keep":0,"obj":{"k":1}}`, d2.Marshal(),
		"collection deleted the restored member on the replica that did not undo")
}

// TestUndoneObjectRemoveLeavesNoGarbage pins that the tombstone the removal
// registered is actually collected, on every replica.
//
// The stale entry a peer keeps resolves to the restored member, whose
// removedAt is nil, so collection can never take it: the worklist entry and
// the size charged against it stay for the life of the document.
func TestUndoneObjectRemoveLeavesNoGarbage(t *testing.T) {
	d1 := document.New("restore-gc-len")
	d2 := document.New("restore-gc-len")

	require.NoError(t, d1.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewObject("obj").SetInteger("k", 1)
		return nil
	}))
	deliverChanges(t, d1, d2)

	require.NoError(t, d1.Update(func(r *json.Object, _ *presence.Presence) error {
		r.Delete("obj")
		return nil
	}, "remove obj"))
	require.NoError(t, d1.Undo())
	deliverChanges(t, d1, d2)

	vector := helper.MaxVersionVector(d1.ActorID(), d2.ActorID())
	d1.GarbageCollect(vector)
	d2.GarbageCollect(vector)

	assert.Equal(t, 0, d1.GarbageLen())
	assert.Equal(t, 0, d2.GarbageLen(),
		"the peer holds a worklist entry that can never be collected")
	assert.Equal(t, d1.DocSize().GC, d2.DocSize().GC,
		"the peer charges garbage the undoing replica does not")
}

// TestUndoneObjectRemoveStaysAddressableAfterCollection pins that the
// restored member is still reachable by its identity after collection, not
// merely still present in the marshalled output.
//
// Marshal reads ElementRHT.nodeMapByKey; every later operation reaches the
// member through Root.elementMap and nodeMapByCreatedAt instead. A fix that
// only stopped purge from unlinking the key -- by checking that the node it
// found holds the element it was asked to purge -- would leave collection to
// deregister the stale pair's element anyway, and deregisterElement deletes
// Root.elementMap under that createdAt: the identity the restored member now
// owns. The member would survive the assertions above and the next change
// touching it would fail to apply, which on the server aborts a snapshot
// rebuild. That is a worse outcome than the data loss it replaces, so pin
// the difference rather than the symptom.
func TestUndoneObjectRemoveStaysAddressableAfterCollection(t *testing.T) {
	d1 := document.New("restore-gc-addressable")
	d2 := document.New("restore-gc-addressable")

	require.NoError(t, d1.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewObject("obj").SetInteger("k", 1)
		return nil
	}))
	deliverChanges(t, d1, d2)

	require.NoError(t, d1.Update(func(r *json.Object, _ *presence.Presence) error {
		r.Delete("obj")
		return nil
	}, "remove obj"))
	require.NoError(t, d1.Undo())
	deliverChanges(t, d1, d2)

	vector := helper.MaxVersionVector(d1.ActorID(), d2.ActorID())
	d1.GarbageCollect(vector)
	d2.GarbageCollect(vector)

	// Written on the replica that undid, applied on the one that did not:
	// the write resolves the member through d1's indexes and the apply
	// through d2's, so both halves of the identity have to have survived.
	require.NoError(t, d1.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetObject("obj").SetInteger("x", 5)
		return nil
	}))
	require.NoError(t, d1.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetObject("obj").Delete("k")
		return nil
	}))
	deliverChanges(t, d1, d2)

	assert.Equal(t, `{"obj":{"x":5}}`, d1.Marshal())
	assert.Equal(t, `{"obj":{"x":5}}`, d2.Marshal(),
		"the restored member is no longer reachable by its createdAt")
}

// TestUndoneArrayRemoveSurvivesCollection is the array counterpart of
// TestUndoneObjectRemoveSurvivesCollection. It passes on main, and is here to
// hold that line: the array path is immune only because executeUndoRedo
// reissues the restored element's createdAt (document.go:449-451) instead of
// restoring it under its original one, so the tombstone and the restored
// element never share a key in RGATreeList.elementMapByCreatedAt and
// `purge` cannot reach the live element through the dead one's identity. The
// comment there names this exact hazard. Any change that makes the array
// restore identity-preserving -- the revive work this release defers --
// reintroduces the object bug here, and this test is what catches it.
func TestUndoneArrayRemoveSurvivesCollection(t *testing.T) {
	d1 := document.New("restore-gc-array")
	d2 := document.New("restore-gc-array")

	require.NoError(t, d1.Update(func(r *json.Object, _ *presence.Presence) error {
		a := r.SetNewArray("arr")
		a.AddNewObject().SetInteger("k", 1)
		a.AddInteger(9)
		return nil
	}))
	deliverChanges(t, d1, d2)

	require.NoError(t, d1.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").Delete(0)
		return nil
	}, "remove arr[0]"))
	require.NoError(t, d1.Undo())
	deliverChanges(t, d1, d2)

	assert.Equal(t, `{"arr":[{"k":1},9]}`, d1.Marshal())
	assert.Equal(t, `{"arr":[{"k":1},9]}`, d2.Marshal())

	vector := helper.MaxVersionVector(d1.ActorID(), d2.ActorID())
	d1.GarbageCollect(vector)
	d2.GarbageCollect(vector)

	assert.Equal(t, `{"arr":[{"k":1},9]}`, d1.Marshal())
	assert.Equal(t, `{"arr":[{"k":1},9]}`, d2.Marshal(),
		"collection deleted the restored element on the replica that did not undo")
	assert.Equal(t, 0, d1.GarbageLen())
	assert.Equal(t, 0, d2.GarbageLen())
}

// broadcastChanges is deliverChanges for more than one receiver. The ack has
// to happen once, after every receiver has the pack: it clears the sender's
// localChanges, so acking per receiver would leave the second one with
// nothing to apply.
func broadcastChanges(t *testing.T, from *document.Document, tos ...*document.Document) {
	t.Helper()
	pack := from.CreateChangePack()
	for _, to := range tos {
		require.NoError(t, to.ApplyChangePack(change.NewPack(
			pack.DocumentKey,
			change.NewCheckpoint(0, 0),
			pack.Changes,
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

// TestRestoredContainerKeepsForeignDescendantsAddressable pins that restoring
// a container does not cost the identities of members it does not carry.
//
// The reverse of a Remove is a Set of `value.DeepCopy()`, taken when the
// removal was recorded. A peer that added a member into the container after
// that copy was taken has a tombstone whose descendant set is a strict
// superset of the copy's. Retiring the tombstone by deregistering it and its
// descendants evicts those extra members from `elementMap`, and nothing puts
// them back -- so the next change addressed at one of them fails with
// ErrNotApplicableDataType.
//
// That failure is not confined to the replica it happens on. The server
// rebuilds documents and snapshots by replaying the stored change log
// (`server/packs/snapshot.go`, with OpSourceReplay), so a change that cannot
// apply makes the document unloadable from then on, for everyone.
//
// The restored container still diverges -- d2's member is not in the copy, so
// its edit lands on an orphaned subtree and is invisible. That is the
// identity-preserving revive work this release defers. What must hold here is
// narrower and is the whole point of the release: the change log stays
// replayable.
func TestRestoredContainerKeepsForeignDescendantsAddressable(t *testing.T) {
	for _, tc := range []struct {
		name    string
		collect bool
	}{
		{"undo arriving as a remote change", false},
		// The server collects between syncs, so the tombstone is gone by the
		// time the late change arrives. Nothing may depend on it still being
		// registered.
		{"with a collection pass before the late change", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d1 := document.New("restore-foreign")
			d2 := document.New("restore-foreign")
			d3 := document.New("restore-foreign")

			require.NoError(t, d1.Update(func(r *json.Object, _ *presence.Presence) error {
				r.SetNewObject("obj").SetInteger("k", 1)
				return nil
			}))
			broadcastChanges(t, d1, d2, d3)

			// d2 adds a member inside obj. d1 never sees it, so the copy its
			// undo carries will not contain it.
			require.NoError(t, d2.Update(func(r *json.Object, _ *presence.Presence) error {
				r.GetObject("obj").SetNewObject("n").SetInteger("y", 1)
				return nil
			}))
			broadcastChanges(t, d2, d3)

			require.NoError(t, d1.Update(func(r *json.Object, _ *presence.Presence) error {
				r.Delete("obj")
				return nil
			}, "remove obj"))
			require.NoError(t, d1.Undo())
			deliverChanges(t, d1, d3)

			if tc.collect {
				d3.GarbageCollect(helper.MaxVersionVector(
					d1.ActorID(), d2.ActorID(), d3.ActorID()))
			}

			// d2, which has not seen the removal, edits the member it added.
			require.NoError(t, d2.Update(func(r *json.Object, _ *presence.Presence) error {
				r.GetObject("obj").GetObject("n").SetInteger("x", 5)
				return nil
			}))

			pack := d2.CreateChangePack()
			assert.NoError(t, d3.ApplyChangePack(change.NewPack(
				pack.DocumentKey,
				change.NewCheckpoint(0, 0),
				pack.Changes,
				time.InitialVersionVector,
				nil,
			)), "the peer's change no longer applies, so the stored change log is unreplayable")
		})
	}
}

// TestRepeatedRestoreAndRemoveCollects pins that a createdAt can be restored
// and removed again without collection tripping over the tombstones that
// accumulate under it.
//
// Each cycle leaves another tombstone answering to the same createdAt. A
// collection worklist or a purge that resolves an entry through a
// createdAt-keyed index rather than through the element it was registered for
// will, on the second pass, either unlink a live member on a dead one's
// behalf or fail to find the node at all -- and `Document.GarbageCollect`
// turns that error into a panic.
//
// Map iteration order decides which entry a pass reaches first, so a single
// run proves little; this repeats.
func TestRepeatedRestoreAndRemoveCollects(t *testing.T) {
	for range 200 {
		d := document.New("restore-repeat")

		require.NoError(t, d.Update(func(r *json.Object, _ *presence.Presence) error {
			r.SetNewObject("o").SetInteger("k", 1)
			return nil
		}))

		for range 3 {
			require.NoError(t, d.Update(func(r *json.Object, _ *presence.Presence) error {
				r.Delete("o")
				return nil
			}, "remove o"))
			require.NoError(t, d.Undo())
		}

		require.NoError(t, d.Update(func(r *json.Object, _ *presence.Presence) error {
			r.Delete("o")
			return nil
		}, "remove o"))

		d.GarbageCollect(helper.MaxVersionVector(d.ActorID()))
		assert.Equal(t, `{}`, d.Marshal())
		assert.Equal(t, 0, d.GarbageLen())
	}
}

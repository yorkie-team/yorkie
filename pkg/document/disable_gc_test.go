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

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestDisableGCKeepsChangeVVAtSizeOne pins the contract from
// docs/design/disable-gc-on-attach.md: under disable_gc, every locally
// produced Change must carry a version vector of size 1, regardless of how
// many other actors have been seen via ApplyChanges. Without the
// SyncLamport branch in InternalDocument.ApplyChanges, the per-Change VV
// would grow to O(num_actors) and erase the wire savings the opt-out is
// supposed to deliver.
func TestDisableGCKeepsChangeVVAtSizeOne(t *testing.T) {
	const numActors = 5

	docs := make([]*document.Document, numActors)
	for i := 0; i < numActors; i++ {
		actor, err := time.ActorIDFromHex(fmt.Sprintf("0000000000000000000000%02d", i+1))
		assert.NoError(t, err)
		d := document.New("disable-gc")
		d.SetActor(actor)
		d.SetDisableGC(true)
		docs[i] = d
	}

	// docs[0] creates the counter and broadcasts the create change to all
	// peers so they share the root element id.
	assert.NoError(t, docs[0].Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewCounter("c", 0)
		return nil
	}))
	createPack := docs[0].CreateChangePack()
	for j := 1; j < numActors; j++ {
		// CreateChangePack returns pack.VersionVector aliased to the
		// sender's internal VV; copy per delivery to avoid corruption.
		pCopy := *createPack
		pCopy.VersionVector = createPack.VersionVector.DeepCopy()
		pCopy.VersionVector.Set(docs[j].ActorID(),
			docs[j].VersionVector().VersionOf(docs[j].ActorID()))
		assert.NoError(t, docs[j].ApplyChangePack(&pCopy))
	}

	// Run three rounds of fully-connected fanout. After the very first
	// round every actor has seen every other actor at least once; if VV
	// merging were still happening, doc.VV would jump to numActors.
	for round := 1; round <= 3; round++ {
		for i := 0; i < numActors; i++ {
			assert.NoError(t, docs[i].Update(func(r *json.Object, _ *presence.Presence) error {
				r.GetCounter("c").Increase(1)
				return nil
			}))

			pack := docs[i].CreateChangePack()
			vv := pack.Changes[len(pack.Changes)-1].ID().VersionVector()
			assert.Equal(t, 1, len(vv),
				"round=%d actor=%d local Change.VV must stay size 1, got %s",
				round, i, vv.Marshal())

			for j := 0; j < numActors; j++ {
				if j == i {
					continue
				}
				pCopy := *pack
				pCopy.VersionVector = pack.VersionVector.DeepCopy()
				pCopy.VersionVector.Set(docs[j].ActorID(),
					docs[j].VersionVector().VersionOf(docs[j].ActorID()))
				assert.NoError(t, docs[j].ApplyChangePack(&pCopy))
			}
		}

		for i := 0; i < numActors; i++ {
			vv := docs[i].VersionVector()
			assert.Equal(t, 1, len(vv),
				"round=%d actor=%d doc.VV must stay size 1, got %s",
				round, i, vv.Marshal())
		}
	}
}

// TestDisableGCStillAdvancesLamport verifies that opting out of GC does
// not stop lamport progression — TimeTickets produced after consuming a
// remote change must still be ordered against it. Without lamport sync,
// concurrent ticks from this client would collide with remote element ids.
func TestDisableGCStillAdvancesLamport(t *testing.T) {
	actorA, err := time.ActorIDFromHex("000000000000000000000001")
	assert.NoError(t, err)
	actorB, err := time.ActorIDFromHex("000000000000000000000002")
	assert.NoError(t, err)

	docA := document.New("lamport")
	docA.SetActor(actorA)
	docB := document.New("lamport")
	docB.SetActor(actorB)
	docB.SetDisableGC(true)

	// A advances its lamport by writing twice.
	assert.NoError(t, docA.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewCounter("c", 0).Increase(1)
		return nil
	}))
	assert.NoError(t, docA.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetCounter("c").Increase(1)
		return nil
	}))
	beforeLamport := docB.InternalDocument().Lamport()

	packA := docA.CreateChangePack()
	pCopy := *packA
	pCopy.VersionVector = packA.VersionVector.DeepCopy()
	pCopy.VersionVector.Set(docB.ActorID(), docB.VersionVector().VersionOf(docB.ActorID()))
	assert.NoError(t, docB.ApplyChangePack(&pCopy))

	assert.Greater(t, docB.InternalDocument().Lamport(), beforeLamport,
		"disable_gc must still advance lamport on incoming remote changes")
	assert.Equal(t, 1, len(docB.VersionVector()),
		"disable_gc must keep doc.VV at size 1 after consuming remote changes")
}

// TestDisableGCSnapshotPullCatchesUpLamport pins the client-side contract
// for the snapshot pull path under disable_gc: the server sends a snapshot
// together with a single-entry VV keyed by the receiving client's actor
// and carrying the doc's max lamport (see server/packs/pushpull.go
// pullPack). The client's applySnapshot must use that lamport to advance
// its change clock and keep doc.VV at size 1.
func TestDisableGCSnapshotPullCatchesUpLamport(t *testing.T) {
	actorA, err := time.ActorIDFromHex("000000000000000000000001")
	assert.NoError(t, err)
	actorB, err := time.ActorIDFromHex("000000000000000000000002")
	assert.NoError(t, err)

	// 01. docA accumulates lamport by making many local updates.
	docA := document.New("snap-contract")
	docA.SetActor(actorA)
	const numUpdates = 20
	assert.NoError(t, docA.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewCounter("c", 0)
		return nil
	}))
	for i := 0; i < numUpdates; i++ {
		assert.NoError(t, docA.Update(func(r *json.Object, _ *presence.Presence) error {
			r.GetCounter("c").Increase(1)
			return nil
		}))
	}
	docALamport := docA.InternalDocument().Lamport()
	assert.GreaterOrEqual(t, docALamport, int64(numUpdates),
		"sanity: docA's lamport must reflect its updates")

	// 02. Build the snapshot bytes the server would persist.
	snapshotBytes, err := converter.SnapshotToBytes(docA.RootObject(), docA.AllPresences())
	assert.NoError(t, err)

	// 03. Opt-out docB receives the snapshot. Mirror what the server now
	//     sends: a single-entry VV keyed by the receiving client's actor
	//     and carrying the doc's max lamport. This keeps the wire footprint
	//     at one entry while letting the client catch its clock up.
	docB := document.New("snap-contract")
	docB.SetActor(actorB)
	docB.SetDisableGC(true)

	snapshotPack := change.NewPack(
		docB.Key(),
		change.InitialCheckpoint,
		nil,
		time.VersionVector{docB.ActorID(): docALamport},
		snapshotBytes,
	)
	assert.NoError(t, docB.ApplyChangePack(snapshotPack))

	docBLamport := docB.InternalDocument().Lamport()
	assert.GreaterOrEqual(t, docBLamport, docALamport,
		"opt-out client must catch up to the snapshot's lamport (server=%d got=%d)",
		docALamport, docBLamport)
	assert.Equal(t, 1, len(docB.VersionVector()),
		"opt-out doc.VV must stay at size 1 after snapshot pull, got %s",
		docB.VersionVector().Marshal())

	// 04. The next produced local Change must therefore start at a
	//     lamport beyond the server's, and still carry a size-1 VV.
	assert.NoError(t, docB.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetCounter("c").Increase(1)
		return nil
	}))
	newPack := docB.CreateChangePack()
	newChange := newPack.Changes[len(newPack.Changes)-1]
	assert.Greater(t, newChange.ID().Lamport(), docALamport,
		"new local Change.lamport must exceed server's previous max")
	assert.Equal(t, 1, len(newChange.ID().VersionVector()),
		"new local Change.VV must stay size 1, got %s",
		newChange.ID().VersionVector().Marshal())
}

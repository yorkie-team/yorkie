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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// vectorCovering returns a version vector that has observed every ticket up
// to and including the given one, as the collection vector of a replica that
// has seen that removal does.
func vectorCovering(ticket *time.Ticket) time.VersionVector {
	vector := time.NewVersionVector()
	vector.Set(ticket.ActorID(), ticket.Lamport())
	return vector
}

// TestTreeDoesNotHoldTombstoneForChildren pins that a removed element is not
// held for its children. A live child under it has no GC pair of its own, so
// a hold for one would never end and would pin the element, and its bytes in
// docSize.GC, for the life of the document.
func TestTreeDoesNotHoldTombstoneForChildren(t *testing.T) {
	f := newMergeLineageFixture(t)
	removedAt := f.nextTicket()
	f.p2.remove(removedAt)

	require.Nil(t, f.text.removedAt)
	assert.False(t, f.tree.HoldsTombstone(f.p2, vectorCovering(removedAt), nil),
		"a live child is on no worklist, so it must not hold the element")

	f.text.remove(f.nextTicket())
	assert.False(t, f.tree.HoldsTombstone(f.p2, time.NewVersionVector(), nil),
		"a removed child does not hold the element either")
}

// TestTreeHoldsTombstoneForMergeSource pins the GCHold rule: a removed node a
// merge moved is the §1.1 boundary for positions declared inside its merge
// source, so it outlives its own removal until every replica has seen the
// merge -- and no longer.
func TestTreeHoldsTombstoneForMergeSource(t *testing.T) {
	// merged stamps text as moved out of p3 into p2 by a merge at the ticket
	// mergedAt returns (the fixture's next one when nil), tombstones p3,
	// removes text, and re-reads the tree as a decode does, which derives
	// p3's forwarding pointer from the stamp. It returns the fixture, the
	// merge ticket and the text's removal.
	merged := func(t *testing.T, mergedAt *time.Ticket) (*mergeLineageFixture, *time.Ticket, *time.Ticket) {
		t.Helper()
		f := newMergeLineageFixture(t)
		if mergedAt == nil {
			mergedAt = f.nextTicket()
		}
		f.p3.remove(mergedAt)
		f.text.MergedFrom = f.p3.id
		f.text.MergedAt = mergedAt
		removedAt := f.nextTicket()
		f.text.remove(removedAt)
		f.tree = NewTree(f.tree.Root(), f.nextTicket())
		require.NotNil(t, f.p3.mergedInto)
		return f, mergedAt, removedAt
	}

	t.Run("the merge is not yet common knowledge", func(t *testing.T) {
		f, mergedAt, _ := merged(t, nil)
		seen := vectorCovering(f.nextTicket())

		assert.True(t, f.tree.HoldsTombstone(f.text, time.NewVersionVector(), seen),
			"a peer that has not seen the merge still resolves positions through it")
		assert.False(t, f.tree.HoldsTombstone(f.text, vectorCovering(mergedAt), seen),
			"once every replica has seen the merge the boundary is free")
	})

	// The source's removedAt is last-writer-wins: a later delete of the
	// source overwrites it on the replicas that applied that delete only. The
	// hold reads the merge ticket the child carries, which every replica has,
	// so they release together.
	t.Run("a later delete overwrote the source's removedAt", func(t *testing.T) {
		peer, err := time.ActorIDFromHex("0000000000000000000000dd")
		require.NoError(t, err)
		f, mergedAt, _ := merged(t, nil)
		f.p3.remove(time.NewTicket(1<<20, 0, peer))
		seen := vectorCovering(f.nextTicket())
		seen.Set(peer, 1<<20)

		assert.False(t, f.tree.HoldsTombstone(f.text, vectorCovering(mergedAt), seen),
			"covering the merge releases the hold whatever deleted the source since")
	})

	// MergedFrom rides in on a client-supplied element payload, so a stamp can
	// name an element that is LIVE -- either forged, or left behind by a
	// snapshot written before a revive dissolved the lineage. Holding for it
	// would pin the tombstone, and its bytes in docSize.GC, for the life of
	// the document: GC could never collect it again.
	t.Run("source is live", func(t *testing.T) {
		f := newMergeLineageFixture(t)
		f.text.MergedFrom = f.p3.id
		f.text.MergedAt = f.nextTicket()
		f.text.remove(f.nextTicket())

		assert.False(t, f.tree.HoldsTombstone(f.text, time.NewVersionVector(), vectorCovering(f.nextTicket())),
			"a live element is not a merge source, so it pins nothing")
	})

	t.Run("source names nothing", func(t *testing.T) {
		f := newMergeLineageFixture(t)
		f.text.MergedFrom = f.forgedIDNamingNone(f.p3)
		f.text.MergedAt = f.nextTicket()
		f.text.remove(f.nextTicket())

		assert.False(t, f.tree.HoldsTombstone(f.text, time.NewVersionVector(), vectorCovering(f.nextTicket())),
			"an ID no element answers to holds nothing")
	})

	t.Run("a collection with no vector of its own takes no hold", func(t *testing.T) {
		f, _, _ := merged(t, nil)

		assert.False(t, f.tree.HoldsTombstone(f.text, time.NewVersionVector(), nil))
		root := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
		root.RegisterGCPair(GCPair{Parent: f.tree, Child: f.text})
		n, err := root.GarbageCollect(vectorCovering(f.text.removedAt))
		require.NoError(t, err)
		assert.Equal(t, 1, n, "Root.GarbageCollect purges as it did before the hold existed")
	})

	// The forged shape: an element payload whose child names a removed source
	// and carries a merge ticket for an actor no change ever came from. Decoded,
	// it derives the same forwarding pointer an honest merge would, so only
	// the ticket tells it apart: no collection vector ever covers it, and the
	// replica's own vector has not seen it either.
	t.Run("a forged merge ticket", func(t *testing.T) {
		phantom, err := time.ActorIDFromHex("0000000000000000000000ee")
		require.NoError(t, err)
		forged := time.NewTicket(1<<62, 0, phantom)
		f, _, removedAt := merged(t, forged)
		seen := vectorCovering(removedAt)

		assert.False(t, f.tree.HoldsTombstone(f.text, vectorCovering(removedAt), seen),
			"a merge this replica never applied is not one a later vector releases")
		root := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
		root.RegisterGCPair(GCPair{Parent: f.tree, Child: f.text})
		n, err := root.GarbageCollectSeen(vectorCovering(removedAt), seen)
		require.NoError(t, err)
		assert.Equal(t, 1, n, "the child is purged once its own removal is covered")
		assert.Equal(t, 0, root.GarbageLen())
	})

	// The same ticket claimed in the crafting change's version vector, which
	// every replica merges into its own: the hold is taken, and lasts exactly
	// as long as an honest merge in that change would -- until the minimum
	// synced vector carries the claim.
	t.Run("a forged merge ticket its change claims", func(t *testing.T) {
		phantom, err := time.ActorIDFromHex("0000000000000000000000ee")
		require.NoError(t, err)
		forged := time.NewTicket(1<<62, 0, phantom)
		f, _, removedAt := merged(t, forged)
		seen := vectorCovering(removedAt)
		seen.Set(phantom, 1<<62)
		lagging := vectorCovering(removedAt)
		lagging.Set(phantom, 0)

		root := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
		root.RegisterGCPair(GCPair{Parent: f.tree, Child: f.text})
		n, err := root.GarbageCollectSeen(lagging, seen)
		require.NoError(t, err)
		assert.Equal(t, 0, n, "held while a replica has not synced the claim")
		n, err = root.GarbageCollectSeen(seen, seen)
		require.NoError(t, err)
		assert.Equal(t, 1, n, "released once every replica has")
	})
}

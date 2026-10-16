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

// TestTreeHoldsTombstoneForMergeSource pins the second GCHold rule: a removed
// node a merge moved is the §1.1 boundary for positions declared inside its
// merge source, so it outlives the source's own removal becoming common
// knowledge (#2099) -- and no longer.
func TestTreeHoldsTombstoneForMergeSource(t *testing.T) {
	t.Run("source removal not yet common knowledge", func(t *testing.T) {
		f := newMergeLineageFixture(t)
		removedAt := f.nextTicket()
		f.p3.remove(removedAt)
		f.text.MergedFrom = f.p3.id
		f.text.MergedAt = removedAt
		f.text.remove(f.nextTicket())

		seen := vectorCovering(f.nextTicket())
		assert.True(t, f.tree.HoldsTombstone(f.text, time.NewVersionVector(), seen),
			"a peer that has not seen the merge still resolves positions through it")
		assert.False(t, f.tree.HoldsTombstone(f.text, vectorCovering(removedAt), seen),
			"once every replica has seen the merge the boundary is free")
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

		assert.False(t, f.tree.HoldsTombstone(f.text, time.NewVersionVector(), nil),
			"a live element is not a merge source, so it pins nothing")
	})

	t.Run("source names nothing", func(t *testing.T) {
		f := newMergeLineageFixture(t)
		f.text.MergedFrom = f.forgedIDNamingNone(f.p3)
		f.text.MergedAt = f.nextTicket()
		f.text.remove(f.nextTicket())

		assert.False(t, f.tree.HoldsTombstone(f.text, time.NewVersionVector(), nil),
			"an ID no element answers to holds nothing")
	})

	// The source's removedAt rides in on a client-supplied element payload
	// too. One for an actor no change ever came from is never covered by a
	// collection vector, so holding on it would pin the child, and its bytes
	// in docSize.GC, for the life of the document. The replica's own vector
	// has not seen it either, and that is what keeps it from holding.
	t.Run("source removal no change ever made", func(t *testing.T) {
		f := newMergeLineageFixture(t)
		phantom, err := time.ActorIDFromHex("0000000000000000000000ee")
		require.NoError(t, err)
		f.p3.remove(time.NewTicket(1<<62, 0, phantom))
		f.text.MergedFrom = f.p3.id
		f.text.MergedAt = f.nextTicket()
		removedAt := f.nextTicket()
		f.text.remove(removedAt)

		assert.False(t, f.tree.HoldsTombstone(f.text, vectorCovering(removedAt), vectorCovering(removedAt)),
			"a removal this replica never applied is not one a later vector releases")

		// The same through Root's collection, which is what a document runs:
		// the child is purged once its own removal is covered.
		root := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
		root.RegisterGCPair(GCPair{Parent: f.tree, Child: f.text})
		require.Equal(t, 1, root.GarbageLen())
		n, err := root.GarbageCollectSeen(vectorCovering(removedAt), vectorCovering(removedAt))
		require.NoError(t, err)
		assert.Equal(t, 1, n, "the child is not held for the forged source")
		assert.Equal(t, 0, root.GarbageLen())
	})

	// The honest counterpart, through Root: a source removal the replica has
	// applied holds the child until the collection vector covers it too.
	t.Run("source removal the replica applied", func(t *testing.T) {
		f := newMergeLineageFixture(t)
		peer, err := time.ActorIDFromHex("0000000000000000000000dd")
		require.NoError(t, err)
		sourceRemovedAt := time.NewTicket(5, 0, peer)
		f.p3.remove(sourceRemovedAt)
		f.text.MergedFrom = f.p3.id
		f.text.MergedAt = sourceRemovedAt
		removedAt := f.nextTicket()
		f.text.remove(removedAt)

		// Every replica has seen the child's removal, not yet the merge.
		lagging := vectorCovering(removedAt)
		lagging.Set(peer, 4)
		seen := vectorCovering(removedAt)
		seen.Set(peer, 5)

		root := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
		root.RegisterGCPair(GCPair{Parent: f.tree, Child: f.text})
		n, err := root.GarbageCollectSeen(lagging, seen)
		require.NoError(t, err)
		assert.Equal(t, 0, n, "held while the source's removal is not covered")
		assert.Equal(t, 1, root.GarbageLen())

		n, err = root.GarbageCollectSeen(seen, seen)
		require.NoError(t, err)
		assert.Equal(t, 1, n, "released once it is")
		assert.Equal(t, 0, root.GarbageLen())
	})
}

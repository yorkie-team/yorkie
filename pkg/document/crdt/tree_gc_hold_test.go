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

// TestTreeHoldsTombstoneForChildren pins the first GCHold rule: a removed
// element stays linked while its subtree still holds a tombstone this
// collection cannot purge yet. Purging the element detaches the whole subtree
// in one go, so such a tombstone would be unlinked before the vector that
// decides its own place has arrived.
func TestTreeHoldsTombstoneForChildren(t *testing.T) {
	f := newMergeLineageFixture(t)
	vector := time.NewVersionVector()

	assert.False(t, f.tree.HoldsTombstone(f.p1, vector),
		"an element with no children is not held")

	f.p2.remove(f.nextTicket())
	assert.False(t, f.tree.HoldsTombstone(f.p2, vector),
		"a subtree of live children holds nothing back")

	textRemovedAt := f.nextTicket()
	f.text.remove(textRemovedAt)
	assert.True(t, f.tree.HoldsTombstone(f.p2, vector),
		"a tombstone inside it this vector cannot purge holds it")

	assert.False(t, f.tree.HoldsTombstone(f.text, vector),
		"a text node holds nothing, so it is never held for children")

	// The hold is on the vector, not on the link: once the collection covers
	// the child's own removal the child goes in this same pass, and the element
	// follows it whichever order the worklist offers the two in.
	assert.False(t, f.tree.HoldsTombstone(f.p2, vectorCovering(textRemovedAt)),
		"a vector that covers the subtree's removals releases the hold")

	require.NoError(t, f.tree.Purge(f.text))
	assert.False(t, f.tree.HoldsTombstone(f.p2, vector),
		"the hold is released once the subtree is gone")
}

// TestTreeHoldForChildrenAlwaysEnds pins what keeps the first GCHold rule from
// leaking: a LIVE child under a removed element -- a concurrent insert under
// an element some replica removed, or one a restore revived -- is on nobody's
// worklist, so a hold for one would never end and would pin the element, and
// its bytes in docSize.GC, for the life of the document. The survivor is
// unregistered with the subtree it is detached in instead, so nothing is left
// resolvable into a subtree the tree no longer holds.
func TestTreeHoldForChildrenAlwaysEnds(t *testing.T) {
	f := newMergeLineageFixture(t)
	removedAt := f.nextTicket()
	f.p2.remove(removedAt)

	// The child stays live: with no removal of its own, no GC pair ever offers
	// it for purging.
	require.Nil(t, f.text.removedAt)
	assert.False(t, f.tree.HoldsTombstone(f.p2, vectorCovering(removedAt)),
		"a live child is on no worklist, so it must not hold the element")

	require.NoError(t, f.tree.Purge(f.p2))
	assert.Nil(t, f.tree.findFloorNode(f.text.id),
		"the live child is unregistered with the subtree it was detached in")
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

		assert.True(t, f.tree.HoldsTombstone(f.text, time.NewVersionVector()),
			"a peer that has not seen the merge still resolves positions through it")
		assert.False(t, f.tree.HoldsTombstone(f.text, vectorCovering(removedAt)),
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

		assert.False(t, f.tree.HoldsTombstone(f.text, time.NewVersionVector()),
			"a live element is not a merge source, so it pins nothing")
	})

	t.Run("source names nothing", func(t *testing.T) {
		f := newMergeLineageFixture(t)
		f.text.MergedFrom = f.forgedIDNamingNone(f.p3)
		f.text.MergedAt = f.nextTicket()
		f.text.remove(f.nextTicket())

		assert.False(t, f.tree.HoldsTombstone(f.text, time.NewVersionVector()),
			"an ID no element answers to holds nothing")
	})
}

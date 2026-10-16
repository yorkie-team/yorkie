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
// element stays linked while it still has children. Purging it detaches the
// whole subtree in one go, so a live child inside it would be lost while
// staying registered in NodeMapByID, where position resolution then walks off
// the top of a subtree no longer attached to the tree.
func TestTreeHoldsTombstoneForChildren(t *testing.T) {
	f := newMergeLineageFixture(t)
	vector := time.NewVersionVector()

	assert.False(t, f.tree.HoldsTombstone(f.p1, vector),
		"an element with no children is not held")

	f.p2.remove(f.nextTicket())
	assert.True(t, f.tree.HoldsTombstone(f.p2, vector),
		"a removed element is held while its subtree is still linked")

	assert.False(t, f.tree.HoldsTombstone(f.text, vector),
		"a text node holds nothing, so it is never held for children")

	// Its own children are on the worklist, so they go first; the element
	// follows them within the same collection pass.
	require.NoError(t, f.tree.Purge(f.text))
	assert.False(t, f.tree.HoldsTombstone(f.p2, vector),
		"the hold is released once the subtree is gone")
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

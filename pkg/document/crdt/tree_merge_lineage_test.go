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
	gotime "time"

	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// mergeLineageFixture builds <r><p1></p1><p2>cd</p2><p3></p3></r>, the shape
// every case below forges a merge lineage over: p1 is the merge destination,
// p2 holds the child that carries MergedFrom, and p3 is the unrelated live
// element a forged pointer tries to name.
type mergeLineageFixture struct {
	tree               *Tree
	p1, p2, p3, text   *TreeNode
	nextTicket         func() *time.Ticket
	forgedIDNamingNone func(*TreeNode) *TreeNodeID
}

func newMergeLineageFixture(t *testing.T) *mergeLineageFixture {
	t.Helper()

	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)

	lamport := int64(0)
	nextTicket := func() *time.Ticket {
		lamport++
		return time.NewTicket(lamport, 0, actor)
	}

	root := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "r", nil)
	p1 := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "p", nil)
	p2 := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "p", nil)
	p3 := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "p", nil)
	text := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "text", nil, "cd")
	require.NoError(t, root.Append(p1, p2, p3))
	require.NoError(t, p2.Append(text))

	return &mergeLineageFixture{
		tree:       NewTree(root, nextTicket()),
		p1:         p1,
		p2:         p2,
		p3:         p3,
		text:       text,
		nextTicket: nextTicket,
		// Same CreatedAt, an offset the node never had: the floor lookup
		// answers with the node, an exact-ID lookup does not.
		forgedIDNamingNone: func(node *TreeNode) *TreeNodeID {
			return NewTreeNodeID(node.id.CreatedAt, node.id.Offset+9)
		},
	}
}

// TestFindMergeNodeRequiresExactElement pins what a merge pointer has to name.
//
// MergedFrom survives on an element payload (Set/Add/ArraySet keep the lineage
// a reverse-of-Remove legitimately carries), so both ends of a merge relation
// can be client-supplied. A floor match is not enough: it would let a made-up
// offset name a node the merge never touched.
func TestFindMergeNodeRequiresExactElement(t *testing.T) {
	f := newMergeLineageFixture(t)

	assert.Same(t, f.p3, f.tree.findMergeNode(f.p3.id),
		"the exact ID of an element is the one shape a merge records")
	assert.Nil(t, f.tree.findMergeNode(f.forgedIDNamingNone(f.p3)),
		"an offset the node never had must not resolve to it")
	assert.Nil(t, f.tree.findMergeNode(f.text.id),
		"a text node holds no children, so it is neither end of a merge")
	assert.Nil(t, f.tree.findMergeNode(nil))
}

// TestRebuildMergeStateRejectsForgedSource pins the decode boundary: a
// MergedFrom that only floors onto a live element plants no forwarding pointer
// on it, while the genuine one still rebuilds.
func TestRebuildMergeStateRejectsForgedSource(t *testing.T) {
	t.Run("forged source", func(t *testing.T) {
		f := newMergeLineageFixture(t)
		f.text.MergedFrom = f.forgedIDNamingNone(f.p3)
		f.text.MergedAt = f.nextTicket()

		// Re-read exactly as the converter re-reads an element payload.
		NewTree(f.tree.Root(), f.nextTicket())

		assert.Nil(t, f.p3.mergedInto,
			"a later delete would follow this pointer and tombstone p3's children")
	})

	t.Run("genuine source", func(t *testing.T) {
		f := newMergeLineageFixture(t)
		// A merge source is a tombstone: Phase 5 removes it before Phase 6
		// moves its children.
		f.p3.remove(f.nextTicket())
		f.text.MergedFrom = f.p3.id
		f.text.MergedAt = f.nextTicket()

		NewTree(f.tree.Root(), f.nextTicket())

		require.NotNil(t, f.p3.mergedInto)
		assert.True(t, f.p3.mergedInto.Equal(f.p2.id),
			"the lineage an element payload legitimately carries still rebuilds")
	})

	// The source an undo revived is live, and the stamps on the children the
	// merge moved outlive the revive. Rebuilding the pointer onto a live
	// element is what makes a later delete of it cascade §6.2 over those
	// children; declining is what makes a snapshot reload hold exactly what
	// the in-memory replica holds after the revive (#2189).
	t.Run("live source", func(t *testing.T) {
		f := newMergeLineageFixture(t)
		mergedAt := f.nextTicket()
		f.text.MergedFrom = f.p3.id
		f.text.MergedAt = mergedAt

		NewTree(f.tree.Root(), f.nextTicket())

		assert.Nil(t, f.p3.mergedInto,
			"a live element is not a merge source")
		// The stamps are persisted CRDT state that §7.1's split partitioning
		// and §9.4's interloper guard read directly: a decode that erased them
		// would partition a later concurrent split differently from a replica
		// that never reloaded.
		require.NotNil(t, f.text.MergedFrom,
			"declining to derive the pointer must not destroy the merge record")
		assert.True(t, f.text.MergedFrom.Equal(f.p3.id))
		assert.Same(t, mergedAt, f.text.MergedAt)
	})
}

// TestRestoreDissolvesMergeSourcePointer pins the revive half of #2189: the
// element a restore revives stops being a merge source, and nothing but the
// runtime pointer is dropped to make that so.
func TestRestoreDissolvesMergeSourcePointer(t *testing.T) {
	f := newMergeLineageFixture(t)
	// p3 as a genuine merge source: Phase 5 tombstoned it, Phase 6 moved the
	// child now sitting under p2 and stamped it.
	f.p3.remove(f.nextTicket())
	mergedAt := f.nextTicket()
	f.text.MergedFrom = f.p3.id
	f.text.MergedAt = mergedAt
	tree := NewTree(f.tree.Root(), f.nextTicket())
	require.NotNil(t, f.p3.mergedInto, "the source carries the lineage as a tombstone")

	untombstoned, recreated, _, _, err := tree.Restore([]*TreeRestoreSpan{{
		ID:       f.p3.id,
		NodeType: "p",
		Length:   1,
	}})
	require.NoError(t, err)
	require.Empty(t, recreated, "the tombstone is revived in place, not recreated")
	require.Len(t, untombstoned, 1)
	require.Same(t, f.p3, untombstoned[0])

	assert.False(t, f.p3.IsRemoved())
	assert.Nil(t, f.p3.mergedInto,
		"a live element is nobody's merge source, so a later delete cannot cascade §6.2")
	require.NotNil(t, f.text.MergedFrom,
		"the merge that moved the child still happened; only the pointer is dropped")
	assert.True(t, f.text.MergedFrom.Equal(f.p3.id))
	assert.Same(t, mergedAt, f.text.MergedAt)

	// A reload derives the same state the revive left behind, without the
	// restore being replayed: declining on a live source is the snapshot-side
	// half of the dissolution.
	NewTree(tree.Root(), f.nextTicket())
	assert.Nil(t, f.p3.mergedInto,
		"a snapshot reload must not resurrect the revived element as a source")
}

// TestMergeNodesRejectsLiveSource pins the same contract at the operation
// boundary that rebuildMergeState holds at the decode boundary: an exactly
// named but LIVE source gets no forwarding pointer.
//
// MergedFrom rides on an element payload (Set/Add/ArraySet), so a client can
// name a live element it was never merged into; an honest merge cannot reach
// here with one, because Phase 5 tombstones every merge boundary before Phase
// 6 moves its children. Planting the pointer is the #2189 state: the next
// delete of that live element tombstones the named children.
func TestMergeNodesRejectsLiveSource(t *testing.T) {
	f := newMergeLineageFixture(t)
	f.text.MergedFrom = f.p3.id // exact ID, and p3 is live
	f.text.MergedAt = f.nextTicket()

	stranded, err := f.tree.mergeNodes(f.p1, []*TreeNode{f.text}, f.nextTicket())
	require.NoError(t, err)
	assert.Empty(t, stranded, "an ordinary merge tombstones nothing")

	assert.Nil(t, f.p3.mergedInto,
		"a live element named by MergedFrom must not become a merge source")
	assert.Same(t, f.p1, f.text.Index.Parent.Value,
		"the merge itself still moves the child")
}

// TestMergeNodesRejectsForgedSource pins that the check holds for the whole
// lifetime of the field, not only at the decode that first saw it.
//
// MergedFrom is retained on the node inside the live document, and mergeNodes
// re-derives the forwarding pointer from it whenever an ordinary, innocent
// merge moves that node again. Resolved by floor there, the forged offset
// would plant on p3 the pointer the decode refused to.
func TestMergeNodesRejectsForgedSource(t *testing.T) {
	f := newMergeLineageFixture(t)
	f.text.MergedFrom = f.forgedIDNamingNone(f.p3)
	f.text.MergedAt = f.nextTicket()

	// An ordinary merge of p2 into p1 moves the child that carries the forged
	// lineage; the child keeps it, because MergedFrom is stamped only on a
	// first move.
	stranded, err := f.tree.mergeNodes(f.p1, []*TreeNode{f.text}, f.nextTicket())
	require.NoError(t, err)
	assert.Empty(t, stranded, "an ordinary merge tombstones nothing")

	assert.Nil(t, f.p3.mergedInto,
		"an ordinary merge must not plant a forwarding pointer on an unrelated node")
	assert.Same(t, f.p1, f.text.Index.Parent.Value,
		"the merge itself still moves the child")
}

// TestMergeNodesStrandsLaterChildrenOfATombstonedDest pins that the cycle
// guard does not leave the rest of the merge list moving into a destination it
// has just tombstoned.
//
// The destination is resolved once, before the loop, but collectBetween fills
// the move list from every merge source in the range, so entries remain after
// the guard fires on one of them. Tombstoning a subtree that contains dest
// tombstones dest as well, and a later entry moved under it would be live
// under a tombstone -- the detached-but-registered shape of #2008 that the
// guard exists to prevent.
func TestMergeNodesStrandsLaterChildrenOfATombstonedDest(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	lamport := int64(0)
	nextTicket := func() *time.Ticket {
		lamport++
		return time.NewTicket(lamport, 0, actor)
	}

	// <r><a/><b><c><d>e</d></c></b><q>y</q></r>, with e carrying MergedFrom = a
	// as an element payload may, so a forwards to d.
	root := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "r", nil)
	a := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "a", nil)
	b := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "b", nil)
	c := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "c", nil)
	d := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "d", nil)
	e := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "text", nil, "e")
	q := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "q", nil)
	y := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "text", nil, "y")
	require.NoError(t, root.Append(a, b, q))
	require.NoError(t, b.Append(c))
	require.NoError(t, c.Append(d))
	require.NoError(t, d.Append(e))
	require.NoError(t, q.Append(y))
	a.remove(nextTicket())
	e.MergedFrom = a.id
	e.MergedAt = nextTicket()

	// Re-read exactly as the converter re-reads an element payload.
	tree := NewTree(root, nextTicket())
	require.NotNil(t, a.mergedInto)
	require.True(t, a.mergedInto.Equal(d.id))

	// c is stranded by the guard (it contains the destination d); y, collected
	// from the second source q, comes after it in the same list.
	editedAt := nextTicket()
	stranded, err := tree.mergeNodes(a, []*TreeNode{c, y}, editedAt)
	require.NoError(t, err)

	assert.Same(t, q.Index, y.Index.Parent,
		"a child must not move under a destination the guard just tombstoned")
	assert.Nil(t, y.MergedFrom, "a stranded child is not stamped as moved")
	assert.Nil(t, q.mergedInto,
		"no child of q moved, so neither runtime nor rebuild points it")

	strandedSet := map[*TreeNode]bool{}
	for _, node := range stranded {
		strandedSet[node] = true
	}
	for _, node := range []*TreeNode{c, d, e, y} {
		assert.True(t, node.IsRemoved(),
			"a child the merge refused to move must not stay live under a tombstone")
		assert.True(t, strandedSet[node],
			"every node tombstoned here must be returned for its GC pair")
	}
}

// TestMergeNodesRefusesCycleFromForgedLineage pins that a forged merge lineage
// cannot steer a merge into one of the nodes it moves.
//
// The tree is <r><a>x</a><b><c><d>e</d></c></b></r> with a tombstoned and one
// node carrying MergedFrom = a, as an element payload may; rebuildMergeState
// then points a at that node's parent. A merge of b into a resolves its
// destination through that pointer, so it lands on c, the child it moves, or
// on d inside it. Moving c there would close a parent cycle in the index tree,
// and every later ancestor walk would spin forever under the document lock.
func TestMergeNodesRefusesCycleFromForgedLineage(t *testing.T) {
	tests := []struct {
		name string
		// carrier picks the node that carries the forged MergedFrom; its
		// parent becomes the merge destination.
		carrier func(c, d, e *TreeNode) (carrier, dest *TreeNode)
	}{
		{
			name:    "destination is the moved node",
			carrier: func(c, d, _ *TreeNode) (*TreeNode, *TreeNode) { return d, c },
		},
		{
			name:    "destination inside the moved node",
			carrier: func(_, d, e *TreeNode) (*TreeNode, *TreeNode) { return e, d },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actor, err := time.ActorIDFromHex("000000000000000000000001")
			require.NoError(t, err)
			lamport := int64(0)
			nextTicket := func() *time.Ticket {
				lamport++
				return time.NewTicket(lamport, 0, actor)
			}

			root := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "r", nil)
			a := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "a", nil)
			x := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "text", nil, "x")
			b := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "b", nil)
			c := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "c", nil)
			d := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "d", nil)
			e := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "text", nil, "e")
			require.NoError(t, root.Append(a, b))
			require.NoError(t, a.Append(x))
			require.NoError(t, b.Append(c))
			require.NoError(t, c.Append(d))
			require.NoError(t, d.Append(e))
			a.remove(nextTicket())
			carrier, dest := tc.carrier(c, d, e)
			carrier.MergedFrom = a.id
			carrier.MergedAt = nextTicket()

			// Re-read exactly as the converter re-reads an element payload.
			tree := NewTree(root, nextTicket())
			require.NotNil(t, a.mergedInto)
			require.True(t, a.mergedInto.Equal(dest.id),
				"the forged lineage points a at a node inside b")

			// Measured before the edit: b is the range this edit consumes,
			// and all of it is live, so RemovedSize has to come back as b's
			// pre-edit length. strandedSize is the part of it the guard
			// refuses to move and tombstones in place instead; bPadding is
			// the only part of b left in toBeRemoveds' own accounting.
			bSize := b.Index.PaddedLength()
			strandedSize := c.Index.PaddedLength()
			bPadding := bSize - strandedSize

			type editResult struct {
				pairs []GCPair
				info  TreeEditReverseInfo
				err   error
			}
			done := make(chan editResult, 1)
			go func() {
				pairs, _, info, err := tree.Edit(
					NewTreePos(a.id, x.id), NewTreePos(b.id, b.id),
					nil, 0, nextTicket(), nextTicket, nil, false,
				)
				done <- editResult{pairs: pairs, info: info, err: err}
			}()

			var pairs []GCPair
			var info TreeEditReverseInfo
			select {
			case res := <-done:
				require.NoError(t, res.err)
				pairs, info = res.pairs, res.info
			case <-gotime.After(3 * gotime.Second):
				t.Fatal("the merge closed a parent cycle and the edit never returned")
			}

			assert.Same(t, b.Index, c.Index.Parent,
				"c stays where it was instead of moving under itself")
			assert.Same(t, c.Index, d.Index.Parent)
			assert.Nil(t, c.MergedFrom, "a skipped child is not stamped as moved")
			assert.Nil(t, b.mergedInto,
				"no moved child names b, so neither runtime nor rebuild points it")
			assert.Equal(t, "<r></r>", tree.ToXML())

			// The skipped child stays under b, which this edit tombstoned, so
			// leaving it live would hand b's Purge a subtree it detaches
			// without unregistering or un-charging it (#2008). It is
			// tombstoned in place instead, with its descendants, and every one
			// of them is booked as a GC pair so the purge reaches it.
			booked := map[*TreeNode]bool{}
			for _, pair := range pairs {
				booked[pair.Child.(*TreeNode)] = true
			}
			for _, node := range []*TreeNode{c, d, e} {
				assert.True(t, node.IsRemoved(),
					"a child the merge refused to move must not stay live under a tombstone")
				assert.True(t, booked[node],
					"every node tombstoned here needs a GC pair to be purged and un-charged")
			}

			// The stranded subtree is not in toBeRemoveds -- the merge had
			// collected it to move -- yet tombstoning it in place drained its
			// bytes out of b, the removed source it stays under. Summed over
			// toBeRemoveds alone, RemovedSize would lose them entirely and
			// report only b's own padding, handing undo/redo reconciliation a
			// range short by the whole subtree.
			assert.Equal(t, bSize, info.RemovedSize,
				"the range the edit consumed is all of b, stranded subtree included")
			assert.Equal(t, bSize, bPadding+strandedSize,
				"and that range is b's padding plus the subtree it stranded")
		})
	}
}

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

// knowing returns the version vector of a change that has seen every ticket
// up to and including the given one, as a restore's change carries.
func knowing(ticket *time.Ticket) time.VersionVector {
	vector := time.NewVersionVector()
	vector.Set(ticket.ActorID(), ticket.Lamport())
	return vector
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

	// A snapshot written before #2189 can name a LIVE element: the source was
	// revived by an undo and nothing dissolved the lineage it stopped having.
	// Rebuilding the pointer there is what makes a later delete of that element
	// cascade §6.2 over children the merge moved out of it before the revive.
	t.Run("live source", func(t *testing.T) {
		f := newMergeLineageFixture(t)
		f.text.MergedFrom = f.p3.id
		mergedAt := f.nextTicket()
		f.text.MergedAt = mergedAt

		NewTree(f.tree.Root(), f.nextTicket())

		assert.Nil(t, f.p3.mergedInto,
			"a live element is not a merge source")
		assert.True(t, f.text.MergedFrom.Equal(f.p3.id),
			"the decode reads the stamps without rewriting them")
		assert.Same(t, mergedAt, f.text.MergedAt)
	})
}

// TestRestoreDissolvesMergeLineage pins the revive half of #2189: the element
// a restore revives stops being a merge source, in both halves of the lineage.
//
// The second half is what makes the dissolution outlive the process. Dropping
// only the pointer leaves the element re-derivable as a source: liveness is a
// property of the moment, so once a later edit tombstones the revived element
// the surviving stamps name a removed source again and the next decode rebuilds
// the pointer this restore dropped.
func TestRestoreDissolvesMergeLineage(t *testing.T) {
	f := newMergeLineageFixture(t)
	// p3 as a genuine merge source: Phase 5 tombstoned it, Phase 6 moved the
	// child now sitting under p2 and stamped it.
	f.p3.remove(f.nextTicket())
	f.text.MergedFrom = f.p3.id
	f.text.MergedAt = f.nextTicket()
	tree := NewTree(f.tree.Root(), f.nextTicket())
	require.NotNil(t, f.p3.mergedInto, "the source carries the lineage as a tombstone")

	untombstoned, recreated, _, _, _, err := tree.Restore([]*TreeRestoreSpan{{
		ID:       f.p3.id,
		NodeType: "p",
		Length:   1,
	}}, false, knowing(f.nextTicket()))
	require.NoError(t, err)
	require.Empty(t, recreated, "the tombstone is revived in place, not recreated")
	require.Len(t, untombstoned, 1)
	require.Same(t, f.p3, untombstoned[0])

	assert.False(t, f.p3.IsRemoved())
	assert.Nil(t, f.p3.mergedInto,
		"a live element is nobody's merge source, so a later delete cannot cascade §6.2")
	assert.Nil(t, f.text.MergedFrom,
		"the persisted half goes too, or a reload derives the pointer again")
	assert.Nil(t, f.text.MergedAt)

	// The revived element is deleted again by an ordinary edit, and the tree is
	// reloaded from the snapshot that delete would be written into. With the
	// stamps gone there is nothing left to derive from, so the reloaded replica
	// holds what the in-memory one holds: no forwarding pointer, and the moved
	// child untouched by §6.2.
	f.p3.remove(f.nextTicket())
	NewTree(tree.Root(), f.nextTicket())
	assert.Nil(t, f.p3.mergedInto,
		"re-deleting the revived element must not resurrect it as a merge source")
	assert.Same(t, f.p2, f.text.Index.Parent.Value,
		"the merge-moved child stays where the merge put it")
}

// TestRestoreDissolvesLineageAwayFromTheTarget pins that the dissolution is
// keyed on the source, not on the node its pointer happens to name.
//
// The stamps a merge wrote do not all stay under the target: §7.1's
// SplitElement partitions a merged parent and carries merge-stamped children
// into the other half of the pair, and a purged source comes back through
// recreateFromSpan with no pointer at all. A stamp left behind in either case
// is re-derivable -- it names a live source now, so rebuildMergeState declines
// it, but the next edit that tombstones the revived element makes it name a
// removed one again and the pointer #2189 cascades from comes back.
func TestRestoreDissolvesLineageAwayFromTheTarget(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	lamport := int64(0)
	nextTicket := func() *time.Ticket {
		lamport++
		return time.NewTicket(lamport, 0, actor)
	}

	// <r><p1>ab</p1><p2>cd</p2><src></src></r>: src is the merge source, and
	// both texts carry its stamp -- "cd" under the parent the merge moved them
	// into, "ab" under the half a later split carried them away to.
	root := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "r", nil)
	p1 := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "p", nil)
	p2 := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "p", nil)
	src := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "p", nil)
	relocated := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "text", nil, "ab")
	moved := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "text", nil, "cd")
	require.NoError(t, root.Append(p1, p2, src))
	require.NoError(t, p1.Append(relocated))
	require.NoError(t, p2.Append(moved))

	src.remove(nextTicket())
	mergedAt := nextTicket()
	for _, node := range []*TreeNode{relocated, moved} {
		node.MergedFrom = src.id
		node.MergedAt = mergedAt
	}

	tree := NewTree(root, nextTicket())
	require.NotNil(t, src.mergedInto, "the source carries the pointer as a tombstone")

	// A purged element the same restore recreates: nothing in the tree holds
	// its ID, so no pointer was ever derived, yet a stamp still names it.
	purgedID := NewTreeNodeID(nextTicket(), 0)
	stranded := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "text", nil, "ef")
	require.NoError(t, p2.Append(stranded))
	stranded.MergedFrom = purgedID
	stranded.MergedAt = mergedAt
	tree.putNode(stranded)

	untombstoned, recreated, _, _, _, err := tree.Restore([]*TreeRestoreSpan{
		{ID: src.id, NodeType: "p", Length: 1},
		{ID: purgedID, NodeType: "p", Length: 1, ParentID: root.id, LeftSiblingID: p2.id},
	}, false, knowing(nextTicket()))
	require.NoError(t, err)
	require.Len(t, untombstoned, 1)
	require.Len(t, recreated, 1, "the purged element comes back under its own ID")

	assert.Nil(t, src.mergedInto)
	assert.Nil(t, moved.MergedFrom, "the half under the target goes")
	assert.Nil(t, relocated.MergedFrom, "and so does the half a split carried off")
	assert.Nil(t, relocated.MergedAt)
	assert.Nil(t, stranded.MergedFrom,
		"a recreated element carries no pointer, but stamps still name it")
	assert.Nil(t, stranded.MergedAt)

	// Re-delete both revived elements and reload: with every stamp gone there
	// is nothing left for the decode to derive a forwarding pointer from.
	src.remove(nextTicket())
	recreated[0].remove(nextTicket())
	NewTree(tree.Root(), nextTicket())
	assert.Nil(t, src.mergedInto,
		"re-deleting the revived element must not resurrect it as a merge source")
	assert.Nil(t, recreated[0].mergedInto)
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

// TestRestoreDissolvesLineageWhateverTheSpanResolvesTo pins that the
// dissolution follows the SPAN, not the outcome the local tree happens to
// produce for it.
//
// An element span resolves to one of four things -- the element is live
// already, it is a tombstone the span revives, it is purged and recreated, or
// the recreate declines because the parent is itself removed -- and which one
// a replica takes is a property of that replica's state, never of the
// operation. Dissolving only on the revive and the successful recreate left
// the other two keeping the persisted MergedFrom/MergedAt that their peers
// wiped, and that is the lineage §6.2 cascades on: the replica that kept the
// stamps re-derives the pointer on the next load and tombstones content the
// one that dropped them keeps (#2189).
func TestRestoreDissolvesLineageWhateverTheSpanResolvesTo(t *testing.T) {
	// build returns <r><p>cd</p><dead></dead></r> with "cd" stamped as moved
	// out of srcID, plus the tickets to keep editing it.
	build := func(t *testing.T) (
		tree *Tree, root, dead, moved *TreeNode, nextTicket func() *time.Ticket,
	) {
		t.Helper()

		actor, err := time.ActorIDFromHex("000000000000000000000001")
		require.NoError(t, err)
		lamport := int64(0)
		nextTicket = func() *time.Ticket {
			lamport++
			return time.NewTicket(lamport, 0, actor)
		}

		root = NewTreeNode(NewTreeNodeID(nextTicket(), 0), "r", nil)
		p := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "p", nil)
		dead = NewTreeNode(NewTreeNodeID(nextTicket(), 0), "p", nil)
		moved = NewTreeNode(NewTreeNodeID(nextTicket(), 0), "text", nil, "cd")
		require.NoError(t, root.Append(p, dead))
		require.NoError(t, p.Append(moved))
		dead.remove(nextTicket())

		return NewTree(root, nextTicket()), root, dead, moved, nextTicket
	}

	t.Run("the element is live already", func(t *testing.T) {
		tree, root, _, moved, nextTicket := build(t)
		// A live element named by the stamps: the peer that revived it from a
		// tombstone dissolves them, so this replica has to as well.
		live := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "p", nil)
		require.NoError(t, root.Append(live))
		tree.putNode(live)
		moved.MergedFrom = live.id
		moved.MergedAt = nextTicket()
		// Re-read as a decode does, which indexes the stamp.
		tree = NewTree(tree.Root(), nextTicket())

		untombstoned, recreated, _, _, _, err := tree.Restore([]*TreeRestoreSpan{
			{ID: live.id, NodeType: "p", Length: 1},
		}, false, knowing(nextTicket()))
		require.NoError(t, err)
		require.Empty(t, untombstoned, "a live element is neither revived")
		require.Empty(t, recreated, "nor recreated")

		assert.Nil(t, moved.MergedFrom,
			"an undo that finds the element live must still drop the stamps its peer drops")
		assert.Nil(t, moved.MergedAt)

		// The element is deleted again and the tree reloaded from the snapshot
		// that delete writes: with no stamps left there is nothing to derive a
		// forwarding pointer from, so §6.2 has nothing to cascade over.
		live.remove(nextTicket())
		NewTree(tree.Root(), nextTicket())
		assert.Nil(t, live.mergedInto,
			"a kept stamp would hand the next load the pointer #2189 cascades from")
		assert.Equal(t, "cd", moved.Value)
	})

	t.Run("the recreate declines under a removed parent", func(t *testing.T) {
		tree, _, dead, moved, nextTicket := build(t)
		// A purged element whose parent has since been removed: the recreate
		// brings it back born tombstoned and reports nothing, while a peer
		// whose parent was still live recreated it and dissolved the stamps.
		purgedID := NewTreeNodeID(nextTicket(), 0)
		moved.MergedFrom = purgedID
		moved.MergedAt = nextTicket()
		tree = NewTree(tree.Root(), nextTicket())

		untombstoned, recreated, _, _, _, err := tree.Restore([]*TreeRestoreSpan{
			{ID: purgedID, NodeType: "p", Length: 1, ParentID: dead.id},
		}, false, knowing(nextTicket()))
		require.NoError(t, err)
		require.Empty(t, untombstoned)
		require.Empty(t, recreated, "a node born tombstoned is not reported as recreated")

		assert.Nil(t, moved.MergedFrom,
			"the stamps go even when this replica's recreate declines")
		assert.Nil(t, moved.MergedAt)

		reborn := tree.findFloorNode(purgedID)
		require.NotNil(t, reborn, "the element is still recreated, under the dead parent")
		require.True(t, reborn.IsRemoved())
		NewTree(tree.Root(), nextTicket())
		assert.Nil(t, reborn.mergedInto,
			"and the reload must not derive a pointer onto the element it brought back")
	})
}

// TestRestoreWithoutClocksDissolvesNothing pins that the dissolution fails
// closed. A change's version vector is whatever its client put on the wire,
// and an empty one would otherwise read as having seen every merge, clearing
// the stamps of merges concurrent with the restore on one replica only.
func TestRestoreWithoutClocksDissolvesNothing(t *testing.T) {
	f := newMergeLineageFixture(t)
	f.p3.remove(f.nextTicket())
	f.text.MergedFrom = f.p3.id
	mergedAt := f.nextTicket()
	f.text.MergedAt = mergedAt
	tree := NewTree(f.tree.Root(), f.nextTicket())

	untombstoned, _, _, _, _, err := tree.Restore([]*TreeRestoreSpan{{
		ID:       f.p3.id,
		NodeType: "p",
		Length:   1,
	}}, false, time.NewVersionVector())
	require.NoError(t, err)
	require.Len(t, untombstoned, 1, "the element is revived all the same")
	assert.True(t, f.text.MergedFrom.Equal(f.p3.id), "the stamps stay")
	assert.Same(t, mergedAt, f.text.MergedAt)
	assert.NotNil(t, f.p3.mergedInto, "and so does the pointer, in step with them")
}

// TestPurgeForgetsTheMergeStampsOfTheDetachedSubtree pins that the
// mergeStamps index lets go of every stamped node a purge detaches, not only
// the purged node itself, so it keeps no purged subtree alive.
func TestPurgeForgetsTheMergeStampsOfTheDetachedSubtree(t *testing.T) {
	f := newMergeLineageFixture(t)
	f.p3.remove(f.nextTicket())
	f.text.MergedFrom = f.p3.id
	f.text.MergedAt = f.nextTicket()
	tree := NewTree(f.tree.Root(), f.nextTicket())
	require.Len(t, tree.mergeStamps[f.p3.id.toIDString()], 1)

	// The stamped text goes with its paragraph, which is the node purged.
	f.p2.remove(f.nextTicket())
	f.text.remove(f.nextTicket())
	require.NoError(t, tree.Purge(f.p2))
	assert.Empty(t, tree.mergeStamps, "no entry outlives the subtree it named")
}

// TestRestoreDissolvesTheStampOfANodeRefusedItsID pins that a stamped node
// putNode refuses the ID lookup -- a tombstone that shares its ID with a live
// node -- is still indexed for the dissolution. It stays in the index tree,
// where rebuildMergeState reads its stamp, so leaving it out would keep on
// one replica lineage another dissolves.
func TestRestoreDissolvesTheStampOfANodeRefusedItsID(t *testing.T) {
	f := newMergeLineageFixture(t)
	f.p3.remove(f.nextTicket())
	f.text.MergedFrom = f.p3.id
	f.text.MergedAt = f.nextTicket()

	tree := NewTree(f.tree.Root(), f.nextTicket())

	// A tombstone joins under the same ID as the live text, stamped the same
	// way, and putNode refuses it the lookup.
	twin := NewTreeNode(f.text.id, "text", nil, "cd")
	twin.MergedFrom = f.p3.id
	twin.MergedAt = f.text.MergedAt
	twin.removedAt = f.nextTicket()
	require.NoError(t, f.p2.Append(twin))
	tree.putNode(twin)
	require.Same(t, f.text, tree.findFloorNode(f.text.id), "the live node keeps the ID")

	_, _, _, _, _, err := tree.Restore([]*TreeRestoreSpan{{
		ID: f.p3.id, NodeType: "p", Length: 1,
	}}, false, knowing(f.nextTicket()))
	require.NoError(t, err)
	assert.Nil(t, f.text.MergedFrom)
	assert.Nil(t, twin.MergedFrom, "the refused node's stamp goes too")
}

// TestRestoreKeepsThePointerWithAnUnknownMergesStamp pins that the pointer
// half of the dissolution follows the stamps. A merge the restoring change
// did not know still stamps a child of the source; its stamp stays, and so
// does the source's pointer, as a reload would derive it again from that
// stamp once the source is removed.
func TestRestoreKeepsThePointerWithAnUnknownMergesStamp(t *testing.T) {
	f := newMergeLineageFixture(t)
	peer, err := time.ActorIDFromHex("0000000000000000000000dd")
	require.NoError(t, err)
	f.p3.remove(f.nextTicket())
	f.text.MergedFrom = f.p3.id
	f.text.MergedAt = time.NewTicket(1<<20, 0, peer)
	tree := NewTree(f.tree.Root(), f.nextTicket())
	require.NotNil(t, f.p3.mergedInto)

	_, _, _, _, _, err = tree.Restore([]*TreeRestoreSpan{{
		ID: f.p3.id, NodeType: "p", Length: 1,
	}}, false, knowing(f.nextTicket()))
	require.NoError(t, err)
	assert.True(t, f.text.MergedFrom.Equal(f.p3.id), "the unknown merge's stamp stays")
	assert.NotNil(t, f.p3.mergedInto, "and the pointer stays with it")
}

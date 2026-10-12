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
	"runtime"
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestDropSplitLinksClearsBoundaryIndex checks that stripping the boundary
// records from a built tree also forgets them in anchoredBefore. Otherwise a
// later purge of the anchor would relink the product and give it a record
// back that no peer decoding the same payload has.
func TestDropSplitLinksClearsBoundaryIndex(t *testing.T) {
	actor := time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	ticket := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }

	root := NewTreeNode(NewTreeNodeID(ticket(1), 0), "r", nil)
	first := NewTreeNode(NewTreeNodeID(ticket(2), 0), "p", nil)
	text := NewTreeNode(NewTreeNodeID(ticket(3), 0), "text", nil, "a")
	product := NewTreeNode(NewTreeNodeID(ticket(4), 0), "p", nil)
	require.NoError(t, first.Append(text))
	require.NoError(t, root.Append(first, product))
	product.BoundaryAnchor = text.ID()

	tree := NewTree(root, ticket(1))
	require.Len(t, tree.anchoredBefore, 1)

	DropSplitLinksInElement(tree)
	assert.False(t, hasBoundaryRecord(product))
	assert.Empty(t, tree.anchoredBefore)

	tree.relinkAnchors(text)
	assert.False(t, hasBoundaryRecord(product), "a purge must not bring the record back")
}

// TestRelinkPurgedSubtreeOrder purges a removed product and its removed text
// child in both orders. A boundary anchored before the child must end up at
// the next boundary either way: GC visits its pairs in map order, and a child
// reached after its detached parent would otherwise relink to the end.
func TestRelinkPurgedSubtreeOrder(t *testing.T) {
	actor := time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	ticket := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }

	for _, childFirst := range []bool{true, false} {
		root := NewTreeNode(NewTreeNodeID(ticket(1), 0), "r", nil)
		head := NewTreeNode(NewTreeNodeID(ticket(2), 0), "p", nil)
		product := NewTreeNode(NewTreeNodeID(ticket(3), 0), "p", nil)
		child := NewTreeNode(NewTreeNodeID(ticket(4), 0), "text", nil, "x")
		next := NewTreeNode(NewTreeNodeID(ticket(5), 0), "p", nil)
		anchored := NewTreeNode(NewTreeNodeID(ticket(6), 0), "p", nil)
		require.NoError(t, product.Append(child))
		require.NoError(t, root.Append(head, product, next, anchored))
		head.InsNextID, product.InsPrevID = product.id, head.id
		product.InsNextID, next.InsPrevID = next.id, product.id
		product.BoundaryAtEnd = true
		next.BoundaryAtEnd = true
		anchored.BoundaryAnchor = child.id
		tree := NewTree(root, ticket(1))

		child.SetRemovedAt(ticket(7))
		product.SetRemovedAt(ticket(7))
		order := []*TreeNode{product, child}
		if childFirst {
			order = []*TreeNode{child, product}
		}
		for _, n := range order {
			require.NoError(t, tree.Purge(n))
		}

		require.NotNil(t, anchored.BoundaryAnchor, "child first: %v", childFirst)
		assert.True(t, anchored.BoundaryAnchor.Equal(next.id), "child first: %v", childFirst)
	}
}

// TestPreMergePointStopsOnLoopingFamily points two nodes' InsPrevID at each
// other and walks the family from one of them. InsPrevID arrives verbatim from
// client-supplied bytes, so a chain that loops back on itself must stop the
// walk rather than spin the applying goroutine while it holds the document
// lock; see insNextWalker.
func TestPreMergePointStopsOnLoopingFamily(t *testing.T) {
	actor := time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	ticket := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }

	root := NewTreeNode(NewTreeNodeID(ticket(1), 0), "r", nil)
	src := NewTreeNode(NewTreeNodeID(ticket(2), 0), "p", nil)
	moved := NewTreeNode(NewTreeNodeID(ticket(3), 0), "p", nil)
	pred := NewTreeNode(NewTreeNodeID(ticket(5), 0), "p", nil)
	product := NewTreeNode(NewTreeNodeID(ticket(6), 0), "p", nil)
	require.NoError(t, root.Append(src, moved, pred, product))

	moved.MergedAt, moved.MergedFrom = ticket(4), src.id
	product.InsPrevID, product.BoundaryAnchor = pred.id, moved.id
	// Both nodes are newer than the merge and nest under the merged-away
	// node, so neither ends the walk: only the loop guard does.
	pred.BoundaryNest, product.BoundaryNest = src.id, src.id
	pred.InsPrevID = product.id
	tree := NewTree(root, ticket(1))

	done := make(chan struct{})
	go func() {
		defer close(done)
		holder, left := tree.preMergePoint(product, moved, ticket(7), nil)
		assert.Nil(t, holder)
		assert.Nil(t, left)
	}()
	select {
	case <-done:
	case <-gotime.After(10 * gotime.Second):
		t.Fatal("preMergePoint did not stop on a looping InsPrevID chain")
	}
}

// TestRestoreMergesAddsTicketsAtOnce checks that a snapshot's merge tickets,
// which a peer controls, come back sorted and deduplicated. RestoreMerges adds
// each entry in one pass rather than a ticket at a time, which would copy the
// whole set per ticket and so cost O(k^2) for an entry of length k.
func TestRestoreMergesAddsTicketsAtOnce(t *testing.T) {
	actor := time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	ticket := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }

	root := NewTreeNode(NewTreeNodeID(ticket(1), 0), "r", nil)
	src := NewTreeNode(NewTreeNodeID(ticket(2), 0), "p", nil)
	require.NoError(t, root.Append(src))
	tree := NewTree(root, ticket(1))
	src.addMergeTicket(ticket(5))

	tree.RestoreMerges([]TreeMerge{{
		Source:  src.id,
		Tickets: []*time.Ticket{ticket(9), nil, ticket(3), ticket(5), ticket(9)},
	}})

	require.Len(t, src.mergeTickets, 3)
	for i, lamport := range []int64{3, 5, 9} {
		assert.Equal(t, lamport, src.mergeTickets[i].Lamport())
	}
	require.Len(t, tree.Merges(), 1)
	assert.Equal(t, src.mergeTickets, tree.Merges()[0].Tickets)
}

// TestRestoreMergesRepeatedSourceStaysLinear sends many entries that all name
// the same node, the other axis a peer controls: nothing stops a payload from
// repeating one source, and adding an entry at a time would copy the node's
// whole set per entry, so the set grows quadratically with the entry count.
// The bound is on bytes allocated rather than on time, since what the shape
// costs is the repeated copy of the set.
func TestRestoreMergesRepeatedSourceStaysLinear(t *testing.T) {
	actor := time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	ticket := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }

	root := NewTreeNode(NewTreeNodeID(ticket(1), 0), "r", nil)
	src := NewTreeNode(NewTreeNodeID(ticket(2), 0), "p", nil)
	require.NoError(t, root.Append(src))
	tree := NewTree(root, ticket(1))

	const entries = 20000
	merges := make([]TreeMerge, 0, entries)
	for i := entries; i > 0; i-- {
		merges = append(merges, TreeMerge{Source: src.id, Tickets: []*time.Ticket{ticket(int64(i))}})
	}

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	tree.RestoreMerges(merges)
	runtime.ReadMemStats(&after)

	// One pass per node costs O(entries); one pass per entry costs about
	// entries^2/2 pointers, which is 1.6 GB here.
	assert.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(32<<20))
	require.Len(t, src.mergeTickets, entries)
	for i, tk := range src.mergeTickets {
		assert.Equal(t, int64(i+1), tk.Lamport())
	}
}

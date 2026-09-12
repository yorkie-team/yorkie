/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
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
	"github.com/yorkie-team/yorkie/pkg/document/resource"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// GCPair is a structure that represents a pair of parent and child for garbage
// collection.
type GCPair struct {
	Parent GCParent
	Child  GCChild

	// GCOnlySize is set for a child whose size was never counted in
	// docSize.Live: a piece born removed by splitting an already-tombstoned
	// node. Its value is the net-new size the split created. When set,
	// RegisterGCPair adds this to docSize.GC and AdjustDiffForGCPair leaves
	// docSize.Live untouched, instead of the usual live->gc move. Purge
	// subtracts the child's full size from GC; across the split's original
	// node and its born-dead pieces these telescope back to zero.
	GCOnlySize *resource.DataSize
}

// GCParent is an interface for the parent of the garbage collection target.
type GCParent interface {
	Purge(node GCChild) error
}

// GCChild is an interface for the child of the garbage collection target.
type GCChild interface {
	IDString() string
	RemovedAt() *time.Ticket
	DataSize() resource.DataSize
}

// GCBarrier is an optional capability of a GC parent whose surviving order is
// decided by which nodes are still linked.
//
// Every such container resolves a concurrent insert by walking forward from the
// anchor and stopping at the first node whose positioning ticket does not
// follow the insert: RGATreeList.findNextBeforeExecutedAt, RGATreeSplit's skip
// in findNodeWithSplit, and Tree's sibling skip in findNodesAndSplitText. The
// walk reads the nodes currently linked, tombstones included, so a tombstone
// with a small ticket is a hard barrier that ends the walk. Purging it physi-
// cally unlinks it, which means collection mutates the input to the insertion
// rule: a replica that has collected sends a still-in-flight insert past the
// node behind the tombstone, a replica that has not does not, and the two
// orders never reconverge.
//
// removedAt alone does not authorise the unlink. What does is the node that
// would become the walk's new stopping point: once that node is causally
// stable, every future insert carries a ticket after it, so every future walk
// stops there whether or not the tombstone in front of it still exists, and the
// insert lands in the same place either way. That successor's positioning
// ticket is what PurgeBarrierAt reports, and Root.GarbageCollect holds the
// purge back until the version vector covers it as well as removedAt.
//
// There can be more than one such ticket, and they are NOT reducible to their
// maximum. Ticket order is (lamport, actorID) and is total, but "covered by a
// version vector" is decided per actor: 3:1:C sorts after 3:1:B and a vector
// can cover it while the B entry is still at 1. Every ticket the barrier
// reports has to be checked on its own.
//
// The type parameter is only there because the two purge paths name their child
// differently: Root.GarbageCollect walks removed elements as Element and
// removed nodes as GCChild, and both end in the same physical unlink.
type GCBarrier[C any] interface {
	// PurgeBarrierAt returns the tickets that must ALL be covered before the
	// given child may be unlinked. A zero PurgeBarrier holds nothing back.
	PurgeBarrierAt(child C) PurgeBarrier
}

// PurgeBarrier is the set of tickets a purge is waiting on. It is a fixed-size
// value rather than a slice because Root.GarbageCollect asks for one per
// candidate per pass, and a slice put an allocation on every one of them: a
// wide tree collection allocated 4000 extra times and ran 30% slower for it.
//
// Two is the number of distinct things that can hold a purge back today -- the
// node that inherits the skip's stopping decision, and the last name still
// pointing at this one. Widen it if a third appears; nil entries are ignored.
type PurgeBarrier [2]*time.Ticket

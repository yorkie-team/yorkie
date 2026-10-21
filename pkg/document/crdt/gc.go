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
	// docSize.Live, and its value is what should enter docSize.GC. When set,
	// RegisterGCPair adds it to GC and leaves Live alone, instead of the
	// usual live->gc move. Purge always subtracts the child's full size from
	// GC; the two telescope back to zero.
	//
	// Several shapes are never in Live and so set it:
	//
	//   - a piece born removed by splitting an already-tombstoned node, where
	//     the value is the net-new size the split created and the rest is
	//     inside the original node's charge;
	//   - anything the snapshot-load scan registers, because the Live it runs
	//     against was computed from visible content only;
	//   - an array dead position node, which holds no element;
	//   - a node recreated under a removed parent, born tombstoned and never
	//     reported as recreated;
	//   - a live attribute removed from a node that is ALREADY a tombstone,
	//     whose bytes are inside that node's charge -- this one carries zero.
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
// The type parameter is only there because the two purge paths name their child
// differently: Root.GarbageCollect walks removed elements as Element and
// removed nodes as GCChild, and both end in the same physical unlink.
type GCBarrier[C any] interface {
	// PurgeBarrierAt returns the additional ticket that must be covered before
	// the given child may be unlinked, or nil when the child has no successor
	// and unlinking it cannot move anything.
	PurgeBarrierAt(child C) *time.Ticket
}

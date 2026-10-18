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
	//   - content an Edit inserts under a removed parent, born tombstoned;
	//   - a §7.4 split product tombstoned as it moves, cut off a node under
	//     a removed ancestor or moved into a detached subtree, where the
	//     value includes the removal ticket it is given;
	//   - the attribute tombstones a split copies into its product;
	//   - a live attribute removed from a node that is ALREADY a tombstone,
	//     whose bytes are inside that node's charge -- this one carries zero.
	//
	//   - a node a purge released, its tombstone's size, which the purge
	//     took off Live; collect leaves its pair to the drop of its subtree
	//     (see TreeNode.released). A piece split off one books its split
	//     overhead, as one split off a tombstone does.
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

// GCLiveRelease is an optional capability of a GC parent whose Purge takes
// the child out together with its descendants. A descendant can still be
// live: an insert or an identity restore concurrent with the child's
// removal leaves one there, and so can a crafted change that claims not to
// know the removal. A live node has no GC pair of its own, so purging its
// ancestor would detach bytes docSize.Live still holds while a rebuild,
// walking the attached tree, no longer counts them (yorkie#2244).
//
// Root therefore purges through purgeChild once removedAt and
// GCBarrier have both let the child go: it takes the child out, tombstones
// every live node under it, and reports them and their size, which Root
// moves from Live to GC under a pair of each node's own. They leave the
// document with the child's subtree, when a later collection lets it go,
// as if the child's removal had covered them, and they happen only with
// the purge. Nothing is held
// back, so no client input can keep a removed element in the tree: what a
// pass purges is decided by the collection vector and the tree, as on main.
//
// Replicas collecting at one vector release the same nodes. A client's
// synced vector covers a removal only after it has pushed every change it
// made before learning of it, and the server hands out a minimum vector
// only with the changes behind it, so an honest change concurrent with the
// removal is on every replica before any of them purges the child. The
// server race of yorkie#2110 breaks that contract: a minimum vector can
// arrive ahead of a change behind it. Such a change, like a crafted one,
// reaches a replica after its purge; the node it names is then gone or
// detached and stays a tombstone (see TreeNode.detached), which is what
// the release made it everywhere else. Released nodes still resolve, as
// main's detached nodes do, and a position inside the subtree resolves to
// the place the subtree was purged from, where a replica that has not
// purged it counts it from.
type GCLiveRelease[C any] interface {
	// purgeChild purges child as Purge does, tombstoning every live node
	// under it, and reports what Root has to book for it (see purgeResult).
	purgeChild(child C) (purgeResult, error)
}

// purgeResult is what a purge through purgeChild leaves for Root to
// book.
type purgeResult struct {
	// Purged reports whether the child's pair is done with: the child left
	// the tree, or was already out of it. A child that is not a tombstone is
	// not purged, and its pair stays.
	Purged bool

	// Released is the size the live nodes the purge tombstoned had in Live.
	Released resource.DataSize

	// ReleasedNodes are those nodes. Root books a pair for each, which
	// holds its bytes in GC until the subtree is let go (see Tree.released).
	ReleasedNodes []*TreeNode
}

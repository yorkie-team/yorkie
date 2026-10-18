/*
 * Copyright 2023 The Yorkie Authors. All rights reserved.
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
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/yorkie-team/yorkie/pkg/document/resource"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/index"
	"github.com/yorkie-team/yorkie/pkg/llrb"
)

var (
	// ErrNodeNotFound is returned when the node is not found.
	ErrNodeNotFound = errors.New("node not found")

	// ErrSplitOutOfRange is returned when a position anchors past the end of
	// the node it resolves to, which means the position cannot be resolved on
	// this replica.
	ErrSplitOutOfRange = errors.New("split offset out of range")
)

// TreeNodeForTest is a TreeNode for test.
type TreeNodeForTest struct {
	Type      string
	Children  []TreeNodeForTest
	Value     string
	Size      int
	IsRemoved bool
}

// TreePos represents a position in the tree. It is used to determine the
// position of insertion, deletion, and style change.
type TreePos struct {
	// ParentID is the ID of the parent node.
	ParentID *TreeNodeID

	// LeftSiblingID is the ID of the left sibling node. If the node is the
	// parent, it means that the position is leftmost.
	LeftSiblingID *TreeNodeID
}

// NewTreePos creates a new instance of TreePos.
func NewTreePos(parentID *TreeNodeID, leftSiblingID *TreeNodeID) *TreePos {
	return &TreePos{
		ParentID:      parentID,
		LeftSiblingID: leftSiblingID,
	}
}

// Equal returns whether the given pos equals or not.
func (t *TreePos) Equal(other *TreePos) bool {
	return t.ParentID.CreatedAt.Compare(other.ParentID.CreatedAt) == 0 &&
		t.ParentID.Offset == other.ParentID.Offset &&
		t.LeftSiblingID.CreatedAt.Compare(other.LeftSiblingID.CreatedAt) == 0 &&
		t.LeftSiblingID.Offset == other.LeftSiblingID.Offset
}

// TreeNodeID represent an ID of a node in the tree. It is used to
// identify a node in the tree. It is composed of the creation time of the node
// and the offset from the beginning of the node if the node is split.
//
// Some replicas may have nodes that are not split yet. In this case, we can
// use `map.floorEntry()` to find the adjacent node.
type TreeNodeID struct {
	CreatedAt *time.Ticket
	Offset    int
}

// NewTreeNodeID creates a new instance of TreeNodeID.
func NewTreeNodeID(createdAt *time.Ticket, offset int) *TreeNodeID {
	return &TreeNodeID{
		CreatedAt: createdAt,
		Offset:    offset,
	}
}

// NewTreeNode creates a new instance of TreeNode.
func NewTreeNode(id *TreeNodeID, nodeType string, attributes *RHT, value ...string) *TreeNode {
	node := &TreeNode{id: id}

	// NOTE(hackerwins): The value of TreeNode is optional. If the value is
	// empty, it means that the node is an element node.
	if len(value) > 0 {
		node.Value = value[0]
	}
	node.Attrs = attributes
	node.Index = index.NewNode(nodeType, node)

	return node
}

// toIDString returns a string that can be used as an ID for this TreeNodeID.
func (t *TreeNodeID) toIDString() string {
	return t.CreatedAt.Key() + ":" + strconv.Itoa(t.Offset)
}

// Compare compares the given two CRDTTreePos.
func (t *TreeNodeID) Compare(other llrb.Key) int {
	compare := t.CreatedAt.Compare(other.(*TreeNodeID).CreatedAt)
	if compare != 0 {
		return compare
	}

	if t.Offset > other.(*TreeNodeID).Offset {
		return 1
	} else if t.Offset < other.(*TreeNodeID).Offset {
		return -1
	}
	return 0
}

// Equal returns whether given ID is equal to this ID or not.
func (t *TreeNodeID) Equal(id *TreeNodeID) bool {
	return t.CreatedAt.Compare(id.CreatedAt) == 0 && t.Offset == id.Offset
}

// TreeNode is a node of Tree.
type TreeNode struct {
	Index *index.Node[*TreeNode]

	id        *TreeNodeID
	removedAt *time.Ticket

	InsPrevID *TreeNodeID
	InsNextID *TreeNodeID

	// Value is optional. If the value is not empty, it means that the node is a
	// text node.
	Value string

	// Attrs is optional. If the value is not empty,
	//it means that the node is an element node.
	Attrs *RHT

	// MergedFrom records the parent this node logically belongs to when a
	// merge relocated it into the merge target: either the source parent
	// it was physically moved out of, or — for a concurrent insert whose
	// position was declared inside a merged-away parent — that declared
	// parent (§9.4 intended-parent stamp). Persisted in the snapshot
	// encoding as the witness of the merge relationship.
	MergedFrom *TreeNodeID

	// MergedAt records the immutable ticket of the merge operation.
	// Persisted alongside MergedFrom because the source parent's
	// removedAt may be overwritten by later LWW tombstones and thus
	// cannot serve as the merge-time causal boundary for SplitElement's
	// §7.1 version-vector check. See docs/design/concurrent-merge-split.md.
	MergedAt *time.Ticket

	// mergedInto is a runtime cache pointing at the merge target (the
	// parent that received this node's children). Set locally during
	// merge execution and rebuilt from MergedFrom on snapshot load.
	// Used for the fast "is this tombstoned parent a merge source?"
	// check in FindTreeNodesWithSplitText; the alternative (scanning
	// NodeMapByID every position resolution) would be prohibitively
	// expensive.
	mergedInto *TreeNodeID

	// detached is set on every node of a subtree a purge took out of the
	// tree (see Tree.purge). Runtime only: a snapshot or DeepCopy
	// holds the attached tree alone.
	//
	// The invariant: a detached node is a tombstone, and stays one. A purge
	// tombstones every live node in the subtree it takes out
	// (Tree.purgeReleasing), so everything it detaches was removed and off
	// docSize.Live. The one op that could make a node live again, Restore,
	// resolves a detached node as a replica that purged it does (see
	// Tree.forgetDetached), and anything placed under one, or split off
	// one, is detached too and born tombstoned, as under any removed
	// parent. Detached nodes are kept in Tree.detachedByID rather than
	// NodeMapByID, which holds only what is reachable from the root, and
	// lookups consult both, so lookups and chain walks still reach detached
	// tombstones as they do on main. A position inside a detached subtree
	// resolves to the place the subtree was purged from (Tree.purgedSlot).
	// None of it can charge Live for a node a rebuild does not hold.
	detached bool

	// purgedFrom and purgedBefore are set on the node a purge took out: the
	// parent it left and the sibling it sat in front of, nil for the last.
	// Runtime only, like detached. They are where a position inside the
	// subtree resolves on this replica (see Tree.purgedSlot), as a replica
	// that has not purged the subtree resolves it at the subtree's place.
	purgedFrom   *TreeNode
	purgedBefore *TreeNode

	// released is set on a node a purge tombstoned because it was live in
	// the subtree taken out (see GCLiveRelease), and on pieces split off
	// one. Runtime only. Such a node carries no GC pair, and its bytes are
	// in neither Live nor GC: the purge took them off Live and booked
	// nothing, as a rebuild holds nothing for it.
	released bool
}

// Children returns the children of this node.
func (n *TreeNode) Children(includeRemovedNode ...bool) []*TreeNode {
	children := n.Index.Children(len(includeRemovedNode) > 0 && includeRemovedNode[0])
	nodes := make([]*TreeNode, len(children))
	for i, child := range children {
		nodes[i] = child.Value
	}
	return nodes
}

// Type returns the type of the Node.
func (n *TreeNode) Type() string {
	return n.Index.Type
}

// Len returns the length of the Node.
func (n *TreeNode) Len() int {
	return n.Index.Len()
}

// IsText returns whether the Node is text or not.
func (n *TreeNode) IsText() bool {
	return n.Index.IsText()
}

// ID returns the ID of this Node.
func (n *TreeNode) ID() *TreeNodeID {
	return n.id
}

// IDString returns the IDString of this Node.
func (n *TreeNode) IDString() string {
	return n.id.toIDString()
}

// RemovedAt returns the removal time of this Node.
func (n *TreeNode) RemovedAt() *time.Ticket {
	return n.removedAt
}

// SetRemovedAt sets the removal time of this node.
func (n *TreeNode) SetRemovedAt(ticket *time.Ticket) {
	n.removedAt = ticket
}

// IsRemoved returns whether the Node is removed or not.
func (n *TreeNode) IsRemoved() bool {
	return n.removedAt != nil
}

// Length returns the length of this node.
func (n *TreeNode) Length() int {
	encoded := utf16.Encode([]rune(n.Value))
	return len(encoded)
}

// String returns the string representation of this node's value.
func (n *TreeNode) String() string {
	return n.Value
}

// Attributes returns the string representation of this node's attributes.
func (n *TreeNode) Attributes() string {
	if n.Attrs == nil || n.Attrs.Len() == 0 {
		return ""
	}
	members := n.Attrs.Elements()

	// Sort the keys so the encoding is deterministic.
	keys := slices.AppendSeq(make([]string, 0, len(members)), maps.Keys(members))
	slices.Sort(keys)

	sb := strings.Builder{}
	for idx, k := range keys {
		if idx > 0 {
			sb.WriteString(" ")
		}
		value := members[k]
		fmt.Fprintf(&sb, `%s="%s"`, k, EscapeString(value))
	}

	return " " + sb.String()
}

// Append appends the given node to the end of the children.
func (n *TreeNode) Append(newNodes ...*TreeNode) error {
	indexNodes := make([]*index.Node[*TreeNode], len(newNodes))
	for i, newNode := range newNodes {
		indexNodes[i] = newNode.Index
	}

	return n.Index.Append(indexNodes...)
}

// Prepend prepends the given node to the beginning of the children.
func (n *TreeNode) Prepend(newNodes ...*TreeNode) error {
	indexNodes := make([]*index.Node[*TreeNode], len(newNodes))
	for i, newNode := range newNodes {
		indexNodes[i] = newNode.Index
	}

	return n.Index.Prepend(indexNodes...)
}

// Child returns the child of the given offset.
func (n *TreeNode) Child(offset int) (*TreeNode, error) {
	child, err := n.Index.Child(offset)
	if err != nil {
		return nil, err
	}

	return child.Value, nil
}

// Purge removes the given child from the children.
func (n *TreeNode) Purge(child GCChild) error {
	rhtNode := child.(*RHTNode)
	return n.Attrs.Purge(rhtNode)
}

// Split splits the node at the given offset.
func (n *TreeNode) Split(
	tree *Tree,
	offset int,
	issueTimeTicket func() *time.Ticket,
	versionVector time.VersionVector,
) (resource.DataSize, error) {
	var split *TreeNode
	var err error
	var diff resource.DataSize

	if n.IsText() {
		split, diff, err = n.SplitText(offset, n.id.Offset)
		if err != nil {
			return diff, err
		}
	} else {
		split, diff, err = n.SplitElement(offset, issueTimeTicket, versionVector)
		if err != nil {
			return diff, err
		}
	}

	if split != nil {
		// A piece split off a detached node sits in the same detached
		// subtree (see TreeNode.detached).
		split.detached = n.detached
		split.released = n.released

		split.InsPrevID = n.id
		if n.InsNextID != nil {
			insNext := tree.findFloorNode(n.InsNextID)
			split.InsNextID = n.InsNextID

			if insNext != nil {
				insNext.InsPrevID = split.id

				// §7.4 Empty Sibling Re-Parenting: When the existing
				// InsNext sibling is in a different parent (due to a prior
				// parent-level split), move the new split sibling to that
				// parent. This ensures that split siblings land in the same
				// parent regardless of operation application order.
				// VV-independent for clone/root consistency.
				//
				// Moved rather than detached and re-inserted: a split born
				// tombstoned contributes no VisibleLength to either parent,
				// so detaching would take two tokens off the source that it
				// never held and re-inserting would give the destination two
				// it must not have. MoveChildBefore carries the same
				// tombstone-aware semantics MoveChild documents.
				if !n.IsText() && insNext.Index.Parent != nil &&
					insNext.Index.Parent != split.Index.Parent &&
					len(split.Index.Children(true)) == 0 {
					// An empty product cut off a node out of sight, under a
					// removed ancestor, is born tombstoned before it moves, as
					// content inserted under a removed parent is. A replica
					// that purged the ancestor holds the node released, a
					// tombstone, and cuts a tombstone off it; moving a live
					// one out here would show it on this replica alone. The
					// product holds no children, so nothing goes with it.
					if split.removedAt == nil {
						if at := removedAncestorAt(n); at != nil {
							split.remove(at)
							diff.Meta += time.TicketSize
						}
					}
					if err := insNext.Index.Parent.MoveChildBefore(
						split.Index, insNext.Index,
					); err != nil {
						return diff, err
					}

					// The move can cross into or out of a subtree a purge
					// took out (see TreeNode.detached). The product takes
					// its new parent's side: out of one it is an attached
					// tombstone, as split off a removed node, and into one
					// it is released as the purge released what it found
					// there, so it is booked to GC below and not to Live.
					if dest := insNext.Index.Parent.Value; dest.detached != split.detached {
						split.detached = dest.detached
						// Out of a released subtree, the product is an
						// attached tombstone, which a rebuild counts, so it
						// books its pair below like any other.
						split.released = split.released && split.detached
						if split.detached && split.removedAt == nil {
							split.remove(detachedRemovalOr(dest, split.id.CreatedAt))
							diff.Meta += time.TicketSize
						}
					}
				}
			}
		}
		n.InsNextID = split.id
		tree.putNode(split)

		// NOTE: SplitElement deep-copies the node's attributes, tombstones
		// included -- it has to, or the two halves of what was one node would
		// resolve a concurrent style differently and never reconverge. Each
		// copied tombstone is a distinct piece of garbage that no removal path
		// produced, so buffer a registration for it; otherwise it sits in the
		// split's RHT forever, uncounted and unpurgeable.
		//
		// TreeNode.DataSize skips removed attributes, so the split's diff
		// never charged these to docSize.Live -- GCPairs already marks each
		// GCOnlySize, which sends it straight to docSize.GC, and Purge
		// subtracts the same amount back.
		tree.pendingGCPairs = append(tree.pendingGCPairs, split.GCPairs()...)

		// NOTE: A piece split off an already-tombstoned node inherits
		// removedAt without going through remove(), so no GC pair is
		// created for it in the normal deletion path. Buffer one here so
		// it can be purged; otherwise it stays in the tree forever. The
		// piece was never live, so the net-new size the split created goes
		// straight to docSize.GC when the pair is registered; return a zero
		// diff to the caller (which accounts diffs to docSize.Live).
		//
		// A piece split off a released node is released too: its source's
		// bytes are in neither Live nor GC, so the piece's are not either,
		// and a pair booked for its split overhead alone would give back its
		// whole size when purged, which GC never held.
		if split.removedAt != nil && split.released {
			return resource.DataSize{}, nil
		}
		if split.removedAt != nil {
			gcSize := diff
			tree.pendingGCPairs = append(tree.pendingGCPairs, GCPair{
				Parent:     tree,
				Child:      split,
				GCOnlySize: &gcSize,
			})
			return resource.DataSize{}, nil
		}
	}

	return diff, nil
}

// drainPendingGCPairs returns the GC pairs buffered for born-tombstoned
// split pieces and clears the buffer.
func (t *Tree) drainPendingGCPairs() []GCPair {
	pairs := t.pendingGCPairs
	t.pendingGCPairs = nil
	return pairs
}

// SplitText splits the text node at the given offset.
func (n *TreeNode) SplitText(
	offset int,
	absOffset int,
) (*TreeNode, resource.DataSize, error) {
	var diff resource.DataSize

	if offset == 0 || offset == n.Len() {
		return nil, diff, nil
	}

	// A position that anchors past the end of this node cannot be resolved
	// here. Report it instead of slicing out of range: the caller turns it
	// into a failed operation rather than a crashed server.
	if offset < 0 || offset > n.Len() {
		return nil, diff, fmt.Errorf(
			"split %s at %d of %d: %w", n.IDString(), offset, n.Len(), ErrSplitOutOfRange,
		)
	}

	encoded := utf16.Encode([]rune(n.Value))
	leftRune := utf16.Decode(encoded[0:offset])
	rightRune := utf16.Decode(encoded[offset:])

	if len(rightRune) == 0 {
		return nil, diff, nil
	}

	prevSize := n.DataSize()

	n.Value = string(leftRune)
	// The left piece spans UTF-16 offsets [0,offset), so offset is its length.
	// len(leftRune) is a rune count — utf16.Decode hands back runes — and it
	// undercounts by one per surrogate pair, while every offset measured
	// against this node counts UTF-16 code units.
	n.Index.VisibleLength = offset
	n.Index.TotalLength = offset

	rightNode := NewTreeNode(&TreeNodeID{
		CreatedAt: n.id.CreatedAt,
		Offset:    offset + absOffset,
	}, n.Type(), nil, string(rightRune))
	rightNode.removedAt = n.removedAt
	rightNode.MergedFrom = n.MergedFrom
	rightNode.MergedAt = n.MergedAt

	if err := n.Index.Parent.InsertAfterInternal(
		rightNode.Index,
		n.Index,
	); err != nil {
		return nil, diff, err
	}

	// NOTE(hackerwins): Calculate data size after node splitting:
	// Take the sum of the two split nodes(left and right) minus the size of
	// the original node. This calculates the net metadata overhead added by
	// the split operation.
	diff.Add(n.DataSize(), rightNode.DataSize())
	diff.Sub(prevSize)

	return rightNode, diff, nil
}

// SplitElement splits the given element at the given offset.
func (n *TreeNode) SplitElement(
	offset int,
	issueTimeTicket func() *time.Ticket,
	versionVector time.VersionVector,
) (*TreeNode, resource.DataSize, error) {
	var diff resource.DataSize

	prvSize := n.DataSize()

	// TODO(hackerwins): Define IDString of split node for concurrent editing.
	// Text has fixed content and its split nodes could have limited offset
	// range. But element node could have arbitrary children and its split
	// nodes could have arbitrary offset range. So, id could be duplicated
	// and its order could be broken when concurrent editing happens.
	// Currently, we use the similar IDString of split element with the split text.
	var splitAttrs *RHT
	if n.Attrs != nil {
		splitAttrs = n.Attrs.DeepCopy()
	}
	split := NewTreeNode(&TreeNodeID{CreatedAt: issueTimeTicket(), Offset: 0}, n.Type(), splitAttrs)
	split.removedAt = n.removedAt
	split.MergedFrom = n.MergedFrom
	split.MergedAt = n.MergedAt
	if err := n.Index.Parent.InsertAfterInternal(split.Index, n.Index); err != nil {
		return nil, diff, err
	}
	// A piece born tombstoned -- split off a node a concurrent deletion has
	// already removed -- is not visible, so it must not lengthen its live
	// ancestors. remove() holds the same invariant from the other side,
	// subtracting a node's padded length from its ancestors as it becomes a
	// tombstone. TotalLength counts tombstones and grows either way.
	if split.removedAt == nil {
		split.Index.UpdateAncestorsLength(split.Index.PaddedLength())
	}
	split.Index.UpdateAncestorsLength(split.Index.PaddedLength(true), true)

	allChildren := n.Index.Children(true)
	leftChildren := allChildren[0:offset]
	rightChildren := allChildren[offset:]

	// §7.1 Merge-Moved Children: Keep children merge-moved from a
	// source that is itself a child of the node being split in the
	// original (left) partition. When the source is external (e.g., a
	// sibling element that was merged), the content flows naturally to
	// the split (right) node. The merge ticket is read from the child's
	// immutable MergedAt field — NOT from the source's removedAt, which
	// may have been overwritten by a later LWW tombstone.
	var actualRight []*index.Node[*TreeNode]
	for _, child := range rightChildren {
		if child.Value.MergedFrom != nil && child.Value.MergedAt != nil &&
			len(versionVector) > 0 {
			actorID := child.Value.MergedAt.ActorID()
			if l, ok := versionVector.Get(actorID); !ok || l < child.Value.MergedAt.Lamport() {
				// Check if the merge source is a child of this node.
				sourceIsChild := false
				for _, sibling := range allChildren {
					if sibling.Value.id.Equal(child.Value.MergedFrom) {
						sourceIsChild = true
						break
					}
				}
				if sourceIsChild {
					leftChildren = append(leftChildren, child)
					continue
				}
			}
		}
		actualRight = append(actualRight, child)
	}

	// §7.3 Boundary Insert Migration: Move concurrent inserts at the
	// split boundary to the left. A concurrent insert placed between the
	// split boundary and the next original child was positioned relative
	// to the pre-split child order. Its CRDT position (after a
	// left-partition child) means it should stay in the left partition.
	//
	// Element split siblings (nodes with InsPrevID) are skipped during
	// the scan — they are split products handled by §7.4. Without
	// skipping, a split sibling at the start of the right partition
	// would set boundaryReached=true, hiding concurrent inserts that
	// follow it.
	if len(versionVector) > 0 {
		var movedToLeft []*index.Node[*TreeNode]
		var remaining []*index.Node[*TreeNode]
		boundaryReached := false
		for _, child := range actualRight {
			if !boundaryReached {
				// Skip element split siblings — they are split products
				// (handled by §7.4), not concurrent inserts. Text split
				// siblings have deterministic IDs and act as normal
				// boundary markers.
				if child.Value.InsPrevID != nil && !child.Value.IsText() {
					remaining = append(remaining, child)
					continue
				}
				actorID := child.Value.id.CreatedAt.ActorID()
				if l, ok := versionVector.Get(actorID); !ok || l < child.Value.id.CreatedAt.Lamport() {
					movedToLeft = append(movedToLeft, child)
					continue
				}
			}
			boundaryReached = true
			remaining = append(remaining, child)
		}
		if len(movedToLeft) > 0 {
			leftChildren = append(leftChildren, movedToLeft...)
			actualRight = remaining
		}
	}

	if err := n.Index.SetChildren(leftChildren); err != nil {
		return nil, diff, err
	}
	if err := split.Index.SetChildren(actualRight); err != nil {
		return nil, diff, err
	}

	recalcLength(n.Index)
	recalcLength(split.Index)

	// NOTE(hackerwins): Calculate data size after node splitting:
	// Take the sum of the two split nodes(left and right) minus the size of
	// the original node. This calculates the net metadata overhead added by
	// the split operation.
	diff.Add(n.DataSize(), split.DataSize())
	diff.Sub(prvSize)

	return split, diff, nil
}

// recalcLength recomputes VisibleLength and TotalLength of the given
// index node from its children. Used after SplitElement repartitions
// children between the original and split node.
func recalcLength(n *index.Node[*TreeNode]) {
	visible := 0
	for _, child := range n.Children(false) {
		visible += child.PaddedLength()
	}
	n.VisibleLength = visible

	total := 0
	for _, child := range n.Children(true) {
		total += child.PaddedLength(true)
	}
	n.TotalLength = total
}

// remove marks the node as removed.
func (n *TreeNode) remove(removedAt *time.Ticket) bool {
	if n.removedAt == nil {
		n.removedAt = removedAt

		// NOTE(hackerwins): Decrease only VisibleLength because
		// this node marked as tombstone, not purged.
		n.Index.UpdateAncestorsLength(-(n.Index.PaddedLength()))
		return true
	}

	// NOTE(sigmaith): Overwrite if newer tombstone.
	// This enables LWW for concurrent deletions.
	if removedAt.After(n.removedAt) {
		n.removedAt = removedAt
	}
	return false
}

// unremove clears the tombstone of this node (identity-preserving restore),
// mirroring remove()'s ancestor-length bookkeeping so the node becomes visible
// again in place.
//
// It leaves the merge lineage alone. A revived merge source loses it, both
// the mergedInto pointer and the MergedFrom/MergedAt stamps on the children a
// merge moved out of it, in Tree.dissolveMergeSources, which Restore runs over
// every element span it applies (#2189). Keeping both halves there, under the
// one gate on the restoring change's version vector, is what keeps them in
// step: a restore that dissolves nothing keeps the pointer with its stamps.
func (n *TreeNode) unremove() {
	if n.removedAt == nil {
		return
	}
	n.removedAt = nil
	n.Index.UpdateAncestorsLength(n.Index.PaddedLength())
}

func (n *TreeNode) canDelete(removedAt *time.Ticket, creationKnown, tombstoneKnown bool) bool {
	// NOTE(sigmaith): Skip if the node's creation was not visible to this operation.
	if !creationKnown {
		return false
	}

	if n.removedAt == nil {
		return true
	}

	// NOTE(sigmaith): Overwrite only if prior tombstone was not known
	// (concurrent or unseen) and newer. This enables LWW for concurrent deletions.
	if !tombstoneKnown && removedAt.After(n.removedAt) {
		return true
	}
	return false
}

// canStyle checks if node is able to set style. It answers the same question
// as RGATreeSplitNode.canStyle, the same way — see the contract there.
func (n *TreeNode) canStyle(vector time.VersionVector) bool {
	return !n.IsText() && time.TicketKnown(vector, n.id.CreatedAt)
}

// InsertAt inserts the given node at the given offset.
func (n *TreeNode) InsertAt(newNode *TreeNode, offset int) error {
	return n.Index.InsertAt(newNode.Index, offset)
}

// DeepCopy copies itself deeply.
func (n *TreeNode) DeepCopy() (*TreeNode, error) {
	var attrs *RHT
	if n.Attrs != nil {
		attrs = n.Attrs.DeepCopy()
	}

	clone := NewTreeNode(n.id, n.Type(), attrs, n.Value)
	clone.Index.VisibleLength = n.Index.VisibleLength
	clone.Index.TotalLength = n.Index.TotalLength
	clone.removedAt = n.removedAt
	clone.InsPrevID = n.InsPrevID
	clone.InsNextID = n.InsNextID
	clone.mergedInto = n.mergedInto
	clone.MergedFrom = n.MergedFrom
	clone.MergedAt = n.MergedAt

	if n.IsText() {
		return clone, nil
	}

	var children []*index.Node[*TreeNode]
	for _, child := range n.Index.Children(true) {
		node, err := child.Value.DeepCopy()
		if err != nil {
			return nil, err
		}

		children = append(children, node.Index)
	}

	if err := clone.Index.SetChildren(children); err != nil {
		return nil, err
	}

	return clone, nil
}

// DropSplitLinks clears the split-sibling links on this node and every one of
// its descendants.
//
// InsPrevID/InsNextID name positions in a split chain and only SplitElement
// may create them. A node arriving as operation content is freshly created by
// the editing client, so it can never legitimately be a split product — but
// the wire format carries the fields regardless, and the chain walks that
// read them (advancePastUnknownSplitSiblings, orderSameBoundarySplit) treat
// them as trusted structural pointers. Drop them on the way in rather than
// let a client hand the tree a chain of its choosing.
//
// The merge lineage is deliberately left alone here: unlike operation content,
// an element payload can be a DeepCopy of real document state (the reverse of
// a Remove restores the tree as it stood, merges and all), and erasing
// MergedFrom/MergedAt would switch off the §1.1 insert redirect and the §6.2
// propagation skip for a tree that legitimately earned them. Only
// DropEngineOnlyLinks, which runs on content no merge can have touched,
// clears those.
func (n *TreeNode) DropSplitLinks() {
	index.TraverseNode(n.Index, func(node *index.Node[*TreeNode], _ int) {
		node.Value.InsPrevID = nil
		node.Value.InsNextID = nil
	})
}

// DropEngineOnlyLinks clears the engine-only links on this node and every one
// of its descendants: the split-sibling chain and the merge lineage.
//
// It is the operation-content counterpart of DropSplitLinks. MergedFrom/
// MergedAt name the parent a node was moved out of and only a merge may stamp
// them (Edit restamps them on the content it inserts, from the merge parent it
// resolves locally), so content arriving on a TreeEdit — always freshly
// created by the editing client, never a copy of live state — can never
// legitimately carry them, while the §1.1 redirect and §6.2 propagation read
// them as trusted structural pointers. mergedInto goes with them: NewTree
// derives it from MergedFrom while decoding, so leaving it would keep a source
// parent pointing at a destination no field records any more.
func (n *TreeNode) DropEngineOnlyLinks() {
	index.TraverseNode(n.Index, func(node *index.Node[*TreeNode], _ int) {
		node.Value.InsPrevID = nil
		node.Value.InsNextID = nil
		node.Value.MergedFrom = nil
		node.Value.MergedAt = nil
		node.Value.mergedInto = nil
	})
}

// ClearTombstones clears the tombstone on this node and every one of its
// descendants, restoring the index lengths a decoded tombstone suppressed.
//
// Like the engine-only links above, removedAt is a field the wire format
// carries on every tree node yet operation content can never legitimately
// hold: a TreeEdit's content is freshly created by the editing client (the
// json layer builds new nodes, and a copy-reinsert reverse clears the
// tombstone on every node it keeps — see CloneForReinsert), so a node
// arriving born tombstoned is a crafted one. Left in place it is the worst of
// both states: Edit's insert loop counts it into the live data size and
// registers no GC pair for it under a live parent, so it is billed as live
// content that nothing will ever collect, while IsRemoved reports it invisible
// to every later edit.
//
// Cleared via unremove rather than by assignment so each node's padded length
// is given back to its ancestors, undoing what FromTreeNodes' removed-aware
// length pass withheld. Only the tombstone is cleared: the merge lineage on
// this content is DropEngineOnlyLinks' to drop, and it already has. Post-order matters: a child is revived before its
// parent, so the parent's own padded length already includes it when the
// parent hands its length further up.
func (n *TreeNode) ClearTombstones() {
	index.TraverseNode(n.Index, func(node *index.Node[*TreeNode], _ int) {
		node.Value.unremove()
	})
}

// ReissueIDs gives this node and every one of its descendants a fresh
// identity, taking one ticket per node in post-order.
//
// A reverse operation that reverses a deletion by re-inserting a copy of the
// removed nodes carries their original ids, so executing it would put two
// nodes under one id — the ambiguity that makes a position anchored there
// resolve differently on different replicas, and that makes the tree refuse
// the copy outright as content it already holds.
//
// A fresh identity has to be fresh in every field that names a node. The copy
// came from DeepCopy, which carries the split chain and the merge lineage of
// the node it copied: left in place they would splice this node into a chain
// it never belonged to, and Purge relinking that chain would unlink the real
// tombstone from it.
func (n *TreeNode) ReissueIDs(issueTimeTicket func() *time.Ticket) {
	index.TraverseNode(n.Index, func(node *index.Node[*TreeNode], _ int) {
		value := node.Value
		value.id = NewTreeNodeID(issueTimeTicket(), 0)
		value.InsPrevID = nil
		value.InsNextID = nil
		value.MergedFrom = nil
		value.MergedAt = nil
		value.mergedInto = nil
	})
}

// CloneForReinsert deep-copies this node for a reverse operation to re-insert
// and drops every descendant whose ID is in preTombstoned — i.e. descendants
// that were already tombstoned before the edit that removed this node ran.
// Those descendants represent the user's earlier delete intent and must not be
// resurrected by undoing that edit.
//
// The tombstone is cleared on every node the clone keeps, so the re-insertion
// brings them back live.
func (n *TreeNode) CloneForReinsert(preTombstoned map[string]struct{}) (*TreeNode, error) {
	clone, err := n.DeepCopy()
	if err != nil {
		return nil, err
	}

	if err := dropPreTombstoned(clone, preTombstoned); err != nil {
		return nil, err
	}

	// Post-order: clear the tombstone on every survivor and recompute its size
	// from its (already-resized) children. DeepCopy carried the original
	// node's size; after the drop above, that size is stale and has to be
	// rebuilt bottom-up. Element nodes derive their size from their children's
	// padded lengths, text nodes from their own value.
	index.TraverseNode(clone.Index, func(node *index.Node[*TreeNode], _ int) {
		node.Value.removedAt = nil
		if node.IsText() {
			node.VisibleLength = node.Value.Length()
			node.TotalLength = node.Value.Length()
			return
		}

		size := 0
		for _, child := range node.Children(true) {
			size += child.PaddedLength()
		}
		node.VisibleLength = size
		node.TotalLength = size
	})

	return clone, nil
}

// dropPreTombstoned drops every descendant of node whose ID is in
// preTombstoned, so a clone keeps only the nodes the edit itself transitioned
// from visible to tombstoned.
func dropPreTombstoned(node *TreeNode, preTombstoned map[string]struct{}) error {
	if node.IsText() {
		return nil
	}

	children := node.Index.Children(true)
	kept := make([]*index.Node[*TreeNode], 0, len(children))
	for _, child := range children {
		if _, ok := preTombstoned[child.Value.IDString()]; ok {
			continue
		}
		if err := dropPreTombstoned(child.Value, preTombstoned); err != nil {
			return err
		}
		kept = append(kept, child)
	}

	return node.Index.SetChildren(kept)
}

// InsertAfter inserts the given node after the given leftSibling.
func (n *TreeNode) InsertAfter(content *TreeNode, children *TreeNode) error {
	return n.Index.InsertAfter(content.Index, children.Index)
}

// SetAttr sets the given attribute of the element.
// SetAttr writes an attribute and reports what it replaced. See RHT.Set for
// what each return means and why the caller has to act on both.
func (n *TreeNode) SetAttr(k string, v string, ticket *time.Ticket) RHTWrite {
	if n.Attrs == nil {
		n.Attrs = NewRHT()
	}

	return n.Attrs.Set(k, v, ticket)
}

// RemoveAttr removes the given attribute of the element and reports what the
// removal dropped. See RHTRemoval.
func (n *TreeNode) RemoveAttr(k string, ticket *time.Ticket) RHTRemoval {
	if n.Attrs == nil {
		n.Attrs = NewRHT()
	}

	return n.Attrs.Remove(k, ticket)
}

func (n *TreeNode) DataSize() resource.DataSize {
	dataSize := resource.DataSize{
		Data: 0,
		Meta: 0,
	}

	if n.IsText() {
		dataSize.Data += n.Length() * 2
	}

	if n.id != nil {
		dataSize.Meta += time.TicketSize
	}

	if n.removedAt != nil {
		dataSize.Meta += time.TicketSize
	}

	if n.Attrs != nil {
		for _, node := range n.Attrs.Nodes() {
			if node.RemovedAt() != nil {
				continue
			}

			size := node.DataSize()
			dataSize.Data += size.Data
			dataSize.Meta += size.Meta
		}
	}

	return dataSize
}

// GCPairs returns the pairs of GC.
func (n *TreeNode) GCPairs() []GCPair {
	if n.Attrs == nil {
		return nil
	}

	var pairs []GCPair
	for _, node := range n.Attrs.Nodes() {
		if node.isRemoved {
			// TreeNode.DataSize skips a removed attribute, so it was never in
			// Live. See RegisterGCPair for what GCOnlySize means.
			gcSize := node.DataSize()
			pairs = append(pairs, GCPair{
				Parent:     n,
				Child:      node,
				GCOnlySize: &gcSize,
			})
		}
	}

	return pairs
}

// TreeRestoreSpan identifies a node a deletion transitioned visible →
// tombstoned, for identity-preserving Tree undo/redo. For text nodes the span
// is the absolute-offset interval [ID.Offset, ID.Offset+Length) of the
// original insertion (split-invariant); for element nodes it is the whole
// node. Value/Attributes are a deep copy so a GC-purged node can be recreated.
// LeftSiblingID/RightSiblingID are the deleted run's external boundary anchors
// (redundant on purpose): since a run's spans travel together, restore rebuilds
// the run's internal order from the op itself and needs only ONE surviving
// boundary to place it (id-order is not sibling-order in a tree).
type TreeRestoreSpan struct {
	ID             *TreeNodeID
	NodeType       string
	IsText         bool
	Length         int
	Value          string
	Attributes     *RHT
	ParentID       *TreeNodeID
	LeftSiblingID  *TreeNodeID
	RightSiblingID *TreeNodeID
}

// copy returns a copy of the span that shares nothing mutable with it: the
// IDs are never written once made, and the attribute snapshot is copied, so a
// reverse operation built from it never aliases the operation's payload.
func (s *TreeRestoreSpan) copy() *TreeRestoreSpan {
	copied := *s
	if s.Attributes != nil {
		copied.Attributes = s.Attributes.DeepCopy()
	}
	return &copied
}

// kindAgrees reports whether the span's IsText flag matches its NodeType, as
// it always does on a span an honest replica captured. A span that disagrees
// would recreate a node of the wrong kind -- an element carrying a Value is
// then invisible to the text lookup, so the same span applied again inserts a
// second node under one ID. Decoding cannot reject it: stored changes go
// through the same decoder and must stay loadable, so Restore and Retombstone
// skip it instead. The kind is part of the span itself, so every replica
// skips it alike (#2168).
func (s *TreeRestoreSpan) kindAgrees() bool {
	return s.IsText == (s.NodeType == index.TextNodeType)
}

// Tree represents the tree of CRDT. It has doubly linked list structure and
// index tree structure.
type Tree struct {
	IndexTree   *index.Tree[*TreeNode]
	NodeMapByID *llrb.Tree[*TreeNodeID, *TreeNode]

	createdAt *time.Ticket
	movedAt   *time.Ticket
	removedAt *time.Ticket

	// pendingGCPairs buffers GC pairs for nodes that were created
	// already-tombstoned by splitting a removed node. Such pieces inherit
	// removedAt without ever passing through remove(), so they would
	// otherwise never be registered for GC. Edit, Style and RemoveStyle
	// drain this buffer into the GC pairs they return.
	pendingGCPairs []GCPair

	// forgotten holds the detached nodes Restore forgot since the last
	// TakeForgotten, whose pending GC pairs the caller drops (see
	// forgetDetached).
	forgotten []*TreeNode

	// mergeStamps indexes the nodes that carry a MergedFrom by the source it
	// names, so dissolveMergeSources visits only those (#2189). Every way a
	// node joins the index tree registers it -- NewTree, or putNode, which
	// indexes a node even when it refuses it the ID lookup -- and mergeNodes
	// indexes a node it stamps in place, so no stamped node a walk of the
	// tree reaches is missing. A detached node stays indexed, as it does on
	// a replica that has not purged its subtree; only a node leaving the
	// document (its own purge, forgetDetached) leaves it. It is a candidate
	// set, re-checked when read: an entry for a node that lost its stamp is
	// dropped then.
	mergeStamps map[string]map[*TreeNode]struct{}

	// detachedByID holds the nodes of subtrees a purge took out, by ID, so
	// NodeMapByID holds exactly the nodes reachable from the root. Lookups
	// consult both (see findFloorNode), so a position naming a detached node
	// resolves as it did while main kept such nodes in NodeMapByID. A
	// detached node leaves it when its own GC pair purges it, or when a
	// restore names it (see forgetDetached). NewTree, the only way a Tree is
	// made, always sets it.
	detachedByID *llrb.Tree[*TreeNodeID, *TreeNode]
}

// NewTree creates a new instance of Tree.
func NewTree(root *TreeNode, createdAt *time.Ticket) *Tree {
	tree := &Tree{
		IndexTree:    index.NewTree[*TreeNode](root.Index),
		NodeMapByID:  llrb.NewTree[*TreeNodeID, *TreeNode](),
		detachedByID: llrb.NewTree[*TreeNodeID, *TreeNode](),
		createdAt:    createdAt,
	}

	// Registering every node is the cost of loading a document, so it runs
	// without the duplicate check: a plain Put per node, then one comparison
	// to see whether any ID was claimed twice. Only a tree that carries
	// duplicates pays for resolving them.
	nodeCount := 0
	index.Traverse(tree.IndexTree, func(node *index.Node[*TreeNode], depth int) {
		tree.NodeMapByID.Put(node.Value.id, node.Value)
		tree.noteMergeStamp(node.Value)
		nodeCount++
	})
	if tree.NodeMapByID.Len() != nodeCount {
		index.Traverse(tree.IndexTree, func(node *index.Node[*TreeNode], depth int) {
			tree.putNode(node.Value)
		})
	}

	// Rebuild runtime merge state from the persisted MergedFrom field.
	// MergedFrom is the only merge-related field written to the snapshot;
	// mergedInto, mergedChildIDs, and mergedAt are derived here so that a
	// replica loaded from a snapshot can still handle concurrent ops that
	// target merged-away parents (§1.1 redirect, §6.2 propagation,
	// §7.1 split skip).
	tree.rebuildMergeState()

	return tree
}

// rebuildMergeState reconstructs the mergedInto cache on source
// parents from the persisted MergedFrom field on moved children.
// Called after NodeMapByID is populated. The merge ticket is read
// from the child's persisted MergedAt field; for snapshots written
// before MergedAt was added to the proto, it falls back to the
// source's removedAt (an approximation that may be wrong if the
// source was later overwritten by a concurrent delete).
func (t *Tree) rebuildMergeState() {
	index.Traverse(t.IndexTree, func(node *index.Node[*TreeNode], _ int) {
		child := node.Value
		if child.MergedFrom == nil || node.Parent == nil {
			return
		}

		// findMergeNode, not findFloorNode: MergedFrom is client-supplied on an
		// element payload (Set/Add/ArraySet), so the source it names has to be
		// the node it names exactly, and an element. See findMergeNode.
		src := t.findMergeNode(child.MergedFrom)
		if src == nil {
			return
		}

		// A merge moves children under an element parent, so a child sitting
		// under a text node cannot be one a merge moved. Only a snapshot is
		// guaranteed well-formed here: an element payload (Set/Add/ArraySet)
		// passes through this same reader and keeps its MergedFrom, because
		// the reverse of a Remove legitimately carries the merges the tree
		// really underwent. Deriving a forwarding pointer at a text node from
		// a crafted one is what would hand a later insert a parent that can
		// hold no children.
		if node.Parent.Value.IsText() {
			return
		}

		// A merge only ever names a source the merge itself removed: Phase 5
		// tombstones every merge boundary before Phase 6 moves its children,
		// so a live element is never a merge source (see TreeNode.unremove).
		// Decline to derive the pointer, but leave the stamps as they are:
		// this reader also runs on element payloads and on DeepCopy, so it
		// must not rewrite what it reads. Mirrors the JS rebuildMergeState.
		if src.removedAt == nil {
			return
		}

		// Backwards compatibility: older snapshots have MergedFrom
		// without MergedAt. Fall back to src.removedAt — imperfect
		// when a later tombstone overwrote it, but this is the best
		// we can do without the persisted merge ticket.
		if child.MergedAt == nil {
			child.MergedAt = src.removedAt
		}

		if src.mergedInto == nil {
			src.mergedInto = node.Parent.Value.id
		}
	})
}

// dissolveMergeSources clears the merge lineage the elements with the given
// IDs carry as merge sources, in both halves: the mergedInto pointer, and the
// MergedFrom/MergedAt stamps every child a merge moved out of them still
// carries. Restore calls it once, after applying every span (#2189).
//
// Only the stamps of merges the restoring change knew are cleared, judged by
// MergedAt against its version vector. The restore reverses a removal its
// author saw, and the merges that author saw are the lineage that removal
// left; a merge concurrent with the restore stamps its children on every
// replica whichever order the two arrive in — after the restore on a replica
// that applied the restore first — so clearing its stamps on a replica that
// applied the merge first is what would make the persisted lineage depend on
// the arrival order. A stamp without MergedAt predates the field and is
// treated as known.
//
// An empty version vector knows no merge, so the restore dissolves nothing.
// Elsewhere an empty vector reads as one that has seen everything, the local
// convention, but here it would make the dissolution unconditional, and a
// change's vector is whatever its pushing client put on the wire. Every
// change an SDK makes carries its author's vector, so only a change without
// clocks takes this path; it leaves the stamps as they are on every replica.
//
// It takes IDs, not nodes, because the caller queues one per element span
// before knowing what the local tree holds for it: the element may be live
// already, a tombstone the span revived, a recreate, or absent entirely
// (recreateFromSpan declines under a removed or unresolvable parent). Only the
// pointer half needs a node, and it is looked up here; the stamp half is a
// sweep over IDs, so an element that is not in this replica's tree still has
// the lineage naming it cleared — which is the point, since a replica that did
// hold it clears the same stamps.
//
// Clearing the stamps is what makes the dissolution survive a snapshot:
// mergedInto is a runtime cache that rebuildMergeState re-derives from the
// stamps, so dropping only the pointer would let a reload resurrect the live
// element as a source. The two halves are dropped together.
//
// The sweep keys on the sources' own IDs, through the mergeStamps index,
// rather than walking the children of the target mergedInto names, because
// that target is not where all of the lineage necessarily lives:
//
//   - §7.1's SplitElement relocates merge-stamped children into the other half
//     of the split pair, out of reach of a children-of-target walk; and
//   - a recreated (previously purged) element comes back live under its
//     original ID carrying no pointer at all, while stamps elsewhere may still
//     name it.
//
// Either leftover is re-derivable: it names a live source so rebuildMergeState
// declines it now, but the moment a later edit tombstones the revived element
// the same stamps name a removed one and the pointer comes back. Keying on the
// source ID also means only lineage naming *this* element is touched, with no
// dependence on a pointer that may be a client's (see findMergeNode).
func (t *Tree) dissolveMergeSources(sources []*TreeNodeID, versionVector time.VersionVector) {
	if len(sources) == 0 || len(versionVector) == 0 {
		return
	}

	ids := make(map[string]*TreeNodeID, len(sources))
	for _, id := range sources {
		ids[id.toIDString()] = id
	}

	for key, id := range ids {
		stamped := t.mergeStamps[key]
		for child := range stamped {
			// Only a node still stamped with this source. A detached one
			// counts as an attached one does: whether a purge has taken the
			// stamp's subtree out must not decide whether the pointer goes,
			// or a replica that collected would drop what one that did not
			// keeps.
			if child.MergedFrom == nil || child.MergedFrom.toIDString() != key {
				delete(stamped, child)
				continue
			}
			if child.MergedAt != nil && !time.TicketKnown(versionVector, child.MergedAt) {
				continue
			}

			child.MergedFrom = nil
			child.MergedAt = nil
			delete(stamped, child)
		}
		// The pointer follows the stamps: it goes only when no stamp naming
		// the source is left, as a reload would then derive none. One that a
		// merge the restore did not know still stamps keeps it, as a reload
		// of a removed source would derive it again.
		if len(stamped) == 0 {
			delete(t.mergeStamps, key)
			if src := t.findFloorNode(id); src != nil && src.id.Equal(id) {
				src.mergedInto = nil
			}
		}
	}
}

// Marshal returns the JSON encoding of this Tree.
func (t *Tree) Marshal() string {
	builder := &strings.Builder{}
	marshal(builder, t.Root())
	return builder.String()
}

// Purge physically purges the given node.
// PurgeBarrierAt implements GCBarrier[GCChild]. findNodesAndSplitText walks the
// parent's children, removed ones included, advancing while the next sibling
// was created after the incoming edit; a tombstoned sibling with an older
// ticket ends that walk. Purging detaches it from the parent, so the next
// sibling inherits the decision and must be causally stable first.
func (t *Tree) PurgeBarrierAt(child GCChild) *time.Ticket {
	node, ok := child.(*TreeNode)
	if !ok || node.Index == nil || node.Index.Parent == nil {
		return nil
	}

	next := node.Index.Parent.NextSiblingOf(node.Index)
	if next == nil {
		return nil
	}
	return next.Value.id.CreatedAt
}

// Purge implements GCParent: it detaches child, with its subtree, from the
// tree, releasing the live nodes under it as purgeChild does. It cannot
// report their size; Root, which keeps the size ledger, purges through
// purgeChild instead (see GCLiveRelease). See
// TreeNode.detached for what a detached node is.
func (t *Tree) Purge(child GCChild) error {
	_, _, err := t.purgeReleasing(child.(*TreeNode))
	return err
}

// purgeChild implements GCLiveRelease[GCChild]: it is Purge, and it
// reports what Root has to book for it.
func (t *Tree) purgeChild(child GCChild) (purgeResult, error) {
	_, result, err := t.purgeReleasing(child.(*TreeNode))
	return result, err
}

// purge is Purge, also reporting how many nodes it walked.
func (t *Tree) purge(node *TreeNode) (int, error) {
	visits, _, err := t.purgeReleasing(node)
	return visits, err
}

// purgeReleasing takes node, with its subtree, out of the tree, reporting
// how many nodes it walked and what Root has to book (see purgeResult). It
// unregisters and unlinks the purged node, as on main, and records where it
// was taken from. Its subtree is marked detached and moved to detachedByID,
// its merge stamps stay indexed (only node's own leaves with node), and
// every live node in it is tombstoned
// with node's removedAt (see GCLiveRelease); those still resolve, as main's
// detached nodes do. The pairs of the subtree's tombstones and removed
// attributes stay, and each is collected when its own removal is, as on
// main. The subtree is walked once in
// all: a purge of a node already inside one finds the mark and walks
// nothing, so one collection pass costs O(nodes). Nothing changes unless
// the node, a tombstone, actually leaves its parent.
func (t *Tree) purgeReleasing(node *TreeNode) (int, purgeResult, error) {
	var result purgeResult

	// A parentless node cannot be detached: purging is removal from a parent's
	// child list. GCPairs never books one, but a pair outlives the tree state
	// it was booked against (it sits in gcNodePairMap until the minimum synced
	// version passes it), so a node detached in between reaches here. Dropping
	// it keeps GarbageCollect advancing instead of panicking on the deref.
	if node.Index == nil || node.Index.Parent == nil {
		result.Purged = true
		return 0, result, nil
	}
	// Only a tombstone is purged: a GC pair claims its child is removed, and
	// a live node taken out would carry live bytes, and the live nodes under
	// it, out of the tree with no removal to release them with. Its pair
	// stays.
	if node.removedAt == nil {
		return 0, result, nil
	}
	from := node.Index.Parent
	var before *TreeNode
	if next := from.NextSiblingOf(node.Index); next != nil {
		before = next.Value
	}
	if err := from.RemoveChild(node.Index); err != nil {
		return 0, result, err
	}
	node.purgedFrom, node.purgedBefore = from.Value, before

	visits := 0
	if !node.detached {
		index.TraverseNode(node.Index, func(n *index.Node[*TreeNode], _ int) {
			visits++
			if !n.Value.IsRemoved() {
				result.Released.Add(n.Value.DataSize())
				n.Value.remove(node.removedAt)
				n.Value.released = true
			}
			n.Value.detached = true
			if n.Value != node {
				t.moveToDetached(n.Value)
			}
		})
	}

	// NodeMapByID is keyed by ID, so an unconditional Remove would also
	// unregister a different node that shares this one's ID — see putNode.
	// Only drop the entry this node actually holds. The node leaves the
	// document, so its merge stamp leaves the index; those of the detached
	// nodes under it stay, as they stay on a replica that has not purged.
	t.unregisterNode(node)
	if node.MergedFrom != nil {
		t.forgetMergeStamp(node)
	}
	t.unlinkInsChain(node)

	result.Purged = true
	return visits, result, nil
}

// unlinkInsChain joins node's InsPrevID/InsNextID neighbours to each other
// and clears node's own links, for a node leaving the tree.
func (t *Tree) unlinkInsChain(node *TreeNode) {
	insPrevID := node.InsPrevID
	insNextID := node.InsNextID
	// NOTE: findFloorNode may return nil when the insertion neighbor was
	// already purged (restore/recreate reorders GC so a neighbor can be
	// collected first). Guard both derefs rather than assuming survival.
	if insPrevID != nil {
		if insPrev := t.findFloorNode(insPrevID); insPrev != nil {
			insPrev.InsNextID = insNextID
		}
	}
	if insNextID != nil {
		if insNext := t.findFloorNode(insNextID); insNext != nil {
			insNext.InsPrevID = insPrevID
		}
	}
	node.InsPrevID = nil
	node.InsNextID = nil
}

// detachedRemovalOf returns the removal a node placed under the detached
// node is tombstoned with: the node's own, or its nearest removed
// ancestor's. Every detached node is a tombstone, since a purge takes out
// only a removed node and tombstones what is live under it, so it is nil
// only for a tree that breaks that invariant.
func detachedRemovalOf(node *TreeNode) *time.Ticket {
	if node.removedAt != nil {
		return node.removedAt
	}
	return removedAncestorAt(node)
}

// takeDetachedSide gives node, just moved under a detached parent, that
// parent's side of a purge (see TreeNode.detached): the node and its
// subtree are marked detached and moved to detachedByID, keeping their
// merge stamps indexed, and every live node in it is tombstoned with at, as the
// purge released what it found there. It returns the size those had in
// Live. A node already detached is left as it is.
func (t *Tree) takeDetachedSide(node *TreeNode, at *time.Ticket) resource.DataSize {
	var released resource.DataSize
	index.TraverseNode(node.Index, func(n *index.Node[*TreeNode], _ int) {
		if n.Value.detached {
			return
		}
		if !n.Value.IsRemoved() {
			released.Add(n.Value.DataSize())
			n.Value.remove(at)
			n.Value.released = true
		}
		n.Value.detached = true
		t.moveToDetached(n.Value)
	})
	return released
}

// detachedRemovalOr returns the removal a node placed under the detached
// node is tombstoned with (detachedRemovalOf), or fallback when there is
// none. A purge takes out only a tombstone, so a detached node always has a
// removal and the fallback is never used through a purge. It is there so
// that a tree breaking the invariant still keeps "every detached node is a
// tombstone" and applies the change: the server stores a change before it
// applies one, so an error here would leave every later replay of the
// document failing. The fallback is a ticket of the change itself, the same
// on every replica that applies it.
func detachedRemovalOr(node *TreeNode, fallback *time.Ticket) *time.Ticket {
	if at := detachedRemovalOf(node); at != nil {
		return at
	}
	return fallback
}

// removedAncestorAt returns the removal of node's nearest removed ancestor,
// or nil when every ancestor is live.
func removedAncestorAt(node *TreeNode) *time.Ticket {
	for n := node.Index.Parent; n != nil; n = n.Parent {
		if n.Value.removedAt != nil {
			return n.Value.removedAt
		}
	}
	return nil
}

// purgedSlot returns where the detached subtree holding node was purged
// from in the attached tree: the attached parent and the child it sat in
// front of, nil for the end. A sibling purged since is passed over to the
// one it sat in front of in turn, and a parent purged since resolves to its
// own place, so the slot is always in the attached tree. A sibling that has
// left the parent some other way, by a merge moving it out, is no child of
// it, and the slot is then the end, as for nil: whether a replica that has
// not purged the subtree moved it along depends on which side of the
// merging range it sat, which is not recorded. ok is false only for a
// detached node with no purge recorded above it.
func (t *Tree) purgedSlot(node *TreeNode) (*TreeNode, *TreeNode, bool) {
	// The recorded parents and siblings of honest purges never loop, but a
	// node can be placed under a subtree it was purged from; a walk that
	// comes back to a node it passed fails, as an unresolvable position
	// does, instead of spinning.
	seen := make(map[*TreeNode]struct{})
	visit := func(n *TreeNode) bool {
		if _, ok := seen[n]; ok {
			return false
		}
		seen[n] = struct{}{}
		return true
	}
	top := node
	for top.Index.Parent != nil {
		top = top.Index.Parent.Value
	}
	for top.detached {
		if top.purgedFrom == nil || !visit(top) {
			return nil, nil, false
		}
		parent, before := top.purgedFrom, top.purgedBefore
		for before != nil && before.detached && before.purgedFrom == parent {
			if !visit(before) {
				return nil, nil, false
			}
			before = before.purgedBefore
		}
		if !parent.detached {
			if before != nil && before.Index.Parent != parent.Index {
				before = nil
			}
			return parent, before, true
		}
		top = parent
		for top.Index.Parent != nil {
			top = top.Index.Parent.Value
		}
	}
	return nil, nil, false
}

// unregisterNode drops node's own entry from NodeMapByID, if the entry
// registered under its ID is this node. NodeMapByID is keyed by ID, so an
// unconditional Remove would also unregister a different node that shares
// this one's ID — see putNode.
func (t *Tree) unregisterNode(node *TreeNode) {
	if key, registered := t.NodeMapByID.Floor(node.id); registered == node && key.Equal(node.id) {
		t.NodeMapByID.Remove(node.id)
	}
	if key, registered := t.detachedByID.Floor(node.id); registered == node && key.Equal(node.id) {
		t.detachedByID.Remove(node.id)
	}
}

// moveToDetached moves node's own entry from NodeMapByID to detachedByID,
// for a node a purge has just detached. A node another one holds the ID
// over has no entry to move: no lookup reached it before the purge either,
// and none does on a replica that has not purged.
func (t *Tree) moveToDetached(node *TreeNode) {
	if key, registered := t.NodeMapByID.Floor(node.id); registered == node && key.Equal(node.id) {
		t.NodeMapByID.Remove(node.id)
		t.detachedByID.Put(node.id, node)
	}
}

// TreeVisibleEdit is one visible change an identity-preserving Restore or
// Retombstone made: the visible-index range [From, To) it replaced, measured
// against the tree as it stood at that moment, and the size it inserted there.
// A removal reports the range the node covered and InsertedSize 0; a revival
// reports a collapsed range (From == To) at the node's position and its padded
// size as InsertedSize. Undo-stack reconciliation applies them one after
// another, exactly like a sequence of ordinary edits; an ordinary TreeEdit
// reports its single range in the same shape (see
// operations.TreeEdit.ExecutedRanges). Mirrors the JS TreeVisibleEdit, minus
// the paths and value only its OpInfo uses.
type TreeVisibleEdit struct {
	From, To, InsertedSize int
}

// Restore re-establishes the nodes described by spans under their ORIGINAL
// identities (identity-preserving Tree undo): live → skip (idempotent),
// tombstoned → unremove in place, purged → recreate. A span whose kind does
// not agree with itself is skipped outright (see kindAgrees), as it is in
// Retombstone. Spans must be in
// parent-before-child order. Returns (untombstoned, recreated); the caller
// un-registers GC pairs for the un-tombstoned nodes and accounts recreated
// sizes to Live. When measure is set it also returns one insertion per node
// that became visible, in the order it did, each measured right after that
// node came back, so applying them one after another reproduces the result
// (see TreeVisibleEdit). versionVector is that of the change the restore
// belongs to; it decides which merge lineage the restore dissolves (see
// dissolveMergeSources). Mirrors the JS CRDTTree.restore.
func (t *Tree) Restore(spans []*TreeRestoreSpan, measure bool, versionVector time.VersionVector) (
	untombstoned, recreated []*TreeNode,
	pairs []GCPair,
	diff resource.DataSize,
	edits []TreeVisibleEdit,
	err error,
) {
	// Splitting a removed straddler buffers born-removed remainders as pending
	// GC pairs (see TreeNode.Split). Drain them into the returned pairs on EVERY
	// path — including a mid-restore error — so they can never linger to
	// contaminate the next operation's drain. The caller registers these BEFORE
	// un-registering the un-tombstoned targets, so a split-born target is walked
	// gc->live correctly (mirrors the Text path).
	defer func() {
		pairs = append(pairs, t.drainPendingGCPairs()...)
	}()

	revived := func(node *TreeNode) {
		if !measure {
			return
		}
		if from, to, ok := t.visibleRangeOf(node); ok {
			edits = append(edits, TreeVisibleEdit{From: from, To: from, InsertedSize: to - from})
		}
	}

	// Elements this call restores and so may no longer be named as a merge
	// source, dissolved together once every span is applied. Queued from the
	// SPAN rather than from whatever the local tree turned out to hold: an
	// element span resolves to one of four outcomes — already live, a revived
	// tombstone, a recreated element, or a recreate that declines (born
	// tombstoned under a removed parent, or an unresolvable parent) — and
	// which one a replica takes depends only on its local state. Keying the
	// dissolution on the span's own ID is what keeps the persisted half
	// (MergedFrom/MergedAt on children a merge moved out of the element)
	// identical on every replica; deriving it from the outcome let two
	// replicas persist different lineage for the same undo, and that lineage
	// is what propagateMergeDeletes cascades on. See dissolveMergeSources
	// (#2189). It runs on the success path only: a restore that fails part
	// way leaves the lineage of spans it never applied alone.
	var mergeSources []*TreeNodeID

	// The forgotten list reports this call's alone (see TakeForgotten).
	t.forgotten = nil

	underKnownRemoval := t.knownRemovalAbove(versionVector)
	for _, span := range spans {
		if !span.kindAgrees() {
			continue
		}
		if !span.IsText {
			// A restored element can no longer be a merge source: the lineage
			// goes with the tombstone that gave it meaning (#2189). Queued for
			// every element span, before the outcome is known, and whether or
			// not the runtime pointer is set — the persisted half outlives the
			// pointer and rebuildMergeState would derive it back from a
			// snapshot the moment a later edit removes this node again.
			mergeSources = append(mergeSources, span.ID)

			node := t.findFloorNode(span.ID)
			if node != nil && node.id.Equal(span.ID) && node.detached {
				// Resolved as a replica that purged it does (see
				// forgetDetached): recreated if its parent is there.
				t.forgetDetached(node)
				node = nil
			}
			if node != nil && node.id.Equal(span.ID) {
				if node.IsRemoved() && !underKnownRemoval(node) {
					node.unremove()
					untombstoned = append(untombstoned, node)
					revived(node)
				}
				continue
			}
			created, cErr := t.recreateFromSpan(span, span.ID.Offset, span.Length, versionVector)
			if cErr != nil {
				return nil, nil, nil, diff, nil, cErr
			}
			if created != nil {
				recreated = append(recreated, created)
				revived(created)
			}
			continue
		}

		// Text: pieces may be split finer than the span, and a concurrent op or
		// a post-GC recreate can leave pieces whose boundaries straddle it.
		// Isolate the exact [start, end) sub-range out of every overlapping
		// piece — splitting at the span boundaries, live or removed — so all
		// replicas converge on identical segmentation (the tree analogue of
		// RGATreeSplit.isolateRange), then revive removed parts and recreate
		// purged gaps.
		start := span.ID.Offset
		end := start + span.Length
		// Detached pieces are forgotten until none is left: each lookup
		// answers with whatever now holds the range, which is never a
		// detached piece once the loop ends.
		pieces := t.findPiecesOverlapping(span.ID.CreatedAt, start, end)
		for slices.ContainsFunc(pieces, func(piece *TreeNode) bool { return piece.detached }) {
			for _, piece := range pieces {
				if piece.detached {
					t.forgetDetached(piece)
				}
			}
			pieces = t.findPiecesOverlapping(span.ID.CreatedAt, start, end)
		}
		cursor := start
		pieceIdx := 0
		for cursor < end {
			if pieceIdx < len(pieces) && pieces[pieceIdx].id.Offset <= cursor {
				piece := pieces[pieceIdx]
				pieceEnd := piece.id.Offset + piece.Length()
				overlapEnd := min(pieceEnd, end)
				target, iErr := t.isolateTextRange(piece, cursor, overlapEnd, &diff)
				if iErr != nil {
					return nil, nil, nil, diff, nil, iErr
				}
				if target.IsRemoved() && !underKnownRemoval(target) {
					target.unremove()
					untombstoned = append(untombstoned, target)
					revived(target)
				}
				cursor = overlapEnd
				if overlapEnd >= pieceEnd {
					pieceIdx++
				}
			} else {
				gapEnd := end
				if pieceIdx < len(pieces) {
					gapEnd = min(pieces[pieceIdx].id.Offset, end)
				}
				created, cErr := t.recreateFromSpan(span, cursor, gapEnd-cursor, versionVector)
				if cErr != nil {
					return nil, nil, nil, diff, nil, cErr
				}
				if created != nil {
					recreated = append(recreated, created)
					revived(created)
				}
				cursor = gapEnd
			}
		}
	}

	t.dissolveMergeSources(mergeSources, versionVector)

	return untombstoned, recreated, nil, diff, edits, nil
}

// forgetDetached takes a detached node out of NodeMapByID and its insertion
// chain, as its own GC pair's purge will, for a Restore that names it.
//
// A detached node is a tombstone in a subtree a purge took out, kept
// registered only until its own removal is collectable. A replica that has
// collected that far does not hold it, nor does one loaded from a snapshot.
// Those find nothing under the span's ID and recreate it if its parent is
// there, so this one has to resolve the span the same way; a DeepCopy,
// which carries the detached subtrees, resolves it as its source does. The node itself
// stays a detached tombstone (see TreeNode.detached). It leaves the document
// here as a purge would take it, so its pending GC pair goes too: Restore's
// caller drops it (see TakeForgotten), and the recreate that may follow
// under the same ID books its own.
func (t *Tree) forgetDetached(node *TreeNode) {
	t.unregisterNode(node)
	if node.MergedFrom != nil {
		t.forgetMergeStamp(node)
	}
	t.unlinkInsChain(node)
	t.forgotten = append(t.forgotten, node)
}

// TakeForgotten returns the detached nodes the last Restore forgot and
// clears the list; each Restore starts it afresh, so a caller that does not
// take it leaves nothing for a later one to act on. Internal to the CRDT
// engine, exported only for the operation that applies a restore: that
// caller drops their pending GC pairs (Root.DropGCPair) before it
// registers the pairs the same Restore booked: a recreate reuses the
// forgotten node's ID, and a pair left under that key would make the
// recreated node's registration cancel it instead of adding its own.
func (t *Tree) TakeForgotten() []*TreeNode {
	forgotten := t.forgotten
	t.forgotten = nil
	return forgotten
}

// knownRemovalAbove returns a test, for one Restore call, of whether a node
// sits under a removed ancestor whose removal the restoring change knew.
//
// Restore leaves such a node a tombstone. The restoring change saw its
// ancestor go, so a revive there would bring back a node no one can see,
// and whether it then came back with the ancestor would depend on when each
// replica collected: one that had already purged the ancestor leaves the
// node out with it, while one that had not keeps the node live until it
// collects (see GCLiveRelease) and shows it again if the ancestor is
// restored first. A restore concurrent with the ancestor's removal still
// revives, as before: every replica applies it before the minimum synced
// vector lets one purge the ancestor, so they all decide alike. An ancestor
// revived earlier in the same call is live, so restoring a subtree
// parent-first revives it whole.
// An empty vector is read as knowing every removal, as TicketKnown reads it:
// every change with operations carries its author's vector, so only a crafted
// one comes without, and failing open there would bring the GC-timing
// dependence back.
//
// Each node's nearest such ancestor is memoized and re-checked when read,
// since a revive in the call can end it; a node found to have none keeps
// none, since a revive never removes anything.
//
// The rule trusts the restoring change's version vector, as the §6.4
// dissolution does: a forged one claiming a concurrent removal makes the
// outcome depend on apply order, which needs server-side validation of
// pushed version vectors to close (#2234).
func (t *Tree) knownRemovalAbove(versionVector time.VersionVector) func(*TreeNode) bool {
	memo := make(map[*TreeNode]*TreeNode)
	// Iterative, as the depth is the client's to choose. Every node the
	// walk passes on the way up has the same nearest such ancestor: the
	// walk moves to a parent only when that parent's removal is not one the
	// change knew.
	nearest := func(n *TreeNode) *TreeNode {
		var chain []*TreeNode
		var found *TreeNode
		for current := n; ; {
			if f, ok := memo[current]; ok && (f == nil || f.IsRemoved()) {
				found = f
				break
			}
			chain = append(chain, current)
			if current.Index == nil || current.Index.Parent == nil {
				break
			}
			parent := current.Index.Parent.Value
			if parent.removedAt != nil && time.TicketKnown(versionVector, parent.removedAt) {
				found = parent
				break
			}
			current = parent
		}
		for _, c := range chain {
			memo[c] = found
		}
		return found
	}
	return func(n *TreeNode) bool { return nearest(n) != nil }
}

// isolateTextRange splits piece so that a node exactly covering the absolute
// offset interval [from, to) of its insertion exists, and returns it. Splitting
// at the caller's boundaries — rather than skipping a straddler — is what lets
// concurrent restores converge on identical text-node segmentation across
// replicas (the tree analogue of RGATreeSplit.isolateRange). A live split's
// metadata overhead is added to diff; a removed split buffers a pending GC pair
// internally (zero here). Requires pieceStart <= from < to <= pieceEnd. Mirrors
// the JS CRDTTree.isolateTextRange.
func (t *Tree) isolateTextRange(
	piece *TreeNode, from, to int, diff *resource.DataSize,
) (*TreeNode, error) {
	node := piece
	if from > node.id.Offset {
		d, err := node.Split(t, from-node.id.Offset, nil, nil)
		if err != nil {
			return nil, err
		}
		diff.Add(d)
		// Split's right half is registered under offset `from`; pick it up.
		node = t.findFloorNode(&TreeNodeID{CreatedAt: node.id.CreatedAt, Offset: from})
		if node == nil {
			return nil, ErrNodeNotFound
		}
	}
	if to < node.id.Offset+node.Length() {
		d, err := node.Split(t, to-node.id.Offset, nil, nil)
		if err != nil {
			return nil, err
		}
		diff.Add(d)
	}
	return node, nil
}

// Retombstone re-removes the nodes described by spans (redo of an
// identity-preserving undo). Live pieces only; idempotent. A piece that
// straddles a span boundary is split at that boundary so only the in-span range
// is re-removed (symmetric with Restore's isolate, so undo/redo stay mirror
// images and segmentation stays convergent). A span whose kind does not agree
// with itself is skipped, exactly as Restore skips it, so neither direction
// acts on what the other ignores. Returns the GC pairs for the newly
// tombstoned nodes, the live-split overhead, and, when measure is set, one
// deletion per visible node it removed, each measured right before the removal
// so they apply one after another (see TreeVisibleEdit). Mirrors the JS
// CRDTTree.retombstone.
//
// It also returns the spans describing everything it removed, for the reverse
// operation the caller builds: one entry per input span, the input itself or
// a copy with refreshed anchors (see refreshSpanAnchors), followed by one
// freshly captured span per node the §4.1 cascade took with its element. The
// inputs themselves are never modified.
func (t *Tree) Retombstone(
	spans []*TreeRestoreSpan, executedAt *time.Ticket, measure bool, versionVector time.VersionVector,
) ([]GCPair, resource.DataSize, []TreeVisibleEdit, []*TreeRestoreSpan, error) {
	return t.retombstone(spans, executedAt, measure, versionVector, nil)
}

// retombstone is Retombstone, also counting into steps, when given, the
// split siblings its §4.1 cascades walk.
func (t *Tree) retombstone(
	spans []*TreeRestoreSpan, executedAt *time.Ticket, measure bool, versionVector time.VersionVector,
	steps *int,
) ([]GCPair, resource.DataSize, []TreeVisibleEdit, []*TreeRestoreSpan, error) {
	var pairs []GCPair
	var diff resource.DataSize
	var edits []TreeVisibleEdit
	applied := make([]*TreeRestoreSpan, 0, len(spans))
	var cascaded []*TreeRestoreSpan
	// The split siblings the §4.1 cascade has walked in this call. A later
	// span's cascade stops where an earlier one already went, so the call
	// walks each chain once however many of its elements the spans name.
	walked := make(map[*TreeNode]struct{})
	for _, span := range spans {
		if !span.kindAgrees() {
			// Skipped, but reported as a copy like every other span, so the
			// reverse operation never shares the operation's payload.
			applied = append(applied, span.copy())
			continue
		}
		start := span.ID.Offset
		length := max(span.Length, 1)
		end := start + length

		// A text span names exactly its Length, as Restore reads it: an
		// empty one removes nothing, where reading it as one character would
		// remove a character its reverse, which reports Length 0, could not
		// bring back.
		var pieces []*TreeNode
		if span.IsText && span.Length <= 0 {
			pieces = nil
		} else if span.IsText {
			pieces = t.findPiecesOverlapping(span.ID.CreatedAt, start, end)
		} else if n := t.findFloorNode(span.ID); n != nil && n.id.Equal(span.ID) {
			pieces = []*TreeNode{n}
		}

		for _, piece := range pieces {
			// A detached node is a tombstone already and out of the tree
			// (see TreeNode.detached). What it links to is not what a
			// replica that purged one level deeper would see, so it starts
			// no cascade, as Restore leaves it alone.
			if piece.detached {
				continue
			}
			if piece.IsRemoved() {
				// The §4.1 cascade still runs for an element a peer already
				// removed, as the forward delete's does: whether it reaches
				// the unknown split products must not depend on whether the
				// peer's delete arrived first.
				pairs, edits, cascaded = t.retombstoneUnknownSplitSiblings(
					piece, executedAt, measure, versionVector, walked, steps, pairs, edits, cascaded,
				)
				continue
			}
			target := piece
			if piece.IsText() {
				from := max(piece.id.Offset, start)
				to := min(piece.id.Offset+piece.Length(), end)
				var iErr error
				target, iErr = t.isolateTextRange(piece, from, to, &diff)
				if iErr != nil {
					return nil, resource.DataSize{}, nil, nil, iErr
				}
			}
			// Measure while target is still visible.
			var from, to int
			visible := false
			if measure {
				from, to, visible = t.visibleRangeOf(target)
			}
			if target.remove(executedAt) {
				pairs = append(pairs, GCPair{Parent: t, Child: target})
				if visible {
					edits = append(edits, TreeVisibleEdit{From: from, To: to})
				}
			}
			pairs, edits, cascaded = t.retombstoneUnknownSplitSiblings(
				target, executedAt, measure, versionVector, walked, steps, pairs, edits, cascaded,
			)
		}
		// Refreshed whether or not anything was live: a span whose pieces a
		// peer already removed sits next to whatever is beside those
		// tombstones now, just as one this call removed does (#2169).
		applied = append(applied, t.refreshSpanAnchors(span))
	}
	return pairs, diff, edits, appendUncoveredSpans(applied, cascaded), nil
}

// appendUncoveredSpans merges the spans the §4.1 cascade captured into the
// input spans: every cascaded span, or the part of it, that names what the
// input spans do not already describe -- an element by its ID, a text range
// by the text spans of the same insertion under the same parent. The input
// spans of a delete routinely include a split product together with the
// element it was cut off, and the cascade from the element reaches the
// product before its own span does; without this the reverse operation
// would carry the product twice, and one more copy on every undo/redo that
// toggles it.
//
// A cascaded text piece under a split product can overlap an input span of
// its insertion recorded under the original element. The merged list puts
// every input element first, then the kept cascaded spans, then the input
// text spans, so that a Restore after GC recreates the product's piece under
// the product before the input span reaches that range, finds it there, and
// recreates only the rest under the original element. parentsFirst then
// restores parent-before-child order where the reordering broke it, keeping
// that precedence.
//
// Coverage is looked up in sorted, merged intervals per (insertion, parent),
// so the merge costs O(N log N) however the client sized the span list.
func appendUncoveredSpans(spans, cascaded []*TreeRestoreSpan) []*TreeRestoreSpan {
	if len(cascaded) == 0 {
		return spans
	}

	elements := make(map[string]struct{}, len(spans))
	covers := make(map[string][][2]int)
	var inputElements, inputTexts []*TreeRestoreSpan
	for _, span := range spans {
		// A span whose kind disagrees was skipped, and Restore skips it
		// too, so it describes nothing a cascaded span could leave to it.
		describes := span.kindAgrees()
		if span.IsText {
			inputTexts = append(inputTexts, span)
			if describes && span.Length > 0 {
				key := textCoverKey(span)
				covers[key] = append(covers[key], [2]int{span.ID.Offset, span.ID.Offset + span.Length})
			}
		} else {
			inputElements = append(inputElements, span)
			if describes {
				elements[span.ID.toIDString()] = struct{}{}
			}
		}
	}
	for key, intervals := range covers {
		covers[key] = mergeIntervals(intervals)
	}

	var kept []*TreeRestoreSpan
	for _, span := range cascaded {
		if !span.IsText {
			if _, ok := elements[span.ID.toIDString()]; !ok {
				kept = append(kept, span)
			}
			continue
		}
		kept = append(kept, uncoveredTextSpans(span, covers[textCoverKey(span)])...)
	}

	merged := make([]*TreeRestoreSpan, 0, len(spans)+len(kept))
	merged = append(merged, inputElements...)
	merged = append(merged, kept...)
	inputTextsFrom := len(merged)
	merged = append(merged, inputTexts...)
	return parentsFirst(merged, inputTextsFrom)
}

// mergeIntervals sorts half-open intervals and merges the ones that overlap
// or touch.
func mergeIntervals(intervals [][2]int) [][2]int {
	slices.SortFunc(intervals, func(a, b [2]int) int { return a[0] - b[0] })
	merged := intervals[:0]
	for _, iv := range intervals {
		if n := len(merged); n > 0 && iv[0] <= merged[n-1][1] {
			merged[n-1][1] = max(merged[n-1][1], iv[1])
			continue
		}
		merged = append(merged, iv)
	}
	return merged
}

// parentsFirst returns spans in parent-before-child order, as Restore needs
// them: once GC has purged the nodes, each is recreated under a parent an
// earlier span brought back, and one whose parent is not there yet is
// declined. Leaving out a cascaded element an input span already names can
// put the input span of one of its descendants ahead of the cascaded spans
// it hangs under. Spans already in order are returned as they are;
// otherwise they are stably sorted by how many of their ancestors the list
// itself holds, which keeps the order of every span against its siblings.
//
// The spans from inputTextsFrom on are input text spans, which have to stay
// behind every earlier text span of the same insertion (see
// appendUncoveredSpans), however deep that one sits. So an input text span
// sorts at the depth of the deepest of those at least, and the stable sort
// keeps it after them on a tie, as the list has it. The input text spans
// also keep their own order, as a later one can anchor on an earlier one:
// each sorts at least as deep as every input text span before it. A text
// span is no span's parent, so moving it later keeps every parent ahead of
// its children.
func parentsFirst(spans []*TreeRestoreSpan, inputTextsFrom int) []*TreeRestoreSpan {
	position := make(map[string]int, len(spans))
	for i, span := range spans {
		if !span.IsText {
			position[span.ID.toIDString()] = i
		}
	}
	parentOf := func(span *TreeRestoreSpan) (int, bool) {
		if span.ParentID == nil {
			return 0, false
		}
		i, ok := position[span.ParentID.toIDString()]
		return i, ok
	}

	inOrder := true
	for i, span := range spans {
		if j, ok := parentOf(span); ok && j > i {
			inOrder = false
			break
		}
	}
	if inOrder {
		return spans
	}

	// Each span's depth is computed once and reused by its descendants, so
	// the whole list costs O(n). A crafted span set can name its parents in
	// a cycle; a span reached again while its own depth is pending ends the
	// chain there, at depth 0.
	const (
		unvisited = iota
		pending
		done
	)
	depth := make([]int, len(spans))
	state := make([]int, len(spans))
	var chain []int
	for i := range spans {
		chain = chain[:0]
		base := 0
		for current := i; ; {
			if state[current] == done {
				base = depth[current] + 1
				break
			}
			if state[current] == pending {
				break
			}
			state[current] = pending
			chain = append(chain, current)
			j, ok := parentOf(spans[current])
			if !ok {
				break
			}
			current = j
		}
		for k := len(chain) - 1; k >= 0; k-- {
			depth[chain[k]] = base
			state[chain[k]] = done
			base++
		}
	}

	deepest := make(map[string]int)
	for i, span := range spans[:inputTextsFrom] {
		if span.IsText {
			key := span.ID.CreatedAt.Key()
			if d, ok := deepest[key]; !ok || depth[i] > d {
				deepest[key] = depth[i]
			}
		}
	}
	floor := 0
	for i := inputTextsFrom; i < len(spans); i++ {
		if d, ok := deepest[spans[i].ID.CreatedAt.Key()]; ok && d > depth[i] {
			depth[i] = d
		}
		floor = max(floor, depth[i])
		depth[i] = floor
	}

	order := make([]int, len(spans))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return depth[a] - depth[b] })
	sorted := make([]*TreeRestoreSpan, len(spans))
	for i, j := range order {
		sorted[i] = spans[j]
	}
	return sorted
}

// textCoverKey groups text spans that can cover one another: the same
// insertion under the same parent. A piece of the insertion that a split
// carried under its product is not what an input span recorded under the
// original element describes, though their offsets overlap: Restore would
// bring that span back under the original element and leave the product's
// piece removed.
func textCoverKey(span *TreeRestoreSpan) string {
	parent := ""
	if span.ParentID != nil {
		parent = span.ParentID.toIDString()
	}
	return span.ID.CreatedAt.Key() + "|" + parent
}

// uncoveredTextSpans returns the parts of the text span that none of covers,
// sorted and merged intervals, holds, each as a span of its own. A part
// keeps the span's left anchor only if it starts where the span does, and
// its right anchor only if it ends there too; between parts, the anchors are
// the covered characters of the same insertion on either side. A Restore
// after GC recreates the parts before the covering spans, so a part past
// the first places itself after the part before it (the floor of the
// character in front of its gap), not after the span's left neighbour,
// which would put it ahead of that part.
func uncoveredTextSpans(span *TreeRestoreSpan, covers [][2]int) []*TreeRestoreSpan {
	// An empty span covers no range for another span to cover; it is kept as
	// it is rather than dropped from the reverse operation.
	if span.Length <= 0 {
		return []*TreeRestoreSpan{span}
	}
	start, end := span.ID.Offset, span.ID.Offset+span.Length

	var gaps [][2]int
	cursor := start
	// The first cover that ends past start: covers are disjoint and sorted,
	// so their ends are sorted too.
	first, _ := slices.BinarySearchFunc(covers, start, func(iv [2]int, at int) int {
		if iv[1] <= at {
			return -1
		}
		return 1
	})
	for i := first; i < len(covers) && covers[i][0] < end; i++ {
		if covers[i][0] > cursor {
			gaps = append(gaps, [2]int{cursor, covers[i][0]})
		}
		cursor = max(cursor, covers[i][1])
	}
	if cursor < end {
		gaps = append(gaps, [2]int{cursor, end})
	}

	if len(gaps) == 1 && gaps[0] == [2]int{start, end} {
		return []*TreeRestoreSpan{span}
	}
	// Value is sliced on its own bounds too, should it carry fewer code
	// units than its Length claims, and each part's Length is what its
	// Value holds: the wire decoder rejects a text span whose Length is not
	// the UTF-16 length of its Value, so a reverse operation carrying one
	// could not be decoded. A part left with nothing describes nothing.
	encoded := utf16.Encode([]rune(span.Value))
	parts := make([]*TreeRestoreSpan, 0, len(gaps))
	for _, gap := range gaps {
		from, to := min(gap[0]-start, len(encoded)), min(gap[1]-start, len(encoded))
		if from >= to {
			continue
		}
		part := *span.copy()
		part.ID = &TreeNodeID{CreatedAt: span.ID.CreatedAt, Offset: gap[0]}
		part.Length = to - from
		part.Value = string(utf16.Decode(encoded[from:to]))
		if gap[0] > start {
			part.LeftSiblingID = &TreeNodeID{CreatedAt: span.ID.CreatedAt, Offset: gap[0] - 1}
		}
		if gap[1] < end {
			part.RightSiblingID = &TreeNodeID{CreatedAt: span.ID.CreatedAt, Offset: gap[1]}
		}
		parts = append(parts, &part)
	}
	return parts
}

// retombstoneUnknownSplitSiblings applies the §4.1 cascade to an element
// Retombstone just removed: the split products a concurrent split cut off it,
// which the acting change did not know, go with it, as they do when an
// ordinary delete removes the element. Without it a replica that applied the
// concurrent split before the retombstone keeps the product live while one
// that applied it after splits an already removed element and gets a removed
// product.
//
// Every sibling it actually tombstones is appended to cascaded as its own
// restore span, captured right before the removal. The forward delete path
// records a span for every node in toBeRemoveds, and this cascade owes the
// same: the spans the acting operation carries describe only the element, so
// without an entry of its own a cascaded sibling would be removed by the redo
// and left tombstoned by the matching undo.
//
// Unlike the forward delete's cascade, it needs no merge-boundary exclusion:
// that one keeps the split siblings of a node whose children the same edit
// moves by a merge, and an edit that merges records no identity spans
// (TreeEditReverseInfo.SpansComplete), so no span a retombstone acts on ever
// names such a node.
//
// The spans go out parent before child, as Restore requires: once GC has
// purged the cascade, the undo recreates each node under a parent an earlier
// span brought back, and declines one whose parent is not there yet.
// appendUnknownSplitSiblings lists each sibling's subtree in postorder, so
// the spans are collected per node and emitted by a preorder walk of every
// sibling it returned.
func (t *Tree) retombstoneUnknownSplitSiblings(
	target *TreeNode,
	executedAt *time.Ticket,
	measure bool,
	versionVector time.VersionVector,
	walked map[*TreeNode]struct{},
	steps *int,
	pairs []GCPair,
	edits []TreeVisibleEdit,
	cascaded []*TreeRestoreSpan,
) ([]GCPair, []TreeVisibleEdit, []*TreeRestoreSpan) {
	if target.IsText() || target.InsNextID == nil {
		return pairs, edits, cascaded
	}

	neverEnclosed := func(*TreeNode) bool { return false }
	siblings := t.appendUnknownSplitSiblings(nil, target, versionVector, neverEnclosed, walked, steps)
	spans := make(map[*TreeNode]*TreeRestoreSpan, len(siblings))
	for _, sibling := range siblings {
		var from, to int
		visible := false
		if measure {
			from, to, visible = t.visibleRangeOf(sibling)
		}
		span := restoreSpanOf(sibling)
		if sibling.remove(executedAt) {
			pairs = append(pairs, GCPair{Parent: t, Child: sibling})
			spans[sibling] = span
			if visible {
				edits = append(edits, TreeVisibleEdit{From: from, To: to})
			}
		}
	}

	// siblings lists every product followed by its subtree, parent before
	// child, so one pass over it emits the spans in that order. A later link
	// of the chain can sit inside an earlier product's subtree and so appear
	// twice; its span is taken out once emitted, so each node is reported
	// once.
	for _, sibling := range siblings {
		if steps != nil {
			*steps++
		}
		if span, ok := spans[sibling]; ok {
			cascaded = append(cascaded, span)
			delete(spans, sibling)
		}
	}
	return pairs, edits, cascaded
}

// refreshSpanAnchors returns a span like the given one whose parent and
// sibling anchors point at the neighbours the span's range has now, right
// after Retombstone removed it. The anchors were captured when the span was
// first recorded; content a peer inserted next to the range since then is
// what the range sits beside now, and a later Restore that has to recreate it
// after GC must land it there, not at the stale anchor (#2169). A removal
// leaves every node in place as a tombstone, so reading the neighbours after
// it reads what was there before it.
//
// Each anchor is read off the piece that bounds the range on its side, live
// or not: the left one off the first piece, the right one off the last. A
// piece a peer had already removed is skipped by the removal but still
// bounds the range, and anchoring on the first piece the removal touched
// instead would name a neighbour inside the range. A removed piece that
// straddles a boundary is not split here. At the start, its own last
// character before the range is the left neighbour; at the end, the piece
// starts inside the span, so there is no right anchor, which never names a
// node that starts inside the range. A purged end leaves the neighbour of
// the nearest surviving piece, which is what now sits beside the gap. A text
// span is read at exactly its Length, so an empty one keeps its anchors.
//
// The three anchors are always replaced together, under the first piece's
// parent (see anchorsOf): recreateFromSpan honours a sibling anchor only
// under the span's ParentID and tries the left one first, so a fresh parent
// beside a stale left anchor would land the range at the stale place. A
// right anchor under another parent -- the span was split across elements --
// is dropped rather than paired with the wrong one.
//
// It never writes through the pointer it is given, and returns a copy on
// every path. The spans Retombstone
// receives are the TreeEdit operation's own payload — the same objects
// to_pb serializes at push time, the clone and the root execution both run,
// and the reverse operation shares — so a refresh written in place would
// retroactively rewrite an unpushed change with anchors derived from local
// state. Only the copy reaches the reverse operation this execution builds.
func (t *Tree) refreshSpanAnchors(span *TreeRestoreSpan) *TreeRestoreSpan {
	// Always a copy, refreshed or not, so the reverse operation never shares
	// a span with the operation's own payload.
	fresh := *span.copy()

	var first, last *TreeNode
	start, end := span.ID.Offset, span.ID.Offset+span.Length
	if span.IsText {
		// A text span names exactly its Length; an empty one has no piece
		// to read anchors off.
		if span.Length <= 0 {
			return &fresh
		}
		pieces := t.findPiecesOverlapping(span.ID.CreatedAt, start, end)
		if len(pieces) == 0 {
			return &fresh
		}
		first, last = pieces[0], pieces[len(pieces)-1]
	} else if n := t.findFloorNode(span.ID); n != nil && n.id.Equal(span.ID) {
		first, last = n, n
	} else {
		return &fresh
	}

	// A detached node's neighbours are off the tree: the span keeps the
	// anchors it came with.
	if first.detached || last.detached {
		return &fresh
	}
	parentID, leftID, _, ok := anchorsOf(first)
	if !ok {
		return &fresh
	}
	if span.IsText && first.id.Offset < start {
		leftID = NewTreeNodeID(span.ID.CreatedAt, start-1)
	}

	// A removed piece that straddles the span's end starts inside the span,
	// so it is no right anchor: it is left out, and the left anchor and the
	// same-insertion rung of recreateFromSpan place the range.
	var rightID *TreeNodeID
	if lastParentID, _, lastRightID, ok := anchorsOf(last); ok && lastParentID.Equal(parentID) &&
		(!span.IsText || last.id.Offset+last.Length() <= end) {
		rightID = lastRightID
	}

	fresh.ParentID, fresh.LeftSiblingID, fresh.RightSiblingID = parentID, leftID, rightID
	return &fresh
}

// visibleRangeOf returns the visible-index range [from, to) node covers, or
// ok=false when it or one of its ancestors is removed and so takes no room in
// the index. Mirrors the JS CRDTTree.visibleRangeOf.
//
// A tree the index cannot walk degrades to ok=false rather than an error. The
// caller runs inside TreeEdit.Execute after the CRDT state has settled, and
// for a remote change the spans came off the wire: failing there would abort
// the pack and wedge the document on every replica that receives it, while a
// missed range only leaves a stacked undo/redo entry unshifted. JS makes the
// same trade (it logs and reports nothing).
func (t *Tree) visibleRangeOf(node *TreeNode) (from, to int, ok bool) {
	if node.Index.Parent == nil {
		return 0, 0, false
	}
	for n := node.Index; n != nil; n = n.Parent {
		if n.Value.IsRemoved() {
			return 0, 0, false
		}
	}

	to, err := t.ToIndex(node.Index.Parent.Value, node)
	if err != nil || to < 0 {
		return 0, 0, false
	}
	return to - node.Index.PaddedLength(), to, true
}

// findPiecesOverlapping collects surviving pieces (live or tombstoned) of the
// text insertion createdAt overlapping [start, end), ascending, via descending
// floor probes. Mirrors the JS CRDTTree.findPiecesOverlapping.
func (t *Tree) findPiecesOverlapping(createdAt *time.Ticket, start, end int) []*TreeNode {
	var pieces []*TreeNode
	probe := end - 1
	for probe >= 0 {
		node := t.findFloorNode(&TreeNodeID{CreatedAt: createdAt, Offset: probe})
		if node == nil || !node.IsText() {
			break
		}
		nodeStart := node.id.Offset
		nodeEnd := nodeStart + node.Length()
		if nodeEnd <= start {
			break
		}
		if nodeStart < end && nodeEnd > start {
			pieces = append(pieces, node)
		}
		if nodeStart <= start {
			break
		}
		probe = nodeStart - 1
	}
	for i, j := 0, len(pieces)-1; i < j; i, j = i+1, j-1 {
		pieces[i], pieces[j] = pieces[j], pieces[i]
	}
	return pieces
}

// recreateParent resolves the node a recreated span hangs under, or nil when
// the span must be left unplaced (B1). Three shapes are unplaceable. The
// parent is genuinely absent: purged and not part of this restore, or
// detached with an ancestor a purge took out, which is the same tree a
// replica that purged it too holds (see TreeNode.detached); a node recreated
// there would be charged to GC for bytes no rebuild holds. The parent is a
// text node, which cannot hold children. Or the identity being recreated,
// id, is one a surviving node already holds -- exactly, or inside a surviving
// text run of the same insertion -- in which case recreating would put a
// second node under one identity, and putNode overwrites the live
// registration of the node that holds it, leaving every later change that
// addresses it unapplicable (#2168).
//
// No honest span is any of these: spans come from the wire, and the server
// stores a change before applying it, so failing here would fail every later
// rebuild of the document. Each test reads only state that never changes
// afterwards (node kind, identity ownership), and a replica that does hold the
// named node resolves the span through Restore's un-tombstone branch instead,
// so the replicas converge either way.
func (t *Tree) recreateParent(span *TreeRestoreSpan, id *TreeNodeID) *TreeNode {
	if span.ParentID == nil {
		return nil
	}
	parent := t.findFloorNode(span.ParentID)
	if parent == nil || !parent.id.Equal(span.ParentID) || parent.IsText() || parent.detached {
		return nil
	}

	if holder := t.findFloorNode(id); holder != nil &&
		(holder.id.Offset == id.Offset ||
			(holder.IsText() && holder.id.Offset+holder.Length() > id.Offset)) {
		return nil
	}

	return parent
}

// relinkRecreatedText splices a recreated text piece back into the
// InsPrevID/InsNextID chain of its insertion. Purge unlinked the piece and
// joined its neighbours to each other; a recreated piece that stays out of
// the chain is invisible to the walks that follow it, and the next split of
// a neighbour hands the right half an InsNextID that skips it.
//
// The splice goes between the nearest piece of the insertion that SURVIVES
// before this one and whatever that piece currently points forward at, not
// between the pieces that are exactly adjacent by offset. After a purge the
// chain's neighbours are the nearest survivors — Purge joined them to each
// other — and the exactly-adjacent ones may be the pieces that are gone.
// Both ends of the splice are rewritten together: linking only the one that
// happened to be exactly adjacent would leave the chain asymmetric, with the
// predecessor pointing at the recreated piece while the piece beyond the gap
// still claimed the predecessor as its own, which is worse than no link.
func (t *Tree) relinkRecreatedText(node *TreeNode) {
	start := node.id.Offset
	end := start + node.Len()

	pred := t.survivingInsPrev(node, start)
	if pred == nil {
		// Nothing of the insertion survives in front of this piece, so there
		// is no chain position to splice into. The successor is still linked
		// if it is exactly adjacent and has no surviving predecessor of its
		// own -- none at all, or one that names a piece no longer registered
		// -- and both ends of that link are written here.
		succ := t.findFloorNode(&TreeNodeID{CreatedAt: node.id.CreatedAt, Offset: end})
		if succ != nil && succ != node && succ.IsText() && succ.detached == node.detached &&
			succ.id.Offset == end && !t.registersExactly(succ.InsPrevID) {
			node.InsNextID = succ.id
			succ.InsPrevID = node.id
		}
		return
	}

	succID := pred.InsNextID
	// Pass over detached successors, which an attached piece does not link
	// to, as a replica that purged them joined their neighbours.
	if !node.detached {
		var walker insNextWalker
		for succID != nil {
			succ := t.findFloorNode(succID)
			if succ == nil || !succ.id.Equal(succID) || !succ.detached || !walker.visit(succ) {
				break
			}
			succID = succ.InsNextID
		}
	}
	// A predecessor still pointing at this ID names an earlier node under
	// it, gone now; linking to it would point the node at itself.
	if succID != nil && succID.Equal(node.id) {
		succID = nil
	}
	node.InsPrevID = pred.id
	node.InsNextID = succID
	pred.InsNextID = node.id
	if succID != nil {
		if succ := t.findFloorNode(succID); succ != nil && succ != node && succ.id.Equal(succID) {
			succ.InsPrevID = node.id
		}
	}
}

// registersExactly reports whether a node is registered under exactly id.
func (t *Tree) registersExactly(id *TreeNodeID) bool {
	if id == nil {
		return false
	}
	node := t.findFloorNode(id)
	return node != nil && node.id.Equal(id)
}

// survivingInsPrev returns the nearest piece of node's insertion that is still
// registered in front of offset start, or nil when none is. It is the chain
// position a recreated piece splices in after: a purge of the pieces between
// moves it further back, but never past the start of the insertion, since
// findFloorNode only answers with a node of the same CreatedAt.
func (t *Tree) survivingInsPrev(node *TreeNode, start int) *TreeNode {
	if start <= 0 {
		return nil
	}

	pred := t.findFloorNode(&TreeNodeID{CreatedAt: node.id.CreatedAt, Offset: start - 1})
	// A detached piece is not part of the attached tree's chain: an attached
	// piece looks past it, as on a replica that purged it (see
	// TreeNode.detached).
	for pred != nil && pred != node && pred.detached && !node.detached && pred.id.Offset > 0 {
		pred = t.findFloorNode(&TreeNodeID{CreatedAt: node.id.CreatedAt, Offset: pred.id.Offset - 1})
	}
	if pred == nil || pred == node || !pred.IsText() || pred.id.Offset+pred.Len() > start ||
		(pred.detached && !node.detached) {
		return nil
	}

	return pred
}

// recreateFromSpan rebuilds a purged node (or purged text sub-range) under its
// original identity and attaches it via the anchor ladder (floor-lookup +
// parent-identity check at each rung): (a) same-insertion successor/
// predecessor piece → exact slot; (b) captured left boundary sibling → after
// it; (c) captured right boundary sibling → before it; (d) deterministic
// id-order fallback (first slot whose child id > node id — a pure function of
// ids, identical on every replica). A span recreateParent refuses is skipped
// (B1). The JS CRDTTree.recreateFromSpan must apply the same rules
// (yorkie-js-sdk#1471).
//
// versionVector is that of the restoring change. Rung (a) reads it for a
// successor that a split the change did not know moved out of the parent.
func (t *Tree) recreateFromSpan(
	span *TreeRestoreSpan, offset, length int, versionVector time.VersionVector,
) (*TreeNode, error) {
	id := &TreeNodeID{CreatedAt: span.ID.CreatedAt, Offset: offset}
	parent := t.recreateParent(span, id)
	if parent == nil {
		return nil, nil
	}

	var node *TreeNode
	var recreatedAttrPairs []GCPair
	if span.IsText {
		encoded := utf16.Encode([]rune(span.Value))
		relStart := offset - span.ID.Offset
		val := string(utf16.Decode(encoded[relStart : relStart+length]))
		node = NewTreeNode(id, span.NodeType, nil, val)
	} else {
		var attrs *RHT
		if span.Attributes != nil {
			attrs = span.Attributes.DeepCopy()
		}
		node = NewTreeNode(span.ID, span.NodeType, attrs)

		// The span's attributes are a deep copy of the node's RHT, tombstones
		// included -- they have to be, or a recreated node would resolve a
		// concurrent style differently from a replica that never lost it. Each
		// copied tombstone is a fresh piece of garbage that no removal path
		// produced: without a registration it sits in the RHT forever,
		// uncounted and unpurgeable, and TreeNode.DataSize excludes it so the
		// node's own charge does not cover it either. GCPairs marks each
		// GCOnlySize, which is what sends it to GC alone.
		//
		// This runs for BOTH parents: a live one, where the node is reported
		// as recreated, and a removed one, where it is born tombstoned below.
		// Buffered by attach, once the insertion has actually succeeded --
		// a failed insert drops the node, and its pairs must go with it.
		recreatedAttrPairs = node.GCPairs()
	}

	siblings := parent.Children(true)
	childIndex := func(target *TreeNode) int {
		for i, c := range siblings {
			if c == target {
				return i
			}
		}
		return len(siblings)
	}
	sameParent := func(n *TreeNode) bool {
		return n.Index.Parent != nil && n.Index.Parent.Value == parent
	}

	// attach inserts node at the resolved slot and registers it. A failed
	// insertion must not leave a half-attached node in NodeMapByID, so the
	// error is returned before Put and Restore drops the node entirely.
	attach := func(insert func() error) (*TreeNode, error) {
		if err := insert(); err != nil {
			return nil, err
		}
		t.putNode(node)
		if span.IsText {
			t.relinkRecreatedText(node)
		}
		t.pendingGCPairs = append(t.pendingGCPairs, recreatedAttrPairs...)
		// The parent has been tombstoned since this node was purged, so the
		// node is born tombstoned rather than live. This mirrors the
		// convention the concurrent-insert path already states ("if
		// insertion happens during concurrent editing and parent node has
		// been removed, make new nodes as tombstone immediately").
		// Recreating it live would leave a node that the next Purge of the
		// parent unlinks but never unregisters -- reachable from nothing,
		// still in NodeMapByID -- which is what sends toTreePos off the top
		// of a detached subtree (#2008).
		//
		// The stamp is the PARENT's removedAt, not executedAt: that is the
		// ticket the removal already wrote onto every sibling it swept, so
		// the node rejoins them carrying what it would have carried had it
		// never been purged, and it agrees with a replica that recreated it
		// live and then had the removal sweep it. Stamping executedAt makes
		// those two disagree, and removedAt feeds canDelete, so they would
		// then collect on different passes. The stamp only has to be
		// overwritable by the eventual LWW winner, and it is: a later
		// concurrent removal with a higher ticket reaches the tombstoned
		// node and overwrites it.
		if home := node.Index.Parent.Value; home.IsRemoved() {
			node.remove(home.RemovedAt())
			// Born tombstoned: it is not reported as recreated, so the caller
			// never accounts it to Live. GCOnlySize is how a pair says "add to
			// GC, take nothing out of Live" -- see Root.RegisterGCPair.
			gcSize := node.DataSize()
			t.pendingGCPairs = append(t.pendingGCPairs, GCPair{
				Parent:     t,
				Child:      node,
				GCOnlySize: &gcSize,
			})
			return nil, nil
		}
		return node, nil
	}

	// (a) same-insertion successor / predecessor piece (text): exact slot.
	if span.IsText {
		succ := t.findFloorNode(&TreeNodeID{CreatedAt: span.ID.CreatedAt, Offset: offset + length})
		if succ != nil && succ.IsText() && succ.id.Offset == offset+length {
			if sameParent(succ) {
				return attach(func() error { return parent.InsertAt(node, childIndex(succ)) })
			}
			if home := t.concurrentSplitHome(succ, parent, versionVector); home != nil {
				return attach(func() error {
					idx := slices.Index(home.Children(true), succ)
					if idx < 0 {
						idx = len(home.Children(true))
					}
					return home.InsertAt(node, idx)
				})
			}
		}
		if offset > span.ID.Offset || offset > 0 {
			pred := t.findFloorNode(&TreeNodeID{CreatedAt: span.ID.CreatedAt, Offset: offset - 1})
			if pred != nil && pred.IsText() && sameParent(pred) {
				return attach(func() error { return parent.InsertAfter(node, pred) })
			}
		}
	}

	// (b) captured left boundary sibling, if it still exists under this parent.
	if span.LeftSiblingID != nil {
		if left := t.findFloorNode(span.LeftSiblingID); left != nil && sameParent(left) {
			return attach(func() error { return parent.InsertAfter(node, left) })
		}
	}

	// (c) captured right boundary sibling (redundant anchor): insert before it.
	if span.RightSiblingID != nil {
		if right := t.findFloorNode(span.RightSiblingID); right != nil && sameParent(right) {
			return attach(func() error { return parent.InsertAt(node, childIndex(right)) })
		}
	}

	// (d) deterministic id-order fallback: first slot whose child id > node id.
	insertIdx := len(siblings)
	for i, c := range siblings {
		if c.id.Compare(node.id) > 0 {
			insertIdx = i
			break
		}
	}
	return attach(func() error { return parent.InsertAt(node, insertIdx) })
}

// concurrentSplitHome returns the element succ, the same-insertion successor
// of a text range being recreated, now sits under when splits the restoring
// change did not know carried it there out of parent; nil otherwise.
//
// Had the range survived as a tombstone, such a split would have carried it
// along too: it sat between the split point, which the successor is past, and
// the successor. A replica that applies the split after this recreate does
// exactly that, so the range goes before the successor here as well, wherever
// its predecessor stayed. Every split between parent and the successor's
// element has to be one the change did not know. A split it knew happened
// before the change recorded the span under parent, so the range was left of
// it and the successor had already been carried off; that is left to the
// other rungs.
func (t *Tree) concurrentSplitHome(succ, parent *TreeNode, versionVector time.VersionVector) *TreeNode {
	if succ.Index.Parent == nil {
		return nil
	}

	// A detached home is out of the tree, as recreateParent refuses a
	// detached parent for (see TreeNode.detached).
	home := succ.Index.Parent.Value
	if home.IsText() || home == parent || home.detached {
		return nil
	}

	var walker insNextWalker
	walker.visit(home)
	for current := home; current != parent; {
		if time.TicketKnown(versionVector, current.id.CreatedAt) || current.InsPrevID == nil {
			return nil
		}
		prev := t.findFloorNode(current.InsPrevID)
		if prev == nil || prev.IsText() || !walker.visit(prev) {
			return nil
		}
		current = prev
	}

	return home
}

// MetaSize returns the size of the metadata of this element.
func (t *Tree) MetaSize() int {
	size := 0
	if t.createdAt != nil {
		size += time.TicketSize
	}
	if t.movedAt != nil {
		size += time.TicketSize
	}
	if t.removedAt != nil {
		size += time.TicketSize
	}
	return size
}

// DataSize returns the data usage of this element.
func (t *Tree) DataSize() resource.DataSize {
	dataSize := resource.DataSize{
		Data: 0,
		Meta: 0,
	}
	for _, node := range t.Nodes() {
		if node.IsRemoved() {
			continue
		}

		size := node.DataSize()
		dataSize.Data += size.Data
		dataSize.Meta += size.Meta
	}

	return resource.DataSize{
		Data: dataSize.Data,
		Meta: dataSize.Meta + t.MetaSize(),
	}
}

// marshal returns the JSON encoding of this Tree.
func marshal(builder *strings.Builder, node *TreeNode) {
	if node.IsText() {
		fmt.Fprintf(builder, `{"type":"%s","value":"%s"}`, node.Type(), EscapeString(node.Value))
		return
	}

	fmt.Fprintf(builder, `{"type":"%s","children":[`, node.Type())
	for idx, child := range node.Index.Children() {
		if idx != 0 {
			builder.WriteString(",")
		}
		marshal(builder, child.Value)
	}
	builder.WriteString(`]`)

	if node.Attrs != nil && node.Attrs.Len() > 0 {
		builder.WriteString(`,"attributes":`)
		builder.WriteString(node.Attrs.Marshal())
	}

	builder.WriteString(`}`)
}

// DeepCopy copies itself deeply.
func (t *Tree) DeepCopy() (Element, error) {
	node, err := t.Root().DeepCopy()
	if err != nil {
		return nil, err
	}

	tree := NewTree(node, t.createdAt)
	tree.movedAt = t.movedAt
	tree.removedAt = t.removedAt
	return tree, nil
}

// copyDetachedFrom gives the tree, a fresh copy of src's attached tree, the
// subtrees src's purges took out, for Root.DeepCopy. Tree.DeepCopy, which
// also makes the copies an element payload carries, keeps the attached tree
// alone, so a value never takes detached state along. Each subtree is
// copied whole, with the detached and released marks and the purge slots,
// and the nodes src has in detachedByID are registered there. A copy that runs a change before src does, as the
// document's clone does, then resolves every position src resolves through
// a detached node or a purge slot alike. A snapshot holds none of this.
func (t *Tree) copyDetachedFrom(src *Tree) error {
	registered := src.detachedByID.Values()
	if len(registered) == 0 {
		return nil
	}

	copies := make(map[*TreeNode]*TreeNode)
	// A subtree is copied whole, from the top a purge took out. The ones
	// holding a registered node come first; a slot in a copied subtree can
	// name a detached node in another, registered or not, so that one is
	// copied too, until every slot names a copied node or an attached one.
	var pending []*TreeNode
	enqueue := func(n *TreeNode) {
		if n == nil || !n.detached {
			return
		}
		top := n
		for top.Index.Parent != nil {
			top = top.Index.Parent.Value
		}
		if _, ok := copies[top]; !ok {
			copies[top] = nil
			pending = append(pending, top)
		}
	}
	for _, n := range registered {
		enqueue(n)
	}
	for len(pending) > 0 {
		top := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		copied, err := top.DeepCopy()
		if err != nil {
			return err
		}
		pairs := [][2]*index.Node[*TreeNode]{{top.Index, copied.Index}}
		for len(pairs) > 0 {
			pair := pairs[len(pairs)-1]
			pairs = pairs[:len(pairs)-1]
			from, to := pair[0].Value, pair[1].Value
			copies[from] = to
			to.detached, to.released = from.detached, from.released
			enqueue(from.purgedFrom)
			enqueue(from.purgedBefore)
			fromChildren, toChildren := pair[0].Children(true), pair[1].Children(true)
			for i := range fromChildren {
				pairs = append(pairs, [2]*index.Node[*TreeNode]{fromChildren[i], toChildren[i]})
			}
		}
	}

	// A slot names a node in either tree: its copy if it was detached, the
	// copied attached tree's node under the same ID otherwise.
	resolve := func(n *TreeNode) *TreeNode {
		if n == nil {
			return nil
		}
		if c, ok := copies[n]; ok && c != nil {
			return c
		}
		if key, attached := t.NodeMapByID.Floor(n.id); attached != nil && key.Equal(n.id) {
			return attached
		}
		return nil
	}
	for from, to := range copies {
		to.purgedFrom, to.purgedBefore = resolve(from.purgedFrom), resolve(from.purgedBefore)
	}
	for _, n := range registered {
		if c, ok := copies[n]; ok {
			t.detachedByID.Put(c.id, c)
			t.noteMergeStamp(c)
		}
	}
	return nil
}

// GCPairs returns the pairs of GC.
func (t *Tree) GCPairs() []GCPair {
	var pairs []GCPair

	for _, node := range t.Nodes() {
		// t.Nodes() walks from the index root down, so the ROOT node itself is
		// in the list. Purge detaches a node from its parent, and the root has
		// none -- booking a pair for it would nil-deref inside GarbageCollect.
		// The root is never legitimately removed, but removedAt on it is not a
		// server invariant: an element payload (Set/Add/ArraySet) is read by
		// the same BytesTo* reader a snapshot is, so a crafted one can mark the
		// root removed. Leave it unbooked; it is unreachable garbage either
		// way, and the tree element that holds it is collected as a whole.
		if node.removedAt != nil && node.Index != nil && node.Index.Parent != nil {
			// Tree.DataSize skips a removed node, so this one was never in
			// the Live the scan's root was built with.
			gcSize := node.DataSize()
			pairs = append(pairs, GCPair{
				Parent:     t,
				Child:      node,
				GCOnlySize: &gcSize,
			})
		}

		pairs = append(pairs, node.GCPairs()...)
	}

	return pairs
}

// detachedGCPairs returns the pairs the detached nodes the tree holds have
// pending: a detached tombstone that is not released still has its own, and
// every detached node the pairs of its removed attributes. Root.DeepCopy
// books them for the copy it gives the detached subtrees, so the copy holds
// the GC its source does.
func (t *Tree) detachedGCPairs() []GCPair {
	var pairs []GCPair
	for _, node := range t.detachedByID.Values() {
		if node.removedAt != nil && !node.released {
			gcSize := node.DataSize()
			pairs = append(pairs, GCPair{Parent: t, Child: node, GCOnlySize: &gcSize})
		}
		pairs = append(pairs, node.GCPairs()...)
	}
	return pairs
}

// CreatedAt returns the creation time of this Tree.
func (t *Tree) CreatedAt() *time.Ticket {
	return t.createdAt
}

// SetCreatedAt sets the creation time of this Tree manually.
func (t *Tree) SetCreatedAt(createdAt *time.Ticket) {
	t.createdAt = createdAt
}

// RemovedAt returns the removal time of this Tree.
func (t *Tree) RemovedAt() *time.Ticket {
	return t.removedAt
}

// MovedAt returns the move time of this Tree.
func (t *Tree) MovedAt() *time.Ticket {
	return t.movedAt
}

// SetMovedAt sets the move time of this Text.
func (t *Tree) SetMovedAt(movedAt *time.Ticket) {
	t.movedAt = movedAt
}

// SetRemovedAt sets the removal time of this array.
func (t *Tree) SetRemovedAt(removedAt *time.Ticket) {
	t.removedAt = removedAt
}

// Remove removes this Text.
func (t *Tree) Remove(removedAt *time.Ticket) bool {
	if (removedAt != nil && removedAt.After(t.createdAt)) &&
		(t.removedAt == nil || removedAt.After(t.removedAt)) {
		t.removedAt = removedAt
		return true
	}
	return false
}

// Nodes traverses the tree and returns the list of nodes.
func (t *Tree) Nodes() []*TreeNode {
	var nodes []*TreeNode
	index.Traverse(t.IndexTree, func(node *index.Node[*TreeNode], depth int) {
		nodes = append(nodes, node.Value)
	})

	return nodes
}

// Root returns the root node of the tree.
func (t *Tree) Root() *TreeNode {
	return t.IndexTree.Root().Value
}

// NodeLen returns the size of the LLRBTree.
func (t *Tree) NodeLen() int {
	return t.NodeMapByID.Len()
}

// DetachedNode returns the detached node registered under exactly id, or nil.
// Detached nodes are kept apart from NodeMapByID, which holds only what is
// reachable from the root, so checks of that index look here for the rest.
// For diagnostics and tests, as NodeLen is.
func (t *Tree) DetachedNode(id *TreeNodeID) *TreeNode {
	key, node := t.detachedByID.Floor(id)
	if node == nil || !key.Equal(id) {
		return nil
	}
	return node
}

// ToXML returns the XML encoding of this tree.
func (t *Tree) ToXML() string {
	return ToXML(t.Root())
}

// EditT edits the given range with the given value.
// This method uses indexes instead of a pair of TreePos for testing.
func (t *Tree) EditT(
	start, end int,
	contents []*TreeNode,
	splitLevel int,
	editedAt *time.Ticket,
	issueTimeTicket func() *time.Ticket,
) ([]GCPair, resource.DataSize, error) {
	fromPos, err := t.FindPos(start)
	if err != nil {
		return nil, resource.DataSize{}, err
	}
	toPos, err := t.FindPos(end)
	if err != nil {
		return nil, resource.DataSize{}, err
	}

	// Asks for the reverse info even though it discards it: this stands in for
	// a local edit in tests, and a local edit computes it.
	pairs, diff, _, err := t.Edit(fromPos, toPos, contents, splitLevel, editedAt, issueTimeTicket, nil, true)
	return pairs, diff, err
}

// FindPos finds the position of the given index in the tree. It rejects an
// index inside a surrogate pair with ErrInvalidUTF16Index.
// (local) index -> (local) TreePos in indexTree -> (logical) TreePos in Tree
func (t *Tree) FindPos(offset int) (*TreePos, error) {
	treePos, err := t.IndexTree.FindTreePos(offset) // local TreePos
	if err != nil {
		return nil, err
	}

	if treePos.Node.IsText() &&
		!isUTF16Boundary(treePos.Node.Value.Value, treePos.Offset) {
		return nil, ErrInvalidUTF16Index
	}

	return toTreePos(treePos), nil
}

// FindPosUnchecked is FindPos without the surrogate pair check. It is for
// indexes the document computed itself, such as an undo range reconciled
// against a remote edit, which can land inside a pair through no fault of the
// caller. Refusing such an index would only drop the undo, and it resolves
// the way it did before the check existed.
func (t *Tree) FindPosUnchecked(offset int) (*TreePos, error) {
	treePos, err := t.IndexTree.FindTreePos(offset) // local TreePos
	if err != nil {
		return nil, err
	}

	return toTreePos(treePos), nil
}

// toTreePos converts a local TreePos in indexTree to a logical TreePos.
func toTreePos(treePos *index.TreePos[*TreeNode]) *TreePos {
	node, offset := treePos.Node, treePos.Offset
	var leftNode *TreeNode

	if node.IsText() {
		parent := node.Parent
		if offset == 0 {
			// A text at offset 0 cannot anchor on itself: a left sibling means
			// "after it", so the edit would land after the whole text. Anchor on
			// what precedes the text instead -- the parent when the text is the
			// first visible child, otherwise the previous visible sibling.
			//
			// That sibling is always an element: FindTreePos resolves a boundary
			// between two texts to the end of the left one, so a text reached at
			// offset 0 never has a text right before it.
			leftNode = parent.Value
			for _, child := range parent.Children() {
				if child == node {
					break
				}
				leftNode = child.Value
			}
		} else {
			leftNode = node.Value
		}
		node = parent
	} else {
		if offset == 0 {
			leftNode = node.Value
		} else {
			leftNode = node.Children()[offset-1].Value
		}
	}

	return &TreePos{
		ParentID: node.Value.id,
		LeftSiblingID: &TreeNodeID{
			CreatedAt: leftNode.id.CreatedAt,
			Offset:    leftNode.id.Offset + offset,
		},
	}
}

// TreeEditReverseInfo is everything Edit reports for building the operation
// that reverses it. It is returned by value; the slices and the map inside are
// still owned by the tree — see the field comments.
type TreeEditReverseInfo struct {
	// RemovedSpans describes, by original identity, each node THIS edit
	// transitioned visible -> tombstoned, in traversal order (parents before
	// children, which Restore relies on when recreating a purged subtree).
	// A reverse built from these revives the nodes in place rather than
	// re-inserting copies, which is what makes concurrent undos of one
	// deletion converge instead of duplicating content.
	//
	// Empty when SpansComplete is false — see there.
	RemovedSpans []*TreeRestoreSpan

	// InsertedSpans describes, by original identity, every node this edit
	// inserted, in parent-before-child order. A reverse re-removes them by
	// identity rather than by index, which a concurrent restore would
	// otherwise clobber. Empty when SpansComplete is false.
	InsertedSpans []*TreeRestoreSpan

	// SpansComplete reports whether RemovedSpans and InsertedSpans fully
	// describe what this edit did. It is false whenever the caller asked Edit
	// for no reverse info at all, so "spans empty" never reads as "nothing was
	// removed or inserted". Otherwise they do not describe the edit when it
	// merged nodes, or
	// when anything beyond the plain delete loop registered a GC pair (merge
	// propagation, content born tombstoned under a removed parent, or a piece
	// split off an already-tombstoned node). A caller must fall back to a
	// copy-reinsert reverse in that case; Removed and PreTombstoned below are
	// what it builds from.
	SpansComplete bool

	// MergeLevel is the number of element boundaries this edit merged away.
	// Non-zero means the reverse is a split — re-inserting the emptied
	// elements would restore shells whose children now live in the merge
	// target.
	MergeLevel int

	// InsertedContentSize is the tree-index size of the content the tree
	// accepted, measured while it was still detached: content reusing an ID
	// the tree already holds is dropped before this is measured, but content
	// tombstoned on the way into a removed parent is still counted, because
	// the reverse range is expressed against the pre-edit tree. Reading the
	// size back off the tree afterwards would shrink it and make the reverse
	// range cover a neighbour.
	InsertedContentSize int

	// Removed holds each node of Phase 5's toBeRemoveds that THIS edit
	// transitioned visible -> tombstoned, in the order that phase walked them.
	// PreTombstoned names — by IDString, mirroring the JS port's ID-string Set
	// — every one of those same collected nodes that was already tombstoned
	// before this edit ran. The two partition toBeRemoveds; they say nothing
	// about nodes propagateMergeDeletes additionally tombstones, since those
	// never enter toBeRemoveds either (matching JS, whose merge propagation
	// likewise never appends to nodesToBeRemoved).
	//
	// A node in PreTombstoned represents a deletion the user already made
	// independently of this edit and must not be resurrected by a reverse
	// built from Removed. JS's own nodesToBeRemoved (the analogue of Removed
	// here) INCLUDES pre-tombstoned nodes, unlike this field — so the
	// top-level filter for a copy-reinsert reverse's contents is
	//
	//	parent == nil || (parent NOT IN Removed AND parent.IDString() NOT IN PreTombstoned)
	//
	// not "parent not in Removed" alone: a live descendant whose immediate
	// parent is pre-tombstoned must not be promoted to top-level, exactly as
	// JS drops it too (tree_edit_operation.ts:622-627) by excluding any node
	// whose parent is in its (unfiltered) nodesToBeRemoved.
	//
	// These nodes are live tombstones still linked into the tree, not copies.
	// A caller that needs to keep them past the Edit call must DeepCopy()
	// before any subsequent edit: a later SplitText mutates in place and
	// splits tombstones too, so it can truncate a captured node's value out
	// from under a caller holding onto it. Do not store the slice or the map
	// beyond the Execute call that produced them — build any reverse
	// operation's content from them synchronously, the way JS does inside the
	// same execute().
	Removed       []*TreeNode
	PreTombstoned map[string]struct{}

	// PreEditFromIdx is the visible index `from` occupies after Phase 3
	// (Range Narrowing), before Phase 5 (Delete) or anything else below
	// mutates the tree. Mirrors preEditFromIdx in the JS SDK
	// (crdt/tree.ts:1872), computed at the identical point: after both
	// positions are resolved and split (findNodesAndSplitText) AND after
	// the split-sibling advance (Phase 2), using the post-advance fromLeft
	// -- not right after Phase 1, which would miss the padded size of any
	// concurrent split product Phase 2 skips past. Phases 2-4 do not mutate
	// the tree, so this is also the latest point that still sees it
	// pre-edit. The operations layer reads this to report the range a
	// forward execution affected, for undo/redo reconciliation; this
	// package does not read it itself, and leaves it at 0 when the caller asked
	// for no reverse info.
	PreEditFromIdx int

	// RemovedSize is the total padded size of every node Phase 5 processed
	// -- both the ones this edit newly tombstoned and any it found already
	// tombstoned -- measured by each node's own PaddedLength AFTER every
	// phase (merge, split, insert) has run, not right after Phase 5's
	// delete loop: toBeRemoveds is parent-before-child, and a node's own
	// removal only drains its ANCESTORS' aggregate (see TreeNode.remove),
	// one child at a time, so measuring a removed parent element before its
	// own children lower in toBeRemoveds have been processed would still
	// see its full pre-removal aggregate and double-count them. Mirrors
	// JS's removedNodes.reduce((sum, n) => sum + n.paddedSize(), 0)
	// (tree_edit_operation.ts:463-466), computed after tree.edit() has
	// fully returned, over the same set: JS's nodesToBeRemoved includes
	// pre-tombstoned nodes the way this field does, unlike Removed above --
	// see Removed's own doc comment.
	RemovedSize int

	// SplitSize is the visible-index size Phase 7's split added: one close
	// token plus one open token per element it actually split. Measured off
	// the tree rather than computed as 2*splitLevel, so a split whose product
	// is born tombstoned -- the element was already removed by a concurrent
	// deletion -- reports the zero growth it really produced.
	//
	// A split creates boundaries instead of inserting nodes, so it adds
	// nothing to InsertedContentSize; without this field the operations layer
	// reported a split as a zero-width, zero-growth edit and undo/redo
	// reconciliation left every stacked entry to the right of it unshifted.
	SplitSize int
}

// Edit edits the tree with the given range and content. If the content is
// undefined, the range will be removed. Besides the GC pairs and the size
// diff, it reports what a reverse operation needs; see TreeEditReverseInfo.
//
// needsReverseInfo selects whether that reverse info is computed at all. It is
// pure bookkeeping — nothing this method reads back, and nothing that reaches
// the tree's own state — so a caller that discards it can and should ask for
// none: the server's change replay does, and computing it there costs a
// visible-index resolution plus one sibling scan per touched node, on a path
// that runs over every change a document ever accumulated. The rest of the
// info (sizes, removed nodes, merge level) is cheap and always reported.
func (t *Tree) Edit(
	from, to *TreePos,
	contents []*TreeNode,
	splitLevel int,
	editedAt *time.Ticket,
	issueTimeTicket func() *time.Ticket,
	versionVector time.VersionVector,
	needsReverseInfo bool,
) ([]GCPair, resource.DataSize, TreeEditReverseInfo, error) {
	var diff resource.DataSize
	var pairs []GCPair
	var info TreeEditReverseInfo

	// Phase 1: Position Resolution — resolve CRDTTreePos to tree nodes.
	// Only an empty range is a pure insert position (§1.3); both ends of a
	// non-empty range are boundaries its client drew around whole elements.
	mode := BoundaryInsert
	if from.ParentID.Equal(to.ParentID) && from.LeftSiblingID.Equal(to.LeftSiblingID) {
		mode = BoundaryPureInsert
	}
	fromParent, fromLeft, diffFrom, err := t.FindTreeNodesWithSplitText(from, editedAt, mode)
	if err != nil {
		return t.drainPendingGCPairs(), diff, info, err
	}
	toParent, toLeft, diffTo, err := t.FindTreeNodesWithSplitText(to, editedAt, mode)
	if err != nil {
		diff.Add(diffFrom)
		return t.drainPendingGCPairs(), diff, info, err
	}

	diff.Add(diffFrom, diffTo)

	// Phase 2: Split Sibling Advance — advance past concurrent split
	// products linked via InsNextID that the editor could not have seen.
	// Skip when leftNode == parent (leftmost child position).
	// skipActorID carries the editing actor so this resolves a same-boundary
	// empty run exactly as the split loop does (§7.5/§7.8); without it the
	// two sides would place the same boundary differently.
	editActorID := editedAt.ActorID()
	if fromLeft != fromParent {
		fromLeft = t.advancePastUnknownSplitSiblings(fromLeft, versionVector, advanceOpts{
			skipActorID: &editActorID,
		})
	}
	if toLeft != toParent {
		toLeft = t.advancePastUnknownSplitSiblings(toLeft, versionVector, advanceOpts{
			skipActorID: &editActorID,
		})
	}

	// Phase 3: Range Narrowing — when fromLeft and toLeft are in
	// different parents (due to a concurrent element split), follow
	// fromLeft's InsNextID chain to find a split sibling in toParent
	// and narrow the collectBetween range. The original fromParent/
	// fromLeft are preserved for merge, split, and insert steps.
	// VV-independent for clone/root consistency.
	collectFromParent, collectFromLeft := t.narrowCollectRange(fromParent, fromLeft, toParent, toLeft)

	// Captured here, after Phase 3 -- matching JS's own capture point
	// exactly (crdt/tree.ts:1872, after findNodesAndSplitText(to) and the
	// split-sibling advance, using the post-advance fromLeft). Phases 2-4 do
	// not mutate the tree, so this is the latest point before Phase 5
	// (delete) that still sees the pre-edit tree, and the earliest point
	// where fromLeft has already been advanced past any concurrent split
	// products Phase 2 skips -- a position interior to an unsplit node has
	// already been split by FindTreeNodesWithSplitText above, so this
	// resolves the visible index `from` names post-split, not the coarser
	// one a non-splitting resolver would collapse it to.
	//
	// Gated, not moved: the capture point stays exactly here for every caller
	// that does need it. Only the caller that reads nothing back skips it.
	if needsReverseInfo {
		preEditFromIdx, err := t.ToIndex(fromParent, fromLeft)
		if err != nil {
			return t.drainPendingGCPairs(), diff, info, err
		}
		info.PreEditFromIdx = preEditFromIdx
	}

	toBeRemoveds, toBeMovedToFromParents, toBeMergedNodes, err := t.collectBetween(
		collectFromParent, collectFromLeft, toParent, toLeft,
		editedAt, versionVector,
	)
	if err != nil {
		return t.drainPendingGCPairs(), diff, info, err
	}

	// Phase 4: Count the boundaries this edit merges away, BEFORE Phase 6
	// moves their children. A reverse built after the move would re-insert
	// empty shells, so the count is what tells the caller to build a split
	// reverse instead.
	info.MergeLevel = len(toBeMergedNodes)

	// Phase 5: Delete — tombstone the collected nodes.
	deletePairs, removed, removedSpans, preTombstoned := t.tombstoneCollected(
		toBeRemoveds, editedAt, needsReverseInfo,
	)
	pairs = append(pairs, deletePairs...)
	info.Removed, info.PreTombstoned = removed, preTombstoned

	// Every pair registered from here on is one the spans do not describe:
	// merge propagation, content born tombstoned under a removed parent, or a
	// piece split off an already-tombstoned node. Comparing the count at the
	// end is how SpansComplete detects all three at once.
	deletePairCount := len(pairs)

	// Phase 6: Merge — move children to fromParent, set forwarding pointers.
	// A child a forged lineage would have moved under itself is tombstoned
	// rather than moved, so those pairs are registered even on the error path.
	stranded, released, err := t.mergeNodes(
		fromParent, toBeMovedToFromParents, editedAt,
	)
	diff.Sub(released)
	for _, node := range stranded {
		pairs = append(pairs, GCPair{
			Parent: t,
			Child:  node,
		})
	}
	if err != nil {
		return append(pairs, t.drainPendingGCPairs()...), diff, info, err
	}

	// §6.2: Propagate deletes to children moved by prior merges. The declared
	// positions are passed through because the skip is keyed on them; what the
	// propagation tombstones stays out of the reverse info, as it does in JS.
	mergePairs := t.propagateMergeDeletes(
		fromParent, from, to, toBeRemoveds, toBeMergedNodes, editedAt,
	)
	pairs = append(pairs, mergePairs...)

	// Phase 7: Split — split element nodes for the given splitLevel.
	//
	// Two things are reported across the call. diffSplit is the metadata the
	// new elements added, and it is added before the error check the way
	// Phase 1 adds diffFrom before returning on the to-resolution error: a
	// multi-level split that fails partway through has still created the
	// elements it got to, and docSize.Live has to carry them.
	//
	// SplitSize is the visible index those boundaries opened, which nothing
	// else in this method reports: they are not content, so Phase 8's
	// InsertedContentSize does not see them, and they remove nothing, so
	// RemovedSize does not either.
	sizeBeforeSplit := t.Root().Len()
	diffSplit, err := t.split(fromParent, fromLeft, splitLevel, editedAt, issueTimeTicket, versionVector)
	diff.Add(diffSplit)
	info.SplitSize = t.Root().Len() - sizeBeforeSplit
	if err != nil {
		return append(pairs, t.drainPendingGCPairs()...), diff, info, err
	}

	// Phase 8: Insert — insert the given node at the given position.
	//
	// The identity check runs here rather than on entry: Phase 1 splits text
	// nodes to resolve the range, and a split can create the very ID a content
	// node carries. Checking before that would let the copy through and leave
	// two nodes under one ID.
	contents = t.dropDuplicateContents(contents, editedAt)

	// Measured now, while the content is still detached: inserting under a
	// removed parent tombstones it, which shrinks what its size reads back as.
	for _, content := range contents {
		info.InsertedContentSize += content.Index.PaddedLength()
	}

	var insertedSpans []*TreeRestoreSpan
	if len(contents) != 0 {
		intendedParent, intendedMergedAt := t.intendedMergeParent(from, fromParent)

		leftInChildren := fromLeft

		for _, content := range contents {
			if leftInChildren == fromParent {
				err := fromParent.InsertAt(content, 0)
				if err != nil {
					return append(pairs, t.drainPendingGCPairs()...), diff, info, err
				}
			} else {
				err := fromParent.InsertAfter(content, leftInChildren)
				if err != nil {
					return append(pairs, t.drainPendingGCPairs()...), diff, info, err
				}
			}

			if intendedParent != nil {
				content.MergedFrom = intendedParent.id
				content.MergedAt = intendedMergedAt
			}

			leftInChildren = content
			index.TraverseNode(content.Index, func(node *index.Node[*TreeNode], depth int) {
				// Content placed in a subtree a purge took out is part of it
				// (see TreeNode.detached); its parent is removed, so it is
				// tombstoned below as well.
				node.Value.detached = fromParent.detached
				// if insertion happens during concurrent editing and parent node has been removed,
				// make new nodes as tombstone immediately
				if fromParent.IsRemoved() {
					node.Value.remove(editedAt)
					// Born tombstoned, so never charged to Live: the pair
					// books its size to GC alone.
					gcSize := node.Value.DataSize()
					pairs = append(pairs, GCPair{
						Parent:     t,
						Child:      node.Value,
						GCOnlySize: &gcSize,
					})
				} else {
					diff.Add(node.Value.DataSize())
				}

				t.putNode(node.Value)

				// Capture this inserted node's identity span so an undo
				// re-removes it by identity rather than by index.
				insertedSpans = appendRestoreSpan(
					insertedSpans, node.Value, needsReverseInfo,
				)
			})
		}
	}

	pairs = append(pairs, t.drainPendingGCPairs()...)

	// Identity-preserving restore only describes a plain edit. If this one
	// merged nodes, or anything past the delete loop registered a GC pair, the
	// spans are an incomplete account of it and the caller has to fall back to
	// the copy-reinsert reverse.
	info.SpansComplete = needsReverseInfo &&
		info.MergeLevel == 0 && len(pairs) == deletePairCount
	if info.SpansComplete {
		info.RemovedSpans = removedSpans
		// TraverseNode is post-order (children before parent), so reverse to
		// get parent-before-child — the order Restore needs to recreate a
		// purged subtree top-down, since a child resolves its parent by
		// identity.
		slices.Reverse(insertedSpans)
		info.InsertedSpans = insertedSpans
	}

	// RemovedSize is summed here, last, after every phase (merge, split,
	// insert) has run -- mirroring JS's timing exactly: JS sums
	// removedNodes.reduce(paddedSize) after tree.edit() has fully returned
	// (tree_edit_operation.ts:462-466), not partway through. Timing matters:
	// toBeRemoveds is in traversal order, parent before children, and a
	// node's own removal does not zero its own PaddedLength -- only each of
	// its removed CHILDREN's own remove() calls drain the parent's
	// aggregate, one child at a time (TreeNode.remove only adjusts its
	// ancestors, never itself). Measuring a parent element right after
	// Phase 5's delete loop, before its children lower in toBeRemoveds have
	// been processed, would double-count: the parent's still-full aggregate
	// plus each child's own size again. Summing only after every phase has
	// run — by which point every node in toBeRemoveds, parent or child, has
	// had its final say in every other node's aggregate — gives each node
	// exactly the contribution JS's post-edit() measurement gives it.
	var removedSize int
	for _, node := range toBeRemoveds {
		removedSize += node.Index.PaddedLength()
	}
	// Phase 6's stranded nodes are removed by this edit too, and the loop
	// above cannot see them: the merge had collected them to MOVE, so they
	// are in toBeMovedToFromParents, not toBeRemoveds. Tombstoning them in
	// place drained their bytes out of the removed source they stay under,
	// so without this second sum the range the edit consumed reads short by
	// a whole stranded subtree. They are summed on the same post-every-phase
	// timing and for the same reason the loop above works: tombstoneSubtree
	// takes the entire subtree, so every stranded node has had its final say
	// in every other node's aggregate, and it cannot double-count against
	// toBeRemoveds because the two sets are disjoint (see tombstoneSubtree).
	for _, node := range stranded {
		removedSize += node.Index.PaddedLength()
	}
	info.RemovedSize = removedSize

	return pairs, diff, info, nil
}

// tombstoneCollected runs Edit's Phase 5 (Delete) over the nodes collected for
// removal. It reports the GC pairs to register, the nodes THIS call newly
// tombstoned, their identity spans, and — by IDString, mirroring the JS port's
// ID-string Set — the collected nodes it found already tombstoned.
//
// Everything is captured in the single pass: `remove` returns true only for a
// node transitioning live -> tombstoned (see TreeNode.remove), so the two
// branches below are exactly "newly removed by this edit" and "was already
// removed" — no second traversal is needed to tell them apart. That is also
// exactly the set worth an identity span: pre-tombstoned nodes and LWW
// overwrites are excluded automatically.
//
// The spans are captured only when needsReverseInfo is set; see Edit.
func (t *Tree) tombstoneCollected(
	toBeRemoveds []*TreeNode,
	editedAt *time.Ticket,
	needsReverseInfo bool,
) ([]GCPair, []*TreeNode, []*TreeRestoreSpan, map[string]struct{}) {
	pairs := make([]GCPair, 0, len(toBeRemoveds))
	removed := make([]*TreeNode, 0, len(toBeRemoveds))
	var removedSpans []*TreeRestoreSpan
	var preTombstoned map[string]struct{}

	for _, node := range toBeRemoveds {
		if node.remove(editedAt) {
			pairs = append(pairs, GCPair{
				Parent: t,
				Child:  node,
			})
			removed = append(removed, node)
			removedSpans = appendRestoreSpan(removedSpans, node, needsReverseInfo)
			continue
		}
		if preTombstoned == nil {
			preTombstoned = make(map[string]struct{})
		}
		preTombstoned[node.IDString()] = struct{}{}
	}

	return pairs, removed, removedSpans, preTombstoned
}

// appendRestoreSpan appends node's identity span to spans when the caller asked
// for reverse info, and returns spans untouched when it did not. Capturing a
// span walks node's siblings, so skipping it is the point rather than an
// optimization detail; see Edit's needsReverseInfo.
func appendRestoreSpan(
	spans []*TreeRestoreSpan,
	node *TreeNode,
	needsReverseInfo bool,
) []*TreeRestoreSpan {
	if !needsReverseInfo {
		return spans
	}

	return append(spans, restoreSpanOf(node))
}

// restoreSpanOf captures node's identity and where it sits among its siblings,
// so a reverse operation can revive or re-remove it under that identity rather
// than by index. Read at the moment of capture: for a removed node that is
// right after remove() tombstoned it, and for an inserted one right after it
// was linked in.
func restoreSpanOf(node *TreeNode) *TreeRestoreSpan {
	span := &TreeRestoreSpan{
		ID:       node.id,
		NodeType: node.Type(),
		IsText:   node.IsText(),
	}
	if node.IsText() {
		span.Length = node.Length()
		span.Value = node.Value
	}
	if node.Attrs != nil {
		span.Attributes = node.Attrs.DeepCopy()
	}

	parentID, leftID, rightID, ok := anchorsOf(node)
	if !ok {
		return span
	}
	span.ParentID = parentID
	span.LeftSiblingID = leftID
	span.RightSiblingID = rightID

	return span
}

// anchorsOf reads where node sits among its siblings right now: the ID of its
// parent and the left/right sibling anchors a restore span puts it back by.
// ok is false when node has no parent to read them from. The three are only
// ever read together: recreateFromSpan honours a sibling anchor solely when
// it is still a child of the span's ParentID (see its sameParent check), so a
// span that mixes a fresh anchor with a stale parent silently falls back to
// id-order placement.
func anchorsOf(node *TreeNode) (parentID, leftID, rightID *TreeNodeID, ok bool) {
	if node.Index.Parent == nil {
		return nil, nil, nil, false
	}

	parent := node.Index.Parent.Value
	siblings := parent.Index.Children(true)
	idx := slices.Index(siblings, node.Index)
	if idx > 0 {
		leftID = leftAnchorID(siblings[idx-1].Value)
	}
	if idx >= 0 && idx < len(siblings)-1 {
		rightID = siblings[idx+1].Value.id
	}

	return parent.id, leftID, rightID, true
}

// leftAnchorID returns the ID to store as a restore span's left-sibling
// anchor. For a text node the anchor is its LAST character's offset, not its
// start: a concurrent delete may later split the left neighbor, and only the
// last-char offset floor-resolves to the rightmost fragment (the true left
// neighbor of the restored node). For elements (never split by offset) the
// node's own ID is exact. Right-sibling anchors always use the start offset,
// which floor-resolves to the leftmost fragment — the true right neighbor.
func leftAnchorID(sibling *TreeNode) *TreeNodeID {
	// A text node with no characters has no last character to anchor on, and
	// Length()-1 would land one code unit before its own start. Local edits
	// never create one, but a remote peer's contents can carry it; the node's
	// own ID floor-resolves to the same node.
	if !sibling.IsText() || sibling.Length() == 0 {
		return sibling.id
	}

	return NewTreeNodeID(sibling.id.CreatedAt, sibling.id.Offset+sibling.Length()-1)
}

// intendedMergeParent resolves the §9.4 stamp for an insert whose position
// was declared inside a parent that a concurrent merge removed: the content
// lands in the merge target, so stamp it as merged-from the declared parent
// to keep it distinguishable from nodes that were never inside that parent
// (the §9.4 filter and merge-delete propagation key on this). The merge
// ticket comes from a sibling the merge moved, since the parent's own
// removedAt may be overwritten by a later LWW tombstone.
func (t *Tree) intendedMergeParent(
	from *TreePos,
	fromParent *TreeNode,
) (*TreeNode, *time.Ticket) {
	declaredFromParent, _ := t.ToTreeNodes(from)
	if declaredFromParent == nil || declaredFromParent == fromParent ||
		!declaredFromParent.IsRemoved() || declaredFromParent.mergedInto == nil ||
		t.resolveMergeTarget(declaredFromParent) != fromParent {
		return nil, nil
	}
	for _, child := range fromParent.Index.Children(true) {
		if child.Value.MergedFrom != nil &&
			child.Value.MergedFrom.Equal(declaredFromParent.id) &&
			child.Value.MergedAt != nil {
			return declaredFromParent, child.Value.MergedAt
		}
	}
	return declaredFromParent, declaredFromParent.removedAt
}

// mergedAnchorInterloperGuard prepares the §9.4 per-node filter for a style
// range whose end position was declared inside a parent that a merge unknown
// to the styling client removed. The moved anchor child resolves in the
// merge target, so the traversal covers nodes sitting between the
// merge-source tombstone and the moved children — nodes the styling client
// saw outside its range, after the then-live parent. The predicate skips
// exactly those interlopers.
func (t *Tree) mergedAnchorInterloperGuard(
	pos *TreePos,
	versionVector time.VersionVector,
) (func(*TreeNode) bool, *TreeNode, *TreeNode) {
	if len(versionVector) == 0 {
		return nil, nil, nil
	}
	declaredParent, _ := t.ToTreeNodes(pos)
	if declaredParent == nil || !declaredParent.IsRemoved() ||
		declaredParent.mergedInto == nil || declaredParent.removedAt == nil ||
		time.TicketKnown(versionVector, declaredParent.removedAt) {
		return nil, nil, nil
	}
	target := t.resolveMergeTarget(declaredParent)
	// Restricted to the shape where the tombstone sits directly under the
	// merge target; in other shapes the resolved range already excludes
	// the interlopers.
	if target == declaredParent || declaredParent.Index.Parent == nil ||
		declaredParent.Index.Parent.Value != target {
		return nil, nil, nil
	}
	// Collect the target's children positioned after the merge-source
	// tombstone in one pass, so the predicate is O(depth) per node.
	afterTombstone := make(map[*TreeNode]bool)
	seenTombstone := false
	for _, child := range target.Index.Children(true) {
		if child.Value == declaredParent {
			seenTombstone = true
			continue
		}
		if seenTombstone {
			afterTombstone[child.Value] = true
		}
	}
	return func(node *TreeNode) bool {
		// Judge by the node's highest ancestor directly under the merge
		// target, so an interloper's descendants are skipped with it.
		top := node
		for top.Index.Parent != nil && top.Index.Parent.Value != target {
			top = top.Index.Parent.Value
		}
		if top.Index.Parent == nil || top.Index.Parent.Value != target {
			return false
		}
		// Fail open for any node carrying a merge stamp: a child that
		// arrived via an earlier merge keeps the ORIGINAL source in
		// MergedFrom (first-move rule, Fix 20), so membership in the
		// declared parent is unrecoverable. Only stamp-free nodes are
		// positively identifiable as interlopers (§9.4).
		if top.MergedFrom != nil {
			return false
		}
		return afterTombstone[top]
	}, declaredParent, target
}

// stylePrevAttrs reports, for each key in attrs in sorted order, the value
// that key held (or its absence) on the given node, for the reverse Style
// capture.
func stylePrevAttrs(node *TreeNode, attrs map[string]string) []PrevAttr {
	keys := slices.AppendSeq(make([]string, 0, len(attrs)), maps.Keys(attrs))
	slices.Sort(keys)
	var prevAttrs []PrevAttr
	for _, key := range keys {
		if node.Attrs != nil && node.Attrs.Has(key) {
			prevAttrs = append(prevAttrs, PrevAttr{Key: key, Value: node.Attrs.Get(key), Existed: true})
		} else {
			prevAttrs = append(prevAttrs, PrevAttr{Key: key, Existed: false})
		}
	}
	return prevAttrs
}

// reversedFromAnchorRecovery prepares the §9.4 from-side counterpart of
// mergedAnchorInterloperGuard for a style range whose start position was
// declared inside a parent that a merge unknown to the styling client
// removed. The resolved range then collapses (start past end) and the
// traversal misses nodes the styling client covered. The recovery
// re-anchors the traversal start just after the last live sibling before
// the merge-source tombstone; the caller must style only nodes the
// returned predicate positively identifies as interlopers. Stamped nodes
// in the span stay out of reach and fail open unstyled — see the
// §9.4 known limitations in docs/design/concurrent-merge-split.md.
func (t *Tree) reversedFromAnchorRecovery(
	from, to *TreePos,
	fromParent, fromLeft, toParent, toLeft *TreeNode,
	versionVector time.VersionVector,
) (*TreeNode, *TreeNode, func(*TreeNode) bool, error) {
	isInterloper, declaredParent, target := t.mergedAnchorInterloperGuard(from, versionVector)
	if isInterloper == nil {
		return nil, nil, nil, nil
	}
	// Only a range that actually collapsed needs recovery: when both
	// anchors moved with the merge, the resolved range stays ordered and
	// still covers what the styling client covered. A range that resolved
	// empty counts as collapsed too — the merge can leave the start anchor
	// exactly on the end one, and covering nothing loses the client's range
	// just as completely as covering it backwards. Unless the client's own
	// range was empty, in which case covering nothing is right.
	fromIdx, err := t.ToIndex(fromParent, fromLeft)
	if err != nil {
		return nil, nil, nil, err
	}
	toIdx, err := t.ToIndex(toParent, toLeft)
	if err != nil {
		return nil, nil, nil, err
	}
	if fromIdx < toIdx || (fromIdx == toIdx && from.Equal(to)) {
		return nil, nil, nil, nil
	}
	anchorLeft := target
	for _, child := range target.Index.Children(true) {
		if child.Value == declaredParent {
			break
		}
		if !child.Value.IsRemoved() {
			anchorLeft = child.Value
		}
	}
	return target, anchorLeft, isInterloper, nil
}

// isSplitProductOf reports whether node is origin itself or a piece a split
// cut off origin, by walking back along InsPrevID. Only SplitElement sets
// InsPrevID, so the chain is exactly the split lineage; the walker stops a
// crafted chain that loops back on itself, and a text link cuts it because an
// element is never split off a text node.
func (t *Tree) isSplitProductOf(node, origin *TreeNode) bool {
	return t.splitLineageOf(origin).reaches(node)
}

// splitLineage answers, for one origin, whether nodes were split off it,
// memoizing every node a walk back along InsPrevID visits. It is the one
// implementation of that walk: isSplitProductOf asks it once, and step 04
// asks it for every sibling of one position, where the products of repeated
// splits at one anchor are both the siblings and the links of the chain, so
// the memo keeps those questions at O(siblings + chain) in all.
type splitLineage struct {
	tree  *Tree
	known map[*TreeNode]bool
}

// splitLineageOf returns the split lineage of origin.
func (t *Tree) splitLineageOf(origin *TreeNode) *splitLineage {
	return &splitLineage{tree: t, known: map[*TreeNode]bool{origin: true}}
}

// reaches reports whether walking InsPrevID back from node arrives at the
// origin. The walk may start at a text node, but only ever steps onto an
// element: an element is never split off a text node. A crafted chain that
// loops back on itself reaches no origin.
func (l *splitLineage) reaches(node *TreeNode) bool {
	var path []*TreeNode
	onPath := make(map[*TreeNode]struct{})
	answer := false
	for current := node; ; {
		if result, ok := l.known[current]; ok {
			answer = result
			break
		}
		if _, ok := onPath[current]; ok {
			break
		}
		path = append(path, current)
		onPath[current] = struct{}{}

		if current.InsPrevID == nil {
			break
		}
		prev := l.tree.findFloorNode(current.InsPrevID)
		if prev == nil || prev.IsText() {
			break
		}
		current = prev
	}
	for _, n := range path {
		l.known[n] = answer
	}
	return answer
}

// splitProductTest returns a test for whether a sibling the step-04 walk of
// FindTreeNodesWithSplitText is about to pass was split off left, the element
// the position anchors on, sharing one splitLineage across the walk. Only an
// element sibling with an InsPrevID can be one, and only an element anchor has
// split products; a left-most position anchors on its parent, not on a
// sibling.
func (t *Tree) splitProductTest(left *TreeNode, isLeftMost bool) func(next *TreeNode) bool {
	if isLeftMost || left.IsText() {
		return func(*TreeNode) bool { return false }
	}

	lineage := t.splitLineageOf(left)
	return func(next *TreeNode) bool {
		return !next.IsText() && next.InsPrevID != nil && lineage.reaches(next)
	}
}

// declaredAncestryHas walks the CURRENT ancestry of the element a position
// named as its parent, from that element up to the root, and reports whether
// any of them matches. Both callers below ask "was this position declared
// inside node", differing only in how an ancestor is matched to node.
func (t *Tree) declaredAncestryHas(declaredParent *TreeNode, match func(*TreeNode) bool) bool {
	for current := declaredParent; current != nil; {
		if match(current) {
			return true
		}
		if current.Index.Parent == nil {
			return false
		}
		current = current.Index.Parent.Value
	}

	return false
}

// endsInside reports whether the change's range ended inside node, i.e.
// whether node is the element the range-end position named as its parent, or
// an ancestor of it. That is the one question the index space cannot answer
// after a concurrent split: node's End token is in the resolved range either
// because the range genuinely ran past node's end, or because the split
// pushed the end anchor into a sibling. The range-end position is carried by
// the change and never moves, so it separates the two.
//
// The ancestry walked is the CURRENT one, and a concurrent split moves the
// children after the split point into the new right half — so the declared
// parent can now sit under a piece of node instead of under node. Each
// ancestor is therefore matched through its split lineage rather than by
// identity: every product of splitting node stands for node, which is what
// the ancestry looked like when the change declared the position.
func (t *Tree) endsInside(node, declaredToParent *TreeNode) bool {
	return t.declaredAncestryHas(declaredToParent, func(ancestor *TreeNode) bool {
		return t.isSplitProductOf(ancestor, node)
	})
}

// beginsInside is the same question for the range-START position (§9.6): was
// the change's range start declared inside node, so that node's End token was
// in the range on the replica that issued the change.
//
// It matches a declared ancestor to node in BOTH directions along the split
// lineage, which endsInside deliberately does not. A split of the element the
// range began inside leaves the start anchor in whichever half now holds it,
// so the resolved from-parent can be a product of the declared one — the
// direction endsInside does not accept, because there the extra matches would
// make a guard that must not fail open do exactly that. Here the two halves
// are one element as far as the change is concerned: it declared a position
// inside the element that was split, and both halves are that element.
func (t *Tree) beginsInside(node, declaredFromParent *TreeNode) bool {
	return t.declaredAncestryHas(declaredFromParent, func(ancestor *TreeNode) bool {
		return t.isSplitProductOf(ancestor, node) || t.isSplitProductOf(node, ancestor)
	})
}

// beginsAtOrInside reports whether the change's range START leaves room for
// the change to have covered node: it began inside node, or at or before
// node's Start token.
//
// beginsInside alone answers only the first. A range that began strictly
// BEFORE node never named node as its parent, so beginsInside is false for it
// exactly as it is for a range that began after node — and only the latter is
// a range that never covered node. The two are separated here by document
// order, measured with removed nodes included so that a concurrent removal
// between the two positions moves neither.
//
// A from-position whose parent or left sibling is unknown here answers no.
// One whose left sibling is no longer a child of the declared parent still
// began inside what it declared, so beginsInside answers for it, but it has
// no document order: a concurrent merge moved the sibling, and the index
// lookup fails and answers no. Answering yes there instead turns converging
// pairs into diverging ones; refusing the begins-inside half as well drops the
// closure for a concurrent split that moved the sibling, which is the case
// the closure exists for. The Port specification states both, because an
// offset lookup that does not fail on a missing child (JS findOffset returns
// -1) would resolve a position here instead of refusing it.
func (t *Tree) beginsAtOrInside(node, declaredFromParent, declaredFromLeft *TreeNode) bool {
	if declaredFromParent == nil || declaredFromLeft == nil {
		return false
	}
	if t.beginsInside(node, declaredFromParent) {
		return true
	}
	if node.Index.Parent == nil {
		return false
	}

	offset, err := node.Index.Parent.FindOffset(node.Index, true)
	if err != nil {
		return false
	}
	nodeIdx, err := t.IndexTree.IndexOf(&index.TreePos[*TreeNode]{
		Node:   node.Index.Parent,
		Offset: offset,
	}, true)
	if err != nil {
		return false
	}
	declaredIdx, err := t.ToIndex(declaredFromParent, declaredFromLeft, true)
	if err != nil || declaredIdx < 0 {
		return false
	}

	return declaredIdx <= nodeIdx
}

// styleSkipPredicate builds the per-token skip checks shared by Style and
// RemoveStyle, as two predicates over the same state.
//
// skipReached answers "did the change reach this node at all": the End-token
// unknown-split-sibling exclusion (§9.1), the §9.6 range-start guard and the
// §9.4 merged-anchor interloper filter for the range-end position. It is
// about the change, so it holds for every node a style writes to, however
// that node was found.
//
// skipToken adds the one restriction that belongs to the index traversal
// alone — the §9.4 from-side recovery re-anchors the traversal start over
// nodes the change never covered, so a recovered traversal may touch only the
// interlopers the recovery positively identified. Nodes derived from the
// change's own positions are not in that widened span and answer to
// skipReached only.
//
// beganInside is the set of elements THIS replica's resolved range begins
// inside — the resolved from-parent and its ancestors, the only elements a
// token traversal reaches through their End token alone. Every other element
// the traversal reaches on an End token was reached on its Start token first.
func (t *Tree) styleSkipPredicate(
	from, to *TreePos,
	fromParent *TreeNode,
	versionVector time.VersionVector,
	recoveredInterloper func(*TreeNode) bool,
) (skipToken, skipReached func(index.TreeToken[*TreeNode]) bool) {
	isVersionVectorEmpty := len(versionVector) == 0
	isAnchorInterloper, _, _ := t.mergedAnchorInterloperGuard(to, versionVector)
	declaredToParent, _ := t.ToTreeNodes(to)
	declaredFromParent, _ := t.ToTreeNodes(from)

	beganInside := map[*TreeNode]bool{}
	for current := fromParent; current != nil; {
		beganInside[current] = true
		if current.Index.Parent == nil {
			break
		}
		current = current.Index.Parent.Value
	}

	skipReached = func(token index.TreeToken[*TreeNode]) bool {
		// Skip styling via End token when the node has an unknown split
		// sibling AND the change's range ended inside the node. Only then is
		// the End token in the range solely because a concurrent split
		// extended the range into the sibling. When the range ran past the
		// node's end the End token was in it before any split existed, and
		// skipping it loses the style on every replica that applies the
		// split first — the same style the opposite order keeps.
		if token.TokenType == index.End && !isVersionVectorEmpty &&
			(declaredToParent == nil || t.endsInside(token.Node, declaredToParent)) &&
			t.hasUnknownSplitSibling(token.Node, versionVector) {
			return true
		}
		// §9.6, the mirror of §9.1 on the range-start side. This replica's
		// range begins inside the node, so only its End token is in the
		// range; the change reached it that way only if the change's own
		// range-start position was declared inside it. When a merge removes
		// a paragraph's opening tag its children move into the merge target,
		// and the start anchor resolves inside that target — an element the
		// change never entered, whose End token is in the range solely
		// because of the merge. Styling it puts the attribute on a LIVE node
		// the replica that styled first left alone.
		if token.TokenType == index.End && !isVersionVectorEmpty &&
			beganInside[token.Node] && declaredFromParent != nil &&
			!t.beginsInside(token.Node, declaredFromParent) {
			return true
		}
		// §9.4: the node is in the range only because an unknown merge
		// pulled the range-end anchor past it.
		return isAnchorInterloper != nil && isAnchorInterloper(token.Node)
	}

	skipToken = func(token index.TreeToken[*TreeNode]) bool {
		if skipReached(token) {
			return true
		}
		// §9.4 from-side: a recovered traversal may only touch nodes the
		// collapsed range lost, the positively identified interlopers.
		return recoveredInterloper != nil && !recoveredInterloper(token.Node)
	}

	return skipToken, skipReached
}

// resolveMergeTarget follows the mergedInto forwarding chain from the given
// node while the current node is a merge-away tombstone, returning the final
// live target. When a merge lands on a parent that a prior concurrent merge
// already merged away (a chained merge P->Q->R, applied Q->R before this
// P->Q), the children must flow to that parent's final destination so the
// merge chain stays flat (P->R, not P->Q) and both replicas converge. The
// seen set guards against cycles from a concurrent mutual merge.
//
// Each link is resolved through findMergeNode, so a text node or a floor-only
// match cuts the chain: mergedInto is derived from the MergedFrom an element
// payload keeps, so a crafted one can name either here, and mergeNodes hands
// what this returns straight to MoveChild. Stopping at the last well-formed
// link treats the crafted tail as the absent lineage it is.
func (t *Tree) resolveMergeTarget(node *TreeNode) *TreeNode {
	target := node
	seen := map[*TreeNode]bool{target: true}
	for target.IsRemoved() && target.mergedInto != nil {
		next := t.findMergeNode(target.mergedInto)
		if next == nil || seen[next] {
			break
		}
		seen[next] = true
		target = next
	}
	return target
}

// mergeNodes moves children to the merge target and sets forwarding
// pointers on merge-source nodes. On each moved child it records MergedFrom
// (the source parent's ID) and MergedAt (the merge ticket) — both
// persisted in the snapshot encoding. MergedAt must be captured here
// because the source's removedAt is not immutable (later LWW tombstones
// overwrite it). On each merge-source parent it sets mergedInto as a
// runtime cache used by FindTreeNodesWithSplitText.
//
// §6.3 Chained-Merge Flattening (Fix 20): a merge chain P->Q->R is kept
// flat so the runtime state matches what rebuildMergeState derives from a
// snapshot (which can only ever represent the compressed chain, because it
// records one MergedFrom pointer per child and reads the child's final
// physical parent). Two mechanisms keep it flat:
//
//  1. The destination is resolved through resolveMergeTarget, so children
//     merged into an already-merged-away parent forward to the final live
//     target R instead of piling up under the removed intermediate Q.
//  2. A child's MergedFrom is stamped only on its first move, preserving the
//     original source P across later merges. The redirect boundary in
//     FindTreeNodesWithSplitText then still resolves (first child with
//     MergedFrom == P), and every source's mergedInto is (re)pointed at the
//     resolved destination — deriving it from each moved child's MergedFrom
//     exactly as rebuildMergeState does, so runtime and snapshot agree.
//
// mergedInto is set solely from moved children (never from the merge-source
// list directly), so it is set only when rebuildMergeState can reconstruct
// it — a source with no moved child of its own (e.g. an intermediate that
// only relayed another source's children) is left unset on both paths,
// keeping runtime and snapshot consistent.
//
// It returns the nodes it had to tombstone in place instead of moving — the
// stranded subtrees of the cycle guard below, in post-order. The caller books
// a GC pair for each and counts each toward the size the edit removed; they
// are returned as nodes rather than as pairs because the pair alone does not
// carry the length that accounting needs.
func (t *Tree) mergeNodes(
	fromParent *TreeNode,
	toBeMovedToFromParents []*TreeNode,
	editedAt *time.Ticket,
) ([]*TreeNode, resource.DataSize, error) {
	var released resource.DataSize
	dest := t.resolveMergeTarget(fromParent)

	// A merge into a detached destination takes the moved nodes into a
	// subtree a purge took out, and they take its side (takeDetachedSide),
	// tombstoned with its removal.
	var detachedAt *time.Ticket
	if dest.detached {
		detachedAt = detachedRemovalOr(dest, editedAt)
	}

	var stranded []*TreeNode
	// Set once the guard below tombstones a subtree that contains dest. The
	// destination is resolved once, before the loop, but the list it moves can
	// hold several merge sources' children, so entries remain after the guard
	// fires. dest is inside the subtree just tombstoned, so moving those
	// remaining children there would put live nodes under a tombstone -- the
	// very #2008 shape the guard exists to prevent. They are stranded too.
	destTombstoned := false
	for _, node := range toBeMovedToFromParents {
		// A moved child must have a source parent to record; skip
		// otherwise rather than append an untracked node (a node without
		// MergedFrom is invisible to propagateMergeDeletes and
		// rebuildMergeState).
		if node.Index.Parent == nil {
			continue
		}
		// A destination that is the node itself or lies inside it can only
		// come from a forged lineage: mergedInto is derived from the
		// MergedFrom an element payload keeps, so a crafted one can aim the
		// destination's forwarding chain at a child this merge moves, or at
		// a node inside one. Moving the node there would close a parent cycle
		// in the index tree, and every later ancestor walk would spin forever.
		// Leave it where it is, unstamped. The check reads only the tree the
		// change lands on, so replicas holding the same tree decide alike.
		//
		// Left live, it would be stranded instead: Phase 5 has already
		// tombstoned the source it stays under, so the node would sit out of
		// sight until a collection released it with the source (see
		// GCLiveRelease), and a restore of the source would show it again
		// only on replicas that had not collected yet. So it is tombstoned where it
		// stands, with its descendants, exactly as propagateMergeDeletes
		// tombstones a merge-moved child whose source this edit deletes. The
		// pair the caller books does both halves: it moves the bytes from Live
		// to GC, and it is what purges the node once the removal is causally
		// stable.
		//
		// Once that has happened for any entry, dest carries a removedAt of
		// its own, so every entry still to come is stranded for the same
		// reason and takes the same treatment.
		if node == dest || node.Index.IsAncestorOf(dest.Index) {
			destTombstoned = true
			stranded = append(stranded, t.tombstoneSubtree(node, editedAt)...)
			continue
		}
		if destTombstoned {
			stranded = append(stranded, t.tombstoneSubtree(node, editedAt)...)
			continue
		}
		// A detached node is not moved into the attached tree: a replica
		// that purged its subtree has nothing there to move (see
		// TreeNode.detached). Defensive; unreachable because a merge's
		// moved children are collected from a range of the attached tree,
		// which holds no detached node.
		if node.detached && !dest.detached {
			continue
		}
		// Record source parent and merge ticket on the moved child.
		// Both fields are persisted; MergedAt must be stored explicitly
		// because source.removedAt can be overwritten by later concurrent
		// deletes (LWW). Tombstoned children are moved too (kept removed):
		// they stay as RGA anchors so a concurrent insert referencing one
		// resolves in the merge target and orders via the RGA tie-break,
		// converging with the replica that inserted before the merge.
		// MoveChild relocates the node with correct length accounting for
		// both live and tombstoned children (visible-neutral for the
		// latter), so index positions stay correct on both parents.
		//
		// MergedFrom and MergedAt are stamped together, only on the first
		// move, so a child carried through a chained merge keeps its
		// original source P and the original P->Q merge ticket (Fix 20).
		// Stamping MergedAt on every move would diverge: a replica that
		// applied P->Q then Q->R would record the Q->R ticket, while a
		// replica where Q was already merged records the P->Q ticket on the
		// single forwarded move — an inconsistency the §7.1 split VV check
		// would then resolve differently on each side.
		if node.MergedFrom == nil {
			node.MergedFrom = node.Index.Parent.Value.id
			node.MergedAt = editedAt
			t.noteMergeStamp(node)
		}
		if err := dest.Index.MoveChild(node.Index); err != nil {
			return stranded, released, err
		}
		if dest.detached {
			released.Add(t.takeDetachedSide(node, detachedAt))
		}
		// Point this child's original source at the resolved destination,
		// path-compressing a transitive source (a prior merge whose children
		// were just relocated again) from the now-removed intermediate to the
		// final target. Deriving mergedInto from a *moved* child — one that
		// had a parent above — mirrors rebuildMergeState, which likewise skips
		// parentless children, so runtime and snapshot agree. (A parentless
		// child, detached by a concurrent split cascade, is continue'd above
		// and must not repoint its source here.)
		//
		// Resolved through findMergeNode for the same reason rebuildMergeState
		// resolves it there: MergedFrom is stamped here only when the node
		// carries none, so a node that arrived on an element payload with one
		// already set keeps the client's value, and this is where that value is
		// read again long after the decode that first saw it. Left to a plain
		// floor lookup, a made-up offset would plant the forwarding pointer on
		// whatever node happens to sit below it.
		//
		// The source must also be a tombstone. An honest merge always
		// tombstones it in Phase 5 (collectBetween appends every merge
		// boundary to toBeRemoveds when canDelete passes, and when it does not
		// the node was already removed), so this only refuses a client-supplied
		// MergedFrom naming a live element — the state #2189 showed §6.2
		// cascading from, and the one condition every reader of mergedInto
		// already requires.
		if src := t.findMergeNode(node.MergedFrom); src != nil && src.IsRemoved() {
			src.mergedInto = dest.id
		}
	}
	return stranded, released, nil
}

// tombstoneSubtree tombstones node and every live node below it with editedAt,
// returning the nodes it actually tombstoned. A node already removed keeps its
// own removedAt (LWW is decided elsewhere) and is not reported a second time,
// so calling this on a partly-tombstoned subtree is safe — which is also what
// keeps the returned set disjoint from Phase 5's toBeRemoveds, since Phase 5
// runs first and leaves every node it collected carrying a removedAt.
func (t *Tree) tombstoneSubtree(node *TreeNode, editedAt *time.Ticket) []*TreeNode {
	var removed []*TreeNode
	index.TraverseNode(node.Index, func(n *index.Node[*TreeNode], _ int) {
		if n.Value.removedAt != nil {
			return
		}
		if n.Value.remove(editedAt) {
			removed = append(removed, n.Value)
		}
	})

	return removed
}

// declaredBoundaries returns the elements the edit's own positions named as
// boundaries: the element each position declared as its parent, and every
// ancestor of that element. A range stops at those rather than covering them --
// the edit asks to merge their remaining content away, not to delete it -- so a
// concurrent merge that already moved their children where this edit would have
// put them did this edit's work rather than something it now has to undo.
//
// The declared parent is resolved with findMergeNode, not ToTreeNodes: a floor
// lookup matches on CreatedAt alone, so a ParentID naming an element-split
// product this replica does not hold (a concurrent split not yet applied, or a
// client-supplied offset) would land on the offset-0 element and hand the skip
// to a node the position never named. An exact element match treats the absent
// product as the absent lineage it is.
//
// The walk upward prefers MergedFrom over the physical parent: a prior merge
// moves a node under the merge target, so Index.Parent no longer names the
// element that enclosed it when the position was declared. Without that, an
// edit merging at more than one level keeps only its innermost boundary --
// the enclosing element the range also stops at is just as intentional, and
// tombstoning its merge-moved children is the over-deletion this skip exists
// to prevent. The boundaries map doubles as the seen set, guarding against a
// cycle in a client-supplied MergedFrom chain the way resolveMergeTarget does.
func (t *Tree) declaredBoundaries(positions ...*TreePos) map[*TreeNode]struct{} {
	boundaries := map[*TreeNode]struct{}{}
	for _, pos := range positions {
		if pos == nil {
			continue
		}
		for current := t.findMergeNode(pos.ParentID); current != nil; {
			if _, ok := boundaries[current]; ok {
				break
			}
			boundaries[current] = struct{}{}

			if src := t.findMergeNode(current.MergedFrom); src != nil {
				current = src
				continue
			}
			if current.Index.Parent == nil {
				break
			}
			current = current.Index.Parent.Value
		}
	}

	return boundaries
}

// propagateMergeDeletes tombstones children that were moved by prior
// merges when the merge-source node is fully deleted (not a merge
// boundary). It skips a source whose children a concurrent merge already
// moved into this edit's own destination AND that one of this edit's own
// positions named -- directly, or as an ancestor of the one it named; see
// declaredBoundaries: there the concurrent merge moved them where this edit
// would have, so they are not part of what this edit deletes. The list of
// moved children is recomputed on the fly from the merge target's children
// filtered by MergedFrom.
//
// What it tombstones is reported as GC pairs only, never through
// TreeEditReverseInfo: Phase 5's toBeRemoveds does not hold these nodes, and
// JS's merge propagation likewise never appends to nodesToBeRemoved, so undo
// drops them on both ports. Reporting them here would be a Go-only behavior
// change to undo, which undo-redo-go-port.md rules out: a defect is ported as
// is and fixed in both ports at once.
func (t *Tree) propagateMergeDeletes(
	fromParent *TreeNode,
	from, to *TreePos,
	toBeRemoveds []*TreeNode,
	toBeMergedNodes []*TreeNode,
	editedAt *time.Ticket,
) []GCPair {
	// Compare against the resolved destination, not fromParent: mergeNodes
	// points every source's mergedInto at the flattened target (§6.3), so a
	// chained merge (dest != fromParent) must recognize a concurrent-merge
	// boundary by dest to skip it here.
	dest := t.resolveMergeTarget(fromParent)
	// The boundaries the edit's own positions name, resolved lazily: §1.1
	// redirects a position away from a merged-away parent, so fromParent and
	// toParent no longer say which boundaries the edit asked for. Only the
	// same-destination branch below needs them.
	var declared map[*TreeNode]struct{}
	var pairs []GCPair
	for _, node := range toBeRemoveds {
		if node.mergedInto == nil ||
			slices.Contains(toBeMergedNodes, node) {
			continue
		}
		// A source whose children already sit in this edit's own destination
		// stands for a merge the edit itself asks for only when one of the
		// edit's positions named that source: the edit's range then stops at
		// the source instead of covering it, and keeping the children is what
		// makes the two replicas agree (§6.2). A source the range merely spans
		// is a plain delete of everything that was inside it, so its children
		// are tombstoned wherever the concurrent merge left them.
		if node.mergedInto.Equal(dest.id) {
			if declared == nil {
				declared = t.declaredBoundaries(from, to)
			}
			if _, ok := declared[node]; ok {
				continue
			}
		}
		// The destination has to be the node mergedInto names exactly, and an
		// element: this loop tombstones that node's children, so a floor
		// lookup landing on a neighbour is the cascade reaching live nodes no
		// merge ever moved. mergedInto is derived from the MergedFrom an
		// element payload keeps, so the pointer can be a client's.
		mergeTarget := t.findMergeNode(node.mergedInto)
		if mergeTarget == nil {
			continue
		}
		for _, targetChild := range mergeTarget.Index.Children(true) {
			child := targetChild.Value
			if child.MergedFrom == nil || !child.MergedFrom.Equal(node.id) {
				continue
			}
			if child.removedAt != nil {
				continue
			}
			if child.remove(editedAt) {
				pairs = append(pairs, GCPair{
					Parent: t,
					Child:  child,
				})
			}
			// Also tombstone descendants of the moved child.
			index.TraverseNode(child.Index, func(n *index.Node[*TreeNode], _ int) {
				if n.Value != child && n.Value.removedAt == nil {
					if n.Value.remove(editedAt) {
						pairs = append(pairs, GCPair{
							Parent: t,
							Child:  n.Value,
						})
					}
				}
			})
		}
	}
	return pairs
}

// narrowCollectRange returns the from-position Phase 3 hands to
// collectBetween: fromParent/fromLeft, or the split sibling of fromLeft that
// sits in toParent when the range crosses a concurrent element split.
func (t *Tree) narrowCollectRange(
	fromParent, fromLeft, toParent, toLeft *TreeNode,
) (*TreeNode, *TreeNode) {
	collectFromParent, collectFromLeft := fromParent, fromLeft
	if fromLeft != fromParent && fromParent != toParent {
		current := fromLeft
		var walker insNextWalker
		walker.visit(current)
		for current.InsNextID != nil {
			next := t.findFloorNode(current.InsNextID)
			if next == nil || next.IsText() {
				break
			}
			// Stop on a chain that loops back on itself; see insNextWalker.
			if !walker.visit(next) {
				break
			}
			if next.Index.Parent != nil &&
				next.Index.Parent.Value == toParent {
				// Skip narrowing when toLeft == toParent (leftmost child
				// position, offset 0). The narrowed collectFromLeft would
				// be a child at offset >= 1, a backwards range that
				// suppresses the intended merge.
				if toLeft != toParent {
					collectFromLeft = next
					collectFromParent = toParent
				}
				break
			}
			current = next
		}
	}
	return collectFromParent, collectFromLeft
}

// collectBetween collects nodes that are marked as removed or moved.
func (t *Tree) collectBetween(
	fromParent *TreeNode, fromLeft *TreeNode,
	toParent *TreeNode, toLeft *TreeNode,
	editedAt *time.Ticket,
	versionVector time.VersionVector,
) ([]*TreeNode, []*TreeNode, []*TreeNode, error) {
	var toBeRemoveds []*TreeNode
	var toBeMovedToFromParents []*TreeNode
	// toBeMergedNodes tracks element nodes at the toParent boundary whose
	// children are moved (merged) rather than deleted. Split siblings of these
	// nodes should not be cascade-deleted.
	var toBeMergedNodes []*TreeNode

	// enclosed reports whether the range covers the element whole, start and
	// end tokens both. Collected on first use; most deletes never ask.
	var enclosedNodes map[*TreeNode]bool
	// A failed collection leaves the memo empty, which would silently
	// under-apply the cascade. Record the error and fail the whole collection
	// instead of answering from a memo that was never filled.
	var enclosedErr error
	enclosed := func(n *TreeNode) bool {
		if enclosedNodes == nil {
			enclosedNodes = make(map[*TreeNode]bool)
			if err := t.traverseInPosRange(
				fromParent, fromLeft, toParent, toLeft,
				func(token index.TreeToken[*TreeNode], ended bool) {
					if token.TokenType == index.Start && ended {
						enclosedNodes[token.Node] = true
					}
				},
				true,
			); err != nil {
				enclosedErr = err
			}
		}
		return enclosedNodes[n]
	}

	if err := t.traverseInPosRange(
		fromParent, fromLeft,
		toParent, toLeft,
		func(token index.TreeToken[*TreeNode], ended bool) {
			node, tokenType := token.Node, token.TokenType
			// NOTE(hackerwins): If the node overlaps as a start token with the
			// range then we need to move the remaining children to fromParent.
			if tokenType == index.Start && !ended {
				// TODO(hackerwins): Define more clearly merge-able rules
				// between two parents. For now, we only merge two parents are
				// both element nodes having text children.
				// e.g. <p>a|b</p><p>c|d</p> -> <p>a|d</p>
				// if !fromParent.Index.HasTextChild() ||
				// 	!toParent.Index.HasTextChild() {
				// 	return
				// }

				// §4.3 Skip Concurrent Element Merge: the editor didn't
				// know about this element, so crossing into it is an
				// artifact of a concurrent split, not an intentional merge.
				if time.TicketKnown(versionVector, node.id.CreatedAt) {
					toBeMergedNodes = append(toBeMergedNodes, node)
					// Include removed children (Children(true)) so tombstones
					// move with the merge and survive as RGA anchors; a
					// concurrent insert referencing one then resolves in the
					// merge target and orders via the RGA tie-break.
					for _, child := range node.Index.Children(true) {
						toBeMovedToFromParents = append(toBeMovedToFromParents, child.Value)
					}
				}
			}

			// NOTE(sigmaith): Determine if the node's creation event was visible.
			creationKnown := time.TicketKnown(versionVector, node.id.CreatedAt)

			// NOTE(sigmaith): Determine if existing tombstone was already causally known.
			tombstoneKnown := node.removedAt != nil && time.TicketKnown(versionVector, node.removedAt)

			// NOTE(sejongk): If the node is removable or its parent is going to
			// be removed, then this node should be removed.
			// NOTE: Do not cascade-delete children of merge-boundary nodes
			// (toBeMergedNodes), because those children are moved rather than
			// deleted. Cascading into them would tombstone nodes that were
			// inserted concurrently into the merged paragraph.
			if node.canDelete(editedAt, creationKnown, tombstoneKnown) ||
				(slices.Contains(toBeRemoveds, node.Index.Parent.Value) &&
					!slices.Contains(toBeMergedNodes, node.Index.Parent.Value)) {
				// NOTE(hackerwins): If the node overlaps as an end token with the
				// range then we need to keep the node.
				if tokenType == index.Text || tokenType == index.Start {
					toBeRemoveds = append(toBeRemoveds, node)

					// §4.1 Cascade delete to split siblings created by
					// concurrent SplitElement. Only for element nodes — text
					// splits use offset-based IDs that findFloorNode already
					// resolves. Skip nodes at the merge boundary (toParent
					// side) whose children are moved rather than deleted —
					// their split siblings should survive the merge.
					if !node.IsText() && node.InsNextID != nil &&
						!slices.Contains(toBeMergedNodes, node) {
						toBeRemoveds = t.appendUnknownSplitSiblings(
							toBeRemoveds, node, versionVector, enclosed, nil, nil,
						)
					}
				}
			}
		},
		true,
	); err != nil {
		return nil, nil, nil, err
	}
	if enclosedErr != nil {
		return nil, nil, nil, enclosedErr
	}

	return toBeRemoveds, toBeMovedToFromParents, toBeMergedNodes, nil
}

// appendUnknownSplitSiblings appends to toBeRemoveds the split siblings of
// the deleted element node that the editor did not know, with their
// subtrees (§4.1).
//
// The walk passes a sibling the editor knew only if the editor saw it gone:
// removed by a change the editor knew (merged back into node, say), or
// enclosed whole by this delete. What lies past it was then inside node or
// inside the deleted range for the editor too. Any other known sibling ends
// the walk. The chain is in document order and a split product lands right
// after the node it was split off (or, under §7.8, after the same-boundary
// products ordered ahead of it), so an unknown sibling past such a sibling
// holds content the editor saw inside that sibling, not inside node. Walking
// on tombstoned it: after a split at offset 0 and a delete of the empty left
// piece, the concurrent same-boundary product that holds the moved content
// sits right after the editor's own product (yorkie-js-sdk#1408).
//
// "Saw it gone" reads the sibling's current tombstone, which keeps only the
// newest delete. GC leaves that read alone: findFloorNode matches createdAt
// exactly, so a purged ID resolves to nil and Purge's relink already points
// the predecessor past it at the same successor; and a node is purged only
// once the minimum synced version vector covers its removedAt, which is when
// the read would have answered true anyway. Restore does not: clearing a
// tombstone in place makes the walk stop where it used to pass. See the
// limitations in §4.1 of docs/design/concurrent-merge-split.md.
func (t *Tree) appendUnknownSplitSiblings(
	toBeRemoveds []*TreeNode,
	node *TreeNode,
	versionVector time.VersionVector,
	enclosed func(*TreeNode) bool,
	walked map[*TreeNode]struct{},
	steps *int,
) []*TreeNode {
	var walker insNextWalker
	walker.visit(node)
	next := t.findFloorNode(node.InsNextID)
	// Stop on a chain that loops back on itself; see insNextWalker.
	// Unbounded here would also grow toBeRemoveds without limit. With walked
	// set, also stop at a sibling an earlier walk of the same call passed:
	// the chain from there on was walked then.
	for next != nil && walker.visit(next) {
		if walked != nil {
			if _, ok := walked[next]; ok {
				break
			}
			walked[next] = struct{}{}
		}
		if steps != nil {
			*steps++
		}
		if time.TicketKnown(versionVector, next.ID().CreatedAt) {
			seenGone := next.removedAt != nil && time.TicketKnown(versionVector, next.removedAt)
			if !seenGone && !enclosed(next) {
				break
			}
		} else if !next.detached {
			// A detached sibling is a tombstone in a subtree a purge took
			// out, with nothing to remove there; the walk goes on past it, as
			// Retombstone's does (see TreeNode.detached).
			//
			// Cascade through the full subtree, not just immediate children,
			// parent before child: the order becomes the reverse operation's
			// spans, and Restore revives a child only once its parent is live
			// again (see knownRemovalAbove).
			// Iterative, as the subtree's depth is the client's to choose.
			stack := []*index.Node[*TreeNode]{next.Index}
			for len(stack) > 0 {
				n := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				toBeRemoveds = append(toBeRemoveds, n.Value)
				if steps != nil {
					*steps++
				}
				children := n.Children(true)
				for i := len(children) - 1; i >= 0; i-- {
					stack = append(stack, children[i])
				}
			}
		}
		if next.InsNextID == nil {
			break
		}
		next = t.findFloorNode(next.InsNextID)
	}
	return toBeRemoveds
}

// advanceOpts holds optional parameters for advancePastUnknownSplitSiblings.
type advanceOpts struct {
	// relaxParent skips the parent-equality check. Needed at ancestor
	// iterations of the split loop where a concurrent recursive split
	// may have moved the sibling to a different parent.
	relaxParent bool

	// skipActorID stops advancement at siblings created by this actor.
	// They are the current operation's own split products, not concurrent ones.
	skipActorID *time.ActorID
}

// insNextWalker bounds a walk of an InsNextID chain. InsNextID is a
// structural pointer that only SplitElement is supposed to set, but it also
// arrives verbatim from client-supplied bytes (api/converter/from_bytes.go,
// and — until DropSplitLinks strips it — operation contents), so a chain that
// loops back on itself would spin the applying goroutine forever while it
// holds the document lock. Every chain walk runs through one of these.
type insNextWalker struct {
	seen map[*TreeNode]struct{}
}

// visit records node and reports whether this walk had not already passed
// through it. A false result means the chain is cyclic; stop following it.
func (w *insNextWalker) visit(node *TreeNode) bool {
	if w.seen == nil {
		w.seen = make(map[*TreeNode]struct{}, 4)
	}

	if _, ok := w.seen[node]; ok {
		return false
	}
	w.seen[node] = struct{}{}

	return true
}

// advancePastUnknownSplitSiblings follows the InsNextID chain of the given
// node, advancing past element-type split siblings that the editing client
// did not know about (not in versionVector). This ensures that concurrent
// operations resolve positions after all split products the editor could
// not have seen. See advanceOpts for optional parameters.
func (t *Tree) advancePastUnknownSplitSiblings(
	node *TreeNode,
	versionVector time.VersionVector,
	opts ...advanceOpts,
) *TreeNode {
	if len(versionVector) == 0 || node == nil {
		return node
	}

	var o advanceOpts
	if len(opts) > 0 {
		o = opts[0]
	}

	current := node
	var walker insNextWalker
	walker.visit(current)
	for current.InsNextID != nil {
		next := t.findFloorNode(current.InsNextID)
		if next == nil || next.IsText() {
			break
		}

		// Stop on a chain that loops back on itself; see insNextWalker.
		if !walker.visit(next) {
			break
		}

		// Stop if the sibling has been moved to a different parent
		// (e.g., by a higher-level concurrent split).
		// Skip this check when relaxParent is true — at ancestor
		// iterations of the split loop, a concurrent recursive split may
		// have moved the sibling to a different parent. InsNextID is only
		// set by SplitElement, so its existence is sufficient evidence
		// of a split sibling (same rationale as hasUnknownSplitSibling).
		if !o.relaxParent && next.Index.Parent != current.Index.Parent {
			break
		}

		actorID := next.id.CreatedAt.ActorID()

		// Stop at siblings from the same actor as the current operation.
		// They are our own split products, not concurrent ones.
		if o.skipActorID != nil && actorID == *o.skipActorID {
			break
		}

		if l, ok := versionVector.Get(actorID); ok && l >= next.id.CreatedAt.Lamport() {
			break
		}

		// Empty unknown siblings standing right before our own product are
		// concurrent splits of the same boundary, ordered ahead of ours by
		// orderSameBoundarySplit. They are not content the editor meant to
		// keep on its left; a replica that applied them later re-parented
		// them after the boundary (§7.4), so stay in front of them here too.
		if o.skipActorID != nil && t.emptyRunReachesActor(next, *o.skipActorID, versionVector) {
			break
		}

		current = next
	}

	return current
}

// emptyRunReachesActor reports whether the InsNextID chain starting at node
// runs through empty, unknown element split siblings only and then reaches a
// node created by actorID.
func (t *Tree) emptyRunReachesActor(
	node *TreeNode,
	actorID time.ActorID,
	versionVector time.VersionVector,
) bool {
	var walker insNextWalker
	for current := node; current != nil && !current.IsText() && walker.visit(current); {
		createdAt := current.id.CreatedAt
		if createdAt.ActorID() == actorID {
			return current != node
		}
		if len(current.Index.Children(true)) > 0 {
			return false
		}
		if l, ok := versionVector.Get(createdAt.ActorID()); ok && l >= createdAt.Lamport() {
			return false
		}
		if current.InsNextID == nil {
			return false
		}
		current = t.findFloorNode(current.InsNextID)
	}

	return false
}

// orderSameBoundarySplit (§7.8) decides which node a split at offset of parent
// actually splits, so that concurrent splits of one node at one boundary land
// in the same order on every replica.
//
// SplitElement places its product directly after the node it splits. When a
// concurrent split of the same boundary has already been applied, that puts
// the products in arrival order, which differs per replica. The XML still
// matches (all but the last product are empty) but position-based operations
// that follow do not.
//
// Order them by ticket instead, newest first, as RGA orders concurrent
// inserts after the same node: skip the unknown split siblings with a newer
// ticket and split the last of them at its start. The right half lives in
// that sibling on this replica, so it moves into our product exactly as it
// would have moved out of parent on a replica that applied us first.
func (t *Tree) orderSameBoundarySplit(
	parent *TreeNode,
	offset int,
	editedAt *time.Ticket,
	versionVector time.VersionVector,
) (*TreeNode, int) {
	// A concurrent split of the same boundary took everything to the right of
	// it, so only a split at the end of parent can be one.
	if len(versionVector) == 0 || offset != len(parent.Index.Children(true)) {
		return parent, offset
	}

	target := parent
	var walker insNextWalker
	walker.visit(target)
	for target.InsNextID != nil {
		next := t.findFloorNode(target.InsNextID)
		if next == nil || next.IsText() || next.Index.Parent == nil {
			break
		}

		// Stop on a chain that loops back on itself; see insNextWalker.
		if !walker.visit(next) {
			break
		}

		// The sibling has to belong to target's own split family. The strict
		// parent-equality check §7.5 uses is too strong here — at a
		// multi-level split the sibling may already sit under the next
		// level's product — but dropping it entirely would let an InsNextID
		// that did not come from SplitElement redirect this split onto an
		// arbitrary element elsewhere in the tree.
		if !t.sharesSplitFamilyParent(target, next) {
			break
		}

		// Splitting a tombstoned sibling would make our product born
		// tombstoned, which a replica that applied us before the concurrent
		// split never does. Fall back to splitting parent, as that replica
		// did, rather than diverge on liveness.
		if next.IsRemoved() {
			break
		}

		createdAt := next.id.CreatedAt
		if createdAt.ActorID() == editedAt.ActorID() {
			break
		}
		if l, ok := versionVector.Get(createdAt.ActorID()); ok && l >= createdAt.Lamport() {
			break
		}
		if !createdAt.After(editedAt) {
			break
		}

		target = next
	}

	if target == parent {
		return parent, offset
	}
	return target, 0
}

// sharesSplitFamilyParent reports whether next sits under node's parent, or
// under a split product of that parent. A multi-level split moves a sibling
// under the next level's product, so the two parents legitimately differ —
// but only within one split family. Anything beyond that is not a split
// sibling of node, whatever its InsNextID claims.
func (t *Tree) sharesSplitFamilyParent(node, next *TreeNode) bool {
	if node.Index.Parent == nil || next.Index.Parent == nil {
		return false
	}
	if node.Index.Parent == next.Index.Parent {
		return true
	}

	var walker insNextWalker
	for current := node.Index.Parent.Value; current != nil && walker.visit(current); {
		if current == next.Index.Parent.Value {
			return true
		}
		if current.InsNextID == nil {
			return false
		}
		current = t.findFloorNode(current.InsNextID)
	}

	return false
}

// split splits element nodes for the given splitLevel and reports the net
// metadata the new elements added, which the caller accounts to docSize.Live.
// A piece born tombstoned -- split off a node a concurrent deletion already
// removed -- contributes nothing here: TreeNode.Split routes its size straight
// to docSize.GC and hands back a zero diff.
func (t *Tree) split(
	fromParent *TreeNode,
	fromLeft *TreeNode,
	splitLevel int,
	editedAt *time.Ticket,
	issueTimeTicket func() *time.Ticket,
	versionVector time.VersionVector,
) (resource.DataSize, error) {
	var diff resource.DataSize
	if splitLevel == 0 {
		return diff, nil
	}

	splitCount := 0
	parent := fromParent
	left := fromLeft
	for splitCount < splitLevel {
		// §7.5 Per-Iteration Advance: advance past unknown element
		// split siblings at the current ancestor level. Element splits
		// produce non-deterministic IDs discoverable only via InsNextID.
		// Use relaxed parent check (§7.5) because a concurrent ancestor
		// split may have moved siblings to different parents.
		// skipActorID (§7.7) prevents advancing past siblings created
		// by the current operation's own issueTimeTicket — same-actor
		// siblings are own split products, not concurrent.
		if left != parent {
			changeActorID := editedAt.ActorID()
			left = t.advancePastUnknownSplitSiblings(left, versionVector, advanceOpts{
				relaxParent: true,
				skipActorID: &changeActorID,
			})
			// If the advance moved left to a node under a different
			// parent (due to a concurrent ancestor split), update parent
			// to match so FindOffset and Split operate on the correct
			// subtree.
			if left.Index.Parent != nil && left.Index.Parent.Value != parent {
				parent = left.Index.Parent.Value
			}
		}

		if parent.Index.Parent == nil {
			break
		}

		var err error
		offset := 0
		if left != parent {
			offset, err = parent.Index.FindOffset(left.Index, true)
			if err != nil {
				return diff, err
			}

			offset++
		}
		// §7.8 Same-Boundary Ordering: a concurrent split of the same
		// boundary may already have taken the right half; order against it.
		target, offset := t.orderSameBoundarySplit(parent, offset, editedAt, versionVector)
		splitDiff, err := target.Split(t, offset, issueTimeTicket, versionVector)
		diff.Add(splitDiff)
		if err != nil {
			return diff, err
		}

		// Ascend from parent, not from target. A §7.8 retarget reorders our
		// product *within* this level; the node this operation split is
		// still parent, and the next level's boundary is "after parent" in
		// parent's own branch. The advance at the top of the next iteration
		// stops in front of the same-boundary empty run (§7.5), and
		// orderSameBoundarySplit re-applies the same ticket ordering at that
		// level, so each level orders itself against its own chain.
		left = parent
		parent = parent.Index.Parent.Value
		splitCount++
	}

	return diff, nil
}

// unknownSplitSiblings returns the element nodes a split concurrent with this
// change produced from node: its InsNextID chain, stopped at the first
// sibling the change already knew about. A style that covered node covered
// these too, because the change resolved its range while node was still one
// piece.
func (t *Tree) unknownSplitSiblings(
	node *TreeNode,
	versionVector time.VersionVector,
) []*TreeNode {
	var siblings []*TreeNode

	current := node
	var walker insNextWalker
	walker.visit(current)
	for current.InsNextID != nil {
		next := t.findFloorNode(current.InsNextID)
		if next == nil || next.IsText() {
			break
		}
		// Stop on a chain that loops back on itself; see insNextWalker.
		if !walker.visit(next) {
			break
		}
		if time.TicketKnown(versionVector, next.id.CreatedAt) {
			break
		}
		siblings = append(siblings, next)
		current = next
	}

	return siblings
}

// hasUnknownSplitSibling checks whether the given element node has a split
// sibling (via InsNextID) whose creation the editor did not know about. This
// is used to prevent styling an element via its End token when the End token
// only appears in the style range because a concurrent split extended the
// range into the split sibling.
func (t *Tree) hasUnknownSplitSibling(
	node *TreeNode,
	versionVector time.VersionVector,
) bool {
	if node.InsNextID == nil {
		return false
	}

	next := t.findFloorNode(node.InsNextID)
	if next == nil || next.IsText() {
		return false
	}

	// NOTE: Unlike advancePastUnknownSplitSiblings, we intentionally omit
	// the parent-equality check here. In multi-level splits (splitLevel>=2),
	// the split sibling may have been moved to a different parent by the
	// recursive ancestor split. The End-token guard must still fire because
	// the node WAS split — InsNextID is only set by SplitElement.
	actorID := next.id.CreatedAt.ActorID()
	lamport, ok := versionVector.Get(actorID)

	return !ok || lamport < next.id.CreatedAt.Lamport()
}

// traverseInPosRange traverses the tree in the given position range.
// If includeRemoved is true, it includes removed nodes in the traversal.
func (t *Tree) traverseInPosRange(fromParent, fromLeft, toParent, toLeft *TreeNode,
	callback func(token index.TreeToken[*TreeNode], ended bool),
	includeRemoved ...bool,
) error {
	include := len(includeRemoved) > 0 && includeRemoved[0]

	fromIdx, err := t.rangeIndex(fromParent, fromLeft, include)
	if err != nil {
		return err
	}
	toIdx, err := t.rangeIndex(toParent, toLeft, include)
	if err != nil {
		return err
	}

	// When a concurrent merge redirects the to-position into an earlier
	// part of the tree, the range becomes empty. This happens when a
	// prior merge already handled the work. Treat as a no-op.
	if fromIdx > toIdx {
		return nil
	}

	return t.IndexTree.TokensBetween(fromIdx, toIdx, callback, include)
}

// StyleByIndex applies the given attributes of the given range.
// This method uses indexes instead of a pair of TreePos for testing.
func (t *Tree) StyleByIndex(
	start, end int,
	attributes map[string]string,
	editedAt *time.Ticket,
	versionVector time.VersionVector,
) ([]GCPair, resource.DocSize, error) {
	fromPos, err := t.FindPos(start)
	if err != nil {
		return nil, resource.DocSize{}, err
	}

	toPos, err := t.FindPos(end)
	if err != nil {
		return nil, resource.DocSize{}, err
	}

	pairs, size, _, err := t.Style(fromPos, toPos, attributes, editedAt, versionVector)
	return pairs, size, err
}

// styleTargets resolves the nodes a Style or RemoveStyle applies to, in
// document order and without repeats, along with the size the boundary text
// splits added to Live.
//
// Style and RemoveStyle have to reach the same nodes — they are one range
// resolution asked to write two different things — so the resolution lives
// here once instead of in two copies that have to be kept identical by hand.
//
// A node is reported at most once even though a fully covered element is
// visited twice, on its Start token and on its End token. The second write
// was already a no-op: RHT.Set and RHT.Remove both reject a ticket that is
// not After the one they hold, which is the same ticket on the second visit.
//
// NOTE(cross-implementation): the set this returns is a replicated contract,
// not a local detail — every SDK has to reach the same nodes for the same
// change. The rules are stated without reference to Go identifiers, with
// replayable acceptance vectors, under "Port specification" in
// docs/design/concurrent-merge-split.md; change them here and that section
// and the yorkie-js-sdk port have to move with them.
func (t *Tree) styleTargets(
	from, to *TreePos,
	editedAt *time.Ticket,
	versionVector time.VersionVector,
) ([]*TreeNode, resource.DataSize, error) {
	var diff resource.DataSize

	fromParent, fromLeft, diffFrom, err := t.FindTreeNodesWithSplitText(from, editedAt, BoundaryRange)
	if err != nil {
		return nil, diff, err
	}
	diff.Add(diffFrom)

	toParent, toLeft, diffTo, err := t.FindTreeNodesWithSplitText(to, editedAt, BoundaryRange)
	if err != nil {
		return nil, diff, err
	}
	diff.Add(diffTo)

	// skipActorID for the same reason as Edit's Phase 2: a same-boundary
	// empty run has to resolve here the way the split loop resolves it.
	styleActorID := editedAt.ActorID()
	if fromLeft != fromParent {
		fromLeft = t.advancePastUnknownSplitSiblings(fromLeft, versionVector, advanceOpts{
			skipActorID: &styleActorID,
		})
	}
	if toLeft != toParent {
		toLeft = t.advancePastUnknownSplitSiblings(toLeft, versionVector, advanceOpts{
			skipActorID: &styleActorID,
		})
	}

	isVersionVectorEmpty := len(versionVector) == 0
	recoveredParent, recoveredLeft, isRecoveredInterloper, err := t.reversedFromAnchorRecovery(
		from, to, fromParent, fromLeft, toParent, toLeft, versionVector)
	if err != nil {
		return nil, diff, err
	}
	if recoveredParent != nil {
		fromParent, fromLeft = recoveredParent, recoveredLeft
	}
	shouldSkipToken, shouldSkipReached := t.styleSkipPredicate(
		from, to, fromParent, versionVector, isRecoveredInterloper)
	declaredToParent, _ := t.ToTreeNodes(to)
	declaredFromParent, declaredFromLeft := t.ToTreeNodes(from)

	var targets []*TreeNode
	seen := make(map[*TreeNode]bool)
	add := func(node *TreeNode) {
		if seen[node] {
			return
		}
		seen[node] = true
		targets = append(targets, node)
	}

	if err := t.traverseInPosRange(fromParent, fromLeft, toParent, toLeft,
		func(token index.TreeToken[*TreeNode], _ bool) {
			node := token.Node
			// A fully covered element arrives twice, on its Start token and
			// on its End token. Everything below depends only on the node,
			// so the second visit would redo the lineage walks to add what
			// the first already added — quadratic over a split chain whose
			// every member the traversal visits.
			if seen[node] {
				return
			}
			if shouldSkipToken(token) {
				return
			}

			if node.canStyle(versionVector) {
				add(node)
				// Carry the style onto split products the change could not
				// have known, so a range resolved before the split still
				// covers the piece the split cut off. Which token reached
				// the node does not decide this: a node the range covered
				// whole arrives on its Start token, or on its End token
				// alone when the range began inside it.
				for _, sibling := range t.unknownSplitSiblings(node, versionVector) {
					add(sibling)
				}
				return
			}

			// The change did not know this node was created, so it cannot
			// have named it — unless a concurrent split produced it from one
			// the change did name. An End token the range genuinely ran past
			// says the change styled that element while it was still one
			// piece, so the whole split family carries the style, including
			// the half the change knew and the traversal no longer reaches.
			if token.TokenType == index.End && !isVersionVectorEmpty &&
				declaredToParent != nil && !t.endsInside(node, declaredToParent) {
				family := t.splitFamilyOf(node, versionVector)
				// The family is reached only through the node the change
				// knew, which is the one the End-token guard judges. When
				// that guard excludes it, the split is the only reason this
				// token is in the range at all, and re-adding the family here
				// would style the very node the guard just skipped.
				//
				// The change must also have begun at or inside that node: a
				// range reaches an element it began after through neither
				// token. A split of more than one level carries the right
				// half into a new parent, past a range that began right after
				// the known node, and its End token then enters the range
				// with nothing to do with the change. That is the one shape
				// this excludes — a range that began BEFORE the known node
				// covered it whole, however the traversal lost it, and keeps
				// the closure.
				if len(family) > 0 && !shouldSkipToken(
					index.TreeToken[*TreeNode]{Node: family[0], TokenType: index.End},
				) && t.beginsAtOrInside(family[0], declaredFromParent, declaredFromLeft) {
					for _, member := range family {
						add(member)
					}
				}
			}
		}); err != nil {
		return nil, diff, err
	}

	// Add back the boundary elements the change itself reached. Without a
	// concurrent merge the traversal has already found every one of them and
	// these add nothing; with one, they are the nodes the resolved range can
	// no longer see.
	//
	// This supplements a traversal that writes nothing for a range the change
	// declared empty and nothing for one that runs backwards
	// (traverseInPosRange returns early on fromIdx > toIdx), so it must not
	// write there either. Both are reachable from outside: a remote change
	// carries whatever positions the wire gave it.
	covers, err := t.boundaryRangeCovers(from, to, fromParent, fromLeft, toParent, toLeft)
	if err != nil {
		return nil, diff, err
	}
	if covers {
		for _, token := range t.boundaryElements(from, to) {
			// The §9.4 interloper filters and the End-token split guard are
			// about which nodes the change reached, not about how the
			// traversal found them, so a boundary element answers to them too.
			if shouldSkipReached(token) {
				continue
			}
			if token.Node.canStyle(versionVector) {
				add(token.Node)
			}
		}
	}

	return targets, diff, nil
}

// boundaryRangeCovers reports whether the change declared a range that
// covers at least one element, so that reconstructing its boundary elements
// is repair rather than invention.
//
// Two ranges cover nothing. A range whose two positions are equal covers
// nothing anywhere, on any replica — there is no boundary to rebuild. A
// range that resolves backwards covers nothing here, and traverseInPosRange
// already returns early on it; the question is whether it was declared
// backwards or was collapsed by a change this replica has and the styling
// client did not. Only a removal collapses a range, so an inversion with a
// live declared ancestry on both ends is the caller's own, and the boundary
// elements would write attributes where the traversal writes none.
func (t *Tree) boundaryRangeCovers(
	from, to *TreePos,
	fromParent, fromLeft, toParent, toLeft *TreeNode,
) (bool, error) {
	if from.Equal(to) {
		return false, nil
	}

	fromIdx, err := t.rangeIndex(fromParent, fromLeft, false)
	if err != nil {
		return false, err
	}
	toIdx, err := t.rangeIndex(toParent, toLeft, false)
	if err != nil {
		return false, err
	}
	if fromIdx <= toIdx {
		return true, nil
	}

	return t.declaredAncestryRemoved(from) || t.declaredAncestryRemoved(to), nil
}

// declaredAncestryRemoved reports whether the element a position named as
// its parent, or any ancestor of it, has been removed — the only way a range
// the styling client declared forwards can resolve backwards here.
func (t *Tree) declaredAncestryRemoved(pos *TreePos) bool {
	parent, _ := t.ToTreeNodes(pos)
	for current := parent; current != nil; {
		if current.IsRemoved() {
			return true
		}
		if current.Index.Parent == nil {
			return false
		}
		current = current.Index.Parent.Value
	}

	return false
}

// boundaryElements returns the elements the change reached through a single
// token on the replica that issued it: the ancestors of the range-start
// position, whose End tokens the range ran past, and the ancestors of the
// range-end position, whose Start tokens it ran past — each chain stopping
// below the common ancestor of the two. Elements strictly between the two
// positions are reached through both tokens and need no reconstruction; the
// index traversal still finds them.
//
// Each element is returned with the token the range ran past, so the caller
// can put it through the same per-token skip checks the traversal applies.
//
// These come from the positions the change carries, which never move, so
// every replica computes the same set whatever a concurrent merge did to the
// index space. That is what the index space cannot supply: a merge that
// empties one parent into another pulls the range-end anchor back inside the
// range-start parent, and the resolved range then stops short of the End
// token it ran past before — permanently, on that replica only.
func (t *Tree) boundaryElements(from, to *TreePos) []index.TreeToken[*TreeNode] {
	fromParent, _ := t.ToTreeNodes(from)
	toParent, _ := t.ToTreeNodes(to)
	if fromParent == nil || toParent == nil {
		return nil
	}

	depth := map[*TreeNode]int{}
	for current, i := fromParent, 0; current != nil; i++ {
		depth[current] = i
		if current.Index.Parent == nil {
			break
		}
		current = current.Index.Parent.Value
	}

	// The common ancestor is the first ancestor of the range-end position
	// that also sits above the range-start position.
	var common *TreeNode
	var tokens []index.TreeToken[*TreeNode]
	for current := toParent; current != nil; {
		if _, ok := depth[current]; ok {
			common = current
			break
		}
		// The range ran past this element's Start token.
		tokens = append(tokens, index.TreeToken[*TreeNode]{Node: current, TokenType: index.Start})
		if current.Index.Parent == nil {
			break
		}
		current = current.Index.Parent.Value
	}
	if common == nil {
		return nil
	}

	var elements []index.TreeToken[*TreeNode]
	for current := fromParent; current != common; {
		// The range ran past this element's End token.
		elements = append(elements, index.TreeToken[*TreeNode]{Node: current, TokenType: index.End})
		if current.Index.Parent == nil {
			break
		}
		current = current.Index.Parent.Value
	}

	return append(elements, tokens...)
}

// splitFamilyOf walks back along InsPrevID from an element whose creation the
// change did not know, collecting the products a concurrent split made of a
// node it did know. It returns the family in document order, that known node
// first, or nil when the chain reaches no such node — a node that is simply
// new to the change, not a piece of one it styled.
//
// The walk collects backwards and reverses once at the end: prepending each
// link instead would copy the whole slice per link, which is quadratic in a
// chain length the change's own author chooses.
func (t *Tree) splitFamilyOf(
	node *TreeNode,
	versionVector time.VersionVector,
) []*TreeNode {
	reversed := []*TreeNode{node}

	current := node
	var walker insNextWalker
	walker.visit(current)
	for current.InsPrevID != nil {
		prev := t.findFloorNode(current.InsPrevID)
		if prev == nil || prev.IsText() {
			return nil
		}
		// Stop on a chain that loops back on itself; see insNextWalker.
		if !walker.visit(prev) {
			return nil
		}
		reversed = append(reversed, prev)
		if prev.canStyle(versionVector) {
			slices.Reverse(reversed)
			return reversed
		}
		current = prev
	}

	return nil
}

// Style applies the given attributes of the given range. Besides the GC
// pairs and size diff, it reports, for each key in attrs, the value that key
// held (or its absence) on the first node actually styled — see PrevAttr —
// so a reverse Style can restore that prior state. Keys are captured in
// sorted order so the result is deterministic regardless of Go's randomized
// map iteration order.
func (t *Tree) Style(
	from, to *TreePos,
	attrs map[string]string,
	editedAt *time.Ticket,
	versionVector time.VersionVector,
) ([]GCPair, resource.DocSize, []PrevAttr, error) {
	var size resource.DocSize

	targets, diff, err := t.styleTargets(from, to, editedAt, versionVector)
	size.Live.Add(diff)
	if err != nil {
		return t.drainPendingGCPairs(), size, nil, err
	}
	if len(attrs) == 0 {
		return t.drainPendingGCPairs(), size, nil, nil
	}

	var pairs []GCPair
	var prevAttrs []PrevAttr
	for i, node := range targets {
		if i == 0 {
			prevAttrs = stylePrevAttrs(node, attrs)
		}

		for key, value := range attrs {
			// canStyle admits a node removed CONCURRENTLY with this
			// style, and Tree.DataSize excludes removed nodes, so Live
			// is not holding this one's attributes -- accAttrWrite
			// books it to GC instead. Note TreeNode.DataSize counts a
			// live attribute either way; the exclusion is the
			// container's.
			accAttrWrite(
				node.SetAttr(key, value, editedAt),
				node,
				!node.IsRemoved(),
				&pairs,
				&size,
			)
		}
	}

	return append(pairs, t.drainPendingGCPairs()...), size, prevAttrs, nil
}

// RemoveStyle removes the given attributes of the given range. Besides the
// GC pairs and the ledger movement (see Style), it reports the value each removed key held on the
// first node actually visited — see PrevAttr — so a reverse operation can
// restore it. Unlike Style, a key that did not exist on that node is simply
// omitted (no Existed: false entry): removing an already-absent attribute
// has nothing to reverse. Keys are captured in sorted order for the same
// determinism reason as Style.
func (t *Tree) RemoveStyle(
	from *TreePos,
	to *TreePos,
	attrs []string,
	editedAt *time.Ticket,
	versionVector time.VersionVector,
) ([]GCPair, resource.DocSize, []PrevAttr, error) {
	var size resource.DocSize

	targets, diff, err := t.styleTargets(from, to, editedAt, versionVector)
	size.Live.Add(diff)
	if err != nil {
		return t.drainPendingGCPairs(), size, nil, err
	}
	if len(attrs) == 0 {
		return t.drainPendingGCPairs(), size, nil, nil
	}

	var pairs []GCPair
	var prevAttrs []PrevAttr
	for i, node := range targets {
		if i == 0 {
			keys := slices.Clone(attrs)
			slices.Sort(keys)
			for _, key := range keys {
				if node.Attrs != nil && node.Attrs.Has(key) {
					prevAttrs = append(prevAttrs, PrevAttr{Key: key, Value: node.Attrs.Get(key), Existed: true})
				}
			}
		}

		for _, attr := range attrs {
			// canStyle admits a node removed concurrently with this
			// change, so nodeIsLive is the third question attrGCPair asks.
			wasLive := node.Attrs != nil && node.Attrs.Has(attr)
			nodeIsLive := !node.IsRemoved()
			removal := node.RemoveAttr(attr, editedAt)

			// The tombstone carries no value, so the bytes the replaced
			// value was charging leave the ledger that was holding them.
			// See Text.RemoveStyle for the same two cases.
			if nodeIsLive {
				size.Live.Sub(removal.ValueDropped)
			} else {
				size.GC.Sub(removal.ValueDropped)
			}

			for _, rhtNode := range removal.GCNodes {
				pairs = append(pairs, attrGCPair(node, rhtNode, wasLive, nodeIsLive))
				// Only the node that replaces the live value takes a size
				// out of Live; a second one in the same call is the
				// tombstone it superseded.
				wasLive = false
			}
		}
	}

	return append(pairs, t.drainPendingGCPairs()...), size, prevAttrs, nil
}

// attrGCPair builds the GC pair for an RHT node a style edit turned into
// garbage. wasLive says whether docSize.Live was holding the value this node
// replaces: only then does collecting it take a size out of Live.
//
// A tombstone minted over a key that was absent, or over one that was already
// removed, was never in Live. Charging it to Live anyway walked the live size
// down by the attribute's size on every such edit, without bound — a
// rich-text editor toggling one key is exactly that loop — and drove it
// negative, at which point the document size limit stops applying at all.
// Those go to GC alone, by the same GCOnlySize route a born-tombstoned split
// piece takes.
// accAttrWrite books one attribute write into the ledger. It reads only what
// the write reported, never the map: a write that lost LWW installed nothing,
// so it must charge nothing, and a node visited twice in one traversal (once
// as Start and once as End) loses LWW on the second visit and is naturally
// deduped. Deciding from the map instead made Live depend on delivery order
// and, where a token-type guard suppressed only one half, drove it negative.
func accAttrWrite(
	w RHTWrite,
	parent GCParent,
	nodeIsLive bool,
	pairs *[]GCPair,
	size *resource.DocSize,
) {
	if w.Revived != nil {
		*pairs = append(*pairs, attrGCPair(parent, w.Revived, false, nodeIsLive))
	}

	// nodeIsLive is false when the container does not count this node's
	// attributes in Live at all -- a tombstoned node, which Text.DataSize and
	// Tree.DataSize both skip. (The per-node DataSize functions do NOT skip;
	// the exclusion lives in the container.) Booking either half to Live
	// there drifts it
	// by the SIGNED difference between the two values' sizes, and a shrinking
	// overwrite takes it negative.
	//
	// The bytes are not nowhere, though: they are inside the GC charge the
	// node's removal took, and collect subtracts the node's size as it stands
	// when it is purged. So the same delta goes to GC, or registration and
	// purge stop agreeing about that one node -- which a style concurrent
	// with a removal reaches on the replica that receives it, see canStyle.
	target := &size.Live
	if !nodeIsLive {
		target = &size.GC
	}

	if w.Superseded != nil {
		target.Sub(w.Superseded.DataSize())
	}
	if w.Installed != nil {
		target.Add(w.Installed.DataSize())
	}
}

// attrGCPair builds the GC pair for an attribute node, deciding which half of
// the ledger it moves through. Three cases, because the NODE holding the
// attribute may itself be a tombstone -- canStyle admits one removed
// concurrently with the change:
//
//	live attr on a live node -- in Live, so move Live -> GC the usual way.
//	live attr on a REMOVED node -- the container skips a removed node
//	  (Text.DataSize, Tree.DataSize), so it is not in Live; its bytes are
//	  already inside the GC charge taken when the node was removed, and
//	  removing the attribute shrinks that charge by exactly them. Charging
//	  them again doubles them, so the pair carries exactly zero.
//	attr that was already a tombstone -- never in Live, and not inside the
//	  node's charge either, so it carries its own size.
//
// Shared by the tree and the text halves -- both hold attributes in an RHT and
// must answer this the same way, which they historically did not (#2007).
func attrGCPair(parent GCParent, child *RHTNode, attrWasLive, nodeIsLive bool) GCPair {
	if attrWasLive && nodeIsLive {
		return GCPair{Parent: parent, Child: child}
	}

	size := child.DataSize()
	if attrWasLive {
		// Already counted inside the removed node's GC charge.
		size = resource.DataSize{}
	}

	return GCPair{Parent: parent, Child: child, GCOnlySize: &size}
}

// PosBoundary selects how a position inside a merged-away parent resolves
// in FindTreeNodesWithSplitText.
type PosBoundary int

const (
	// BoundaryInsert places the position at the insertion boundary in the
	// merge target (before the first moved child), so RGA ordering breaks
	// ties between concurrent inserts at the same anchor.
	BoundaryInsert PosBoundary = iota

	// BoundaryRange places the position right after the merge-source
	// tombstone, so a style range neither grows over nor shrinks past
	// nodes concurrently inserted at that anchor.
	BoundaryRange

	// BoundaryPureInsert is a pure insert position, both ends of an empty
	// Edit range. It resolves as BoundaryInsert does, and step 04 also passes
	// over a split product of the left element (§1.3): only a pure insert
	// lands between the element and its product. A caller has to ask for it;
	// one that does not keeps the plain walk.
	BoundaryPureInsert
)

// FindTreeNodesWithSplitText finds TreeNode of the given crdt.TreePos and
// splits the text node if the position is in the middle of the text node.
// crdt.TreePos is a position in the CRDT perspective. This is different
// from indexTree.TreePos which is a position of the tree in physical
// perspective.
// The optional boundary selects how a position inside a merged-away parent
// resolves; it defaults to BoundaryInsert.
func (t *Tree) FindTreeNodesWithSplitText(pos *TreePos, editedAt *time.Ticket, boundary ...PosBoundary) (
	*TreeNode, *TreeNode, resource.DataSize, error,
) {
	mode := BoundaryInsert
	if len(boundary) > 0 {
		mode = boundary[0]
	}

	var diff resource.DataSize
	// 01. Find the parent and left sibling nodes of the given position.
	parentNode, leftNode := t.ToTreeNodes(pos)
	if parentNode == nil || leftNode == nil {
		return nil, nil, diff, fmt.Errorf("%p: %w", pos, ErrNodeNotFound)
	}

	// 02. Determine whether the position is left-most and the exact parent
	// in the current tree.
	isLeftMost := parentNode == leftNode
	realParentNode := parentNode
	if leftNode.Index.Parent != nil && !isLeftMost {
		realParentNode = leftNode.Index.Parent.Value
	}

	// 02-1. If the parent has been tombstoned by a merge, redirect to the
	// merge destination using the forwarding pointer. The merge moved the
	// parent's children (including tombstones) here, so a left-sibling
	// anchor already resolves in the target via the normal path; only a
	// left-most position still points at the emptied tombstone. The
	// insertion boundary is the first child in the target whose MergedFrom
	// points back at the tombstoned parent (i.e. the first child moved by
	// the merge, in target child order).
	if realParentNode.IsRemoved() && isLeftMost && realParentNode.mergedInto != nil {
		// §9.3 Range Boundary at Merged-Away Anchors: a range boundary
		// resolves to the position right after the merge-source tombstone,
		// not the insertion boundary below. The insertion boundary sits
		// before the first moved child, so it would extend a style range
		// over nodes concurrently inserted between the tombstone and the
		// moved children — nodes the styling client saw outside its range
		// (after the then-live parent).
		if mode == BoundaryRange && realParentNode.Index.Parent != nil {
			return realParentNode.Index.Parent.Value, realParentNode, diff, nil
		}
		// findMergeNode, not findFloorNode: a text node is never a merge
		// destination (mergeNodes moves children into an element parent, and a
		// text node cannot hold children at all), and neither is a node the
		// pointer merely floors onto. The check is on the resolved node rather
		// than on the pointer because the pointer can come from a client: an
		// element payload keeps the MergedFrom that rebuildMergeState derives
		// mergedInto from, so a crafted one can name either here. Returned as
		// the insertion parent it would fail every later edit that resolves
		// through this tombstone, permanently and on every replica. Falling
		// through to the normal path treats the crafted lineage as the absent
		// one it is.
		mergeTarget := t.findMergeNode(realParentNode.mergedInto)
		if mergeTarget != nil && !mergeTarget.IsRemoved() {
			targetChildren := mergeTarget.Index.Children(true)
			for i, targetChild := range targetChildren {
				if targetChild.Value.MergedFrom == nil ||
					!targetChild.Value.MergedFrom.Equal(realParentNode.id) {
					continue
				}
				if i == 0 {
					return mergeTarget, mergeTarget, diff, nil
				}
				return mergeTarget, targetChildren[i-1].Value, diff, nil
			}
			// Fallback: insert at leftmost of merge target.
			return mergeTarget, mergeTarget, diff, nil
		}
	}

	// 03. Split text node if the left node is text node, unless the position
	// is past purged pieces (see afterPurgedText): those were tombstones every
	// replica had seen removed, so a position after them is a position after
	// the floor piece, and there is nothing to split. Any other offset past
	// the piece is out of range and fails the split.
	if leftNode.IsText() && !t.afterPurgedText(leftNode, pos.LeftSiblingID) {
		diff2, err := leftNode.Split(t, pos.LeftSiblingID.Offset-leftNode.id.Offset, nil, nil)
		if err != nil {
			return nil, nil, diff, err
		}
		diff.Add(diff2)
	}

	// 04. Find the appropriate left node. If some nodes are inserted at the
	// same position concurrently, then we need to find the appropriate left
	// node. This is similar to RGA.
	idx := 0
	if !isLeftMost {
		idx = realParentNode.Index.OffsetOfChild(leftNode.Index) + 1
	}

	// A split product of the left element is not a concurrent insert at this
	// position even though its createdAt is newer: it is the right half of the
	// element the position names, and §7.4 places a position after the left
	// half before that right half. Taking it as the anchor would move the
	// position past content the editor saw on the far side of the element.
	//
	// Passed over rather than walked away from: the product is skipped as an
	// anchor, but the walk continues through it. Ending the walk here would
	// also hide the genuine concurrent inserts that follow it, which a replica
	// that has not applied the split still walks past -- it sees them as
	// siblings of the unsplit element -- so the two sides would anchor the
	// same insert on different nodes.
	//
	// Only a pure insert position does this (BoundaryPureInsert). Any other
	// boundary after the element -- a style range's (BoundaryRange) or
	// either end of a non-empty Edit range (BoundaryInsert) -- was drawn
	// after the whole element its client saw, unsplit, so the walk takes the
	// product as it always has, and the caller's split handling starts from
	// there.
	var isSplitProduct func(*TreeNode) bool
	if mode == BoundaryPureInsert {
		isSplitProduct = t.splitProductTest(leftNode, isLeftMost)
	}
	parentChildren := realParentNode.Index.Children(true)
	for i := idx; i < len(parentChildren); i++ {
		next := parentChildren[i].Value
		if !next.id.CreatedAt.After(editedAt) {
			break
		}
		if isSplitProduct != nil && isSplitProduct(next) {
			continue
		}
		leftNode = next
	}

	return realParentNode, leftNode, diff, nil
}

// afterPurgedText reports whether id, a left sibling ID that resolved to the
// text piece floor, names an offset inside a gap of floor's insertion that
// only its own chain explains, so that step 03 anchors after floor rather
// than splitting it out of range.
//
// The gap is the run between floor and next, floor's neighbour in the
// insertion's InsPrevID/InsNextID chain: next has to be registered under
// exactly floor.InsNextID, be text of the same insertion, and point back at
// floor. An insertion's pieces cover its offsets without holes, and splits
// keep neighbours contiguous, so this replica's chain holds such a gap only
// where it took pieces out and joined the survivors: Purge, or a restore that
// recreated a piece and relinkRecreatedText spliced it in. The offset is
// accepted when floor's end < offset <= next's start. A position names the
// character before its offset, so offset next.start is the one after the
// last character of the gap; ToTreeNodes resolves that ID, next's own, to
// the piece before next, which is floor. Every other offset past floor is
// rejected: past the end of an insertion's last surviving piece (nothing
// tells a purged tail from an offset the insertion never had), or with
// neighbours that are contiguous or not linked to each other both ways.
//
// The tree records no length of what it purged, so a gap's existence is the
// only evidence; its length cannot be checked against content that existed.
func (t *Tree) afterPurgedText(floor *TreeNode, id *TreeNodeID) bool {
	floorEnd := floor.id.Offset + floor.Len()
	if id.Offset <= floorEnd || floor.InsNextID == nil {
		return false
	}

	next := t.findFloorNode(floor.InsNextID)
	if next == nil || next == floor || !next.IsText() || !next.id.Equal(floor.InsNextID) ||
		next.id.CreatedAt.Compare(floor.id.CreatedAt) != 0 ||
		next.InsPrevID == nil || !next.InsPrevID.Equal(floor.id) {
		return false
	}
	return floorEnd < next.id.Offset && id.Offset <= next.id.Offset
}

// toTreePos converts the given crdt.TreePos to local index.TreePos<CRDTTreeNode>.
// If includeRemoved is true, it includes removed nodes in the calculation.
func (t *Tree) toTreePos(parentNode, leftNode *TreeNode, includeRemoved ...bool) (*index.TreePos[*TreeNode], error) {
	include := len(includeRemoved) > 0 && includeRemoved[0]
	return t.toTreePosIn(parentNode, leftNode, include, false)
}

// toTreePosIn is toTreePos. With outOfSight set, a live parent under a
// removed ancestor resolves from the topmost removed one, as a replica that
// has released it resolves it; only the range an operation acts on needs
// that (see traverseInPosRange), so positions reported to users keep their
// index.
func (t *Tree) toTreePosIn(
	parentNode, leftNode *TreeNode, include, outOfSight bool,
) (*index.TreePos[*TreeNode], error) {
	if parentNode == nil || leftNode == nil {
		return nil, nil
	}

	// A parent in a subtree a purge took out has no index in the attached
	// tree. The position resolves to the place the subtree was purged from,
	// where a replica that has not purged it counts the subtree from: the
	// tokens a range ending inside it covers on that replica are the
	// attached ones before that place, and the subtree's own, which are
	// tombstones (see TreeNode.detached).
	if parentNode.detached {
		slotParent, before, ok := t.purgedSlot(parentNode)
		if !ok {
			return nil, fmt.Errorf("least alive ancestor of %s: %w", parentNode.ID().toIDString(), ErrNodeNotFound)
		}
		if include || !slotParent.IsRemoved() {
			offset := 0
			for _, child := range slotParent.Index.Children(true) {
				if before != nil && child.Value == before {
					break
				}
				if include || !child.Value.IsRemoved() {
					offset++
				}
			}
			return &index.TreePos[*TreeNode]{Node: slotParent.Index, Offset: offset}, nil
		}
		parentNode = slotParent
	}

	if !include && outOfSight {
		// A live parent under a removed ancestor is out of sight too, as on
		// a replica that collected the ancestor and released it: resolve
		// from the topmost removed ancestor, as for a removed parent.
		for n := parentNode.Index.Parent; n != nil; n = n.Parent {
			if n.Value.IsRemoved() {
				parentNode = n.Value
			}
		}
	}

	if !include && parentNode.IsRemoved() {
		// If parentNode is removed, treePos is the position of its least alive ancestor.
		var childNode *TreeNode
		for parentNode.IsRemoved() {
			childNode = parentNode
			// If the subtree has been detached by garbage collection, the walk
			// can run off the top of it. Report it instead of dereferencing nil.
			if childNode.Index.Parent == nil {
				return nil, fmt.Errorf(
					"least alive ancestor of %s: %w",
					childNode.ID().toIDString(),
					ErrNodeNotFound,
				)
			}
			parentNode = childNode.Index.Parent.Value
		}

		offset, err := parentNode.Index.FindOffset(childNode.Index, include)
		if err != nil {
			return nil, nil
		}

		return &index.TreePos[*TreeNode]{
			Node:   parentNode.Index,
			Offset: offset,
		}, nil
	}

	if parentNode == leftNode {
		return &index.TreePos[*TreeNode]{
			Node:   leftNode.Index,
			Offset: 0,
		}, nil
	}

	// Find the closest existing leftSibling node.
	offset, err := parentNode.Index.FindOffset(leftNode.Index, include)
	if err != nil {
		return nil, err
	}

	if include || !leftNode.IsRemoved() {
		if leftNode.IsText() {
			return &index.TreePos[*TreeNode]{
				Node:   leftNode.Index,
				Offset: leftNode.Index.PaddedLength(include),
			}, nil
		}
		offset++
	}

	return &index.TreePos[*TreeNode]{
		Node:   parentNode.Index,
		Offset: offset,
	}, nil
}

// rangeIndex is ToIndex for an end of the range an operation acts on: a
// position whose live parent sits under a removed ancestor counts from the
// topmost removed one, so the range reaches the same attached content as on
// a replica that has released that parent (see toTreePosIn).
func (t *Tree) rangeIndex(parentNode, leftNode *TreeNode, include bool) (int, error) {
	treePos, err := t.toTreePosIn(parentNode, leftNode, include, true)
	if err != nil {
		return 0, err
	}
	if treePos == nil {
		return -1, nil
	}
	return t.IndexTree.IndexOf(treePos, include)
}

// ToIndex converts the given CRDTTreePos to the index of the tree.
// If includeRemoved is true, it includes removed nodes in the calculation.
func (t *Tree) ToIndex(parentNode, leftSiblingNode *TreeNode, includeRemoved ...bool) (int, error) {
	include := len(includeRemoved) > 0 && includeRemoved[0]

	treePos, err := t.toTreePos(parentNode, leftSiblingNode, include)
	if err != nil {
		return 0, err
	}
	if treePos == nil {
		return -1, nil
	}

	idx, err := t.IndexTree.IndexOf(treePos, include)
	if err != nil {
		return 0, err
	}

	return idx, nil
}

// ToPath returns path from given CRDTTreePos
func (t *Tree) ToPath(parentNode, leftSiblingNode *TreeNode) ([]int, error) {
	treePos, err := t.toTreePos(parentNode, leftSiblingNode)
	if err != nil {
		return nil, err
	}
	if treePos == nil {
		return nil, nil
	}

	return t.IndexTree.TreePosToPath(treePos)
}

// dropDuplicateContents returns the contents that would not put a second node
// under an ID already in the tree.
//
// Content created by this edit carries this edit's lamport and actor, so a
// content node whose ID names an older change is a copy of a node that already
// exists — what the SDK's copy-reinsert undo sends when it reverses a
// deletion. Inserting it would leave two nodes under one identity, so the copy
// is dropped and the rest of the edit applies.
//
// Content from this edit's own change is kept even when its ID collides: the
// delimiters an element split consumes are simulated rather than replayed (see
// the note in operations.TreeEdit), so an ID issued here can legitimately
// collide, and dropping it would lose a node the client already inserted.
//
// Dropping rather than failing is deliberate: such changes are already in the
// history of existing documents, and a change the server refuses to replay is
// a document that can never be loaded again. The visible cost is that an undo
// from those clients does not restore the text; the repair is for the client
// to reverse deletions through the restore path, which revives the node under
// its original identity instead of copying it.
//
// A collision anywhere in a content node's subtree drops that whole subtree,
// on the grounds that a copy is copied whole. A partial drop would insert an
// element whose text the tree already holds elsewhere.
func (t *Tree) dropDuplicateContents(contents []*TreeNode, editedAt *time.Ticket) []*TreeNode {
	kept := make([]*TreeNode, 0, len(contents))
	for _, content := range contents {
		reused := false
		index.TraverseNode(content.Index, func(node *index.Node[*TreeNode], _ int) {
			createdAt := node.Value.id.CreatedAt
			if createdAt.Lamport() == editedAt.Lamport() &&
				createdAt.ActorID().Compare(editedAt.ActorID()) == 0 {
				return
			}

			if existing := t.findFloorNode(node.Value.id); existing != nil &&
				existing.id.Equal(node.Value.id) {
				reused = true
			}
		})
		if !reused {
			kept = append(kept, content)
		}
	}

	return kept
}

// putNode registers the node under its ID in NodeMapByID, keeping a live node
// over a tombstone when both claim the same ID.
//
// Documents written by older clients can carry two nodes under one ID (an undo
// that re-inserted a deleted piece by copy). Plain Put lets the winner depend
// on the order the nodes were put — operation order on a live replica,
// document order on one rebuilt from a snapshot — so after a restart the same
// position resolves to a different node and its offset can fall outside that
// node. Keeping the live one makes both orders agree for a live/tombstone
// pair, which is the shape those documents carry.
//
// Two nodes in the same state keep the last-put-wins behavior, and stay
// order-dependent: element IDs issued for a split can legitimately collide
// with an inserted node's ID (see the delimiter note in operations.TreeEdit),
// and that resolution order is what the rest of the tree already assumes.
//
// A node this refuses stays in the index tree while another node answers for
// its ID, so it is reachable by traversal but not by lookup. That is the
// intended trade for a duplicate: the alternative is unregistering a node that
// positions still resolve through.
func (t *Tree) putNode(node *TreeNode) {
	// A node placed in a detached subtree is not reachable from the root,
	// so it is registered with the other detached nodes.
	if node.detached {
		t.noteMergeStamp(node)
		// The same rule as for attached nodes below: a live node keeps its
		// ID over a tombstone, which for detached nodes, all tombstones,
		// leaves the last put.
		key, existing := t.detachedByID.Floor(node.id)
		if existing != nil && existing != node && key.Equal(node.id) &&
			node.IsRemoved() && !existing.IsRemoved() {
			return
		}
		t.detachedByID.Put(node.id, node)
		return
	}

	// Indexed whether or not it gets the ID: a refused node stays in the
	// index tree, so the lineage it carries is still read (rebuildMergeState
	// walks the tree) and has to be dissolvable.
	t.noteMergeStamp(node)

	key, existing := t.NodeMapByID.Floor(node.id)
	if existing != nil && existing != node && key.Equal(node.id) &&
		node.IsRemoved() && !existing.IsRemoved() {
		return
	}

	t.NodeMapByID.Put(node.id, node)
}

// forgetMergeStamp drops node's entry from mergeStamps, for a node leaving
// the tree.
func (t *Tree) forgetMergeStamp(node *TreeNode) {
	key := node.MergedFrom.toIDString()
	if stamped := t.mergeStamps[key]; stamped != nil {
		delete(stamped, node)
		if len(stamped) == 0 {
			delete(t.mergeStamps, key)
		}
	}
}

// noteMergeStamp adds node to mergeStamps if it carries a MergedFrom.
func (t *Tree) noteMergeStamp(node *TreeNode) {
	if node.MergedFrom == nil {
		return
	}
	if t.mergeStamps == nil {
		t.mergeStamps = make(map[string]map[*TreeNode]struct{})
	}
	key := node.MergedFrom.toIDString()
	if t.mergeStamps[key] == nil {
		t.mergeStamps[key] = make(map[*TreeNode]struct{})
	}
	t.mergeStamps[key][node] = struct{}{}
}

// findMergeNode resolves one end of a merge relation -- the source a
// MergedFrom names, or the destination a mergedInto names -- and returns nil
// unless the id names such a node exactly.
//
// Two requirements, neither of which findFloorNode makes. First the exact ID:
// the floor lookup answers with the node that *contains* the offset, which is
// what a position interior to a split node needs, but both ends of a merge are
// recorded by mergeNodes as a node's own ID, so a floor that lands on a
// neighbour is a pointer to a node the merge never touched. Second the element
// type: a merge moves children out of one element parent into another, and a
// text node can hold no children at all.
//
// Both matter because these pointers are not all server-derived. MergedFrom is
// retained on an element payload (Set/Add/ArraySet) -- DropSplitLinksInElement
// deliberately keeps the lineage a reverse-of-Remove legitimately carries --
// so on that path the field is client-supplied, persists on the node inside
// the live document, and mergedInto is derived from it. Every reader goes
// through here rather than through findFloorNode so the check holds for the
// whole lifetime of the value and not only at the decode that first saw it:
// otherwise a made-up offset becomes a forwarding pointer planted on an
// unrelated node, which a later, innocent delete follows in
// propagateMergeDeletes to cascade-tombstone that node's live children.
func (t *Tree) findMergeNode(id *TreeNodeID) *TreeNode {
	if id == nil {
		return nil
	}

	node := t.findFloorNode(id)
	if node == nil || !node.id.Equal(id) || node.IsText() {
		return nil
	}

	return node
}

// findFloorNode returns node from given id.
func (t *Tree) findFloorNode(id *TreeNodeID) *TreeNode {
	key, node := t.NodeMapByID.Floor(id)
	if dkey, dnode := t.detachedByID.Floor(id); dnode != nil && (node == nil || dkey.Compare(key) > 0) {
		key, node = dkey, dnode
	}

	if node == nil || key.CreatedAt.Compare(id.CreatedAt) != 0 {
		return nil
	}

	return node
}

// ToTreeNodes converts the pos to parent and left sibling nodes.
// If the position points to the middle of a node, then the left sibling node
// is the node that contains the position. Otherwise, the left sibling node is
// the node that is the left of the position.
func (t *Tree) ToTreeNodes(pos *TreePos) (*TreeNode, *TreeNode) {
	parentNode := t.findFloorNode(pos.ParentID)
	leftNode := t.findFloorNode(pos.LeftSiblingID)

	if parentNode == nil || leftNode == nil {
		return nil, nil
	}

	// NOTE(hackerwins): If the left node and the parent node are the same,
	// it means that the position is the left-most of the parent node.
	// We need to skip finding the left of the position.
	if !pos.LeftSiblingID.Equal(parentNode.id) &&
		pos.LeftSiblingID.Offset > 0 &&
		pos.LeftSiblingID.Offset == leftNode.id.Offset &&
		leftNode.InsPrevID != nil {
		return parentNode, t.findFloorNode(leftNode.InsPrevID)
	}

	return parentNode, leftNode
}

// ToTreeNodeForTest returns the JSON of this tree for debugging.
func (t *Tree) ToTreeNodeForTest() TreeNodeForTest {
	return ToTreeNodeForTest(t.Root())
}

// PathToPos returns the position of the given path
func (t *Tree) PathToPos(path []int) (*TreePos, error) {
	idx, err := t.IndexTree.PathToIndex(path)
	if err != nil {
		return nil, err
	}

	pos, err := t.FindPos(idx)
	if err != nil {
		return nil, err
	}

	return pos, nil
}

// ToTreeNodeForTest returns the JSON of this tree for debugging.
func ToTreeNodeForTest(node *TreeNode) TreeNodeForTest {
	if node.IsText() {
		currentNode := node
		return TreeNodeForTest{
			Type:      currentNode.Type(),
			Value:     currentNode.Value,
			Size:      currentNode.Len(),
			IsRemoved: currentNode.IsRemoved(),
		}
	}

	var children []TreeNodeForTest
	for _, child := range node.Index.Children() {
		children = append(children, ToTreeNodeForTest(child.Value))
	}

	return TreeNodeForTest{
		Type:      node.Type(),
		Children:  children,
		Size:      node.Len(),
		IsRemoved: node.IsRemoved(),
	}
}

// ToXML returns the XML representation of this tree.
func ToXML(node *TreeNode) string {
	return index.ToXML(node.Index)
}

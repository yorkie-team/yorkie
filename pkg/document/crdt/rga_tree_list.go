/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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
	"fmt"
	"strings"

	"github.com/yorkie-team/yorkie/pkg/document/resource"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/treelist"
)

// ElementEntry is the stable identity of an element in the RGATreeList.
// It holds the element value and tracks which position node currently owns it.
type ElementEntry struct {
	elem         Element
	positionNode *RGATreeListNode
	posMovedAt   *time.Ticket
}

// RGATreeListNode is a position slot in the RGA linked list.
// When elementEntry is nil, it is a dead slot abandoned by a move.
type RGATreeListNode struct {
	indexNode    *treelist.Node[*RGATreeListNode]
	elementEntry *ElementEntry
	createdAt    *time.Ticket
	removedAt    *time.Ticket

	// origin is the position ticket of the slot the creating operation named
	// as its anchor -- the node's parent in the RGA insertion tree, not its
	// left neighbour after the forward skip resolved. It is what lets
	// findNextBeforeExecutedAt decide where an insert lands from what the
	// surviving nodes say about themselves rather than from which tombstones
	// are still linked. Nil only on the dummy head and on nodes restored from
	// a snapshot written before origins were recorded.
	origin *time.Ticket

	prev *RGATreeListNode
	next *RGATreeListNode
}

func newRGATreeListNode(elem Element) *RGATreeListNode {
	entry := &ElementEntry{
		elem: elem,
	}
	node := &RGATreeListNode{
		prev:         nil,
		next:         nil,
		elementEntry: entry,
		createdAt:    elem.CreatedAt(),
	}
	entry.positionNode = node
	node.indexNode = treelist.NewNode(node)

	return node
}

// newBarePositionNode creates a position node without an element (used for move).
func newBarePositionNode(createdAt *time.Ticket) *RGATreeListNode {
	node := &RGATreeListNode{
		prev:      nil,
		next:      nil,
		createdAt: createdAt,
	}
	node.indexNode = treelist.NewNode(node)

	return node
}

func newRGATreeListNodeAfter(prev *RGATreeListNode, elem Element) *RGATreeListNode {
	newNode := newRGATreeListNode(elem)
	prevNext := prev.next

	prev.next = newNode
	newNode.prev = prev
	newNode.next = prevNext
	if prevNext != nil {
		prevNext.prev = newNode
	}

	return prev.next
}

func insertNodeAfter(prev *RGATreeListNode, newNode *RGATreeListNode) {
	prevNext := prev.next

	prev.next = newNode
	newNode.prev = prev
	newNode.next = prevNext
	if prevNext != nil {
		prevNext.prev = newNode
	}
}

// Element returns the element of this node.
func (n *RGATreeListNode) Element() Element {
	if n.elementEntry == nil {
		return nil
	}
	return n.elementEntry.elem
}

// CreatedAt returns the creation time of this node.
// If the node has an element, it returns the element's createdAt for backward
// compatibility with lookups by element ID.
func (n *RGATreeListNode) CreatedAt() *time.Ticket {
	if n.elementEntry != nil {
		return n.elementEntry.elem.CreatedAt()
	}
	return n.createdAt
}

// PositionedAt returns the time this element was positioned.
// For live nodes, the position register (posMovedAt) is the source of truth.
// For dead nodes (no element), the position node's own createdAt is used.
func (n *RGATreeListNode) PositionedAt() *time.Ticket {
	if n.elementEntry != nil {
		if n.elementEntry.posMovedAt != nil {
			return n.elementEntry.posMovedAt
		}
		return n.elementEntry.elem.CreatedAt()
	}
	return n.createdAt
}

// String returns the string representation of this node.
func (n *RGATreeListNode) String() string {
	if n.elementEntry == nil {
		return ""
	}
	return n.elementEntry.elem.Marshal()
}

// IsRemoved returns true if this node is a dead position (no element) or its element was deleted.
func (n *RGATreeListNode) IsRemoved() bool {
	if n.elementEntry == nil {
		return true
	}
	return n.elementEntry.elem.RemovedAt() != nil
}

// PositionCreatedAt returns the position node's own createdAt.
func (n *RGATreeListNode) PositionCreatedAt() *time.Ticket {
	return n.createdAt
}

// Origin returns the position ticket of the slot this node's creating
// operation anchored on. Nil on the dummy head and on nodes restored from a
// snapshot that predates origin recording.
func (n *RGATreeListNode) Origin() *time.Ticket {
	return n.origin
}

// PositionMovedAt returns the LWW timestamp of the element's move into this
// position. Nil for insert-created positions.
func (n *RGATreeListNode) PositionMovedAt() *time.Ticket {
	if n.elementEntry == nil {
		return nil
	}
	return n.elementEntry.posMovedAt
}

// IDString returns a unique identifier for this position node (for GC).
func (n *RGATreeListNode) IDString() string {
	return n.createdAt.Key()
}

// RemovedAt returns the time this dead position node was removed (for GC).
func (n *RGATreeListNode) RemovedAt() *time.Ticket {
	return n.removedAt
}

// DataSize returns the size of this position node's metadata (for GC).
func (n *RGATreeListNode) DataSize() resource.DataSize {
	meta := time.TicketSize
	if n.removedAt != nil {
		meta += time.TicketSize
	}
	return resource.DataSize{
		Data: 0,
		Meta: meta,
	}
}

// RGATreeList is a list with improved index-based lookup in RGA. RGA is a
// linked list that has a logical clock and tombstone. Since RGA is composed as
// a linked list, index-based element search is slow, O(n). To optimise for fast
// insertions and removals at any index in the list, RGATreeList has a tree.
type RGATreeList struct {
	dummyHead             *RGATreeListNode
	last                  *RGATreeListNode
	nodeMapByIndex        *treelist.Tree[*RGATreeListNode]
	nodeMapByCreatedAt    map[string]*RGATreeListNode
	elementMapByCreatedAt map[string]*ElementEntry
}

// NewRGATreeList creates a new instance of RGATreeList.
func NewRGATreeList() *RGATreeList {
	// NOTE(hackerwins): A dummy value can not return an error, so we can ignore
	// the error check.
	dummyValue, _ := NewPrimitive(0, time.InitialTicket)

	dummyValue.SetRemovedAt(time.InitialTicket)
	dummyHead := newRGATreeListNode(dummyValue)
	nodeMapByIndex := treelist.NewTree(dummyHead.indexNode)
	nodeMapByCreatedAt := make(map[string]*RGATreeListNode)
	nodeMapByCreatedAt[dummyHead.CreatedAt().Key()] = dummyHead
	elementMapByCreatedAt := make(map[string]*ElementEntry)

	return &RGATreeList{
		dummyHead:             dummyHead,
		last:                  dummyHead,
		nodeMapByIndex:        nodeMapByIndex,
		nodeMapByCreatedAt:    nodeMapByCreatedAt,
		elementMapByCreatedAt: elementMapByCreatedAt,
	}
}

// Marshal returns the JSON encoding of this RGATreeList.
func (a *RGATreeList) Marshal() string {
	sb := strings.Builder{}
	sb.WriteString("[")

	current := a.dummyHead.next
	isFirst := true
	for current != nil {
		if !current.IsRemoved() {
			if isFirst {
				isFirst = false
			} else {
				sb.WriteString(",")
			}
			sb.WriteString(current.elementEntry.elem.Marshal())
		}

		current = current.next
	}

	sb.WriteString("]")

	return sb.String()
}

// Add adds the given element at the last.
//
// The anchor must be the last node's POSITION identity (LastCreatedAt), not its
// element identity (last.CreatedAt). When the last element was moved here, its
// element createdAt maps in nodeMapByCreatedAt to the element's now-dead
// original position node, so anchoring on element identity would insert after
// that stale dead slot instead of the tail — and the forward-skip in
// insertAfter would then place each successive Add before the previous one,
// reversing the appended run (yorkie#1948, seen only via snapshot restore and
// DeepCopy, which rebuild the list through Add).
func (a *RGATreeList) Add(elem Element) error {
	return a.InsertAfter(a.LastCreatedAt(), elem, nil)
}

// Restore appends one position node in physical order, carrying the whole
// position identity a snapshot or a deep copy holds: the position ticket, the
// anchor the creating operation named, the move ticket and the removal ticket.
// It is the single restoration primitive -- appending through Add would
// re-derive the anchor from the current tail, which is not the anchor the
// operation actually named and would therefore rebuild a different insertion
// tree.
//
// elem may be nil, which restores a dead position slot abandoned by a move.
// origin may be nil only for snapshots written before origins were recorded;
// see fromJSONArray for what that costs.
func (a *RGATreeList) Restore(
	elem Element,
	posCreatedAt, origin, posMovedAt, removedAt *time.Ticket,
) error {
	node := newBarePositionNode(posCreatedAt)
	node.origin = origin
	node.removedAt = removedAt

	if elem != nil {
		entry := &ElementEntry{elem: elem, posMovedAt: posMovedAt}
		node.elementEntry = entry
		entry.positionNode = node
		a.elementMapByCreatedAt[elem.CreatedAt().Key()] = entry
	}

	prevNode := a.last
	insertNodeAfter(prevNode, node)
	a.last = node

	a.nodeMapByIndex.InsertAfter(prevNode.indexNode, node.indexNode)
	a.nodeMapByCreatedAt[posCreatedAt.Key()] = node
	return nil
}

// Nodes returns an array of live nodes (with elements) in this RGATreeList.
// Dead position nodes (abandoned by moves) are excluded.
// TODO: If we encounter performance issues, we need to replace this with other solution.
func (a *RGATreeList) Nodes() []*RGATreeListNode {
	var nodes []*RGATreeListNode
	current := a.dummyHead.next
	for current != nil {
		if current.elementEntry != nil {
			nodes = append(nodes, current)
		}
		current = current.next
	}

	return nodes
}

// AllNodes returns all nodes including dead position nodes.
func (a *RGATreeList) AllNodes() []*RGATreeListNode {
	var nodes []*RGATreeListNode
	current := a.dummyHead.next
	for current != nil {
		nodes = append(nodes, current)
		current = current.next
	}

	return nodes
}

// LastCreatedAt returns the position identity an append should anchor on: the
// last position that still holds a live element, or the dummy head when the
// list holds none.
//
// It deliberately does not return the last PHYSICAL position. A tombstone or a
// dead slot at the tail is collectable the moment its removal is causally
// stable, and an append that named it would then fail to apply on every replica
// that collected -- no concurrency needed, since the appending client already
// knows the tail is dead. An operation may only anchor on a position that is
// alive when the operation is created; such a position cannot be collected
// before the operation has been delivered everywhere.
//
// yorkie#1948 is still respected: this is a position identity, not an element
// identity, so a moved last element resolves to the slot it now occupies rather
// than to the dead slot it left behind.
func (a *RGATreeList) LastCreatedAt() *time.Ticket {
	for node := a.last; node != a.dummyHead; node = node.prev {
		if !node.IsRemoved() {
			return node.PositionCreatedAt()
		}
	}
	return a.dummyHead.PositionCreatedAt()
}

// InsertAfter inserts the given element after the given previous element.
func (a *RGATreeList) InsertAfter(prevCreatedAt *time.Ticket, elem Element, executedAt *time.Ticket) error {
	if executedAt == nil {
		executedAt = elem.CreatedAt()
	}
	_, err := a.insertAfter(prevCreatedAt, elem, executedAt)
	return err
}

// Get returns the element of the given index.
func (a *RGATreeList) Get(idx int) (*RGATreeListNode, error) {
	treelistNode, err := a.nodeMapByIndex.Find(idx)
	if err != nil {
		return nil, err
	}
	return treelistNode.Value(), nil
}

// DeleteByCreatedAt deletes the given element.
func (a *RGATreeList) DeleteByCreatedAt(createdAt *time.Ticket, deletedAt *time.Ticket) (*RGATreeListNode, error) {
	entry, ok := a.elementMapByCreatedAt[createdAt.Key()]
	if !ok {
		return nil, fmt.Errorf("DeleteByCreatedAt %s: %w", createdAt.Key(), ErrChildNotFound)
	}

	node := entry.positionNode

	// A removal that loses to one already recorded on the element changes
	// nothing, and reporting the node anyway invites the caller to register a
	// GC pair for an element this removal did not tombstone. Report only the
	// removal that took: `Remove` returns false when an earlier removal
	// already won, and the caller decides what to do with the nil.
	alreadyRemoved := node.IsRemoved()
	if !entry.elem.Remove(deletedAt) {
		return nil, nil
	}
	if !alreadyRemoved {
		a.nodeMapByIndex.UpdateWeight(node.indexNode)
	}
	return node, nil
}

// Len returns length of this RGATreeList.
func (a *RGATreeList) Len() int {
	return a.nodeMapByIndex.Len()
}

// ToTestString returns a String containing the metadata of the node id
// for debugging purpose.
func (a *RGATreeList) ToTestString() string {
	return a.nodeMapByIndex.ToTestString()
}

// Delete deletes the node of the given index.
func (a *RGATreeList) Delete(idx int, deletedAt *time.Ticket) (*RGATreeListNode, error) {
	target, err := a.Get(idx)
	if err != nil {
		return nil, err
	}
	return a.DeleteByCreatedAt(target.CreatedAt(), deletedAt)
}

// MoveAfter moves the given `createdAt` element after the `prevCreatedAt`
// element using LWW (Last-Writer-Wins) position register semantics.
// Returns the dead position node (if any) for GC registration.
func (a *RGATreeList) MoveAfter(prevCreatedAt, createdAt, executedAt *time.Ticket) (*RGATreeListNode, error) {
	if _, _, ok := a.resolveAnchor(prevCreatedAt); !ok {
		return nil, fmt.Errorf("MoveAfter %s: %w", prevCreatedAt.Key(), ErrChildNotFound)
	}

	entry, ok := a.elementMapByCreatedAt[createdAt.Key()]
	if !ok {
		return nil, fmt.Errorf("MoveAfter %s: %w", createdAt.Key(), ErrChildNotFound)
	}

	// LWW check: if a newer move already won, this move is discarded.
	// But we still create the position node so that operations referencing
	// this move's position (e.g., inserts after it) can find it.
	if entry.posMovedAt != nil && !executedAt.After(entry.posMovedAt) {
		if _, ok := a.nodeMapByCreatedAt[executedAt.Key()]; ok {
			return nil, nil
		}

		deadPosNode, err := a.insertPositionAfter(prevCreatedAt, executedAt)
		if err != nil {
			return nil, err
		}
		// The slot is stamped with the move that BEAT it, not with its own
		// ticket. What killed it is the winning move, and collection reads
		// removedAt to decide whether every replica knows about the death.
		// Stamping the loser's own ticket claims a death that everyone already
		// knows about at the instant the slot appears, so a replica that
		// applied the winner first may collect the slot while a replica that
		// has not yet seen the winner still holds the element there and is
		// still issuing operations anchored on it -- which then arrive at an
		// anchor that no longer exists.
		deadPosNode.removedAt = entry.posMovedAt
		a.nodeMapByIndex.UpdateWeight(deadPosNode.indexNode)
		return deadPosNode, nil
	}

	// Create a new position node after the target position.
	newPosNode, err := a.insertPositionAfter(prevCreatedAt, executedAt)
	if err != nil {
		return nil, err
	}

	// Mark old position as dead.
	oldPosNode := entry.positionNode
	oldPosNode.elementEntry = nil
	oldPosNode.removedAt = executedAt
	a.nodeMapByIndex.UpdateWeight(oldPosNode.indexNode)

	// NOTE: We do NOT delete/reassign nodeMapByCreatedAt[createdAt] here.
	// The old position node keeps its key in nodeMapByCreatedAt (dead but findable).
	// The new position node is already registered under executedAt.Key() by
	// insertPositionAfter. This makes position references stable for concurrent moves.

	// Attach element to new position.
	newPosNode.elementEntry = entry
	entry.positionNode = newPosNode
	entry.posMovedAt = executedAt
	entry.elem.SetMovedAt(executedAt)

	a.nodeMapByIndex.UpdateWeight(newPosNode.indexNode)

	return oldPosNode, nil
}

// FindPrevCreatedAt returns the position node's createdAt of the previous
// element of the given element. This returns a position identity suitable
// for use as prevCreatedAt in MoveAfter.
func (a *RGATreeList) FindPrevCreatedAt(createdAt *time.Ticket) (*time.Ticket, error) {
	entry, ok := a.elementMapByCreatedAt[createdAt.Key()]
	if !ok {
		return nil, fmt.Errorf("FindPrevCreatedAt %s: %w", createdAt.Key(), ErrChildNotFound)
	}

	node := entry.positionNode
	for {
		node = node.prev
		// Skip dead position nodes (no element).
		if node.elementEntry == nil {
			continue
		}
		if a.dummyHead == node || !node.IsRemoved() {
			break
		}
	}

	// Return position node's createdAt (stable identity), not element's createdAt.
	return node.createdAt, nil
}

// PosCreatedAt returns the createdAt of the position node currently holding
// the element. This is used to convert element identity to position identity.
func (a *RGATreeList) PosCreatedAt(elemCreatedAt *time.Ticket) (*time.Ticket, error) {
	entry, ok := a.elementMapByCreatedAt[elemCreatedAt.Key()]
	if !ok {
		return nil, fmt.Errorf("PosCreatedAt %s: %w", elemCreatedAt.Key(), ErrChildNotFound)
	}
	return entry.positionNode.createdAt, nil
}

// GetByID returns the node holding the element of the given creation time,
// or nil when this list does not hold it. It mirrors RGATreeList.getByID
// (rga_tree_list.ts:495-501): elementMapByCreatedAt first, so a moved element
// resolves through its current position node, then nodeMapByCreatedAt.
//
// Unlike Root.FindByCreatedAt this is scoped to one list, so it cannot report
// an element that lives in some other container.
func (a *RGATreeList) GetByID(createdAt *time.Ticket) *RGATreeListNode {
	if entry, ok := a.elementMapByCreatedAt[createdAt.Key()]; ok {
		return entry.positionNode
	}
	return a.nodeMapByCreatedAt[createdAt.Key()]
}

// Purge physically removes a dead position node from the list (GCParent).
func (a *RGATreeList) Purge(child GCChild) error {
	node, ok := child.(*RGATreeListNode)
	if !ok {
		return fmt.Errorf("purge: expected *RGATreeListNode, got %T", child)
	}
	a.release(node)
	return nil
}

// purge physically purge child element.
func (a *RGATreeList) purge(elem Element) error {
	entry, ok := a.elementMapByCreatedAt[elem.CreatedAt().Key()]
	if !ok {
		return fmt.Errorf("purge %s: %w", elem.CreatedAt().Key(), ErrChildNotFound)
	}

	// Same guard as ElementRHT.purge: releasing the position node of an entry
	// that now holds a different element would unlink a live one on a
	// tombstone's behalf.
	if entry.elem != elem {
		return nil
	}

	node := entry.positionNode
	delete(a.elementMapByCreatedAt, elem.CreatedAt().Key())
	a.release(node)

	return nil
}

// findNextBeforeExecutedAt walks forward from the anchor to the slot the new
// node belongs after.
//
// The RGA order is the preorder of the tree each node's origin defines, with
// the children of one anchor ordered by descending position ticket. An
// operation anchored at `anchorID` therefore lands after the anchor and after
// the whole subtree of every child of the anchor whose ticket is newer than
// executedAt -- and before everything else.
//
// The plain forward skip `for next.PositionedAt().After(executedAt)` computes
// that same point, but only while every node between the anchor and the
// insertion point is still linked: it infers "is this node inside a subtree I
// am skipping" from the node's own ticket, which is sound only because a
// subtree's root is still there to stop the walk. Collection unlinks exactly
// those stoppers, so on a replica that collected, a node that used to sit
// behind a tombstone with an older ticket gets skipped instead of stopped at,
// and the two replicas order the insert differently and never recover.
//
// This walk instead reconstructs the ancestry from the origins the surviving
// nodes carry. `path` holds the tickets of the cursor's ancestors below the
// anchor, shallowest first and strictly increasing (a node's origin is always
// causally before it, so a child's ticket is always newer than its parent's).
// The decision reads path[0], the child of the anchor that owns the cursor's
// subtree, which a collected intermediate node does not change as long as some
// survivor still names it as its origin.
//
// On a list with nothing collected this returns exactly what the plain skip
// returns: the first node whose subtree root is older than executedAt is the
// first node that is itself older than executedAt.
func (a *RGATreeList) findNextBeforeExecutedAt(
	node *RGATreeListNode,
	anchorID *time.Ticket,
	executedAt *time.Ticket,
) *RGATreeListNode {
	prev := node

	// The path is bounded by the depth of the insertion tree under the anchor,
	// which is one or two for ordinary editing; the backing array keeps the
	// common case off the heap.
	var buf [8]*time.Ticket
	path := buf[:0]

	for cur := node.next; cur != nil; cur = cur.next {
		origin := cur.origin
		if origin == nil {
			// A node restored from a pre-origin snapshot says nothing about
			// its ancestry; fall back to the plain ticket comparison for it.
			if !cur.createdAt.After(executedAt) {
				return prev
			}
			prev = cur
			continue
		}

		// Unwind to the cursor's parent. Tickets increase with depth, so
		// everything deeper than the origin is on a sibling branch.
		for len(path) > 0 && path[len(path)-1].After(origin) {
			path = path[:len(path)-1]
		}

		if cmp := origin.Compare(anchorID); cmp < 0 && len(path) == 0 {
			// The origin is above the anchor, so the walk has left the
			// anchor's subtree entirely.
			return prev
		} else if cmp != 0 &&
			(len(path) == 0 || path[len(path)-1].Compare(origin) != 0) {
			// The parent is gone -- collected, or never delivered here. Its
			// ticket survives on this child, which is all the order needs.
			path = append(path, origin)
		}
		path = append(path, cur.createdAt)

		if !path[0].After(executedAt) {
			return prev
		}
		prev = cur
	}

	return prev
}

// resolveAnchor finds where an operation's prevCreatedAt sits, and returns the
// node the forward walk should start from together with the anchor's own
// position ticket -- which is not the same thing once the anchor has been
// collected.
//
// A collected anchor is not necessarily lost. Its children still name it as
// their origin, and a parent is immediately followed by its children, so the
// first node in list order that names it marks the slot it used to occupy.
// Resolving that way is what keeps the insertion point independent of whether
// this replica has collected: falling through to elementMapByCreatedAt instead,
// as the plain lookup does, silently re-points the operation at wherever the
// element happens to live now, which is a different place in the list.
func (a *RGATreeList) resolveAnchor(prevCreatedAt *time.Ticket) (*RGATreeListNode, *time.Ticket, bool) {
	if node, ok := a.nodeMapByCreatedAt[prevCreatedAt.Key()]; ok {
		return node, node.createdAt, true
	}

	for cur := a.dummyHead.next; cur != nil; cur = cur.next {
		if cur.origin != nil && cur.origin.Compare(prevCreatedAt) == 0 {
			return cur.prev, prevCreatedAt, true
		}
	}

	// Nothing left names it: the anchor was collected together with everything
	// that hung off it, so there is no evidence of where it stood.
	if entry, ok := a.elementMapByCreatedAt[prevCreatedAt.Key()]; ok {
		return entry.positionNode, entry.positionNode.createdAt, true
	}
	return nil, nil, false
}

func (a *RGATreeList) release(node *RGATreeListNode) {
	if a.last == node {
		a.last = node.prev
	}

	node.prev.next = node.next
	if node.next != nil {
		node.next.prev = node.prev
	}
	node.prev, node.next = nil, nil

	a.nodeMapByIndex.Delete(node.indexNode)

	// nodeMapByCreatedAt is keyed by position node's createdAt.
	delete(a.nodeMapByCreatedAt, node.createdAt.Key())
}

func (a *RGATreeList) insertAfter(
	prevCreatedAt *time.Ticket,
	value Element,
	executedAt *time.Ticket,
) (*RGATreeListNode, error) {
	// prevCreatedAt is a position node identity.
	startNode, anchorID, ok := a.resolveAnchor(prevCreatedAt)
	if !ok {
		return nil, fmt.Errorf("insertAfter %s: %w", prevCreatedAt.Key(), ErrChildNotFound)
	}

	prevNode := a.findNextBeforeExecutedAt(startNode, anchorID, executedAt)

	newNode := newRGATreeListNodeAfter(prevNode, value)
	newNode.origin = anchorID
	if prevNode == a.last {
		a.last = newNode
	}

	a.nodeMapByIndex.InsertAfter(prevNode.indexNode, newNode.indexNode)
	a.nodeMapByCreatedAt[value.CreatedAt().Key()] = newNode
	a.elementMapByCreatedAt[value.CreatedAt().Key()] = newNode.elementEntry
	return newNode, nil
}

// insertPositionAfter creates a bare position node after resolving position
// via forward skip (RGA insertion rule). Used by MoveAfter.
// prevCreatedAt here is a POSITION node identity, resolved via nodeMapByCreatedAt.
func (a *RGATreeList) insertPositionAfter(
	prevCreatedAt *time.Ticket,
	executedAt *time.Ticket,
) (*RGATreeListNode, error) {
	startNode, anchorID, ok := a.resolveAnchor(prevCreatedAt)
	if !ok {
		return nil, fmt.Errorf("insertPositionAfter %s: %w", prevCreatedAt.Key(), ErrChildNotFound)
	}

	prevNode := a.findNextBeforeExecutedAt(startNode, anchorID, executedAt)

	newNode := newBarePositionNode(executedAt)
	newNode.origin = anchorID
	insertNodeAfter(prevNode, newNode)
	if prevNode == a.last {
		a.last = newNode
	}

	a.nodeMapByIndex.InsertAfter(prevNode.indexNode, newNode.indexNode)
	a.nodeMapByCreatedAt[executedAt.Key()] = newNode
	return newNode, nil
}

// Set sets the given element at the given creation time.
func (a *RGATreeList) Set(
	createdAt, prevCreatedAt *time.Ticket,
	element Element,
	executedAt *time.Ticket,
) (*RGATreeListNode, error) {
	if _, ok := a.elementMapByCreatedAt[createdAt.Key()]; !ok {
		return nil, fmt.Errorf("set %s: %w", createdAt.Key(), ErrChildNotFound)
	}

	// prevCreatedAt is the slot the originating client chose while it was
	// alive. Falling back to createdAt -- the element's ORIGINAL slot -- is
	// only for operations that predate the field: once a move has abandoned
	// that slot it is collectable, and the assignment then lands in a
	// different place on a replica that collected it.
	if prevCreatedAt == nil {
		prevCreatedAt = createdAt
	}
	_, err := a.insertAfter(prevCreatedAt, element, executedAt)
	if err != nil {
		return nil, nil
	}

	removed, err := a.DeleteByCreatedAt(createdAt, executedAt)
	if err != nil {
		return removed, err
	}

	return removed, nil
}

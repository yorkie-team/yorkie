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
	"github.com/yorkie-team/yorkie/pkg/splay"
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
	indexNode    *splay.Node[*RGATreeListNode]
	elementEntry *ElementEntry
	createdAt    *time.Ticket
	removedAt    *time.Ticket

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
	node.indexNode = splay.NewNode(node)

	return node
}

// newBarePositionNode creates a position node without an element (used for move).
func newBarePositionNode(createdAt *time.Ticket) *RGATreeListNode {
	node := &RGATreeListNode{
		prev:      nil,
		next:      nil,
		createdAt: createdAt,
	}
	node.indexNode = splay.NewNode(node)

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
func (n *RGATreeListNode) PositionedAt() *time.Ticket {
	if n.elementEntry != nil {
		if n.elementEntry.elem.MovedAt() != nil {
			return n.elementEntry.elem.MovedAt()
		}
		return n.elementEntry.elem.CreatedAt()
	}
	return n.createdAt
}

// Len returns the length of this node.
// Dead nodes (no element) return 0, removed elements return 0.
func (n *RGATreeListNode) Len() int {
	if n.elementEntry == nil || n.isRemoved() {
		return 0
	}
	return 1
}

// String returns the string representation of this node.
func (n *RGATreeListNode) String() string {
	if n.elementEntry == nil {
		return ""
	}
	return n.elementEntry.elem.Marshal()
}

func (n *RGATreeListNode) isRemoved() bool {
	if n.elementEntry == nil {
		return true
	}
	return n.elementEntry.elem.RemovedAt() != nil
}

// PositionCreatedAt returns the position node's own createdAt.
func (n *RGATreeListNode) PositionCreatedAt() *time.Ticket {
	return n.createdAt
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
	nodeMapByIndex        *splay.Tree[*RGATreeListNode]
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
	nodeMapByIndex := splay.NewTree(dummyHead.indexNode)
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
		if current.elementEntry != nil && !current.isRemoved() {
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
func (a *RGATreeList) Add(elem Element) error {
	return a.InsertAfter(a.last.CreatedAt(), elem, nil)
}

// AddDeadPosition appends a dead position node during snapshot restoration.
func (a *RGATreeList) AddDeadPosition(posCreatedAt, removedAt *time.Ticket) {
	node := newBarePositionNode(posCreatedAt)
	node.removedAt = removedAt
	prevNode := a.last
	insertNodeAfter(prevNode, node)
	a.last = node
	a.nodeMapByIndex.InsertAfter(prevNode.indexNode, node.indexNode)
	a.nodeMapByCreatedAt[posCreatedAt.Key()] = node
}

// AddMovedElement appends an element with explicit position identity during
// snapshot restoration. The position node's createdAt is posCreatedAt, and
// the element is recorded as having been moved at posMovedAt.
func (a *RGATreeList) AddMovedElement(elem Element, posCreatedAt, posMovedAt *time.Ticket) error {
	entry := &ElementEntry{elem: elem, posMovedAt: posMovedAt}

	node := newBarePositionNode(posCreatedAt)
	node.elementEntry = entry
	entry.positionNode = node

	prevNode := a.last
	insertNodeAfter(prevNode, node)
	a.last = node

	a.nodeMapByIndex.InsertAfter(prevNode.indexNode, node.indexNode)
	a.nodeMapByCreatedAt[posCreatedAt.Key()] = node
	a.elementMapByCreatedAt[elem.CreatedAt().Key()] = entry
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

// LastCreatedAt returns the creation time of last elements.
func (a *RGATreeList) LastCreatedAt() *time.Ticket {
	return a.last.CreatedAt()
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
	splayNode, err := a.nodeMapByIndex.FindForArray(idx)
	if err != nil {
		return nil, err
	}
	return splayNode.Value(), nil
}

// DeleteByCreatedAt deletes the given element.
func (a *RGATreeList) DeleteByCreatedAt(createdAt *time.Ticket, deletedAt *time.Ticket) (*RGATreeListNode, error) {
	entry, ok := a.elementMapByCreatedAt[createdAt.Key()]
	if !ok {
		return nil, fmt.Errorf("DeleteByCreatedAt %s: %w", createdAt.Key(), ErrChildNotFound)
	}

	node := entry.positionNode

	alreadyRemoved := node.isRemoved()
	if entry.elem.Remove(deletedAt) && !alreadyRemoved {
		a.nodeMapByIndex.Splay(node.indexNode)
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
	if _, ok := a.nodeMapByCreatedAt[prevCreatedAt.Key()]; !ok {
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
		deadPosNode.removedAt = executedAt
		a.nodeMapByIndex.Splay(deadPosNode.indexNode)
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
	a.nodeMapByIndex.Splay(oldPosNode.indexNode)

	// NOTE: We do NOT delete/reassign nodeMapByCreatedAt[createdAt] here.
	// The old position node keeps its key in nodeMapByCreatedAt (dead but findable).
	// The new position node is already registered under executedAt.Key() by
	// insertPositionAfter. This makes position references stable for concurrent moves.

	// Attach element to new position.
	newPosNode.elementEntry = entry
	entry.positionNode = newPosNode
	entry.posMovedAt = executedAt
	entry.elem.SetMovedAt(executedAt)

	a.nodeMapByIndex.Splay(newPosNode.indexNode)

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
		if a.dummyHead == node || !node.isRemoved() {
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

	node := entry.positionNode
	delete(a.elementMapByCreatedAt, elem.CreatedAt().Key())
	a.release(node)

	return nil
}

func (a *RGATreeList) findNextBeforeExecutedAt(
	node *RGATreeListNode,
	executedAt *time.Ticket,
) *RGATreeListNode {
	for node.next != nil && node.next.PositionedAt().After(executedAt) {
		node = node.next
	}

	return node
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
	// prevCreatedAt is a position node identity. Look up in nodeMapByCreatedAt
	// first (covers both live and dead position nodes), then fall back to
	// elementMapByCreatedAt for backward compatibility with element identity.
	var startNode *RGATreeListNode
	if node, ok := a.nodeMapByCreatedAt[prevCreatedAt.Key()]; ok {
		startNode = node
	} else if entry, ok := a.elementMapByCreatedAt[prevCreatedAt.Key()]; ok {
		startNode = entry.positionNode
	} else {
		return nil, fmt.Errorf("insertAfter %s: %w", prevCreatedAt.Key(), ErrChildNotFound)
	}

	prevNode := a.findNextBeforeExecutedAt(startNode, executedAt)

	newNode := newRGATreeListNodeAfter(prevNode, value)
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
	startNode, ok := a.nodeMapByCreatedAt[prevCreatedAt.Key()]
	if !ok {
		return nil, fmt.Errorf("insertPositionAfter %s: %w", prevCreatedAt.Key(), ErrChildNotFound)
	}

	prevNode := a.findNextBeforeExecutedAt(startNode, executedAt)

	newNode := newBarePositionNode(executedAt)
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
	createdAt *time.Ticket,
	element Element,
	executedAt *time.Ticket,
) (*RGATreeListNode, error) {
	if _, ok := a.elementMapByCreatedAt[createdAt.Key()]; !ok {
		return nil, fmt.Errorf("set %s: %w", createdAt.Key(), ErrChildNotFound)
	}

	// Use the element's original position (via nodeMapByCreatedAt[createdAt])
	// so that Set always inserts at the position where the element was when
	// the Set operation was created, regardless of concurrent moves.
	_, err := a.insertAfter(createdAt, element, executedAt)
	if err != nil {
		return nil, nil
	}

	removed, err := a.DeleteByCreatedAt(createdAt, executedAt)
	if err != nil {
		return removed, err
	}

	return removed, nil
}

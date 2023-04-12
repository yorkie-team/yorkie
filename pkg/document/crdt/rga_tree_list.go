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

	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/splay"
)

// RGATreeListNode is a node of RGATreeList.
type RGATreeListNode struct {
	indexNode *splay.Node[*RGATreeListNode]
	elem      Element

	prev *RGATreeListNode
	next *RGATreeListNode
}

func newRGATreeListNode(elem Element) *RGATreeListNode {
	node := &RGATreeListNode{
		prev: nil,
		next: nil,
		elem: elem,
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

// Element returns the element of this node.
func (n *RGATreeListNode) Element() Element {
	return n.elem
}

// CreatedAt returns the creation time of this element.
func (n *RGATreeListNode) CreatedAt() *time.Ticket {
	return n.elem.CreatedAt()
}

// PositionedAt returns the time this element was positioned.
func (n *RGATreeListNode) PositionedAt() *time.Ticket {
	if n.elem.MovedAt() != nil {
		return n.elem.MovedAt()
	}

	return n.elem.CreatedAt()
}

// Len returns the length of this node.
func (n *RGATreeListNode) Len() int {
	if n.isRemoved() {
		return 0
	}
	return 1
}

// String returns the string representation of this node.
func (n *RGATreeListNode) String() (string, error) {
	str, err := n.elem.Marshal()
	if err != nil {
		return "", fmt.Errorf("string RGA tree list node %w", err)
	}
	return str, nil
}

func (n *RGATreeListNode) isRemoved() bool {
	return n.elem.RemovedAt() != nil
}

// RGATreeList is a list with improved index-based lookup in RGA. RGA is a
// linked list that has a logical clock and tombstone. Since RGA is composed as
// a linked list, index-based element search is slow, O(n). To optimise for fast
// insertions and removals at any index in the list, RGATreeList has a tree.
type RGATreeList struct {
	dummyHead          *RGATreeListNode
	last               *RGATreeListNode
	nodeMapByIndex     *splay.Tree[*RGATreeListNode]
	nodeMapByCreatedAt map[string]*RGATreeListNode
}

// NewRGATreeList creates a new instance of RGATreeList.
func NewRGATreeList() (*RGATreeList, error) {
	dummyValue, err := NewPrimitive(0, time.InitialTicket)
	if err != nil {
		return nil, fmt.Errorf("new RGA tree list: %w", err)
	}
	dummyValue.SetRemovedAt(time.InitialTicket)
	dummyHead := newRGATreeListNode(dummyValue)
	nodeMapByIndex := splay.NewTree(dummyHead.indexNode)
	nodeMapByCreatedAt := make(map[string]*RGATreeListNode)
	nodeMapByCreatedAt[dummyHead.CreatedAt().Key()] = dummyHead

	return &RGATreeList{
		dummyHead:          dummyHead,
		last:               dummyHead,
		nodeMapByIndex:     nodeMapByIndex,
		nodeMapByCreatedAt: nodeMapByCreatedAt,
	}, nil
}

// Marshal returns the JSON encoding of this RGATreeList.
func (a *RGATreeList) Marshal() (string, error) {
	sb := strings.Builder{}
	sb.WriteString("[")

	current := a.dummyHead.next
	isFirst := true
	for current != nil {
		if !current.isRemoved() {
			if isFirst {
				isFirst = false
			} else {
				sb.WriteString(",")
			}

			str, err := current.elem.Marshal()
			if err != nil {
				return "", fmt.Errorf("marshal RGA tree list node: %w", err)
			}
			sb.WriteString(str)
		}

		current = current.next
	}

	sb.WriteString("]")

	return sb.String(), nil
}

// Add adds the given element at the last.
func (a *RGATreeList) Add(elem Element) error {
	if err := a.insertAfter(a.last.CreatedAt(), elem, elem.CreatedAt()); err != nil {
		return fmt.Errorf("add RGA tree list node: %w", err)
	}
	return nil
}

// Nodes returns an array of elements contained in this RGATreeList.
// TODO: If we encounter performance issues, we need to replace this with other solution.
func (a *RGATreeList) Nodes() []*RGATreeListNode {
	var nodes []*RGATreeListNode
	current := a.dummyHead.next
	for {
		if current == nil {
			break
		}
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
func (a *RGATreeList) InsertAfter(prevCreatedAt *time.Ticket, elem Element) error {
	if err := a.insertAfter(prevCreatedAt, elem, elem.CreatedAt()); err != nil {
		return fmt.Errorf("insert after RGA tree list node: %w", err)
	}
	return nil
}

// Get returns the element of the given index.
func (a *RGATreeList) Get(idx int) (*RGATreeListNode, error) {
	splayNode, offset, err := a.nodeMapByIndex.Find(idx)
	if err != nil {
		return nil, fmt.Errorf("get RGA tree list node: %w", err)
	}
	node := splayNode.Value()

	if idx == 0 && splayNode == a.dummyHead.indexNode {
		for {
			node = node.next
			if !node.isRemoved() {
				break
			}
		}
	} else if offset > 0 {
		for {
			node = node.next
			if !node.isRemoved() {
				break
			}
		}
	}

	return node, nil
}

// DeleteByCreatedAt deletes the given element.
func (a *RGATreeList) DeleteByCreatedAt(createdAt *time.Ticket, deletedAt *time.Ticket) (*RGATreeListNode, error) {
	node, ok := a.nodeMapByCreatedAt[createdAt.Key()]
	if !ok {
		return nil, fmt.Errorf(
			"delete by created at RGA tree list node: fail to find the given createdAt: %s",
			createdAt.Key(),
		)
	}

	alreadyRemoved := node.isRemoved()
	if node.elem.Remove(deletedAt) && !alreadyRemoved {
		a.nodeMapByIndex.Splay(node.indexNode)
	}

	return node, nil
}

// Len returns length of this RGATreeList.
func (a *RGATreeList) Len() int {
	return a.nodeMapByIndex.Len()
}

// StructureAsString returns a String containing the metadata of the node id
// for debugging purpose.
func (a *RGATreeList) StructureAsString() (string, error) {
	return a.nodeMapByIndex.StructureAsString()
}

// Delete deletes the node of the given index.
func (a *RGATreeList) Delete(idx int, deletedAt *time.Ticket) (*RGATreeListNode, error) {
	target, err := a.Get(idx)
	if err != nil {
		return nil, fmt.Errorf("delete RGA tree list node: %w", err)
	}
	node, err := a.DeleteByCreatedAt(target.CreatedAt(), deletedAt)
	if err != nil {
		return nil, fmt.Errorf("delete RGA tree list node: %w", err)
	}
	return node, nil
}

// MoveAfter moves the given `createdAt` element after the `prevCreatedAt`
// element.
func (a *RGATreeList) MoveAfter(prevCreatedAt, createdAt, executedAt *time.Ticket) error {
	prevNode, ok := a.nodeMapByCreatedAt[prevCreatedAt.Key()]
	if !ok {
		return fmt.Errorf(
			"move after RGA tree list node: fail to find the given prevCreatedAt: %s",
			prevCreatedAt.Key(),
		)
	}

	node, ok := a.nodeMapByCreatedAt[createdAt.Key()]
	if !ok {
		return fmt.Errorf("move after RGA tree list node: fail to find the given createdAt: %s", createdAt.Key())
	}

	if node.elem.MovedAt() == nil || executedAt.After(node.elem.MovedAt()) {
		a.release(node)
		if err := a.insertAfter(prevNode.CreatedAt(), node.elem, executedAt); err != nil {
			return fmt.Errorf("move after RGA tree list node: %w", err)
		}
		node.elem.SetMovedAt(executedAt)
	}

	return nil
}

// FindPrevCreatedAt returns the creation time of the previous element of the
// given element.
func (a *RGATreeList) FindPrevCreatedAt(createdAt *time.Ticket) (*time.Ticket, error) {
	node, ok := a.nodeMapByCreatedAt[createdAt.Key()]
	if !ok {
		return nil, fmt.Errorf(
			"find prev RGA tree list node: fail to find the given prevCreatedAt: %s",
			createdAt.Key(),
		)
	}

	for {
		node = node.prev
		if a.dummyHead == node || !node.isRemoved() {
			break
		}
	}

	return node.CreatedAt(), nil
}

// purge physically purge child element.
func (a *RGATreeList) purge(elem Element) error {
	node, ok := a.nodeMapByCreatedAt[elem.CreatedAt().Key()]
	if !ok {
		return fmt.Errorf("purge RGA tree list node: fail to find the given createdAt: %s", elem.CreatedAt().Key())
	}

	a.release(node)
	return nil
}

func (a *RGATreeList) findNextBeforeExecutedAt(
	createdAt *time.Ticket,
	executedAt *time.Ticket,
) (*RGATreeListNode, error) {
	node, ok := a.nodeMapByCreatedAt[createdAt.Key()]
	if !ok {
		return nil, fmt.Errorf("find next before executed at: fail to find the given createdAt: %s", createdAt.Key())
	}

	for node.next != nil && node.next.PositionedAt().After(executedAt) {
		node = node.next
	}

	return node, nil
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
	delete(a.nodeMapByCreatedAt, node.CreatedAt().Key())
}

func (a *RGATreeList) insertAfter(
	prevCreatedAt *time.Ticket,
	value Element,
	executedAt *time.Ticket,
) error {
	prevNode, err := a.findNextBeforeExecutedAt(prevCreatedAt, executedAt)
	if err != nil {
		return fmt.Errorf("insert after: %w", err)
	}
	newNode := newRGATreeListNodeAfter(prevNode, value)
	if prevNode == a.last {
		a.last = newNode
	}

	a.nodeMapByIndex.InsertAfter(prevNode.indexNode, newNode.indexNode)
	a.nodeMapByCreatedAt[value.CreatedAt().Key()] = newNode
	return nil
}

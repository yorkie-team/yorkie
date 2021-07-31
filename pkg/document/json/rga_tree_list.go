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

package json

import (
	"strings"

	"github.com/yorkie-team/yorkie/internal/log"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/pq"
	"github.com/yorkie-team/yorkie/pkg/splay"
)

// RGATreeListValueNode is a node of PriorityQueue.
type RGATreeListValueNode struct {
	elem Element
}

func newRGATreeListValueNode(elem Element) *RGATreeListValueNode {
	return &RGATreeListValueNode{
		elem: elem,
	}
}

// Less is the implementation of the PriorityQueue Value interface.
// elements inserted later must be exposed above.
func (n *RGATreeListValueNode) Less(other pq.Value) bool {
	node := other.(*RGATreeListValueNode)
	return n.elem.CreatedAt().After(node.elem.CreatedAt())
}

// Element returns the element of this node.
func (n *RGATreeListValueNode) Element() Element {
	return n.elem
}

// Marshal returns the JSON encoding of this element.
func (n *RGATreeListValueNode) Marshal() string {
	return n.elem.Marshal()
}

// RGATreeListNode is a node of RGATreeList.
type RGATreeListNode struct {
	indexNode *splay.Node
	nodeQueue *pq.PriorityQueue

	prev *RGATreeListNode
	next *RGATreeListNode
}

func newRGATreeListNode(elem Element) *RGATreeListNode {
	valueNode := newRGATreeListValueNode(elem)
	queue := pq.NewPriorityQueue()
	queue.Push(valueNode)

	node := &RGATreeListNode{
		prev:      nil,
		next:      nil,
		nodeQueue: queue,
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

// InsertNodeAfter inserts the given node after the given previous node.
func (n *RGATreeListNode) InsertNodeAfter(node *RGATreeListNode) *RGATreeListNode {
	prevNext := n.next

	n.next = node
	node.prev = n
	node.next = prevNext
	if prevNext != nil {
		prevNext.prev = node
	}

	return n.next
}

// Element returns the element of this node.
func (n *RGATreeListNode) Element() Element {
	valueNode := n.nodeQueue.Peek().(*RGATreeListValueNode)
	return valueNode.elem
}

func (n *RGATreeListNode) findElemByCreatedAt(createdAt *time.Ticket) Element {
	for _, value := range n.nodeQueue.Values() {
		elem := value.(*RGATreeListValueNode).Element()

		if elem.CreatedAt().Compare(createdAt) == 0 {
			return elem
		}
	}

	return nil
}

// Elements returns the elements of this node.
// Shows all deleted nodes.
func (n *RGATreeListNode) Elements() []Element {
	var elements []Element

	for _, value := range n.nodeQueue.Values() {
		elements = append(elements, value.(*RGATreeListValueNode).Element())
	}

	return elements
}

// CreatedAt returns the creation time of this element.
func (n *RGATreeListNode) CreatedAt() *time.Ticket {
	return n.Element().CreatedAt()
}

// PositionedAt returns the time this element was positioned.
func (n *RGATreeListNode) PositionedAt() *time.Ticket {
	if n.Element().MovedAt() != nil {
		return n.Element().MovedAt()
	}

	return n.Element().CreatedAt()
}

// Len returns the length of this node.
func (n *RGATreeListNode) Len() int {
	if n.isRemoved() {
		return 0
	}
	return 1
}

// String returns the string representation of this node.
func (n *RGATreeListNode) String() string {
	return n.Element().Marshal()
}

func (n *RGATreeListNode) isRemoved() bool {
	return n.Element().RemovedAt() != nil
}

// Set sets the element on this node.
func (n *RGATreeListNode) Set(elem Element) Element {
	valueNode := newRGATreeListValueNode(elem)

	if n.Len() == 0 {
		n.nodeQueue.Push(valueNode)
		return nil
	}

	prevNode := n.nodeQueue.Peek().(*RGATreeListValueNode)
	n.nodeQueue.Push(valueNode)

	if prevNode.Less(valueNode) {
		return nil
	}

	return prevNode.Element()
}

func (n *RGATreeListNode) release(elem Element) {
	for _, value := range n.nodeQueue.Values() {
		prevElem := value.(*RGATreeListValueNode).Element()

		if prevElem.CreatedAt().Compare(elem.CreatedAt()) == 0 {
			n.nodeQueue.Release(value)
			break
		}
	}
}

// RGATreeList is a list with improved index-based lookup in RGA. RGA is a
// linked list that has a logical clock and tombstone. Since RGA is composed as
// a linked list, index-based element search is slow, O(n). To optimise for fast
// insertions and removals at any index in the list, RGATreeList has a tree.
type RGATreeList struct {
	dummyHead          *RGATreeListNode
	last               *RGATreeListNode
	size               int
	nodeMapByIndex     *splay.Tree
	nodeMapByCreatedAt map[string]*RGATreeListNode
}

// NewRGATreeList creates a new instance of RGATreeList.
func NewRGATreeList() *RGATreeList {
	dummyValue := NewPrimitive(0, time.InitialTicket)
	dummyValue.SetRemovedAt(time.InitialTicket)
	dummyHead := newRGATreeListNode(dummyValue)
	nodeMapByIndex := splay.NewTree(dummyHead.indexNode)
	nodeMapByCreatedAt := make(map[string]*RGATreeListNode)
	nodeMapByCreatedAt[dummyHead.CreatedAt().Key()] = dummyHead

	return &RGATreeList{
		dummyHead:          dummyHead,
		last:               dummyHead,
		size:               0,
		nodeMapByIndex:     nodeMapByIndex,
		nodeMapByCreatedAt: nodeMapByCreatedAt,
	}
}

// Marshal returns the JSON encoding of this RGATreeList.
func (a *RGATreeList) Marshal() string {
	sb := strings.Builder{}
	sb.WriteString("[")

	current := a.dummyHead.next
	for {
		if current == nil {
			break
		}

		if !current.isRemoved() {
			sb.WriteString(current.Element().Marshal())

			// FIXME: When the last element of the array is deleted, it does not
			// work properly.
			if current != a.last {
				sb.WriteString(",")
			}
		}

		current = current.next
	}

	sb.WriteString("]")

	return sb.String()
}

// Add adds the given element at the last.
func (a *RGATreeList) Add(elem Element) {
	a.insertAfter(a.last.CreatedAt(), elem, elem.CreatedAt())
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
func (a *RGATreeList) InsertAfter(prevCreatedAt *time.Ticket, elem Element) {
	a.insertAfter(prevCreatedAt, elem, elem.CreatedAt())
}

// Get returns the element of the given index.
func (a *RGATreeList) Get(idx int) *RGATreeListNode {
	splayNode, offset := a.nodeMapByIndex.Find(idx)
	node := splayNode.Value().(*RGATreeListNode)

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

	return node
}

// SetByIndex sets the given element at the given position.
func (a *RGATreeList) SetByIndex(targetCreatedAt *time.Ticket, elem Element) Element {
	node, ok := a.nodeMapByCreatedAt[targetCreatedAt.Key()]
	if !ok {
		log.Logger.Fatalf(
			"fail to find the given targetCreatedAt: %s",
			targetCreatedAt.Key(),
		)
	}

	elem.SetMovedAt(targetCreatedAt)
	a.nodeMapByCreatedAt[elem.CreatedAt().Key()] = node

	deleted := node.Set(elem)
	if deleted != nil {
		deleted.Remove(elem.CreatedAt())
		return deleted
	}

	return nil
}

// DeleteByCreatedAt deletes the given element.
func (a *RGATreeList) DeleteByCreatedAt(createdAt *time.Ticket, deletedAt *time.Ticket) *RGATreeListNode {
	node, ok := a.nodeMapByCreatedAt[createdAt.Key()]
	if !ok {
		log.Logger.Fatalf(
			"fail to find the given createdAt: %s",
			createdAt.Key(),
		)
	}

	if node.findElemByCreatedAt(createdAt).Remove(deletedAt) {
		a.nodeMapByIndex.Splay(node.indexNode)
		a.size--
	}
	return node
}

// Len returns length of this RGATreeList.
func (a *RGATreeList) Len() int {
	return a.size
}

// AnnotatedString returns a String containing the meta data of the node id
// for debugging purpose.
func (a *RGATreeList) AnnotatedString() string {
	return a.nodeMapByIndex.AnnotatedString()
}

// Delete deletes the node of the given index.
func (a *RGATreeList) Delete(idx int, deletedAt *time.Ticket) *RGATreeListNode {
	target := a.Get(idx)
	return a.DeleteByCreatedAt(target.CreatedAt(), deletedAt)
}

// MoveAfter moves the given `createdAt` element after the `prevCreatedAt`
// element.
func (a *RGATreeList) MoveAfter(prevCreatedAt, createdAt, executedAt *time.Ticket) {
	prevNode, ok := a.nodeMapByCreatedAt[prevCreatedAt.Key()]
	if !ok {
		log.Logger.Fatalf(
			"fail to find the given prevCreatedAt: %s",
			prevCreatedAt.Key(),
		)
	}

	node, ok := a.nodeMapByCreatedAt[createdAt.Key()]
	if !ok {
		log.Logger.Fatalf(
			"fail to find the given createdAt: %s",
			createdAt.Key(),
		)
	}

	if node.Element().MovedAt() == nil || executedAt.After(node.Element().MovedAt()) {
		a.release(node)
		a.insertNodeAfter(prevNode.CreatedAt(), node, executedAt)
		node.Element().SetMovedAt(executedAt)
	}
}

// FindPrevCreatedAt returns the creation time of the previous element of the
// given element.
func (a *RGATreeList) FindPrevCreatedAt(createdAt *time.Ticket) *time.Ticket {
	node, ok := a.nodeMapByCreatedAt[createdAt.Key()]
	if !ok {
		log.Logger.Fatalf(
			"fail to find the given prevCreatedAt: %s",
			createdAt.Key(),
		)
	}

	for {
		node = node.prev
		if a.dummyHead == node || !node.isRemoved() {
			break
		}
	}

	return node.CreatedAt()
}

// purge physically purge child element.
func (a *RGATreeList) purge(elem Element) {
	node, ok := a.nodeMapByCreatedAt[elem.CreatedAt().Key()]
	if !ok {
		log.Logger.Fatalf(
			"fail to find the given createdAt: %s",
			elem.CreatedAt().Key(),
		)
	}

	delete(a.nodeMapByCreatedAt, elem.CreatedAt().Key())
	a.purgeNode(node)
	node.release(elem)
}

func (a *RGATreeList) purgeNode(node *RGATreeListNode) {
	for _, prevElem := range node.Elements() {
		if _, ok := a.nodeMapByCreatedAt[prevElem.CreatedAt().Key()]; ok {
			return
		}
	}

	a.release(node)
}

func (a *RGATreeList) findNextBeforeExecutedAt(
	createdAt *time.Ticket,
	executedAt *time.Ticket,
) *RGATreeListNode {
	node, ok := a.nodeMapByCreatedAt[createdAt.Key()]
	if !ok {
		log.Logger.Fatalf(
			"fail to find the given createdAt: %s",
			createdAt.Key(),
		)
		return nil
	}

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

	delete(a.nodeMapByCreatedAt, node.CreatedAt().Key())
	if !node.isRemoved() {
		a.size--
	}
}

func (a *RGATreeList) insertAfter(
	prevCreatedAt *time.Ticket,
	value Element,
	executedAt *time.Ticket,
) {
	prevNode := a.findNextBeforeExecutedAt(prevCreatedAt, executedAt)
	newNode := newRGATreeListNodeAfter(prevNode, value)
	if prevNode == a.last {
		a.last = newNode
	}

	a.nodeMapByIndex.InsertAfter(prevNode.indexNode, newNode.indexNode)
	a.nodeMapByCreatedAt[value.CreatedAt().Key()] = newNode

	a.size++
}

func (a *RGATreeList) insertNodeAfter(
	prevCreatedAt *time.Ticket,
	node *RGATreeListNode,
	executedAt *time.Ticket,
) {
	prevNode := a.findNextBeforeExecutedAt(prevCreatedAt, executedAt)
	newNode := prevNode.InsertNodeAfter(node)
	if prevNode == a.last {
		a.last = newNode
	}

	a.nodeMapByIndex.InsertAfter(prevNode.indexNode, newNode.indexNode)
	a.nodeMapByCreatedAt[node.CreatedAt().Key()] = newNode

	a.size++
}

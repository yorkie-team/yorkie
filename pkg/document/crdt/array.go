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
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// Array represents JSON array data structure including logical clock.
// Array implements Element interface.
type Array struct {
	elements  *RGATreeList
	createdAt *time.Ticket
	movedAt   *time.Ticket
	removedAt *time.Ticket
}

// NewArray creates a new instance of Array.
func NewArray(elements *RGATreeList, createdAt *time.Ticket) *Array {
	return &Array{
		elements:  elements,
		createdAt: createdAt,
	}
}

// Purge physically purge child element.
func (a *Array) Purge(elem Element) error {
	return a.elements.purge(elem)
}

// Add adds the given element at the last.
func (a *Array) Add(elem Element) *Array {
	a.elements.Add(elem)
	return a
}

// Get returns the element of the given index.
func (a *Array) Get(idx int) (Element, error) {
	node, err := a.elements.Get(idx)
	if err != nil {
		return nil, err
	}
	return node.elem, nil
}

// FindPrevCreatedAt returns the creation time of the previous element of the
// given element.
func (a *Array) FindPrevCreatedAt(createdAt *time.Ticket) *time.Ticket {
	return a.elements.FindPrevCreatedAt(createdAt)
}

// Delete deletes the element of the given index.
func (a *Array) Delete(idx int, deletedAt *time.Ticket) (Element, error) {
	node, err := a.elements.Delete(idx, deletedAt)
	if err != nil {
		return nil, err
	}
	return node.elem, nil
}

// MoveAfter moves the given `createdAt` element after the `prevCreatedAt`
// element.
func (a *Array) MoveAfter(prevCreatedAt, createdAt, executedAt *time.Ticket) {
	a.elements.MoveAfter(prevCreatedAt, createdAt, executedAt)
}

// Elements returns an array of elements contained in this RGATreeList.
func (a *Array) Elements() []Element {
	var elements []Element
	for _, node := range a.elements.Nodes() {
		if node.isRemoved() {
			continue
		}
		elements = append(elements, node.elem)
	}

	return elements
}

// Marshal returns the JSON encoding of this Array.
func (a *Array) Marshal() string {
	return a.elements.Marshal()
}

// StructureAsString returns a String containing the metadata of the elements
// for debugging purpose.
func (a *Array) StructureAsString() string {
	return a.elements.StructureAsString()
}

// DeepCopy copies itself deeply.
func (a *Array) DeepCopy() (Element, error) {
	elements := NewRGATreeList()

	for _, node := range a.elements.Nodes() {
		copiedNode, err := node.elem.DeepCopy()
		if err != nil {
			return nil, err
		}
		elements.Add(copiedNode)
	}

	array := NewArray(elements, a.createdAt)
	array.removedAt = a.removedAt
	return array, nil
}

// CreatedAt returns the creation time of this array.
func (a *Array) CreatedAt() *time.Ticket {
	return a.createdAt
}

// MovedAt returns the move time of this array.
func (a *Array) MovedAt() *time.Ticket {
	return a.movedAt
}

// SetMovedAt sets the move time of this array.
func (a *Array) SetMovedAt(movedAt *time.Ticket) {
	a.movedAt = movedAt
}

// RemovedAt returns the removal time of this array.
func (a *Array) RemovedAt() *time.Ticket {
	return a.removedAt
}

// SetRemovedAt sets the removal time of this array.
func (a *Array) SetRemovedAt(removedAt *time.Ticket) {
	a.removedAt = removedAt
}

// Remove removes this array.
func (a *Array) Remove(removedAt *time.Ticket) bool {
	if (removedAt != nil && removedAt.After(a.createdAt)) &&
		(a.removedAt == nil || removedAt.After(a.removedAt)) {
		a.removedAt = removedAt
		return true
	}
	return false
}

// LastCreatedAt returns the creation time of the last element.
func (a *Array) LastCreatedAt() *time.Ticket {
	return a.elements.LastCreatedAt()
}

// InsertAfter inserts the given element after the given previous element.
func (a *Array) InsertAfter(prevCreatedAt *time.Ticket, element Element) {
	a.elements.InsertAfter(prevCreatedAt, element)
}

// DeleteByCreatedAt deletes the given element.
func (a *Array) DeleteByCreatedAt(createdAt *time.Ticket, deletedAt *time.Ticket) Element {
	return a.elements.DeleteByCreatedAt(createdAt, deletedAt).elem
}

// Len returns length of this Array.
func (a *Array) Len() int {
	return a.elements.Len()
}

// Descendants traverse the descendants of this array.
func (a *Array) Descendants(callback func(elem Element, parent Container) bool) {
	for _, node := range a.elements.Nodes() {
		if callback(node.elem, a) {
			return
		}

		switch elem := node.elem.(type) {
		case *Object:
			elem.Descendants(callback)
		case *Array:
			elem.Descendants(callback)
		}
	}
}

// RGANodes returns the slices of RGATreeListNode.
func (a *Array) RGANodes() []*RGATreeListNode {
	return a.elements.Nodes()
}

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
	"github.com/yorkie-team/yorkie/pkg/resource"
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
func NewArray(elements *RGATreeList, createdAt *time.Ticket, value ...[]Element) *Array {
	if len(value) == 1 {
		for _, v := range value[0] {
			_ = elements.InsertAfter(elements.LastCreatedAt(), v, nil)
		}
	}
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
func (a *Array) Add(elem Element) error {
	return a.elements.Add(elem)
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
func (a *Array) FindPrevCreatedAt(createdAt *time.Ticket) (*time.Ticket, error) {
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
func (a *Array) MoveAfter(prevCreatedAt, createdAt, executedAt *time.Ticket) error {
	return a.elements.MoveAfter(prevCreatedAt, createdAt, executedAt)
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

// MetaSize returns the size of the metadata of this element.
func (a *Array) MetaSize() int {
	size := 0
	if a.createdAt != nil {
		size += time.TicketSize
	}
	if a.movedAt != nil {
		size += time.TicketSize
	}
	if a.removedAt != nil {
		size += time.TicketSize
	}
	return size
}

// DataSize returns the size of this array.
func (a *Array) DataSize() resource.DataSize {
	return resource.DataSize{
		Data: 0,
		Meta: a.MetaSize(),
	}
}

// Marshal returns the JSON encoding of this Array.
func (a *Array) Marshal() string {
	return a.elements.Marshal()
}

// ToTestString returns a String containing the metadata of the elements
// for debugging purpose.
func (a *Array) ToTestString() string {
	return a.elements.ToTestString()
}

// DeepCopy copies itself deeply.
func (a *Array) DeepCopy() (Element, error) {
	elements := NewRGATreeList()
	for _, node := range a.elements.Nodes() {
		copiedNode, err := node.elem.DeepCopy()
		if err != nil {
			return nil, err
		}
		if err = elements.Add(copiedNode); err != nil {
			return nil, err
		}
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
func (a *Array) InsertAfter(prevCreatedAt *time.Ticket, element Element, executedAt *time.Ticket) error {
	return a.elements.InsertAfter(prevCreatedAt, element, executedAt)
}

// DeleteByCreatedAt deletes the given element.
func (a *Array) DeleteByCreatedAt(createdAt *time.Ticket, deletedAt *time.Ticket) (Element, error) {
	node, err := a.elements.DeleteByCreatedAt(createdAt, deletedAt)
	if err != nil {
		return nil, err
	}
	return node.elem, nil
}

// Set sets the given element at the given position of the creation time.
func (a *Array) Set(createdAt *time.Ticket, element Element, executedAt *time.Ticket) (Element, error) {
	node, err := a.elements.Set(createdAt, element, executedAt)
	if err != nil {
		return nil, err
	}
	if node != nil {
		return node.elem, nil
	}
	return nil, nil
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

		if elem, ok := node.elem.(Container); ok {
			elem.Descendants(callback)
		}
	}
}

// RGANodes returns the slices of RGATreeListNode.
func (a *Array) RGANodes() []*RGATreeListNode {
	return a.elements.Nodes()
}

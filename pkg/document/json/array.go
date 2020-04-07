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
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// Array represents JSON array data structure including logical clock.
// Array implements Element interface.
type Array struct {
	elements  *RGATreeList
	createdAt *time.Ticket
	deletedAt *time.Ticket
}

// NewArray creates a new instance of Array.
func NewArray(elements *RGATreeList, createdAt *time.Ticket) *Array {
	return &Array{
		elements:  elements,
		createdAt: createdAt,
	}
}

// Add adds the given element at the last.
func (a *Array) Add(elem Element) *Array {
	a.elements.Add(elem)
	return a
}

// Get returns the element of the given index.
func (a *Array) Get(idx int) Element {
	return a.elements.Get(idx).elem
}

// Remove removes the element of the given index.
func (a *Array) Remove(idx int, deletedAt *time.Ticket) Element {
	return a.elements.Remove(idx, deletedAt).elem
}

// Elements returns an array of elements contained in this RGATreeList.
func (a *Array) Elements() []Element {
	var elements []Element
	for _, node := range a.elements.Nodes() {
		if node.isDeleted() {
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

func (a *Array) AnnotatedString() string {
	return a.elements.AnnotatedString()
}

// DeepCopy copies itself deeply.
func (a *Array) DeepCopy() Element {
	elements := NewRGATreeList()

	for _, node := range a.elements.Nodes() {
		elements.Add(node.elem.DeepCopy())
	}

	array := NewArray(elements, a.createdAt)
	array.deletedAt = a.deletedAt
	return array
}

// CreatedAt returns the creation time of this Array.
func (a *Array) CreatedAt() *time.Ticket {
	return a.createdAt
}

// DeletedAt returns the deletion time of this Array.
func (a *Array) DeletedAt() *time.Ticket {
	return a.deletedAt
}

// Delete deletes this element.
func (a *Array) Delete(deletedAt *time.Ticket) bool {
	if a.deletedAt == nil || deletedAt.After(a.deletedAt) {
		a.deletedAt = deletedAt
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

// RemoveByCreatedAt removes the given element.
func (a *Array) RemoveByCreatedAt(createdAt *time.Ticket, deletedAt *time.Ticket) Element {
	return a.elements.RemoveByCreatedAt(createdAt, deletedAt).elem
}

// Len returns length of this Array.
func (a *Array) Len() int {
	return a.elements.Len()
}

func (a *Array) Descendants(descendants chan Element) {
	for _, node := range a.elements.Nodes() {
		switch elem := node.elem.(type) {
		case *Object:
			elem.Descendants(descendants)
		case *Array:
			elem.Descendants(descendants)
		}
		descendants <- node.elem
	}
}

func (a *Array) RGANodes() []*RGATreeListNode {
	return a.elements.Nodes()
}

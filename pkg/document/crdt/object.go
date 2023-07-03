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

// Object represents a JSON object, but unlike regular JSON, it has time
// tickets which is created by logical clock.
type Object struct {
	memberNodes *ElementRHT
	createdAt   *time.Ticket
	movedAt     *time.Ticket
	removedAt   *time.Ticket
}

// NewObject creates a new instance of Object.
func NewObject(memberNodes *ElementRHT, createdAt *time.Ticket) *Object {
	return &Object{
		memberNodes: memberNodes,
		createdAt:   createdAt,
	}
}

// Purge physically purge child element.
func (o *Object) Purge(elem Element) error {
	return o.memberNodes.purge(elem)
}

// Set sets the given element of the given key.
func (o *Object) Set(k string, v Element) Element {
	return o.memberNodes.Set(k, v)
}

// Members returns the member of this object as a map.
func (o *Object) Members() map[string]Element {
	return o.memberNodes.Elements()
}

// Get returns the value of the given key.
func (o *Object) Get(k string) Element {
	return o.memberNodes.Get(k)
}

// Has returns whether the element exists of the given key or not.
func (o *Object) Has(k string) bool {
	return o.memberNodes.Has(k)
}

// DeleteByCreatedAt deletes the element of the given creation time.
func (o *Object) DeleteByCreatedAt(createdAt *time.Ticket, deletedAt *time.Ticket) Element {
	return o.memberNodes.DeleteByCreatedAt(createdAt, deletedAt)
}

// Delete deletes the element of the given key.
func (o *Object) Delete(k string, deletedAt *time.Ticket) Element {
	return o.memberNodes.Delete(k, deletedAt)
}

// Descendants traverse the descendants of this object.
func (o *Object) Descendants(callback func(elem Element, parent Container) bool) {
	for _, node := range o.memberNodes.Nodes() {
		if callback(node.elem, o) {
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

// Marshal returns the JSON encoding of this object.
func (o *Object) Marshal() string {
	return o.memberNodes.Marshal()
}

// DeepCopy copies itself deeply.
func (o *Object) DeepCopy() (Element, error) {
	members := NewElementRHT()

	for _, node := range o.memberNodes.Nodes() {
		copiedNode, err := node.elem.DeepCopy()
		if err != nil {
			return nil, err
		}
		members.Set(node.key, copiedNode)
	}

	obj := NewObject(members, o.createdAt)
	obj.removedAt = o.removedAt
	return obj, nil
}

// CreatedAt returns the creation time of this object.
func (o *Object) CreatedAt() *time.Ticket {
	return o.createdAt
}

// MovedAt returns the move time of this object.
func (o *Object) MovedAt() *time.Ticket {
	return o.movedAt
}

// SetMovedAt sets the move time of this object.
func (o *Object) SetMovedAt(movedAt *time.Ticket) {
	o.movedAt = movedAt
}

// RemovedAt returns the removal time of this object.
func (o *Object) RemovedAt() *time.Ticket {
	return o.removedAt
}

// SetRemovedAt sets the removal time of this array.
func (o *Object) SetRemovedAt(removedAt *time.Ticket) {
	o.removedAt = removedAt
}

// Remove removes this object.
func (o *Object) Remove(removedAt *time.Ticket) bool {
	if (removedAt != nil && removedAt.After(o.createdAt)) &&
		(o.removedAt == nil || removedAt.After(o.removedAt)) {
		o.removedAt = removedAt
		return true
	}
	return false
}

// RHTNodes returns the ElementRHT nodes.
func (o *Object) RHTNodes() []*ElementRHTNode {
	return o.memberNodes.Nodes()
}

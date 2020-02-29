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
	"fmt"
	"sort"
	"strings"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// Object represents a JSON object, but unlike regular JSON, it has time
// tickets which is created by logical clock.
type Object struct {
	memberNodes *RHT
	createdAt   *time.Ticket
	deletedAt   *time.Ticket
}

// NewObject creates a new instance of Object.
func NewObject(memberNodes *RHT, createdAt *time.Ticket) *Object {
	return &Object{
		memberNodes: memberNodes,
		createdAt:   createdAt,
	}
}

// Set sets the given element of the given key.
func (o *Object) Set(k string, v Element) {
	o.memberNodes.Set(k, v)
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

// RemoveByCreatedAt removes the element of the given creation time.
func (o *Object) RemoveByCreatedAt(createdAt *time.Ticket, deletedAt *time.Ticket) Element {
	return o.memberNodes.RemoveByCreatedAt(createdAt, deletedAt)
}

// Remove removes the element of the given key.
func (o *Object) Remove(k string, deletedAt *time.Ticket) Element {
	return o.memberNodes.Remove(k, deletedAt)
}

func (o *Object) Descendants(descendants chan Element) {
	for _, node := range o.memberNodes.AllNodes() {
		switch elem := node.elem.(type) {
		case *Object:
			elem.Descendants(descendants)
		case *Array:
			elem.Descendants(descendants)
		}
		descendants <- node.elem
	}
}

// Marshal returns the JSON encoding of this object.
func (o *Object) Marshal() string {
	members := o.memberNodes.Elements()

	size := len(members)
	keys := make([]string, 0, size)
	for k := range members {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	sb := strings.Builder{}
	sb.WriteString("{")

	idx := 0
	for _, k := range keys {
		value := members[k]
		sb.WriteString(fmt.Sprintf("\"%s\":%s", k, value.Marshal()))
		if size-1 != idx {
			sb.WriteString(",")
		}
		idx++
	}
	sb.WriteString("}")

	return sb.String()
}

// Deepcopy copies itself deeply.
func (o *Object) Deepcopy() Element {
	members := NewRHT()

	for _, node := range o.memberNodes.AllNodes() {
		members.Set(node.key, node.elem.Deepcopy())
	}

	obj := NewObject(members, o.createdAt)
	obj.deletedAt = o.deletedAt
	return obj
}

// CreatedAt returns the creation time of this object.
func (o *Object) CreatedAt() *time.Ticket {
	return o.createdAt
}

// RemovedAt returns the deletion time of this object.
func (o *Object) DeletedAt() *time.Ticket {
	return o.deletedAt
}

// Delete deletes this object.
func (o *Object) Delete(deletedAt *time.Ticket) {
	o.deletedAt = deletedAt
}

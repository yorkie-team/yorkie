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

// Package crdt provides the implementation of the CRDT data structure.
// The CRDT data structure is a data structure that can be replicated and
// shared among multiple replicas.
package crdt

import (
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// ElementPair represents pair that has a parent element and child element.
type ElementPair struct {
	parent Container
	elem   Element
}

// Root is a structure represents the root of JSON. It has a hash table of
// all JSON elements to find a specific element when applying remote changes
// received from server.
//
// Every element has a unique time ticket at creation, which allows us to find
// a particular element.
type Root struct {
	object                               *Object
	elementMapByCreatedAt                map[string]Element
	removedElementPairMapByCreatedAt     map[string]ElementPair
	elementHasRemovedNodesSetByCreatedAt map[string]GCElement
}

// NewRoot creates a new instance of Root.
func NewRoot(root *Object) *Root {
	r := &Root{
		elementMapByCreatedAt:                make(map[string]Element),
		removedElementPairMapByCreatedAt:     make(map[string]ElementPair),
		elementHasRemovedNodesSetByCreatedAt: make(map[string]GCElement),
	}

	r.object = root
	r.RegisterElement(root)

	root.Descendants(func(elem Element, parent Container) bool {
		r.RegisterElement(elem)
		if elem.RemovedAt() != nil {
			r.RegisterRemovedElementPair(parent, elem)
		}
		// TODO(hackerwins): Register text elements with garbage
		return false
	})

	return r
}

// Object returns the root object of the JSON.
func (r *Root) Object() *Object {
	return r.object
}

// FindByCreatedAt returns the element of given creation time.
func (r *Root) FindByCreatedAt(createdAt *time.Ticket) Element {
	return r.elementMapByCreatedAt[createdAt.Key()]
}

// RegisterElement registers the given element to hash table.
func (r *Root) RegisterElement(elem Element) {
	r.elementMapByCreatedAt[elem.CreatedAt().Key()] = elem
}

// DeregisterElement deregister the given element from hash tables.
func (r *Root) DeregisterElement(elem Element) {
	createdAt := elem.CreatedAt().Key()
	delete(r.elementMapByCreatedAt, createdAt)
	delete(r.removedElementPairMapByCreatedAt, createdAt)
}

// RegisterRemovedElementPair register the given element pair to hash table.
func (r *Root) RegisterRemovedElementPair(parent Container, elem Element) {
	r.removedElementPairMapByCreatedAt[elem.CreatedAt().Key()] = ElementPair{
		parent,
		elem,
	}
}

// RegisterElementHasRemovedNodes register the given element with garbage to hash table.
func (r *Root) RegisterElementHasRemovedNodes(element GCElement) {
	r.elementHasRemovedNodesSetByCreatedAt[element.CreatedAt().Key()] = element
}

// DeepCopy copies itself deeply.
func (r *Root) DeepCopy() (*Root, error) {
	copiedObject, err := r.object.DeepCopy()
	if err != nil {
		return nil, err
	}
	return NewRoot(copiedObject.(*Object)), nil
}

// GarbageCollect purge elements that were removed before the given time.
func (r *Root) GarbageCollect(ticket *time.Ticket) (int, error) {
	count := 0

	for _, pair := range r.removedElementPairMapByCreatedAt {
		if pair.elem.RemovedAt() != nil && ticket.Compare(pair.elem.RemovedAt()) >= 0 {
			if err := pair.parent.Purge(pair.elem); err != nil {
				return 0, err
			}

			count += r.garbageCollect(pair.elem)
		}
	}

	for _, node := range r.elementHasRemovedNodesSetByCreatedAt {
		purgedNodes, err := node.purgeRemovedNodesBefore(ticket)
		if err != nil {
			return 0, err
		}

		if purgedNodes > 0 {
			delete(r.elementHasRemovedNodesSetByCreatedAt, node.CreatedAt().Key())
		}
		count += purgedNodes
	}

	return count, nil
}

// ElementMapLen returns the size of element map.
func (r *Root) ElementMapLen() int {
	return len(r.elementMapByCreatedAt)
}

// RemovedElementLen returns the size of removed element map.
func (r *Root) RemovedElementLen() int {
	return len(r.removedElementPairMapByCreatedAt)
}

// GarbageLen returns the count of removed elements.
func (r *Root) GarbageLen() int {
	count := 0

	for _, pair := range r.removedElementPairMapByCreatedAt {
		count++

		switch elem := pair.elem.(type) {
		case Container:
			elem.Descendants(func(elem Element, parent Container) bool {
				count++
				return false
			})
		}
	}

	for _, element := range r.elementHasRemovedNodesSetByCreatedAt {
		count += element.removedNodesLen()
	}

	return count
}

func (r *Root) garbageCollect(elem Element) int {
	count := 0

	callback := func(elem Element, parent Container) bool {
		r.DeregisterElement(elem)
		count++
		return false
	}

	callback(elem, nil)
	switch elem := elem.(type) {
	case *Object:
		elem.Descendants(callback)
	case *Array:
		elem.Descendants(callback)
	}

	return count
}

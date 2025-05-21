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
	"github.com/yorkie-team/yorkie/pkg/resource"
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
	object           *Object
	elementMap       map[string]Element
	gcElementPairMap map[string]ElementPair
	gcNodePairMap    map[string]GCPair
	docSize          resource.DocSize
}

// NewRoot creates a new instance of Root.
func NewRoot(root *Object) *Root {
	r := &Root{
		elementMap:       make(map[string]Element),
		gcElementPairMap: make(map[string]ElementPair),
		gcNodePairMap:    make(map[string]GCPair),
		docSize: resource.DocSize{
			Live: resource.DataSize{
				Data: 0,
				Meta: 0,
			},
			GC: resource.DataSize{
				Data: 0,
				Meta: 0,
			},
		},
	}

	r.object = root
	r.RegisterElement(root)

	root.Descendants(func(elem Element, parent Container) bool {
		if elem.RemovedAt() != nil {
			r.RegisterRemovedElementPair(parent, elem)
		}

		switch e := elem.(type) {
		case *Text:
			for _, pair := range e.GCPairs() {
				r.RegisterGCPair(pair)
			}
		case *Tree:
			for _, pair := range e.GCPairs() {
				r.RegisterGCPair(pair)
			}
		}
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
	return r.elementMap[createdAt.Key()]
}

// RegisterElement registers the given element to hash table.
func (r *Root) RegisterElement(element Element) {
	r.elementMap[element.CreatedAt().Key()] = element
	r.docSize.Live.Add(element.DataSize())

	switch element := element.(type) {
	case Container:
		{
			element.Descendants(func(elem Element, parent Container) bool {
				r.elementMap[elem.CreatedAt().Key()] = elem
				r.docSize.Live.Add(elem.DataSize())
				return false
			})
		}
	}
}

// deregisterElement deregister the given element from hash tables.
func (r *Root) deregisterElement(element Element) int {
	count := 0

	deregisterElementInternal := func(elem Element) {
		createdAt := elem.CreatedAt().Key()
		r.docSize.GC.Sub(elem.DataSize())

		delete(r.elementMap, createdAt)
		delete(r.gcElementPairMap, createdAt)
		count++
	}

	deregisterElementInternal(element)

	switch element := element.(type) {
	case Container:
		{
			element.Descendants(func(elem Element, parent Container) bool {
				deregisterElementInternal(elem)
				return false
			})
		}
	}

	return count
}

// RegisterRemovedElementPair register the given element pair to hash table.
func (r *Root) RegisterRemovedElementPair(parent Container, elem Element) {
	r.docSize.GC.Add(elem.DataSize())
	r.docSize.Live.Sub(elem.DataSize())
	// NOTE(hackerwins): When an element is removed, parent sets the removedAt
	// to mark the child as removed.
	r.docSize.Live.Meta += time.TicketSize

	r.gcElementPairMap[elem.CreatedAt().Key()] = ElementPair{
		parent,
		elem,
	}
}

// DocSize returns the size of the document.
func (r *Root) DocSize() resource.DocSize {
	return r.docSize
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
func (r *Root) GarbageCollect(vector time.VersionVector) (int, error) {
	count := 0

	for _, pair := range r.gcElementPairMap {
		if vector.EqualToOrAfter(pair.elem.RemovedAt()) {
			if err := pair.parent.Purge(pair.elem); err != nil {
				return 0, err
			}

			count += r.deregisterElement(pair.elem)
		}
	}

	for _, pair := range r.gcNodePairMap {
		if vector.EqualToOrAfter(pair.Child.RemovedAt()) {
			if err := pair.Parent.Purge(pair.Child); err != nil {
				return 0, err
			}

			delete(r.gcNodePairMap, pair.Child.IDString())
			count++
		}
	}

	return count, nil
}

// ElementMapLen returns the size of element map.
func (r *Root) ElementMapLen() int {
	return len(r.elementMap)
}

// GarbageElementLen return the count of removed elements.
func (r *Root) GarbageElementLen() int {
	seen := make(map[string]bool)

	for _, pair := range r.gcElementPairMap {
		seen[pair.elem.CreatedAt().Key()] = true

		switch elem := pair.elem.(type) {
		case Container:
			elem.Descendants(func(elem Element, parent Container) bool {
				seen[elem.CreatedAt().Key()] = true
				return false
			})
		}
	}

	return len(seen)
}

// GarbageLen returns the count of removed elements and internal nodes.
func (r *Root) GarbageLen() int {
	return r.GarbageElementLen() + len(r.gcNodePairMap)
}

// RegisterGCPair registers the given pair to hash table.
func (r *Root) RegisterGCPair(pair GCPair) {
	// NOTE(hackerwins): If the child is already registered, it means that the
	// child should be removed from the cache.
	if p, ok := r.gcNodePairMap[pair.Child.IDString()]; ok {
		size := p.Child.DataSize()
		r.docSize.GC.Sub(size)

		delete(r.gcNodePairMap, p.Child.IDString())
		return
	}

	r.gcNodePairMap[pair.Child.IDString()] = pair

	size := pair.Child.DataSize()
	r.docSize.GC.Add(size)
	r.docSize.Live.Sub(size)

	// For non-RHTNode types, we need to account for the used by the removedAt
	// field that tracks when the node was deleted. This metadata remains part
	// of the live document's footprint.
	if _, isRHTNode := pair.Child.(*RHTNode); !isRHTNode {
		r.docSize.Live.Meta += time.TicketSize
	}
}

// Acc accumulates the given DataSize to Live.
func (r *Root) Acc(diff resource.DataSize) {
	r.docSize.Live.Add(diff)
}

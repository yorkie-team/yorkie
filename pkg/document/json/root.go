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

type ElementPair struct {
	parent Container
	elem   Element
}

// Root is a structure represents the root of JSON. It has a hash table of
// all JSON elements to find a specific element when applying remote changes
// received from agent.
//
// Every element has a unique time ticket at creation, which allows us to find
// a particular element.
type Root struct {
	object                           *Object
	elementMapByCreatedAt            map[string]Element
	removedElementPairMapByCreatedAt map[string]ElementPair
	editedTextElementMapByCreatedAt  map[string]TextElement
}

// NewRoot creates a new instance of Root.
func NewRoot(root *Object) *Root {
	r := &Root{
		elementMapByCreatedAt:            make(map[string]Element),
		removedElementPairMapByCreatedAt: make(map[string]ElementPair),
		editedTextElementMapByCreatedAt:  make(map[string]TextElement),
	}

	r.object = root
	r.RegisterElement(root)

	root.Descendants(func(elem Element, parent Container) bool {
		r.RegisterElement(elem)
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

// RegisterEditedTextElement register the given text element to hash table.
func (r *Root) RegisterEditedTextElement(textType TextElement) {
	r.editedTextElementMapByCreatedAt[textType.CreatedAt().Key()] = textType
}

// DeepCopy copies itself deeply.
func (r *Root) DeepCopy() *Root {
	return NewRoot(r.object.DeepCopy().(*Object))
}

// GarbageCollect purge elements that were removed before the given time.
func (r *Root) GarbageCollect(ticket *time.Ticket) int {
	count := 0

	for _, pair := range r.removedElementPairMapByCreatedAt {
		if pair.elem.RemovedAt() != nil && ticket.Compare(pair.elem.RemovedAt()) >= 0 {
			pair.parent.Purge(pair.elem)
			count += r.garbageCollect(pair.elem)
		}
	}

	for _, text := range r.editedTextElementMapByCreatedAt {
		removedNodeCnt := text.cleanupRemovedNodes(ticket)
		if removedNodeCnt > 0 {
			delete(r.editedTextElementMapByCreatedAt, text.CreatedAt().Key())
		}
		count += removedNodeCnt
	}

	return count
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

	for _, text := range r.editedTextElementMapByCreatedAt {
		count += text.removedNodesLen()
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

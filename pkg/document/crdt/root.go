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

// GCPair represents pair that has a parent GCNode and child GCNode.
// Actual GC target is child GCNode.
type GCPair struct {
	Parent GCNode
	Child  GCNode
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
	gcNodePairMapByID                    map[string]GCPair
}

// NewRoot creates a new instance of Root.
func NewRoot(root *Object) *Root {
	r := &Root{
		elementMapByCreatedAt:                make(map[string]Element),
		removedElementPairMapByCreatedAt:     make(map[string]ElementPair),
		elementHasRemovedNodesSetByCreatedAt: make(map[string]GCElement),
		gcNodePairMapByID:                    make(map[string]GCPair),
	}

	r.object = root
	r.RegisterElement(root)

	root.Descendants(func(elem Element, parent Container) bool {
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
func (r *Root) RegisterElement(element Element) {
	r.elementMapByCreatedAt[element.CreatedAt().Key()] = element

	switch element := element.(type) {
	case Container:
		{
			element.Descendants(func(elem Element, parent Container) bool {
				r.elementMapByCreatedAt[elem.CreatedAt().Key()] = elem
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
		delete(r.elementMapByCreatedAt, createdAt)
		delete(r.removedElementPairMapByCreatedAt, createdAt)
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
	r.removedElementPairMapByCreatedAt[elem.CreatedAt().Key()] = ElementPair{
		parent,
		elem,
	}
}

// RegisterElementHasRemovedNodes register the given element with garbage to hash table.
func (r *Root) RegisterElementHasRemovedNodes(element GCElement) {
	r.elementHasRemovedNodesSetByCreatedAt[element.CreatedAt().Key()] = element
}

// RegisterGCNodePairMapByID register the given GCNode pair to hash table.
func (r *Root) RegisterGCNodePairMapByID(key string, parent GCNode, child GCNode) {
	r.gcNodePairMapByID[key] = GCPair{
		parent,
		child,
	}
}

// DeepCopy copies itself deeply.
func (r *Root) DeepCopy() (*Root, error) {
	copiedObject, err := r.object.DeepCopy()
	if err != nil {
		return nil, err
	}
	return NewRoot(copiedObject.(*Object)), nil
}

func (r *Root) buildTreeForGC() map[string]*GCTreeNode {
	gcTreeNodeMap := make(map[string]*GCTreeNode)

	for k, pair := range r.gcNodePairMapByID {
		child := NewGCTreeNode(pair.Child, k)
		gcTreeNodeMap[k] = child

		parentID := pair.Parent.GetID()
		parentNode, exists := gcTreeNodeMap[parentID]
		if !exists {
			parentNode = NewGCTreeNode(pair.Parent)
			gcTreeNodeMap[parentID] = parentNode
		}

		parentNode.AddChild(child)

		// TODO(raararaara): What to do if it is a type that cannot define Parent?
		// The current specification has the limitation that the target node must have a Parent.
		switch node := pair.Parent.(type) {
		case *TreeNode:
			node.liftUp(gcTreeNodeMap)
		}
	}

	return gcTreeNodeMap
}

func (r *Root) traverseForGC(
	currentNode *GCTreeNode,
	gcTreeNodeMap map[string]*GCTreeNode,
	ticket *time.Ticket,
	skip bool,
) (int, error) {
	var totalPurged = 0
	for _, child := range currentNode.children {
		if _, ok := r.gcNodePairMapByID[child.GetID()]; ok {

			if !skip {
				subTreeCount, err := currentNode.Purge(child, ticket)
				if err != nil {
					return 0, err
				}
				if subTreeCount > 0 {
					totalPurged += subTreeCount
				}
				skip = true
			}
			if ticket.After(child.removedAt) {
				delete(r.gcNodePairMapByID, child.GetID())
			}
		}

		count, err := r.traverseForGC(child, gcTreeNodeMap, ticket, skip)
		if err != nil {
			return 0, err
		}
		totalPurged += count
	}

	delete(gcTreeNodeMap, currentNode.GetID())
	return totalPurged, nil
}

// GarbageCollect purge elements that were removed before the given time.
func (r *Root) GarbageCollect(ticket *time.Ticket) (int, error) {
	count := 0
	gcTreeNodeMap := r.buildTreeForGC()
	for _, node := range gcTreeNodeMap {
		// TODO(raararaara): A clearer expression is needed than the current method.
		if node.par == nil {
			purgedNodes, err := r.traverseForGC(node, gcTreeNodeMap, ticket, false)
			if err != nil {
				return 0, err
			}
			count += purgedNodes
		}
	}
	for _, pair := range r.removedElementPairMapByCreatedAt {
		if pair.elem.RemovedAt() != nil && ticket.Compare(pair.elem.RemovedAt()) >= 0 {
			if err := pair.parent.Purge(pair.elem); err != nil {
				return 0, err
			}

			count += r.deregisterElement(pair.elem)
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
	seen := make(map[string]bool)

	for _, pair := range r.removedElementPairMapByCreatedAt {
		seen[pair.elem.CreatedAt().Key()] = true

		switch elem := pair.elem.(type) {
		case Container:
			elem.Descendants(func(elem Element, parent Container) bool {
				seen[elem.CreatedAt().Key()] = true
				return false
			})
		}
	}

	count += len(seen)

	count += len(r.gcNodePairMapByID)

	for _, element := range r.elementHasRemovedNodesSetByCreatedAt {
		count += element.removedNodesLen()
	}

	return count
}

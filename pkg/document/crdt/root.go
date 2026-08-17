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
	"github.com/yorkie-team/yorkie/pkg/document/resource"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// ElementPair represents pair that has a parent element and child element.
type ElementPair struct {
	parent Container
	elem   Element
}

// Elem returns the element of the ElementPair for testing purposes.
func (p *ElementPair) Elem() Element {
	return p.elem
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

	// sizeInGC maps the creation time of every registered element whose size
	// counts toward docSize.GC rather than docSize.Live, to the exact amount
	// charged. Each element's size belongs to exactly one of the two, and an
	// element reaches GC by more routes than it has removals: it can be
	// removed itself, or be a descendant of a removed container. Recording
	// the amount rather than a flag keeps the two sides symmetric even though
	// DataSize is not stable over an element's lifetime -- it grows by a
	// ticket the moment removedAt is set, which can happen after the size has
	// already moved.
	sizeInGC map[string]resource.DataSize
}

// NewRoot creates a new instance of Root.
func NewRoot(root *Object) *Root {
	r := &Root{
		elementMap:       make(map[string]Element),
		gcElementPairMap: make(map[string]ElementPair),
		gcNodePairMap:    make(map[string]GCPair),
		sizeInGC:         make(map[string]resource.DataSize),
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
		case *Array:
			for _, pair := range e.GCPairs() {
				r.RegisterGCPair(pair)
			}
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

	if element, ok := element.(Container); ok {
		element.Descendants(func(elem Element, parent Container) bool {
			r.elementMap[elem.CreatedAt().Key()] = elem
			r.docSize.Live.Add(elem.DataSize())
			return false
		})
	}
}

// DeregisterElement deregisters the given element and its descendants from
// the hash tables. It is exported for operations that must replace an
// element under a pre-existing createdAt during undo/redo (set_operation.ts:
// 98-104 in the JS SDK), where a previously tombstoned element is restored
// under the same identity and its stale entry must be dropped first.
func (r *Root) DeregisterElement(element Element) int {
	return r.deregisterElement(element)
}

// deregisterElement deregister the given element from hash tables.
func (r *Root) deregisterElement(element Element) int {
	count := 0

	deregister := func(elem Element) {
		createdAt := elem.CreatedAt().Key()
		// Subtract the size from wherever it is actually counted, and by the
		// amount actually charged. A descendant created inside an
		// already-removed container never passed through a removal, so it
		// still sits in Live; subtracting it from GC would push GC below zero
		// and leave its cost in Live forever.
		if charged, ok := r.sizeInGC[createdAt]; ok {
			r.docSize.GC.Sub(charged)
			delete(r.sizeInGC, createdAt)
		} else {
			r.docSize.Live.Sub(elem.DataSize())
		}

		delete(r.elementMap, createdAt)
		delete(r.gcElementPairMap, createdAt)
		count++
	}

	deregister(element)

	if element, ok := element.(Container); ok {
		element.Descendants(func(elem Element, parent Container) bool {
			deregister(elem)
			return false
		})
	}

	return count
}

// RegisterRemovedElementPair register the given element pair to hash table.
func (r *Root) RegisterRemovedElementPair(parent Container, elem Element) {
	moved := r.moveSizeToGC(elem)

	// NOTE(hackerwins): RegisterElement books a container and every descendant
	// into Live, and deregisterElement subtracts both when the tombstone is
	// collected. Removing a container therefore has to move its descendants as
	// well: booking only the container itself would strand their size in Live
	// forever and drive GC negative once the collection subtracted them.
	if container, ok := elem.(Container); ok {
		container.Descendants(func(e Element, _ Container) bool {
			r.moveSizeToGC(e)
			return false
		})
	}

	// NOTE(hackerwins): When an element is removed, parent sets the removedAt
	// to mark the child as removed. That ticket is part of the size charged to
	// GC just now, but it was not part of what Live held -- RegisterElement ran
	// before the removal -- so Live gets it back. Only on the move that carried
	// it: a size already in GC, or one moved as a descendant while its own
	// removedAt is still unset, did not.
	//
	// This holds for the incremental path. NewRoot instead registers an
	// already-tombstoned element at its post-removal size, so Live did hold the
	// ticket and the refund over-credits it by one per tombstone. That drift is
	// pre-existing and unchanged here; see the follow-up task
	// docs/tasks/active/20260817-docsize-snapshot-rebuild-drift-todo.md.
	if moved && elem.RemovedAt() != nil {
		r.docSize.Live.Meta += time.TicketSize
	}

	r.gcElementPairMap[elem.CreatedAt().Key()] = ElementPair{
		parent,
		elem,
	}
}

// moveSizeToGC moves the size of the given element from Live to GC, and
// reports whether it moved a size Live was holding. A size already in GC --
// because the element was removed before, or because a container above it was
// -- only has its charge topped up: DataSize grows by a ticket when removedAt
// is set, which can happen after the move.
func (r *Root) moveSizeToGC(elem Element) bool {
	createdAt := elem.CreatedAt().Key()
	size := elem.DataSize()

	charged, ok := r.sizeInGC[createdAt]
	if ok {
		diff := size
		diff.Sub(charged)
		r.docSize.GC.Add(diff)
		r.sizeInGC[createdAt] = size
		return false
	}

	r.docSize.GC.Add(size)
	r.docSize.Live.Sub(size)
	r.sizeInGC[createdAt] = size
	return true
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

			r.docSize.GC.Sub(pair.Child.DataSize())
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

		if elem, ok := pair.elem.(Container); ok {
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
		// Subtract exactly what registration added: GCOnlySize for a
		// born-dead split piece (only its net-new size was added to GC),
		// the full child size otherwise.
		if p.GCOnlySize != nil {
			r.docSize.GC.Sub(*p.GCOnlySize)
		} else {
			r.docSize.GC.Sub(p.Child.DataSize())
		}

		delete(r.gcNodePairMap, p.Child.IDString())
		return
	}

	r.gcNodePairMap[pair.Child.IDString()] = pair

	// NOTE: A born-removed split piece was never counted in docSize.Live,
	// so only its net-new size is added to GC (Live is left untouched by
	// AdjustDiffForGCPair below).
	if pair.GCOnlySize != nil {
		r.docSize.GC.Add(*pair.GCOnlySize)
		return
	}

	size := pair.Child.DataSize()
	r.docSize.GC.Add(size)
}

// UnregisterGCPair removes a GC pair whose child has been restored
// (un-tombstoned) by an identity-preserving undo, moving its size GC→Live.
func (r *Root) UnregisterGCPair(pair GCPair) {
	_, ok := r.gcNodePairMap[pair.Child.IDString()]
	if !ok {
		return
	}

	// NOTE: Unlike RegisterGCPair, GCOnlySize doesn't apply here. It only
	// exists to avoid double-counting data at registration time, when a
	// split-born child's content was already counted via a sibling's
	// existing registration. Once registered, this entry's contribution
	// to docSize.GC is always the child's own current DataSize() — that's
	// what must come back out, regardless of how it went in.
	//
	// The caller clears removedAt before calling this, so DataSize() no
	// longer includes the removedAt ticket that WAS counted while it was
	// still registered. Add it back explicitly so GC doesn't retain a
	// stale ticket's worth of residue.
	r.docSize.GC.Sub(pair.Child.DataSize())
	if _, isRHTNode := pair.Child.(*RHTNode); !isRHTNode {
		r.docSize.GC.Meta -= time.TicketSize
	}

	delete(r.gcNodePairMap, pair.Child.IDString())
}

// Acc accumulates the given DataSize to Live.
func (r *Root) Acc(diff resource.DataSize) {
	r.docSize.Live.Add(diff)
}

// AdjustDiffForGCPair adjusts the given diff for the given GCPair.
func (r *Root) AdjustDiffForGCPair(diff *resource.DataSize, pair GCPair) {
	// NOTE: A born-removed split piece was never in docSize.Live, so there
	// is nothing to subtract from Live for it.
	if pair.GCOnlySize != nil {
		return
	}

	size := pair.Child.DataSize()
	diff.Sub(size)

	// NOTE(hackerwins): In general cases, when removing a node, its size
	// includes removedAt, so when subtracting the node size from docSize.Live,
	// we need to subtract the removedAt size. However, RHTNode doesn't have
	// removedAt, so we don't need to subtract it from the Live size.
	if _, isRHTNode := pair.Child.(*RHTNode); !isRHTNode {
		diff.Meta += time.TicketSize
	}
}

// GCElementPairMap returns the gcElementPairMap for testing purposes.
func (r *Root) GCElementPairMap() map[string]ElementPair {
	return r.gcElementPairMap
}

/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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

// Package heap provides a generic priority queue implementation.
// It can be configured to work as either a min heap or max heap
// depending on the comparison function provided.
package heap

import "container/heap"

// Heap is a generic priority queue that maintains a fixed maximum size.
// It can work as a min heap (with a < b) or max heap (with a > b).
// It's particularly useful for finding top-K or bottom-K elements efficiently.
type Heap[T any] struct {
	items   []T
	maxSize int
	less    func(a, b T) bool
}

// internal heap implementation for container/heap
type internalHeap[T any] struct {
	items []T
	less  func(a, b T) bool
}

func (h internalHeap[T]) Len() int           { return len(h.items) }
func (h internalHeap[T]) Less(i, j int) bool { return h.less(h.items[i], h.items[j]) }
func (h internalHeap[T]) Swap(i, j int)      { h.items[i], h.items[j] = h.items[j], h.items[i] }

func (h *internalHeap[T]) Push(x interface{}) {
	h.items = append(h.items, x.(T))
}

func (h *internalHeap[T]) Pop() interface{} {
	old := h.items
	n := len(old)
	x := old[n-1]
	h.items = old[0 : n-1]
	return x
}

// New creates a new Heap with the given maximum size and comparison function.
// The less function determines the heap ordering:
//   - For min heap behavior: use func(a, b T) bool { return a < b }
//   - For max heap behavior: use func(a, b T) bool { return a > b }
//
// If maxSize is 0, the heap will have unlimited size.
func New[T any](maxSize int, less func(a, b T) bool) *Heap[T] {
	return &Heap[T]{
		items:   make([]T, 0),
		maxSize: maxSize,
		less:    less,
	}
}

// Push adds an element to the heap.
// If the heap has reached maxSize and the new element has higher priority,
// it replaces the root element.
func (h *Heap[T]) Push(item T) {
	ih := &internalHeap[T]{items: h.items, less: h.less}

	if h.maxSize > 0 && len(h.items) >= h.maxSize {
		// Heap is full, check if we should replace the root
		if !h.less(item, h.items[0]) {
			// New item has higher priority, replace the root
			heap.Pop(ih)
			heap.Push(ih, item)
		}
		// Otherwise, ignore the new item
	} else {
		// Heap is not full, just add the item
		heap.Push(ih, item)
	}

	h.items = ih.items
}

// Pop removes and returns the root element from the heap.
// For min heap, this returns the minimum element.
// For max heap, this returns the maximum element.
// Returns the zero value of T if the heap is empty.
func (h *Heap[T]) Pop() T {
	if len(h.items) == 0 {
		var zero T
		return zero
	}

	ih := &internalHeap[T]{items: h.items, less: h.less}
	item := heap.Pop(ih).(T)
	h.items = ih.items
	return item
}

// Peek returns the root element without removing it.
// For min heap, this returns the minimum element.
// For max heap, this returns the maximum element.
// Returns the zero value of T if the heap is empty.
func (h *Heap[T]) Peek() T {
	if len(h.items) == 0 {
		var zero T
		return zero
	}
	return h.items[0]
}

// Len returns the number of elements in the heap.
func (h *Heap[T]) Len() int {
	return len(h.items)
}

// IsEmpty returns true if the heap is empty.
func (h *Heap[T]) IsEmpty() bool {
	return len(h.items) == 0
}

// IsFull returns true if the heap has reached its maximum size.
// Always returns false if maxSize is 0 (unlimited).
func (h *Heap[T]) IsFull() bool {
	if h.maxSize == 0 {
		return false
	}
	return len(h.items) >= h.maxSize
}

// Items returns a copy of all items in the heap.
// The order is not guaranteed to be sorted.
func (h *Heap[T]) Items() []T {
	result := make([]T, len(h.items))
	copy(result, h.items)
	return result
}

// Clear removes all elements from the heap.
func (h *Heap[T]) Clear() {
	h.items = make([]T, 0)
}

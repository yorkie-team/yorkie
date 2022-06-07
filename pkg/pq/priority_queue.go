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

package pq

import (
	"container/heap"
)

// PriorityQueue is a priority queue implemented with max heap.
type PriorityQueue[V Value] struct {
	queue *internalQueue[V]
}

// NewPriorityQueue creates an instance of NewPriorityQueue.
func NewPriorityQueue[V Value]() *PriorityQueue[V] {
	pq := &internalQueue[V]{}
	heap.Init(pq)

	return &PriorityQueue[V]{
		queue: pq,
	}
}

// Peek returns the maximum element from this PriorityQueue.
func (pq *PriorityQueue[V]) Peek() V {
	return pq.queue.Peek().(*pqItem[V]).value
}

// Pop removes and returns the maximum element from this PriorityQueue.
func (pq *PriorityQueue[V]) Pop() V {
	return heap.Pop(pq.queue).(*pqItem[V]).value
}

// Push pushes the element x onto this PriorityQueue.
func (pq *PriorityQueue[V]) Push(value V) {
	item := newPQItem(value)
	heap.Push(pq.queue, item)
}

// Len is the number of elements in this PriorityQueue.
func (pq *PriorityQueue[V]) Len() int {
	return pq.queue.Len()
}

// Release deletes the given value from this PriorityQueue.
func (pq *PriorityQueue[V]) Release(value V) {
	idx := -1
	for i, item := range *pq.queue {
		if !item.value.Less(value) && !value.Less(item.value) {
			idx = i
			break
		}
	}

	if idx == -1 {
		return
	}

	heap.Remove(pq.queue, idx)
}

// Values returns the values of this PriorityQueue.
func (pq *PriorityQueue[V]) Values() []V {
	var values []V
	for _, item := range *pq.queue {
		values = append(values, item.value)
	}
	return values
}

// Value represents the data stored by PriorityQueue.
type Value interface {
	Less(other Value) bool
}

// pqItem is something we manage in a priority queue.
type pqItem[V Value] struct {
	value V
	index int
}

func newPQItem[V Value](value V) *pqItem[V] {
	return &pqItem[V]{
		value: value,
		index: -1,
	}
}

// A internalQueue implements heap.Interface and holds Items.
type internalQueue[V Value] []*pqItem[V]

// Len is the number of elements in this internalQueue.
func (pq internalQueue[V]) Len() int { return len(pq) }

// Less reports whether the element with
// index i should sort before the element with index j.
func (pq internalQueue[V]) Less(i, j int) bool {
	// We want Pop to give us the highest priority, not the lowest, so we use greater than here.
	return pq[i].value.Less(pq[j].value)
}

// Swap swaps the elements with indexes i and j.
func (pq internalQueue[V]) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}

// Push pushes the element x onto this internalQueue.
func (pq *internalQueue[V]) Push(x interface{}) {
	n := len(*pq)
	item := x.(*pqItem[V])
	item.index = n
	*pq = append(*pq, item)
}

// Pop removes and returns the maximum element from this internalQueue.
func (pq *internalQueue[V]) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil  // avoid memory leak
	item.index = -1 // for safety
	*pq = old[0 : n-1]
	return item
}

// Peek returns the maximum element from this internalQueue.
func (pq *internalQueue[V]) Peek() interface{} {
	return (*pq)[0]
}

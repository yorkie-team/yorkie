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

type PriorityQueue struct {
	queue *internalQueue
}

func NewPriorityQueue() *PriorityQueue {
	pq := &internalQueue{}
	heap.Init(pq)

	return &PriorityQueue{
		queue: pq,
	}
}

func (pq *PriorityQueue) Peek() Value {
	return pq.queue.Peek().(*Item).value
}

func (pq *PriorityQueue) Pop() Value {
	return heap.Pop(pq.queue).(*Item).value
}

func (pq *PriorityQueue) Push(value Value) {
	item := NewItem(value)
	heap.Push(pq.queue, item)
}

func (pq *PriorityQueue) Release(value Value) {
	queue := &internalQueue{}
	heap.Init(queue)

	for _, item := range *pq.queue {
		if item.value != value {
			heap.Push(queue, item)
		}
	}

	pq.queue = queue
}

func (pq *PriorityQueue) Values() []Value {
	var values []Value
	for _, item := range *pq.queue {
		values = append(values, item.value)
	}
	return values
}

type Value interface {
	Less(other Value) bool
}

// Item is something we manage in a priority queue.
type Item struct {
	value Value
	index int
}

func NewItem(value Value) *Item {
	return &Item{
		value: value,
		index: -1,
	}
}

// A internalQueue implements heap.Interface and holds Items.
type internalQueue []*Item

func (pq internalQueue) Len() int { return len(pq) }

func (pq internalQueue) Less(i, j int) bool {
	// We want Pop to give us the highest, not lowest, priority so we use greater than here.
	return pq[i].value.Less(pq[j].value)
}

func (pq internalQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}

func (pq *internalQueue) Push(x interface{}) {
	n := len(*pq)
	item := x.(*Item)
	item.index = n
	*pq = append(*pq, item)
}

func (pq *internalQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil  // avoid memory leak
	item.index = -1 // for safety
	*pq = old[0 : n-1]
	return item
}

func (pq *internalQueue) Peek() interface{} {
	return (*pq)[0]
}

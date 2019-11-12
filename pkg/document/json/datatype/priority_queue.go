package datatype

import "container/heap"

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

func (pq *PriorityQueue) Peek() *PQItem {
	return pq.queue.Peek().(*PQItem)
}

func (pq *PriorityQueue) Pop() *PQItem {
	return heap.Pop(pq.queue).(*PQItem)
}

func (pq *PriorityQueue) Push(value Element) *PQItem {
	item := NewPQItem(value)
	heap.Push(pq.queue, item)
	return item
}

// PQItem is something we manage in a priority queue.
type PQItem struct {
	value     Element // The value of the item; arbitrary.
	isRemoved bool    // Whether the item is removed or not.
	index     int     // The index of the item in the heap.
}

func (item *PQItem) Remove() {
	item.isRemoved = true
}

func NewPQItem(value Element) *PQItem {
	return &PQItem{
		value:     value,
		isRemoved: false,
		index:     -1,
	}
}

// A internalQueue implements heap.Interface and holds Items.
type internalQueue []*PQItem

func (pq internalQueue) Len() int { return len(pq) }

func (pq internalQueue) Less(i, j int) bool {
	// We want Pop to give us the highest, not lowest, priority so we use greater than here.
	return pq[i].value.CreatedAt().After(pq[j].value.CreatedAt())
}

func (pq internalQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}

func (pq *internalQueue) Push(x interface{}) {
	n := len(*pq)
	item := x.(*PQItem)
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

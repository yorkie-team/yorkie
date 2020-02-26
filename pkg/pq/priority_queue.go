package pq

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

func (pq *PriorityQueue) Peek() PQValue {
	return pq.queue.Peek().(*PQItem).value
}

func (pq *PriorityQueue) Pop() PQValue {
	return heap.Pop(pq.queue).(*PQItem).value
}

func (pq *PriorityQueue) Push(value PQValue) {
	item := NewPQItem(value)
	heap.Push(pq.queue, item)
}

func (pq *PriorityQueue) Values() []PQValue {
	var values []PQValue
	for _, item := range *pq.queue {
		values = append(values, item.value)
	}
	return values
}

type PQValue interface {
	Less(other PQValue) bool
}

// PQItem is something we manage in a priority queue.
type PQItem struct {
	value PQValue
	index int
}

func NewPQItem(value PQValue) *PQItem {
	return &PQItem{
		value: value,
		index: -1,
	}
}

// A internalQueue implements heap.Interface and holds Items.
type internalQueue []*PQItem

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

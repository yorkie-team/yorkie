package json

import (
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/log"
	"github.com/yorkie-team/yorkie/pkg/pq"
)

type rhtNode struct {
	key  string
	elem Element
}

func newRHTNode(key string, elem Element) *rhtNode {
	return &rhtNode{
		key:  key,
		elem: elem,
	}
}

func (n *rhtNode) Delete(deletedAt *time.Ticket) {
	n.elem.Delete(deletedAt)
}

func (n *rhtNode) Less(other pq.PQValue) bool {
	node := other.(*rhtNode)
	return n.elem.CreatedAt().After(node.elem.CreatedAt())
}

func (n *rhtNode) isDeleted() bool {
	return n.elem.DeletedAt() != nil
}

// RHT is replicated hash table.
type RHT struct {
	nodeQueueMapByKey  map[string]*pq.PriorityQueue
	nodeMapByCreatedAt map[string]*rhtNode
}

// NewRHT creates a new instance of RHT.
func NewRHT() *RHT {
	return &RHT{
		nodeQueueMapByKey:  make(map[string]*pq.PriorityQueue),
		nodeMapByCreatedAt: make(map[string]*rhtNode),
	}
}

// Get returns the value of the given key.
func (rht *RHT) Get(key string) Element {
	if queue, ok := rht.nodeQueueMapByKey[key]; ok {
		node := queue.Peek().(*rhtNode)
		if node.isDeleted() {
			return nil
		}
		return node.elem
	}

	return nil
}

// Has returns whether the element exists of the given key or not.
func (rht *RHT) Has(key string) bool {
	if queue, ok := rht.nodeQueueMapByKey[key]; ok {
		node := queue.Peek().(*rhtNode)
		return node != nil && !node.isDeleted()
	}

	return false
}

// Set sets the value of the given key.
func (rht *RHT) Set(k string, v Element) {
	if _, ok := rht.nodeQueueMapByKey[k]; !ok {
		rht.nodeQueueMapByKey[k] = pq.NewPriorityQueue()
	}

	node := newRHTNode(k, v)
	rht.nodeQueueMapByKey[k].Push(node)
	rht.nodeMapByCreatedAt[v.CreatedAt().Key()] = node
}

// Remove removes the Element of the given key.
func (rht *RHT) Remove(k string, deletedAt *time.Ticket) Element {
	if queue, ok := rht.nodeQueueMapByKey[k]; ok {
		node := queue.Peek().(*rhtNode)
		node.Delete(deletedAt)
		return node.elem
	}
	return nil
}

// RemoveByCreatedAt removes the Element of the given creation time.
func (rht *RHT) RemoveByCreatedAt(createdAt *time.Ticket, deletedAt *time.Ticket) Element {
	if node, ok := rht.nodeMapByCreatedAt[createdAt.Key()]; ok {
		node.Delete(deletedAt)
		return node.elem
	}

	log.Logger.Warn("fail to find " + createdAt.Key())
	return nil
}

// Members returns a map of elements because the map easy to use for loop.
// TODO If we encounter performance issues, we need to replace this with other solution.
func (rht *RHT) Elements() map[string]Element {
	members := make(map[string]Element)
	for _, queue := range rht.nodeQueueMapByKey {
		if node := queue.Peek().(*rhtNode); !node.isDeleted() {
			members[node.key] = node.elem

		}
	}

	return members
}

// Members returns a map of elements because the map easy to use for loop.
// TODO If we encounter performance issues, we need to replace this with other solution.
func (rht *RHT) AllNodes() []*rhtNode {
	var nodes []*rhtNode
	for _, queue := range rht.nodeQueueMapByKey {
		for _, value := range queue.Values() {
			nodes = append(nodes, value.(*rhtNode))

		}
	}

	return nodes
}

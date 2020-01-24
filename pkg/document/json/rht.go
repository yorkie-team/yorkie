package json

import (
	"github.com/hackerwins/yorkie/pkg/document/time"
	"github.com/hackerwins/yorkie/pkg/log"
	"github.com/hackerwins/yorkie/pkg/pq"
)

type rhtNode struct {
	key       string
	elem      Element
	isRemoved bool
}

func newRHTNode(key string, elem Element) *rhtNode {
	return &rhtNode{
		key:       key,
		elem:      elem,
		isRemoved: false,
	}
}

func (n *rhtNode) Remove() {
	n.isRemoved = true
}

func (n *rhtNode) Less(other pq.PQValue) bool {
	node := other.(*rhtNode)
	return n.elem.CreatedAt().After(node.elem.CreatedAt())
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
		if node.isRemoved {
			return nil
		}
		return node.elem
	}

	return nil
}

// Set sets the value of the given key.
func (rht *RHT) Set(k string, v Element, isRemoved bool) {
	if _, ok := rht.nodeQueueMapByKey[k]; !ok {
		rht.nodeQueueMapByKey[k] = pq.NewPriorityQueue()
	}

	node := newRHTNode(k, v)
	node.isRemoved = isRemoved
	rht.nodeQueueMapByKey[k].Push(node)
	rht.nodeMapByCreatedAt[v.CreatedAt().Key()] = node
}

// Remove removes the Element of the given key.
func (rht *RHT) Remove(k string) Element {
	if queue, ok := rht.nodeQueueMapByKey[k]; ok {
		node := queue.Peek().(*rhtNode)
		node.Remove()
		return node.elem
	}
	return nil
}

// RemoveByCreatedAt removes the Element of the given creation time.
func (rht *RHT) RemoveByCreatedAt(createdAt *time.Ticket) Element {
	if node, ok := rht.nodeMapByCreatedAt[createdAt.Key()]; ok {
		node.Remove()
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
		if node := queue.Peek().(*rhtNode); !node.isRemoved {
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

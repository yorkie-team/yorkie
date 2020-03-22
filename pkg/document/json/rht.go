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
	"github.com/yorkie-team/yorkie/pkg/log"
	"github.com/yorkie-team/yorkie/pkg/pq"
)

type RHTNode struct {
	key  string
	elem Element
}

func newRHTNode(key string, elem Element) *RHTNode {
	return &RHTNode{
		key:  key,
		elem: elem,
	}
}

func (n *RHTNode) Delete(deletedAt *time.Ticket) {
	n.elem.Delete(deletedAt)
}

func (n *RHTNode) Less(other pq.Value) bool {
	node := other.(*RHTNode)
	return n.elem.CreatedAt().After(node.elem.CreatedAt())
}

func (n *RHTNode) isDeleted() bool {
	return n.elem.DeletedAt() != nil
}

func (n *RHTNode) Key() string {
	return n.key
}

func (n *RHTNode) Element() Element {
	return n.elem
}

// RHT is replicated hash table.
type RHT struct {
	nodeQueueMapByKey  map[string]*pq.PriorityQueue
	nodeMapByCreatedAt map[string]*RHTNode
}

// NewRHT creates a new instance of RHT.
func NewRHT() *RHT {
	return &RHT{
		nodeQueueMapByKey:  make(map[string]*pq.PriorityQueue),
		nodeMapByCreatedAt: make(map[string]*RHTNode),
	}
}

// Get returns the value of the given key.
func (rht *RHT) Get(key string) Element {
	if queue, ok := rht.nodeQueueMapByKey[key]; ok {
		node := queue.Peek().(*RHTNode)
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
		node := queue.Peek().(*RHTNode)
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
		node := queue.Peek().(*RHTNode)
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

// Elements returns a map of elements because the map easy to use for loop.
// TODO If we encounter performance issues, we need to replace this with other solution.
func (rht *RHT) Elements() map[string]Element {
	members := make(map[string]Element)
	for _, queue := range rht.nodeQueueMapByKey {
		if node := queue.Peek().(*RHTNode); !node.isDeleted() {
			members[node.key] = node.elem
		}
	}

	return members
}

// AllNodes returns a map of elements because the map easy to use for loop.
// TODO If we encounter performance issues, we need to replace this with other solution.
func (rht *RHT) AllNodes() []*RHTNode {
	var nodes []*RHTNode
	for _, queue := range rht.nodeQueueMapByKey {
		for _, value := range queue.Values() {
			nodes = append(nodes, value.(*RHTNode))
		}
	}

	return nodes
}

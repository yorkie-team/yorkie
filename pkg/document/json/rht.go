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
	"fmt"
	"sort"
	"strings"

	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/log"
)

// RHTNode is a node of RHT(Replicated Hashtable).
type RHTNode struct {
	key       string
	val       string
	updatedAt *time.Ticket
	removedAt *time.Ticket
}

func newRHTNode(key, val string, updatedAt *time.Ticket) *RHTNode {
	return &RHTNode{
		key:       key,
		val:       val,
		updatedAt: updatedAt,
	}
}

// Remove removes this node. It only marks the deleted time (tombstone).
func (n *RHTNode) Remove(removedAt *time.Ticket) {
	if n.removedAt == nil || removedAt.After(n.removedAt) {
		n.removedAt = removedAt
	}
}

func (n *RHTNode) isRemoved() bool {
	return n.removedAt != nil
}

// Key returns the key of this node.
func (n *RHTNode) Key() string {
	return n.key
}

// Value returns the value of this node.
func (n *RHTNode) Value() string {
	return n.val
}

// UpdatedAt returns the last update time.
func (n *RHTNode) UpdatedAt() *time.Ticket {
	return n.updatedAt
}

// RemovedAt returns the deletion time of this node.
func (n *RHTNode) RemovedAt() *time.Ticket {
	return n.removedAt
}

// RHT is a hashtable with logical clock(Replicated hashtable).
// For more details about RHT: http://csl.skku.edu/papers/jpdc11.pdf
type RHT struct {
	nodeMapByKey       map[string]*RHTNode
	nodeMapByCreatedAt map[string]*RHTNode
}

// NewRHT creates a new instance of RHT.
func NewRHT() *RHT {
	return &RHT{
		nodeMapByKey:       make(map[string]*RHTNode),
		nodeMapByCreatedAt: make(map[string]*RHTNode),
	}
}

// Get returns the value of the given key.
func (rht *RHT) Get(key string) string {
	if node, ok := rht.nodeMapByKey[key]; ok {
		if node.isRemoved() {
			return ""
		}
		return node.val
	}

	return ""
}

// Has returns whether the element exists of the given key or not.
func (rht *RHT) Has(key string) bool {
	if node, ok := rht.nodeMapByKey[key]; ok {
		return node != nil && !node.isRemoved()
	}

	return false
}

// Set sets the value of the given key.
func (rht *RHT) Set(k, v string, executedAt *time.Ticket) {
	if node, ok := rht.nodeMapByKey[k]; !ok || executedAt.After(node.updatedAt) {
		newNode := newRHTNode(k, v, executedAt)
		rht.nodeMapByKey[k] = newNode
		rht.nodeMapByCreatedAt[executedAt.Key()] = newNode
	}
}

// Remove removes the Element of the given key.
func (rht *RHT) Remove(k string, executedAt *time.Ticket) string {
	if node, ok := rht.nodeMapByKey[k]; ok && executedAt.After(node.removedAt) {
		node.Remove(executedAt)
		return node.val
	}

	return ""
}

// RemoveByCreatedAt removes the Element of the given creation time.
func (rht *RHT) RemoveByCreatedAt(createdAt *time.Ticket, removedAt *time.Ticket) string {
	if node, ok := rht.nodeMapByCreatedAt[createdAt.Key()]; ok {
		node.Remove(removedAt)
		return node.val
	}

	log.Logger.Warn("fail to find " + createdAt.Key())
	return ""
}

// Elements returns a map of elements because the map easy to use for loop.
// TODO: If we encounter performance issues, we need to replace this with other solution.
func (rht *RHT) Elements() map[string]string {
	members := make(map[string]string)
	for _, node := range rht.nodeMapByKey {
		if !node.isRemoved() {
			members[node.key] = node.val
		}
	}

	return members
}

// Nodes returns a map of elements because the map easy to use for loop.
// TODO: If we encounter performance issues, we need to replace this with other solution.
func (rht *RHT) Nodes() []*RHTNode {
	var nodes []*RHTNode
	for _, node := range rht.nodeMapByKey {
		nodes = append(nodes, node)
	}

	return nodes
}

// DeepCopy copies itself deeply.
func (rht *RHT) DeepCopy() *RHT {
	instance := NewRHT()

	for _, node := range rht.Nodes() {
		instance.Set(node.key, node.val, node.updatedAt)
	}
	return instance
}

// Marshal returns the JSON encoding of this hashtable.
func (rht *RHT) Marshal() string {
	members := rht.Elements()

	size := len(members)

	// Extract and sort the keys
	keys := make([]string, 0, size)
	for k := range members {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	sb := strings.Builder{}
	sb.WriteString("{")
	for idx, k := range keys {
		if idx > 0 {
			sb.WriteString(",")
		}
		value := members[k]
		sb.WriteString(fmt.Sprintf(`"%s":"%s"`, k, value))
	}
	sb.WriteString("}")

	return sb.String()
}

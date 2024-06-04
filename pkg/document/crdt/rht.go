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

package crdt

import (
	"fmt"
	"sort"
	"strings"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// RHTNode is a node of RHT(Replicated Hashtable).
type RHTNode struct {
	key       string
	val       string
	updatedAt *time.Ticket
	isRemoved bool
}

func newRHTNode(key, val string, updatedAt *time.Ticket, isRemoved bool) *RHTNode {
	return &RHTNode{
		key:       key,
		val:       val,
		updatedAt: updatedAt,
		isRemoved: isRemoved,
	}
}

// IDString returns the string representation of this node.
func (n *RHTNode) IDString() string {
	return n.updatedAt.Key() + ":" + n.key
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

// RemovedAt returns the time when this node was removed.
func (n *RHTNode) RemovedAt() *time.Ticket {
	if n.isRemoved {
		return n.updatedAt
	}

	return nil
}

// IsRemoved returns whether this node is removed or not.
func (n *RHTNode) IsRemoved() bool {
	return n.isRemoved
}

// RHT is a hashtable with logical clock(Replicated hashtable).
// For more details about RHT: http://csl.skku.edu/papers/jpdc11.pdf
// NOTE(justiceHui): RHT and ElementRHT has duplicated functions.
type RHT struct {
	nodeMapByKey           map[string]*RHTNode
	numberOfRemovedElement int
}

// NewRHT creates a new instance of RHT.
func NewRHT() *RHT {
	return &RHT{
		nodeMapByKey:           make(map[string]*RHTNode),
		numberOfRemovedElement: 0,
	}
}

// Get returns the value of the given key.
func (rht *RHT) Get(key string) string {
	if node, ok := rht.nodeMapByKey[key]; ok {
		if node.isRemoved {
			return ""
		}
		return node.val
	}

	return ""
}

// Has returns whether the element exists of the given key or not.
func (rht *RHT) Has(key string) bool {
	if node, ok := rht.nodeMapByKey[key]; ok {
		return node != nil && !node.isRemoved
	}

	return false
}

// Set sets the value of the given key.
func (rht *RHT) Set(k, v string, executedAt *time.Ticket) *RHTNode {
	node := rht.nodeMapByKey[k]

	if node != nil && node.isRemoved && executedAt.After(node.updatedAt) {
		rht.numberOfRemovedElement--
	}

	if node == nil || executedAt.After(node.updatedAt) {
		newNode := newRHTNode(k, v, executedAt, false)
		rht.nodeMapByKey[k] = newNode
	}

	if node != nil && node.isRemoved {
		return node
	}

	return nil
}

// SetInternal sets the value of the given key internally.
func (rht *RHT) SetInternal(k string, v string, updatedAt *time.Ticket, removed bool) {
	newNode := newRHTNode(k, v, updatedAt, removed)
	rht.nodeMapByKey[k] = newNode

	if removed {
		rht.numberOfRemovedElement++
	}
}

// Remove removes the value of the given key.
func (rht *RHT) Remove(k string, executedAt *time.Ticket) []*RHTNode {
	// NOTE(hackerwins): We need to consider the logic and the policy of removing the element.
	// A. RHT always overrides the value of the same key in a immutable way.
	// B. Even if the key is not existed, RHT sets the flag `isRemoved` for concurrency.
	node, ok := rht.nodeMapByKey[k]
	if !ok {
		rht.numberOfRemovedElement++
		newNode := newRHTNode(k, "", executedAt, true)
		rht.nodeMapByKey[k] = newNode
		return []*RHTNode{newNode}
	}

	if !executedAt.After(node.updatedAt) {
		return nil
	}

	alreadyRemoved := node.isRemoved
	if !alreadyRemoved {
		rht.numberOfRemovedElement++
	}

	var gcNodes []*RHTNode
	if alreadyRemoved {
		gcNodes = append(gcNodes, node)
	}

	newNode := newRHTNode(k, node.val, executedAt, true)
	rht.nodeMapByKey[k] = newNode
	gcNodes = append(gcNodes, newNode)

	return gcNodes
}

// Elements returns a map of elements because the map easy to use for loop.
// TODO: If we encounter performance issues, we need to replace this with other solution.
func (rht *RHT) Elements() map[string]string {
	members := make(map[string]string)
	for _, node := range rht.nodeMapByKey {
		if !node.isRemoved {
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

// Len returns the number of elements.
func (rht *RHT) Len() int {
	return len(rht.nodeMapByKey) - rht.numberOfRemovedElement
}

// DeepCopy copies itself deeply.
func (rht *RHT) DeepCopy() *RHT {
	instance := NewRHT()

	for _, node := range rht.Nodes() {
		instance.SetInternal(node.key, node.val, node.updatedAt, node.isRemoved)
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
		sb.WriteString(fmt.Sprintf(`"%s":"%s"`, EscapeString(k), EscapeString(value)))
	}
	sb.WriteString("}")

	return sb.String()
}

// Purge purges the given child node.
func (rht *RHT) Purge(child *RHTNode) error {
	if node, ok := rht.nodeMapByKey[child.key]; !ok || node.IDString() != child.IDString() {
		// TODO(hackerwins): Should we return an error when the child is not found?
		return nil
	}

	delete(rht.nodeMapByKey, child.key)
	rht.numberOfRemovedElement--
	return nil
}

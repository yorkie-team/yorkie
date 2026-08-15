/*
 * Copyright 2023 The Yorkie Authors. All rights reserved.
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

// ElementRHTNode is a node of ElementRHT.
type ElementRHTNode struct {
	key  string
	elem Element
}

func newElementRHTNode(key string, elem Element) *ElementRHTNode {
	return &ElementRHTNode{
		key:  key,
		elem: elem,
	}
}

// Remove removes this node. It only marks the deleted time (tombstone).
func (n *ElementRHTNode) Remove(removedAt *time.Ticket) bool {
	if removedAt != nil && removedAt.After(n.elem.CreatedAt()) {
		if n.elem.RemovedAt() == nil || removedAt.After(n.elem.RemovedAt()) {
			return n.elem.Remove(removedAt)
		}
	}
	return false
}

func (n *ElementRHTNode) isRemoved() bool {
	return n.elem.RemovedAt() != nil
}

// Key returns the key of this node.
func (n *ElementRHTNode) Key() string {
	return n.key
}

// Element returns the element of this node.
func (n *ElementRHTNode) Element() Element {
	return n.elem
}

// ElementRHT is a hashtable with logical clock(Replicated hashtable).
type ElementRHT struct {
	// nodeMapByKey is a map with values of nodes by key.
	nodeMapByKey map[string]*ElementRHTNode
	// nodeMapByCreatedAt is a map with values of nodes by creation time.
	// Even if an element is removed by `set` or `delete`, it remains in
	// nodeMapByCreatedAt and will be deleted physically by GC.
	nodeMapByCreatedAt map[string]*ElementRHTNode
}

// NewElementRHT creates a new instance of ElementRHT.
func NewElementRHT() *ElementRHT {
	return &ElementRHT{
		nodeMapByKey:       make(map[string]*ElementRHTNode),
		nodeMapByCreatedAt: make(map[string]*ElementRHTNode),
	}
}

// Get returns the value of the given key.
func (rht *ElementRHT) Get(key string) Element {
	if node, ok := rht.nodeMapByKey[key]; ok {
		if node.isRemoved() {
			return nil
		}
		return node.elem
	}
	return nil
}

// Has returns whether the element exists of the given key or not.
func (rht *ElementRHT) Has(key string) bool {
	if node, ok := rht.nodeMapByKey[key]; ok {
		return node != nil && !node.isRemoved()
	}
	return false
}

// Set sets the value of the given key. If there is an existing value, it is removed.
func (rht *ElementRHT) Set(k string, v Element) Element {
	return rht.SetWithExecutedAt(k, v, v.CreatedAt())
}

// SetWithExecutedAt behaves like Set, but uses the given executedAt as the
// LWW tie-break ticket instead of v's own createdAt. This is required when
// undo/redo restores an element under its original (older) createdAt: the
// comparison must use the operation's fresh execution ticket, not the
// restored element's creation ticket, or the restore can never win against
// whatever currently occupies the key. It mirrors the separate `executedAt`
// parameter of ElementRHT.set in the JS SDK (element_rht.ts:92-114).
//
// The win/lose decision and the eviction of the previous occupant must use
// the same anchor (positionedAt). Deciding eviction from the occupant's raw
// createdAt while deciding the winner from its positionedAt can disagree:
// a write that only wins by createdAt but loses by positionedAt would then
// tombstone the true (positionedAt) winner while still leaving it linked as
// the key's value, corrupting the slot (Get would find a node whose element
// is marked removed and incorrectly report the key as absent).
func (rht *ElementRHT) SetWithExecutedAt(k string, v Element, executedAt *time.Ticket) Element {
	node, ok := rht.nodeMapByKey[k]
	newNode := newElementRHTNode(k, v)
	rht.nodeMapByCreatedAt[v.CreatedAt().Key()] = newNode

	var removed Element
	if !ok || executedAt.After(positionedAt(node.elem)) {
		if ok && !node.isRemoved() && node.Remove(executedAt) {
			removed = node.elem
		}
		rht.nodeMapByKey[k] = newNode
		v.SetMovedAt(executedAt)
	} else if !node.isRemoved() {
		// The new node loses the LWW conflict — mark it as removed
		// so it doesn't appear as a duplicate during iteration.
		v.Remove(positionedAt(node.elem))
	}

	return removed
}

// positionedAt returns elem's last-moved ticket, or its creation ticket if
// it has never moved. It mirrors CRDTElement.getPositionedAt in the JS SDK
// (element.ts:68-74) and is the correct LWW tie-break anchor for a node
// already occupying an ElementRHT slot: once a node has been (re-)placed at
// a key with a ticket newer than its own createdAt -- as undo/redo does
// when restoring an element under its original, older createdAt -- further
// comparisons must use that newer ticket, or a later write with a ticket in
// between can incorrectly win on one replica and lose on another,
// diverging the two.
func positionedAt(elem Element) *time.Ticket {
	if movedAt := elem.MovedAt(); movedAt != nil {
		return movedAt
	}
	return elem.CreatedAt()
}

// Delete deletes the Element of the given key.
func (rht *ElementRHT) Delete(k string, deletedAt *time.Ticket) Element {
	node, ok := rht.nodeMapByKey[k]
	if !ok {
		return nil
	}

	if !node.Remove(deletedAt) {
		return nil
	}

	return node.elem
}

// DeleteByCreatedAt deletes the Element of the given creation time.
func (rht *ElementRHT) DeleteByCreatedAt(createdAt *time.Ticket, deletedAt *time.Ticket) (Element, error) {
	node, ok := rht.nodeMapByCreatedAt[createdAt.Key()]
	if !ok {
		return nil, fmt.Errorf("DeleteByCreatedAt %s: %w", createdAt.Key(), ErrChildNotFound)
	}

	if !node.Remove(deletedAt) {
		return nil, nil
	}

	return node.elem, nil
}

// SubPathOf returns the key of the node with the given creation time, and
// false if no such node is registered. It mirrors ElementRHT.subPathOf in
// the JS SDK (element_rht.ts) and is used to build the reverse Set for an
// undone Remove on an Object.
func (rht *ElementRHT) SubPathOf(createdAt *time.Ticket) (string, bool) {
	node, ok := rht.nodeMapByCreatedAt[createdAt.Key()]
	if !ok {
		return "", false
	}
	return node.Key(), true
}

// Elements returns a map of elements because the map easy to use for loop.
// TODO: If we encounter performance issues, we need to replace this with other solution.
func (rht *ElementRHT) Elements() map[string]Element {
	members := make(map[string]Element)
	for _, node := range rht.nodeMapByKey {
		if !node.isRemoved() {
			members[node.key] = node.elem
		}
	}

	return members
}

// Nodes returns a map of elements because the map easy to use for loop.
// TODO: If we encounter performance issues, we need to replace this with other solution.
func (rht *ElementRHT) Nodes() []*ElementRHTNode {
	var nodes []*ElementRHTNode
	for _, node := range rht.nodeMapByCreatedAt {
		nodes = append(nodes, node)
	}

	return nodes
}

// purge physically purge child element.
func (rht *ElementRHT) purge(elem Element) error {
	node, ok := rht.nodeMapByCreatedAt[elem.CreatedAt().Key()]
	if !ok {
		return fmt.Errorf("purge %s: %w", elem.CreatedAt().Key(), ErrChildNotFound)
	}
	delete(rht.nodeMapByCreatedAt, node.elem.CreatedAt().Key())

	nodeByKey, ok := rht.nodeMapByKey[node.key]
	if ok && node == nodeByKey {
		delete(rht.nodeMapByKey, nodeByKey.key)
	}

	return nil
}

// Marshal returns the JSON encoding of this map.
func (rht *ElementRHT) Marshal() string {
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
		sb.WriteString(fmt.Sprintf(`"%s":%s`, EscapeString(k), value.Marshal()))
	}
	sb.WriteString("}")

	return sb.String()
}

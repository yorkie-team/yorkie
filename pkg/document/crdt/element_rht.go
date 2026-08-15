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
// parameter of ElementRHT.set in the JS SDK (element_rht.ts:92-114) -- with
// one deliberate divergence, described next.
//
// The win/lose decision and the eviction of the previous occupant here
// always use the same anchor (PositionedAt). JS's eviction check instead
// gates on the occupant's raw createdAt (element_rht.ts:99, via
// CRDTElement.remove), independently of the positionedAt-based winner
// check a few lines below it (element_rht.ts:105,109). Those two checks
// can disagree there: when createdAt < executedAt < positionedAt, JS's
// eviction check (using createdAt) still fires and tombstones the true
// (positionedAt) winner, while its winner check (using positionedAt)
// decides the incoming value should *not* replace it -- so the winner
// ends up tombstoned but still linked as the key's value, and the
// incoming value is dropped without being registered as removed either.
// The key then spuriously reads as absent from get() on that replica.
// This is JS's actual, shipped behavior in that case, not a hypothetical.
//
// Go deliberately does not reproduce this: matching it would mean
// gating eviction on createdAt while gating the winner on PositionedAt,
// which is corrupt by construction, not merely different. Go anchors
// both checks on PositionedAt, so in the same case the true winner
// simply stays in place -- correct, but not what JS's shipped code does.
func (rht *ElementRHT) SetWithExecutedAt(k string, v Element, executedAt *time.Ticket) Element {
	node, ok := rht.nodeMapByKey[k]
	newNode := newElementRHTNode(k, v)
	rht.nodeMapByCreatedAt[v.CreatedAt().Key()] = newNode

	var removed Element
	if !ok || executedAt.After(PositionedAt(node.elem)) {
		if ok && !node.isRemoved() && node.Remove(executedAt) {
			removed = node.elem
		}
		rht.nodeMapByKey[k] = newNode
		v.SetMovedAt(executedAt)
	} else if !node.isRemoved() {
		// The new node loses the LWW conflict — mark it as removed
		// so it doesn't appear as a duplicate during iteration.
		v.Remove(PositionedAt(node.elem))
	}

	return removed
}

// PositionedAt returns elem's last-moved ticket, or its creation ticket if
// it has never moved. It mirrors CRDTElement.getPositionedAt in the JS SDK
// (element.ts:68-74) and is the correct LWW tie-break anchor for a node
// already occupying an ElementRHT slot: once a node has been (re-)placed at
// a key with a ticket newer than its own createdAt -- as undo/redo does
// when restoring an element under its original, older createdAt -- further
// comparisons must use that newer ticket, or a later write with a ticket in
// between can incorrectly win on one replica and lose on another,
// diverging the two. Exported for api/converter, which must replay an
// already-decoded element's own positionedAt rather than its createdAt when
// rebuilding an ElementRHT from a snapshot (converter.ts:1667).
func PositionedAt(elem Element) *time.Ticket {
	if movedAt := elem.MovedAt(); movedAt != nil {
		return movedAt
	}
	return elem.CreatedAt()
}

// DeepCopy copies itself deeply, preserving each node's identity, key
// mapping, and moved/removed timestamps exactly. Unlike Set, it does not
// replay the LWW race: doing so would use each copied node's own createdAt
// as the tie-break ticket, which can lose to a value it had already validly
// beaten (an element restored by undo/redo keeps its original createdAt but
// carries a newer movedAt), silently dropping a live member. It mirrors
// ElementRHT.deepcopy in the JS SDK (element_rht.ts:214-234), which copies
// both maps structurally for the same reason.
func (rht *ElementRHT) DeepCopy() (*ElementRHT, error) {
	clone := NewElementRHT()

	for _, node := range rht.nodeMapByCreatedAt {
		copied, err := node.elem.DeepCopy()
		if err != nil {
			return nil, err
		}
		clone.nodeMapByCreatedAt[copied.CreatedAt().Key()] = newElementRHTNode(node.key, copied)
	}

	for key, node := range rht.nodeMapByKey {
		clonedNode, ok := clone.nodeMapByCreatedAt[node.elem.CreatedAt().Key()]
		if !ok {
			return nil, fmt.Errorf("deep copy %s: %w", node.elem.CreatedAt().Key(), ErrChildNotFound)
		}
		clone.nodeMapByKey[key] = clonedNode
	}

	return clone, nil
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

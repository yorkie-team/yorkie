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
// parameter of ElementRHT.set in the JS SDK.
//
// Both the win/lose decision and the eviction of the previous occupant are
// anchored on the same ticket, PositionedAt. They must be: gating the
// eviction on the occupant's raw createdAt while gating the winner on its
// PositionedAt lets the two disagree whenever
// createdAt < executedAt < positionedAt. The eviction then fires and
// tombstones the true winner, the winner check declines to replace it, and
// the key is left pointing at a tombstone nothing ever removed -- it reads
// as absent from get() on that replica.
//
// The JS SDK shipped exactly that split through v0.7.21, which is how a
// randomized snapshot member order turned into a key that resolved
// differently on different page loads; the matching yorkie-js-sdk change
// anchors both on positionedAt as this does. Note that undo/redo is what makes the window reachable at
// all: before it, movedAt always equaled createdAt and the two anchors could
// not disagree. A separate reach of the same precondition -- a remote redo
// deleting a restored key on a peer via GC -- is filed in
// docs/tasks/active/20260816-remote-redo-replica-divergence-todo.md.
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
// rebuilding an ElementRHT from a snapshot (fromObject in converter.ts).
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
// ElementRHT.deepcopy in the JS SDK, which copies
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

// Nodes returns every node of this hashtable, live and tombstoned, in
// ascending PositionedAt order (tie-broken by createdAt).
//
// The order is protocol-visible: api/converter emits an object's members
// from here into a repeated field, and a peer rebuilds the object by
// replaying SetWithExecutedAt over that order. Ranging nodeMapByCreatedAt
// directly gives Go's randomized map order, so the same unchanged document
// encoded twice produced two different snapshots.
//
// Ascending PositionedAt is replay order: each node arrives with a ticket
// newer than the one occupying its key, so the LWW comparison always
// resolves forward. SetWithExecutedAt does not need that -- it is
// order-independent (TestSnapshotDecodeIsOrderIndependent) -- but a client
// that has not taken the matching yorkie-js-sdk fix does, and this costs
// nothing. That guarantee assumes no two nodes under one key share a
// PositionedAt, which per-operation tickets make unreachable; the createdAt
// tie-break restores determinism, not the forward-replay property.
//
// See docs/tasks/active/20260914-nondeterministic-snapshot-member-order-todo.md.
//
// TODO: If we encounter performance issues, we need to replace this with other solution.
func (rht *ElementRHT) Nodes() []*ElementRHTNode {
	nodes := make([]*ElementRHTNode, 0, len(rht.nodeMapByCreatedAt))
	for _, node := range rht.nodeMapByCreatedAt {
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool {
		if c := PositionedAt(nodes[i].elem).Compare(PositionedAt(nodes[j].elem)); c != 0 {
			return c < 0
		}
		return nodes[i].elem.CreatedAt().Compare(nodes[j].elem.CreatedAt()) < 0
	})

	return nodes
}

// purge physically purge child element.
func (rht *ElementRHT) purge(elem Element) error {
	node, ok := rht.nodeMapByCreatedAt[elem.CreatedAt().Key()]
	if !ok {
		return fmt.Errorf("purge %s: %w", elem.CreatedAt().Key(), ErrChildNotFound)
	}

	// The slot names a creation time, not an element. Undo of a Remove
	// re-inserts a copy under the original createdAt and SetWithExecutedAt
	// re-points this map at it, so unlinking whatever the key answers with
	// would delete a live member on a tombstone's behalf. The tombstone is
	// already off both maps by then, so there is nothing left to unlink.
	if node.elem != elem {
		return nil
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

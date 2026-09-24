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
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/yorkie-team/yorkie/pkg/document/resource"
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

// logicalValue strips the one layer of JSON encoding a client may have added
// to an attribute value before storing it.
//
// The JS SDK's `stringifyObjectValues` stores a plain string raw and keeps the
// quotes only on a string that itself parses as a JSON document, so that
// reading it back can tell the string `'1'` from the number `1`. It then sizes
// the value it started from, not the one it stored (`logicalValue` in
// `crdt/rht.ts`). Sizing the stored string here instead made the two disagree
// across the wire on exactly that subset -- `{b:'1'}` is stored as `"\"1\""`
// and was charged 8 bytes by the server against the SDK's 4 -- which is a
// divergence between two accountants describing one document, not a difference
// in what either one stores. The storage format is deliberate and does not
// move; only the charge follows the SDK.
//
// A value that does not begin with a quote cannot be a JSON string literal, so
// the common case costs one byte compare and no decode.
func logicalValue(val string) string {
	if len(val) < 2 || val[0] != '"' {
		return val
	}

	var decoded string
	if err := json.Unmarshal([]byte(val), &decoded); err != nil {
		return val
	}

	return decoded
}

// DataSize returns the size of this node. A removed node charges the same as
// the live one it replaced minus the value, because Remove mints a tombstone
// carrying no value at all -- see Remove.
func (n *RHTNode) DataSize() resource.DataSize {
	return resource.DataSize{
		Data: (len(n.key) + len(logicalValue(n.val))) * 2,
		Meta: time.TicketSize,
	}
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
// RHTWrite reports what a Set did, so the caller can keep docSize honest
// without inspecting the map afterwards. Reading the map cannot distinguish a
// write that installed a node from one that lost LWW and left the incumbent in
// place, and charging Live for the latter makes the running size depend on
// delivery order.
type RHTWrite struct {
	// Installed is the node this write put in the map, or nil when the write
	// lost LWW and changed nothing. Its size is what enters docSize.Live.
	Installed *RHTNode

	// Revived is a tombstone this write replaced. It was registered as garbage
	// when it was removed, so the caller re-registers the pair to cancel that
	// registration: it is no longer collectable, it is simply gone.
	Revived *RHTNode

	// Superseded is a LIVE node this write replaced. RHT overrides immutably,
	// so the old node is dropped with no tombstone and nothing to collect, but
	// its bytes were counted in docSize.Live and have to leave it.
	Superseded *RHTNode
}

// Set writes the value of the given key and reports what the write replaced.
// See RHTWrite for what the caller has to do with each field.
func (rht *RHT) Set(k, v string, executedAt *time.Ticket) RHTWrite {
	node := rht.nodeMapByKey[k]

	if node != nil && !executedAt.After(node.updatedAt) {
		return RHTWrite{}
	}

	if node != nil && node.isRemoved {
		rht.numberOfRemovedElement--
	}

	installed := newRHTNode(k, v, executedAt, false)
	rht.nodeMapByKey[k] = installed

	write := RHTWrite{Installed: installed}
	if node == nil {
		return write
	}
	if node.isRemoved {
		write.Revived = node
	} else {
		write.Superseded = node
	}

	return write
}

// SetInternal sets the value of the given key internally.
func (rht *RHT) SetInternal(k string, v string, updatedAt *time.Ticket, removed bool) {
	newNode := newRHTNode(k, v, updatedAt, removed)
	rht.nodeMapByKey[k] = newNode

	if removed {
		rht.numberOfRemovedElement++
	}
}

// RHTRemoval reports what a Remove did, for the same reason RHTWrite reports
// what a Set did: the caller cannot recover it by reading the map afterwards.
type RHTRemoval struct {
	// GCNodes are the tombstones this removal produced, in registration
	// order. Empty when the removal lost LWW and changed nothing.
	GCNodes []*RHTNode

	// ValueDropped is what the value the tombstone does NOT carry was
	// charging. It is non-zero only when the removal replaced a LIVE value,
	// and it leaves whichever ledger was holding those bytes: docSize.Live
	// while the node owning the attribute is live, docSize.GC once that node
	// is itself a tombstone whose charge covers its live attributes.
	ValueDropped resource.DataSize
}

// Remove removes the value of the given key and reports what the removal
// dropped. See RHTRemoval for what the caller has to do with each field.
//
// The tombstone carries no value. Copying the value it replaced made a
// tombstone's stored bytes a function of what had landed at that key when the
// removal arrived, so a concurrent same-key set and remove charged differently
// depending on delivery order while rendering identically. The carried value
// was never reachable anyway -- Get, Has, Elements and Marshal all filter on
// isRemoved, and a Set that revives a tombstone overwrites the value wholesale
// -- so only a size accountant could observe it, and what it observed was
// order-dependent.
func (rht *RHT) Remove(k string, executedAt *time.Ticket) RHTRemoval {
	// NOTE(hackerwins): We need to consider the logic and the policy of removing the element.
	// A. RHT always overrides the value of the same key in a immutable way.
	// B. Even if the key is not existed, RHT sets the flag `isRemoved` for concurrency.
	node, ok := rht.nodeMapByKey[k]
	if !ok {
		rht.numberOfRemovedElement++
		newNode := newRHTNode(k, "", executedAt, true)
		rht.nodeMapByKey[k] = newNode
		return RHTRemoval{GCNodes: []*RHTNode{newNode}}
	}

	if !executedAt.After(node.updatedAt) {
		return RHTRemoval{}
	}

	alreadyRemoved := node.isRemoved
	if !alreadyRemoved {
		rht.numberOfRemovedElement++
	}

	var gcNodes []*RHTNode
	if alreadyRemoved {
		gcNodes = append(gcNodes, node)
	}

	newNode := newRHTNode(k, "", executedAt, true)
	rht.nodeMapByKey[k] = newNode
	gcNodes = append(gcNodes, newNode)

	removal := RHTRemoval{GCNodes: gcNodes}
	if !alreadyRemoved {
		removal.ValueDropped = resource.DataSize{Data: len(logicalValue(node.val)) * 2}
	}

	return removal
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

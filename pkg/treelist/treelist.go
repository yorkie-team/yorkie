/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
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

// Package treelist provides a Tree List implementation using Left-leaning Red-Black Tree.
// It is an order-statistic tree that supports index-based operations for RGATreeList.
// Unlike splay trees, it guarantees O(log N) worst-case for all operations.
package treelist

import (
	"fmt"
	"strings"
)

// ErrOutOfIndex is returned when the given index is out of index.
var ErrOutOfIndex = fmt.Errorf("out of index")

// Value represents the data stored in the nodes of Tree.
type Value interface {
	IsRemoved() bool
	String() string
}

// Node is a node of Tree.
type Node[V Value] struct {
	value  V
	left   *Node[V]
	right  *Node[V]
	parent *Node[V]

	// weight is the number of non-removed (live) nodes in this subtree.
	// Used for logical index-based operations (Find, indexOf).
	weight int

	// count is the total number of nodes in this subtree (including tombstones).
	// Used for structural positioning (InsertAfter, Delete).
	count int

	isRed bool
}

// NewNode creates a new instance of Node.
func NewNode[V Value](value V) *Node[V] {
	n := &Node[V]{
		value: value,
		isRed: true,
	}
	n.weight = n.Size()
	n.count = 1
	return n
}

// Value returns the value of this Node.
func (n *Node[V]) Value() V {
	return n.value
}

// Size returns 1 if the node is live, 0 if removed (tombstone).
func (n *Node[V]) Size() int {
	if n.value.IsRemoved() {
		return 0
	}
	return 1
}

// Tree is an order-statistic tree based on Left-leaning Red-Black Tree.
// It maintains two weights per node:
//   - weight: count of non-removed nodes (for index-based lookup)
//   - count: total nodes including tombstones (for structural operations)
type Tree[V Value] struct {
	root *Node[V]
}

// NewTree creates a new instance of Tree.
func NewTree[V Value](root *Node[V]) *Tree[V] {
	if root != nil {
		root.isRed = false
	}
	return &Tree[V]{root: root}
}

// InsertAfter inserts the target node right after prev in the in-order traversal.
// It uses structural (count-based) indexing to correctly handle tombstone nodes.
func (t *Tree[V]) InsertAfter(prev *Node[V], target *Node[V]) {
	if prev == nil || target == nil {
		return
	}

	target.left = nil
	target.right = nil
	target.parent = nil
	target.isRed = true
	target.weight = target.Size()
	target.count = 1

	idx := t.structuralIndexOf(prev)
	t.root = t.insertByCount(t.root, idx+1, target)
	t.root.isRed = false
	t.root.parent = nil
}

func (t *Tree[V]) insertByCount(node *Node[V], index int, newNode *Node[V]) *Node[V] {
	if node == nil {
		return newNode
	}

	if index <= node.leftCount() {
		node.left = t.insertByCount(node.left, index, newNode)
		node.left.parent = node
	} else {
		node.right = t.insertByCount(node.right, index-node.leftCount()-1, newNode)
		node.right.parent = node
	}

	return fixUp(node)
}

// Len returns the number of non-removed (live) nodes.
func (t *Tree[V]) Len() int {
	if t.root == nil {
		return 0
	}
	return t.root.weight
}

// Find returns the node at the given logical index (among non-removed nodes).
func (t *Tree[V]) Find(index int) (*Node[V], error) {
	if t.root == nil || index < 0 || index >= t.Len() {
		return nil, fmt.Errorf("tree size %d, index %d: %w", t.Len(), index, ErrOutOfIndex)
	}

	node := t.root
	for {
		if index < node.leftWeight() {
			node = node.left
		} else if index < node.leftWeight()+node.Size() {
			break
		} else {
			index -= node.leftWeight() + node.Size()
			node = node.right
		}
	}
	return node, nil
}

// Delete physically removes a node from the tree.
// Unlike tombstoning, this completely removes the node from the tree structure.
// It uses structural (count-based) indexing and swaps the node structure
// (not values) with its successor to preserve node identity.
func (t *Tree[V]) Delete(node *Node[V]) {
	if node == nil || t.root == nil {
		return
	}

	if !isRed(t.root.left) && !isRed(t.root.right) {
		t.root.isRed = true
	}

	idx := t.structuralIndexOf(node)
	t.root = t.deleteByCount(t.root, idx)

	if t.root != nil {
		t.root.isRed = false
		t.root.parent = nil
	}
}

func (t *Tree[V]) deleteByCount(node *Node[V], index int) *Node[V] {
	if index < node.leftCount() {
		if !isRed(node.left) && !isRed(node.left.left) {
			node = moveRedLeft(node)
		}
		node.left = t.deleteByCount(node.left, index)
		if node.left != nil {
			node.left.parent = node
		}
	} else {
		if isRed(node.left) {
			node = rotateRight(node)
		}

		if index == node.leftCount() && node.right == nil {
			return nil
		}

		if !isRed(node.right) && !isRed(node.right.left) {
			node = moveRedRight(node)
		}

		if index == node.leftCount() {
			// Swap the successor into this position instead of copying values.
			// This preserves node identity so external references remain valid.
			successor := min(node.right)
			newRight := removeMin(node.right)

			successor.left = node.left
			successor.right = newRight
			successor.isRed = node.isRed
			if successor.left != nil {
				successor.left.parent = successor
			}
			if successor.right != nil {
				successor.right.parent = successor
			}

			node.left = nil
			node.right = nil
			node.parent = nil

			node = successor
		} else {
			node.right = t.deleteByCount(node.right, index-node.leftCount()-1)
			if node.right != nil {
				node.right.parent = node
			}
		}
	}

	return fixUp(node)
}

// UpdateWeight propagates weight changes from the given node up to the root.
// Call this after a node's IsRemoved() status changes (i.e., after tombstoning).
func (t *Tree[V]) UpdateWeight(node *Node[V]) {
	for cur := node; cur != nil; cur = cur.parent {
		cur.weight = cur.leftWeight() + cur.Size() + cur.rightWeight()
	}
}

// ToTestString returns a String containing the metadata of the node
// for debugging purpose.
func (t *Tree[V]) ToTestString() string {
	var b strings.Builder
	traverseInOrder(t.root, func(node *Node[V]) {
		b.WriteString(fmt.Sprintf("[%d,%d]%s", node.weight, node.Size(), node.value.String()))
	})
	return b.String()
}

// structuralIndexOf returns the structural position of the node,
// counting all nodes including tombstones.
func (t *Tree[V]) structuralIndexOf(node *Node[V]) int {
	index := node.leftCount()
	for cur := node; cur.parent != nil; cur = cur.parent {
		if cur == cur.parent.right {
			index += cur.parent.leftCount() + 1
		}
	}
	return index
}

func (n *Node[V]) leftWeight() int {
	if n.left != nil {
		return n.left.weight
	}
	return 0
}

func (n *Node[V]) rightWeight() int {
	if n.right != nil {
		return n.right.weight
	}
	return 0
}

func (n *Node[V]) leftCount() int {
	if n.left != nil {
		return n.left.count
	}
	return 0
}

func (n *Node[V]) rightCount() int {
	if n.right != nil {
		return n.right.count
	}
	return 0
}

func isRed[V Value](node *Node[V]) bool {
	return node != nil && node.isRed
}

func updateNode[V Value](node *Node[V]) {
	node.weight = node.leftWeight() + node.Size() + node.rightWeight()
	node.count = node.leftCount() + 1 + node.rightCount()
}

func rotateLeft[V Value](node *Node[V]) *Node[V] {
	right := node.right

	node.right = right.left
	if node.right != nil {
		node.right.parent = node
	}

	right.left = node
	right.parent = node.parent
	node.parent = right

	right.isRed = node.isRed
	node.isRed = true

	updateNode(node)
	updateNode(right)
	return right
}

func rotateRight[V Value](node *Node[V]) *Node[V] {
	left := node.left

	node.left = left.right
	if node.left != nil {
		node.left.parent = node
	}

	left.right = node
	left.parent = node.parent
	node.parent = left

	left.isRed = node.isRed
	node.isRed = true

	updateNode(node)
	updateNode(left)
	return left
}

func flipColors[V Value](node *Node[V]) {
	node.isRed = !node.isRed
	node.left.isRed = !node.left.isRed
	node.right.isRed = !node.right.isRed
}

func moveRedLeft[V Value](node *Node[V]) *Node[V] {
	flipColors(node)
	if isRed(node.right.left) {
		node.right = rotateRight(node.right)
		node.right.parent = node
		node = rotateLeft(node)
		flipColors(node)
	}
	return node
}

func moveRedRight[V Value](node *Node[V]) *Node[V] {
	flipColors(node)
	if isRed(node.left.left) {
		node = rotateRight(node)
		flipColors(node)
	}
	return node
}

func removeMin[V Value](node *Node[V]) *Node[V] {
	if node.left == nil {
		return nil
	}

	if !isRed(node.left) && !isRed(node.left.left) {
		node = moveRedLeft(node)
	}

	node.left = removeMin(node.left)
	if node.left != nil {
		node.left.parent = node
	}

	return fixUp(node)
}

func min[V Value](node *Node[V]) *Node[V] {
	for node.left != nil {
		node = node.left
	}
	return node
}

func fixUp[V Value](node *Node[V]) *Node[V] {
	if isRed(node.right) && !isRed(node.left) {
		node = rotateLeft(node)
	}
	if isRed(node.left) && isRed(node.left.left) {
		node = rotateRight(node)
	}
	if isRed(node.left) && isRed(node.right) {
		flipColors(node)
	}
	updateNode(node)
	return node
}

func traverseInOrder[V Value](node *Node[V], cb func(*Node[V])) {
	if node == nil {
		return
	}
	traverseInOrder(node.left, cb)
	cb(node)
	traverseInOrder(node.right, cb)
}

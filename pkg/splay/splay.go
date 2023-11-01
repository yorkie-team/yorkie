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

// Package splay provides splay tree implementation. It is used to implement
// List by using splay tree.
package splay

import (
	"fmt"
	"strings"
)

// ErrOutOfIndex is returned when the given index is out of index.
var ErrOutOfIndex = fmt.Errorf("out of index")

// Value represents the data stored in the nodes of Tree.
type Value interface {
	Len() int
	String() string
}

// Node is a node of Tree.
type Node[V Value] struct {
	value  V
	weight int

	left   *Node[V]
	right  *Node[V]
	parent *Node[V]
}

// NewNode creates a new instance of Node.
func NewNode[V Value](value V) *Node[V] {
	n := &Node[V]{
		value: value,
	}
	n.InitWeight()
	return n
}

// Value returns the value of this Node.
func (t *Node[V]) Value() V {
	return t.value
}

func (t *Node[V]) leftWeight() int {
	if t.left == nil {
		return 0
	}
	return t.left.weight
}

func (t *Node[V]) rightWeight() int {
	if t.right == nil {
		return 0
	}
	return t.right.weight
}

// InitWeight sets initial weight of this node.
func (t *Node[V]) InitWeight() {
	t.weight = t.value.Len()
}

func (t *Node[V]) increaseWeight(weight int) {
	t.weight += weight
}

func (t *Node[V]) unlink() {
	t.parent = nil
	t.right = nil
	t.left = nil
}

func (t *Node[V]) hasLinks() bool {
	return t.parent != nil || t.left != nil || t.right != nil
}

// Tree is weighted binary search tree which is based on Splay tree.
// original paper on Splay Trees: https://www.cs.cmu.edu/~sleator/papers/self-adjusting.pdf
type Tree[V Value] struct {
	root *Node[V]
}

// NewTree creates a new instance of Tree.
func NewTree[V Value](root *Node[V]) *Tree[V] {
	return &Tree[V]{
		root: root,
	}
}

// Insert inserts the node at the last.
func (t *Tree[V]) Insert(node *Node[V]) *Node[V] {
	if t.root == nil {
		t.root = node
		return node
	}

	return t.InsertAfter(t.root, node)
}

// InsertAfter inserts the node after the given previous node.
func (t *Tree[V]) InsertAfter(prev *Node[V], node *Node[V]) *Node[V] {
	t.Splay(prev)
	t.root = node
	node.right = prev.right
	if prev.right != nil {
		prev.right.parent = node
	}
	node.left = prev
	prev.parent = node
	prev.right = nil

	t.UpdateWeight(prev)
	t.UpdateWeight(node)

	return node
}

// Splay moves the given node to the root.
func (t *Tree[V]) Splay(node *Node[V]) {
	if node == nil {
		return
	}

	for {
		if isLeftChild(node.parent) && isRightChild(node) {
			// zig-zag
			t.rotateLeft(node)
			t.rotateRight(node)
		} else if isRightChild(node.parent) && isLeftChild(node) {
			// zig-zag
			t.rotateRight(node)
			t.rotateLeft(node)
		} else if isLeftChild(node.parent) && isLeftChild(node) {
			// zig-zig
			t.rotateRight(node.parent)
			t.rotateRight(node)
		} else if isRightChild(node.parent) && isRightChild(node) {
			// zig-zig
			t.rotateLeft(node.parent)
			t.rotateLeft(node)
		} else {
			// zig
			if isLeftChild(node) {
				t.rotateRight(node)
			} else if isRightChild(node) {
				t.rotateLeft(node)
			}
			t.updateTreeWeight(node)
			return
		}
	}
}

// IndexOf Find the index of the given node.
func (t *Tree[V]) IndexOf(node *Node[V]) int {
	if node == nil || node != t.root && !node.hasLinks() {
		return -1
	}

	index := 0
	current := node
	var prev *Node[V]
	for current != nil {
		if prev == nil || prev == current.right {
			index += current.value.Len() + current.leftWeight()
		}
		prev = current
		current = current.parent
	}
	return index - node.value.Len()
}

// Find returns the Node and offset of the given index.
func (t *Tree[V]) Find(index int) (*Node[V], int, error) {
	if t.root == nil {
		return nil, 0, nil
	}

	node := t.root
	offset := index
	for {
		if node.left != nil && offset <= node.leftWeight() {
			node = node.left
		} else if node.right != nil && node.leftWeight()+node.value.Len() < offset {
			offset -= node.leftWeight() + node.value.Len()
			node = node.right
		} else {
			offset -= node.leftWeight()
			break
		}
	}

	if offset > node.value.Len() {
		return nil, 0, fmt.Errorf("node length %d, index %d: %w", node.value.Len(), offset, ErrOutOfIndex)
	}

	return node, offset, nil
}

// String returns a string containing node values.
func (t *Tree[V]) String() string {
	var builder strings.Builder
	traverseInOrder(t.root, func(node *Node[V]) {
		builder.WriteString(node.value.String())
	})
	return builder.String()
}

// ToTestString returns a string containing the metadata of the Node
// for debugging purpose.
func (t *Tree[V]) ToTestString() string {
	var builder strings.Builder

	traverseInOrder(t.root, func(node *Node[V]) {
		builder.WriteString(fmt.Sprintf(
			"[%d,%d]%s",
			node.weight,
			node.value.Len(),
			node.value.String(),
		))
	})
	return builder.String()
}

// CheckWeight returns false when there is an incorrect weight node.
// for debugging purpose.
func (t *Tree[V]) CheckWeight() bool {
	var nodes []*Node[V]
	traversePostorder(t.root, func(node *Node[V]) {
		nodes = append(nodes, node)
	})
	for _, node := range nodes {
		if node.weight != node.Value().Len()+node.leftWeight()+node.rightWeight() {
			return false
		}
	}
	return true
}

// UpdateWeight recalculates the weight of this node with the value and children.
func (t *Tree[V]) UpdateWeight(node *Node[V]) {
	node.InitWeight()

	if node.left != nil {
		node.increaseWeight(node.leftWeight())
	}

	if node.right != nil {
		node.increaseWeight(node.rightWeight())
	}
}

func (t *Tree[V]) updateTreeWeight(node *Node[V]) {
	for node != nil {
		t.UpdateWeight(node)
		node = node.parent
	}
}

// Delete deletes the given node from this Tree.
func (t *Tree[V]) Delete(node *Node[V]) {
	t.Splay(node)

	leftTree := NewTree(node.left)
	if leftTree.root != nil {
		leftTree.root.parent = nil
	}

	rightTree := NewTree(node.right)
	if rightTree.root != nil {
		rightTree.root.parent = nil
	}

	if leftTree.root != nil {
		rightmost := leftTree.rightmost()
		leftTree.Splay(rightmost)
		leftTree.root.right = rightTree.root
		if rightTree.root != nil {
			rightTree.root.parent = leftTree.root
		}
		t.root = leftTree.root
	} else {
		t.root = rightTree.root
	}

	node.unlink()
	if t.root != nil {
		t.UpdateWeight(t.root)
	}
}

// DeleteRange separates the range between given 2 boundaries from this Tree.
// This function separates the range to delete as a subtree
// by splaying outer boundary nodes.
// leftBoundary must exist because of 0-indexed initial dummy node of tree,
// but rightBoundary can be nil means range to delete includes the end of tree.
// Refer to the design document: ./design/range-deletion-in-slay-tree.md
func (t *Tree[V]) DeleteRange(leftBoundary, rightBoundary *Node[V]) {
	if rightBoundary == nil {
		t.Splay(leftBoundary)
		t.cutOffRight(leftBoundary)
		return
	}
	t.Splay(leftBoundary)
	t.Splay(rightBoundary)
	if rightBoundary.left != leftBoundary {
		t.rotateRight(leftBoundary)
	}
	t.cutOffRight(leftBoundary)
}

func (t *Tree[V]) cutOffRight(root *Node[V]) {
	traversePostorder(root.right, func(node *Node[V]) { node.InitWeight() })
	t.updateTreeWeight(root)
}

func (t *Tree[V]) rotateLeft(pivot *Node[V]) {
	root := pivot.parent
	if root.parent != nil {
		if root == root.parent.left {
			root.parent.left = pivot
		} else {
			root.parent.right = pivot
		}
	} else {
		t.root = pivot
	}

	pivot.parent = root.parent

	root.right = pivot.left
	if root.right != nil {
		root.right.parent = root
	}

	pivot.left = root
	pivot.left.parent = pivot

	t.UpdateWeight(root)
	t.UpdateWeight(pivot)
}

func (t *Tree[V]) rotateRight(pivot *Node[V]) {
	root := pivot.parent
	if root.parent != nil {
		if root == root.parent.left {
			root.parent.left = pivot
		} else {
			root.parent.right = pivot
		}
	} else {
		t.root = pivot
	}
	pivot.parent = root.parent

	root.left = pivot.right
	if root.left != nil {
		root.left.parent = root
	}

	pivot.right = root
	pivot.right.parent = pivot

	t.UpdateWeight(root)
	t.UpdateWeight(pivot)
}

func (t *Tree[V]) rightmost() *Node[V] {
	node := t.root
	for node.right != nil {
		node = node.right
	}
	return node
}

// Len returns the size of this Tree.
func (t *Tree[V]) Len() int {
	if t.root == nil {
		return 0
	}

	return t.root.weight
}

func traverseInOrder[V Value](node *Node[V], callback func(node *Node[V])) {
	if node == nil {
		return
	}

	traverseInOrder(node.left, callback)
	callback(node)
	traverseInOrder(node.right, callback)
}

func traversePostorder[V Value](node *Node[V], callback func(node *Node[V])) {
	if node == nil {
		return
	}

	traversePostorder(node.left, callback)
	traversePostorder(node.right, callback)
	callback(node)
}

func isLeftChild[V Value](node *Node[V]) bool {
	return node != nil && node.parent != nil && node.parent.left == node
}

func isRightChild[V Value](node *Node[V]) bool {
	return node != nil && node.parent != nil && node.parent.right == node
}

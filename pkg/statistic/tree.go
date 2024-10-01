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

// Package statistic provides order statistic tree
// with Left-leaning Red-Black tree
package statistic

import (
	"fmt"
	"strings"
)

// ErrOutOfIndex is returned when the given index is out of index.
var ErrOutOfIndex = fmt.Errorf("out of index")

// Tree is an implementation of Order statistic tree with Left-learning Red-Black Tree.
// Original paper on Left-leaning Red-Black Trees:
// https://sedgewick.io/wp-content/themes/sedgewick/papers/2008LLRB.pdf
//
// Invariant 1: No red node has a red child
// Invariant 2: Every leaf path has the same number of black nodes
// Invariant 3: Only the left child can be red (left leaning)
type Tree[V Value] struct {
	root       *Node[V]
	keyCounter uint32
}

// NewTree creates a new instance of Tree.
func NewTree[V Value]() *Tree[V] {
	return &Tree[V]{}
}

// Find returns the Node and offset of the given index.
func (tree *Tree[V]) Find(index int) (node *Node[V], offset int, err error) {
	if tree.root == nil {
		return nil, 0, nil
	}

	node = tree.root
	offset = index
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

// Delete deletes the given node from this Tree.
func (t *Tree[V]) Delete(node *Node[V]) {
	if !isRed(t.root.left) && !isRed(t.root.right) {
		t.root.isRed = true
	}

	t.root = t.delete(t.root, node.key)
	updateWeight(t.root)
	if t.root != nil {
		t.root.isRed = false
	}
}

// Len returns the length of the tree.
func (t *Tree[V]) Len() int {
	return t.root.weight
}

func (tree *Tree[V]) InsertAfter(prev *Node[V], node *Node[V]) *Node[V] {
	node.parent = prev.parent

	if prev.parent != nil {
		parent := prev.parent
		if parent.left == prev {
			prev.left = node
		} else {
			prev.right = node
		}
	}

	node.right = prev.right
	if prev.right != nil {
		prev.right.parent = node
	}

	node.left = prev
	prev.parent = node
	prev.right = nil

	if isRed(node.right) && !isRed(node.left) {
		node = rotateLeft(node)
		updateTreeWeight(node)
	}

	if isRed(node.left) && isRed(node.left.left) {
		node = rotateRight(node)
		updateTreeWeight(node)
	}

	if isRed(node.left) && isRed(node.right) {
		flipColors(node)
	}

	checkColor(node)
	checkColor(node.parent)
	return node
}

func (t *Tree[V]) delete(node *Node[V], key Key) *Node[V] {
	if key.compare(node.key) < 0 {
		if !isRed(node.left) && !isRed(node.left.left) {
			node = moveRedLeft(node)
		}
		node.left = t.delete(node.left, key)
		updateTreeWeight(node.left)
	} else {
		if isRed(node.left) {
			node = rotateRight(node)
		}

		if key.compare(node.key) == 0 && node.right == nil {
			return nil
		}

		if !isRed(node.right) && !isRed(node.right.left) {
			node = moveRedRight(node)
		}

		if key.compare(node.key) == 0 {
			smallest := min(node.right)
			node.value = smallest.value
			node.key = smallest.key
			node.right = removeMin(node.right)
		} else {
			node.right = t.delete(node.right, key)
			updateTreeWeight(node.right)
		}
	}

	return fixUp(node)
}

func rotateLeft[V Value](node *Node[V]) *Node[V] {
	updateNode := node.right.left
	right := node.right
	node.right = right.left
	right.left = node
	right.isRed = right.left.isRed
	right.left.isRed = true
	updateTreeWeight(updateNode)
	return right
}

func rotateRight[V Value](node *Node[V]) *Node[V] {
	updateNode := node.left.right
	left := node.left
	node.left = left.right
	left.right = node
	left.isRed = left.right.isRed
	left.right.isRed = true
	updateTreeWeight(updateNode)
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
		updateNode1 := node
		updateNode2 := node.right

		node.right = rotateRight(node.right)
		node = rotateLeft(node)
		flipColors(node)

		updateTreeWeight(updateNode1)
		updateTreeWeight(updateNode2)
	}

	return node
}

func moveRedRight[V Value](node *Node[V]) *Node[V] {
	flipColors(node)
	if isRed(node.left.left) {
		updateNode1 := node
		updateNode2 := node.left

		node = rotateRight(node)
		flipColors(node)

		updateWeight(updateNode1)
		updateWeight(updateNode2)
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
	return fixUp(node)
}

func min[V Value](node *Node[V]) *Node[V] {
	if node.left == nil {
		return node
	}

	return min(node.left)
}

func checkColor[V Value](node *Node[V]) {
	if isRed(node.right) && !isRed(node.left) {
		node = rotateLeft(node)
		updateTreeWeight(node)
	}

	if isRed(node.left) && isRed(node.left.left) {
		node = rotateRight(node)
		updateTreeWeight(node)
	}

	if isRed(node.left) && isRed(node.right) {
		flipColors(node)
	}
}

func fixUp[V Value](node *Node[V]) *Node[V] {
	if isRed(node.right) {
		node = rotateLeft(node)
	}

	if isRed(node.left) && isRed(node.left.left) {
		node = rotateRight(node)
	}

	if isRed(node.left) && isRed(node.right) {
		flipColors(node)
	}

	updateTreeWeight(node)
	return node
}

func isRed[V Value](node *Node[V]) bool {
	return node != nil && node.isRed
}

func traverseInOrder[V Value](node *Node[V], callback func(node *Node[V])) {
	if node == nil {
		return
	}

	traverseInOrder(node.left, callback)
	callback(node)
	traverseInOrder(node.right, callback)
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

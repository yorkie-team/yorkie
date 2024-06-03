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

// Package llrb provides a Left-leaning Red-Black tree implementation.
package llrb

import (
	"strings"
)

// Key represents key of Tree.
type Key interface {
	// Compare gives the result of a 3-way comparison
	// a.Compare(b) = 1 => a > b
	// a.Compare(b) = 0 => a == b
	// a.Compare(b) = -1 => a < b
	Compare(k Key) int
}

// Value represents the data stored in the nodes of Tree.
type Value interface {
	String() string
}

// Node is a node of Tree.
type Node[K Key, V Value] struct {
	key    K
	value  V
	parent *Node[K, V]
	left   *Node[K, V]
	right  *Node[K, V]
	isRed  bool
}

// NewNode creates a new instance of Node.
func NewNode[K Key, V Value](key K, value V, isRed bool) *Node[K, V] {
	return &Node[K, V]{
		key:   key,
		value: value,
		isRed: isRed,
	}
}

// Tree is an implementation of Left-learning Red-Black Tree.
// Original paper on Left-leaning Red-Black Trees:
// http://www.cs.princeton.edu/~rs/talks/LLRB/LLRB.pdf
//
// Invariant 1: No red node has a red child
// Invariant 2: Every leaf path has the same number of black nodes
// Invariant 3: Only the left child can be red (left leaning)
type Tree[K Key, V Value] struct {
	root *Node[K, V]
	size int
}

// NewTree creates a new instance of Tree.
func NewTree[K Key, V Value]() *Tree[K, V] {
	return &Tree[K, V]{}
}

// Put puts the value of the given key.
func (t *Tree[K, V]) Put(k K, v V) V {
	t.root = t.put(t.root, k, v)
	t.root.isRed = false
	return v
}

func (t *Tree[K, V]) String() string {
	var str []string
	traverseInOrder(t.root, func(node *Node[K, V]) {
		str = append(str, node.value.String())
	})
	return strings.Join(str, ",")
}

// Remove removes the value of the given key.
func (t *Tree[K, V]) Remove(key K) {
	if !isRed(t.root.left) && !isRed(t.root.right) {
		t.root.isRed = true
	}

	t.root = t.remove(t.root, key)
	if t.root != nil {
		t.root.isRed = false
	}
}

// Floor returns the greatest key less than or equal to the given key.
func (t *Tree[K, V]) Floor(key K) (K, V) {
	node := t.root

	for node != nil {
		compare := key.Compare(node.key)
		if compare > 0 {
			if node.right != nil {
				node.right.parent = node
				node = node.right
			} else {
				return node.key, node.value
			}
		} else if compare < 0 {
			if node.left != nil {
				node.left.parent = node
				node = node.left
			} else {
				parent := node.parent
				child := node
				for parent != nil && child == parent.left {
					child = parent
					parent = parent.parent
				}

				// TODO(hackerwins): check below warning
				return parent.key, parent.value
			}
		} else {
			return node.key, node.value
		}
	}

	var zeroK K
	var zeroV V
	return zeroK, zeroV
}

// Len returns the length of the tree.
func (t *Tree[K, V]) Len() int {
	return t.size
}

func (t *Tree[K, V]) put(node *Node[K, V], key K, value V) *Node[K, V] {
	if node == nil {
		t.size++
		return NewNode(key, value, true)
	}

	compare := key.Compare(node.key)
	if compare < 0 {
		node.left = t.put(node.left, key, value)
	} else if compare > 0 {
		node.right = t.put(node.right, key, value)
	} else {
		node.value = value
	}

	if isRed(node.right) && !isRed(node.left) {
		node = rotateLeft(node)
	}

	if isRed(node.left) && isRed(node.left.left) {
		node = rotateRight(node)
	}

	if isRed(node.left) && isRed(node.right) {
		flipColors(node)
	}

	return node
}

func (t *Tree[K, V]) remove(node *Node[K, V], key K) *Node[K, V] {
	if key.Compare(node.key) < 0 {
		if !isRed(node.left) && !isRed(node.left.left) {
			node = moveRedLeft(node)
		}
		node.left = t.remove(node.left, key)
	} else {
		if isRed(node.left) {
			node = rotateRight(node)
		}

		if key.Compare(node.key) == 0 && node.right == nil {
			t.size--
			return nil
		}

		if !isRed(node.right) && !isRed(node.right.left) {
			node = moveRedRight(node)
		}

		if key.Compare(node.key) == 0 {
			t.size--
			smallest := min(node.right)
			node.value = smallest.value
			node.key = smallest.key
			node.right = removeMin(node.right)
		} else {
			node.right = t.remove(node.right, key)
		}
	}

	return fixUp(node)
}

func rotateLeft[K Key, V Value](node *Node[K, V]) *Node[K, V] {
	right := node.right
	node.right = right.left
	right.left = node
	right.isRed = right.left.isRed
	right.left.isRed = true
	return right
}

func rotateRight[K Key, V Value](node *Node[K, V]) *Node[K, V] {
	left := node.left
	node.left = left.right
	left.right = node
	left.isRed = left.right.isRed
	left.right.isRed = true
	return left
}

func flipColors[K Key, V Value](node *Node[K, V]) {
	node.isRed = !node.isRed
	node.left.isRed = !node.left.isRed
	node.right.isRed = !node.right.isRed
}

func moveRedLeft[K Key, V Value](node *Node[K, V]) *Node[K, V] {
	flipColors(node)
	if isRed(node.right.left) {
		node.right = rotateRight(node.right)
		node = rotateLeft(node)
		flipColors(node)
	}
	return node
}

func moveRedRight[K Key, V Value](node *Node[K, V]) *Node[K, V] {
	flipColors(node)
	if isRed(node.left.left) {
		node = rotateRight(node)
		flipColors(node)
	}
	return node
}

func removeMin[K Key, V Value](node *Node[K, V]) *Node[K, V] {
	if node.left == nil {
		return nil
	}

	if !isRed(node.left) && !isRed(node.left.left) {
		node = moveRedLeft(node)
	}

	node.left = removeMin(node.left)
	return fixUp(node)

}

func min[K Key, V Value](node *Node[K, V]) *Node[K, V] {
	if node.left == nil {
		return node
	}

	return min(node.left)
}

func fixUp[K Key, V Value](node *Node[K, V]) *Node[K, V] {
	if isRed(node.right) {
		node = rotateLeft(node)
	}

	if isRed(node.left) && isRed(node.left.left) {
		node = rotateRight(node)
	}

	if isRed(node.left) && isRed(node.right) {
		flipColors(node)
	}

	return node
}

func isRed[K Key, V Value](node *Node[K, V]) bool {
	return node != nil && node.isRed
}

func traverseInOrder[K Key, V Value](node *Node[K, V], callback func(node *Node[K, V])) {
	if node == nil {
		return
	}

	traverseInOrder(node.left, callback)
	callback(node)
	traverseInOrder(node.right, callback)
}

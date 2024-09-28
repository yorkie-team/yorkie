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

// Package llrb2 provides a Left-leaning Red-Black tree implementation.
package llrb2

// Key represents key of Tree.
type Key interface {
	// Compare gives the result of a 3-way comparison
	// a.Compare(b) = 1 => a > b
	// a.Compare(b) = 0 => a == b
	// a.Compare(b) = -1 => a < b
	Compare(k Key) int
}

// Value represents the data stored in the nodes of Tree.
// TODO(binary-ho) Len이 필요할까? <- isRemoved가 필요할까? <- Soft Delete가 필요할까?
type Value interface {
	Len() int
	String() string
}

// Node is a node of Tree.
type Node[K Key, V Value] struct {
	key    K
	value  V
	weight int
	isRed  bool

	parent *Node[K, V]
	left   *Node[K, V]
	right  *Node[K, V]
}

// NewNode creates a new instance of Node.
func NewNode[K Key, V Value](key K, value V, isRed bool) *Node[K, V] {
	node := &Node[K, V]{
		key:   key,
		value: value,
		isRed: isRed,
	}
	node.initWeight()
	return node
}

// initWeight sets initial weight of this node.
func (node *Node[K, V]) initWeight() {
	node.weight = node.value.Len()
}

func updateTreeWeight[K Key, V Value](node *Node[K, V]) {
	for node != nil {
		updateWeight(node)
		node = node.parent
	}
}

func updateWeight[K Key, V Value](node *Node[K, V]) {
	node.initWeight()

	if node.left != nil {
		node.increaseWeight(node.leftWeight())
	}

	if node.right != nil {
		node.increaseWeight(node.rightWeight())
	}
}

func (node *Node[K, V]) leftWeight() int {
	if node.left == nil {
		return 0
	}
	return node.left.weight
}

func (node *Node[K, V]) rightWeight() int {
	if node.right == nil {
		return 0
	}
	return node.right.weight
}

func (node *Node[K, V]) increaseWeight(weight int) {
	node.weight += weight
}

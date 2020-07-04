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

package splay

import (
	"fmt"
	"strings"

	"github.com/yorkie-team/yorkie/pkg/log"
)

// DocKey is an interface that represents the value of Node.
// User can extend this interface to use custom value in Node.
type Value interface {
	Len() int
	String() string
}

// Node is a node of Tree.
type Node struct {
	value  Value
	weight int

	left   *Node
	right  *Node
	parent *Node
}

// NewNode creates a new instance of Node.
func NewNode(value Value) *Node {
	n := &Node{
		value: value,
	}
	n.initWeight()
	return n
}

func (n *Node) Value() Value {
	return n.value
}

func (n *Node) leftWeight() int {
	if n.left == nil {
		return 0
	}
	return n.left.weight
}

func (n *Node) rightWeight() int {
	if n.right == nil {
		return 0
	}
	return n.right.weight
}

func (n *Node) initWeight() {
	n.weight = n.value.Len()
}

func (n *Node) increaseWeight(weight int) {
	n.weight += weight
}

// Tree is weighted binary search tree which is based on Splay tree.
// original paper on Splay Trees:
//  - https://www.cs.cmu.edu/~sleator/papers/self-adjusting.pdf
type Tree struct {
	root *Node
}

// NewTree creates a new instance of Tree.
func NewTree(root *Node) *Tree {
	return &Tree{
		root: root,
	}
}

// Insert inserts the node at the last.
func (t *Tree) Insert(node *Node) *Node {
	if t.root == nil {
		t.root = node
		return node
	}

	return t.InsertAfter(t.root, node)
}

// InsertAfter inserts the node after the given previous node.
func (t *Tree) InsertAfter(prev *Node, node *Node) *Node {
	t.Splay(prev)
	t.root = node
	node.right = prev.right
	if prev.right != nil {
		prev.right.parent = node
	}
	node.left = prev
	prev.parent = node
	prev.right = nil

	t.UpdateSubtree(prev)
	t.UpdateSubtree(node)

	return node
}

// Splay moves the given node to the root.
func (t *Tree) Splay(node *Node) {
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
			return
		}
	}
}

// IndexOf Find the index of the given node.
func (t *Tree) IndexOf(node *Node) int {
	if node == nil {
		return -1
	}

	index := 0
	current := node
	var prev *Node
	for current != nil {
		if prev == nil || prev == current.right {
			index += current.value.Len() + current.leftWeight()
		}
		prev = current
		current = current.parent
	}
	return index - node.value.Len()
}

func (t *Tree) Find(index int) (*Node, int) {
	node := t.root
	for {
		if node.left != nil && index <= node.leftWeight() {
			node = node.left
		} else if node.right != nil && node.leftWeight()+node.value.Len() < index {
			index -= node.leftWeight() + node.value.Len()
			node = node.right
		} else {
			index -= node.leftWeight()
			break
		}
	}

	if index > node.value.Len() {
		log.Logger.Fatalf(
			"out of bound of text index: node.length %d, pos %d",
			node.value.Len(),
			index,
		)
	}

	return node, index
}

// String returns a string containing node values.
func (t *Tree) String() string {
	var str []string
	traverseInOrder(t.root, func(node *Node) {
		str = append(str, node.value.String())
	})
	return strings.Join(str, "")
}

// AnnotatedString returns a string containing the meta data of the Node
// for debugging purpose.
func (t *Tree) AnnotatedString() string {
	var metaString []string
	traverseInOrder(t.root, func(node *Node) {
		metaString = append(metaString, fmt.Sprintf(
			"[%d,%d]%s",
			node.weight,
			node.value.Len(),
			node.value.String(),
		))
	})
	return strings.Join(metaString, "")
}

func (t *Tree) UpdateSubtree(node *Node) {
	node.initWeight()

	if node.left != nil {
		node.increaseWeight(node.leftWeight())
	}

	if node.right != nil {
		node.increaseWeight(node.rightWeight())
	}
}

func (t *Tree) Delete(node *Node) {
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
		maxNode := leftTree.maximum()
		leftTree.Splay(maxNode)
		leftTree.root.right = rightTree.root
		t.root = leftTree.root
	} else {
		t.root = rightTree.root
	}
}

func (t *Tree) rotateLeft(pivot *Node) {
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

	t.UpdateSubtree(root)
	t.UpdateSubtree(pivot)
}

func (t *Tree) rotateRight(pivot *Node) {
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

	t.UpdateSubtree(root)
	t.UpdateSubtree(pivot)
}

func (t *Tree) maximum() *Node {
	node := t.root
	for node.right != nil {
		node = node.right
	}
	return node
}

func traverseInOrder(node *Node, callback func(node *Node)) {
	if node == nil {
		return
	}

	traverseInOrder(node.left, callback)
	callback(node)
	traverseInOrder(node.right, callback)
}

func isLeftChild(node *Node) bool {
	return node != nil && node.parent != nil && node.parent.left == node
}

func isRightChild(node *Node) bool {
	return node != nil && node.parent != nil && node.parent.right == node
}

package datatype

import (
	"fmt"
	"strings"
)

// SplayValue is an interface that represents the value of SplayNode.
// User can extend this interface to use custom value in SplayNode.
type SplayValue interface {
	GetLength() int
	String() string
}

// SplayNode is a node of SplayTree.
type SplayNode struct {
	value  SplayValue
	weight int

	left   *SplayNode
	right  *SplayNode
	parent *SplayNode
}

// NewSplayNode creates a new instance of SplayNode.
func NewSplayNode(value SplayValue) *SplayNode {
	n := &SplayNode{
		value: value,
	}
	n.initWeight()
	return n
}

func (n *SplayNode) leftWeight() int {
	if n.left == nil {
		return 0
	}
	return n.left.weight
}

func (n *SplayNode) rightWeight() int {
	if n.right == nil {
		return 0
	}
	return n.right.weight
}

func (n *SplayNode) initWeight() {
	n.weight = n.value.GetLength()
}

func (n *SplayNode) increaseWeight(weight int) {
	n.weight += weight
}

// SplayTree is weighted binary search tree which is based on splay tree.
// original paper: https://www.cs.cmu.edu/~sleator/papers/self-adjusting.pdf
type SplayTree struct {
	root *SplayNode
}

// NewSplayTree creates a new instance of SplayTree.
func NewSplayTree(root *SplayNode) *SplayTree {
	return &SplayTree{
		root: root,
	}
}

// Insert inserts the node at the last.
func (t *SplayTree) Insert(node *SplayNode) *SplayNode {
	return t.InsertAfter(t.root, node)
}

// Insert inserts the node after the given previous node.
func (t *SplayTree) InsertAfter(prev *SplayNode, node *SplayNode) *SplayNode {
	t.Splay(prev)
	t.root = node
	node.right = prev.right
	if prev.right != nil {
		prev.right.parent = node
	}
	node.left = prev
	prev.parent = node
	prev.right = nil

	t.updateSubtree(prev)
	t.updateSubtree(node)

	return node
}

// Splay moves the given node to the root.
func (t *SplayTree) Splay(node *SplayNode) {
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
func (t *SplayTree) IndexOf(node *SplayNode) int {
	if node == nil {
		return -1
	}

	index := 0
	current := node
	var prev *SplayNode
	for current != nil {
		if prev == nil || prev == current.right {
			index += current.value.GetLength() + current.leftWeight()
		}
		prev = current
		current = current.parent
	}
	return index - node.value.GetLength()
}

// String returns a string containing node values.
func (t *SplayTree) String() string {
	var str []string
	traverseInOrder(t.root, func(node *SplayNode) {
		str = append(str, node.value.String())
	})
	return strings.Join(str, "")
}

// MetaString returns a string containing the metadata of the SplayNode
// for debugging purpose.
func (t *SplayTree) MetaString() string {
	var metaString []string
	traverseInOrder(t.root, func(node *SplayNode) {
		metaString = append(metaString, fmt.Sprintf(
			"[%d,%d]%s",
			node.weight,
			node.value.GetLength(),
			node.value.String(),
		))
	})
	return strings.Join(metaString, "")
}

func (t *SplayTree) rotateLeft(pivot *SplayNode) {
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

	t.updateSubtree(root)
	t.updateSubtree(pivot)
}

func (t *SplayTree) rotateRight(pivot *SplayNode) {
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

	t.updateSubtree(root)
	t.updateSubtree(pivot)
}

func (t *SplayTree) updateSubtree(node *SplayNode) {
	node.initWeight()

	if node.left != nil {
		node.increaseWeight(node.leftWeight())
	}

	if node.right != nil {
		node.increaseWeight(node.rightWeight())
	}
}

func traverseInOrder(node *SplayNode, callback func(node *SplayNode)) {
	if node == nil {
		return
	}

	traverseInOrder(node.left, callback)
	callback(node)
	traverseInOrder(node.right, callback)
}

func isLeftChild(node *SplayNode) bool {
	return node != nil && node.parent != nil && node.parent.left == node
}

func isRightChild(node *SplayNode) bool {
	return node != nil && node.parent != nil && node.parent.right == node
}

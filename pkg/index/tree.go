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

// Package index provides an index tree structure to represent a document of
// text-base editor.
package index

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

/**
 * About `index`, `size` and `TreePos` in index.Tree.
 *
 * `index` of index.Tree represents a position of a node in the tree.
 * `size` is used to calculate the index of nodes in the tree.
 * `index` in index.Tree inspired by ProseMirror's index.
 *
 * For example, empty paragraph's size is 0 and index 0 is the position of the:
 *    0
 * <p> </p>,                                p.size = 0
 *
 * If a paragraph has <i>, its size becomes 2 and there are 3 indexes:
 *     0   1    2
 *  <p> <i> </i> </p>                       p.size = 2, i.size = 0
 *
 * If the paragraph has <i> and <b>, its size becomes 4:
 *     0   1    2   3   4
 *  <p> <i> </i> <b> </b> </p>              p.size = 4, i.size = 0, b.size = 0
 *     0   1    2   3    4    5   6
 *  <p> <i> </i> <b> </b> <s> </s> </p>     p.size = 6, i.size = 0, b.size = 0, s.size = 0
 *
 * If a paragraph has text, its size becomes length of the characters:
 *     0 1 2 3
 *  <p> A B C </p>                          p.size = 3,   text.size = 3
 *
 * So the size of a node is the sum of the size and type of its children:
 *  `size = children(element type).length * 2 + children.reduce((child, acc) => child.size + acc, 0)`
 *
 * `TreePos` is also used to represent the position in the tree. It contains node and offset.
 * `TreePos` can be converted to `index` and vice versa.
 *
 * For example, if a paragraph has <i>, there are 3 indexes:
 *     0   1    2
 *  <p> <i> </i> </p>                       p.size = 2, i.size = 0
 *
 * In this case, index of TreePos(p, 0) is 0, index of TreePos(p, 1) is 2.
 * Index 1 can be converted to TreePos(i, 0).
 */

const (
	// DefaultTextType is the type of default text node.
	// TODO(hackerwins): Allow users to define the type of text node.
	DefaultTextType = "text"
)

const (
	// elementPaddingLength is the length of padding for element node. The element
	// has open tag and close tag, so the length is 2.
	elementPaddingLength = 2
)

// TraverseNode traverses the tree with the given callback.
func TraverseNode[V Value](node *Node[V], callback func(node *Node[V], depth int)) {
	postOrderTraversal(node, callback, 0)
}

// postOrderTraversal traverses the tree with postorder traversal.
func postOrderTraversal[V Value](node *Node[V], callback func(node *Node[V], depth int), depth int) {
	if node == nil {
		return
	}

	for _, child := range node.Children() {
		postOrderTraversal(child, callback, depth+1)
	}
	callback(node, depth)
}

// nodesBetween iterates the nodes between the given range.
// If the given range is collapsed, the callback is not called.
// It traverses the tree with postorder traversal.
func nodesBetween[V Value](root *Node[V], from, to int, callback func(node V)) {
	if from > to {
		panic(fmt.Sprintf("from cannot be greater than to %d > %d", from, to))
	}

	if from > root.Length {
		panic(fmt.Sprintf("from is out of range %d > %d", from, root.Length))
	}

	if to > root.Length {
		panic(fmt.Sprintf("to is out of range %d > %d", to, root.Length))
	}

	if from == to {
		return
	}

	pos := 0
	for _, child := range root.Children() {
		if from-child.PaddedLength() < pos && pos < to {
			fromChild := from - pos - 1
			if child.IsText() {
				fromChild = from - pos
			}
			toChild := to - pos - 1
			if child.IsText() {
				toChild = to - pos
			}
			nodesBetween(
				child,
				int(math.Max(0, float64(fromChild))),
				int(math.Min(float64(toChild), float64(child.Length))),
				callback,
			)

			if fromChild < 0 || toChild > child.Length || child.IsText() {
				callback(child.Value)
			}
		}
		pos += child.PaddedLength()
	}
}

// ToXML returns the XML representation of this tree.
func ToXML[V Value](node *Node[V]) string {
	if node.IsText() {
		return node.Value.String()
	}

	builder := strings.Builder{}
	builder.WriteString("<" + string(node.Type) + ">")
	for _, child := range node.Children() {
		builder.WriteString(ToXML(child))
	}
	builder.WriteString("</" + string(node.Type) + ">")

	return builder.String()
}

// Traverse traverses the tree with postorder traversal.
func Traverse[V Value](tree *Tree[V], callback func(node *Node[V], depth int)) {
	postOrderTraversal(tree.root, callback, 0)
}

// Value represents the data stored in the nodes of Tree.
type Value interface {
	IsRemoved() bool
	Length() int
	String() string
}

// Node is a node of Tree.
type Node[V Value] struct {
	Type string

	Parent   *Node[V]
	children []*Node[V]

	Value  V
	Length int
}

// NewNode creates a new instance of Node.
func NewNode[V Value](nodeType string, value V, children ...*Node[V]) *Node[V] {
	return &Node[V]{
		Type: nodeType,

		children: children,

		Length: value.Length(),
		Value:  value,
	}
}

// Len returns the length of the Node.
func (n *Node[V]) Len() int {
	return n.Length
}

// IsText returns whether the Node is text or not.
func (n *Node[V]) IsText() bool {
	return n.Type == DefaultTextType
}

// Append appends the given node to the end of the children.
func (n *Node[V]) Append(newNodes ...*Node[V]) {
	if n.IsText() {
		panic(errors.New("text node cannot have children"))
	}

	n.children = append(n.children, newNodes...)
	for _, newNode := range newNodes {
		newNode.Parent = n
		newNode.UpdateAncestorsSize()
	}
}

// Children returns the children of the given node.
func (n *Node[V]) Children(includeRemovedNode ...bool) []*Node[V] {
	if len(includeRemovedNode) > 0 && includeRemovedNode[0] {
		return n.children
	}

	// Tombstone nodes remain awhile in the tree during editing.
	// They will be removed after the editing is done.
	// So, we need to filter out the tombstone nodes to get the real children.
	children := make([]*Node[V], 0, len(n.children))
	for _, child := range n.children {
		if !child.Value.IsRemoved() {
			children = append(children, child)
		}
	}

	return children
}

// SetChildren sets the children of the given node.
func (n *Node[V]) SetChildren(children []*Node[V]) {
	if n.IsText() {
		panic(errors.New("text node cannot have children"))
	}

	n.children = children
	for _, child := range children {
		child.Parent = n
		child.UpdateAncestorsSize()
	}
}

// UpdateAncestorsSize updates the size of ancestors.
func (n *Node[V]) UpdateAncestorsSize() {
	parent := n.Parent
	sign := 1
	if n.Value.IsRemoved() {
		sign = -1
	}

	for parent != nil {
		parent.Length += n.PaddedLength() * sign

		parent = parent.Parent
	}
}

// PaddedLength returns the length of the node with padding.
func (n *Node[V]) PaddedLength() int {
	length := n.Length
	if !n.IsText() {
		length += elementPaddingLength
	}

	return length
}

// Child returns the child of the given index.
func (n *Node[V]) Child(index int) *Node[V] {
	if n.IsText() {
		panic(errors.New("text node cannot have children"))
	}

	return n.Children()[index]
}

// InsertAfterInternal inserts the given node after the given child.
// This method does not update the size of the ancestors.
func (n *Node[V]) InsertAfterInternal(newNode, prevNode *Node[V]) {
	if n.IsText() {
		panic(errors.New("text node cannot have children"))
	}

	offset := n.OffsetOfChild(prevNode)
	if offset == -1 {
		panic(errors.New("prevNode is not a child of the node"))
	}

	// TODO(hackerwins, krapie): Needs to inspect this code later
	n.children = append(n.children[:offset+1], n.children[offset:]...)
	n.children[offset+1] = newNode
	newNode.Parent = n
}

// nextSibling returns the next sibling of the node.
func (n *Node[V]) nextSibling() *Node[V] {
	offset := n.Parent.findOffset(n)

	// TODO(hackerwins): Needs to inspect the code below later.
	// if the node is the last child, there is no next sibling.
	if len(n.Parent.Children()) <= offset+1 {
		return nil
	}
	sibling := n.Parent.Children()[offset+1]
	if sibling != nil {
		return sibling
	}

	return nil
}

// findOffset returns the offset of the given node in the children.
func (n *Node[V]) findOffset(node *Node[V]) int {
	if n.IsText() {
		panic(errors.New("text node cannot have children"))
	}

	for i, child := range n.Children() {
		if child == node {
			return i
		}
	}

	return -1
}

// IsAncestorOf returns true if the node is an ancestor of the given node.
func (n *Node[V]) IsAncestorOf(node *Node[V]) bool {
	return n.ancestorOf(n, node)
}

// ancestorOf returns true if the given node is an ancestor of the other node.
func (n *Node[V]) ancestorOf(ancestor, node *Node[V]) bool {
	if ancestor == node {
		return false
	}

	for node.Parent != nil {
		if node.Parent == ancestor {
			return true
		}

		node = node.Parent
	}

	return false
}

// FindBranchOffset returns offset of the given descendant node in this node.
// If the given node is not a descendant of this node, it returns -1.
func (n *Node[V]) FindBranchOffset(node *Node[V]) int {
	if n.IsText() {
		panic(errors.New("text node cannot have children"))
	}

	current := node
	for current != nil {
		offset := n.OffsetOfChild(current)
		if offset != -1 {
			return offset
		}

		current = current.Parent
	}

	return -1
}

// InsertAt inserts the given node at the given offset.
func (n *Node[V]) InsertAt(newNode *Node[V], offset int) {
	if n.IsText() {
		panic(errors.New("text node cannot have children"))
	}

	n.insertAtInternal(newNode, offset)
	newNode.UpdateAncestorsSize()
}

// insertAtInternal inserts the given node at the given index.
// This method does not update the size of the ancestors.
func (n *Node[V]) insertAtInternal(newNode *Node[V], offset int) {
	if n.IsText() {
		panic(errors.New("text node cannot have children"))
	}

	// splice the new node into the children
	// if children array is empty or offset is out or range, append the new node
	if offset > len(n.children) || len(n.children) == 0 {
		n.children = append(n.children, newNode)
	} else {
		n.children = append(n.children[:offset], append([]*Node[V]{newNode}, n.children[offset:]...)...)
	}
	newNode.Parent = n
}

// Prepend prepends the given nodes to the children.
func (n *Node[V]) Prepend(children ...*Node[V]) {
	if n.IsText() {
		panic(errors.New("text node cannot have children"))
	}

	n.children = append(children, n.children...)
	for _, node := range children {
		node.Parent = n
		node.UpdateAncestorsSize()
	}
}

// InsertBefore inserts the given node before the given child.
func (n *Node[V]) InsertBefore(newNode, referenceNode *Node[V]) {
	if n.IsText() {
		panic(errors.New("text node cannot have children"))
	}

	offset := n.OffsetOfChild(referenceNode)
	if offset == -1 {
		panic(errors.New("child not found"))
	}

	n.insertAtInternal(newNode, offset)
	newNode.UpdateAncestorsSize()
}

// InsertAfter inserts the given node after the given child.
func (n *Node[V]) InsertAfter(newNode, referenceNode *Node[V]) {
	if n.IsText() {
		panic(errors.New("text node cannot have children"))
	}

	offset := n.OffsetOfChild(referenceNode)
	if offset == -1 {
		panic(errors.New("child not found"))
	}

	n.insertAtInternal(newNode, offset+1)
	newNode.UpdateAncestorsSize()
}

// hasTextChild returns true if the node has a text child.
func (n *Node[V]) hasTextChild() bool {
	for _, child := range n.Children() {
		if child.IsText() {
			return true
		}
	}

	return false
}

// OffsetOfChild returns offset of children of the given node.
func (n *Node[V]) OffsetOfChild(node *Node[V]) int {
	for i, child := range n.children {
		if child == node {
			return i
		}
	}

	return -1
}

// NodesBetween returns the nodes between the given range.
func (t *Tree[V]) NodesBetween(from int, to int, callback func(node V)) {
	nodesBetween(t.root, from, to, callback)
}

// TreePos is the position of a node in the tree.
type TreePos[V Value] struct {
	Node   *Node[V]
	Offset int
}

// Tree is a tree implementation to represent a document of text-based editors.
type Tree[V Value] struct {
	root *Node[V]
}

// NewTree creates a new instance of Tree.
func NewTree[V Value](root *Node[V]) *Tree[V] {
	return &Tree[V]{
		root: root,
	}
}

// Root returns the root node of the tree.
func (t *Tree[V]) Root() *Node[V] {
	return t.root
}

// FindTreePos finds the position of the given index in the tree.
func (t *Tree[V]) FindTreePos(index int, preferTexts ...bool) *TreePos[V] {
	preferText := true
	if len(preferTexts) > 0 {
		preferText = preferTexts[0]
	}

	return t.findTreePos(t.root, index, preferText)
}

func (t *Tree[V]) findTreePos(node *Node[V], index int, preferText bool) *TreePos[V] {
	if index > node.Length {
		panic(fmt.Errorf("index is out of range: %d > %d", index, node.Length))
	}

	if node.IsText() {
		return &TreePos[V]{
			Node:   node,
			Offset: index,
		}
	}

	// offset is the index of the child node.
	// pos is the window of the index in the given node.
	offset := 0
	pos := 0
	for _, child := range node.Children() {
		// The pos is in both sides of the text node, we should traverse
		// inside the text node if preferText is true.
		if preferText && child.IsText() && child.Length >= index-pos {
			return t.findTreePos(child, index-pos, preferText)
		}

		// The position is in left side of the element node.
		if index == pos {
			return &TreePos[V]{
				Node:   node,
				Offset: offset,
			}
		}

		// The position is in right side of the element node and preferText is false.
		if !preferText && child.PaddedLength() == index-pos {
			return &TreePos[V]{
				Node:   node,
				Offset: offset + 1,
			}
		}

		// The position is in middle the element node.
		if child.PaddedLength() > index-pos {
			// If we traverse inside the element node, we should skip the open.
			skipOpenSize := 1
			return t.findTreePos(child, index-pos-skipOpenSize, preferText)
		}

		pos += child.PaddedLength()
		offset++
	}

	// The position is in the end of the element node.
	return &TreePos[V]{
		Node:   node,
		Offset: offset,
	}
}

// TreePosToPath returns path from given treePos
func (t *Tree[V]) TreePosToPath(treePos *TreePos[V]) []int {
	var path []int
	node := treePos.Node

	if node.IsText() {
		offset := node.Parent.OffsetOfChild(node)
		if offset == -1 {
			panic("invalid treePos")
		}

		leftSiblingsSize := 0
		for _, child := range node.Parent.Children()[:offset] {
			leftSiblingsSize += child.Length
		}

		node = node.Parent
		path = append(path, leftSiblingsSize+treePos.Offset)
	} else {
		path = append(path, treePos.Offset)
	}

	for node.Parent != nil {
		var pathInfo int
		for i, child := range node.Parent.Children() {
			if child == node {
				pathInfo = i
				break
			}
		}

		if ^pathInfo == 0 {
			panic("invalid treePos")
		}

		path = append(path, pathInfo)

		node = node.Parent
	}

	// return path array to reverse order
	reversePath := make([]int, len(path))
	for i, pathInfo := range path {
		reversePath[len(path)-i-1] = pathInfo
	}

	return reversePath
}

// PathToTreePos returns treePos from given path
func (t *Tree[V]) PathToTreePos(path []int) *TreePos[V] {
	if len(path) == 0 {
		panic("unacceptable path")
	}

	node := t.root
	for i := 0; i < len(path)-1; i++ {
		pathElement := path[i]
		node = node.Children()[pathElement]

		if node == nil {
			panic("unacceptable path")
		}
	}

	if node.hasTextChild() {
		return findTextPos(node, path[len(path)-1])
	}
	if len(node.Children()) < path[len(path)-1] {
		panic("unacceptable path")
	}

	return &TreePos[V]{
		Node: node,
	}
}

// findTextPos returns the tree position of the given path element.
func findTextPos[V Value](node *Node[V], pathElement int) *TreePos[V] {
	if node.Length < pathElement {
		panic("unacceptable path")
	}

	for _, childNode := range node.Children() {
		if childNode.Length < pathElement {
			pathElement -= childNode.Length
		} else {
			node = childNode

			break
		}
	}

	return &TreePos[V]{
		Node:   node,
		Offset: pathElement,
	}
}

// FindPostorderRight finds right node of the given tree position with postorder traversal.
func (t *Tree[V]) FindPostorderRight(pos *TreePos[V]) V {
	node := pos.Node
	offset := pos.Offset

	if node.IsText() {
		if node.Len() == offset {
			if nextSibling := node.nextSibling(); nextSibling != nil {
				return nextSibling.Value
			}

			return node.Parent.Value
		}

		return node.Value
	}

	if len(node.Children()) == offset {
		return node.Value
	}

	return t.FindLeftmost(node.Children()[offset])
}

// GetAncestors returns the ancestors of the given node.
func (t *Tree[V]) GetAncestors(node *Node[V]) []*Node[V] {
	var ancestors []*Node[V]
	parent := node.Parent
	for parent != nil {
		ancestors = append([]*Node[V]{parent}, ancestors...)
		parent = parent.Parent
	}
	return ancestors
}

// FindCommonAncestor finds the lowest common ancestor of the given nodes.
func (t *Tree[V]) FindCommonAncestor(nodeA, nodeB *Node[V]) V {
	if nodeA == nodeB {
		return nodeA.Value
	}

	ancestorsOfA := t.GetAncestors(nodeA)
	ancestorsOfB := t.GetAncestors(nodeB)

	var commonAncestor V
	for i := 0; i < len(ancestorsOfA); i++ {
		ancestorOfA := ancestorsOfA[i]
		ancestorOfB := ancestorsOfB[i]

		if ancestorOfA != ancestorOfB {
			break
		}

		commonAncestor = ancestorOfA.Value
	}

	return commonAncestor
}

// FindLeftmost finds the leftmost node of the given tree.
func (t *Tree[V]) FindLeftmost(node *Node[V]) V {
	if node.IsText() || len(node.Children()) == 0 {
		return node.Value
	}

	return t.FindLeftmost(node.Children()[0])
}

// IndexOf returns the index of the given node.
func (t *Tree[V]) IndexOf(node *Node[V]) int {
	index := 0
	current := node

	for current != t.root {
		parent := current.Parent
		if parent == nil {
			panic(errors.New("parent is not found"))
		}

		offset := parent.findOffset(current)
		childrenSlice := parent.Children()[:offset]
		for _, previous := range childrenSlice {
			index += previous.PaddedLength()
		}

		// If this step escape from element node, we should add 1 to the index,
		// because the element node has open tag.
		if current != t.root && current != node && !current.IsText() {
			index++
		}

		current = parent
	}

	return index
}

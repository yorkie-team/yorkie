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
 *
 * `path` of crdt.IndexTree represents a position like `index` in crdt.IndexTree.
 * It contains offsets of each node from the root node as elements except the last.
 * The last element of the path represents the position in the parent node.
 *
 * Let's say we have a tree like this:
 *                     0 1 2
 * <p> <i> a b </i> <b> c d </b> </p>
 *
 * The path of the position between 'c' and 'd' is [1, 1]. The first element of the
 * path is the offset of the <b> in <p> and the second element represents the position
 * between 'c' and 'd' in <b>.
 */

var (
	// ErrInvalidMethodCallForTextNode is returned when a invalid method is called for text node.
	ErrInvalidMethodCallForTextNode = errors.New("text node cannot have children")

	// ErrChildNotFound is returned when a child is not found.
	ErrChildNotFound = errors.New("child not found")

	// ErrUnreachablePath is returned when a path is unreachable.
	ErrUnreachablePath = errors.New("unreachable path")

	// ErrInvalidTreePos is returned when a TreePos is invalid.
	ErrInvalidTreePos = errors.New("invalid tree pos")
)

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
	postorderTraversal(node, callback, 0)
}

// postorderTraversal traverses the tree with postorder traversal.
func postorderTraversal[V Value](node *Node[V], callback func(node *Node[V], depth int), depth int) {
	if node == nil {
		return
	}

	for _, child := range node.Children(true) {
		postorderTraversal(child, callback, depth+1)
	}
	callback(node, depth)
}

// TagContained represents whether the opening or closing tag of a element is selected.
type TagContained int

const (
	// AllContained represents that both opening and closing tag of a element are selected.
	AllContained TagContained = 1 + iota
	// OpeningContained represents that only the opening tag is selected.
	OpeningContained
	// ClosingContained represents that only the closing tag is selected.
	ClosingContained
)

// ToString returns the string of TagContain.
func (c TagContained) ToString() string {
	var str string
	switch c {
	case AllContained:
		str = "All"
	case OpeningContained:
		str = "Opening"
	case ClosingContained:
		str = "Closing"
	}
	return str
}

// nodesBetween iterates the nodes between the given range.
// If the given range is collapsed, the callback is not called.
// It traverses the tree with postorder traversal.
// NOTE(sejongk): Nodes should not be removed in callback, because it leads wrong behaviors.
func nodesBetween[V Value](root *Node[V], from, to int, callback func(node V, contain TagContained)) error {
	if from > to {
		return fmt.Errorf("from cannot be greater than to %d > %d", from, to)
	}

	if from > root.Length {
		return fmt.Errorf("from is out of range %d > %d", from, root.Length)
	}

	if to > root.Length {
		return fmt.Errorf("to is out of range %d > %d", to, root.Length)
	}

	if from == to {
		return nil
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

			if err := nodesBetween(
				child,
				int(math.Max(0, float64(fromChild))),
				int(math.Min(float64(toChild), float64(child.Length))),
				callback,
			); err != nil {
				return err
			}

			if fromChild < 0 || toChild > child.Length || child.IsText() {
				var contain TagContained
				if (fromChild < 0 && toChild > child.Length) || child.IsText() {
					contain = AllContained
				} else if fromChild < 0 {
					contain = OpeningContained
				} else {
					contain = ClosingContained
				}
				callback(child.Value, contain)
			}
		}
		pos += child.PaddedLength()
	}

	return nil
}

// ToXML returns the XML representation of this tree.
func ToXML[V Value](node *Node[V]) string {
	if node.IsText() {
		return node.Value.String()
	}

	builder := strings.Builder{}
	builder.WriteString("<" + string(node.Type) + node.Value.Attributes() + ">")
	for _, child := range node.Children() {
		builder.WriteString(ToXML(child))
	}
	builder.WriteString("</" + string(node.Type) + ">")

	return builder.String()
}

// Traverse traverses the tree with postorder traversal.
func Traverse[V Value](tree *Tree[V], callback func(node *Node[V], depth int)) {
	postorderTraversal(tree.root, callback, 0)
}

// Value represents the data stored in the nodes of Tree.
type Value interface {
	IsRemoved() bool
	Length() int
	String() string
	Attributes() string
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
func (n *Node[V]) Append(newNodes ...*Node[V]) error {
	if n.IsText() {
		return ErrInvalidMethodCallForTextNode
	}

	n.children = append(n.children, newNodes...)
	for _, newNode := range newNodes {
		newNode.Parent = n
		newNode.UpdateAncestorsSize()
	}

	return nil
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
func (n *Node[V]) SetChildren(children []*Node[V]) error {
	if n.IsText() {
		return ErrInvalidMethodCallForTextNode
	}

	n.children = children
	for _, child := range children {
		child.Parent = n
		child.UpdateAncestorsSize()
	}

	return nil
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
func (n *Node[V]) Child(index int) (*Node[V], error) {
	if n.IsText() {
		return nil, ErrInvalidMethodCallForTextNode
	}

	return n.Children()[index], nil
}

// InsertAfterInternal inserts the given node after the given child.
// This method does not update the size of the ancestors.
func (n *Node[V]) InsertAfterInternal(newNode, prevNode *Node[V]) error {
	if n.IsText() {
		return ErrInvalidMethodCallForTextNode
	}

	offset := n.OffsetOfChild(prevNode)
	if offset == -1 {
		return errors.New("prevNode is not a child of the node")
	}

	// TODO(hackerwins, krapie): Needs to inspect this code later
	n.children = append(n.children[:offset+1], n.children[offset:]...)
	n.children[offset+1] = newNode
	newNode.Parent = n

	return nil
}

// nextSibling returns the next sibling of the node.
func (n *Node[V]) nextSibling() (*Node[V], error) {
	offset, err := n.Parent.FindOffset(n)
	if err != nil {
		return nil, err
	}

	// TODO(hackerwins): Needs to inspect the code below later.
	// if the node is the last child, there is no next sibling.
	if len(n.Parent.Children()) <= offset+1 {
		return nil, nil
	}
	sibling := n.Parent.Children()[offset+1]
	if sibling != nil {
		return sibling, nil
	}

	return nil, nil
}

// FindOffset returns the offset of the given node in the children.
func (n *Node[V]) FindOffset(node *Node[V]) (int, error) {
	if n.IsText() {
		return 0, ErrInvalidMethodCallForTextNode
	}

	// If nodes are removed, the offset of the removed node is the number of
	// nodes before the node excluding the removed nodes.
	offset := 0
	for _, child := range n.Children(true) {
		if child == node {
			return offset, nil
		}
		if !child.Value.IsRemoved() {
			offset++
		}
	}

	return -1, ErrChildNotFound
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
func (n *Node[V]) FindBranchOffset(node *Node[V]) (int, error) {
	if n.IsText() {
		return 0, ErrInvalidMethodCallForTextNode
	}

	current := node
	for current != nil {
		offset := n.OffsetOfChild(current)
		if offset != -1 {
			return offset, nil
		}

		current = current.Parent
	}

	return -1, nil
}

// InsertAt inserts the given node at the given offset.
func (n *Node[V]) InsertAt(newNode *Node[V], offset int) error {
	if n.IsText() {
		return ErrInvalidMethodCallForTextNode
	}

	if err := n.insertAtInternal(newNode, offset); err != nil {
		return err
	}

	newNode.UpdateAncestorsSize()

	return nil
}

// insertAtInternal inserts the given node at the given index.
// This method does not update the size of the ancestors.
func (n *Node[V]) insertAtInternal(newNode *Node[V], offset int) error {
	if n.IsText() {
		return ErrInvalidMethodCallForTextNode
	}

	// splice the new node into the children
	// if children array is empty or offset is out or range, append the new node
	if offset > len(n.children) || len(n.children) == 0 {
		n.children = append(n.children, newNode)
	} else {
		n.children = append(n.children[:offset], append([]*Node[V]{newNode}, n.children[offset:]...)...)
	}
	newNode.Parent = n

	return nil
}

// Prepend prepends the given nodes to the children.
func (n *Node[V]) Prepend(children ...*Node[V]) error {
	if n.IsText() {
		return ErrInvalidMethodCallForTextNode
	}

	n.children = append(children, n.children...)
	for _, node := range children {
		node.Parent = n

		if !node.Value.IsRemoved() {
			node.UpdateAncestorsSize()
		}
	}

	return nil
}

// RemoveChild removes the given child.
func (n *Node[V]) RemoveChild(child *Node[V]) error {
	if n.IsText() {
		return ErrInvalidMethodCallForTextNode
	}
	offset := -1

	for i, c := range n.children {
		if c == child {
			offset = i
			break
		}
	}

	if offset == -1 {
		return ErrChildNotFound
	}

	n.children = append(n.children[:offset], n.children[offset+1:]...)
	child.Parent = nil

	return nil
}

// InsertBefore inserts the given node before the given child.
func (n *Node[V]) InsertBefore(newNode, referenceNode *Node[V]) error {
	if n.IsText() {
		return ErrInvalidMethodCallForTextNode
	}

	offset := n.OffsetOfChild(referenceNode)
	if offset == -1 {
		return ErrChildNotFound
	}

	if err := n.insertAtInternal(newNode, offset); err != nil {
		return err
	}

	newNode.UpdateAncestorsSize()

	return nil
}

// InsertAfter inserts the given node after the given child.
func (n *Node[V]) InsertAfter(newNode, referenceNode *Node[V]) error {
	if n.IsText() {
		return ErrInvalidMethodCallForTextNode
	}

	offset := n.OffsetOfChild(referenceNode)
	if offset == -1 {
		return ErrChildNotFound
	}

	if err := n.insertAtInternal(newNode, offset+1); err != nil {
		return err
	}

	newNode.UpdateAncestorsSize()

	return nil
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
func (t *Tree[V]) NodesBetween(from int, to int, callback func(node V, contain TagContained)) error {
	return nodesBetween(t.root, from, to, callback)
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
func (t *Tree[V]) FindTreePos(index int, preferTexts ...bool) (*TreePos[V], error) {
	preferText := true
	if len(preferTexts) > 0 {
		preferText = preferTexts[0]
	}

	return t.findTreePos(t.root, index, preferText)
}

func (t *Tree[V]) findTreePos(node *Node[V], index int, preferText bool) (*TreePos[V], error) {
	if index > node.Length {
		return nil, fmt.Errorf("index is out of range: %d > %d", index, node.Length)
	}

	if node.IsText() {
		return &TreePos[V]{
			Node:   node,
			Offset: index,
		}, nil
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
			}, nil
		}

		// The position is in right side of the element node and preferText is false.
		if !preferText && child.PaddedLength() == index-pos {
			return &TreePos[V]{
				Node:   node,
				Offset: offset + 1,
			}, nil
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
	}, nil
}

// TreePosToPath returns path from given treePos
func (t *Tree[V]) TreePosToPath(treePos *TreePos[V]) ([]int, error) {
	var path []int
	node := treePos.Node

	if node.IsText() {
		offset := node.Parent.OffsetOfChild(node)
		if offset == -1 {
			return nil, ErrInvalidTreePos
		}

		leftSiblingsSize, err := t.LeftSiblingsSize(node.Parent, offset)
		if err != nil {
			return nil, err
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
			return nil, ErrInvalidTreePos
		}

		path = append(path, pathInfo)

		node = node.Parent
	}

	// return path array to reverse order
	reversePath := make([]int, len(path))
	for i, pathInfo := range path {
		reversePath[len(path)-i-1] = pathInfo
	}

	return reversePath, nil
}

// LeftSiblingsSize returns the size of left siblings of the given node
func (t *Tree[V]) LeftSiblingsSize(parent *Node[V], offset int) (int, error) {
	leftSiblingsSize := 0
	children := parent.Children()
	for i := 0; i < offset; i++ {
		if children[i] == nil || children[i].Value.IsRemoved() {
			continue
		}
		leftSiblingsSize += children[i].PaddedLength()
	}

	return leftSiblingsSize, nil
}

// PathToTreePos returns treePos from given path
func (t *Tree[V]) PathToTreePos(path []int) (*TreePos[V], error) {
	if len(path) == 0 {
		return nil, ErrUnreachablePath
	}

	node := t.root
	for i := 0; i < len(path)-1; i++ {
		pathElement := path[i]
		node = node.Children()[pathElement]

		if node == nil {
			return nil, ErrUnreachablePath
		}
	}

	if node.hasTextChild() {
		return findTextPos(node, path[len(path)-1])
	}
	if len(node.Children()) < path[len(path)-1] {
		return nil, ErrUnreachablePath
	}

	return &TreePos[V]{
		Node:   node,
		Offset: path[len(path)-1],
	}, nil
}

// PathToIndex converts the given path to index.
func (t *Tree[V]) PathToIndex(path []int) (int, error) {
	treePos, err := t.PathToTreePos(path)
	if err != nil {
		return -1, err
	}

	idx, err := t.IndexOf(treePos)
	if err != nil {
		return 0, err
	}

	return idx, nil
}

// findTextPos returns the tree position of the given path element.
func findTextPos[V Value](node *Node[V], pathElement int) (*TreePos[V], error) {
	if node.Length < pathElement {
		return nil, ErrUnreachablePath
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
	}, nil
}

// FindPostorderRight finds right node of the given tree position with postorder traversal.
func (t *Tree[V]) FindPostorderRight(pos *TreePos[V]) (V, error) {
	node := pos.Node
	offset := pos.Offset

	if node.IsText() {
		if node.Len() == offset {
			nextSibling, err := node.nextSibling()
			if err != nil {
				return nextSibling.Value, err
			}

			if nextSibling != nil {
				return nextSibling.Value, nil
			}

			return node.Parent.Value, nil
		}

		return node.Value, nil
	}

	if len(node.Children()) == offset {
		return node.Value, nil
	}

	return t.FindLeftmost(node.Children()[offset]), nil
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

// IndexOf returns the index of the given tree position.
func (t *Tree[V]) IndexOf(pos *TreePos[V]) (int, error) {
	node, offset := pos.Node, pos.Offset

	size := 0
	depth := 1

	if node.IsText() {
		size += offset

		parent := node.Parent
		offsetOfNode, err := parent.FindOffset(node)
		if err != nil {
			return 0, err
		}

		leftSiblingsSize, err := t.LeftSiblingsSize(parent, offsetOfNode)
		if err != nil {
			return 0, err
		}
		size += leftSiblingsSize

		node = node.Parent
	} else {
		leftSiblingsSize, err := t.LeftSiblingsSize(node, offset)
		if err != nil {
			return 0, err
		}
		size += leftSiblingsSize
	}

	for node.Parent != nil {
		parent := node.Parent
		offsetOfNode, err := parent.FindOffset(node)
		if err != nil {
			return 0, err
		}

		leftSiblingsSize, err := t.LeftSiblingsSize(parent, offsetOfNode)
		if err != nil {
			return 0, err
		}

		size += leftSiblingsSize
		depth++
		node = node.Parent
	}

	return size + depth - 1, nil
}

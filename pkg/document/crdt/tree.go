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

package crdt

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/index"
	"github.com/yorkie-team/yorkie/pkg/llrb"
)

var (
	// ErrNodeNotFound is returned when the node is not found.
	ErrNodeNotFound = errors.New("node not found")
)

var (
	// DummyTreePos is a dummy position of Tree. It is used to represent the head node of RGASplit.
	DummyTreePos = &TreePos{
		CreatedAt: time.InitialTicket,
		Offset:    0,
	}
)

const (
	// DummyHeadType is a type of dummy head. It is used to represent the head node of RGASplit.
	DummyHeadType = "dummy"
)

// TreeNodeForTest is a TreeNode for test.
type TreeNodeForTest struct {
	Type      string
	Children  []TreeNodeForTest
	Value     string
	Size      int
	IsRemoved bool
}

// TreeNode is a node of Tree.
type TreeNode struct {
	IndexTreeNode *index.Node[*TreeNode]

	Pos       *TreePos
	RemovedAt *time.Ticket

	Next    *TreeNode
	Prev    *TreeNode
	InsPrev *TreeNode

	Value string
	Attrs *RHT
}

// TreePos represents the position of Tree.
type TreePos struct {
	CreatedAt *time.Ticket
	Offset    int
}

// NewTreePos creates a new instance of TreePos.
func NewTreePos(createdAt *time.Ticket, offset int) *TreePos {
	return &TreePos{
		CreatedAt: createdAt,
		Offset:    offset,
	}
}

// Compare compares the given two CRDTTreePos.
func (t *TreePos) Compare(other llrb.Key) int {
	compare := t.CreatedAt.Compare(other.(*TreePos).CreatedAt)
	if compare != 0 {
		return compare
	}

	if t.Offset > other.(*TreePos).Offset {
		return 1
	} else if t.Offset < other.(*TreePos).Offset {
		return -1
	}
	return 0
}

// NewTreeNode creates a new instance of TreeNode.
func NewTreeNode(pos *TreePos, nodeType string, attributes *RHT, value ...string) *TreeNode {
	node := &TreeNode{
		Pos: pos,
	}

	if len(value) > 0 {
		node.Value = value[0]
	}
	node.Attrs = attributes

	node.IndexTreeNode = index.NewNode(nodeType, node)

	return node
}

// Type returns the type of the Node.
func (n *TreeNode) Type() string {
	return string(n.IndexTreeNode.Type)
}

// Len returns the length of the Node.
func (n *TreeNode) Len() int {
	return n.IndexTreeNode.Len()
}

// IsText returns whether the Node is text or not.
func (n *TreeNode) IsText() bool {
	return n.IndexTreeNode.IsText()
}

// IsRemoved returns whether the Node is removed or not.
func (n *TreeNode) IsRemoved() bool {
	return n.RemovedAt != nil
}

// Length returns the length of this node.
func (n *TreeNode) Length() int {
	encoded := utf16.Encode([]rune(n.Value))
	return len(encoded)
}

// String returns the string representation of this node's value.
func (n *TreeNode) String() string {
	return n.Value
}

// Attributes returns the string representation of this node's attributes.
func (n *TreeNode) Attributes() string {
	if n.Attrs == nil || n.Attrs.Len() == 0 {
		return ""
	}

	return " " + n.Attrs.ToXML()
}

// Append appends the given node to the end of the children.
func (n *TreeNode) Append(newNodes ...*TreeNode) error {
	indexNodes := make([]*index.Node[*TreeNode], len(newNodes))
	for i, newNode := range newNodes {
		indexNodes[i] = newNode.IndexTreeNode
	}

	return n.IndexTreeNode.Append(indexNodes...)
}

// Prepend prepends the given node to the beginning of the children.
func (n *TreeNode) Prepend(newNodes ...*TreeNode) error {
	indexNodes := make([]*index.Node[*TreeNode], len(newNodes))
	for i, newNode := range newNodes {
		indexNodes[i] = newNode.IndexTreeNode
	}

	return n.IndexTreeNode.Prepend(indexNodes...)
}

// Child returns the child of the given offset.
func (n *TreeNode) Child(offset int) (*TreeNode, error) {
	child, err := n.IndexTreeNode.Child(offset)
	if err != nil {
		return nil, err
	}

	return child.Value, nil
}

// Split splits the node at the given offset.
func (n *TreeNode) Split(offset int) (*TreeNode, error) {
	if n.IsText() {
		return n.SplitText(offset)
	}

	return nil, nil
}

// SplitText splits the text node at the given offset.
func (n *TreeNode) SplitText(offset int) (*TreeNode, error) {
	if offset == 0 || offset == n.Len() {
		return nil, nil
	}

	encoded := utf16.Encode([]rune(n.Value))
	leftRune := utf16.Decode(encoded[0:offset])
	rightRune := utf16.Decode(encoded[offset:])

	n.Value = string(leftRune)
	n.IndexTreeNode.Length = len(leftRune)

	rightNode := NewTreeNode(&TreePos{
		CreatedAt: n.Pos.CreatedAt,
		Offset:    offset,
	}, n.Type(), nil, string(rightRune))
	if err := n.IndexTreeNode.Parent.InsertAfterInternal(
		rightNode.IndexTreeNode,
		n.IndexTreeNode,
	); err != nil {
		return nil, err
	}

	return rightNode, nil
}

// remove marks the node as removed.
func (n *TreeNode) remove(removedAt *time.Ticket) {
	justRemoved := n.RemovedAt == nil
	if n.RemovedAt == nil || n.RemovedAt.Compare(removedAt) > 0 {
		n.RemovedAt = removedAt
	}

	if justRemoved {
		n.IndexTreeNode.UpdateAncestorsSize()
	}
}

// InsertAt inserts the given node at the given offset.
func (n *TreeNode) InsertAt(newNode *TreeNode, offset int) error {
	return n.IndexTreeNode.InsertAt(newNode.IndexTreeNode, offset)
}

// DeepCopy copies itself deeply.
func (n *TreeNode) DeepCopy() (*TreeNode, error) {
	var clone *TreeNode
	if n.Attrs != nil {
		clone = NewTreeNode(n.Pos, n.Type(), n.Attrs.DeepCopy(), n.Value)
	} else {
		clone = NewTreeNode(n.Pos, n.Type(), nil, n.Value)
	}
	clone.RemovedAt = n.RemovedAt

	if n.IsText() {
		return clone, nil
	}

	var children []*index.Node[*TreeNode]
	for _, child := range n.IndexTreeNode.Children(true) {
		node, err := child.Value.DeepCopy()
		if err != nil {
			return nil, err
		}

		children = append(children, node.IndexTreeNode)
	}
	if err := clone.IndexTreeNode.SetChildren(children); err != nil {
		return nil, err
	}

	return clone, nil
}

// Tree represents the tree of CRDT. It has doubly linked list structure and
// index tree structure.
type Tree struct {
	DummyHead      *TreeNode
	IndexTree      *index.Tree[*TreeNode]
	NodeMapByPos   *llrb.Tree[*TreePos, *TreeNode]
	removedNodeMap map[string]*TreeNode

	createdAt *time.Ticket
	movedAt   *time.Ticket
	removedAt *time.Ticket
}

// NewTree creates a new instance of Tree.
func NewTree(root *TreeNode, createdAt *time.Ticket) *Tree {
	tree := &Tree{
		DummyHead:      NewTreeNode(DummyTreePos, DummyHeadType, nil),
		IndexTree:      index.NewTree[*TreeNode](root.IndexTreeNode),
		NodeMapByPos:   llrb.NewTree[*TreePos, *TreeNode](),
		removedNodeMap: make(map[string]*TreeNode),
		createdAt:      createdAt,
	}

	previous := tree.DummyHead
	index.Traverse(tree.IndexTree, func(node *index.Node[*TreeNode], depth int) {
		tree.InsertAfter(previous, node.Value)
		previous = node.Value
	})

	return tree
}

// Marshal returns the JSON encoding of this Tree.
func (t *Tree) Marshal() string {
	builder := &strings.Builder{}
	marshal(builder, t.Root())
	return builder.String()
}

// removedNodesLen returns the length of removed nodes.
func (t *Tree) removedNodesLen() int {
	return len(t.removedNodeMap)
}

// purgeRemovedNodesBefore physically purges nodes that have been removed.
func (t *Tree) purgeRemovedNodesBefore(ticket *time.Ticket) (int, error) {
	count := 0
	nodesToBeRemoved := make(map[*TreeNode]bool)

	for _, node := range t.removedNodeMap {
		if node.RemovedAt != nil && ticket.Compare(node.RemovedAt) >= 0 {
			count++
			nodesToBeRemoved[node] = true
		}
	}

	if err := index.TraverseAll(t.IndexTree, func(node *index.Node[*TreeNode], depth int) error {
		if _, ok := nodesToBeRemoved[node.Value]; ok {
			parent := node.Parent

			if parent == nil {
				count--
				delete(nodesToBeRemoved, node.Value)
				return nil
			}

			if err := parent.RemoveChild(node); err != nil {
				return err
			}
		}

		return nil
	}); err != nil {
		return 0, err
	}

	for node := range nodesToBeRemoved {
		t.NodeMapByPos.Remove(node.Pos)
		t.Purge(node)
		delete(t.removedNodeMap, node.Pos.CreatedAt.StructureAsString()+":"+strconv.Itoa(node.Pos.Offset))
	}

	return count, nil
}

// Purge physically purges the given node.
func (t *Tree) Purge(node *TreeNode) {
	if node.Prev != nil {
		node.Prev.Next = node.Next
	}

	if node.Next != nil {
		node.Next.Prev = node.Prev
	}

	node.Prev = nil
	node.Next = nil
	node.InsPrev = nil
}

// marshal returns the JSON encoding of this Tree.
func marshal(builder *strings.Builder, node *TreeNode) {
	if node.IsText() {
		builder.WriteString(fmt.Sprintf(`{"type":"%s","value":"%s"}`, node.Type(), node.Value))
		return
	}

	builder.WriteString(fmt.Sprintf(`{"type":"%s","children":[`, node.Type()))
	for idx, child := range node.IndexTreeNode.Children() {
		if idx != 0 {
			builder.WriteString(",")
		}
		marshal(builder, child.Value)
	}
	builder.WriteString(`]`)

	if node.Attrs != nil && node.Attrs.Len() > 0 {
		builder.WriteString(fmt.Sprintf(`,"attributes":`))
		builder.WriteString(node.Attrs.Marshal())
	}

	builder.WriteString(`}`)
}

// DeepCopy copies itself deeply.
func (t *Tree) DeepCopy() (Element, error) {
	node, err := t.Root().DeepCopy()
	if err != nil {
		return nil, err
	}

	return NewTree(node, t.createdAt), nil
}

// CreatedAt returns the creation time of this Tree.
func (t *Tree) CreatedAt() *time.Ticket {
	return t.createdAt
}

// RemovedAt returns the removal time of this Tree.
func (t *Tree) RemovedAt() *time.Ticket {
	return t.removedAt
}

// MovedAt returns the move time of this Tree.
func (t *Tree) MovedAt() *time.Ticket {
	return t.movedAt
}

// SetMovedAt sets the move time of this Text.
func (t *Tree) SetMovedAt(movedAt *time.Ticket) {
	t.movedAt = movedAt
}

// SetRemovedAt sets the removal time of this array.
func (t *Tree) SetRemovedAt(removedAt *time.Ticket) {
	t.removedAt = removedAt
}

// Remove removes this Text.
func (t *Tree) Remove(removedAt *time.Ticket) bool {
	if (removedAt != nil && removedAt.After(t.createdAt)) &&
		(t.removedAt == nil || removedAt.After(t.removedAt)) {
		t.removedAt = removedAt
		return true
	}
	return false
}

// InsertAfter inserts the given node after the given previous node.
func (t *Tree) InsertAfter(prevNode *TreeNode, newNode *TreeNode) {
	next := prevNode.Next
	prevNode.Next = newNode
	newNode.Prev = prevNode

	if next != nil {
		newNode.Next = next
		next.Prev = newNode
	}

	t.NodeMapByPos.Put(newNode.Pos, newNode)
}

// Nodes traverses the tree and returns the list of nodes.
func (t *Tree) Nodes() []*TreeNode {
	var nodes []*TreeNode
	index.Traverse(t.IndexTree, func(node *index.Node[*TreeNode], depth int) {
		nodes = append(nodes, node.Value)
	})

	return nodes
}

// Root returns the root node of the tree.
func (t *Tree) Root() *TreeNode {
	return t.IndexTree.Root().Value
}

// ToXML returns the XML encoding of this tree.
func (t *Tree) ToXML() string {
	return ToXML(t.Root())
}

// EditByIndex edits the given range with the given value.
// This method uses indexes instead of a pair of TreePos for testing.
func (t *Tree) EditByIndex(start, end int, content *TreeNode, editedAt *time.Ticket) error {
	fromPos, err := t.FindPos(start)
	if err != nil {
		return err
	}
	toPos, err := t.FindPos(end)
	if err != nil {
		return err
	}

	return t.Edit(fromPos, toPos, content, editedAt)
}

// FindPos finds the position of the given index in the tree.
func (t *Tree) FindPos(offset int) (*TreePos, error) {
	treePos, err := t.IndexTree.FindTreePos(offset)
	if err != nil {
		return nil, err
	}

	return &TreePos{
		CreatedAt: treePos.Node.Value.Pos.CreatedAt,
		Offset:    treePos.Node.Value.Pos.Offset + treePos.Offset,
	}, nil
}

// Edit edits the tree with the given range and content.
// If the content is undefined, the range will be removed.
func (t *Tree) Edit(from, to *TreePos, content *TreeNode, editedAt *time.Ticket) error {
	// 01. split text nodes at the given range if needed.
	toPos, toRight, err := t.findTreePosWithSplitText(to, editedAt)
	if err != nil {
		return err
	}
	fromPos, fromRight, err := t.findTreePosWithSplitText(from, editedAt)
	if err != nil {
		return err
	}

	toBeRemoveds := make([]*TreeNode, 0)
	// 02. remove the nodes and update linked list and index tree.
	if fromRight != toRight {
		if err := t.nodesBetween(fromRight, toRight, func(node *TreeNode) {
			if !node.IsRemoved() {
				toBeRemoveds = append(toBeRemoveds, node)
			}
		}); err != nil {
			return err
		}

		isRangeOnSameBranch := toPos.Node.IsAncestorOf(fromPos.Node)
		for _, node := range toBeRemoveds {
			node.remove(editedAt)

			if node.IsRemoved() {
				t.removedNodeMap[node.Pos.CreatedAt.StructureAsString()+":"+strconv.Itoa(node.Pos.Offset)] = node
			}
		}

		// move the alive children of the removed block node
		if isRangeOnSameBranch {
			var removedBlockNode *TreeNode
			if fromPos.Node.Parent.Value.IsRemoved() {
				removedBlockNode = fromPos.Node.Parent.Value
			} else if !fromPos.Node.IsText() && fromPos.Node.Value.IsRemoved() {
				removedBlockNode = fromPos.Node.Value
			}

			// If the nearest removed block node of the fromNode is found,
			// insert the alive children of the removed block node to the toNode.
			if removedBlockNode != nil {
				blockNode := toPos.Node
				offset, err := blockNode.FindBranchOffset(removedBlockNode.IndexTreeNode)
				if err != nil {
					return err
				}

				for i := len(removedBlockNode.IndexTreeNode.Children()) - 1; i >= 0; i-- {
					node := removedBlockNode.IndexTreeNode.Children()[i]
					if err := blockNode.InsertAt(node, offset); err != nil {
						return err
					}
				}
			}
		} else {
			if fromPos.Node.Parent != nil && fromPos.Node.Parent.Value.IsRemoved() {
				if err := toPos.Node.Parent.Prepend(fromPos.Node.Parent.Children()...); err != nil {
					return err
				}
			}
		}
	}

	// 03. insert the given node at the given position.
	if content != nil {
		// 03-1. insert the content nodes to the list.
		previous := fromRight.Prev
		index.TraverseNode(content.IndexTreeNode, func(node *index.Node[*TreeNode], depth int) {
			t.InsertAfter(previous, node.Value)
			previous = node.Value
		})

		// 03-2. insert the content nodes to the tree.
		if fromPos.Node.IsText() {
			if fromPos.Offset == 0 {
				if err := fromPos.Node.Parent.InsertBefore(content.IndexTreeNode, fromPos.Node); err != nil {
					return err
				}
			} else {
				if err := fromPos.Node.Parent.InsertAfter(content.IndexTreeNode, fromPos.Node); err != nil {
					return err
				}
			}
		} else {
			target := fromPos.Node
			if err := target.InsertAt(content.IndexTreeNode, fromPos.Offset+1); err != nil {
				return err
			}
		}
	}

	return nil
}

// StyleByIndex applies the given attributes of the given range.
// This method uses indexes instead of a pair of TreePos for testing.
func (t *Tree) StyleByIndex(start, end int, attributes map[string]string, editedAt *time.Ticket) error {
	fromPos, err := t.FindPos(start)
	if err != nil {
		return err
	}
	toPos, err := t.FindPos(end)
	if err != nil {
		return err
	}

	return t.Style(fromPos, toPos, attributes, editedAt)
}

// Style applies the given attributes of the given range.
func (t *Tree) Style(from, to *TreePos, attributes map[string]string, editedAt *time.Ticket) error {
	_, toRight, err := t.findTreePos(to, editedAt)
	if err != nil {
		return err
	}
	_, fromRight, err := t.findTreePos(from, editedAt)
	if err != nil {
		return err
	}

	// 02. style the nodes.
	return t.nodesBetween(fromRight, toRight, func(node *TreeNode) {
		if node.IsText() {
			return
		}

		for key, value := range attributes {
			if node.Attrs == nil {
				node.Attrs = NewRHT()
			}
			node.Attrs.Set(key, value, editedAt)
		}
	})
}

// findTreePos returns TreePos and the right node of the given index in postorder.
func (t *Tree) findTreePos(pos *TreePos, editedAt *time.Ticket) (*index.TreePos[*TreeNode], *TreeNode, error) {
	treePos := t.toTreePos(pos)
	if treePos == nil {
		return nil, nil, fmt.Errorf("%p: %w", pos, ErrNodeNotFound)
	}

	// Find the appropriate position. This logic is similar to the logical to
	// handle the same position insertion of RGA.
	current := treePos
	for current.Node.Value.Next != nil && current.Node.Value.Next.Pos.CreatedAt.After(editedAt) &&
		current.Node.Value.IndexTreeNode.Parent == current.Node.Value.Next.IndexTreeNode.Parent {

		current = &index.TreePos[*TreeNode]{
			Node:   current.Node.Value.Next.IndexTreeNode,
			Offset: current.Node.Value.Next.Len(),
		}
	}

	// TODO(hackerwins): Consider to use current instead of treePos.
	right, err := t.IndexTree.FindPostorderRight(treePos)
	if err != nil {
		return nil, nil, err
	}

	return current, right, nil
}

// findTreePosWithSplitText finds the right node of the given index in postorder.
func (t *Tree) findTreePosWithSplitText(pos *TreePos, editedAt *time.Ticket) (
	*index.TreePos[*TreeNode], *TreeNode, error,
) {
	treePos := t.toTreePos(pos)
	if treePos == nil {
		return nil, nil, fmt.Errorf("%p: %w", pos, ErrNodeNotFound)
	}

	// Find the appropriate position. This logic is similar to the logical to
	// handle the same position insertion of RGA.
	current := treePos
	for current.Node.Value.Next != nil && current.Node.Value.Next.Pos.CreatedAt.After(editedAt) &&
		current.Node.Value.IndexTreeNode.Parent == current.Node.Value.Next.IndexTreeNode.Parent {

		current = &index.TreePos[*TreeNode]{
			Node:   current.Node.Value.Next.IndexTreeNode,
			Offset: current.Node.Value.Next.Len(),
		}
	}

	if current.Node.IsText() {
		split, err := current.Node.Value.Split(current.Offset)
		if err != nil {
			return nil, nil, err
		}

		if split != nil {
			t.InsertAfter(current.Node.Value, split)
			split.InsPrev = current.Node.Value
		}
	}

	right, err := t.IndexTree.FindPostorderRight(treePos)
	if err != nil {
		return nil, nil, err
	}

	return current, right, nil
}

// toTreePos converts the given crdt.TreePos to index.TreePos<CRDTTreeNode>.
func (t *Tree) toTreePos(pos *TreePos) *index.TreePos[*TreeNode] {
	key, node := t.NodeMapByPos.Floor(pos)
	if node == nil || key.CreatedAt.Compare(pos.CreatedAt) != 0 {
		return nil
	}

	// Choose the left node if the position is on the boundary of the split nodes.
	if pos.Offset > 0 && pos.Offset == node.Pos.Offset && node.InsPrev != nil {
		node = node.InsPrev
	}

	return &index.TreePos[*TreeNode]{
		Node:   node.IndexTreeNode,
		Offset: pos.Offset - node.Pos.Offset,
	}
}

// toIndex converts the given CRDTTreePos to the index of the tree.
func (t *Tree) toIndex(pos *TreePos) (int, error) {
	treePos := t.toTreePos(pos)
	if treePos == nil {
		return -1, nil
	}

	idx, err := t.IndexTree.IndexOf(treePos.Node)
	if err != nil {
		return 0, err
	}

	return idx + treePos.Offset, nil
}

// nodesBetween returns the nodes between the given range.
// This method includes the given left node but excludes the given right node.
func (t *Tree) nodesBetween(left *TreeNode, right *TreeNode, callback func(*TreeNode)) error {
	current := left
	for current != right {
		if current == nil {
			return errors.New("left and right are not in the same list")
		}

		callback(current)
		current = current.Next
	}

	return nil
}

// Structure returns the structure of this tree.
func (t *Tree) Structure() TreeNodeForTest {
	return ToStructure(t.Root())
}

// PathToPos returns the position of the given path
func (t *Tree) PathToPos(path []int) (*TreePos, error) {
	treePos, err := t.IndexTree.PathToTreePos(path)
	if err != nil {
		return nil, err
	}

	return &TreePos{
		CreatedAt: treePos.Node.Value.Pos.CreatedAt,
		Offset:    treePos.Node.Value.Pos.Offset + treePos.Offset,
	}, nil
}

// ToStructure returns the JSON of this tree for debugging.
func ToStructure(node *TreeNode) TreeNodeForTest {
	if node.IsText() {
		currentNode := node
		return TreeNodeForTest{
			Type:      currentNode.Type(),
			Value:     currentNode.Value,
			Size:      currentNode.Len(),
			IsRemoved: currentNode.IsRemoved(),
		}
	}

	var children []TreeNodeForTest
	for _, child := range node.IndexTreeNode.Children() {
		children = append(children, ToStructure(child.Value))
	}

	return TreeNodeForTest{
		Type:      node.Type(),
		Children:  children,
		Size:      node.Len(),
		IsRemoved: node.IsRemoved(),
	}
}

// ToXML returns the XML representation of this tree.
func ToXML(node *TreeNode) string {
	return index.ToXML(node.IndexTreeNode)
}

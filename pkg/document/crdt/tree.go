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
	// DummyTreeNodeID is a dummy position of Tree. It is used to represent the head node of RGASplit.
	DummyTreeNodeID = &TreeNodeID{
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

	Pos       *TreeNodeID
	RemovedAt *time.Ticket

	Next    *TreeNode
	Prev    *TreeNode
	InsPrev *TreeNode
	InsNext *TreeNode

	Value string
	Attrs *RHT
}

// TreePos represents the position of Tree.
type TreePos struct {
	ParentID      *TreeNodeID
	LeftSiblingID *TreeNodeID
}

// NewTreePos creates a new instance of TreePos.
func NewTreePos(parentID *TreeNodeID, leftSiblingID *TreeNodeID) *TreePos {
	return &TreePos{
		ParentID:      parentID,
		LeftSiblingID: leftSiblingID,
	}
}

// Equals compares the given two CRDTTreePos.
func (t *TreePos) Equals(other *TreePos) bool {
	return (t.ParentID.CreatedAt.Compare(other.ParentID.CreatedAt) == 0 &&
		t.ParentID.Offset == other.ParentID.Offset &&
		t.LeftSiblingID.CreatedAt.Compare(other.LeftSiblingID.CreatedAt) == 0 &&
		t.LeftSiblingID.Offset == other.LeftSiblingID.Offset)
}

// TreeNodeID represent an ID of a node in the tree. It is used to
// identify a node in the tree. It is composed of the creation time of the node
// and the offset from the beginning of the node if the node is split.
//
// Some of replicas may have nodes that are not split yet. In this case, we can
// use `map.floorEntry()` to find the adjacent node.
type TreeNodeID struct {
	CreatedAt *time.Ticket
	Offset    int
}

// NewTreeNodeID creates a new instance of TreeNodeID.
func NewTreeNodeID(createdAt *time.Ticket, offset int) *TreeNodeID {
	return &TreeNodeID{
		CreatedAt: createdAt,
		Offset:    offset,
	}
}

// ToIDString returns a string that can be used as an ID for this TreeNodeID.
func (t *TreeNodeID) ToIDString() string { // TODO(sejongk): change this to be private
	return t.CreatedAt.StructureAsString() + ":" + strconv.Itoa(t.Offset)
}

// Compare compares the given two CRDTTreePos.
func (t *TreeNodeID) Compare(other llrb.Key) int {
	compare := t.CreatedAt.Compare(other.(*TreeNodeID).CreatedAt)
	if compare != 0 {
		return compare
	}

	if t.Offset > other.(*TreeNodeID).Offset {
		return 1
	} else if t.Offset < other.(*TreeNodeID).Offset {
		return -1
	}
	return 0
}

// NewTreeNode creates a new instance of TreeNode.
func NewTreeNode(pos *TreeNodeID, nodeType string, attributes *RHT, value ...string) *TreeNode {
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
	return n.IndexTreeNode.Type
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

// NextNode returns the next node of this TreeNode.
func (n *TreeNode) NextNode() *TreeNode {
	return n.Next
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
func (n *TreeNode) Split(offset, absOffset int) (*TreeNode, error) {
	if n.IsText() {
		return n.SplitText(offset, absOffset)
	}

	return n.SplitElement(offset)
}

// SplitText splits the text node at the given offset.
func (n *TreeNode) SplitText(offset, absOffset int) (*TreeNode, error) {
	if offset == 0 || offset == n.Len() {
		return nil, nil
	}

	encoded := utf16.Encode([]rune(n.Value))
	leftRune := utf16.Decode(encoded[0:offset])
	rightRune := utf16.Decode(encoded[offset:])

	if len(rightRune) == 0 {
		return nil, nil
	}

	n.Value = string(leftRune)
	n.IndexTreeNode.Length = len(leftRune)

	rightNode := NewTreeNode(&TreeNodeID{
		CreatedAt: n.Pos.CreatedAt,
		Offset:    offset + absOffset,
	}, n.Type(), nil, string(rightRune))
	rightNode.RemovedAt = n.RemovedAt

	if err := n.IndexTreeNode.Parent.InsertAfterInternal(
		rightNode.IndexTreeNode,
		n.IndexTreeNode,
	); err != nil {
		return nil, err
	}

	return rightNode, nil
}

// SplitElement splits the given element at the given offset.
func (n *TreeNode) SplitElement(offset int) (*TreeNode, error) {
	split := NewTreeNode(&TreeNodeID{
		CreatedAt: n.Pos.CreatedAt,
		Offset:    offset,
	}, n.Type(), nil)
	split.RemovedAt = n.RemovedAt

	err := n.IndexTreeNode.SetChildren(n.IndexTreeNode.Children(true)[0:offset])
	if err != nil {
		return nil, err
	}
	err = split.IndexTreeNode.SetChildren(n.IndexTreeNode.Children(true)[offset:])
	if err != nil {
		return nil, err
	}

	nodeLength, splitLength := 0, 0
	for _, child := range n.IndexTreeNode.Children() {
		nodeLength += child.Length
	}
	for _, child := range split.IndexTreeNode.Children() {
		splitLength += child.Length
	}

	n.IndexTreeNode.Length = nodeLength
	split.IndexTreeNode.Length = splitLength

	return split, nil
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
	IndexTree      *index.Tree[*TreeNode]
	NodeMapByPos   *llrb.Tree[*TreeNodeID, *TreeNode]
	removedNodeMap map[string]*TreeNode

	createdAt *time.Ticket
	movedAt   *time.Ticket
	removedAt *time.Ticket
}

// NewTree creates a new instance of Tree.
func NewTree(root *TreeNode, createdAt *time.Ticket) *Tree {
	tree := &Tree{
		IndexTree:      index.NewTree[*TreeNode](root.IndexTreeNode),
		NodeMapByPos:   llrb.NewTree[*TreeNodeID, *TreeNode](),
		removedNodeMap: make(map[string]*TreeNode),
		createdAt:      createdAt,
	}

	index.Traverse(tree.IndexTree, func(node *index.Node[*TreeNode], depth int) {
		tree.NodeMapByPos.Put(node.Value.Pos, node.Value)
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

	for node := range nodesToBeRemoved {
		if err := node.IndexTreeNode.Parent.RemoveChild(node.IndexTreeNode); err != nil {
			return 0, err
		}
		t.NodeMapByPos.Remove(node.Pos)
		t.Purge(node)
		delete(t.removedNodeMap, node.Pos.ToIDString())
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
func (t *Tree) EditByIndex(start, end int, contents []*TreeNode, editedAt *time.Ticket) error {
	fromPos, err := t.FindPos(start)
	if err != nil {
		return err
	}
	toPos, err := t.FindPos(end)
	if err != nil {
		return err
	}

	return t.Edit(fromPos, toPos, contents, editedAt)
}

// FindPos finds the position of the given index in the tree.
// (local) index -> (local) TreePos in indexTree -> (logical) TreePos in Tree
func (t *Tree) FindPos(offset int) (*TreePos, error) {
	treePos, err := t.IndexTree.FindTreePos(offset) // local TreePos
	if err != nil {
		return nil, err
	}

	node, offset := treePos.Node, treePos.Offset
	var leftSibling *TreeNode

	if node.IsText() {
		if node.Parent.Children(true)[0] == node && offset == 0 {
			leftSibling = node.Parent.Value
		} else {
			leftSibling = node.Value
			absOffset := node.Value.Pos.Offset
			split, err := node.Value.Split(offset, absOffset)
			if err != nil {
				return nil, err
			}

			if split != nil {
				split.InsPrev = node.Value
				t.NodeMapByPos.Put(split.Pos, split)

				if node.Value.InsNext != nil {
					node.Value.InsNext.InsPrev = split
					split.InsNext = node.Value.InsNext
				}
				node.Value.InsNext = split
			}
		}
		node = node.Parent
	} else {
		if offset == 0 {
			leftSibling = node.Value
		} else {
			leftSibling = node.Children()[offset-1].Value
		}
	}

	return &TreePos{
		ParentID: node.Value.Pos,
		LeftSiblingID: &TreeNodeID{
			CreatedAt: leftSibling.Pos.CreatedAt,
			Offset:    leftSibling.Pos.Offset + offset,
		},
	}, nil
}

// Edit edits the tree with the given range and content.
// If the content is undefined, the range will be removed.
func (t *Tree) Edit(from, to *TreePos, contents []*TreeNode, editedAt *time.Ticket) error {
	// 01. split text nodes at the given range if needed.
	fromParent, fromLeft, err := t.findTreeNodesWithSplitText(from, editedAt)
	if err != nil {
		return err
	}
	toParent, toLeft, err := t.findTreeNodesWithSplitText(to, editedAt)
	if err != nil {
		return err
	}

	// 02. remove the nodes and update linked list and index tree.
	toBeRemoveds := make([]*TreeNode, 0)

	if fromLeft != toLeft {
		var fromChildIndex int
		var parent *index.Node[*TreeNode]

		if fromParent == toParent {
			parent = fromParent
			fromChildIndex = parent.OffsetOfChild(fromLeft) + 1
		} else {
			parent = fromLeft
			fromChildIndex = 0
		}

		toChildIndex := parent.OffsetOfChild(toLeft)

		parentChildern := parent.Children(true)
		for i := fromChildIndex; i <= toChildIndex; i++ {
			node := parentChildern[i]

			if node.Value.Pos.CreatedAt.Lamport() ==
				editedAt.Lamport() &&
				node.Value.Pos.CreatedAt.ActorID().String() != editedAt.ActorID().String() {
				continue
			}

			// traverse the nodes including tombstones
			index.TraverseNode(node, func(node *index.Node[*TreeNode], depth int) {
				if !node.Value.IsRemoved() {
					toBeRemoveds = append(toBeRemoveds, node.Value)
				}
			})
		}

		for _, node := range toBeRemoveds {
			node.remove(editedAt)

			if node.IsRemoved() {
				t.removedNodeMap[node.Pos.ToIDString()] = node
			}
		}
	}

	// 03. insert the given node at the given position.
	if len(contents) != 0 {
		leftInChildren := fromLeft //tree

		for _, content := range contents {
			// 03-1. insert the content nodes to the tree.
			if leftInChildren == fromParent {
				// 03-1-1. when there's no leftSibling, then insert content into very front of parent's children List
				err := fromParent.InsertAt(content.IndexTreeNode, 0)
				if err != nil {
					return err
				}
			} else {
				// 03-1-2. insert after leftSibling
				err := fromParent.InsertAfter(content.IndexTreeNode, leftInChildren)
				if err != nil {
					return err
				}
			}

			leftInChildren = content.IndexTreeNode
			index.TraverseNode(content.IndexTreeNode, func(node *index.Node[*TreeNode], depth int) {
				// if insertion happens during concurrent editing and parent node has been removed,
				// make new nodes as tombstone immediately
				if fromParent.Value.IsRemoved() {
					node.Value.remove(editedAt)
					t.removedNodeMap[node.Value.Pos.ToIDString()] = node.Value
				}

				t.NodeMapByPos.Put(node.Value.Pos, node.Value)
			})
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
	// 01. split text nodes at the given range if needed.
	fromParent, fromLeft, err := t.findTreeNodesWithSplitText(from, editedAt)
	if err != nil {
		return err
	}
	toParent, toLeft, err := t.findTreeNodesWithSplitText(to, editedAt)
	if err != nil {
		return err
	}

	if fromLeft != toLeft {
		var fromChildIndex int
		var parent *index.Node[*TreeNode]

		if fromParent == toParent {
			parent = fromParent //TODO(sejongk): js sdk error for idx 0!! fromLeft.Parent would be nil
			fromChildIndex = fromParent.OffsetOfChild(fromLeft) + 1
		} else {
			parent = fromLeft
			fromChildIndex = 0
		}

		toChildIndex := toParent.OffsetOfChild(toLeft)

		// 02. style the nodes.
		parentChildern := parent.Children(true)
		for i := fromChildIndex; i <= toChildIndex; i++ {
			node := parentChildern[i]

			if !node.Value.IsRemoved() {
				if node.Value.Attrs == nil {
					node.Value.Attrs = NewRHT()
				}

				for key, value := range attributes {
					node.Value.Attrs.Set(key, value, editedAt)
				}
			}
		}
	}

	return nil
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

/**
 * TODO(sejongk): clarify the comments
 * findTreeNodesWithSplitText finds TreeNode of the given crdt.TreePos and
 * splits the text node if necessary.
 *
 * crdt.TreePos is a position in the CRDT perspective. This is different
 * from indexTree.TreePos which is a position of the tree in the local perspective.
**/
func (t *Tree) findTreeNodesWithSplitText(pos *TreePos, editedAt *time.Ticket) (
	*index.Node[*TreeNode], *index.Node[*TreeNode], error,
) {
	parentNode, leftSiblingNode := t.toTreeNodes(pos)
	if parentNode == nil || leftSiblingNode == nil {
		return nil, nil, fmt.Errorf("%p: %w", pos, ErrNodeNotFound)
	}

	// Find the appropriate position. This logic is similar to the logical to
	// handle the same position insertion of RGA.
	if leftSiblingNode.IsText() {
		absOffset := leftSiblingNode.Pos.Offset
		split, err := leftSiblingNode.Split(pos.LeftSiblingID.Offset-absOffset, absOffset)
		if err != nil {
			return nil, nil, err
		}

		if split != nil {
			split.InsPrev = leftSiblingNode
			t.NodeMapByPos.Put(split.Pos, split)

			if leftSiblingNode.InsNext != nil {
				leftSiblingNode.InsNext.InsPrev = split
				split.InsNext = leftSiblingNode.InsNext
			}
			leftSiblingNode.InsNext = split
		}
	}

	var index int
	if parentNode == leftSiblingNode {
		index = 0
	} else {
		index = parentNode.IndexTreeNode.OffsetOfChild(leftSiblingNode.IndexTreeNode) + 1
	}

	parentChildren := parentNode.IndexTreeNode.Children(true)
	for i := index; i < len(parentChildren); i++ {
		next := parentChildren[i].Value
		if !next.Pos.CreatedAt.After(editedAt) {
			break
		}
		leftSiblingNode = next
	}

	return parentNode.IndexTreeNode, leftSiblingNode.IndexTreeNode, nil
}

// toTreePos converts the given crdt.TreePos to local index.TreePos<CRDTTreeNode>.
func (t *Tree) toTreePos(pos *TreePos) *index.TreePos[*TreeNode] {
	if pos.ParentID == nil || pos.LeftSiblingID == nil {
		return nil
	}

	parentNode, leftSiblingNode := t.toTreeNodes(pos)
	if parentNode == nil || leftSiblingNode == nil {
		return nil
	}

	var treePos *index.TreePos[*TreeNode]

	if parentNode == leftSiblingNode {
		treePos = &index.TreePos[*TreeNode]{
			Node:   leftSiblingNode.IndexTreeNode,
			Offset: 0,
		}
	} else {
		leftSiblingOffset, err := parentNode.IndexTreeNode.FindOffset(leftSiblingNode.IndexTreeNode)
		if err != nil {
			return nil
		}

		offset := leftSiblingOffset + 1
		if leftSiblingNode.IsText() {
			offset, err = t.IndexTree.LeftSiblingsSize(parentNode.IndexTreeNode, offset)
			if err != nil {
				return nil // NOTE(sejongk): should return error instead?
			}
		}

		treePos = &index.TreePos[*TreeNode]{
			Node:   parentNode.IndexTreeNode,
			Offset: offset,
		}

	}

	return treePos
}

// toIndex converts the given CRDTTreePos to the index of the tree.
func (t *Tree) toIndex(pos *TreePos) (int, error) {
	treePos := t.toTreePos(pos)
	if treePos == nil {
		return -1, nil
	}

	idx, err := t.IndexTree.IndexOf(treePos)
	if err != nil {
		return 0, err
	}

	return idx, nil
}

func (t *Tree) toTreeNodes(pos *TreePos) (*TreeNode, *TreeNode) {
	parentKey, parentNode := t.NodeMapByPos.Floor(pos.ParentID)
	leftSiblingKey, leftSiblingNode := t.NodeMapByPos.Floor(pos.LeftSiblingID)

	if parentNode == nil ||
		leftSiblingNode == nil ||
		parentKey.CreatedAt.Compare(pos.ParentID.CreatedAt) != 0 ||
		leftSiblingKey.CreatedAt.Compare(pos.LeftSiblingID.CreatedAt) != 0 {
		return nil, nil
	}

	if pos.LeftSiblingID.Offset > 0 &&
		pos.LeftSiblingID.Offset == leftSiblingNode.Pos.Offset &&
		leftSiblingNode.InsPrev != nil {
		return parentNode, leftSiblingNode.InsPrev
	}

	return parentNode, leftSiblingNode
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
	index, err := t.IndexTree.PathToIndex(path)
	if err != nil {
		return nil, err
	}

	pos, err := t.FindPos(index)
	if err != nil {
		return nil, err
	}

	return pos, nil
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

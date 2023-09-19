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

	ID        *TreeNodeID
	RemovedAt *time.Ticket

	InsPrevID *TreeNodeID
	InsNextID *TreeNodeID

	// Value is optional. If the value is not empty, it means that the node is a
	// text node.
	Value string

	// Attrs is optional. If the value is not empty, it means that the node is a
	// element node.
	Attrs *RHT
}

// TreePos represents a position in the tree. It is used to determine the
// position of insertion, deletion, and style change.
type TreePos struct {
	// ParentID is the ID of the parent node.
	ParentID *TreeNodeID

	// LeftSiblingID is the ID of the left sibling node. If the node is the
	// parent, it means that the position is leftmost.
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
	return t.ParentID.CreatedAt.Compare(other.ParentID.CreatedAt) == 0 &&
		t.ParentID.Offset == other.ParentID.Offset &&
		t.LeftSiblingID.CreatedAt.Compare(other.LeftSiblingID.CreatedAt) == 0 &&
		t.LeftSiblingID.Offset == other.LeftSiblingID.Offset
}

// TreeNodeID represent an ID of a node in the tree. It is used to
// identify a node in the tree. It is composed of the creation time of the node
// and the offset from the beginning of the node if the node is split.
//
// Some replicas may have nodes that are not split yet. In this case, we can
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

// NewTreeNode creates a new instance of TreeNode.
func NewTreeNode(id *TreeNodeID, nodeType string, attributes *RHT, value ...string) *TreeNode {
	node := &TreeNode{ID: id}

	// NOTE(hackerwins): The value of TreeNode is optional. If the value is
	// empty, it means that the node is an element node.
	if len(value) > 0 {
		node.Value = value[0]
	}
	node.Attrs = attributes
	node.IndexTreeNode = index.NewNode(nodeType, node)

	return node
}

// toIDString returns a string that can be used as an ID for this TreeNodeID.
func (t *TreeNodeID) toIDString() string {
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
		CreatedAt: n.ID.CreatedAt,
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
		CreatedAt: n.ID.CreatedAt,
		Offset:    offset,
	}, n.Type(), nil)
	split.RemovedAt = n.RemovedAt

	if err := n.IndexTreeNode.SetChildren(n.IndexTreeNode.Children(true)[0:offset]); err != nil {
		return nil, err
	}

	if err := split.IndexTreeNode.SetChildren(n.IndexTreeNode.Children(true)[offset:]); err != nil {
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
func (n *TreeNode) remove(removedAt *time.Ticket) bool {
	justRemoved := n.RemovedAt == nil
	if n.RemovedAt == nil || n.RemovedAt.Compare(removedAt) > 0 {
		n.RemovedAt = removedAt
		if justRemoved {
			n.IndexTreeNode.UpdateAncestorsSize()
		}
		return true
	}

	return false
}

func (n *TreeNode) canDelete(removedAt *time.Ticket, latestCreatedAt *time.Ticket) bool {
	if !n.ID.CreatedAt.After(latestCreatedAt) &&
		(n.RemovedAt == nil || n.RemovedAt.Compare(removedAt) > 0) {
		return true
	}
	return false
}

// InsertAt inserts the given node at the given offset.
func (n *TreeNode) InsertAt(newNode *TreeNode, offset int) error {
	return n.IndexTreeNode.InsertAt(newNode.IndexTreeNode, offset)
}

// DeepCopy copies itself deeply.
func (n *TreeNode) DeepCopy() (*TreeNode, error) {
	var clone *TreeNode
	if n.Attrs != nil {
		clone = NewTreeNode(n.ID, n.Type(), n.Attrs.DeepCopy(), n.Value)
	} else {
		clone = NewTreeNode(n.ID, n.Type(), nil, n.Value)
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
	NodeMapByID    *llrb.Tree[*TreeNodeID, *TreeNode]
	removedNodeMap map[string]*TreeNode

	createdAt *time.Ticket
	movedAt   *time.Ticket
	removedAt *time.Ticket
}

// NewTree creates a new instance of Tree.
func NewTree(root *TreeNode, createdAt *time.Ticket) *Tree {
	tree := &Tree{
		IndexTree:      index.NewTree[*TreeNode](root.IndexTreeNode),
		NodeMapByID:    llrb.NewTree[*TreeNodeID, *TreeNode](),
		removedNodeMap: make(map[string]*TreeNode),
		createdAt:      createdAt,
	}

	index.Traverse(tree.IndexTree, func(node *index.Node[*TreeNode], depth int) {
		tree.NodeMapByID.Put(node.Value.ID, node.Value)
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
		if err := t.purgeNode(node); err != nil {
			return 0, err
		}
	}

	return count, nil
}

// purgeNode physically purges the given node.
func (t *Tree) purgeNode(node *TreeNode) error {
	if err := node.IndexTreeNode.Parent.RemoveChild(node.IndexTreeNode); err != nil {
		return err
	}
	t.NodeMapByID.Remove(node.ID)

	insPrevID := node.InsPrevID
	insNextID := node.InsNextID
	if insPrevID != nil {
		insPrev := t.findFloorNode(insPrevID)
		insPrev.InsNextID = insNextID
	}
	if insNextID != nil {
		insNext := t.findFloorNode(insNextID)
		insNext.InsPrevID = insPrevID
	}
	node.InsPrevID = nil
	node.InsNextID = nil

	delete(t.removedNodeMap, node.ID.toIDString())
	return nil
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
func (t *Tree) EditByIndex(start, end int,
	latestCreatedAtMapByActor map[string]*time.Ticket,
	contents []*TreeNode,
	editedAt *time.Ticket,
) (map[string]*time.Ticket, error) {
	fromPos, err := t.FindPos(start)
	if err != nil {
		return nil, err
	}
	toPos, err := t.FindPos(end)
	if err != nil {
		return nil, err
	}

	return t.Edit(fromPos, toPos, latestCreatedAtMapByActor, contents, editedAt)
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
		if node.Parent.Children(false)[0] == node && offset == 0 {
			leftSibling = node.Parent.Value
		} else {
			leftSibling = node.Value
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
		ParentID: node.Value.ID,
		LeftSiblingID: &TreeNodeID{
			CreatedAt: leftSibling.ID.CreatedAt,
			Offset:    leftSibling.ID.Offset + offset,
		},
	}, nil
}

// Edit edits the tree with the given range and content.
// If the content is undefined, the range will be removed.
func (t *Tree) Edit(from, to *TreePos,
	latestCreatedAtMapByActor map[string]*time.Ticket,
	contents []*TreeNode,
	editedAt *time.Ticket,
) (map[string]*time.Ticket, error) {
	// 01. split text nodes at the given range if needed.
	fromParent, fromLeft, err := t.FindTreeNodesWithSplitText(from, editedAt)
	if err != nil {
		return nil, err
	}
	toParent, toLeft, err := t.FindTreeNodesWithSplitText(to, editedAt)
	if err != nil {
		return nil, err
	}

	// 02. remove the nodes and update index tree.
	createdAtMapByActor := make(map[string]*time.Ticket)
	var toBeRemoved []*TreeNode

	err = t.traverseInPosRange(fromParent.Value, fromLeft.Value, toParent.Value, toLeft.Value,
		func(node *TreeNode, contain index.TagContained) {
			// If node is a element node and half-contained in the range,
			// it should not be removed.
			if !node.IsText() && contain != index.AllContained {
				return
			}

			actorIDHex := node.ID.CreatedAt.ActorIDHex()

			var latestCreatedAt *time.Ticket
			if latestCreatedAtMapByActor == nil {
				latestCreatedAt = time.MaxTicket
			} else {
				createdAt, ok := latestCreatedAtMapByActor[actorIDHex]
				if ok {
					latestCreatedAt = createdAt
				} else {
					latestCreatedAt = time.InitialTicket
				}
			}

			if node.canDelete(editedAt, latestCreatedAt) {
				latestCreatedAt = createdAtMapByActor[actorIDHex]
				createdAt := node.ID.CreatedAt
				if latestCreatedAt == nil || createdAt.After(latestCreatedAt) {
					createdAtMapByActor[actorIDHex] = createdAt
				}
				toBeRemoved = append(toBeRemoved, node)
			}

		})
	if err != nil {
		return nil, err
	}

	for _, node := range toBeRemoved {
		if node.remove(editedAt) {
			t.removedNodeMap[node.ID.toIDString()] = node
		}
	}

	// 03. insert the given node at the given position.
	if len(contents) != 0 {
		leftInChildren := fromLeft

		for _, content := range contents {
			// 03-1. insert the content nodes to the tree.
			if leftInChildren == fromParent {
				// 03-1-1. when there's no leftSibling, then insert content into very front of parent's children List
				err := fromParent.InsertAt(content.IndexTreeNode, 0)
				if err != nil {
					return nil, err
				}
			} else {
				// 03-1-2. insert after leftSibling
				err := fromParent.InsertAfter(content.IndexTreeNode, leftInChildren)
				if err != nil {
					return nil, err
				}
			}

			leftInChildren = content.IndexTreeNode
			index.TraverseNode(content.IndexTreeNode, func(node *index.Node[*TreeNode], depth int) {
				// if insertion happens during concurrent editing and parent node has been removed,
				// make new nodes as tombstone immediately
				if fromParent.Value.IsRemoved() {
					actorIDHex := node.Value.ID.CreatedAt.ActorIDHex()
					if node.Value.remove(editedAt) {
						latestCreatedAt := createdAtMapByActor[actorIDHex]
						createdAt := node.Value.ID.CreatedAt
						if latestCreatedAt == nil || createdAt.After(latestCreatedAt) {
							createdAtMapByActor[actorIDHex] = createdAt
						}
					}
					t.removedNodeMap[node.Value.ID.toIDString()] = node.Value
				}

				t.NodeMapByID.Put(node.Value.ID, node.Value)
			})
		}
	}
	return createdAtMapByActor, nil
}

func (t *Tree) traverseInPosRange(fromParent, fromLeft, toParent, toLeft *TreeNode,
	callback func(node *TreeNode, contain index.TagContained),
) error {
	fromIdx, err := t.ToIndex(fromParent, fromLeft)
	if err != nil {
		return err
	}
	toIdx, err := t.ToIndex(toParent, toLeft)
	if err != nil {
		return err
	}

	return t.IndexTree.NodesBetween(fromIdx, toIdx, callback)
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
	fromParent, fromLeft, err := t.FindTreeNodesWithSplitText(from, editedAt)
	if err != nil {
		return err
	}
	toParent, toLeft, err := t.FindTreeNodesWithSplitText(to, editedAt)
	if err != nil {
		return err
	}

	err = t.traverseInPosRange(fromParent.Value, fromLeft.Value, toParent.Value, toLeft.Value,
		func(node *TreeNode, contain index.TagContained) {
			if !node.IsRemoved() && !node.IsText() && len(attributes) > 0 {
				if node.Attrs == nil {
					node.Attrs = NewRHT()
				}

				for key, value := range attributes {
					node.Attrs.Set(key, value, editedAt)
				}
			}
		})
	if err != nil {
		return err
	}

	return nil
}

// FindTreeNodesWithSplitText finds TreeNode of the given crdt.TreePos and
// splits the text node if necessary.
// crdt.TreePos is a position in the CRDT perspective. This is different
// from indexTree.TreePos which is a position of the tree in the local perspective.
func (t *Tree) FindTreeNodesWithSplitText(pos *TreePos, editedAt *time.Ticket) (
	*index.Node[*TreeNode], *index.Node[*TreeNode], error,
) {
	parentNode, leftSiblingNode := t.toTreeNodes(pos)
	if parentNode == nil || leftSiblingNode == nil {
		return nil, nil, fmt.Errorf("%p: %w", pos, ErrNodeNotFound)
	}

	// Find the appropriate position. This logic is similar to the logical to
	// handle the same position insertion of RGA.
	if leftSiblingNode.IsText() {
		absOffset := leftSiblingNode.ID.Offset
		split, err := leftSiblingNode.Split(pos.LeftSiblingID.Offset-absOffset, absOffset)
		if err != nil {
			return nil, nil, err
		}

		if split != nil {
			split.InsPrevID = leftSiblingNode.ID
			t.NodeMapByID.Put(split.ID, split)

			if leftSiblingNode.InsNextID != nil {
				insNext := t.findFloorNode(leftSiblingNode.InsNextID)
				insNext.InsPrevID = split.ID
				split.InsNextID = leftSiblingNode.InsNextID
			}
			leftSiblingNode.InsNextID = split.ID
		}
	}

	idx := 0
	if parentNode != leftSiblingNode {
		idx = parentNode.IndexTreeNode.OffsetOfChild(leftSiblingNode.IndexTreeNode) + 1
	}

	parentChildren := parentNode.IndexTreeNode.Children(true)
	for i := idx; i < len(parentChildren); i++ {
		next := parentChildren[i].Value
		if !next.ID.CreatedAt.After(editedAt) {
			break
		}
		leftSiblingNode = next
	}

	return parentNode.IndexTreeNode, leftSiblingNode.IndexTreeNode, nil
}

// toTreePos converts the given crdt.TreePos to local index.TreePos<CRDTTreeNode>.
func (t *Tree) toTreePos(parentNode, leftSiblingNode *TreeNode) (*index.TreePos[*TreeNode], error) {
	if parentNode == nil || leftSiblingNode == nil {
		return nil, nil
	}

	var treePos *index.TreePos[*TreeNode]

	if parentNode.IsRemoved() {
		// If parentNode is removed, treePos is the position of its least alive ancestor.
		var childNode *TreeNode
		for parentNode.IsRemoved() {
			childNode = parentNode
			parentNode = childNode.IndexTreeNode.Parent.Value
		}

		childOffset, err := parentNode.IndexTreeNode.FindOffset(childNode.IndexTreeNode)
		if err != nil {
			return nil, nil
		}

		treePos = &index.TreePos[*TreeNode]{
			Node:   parentNode.IndexTreeNode,
			Offset: childOffset,
		}
	} else {
		if parentNode == leftSiblingNode {
			treePos = &index.TreePos[*TreeNode]{
				Node:   leftSiblingNode.IndexTreeNode,
				Offset: 0,
			}
		} else {
			// Find the closest existing leftSibling node.
			offset, err := parentNode.IndexTreeNode.FindOffset(leftSiblingNode.IndexTreeNode)
			if err != nil {
				return nil, nil
			}

			if !leftSiblingNode.IsRemoved() {
				if leftSiblingNode.IsText() {
					treePos = &index.TreePos[*TreeNode]{
						Node:   leftSiblingNode.IndexTreeNode,
						Offset: leftSiblingNode.IndexTreeNode.PaddedLength(),
					}
					return treePos, nil
				}
				offset++
			}

			treePos = &index.TreePos[*TreeNode]{
				Node:   parentNode.IndexTreeNode,
				Offset: offset,
			}

		}
	}

	return treePos, nil
}

// ToIndex converts the given CRDTTreePos to the index of the tree.
func (t *Tree) ToIndex(parentNode, leftSiblingNode *TreeNode) (int, error) {
	treePos, err := t.toTreePos(parentNode, leftSiblingNode)
	if treePos == nil {
		return -1, nil
	}

	if err != nil {
		return 0, err
	}

	idx, err := t.IndexTree.IndexOf(treePos)
	if err != nil {
		return 0, err
	}

	return idx, nil
}

// findFloorNode returns node from given id.
func (t *Tree) findFloorNode(id *TreeNodeID) *TreeNode {
	key, node := t.NodeMapByID.Floor(id)

	if node == nil || key.CreatedAt.Compare(id.CreatedAt) != 0 {
		return nil
	}

	return node
}

func (t *Tree) toTreeNodes(pos *TreePos) (*TreeNode, *TreeNode) {
	parentNode := t.findFloorNode(pos.ParentID)
	leftSiblingNode := t.findFloorNode(pos.LeftSiblingID)

	if parentNode == nil || leftSiblingNode == nil {
		return nil, nil
	}

	if pos.LeftSiblingID.Offset > 0 &&
		pos.LeftSiblingID.Offset == leftSiblingNode.ID.Offset &&
		leftSiblingNode.InsPrevID != nil {
		return parentNode, t.findFloorNode(leftSiblingNode.InsPrevID)
	}

	return parentNode, leftSiblingNode
}

// Structure returns the structure of this tree.
func (t *Tree) Structure() TreeNodeForTest {
	return ToStructure(t.Root())
}

// PathToPos returns the position of the given path
func (t *Tree) PathToPos(path []int) (*TreePos, error) {
	idx, err := t.IndexTree.PathToIndex(path)
	if err != nil {
		return nil, err
	}

	pos, err := t.FindPos(idx)
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

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
	"slices"
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
	Index *index.Node[*TreeNode]

	ID        *TreeNodeID
	RemovedAt *time.Ticket

	InsPrevID *TreeNodeID
	InsNextID *TreeNodeID

	// Value is optional. If the value is not empty, it means that the node is a
	// text node.
	Value string

	// Attrs is optional. If the value is not empty,
	//it means that the node is an element node.
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
	node.Index = index.NewNode(nodeType, node)

	return node
}

// toIDString returns a string that can be used as an ID for this TreeNodeID.
func (t *TreeNodeID) toIDString() string {
	return t.CreatedAt.ToTestString() + ":" + strconv.Itoa(t.Offset)
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

// Equals returns whether given ID is equal to this ID or not.
func (t *TreeNodeID) Equals(id *TreeNodeID) bool {
	return t.CreatedAt.Compare(id.CreatedAt) == 0 && t.Offset == id.Offset
}

// Type returns the type of the Node.
func (n *TreeNode) Type() string {
	return n.Index.Type
}

// Len returns the length of the Node.
func (n *TreeNode) Len() int {
	return n.Index.Len()
}

// IsText returns whether the Node is text or not.
func (n *TreeNode) IsText() bool {
	return n.Index.IsText()
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
		indexNodes[i] = newNode.Index
	}

	return n.Index.Append(indexNodes...)
}

// Prepend prepends the given node to the beginning of the children.
func (n *TreeNode) Prepend(newNodes ...*TreeNode) error {
	indexNodes := make([]*index.Node[*TreeNode], len(newNodes))
	for i, newNode := range newNodes {
		indexNodes[i] = newNode.Index
	}

	return n.Index.Prepend(indexNodes...)
}

// Child returns the child of the given offset.
func (n *TreeNode) Child(offset int) (*TreeNode, error) {
	child, err := n.Index.Child(offset)
	if err != nil {
		return nil, err
	}

	return child.Value, nil
}

// Split splits the node at the given offset.
func (n *TreeNode) Split(tree *Tree, offset int) error {
	var split *TreeNode
	var err error
	if n.IsText() {
		split, err = n.SplitText(offset, n.ID.Offset)
		if err != nil {
			return err
		}
	} else {
		split, err = n.SplitElement(offset, n.ID.Offset)
		if err != nil {
			return err
		}
	}

	if split != nil {
		split.InsPrevID = n.ID
		if n.InsNextID != nil {
			insNext := tree.findFloorNode(n.InsNextID)
			insNext.InsPrevID = split.ID
			split.InsNextID = n.InsNextID
		}
		n.InsNextID = split.ID
		tree.NodeMapByID.Put(split.ID, split)
	}

	return nil
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
	n.Index.Length = len(leftRune)

	rightNode := NewTreeNode(&TreeNodeID{
		CreatedAt: n.ID.CreatedAt,
		Offset:    offset + absOffset,
	}, n.Type(), nil, string(rightRune))
	rightNode.RemovedAt = n.RemovedAt

	if err := n.Index.Parent.InsertAfterInternal(
		rightNode.Index,
		n.Index,
	); err != nil {
		return nil, err
	}

	return rightNode, nil
}

// SplitElement splits the given element at the given offset.
func (n *TreeNode) SplitElement(offset, absOffset int) (*TreeNode, error) {
	// TODO(hackerwins): Define ID of split node for concurrent editing.
	// Text has fixed content and its split nodes could have limited offset
	// range. But element node could have arbitrary children and its split
	// nodes could have arbitrary offset range. So, id could be duplicated
	// and its order could be broken when concurrent editing happens.
	// Currently, we use the similar ID of split element with the split text.
	split := NewTreeNode(&TreeNodeID{CreatedAt: n.ID.CreatedAt, Offset: offset + absOffset}, n.Type(), nil)
	split.RemovedAt = n.RemovedAt
	if err := n.Index.Parent.InsertAfterInternal(split.Index, n.Index); err != nil {
		return nil, err
	}
	split.Index.UpdateAncestorsSize()

	leftChildren := n.Index.Children(true)[0:offset]
	rightChildren := n.Index.Children(true)[offset:]
	if err := n.Index.SetChildrenInternal(leftChildren); err != nil {
		return nil, err
	}
	if err := split.Index.SetChildrenInternal(rightChildren); err != nil {
		return nil, err
	}

	nodeLength := 0
	for _, child := range n.Index.Children(true) {
		nodeLength += child.PaddedLength()
	}
	n.Index.Length = nodeLength

	splitLength := 0
	for _, child := range split.Index.Children(true) {
		splitLength += child.PaddedLength()
	}
	split.Index.Length = splitLength

	return split, nil
}

// remove marks the node as removed.
func (n *TreeNode) remove(removedAt *time.Ticket) bool {
	justRemoved := n.RemovedAt == nil
	if n.RemovedAt == nil || n.RemovedAt.Compare(removedAt) > 0 {
		n.RemovedAt = removedAt
		if justRemoved {
			n.Index.UpdateAncestorsSize()
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
	return n.Index.InsertAt(newNode.Index, offset)
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
	for _, child := range n.Index.Children(true) {
		node, err := child.Value.DeepCopy()
		if err != nil {
			return nil, err
		}

		children = append(children, node.Index)
	}
	if err := clone.Index.SetChildren(children); err != nil {
		return nil, err
	}

	return clone, nil
}

// InsertAfter inserts the given node after the given leftSibling.
func (n *TreeNode) InsertAfter(content *TreeNode, children *TreeNode) error {
	return n.Index.InsertAfter(content.Index, children.Index)
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
		IndexTree:      index.NewTree[*TreeNode](root.Index),
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
	if err := node.Index.Parent.RemoveChild(node.Index); err != nil {
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
	for idx, child := range node.Index.Children() {
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

// EditT edits the given range with the given value.
// This method uses indexes instead of a pair of TreePos for testing.
func (t *Tree) EditT(
	start, end int,
	contents []*TreeNode,
	splitLevel int,
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

	return t.Edit(fromPos, toPos, contents, splitLevel, editedAt, nil)
}

// FindPos finds the position of the given index in the tree.
// (local) index -> (local) TreePos in indexTree -> (logical) TreePos in Tree
func (t *Tree) FindPos(offset int) (*TreePos, error) {
	treePos, err := t.IndexTree.FindTreePos(offset) // local TreePos
	if err != nil {
		return nil, err
	}

	node, offset := treePos.Node, treePos.Offset
	var leftNode *TreeNode

	if node.IsText() {
		if node.Parent.Children(false)[0] == node && offset == 0 {
			leftNode = node.Parent.Value
		} else {
			leftNode = node.Value
		}
		node = node.Parent
	} else {
		if offset == 0 {
			leftNode = node.Value
		} else {
			leftNode = node.Children()[offset-1].Value
		}
	}

	return &TreePos{
		ParentID: node.Value.ID,
		LeftSiblingID: &TreeNodeID{
			CreatedAt: leftNode.ID.CreatedAt,
			Offset:    leftNode.ID.Offset + offset,
		},
	}, nil
}

// Edit edits the tree with the given range and content.
// If the content is undefined, the range will be removed.
func (t *Tree) Edit(
	from, to *TreePos,
	contents []*TreeNode,
	splitLevel int,
	editedAt *time.Ticket,
	latestCreatedAtMapByActor map[string]*time.Ticket,
) (map[string]*time.Ticket, error) {
	// 01. find nodes from the given range and split nodes.
	fromParent, fromLeft, err := t.FindTreeNodesWithSplitText(from, editedAt)
	if err != nil {
		return nil, err
	}
	toParent, toLeft, err := t.FindTreeNodesWithSplitText(to, editedAt)
	if err != nil {
		return nil, err
	}

	toBeRemoveds, toBeMovedToFromParents, createdAtMapByActor, err := t.collectBetween(
		fromParent, fromLeft, toParent, toLeft,
		latestCreatedAtMapByActor, editedAt,
	)
	if err != nil {
		return nil, err
	}

	// 02. Delete: delete the nodes that are marked as removed.
	for _, node := range toBeRemoveds {
		if node.remove(editedAt) {
			t.removedNodeMap[node.ID.toIDString()] = node
		}
	}

	// 03. Merge: move the nodes that are marked as moved.
	for _, node := range toBeMovedToFromParents {
		if err := fromParent.Append(node); err != nil {
			return nil, err
		}
	}

	// 04. Split: split the element nodes for the given splitLevel.
	if err := t.split(fromParent, fromLeft, splitLevel); err != nil {
		return nil, err
	}

	// 05. Insert: insert the given node at the given position.
	if len(contents) != 0 {
		leftInChildren := fromLeft

		for _, content := range contents {
			// 05-1. insert the content nodes to the tree.
			if leftInChildren == fromParent {
				// 05-1-1. when there's no leftSibling, then insert content into very front of parent's children List
				err := fromParent.InsertAt(content, 0)
				if err != nil {
					return nil, err
				}
			} else {
				// 05-1-2. insert after leftSibling
				err := fromParent.InsertAfter(content, leftInChildren)
				if err != nil {
					return nil, err
				}
			}

			leftInChildren = content
			index.TraverseNode(content.Index, func(node *index.Node[*TreeNode], depth int) {
				// if insertion happens during concurrent editing and parent node has been removed,
				// make new nodes as tombstone immediately
				if fromParent.IsRemoved() {
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

// collectBetween collects nodes that are marked as removed or moved. It also
// returns the latestCreatedAtMapByActor that is used to determine whether the
// node can be deleted or not.
func (t *Tree) collectBetween(
	fromParent *TreeNode, fromLeft *TreeNode,
	toParent *TreeNode, toLeft *TreeNode,
	latestCreatedAtMapByActor map[string]*time.Ticket, editedAt *time.Ticket,
) ([]*TreeNode, []*TreeNode, map[string]*time.Ticket, error) {
	var toBeRemoveds []*TreeNode
	var toBeMovedToFromParents []*TreeNode
	createdAtMapByActor := make(map[string]*time.Ticket)
	if err := t.traverseInPosRange(
		fromParent, fromLeft,
		toParent, toLeft,
		func(node *TreeNode, contain index.TagContained) {
			// NOTE(hackerwins): If the node overlaps as a closing tag with the
			// range, then we need to keep it.
			if !node.IsText() && contain == index.ClosingContained {
				return
			}

			// NOTE(hackerwins): If the node overlaps as an opening tag with the
			// range then we need to move the remaining children to fromParent.
			if !node.IsText() && contain == index.OpeningContained {
				// TODO(hackerwins): Define more clearly merge-able rules
				// between two parents. For now, we only merge two parents are
				// both element nodes having text children.
				// e.g. <p>a|b</p><p>c|d</p> -> <p>a|d</p>
				// if !fromParent.Index.HasTextChild() ||
				// 	!toParent.Index.HasTextChild() {
				// 	return
				// }

				for _, child := range node.Index.Children() {
					if slices.Contains(toBeRemoveds, child.Value) {
						continue
					}

					toBeMovedToFromParents = append(toBeMovedToFromParents, child.Value)
				}
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
				toBeRemoveds = append(toBeRemoveds, node)
			}
		},
	); err != nil {
		return nil, nil, nil, err
	}

	return toBeRemoveds, toBeMovedToFromParents, createdAtMapByActor, nil
}

func (t *Tree) split(fromParent *TreeNode, fromLeft *TreeNode, splitLevel int) error {
	if splitLevel == 0 {
		return nil
	}

	splitCount := 0
	parent := fromParent
	left := fromLeft
	for splitCount < splitLevel {
		var err error
		offset := 0
		if left != parent {
			offset, err = parent.Index.FindOffset(left.Index)
			if err != nil {
				return err
			}

			offset++
		}
		if err := parent.Split(t, offset); err != nil {
			return err
		}
		left = parent
		parent = parent.Index.Parent.Value
		splitCount++
	}

	return nil
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

	err = t.traverseInPosRange(fromParent, fromLeft, toParent, toLeft,
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
// splits the text node if the position is in the middle of the text node.
// crdt.TreePos is a position in the CRDT perspective. This is different
// from indexTree.TreePos which is a position of the tree in physical
// perspective.
func (t *Tree) FindTreeNodesWithSplitText(pos *TreePos, editedAt *time.Ticket) (
	*TreeNode, *TreeNode, error,
) {
	// 01. Find the parent and left sibling nodes of the given position.
	parentNode, leftNode := t.toTreeNodes(pos)
	if parentNode == nil || leftNode == nil {
		return nil, nil, fmt.Errorf("%p: %w", pos, ErrNodeNotFound)
	}

	// 02. Split text node if the left node is text node.
	if leftNode.IsText() {
		err := leftNode.Split(t, pos.LeftSiblingID.Offset-leftNode.ID.Offset)
		if err != nil {
			return nil, nil, err
		}
	}

	// 03. Find the appropriate left node. If some nodes are inserted at the
	// same position concurrently, then we need to find the appropriate left
	// node. This is similar to RGA.
	idx := 0
	if parentNode != leftNode {
		idx = parentNode.Index.OffsetOfChild(leftNode.Index) + 1
	}

	parentChildren := parentNode.Index.Children(true)
	for i := idx; i < len(parentChildren); i++ {
		next := parentChildren[i].Value
		if !next.ID.CreatedAt.After(editedAt) {
			break
		}
		leftNode = next
	}

	return parentNode, leftNode, nil
}

// toTreePos converts the given crdt.TreePos to local index.TreePos<CRDTTreeNode>.
func (t *Tree) toTreePos(parentNode, leftNode *TreeNode) (*index.TreePos[*TreeNode], error) {
	if parentNode == nil || leftNode == nil {
		return nil, nil
	}

	if parentNode.IsRemoved() {
		// If parentNode is removed, treePos is the position of its least alive ancestor.
		var childNode *TreeNode
		for parentNode.IsRemoved() {
			childNode = parentNode
			parentNode = childNode.Index.Parent.Value
		}

		offset, err := parentNode.Index.FindOffset(childNode.Index)
		if err != nil {
			return nil, nil
		}

		return &index.TreePos[*TreeNode]{
			Node:   parentNode.Index,
			Offset: offset,
		}, nil
	}

	if parentNode == leftNode {
		return &index.TreePos[*TreeNode]{
			Node:   leftNode.Index,
			Offset: 0,
		}, nil
	}

	// Find the closest existing leftSibling node.
	offset, err := parentNode.Index.FindOffset(leftNode.Index)
	if err != nil {
		return nil, err
	}

	if !leftNode.IsRemoved() {
		if leftNode.IsText() {
			return &index.TreePos[*TreeNode]{
				Node:   leftNode.Index,
				Offset: leftNode.Index.PaddedLength(),
			}, nil
		}
		offset++
	}

	return &index.TreePos[*TreeNode]{
		Node:   parentNode.Index,
		Offset: offset,
	}, nil
}

// ToIndex converts the given CRDTTreePos to the index of the tree.
func (t *Tree) ToIndex(parentNode, leftSiblingNode *TreeNode) (int, error) {
	treePos, err := t.toTreePos(parentNode, leftSiblingNode)
	if err != nil {
		return 0, err
	}
	if treePos == nil {
		return -1, nil
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

// toTreeNodes converts the pos to parent and left sibling nodes.
// If the position points to the middle of a node, then the left sibling node
// is the node that contains the position. Otherwise, the left sibling node is
// the node that is the left of the position.
func (t *Tree) toTreeNodes(pos *TreePos) (*TreeNode, *TreeNode) {
	parentNode := t.findFloorNode(pos.ParentID)
	leftNode := t.findFloorNode(pos.LeftSiblingID)

	if parentNode == nil || leftNode == nil {
		return nil, nil
	}

	// NOTE(hackerwins): If the left node and the parent node are the same,
	// it means that the position is the left-most of the parent node.
	// We need to skip finding the left of the position.
	if !pos.LeftSiblingID.Equals(parentNode.ID) &&
		pos.LeftSiblingID.Offset > 0 &&
		pos.LeftSiblingID.Offset == leftNode.ID.Offset &&
		leftNode.InsPrevID != nil {
		return parentNode, t.findFloorNode(leftNode.InsPrevID)
	}

	return parentNode, leftNode
}

// ToTreeNodeForTest returns the JSON of this tree for debugging.
func (t *Tree) ToTreeNodeForTest() TreeNodeForTest {
	return ToTreeNodeForTest(t.Root())
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

// ToTreeNodeForTest returns the JSON of this tree for debugging.
func ToTreeNodeForTest(node *TreeNode) TreeNodeForTest {
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
	for _, child := range node.Index.Children() {
		children = append(children, ToTreeNodeForTest(child.Value))
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
	return index.ToXML(node.Index)
}

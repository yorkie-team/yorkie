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
	"sort"
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
	node := &TreeNode{id: id}

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

// TreeNode is a node of Tree.
type TreeNode struct {
	Index *index.Node[*TreeNode]

	id        *TreeNodeID
	removedAt *time.Ticket

	InsPrevID *TreeNodeID
	InsNextID *TreeNodeID

	// Value is optional. If the value is not empty, it means that the node is a
	// text node.
	Value string

	// Attrs is optional. If the value is not empty,
	//it means that the node is an element node.
	Attrs *RHT
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

// ID returns the ID of this Node.
func (n *TreeNode) ID() *TreeNodeID {
	return n.id
}

// IDString returns the IDString of this Node.
func (n *TreeNode) IDString() string {
	return n.id.toIDString()
}

// RemovedAt returns the removal time of this Node.
func (n *TreeNode) RemovedAt() *time.Ticket {
	return n.removedAt
}

// SetRemovedAt sets the removal time of this node.
func (n *TreeNode) SetRemovedAt(ticket *time.Ticket) {
	n.removedAt = ticket
}

// IsRemoved returns whether the Node is removed or not.
func (n *TreeNode) IsRemoved() bool {
	return n.removedAt != nil
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
	members := n.Attrs.Elements()

	size := len(members)

	// Extract and sort the keys
	keys := make([]string, 0, size)
	for k := range members {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	sb := strings.Builder{}
	for idx, k := range keys {
		if idx > 0 {
			sb.WriteString(" ")
		}
		value := members[k]
		sb.WriteString(fmt.Sprintf(`%s="%s"`, k, EscapeString(value)))
	}

	return " " + sb.String()
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

// Purge removes the given child from the children.
func (n *TreeNode) Purge(child GCChild) error {
	rhtNode := child.(*RHTNode)
	return n.Attrs.Purge(rhtNode)
}

// Split splits the node at the given offset.
func (n *TreeNode) Split(tree *Tree, offset int, issueTimeTicket func() *time.Ticket) error {
	var split *TreeNode
	var err error
	if n.IsText() {
		split, err = n.SplitText(offset, n.id.Offset)
		if err != nil {
			return err
		}
	} else {
		split, err = n.SplitElement(offset, issueTimeTicket)
		if err != nil {
			return err
		}
	}

	if split != nil {
		split.InsPrevID = n.id
		if n.InsNextID != nil {
			insNext := tree.findFloorNode(n.InsNextID)
			insNext.InsPrevID = split.id
			split.InsNextID = n.InsNextID
		}
		n.InsNextID = split.id
		tree.NodeMapByID.Put(split.id, split)
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
		CreatedAt: n.id.CreatedAt,
		Offset:    offset + absOffset,
	}, n.Type(), nil, string(rightRune))
	rightNode.removedAt = n.removedAt

	if err := n.Index.Parent.InsertAfterInternal(
		rightNode.Index,
		n.Index,
	); err != nil {
		return nil, err
	}

	return rightNode, nil
}

// SplitElement splits the given element at the given offset.
func (n *TreeNode) SplitElement(offset int, issueTimeTicket func() *time.Ticket) (*TreeNode, error) {
	// TODO(hackerwins): Define IDString of split node for concurrent editing.
	// Text has fixed content and its split nodes could have limited offset
	// range. But element node could have arbitrary children and its split
	// nodes could have arbitrary offset range. So, id could be duplicated
	// and its order could be broken when concurrent editing happens.
	// Currently, we use the similar IDString of split element with the split text.
	split := NewTreeNode(&TreeNodeID{CreatedAt: issueTimeTicket(), Offset: 0}, n.Type(), nil)
	split.removedAt = n.removedAt
	if err := n.Index.Parent.InsertAfterInternal(split.Index, n.Index); err != nil {
		return nil, err
	}
	split.Index.UpdateAncestorsSize()

	leftChildren := n.Index.Children(true)[0:offset]
	rightChildren := n.Index.Children(true)[offset:]
	if err := n.Index.SetChildren(leftChildren); err != nil {
		return nil, err
	}
	if err := split.Index.SetChildren(rightChildren); err != nil {
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
	justRemoved := n.removedAt == nil

	if n.removedAt == nil || n.removedAt.Compare(removedAt) > 0 {
		n.removedAt = removedAt
		if justRemoved {
			n.Index.UpdateAncestorsSize()
		}
		return justRemoved
	}

	return false
}

func (n *TreeNode) canDelete(removedAt *time.Ticket, maxCreatedAt *time.Ticket) bool {
	if !n.id.CreatedAt.After(maxCreatedAt) &&
		(n.removedAt == nil || n.removedAt.Compare(removedAt) > 0) {
		return true
	}
	return false
}

func (n *TreeNode) canStyle(editedAt *time.Ticket, maxCreatedAt *time.Ticket) bool {
	if n.IsText() {
		return false
	}

	return !n.id.CreatedAt.After(maxCreatedAt) &&
		(n.removedAt == nil || editedAt.After(n.removedAt))
}

// InsertAt inserts the given node at the given offset.
func (n *TreeNode) InsertAt(newNode *TreeNode, offset int) error {
	return n.Index.InsertAt(newNode.Index, offset)
}

// DeepCopy copies itself deeply.
func (n *TreeNode) DeepCopy() (*TreeNode, error) {
	var attrs *RHT
	if n.Attrs != nil {
		attrs = n.Attrs.DeepCopy()
	}

	clone := NewTreeNode(n.id, n.Type(), attrs, n.Value)
	clone.Index.Length = n.Index.Length
	clone.removedAt = n.removedAt
	clone.InsPrevID = n.InsPrevID
	clone.InsNextID = n.InsNextID

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

// SetAttr sets the given attribute of the element.
func (n *TreeNode) SetAttr(k string, v string, ticket *time.Ticket) *RHTNode {
	if n.Attrs == nil {
		n.Attrs = NewRHT()
	}

	return n.Attrs.Set(k, v, ticket)
}

// RemoveAttr removes the given attribute of the element.
func (n *TreeNode) RemoveAttr(k string, ticket *time.Ticket) []*RHTNode {
	if n.Attrs == nil {
		n.Attrs = NewRHT()
	}

	return n.Attrs.Remove(k, ticket)
}

// GCPairs returns the pairs of GC.
func (n *TreeNode) GCPairs() []GCPair {
	if n.Attrs == nil {
		return nil
	}

	var pairs []GCPair
	for _, node := range n.Attrs.Nodes() {
		if node.isRemoved {
			pairs = append(pairs, GCPair{
				Parent: n,
				Child:  node,
			})
		}
	}

	return pairs
}

// Tree represents the tree of CRDT. It has doubly linked list structure and
// index tree structure.
type Tree struct {
	IndexTree   *index.Tree[*TreeNode]
	NodeMapByID *llrb.Tree[*TreeNodeID, *TreeNode]

	createdAt *time.Ticket
	movedAt   *time.Ticket
	removedAt *time.Ticket
}

// NewTree creates a new instance of Tree.
func NewTree(root *TreeNode, createdAt *time.Ticket) *Tree {
	tree := &Tree{
		IndexTree:   index.NewTree[*TreeNode](root.Index),
		NodeMapByID: llrb.NewTree[*TreeNodeID, *TreeNode](),
		createdAt:   createdAt,
	}

	index.Traverse(tree.IndexTree, func(node *index.Node[*TreeNode], depth int) {
		tree.NodeMapByID.Put(node.Value.id, node.Value)
	})

	return tree
}

// Marshal returns the JSON encoding of this Tree.
func (t *Tree) Marshal() string {
	builder := &strings.Builder{}
	marshal(builder, t.Root())
	return builder.String()
}

// Purge physically purges the given node.
func (t *Tree) Purge(child GCChild) error {
	node := child.(*TreeNode)

	if err := node.Index.Parent.RemoveChild(node.Index); err != nil {
		return err
	}
	t.NodeMapByID.Remove(node.id)

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

	return nil
}

// marshal returns the JSON encoding of this Tree.
func marshal(builder *strings.Builder, node *TreeNode) {
	if node.IsText() {
		builder.WriteString(fmt.Sprintf(`{"type":"%s","value":"%s"}`, node.Type(), EscapeString(node.Value)))
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

// GCPairs returns the pairs of GC.
func (t *Tree) GCPairs() []GCPair {
	var pairs []GCPair

	for _, node := range t.Nodes() {
		if node.removedAt != nil {
			pairs = append(pairs, GCPair{
				Parent: t,
				Child:  node,
			})
		}

		for _, p := range node.GCPairs() {
			pairs = append(pairs, p)
		}
	}

	return pairs
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

// NodeLen returns the size of the LLRBTree.
func (t *Tree) NodeLen() int {
	return t.NodeMapByID.Len()
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
	issueTimeTicket func() *time.Ticket,
) error {
	fromPos, err := t.FindPos(start)
	if err != nil {
		return err
	}
	toPos, err := t.FindPos(end)
	if err != nil {
		return err
	}

	_, _, err = t.Edit(fromPos, toPos, contents, splitLevel, editedAt, issueTimeTicket, nil)
	return err
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
		ParentID: node.Value.id,
		LeftSiblingID: &TreeNodeID{
			CreatedAt: leftNode.id.CreatedAt,
			Offset:    leftNode.id.Offset + offset,
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
	issueTimeTicket func() *time.Ticket,
	maxCreatedAtMapByActor map[string]*time.Ticket,
) (map[string]*time.Ticket, []GCPair, error) {
	// 01. find nodes from the given range and split nodes.
	fromParent, fromLeft, err := t.FindTreeNodesWithSplitText(from, editedAt)
	if err != nil {
		return nil, nil, err
	}
	toParent, toLeft, err := t.FindTreeNodesWithSplitText(to, editedAt)
	if err != nil {
		return nil, nil, err
	}

	toBeRemoveds, toBeMovedToFromParents, maxCreatedAtMap, err := t.collectBetween(
		fromParent, fromLeft, toParent, toLeft,
		maxCreatedAtMapByActor, editedAt,
	)
	if err != nil {
		return nil, nil, err
	}

	// 02. Delete: delete the nodes that are marked as removed.
	var pairs []GCPair
	for _, node := range toBeRemoveds {
		if node.remove(editedAt) {
			pairs = append(pairs, GCPair{
				Parent: t,
				Child:  node,
			})
		}
	}

	// 03. Merge: move the nodes that are marked as moved.
	for _, node := range toBeMovedToFromParents {
		if node.removedAt == nil {
			if err := fromParent.Append(node); err != nil {
				return nil, nil, err
			}
		}
	}

	// 04. Split: split the element nodes for the given splitLevel.
	if err := t.split(fromParent, fromLeft, splitLevel, issueTimeTicket); err != nil {
		return nil, nil, err
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
					return nil, nil, err
				}
			} else {
				// 05-1-2. insert after leftSibling
				err := fromParent.InsertAfter(content, leftInChildren)
				if err != nil {
					return nil, nil, err
				}
			}

			leftInChildren = content
			index.TraverseNode(content.Index, func(node *index.Node[*TreeNode], depth int) {
				// if insertion happens during concurrent editing and parent node has been removed,
				// make new nodes as tombstone immediately
				if fromParent.IsRemoved() {
					actorIDHex := node.Value.id.CreatedAt.ActorIDHex()
					if node.Value.remove(editedAt) {
						maxCreatedAt := maxCreatedAtMap[actorIDHex]
						createdAt := node.Value.id.CreatedAt
						if maxCreatedAt == nil || createdAt.After(maxCreatedAt) {
							maxCreatedAtMap[actorIDHex] = createdAt
						}
					}

					pairs = append(pairs, GCPair{
						Parent: t,
						Child:  node.Value,
					})
				}

				t.NodeMapByID.Put(node.Value.id, node.Value)
			})
		}
	}

	return maxCreatedAtMap, pairs, nil
}

// collectBetween collects nodes that are marked as removed or moved. It also
// returns the maxCreatedAtMapByActor that is used to determine whether the
// node can be deleted or not.
func (t *Tree) collectBetween(
	fromParent *TreeNode, fromLeft *TreeNode,
	toParent *TreeNode, toLeft *TreeNode,
	maxCreatedAtMapByActor map[string]*time.Ticket, editedAt *time.Ticket,
) ([]*TreeNode, []*TreeNode, map[string]*time.Ticket, error) {
	var toBeRemoveds []*TreeNode
	var toBeMovedToFromParents []*TreeNode
	createdAtMapByActor := make(map[string]*time.Ticket)
	if err := t.traverseInPosRange(
		fromParent, fromLeft,
		toParent, toLeft,
		func(token index.TreeToken[*TreeNode], ended bool) {
			node, tokenType := token.Node, token.TokenType
			// NOTE(hackerwins): If the node overlaps as a start token with the
			// range then we need to move the remaining children to fromParent.
			if tokenType == index.Start && !ended {
				// TODO(hackerwins): Define more clearly merge-able rules
				// between two parents. For now, we only merge two parents are
				// both element nodes having text children.
				// e.g. <p>a|b</p><p>c|d</p> -> <p>a|d</p>
				// if !fromParent.Index.HasTextChild() ||
				// 	!toParent.Index.HasTextChild() {
				// 	return
				// }

				for _, child := range node.Index.Children() {
					toBeMovedToFromParents = append(toBeMovedToFromParents, child.Value)
				}
			}

			actorIDHex := node.id.CreatedAt.ActorIDHex()

			var maxCreatedAt *time.Ticket
			if maxCreatedAtMapByActor == nil {
				maxCreatedAt = time.MaxTicket
			} else {
				createdAt, ok := maxCreatedAtMapByActor[actorIDHex]
				if ok {
					maxCreatedAt = createdAt
				} else {
					maxCreatedAt = time.InitialTicket
				}
			}

			// NOTE(sejongk): If the node is removable or its parent is going to
			// be removed, then this node should be removed.
			if node.canDelete(editedAt, maxCreatedAt) || slices.Contains(toBeRemoveds, node.Index.Parent.Value) {
				maxCreatedAt = createdAtMapByActor[actorIDHex]
				createdAt := node.id.CreatedAt
				if maxCreatedAt == nil || createdAt.After(maxCreatedAt) {
					createdAtMapByActor[actorIDHex] = createdAt
				}
				// NOTE(hackerwins): If the node overlaps as an end token with the
				// range then we need to keep the node.
				if tokenType == index.Text || tokenType == index.Start {
					toBeRemoveds = append(toBeRemoveds, node)
				}
			}
		},
	); err != nil {
		return nil, nil, nil, err
	}

	return toBeRemoveds, toBeMovedToFromParents, createdAtMapByActor, nil
}

func (t *Tree) split(
	fromParent *TreeNode,
	fromLeft *TreeNode,
	splitLevel int,
	issueTimeTicket func() *time.Ticket,
) error {
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
		if err := parent.Split(t, offset, issueTimeTicket); err != nil {
			return err
		}
		left = parent
		parent = parent.Index.Parent.Value
		splitCount++
	}

	return nil
}

func (t *Tree) traverseInPosRange(fromParent, fromLeft, toParent, toLeft *TreeNode,
	callback func(token index.TreeToken[*TreeNode], ended bool),
) error {
	fromIdx, err := t.ToIndex(fromParent, fromLeft)
	if err != nil {
		return err
	}
	toIdx, err := t.ToIndex(toParent, toLeft)
	if err != nil {
		return err
	}

	return t.IndexTree.TokensBetween(fromIdx, toIdx, callback)
}

// StyleByIndex applies the given attributes of the given range.
// This method uses indexes instead of a pair of TreePos for testing.
func (t *Tree) StyleByIndex(
	start, end int,
	attributes map[string]string,
	editedAt *time.Ticket,
	maxCreatedAtMapByActor map[string]*time.Ticket,
) (map[string]*time.Ticket, []GCPair, error) {
	fromPos, err := t.FindPos(start)
	if err != nil {
		return nil, nil, err
	}

	toPos, err := t.FindPos(end)
	if err != nil {
		return nil, nil, err
	}

	return t.Style(fromPos, toPos, attributes, editedAt, maxCreatedAtMapByActor)
}

// Style applies the given attributes of the given range.
func (t *Tree) Style(
	from, to *TreePos,
	attrs map[string]string,
	editedAt *time.Ticket,
	maxCreatedAtMapByActor map[string]*time.Ticket,
) (map[string]*time.Ticket, []GCPair, error) {
	fromParent, fromLeft, err := t.FindTreeNodesWithSplitText(from, editedAt)
	if err != nil {
		return nil, nil, err
	}
	toParent, toLeft, err := t.FindTreeNodesWithSplitText(to, editedAt)
	if err != nil {
		return nil, nil, err
	}

	var pairs []GCPair
	createdAtMapByActor := make(map[string]*time.Ticket)
	if err = t.traverseInPosRange(fromParent, fromLeft, toParent, toLeft, func(token index.TreeToken[*TreeNode], _ bool) {
		node := token.Node
		actorIDHex := node.id.CreatedAt.ActorIDHex()

		var maxCreatedAt *time.Ticket
		if maxCreatedAtMapByActor == nil {
			maxCreatedAt = time.MaxTicket
		} else {
			if createdAt, ok := maxCreatedAtMapByActor[actorIDHex]; ok {
				maxCreatedAt = createdAt
			} else {
				maxCreatedAt = time.InitialTicket
			}
		}

		if node.canStyle(editedAt, maxCreatedAt) && len(attrs) > 0 {
			maxCreatedAt = createdAtMapByActor[actorIDHex]
			createdAt := node.id.CreatedAt
			if maxCreatedAt == nil || createdAt.After(maxCreatedAt) {
				createdAtMapByActor[actorIDHex] = createdAt
			}

			for key, value := range attrs {
				if rhtNode := node.SetAttr(key, value, editedAt); rhtNode != nil {
					pairs = append(pairs, GCPair{
						Parent: node,
						Child:  rhtNode,
					})
				}
			}
		}
	}); err != nil {
		return nil, nil, err
	}

	return createdAtMapByActor, pairs, nil
}

// RemoveStyle removes the given attributes of the given range.
func (t *Tree) RemoveStyle(
	from *TreePos,
	to *TreePos,
	attrs []string,
	editedAt *time.Ticket,
	maxCreatedAtMapByActor map[string]*time.Ticket,
) (map[string]*time.Ticket, []GCPair, error) {
	fromParent, fromLeft, err := t.FindTreeNodesWithSplitText(from, editedAt)
	if err != nil {
		return nil, nil, err
	}
	toParent, toLeft, err := t.FindTreeNodesWithSplitText(to, editedAt)
	if err != nil {
		return nil, nil, err
	}

	var pairs []GCPair
	createdAtMapByActor := make(map[string]*time.Ticket)
	if err = t.traverseInPosRange(fromParent, fromLeft, toParent, toLeft, func(token index.TreeToken[*TreeNode], _ bool) {
		node := token.Node
		actorIDHex := node.id.CreatedAt.ActorIDHex()

		var maxCreatedAt *time.Ticket
		if maxCreatedAtMapByActor == nil {
			maxCreatedAt = time.MaxTicket
		} else {
			if createdAt, ok := maxCreatedAtMapByActor[actorIDHex]; ok {
				maxCreatedAt = createdAt
			} else {
				maxCreatedAt = time.InitialTicket
			}
		}

		if node.canStyle(editedAt, maxCreatedAt) && len(attrs) > 0 {
			maxCreatedAt = createdAtMapByActor[actorIDHex]
			createdAt := node.id.CreatedAt
			if maxCreatedAt == nil || createdAt.After(maxCreatedAt) {
				createdAtMapByActor[actorIDHex] = createdAt
			}

			for _, attr := range attrs {
				rhtNodes := node.RemoveAttr(attr, editedAt)
				for _, rhtNode := range rhtNodes {
					pairs = append(pairs, GCPair{
						Parent: node,
						Child:  rhtNode,
					})
				}
			}
		}
	}); err != nil {
		return nil, nil, err
	}

	return createdAtMapByActor, pairs, nil
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
	parentNode, leftNode := t.ToTreeNodes(pos)
	if parentNode == nil || leftNode == nil {
		return nil, nil, fmt.Errorf("%p: %w", pos, ErrNodeNotFound)
	}

	// 02. Determine whether the position is left-most and the exact parent
	// in the current tree.
	isLeftMost := parentNode == leftNode
	realParentNode := parentNode
	if leftNode.Index.Parent != nil && !isLeftMost {
		realParentNode = leftNode.Index.Parent.Value
	}

	// 03. Split text node if the left node is text node.
	if leftNode.IsText() {
		err := leftNode.Split(t, pos.LeftSiblingID.Offset-leftNode.id.Offset, nil)
		if err != nil {
			return nil, nil, err
		}
	}

	// 04. Find the appropriate left node. If some nodes are inserted at the
	// same position concurrently, then we need to find the appropriate left
	// node. This is similar to RGA.
	idx := 0
	if !isLeftMost {
		idx = realParentNode.Index.OffsetOfChild(leftNode.Index) + 1
	}

	parentChildren := realParentNode.Index.Children(true)
	for i := idx; i < len(parentChildren); i++ {
		next := parentChildren[i].Value
		if !next.id.CreatedAt.After(editedAt) {
			break
		}
		leftNode = next
	}

	return realParentNode, leftNode, nil
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

// ToPath returns path from given CRDTTreePos
func (t *Tree) ToPath(parentNode, leftSiblingNode *TreeNode) ([]int, error) {
	treePos, err := t.toTreePos(parentNode, leftSiblingNode)
	if err != nil {
		return nil, err
	}
	if treePos == nil {
		return nil, nil
	}

	return t.IndexTree.TreePosToPath(treePos)
}

// findFloorNode returns node from given id.
func (t *Tree) findFloorNode(id *TreeNodeID) *TreeNode {
	key, node := t.NodeMapByID.Floor(id)

	if node == nil || key.CreatedAt.Compare(id.CreatedAt) != 0 {
		return nil
	}

	return node
}

// ToTreeNodes converts the pos to parent and left sibling nodes.
// If the position points to the middle of a node, then the left sibling node
// is the node that contains the position. Otherwise, the left sibling node is
// the node that is the left of the position.
func (t *Tree) ToTreeNodes(pos *TreePos) (*TreeNode, *TreeNode) {
	parentNode := t.findFloorNode(pos.ParentID)
	leftNode := t.findFloorNode(pos.LeftSiblingID)

	if parentNode == nil || leftNode == nil {
		return nil, nil
	}

	// NOTE(hackerwins): If the left node and the parent node are the same,
	// it means that the position is the left-most of the parent node.
	// We need to skip finding the left of the position.
	if !pos.LeftSiblingID.Equals(parentNode.id) &&
		pos.LeftSiblingID.Offset > 0 &&
		pos.LeftSiblingID.Offset == leftNode.id.Offset &&
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

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

package json

import (
	"errors"

	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/index"
)

const (
	// DefaultRootNodeType is the default type of root node.
	DefaultRootNodeType = "root"
)

var (
	// ErrEmptyTextNode is returned when there's empty string value in text node
	ErrEmptyTextNode = errors.New("text node cannot have empty value")
	// ErrMixedNodeType is returned when there're element node and text node inside contents array
	ErrMixedNodeType = errors.New("element node and text node cannot be passed together")
	// ErrIndexBoundary is returned when from index is bigger than to index
	ErrIndexBoundary = errors.New("from should be less than or equal to to")
	// ErrPathLenDiff is returned when two paths have different length
	ErrPathLenDiff = errors.New("both paths should have same length")
	// ErrEmptyPath is returned when there's empty path
	ErrEmptyPath = errors.New("path should not be empty")
)

// TreeNode is a node of Tree.
type TreeNode struct {
	// Type is the type of this node. It is used to distinguish between text
	// nodes and element nodes.
	Type string

	// Children is the children of this node. It is used to represent the
	// descendants of this node. If this node is a text node, it is nil.
	Children []TreeNode

	// Value is the value of text node. If this node is an element node, it is
	// empty string.
	Value string

	// Attributes is the attributes of this node.
	Attributes map[string]string
}

// Tree is a CRDT-based tree structure that is used to represent the document
// tree of text-based editor such as ProseMirror.
type Tree struct {
	initialRoot *TreeNode
	*crdt.Tree
	context *change.Context
}

// NewTree creates a new instance of Tree.
func NewTree(root ...*TreeNode) *Tree {
	var t Tree
	if len(root) > 0 {
		t.initialRoot = root[0]
	}
	return &t
}

// Initialize initializes the Tree by the given context and tree.
func (t *Tree) Initialize(ctx *change.Context, tree *crdt.Tree) *Tree {
	t.Tree = tree
	t.context = ctx
	return t
}

// validateTextNode make sure that text node have non-empty string value
func validateTextNode(treeNode TreeNode) error {
	if len(treeNode.Value) == 0 {
		return ErrEmptyTextNode
	}

	return nil
}

// validateTextNode make sure that treeNodes consist of only one of the two types (text or element)
func validateTreeNodes(treeNodes []*TreeNode) error {
	firstTreeNodeType := treeNodes[0].Type

	if firstTreeNodeType == index.DefaultTextType {
		for _, treeNode := range treeNodes {
			if treeNode.Type != index.DefaultTextType {
				return ErrMixedNodeType
			}

			if err := validateTextNode(*treeNode); err != nil {
				return err
			}
		}

	} else {
		for _, treeNode := range treeNodes {
			if treeNode.Type == index.DefaultTextType {
				return ErrMixedNodeType
			}
		}
	}

	return nil
}

// Edit edits this tree with the given node.
func (t *Tree) Edit(fromIdx, toIdx int, content *TreeNode, splitLevel int) bool {
	if fromIdx > toIdx {
		panic(ErrIndexBoundary)
	}

	fromPos, err := t.Tree.FindPos(fromIdx)
	if err != nil {
		panic(err)
	}
	toPos, err := t.Tree.FindPos(toIdx)
	if err != nil {
		panic(err)
	}

	return t.edit(fromPos, toPos, []*TreeNode{content}, splitLevel)
}

// EditBulk edits this tree with the given nodes.
func (t *Tree) EditBulk(fromIdx, toIdx int, contents []*TreeNode, splitLevel int) bool {
	if fromIdx > toIdx {
		panic(ErrIndexBoundary)
	}

	fromPos, err := t.Tree.FindPos(fromIdx)
	if err != nil {
		panic(err)
	}
	toPos, err := t.Tree.FindPos(toIdx)
	if err != nil {
		panic(err)
	}

	return t.edit(fromPos, toPos, contents, splitLevel)
}

// EditByPath edits this tree with the given path and nodes.
func (t *Tree) EditByPath(fromPath []int, toPath []int, content *TreeNode, splitLevel int) bool {
	if len(fromPath) != len(toPath) {
		panic(ErrPathLenDiff)
	}

	if len(fromPath) == 0 || len(toPath) == 0 {
		panic(ErrEmptyPath)
	}

	fromPos, err := t.Tree.PathToPos(fromPath)
	if err != nil {
		panic(err)
	}
	toPos, err := t.Tree.PathToPos(toPath)
	if err != nil {
		panic(err)
	}

	return t.edit(fromPos, toPos, []*TreeNode{content}, splitLevel)
}

// EditBulkByPath edits this tree with the given path and nodes.
func (t *Tree) EditBulkByPath(fromPath []int, toPath []int, contents []*TreeNode, splitLevel int) bool {
	if len(fromPath) != len(toPath) {
		panic(ErrPathLenDiff)
	}

	if len(fromPath) == 0 || len(toPath) == 0 {
		panic(ErrEmptyPath)
	}

	fromPos, err := t.Tree.PathToPos(fromPath)
	if err != nil {
		panic(err)
	}
	toPos, err := t.Tree.PathToPos(toPath)
	if err != nil {
		panic(err)
	}

	return t.edit(fromPos, toPos, contents, splitLevel)
}

// Style sets the attributes to the elements of the given range.
func (t *Tree) Style(fromIdx, toIdx int, attributes map[string]string) bool {
	if fromIdx > toIdx {
		panic("from should be less than or equal to to")
	}

	if len(attributes) == 0 {
		return true
	}

	fromPos, err := t.Tree.FindPos(fromIdx)
	if err != nil {
		panic(err)
	}
	toPos, err := t.Tree.FindPos(toIdx)
	if err != nil {
		panic(err)
	}

	ticket := t.context.IssueTimeTicket()
	maxCreationMapByActor, pairs, err := t.Tree.Style(fromPos, toPos, attributes, ticket, nil)
	if err != nil {
		panic(err)
	}

	for _, pair := range pairs {
		t.context.RegisterGCPair(pair)
	}

	t.context.Push(operations.NewTreeStyle(
		t.CreatedAt(),
		fromPos,
		toPos,
		maxCreationMapByActor,
		attributes,
		ticket,
	))

	return true
}

// RemoveStyle sets the attributes to the elements of the given range.
func (t *Tree) RemoveStyle(fromIdx, toIdx int, attributesToRemove []string) bool {
	if fromIdx > toIdx {
		panic("from should be less than or equal to to")
	}

	if len(attributesToRemove) == 0 {
		return true
	}

	fromPos, err := t.Tree.FindPos(fromIdx)
	if err != nil {
		panic(err)
	}
	toPos, err := t.Tree.FindPos(toIdx)
	if err != nil {
		panic(err)
	}

	ticket := t.context.IssueTimeTicket()
	maxCreationMapByActor, pairs, err := t.Tree.RemoveStyle(fromPos, toPos, attributesToRemove, ticket, nil)
	if err != nil {
		panic(err)
	}

	for _, pair := range pairs {
		t.context.RegisterGCPair(pair)
	}

	t.context.Push(operations.NewTreeStyleRemove(
		t.CreatedAt(),
		fromPos,
		toPos,
		maxCreationMapByActor,
		attributesToRemove,
		ticket,
	))

	return true
}

// Len returns the length of this tree.
func (t *Tree) Len() int {
	return t.IndexTree.Root().Len()
}

// edit edits the tree with the given nodes.
func (t *Tree) edit(fromPos, toPos *crdt.TreePos, contents []*TreeNode, splitLevel int) bool {
	ticket := t.context.IssueTimeTicket()

	var nodes []*crdt.TreeNode

	if len(contents) != 0 && contents[0] != nil {
		if err := validateTreeNodes(contents); err != nil {
			panic(err)
		}

		if contents[0].Type == index.DefaultTextType {
			value := ""

			for _, content := range contents {
				value += content.Value
			}

			nodes = append(nodes, crdt.NewTreeNode(crdt.NewTreeNodeID(ticket, 0), index.DefaultTextType, nil, value))
		} else {
			for _, content := range contents {
				var attributes *crdt.RHT
				if content.Attributes != nil {
					attributes = crdt.NewRHT()
					for key, val := range content.Attributes {
						attributes.Set(key, val, ticket)
					}
				}
				var node *crdt.TreeNode

				node = crdt.NewTreeNode(crdt.NewTreeNodeID(ticket, 0), content.Type, attributes, content.Value)

				for _, child := range content.Children {
					if err := buildDescendants(t.context, child, node); err != nil {
						panic(err)
					}
				}

				nodes = append(nodes, node)
			}
		}
	}

	var clones []*crdt.TreeNode
	if len(nodes) != 0 {
		for _, node := range nodes {
			var clone *crdt.TreeNode

			clone, err := node.DeepCopy()
			if err != nil {
				panic(err)
			}

			clones = append(clones, clone)
		}
	}

	ticket = t.context.LastTimeTicket()
	maxCreationMapByActor, pairs, err := t.Tree.Edit(
		fromPos,
		toPos,
		clones,
		splitLevel,
		ticket,
		t.context.IssueTimeTicket,
		nil,
	)
	if err != nil {
		panic(err)
	}

	for _, pair := range pairs {
		t.context.RegisterGCPair(pair)
	}

	t.context.Push(operations.NewTreeEdit(
		t.CreatedAt(),
		fromPos,
		toPos,
		nodes,
		splitLevel,
		maxCreationMapByActor,
		ticket,
	))

	return true
}

// buildRoot converts the given node to a CRDT-based tree node. If the given
// node is nil, it creates a default root node.
func buildRoot(ctx *change.Context, node *TreeNode, createdAt *time.Ticket) *crdt.TreeNode {
	if node == nil {
		return crdt.NewTreeNode(crdt.NewTreeNodeID(createdAt, 0), DefaultRootNodeType, nil)
	}

	root := crdt.NewTreeNode(crdt.NewTreeNodeID(createdAt, 0), node.Type, nil)
	for _, child := range node.Children {
		if err := buildDescendants(ctx, child, root); err != nil {
			panic(err)
		}
	}

	return root
}

// buildDescendants converts the given node to a CRDT-based tree node.
func buildDescendants(ctx *change.Context, n TreeNode, parent *crdt.TreeNode) error {
	if n.Type == index.DefaultTextType {

		if err := validateTextNode(n); err != nil {
			return err
		}

		treeNode := crdt.NewTreeNode(crdt.NewTreeNodeID(ctx.IssueTimeTicket(), 0), n.Type, nil, n.Value)
		return parent.Append(treeNode)
	}

	ticket := ctx.IssueTimeTicket()

	var attributes *crdt.RHT
	if n.Attributes != nil {
		attributes = crdt.NewRHT()
		for key, val := range n.Attributes {
			attributes.Set(key, val, ticket)
		}
	}

	treeNode := crdt.NewTreeNode(crdt.NewTreeNodeID(ticket, 0), n.Type, attributes)
	if err := parent.Append(treeNode); err != nil {
		return err
	}

	for _, child := range n.Children {
		if err := buildDescendants(ctx, child, treeNode); err != nil {
			return err
		}
	}

	return nil
}

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
}

// Tree is a CRDT-based tree structure that is used to represent the document
// tree of text-based editor such as ProseMirror.
type Tree struct {
	*crdt.Tree
	context *change.Context
}

// NewTree creates a new instance of Tree.
func NewTree(ctx *change.Context, tree *crdt.Tree) *Tree {
	return &Tree{
		Tree:    tree,
		context: ctx,
	}
}

// Edit edits this tree with the given node.
func (t *Tree) Edit(fromIdx, toIdx int, content *TreeNode) bool {
	if fromIdx > toIdx {
		panic("from should be less than or equal to to")
	}

	ticket := t.context.IssueTimeTicket()
	var node *crdt.TreeNode
	if content != nil {
		node = crdt.NewTreeNode(crdt.NewTreePos(ticket, 0), content.Type, content.Value)
		for _, child := range content.Children {
			buildDescendants(t.context, child, node)
		}
	}

	fromPos := t.Tree.FindPos(fromIdx)
	toPos := t.Tree.FindPos(toIdx)
	var clone *crdt.TreeNode
	if node != nil {
		clone = node.DeepCopy()
	}
	t.Tree.Edit(fromPos, toPos, clone, ticket)

	t.context.Push(operations.NewTreeEdit(
		t.CreatedAt(),
		fromPos,
		toPos,
		node,
		ticket,
	))

	return true
}

// Len returns the length of this tree.
func (t *Tree) Len() int {
	return t.IndexTree.Root().Len()
}

// EditByPath edits this tree with the given path and node.
func (t *Tree) EditByPath(fromPath []int, toPath []int, content *TreeNode) bool {
	ticket := t.context.IssueTimeTicket()
	var node *crdt.TreeNode
	if content != nil {
		node = crdt.NewTreeNode(crdt.NewTreePos(ticket, 0), content.Type, content.Value)
	}

	fromPos := t.Tree.PathToPos(fromPath)
	toPos := t.Tree.PathToPos(toPath)
	t.Tree.Edit(fromPos, toPos, node, ticket)

	return true
}

// buildRoot converts the given node to a CRDT-based tree node. If the given
// node is nil, it creates a default root node.
func buildRoot(ctx *change.Context, node *TreeNode, createdAt *time.Ticket) *crdt.TreeNode {
	if node == nil {
		return crdt.NewTreeNode(crdt.NewTreePos(createdAt, 0), DefaultRootNodeType)
	}

	root := crdt.NewTreeNode(crdt.NewTreePos(createdAt, 0), node.Type)
	for _, child := range node.Children {
		buildDescendants(ctx, child, root)
	}

	return root
}

// buildDescendants converts the given node to a CRDT-based tree node.
func buildDescendants(ctx *change.Context, n TreeNode, parent *crdt.TreeNode) {
	if n.Type == index.DefaultTextType {
		treeNode := crdt.NewTreeNode(crdt.NewTreePos(ctx.IssueTimeTicket(), 0), n.Type, n.Value)
		parent.Append(treeNode)
		return
	}

	treeNode := crdt.NewTreeNode(crdt.NewTreePos(ctx.IssueTimeTicket(), 0), n.Type)
	parent.Append(treeNode)

	for _, child := range n.Children {
		buildDescendants(ctx, child, treeNode)
	}
}

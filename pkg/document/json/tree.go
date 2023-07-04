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

	// Attributes is the attributes of this node.
	Attributes map[string]string
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
		var attributes *crdt.RHT
		if content.Attributes != nil {
			attributes = crdt.NewRHT()
			for key, val := range content.Attributes {
				attributes.Set(key, val, ticket)
			}
		}
		node = crdt.NewTreeNode(crdt.NewTreePos(ticket, 0), content.Type, attributes, content.Value)
		for _, child := range content.Children {
			if err := buildDescendants(t.context, child, node); err != nil {
				panic(err)
			}
		}
	}

	fromPos, err := t.Tree.FindPos(fromIdx)
	if err != nil {
		panic(err)
	}
	toPos, err := t.Tree.FindPos(toIdx)
	if err != nil {
		panic(err)
	}

	var clone *crdt.TreeNode
	if node != nil {
		clone, err = node.DeepCopy()
		if err != nil {
			panic(err)
		}
	}

	ticket = t.context.LastTimeTicket()
	if err = t.Tree.Edit(fromPos, toPos, clone, ticket); err != nil {
		panic(err)
	}

	t.context.Push(operations.NewTreeEdit(
		t.CreatedAt(),
		fromPos,
		toPos,
		node,
		ticket,
	))

	if fromPos.CreatedAt.Compare(toPos.CreatedAt) != 0 || fromPos.Offset != toPos.Offset {
		t.context.RegisterElementHasRemovedNodes(t.Tree)
	}

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
		var attributes *crdt.RHT
		if content.Attributes != nil {
			attributes = crdt.NewRHT()
			for key, val := range content.Attributes {
				attributes.Set(key, val, ticket)
			}
		}
		node = crdt.NewTreeNode(crdt.NewTreePos(ticket, 0), content.Type, attributes, content.Value)
		for _, child := range content.Children {
			if err := buildDescendants(t.context, child, node); err != nil {
				panic(err)
			}
		}
	}

	fromPos, err := t.Tree.PathToPos(fromPath)
	if err != nil {
		panic(err)
	}
	toPos, err := t.Tree.PathToPos(toPath)
	if err != nil {
		panic(err)
	}

	var clone *crdt.TreeNode
	if node != nil {
		clone, err = node.DeepCopy()
		if err != nil {
			panic(err)
		}
	}

	ticket = t.context.LastTimeTicket()
	if err = t.Tree.Edit(fromPos, toPos, clone, ticket); err != nil {
		panic(err)
	}

	t.context.Push(operations.NewTreeEdit(
		t.CreatedAt(),
		fromPos,
		toPos,
		node,
		ticket,
	))

	if fromPos.CreatedAt.Compare(toPos.CreatedAt) != 0 || fromPos.Offset != toPos.Offset {
		t.context.RegisterElementHasRemovedNodes(t.Tree)
	}

	return true
}

// Style sets the attributes to the elements of the given range.
func (t *Tree) Style(fromIdx, toIdx int, attributes map[string]string) bool {
	if fromIdx > toIdx {
		panic("from should be less than or equal to to")
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
	if err := t.Tree.Style(fromPos, toPos, attributes, ticket); err != nil {
		panic(err)
	}

	t.context.Push(operations.NewTreeStyle(
		t.CreatedAt(),
		fromPos,
		toPos,
		attributes,
		ticket,
	))

	return true
}

// buildRoot converts the given node to a CRDT-based tree node. If the given
// node is nil, it creates a default root node.
func buildRoot(ctx *change.Context, node *TreeNode, createdAt *time.Ticket) *crdt.TreeNode {
	if node == nil {
		return crdt.NewTreeNode(crdt.NewTreePos(createdAt, 0), DefaultRootNodeType, nil)
	}

	root := crdt.NewTreeNode(crdt.NewTreePos(createdAt, 0), node.Type, nil)
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
		treeNode := crdt.NewTreeNode(crdt.NewTreePos(ctx.IssueTimeTicket(), 0), n.Type, nil, n.Value)
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

	treeNode := crdt.NewTreeNode(crdt.NewTreePos(ticket, 0), n.Type, attributes)
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

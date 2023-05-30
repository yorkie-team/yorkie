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
)

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
func (t *Tree) Edit(fromIdx, toIdx int, content *crdt.JSONTreeNode) bool {
	if fromIdx > toIdx {
		panic("from should be less than or equal to to")
	}

	ticket := t.context.IssueTimeTicket()
	var crdtNode *crdt.TreeNode
	if content != nil && content.Type == "text" {
		crdtNode = crdt.NewTreeNode(crdt.NewTreePos(ticket, 0), "text", content.Value)
	} else if content != nil {
		crdtNode = crdt.NewTreeNode(crdt.NewTreePos(ticket, 0), content.Type)
	}

	fromPos := t.Tree.FindPos(fromIdx)
	toPos := t.Tree.FindPos(toIdx)
	t.Tree.Edit(fromPos, toPos, crdtNode, ticket)

	return true
}

// Len returns the length of this tree.
func (t *Tree) Len() int {
	return t.IndexTree.Root().Len()
}

// BuildRoot returns the root node of this tree.
func BuildRoot(ctx *change.Context, node *crdt.JSONTreeNode) *crdt.TreeNode {
	if node == nil {
		return crdt.NewTreeNode(crdt.NewTreePos(ctx.IssueTimeTicket(), 0), "root")
	}

	root := crdt.NewTreeNode(crdt.NewTreePos(ctx.IssueTimeTicket(), 0), node.Type)

	for _, child := range node.Children {
		traverse(ctx, child, root)
	}

	return root
}

func traverse(ctx *change.Context, n crdt.JSONTreeNode, parent *crdt.TreeNode) {
	if n.Type == "text" {
		treeNode := crdt.NewTreeNode(crdt.NewTreePos(ctx.IssueTimeTicket(), 0), n.Type, n.Value)
		parent.Append(treeNode)
		return
	}

	treeNode := crdt.NewTreeNode(crdt.NewTreePos(ctx.IssueTimeTicket(), 0), n.Type)
	parent.Append(treeNode)

	for _, child := range n.Children {
		traverse(ctx, child, treeNode)
	}
}

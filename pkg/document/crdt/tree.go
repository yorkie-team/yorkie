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

	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/index"
)

const (
	// blockNodePaddingLength is the padding length of BlockNode.
	blockNodePaddingLength = 2
)

// TreeNode is a node of Tree.
type TreeNode struct {
	Pos       *TreePos
	RemovedAt *time.Ticket

	Value    string
	Parent   *TreeNode
	Children []*TreeNode

	Type   string
	length int
}

// TreePos represents the position of Tree.
type TreePos struct {
	CreatedAt *time.Ticket
	Offset    int
}

// NewTreeNode creates a new instance of TreeNode.
func NewTreeNode(pos *TreePos, nodeType string, value ...string) *TreeNode {
	n := &TreeNode{
		Pos:  pos,
		Type: nodeType,
	}

	if len(value) > 0 {
		n.Value = value[0]
		n.length = len(value[0])
	}

	return n
}

// Len returns the length of the Node.
func (n *TreeNode) Len() int {
	// TODO(hackerwins, krapie): Move to the index package.
	return n.length
}

// IsInline returns whether the Node is inline or not.
func (n *TreeNode) IsInline() bool {
	return n.Type == "text"
}

// IsRemoved returns whether the Node is removed or not.
func (n *TreeNode) IsRemoved() bool {
	return n.RemovedAt != nil
}

// Append appends the given node to the end of the children.
func (n *TreeNode) Append(newNodes ...*TreeNode) error {
	if n.IsInline() {
		panic(errors.New("inline node cannot have children"))
	}

	n.Children = append(n.Children, newNodes...)
	for _, newNode := range newNodes {
		newNode.Parent = n
		newNode.UpdateAncestorsSize()
	}

	return nil
}

// UpdateAncestorsSize updates the size of ancestors.
func (n *TreeNode) UpdateAncestorsSize() {
	parent := n.Parent
	sign := 1
	if n.IsRemoved() {
		sign = -1
	}

	for parent != nil {
		parent.length += n.PaddedLength() * sign

		parent = parent.Parent
	}
}

// PaddedLength returns the length of the node with padding.
func (n *TreeNode) PaddedLength() int {
	length := n.length
	if !n.IsInline() {
		length += blockNodePaddingLength
	}

	return length
}

// Tree is a tree implementation of CRDT.
type Tree struct {
	indexTree *index.Tree
	createdAt *time.Ticket
}

// NewTree creates a new instance of Tree.
func NewTree(indexTree *index.Tree, createdAt *time.Ticket) *Tree {
	return &Tree{
		indexTree: indexTree,
		createdAt: createdAt,
	}
}

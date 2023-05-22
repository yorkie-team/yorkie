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

// Package index provides an index tree structure to represent a document of
// text-base editor.
package index

/**
 * About `index`, `size` and `TreePos` in index.Tree.
 *
 * `index` of index.Tree represents a position of a node in the tree.
 * `size` is used to calculate the index of nodes in the tree.
 * `index` in index.Tree inspired by ProseMirror's index.
 *
 * For example, empty paragraph's size is 0 and index 0 is the position of the:
 *    0
 * <p> </p>,                                p.size = 0
 *
 * If a paragraph has <i>, its size becomes 2 and there are 3 indexes:
 *     0   1    2
 *  <p> <i> </i> </p>                       p.size = 2, i.size = 0
 *
 * If the paragraph has <i> and <b>, its size becomes 4:
 *     0   1    2   3   4
 *  <p> <i> </i> <b> </b> </p>              p.size = 4, i.size = 0, b.size = 0
 *     0   1    2   3    4    5   6
 *  <p> <i> </i> <b> </b> <s> </s> </p>     p.size = 6, i.size = 0, b.size = 0, s.size = 0
 *
 * If a paragraph has text, its size becomes length of the characters:
 *     0 1 2 3
 *  <p> A B C </p>                          p.size = 3,   text.size = 3
 *
 * So the size of a node is the sum of the size and type of its children:
 *  `size = children(block type).length * 2 + children.reduce((child, acc) => child.size + acc, 0)`
 *
 * `TreePos` is also used to represent the position in the tree. It contains node and offset.
 * `TreePos` can be converted to `index` and vice versa.
 *
 * For example, if a paragraph has <i>, there are 3 indexes:
 *     0   1    2
 *  <p> <i> </i> </p>                       p.size = 2, i.size = 0
 *
 * In this case, index of TreePos(p, 0) is 0, index of TreePos(p, 1) is 2.
 * Index 1 can be converted to TreePos(i, 0).
 */

// NodeType is the type of Node.
type NodeType string

const (
	// InlineNode is an inline node. It cannot have children.
	// For example, text, bold, italic, underline, strikethrough, link, etc.
	InlineNode NodeType = "InlineNode"

	// BlockNode is a block node. It can have children.
	// For example, paragraph, list, heading, etc.
	BlockNode NodeType = "BlockNode"
)

// Node is a node of Tree.
type Node struct {
	// Type is the type of the node.
	Type NodeType
}

// Tree is a tree implementation to represent a document of text-based editors.
type Tree struct {
	Root *Node
}

// NewTree creates a new instance of Tree.
func NewTree(root *Node) *Tree {
	return &Tree{
		Root: root,
	}
}

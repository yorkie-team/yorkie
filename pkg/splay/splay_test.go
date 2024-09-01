/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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

package splay_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/splay"
)

type stringValue struct {
	content string
	removed bool
}

func newSplayNode(content string) *splay.Node[*stringValue] {
	return splay.NewNode(&stringValue{
		content: content,
	})
}

func (v *stringValue) Len() int {
	if v.removed {
		return 0
	}
	return len(v.content)
}

func (v *stringValue) String() string {
	return v.content
}

func TestSplayTree(t *testing.T) {
	t.Run("insert and splay test", func(t *testing.T) {
		tree := splay.NewTree[*stringValue](nil)

		node, idx, err := tree.Find(0)
		assert.Nil(t, node)
		assert.NoError(t, err)
		assert.Equal(t, 0, idx)

		nodeA := tree.Insert(newSplayNode("A2"))
		assert.Equal(t, "[2,2,1]A2", tree.ToTestString())
		nodeB := tree.Insert(newSplayNode("B23"))
		assert.Equal(t, "[2,2,1]A2[5,3,2]B23", tree.ToTestString())
		nodeC := tree.Insert(newSplayNode("C234"))
		assert.Equal(t, "[2,2,1]A2[5,3,2]B23[9,4,3]C234", tree.ToTestString())
		nodeD := tree.Insert(newSplayNode("D2345"))
		assert.Equal(t, "[2,2,1]A2[5,3,2]B23[9,4,3]C234[14,5,4]D2345", tree.ToTestString())

		tree.Splay(nodeB)
		assert.Equal(t, "[2,2,1]A2[14,3,3]B23[9,4,2]C234[5,5,1]D2345", tree.ToTestString())

		assert.Equal(t, 0, tree.IndexOf(nodeA))
		assert.Equal(t, 2, tree.IndexOf(nodeB))
		assert.Equal(t, 5, tree.IndexOf(nodeC))
		assert.Equal(t, 9, tree.IndexOf(nodeD))

		node, offset, err := tree.Find(1)
		assert.Equal(t, nodeA, node)
		assert.Equal(t, 1, offset)
		assert.NoError(t, err)

		node, offset, err = tree.Find(7)
		assert.Equal(t, nodeC, node)
		assert.Equal(t, 2, offset)
		assert.NoError(t, err)

		node, offset, err = tree.Find(11)
		assert.Equal(t, nodeD, node)
		assert.Equal(t, 2, offset)
		assert.NoError(t, err)
	})

	t.Run("deletion test", func(t *testing.T) {
		tree := splay.NewTree[*stringValue](nil)

		nodeH := tree.Insert(newSplayNode("H"))
		assert.Equal(t, "[1,1,1]H", tree.ToTestString())
		assert.Equal(t, 1, tree.Len())
		nodeE := tree.Insert(newSplayNode("E"))
		assert.Equal(t, "[1,1,1]H[2,1,2]E", tree.ToTestString())
		assert.Equal(t, 2, tree.Len())
		nodeL := tree.Insert(newSplayNode("LL"))
		assert.Equal(t, "[1,1,1]H[2,1,2]E[4,2,3]LL", tree.ToTestString())
		assert.Equal(t, 4, tree.Len())
		nodeO := tree.Insert(newSplayNode("O"))
		assert.Equal(t, "[1,1,1]H[2,1,2]E[4,2,3]LL[5,1,4]O", tree.ToTestString())
		assert.Equal(t, 5, tree.Len())

		tree.Delete(nodeE)
		assert.Equal(t, "[4,1,3]H[3,2,2]LL[1,1,1]O", tree.ToTestString())
		assert.Equal(t, 4, tree.Len())

		assert.Equal(t, tree.IndexOf(nodeH), 0)
		assert.Equal(t, tree.IndexOf(nodeE), -1)
		assert.Equal(t, tree.IndexOf(nodeL), 1)
		assert.Equal(t, tree.IndexOf(nodeO), 3)
	})

	t.Run("range delition test", func(t *testing.T) {
		tree, nodes := makeSampleTree()
		// check the filtering of DeleteRange
		removeNodes(nodes, 7, 8)
		tree.DeleteRange(nodes[6], nil)
		assert.Equal(
			t,
			"[1,1,1]A[3,2,2]BB[6,3,3]CCC[10,4,4]DDDD[15,5,5]EEEEE[19,4,6]FFFF[22,3,7]GGG[0,0,2]HH[0,0,1]I",
			tree.ToTestString(),
		)

		tree, nodes = makeSampleTree()
		// check the case 1 of DeleteRange
		removeNodes(nodes, 3, 6)
		tree.DeleteRange(nodes[2], nodes[7])
		assert.Equal(
			t,
			"[1,1,1]A[3,2,2]BB[6,3,4]CCC[0,0,2]DDDD[0,0,1]EEEEE[0,0,3]FFFF[0,0,1]GGG[9,2,5]HH[1,1,1]I",
			tree.ToTestString(),
		)

		tree, nodes = makeSampleTree()
		tree.Splay(nodes[6])
		tree.Splay(nodes[2])
		// check the case 2 of DeleteRange
		removeNodes(nodes, 3, 7)
		tree.DeleteRange(nodes[2], nodes[8])
		assert.Equal(
			t,
			"[1,1,1]A[3,2,2]BB[6,3,4]CCC[0,0,2]DDDD[0,0,1]EEEEE[0,0,3]FFFF[0,0,1]GGG[0,0,2]HH[7,1,5]I",
			tree.ToTestString(),
		)
	})

	t.Run("single node index test", func(t *testing.T) {
		tree := splay.NewTree[*stringValue](nil)
		node := tree.Insert(newSplayNode("A"))
		assert.Equal(t, 0, tree.IndexOf(node))
		tree.Delete(node)
		assert.Equal(t, -1, tree.IndexOf(node))
	})
}

func makeSampleTree() (*splay.Tree[*stringValue], []*splay.Node[*stringValue]) {
	tree := splay.NewTree[*stringValue](nil)
	var nodes []*splay.Node[*stringValue]

	nodes = append(nodes, tree.Insert(newSplayNode("A")))
	nodes = append(nodes, tree.Insert(newSplayNode("BB")))
	nodes = append(nodes, tree.Insert(newSplayNode("CCC")))
	nodes = append(nodes, tree.Insert(newSplayNode("DDDD")))
	nodes = append(nodes, tree.Insert(newSplayNode("EEEEE")))
	nodes = append(nodes, tree.Insert(newSplayNode("FFFF")))
	nodes = append(nodes, tree.Insert(newSplayNode("GGG")))
	nodes = append(nodes, tree.Insert(newSplayNode("HH")))
	nodes = append(nodes, tree.Insert(newSplayNode("I")))

	return tree, nodes
}

// Make nodes in given range the same state as tombstone.
func removeNodes(nodes []*splay.Node[*stringValue], from, to int) {
	for i := from; i <= to; i++ {
		nodes[i].Value().removed = true
		nodes[i].InitWeight()
	}
}

/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
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

package treelist_test

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/treelist"
)

type testValue struct {
	content string
	removed bool
}

func (v *testValue) IsRemoved() bool {
	return v.removed
}

func (v *testValue) String() string {
	return v.content
}

func newNode(content string) *treelist.Node[*testValue] {
	return treelist.NewNode(&testValue{content: content})
}

func newRemovedNode(content string) *treelist.Node[*testValue] {
	return treelist.NewNode(&testValue{content: content, removed: true})
}

func TestTreeList(t *testing.T) {
	t.Run("insert and find test", func(t *testing.T) {
		dummyHead := newRemovedNode("dummy")
		tree := treelist.NewTree(dummyHead)

		assert.Equal(t, 0, tree.Len())

		nodeA := newNode("A")
		tree.InsertAfter(dummyHead, nodeA)
		assert.Equal(t, 1, tree.Len())

		nodeB := newNode("B")
		tree.InsertAfter(nodeA, nodeB)
		assert.Equal(t, 2, tree.Len())

		nodeC := newNode("C")
		tree.InsertAfter(nodeB, nodeC)
		assert.Equal(t, 3, tree.Len())

		found, err := tree.Find(0)
		assert.NoError(t, err)
		assert.Equal(t, nodeA, found)

		found, err = tree.Find(1)
		assert.NoError(t, err)
		assert.Equal(t, nodeB, found)

		found, err = tree.Find(2)
		assert.NoError(t, err)
		assert.Equal(t, nodeC, found)
	})

	t.Run("insert in the middle test", func(t *testing.T) {
		dummyHead := newRemovedNode("dummy")
		tree := treelist.NewTree(dummyHead)

		nodeA := newNode("A")
		tree.InsertAfter(dummyHead, nodeA)
		nodeC := newNode("C")
		tree.InsertAfter(nodeA, nodeC)

		// Insert B between A and C
		nodeB := newNode("B")
		tree.InsertAfter(nodeA, nodeB)
		assert.Equal(t, 3, tree.Len())

		found, err := tree.Find(0)
		assert.NoError(t, err)
		assert.Equal(t, nodeA, found)

		found, err = tree.Find(1)
		assert.NoError(t, err)
		assert.Equal(t, nodeB, found)

		found, err = tree.Find(2)
		assert.NoError(t, err)
		assert.Equal(t, nodeC, found)
	})

	t.Run("insert after tombstone test", func(t *testing.T) {
		dummyHead := newRemovedNode("dummy")
		tree := treelist.NewTree(dummyHead)

		nodeA := newNode("A")
		tree.InsertAfter(dummyHead, nodeA)
		nodeB := newNode("B")
		tree.InsertAfter(nodeA, nodeB)

		// Tombstone A
		nodeA.Value().removed = true
		tree.UpdateWeight(nodeA)
		assert.Equal(t, 1, tree.Len())

		// Insert C after tombstoned A — should appear before B
		nodeC := newNode("C")
		tree.InsertAfter(nodeA, nodeC)
		assert.Equal(t, 2, tree.Len())

		found, err := tree.Find(0)
		assert.NoError(t, err)
		assert.Equal(t, nodeC, found)

		found, err = tree.Find(1)
		assert.NoError(t, err)
		assert.Equal(t, nodeB, found)
	})

	t.Run("delete test", func(t *testing.T) {
		dummyHead := newRemovedNode("dummy")
		tree := treelist.NewTree(dummyHead)

		nodeA := newNode("A")
		tree.InsertAfter(dummyHead, nodeA)
		nodeB := newNode("B")
		tree.InsertAfter(nodeA, nodeB)
		nodeC := newNode("C")
		tree.InsertAfter(nodeB, nodeC)
		assert.Equal(t, 3, tree.Len())

		// Delete middle node
		tree.Delete(nodeB)
		assert.Equal(t, 2, tree.Len())

		found, err := tree.Find(0)
		assert.NoError(t, err)
		assert.Equal(t, nodeA, found)

		found, err = tree.Find(1)
		assert.NoError(t, err)
		assert.Equal(t, nodeC, found)
	})

	t.Run("delete preserves node identity test", func(t *testing.T) {
		dummyHead := newRemovedNode("dummy")
		tree := treelist.NewTree(dummyHead)

		nodes := make([]*treelist.Node[*testValue], 5)
		prev := dummyHead
		for i := 0; i < 5; i++ {
			nodes[i] = newNode(fmt.Sprintf("%c", 'A'+i))
			tree.InsertAfter(prev, nodes[i])
			prev = nodes[i]
		}
		assert.Equal(t, 5, tree.Len())

		// Delete C (index 2). After deletion, remaining nodes should
		// still be findable and point to the same original node objects.
		tree.Delete(nodes[2])
		assert.Equal(t, 4, tree.Len())

		found, err := tree.Find(0)
		assert.NoError(t, err)
		assert.Equal(t, nodes[0], found) // A

		found, err = tree.Find(1)
		assert.NoError(t, err)
		assert.Equal(t, nodes[1], found) // B

		found, err = tree.Find(2)
		assert.NoError(t, err)
		assert.Equal(t, nodes[3], found) // D

		found, err = tree.Find(3)
		assert.NoError(t, err)
		assert.Equal(t, nodes[4], found) // E
	})

	t.Run("delete first and last nodes test", func(t *testing.T) {
		dummyHead := newRemovedNode("dummy")
		tree := treelist.NewTree(dummyHead)

		nodeA := newNode("A")
		tree.InsertAfter(dummyHead, nodeA)
		nodeB := newNode("B")
		tree.InsertAfter(nodeA, nodeB)
		nodeC := newNode("C")
		tree.InsertAfter(nodeB, nodeC)

		// Delete first live node
		tree.Delete(nodeA)
		assert.Equal(t, 2, tree.Len())
		found, err := tree.Find(0)
		assert.NoError(t, err)
		assert.Equal(t, nodeB, found)

		// Delete last live node
		tree.Delete(nodeC)
		assert.Equal(t, 1, tree.Len())
		found, err = tree.Find(0)
		assert.NoError(t, err)
		assert.Equal(t, nodeB, found)
	})

	t.Run("delete tombstoned node test", func(t *testing.T) {
		dummyHead := newRemovedNode("dummy")
		tree := treelist.NewTree(dummyHead)

		nodeA := newNode("A")
		tree.InsertAfter(dummyHead, nodeA)
		nodeB := newNode("B")
		tree.InsertAfter(nodeA, nodeB)
		nodeC := newNode("C")
		tree.InsertAfter(nodeB, nodeC)

		// Tombstone B, then physically delete it
		nodeB.Value().removed = true
		tree.UpdateWeight(nodeB)
		assert.Equal(t, 2, tree.Len())

		tree.Delete(nodeB)
		assert.Equal(t, 2, tree.Len()) // Still 2 live nodes (A, C)

		found, err := tree.Find(0)
		assert.NoError(t, err)
		assert.Equal(t, nodeA, found)

		found, err = tree.Find(1)
		assert.NoError(t, err)
		assert.Equal(t, nodeC, found)
	})

	t.Run("delete all nodes test", func(t *testing.T) {
		dummyHead := newRemovedNode("dummy")
		tree := treelist.NewTree(dummyHead)

		nodeA := newNode("A")
		tree.InsertAfter(dummyHead, nodeA)
		nodeB := newNode("B")
		tree.InsertAfter(nodeA, nodeB)

		tree.Delete(nodeA)
		tree.Delete(nodeB)
		tree.Delete(dummyHead)

		assert.Equal(t, 0, tree.Len())
	})

	t.Run("tombstone and update weight test", func(t *testing.T) {
		dummyHead := newRemovedNode("dummy")
		tree := treelist.NewTree(dummyHead)

		nodeA := newNode("A")
		tree.InsertAfter(dummyHead, nodeA)
		nodeB := newNode("B")
		tree.InsertAfter(nodeA, nodeB)
		nodeC := newNode("C")
		tree.InsertAfter(nodeB, nodeC)
		assert.Equal(t, 3, tree.Len())

		// Tombstone B
		nodeB.Value().removed = true
		tree.UpdateWeight(nodeB)
		assert.Equal(t, 2, tree.Len())

		// Find should skip B
		found, err := tree.Find(0)
		assert.NoError(t, err)
		assert.Equal(t, nodeA, found)

		found, err = tree.Find(1)
		assert.NoError(t, err)
		assert.Equal(t, nodeC, found)

		_, err = tree.Find(2)
		assert.Error(t, err)
	})

	t.Run("multiple tombstones test", func(t *testing.T) {
		dummyHead := newRemovedNode("dummy")
		tree := treelist.NewTree(dummyHead)

		nodes := make([]*treelist.Node[*testValue], 6)
		prev := dummyHead
		for i := 0; i < 6; i++ {
			nodes[i] = newNode(fmt.Sprintf("%c", 'A'+i))
			tree.InsertAfter(prev, nodes[i])
			prev = nodes[i]
		}
		// In-order: dummy, A, B, C, D, E, F
		assert.Equal(t, 6, tree.Len())

		// Tombstone B, D, F (odd-indexed live nodes)
		nodes[1].Value().removed = true // B
		tree.UpdateWeight(nodes[1])
		nodes[3].Value().removed = true // D
		tree.UpdateWeight(nodes[3])
		nodes[5].Value().removed = true // F
		tree.UpdateWeight(nodes[5])
		assert.Equal(t, 3, tree.Len())

		// Only A, C, E remain
		found, err := tree.Find(0)
		assert.NoError(t, err)
		assert.Equal(t, nodes[0], found) // A

		found, err = tree.Find(1)
		assert.NoError(t, err)
		assert.Equal(t, nodes[2], found) // C

		found, err = tree.Find(2)
		assert.NoError(t, err)
		assert.Equal(t, nodes[4], found) // E
	})

	t.Run("find out of bounds test", func(t *testing.T) {
		dummyHead := newRemovedNode("dummy")
		tree := treelist.NewTree(dummyHead)

		_, err := tree.Find(0)
		assert.Error(t, err)
		assert.ErrorIs(t, err, treelist.ErrOutOfIndex)

		nodeA := newNode("A")
		tree.InsertAfter(dummyHead, nodeA)

		_, err = tree.Find(-1)
		assert.Error(t, err)
		assert.ErrorIs(t, err, treelist.ErrOutOfIndex)

		_, err = tree.Find(1)
		assert.Error(t, err)
		assert.ErrorIs(t, err, treelist.ErrOutOfIndex)
	})

	t.Run("insert after nil is no-op test", func(t *testing.T) {
		dummyHead := newRemovedNode("dummy")
		tree := treelist.NewTree(dummyHead)
		assert.Equal(t, 0, tree.Len())

		tree.InsertAfter(nil, newNode("A"))
		assert.Equal(t, 0, tree.Len())

		tree.InsertAfter(dummyHead, nil)
		assert.Equal(t, 0, tree.Len())
	})

	t.Run("delete nil is no-op test", func(t *testing.T) {
		dummyHead := newRemovedNode("dummy")
		tree := treelist.NewTree(dummyHead)
		nodeA := newNode("A")
		tree.InsertAfter(dummyHead, nodeA)

		tree.Delete(nil)
		assert.Equal(t, 1, tree.Len())
	})

	t.Run("single live node tree test", func(t *testing.T) {
		node := newNode("A")
		tree := treelist.NewTree(node)
		assert.Equal(t, 1, tree.Len())

		found, err := tree.Find(0)
		assert.NoError(t, err)
		assert.Equal(t, node, found)

		tree.Delete(node)
		assert.Equal(t, 0, tree.Len())
	})

	t.Run("large sequential insert and find test", func(t *testing.T) {
		dummyHead := newRemovedNode("dummy")
		tree := treelist.NewTree(dummyHead)

		const n = 100
		nodes := make([]*treelist.Node[*testValue], n)
		prev := dummyHead
		for i := 0; i < n; i++ {
			nodes[i] = newNode(fmt.Sprintf("%d", i))
			tree.InsertAfter(prev, nodes[i])
			prev = nodes[i]
		}
		assert.Equal(t, n, tree.Len())

		// Verify all nodes are findable at correct index
		for i := 0; i < n; i++ {
			found, err := tree.Find(i)
			assert.NoError(t, err)
			assert.Equal(t, nodes[i], found, "mismatch at index %d", i)
		}
	})

	t.Run("large sequential insert, tombstone, and find test", func(t *testing.T) {
		dummyHead := newRemovedNode("dummy")
		tree := treelist.NewTree(dummyHead)

		const n = 100
		nodes := make([]*treelist.Node[*testValue], n)
		prev := dummyHead
		for i := 0; i < n; i++ {
			nodes[i] = newNode(fmt.Sprintf("%d", i))
			tree.InsertAfter(prev, nodes[i])
			prev = nodes[i]
		}

		// Tombstone even-indexed nodes
		for i := 0; i < n; i += 2 {
			nodes[i].Value().removed = true
			tree.UpdateWeight(nodes[i])
		}
		assert.Equal(t, n/2, tree.Len())

		// Verify odd-indexed nodes are at correct positions
		idx := 0
		for i := 1; i < n; i += 2 {
			found, err := tree.Find(idx)
			assert.NoError(t, err)
			assert.Equal(t, nodes[i], found, "mismatch at logical index %d (node %d)", idx, i)
			idx++
		}
	})

	t.Run("large sequential insert and delete test", func(t *testing.T) {
		dummyHead := newRemovedNode("dummy")
		tree := treelist.NewTree(dummyHead)

		const n = 100
		nodes := make([]*treelist.Node[*testValue], n)
		prev := dummyHead
		for i := 0; i < n; i++ {
			nodes[i] = newNode(fmt.Sprintf("%d", i))
			tree.InsertAfter(prev, nodes[i])
			prev = nodes[i]
		}

		// Delete even-indexed nodes
		for i := 0; i < n; i += 2 {
			tree.Delete(nodes[i])
		}
		assert.Equal(t, n/2, tree.Len())

		// Verify remaining nodes
		idx := 0
		for i := 1; i < n; i += 2 {
			found, err := tree.Find(idx)
			assert.NoError(t, err)
			assert.Equal(t, nodes[i], found, "mismatch at logical index %d (node %d)", idx, i)
			idx++
		}
	})

	t.Run("interleaved insert and delete test", func(t *testing.T) {
		dummyHead := newRemovedNode("dummy")
		tree := treelist.NewTree(dummyHead)

		nodeA := newNode("A")
		tree.InsertAfter(dummyHead, nodeA)
		nodeB := newNode("B")
		tree.InsertAfter(nodeA, nodeB)
		nodeC := newNode("C")
		tree.InsertAfter(nodeB, nodeC)

		// Delete B, insert D after A (where B was)
		tree.Delete(nodeB)
		nodeD := newNode("D")
		tree.InsertAfter(nodeA, nodeD)
		assert.Equal(t, 3, tree.Len())

		found, err := tree.Find(0)
		assert.NoError(t, err)
		assert.Equal(t, nodeA, found)

		found, err = tree.Find(1)
		assert.NoError(t, err)
		assert.Equal(t, nodeD, found)

		found, err = tree.Find(2)
		assert.NoError(t, err)
		assert.Equal(t, nodeC, found)
	})

	t.Run("insert after dummy head with existing nodes test", func(t *testing.T) {
		dummyHead := newRemovedNode("dummy")
		tree := treelist.NewTree(dummyHead)

		nodeB := newNode("B")
		tree.InsertAfter(dummyHead, nodeB)
		nodeC := newNode("C")
		tree.InsertAfter(nodeB, nodeC)

		// Insert A right after dummy (before B)
		nodeA := newNode("A")
		tree.InsertAfter(dummyHead, nodeA)
		assert.Equal(t, 3, tree.Len())

		found, err := tree.Find(0)
		assert.NoError(t, err)
		assert.Equal(t, nodeA, found)

		found, err = tree.Find(1)
		assert.NoError(t, err)
		assert.Equal(t, nodeB, found)

		found, err = tree.Find(2)
		assert.NoError(t, err)
		assert.Equal(t, nodeC, found)
	})

	t.Run("ToTestString test", func(t *testing.T) {
		dummyHead := newRemovedNode("dummy")
		tree := treelist.NewTree(dummyHead)

		nodeA := newNode("A")
		tree.InsertAfter(dummyHead, nodeA)
		nodeB := newNode("B")
		tree.InsertAfter(nodeA, nodeB)

		str := tree.ToTestString()
		assert.Contains(t, str, "dummy")
		assert.Contains(t, str, "A")
		assert.Contains(t, str, "B")
	})

	t.Run("stress test with random operations", func(t *testing.T) {
		rng := rand.New(rand.NewSource(42))
		dummyHead := newRemovedNode("dummy")
		tree := treelist.NewTree(dummyHead)

		// Track live nodes in a slice for reference
		var liveNodes []*treelist.Node[*testValue]
		allNodes := []*treelist.Node[*testValue]{dummyHead}

		const ops = 500
		for i := 0; i < ops; i++ {
			op := rng.Intn(3)

			switch {
			case op == 0 || len(allNodes) < 3:
				// Insert after a random existing node
				prevIdx := rng.Intn(len(allNodes))
				prev := allNodes[prevIdx]
				node := newNode(fmt.Sprintf("n%d", i))
				tree.InsertAfter(prev, node)
				allNodes = append(allNodes, node)
				// Update liveNodes from scratch
				liveNodes = rebuildLiveList(tree, len(allNodes))

			case op == 1 && len(liveNodes) > 0:
				// Tombstone a random live node
				idx := rng.Intn(len(liveNodes))
				liveNodes[idx].Value().removed = true
				tree.UpdateWeight(liveNodes[idx])
				liveNodes = rebuildLiveList(tree, len(allNodes))

			case op == 2 && len(allNodes) > 1:
				// Delete a random non-dummy node
				delIdx := 1 + rng.Intn(len(allNodes)-1)
				tree.Delete(allNodes[delIdx])
				allNodes = append(allNodes[:delIdx], allNodes[delIdx+1:]...)
				liveNodes = rebuildLiveList(tree, len(allNodes))
			}

			// Verify tree length matches live node count
			assert.Equal(t, len(liveNodes), tree.Len(), "iteration %d", i)

			// Verify all live nodes are findable
			for j, node := range liveNodes {
				found, err := tree.Find(j)
				assert.NoError(t, err, "iteration %d, find index %d", i, j)
				assert.Equal(t, node, found, "iteration %d, find index %d", i, j)
			}
		}
	})
}

// rebuildLiveList reconstructs the list of live nodes by using Find.
func rebuildLiveList(tree *treelist.Tree[*testValue], _ int) []*treelist.Node[*testValue] {
	var result []*treelist.Node[*testValue]
	for i := 0; i < tree.Len(); i++ {
		node, err := tree.Find(i)
		if err != nil {
			break
		}
		result = append(result, node)
	}
	return result
}

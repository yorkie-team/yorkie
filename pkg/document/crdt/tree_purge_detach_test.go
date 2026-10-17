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

package crdt

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// detachFixture builds <r><div><p>ab</p></div></r> with div and p removed and
// the text live under them, the shape an identity restore leaves.
func detachFixture(t *testing.T) (*Tree, *TreeNode, *TreeNode, *TreeNode, *time.Ticket) {
	t.Helper()
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	lamport := int64(0)
	nextTicket := func() *time.Ticket {
		lamport++
		return time.NewTicket(lamport, 0, actor)
	}
	root := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "r", nil)
	div := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "div", nil)
	p := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "p", nil)
	text := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "text", nil, "ab")
	require.NoError(t, root.Append(div))
	require.NoError(t, div.Append(p))
	require.NoError(t, p.Append(text))
	tree := NewTree(root, nextTicket())
	removedAt := nextTicket()
	div.remove(removedAt)
	p.remove(removedAt)
	return tree, div, p, text, removedAt
}

// TestCollectNeverTakesLiveBelowZero pins the clamp at the call site. The
// live text under the purged elements was never charged to this Root's
// Live, which an honest replica cannot reach since every live node is
// charged at its size; the subtraction still stops at zero instead of
// leaving a negative Live that would switch the size gate off.
func TestCollectNeverTakesLiveBelowZero(t *testing.T) {
	tree, div, p, _, removedAt := detachFixture(t)
	root := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
	// Booked to GC only, as born-dead nodes are, so Live holds nothing for
	// any of the three nodes.
	for _, node := range []*TreeNode{div, p} {
		size := node.DataSize()
		root.RegisterGCPair(GCPair{Parent: tree, Child: node, GCOnlySize: &size})
	}
	require.Zero(t, root.DocSize().Live.Data, "nothing of the text is charged to Live")

	vector := time.NewVersionVector()
	vector.Set(removedAt.ActorID(), removedAt.Lamport())
	n, err := root.GarbageCollect(vector)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	live := root.DocSize().Live
	assert.GreaterOrEqual(t, live.Data, 0, "the text's bytes are not taken out of a Live that never held them")
	assert.GreaterOrEqual(t, live.Meta, 0)
}

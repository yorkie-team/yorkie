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

// TestCollectLeavesAPairOfALiveNode pins that a collection neither purges
// nor books anything for a pair whose child is live (yorkie#2245): the pair
// stays, GC and Live keep their sizes, and the node stays in the tree.
func TestCollectLeavesAPairOfALiveNode(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	root := NewTreeNode(NewTreeNodeID(time.NewTicket(1, 0, actor), 0), "r", nil)
	div := NewTreeNode(NewTreeNodeID(time.NewTicket(2, 0, actor), 0), "div", nil)
	require.NoError(t, root.Append(div))
	tree := NewTree(root, time.NewTicket(3, 0, actor))

	doc := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))
	size := div.DataSize()
	doc.RegisterGCPair(GCPair{Parent: tree, Child: div, GCOnlySize: &size})
	before := doc.DocSize()

	vector := time.NewVersionVector()
	vector.Set(actor, 10)
	n, err := doc.GarbageCollect(vector, nil)
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.Equal(t, 1, doc.GarbageLen(), "the pair stays")
	assert.Equal(t, before, doc.DocSize())
	assert.Same(t, tree.Root(), div.Index.Parent.Value)
}

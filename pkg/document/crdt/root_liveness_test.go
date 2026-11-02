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

package crdt_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestGarbageCollectSkipsLiveRegisteredElement pins that collection tolerates
// a registered pair whose element is not removed, instead of dereferencing a
// nil removedAt and deleting a live member.
//
// The two ways to reach that state are both out of this release:
// identity-preserving revive for Elements, which clears removedAt in place
// the way Text and Tree nodes already do, and the unconditional node return
// in RGATreeList.DeleteByCreatedAt, which registers a pair for a removal
// entry.elem.Remove declined. This test drives the state directly instead,
// because neither route is reachable from a causal change log today. It is
// the guard that makes either one land as a skipped entry rather than a panic
// inside a snapshot build.
func TestGarbageCollectSkipsLiveRegisteredElement(t *testing.T) {
	root := helper.TestRoot()
	ctx := helper.TextChangeContext(root)

	obj := crdt.NewObject(crdt.NewElementRHT(), ctx.IssueTimeTicket())
	root.Object().Set("obj", obj)
	root.RegisterElement(obj, root.Object())

	removedAt := ctx.IssueTimeTicket()
	_, err := root.Object().DeleteByCreatedAt(obj.CreatedAt(), removedAt)
	require.NoError(t, err)
	root.RegisterRemovedElementPair(root.Object(), obj)
	assert.Equal(t, 1, root.GarbageLen())

	// Revive in place: the element keeps its identity and its registration,
	// and stops being a tombstone. Nothing in the Element path does this yet.
	obj.SetRemovedAt(nil)

	n, err := root.GarbageCollect(helper.MaxVersionVector())
	require.NoError(t, err)
	assert.Equal(t, 0, n, "collection purged an element that is not removed")
	assert.Equal(t, obj, root.FindByCreatedAt(obj.CreatedAt()),
		"the revived element lost its registration")
	assert.True(t, root.Object().Has("obj"),
		"the revived element was unlinked from its key")
}

// TestGarbageCollectSkipsLiveRegisteredNode is the node-worklist sibling of the
// test above: collection tolerates a registered GC pair whose child is no
// longer removed.
//
// The node side is where removedAt is actually cleared in place today --
// TreeNode.unremove and RGATreeSplitNode.SetRemovedAt(nil) -- and the entry
// comes off gcNodePairMap only because the reviver also calls
// UnregisterGCPair. Every revive traced does, so this state is driven
// directly. The guard is what makes a revive that forgets land as a skipped
// entry instead of a nil-ticket deref inside a server-side snapshot build.
func TestGarbageCollectSkipsLiveRegisteredNode(t *testing.T) {
	root := helper.TestRoot()
	ctx := helper.TextChangeContext(root)

	treeRoot := crdt.NewTreeNode(crdt.NewTreeNodeID(ctx.IssueTimeTicket(), 0), "r", nil)
	para := crdt.NewTreeNode(crdt.NewTreeNodeID(ctx.IssueTimeTicket(), 0), "p", nil)
	require.NoError(t, treeRoot.Append(para))
	text := crdt.NewTreeNode(crdt.NewTreeNodeID(ctx.IssueTimeTicket(), 0), "text", nil, "hello")
	require.NoError(t, para.Append(text))

	text.SetRemovedAt(ctx.IssueTimeTicket())

	tree := crdt.NewTree(treeRoot, ctx.IssueTimeTicket())
	root.Object().Set("tree", tree)
	root.RegisterElement(tree, root.Object())
	require.Equal(t, 1, root.GarbageLen(), "the tombstoned node is registered")

	// Revive in place without unregistering the pair.
	text.SetRemovedAt(nil)

	n, err := root.GarbageCollect(helper.MaxVersionVector())
	require.NoError(t, err)
	assert.Equal(t, 0, n, "collection purged a node that is not removed")
	assert.Equal(t, 1, root.GarbageLen(),
		"the skipped entry has to stay on the worklist, not be dropped")
	assert.Equal(t, "<r><p>hello</p></r>", tree.ToXML(),
		"the revived node was unlinked from its parent")
}

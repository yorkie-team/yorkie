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

// TestRegisterElementBooksInternalTombstones pins that a tombstone living
// inside a registered element -- a removed tree node, which is reached only
// through a GC pair -- is booked into GC by RegisterElement rather than only
// by snapshot load.
//
// Set/Add/ArraySet carry a whole element, decoded by the same readers a
// snapshot is and just as client-supplied. Before this, only NewRoot
// registered those pairs, so a tree node arriving tombstoned on an element
// payload (an undo restoring a container whose tree still held tombstones, or
// a crafted payload whose nodes all carry removedAt) was invisible to every
// later edit yet collectable by nothing.
func TestRegisterElementBooksInternalTombstones(t *testing.T) {
	root := helper.TestRoot()
	ctx := helper.TextChangeContext(root)

	treeRoot := crdt.NewTreeNode(crdt.NewTreeNodeID(ctx.IssueTimeTicket(), 0), "r", nil)
	para := crdt.NewTreeNode(crdt.NewTreeNodeID(ctx.IssueTimeTicket(), 0), "p", nil)
	require.NoError(t, treeRoot.Append(para))
	text := crdt.NewTreeNode(crdt.NewTreeNodeID(ctx.IssueTimeTicket(), 0), "text", nil, "hello")
	require.NoError(t, para.Append(text))

	// The payload was captured while this node was already a tombstone.
	text.SetRemovedAt(ctx.IssueTimeTicket())

	tree := crdt.NewTree(treeRoot, ctx.IssueTimeTicket())
	root.Object().Set("tree", tree)

	before := root.GarbageLen()
	root.RegisterElement(tree, root.Object())
	assert.Equal(t, before+1, root.GarbageLen(),
		"a tombstone inside the registered element has to be collectable")
	assert.NotEqual(t, 0, root.DocSize().GC.Data+root.DocSize().GC.Meta,
		"its bytes belong to GC, not to nothing")

	n, err := root.GarbageCollect(helper.MaxVersionVector())
	require.NoError(t, err)
	assert.Equal(t, 1, n)
}

// TestRegisterElementSkipsTombstonedTreeRoot pins that the tree ROOT is never
// booked as a GC pair, however it arrived tombstoned.
//
// Purging is detachment from a parent, and the root has none. The root is not
// legitimately removable, but removedAt on it is not a server invariant: the
// element payload of a Set/Add/ArraySet is read by the same BytesTo* reader a
// snapshot is, so a crafted one can mark it removed. Booked, it would take
// GarbageCollect through a nil parent deref on the server.
func TestRegisterElementSkipsTombstonedTreeRoot(t *testing.T) {
	root := helper.TestRoot()
	ctx := helper.TextChangeContext(root)

	treeRoot := crdt.NewTreeNode(crdt.NewTreeNodeID(ctx.IssueTimeTicket(), 0), "r", nil)
	para := crdt.NewTreeNode(crdt.NewTreeNodeID(ctx.IssueTimeTicket(), 0), "p", nil)
	require.NoError(t, treeRoot.Append(para))

	// A crafted payload marks every node removed, the root included.
	treeRoot.SetRemovedAt(ctx.IssueTimeTicket())
	para.SetRemovedAt(ctx.IssueTimeTicket())

	tree := crdt.NewTree(treeRoot, ctx.IssueTimeTicket())
	root.Object().Set("tree", tree)

	before := root.GarbageLen()
	root.RegisterElement(tree, root.Object())
	assert.Equal(t, before+1, root.GarbageLen(),
		"only the parented tombstone is booked, never the root")

	n, err := root.GarbageCollect(helper.MaxVersionVector())
	require.NoError(t, err)
	assert.Equal(t, 1, n)
}

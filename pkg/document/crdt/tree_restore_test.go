/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
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

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

// Identity-preserving Tree restore tests. Undo revives deleted nodes under
// their ORIGINAL identity (createdAt+offset) instead of copy-reinserting
// fresh ones, so concurrent undos converge; redo re-tombstones them. When a
// node was GC-purged before the undo, it is recreated from the carried span
// and re-anchored via the ladder in recreateFromSpan (parent-before-child).

// elementSpan builds a restore span describing an element node under parent.
func elementSpan(node, parent *crdt.TreeNode) *crdt.TreeRestoreSpan {
	return &crdt.TreeRestoreSpan{
		ID:       node.ID(),
		NodeType: node.Type(),
		IsText:   false,
		ParentID: parent.ID(),
	}
}

// textSpan builds a restore span describing a whole text node under parent.
func textSpan(node, parent *crdt.TreeNode) *crdt.TreeRestoreSpan {
	return &crdt.TreeRestoreSpan{
		ID:       node.ID(),
		NodeType: node.Type(),
		IsText:   true,
		Length:   len([]rune(node.Value)),
		Value:    node.Value,
		ParentID: parent.ID(),
	}
}

// TestTreeRestoreUnremove verifies that restoring a still-present (tombstoned,
// not purged) node un-tombstones it IN PLACE under its original identity, and
// that the tree's visible length returns to exactly its pre-delete value.
func TestTreeRestoreUnremove(t *testing.T) {
	ctx := helper.TextChangeContext(helper.TestRoot())
	tree := createHelloTree(t, ctx) // <r><p>hello</p></r>
	before := tree.Root().Len()

	p := tree.Root().Children()[0]
	text := p.Children()[0]
	span := textSpan(text, p)

	// Delete "hello" ([1,6)); the text node becomes a tombstone in place.
	_, _, err := tree.EditT(1, 6, nil, 0, helper.TimeT(ctx), issueTicket(ctx))
	assert.NoError(t, err)
	assert.Equal(t, "<r><p></p></r>", tree.ToXML())

	untombstoned, recreated := tree.Restore([]*crdt.TreeRestoreSpan{span})
	assert.Len(t, untombstoned, 1, "the tombstone is revived in place")
	assert.Empty(t, recreated, "nothing was purged, so nothing is recreated")
	assert.Equal(t, "<r><p>hello</p></r>", tree.ToXML())
	assert.Equal(t, before, tree.Root().Len(), "length must be symmetric")
}

// TestTreeRetombstoneThenRestore checks the redo/undo primitive symmetry:
// retombstoning live nodes by identity removes them; restoring the same spans
// revives them. This is the pair a redo (retombstone) / undo (restore) uses.
func TestTreeRetombstoneThenRestore(t *testing.T) {
	ctx := helper.TextChangeContext(helper.TestRoot())
	tree := createHelloTree(t, ctx) // <r><p>hello</p></r>

	p := tree.Root().Children()[0]
	text := p.Children()[0]
	// Parent-before-child order, as edit() emits it.
	spans := []*crdt.TreeRestoreSpan{elementSpan(p, tree.Root()), textSpan(text, p)}

	// Retombstone (redo of a deletion undo): the live subtree is removed.
	pairs := tree.Retombstone(spans, helper.TimeT(ctx))
	assert.NotEmpty(t, pairs, "removing live nodes yields GC pairs")
	assert.Equal(t, "<r></r>", tree.ToXML())

	// Restore (undo): the same identities come back, parent before child.
	untombstoned, recreated := tree.Restore(spans)
	assert.Len(t, untombstoned, 2)
	assert.Empty(t, recreated)
	assert.Equal(t, "<r><p>hello</p></r>", tree.ToXML())
}

// TestTreeRestoreExecuteAfterGC exercises the full server execution path: a
// deletion, a GC purge that physically removes the tombstones, then a restore
// routed through operations.Execute. Because the nodes are gone, restore must
// RECREATE them from the carried spans and re-anchor them — the element first,
// then its text child under the just-recreated element (parent-before-child).
func TestTreeRestoreExecuteAfterGC(t *testing.T) {
	ctx := helper.TextChangeContext(helper.TestRoot())
	root := helper.TestRoot()
	tree := createHelloTree(t, ctx) // <r><p>hello</p></r>
	root.RegisterElement(tree)
	parent := tree.CreatedAt()

	p := tree.Root().Children()[0]
	text := p.Children()[0]
	// Capture spans from the LIVE nodes, parent-before-child, before deleting.
	spans := []*crdt.TreeRestoreSpan{elementSpan(p, tree.Root()), textSpan(text, p)}

	// Delete the whole <p>hello</p> ([0,7)) via an executed op so GC pairs are
	// registered on the root.
	from, err := tree.FindPos(0)
	assert.NoError(t, err)
	to, err := tree.FindPos(7)
	assert.NoError(t, err)
	delOp := operations.NewTreeEdit(parent, from, to, nil, 0, helper.TimeT(ctx))
	assert.NoError(t, delOp.Execute(root, nil))
	assert.Equal(t, "<r></r>", tree.ToXML())

	// Purge: the tombstones are physically removed, so restore cannot
	// un-tombstone them — it must recreate.
	n, err := root.GarbageCollect(helper.MaxVersionVector())
	assert.NoError(t, err)
	assert.Positive(t, n, "the deleted subtree should be purged")

	// Restore through Execute (identity-addressed; positions unused). An empty
	// version vector marks the trusted local path, skipping identity checks.
	restoreOp := operations.NewRestoreTreeEdit(
		parent, nil, nil, helper.TimeT(ctx),
		spans, crdt.RestoreModeRestore, nil,
	)
	assert.NoError(t, restoreOp.Execute(root, helper.MaxVersionVector()))

	assert.Equal(t, "<r><p>hello</p></r>", tree.ToXML(),
		"the purged subtree is recreated under its original identity")
	assert.Equal(t, 0, root.GarbageLen(), "restore leaves nothing registered for GC")
}

// TestTreeRestoreParentGoneSkip verifies the B1 rule: if a recreated node's
// parent is genuinely absent (purged and not part of this restore's spans),
// the node is skipped rather than mis-attached or panicking. Skipping is
// convergent because every replica resolves parent-absent identically.
func TestTreeRestoreParentGoneSkip(t *testing.T) {
	ctx := helper.TextChangeContext(helper.TestRoot())
	tree := createHelloTree(t, ctx) // <r><p>hello</p></r>

	// A span whose parent id points at a node that does not exist in the tree.
	ghostParent := crdt.NewTreeNodeID(helper.TimeT(ctx), 0)
	orphan := &crdt.TreeRestoreSpan{
		ID:       crdt.NewTreeNodeID(helper.TimeT(ctx), 0),
		NodeType: "text",
		IsText:   true,
		Length:   1,
		Value:    "x",
		ParentID: ghostParent,
	}

	untombstoned, recreated := tree.Restore([]*crdt.TreeRestoreSpan{orphan})
	assert.Empty(t, untombstoned)
	assert.Empty(t, recreated, "a node with no surviving parent is skipped (B1)")
	assert.Equal(t, "<r><p>hello</p></r>", tree.ToXML(), "tree is untouched")
}

// TestTreeRestoreRejectsForgedIdentity verifies that TreeEdit.Execute rejects a
// restore span whose node identity the acting change could not causally have
// observed. Execute materializes spans into authoritative server state, so
// without this guard a client could forge a live node under another actor's
// (actor, lamport) identity. Parity with the Text restore path; victimActor
// and restoreActor are defined in text_restore_test.go (same test package).
func TestTreeRestoreRejectsForgedIdentity(t *testing.T) {
	ctx := helper.TextChangeContext(helper.TestRoot())
	root := helper.TestRoot()
	tree := createHelloTree(t, ctx) // <r><p>hello</p></r>
	root.RegisterElement(tree)
	parent := tree.CreatedAt()

	forged := &crdt.TreeRestoreSpan{
		ID:       crdt.NewTreeNodeID(time.NewTicket(999, 0, victimActor), 0),
		NodeType: "text",
		IsText:   true,
		Length:   2,
		Value:    "XY",
		ParentID: tree.Root().ID(),
	}
	op := operations.NewRestoreTreeEdit(parent, nil, nil, helper.TimeT(ctx),
		[]*crdt.TreeRestoreSpan{forged}, crdt.RestoreModeRestore, nil)

	// The acting change knows only restoreActor, never victimActor, so the
	// forged identity is uncausal and must be rejected.
	vv := helper.VersionVectorOf(map[time.ActorID]int64{restoreActor: time.MaxLamport})
	assert.ErrorIs(t, op.Execute(root, vv), operations.ErrUnknownRestoreIdentity)
	assert.Equal(t, "<r><p>hello</p></r>", tree.ToXML(), "state untouched on rejection")
}

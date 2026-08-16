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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

// A TreeNodeID (createdAt + offset) is the identity every position anchors to,
// so it must name at most one node. The SDK's copy-reinsert undo path
// re-inserts a deleted piece under its ORIGINAL id, which leaves two nodes
// under one id. NodeMapByID.Put then silently overwrites, so which node an id
// resolves to depends on the order the nodes were put: insertion order in a
// live document, document order in one rebuilt from a snapshot. The two
// disagree, and an edit anchored at such an id splits a node past its end.
// This is what makes a document unopenable — see wafflebase#725.

// duplicatedIDs returns the ids that name more than one node in the tree.
func duplicatedIDs(tree *crdt.Tree) []string {
	counts := map[string]int{}
	for _, node := range tree.Nodes() {
		counts[node.IDString()]++
	}

	var dupes []string
	for id, count := range counts {
		if count > 1 {
			dupes = append(dupes, fmt.Sprintf("%s x%d", id, count))
		}
	}
	return dupes
}

// createDigitTree builds <r><p>0123456789</p></r> in a root object under the
// key "t", and returns the tree with the id of its text node.
func createDigitTree(t *testing.T, ctx *change.Context, root *crdt.Root) (*crdt.Tree, *crdt.TreeNodeID) {
	tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "r", nil), helper.TimeT(ctx))
	_, _, err := tree.EditT(0, 0, []*crdt.TreeNode{
		crdt.NewTreeNode(helper.PosT(ctx), "p", nil),
	}, 0, helper.TimeT(ctx), issueTicket(ctx))
	assert.NoError(t, err)

	textID := helper.PosT(ctx)
	_, _, err = tree.EditT(1, 1, []*crdt.TreeNode{
		crdt.NewTreeNode(textID, "text", nil, "0123456789"),
	}, 0, helper.TimeT(ctx), issueTicket(ctx))
	assert.NoError(t, err)
	assert.Equal(t, "<r><p>0123456789</p></r>", tree.ToXML())

	root.Object().Set("t", tree)
	root.RegisterElement(tree)

	return tree, textID
}

// corruptWithDuplicatedNodeID reproduces the state an older SDK left in stored
// documents: the character at text offset 5 is deleted, and a copy of it is
// attached under the tombstone's ORIGINAL id. It bypasses Tree.Edit so the
// test keeps exercising an already-corrupted document even once the edit path
// refuses to create duplicates.
func corruptWithDuplicatedNodeID(
	t *testing.T,
	tree *crdt.Tree,
	ctx *change.Context,
	textID *crdt.TreeNodeID,
) {
	_, _, err := tree.EditT(6, 7, nil, 0, helper.TimeT(ctx), issueTicket(ctx))
	assert.NoError(t, err)
	assert.Equal(t, "<r><p>012346789</p></r>", tree.ToXML())

	p := tree.Root().Children()[0]
	_, tombstone := tree.NodeMapByID.Floor(crdt.NewTreeNodeID(textID.CreatedAt, 5))
	assert.NotNil(t, tombstone)
	assert.True(t, tombstone.IsRemoved(), "the deleted piece is still a tombstone")

	// The copy lands before the tombstone, where an insert at the same index
	// puts it, and wins NodeMapByID because it is put last.
	offset := 0
	for i, child := range p.Children(true) {
		if child == tombstone {
			offset = i
			break
		}
	}
	dupe := crdt.NewTreeNode(crdt.NewTreeNodeID(textID.CreatedAt, 5), "text", nil, "5")
	assert.NoError(t, p.InsertAt(dupe, offset))
	tree.NodeMapByID.Put(dupe.ID(), dupe)
	assert.Equal(t, "<r><p>0123456789</p></r>", tree.ToXML())
}

// rebuildFromSnapshot round-trips the root object through the snapshot
// encoding, as the server does on compaction and on AttachDocument when the
// document is not in the cache.
func rebuildFromSnapshot(t *testing.T, root *crdt.Root) *crdt.Tree {
	snapshot, err := converter.ObjectToBytes(root.Object())
	assert.NoError(t, err)

	obj, err := converter.BytesToObject(snapshot)
	assert.NoError(t, err)

	tree, ok := obj.Get("t").(*crdt.Tree)
	assert.True(t, ok)

	return tree
}

// TestTreeEditDropsDuplicatedContentNodeID verifies that an edit whose content
// carries an id already present in the tree never leaves two nodes sharing
// that id. This is what the SDK's copy-reinsert undo path
// (tree_edit_operation.ts, cloneAndDropPreTombstoned) sends when cmd+z
// reverses a deletion.
func TestTreeEditDropsDuplicatedContentNodeID(t *testing.T) {
	ctx := helper.TextChangeContext(helper.TestRoot())
	tree, textID := createDigitTree(t, ctx, helper.TestRoot())

	_, _, err := tree.EditT(6, 7, nil, 0, helper.TimeT(ctx), issueTicket(ctx))
	assert.NoError(t, err)
	assert.Equal(t, "<r><p>012346789</p></r>", tree.ToXML())

	// The undo arrives as a later change, so it carries a later lamport than
	// the id its content reuses.
	undoAt := time.NewTicket(helper.TimeT(ctx).Lamport()+1, 1, time.InitialActorID)
	_, _, err = tree.EditT(6, 6, []*crdt.TreeNode{
		crdt.NewTreeNode(crdt.NewTreeNodeID(textID.CreatedAt, 5), "text", nil, "5"),
	}, 0, undoAt, func() *time.Ticket { return undoAt })
	assert.NoError(t, err, "the rest of the history must stay replayable")

	assert.Empty(t, duplicatedIDs(tree), "a TreeNodeID must name at most one node")
	assert.Equal(t, "<r><p>012346789</p></r>", tree.ToXML(), "the copy is not inserted")
}

// TestTreeEditDropsContentIDCreatedByItsOwnSplit covers the copy whose id does
// not exist yet when the edit starts: resolving the range splits a text node
// and creates that id moments before the content is inserted under it.
func TestTreeEditDropsContentIDCreatedByItsOwnSplit(t *testing.T) {
	ctx := helper.TextChangeContext(helper.TestRoot())
	tree, textID := createDigitTree(t, ctx, helper.TestRoot())

	undoAt := time.NewTicket(helper.TimeT(ctx).Lamport()+1, 1, time.InitialActorID)
	_, _, err := tree.EditT(6, 6, []*crdt.TreeNode{
		crdt.NewTreeNode(crdt.NewTreeNodeID(textID.CreatedAt, 5), "text", nil, "5"),
	}, 0, undoAt, func() *time.Ticket { return undoAt })
	assert.NoError(t, err)

	assert.Empty(t, duplicatedIDs(tree), "a TreeNodeID must name at most one node")
}

// TestTreeEditDropsDuplicatedContentFromAnotherActor covers a copy sent by a
// different client: what marks content as a copy is that its id belongs to
// another change, whether that change is earlier or merely another actor's.
func TestTreeEditDropsDuplicatedContentFromAnotherActor(t *testing.T) {
	ctx := helper.TextChangeContext(helper.TestRoot())
	tree, textID := createDigitTree(t, ctx, helper.TestRoot())

	_, _, err := tree.EditT(6, 7, nil, 0, helper.TimeT(ctx), issueTicket(ctx))
	assert.NoError(t, err)

	actorID, err := time.ActorIDFromHex("0123456789abcdef01234567")
	assert.NoError(t, err)
	undoAt := time.NewTicket(helper.TimeT(ctx).Lamport(), 1, actorID)
	_, _, err = tree.EditT(6, 6, []*crdt.TreeNode{
		crdt.NewTreeNode(crdt.NewTreeNodeID(textID.CreatedAt, 5), "text", nil, "5"),
	}, 0, undoAt, func() *time.Ticket { return undoAt })
	assert.NoError(t, err)

	assert.Empty(t, duplicatedIDs(tree), "a TreeNodeID must name at most one node")
	assert.Equal(t, "<r><p>012346789</p></r>", tree.ToXML(), "the copy is not inserted")
}

// TestTreeEditKeepsCollidingContentFromSameChange guards the other side of the
// rule. The delimiters an element split consumes are simulated rather than
// replayed (see the note in operations.TreeEdit), so an id issued by this edit
// can collide with one already in the tree. That content belongs to the
// document and must be inserted, not dropped as a copy.
func TestTreeEditKeepsCollidingContentFromSameChange(t *testing.T) {
	ctx := helper.TextChangeContext(helper.TestRoot())
	tree, _ := createDigitTree(t, ctx, helper.TestRoot())

	// Reuse the id of the <p> already in the tree, under this edit's own
	// lamport and actor.
	existingID := tree.Root().Children()[0].ID()
	editedAt := time.NewTicket(
		existingID.CreatedAt.Lamport(),
		existingID.CreatedAt.Delimiter()+1,
		existingID.CreatedAt.ActorID(),
	)
	_, _, err := tree.EditT(12, 12, []*crdt.TreeNode{
		crdt.NewTreeNode(crdt.NewTreeNodeID(existingID.CreatedAt, 0), "p", nil),
	}, 0, editedAt, func() *time.Ticket { return editedAt })
	assert.NoError(t, err)

	assert.Equal(t, "<r><p>0123456789</p><p></p></r>", tree.ToXML(),
		"content issued by this change is inserted even when its id collides")
}

// TestTreePurgeKeepsLiveNodeResolvable verifies that collecting a tombstone
// leaves the live node that shares its id resolvable. NodeMapByID is keyed by
// id, so removing the entry of a duplicated id would take the live node's
// registration with it and put the document back where it started: positions
// anchored at that id resolving to a node that does not span their offset.
func TestTreePurgeKeepsLiveNodeResolvable(t *testing.T) {
	ctx := helper.TextChangeContext(helper.TestRoot())
	root := helper.TestRoot()
	tree, textID := createDigitTree(t, ctx, root)

	corruptWithDuplicatedNodeID(t, tree, ctx, textID)

	id := crdt.NewTreeNodeID(textID.CreatedAt, 5)
	_, live := tree.NodeMapByID.Floor(id)
	assert.False(t, live.IsRemoved())

	var tombstone *crdt.TreeNode
	for _, node := range tree.Nodes() {
		if node.ID().Equal(id) && node.IsRemoved() {
			tombstone = node
			break
		}
	}
	assert.NotNil(t, tombstone)
	assert.NoError(t, tree.Purge(tombstone))

	_, resolved := tree.NodeMapByID.Floor(id)
	assert.Equal(t, live, resolved, "the live node keeps the id after its tombstone is collected")
	assert.Equal(t, "<r><p>0123456789</p></r>", tree.ToXML())
}

// TestTreeEditDroppedCopyDoesNotWidenReverseOperation ports
// tree_duplicate_id_test.ts's "does not let a dropped copy widen the reverse
// operation" (found missing in the Task 21 parity re-audit). It is the one
// case in this file exercised at the operations layer (operations.TreeEdit
// via Execute), not crdt.Tree.EditT directly, because the property under
// test -- GetContentSize() and the produced reverse's own range -- lives on
// the operation, not the CRDT tree: if a dropped copy still counted toward
// the accepted content size, the undo stack's stored range would widen by
// that amount, and reconciling it against a later remote edit would shift a
// position that never moved, or a redo of the reverse would delete a live
// neighbour the original edit never inserted.
func TestTreeEditDroppedCopyDoesNotWidenReverseOperation(t *testing.T) {
	root := helper.TestRoot()
	ctx := helper.TextChangeContext(root)
	tree, textID := createDigitTree(t, ctx, root)

	_, _, err := tree.EditT(6, 7, nil, 0, helper.TimeT(ctx), issueTicket(ctx))
	assert.NoError(t, err)
	assert.Equal(t, "<r><p>012346789</p></r>", tree.ToXML())

	// The undo of that deletion, as the copy-reinsert path builds it: one
	// content node carrying the id of the piece just tombstoned.
	undoAt := time.NewTicket(helper.TimeT(ctx).Lamport()+1, 1, time.InitialActorID)
	pos, err := tree.FindPos(6)
	assert.NoError(t, err)
	content := crdt.NewTreeNode(crdt.NewTreeNodeID(textID.CreatedAt, 5), "text", nil, "5")

	op := operations.NewTreeEdit(tree.CreatedAt(), pos, pos, []*crdt.TreeNode{content}, 0, undoAt)
	reverseOp, err := op.Execute(root, operations.OpSourceLocal, nil)
	assert.NoError(t, err)
	assert.Equal(t, "<r><p>012346789</p></r>", tree.ToXML(), "the copy is not inserted")

	// The undo stack shifts its stored indices by this size when a remote
	// edit arrives, so it has to match what the tree accepted.
	assert.Equal(t, 0, op.GetContentSize(), "an edit whose content was dropped inserted nothing")

	// Redoing must not delete a neighbour that this edit never inserted.
	if reverseOp != nil {
		redo, ok := reverseOp.(*operations.TreeEdit)
		assert.True(t, ok)
		assert.True(t, redo.FromPos().Equal(redo.ToPos()),
			"the reverse of an edit that inserted nothing spans nothing")
	}
}

// TestSplitTextRejectsOutOfRangeOffset covers the guard that keeps an
// unresolvable position from crashing the process. A position can anchor past
// the end of the node it resolves to, and slicing there panics.
func TestSplitTextRejectsOutOfRangeOffset(t *testing.T) {
	node := crdt.NewTreeNode(dummyTreeNodeID, "text", nil, "hello")

	split, _, err := node.SplitText(node.Len()+1, 0)
	assert.ErrorIs(t, err, crdt.ErrSplitOutOfRange)
	assert.Nil(t, split)
	assert.Equal(t, "hello", node.Value, "a rejected split leaves the node untouched")
}

// TestTreeNodeIDResolvesSameAfterSnapshotRebuild verifies that an id resolves
// to the same node before and after a snapshot rebuild. Otherwise a position
// anchored at that id means one thing on a live replica and another on one
// that restarted.
func TestTreeNodeIDResolvesSameAfterSnapshotRebuild(t *testing.T) {
	ctx := helper.TextChangeContext(helper.TestRoot())
	root := helper.TestRoot()
	tree, textID := createDigitTree(t, ctx, root)

	corruptWithDuplicatedNodeID(t, tree, ctx, textID)
	rebuilt := rebuildFromSnapshot(t, root)
	assert.Equal(t, tree.ToXML(), rebuilt.ToXML())

	id := crdt.NewTreeNodeID(textID.CreatedAt, 5)
	_, live := tree.NodeMapByID.Floor(id)
	_, cold := rebuilt.NodeMapByID.Floor(id)
	assert.Equal(t, live.IsRemoved(), cold.IsRemoved(),
		"live resolves to %q(removed=%v), rebuilt to %q(removed=%v)",
		live.Value, live.IsRemoved(), cold.Value, cold.IsRemoved())
}

// TestTreeEditAfterSnapshotRebuildAppliesAtDuplicatedID reproduces the failure
// that makes a document unopenable: on a tree rebuilt from a snapshot, an edit
// anchored at a duplicated id used to resolve to a node that does not span the
// anchored offset, and splitting it ran past the end of its value — a panic
// before the guard, an error after it. Either way the change cannot apply, and
// a change that cannot apply is a document that cannot load.
func TestTreeEditAfterSnapshotRebuildAppliesAtDuplicatedID(t *testing.T) {
	ctx := helper.TextChangeContext(helper.TestRoot())
	root := helper.TestRoot()
	tree, textID := createDigitTree(t, ctx, root)

	corruptWithDuplicatedNodeID(t, tree, ctx, textID)

	// Keep editing before the duplicated id: this splits the run owning
	// offsets 0..5, so the piece at offset 0 no longer spans up to offset 5.
	_, _, err := tree.EditT(4, 4, []*crdt.TreeNode{
		crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "x"),
	}, 0, helper.TimeT(ctx), issueTicket(ctx))
	assert.NoError(t, err)
	assert.Equal(t, "<r><p>012x3456789</p></r>", tree.ToXML())

	rebuilt := rebuildFromSnapshot(t, root)

	parentID := rebuilt.Root().Children()[0].ID()
	from := crdt.NewTreePos(parentID, crdt.NewTreeNodeID(textID.CreatedAt, 5))
	to := crdt.NewTreePos(parentID, crdt.NewTreeNodeID(textID.CreatedAt, 6))
	assert.NotPanics(t, func() {
		_, _, _, err := rebuilt.Edit(from, to, nil, 0, helper.TimeT(ctx), issueTicket(ctx), nil, true)
		assert.NoError(t, err)
	})
}

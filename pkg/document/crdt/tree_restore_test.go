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

	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/index"
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
		// Length is UTF-16 code units, per TreeRestoreSpan's contract.
		Length:   node.Length(),
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

	untombstoned, recreated, err := tree.Restore([]*crdt.TreeRestoreSpan{span})
	assert.NoError(t, err)
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
	untombstoned, recreated, err := tree.Restore(spans)
	assert.NoError(t, err)
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

	// Restore through Execute (identity-addressed; positions unused). A max
	// version vector places every actor's clock at the maximum, so all restore
	// tickets fall within known range and pass identity validation.
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

	untombstoned, recreated, err := tree.Restore([]*crdt.TreeRestoreSpan{orphan})
	assert.NoError(t, err)
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

// treeRestoreActorA/B are two actors distinct from InitialActorID (which
// createHelloTree uses to create nodes), so a version vector that knows only
// InitialActorID sees both actors' operations as concurrent.
var treeRestoreActorA, _ = time.ActorIDFromHex("00000000000000000000000a")
var treeRestoreActorB, _ = time.ActorIDFromHex("00000000000000000000000b")

// elementPaddingLength mirrors the unexported constant of the same name in
// pkg/index: an element node occupies its content length plus an open and a
// close token.
const elementPaddingLength = 2

// expectVisibleLength recomputes a node's VisibleLength from its subtree
// without mutating anything. It mirrors Node.PaddedLength: a removed child
// contributes nothing to its parent, an element child contributes its own
// length plus padding.
//
// NOTE: index.Node.UpdateDescendantsLength cannot be used for verification
// because it ACCUMULATES into VisibleLength rather than assigning it, so
// calling it on an already-computed tree double counts.
func expectVisibleLength(n *index.Node[*crdt.TreeNode]) int {
	if n.IsText() {
		return n.Value.Length()
	}

	sum := 0
	for _, child := range n.Children(true) {
		length := expectVisibleLength(child)
		if child.Value.IsRemoved() {
			continue
		}
		if child.IsText() {
			sum += length
		} else {
			sum += length + elementPaddingLength
		}
	}
	return sum
}

// assertLengthCacheSound checks every node's cached VisibleLength against the
// recomputation. Restore mutates tombstones in place through unremove(), which
// hand-maintains the ancestor length cache; if that bookkeeping drifts, the
// tree still renders correctly but every later index-based edit resolves to
// the wrong offset. This assertion is what catches that.
func assertLengthCacheSound(t *testing.T, tree *crdt.Tree, label string) {
	t.Helper()

	var walk func(*index.Node[*crdt.TreeNode])
	walk = func(node *index.Node[*crdt.TreeNode]) {
		want := expectVisibleLength(node)
		assert.Equal(t, want, node.VisibleLength,
			"%s: stale VisibleLength cache on %q", label, node.Type)

		if node.IsText() {
			return
		}
		for _, child := range node.Children(true) {
			walk(child)
		}
	}
	walk(tree.IndexTree.Root())
}

// TestTreeLengthCacheChecker validates assertLengthCacheSound itself against
// states produced WITHOUT any restore code. Without this, a bug in the checker
// would silently make the integrity tests below vacuous.
func TestTreeLengthCacheChecker(t *testing.T) {
	for _, tc := range []struct {
		name       string
		start, end int
	}{
		{"pristine", 0, 0},
		{"whole text deleted", 1, 6},
		{"text partially deleted (splits)", 2, 4},
		{"subtree deleted", 0, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := helper.TextChangeContext(helper.TestRoot())
			tree := createHelloTree(t, ctx)
			if tc.start != tc.end {
				_, _, err := tree.EditT(tc.start, tc.end, nil, 0, helper.TimeT(ctx), issueTicket(ctx))
				assert.NoError(t, err)
			}
			assertLengthCacheSound(t, tree, tc.name)
		})
	}
}

// TestTreeRestoreLengthCacheIntegrity pins unremove()'s ancestor bookkeeping.
// remove() decrements ancestor VisibleLength and stops at the first removed
// ancestor; unremove() must mirror that exactly, in EITHER span order, or the
// index cache silently drifts.
func TestTreeRestoreLengthCacheIntegrity(t *testing.T) {
	for _, tc := range []struct {
		name       string
		childFirst bool
	}{
		{"parent before child", false},
		{"child before parent", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := helper.TextChangeContext(helper.TestRoot())
			tree := createHelloTree(t, ctx) // <r><p>hello</p></r>
			before := tree.Root().Len()

			p := tree.Root().Children()[0]
			text := p.Children()[0]
			spans := []*crdt.TreeRestoreSpan{elementSpan(p, tree.Root()), textSpan(text, p)}
			if tc.childFirst {
				spans = []*crdt.TreeRestoreSpan{textSpan(text, p), elementSpan(p, tree.Root())}
			}

			_, _, err := tree.EditT(0, 7, nil, 0, helper.TimeT(ctx), issueTicket(ctx))
			assert.NoError(t, err)
			assert.Equal(t, "<r></r>", tree.ToXML())

			_, _, err = tree.Restore(spans)
			assert.NoError(t, err)

			assert.Equal(t, "<r><p>hello</p></r>", tree.ToXML())
			assert.Equal(t, before, tree.Root().Len(), "visible length is restored exactly")
			assertLengthCacheSound(t, tree, tc.name)
		})
	}
}

// TestTreeRestoreRecreateKeepsTreeEditable covers the recreateFromSpan path:
// after a GC purge the nodes are rebuilt from scratch and spliced in by the
// anchor ladder. A node spliced in with a wrong length cache renders fine but
// misplaces the NEXT index-based edit, so the test edits afterwards.
func TestTreeRestoreRecreateKeepsTreeEditable(t *testing.T) {
	ctx := helper.TextChangeContext(helper.TestRoot())
	root := helper.TestRoot()
	tree := createHelloTree(t, ctx) // <r><p>hello</p></r>
	root.RegisterElement(tree)
	parent := tree.CreatedAt()

	p := tree.Root().Children()[0]
	text := p.Children()[0]
	spans := []*crdt.TreeRestoreSpan{elementSpan(p, tree.Root()), textSpan(text, p)}

	from, err := tree.FindPos(0)
	assert.NoError(t, err)
	to, err := tree.FindPos(7)
	assert.NoError(t, err)
	assert.NoError(t, operations.NewTreeEdit(parent, from, to, nil, 0,
		helper.TimeT(ctx)).Execute(root, nil))
	n, err := root.GarbageCollect(helper.MaxVersionVector())
	assert.NoError(t, err)
	assert.Positive(t, n, "the deleted subtree should be purged")

	assert.NoError(t, operations.NewRestoreTreeEdit(parent, nil, nil,
		helper.TimeT(ctx), spans, crdt.RestoreModeRestore, nil).
		Execute(root, helper.MaxVersionVector()))
	assert.Equal(t, "<r><p>hello</p></r>", tree.ToXML())
	assertLengthCacheSound(t, tree, "after recreate")

	// The recreated subtree must behave like any other: an edit at index 3
	// lands between "he" and "llo".
	_, _, err = tree.EditT(3, 3, []*crdt.TreeNode{
		crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "Z"),
	}, 0, helper.TimeT(ctx), issueTicket(ctx))
	assert.NoError(t, err)
	assert.Equal(t, "<r><p>heZllo</p></r>", tree.ToXML(),
		"index-based edits resolve correctly after a recreate")
	assertLengthCacheSound(t, tree, "after edit on recreated subtree")
}

// TestTreeRestoreDocSizeSymmetry pins the GC accounting in TreeEdit.Execute.
// Restore moves a tombstone's size GC->Live via UnregisterGCPair + diff.Add,
// and retombstone moves it back. Either half getting the removedAt ticket
// wrong leaks size permanently, which only shows up as drifting doc-size
// metrics in production.
func TestTreeRestoreDocSizeSymmetry(t *testing.T) {
	ctx := helper.TextChangeContext(helper.TestRoot())
	root := helper.TestRoot()
	tree := createHelloTree(t, ctx) // <r><p>hello</p></r>
	root.RegisterElement(tree)
	parent := tree.CreatedAt()

	p := tree.Root().Children()[0]
	text := p.Children()[0]
	spans := []*crdt.TreeRestoreSpan{elementSpan(p, tree.Root()), textSpan(text, p)}

	baseline := root.DocSize()

	from, err := tree.FindPos(0)
	assert.NoError(t, err)
	to, err := tree.FindPos(7)
	assert.NoError(t, err)
	assert.NoError(t, operations.NewTreeEdit(parent, from, to, nil, 0,
		helper.TimeT(ctx)).Execute(root, nil))
	afterDelete := root.DocSize()
	assert.NotEqual(t, baseline, afterDelete, "the delete must move size Live->GC")

	// Undo: every byte comes back to Live and GC empties out.
	assert.NoError(t, operations.NewRestoreTreeEdit(parent, nil, nil,
		helper.TimeT(ctx), spans, crdt.RestoreModeRestore, nil).
		Execute(root, helper.MaxVersionVector()))
	assert.Equal(t, "<r><p>hello</p></r>", tree.ToXML())
	assert.Equal(t, baseline, root.DocSize(), "restore returns docSize to baseline")

	// Redo: RestoreModeRetombstone swaps the span sets, so the spans in the
	// restore slot are the ones re-tombstoned.
	assert.NoError(t, operations.NewRestoreTreeEdit(parent, nil, nil,
		helper.TimeT(ctx), spans, crdt.RestoreModeRetombstone, nil).
		Execute(root, helper.MaxVersionVector()))
	assert.Equal(t, "<r></r>", tree.ToXML())
	assert.Equal(t, afterDelete, root.DocSize(), "redo returns docSize to the post-delete state")
}

// TestTreeRestoreConvergesUnderConcurrentEdit is the regression test for this
// feature's whole reason to exist: reviving by identity converges where
// copy-reinsertion diverged. Two replicas apply the same three operations --
// a delete D, its undo U, and a CONCURRENT insert X by another actor -- in two
// different causally legal orders, and must agree.
func TestTreeRestoreConvergesUnderConcurrentEdit(t *testing.T) {
	tD := time.NewTicket(100, 0, treeRestoreActorA)
	tX := time.NewTicket(150, 0, treeRestoreActorB)

	// Knows InitialActorID, so the nodes' creation is causally visible, but
	// knows neither A nor B, so D and X are concurrent with each other.
	vv := helper.MaxVersionVector()

	// Both replicas are built from identical tickets, so node identities (and
	// therefore D's positions and U's span) are portable between them.
	newReplica := func() (*crdt.Tree, *change.Context, *crdt.TreePos, *crdt.TreePos, *crdt.TreeRestoreSpan) {
		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := createHelloTree(t, ctx)
		p := tree.Root().Children()[0]
		text := p.Children()[0]

		// D addresses the pristine state, as it would on the replica that
		// generated it before ever seeing X.
		from, err := tree.FindPos(1)
		assert.NoError(t, err)
		to, err := tree.FindPos(6)
		assert.NoError(t, err)
		return tree, ctx, from, to, textSpan(text, p)
	}

	applyD := func(tree *crdt.Tree, ctx *change.Context, from, to *crdt.TreePos) {
		_, _, err := tree.Edit(from, to, nil, 0, tD, issueTicket(ctx), vv)
		assert.NoError(t, err)
	}
	applyU := func(tree *crdt.Tree, span *crdt.TreeRestoreSpan) {
		_, _, err := tree.Restore([]*crdt.TreeRestoreSpan{span})
		assert.NoError(t, err)
	}
	applyX := func(tree *crdt.Tree, ctx *change.Context) {
		_, _, err := tree.EditT(3, 3, []*crdt.TreeNode{
			crdt.NewTreeNode(crdt.NewTreeNodeID(tX, 0), "text", nil, "W"),
		}, 0, tX, issueTicket(ctx))
		assert.NoError(t, err)
	}

	// Replica 1 sees X last: D -> U -> X.
	r1, ctx1, from1, to1, span1 := newReplica()
	applyD(r1, ctx1, from1, to1)
	applyU(r1, span1)
	applyX(r1, ctx1)

	// Replica 2 sees X first: X -> D -> U. U stays after D, which is the only
	// ordering causality actually requires.
	r2, ctx2, from2, to2, span2 := newReplica()
	applyX(r2, ctx2)
	applyD(r2, ctx2, from2, to2)
	applyU(r2, span2)

	assert.Equal(t, r1.ToXML(), r2.ToXML(), "replicas must converge on the same content")
	assert.Equal(t, "<r><p>heWllo</p></r>", r1.ToXML(),
		"the undo revives the original text and the concurrent insert survives")
	assertLengthCacheSound(t, r1, "replica 1")
	assertLengthCacheSound(t, r2, "replica 2")
}

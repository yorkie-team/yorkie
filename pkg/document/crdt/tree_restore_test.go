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

	untombstoned, recreated, _, _, err := tree.Restore([]*crdt.TreeRestoreSpan{span})
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
	pairs, _, rErr := tree.Retombstone(spans, helper.TimeT(ctx))
	assert.NoError(t, rErr)
	assert.NotEmpty(t, pairs, "removing live nodes yields GC pairs")
	assert.Equal(t, "<r></r>", tree.ToXML())

	// Restore (undo): the same identities come back, parent before child.
	untombstoned, recreated, _, _, err := tree.Restore(spans)
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
	root.RegisterElement(tree, root.Object())
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
	_, err = delOp.Execute(root, operations.OpSourceRemote, nil)
	assert.NoError(t, err)
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
	_, err = restoreOp.Execute(root, operations.OpSourceRemote, helper.MaxVersionVector())
	assert.NoError(t, err)

	assert.Equal(t, "<r><p>hello</p></r>", tree.ToXML(),
		"the purged subtree is recreated under its original identity")
	assert.Equal(t, 0, root.GarbageLen(), "restore leaves nothing registered for GC")
}

// TestTreeRestoreReverseFlipsDirection verifies that the reverse of an
// identity-preserving Tree edit keeps both span sets and only flips the
// direction. Undoing a restore must re-remove exactly the nodes it revived,
// still under their original identities; copying their content into a fresh
// insertion would defeat the whole point of the restore path.
func TestTreeRestoreReverseFlipsDirection(t *testing.T) {
	ctx := helper.TextChangeContext(helper.TestRoot())
	root := helper.TestRoot()
	tree := createHelloTree(t, ctx) // <r><p>hello</p></r>
	root.RegisterElement(tree, root.Object())
	parent := tree.CreatedAt()

	p := tree.Root().Children()[0]
	text := p.Children()[0]
	spans := []*crdt.TreeRestoreSpan{elementSpan(p, tree.Root()), textSpan(text, p)}

	from, err := tree.FindPos(0)
	assert.NoError(t, err)
	to, err := tree.FindPos(7)
	assert.NoError(t, err)
	delOp := operations.NewTreeEdit(parent, from, to, nil, 0, helper.TimeT(ctx))
	_, err = delOp.Execute(root, operations.OpSourceRemote, nil)
	assert.NoError(t, err)
	assert.Equal(t, "<r></r>", tree.ToXML())

	restoreOp := operations.NewRestoreTreeEdit(
		parent, from, to, helper.TimeT(ctx),
		spans, crdt.RestoreModeRestore, nil,
	)
	reverseRes, err := restoreOp.Execute(root, operations.OpSourceRemote, helper.MaxVersionVector())
	reverse := reverseRes.Reverse
	assert.NoError(t, err)
	assert.Equal(t, "<r><p>hello</p></r>", tree.ToXML())

	reverseEdit, ok := reverse.(*operations.TreeEdit)
	assert.True(t, ok, "the reverse of a restore is a TreeEdit")
	assert.Equal(t, crdt.RestoreModeRetombstone, reverseEdit.RestoreMode())
	assert.Equal(t, spans, reverseEdit.RestoreSpans(), "the span sets are carried through unchanged")
	assert.Empty(t, reverseEdit.RetombstoneSpans())
	assert.Empty(t, reverseEdit.Contents(), "a restore reverse never copy-reinserts")

	// Executing it re-removes exactly what the restore revived, and its own
	// reverse flips the direction back.
	reverseEdit.SetExecutedAt(helper.TimeT(ctx))
	redoRes, err := reverseEdit.Execute(root, operations.OpSourceRemote, helper.MaxVersionVector())
	redo := redoRes.Reverse
	assert.NoError(t, err)
	assert.Equal(t, "<r></r>", tree.ToXML())

	redoEdit, ok := redo.(*operations.TreeEdit)
	assert.True(t, ok)
	assert.Equal(t, crdt.RestoreModeRestore, redoEdit.RestoreMode())
	assert.Equal(t, spans, redoEdit.RestoreSpans())
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

	untombstoned, recreated, _, _, err := tree.Restore([]*crdt.TreeRestoreSpan{orphan})
	assert.NoError(t, err)
	assert.Empty(t, untombstoned)
	assert.Empty(t, recreated, "a node with no surviving parent is skipped (B1)")
	assert.Equal(t, "<r><p>hello</p></r>", tree.ToXML(), "tree is untouched")
}

// TestTreeRestoreTextParentSkip verifies that a span whose parent ID names a
// text node is skipped like a missing parent (B1) instead of failing the
// restore (#2168). A text node cannot hold children, and node kind never
// changes, so every replica skips the same span and they converge.
func TestTreeRestoreTextParentSkip(t *testing.T) {
	for _, isText := range []bool{false, true} {
		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := createHelloTree(t, ctx) // <r><p>hello</p></r>
		text := tree.Root().Children()[0].Children()[0]

		span := &crdt.TreeRestoreSpan{
			ID:       crdt.NewTreeNodeID(helper.TimeT(ctx), 0),
			NodeType: "p",
			IsText:   isText,
			ParentID: text.ID(),
		}
		if isText {
			span.NodeType, span.Length, span.Value = "text", 1, "x"
		}

		untombstoned, recreated, _, _, err := tree.Restore([]*crdt.TreeRestoreSpan{span})
		assert.NoError(t, err, "isText=%v", isText)
		assert.Empty(t, untombstoned)
		assert.Empty(t, recreated, "a node under a text parent is skipped")
		assert.Equal(t, "<r><p>hello</p></r>", tree.ToXML(), "tree is untouched")
	}
}

// TestTreeRestoreKindMismatchSkip verifies that a span whose IsText flag
// disagrees with its NodeType is skipped. Recreating it would build a node of
// the wrong kind: a zero-length text node, or an element carrying a Value. The
// element is not found by the text lookup, so a second copy of the span
// inserted another node under the same ID.
func TestTreeRestoreKindMismatchSkip(t *testing.T) {
	for _, isText := range []bool{false, true} {
		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := createHelloTree(t, ctx) // <r><p>hello</p></r>
		p := tree.Root().Children()[0]

		span := &crdt.TreeRestoreSpan{
			ID:       crdt.NewTreeNodeID(helper.TimeT(ctx), 0),
			NodeType: "text",
			IsText:   isText,
			ParentID: p.ID(),
		}
		if isText {
			span.NodeType, span.Length, span.Value = "p", 1, "x"
		}

		spans := []*crdt.TreeRestoreSpan{span, span}
		untombstoned, recreated, _, _, err := tree.Restore(spans)
		assert.NoError(t, err, "isText=%v", isText)
		assert.Empty(t, untombstoned)
		assert.Empty(t, recreated, "isText=%v: a mismatched span is skipped", isText)
		assert.Equal(t, "<r><p>hello</p></r>", tree.ToXML(), "isText=%v", isText)
	}
}

// TestTreeRestoreKindMismatchKeepsTombstone covers the other half of Restore's
// skip: a mismatched span naming a node that is still present as a TOMBSTONE
// must not revive it either. The guard sits before the un-tombstone branch, so
// without it the span would resurrect content its kind does not describe.
func TestTreeRestoreKindMismatchKeepsTombstone(t *testing.T) {
	ctx := helper.TextChangeContext(helper.TestRoot())
	tree := createHelloTree(t, ctx) // <r><p>hello</p></r>
	p := tree.Root().Children()[0]
	text := p.Children()[0]

	_, _, err := tree.EditT(1, 6, nil, 0, helper.TimeT(ctx), issueTicket(ctx))
	assert.NoError(t, err)
	assert.Equal(t, "<r><p></p></r>", tree.ToXML())

	// Same identity as the tombstoned text, but typed as an element.
	span := textSpan(text, p)
	span.NodeType = "p"

	untombstoned, recreated, _, _, err := tree.Restore([]*crdt.TreeRestoreSpan{span})
	assert.NoError(t, err)
	assert.Empty(t, untombstoned, "a mismatched span revives nothing")
	assert.Empty(t, recreated)
	assert.Equal(t, "<r><p></p></r>", tree.ToXML(), "the tombstone stays dead")
}

// TestTreeRestoreNegativeOffsetSkip verifies that a span whose OWN identity
// offset is negative is skipped. An offset is an index into its insertion, so
// no honest span carries one; the decoder passes it through because the same
// decoder reads stored changes, and recreateParent's claim check does not
// catch it — the floor probe for a negative offset lands on an unrelated
// earlier insertion, where the exact-offset test is false and the text
// interval test holds for every honest node. Without the guard the span is
// recreated and registered below the insertion's whole offset range (#2180).
func TestTreeRestoreNegativeOffsetSkip(t *testing.T) {
	for _, isText := range []bool{false, true} {
		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := createHelloTree(t, ctx) // <r><p>hello</p></r>
		p := tree.Root().Children()[0]
		text := p.Children()[0]

		// The identity of a real insertion, at an offset it never occupied.
		negID := crdt.NewTreeNodeID(text.ID().CreatedAt, -1)
		span := &crdt.TreeRestoreSpan{
			ID:       negID,
			NodeType: "p",
			IsText:   isText,
			ParentID: p.ID(),
		}
		if isText {
			span.NodeType, span.Length, span.Value = "text", 1, "x"
		}

		untombstoned, recreated, _, _, err := tree.Restore([]*crdt.TreeRestoreSpan{span})
		assert.NoError(t, err, "isText=%v", isText)
		assert.Empty(t, untombstoned, "isText=%v", isText)
		assert.Empty(t, recreated, "isText=%v: a negative-offset span is skipped", isText)
		assert.Equal(t, "<r><p>hello</p></r>", tree.ToXML(), "isText=%v", isText)

		_, holder := tree.NodeMapByID.Floor(negID)
		assert.True(t, holder == nil || holder.ID().Offset >= 0,
			"isText=%v: nothing is registered under a negative offset", isText)
	}
}

// TestTreeRetombstoneNegativeOffsetSkip pins the Retombstone half of the
// offset check. The span names a LIVE text run, so without the guard its
// [-1, 4) range overlaps the run and re-tombstones part of it — a span
// carrying an identity no node holds would pass either way.
func TestTreeRetombstoneNegativeOffsetSkip(t *testing.T) {
	ctx := helper.TextChangeContext(helper.TestRoot())
	tree := createHelloTree(t, ctx) // <r><p>hello</p></r>
	p := tree.Root().Children()[0]
	text := p.Children()[0]

	span := textSpan(text, p)
	span.ID = crdt.NewTreeNodeID(text.ID().CreatedAt, -1)

	pairs, _, err := tree.Retombstone([]*crdt.TreeRestoreSpan{span}, helper.TimeT(ctx))
	assert.NoError(t, err)
	assert.Empty(t, pairs, "a negative-offset span re-tombstones nothing")
	assert.Equal(t, "<r><p>hello</p></r>", tree.ToXML(), "the live run is untouched")
	assertLengthCacheSound(t, tree, "after a skipped negative-offset retombstone")
}

// TestTreeRetombstoneKindMismatchSkip pins the Retombstone half of the kind
// check. The spans name a LIVE node, so Retombstone would find and re-remove
// it without the guard; a span carrying an identity no node holds would make
// the assertions pass either way.
func TestTreeRetombstoneKindMismatchSkip(t *testing.T) {
	for _, tc := range []struct {
		name     string
		isText   bool
		nodeType string
	}{
		{"text node in an element-flagged span", false, "text"},
		{"text node typed as an element", true, "p"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := helper.TextChangeContext(helper.TestRoot())
			tree := createHelloTree(t, ctx) // <r><p>hello</p></r>
			p := tree.Root().Children()[0]
			text := p.Children()[0]

			span := textSpan(text, p)
			span.IsText, span.NodeType = tc.isText, tc.nodeType

			pairs, _, err := tree.Retombstone([]*crdt.TreeRestoreSpan{span}, helper.TimeT(ctx))
			assert.NoError(t, err)
			assert.Empty(t, pairs, "a mismatched span re-tombstones nothing")
			assert.Equal(t, "<r><p>hello</p></r>", tree.ToXML(), "tree is untouched")
		})
	}
}

// TestTreeRestoreClaimedIdentitySkip verifies that a span is skipped when the
// identity it would recreate is already held by a surviving node. Nothing on
// the wire ties a span's ID to a purged node of its own insertion, and
// putNode overwrites the live registration, so recreating under a claimed ID
// would leave two nodes under one identity and make every later change
// addressing the victim unapplicable (#2168).
func TestTreeRestoreClaimedIdentitySkip(t *testing.T) {
	t.Run("text span naming a live element", func(t *testing.T) {
		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := createHelloTree(t, ctx) // <r><p>hello</p></r>
		p := tree.Root().Children()[0]

		// findPiecesOverlapping stops at the non-text floor node, so the gap
		// branch would recreate a text node under <p>'s exact identity.
		span := &crdt.TreeRestoreSpan{
			ID:       p.ID(),
			NodeType: "text",
			IsText:   true,
			Length:   1,
			Value:    "x",
			ParentID: tree.Root().ID(),
		}

		untombstoned, recreated, _, _, err := tree.Restore([]*crdt.TreeRestoreSpan{span})
		assert.NoError(t, err)
		assert.Empty(t, untombstoned)
		assert.Empty(t, recreated, "a claimed identity is not recreated")
		assert.Equal(t, "<r><p>hello</p></r>", tree.ToXML())

		_, holder := tree.NodeMapByID.Floor(p.ID())
		assert.Same(t, p, holder, "<p> still holds its own identity")
	})

	t.Run("element span inside a live text run", func(t *testing.T) {
		ctx := helper.TextChangeContext(helper.TestRoot())
		tree := createHelloTree(t, ctx) // <r><p>hello</p></r>
		p := tree.Root().Children()[0]
		text := p.Children()[0]

		// Offset 2 falls inside "hello": the id is the one the next honest
		// split of that run mints, so creating an element there plants a
		// duplicate that split would collide with.
		span := &crdt.TreeRestoreSpan{
			ID:       crdt.NewTreeNodeID(text.ID().CreatedAt, 2),
			NodeType: "p",
			ParentID: p.ID(),
		}

		untombstoned, recreated, _, _, err := tree.Restore([]*crdt.TreeRestoreSpan{span})
		assert.NoError(t, err)
		assert.Empty(t, untombstoned)
		assert.Empty(t, recreated, "an id inside a live run is not recreated")
		assert.Equal(t, "<r><p>hello</p></r>", tree.ToXML())

		// The honest split that mints that id still works afterwards.
		_, _, err = tree.EditT(3, 3, []*crdt.TreeNode{
			crdt.NewTreeNode(helper.PosT(ctx), "text", nil, "Z"),
		}, 0, helper.TimeT(ctx), issueTicket(ctx))
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>heZllo</p></r>", tree.ToXML())
		assertLengthCacheSound(t, tree, "after split at a would-be duplicate id")
	})
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
	root.RegisterElement(tree, root.Object())
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
	_, err := op.Execute(root, operations.OpSourceRemote, vv)
	assert.ErrorIs(t, err, operations.ErrUnknownRestoreIdentity)
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

			_, _, _, _, err = tree.Restore(spans)
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
	root.RegisterElement(tree, root.Object())
	parent := tree.CreatedAt()

	p := tree.Root().Children()[0]
	text := p.Children()[0]
	spans := []*crdt.TreeRestoreSpan{elementSpan(p, tree.Root()), textSpan(text, p)}

	from, err := tree.FindPos(0)
	assert.NoError(t, err)
	to, err := tree.FindPos(7)
	assert.NoError(t, err)
	_, err = operations.NewTreeEdit(parent, from, to, nil, 0,
		helper.TimeT(ctx)).Execute(root, operations.OpSourceRemote, nil)
	assert.NoError(t, err)
	n, err := root.GarbageCollect(helper.MaxVersionVector())
	assert.NoError(t, err)
	assert.Positive(t, n, "the deleted subtree should be purged")

	_, err = operations.NewRestoreTreeEdit(parent, nil, nil,
		helper.TimeT(ctx), spans, crdt.RestoreModeRestore, nil).
		Execute(root, operations.OpSourceRemote, helper.MaxVersionVector())
	assert.NoError(t, err)
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
	root.RegisterElement(tree, root.Object())
	parent := tree.CreatedAt()

	p := tree.Root().Children()[0]
	text := p.Children()[0]
	spans := []*crdt.TreeRestoreSpan{elementSpan(p, tree.Root()), textSpan(text, p)}

	baseline := root.DocSize()

	from, err := tree.FindPos(0)
	assert.NoError(t, err)
	to, err := tree.FindPos(7)
	assert.NoError(t, err)
	_, err = operations.NewTreeEdit(parent, from, to, nil, 0,
		helper.TimeT(ctx)).Execute(root, operations.OpSourceRemote, nil)
	assert.NoError(t, err)
	afterDelete := root.DocSize()
	assert.NotEqual(t, baseline, afterDelete, "the delete must move size Live->GC")

	// Undo: every byte comes back to Live and GC empties out.
	_, err = operations.NewRestoreTreeEdit(parent, nil, nil,
		helper.TimeT(ctx), spans, crdt.RestoreModeRestore, nil).
		Execute(root, operations.OpSourceRemote, helper.MaxVersionVector())
	assert.NoError(t, err)
	assert.Equal(t, "<r><p>hello</p></r>", tree.ToXML())
	assert.Equal(t, baseline, root.DocSize(), "restore returns docSize to baseline")

	// Redo: RestoreModeRetombstone swaps the span sets, so the spans in the
	// restore slot are the ones re-tombstoned.
	_, err = operations.NewRestoreTreeEdit(parent, nil, nil,
		helper.TimeT(ctx), spans, crdt.RestoreModeRetombstone, nil).
		Execute(root, operations.OpSourceRemote, helper.MaxVersionVector())
	assert.NoError(t, err)
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
		_, _, _, err := tree.Edit(from, to, nil, 0, tD, issueTicket(ctx), vv, true)
		assert.NoError(t, err)
	}
	applyU := func(tree *crdt.Tree, span *crdt.TreeRestoreSpan) {
		_, _, _, _, err := tree.Restore([]*crdt.TreeRestoreSpan{span})
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

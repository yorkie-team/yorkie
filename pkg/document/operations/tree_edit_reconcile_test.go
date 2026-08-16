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

package operations

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/index"
)

// newUndoTreeEdit builds a TreeEdit with the given fromIdx/toIdx, marked as
// an undo/redo entry, the way toReverseOperation's copy-reinsert outcome
// does. White-box construction, mirroring TestEditReconcileOperationCases in
// edit_reconcile_test.go: a real copy-reinsert reverse with a wide
// [fromIdx, toIdx) range is reachable (see TestTreeEditReconcileOperationRealistic
// below), but only through a fallback ordinary single-client editing never
// takes (see TestTreeEditCopyReinsertFallback's doc comment). Constructing it
// directly here pins the ported overlap formula exactly regardless.
func newUndoTreeEdit(localFrom, localTo int) *TreeEdit {
	from, to := localFrom, localTo
	return &TreeEdit{isUndoOp: true, fromIdx: &from, toIdx: &to}
}

// TestTreeEditReconcileOperationCases pins the exact index arithmetic of
// TreeEdit.ReconcileOperation against every overlap case
// tree_edit_operation.ts:735-822 implements (the same six cases
// Edit.ReconcileOperation implements for Text, over integer indices), plus
// its three early-return guards.
func TestTreeEditReconcileOperationCases(t *testing.T) {
	// assertRange reads fromIdx/toIdx directly rather than through
	// NormalizePos: NormalizePos only reports them when isUndoOp is also
	// true (see the "guard: not an undo op" case below, where it is not),
	// so a direct field read is what actually pins whether
	// ReconcileOperation touched the stored range.
	assertRange := func(t *testing.T, e *TreeEdit, from, to int) {
		t.Helper()
		assert.Equal(t, from, *e.fromIdx)
		assert.Equal(t, to, *e.toIdx)
	}

	t.Run("guard: not an undo op leaves the range untouched", func(t *testing.T) {
		from, to := 10, 20
		e := &TreeEdit{fromIdx: &from, toIdx: &to, isUndoOp: false}
		e.ReconcileOperation(0, 100, 5)
		assertRange(t, e, 10, 20)
	})

	t.Run("guard: an identity-preserving op is never index-reconciled", func(t *testing.T) {
		from, to := 10, 20
		e := &TreeEdit{
			fromIdx:      &from,
			toIdx:        &to,
			isUndoOp:     true,
			restoreSpans: []*crdt.TreeRestoreSpan{{}},
		}
		e.ReconcileOperation(0, 100, 5)
		assertRange(t, e, 10, 20)
	})

	t.Run("guard: an inverted remote range (remoteFrom > remoteTo) is ignored", func(t *testing.T) {
		e := newUndoTreeEdit(10, 20)
		e.ReconcileOperation(15, 5, 3)
		assertRange(t, e, 10, 20)
	})

	t.Run("guard: an undo op with no captured range does not panic", func(t *testing.T) {
		// The zero-value case: an undo/redo entry whose fromIdx/toIdx were
		// never captured (e.g. NormalizePos degraded to (0, 0) upstream, see
		// its own doc comment). fromIdx/toIdx must stay nil, not get
		// dereferenced.
		e := &TreeEdit{isUndoOp: true}
		e.ReconcileOperation(0, 100, 5)
		assert.Nil(t, e.fromIdx)
		assert.Nil(t, e.toIdx)
	})

	t.Run("case 1: remote edit left of the undo range shifts it", func(t *testing.T) {
		// [--remote--]  [--undo--]
		e := newUndoTreeEdit(10, 20)
		e.ReconcileOperation(2, 6, 1)
		assertRange(t, e, 7, 17)
	})

	t.Run("case 2: remote edit right of the undo range is a no-op", func(t *testing.T) {
		// [--undo--]  [--remote--]
		e := newUndoTreeEdit(10, 20)
		e.ReconcileOperation(25, 30, 2)
		assertRange(t, e, 10, 20)
	})

	t.Run("case 3: undo range contained within the remote range collapses", func(t *testing.T) {
		// [-------remote-------]
		//      [--undo--]
		e := newUndoTreeEdit(10, 20)
		e.ReconcileOperation(5, 25, 3)
		assertRange(t, e, 5, 5)
	})

	t.Run("case 4: remote range contained within the undo range shrinks it", func(t *testing.T) {
		//      [--remote--]
		// [---------undo---------]
		e := newUndoTreeEdit(10, 20)
		e.ReconcileOperation(12, 15, 1)
		assertRange(t, e, 10, 18)
	})

	t.Run("case 5: remote range overlaps the start of the undo range", func(t *testing.T) {
		// [---remote---]
		//      [---undo---]
		e := newUndoTreeEdit(10, 20)
		e.ReconcileOperation(5, 15, 9)
		assertRange(t, e, 5, 10)
	})

	t.Run("case 6: remote range overlaps the end of the undo range", func(t *testing.T) {
		//      [---remote---]
		// [---undo---]
		e := newUndoTreeEdit(10, 20)
		e.ReconcileOperation(15, 25, 4)
		assertRange(t, e, 10, 15)
	})

	// Case 7 in history_tree_test.ts ("adjacent") is a remote insert sitting
	// exactly at one of the undo range's own boundaries -- not a distinct
	// formula, but the edge between two of the cases above, so both edges are
	// pinned here: a remote insert at the undo range's start boundary is
	// still entirely "to the left" (Case 1's remoteTo <= localFrom uses <=,
	// not <), and one at its end boundary is still entirely "to the right"
	// (Case 2's localTo <= remoteFrom, same). Ties resolve toward "no
	// overlap" on both edges, so an insert exactly at either boundary lands
	// unambiguously on one side, never straddling into Cases 3-6.
	t.Run("case 7 (adjacent, start edge): a remote insert at the undo range's start is case 1", func(t *testing.T) {
		// [--remote--)[--undo--]
		e := newUndoTreeEdit(10, 20)
		e.ReconcileOperation(10, 10, 2)
		assertRange(t, e, 12, 22)
	})

	t.Run("case 7 (adjacent, end edge): a remote insert at the undo range's end is case 2", func(t *testing.T) {
		// [--undo--][--remote--)
		e := newUndoTreeEdit(10, 20)
		e.ReconcileOperation(20, 20, 2)
		assertRange(t, e, 10, 20)
	})

	t.Run("never touches restoreSpans or retombstoneSpans", func(t *testing.T) {
		// The guard above already proves reconciliation skips an identity op
		// entirely; this additionally proves ReconcileOperation's own body
		// contains no assignment to either field, so a future change to the
		// early-return guard could not accidentally start rewriting the
		// identity payload underneath it.
		from, to := 10, 20
		spans := []*crdt.TreeRestoreSpan{{}}
		e := &TreeEdit{
			fromIdx:          &from,
			toIdx:            &to,
			isUndoOp:         true,
			restoreSpans:     spans,
			retombstoneSpans: spans,
		}
		e.ReconcileOperation(0, 5, 1)
		assert.Same(t, &spans[0], &e.restoreSpans[0])
		assert.Same(t, &spans[0], &e.retombstoneSpans[0])
	})
}

// buildDigitTree returns a fresh CRDT tree holding <r><p>0123456789</p></r>,
// mirroring the worked example history_tree_test.ts's "reconcile cases"
// section uses (Initial tree: <doc><p>0123456789</p></doc>).
func buildDigitTree(t *testing.T, issue func() *time.Ticket) *crdt.Tree {
	t.Helper()

	root := crdt.NewTreeNode(crdt.NewTreeNodeID(issue(), 0), "r", nil)
	p := crdt.NewTreeNode(crdt.NewTreeNodeID(issue(), 0), "p", nil)
	assert.NoError(t, p.Append(
		crdt.NewTreeNode(crdt.NewTreeNodeID(issue(), 0), index.TextNodeType, nil, "0123456789"),
	))
	assert.NoError(t, root.Append(p))

	tree := crdt.NewTree(root, issue())
	assert.Equal(t, "<r><p>0123456789</p></r>", tree.ToXML())

	return tree
}

// newTreeReconcileTestRoot wraps buildDigitTree's tree in a Root under key
// "t", so a TreeEdit reverse can be executed for real (Execute resolves its
// parentCreatedAt against a Root, not a bare Tree).
func newTreeReconcileTestRoot(t *testing.T, issue func() *time.Ticket) (*crdt.Root, *crdt.Tree) {
	t.Helper()

	tree := buildDigitTree(t, issue)
	obj := crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket)
	obj.Set("t", tree)

	return crdt.NewRoot(obj), tree
}

// dropSpansForReconcileTest makes info look like an edit whose identity spans
// were incomplete, forcing toReverseOperation's copy-reinsert fallback the
// same way TestTreeEditCopyReinsertFallback does. Ordinary single-client
// editing always produces complete spans (SpansComplete true), which take
// the identity-preserving path instead -- a path ReconcileOperation's own
// guard always skips (see the "guard: an identity-preserving op" case
// above), so exercising the arithmetic end-to-end requires this fallback.
func dropSpansForReconcileTest(info crdt.TreeEditReverseInfo) crdt.TreeEditReverseInfo {
	info.SpansComplete = false
	info.RemovedSpans = nil
	info.InsertedSpans = nil
	return info
}

// TestTreeEditReconcileOperationRealistic exercises NormalizePos and
// ReconcileOperation together against a genuinely executed reverse TreeEdit,
// then re-executes it to confirm the reconciled range -- not the stale one
// this op was built with -- is what the tree actually mutates.
//
// Tree "0123456789" under <p>; a pending undo of deleting [7,9) ("67")
// anchors at 7. A remote edit inserts "XX" at 3, entirely to its left. Case 1
// must shift the anchor right by 2 (the net length of the remote insert), to
// 9 -- and the reverse, once executed, must insert "67" back at index 9 of
// the POST-remote-edit tree "01XX234589", landing it in the same relative
// place it came from: "01XX2345" + "67" + "89" = "01XX23456789".
func TestTreeEditReconcileOperationRealistic(t *testing.T) {
	issue := ticketer()
	root, tree := newTreeReconcileTestRoot(t, issue)

	info := editAt(t, tree, 7, 9, nil, issue)
	assert.Equal(t, "<r><p>01234589</p></r>", tree.ToXML())

	op := NewTreeEdit(tree.CreatedAt(), nil, nil, nil, 0, issue())
	reverse, err := op.toReverseOperation(tree, nil, dropSpansForReconcileTest(info), 7)
	assert.NoError(t, err)

	edit, ok := reverse.(*TreeEdit)
	assert.True(t, ok)
	assert.True(t, edit.isUndoOp)
	assert.Empty(t, edit.RestoreSpans(), "the fallback copies rather than restoring by identity")
	assert.Len(t, edit.Contents(), 1)
	assert.Equal(t, "67", edit.Contents()[0].Value)

	from, to := edit.NormalizePos()
	assert.Equal(t, 7, from, "pending undo anchors at the deleted range's start")
	assert.Equal(t, from, to, "nothing was inserted, so the range is zero-width")

	// Remote: insert "XX" at index 3, entirely to the left of the anchor.
	remoteContent := crdt.NewTreeNode(crdt.NewTreeNodeID(issue(), 0), index.TextNodeType, nil, "XX")
	remoteInfo := editAt(t, tree, 3, 3, []*crdt.TreeNode{remoteContent}, issue)
	assert.Equal(t, "<r><p>01XX234589</p></r>", tree.ToXML())
	assert.Equal(t, 2, remoteInfo.InsertedContentSize)

	edit.ReconcileOperation(3, 3, remoteInfo.InsertedContentSize)
	from, to = edit.NormalizePos()
	assert.Equal(t, 9, from, "anchor should shift right by 2: the remote insert, entirely ahead of it")
	assert.Equal(t, 9, to)

	// Executing the reconciled reverse must insert "67" at the RECONCILED
	// position (9 in the post-remote-edit tree), not the stale one (7) this
	// op was built with. This is the assertion that would fail if the
	// fromIdx/toIdx -> from/to conversion in Execute were missing or wrong --
	// reconciliation would otherwise be inert bookkeeping nobody reads.
	edit.SetExecutedAt(issue())
	_, err = edit.Execute(root, OpSourceUndoRedo, nil)
	assert.NoError(t, err)
	assert.Equal(t, "<r><p>01XX23456789</p></r>", tree.ToXML())
}

// TestTreeEditNormalizePosForwardExecution exercises NormalizePos on a
// genuinely executed FORWARD TreeEdit -- neither an undo/redo entry (whose
// fromIdx/toIdx TestTreeEditReconcileOperationCases already pins as
// literals) nor a hand-built reverse (whose preFromIdx
// TestTreeEditReconcileOperationRealistic passes in directly). This is the
// applyChanges reconciliation loop's OTHER input: for every op in a change's
// executed list, NormalizePos reports the range that op's own execution just
// affected, so OTHER stacked entries can be reconciled against it (see the
// case *operations.TreeEdit branch in Document.applyChanges).
//
// Both cases below start from a FRESH, unsplit text node, matching the
// scenario found in review: a position interior to a node that
// FindTreeNodesWithSplitText has not yet split. lastFromIdx/lastToIdx must
// read as the visible index the position actually names, not the coarser
// value a non-splitting resolver (Tree.ToTreeNodes, which the prior
// implementation used) collapses an interior position to -- that resolver
// documents itself as returning "the node that CONTAINS the position", which
// for an unsplit 10-character node is the same node regardless of where
// inside it the position sits, and ToIndex then reports that whole node's
// end.
func TestTreeEditNormalizePosForwardExecution(t *testing.T) {
	t.Run("insert reports the visible index it inserted at", func(t *testing.T) {
		issue := ticketer()
		root, tree := newTreeReconcileTestRoot(t, issue)

		pos, err := tree.FindPos(3)
		assert.NoError(t, err)
		content := crdt.NewTreeNode(crdt.NewTreeNodeID(issue(), 0), index.TextNodeType, nil, "XX")
		op := NewTreeEdit(tree.CreatedAt(), pos, pos, []*crdt.TreeNode{content}, 0, issue())

		_, err = op.Execute(root, OpSourceLocal, nil)
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>01XX23456789</p></r>", tree.ToXML())

		from, to := op.NormalizePos()
		assert.Equal(t, 3, from, "must be the pre-edit visible index of the insertion point")
		assert.Equal(t, 3, to)
		assert.Equal(t, 2, op.GetContentSize())
	})

	t.Run("delete reports the visible range it removed", func(t *testing.T) {
		issue := ticketer()
		root, tree := newTreeReconcileTestRoot(t, issue)

		fromPos, err := tree.FindPos(3)
		assert.NoError(t, err)
		toPos, err := tree.FindPos(5)
		assert.NoError(t, err)
		op := NewTreeEdit(tree.CreatedAt(), fromPos, toPos, nil, 0, issue())

		_, err = op.Execute(root, OpSourceLocal, nil)
		assert.NoError(t, err)
		assert.Equal(t, "<r><p>01456789</p></r>", tree.ToXML())

		from, to := op.NormalizePos()
		assert.Equal(t, 3, from, "must be the pre-delete visible index of the range's start")
		assert.Equal(t, 5, to, "must be the pre-delete visible index of the range's end")
		assert.Equal(t, 0, op.GetContentSize(), "nothing was inserted")
	})
}

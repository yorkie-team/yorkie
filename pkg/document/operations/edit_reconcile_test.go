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
)

// TestEditReconcileOperationCases pins the exact position arithmetic of
// ReconcileOperation against every overlap case edit_operation.ts:349-410
// implements, plus its two early-return guards. It uses package-internal
// (white-box) access because an isUndoOp Edit with a non-zero-width
// [from, to) range cannot be produced through the exported API: every
// reverse Edit toReverseOperation builds sets to == from (see the
// reachability note below), so the only way to drive Cases 4-6 -- which
// require from != to -- is to construct the struct literal directly.
//
// Reachability note: in the current implementation (both SDKs), every
// isUndoOp Edit that ever reaches a history stack has from == to --
// restoreSpans/retombstoneSpans address the actual content by identity, and
// from/to only serve as a zero-width fallback anchor (see the NOTE in
// ReconcileOperation). That makes Cases 4-6 unreachable through real
// document usage today; they are tested here directly so the ported
// formula matches JS exactly regardless, and so a future change that
// produces a wide isUndoOp range does not silently regress this logic.
// TestEditReconcileOperationRealistic below exercises Case 1 against a
// genuinely executed reverse Edit; the two-client "reconcile cases" suite
// in test/integration exercises the same scenarios JS names Case 1-7, but
// -- because a zero-width local range collapses several of these
// geometrically-distinct scenarios onto the same branch here (see that
// test's doc comment) -- asserts convergence, matching what those
// scenarios can actually prove, rather than claiming to pin one case each.
func TestEditReconcileOperationCases(t *testing.T) {
	id := crdt.InitialTextNode().ID()
	pos := func(offset int) *crdt.RGATreeSplitNodePos {
		return crdt.NewRGATreeSplitNodePos(id, offset)
	}

	newUndoEdit := func(localFrom, localTo int) *Edit {
		return &Edit{
			from:     pos(localFrom),
			to:       pos(localTo),
			isUndoOp: true,
		}
	}

	t.Run("guard: not an undo op leaves the position untouched", func(t *testing.T) {
		e := &Edit{from: pos(10), to: pos(20), isUndoOp: false}
		e.ReconcileOperation(0, 100, 5)
		assert.Equal(t, 10, e.From().RelativeOffset())
		assert.Equal(t, 20, e.To().RelativeOffset())
	})

	t.Run("guard: an inverted remote range (remoteFrom > remoteTo) is ignored", func(t *testing.T) {
		e := newUndoEdit(10, 20)
		e.ReconcileOperation(15, 5, 3)
		assert.Equal(t, 10, e.From().RelativeOffset())
		assert.Equal(t, 20, e.To().RelativeOffset())
	})

	t.Run("case 1: remote edit left of the undo range shifts it", func(t *testing.T) {
		// [--remote--]  [--undo--]
		e := newUndoEdit(10, 20)
		e.ReconcileOperation(2, 6, 1)
		assert.Equal(t, 7, e.From().RelativeOffset())
		assert.Equal(t, 17, e.To().RelativeOffset())
	})

	t.Run("case 2: remote edit right of the undo range is a no-op", func(t *testing.T) {
		// [--undo--]  [--remote--]
		e := newUndoEdit(10, 20)
		e.ReconcileOperation(25, 30, 2)
		assert.Equal(t, 10, e.From().RelativeOffset())
		assert.Equal(t, 20, e.To().RelativeOffset())
	})

	t.Run("case 3: undo range contained within the remote range collapses", func(t *testing.T) {
		// [-------remote-------]
		//      [--undo--]
		e := newUndoEdit(10, 20)
		e.ReconcileOperation(5, 25, 3)
		assert.Equal(t, 5, e.From().RelativeOffset())
		assert.Equal(t, 5, e.To().RelativeOffset())
	})

	t.Run("case 4: remote range contained within the undo range shrinks it", func(t *testing.T) {
		//      [--remote--]
		// [---------undo---------]
		e := newUndoEdit(10, 20)
		e.ReconcileOperation(12, 15, 1)
		assert.Equal(t, 10, e.From().RelativeOffset())
		assert.Equal(t, 18, e.To().RelativeOffset())
	})

	t.Run("case 5: remote range overlaps the start of the undo range", func(t *testing.T) {
		// [---remote---]
		//      [---undo---]
		e := newUndoEdit(10, 20)
		e.ReconcileOperation(5, 15, 9)
		assert.Equal(t, 5, e.From().RelativeOffset())
		assert.Equal(t, 10, e.To().RelativeOffset())
	})

	t.Run("case 6: remote range overlaps the end of the undo range", func(t *testing.T) {
		//      [---remote---]
		// [---undo---]
		e := newUndoEdit(10, 20)
		e.ReconcileOperation(15, 25, 4)
		assert.Equal(t, 10, e.From().RelativeOffset())
		assert.Equal(t, 15, e.To().RelativeOffset())
	})
}

// newReconcileTestRoot builds a root holding a Text under key "t" with
// content "0123456789", mirroring the worked example in
// edit_operation.ts:363-367 that motivates ReconcileOperation, so
// NormalizePos and ReconcileOperation can be exercised together against
// positions produced by real CRDT execution rather than hand-built structs.
func newReconcileTestRoot(t *testing.T, actor *time.ActorID) (*crdt.Root, *crdt.Text) {
	t.Helper()

	text := crdt.NewText(crdt.NewRGATreeSplit(crdt.InitialTextNode()), time.InitialTicket)
	fromPos, toPos, err := text.CreateRange(0, 0)
	assert.NoError(t, err)
	_, _, _, _, _, err = text.Edit(
		fromPos, toPos, "0123456789", nil, time.NewTicket(1, 0, *actor), nil,
	)
	assert.NoError(t, err)

	obj := crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket)
	obj.Set("t", text)
	root := crdt.NewRoot(obj)

	return root, text
}

// TestEditReconcileOperationRealistic exercises NormalizePos and
// ReconcileOperation together against a genuinely executed reverse Edit,
// confirming (a) that a real isUndoOp reverse is always zero-width (from ==
// to), matching the reachability note above, and (b) that Case 1 produces
// the exact offset shift a concurrent remote edit of different net length
// requires.
//
// Text "0123456789"; a pending undo of deleting [4,6) ("45") anchors at 4.
// A remote edit replaces [2,4) ("23") with "Q" -- two characters removed,
// one inserted, a net length change of -1 -- entirely to the left of that
// anchor. Case 1 must shift it left by exactly 1, to offset 3.
//
// This scenario also pins a finding from building it: NormalizePos is not
// simply "count live characters up to here" -- a pure delete's from/to
// always normalize to the same offset (nothing but the deleted content sat
// between them, and it is now worth 0), so it is the mixed-length replace
// here, not a pure delete, that is needed to observe a nonzero shift at
// all. And even that shift is not required for *content* correctness in
// this scenario: restoreSpans revives "45" by identity regardless of where
// e.from/e.to point (confirmed by re-running this scenario with the
// ReconcileOperation call below removed -- the resulting text is
// unchanged). The position assertions below are the only thing in this
// test that would fail if reconciliation silently stopped running; content
// alone would not catch it.
func TestEditReconcileOperationRealistic(t *testing.T) {
	actor, _ := time.ActorIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaa")
	root, text := newReconcileTestRoot(t, &actor)

	// Both ranges are resolved against the pristine, unsplit text up front,
	// mirroring two genuinely concurrent clients: each anchors its edit
	// against the shared state as it stood before either edit executed,
	// not against a chain the other edit has already split.
	fromPos, toPos, err := text.CreateRange(4, 6)
	assert.NoError(t, err)
	remoteFromPos, remoteToPos, err := text.CreateRange(2, 4)
	assert.NoError(t, err)

	deleteOp := NewEdit(text.CreatedAt(), fromPos, toPos, "", nil, time.NewTicket(2, 0, actor))
	reverseRes, err := deleteOp.Execute(root, OpSourceLocal, time.NewVersionVector())
	reverse := reverseRes.Reverse
	assert.NoError(t, err)

	reverseEdit, ok := reverse.(*Edit)
	assert.True(t, ok, "reverse of a delete-only edit should be an Edit")
	assert.True(t, reverseEdit.isUndoOp)
	assert.NotEmpty(t, reverseEdit.RestoreSpans(), "delete of live content restores by identity")

	from, to, ok := reverseEdit.NormalizePos(root)
	assert.True(t, ok)
	assert.Equal(t, 4, from, "pending undo anchors at the deleted range's start")
	assert.Equal(t, from, to, "a restore-mode reverse is always zero-width")

	// Remote: replace [2,4) ("23") with "Q" -- delete 2, insert 1.
	remoteOp := NewEdit(text.CreatedAt(), remoteFromPos, remoteToPos, "Q", nil, time.NewTicket(3, 0, actor))
	_, err = remoteOp.Execute(root, OpSourceRemote, time.NewVersionVector())
	assert.NoError(t, err)
	assert.Equal(t, "01Q6789", text.String())

	remoteFrom, remoteTo, ok := remoteOp.NormalizePos(root)
	assert.True(t, ok)
	assert.Equal(t, 2, remoteFrom)
	assert.Equal(t, 4, remoteTo)
	assert.Equal(t, 1, remoteOp.ContentLen())

	reverseEdit.ReconcileOperation(remoteFrom, remoteTo, remoteOp.ContentLen())
	assert.Equal(t, 3, reverseEdit.From().RelativeOffset(),
		"anchor should shift left by 1: 2 characters removed, 1 inserted, ahead of it")
	assert.Equal(t, 3, reverseEdit.To().RelativeOffset())

	// Executing the reconciled reverse restores "45" at the correct spot
	// regardless -- restoreSpans addresses it by identity, not by this
	// position. This end-to-end check is a sanity net, not the regression
	// guard; see the doc comment above for why the position assertions
	// above are load-bearing and this one alone would not be.
	reverseEdit.SetExecutedAt(time.NewTicket(4, 0, actor))
	_, err = reverseEdit.Execute(root, OpSourceUndoRedo, time.NewVersionVector())
	assert.NoError(t, err)
	assert.Equal(t, "01Q456789", text.String())
}

//go:build integration

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

package integration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestHistoryTextReconcile ports the JS SDK's "Text History - reconcile
// cases" describe block (history_text_test.ts:437-775): two clients each
// hold one pending undo entry (their own single edit), a concurrent remote
// edit from the other client arrives, and both undo then redo their own
// entry. Each case reproduces the geometric scenario JS names Case 1-7.
//
// These assert convergence only, matching the JS suite -- not because a
// weaker check was convenient, but because that is what this scenario
// shape can actually prove. Two things it does not prove: first, per
// Edit.ReconcileOperation's reachability note (pkg/document/operations),
// a stacked reverse Edit's from/to are always equal (restoreSpans locates
// content by identity), so a pure remote delete's normalized range also
// collapses to a single point (see the same note) -- meaning several of
// these geometrically distinct scenarios (e.g. Case 4's remote-insert-
// inside-undo-range) do not necessarily exercise the branch their name
// suggests; TestHistoryTextReconcilePosition and
// TestEditReconcileOperationRealistic pin one scenario end to end,
// including the position, where this suite only checks convergence.
// Second, for the two overlapping-delete cases (3 and 5), content
// correctness itself is an open question -- see
// TestReconcileOverlappingUndoDuplicatesContent below.
func TestHistoryTextReconcile(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	type step struct {
		name       string
		localEdit  func(root *json.Object)
		remoteEdit func(root *json.Object)
	}

	cases := []step{
		{
			name: "case 1 (left): remote edit left of undo should shift position",
			localEdit: func(root *json.Object) {
				root.GetText("t").Edit(6, 8, "")
			},
			remoteEdit: func(root *json.Object) {
				root.GetText("t").Edit(2, 2, "XX")
			},
		},
		{
			name: "case 2 (right): remote edit right of undo should not affect",
			localEdit: func(root *json.Object) {
				root.GetText("t").Edit(2, 4, "")
			},
			remoteEdit: func(root *json.Object) {
				root.GetText("t").Edit(8, 8, "YY")
			},
		},
		{
			name: "case 3 (contained_by): undo range contained by remote should collapse",
			localEdit: func(root *json.Object) {
				root.GetText("t").Edit(4, 6, "")
			},
			remoteEdit: func(root *json.Object) {
				root.GetText("t").Edit(2, 8, "")
			},
		},
		{
			name: "case 4 (contains): remote range contained by undo should adjust",
			localEdit: func(root *json.Object) {
				root.GetText("t").Edit(2, 8, "")
			},
			remoteEdit: func(root *json.Object) {
				root.GetText("t").Edit(5, 5, "ZZ")
			},
		},
		{
			name: "case 5 (overlap_start): remote overlaps start of undo range",
			localEdit: func(root *json.Object) {
				root.GetText("t").Edit(4, 8, "")
			},
			remoteEdit: func(root *json.Object) {
				root.GetText("t").Edit(2, 6, "")
			},
		},
		{
			name: "case 6 (overlap_end): remote overlaps end of undo range",
			localEdit: func(root *json.Object) {
				root.GetText("t").Edit(2, 6, "")
			},
			remoteEdit: func(root *json.Object) {
				root.GetText("t").Edit(4, 8, "")
			},
		},
		{
			name: "case 7 (adjacent): adjacent edits at boundary",
			localEdit: func(root *json.Object) {
				root.GetText("t").Edit(4, 6, "")
			},
			remoteEdit: func(root *json.Object) {
				root.GetText("t").Edit(6, 6, "AA")
			},
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()

			d1 := document.New(helper.TestKey(t))
			assert.NoError(t, c1.Attach(ctx, d1))
			defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
			d2 := document.New(helper.TestKey(t))
			assert.NoError(t, c2.Attach(ctx, d2))
			defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

			assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewText("t").Edit(0, 0, "0123456789")
				return nil
			}, "init"))
			assert.NoError(t, c1.Sync(ctx))
			assert.NoError(t, c2.Sync(ctx))

			assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
				c.localEdit(root)
				return nil
			}, "d1 edit"))
			assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
				c.remoteEdit(root)
				return nil
			}, "d2 edit"))

			assert.NoError(t, c1.Sync(ctx))
			assert.NoError(t, c2.Sync(ctx))
			assert.NoError(t, c1.Sync(ctx))
			assert.Equal(t, d1.Marshal(), d2.Marshal(), "mismatch after both edits")

			assert.NoError(t, d1.Undo())
			assert.NoError(t, d2.Undo())
			assert.NoError(t, c1.Sync(ctx))
			assert.NoError(t, c2.Sync(ctx))
			assert.NoError(t, c1.Sync(ctx))
			assert.Equal(t, d1.Marshal(), d2.Marshal(), "mismatch after both undos")

			assert.NoError(t, d1.Redo())
			assert.NoError(t, d2.Redo())
			assert.NoError(t, c1.Sync(ctx))
			assert.NoError(t, c2.Sync(ctx))
			assert.NoError(t, c1.Sync(ctx))
			assert.Equal(t, d1.Marshal(), d2.Marshal(), "mismatch after both redos")
		})
	}
}

// TestReconcileOverlappingUndoDuplicatesContent ports history_text_test.ts's
// two `it.skip` correctness cases (:705 "Case 3 correctness" and :742 "Case
// 5 correctness"): when two clients concurrently delete overlapping ranges
// and each undoes its own delete, both should converge back to the exact
// original "0123456789".
//
// JS still skips these, but that skip is stale: it was added by JS #1222
// (2026-04-17) against the then-current deep-copy-reinsert undo mechanism,
// and JS #1293 "Identity-preserving restore for Text undo/redo"
// (2026-07-23) replaced exactly that mechanism with the
// restoreSpans/retombstoneSpans identity-addressed restore this port also
// uses -- an ancestor of v0.7.16, the version this port targets. Nobody
// re-ran the skipped pair after #1293 landed (`git log
// 4b00927c..HEAD -- history_text_test.ts` in yorkie-js-sdk is empty). So Go
// did not diverge from JS here: it inherited JS's own fix, and JS's skip is
// a leftover from before that fix. These run live, pinning the
// identity-preserving restore against that stale skip -- a failure here is
// a genuine regression, not a known limitation.
func TestReconcileOverlappingUndoDuplicatesContent(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	cases := []struct {
		name       string
		localEdit  func(root *json.Object)
		remoteEdit func(root *json.Object)
	}{
		{
			name: "case 3 correctness: both undo of overlapping deletes should restore original",
			localEdit: func(root *json.Object) {
				root.GetText("t").Edit(4, 6, "")
			},
			remoteEdit: func(root *json.Object) {
				root.GetText("t").Edit(2, 8, "")
			},
		},
		{
			name: "case 5 correctness: both undo of partially overlapping deletes should restore original",
			localEdit: func(root *json.Object) {
				root.GetText("t").Edit(4, 8, "")
			},
			remoteEdit: func(root *json.Object) {
				root.GetText("t").Edit(2, 6, "")
			},
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()

			d1 := document.New(helper.TestKey(t))
			assert.NoError(t, c1.Attach(ctx, d1))
			defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
			d2 := document.New(helper.TestKey(t))
			assert.NoError(t, c2.Attach(ctx, d2))
			defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

			assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewText("t").Edit(0, 0, "0123456789")
				return nil
			}, "init"))
			assert.NoError(t, c1.Sync(ctx))
			assert.NoError(t, c2.Sync(ctx))

			assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
				c.localEdit(root)
				return nil
			}, "d1 edit"))
			assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
				c.remoteEdit(root)
				return nil
			}, "d2 edit"))
			assert.NoError(t, c1.Sync(ctx))
			assert.NoError(t, c2.Sync(ctx))
			assert.NoError(t, c1.Sync(ctx))

			assert.NoError(t, d1.Undo())
			assert.NoError(t, d2.Undo())
			assert.NoError(t, c1.Sync(ctx))
			assert.NoError(t, c2.Sync(ctx))
			assert.NoError(t, c1.Sync(ctx))

			d1Text := d1.Root().GetText("t").String()
			d2Text := d2.Root().GetText("t").String()
			assert.Equal(t, d1Text, d2Text, "convergence")
			assert.Equal(t, "0123456789", d1Text, "content correctness after undo")
		})
	}
}

// TestHistoryTextReconcilePosition asserts on the reconciled position of a
// pending undo entry directly, not just on Marshal() output. For a Text
// Edit reverse operation, restoreSpans revives removed content by identity,
// so the from/to anchor generally does not affect the resulting content --
// confirmed while building this test: re-running the equivalent scenario in
// pkg/document/operations without the reconciliation call produces
// identical text. Content-only assertions therefore cannot tell a correct
// reconciliation from a missing one for this operation; this test also
// checks the stacked operation's own position, which can.
//
// Text "0123456789". d1 deletes [4,6) ("45") locally, leaving a pending
// undo anchored at offset 4. Before d1 undoes, d2 concurrently replaces
// [2,4) ("23") with "Q" -- a net length change of -1 -- and d1 syncs to
// receive it as a remote change. The anchor must shift left by 1, to 3.
func TestHistoryTextReconcilePosition(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	ctx := context.Background()

	d1 := document.New(helper.TestKey(t))
	assert.NoError(t, c1.Attach(ctx, d1))
	defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
	d2 := document.New(helper.TestKey(t))
	assert.NoError(t, c2.Attach(ctx, d2))
	defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

	assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewText("t").Edit(0, 0, "0123456789")
		return nil
	}, "init"))
	assert.NoError(t, c1.Sync(ctx))
	assert.NoError(t, c2.Sync(ctx))

	assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("t").Edit(4, 6, "")
		return nil
	}, "d1 delete"))

	top := d1.UndoStackTopForTest()
	assert.Len(t, top, 1)
	edit, ok := top[0].Op.(*operations.Edit)
	assert.True(t, ok, "d1's pending undo entry should be an Edit")
	assert.Equal(t, 4, edit.From().RelativeOffset(), "anchor before any remote edit")
	assert.Equal(t, 4, edit.To().RelativeOffset())

	assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("t").Edit(2, 4, "Q")
		return nil
	}, "d2 replace"))
	assert.NoError(t, c2.Sync(ctx))
	assert.NoError(t, c1.Sync(ctx))

	assert.Equal(t, "01Q6789", d1.Root().GetText("t").String(),
		"merged text before d1 undoes")

	// The reconciliation call site rewrites the entry's Op in place, so
	// re-reading the same top-of-stack slot observes the updated position.
	top = d1.UndoStackTopForTest()
	assert.Len(t, top, 1)
	edit, ok = top[0].Op.(*operations.Edit)
	assert.True(t, ok)
	assert.Equal(t, 3, edit.From().RelativeOffset(),
		"anchor should have shifted left by 1 after the remote replace")
	assert.Equal(t, 3, edit.To().RelativeOffset())

	assert.NoError(t, d1.Undo())
	assert.Equal(t, "01Q456789", d1.Root().GetText("t").String(),
		"restore should land \"45\" back next to \"Q\", not at the stale offset")
}

// TestHistoryTextReconcilePerChangeNotBatched pins that a multi-change pack
// is reconciled one change at a time against the root as it stood right
// after each change, not once over the whole pack -- matching
// Document.applyChange in the JS SDK, which reconciles inside the same
// per-change loop that executes each change (document.ts:1517 calling into
// :1552-1566), never after a batch.
//
// NormalizePos sums live length over physical predecessors reachable from a
// position's own (fixed at creation) node identity. For most positions --
// including simple ones anchored to a text's original, never-since-split
// node -- that sum never depends on anything happening elsewhere in the
// same pack, so a naive scenario ("d2 inserts, d2 also deletes a prefix")
// does not actually exercise the batched/per-change difference; both give
// the same answer. It only shows up once a position's node identity is
// itself downstream of an earlier split, so that its prior-predecessor
// walk passes through content a *later* change in the pack tombstones.
// This scenario was verified empirically (not hand-derived) against this
// port before being written down here -- see the two NormalizePos values
// asserted below.
//
// Setup: text "0123456789". Three remote changes from d2, pushed together
// in one pack, in this order:
//  1. Change 0: replace [1,2) ("1") with a freshly-ticketed "1" -- same
//     content, but it splits the original node and gives the next change's
//     anchor a non-original node identity, which is what makes this
//     reachable through prior-predecessor traversal at all.
//  2. Change A: insert "Z" right before the original "3".
//  3. Change B: delete [0,2) (the original "0" and the fresh "1" from
//     Change 0) -- content that precedes Change A's own anchor.
//
// d1 deletes [2,3) ("2") locally first, leaving a pending undo anchored at
// offset 2.
//
// Reconciled per change (correct): Change A's own position, normalized
// right after Change A alone executes (before Change B runs), sums "0"
// and the fresh "1" as still-live predecessors, giving remoteFrom ==
// remoteTo == 3 -- Case 2 against d1's anchor at 2 (remote to the right, a
// no-op). Change B then correctly shifts the anchor left by the "01" it
// removed, landing at offset 1.
//
// Reconciled in a batch (the bug this fixes): Change A's position would
// instead be normalized after Change B has already tombstoned "0" and the
// fresh "1", collapsing those same predecessors to zero and giving
// remoteFrom == remoteTo == 1 instead of 3 -- Case 1 against the same
// anchor (remote to the left), shifting it to offset 3 before Change B is
// even considered. Change B's own (correct) shift then lands the final,
// wrong anchor at offset 2 -- one off from the correct offset 1, and for
// the wrong reason (a case that should not have fired).
func TestHistoryTextReconcilePerChangeNotBatched(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	ctx := context.Background()

	d1 := document.New(helper.TestKey(t))
	assert.NoError(t, c1.Attach(ctx, d1))
	defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
	d2 := document.New(helper.TestKey(t))
	assert.NoError(t, c2.Attach(ctx, d2))
	defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

	assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewText("t").Edit(0, 0, "0123456789")
		return nil
	}, "init"))
	assert.NoError(t, c1.Sync(ctx))
	assert.NoError(t, c2.Sync(ctx))

	assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("t").Edit(2, 3, "")
		return nil
	}, "d1 delete \"2\""))

	top := d1.UndoStackTopForTest()
	assert.Len(t, top, 1)
	edit, ok := top[0].Op.(*operations.Edit)
	assert.True(t, ok, "d1's pending undo entry should be an Edit")
	assert.Equal(t, 2, edit.From().RelativeOffset(), "anchor before the remote pack")
	assert.Equal(t, 2, edit.To().RelativeOffset())

	// Three separate d2 updates, pushed together in one pack: none has been
	// synced yet, so all three are still pending when the next is made.
	assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("t").Edit(1, 2, "1") // Change 0: re-ticket "1" via replace.
		return nil
	}, "d2 re-ticket \"1\""))
	assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("t").Edit(3, 3, "Z") // Change A: insert "Z" before "3".
		return nil
	}, "d2 insert Z"))
	assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("t").Edit(0, 2, "") // Change B: delete [0,2).
		return nil
	}, "d2 delete prefix"))
	assert.NoError(t, c2.Sync(ctx))

	assert.NoError(t, c1.Sync(ctx))

	assert.Equal(t, "Z3456789", d1.Root().GetText("t").String(),
		"merged text: \"0\", \"1\", and \"2\" removed, \"Z\" inserted before \"3\"")

	top = d1.UndoStackTopForTest()
	assert.Len(t, top, 1)
	edit, ok = top[0].Op.(*operations.Edit)
	assert.True(t, ok)
	assert.Equal(t, 1, edit.From().RelativeOffset(),
		"anchor must be reconciled against each change's own post-change "+
			"root, not the root after the whole pack -- a value of 2 here "+
			"means Change A was (wrongly) normalized after Change B already "+
			"tombstoned the content preceding A's anchor, shifting the "+
			"anchor via the wrong ReconcileOperation case")
	assert.Equal(t, 1, edit.To().RelativeOffset())
}

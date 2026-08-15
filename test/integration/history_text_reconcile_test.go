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
// entry. Each case matches one branch of Edit.ReconcileOperation.
//
// These assert convergence only, matching the JS suite, for the same reason
// documented at TestReconcileOverlappingUndoDuplicatesContent below: the
// two overlapping-delete cases (3 and 5) are known to diverge from exact
// original content on double-undo, a pre-existing, cross-SDK limitation of
// the identity-preserving restore path, not something this port changes.
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
// and each undoes its own delete, JS's comment there says the shared
// characters can be restored twice, a limitation of the (older, JS-side)
// deep-copy-reinsert undo mechanism.
//
// Kept skipped here to match JS procedurally rather than un-skipped on our
// own judgment -- see the port's rule to reproduce JS's known defects
// rather than diverge from them -- but flagged for the task owner: run
// unskipped, both cases below currently PASS against this port's
// identity-preserving restore (RestoreSpans/RetombstoneSpans, built in the
// Edit reverse-operation work this task builds on), producing the exact
// original "0123456789", not duplicated content. That contradicts
// docs/design/undo-redo-go-port.md's assumption that this bug is
// "reproduced identically" in Go. Left skipped rather than asserted as
// passing because (a) that assumption is stated as a deliberate design
// decision, not just an unverified guess, and un-skipping is a call this
// task should not make unilaterally, and (b) two scenarios passing does not
// establish the underlying mechanism no longer applies in general -- both
// are open questions for whoever owns the "Overlapping undo content
// duplication" non-goal to verify and decide, not something to resolve
// inside a reconciliation task.
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
			t.Skip("KNOWN: matches the JS SDK's own it.skip for this case " +
				"(history_text_test.ts); kept skipped rather than asserted " +
				"here on our own judgment even though it currently passes " +
				"against this port -- see the doc comment above")

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

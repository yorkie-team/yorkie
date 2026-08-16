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
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

// initTreeDoc seeds the given document with <r><p>abcd</p></r>.
func initTreeDoc(t *testing.T, doc *document.Document) {
	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{Type: "r", Children: []json.TreeNode{{
			Type:     "p",
			Children: []json.TreeNode{{Type: "text", Value: "abcd"}},
		}}})
		return nil
	}, "init"))
}

// TestHistoryTreeConcurrentUndo covers the property identity-preserving
// reverse operations exist for: two clients that concurrently undo the same
// deletion must converge on one copy of the revived content.
//
// A reverse that revives nodes under their ORIGINAL identity is idempotent —
// the second undo finds the nodes already live and skips them. A reverse that
// re-inserts a COPY is not: each client mints its own nodes and both survive,
// so the content comes back twice and the replicas never reconverge on their
// own.
func TestHistoryTreeConcurrentUndo(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	t.Run("concurrent undo of one deletion revives it once test", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		d2 := document.New(helper.TestKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

		initTreeDoc(t, d1)
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.Equal(t, "<r><p>abcd</p></r>", d2.Root().GetTree("t").ToXML())

		// Both clients delete "ab" without seeing the other's delete, so both
		// carry a reverse for it on their own undo stack.
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 3, nil, 0)
			return nil
		}, "d1 delete ab"))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 3, nil, 0)
			return nil
		}, "d2 delete ab"))

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, d1.Marshal(), d2.Marshal())
		assert.Equal(t, "<r><p>cd</p></r>", d1.Root().GetTree("t").ToXML())

		assert.NoError(t, d1.Undo())
		assert.NoError(t, d2.Undo())

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))

		assert.Equal(t, d1.Marshal(), d2.Marshal(), "the two replicas must converge")
		assert.Equal(t, "<r><p>abcd</p></r>", d1.Root().GetTree("t").ToXML(),
			"the deletion is revived once, not once per undoing client")
		assert.Equal(t, "<r><p>abcd</p></r>", d2.Root().GetTree("t").ToXML())
	})

	t.Run("undo of a delete keeps the revived node identity test", func(t *testing.T) {
		// The single-client half of the same property: reviving in place means
		// the peer sees the node it already knows come back, not a new one it
		// has to merge against its own tombstone.
		ctx := context.Background()

		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		d2 := document.New(helper.TestKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

		initTreeDoc(t, d1)
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 3, nil, 0)
			return nil
		}, "d1 delete ab"))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.Equal(t, "<r><p>cd</p></r>", d2.Root().GetTree("t").ToXML())

		assert.NoError(t, d1.Undo())
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.Equal(t, d1.Marshal(), d2.Marshal())
		assert.Equal(t, "<r><p>abcd</p></r>", d2.Root().GetTree("t").ToXML())

		assert.NoError(t, d1.Redo())
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.Equal(t, d1.Marshal(), d2.Marshal())
		assert.Equal(t, "<r><p>cd</p></r>", d2.Root().GetTree("t").ToXML())
	})
}

// initDigitTreeDoc seeds the given document with <r><p>0123456789</p></r>,
// the fixture history_tree_test.ts's "reconcile cases" section uses.
func initDigitTreeDoc(t *testing.T, doc *document.Document) {
	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{Type: "r", Children: []json.TreeNode{{
			Type:     "p",
			Children: []json.TreeNode{{Type: "text", Value: "0123456789"}},
		}}})
		return nil
	}, "init"))
}

// TestHistoryTreeReconcileCases ports history_tree_test.ts's "Tree History -
// reconcile cases" describe block: two clients each delete or insert a
// range of <r><p>0123456789</p></r>, without seeing the other's edit, so
// both carry a pending undo entry that a remote change must reconcile
// against. All seven of JS's overlap cases are covered individually, not
// just the non-overlapping ones (1, 2, 7) -- see the doc comment below for
// why cases 3-6 are included despite docs/design/undo-redo.md calling them
// "known broken, deferred to Phase 2".
//
// Every one of these seven scenarios is a plain single-range delete or
// insert on each side, which -- per TestTreeEditReconcileOperationCases'
// "guard: an identity-preserving op is never index-reconciled" case --
// always takes the identity-preserving reverse path and is therefore never
// touched by ReconcileOperation's index arithmetic at all: convergence here
// comes entirely from reviving/re-removing by identity, the same mechanism
// TestHistoryTreeConcurrentUndo above exercises. That arithmetic is pinned
// directly, against a genuinely non-identity reverse, by
// TestTreeEditReconcileOperationCases and
// TestTreeEditReconcileOperationRealistic in pkg/document/operations. This
// test proves the two-client, sync-then-undo-then-redo scenario JS names
// "Case 1" through "Case 7" converges in Go too -- which is what the JS
// suite's own (non-skipped) Cases 3-6 tests actually prove about JS, despite
// the design doc's older "known broken" note. That note describes an
// earlier version of the reconciliation design (the design doc's own
// "Since identity-preserving restore landed" section says as much); it
// predates the identity-preserving reverse and was never updated after JS's
// own Cases 3-6 tests were un-skipped once that landed.
func TestHistoryTreeReconcileCases(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	cases := []struct {
		name   string
		d1Edit func(root *json.Object)
		d2Edit func(root *json.Object)
	}{
		{
			name:   "Case 1 (left): remote edit LEFT of undo should shift position",
			d1Edit: func(root *json.Object) { root.GetTree("t").Edit(7, 9, nil, 0) },
			d2Edit: func(root *json.Object) {
				root.GetTree("t").Edit(3, 3, &json.TreeNode{Type: "text", Value: "XX"}, 0)
			},
		},
		{
			name:   "Case 2 (right): remote edit RIGHT of undo should not affect",
			d1Edit: func(root *json.Object) { root.GetTree("t").Edit(3, 5, nil, 0) },
			d2Edit: func(root *json.Object) {
				root.GetTree("t").Edit(9, 9, &json.TreeNode{Type: "text", Value: "YY"}, 0)
			},
		},
		{
			name:   "Case 3 (contained_by): undo range contained by remote should collapse",
			d1Edit: func(root *json.Object) { root.GetTree("t").Edit(5, 7, nil, 0) },
			d2Edit: func(root *json.Object) { root.GetTree("t").Edit(3, 9, nil, 0) },
		},
		{
			name:   "Case 4 (contains): remote range contained by undo should adjust",
			d1Edit: func(root *json.Object) { root.GetTree("t").Edit(3, 9, nil, 0) },
			d2Edit: func(root *json.Object) {
				root.GetTree("t").Edit(6, 6, &json.TreeNode{Type: "text", Value: "ZZ"}, 0)
			},
		},
		{
			name:   "Case 5 (overlap_start): remote overlaps start of undo range",
			d1Edit: func(root *json.Object) { root.GetTree("t").Edit(5, 9, nil, 0) },
			d2Edit: func(root *json.Object) { root.GetTree("t").Edit(3, 7, nil, 0) },
		},
		{
			name:   "Case 6 (overlap_end): remote overlaps end of undo range",
			d1Edit: func(root *json.Object) { root.GetTree("t").Edit(3, 7, nil, 0) },
			d2Edit: func(root *json.Object) { root.GetTree("t").Edit(5, 9, nil, 0) },
		},
		{
			name:   "Case 7 (adjacent): adjacent edits at boundary",
			d1Edit: func(root *json.Object) { root.GetTree("t").Edit(5, 7, nil, 0) },
			d2Edit: func(root *json.Object) {
				root.GetTree("t").Edit(7, 7, &json.TreeNode{Type: "text", Value: "AA"}, 0)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			d1 := document.New(helper.TestKey(t))
			assert.NoError(t, c1.Attach(ctx, d1))
			defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
			d2 := document.New(helper.TestKey(t))
			assert.NoError(t, c2.Attach(ctx, d2))
			defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

			initDigitTreeDoc(t, d1)
			assert.NoError(t, c1.Sync(ctx))
			assert.NoError(t, c2.Sync(ctx))

			assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
				tc.d1Edit(root)
				return nil
			}, "d1 edit"))
			assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
				tc.d2Edit(root)
				return nil
			}, "d2 edit"))

			assert.NoError(t, c1.Sync(ctx))
			assert.NoError(t, c2.Sync(ctx))
			assert.NoError(t, c1.Sync(ctx))
			assert.Equal(t, d1.Marshal(), d2.Marshal(), "after concurrent edits")

			assert.NoError(t, d1.Undo())
			assert.NoError(t, d2.Undo())
			assert.NoError(t, c1.Sync(ctx))
			assert.NoError(t, c2.Sync(ctx))
			assert.NoError(t, c1.Sync(ctx))
			assert.Equal(t, d1.Marshal(), d2.Marshal(), "after undo")

			assert.NoError(t, d1.Redo())
			assert.NoError(t, d2.Redo())
			assert.NoError(t, c1.Sync(ctx))
			assert.NoError(t, c2.Sync(ctx))
			assert.NoError(t, c1.Sync(ctx))
			assert.Equal(t, d1.Marshal(), d2.Marshal(), "after redo")
		})
	}
}

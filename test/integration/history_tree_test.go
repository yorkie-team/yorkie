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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/yson"
	"github.com/yorkie-team/yorkie/test/helper"
)

// This file additionally ports history_tree_test.ts's "single client basic",
// "single client chained ops", "single client edge cases", "multi client
// basic", "multi client edge cases", "tree style undo/redo", "multi client
// style undo convergence", "multi client style vs edit/split convergence",
// "undo past initial tree via initialRoot" and "GC symmetry and anchor
// fallback" describe blocks. Its "reconcile cases" describe block (Case
// 1-7) is already ported faithfully above by TestHistoryTreeReconcileCases
// -- nothing from that describe block is duplicated here.

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

// treeOps names the six single-node tree operations exercised by
// history_tree_test.ts's "single client basic" and "multi client basic"
// describe blocks.
var treeOps = []string{
	"insert-text",
	"delete-text",
	"insert-element",
	"delete-element",
	"replace-text",
	"replace-element",
}

// initFoxTree seeds the given document with <doc><p>The fox jumped.</p></doc>,
// history_tree_test.ts's initTree fixture.
func initFoxTree(t *testing.T, doc *document.Document) {
	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{{
			Type:     "p",
			Children: []json.TreeNode{{Type: "text", Value: "The fox jumped."}},
		}}})
		return nil
	}, "init"))
}

// applyTreeOp1 applies the given operation at the position
// history_tree_test.ts's applyTreeOp1 uses (:57-94): middle/end positions.
func applyTreeOp1(t *testing.T, doc *document.Document, op string) {
	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		switch op {
		case "insert-text":
			root.GetTree("t").Edit(16, 16, &json.TreeNode{Type: "text", Value: "X"}, 0)
		case "delete-text":
			root.GetTree("t").Edit(6, 7, nil, 0)
		case "insert-element":
			root.GetTree("t").Edit(17, 17, &json.TreeNode{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "New"}},
			}, 0)
		case "delete-element":
			root.GetTree("t").Edit(0, 17, nil, 0)
		case "replace-text":
			root.GetTree("t").Edit(5, 8, &json.TreeNode{Type: "text", Value: "cat"}, 0)
		case "replace-element":
			root.GetTree("t").Edit(0, 17, &json.TreeNode{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "Replaced"}},
			}, 0)
		}
		return nil
	}, op))
}

// applyTreeOp2 applies the given operation at the position
// history_tree_test.ts's applyTreeOp2 uses (:97-117): start position. It has
// no "delete-element", "replace-text" or "replace-element" case; op2 callers
// never pass those.
func applyTreeOp2(t *testing.T, doc *document.Document, op string) {
	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		switch op {
		case "insert-text":
			root.GetTree("t").Edit(1, 1, &json.TreeNode{Type: "text", Value: "Q"}, 0)
		case "delete-text":
			root.GetTree("t").Edit(15, 16, nil, 0)
		case "insert-element":
			root.GetTree("t").Edit(0, 0, &json.TreeNode{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "Front"}},
			}, 0)
		}
		return nil
	}, op))
}

// TestHistoryTreeSingleClientBasic ports history_tree_test.ts's "Tree
// History - single client basic" describe block (:119-172): 8 runtime
// instances -- one per op in treeOps, plus 2 fixed cases.
func TestHistoryTreeSingleClientBasic(t *testing.T) {
	for _, op := range treeOps {
		op := op
		t.Run(fmt.Sprintf("should undo/redo %s", op), func(t *testing.T) {
			doc := document.New(helper.TestKey(t))
			initFoxTree(t, doc)

			before := doc.Root().GetTree("t").ToXML()
			applyTreeOp1(t, doc, op)
			after := doc.Root().GetTree("t").ToXML()

			assert.NoError(t, doc.Undo())
			assert.Equal(t, before, doc.Root().GetTree("t").ToXML(), fmt.Sprintf("undo %s failed", op))

			assert.NoError(t, doc.Redo())
			assert.Equal(t, after, doc.Root().GetTree("t").ToXML(), fmt.Sprintf("redo %s failed", op))
		})
	}

	t.Run("should handle undo-redo round trip multiple times", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		initFoxTree(t, doc)

		initial := doc.Root().GetTree("t").ToXML()
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 1, &json.TreeNode{Type: "text", Value: "Hello "}, 0)
			return nil
		}, "insert"))
		modified := doc.Root().GetTree("t").ToXML()

		for i := 0; i < 3; i++ {
			assert.NoError(t, doc.Undo())
			assert.Equal(t, initial, doc.Root().GetTree("t").ToXML(), fmt.Sprintf("round %d undo failed", i))

			assert.NoError(t, doc.Redo())
			assert.Equal(t, modified, doc.Root().GetTree("t").ToXML(), fmt.Sprintf("round %d redo failed", i))
		}
	})

	t.Run("should clear redo stack when new edit is made after undo", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		initFoxTree(t, doc)

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(16, 16, &json.TreeNode{Type: "text", Value: "X"}, 0)
			return nil
		}, "append"))

		assert.NoError(t, doc.Undo())
		assert.True(t, doc.CanRedo())

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 1, &json.TreeNode{Type: "text", Value: "Z"}, 0)
			return nil
		}, "new edit"))
		assert.False(t, doc.CanRedo())
	})
}

// TestHistoryTreeSingleClientChainedOps ports history_tree_test.ts's "Tree
// History - single client chained ops" describe block (:174-251): 27 runtime
// instances, the Cartesian product of {insert-text, delete-text,
// insert-element}^3.
func TestHistoryTreeSingleClientChainedOps(t *testing.T) {
	chainOps := []string{"insert-text", "delete-text", "insert-element"}

	applyChainOp := func(t *testing.T, doc *document.Document, op string) {
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			switch op {
			case "insert-text":
				root.GetTree("t").EditByPath([]int{0, 1}, []int{0, 1}, &json.TreeNode{Type: "text", Value: "X"}, 0)
			case "delete-text":
				root.GetTree("t").Edit(1, 2, nil, 0)
			case "insert-element":
				root.GetTree("t").EditByPath([]int{1}, []int{1}, &json.TreeNode{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "N"}},
				}, 0)
			}
			return nil
		}, op))
	}

	for _, op1 := range chainOps {
		for _, op2 := range chainOps {
			for _, op3 := range chainOps {
				op1, op2, op3 := op1, op2, op3
				caseName := fmt.Sprintf("%s-%s-%s", op1, op2, op3)

				t.Run(fmt.Sprintf("should undo chain correctly: %s", caseName), func(t *testing.T) {
					doc := document.New(helper.TestKey(t))
					assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
						root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{{
							Type:     "p",
							Children: []json.TreeNode{{Type: "text", Value: "ABCD"}},
						}}})
						return nil
					}, "init"))

					snapshots := []string{doc.Root().GetTree("t").ToXML()}
					applyChainOp(t, doc, op1)
					snapshots = append(snapshots, doc.Root().GetTree("t").ToXML())
					applyChainOp(t, doc, op2)
					snapshots = append(snapshots, doc.Root().GetTree("t").ToXML())
					applyChainOp(t, doc, op3)
					snapshots = append(snapshots, doc.Root().GetTree("t").ToXML())

					// Undo: S3 -> S2 -> S1 -> S0
					for i := 3; i >= 1; i-- {
						assert.NoError(t, doc.Undo())
						assert.Equal(t, snapshots[i-1], doc.Root().GetTree("t").ToXML(), fmt.Sprintf("undo to S%d", i-1))
					}

					// Redo: S0 -> S1 -> S2 -> S3
					for i := 0; i < 3; i++ {
						assert.NoError(t, doc.Redo())
						assert.Equal(t, snapshots[i+1], doc.Root().GetTree("t").ToXML(), fmt.Sprintf("redo to S%d", i+1))
					}
				})
			}
		}
	}
}

// TestHistoryTreeSingleClientEdgeCases ports history_tree_test.ts's "Tree
// History - single client edge cases" describe block (:254-367): 6 runtime
// instances.
func TestHistoryTreeSingleClientEdgeCases(t *testing.T) {
	t.Run("should handle edit at start position", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		initFoxTree(t, doc)

		before := doc.Root().GetTree("t").ToXML()
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 4, &json.TreeNode{Type: "text", Value: "A"}, 0)
			return nil
		}, "edit at start"))
		after := doc.Root().GetTree("t").ToXML()

		assert.NoError(t, doc.Undo())
		assert.Equal(t, before, doc.Root().GetTree("t").ToXML())

		assert.NoError(t, doc.Redo())
		assert.Equal(t, after, doc.Root().GetTree("t").ToXML())
	})

	t.Run("should handle edit at middle position", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		initFoxTree(t, doc)

		before := doc.Root().GetTree("t").ToXML()
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(5, 8, &json.TreeNode{Type: "text", Value: "cat"}, 0)
			return nil
		}, "edit at middle"))
		after := doc.Root().GetTree("t").ToXML()

		assert.NoError(t, doc.Undo())
		assert.Equal(t, before, doc.Root().GetTree("t").ToXML())

		assert.NoError(t, doc.Redo())
		assert.Equal(t, after, doc.Root().GetTree("t").ToXML())
	})

	t.Run("should handle edit at end position", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		initFoxTree(t, doc)

		before := doc.Root().GetTree("t").ToXML()
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(16, 16, &json.TreeNode{Type: "text", Value: "!"}, 0)
			return nil
		}, "edit at end"))
		after := doc.Root().GetTree("t").ToXML()

		assert.NoError(t, doc.Undo())
		assert.Equal(t, before, doc.Root().GetTree("t").ToXML())

		assert.NoError(t, doc.Redo())
		assert.Equal(t, after, doc.Root().GetTree("t").ToXML())
	})

	t.Run("should handle full tree deletion + undo", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		initFoxTree(t, doc)

		before := doc.Root().GetTree("t").ToXML()
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(0, 17, nil, 0)
			return nil
		}, "delete all"))
		assert.Equal(t, "<doc></doc>", doc.Root().GetTree("t").ToXML())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, before, doc.Root().GetTree("t").ToXML())

		assert.NoError(t, doc.Redo())
		assert.Equal(t, "<doc></doc>", doc.Root().GetTree("t").ToXML())
	})

	t.Run("should handle empty undo/redo stacks", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		initFoxTree(t, doc)

		assert.True(t, doc.CanUndo())
		assert.NoError(t, doc.Undo())
		assert.False(t, doc.CanUndo())
	})

	t.Run("should handle rapid consecutive edits", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{{
				Type: "p",
			}}})
			return nil
		}, "init"))

		states := []string{doc.Root().GetTree("t").ToXML()}
		for i := 0; i < 10; i++ {
			i := i
			assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetTree("t").Edit(1, 1, &json.TreeNode{Type: "text", Value: fmt.Sprintf("%d", i)}, 0)
				return nil
			}, fmt.Sprintf("insert %d", i)))
			states = append(states, doc.Root().GetTree("t").ToXML())
		}

		// Undo all
		for i := 9; i >= 0; i-- {
			assert.NoError(t, doc.Undo())
			assert.Equal(t, states[i], doc.Root().GetTree("t").ToXML())
		}

		// Redo all
		for i := 1; i <= 10; i++ {
			assert.NoError(t, doc.Redo())
			assert.Equal(t, states[i], doc.Root().GetTree("t").ToXML())
		}
	})
}

// TestHistoryTreeMultiClientBasic ports history_tree_test.ts's "Tree
// History - multi client basic" describe block (:369-456): 18 runtime
// instances, {insert-text, delete-text, insert-element}^2 x {converge after
// undo, converge after redo}. JS's redo matrix once excluded the
// insert-text/delete-text combination (a stale `skipRedo` gate); that gate
// was dropped by the same commit that un-skipped the reconcile Cases 3-6
// (`fa6cc513`), so the current JS source -- and this port -- exclude
// nothing.
func TestHistoryTreeMultiClientBasic(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	multiOps := []string{"insert-text", "delete-text", "insert-element"}
	for _, op1 := range multiOps {
		for _, op2 := range multiOps {
			op1, op2 := op1, op2

			t.Run(fmt.Sprintf("should converge after undo: %s-%s", op1, op2), func(t *testing.T) {
				ctx := context.Background()

				d1 := document.New(helper.TestKey(t))
				assert.NoError(t, c1.Attach(ctx, d1))
				defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
				d2 := document.New(helper.TestKey(t))
				assert.NoError(t, c2.Attach(ctx, d2))
				defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

				initFoxTree(t, d1)
				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))

				applyTreeOp1(t, d1, op1)
				applyTreeOp2(t, d2, op2)

				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))
				assert.NoError(t, c1.Sync(ctx))
				assert.Equal(t, d1.Marshal(), d2.Marshal(), "after ops")

				assert.NoError(t, d1.Undo())
				assert.NoError(t, d2.Undo())

				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))
				assert.NoError(t, c1.Sync(ctx))
				assert.Equal(t, d1.Marshal(), d2.Marshal(), "after undo")
			})

			t.Run(fmt.Sprintf("should converge after redo: %s-%s", op1, op2), func(t *testing.T) {
				ctx := context.Background()

				d1 := document.New(helper.TestKey(t))
				assert.NoError(t, c1.Attach(ctx, d1))
				defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
				d2 := document.New(helper.TestKey(t))
				assert.NoError(t, c2.Attach(ctx, d2))
				defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

				initFoxTree(t, d1)
				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))

				applyTreeOp1(t, d1, op1)
				applyTreeOp2(t, d2, op2)

				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))
				assert.NoError(t, c1.Sync(ctx))

				assert.NoError(t, d1.Undo())
				assert.NoError(t, d2.Undo())

				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))
				assert.NoError(t, c1.Sync(ctx))

				assert.NoError(t, d1.Redo())
				assert.NoError(t, d2.Redo())

				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))
				assert.NoError(t, c1.Sync(ctx))
				assert.Equal(t, d1.Marshal(), d2.Marshal(), "after redo")
			})
		}
	}
}

// TestHistoryTreeMultiClientEdgeCases ports history_tree_test.ts's "Tree
// History - multi client edge cases" describe block (:800-948): 3 runtime
// instances.
func TestHistoryTreeMultiClientEdgeCases(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	t.Run("should converge with concurrent element + text edits", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		d2 := document.New(helper.TestKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "ABCD"}},
			}}})
			return nil
		}, "init"))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(5, 5, &json.TreeNode{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "New"}},
			}, 0)
			return nil
		}, "d1 insert element"))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 1, &json.TreeNode{Type: "text", Value: "X"}, 0)
			return nil
		}, "d2 insert text"))

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, d1.Marshal(), d2.Marshal())

		assert.NoError(t, d1.Undo())
		assert.NoError(t, d2.Undo())

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, d1.Marshal(), d2.Marshal())
	})

	t.Run("should converge with concurrent text edits in same paragraph", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		d2 := document.New(helper.TestKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "ABCDEFGH"}},
			}}})
			return nil
		}, "init"))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(3, 5, &json.TreeNode{Type: "text", Value: "XX"}, 0)
			return nil
		}, "d1 edit"))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(7, 9, &json.TreeNode{Type: "text", Value: "YY"}, 0)
			return nil
		}, "d2 edit"))

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, d1.Marshal(), d2.Marshal())

		// d1: undo then redo, d2: just undo.
		assert.NoError(t, d1.Undo())
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))

		assert.NoError(t, d1.Redo())
		assert.NoError(t, d2.Undo())

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, d1.Marshal(), d2.Marshal())
	})

	t.Run("should converge with nested structure concurrent edits", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		d2 := document.New(helper.TestKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "AB"}}},
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "CD"}}},
			}})
			return nil
		}, "init"))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 1, &json.TreeNode{Type: "text", Value: "X"}, 0)
			return nil
		}, "d1 edit first p"))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(5, 5, &json.TreeNode{Type: "text", Value: "Y"}, 0)
			return nil
		}, "d2 edit second p"))

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, d1.Marshal(), d2.Marshal())

		assert.NoError(t, d1.Undo())
		assert.NoError(t, d2.Undo())

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, d1.Marshal(), d2.Marshal())

		assert.NoError(t, d1.Redo())
		assert.NoError(t, d2.Redo())

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, d1.Marshal(), d2.Marshal())
	})
}

// makeStyleTree returns history_tree_test.ts's tree style fixture:
// <doc><p bold="true">AB</p><p>CD</p></doc>.
func makeStyleTree() json.TreeNode {
	return json.TreeNode{Type: "doc", Children: []json.TreeNode{
		{Type: "p", Attributes: map[string]string{"bold": "true"}, Children: []json.TreeNode{{Type: "text", Value: "AB"}}},
		{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "CD"}}},
	}}
}

// treeStyleOps names the four style operations exercised by
// history_tree_test.ts's "tree style undo/redo" describe block.
var treeStyleOps = []string{"set-bold", "set-italic", "set-color", "remove-bold"}

// applyTreeStyleOp applies the given operation, mirroring
// history_tree_test.ts's applyStyleOp (:977-994).
func applyTreeStyleOp(t *testing.T, doc *document.Document, op string) {
	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		switch op {
		case "set-bold":
			root.GetTree("t").Style(0, 1, map[string]string{"bold": "true"})
		case "set-italic":
			root.GetTree("t").Style(0, 1, map[string]string{"italic": "true"})
		case "set-color":
			root.GetTree("t").Style(3, 4, map[string]string{"color": "red"})
		case "remove-bold":
			root.GetTree("t").RemoveStyle(0, 1, []string{"bold"})
		}
		return nil
	}, op))
}

// TestHistoryTreeStyleUndoRedo ports history_tree_test.ts's "Tree History -
// tree style undo/redo" describe block (:950-1095): 21 runtime instances --
// 4 single-op cases (5a), 15 chained-pair cases (5b: 16 combinations of
// treeStyleOps^2 minus the 1 JS itself excludes: remove-bold -> remove-bold
// is a no-op the second time and pushes nothing to the undo stack), and 2
// mixed style/edit chains (5c).
func TestHistoryTreeStyleUndoRedo(t *testing.T) {
	for _, op := range treeStyleOps {
		op := op
		t.Run(fmt.Sprintf("should undo/redo: %s", op), func(t *testing.T) {
			doc := document.New(helper.TestKey(t))
			assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewTree("t", makeStyleTree())
				return nil
			}, "init"))

			s0 := doc.Root().GetTree("t").ToXML()
			applyTreeStyleOp(t, doc, op)
			s1 := doc.Root().GetTree("t").ToXML()

			assert.NoError(t, doc.Undo())
			assert.Equal(t, s0, doc.Root().GetTree("t").ToXML(), fmt.Sprintf("undo %s failed", op))

			assert.NoError(t, doc.Redo())
			assert.Equal(t, s1, doc.Root().GetTree("t").ToXML(), fmt.Sprintf("redo %s failed", op))
		})
	}

	for _, op1 := range treeStyleOps {
		for _, op2 := range treeStyleOps {
			if op1 == "remove-bold" && op2 == "remove-bold" {
				continue
			}
			op1, op2 := op1, op2
			t.Run(fmt.Sprintf("should undo chain: %s -> %s", op1, op2), func(t *testing.T) {
				doc := document.New(helper.TestKey(t))
				assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
					root.SetNewTree("t", makeStyleTree())
					return nil
				}, "init"))

				s0 := doc.Root().GetTree("t").ToXML()
				applyTreeStyleOp(t, doc, op1)
				s1 := doc.Root().GetTree("t").ToXML()
				applyTreeStyleOp(t, doc, op2)
				s2 := doc.Root().GetTree("t").ToXML()

				assert.NoError(t, doc.Undo())
				assert.Equal(t, s1, doc.Root().GetTree("t").ToXML(), fmt.Sprintf("undo %s failed", op2))
				assert.NoError(t, doc.Undo())
				assert.Equal(t, s0, doc.Root().GetTree("t").ToXML(), fmt.Sprintf("undo %s failed", op1))

				assert.NoError(t, doc.Redo())
				assert.Equal(t, s1, doc.Root().GetTree("t").ToXML(), fmt.Sprintf("redo %s failed", op1))
				assert.NoError(t, doc.Redo())
				assert.Equal(t, s2, doc.Root().GetTree("t").ToXML(), fmt.Sprintf("redo %s failed", op2))
			})
		}
	}

	t.Run("should undo style after edit", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", makeStyleTree())
			return nil
		}, "init"))

		s0 := doc.Root().GetTree("t").ToXML()
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 1, &json.TreeNode{Type: "text", Value: "X"}, 0)
			return nil
		}, "insert"))
		s1 := doc.Root().GetTree("t").ToXML()
		applyTreeStyleOp(t, doc, "set-italic")
		s2 := doc.Root().GetTree("t").ToXML()

		assert.NoError(t, doc.Undo())
		assert.Equal(t, s1, doc.Root().GetTree("t").ToXML(), "undo style failed")
		assert.NoError(t, doc.Undo())
		assert.Equal(t, s0, doc.Root().GetTree("t").ToXML(), "undo edit failed")
		assert.NoError(t, doc.Redo())
		assert.Equal(t, s1, doc.Root().GetTree("t").ToXML(), "redo edit failed")
		assert.NoError(t, doc.Redo())
		assert.Equal(t, s2, doc.Root().GetTree("t").ToXML(), "redo style failed")
	})

	t.Run("should undo edit after style", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", makeStyleTree())
			return nil
		}, "init"))

		s0 := doc.Root().GetTree("t").ToXML()
		applyTreeStyleOp(t, doc, "set-italic")
		s1 := doc.Root().GetTree("t").ToXML()
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 1, &json.TreeNode{Type: "text", Value: "X"}, 0)
			return nil
		}, "insert"))
		s2 := doc.Root().GetTree("t").ToXML()

		assert.NoError(t, doc.Undo())
		assert.Equal(t, s1, doc.Root().GetTree("t").ToXML(), "undo edit failed")
		assert.NoError(t, doc.Undo())
		assert.Equal(t, s0, doc.Root().GetTree("t").ToXML(), "undo style failed")
		assert.NoError(t, doc.Redo())
		assert.Equal(t, s1, doc.Root().GetTree("t").ToXML(), "redo style failed")
		assert.NoError(t, doc.Redo())
		assert.Equal(t, s2, doc.Root().GetTree("t").ToXML(), "redo edit failed")
	})
}

// TestHistoryTreeMultiClientStyleUndoConvergence ports history_tree_test.ts's
// "Tree History - multi client style undo convergence" describe block
// (:1097-1198): 18 runtime instances, localOps(3) x remoteOps(3) x
// targets(2).
func TestHistoryTreeMultiClientStyleUndoConvergence(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	localOps := []string{"set-bold", "set-italic", "remove-bold"}
	remoteOps := []string{"set-color", "set-bold", "remove-bold"}
	targets := []string{"same-element", "different-element"}

	for _, localOp := range localOps {
		for _, remoteOp := range remoteOps {
			for _, target := range targets {
				localOp, remoteOp, target := localOp, remoteOp, target
				t.Run(fmt.Sprintf("should converge: local %s + remote %s on %s", localOp, remoteOp, target), func(t *testing.T) {
					ctx := context.Background()

					d1 := document.New(helper.TestKey(t))
					assert.NoError(t, c1.Attach(ctx, d1))
					defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
					d2 := document.New(helper.TestKey(t))
					assert.NoError(t, c2.Attach(ctx, d2))
					defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

					assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
						root.SetNewTree("t", makeStyleTree())
						return nil
					}, "init"))
					assert.NoError(t, c1.Sync(ctx))
					assert.NoError(t, c2.Sync(ctx))

					assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
						switch localOp {
						case "set-bold":
							root.GetTree("t").Style(0, 1, map[string]string{"bold": "true"})
						case "set-italic":
							root.GetTree("t").Style(0, 1, map[string]string{"italic": "true"})
						case "remove-bold":
							root.GetTree("t").RemoveStyle(0, 1, []string{"bold"})
						}
						return nil
					}, "local style"))

					assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
						idx, toIdx := 0, 1
						if target == "different-element" {
							idx, toIdx = 3, 4
						}
						switch remoteOp {
						case "set-color":
							root.GetTree("t").Style(idx, toIdx, map[string]string{"color": "red"})
						case "set-bold":
							root.GetTree("t").Style(idx, toIdx, map[string]string{"bold": "true"})
						case "remove-bold":
							root.GetTree("t").RemoveStyle(idx, toIdx, []string{"bold"})
						}
						return nil
					}, "remote style"))

					assert.NoError(t, c1.Sync(ctx))
					assert.NoError(t, c2.Sync(ctx))
					assert.NoError(t, c1.Sync(ctx))

					assert.NoError(t, d1.Undo())

					assert.NoError(t, c1.Sync(ctx))
					assert.NoError(t, c2.Sync(ctx))
					assert.NoError(t, c1.Sync(ctx))

					assert.Equal(t, d1.Root().GetTree("t").ToXML(), d2.Root().GetTree("t").ToXML(),
						fmt.Sprintf("divergence: %s + %s on %s", localOp, remoteOp, target))
				})
			}
		}
	}
}

// TestHistoryTreeMultiClientStyleVsEditConvergence ports history_tree_test.ts's
// "Tree History - multi client style vs edit/split convergence" describe
// block (:1200-1384): 24 runtime instances, localOps(3) x remoteOps(4) x 2
// directions.
//
// 3 of the 24 -- the reverse-direction case where the LOCAL op is
// remoteOp == "split-l1" (a splitLevel > 0 edit) and d1 undoes it -- are
// skipped: Go's TreeEdit.toReverseOperation returns no reverse at all for a
// splitLevel > 0 edit yet (filed: the "splitLevel > 0 produces no reverse"
// gap in docs/tasks/active/20260816-remote-redo-replica-divergence-todo.md).
// With nothing pushed for that edit, d1.Undo() would pop and undo a
// different, unrelated undo-stack entry instead -- not a JS-equivalent
// scenario, so it is skipped rather than adapted. The forward-direction case
// (remote does the split, d1 undoes its own unrelated style op) does not
// touch this gap and is not skipped.
func TestHistoryTreeMultiClientStyleVsEditConvergence(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	localOps := []string{"set-bold", "set-italic", "remove-bold"}
	remoteOps := []string{"insert-text", "delete-text", "insert-element", "split-l1"}

	makeSplitDoc := func() json.TreeNode {
		return json.TreeNode{Type: "doc", Children: []json.TreeNode{
			{Type: "p", Attributes: map[string]string{"bold": "true"}, Children: []json.TreeNode{{Type: "text", Value: "ABCD"}}},
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "EFGH"}}},
		}}
	}

	applyMixStyle := func(root *json.Object, op string) {
		switch op {
		case "set-bold":
			root.GetTree("t").Style(0, 1, map[string]string{"bold": "true"})
		case "set-italic":
			root.GetTree("t").Style(0, 1, map[string]string{"italic": "true"})
		case "remove-bold":
			root.GetTree("t").RemoveStyle(0, 1, []string{"bold"})
		}
	}

	applyMixEdit := func(root *json.Object, op string) {
		switch op {
		case "insert-text":
			root.GetTree("t").Edit(1, 1, &json.TreeNode{Type: "text", Value: "X"}, 0)
		case "delete-text":
			root.GetTree("t").Edit(1, 2, nil, 0)
		case "insert-element":
			root.GetTree("t").Edit(6, 6, &json.TreeNode{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "NEW"}},
			}, 0)
		case "split-l1":
			root.GetTree("t").Edit(3, 3, nil, 1)
		}
	}

	for _, localOp := range localOps {
		for _, remoteOp := range remoteOps {
			localOp, remoteOp := localOp, remoteOp

			t.Run(fmt.Sprintf("should converge: local style(%s) + remote edit(%s)", localOp, remoteOp), func(t *testing.T) {
				ctx := context.Background()

				d1 := document.New(helper.TestKey(t))
				assert.NoError(t, c1.Attach(ctx, d1))
				defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
				d2 := document.New(helper.TestKey(t))
				assert.NoError(t, c2.Attach(ctx, d2))
				defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

				assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
					root.SetNewTree("t", makeSplitDoc())
					return nil
				}, "init"))
				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))

				assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
					applyMixStyle(root, localOp)
					return nil
				}, "local style"))
				assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
					applyMixEdit(root, remoteOp)
					return nil
				}, "remote edit"))

				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))
				assert.NoError(t, c1.Sync(ctx))

				assert.NoError(t, d1.Undo())

				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))
				assert.NoError(t, c1.Sync(ctx))

				assert.Equal(t, d1.Root().GetTree("t").ToXML(), d2.Root().GetTree("t").ToXML(),
					fmt.Sprintf("divergence: style(%s) + edit(%s)", localOp, remoteOp))
			})

			// Reverse direction: local edit, remote style, undo edit.
			t.Run(fmt.Sprintf("should converge: local edit(%s) + remote style(%s)", remoteOp, localOp), func(t *testing.T) {
				if remoteOp == "split-l1" {
					t.Skip("a splitLevel > 0 Tree.Edit produces no reverse operation in Go yet, so undoing this local split here would incorrectly pop and undo a different, unrelated undo-stack entry (filed: docs/tasks/active/20260816-remote-redo-replica-divergence-todo.md)")
				}

				ctx := context.Background()

				d1 := document.New(helper.TestKey(t))
				assert.NoError(t, c1.Attach(ctx, d1))
				defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
				d2 := document.New(helper.TestKey(t))
				assert.NoError(t, c2.Attach(ctx, d2))
				defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

				assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
					root.SetNewTree("t", makeSplitDoc())
					return nil
				}, "init"))
				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))

				assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
					applyMixEdit(root, remoteOp)
					return nil
				}, "local edit"))
				assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
					applyMixStyle(root, localOp)
					return nil
				}, "remote style"))

				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))
				assert.NoError(t, c1.Sync(ctx))

				assert.NoError(t, d1.Undo())

				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))
				assert.NoError(t, c1.Sync(ctx))

				assert.Equal(t, d1.Root().GetTree("t").ToXML(), d2.Root().GetTree("t").ToXML(),
					fmt.Sprintf("divergence: edit(%s) + style(%s)", remoteOp, localOp))
			})
		}
	}
}

// TestHistoryTreeUndoPastInitialRoot ports history_tree_test.ts's "Tree
// History - undo past initial tree via initialRoot" describe block
// (:1386-1459): 1 runtime instance.
func TestHistoryTreeUndoPastInitialRoot(t *testing.T) {
	clients := activeClients(t, 1)
	c1 := clients[0]
	defer deactivateAndCloseClients(t, clients)

	ctx := context.Background()
	doc := document.New(helper.TestKey(t))

	assert.NoError(t, c1.Attach(ctx, doc, client.WithInitialRoot(yson.Object{
		"content": yson.Tree{Root: yson.TreeNode{
			Type: "doc",
			Children: []yson.TreeNode{{
				Type:       "block",
				Attributes: map[string]string{"id": "block-1"},
				Children:   []yson.TreeNode{{Type: "inline"}},
			}},
		}},
	})))
	defer func() { assert.NoError(t, c1.Detach(ctx, doc)) }()

	initialXML := doc.Root().GetTree("content").ToXML()

	// After attach, the undo stack should be empty -- initialRoot is not an
	// undoable user action.
	assert.Equal(t, 0, doc.UndoStackLenForTest(), "undo stack should be empty after attach")
	assert.False(t, doc.CanUndo())

	// Insert 4 characters one by one (like typing "asdf").
	for _, ch := range []string{"a", "s", "d", "f"} {
		ch := ch
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("content").EditByPath([]int{0, 0, 0}, []int{0, 0, 0}, &json.TreeNode{Type: "text", Value: ch}, 0)
			return nil
		}, fmt.Sprintf("type '%s'", ch)))
	}

	// Undo 4 times -- should revert each character.
	for i := 0; i < 4; i++ {
		assert.True(t, doc.CanUndo())
		assert.NoError(t, doc.Undo())
	}
	assert.Equal(t, initialXML, doc.Root().GetTree("content").ToXML())

	// 5th undo -- should be blocked, tree stays intact.
	assert.False(t, doc.CanUndo(), "should not be able to undo past initialRoot")
	assert.Equal(t, initialXML, doc.Root().GetTree("content").ToXML(), "tree should remain intact")
}

// TestHistoryTreeGCSymmetryAndAnchorFallback ports history_tree_test.ts's
// "Tree History - GC symmetry and anchor fallback" describe block
// (:1461-1577): 2 runtime instances.
func TestHistoryTreeGCSymmetryAndAnchorFallback(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	t.Run("converges and purges symmetrically after undo/redo of concurrent deletes", func(t *testing.T) {
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

		// d2's deleted range sits strictly inside d1's (contained_by).
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 8, nil, 0) // "234567"
			return nil
		}, "d1 delete [2,8)"))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(4, 6, nil, 0) // "45"
			return nil
		}, "d2 delete [4,6)"))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, d1.Marshal(), d2.Marshal(), "after ops")

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

		// GC symmetry: with a version vector both replicas fully know, each
		// must purge the same tombstones and end with an empty garbage set.
		vv := helper.MaxVersionVector(d1.ActorID(), d2.ActorID())
		purged1 := d1.GarbageCollect(vv)
		purged2 := d2.GarbageCollect(vv)
		assert.Equal(t, purged1, purged2, "both replicas purge the same node count")
		assert.Equal(t, 0, d1.GarbageLen(), "d1 garbage drained")
		assert.Equal(t, 0, d2.GarbageLen(), "d2 garbage drained")
		assert.Equal(t, d1.Marshal(), d2.Marshal(), "converged after GC")
	})

	t.Run("recreates a purged subtree on redo and converges", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		d2 := document.New(helper.TestKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "hello"}},
			}}})
			return nil
		}, "init"))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))

		// d1 deletes the whole <p>hello</p>; sync so both replicas tombstone
		// it.
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(0, 7, nil, 0)
			return nil
		}, "d1 delete subtree"))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, d1.Marshal(), d2.Marshal(), "after delete")

		// Undo revives it; sync; then purge with a full vector so the (now
		// again deleted-then-revived) tombstones are physically removed on
		// both.
		assert.NoError(t, d1.Undo())
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))
		vv := helper.MaxVersionVector(d1.ActorID(), d2.ActorID())
		d1.GarbageCollect(vv)
		d2.GarbageCollect(vv)
		assert.Equal(t, d1.Marshal(), d2.Marshal(), "after undo + GC")

		// Redo re-deletes; undo once more must recreate from spans and
		// converge.
		assert.NoError(t, d1.Redo())
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))
		d1.GarbageCollect(vv)
		d2.GarbageCollect(vv)

		assert.NoError(t, d1.Undo())
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, d1.Marshal(), d2.Marshal(), "converged after recreate")
		assert.Equal(t, "<doc><p>hello</p></doc>", d1.Root().GetTree("t").ToXML(),
			"purged subtree recreated under original identity")
	})
}

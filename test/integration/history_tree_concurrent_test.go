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
	"github.com/yorkie-team/yorkie/test/helper"
)

// This file ports history_tree_concurrent_test.ts in full: convergence of
// concurrent OVERLAPPING tree undo/redo once the deleted nodes have been
// GC-purged, so restore takes the recreate path -- history_tree_test.go's
// reconcile cases undo before GC runs and never exercise this.

// settleTreeClients runs several push/pull rounds on both clients so their
// changes are fully exchanged and each replica's min-synced version vector
// advances far enough for GC to purge the tombstones. It ports
// history_tree_concurrent_test.ts's settle helper (:23-28); two calls back
// to back are used before an undo to guarantee the deleted nodes are
// actually purged, so restore exercises the recreate path.
func settleTreeClients(ctx context.Context, t *testing.T, c1, c2 *client.Client) {
	for i := 0; i < 3; i++ {
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
	}
}

// initFlatTreeDoc seeds the given document with <doc><p>0123456789</p></doc>,
// history_tree_concurrent_test.ts's initFlat fixture.
func initFlatTreeDoc(t *testing.T, doc *document.Document) {
	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{{
			Type:     "p",
			Children: []json.TreeNode{{Type: "text", Value: "0123456789"}},
		}}})
		return nil
	}, "init"))
}

// assertTreeConverges ports the JS conv helper (:42-47): assert.Equal with
// both replicas' XML rendered into the failure message so a divergence is
// legible without reproducing it locally.
func assertTreeConverges(t *testing.T, d1, d2 *document.Document, label string) {
	assert.Equal(t, d1.Marshal(), d2.Marshal(), fmt.Sprintf(
		"%s DIVERGED\n  d1=%s\n  d2=%s", label,
		d1.Root().GetTree("t").ToXML(), d2.Root().GetTree("t").ToXML(),
	))
}

// TestHistoryTreeConcurrentOverlappingUndoAfterGC ports
// history_tree_concurrent_test.ts's "Tree History - concurrent overlapping
// undo after GC" describe block in full: 14 runtime instances -- 6 overlap
// relationships x {undo, undo+redo}, plus the file's own 2 `it.skip`
// segmentation cases, ported live as t.Skip with the same KNOWN reason.
func TestHistoryTreeConcurrentOverlappingUndoAfterGC(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	// undo range r1 (d1) vs r2 (d2), covering every overlap relationship.
	overlaps := []struct {
		name   string
		r1, r2 [2]int
	}{
		{"contained_by", [2]int{5, 7}, [2]int{3, 9}},
		{"contains", [2]int{3, 9}, [2]int{5, 7}},
		{"overlap_start", [2]int{5, 9}, [2]int{3, 7}},
		{"overlap_end", [2]int{3, 7}, [2]int{5, 9}},
		{"identical", [2]int{3, 7}, [2]int{3, 7}},
		{"adjacent", [2]int{3, 5}, [2]int{5, 7}},
	}

	for _, ov := range overlaps {
		ov := ov

		t.Run(fmt.Sprintf("converges on undo of overlapping deletes: %s", ov.name), func(t *testing.T) {
			ctx := context.Background()

			d1 := document.New(helper.TestKey(t))
			assert.NoError(t, c1.Attach(ctx, d1))
			defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
			d2 := document.New(helper.TestKey(t))
			assert.NoError(t, c2.Attach(ctx, d2))
			defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

			initFlatTreeDoc(t, d1)
			assert.NoError(t, c1.Sync(ctx))
			assert.NoError(t, c2.Sync(ctx))
			initial := d1.Root().GetTree("t").ToXML()

			assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetTree("t").Edit(ov.r1[0], ov.r1[1], nil, 0)
				return nil
			}, "d1 delete"))
			assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetTree("t").Edit(ov.r2[0], ov.r2[1], nil, 0)
				return nil
			}, "d2 delete"))
			settleTreeClients(ctx, t, c1, c2)
			settleTreeClients(ctx, t, c1, c2) // let GC purge the tombstones
			assertTreeConverges(t, d1, d2, "after deletes")

			// Both undos revive both deleted runs by identity, restoring the
			// pre-delete visible content on both replicas. (The internal
			// text-node segmentation may be finer than the original --
			// isolate splits at the span boundaries -- but both replicas
			// agree, which assertTreeConverges checks.)
			assert.NoError(t, d1.Undo())
			assert.NoError(t, d2.Undo())
			settleTreeClients(ctx, t, c1, c2)
			assertTreeConverges(t, d1, d2, "after undo")
			assert.Equal(t, initial, d1.Root().GetTree("t").ToXML(),
				"undo restores the initial visible content")
		})

		t.Run(fmt.Sprintf("converges on undo+redo of overlapping deletes: %s", ov.name), func(t *testing.T) {
			ctx := context.Background()

			d1 := document.New(helper.TestKey(t))
			assert.NoError(t, c1.Attach(ctx, d1))
			defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
			d2 := document.New(helper.TestKey(t))
			assert.NoError(t, c2.Attach(ctx, d2))
			defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

			initFlatTreeDoc(t, d1)
			assert.NoError(t, c1.Sync(ctx))
			assert.NoError(t, c2.Sync(ctx))
			initial := d1.Root().GetTree("t").ToXML()

			assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetTree("t").Edit(ov.r1[0], ov.r1[1], nil, 0)
				return nil
			}, "d1 delete"))
			assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetTree("t").Edit(ov.r2[0], ov.r2[1], nil, 0)
				return nil
			}, "d2 delete"))
			settleTreeClients(ctx, t, c1, c2)
			settleTreeClients(ctx, t, c1, c2)
			afterDeletes := d1.Root().GetTree("t").ToXML()

			assert.NoError(t, d1.Undo())
			assert.NoError(t, d2.Undo())
			settleTreeClients(ctx, t, c1, c2)
			assertTreeConverges(t, d1, d2, "after undo")
			assert.Equal(t, initial, d1.Root().GetTree("t").ToXML(),
				"undo restores the initial visible content")

			// Both redos re-remove both runs by identity, back to the
			// converged post-delete state.
			assert.NoError(t, d1.Redo())
			assert.NoError(t, d2.Redo())
			settleTreeClients(ctx, t, c1, c2)
			assertTreeConverges(t, d1, d2, "after redo")
			assert.Equal(t, afterDeletes, d1.Root().GetTree("t").ToXML(),
				"redo restores the post-delete visible content")
		})
	}

	// KNOWN (tracked, skipped): when a whole element is deleted concurrently
	// with a text edit INSIDE it and both undo AFTER GC, the visible content
	// converges but internal text-node segmentation can differ (one replica
	// un-tombstones the concurrent edit's finer split, the other recreates
	// the run monolithically from the element's span), so Marshal mismatches.
	// Tracked: docs/design/undo-redo-go-port.md:283-288, which names this
	// case by file and line and notes it is still skipped as of fa6cc513.
	t.Run("KNOWN: delete a whole <p> vs edit text inside it, both undo", func(t *testing.T) {
		t.Skip("KNOWN: when a whole element is deleted concurrently with a text edit inside it and both undo after GC, visible content converges but internal text-node segmentation can differ, so Marshal mismatches (tracked: docs/design/undo-redo-go-port.md:283-288)")

		ctx := context.Background()

		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		d2 := document.New(helper.TestKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "hello"}}},
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "world"}}},
			}})
			return nil
		}, "init"))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))

		// d1 removes the whole first <p>; d2 replaces text inside it.
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(0, 7, nil, 0)
			return nil
		}, "d1 delete <p>"))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(3, 5, &json.TreeNode{Type: "text", Value: "XY"}, 0)
			return nil
		}, "d2 edit inside"))
		settleTreeClients(ctx, t, c1, c2)
		assertTreeConverges(t, d1, d2, "after ops")

		assert.NoError(t, d1.Undo())
		assert.NoError(t, d2.Undo())
		settleTreeClients(ctx, t, c1, c2)
		assertTreeConverges(t, d1, d2, "after undo")
	})

	// KNOWN (tracked): deleting MULTIPLE elements concurrently with an edit
	// inside one of them, then both undo after GC, converges on visible
	// content but NOT on internal text-node segmentation -- so Marshal
	// differs. Tracked: docs/design/undo-redo-go-port.md:283-288, which
	// names this case by file and line and notes it is still skipped as of
	// fa6cc513.
	t.Run("KNOWN: delete two <p> vs edit inside first, both undo (segmentation)", func(t *testing.T) {
		t.Skip("KNOWN: deleting multiple elements concurrently with an edit inside one of them, then both undo after GC, converges on visible content but not on internal text-node segmentation, so Marshal differs (tracked: docs/design/undo-redo-go-port.md:283-288)")

		ctx := context.Background()

		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		d2 := document.New(helper.TestKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "aaaa"}}},
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "bbbb"}}},
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "cccc"}}},
			}})
			return nil
		}, "init"))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(0, 12, nil, 0)
			return nil
		}, "d1 delete first two <p>"))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 4, &json.TreeNode{Type: "text", Value: "XY"}, 0)
			return nil
		}, "d2 edit inside first <p>"))
		settleTreeClients(ctx, t, c1, c2)
		settleTreeClients(ctx, t, c1, c2)

		assert.NoError(t, d1.Undo())
		assert.NoError(t, d2.Undo())
		settleTreeClients(ctx, t, c1, c2)
		assertTreeConverges(t, d1, d2, "after undo")
	})
}

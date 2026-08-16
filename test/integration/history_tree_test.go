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

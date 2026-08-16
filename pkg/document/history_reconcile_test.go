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

package document_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
)

// drainDocEvents consumes a document's event channel until the test ends.
// ApplyChangePack sends every event it produces over a channel of capacity
// one while holding the document lock, so a document that receives more than
// one remote change over the life of a test has to be drained.
func drainDocEvents(t *testing.T, doc *document.Document) {
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-doc.Events():
			case <-done:
				return
			}
		}
	}()
	t.Cleanup(func() { close(done) })
}

// TestHistoryReconcileWithRedoOnly pins that a remote change reconciles the
// redo stack even when the undo stack is empty.
//
// applyChanges skips the reconcile loop entirely when no stack holds anything
// to reconcile, since Text.NormalizePos is linear in the split chain and the
// result would be discarded. The condition for skipping is that *both* stacks
// are empty: a document whose only pending entry is a redo -- every document
// that has undone its single local change, which is the first thing an editor
// binding's undo button produces -- must still be reconciled, or redoing after
// a concurrent remote edit replays at a stale position.
func TestHistoryReconcileWithRedoOnly(t *testing.T) {
	d1, d2, _, _ := newReplicas(t)
	drainDocEvents(t, d1)
	drainDocEvents(t, d2)

	// Seeded on d2 so d1 receives the text remotely and its undo stack holds
	// nothing but the single local edit made below.
	require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewText("t").Edit(0, 0, "0123456789")
		return nil
	}))
	crossSync(t, d1, d2)
	assert.Equal(t, 0, d1.UndoStackLenForTest())

	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("t").Edit(5, 6, "")
		return nil
	}))
	require.NoError(t, d1.Undo())
	require.False(t, d1.CanUndo(), "the only local change was undone")
	require.True(t, d1.CanRedo())

	top := d1.RedoStackTopForTest()
	require.Len(t, top, 1)
	edit, ok := top[0].Op.(*operations.Edit)
	require.True(t, ok)
	assert.Equal(t, 5, edit.From().RelativeOffset(), "anchored before the remote edit")
	assert.Equal(t, 5, edit.To().RelativeOffset())

	// A remote deletion of "01" moves everything the stacked redo addresses
	// two characters to the left.
	require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("t").Edit(0, 2, "")
		return nil
	}))
	crossSync(t, d1, d2)
	assert.Equal(t, "23456789", d1.Root().GetText("t").String())

	top = d1.RedoStackTopForTest()
	require.Len(t, top, 1)
	edit, ok = top[0].Op.(*operations.Edit)
	require.True(t, ok)
	assert.Equal(t, 3, edit.From().RelativeOffset(),
		"the redo entry must be reconciled against the remote deletion even "+
			"though the undo stack is empty")
	assert.Equal(t, 3, edit.To().RelativeOffset())
}

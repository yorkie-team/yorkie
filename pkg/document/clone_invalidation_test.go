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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

// Every path that mutates the document runs against the clone first and the
// document second, and a change that fails partway through is not a no-op on
// the clone: it keeps whatever the failing change applied before it failed,
// which the document never took. The clone is what the NEXT update reads and
// builds its operations from, so leaving a dirty one diverges every later
// change for the rest of the session -- silently, since nothing re-reads the
// document to notice.
//
// assertCloneMatchesDocument is the shared assertion: after a failure, the
// state the next update will run against is the document's own. It reads the
// clone through Document.Root (which rebuilds it from the document when it was
// dropped) and the document through Document.RootObject.
func assertCloneMatchesDocument(t *testing.T, d *document.Document) {
	t.Helper()
	assert.Equal(t, d.RootObject().Marshal(), d.Root().Marshal())
}

// TestUpdateDropsCloneWhenDocumentExecuteFails covers the failure of the
// document-side execute in Update: the updater has already run to completion
// on the clone by then, so the clone holds a change the document rejected.
func TestUpdateDropsCloneWhenDocumentExecuteFails(t *testing.T) {
	d := document.New("clone-update")
	require.NoError(t, d.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetInteger("kept", 1)
		return nil
	}))

	// Root hands out a json wrapper over the clone with a throwaway context,
	// so what it writes lands on the clone and never becomes a change: this
	// object exists for the clone alone, and the document cannot resolve its
	// identity.
	d.Root().SetNewObject("ghost")

	err := d.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetInteger("live", 2)
		r.GetObject("ghost").SetInteger("x", 3)
		return nil
	})
	require.Error(t, err)

	assertCloneMatchesDocument(t, d)
	assert.NotContains(t, d.Root().Marshal(), "ghost")

	// The first operation applied to the document and Execute does not roll
	// back, so the failed update still owes a change: the prefix that ran, on
	// the clientSeq it consumed. Dropping it would hide "live" from every peer
	// and leave a hole the server refuses the next push over, and leaving
	// changeID where it was would reissue the tickets this update spent.
	assert.Contains(t, d.RootObject().Marshal(), "live")
	pack := d.CreateChangePack()
	require.Len(t, pack.Changes, 2)
	failed := pack.Changes[1]
	assert.Equal(t, pack.Changes[0].ClientSeq()+1, failed.ClientSeq())
	assert.Len(t, failed.Operations(), 1)

	// The next update has to see the document, not the abandoned clone.
	require.NoError(t, d.Update(func(r *json.Object, _ *presence.Presence) error {
		assert.NotContains(t, r.Marshal(), "ghost")
		r.SetInteger("after", 4)
		return nil
	}))
	assertCloneMatchesDocument(t, d)
}

// TestUndoDropsCloneWhenExecuteFails covers executeUndoRedo: its operations
// run against the clone before the document, so a stacked entry whose second
// operation cannot execute leaves the first one applied to the clone only.
func TestUndoDropsCloneWhenExecuteFails(t *testing.T) {
	d := document.New("clone-undo")
	require.NoError(t, d.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetInteger("kept", 1)
		return nil
	}))

	// A fabricated entry: the first operation applies, the second names a
	// parent no replica holds, which is how a genuinely unexecutable reverse
	// (one whose target was purged since it was stacked) behaves.
	applied, err := crdt.NewPrimitive(2, time.NewTicket(1000, 0, d.ActorID()))
	require.NoError(t, err)
	orphaned, err := crdt.NewPrimitive(3, time.NewTicket(1001, 0, d.ActorID()))
	require.NoError(t, err)
	unknownParent := time.NewTicket(1002, 0, d.ActorID())

	d.PushUndoForTest([]document.HistoryOperation{
		{Op: operations.NewSet(d.RootObject().CreatedAt(), "undone", applied, nil)},
		{Op: operations.NewSet(unknownParent, "orphaned", orphaned, nil)},
	})
	require.Error(t, d.Undo())

	assertCloneMatchesDocument(t, d)
	assert.NotContains(t, d.Root().Marshal(), "undone")
}

// TestApplyChangesDropsCloneWhenRemoteChangeFails covers applyChanges: a
// remote change executes against the clone first, so one that fails on its
// second operation leaves the first applied to the clone and to nothing else.
func TestApplyChangesDropsCloneWhenRemoteChangeFails(t *testing.T) {
	d1 := document.New("clone-remote")
	d2 := document.New("clone-remote")

	require.NoError(t, d1.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewObject("obj").SetInteger("k", 1)
		return nil
	}))
	deliverChanges(t, d1, d2)

	// One change, two operations: the first lands on the root, the second
	// inside `obj`.
	require.NoError(t, d2.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetInteger("first", 1)
		r.GetObject("obj").SetInteger("k", 2)
		return nil
	}))

	// d1 removes `obj` and collects, so its identity is gone from both the
	// document and the clone before d2's change arrives: the second operation
	// can no longer resolve its parent.
	require.NoError(t, d1.Update(func(r *json.Object, _ *presence.Presence) error {
		r.Delete("obj")
		return nil
	}))
	d1.GarbageCollect(helper.MaxVersionVector(d1.ActorID(), d2.ActorID()))

	pack := d2.CreateChangePack()
	require.Error(t, d1.ApplyChangePack(change.NewPack(
		pack.DocumentKey,
		change.NewCheckpoint(0, 0),
		pack.Changes,
		time.InitialVersionVector,
		nil,
	)))

	assertCloneMatchesDocument(t, d1)
	assert.False(t, strings.Contains(d1.Root().Marshal(), "first"))
}

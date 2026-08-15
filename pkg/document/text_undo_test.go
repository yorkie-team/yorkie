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

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

// textOf returns the CRDT text under key on the document's real root (not
// the clone `Document.Root` hands out), so identity assertions read the
// state that is actually synced.
func textOf(t *testing.T, doc *document.Document, key string) *crdt.Text {
	t.Helper()
	text, ok := doc.RootObject().Get(key).(*crdt.Text)
	assert.True(t, ok, "%q should be a Text", key)
	return text
}

// liveTextIDs returns the identity of every live character run in the text
// under key, as "createdAt:offset" strings. A reverse that revives removed
// content under its original identity leaves these anchored on the ticket
// that first inserted the content; one that re-inserts a copy mints a new
// ticket and shows up here.
func liveTextIDs(t *testing.T, doc *document.Document, key string) []string {
	t.Helper()

	var ids []string
	for _, node := range textOf(t, doc, key).Nodes() {
		if node.RemovedAt() == nil {
			ids = append(ids, node.ID().ToTestString())
		}
	}
	return ids
}

// insertTicket returns the createdAt every character of a single-insertion
// text carries, read from its first node.
func insertTicket(t *testing.T, doc *document.Document, key string) *time.Ticket {
	t.Helper()

	nodes := textOf(t, doc, key).Nodes()
	assert.NotEmpty(t, nodes)
	return nodes[0].ID().CreatedAt()
}

// assertAllCharsFrom asserts that no live character in the text under key
// carries an identity other than seed -- i.e. that undo revived the removed
// run rather than re-inserting a copy under the undo's own ticket.
func assertAllCharsFrom(t *testing.T, doc *document.Document, key string, seed *time.Ticket) {
	t.Helper()

	for _, node := range textOf(t, doc, key).Nodes() {
		if node.RemovedAt() != nil {
			continue
		}
		assert.Zero(t, node.ID().CreatedAt().Compare(seed),
			"live node %s should keep the identity it was inserted under (%s)",
			node.ID().ToTestString(), seed.ToTestString())
	}
}

// newTextDoc returns a document holding an empty Text under "t". The text is
// created in its own change so a later Undo targets only the edit under test.
func newTextDoc(t *testing.T) *document.Document {
	t.Helper()

	doc := document.New("d1")
	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewText("t")
		return nil
	}))
	return doc
}

func editText(t *testing.T, doc *document.Document, from, to int, content string) {
	t.Helper()

	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("t").Edit(from, to, content)
		return nil
	}))
}

func TestTextUndo(t *testing.T) {
	t.Run("insert undo redo survives gc test", func(t *testing.T) {
		// The reverse of an insert re-removes the inserted run by its
		// identity (retombstoneSpans), so undo leaves a tombstone that GC may
		// collect, and redo revives that same identity.
		doc := newTextDoc(t)
		editText(t, doc, 0, 0, "ABCD")
		assert.Equal(t, `[{"val":"ABCD"}]`, doc.Root().GetText("t").Marshal())
		assert.True(t, doc.CanUndo())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, `[]`, doc.Root().GetText("t").Marshal())
		assert.Equal(t, 1, doc.GarbageLen())
		assert.Equal(t, 1, doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID())))
		assert.Equal(t, `[]`, doc.Root().GetText("t").Marshal())
		assert.Equal(t, 0, doc.GarbageLen())

		assert.True(t, doc.CanRedo())
		assert.NoError(t, doc.Redo())
		assert.Equal(t, `[{"val":"ABCD"}]`, doc.Root().GetText("t").Marshal())
		assert.Equal(t, 0, doc.GarbageLen())
	})

	t.Run("delete undo revives by identity and survives gc test", func(t *testing.T) {
		// The reverse of a delete revives the removed run under its original
		// identity (restoreSpans). Two things must hold afterwards: the
		// revived characters keep the ticket they were inserted under, and
		// the tombstone that held them is no longer pending GC -- otherwise a
		// GC pass purges live content.
		doc := newTextDoc(t)
		editText(t, doc, 0, 0, "ABCD")
		seed := insertTicket(t, doc, "t")
		idsBefore := liveTextIDs(t, doc, "t")

		editText(t, doc, 1, 3, "")
		assert.Equal(t, `[{"val":"A"},{"val":"D"}]`, doc.Root().GetText("t").Marshal())
		assert.Equal(t, 1, doc.GarbageLen())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, `[{"val":"A"},{"val":"BC"},{"val":"D"}]`, doc.Root().GetText("t").Marshal())
		assertAllCharsFrom(t, doc, "t", seed)
		assert.NotEqual(t, idsBefore, liveTextIDs(t, doc, "t"),
			"sanity: the delete split the run, so the id list is not the pre-delete one")

		// The revived run must not still be registered for collection.
		assert.Equal(t, 0, doc.GarbageLen())
		assert.Equal(t, 0, doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID())))
		assert.Equal(t, `[{"val":"A"},{"val":"BC"},{"val":"D"}]`, doc.Root().GetText("t").Marshal())
		assertAllCharsFrom(t, doc, "t", seed)

		assert.True(t, doc.CanRedo())
		assert.NoError(t, doc.Redo())
		assert.Equal(t, `[{"val":"A"},{"val":"D"}]`, doc.Root().GetText("t").Marshal())
		assert.Equal(t, 1, doc.GarbageLen())
	})

	t.Run("replace undo redo survives gc test", func(t *testing.T) {
		// A replace both removes and inserts, so its reverse carries both
		// span sets: revive what was removed, re-remove what was inserted.
		doc := newTextDoc(t)
		editText(t, doc, 0, 0, "ABCD")
		seed := insertTicket(t, doc, "t")

		editText(t, doc, 1, 3, "XY")
		assert.Equal(t, `[{"val":"A"},{"val":"XY"},{"val":"D"}]`, doc.Root().GetText("t").Marshal())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, `[{"val":"A"},{"val":"BC"},{"val":"D"}]`, doc.Root().GetText("t").Marshal())
		assertAllCharsFrom(t, doc, "t", seed)
		// "XY" is now the only tombstone; "BC" came back off the GC list.
		assert.Equal(t, 1, doc.GarbageLen())
		assert.Equal(t, 1, doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID())))
		assert.Equal(t, `[{"val":"A"},{"val":"BC"},{"val":"D"}]`, doc.Root().GetText("t").Marshal())
		assertAllCharsFrom(t, doc, "t", seed)

		assert.True(t, doc.CanRedo())
		assert.NoError(t, doc.Redo())
		assert.Equal(t, `[{"val":"A"},{"val":"XY"},{"val":"D"}]`, doc.Root().GetText("t").Marshal())
	})

	t.Run("chained undo redo returns to each state test", func(t *testing.T) {
		doc := newTextDoc(t)
		editText(t, doc, 0, 0, "ABCD")
		editText(t, doc, 4, 4, "EF")
		editText(t, doc, 0, 2, "")
		assert.Equal(t, "CDEF", textOf(t, doc, "t").String())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, "ABCDEF", textOf(t, doc, "t").String())
		assert.NoError(t, doc.Undo())
		assert.Equal(t, "ABCD", textOf(t, doc, "t").String())
		assert.NoError(t, doc.Undo())
		assert.Equal(t, "", textOf(t, doc, "t").String())

		assert.NoError(t, doc.Redo())
		assert.Equal(t, "ABCD", textOf(t, doc, "t").String())
		assert.NoError(t, doc.Redo())
		assert.Equal(t, "ABCDEF", textOf(t, doc, "t").String())
		assert.NoError(t, doc.Redo())
		assert.Equal(t, "CDEF", textOf(t, doc, "t").String())
		assert.False(t, doc.CanRedo())
	})

	t.Run("undo keeps attributes of the removed run test", func(t *testing.T) {
		doc := newTextDoc(t)
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("t").Edit(0, 0, "AB", map[string]string{"b": "1"})
			return nil
		}))
		assert.Equal(t, `[{"attrs":{"b":"1"},"val":"AB"}]`, doc.Root().GetText("t").Marshal())

		editText(t, doc, 0, 2, "")
		assert.Equal(t, `[]`, doc.Root().GetText("t").Marshal())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, `[{"attrs":{"b":"1"},"val":"AB"}]`, doc.Root().GetText("t").Marshal())
		assert.Equal(t, 0, doc.GarbageLen())
		assert.Equal(t, 0, doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID())))
		assert.Equal(t, `[{"attrs":{"b":"1"},"val":"AB"}]`, doc.Root().GetText("t").Marshal())
	})

	t.Run("undo survives a snapshot round trip test", func(t *testing.T) {
		// A restore clears a tombstone in place. Whether that survives being
		// written out and read back is a separate question from whether it
		// held in memory: DeepCopy and snapshot decode rebuild state by
		// replaying setters, and both feed the GC registration.
		doc := newTextDoc(t)
		editText(t, doc, 0, 0, "ABCD")
		seed := insertTicket(t, doc, "t")
		editText(t, doc, 1, 3, "")
		assert.NoError(t, doc.Undo())
		assert.Equal(t, "ABCD", textOf(t, doc, "t").String())
		assert.Equal(t, 0, doc.GarbageLen())

		bytes, err := converter.SnapshotToBytes(doc.RootObject(), doc.AllPresences())
		assert.NoError(t, err)

		restored := document.New("d1")
		assert.NoError(t, restored.ApplyChangePack(change.NewPack(
			restored.Key(),
			change.InitialCheckpoint,
			nil,
			helper.MaxVersionVector(restored.ActorID()),
			bytes,
		)))

		assert.Equal(t, doc.Marshal(), restored.Marshal())
		assert.Equal(t, "ABCD", textOf(t, restored, "t").String())
		assertAllCharsFrom(t, restored, "t", seed)
		assert.Equal(t, 0, restored.GarbageLen())
		assert.Equal(t, 0, restored.GarbageCollect(helper.MaxVersionVector(restored.ActorID())))
		assert.Equal(t, "ABCD", textOf(t, restored, "t").String())
	})

	t.Run("empty edit is undoable as a no-op test", func(t *testing.T) {
		// An edit that neither removes nor inserts still produces a reverse,
		// matching JS. That reverse is the only one built as an ordinary
		// (non-restore) edit, so it is also the only one that reaches the
		// position-refinement step undo operations run before executing.
		doc := newTextDoc(t)
		editText(t, doc, 0, 0, "ABCD")
		editText(t, doc, 2, 2, "")
		assert.Equal(t, "ABCD", textOf(t, doc, "t").String())

		assert.True(t, doc.CanUndo())
		assert.NoError(t, doc.Undo())
		assert.Equal(t, "ABCD", textOf(t, doc, "t").String())
		assert.Equal(t, 0, doc.GarbageLen())

		assert.NoError(t, doc.Redo())
		assert.Equal(t, "ABCD", textOf(t, doc, "t").String())
	})
}

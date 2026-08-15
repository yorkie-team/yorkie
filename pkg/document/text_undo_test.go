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

// assertAllCharsFrom asserts that every live character in the text under key
// carries one of the given identities -- i.e. that undo and redo revived
// content rather than re-inserting copies under their own tickets. Pass one
// seed per original insertion.
func assertAllCharsFrom(t *testing.T, doc *document.Document, key string, seeds ...*time.Ticket) {
	t.Helper()

	for _, node := range textOf(t, doc, key).Nodes() {
		if node.RemovedAt() != nil {
			continue
		}

		known := false
		for _, seed := range seeds {
			if node.ID().CreatedAt().Compare(seed) == 0 {
				known = true
				break
			}
		}
		assert.True(t, known,
			"live node %s should keep an identity it was inserted under, not one minted by undo/redo",
			node.ID().ToTestString())
	}
}

// textAttrs returns the live style attributes carried by the first node of
// the text under key, as a plain map -- so a test can assert on attribute
// state directly rather than inferring it from marshalled text.
func textAttrs(t *testing.T, doc *document.Document, key string) map[string]string {
	t.Helper()

	nodes := textOf(t, doc, key).Nodes()
	assert.NotEmpty(t, nodes)
	return nodes[0].Value().Attrs().Elements()
}

// runTicket returns the identity of the node holding the given run, live or
// tombstoned. Used to pin that a run keeps its identity across a full
// undo/redo cycle, including one that purges and recreates it.
func runTicket(t *testing.T, doc *document.Document, key, value string) *time.Ticket {
	t.Helper()

	for _, node := range textOf(t, doc, key).Nodes() {
		if node.Value().Value() == value {
			return node.ID().CreatedAt()
		}
	}
	t.Fatalf("no node holding %q in text %q", value, key)
	return nil
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
		seed := insertTicket(t, doc, "t")
		assert.Equal(t, `[{"val":"ABCD"}]`, doc.Root().GetText("t").Marshal())
		assert.True(t, doc.CanUndo())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, `[]`, doc.Root().GetText("t").Marshal())
		assert.Equal(t, 1, doc.GarbageLen())
		assert.Equal(t, 1, doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID())))
		assert.Equal(t, `[]`, doc.Root().GetText("t").Marshal())
		assert.Equal(t, 0, doc.GarbageLen())

		// The redo revives content the GC pass above purged, so it has to
		// recreate the run. Collecting again afterwards is what catches a
		// revived node left registered for collection: without it, a stale
		// registration only surfaces on some later, unrelated GC pass.
		assert.True(t, doc.CanRedo())
		assert.NoError(t, doc.Redo())
		assert.Equal(t, `[{"val":"ABCD"}]`, doc.Root().GetText("t").Marshal())
		assertAllCharsFrom(t, doc, "t", seed)
		assert.Equal(t, 0, doc.GarbageLen())
		assert.Equal(t, 0, doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID())))
		assert.Equal(t, `[{"val":"ABCD"}]`, doc.Root().GetText("t").Marshal())
		assertAllCharsFrom(t, doc, "t", seed)
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
		inserted := runTicket(t, doc, "t", "XY")

		assert.NoError(t, doc.Undo())
		assert.Equal(t, `[{"val":"A"},{"val":"BC"},{"val":"D"}]`, doc.Root().GetText("t").Marshal())
		assertAllCharsFrom(t, doc, "t", seed)
		// "XY" is now the only tombstone; "BC" came back off the GC list.
		assert.Equal(t, 1, doc.GarbageLen())
		assert.Equal(t, 1, doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID())))
		assert.Equal(t, `[{"val":"A"},{"val":"BC"},{"val":"D"}]`, doc.Root().GetText("t").Marshal())
		assertAllCharsFrom(t, doc, "t", seed)

		// The redo has to do both halves against a chain the GC pass changed
		// underneath it: recreate the purged "XY" and re-remove "BC". Both
		// runs must come back under the identity they were born with, and the
		// re-removed one must be the only thing left pending collection.
		assert.True(t, doc.CanRedo())
		assert.NoError(t, doc.Redo())
		assert.Equal(t, `[{"val":"A"},{"val":"XY"},{"val":"D"}]`, doc.Root().GetText("t").Marshal())
		assertAllCharsFrom(t, doc, "t", seed, inserted)
		assert.Zero(t, runTicket(t, doc, "t", "XY").Compare(inserted),
			"the revived insertion must keep its original identity, not one minted by the redo")
		assert.Equal(t, 1, doc.GarbageLen())
		assert.Equal(t, 1, doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID())))
		assert.Equal(t, `[{"val":"A"},{"val":"XY"},{"val":"D"}]`, doc.Root().GetText("t").Marshal())
		assertAllCharsFrom(t, doc, "t", seed, inserted)
	})

	t.Run("chained undo redo returns to each state test", func(t *testing.T) {
		// Chained undo/redo is where a copy-reinserted reverse would show up:
		// a fresh node sorts ahead of an as-yet-unrevived neighbour and the
		// text comes back in the wrong order. So every step asserts identity
		// and the pending-collection count, not just the visible text --
		// content alone would pass with stale registrations accumulating.
		doc := newTextDoc(t)
		editText(t, doc, 0, 0, "ABCD")
		first := insertTicket(t, doc, "t")
		editText(t, doc, 4, 4, "EF")
		second := runTicket(t, doc, "t", "EF")
		editText(t, doc, 0, 2, "")
		assert.Equal(t, "CDEF", textOf(t, doc, "t").String())
		assert.Equal(t, 1, doc.GarbageLen(), `"AB" is tombstoned`)

		// Undo the delete: "AB" comes back and stops being garbage.
		assert.NoError(t, doc.Undo())
		assert.Equal(t, "ABCDEF", textOf(t, doc, "t").String())
		assertAllCharsFrom(t, doc, "t", first, second)
		assert.Equal(t, 0, doc.GarbageLen())

		// Undo the "EF" insert: it is re-removed by identity.
		assert.NoError(t, doc.Undo())
		assert.Equal(t, "ABCD", textOf(t, doc, "t").String())
		assertAllCharsFrom(t, doc, "t", first)
		assert.Equal(t, 1, doc.GarbageLen(), `"EF" is tombstoned`)

		// Undo the "ABCD" insert: its span covers both pieces the delete
		// split it into, so both are re-removed.
		assert.NoError(t, doc.Undo())
		assert.Equal(t, "", textOf(t, doc, "t").String())
		assert.Equal(t, 3, doc.GarbageLen(), `"EF", "AB" and "CD"`)

		assert.NoError(t, doc.Redo())
		assert.Equal(t, "ABCD", textOf(t, doc, "t").String())
		assertAllCharsFrom(t, doc, "t", first)
		assert.Equal(t, 1, doc.GarbageLen())

		assert.NoError(t, doc.Redo())
		assert.Equal(t, "ABCDEF", textOf(t, doc, "t").String())
		assertAllCharsFrom(t, doc, "t", first, second)
		assert.Equal(t, 0, doc.GarbageLen())

		assert.NoError(t, doc.Redo())
		assert.Equal(t, "CDEF", textOf(t, doc, "t").String())
		assertAllCharsFrom(t, doc, "t", first, second)
		assert.Equal(t, 1, doc.GarbageLen())
		assert.False(t, doc.CanRedo())

		// Collecting at the end must leave the final state untouched: every
		// revive above had to unregister what it brought back, or a pass here
		// purges live text.
		assert.Equal(t, 1, doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID())))
		assert.Equal(t, "CDEF", textOf(t, doc, "t").String())
		assertAllCharsFrom(t, doc, "t", first, second)
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

	t.Run("style undo restores the previous attribute value test", func(t *testing.T) {
		doc := newTextDoc(t)
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("t").Edit(0, 0, "ABCD", map[string]string{"bold": "true"})
			return nil
		}))

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("t").Style(0, 4, map[string]string{"bold": "false"})
			return nil
		}))
		assert.Equal(t, map[string]string{"bold": "false"}, textAttrs(t, doc, "t"))

		assert.True(t, doc.CanUndo())
		assert.NoError(t, doc.Undo())
		assert.Equal(t, map[string]string{"bold": "true"}, textAttrs(t, doc, "t"),
			"undo should restore the value the attribute held before the style call")

		assert.True(t, doc.CanRedo())
		assert.NoError(t, doc.Redo())
		assert.Equal(t, map[string]string{"bold": "false"}, textAttrs(t, doc, "t"))
	})

	t.Run("style undo removes an attribute that did not exist before test", func(t *testing.T) {
		// The absent-key branch: undoing a style that added a key must
		// remove it, not set it to the empty string.
		doc := newTextDoc(t)
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("t").Edit(0, 0, "ABCD")
			return nil
		}))
		assert.Empty(t, textAttrs(t, doc, "t"))

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("t").Style(0, 4, map[string]string{"italic": "true"})
			return nil
		}))
		assert.Equal(t, map[string]string{"italic": "true"}, textAttrs(t, doc, "t"))

		assert.True(t, doc.CanUndo())
		assert.NoError(t, doc.Undo())
		attrs := textAttrs(t, doc, "t")
		_, exists := attrs["italic"]
		assert.False(t, exists, "undo should remove the key, not set it to \"\"")
		assert.Empty(t, attrs)

		assert.True(t, doc.CanRedo())
		assert.NoError(t, doc.Redo())
		assert.Equal(t, map[string]string{"italic": "true"}, textAttrs(t, doc, "t"))
	})

	t.Run("style undo unions restore and removal across keys test", func(t *testing.T) {
		// One style call touching a mix of an existing key (bold) and a new
		// key (italic) must produce a single reverse that both restores
		// bold and removes italic -- proving Style.Execute processes the
		// set-attributes and remove-attributes branches independently
		// rather than exclusively, since that combined reverse is itself
		// executed again on redo.
		doc := newTextDoc(t)
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("t").Edit(0, 0, "ABCD", map[string]string{"bold": "true"})
			return nil
		}))

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("t").Style(0, 4, map[string]string{"bold": "false", "italic": "true"})
			return nil
		}))
		assert.Equal(t, map[string]string{"bold": "false", "italic": "true"}, textAttrs(t, doc, "t"))

		assert.NoError(t, doc.Undo())
		attrs := textAttrs(t, doc, "t")
		assert.Equal(t, map[string]string{"bold": "true"}, attrs,
			"bold should be restored and italic removed by the same reverse")

		assert.NoError(t, doc.Redo())
		assert.Equal(t, map[string]string{"bold": "false", "italic": "true"}, textAttrs(t, doc, "t"))
	})

	t.Run("style undo survives a snapshot round trip test", func(t *testing.T) {
		// Restore-only: the attribute this reverse touches held a value
		// both before and after (true -> false -> true), so undo never
		// tombstones an RHT node here. A scenario where undo removes a
		// key that did not exist before would also need to survive this
		// round trip, but a text node attribute's isRemoved flag is not
		// carried by the snapshot encoding (pre-existing in both SDKs --
		// see docs/tasks/active/20260816-remote-redo-replica-divergence-todo.md),
		// so that combination is not exercised here.
		doc := newTextDoc(t)
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("t").Edit(0, 0, "ABCD", map[string]string{"bold": "true"})
			return nil
		}))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("t").Style(0, 4, map[string]string{"bold": "false"})
			return nil
		}))
		assert.NoError(t, doc.Undo())
		assert.Equal(t, map[string]string{"bold": "true"}, textAttrs(t, doc, "t"))

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
		assert.Equal(t, map[string]string{"bold": "true"}, textAttrs(t, restored, "t"))
	})
}

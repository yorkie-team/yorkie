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
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

// attrsOfRun returns the style attributes carried by the live node holding the
// given run in the text under "t". Reading the node rather than the marshalled
// string pins what the CRDT holds, not how it prints.
func attrsOfRun(t *testing.T, doc *document.Document, value string) map[string]string {
	t.Helper()

	for _, node := range textOf(t, doc, "t").Nodes() {
		if node.RemovedAt() == nil && node.Value().Value() == value {
			return node.Value().Attrs().Elements()
		}
	}
	t.Fatalf("no live node holding %q in text %q", value, "t")
	return nil
}

// editTextWithAttrs inserts content with style attributes, in its own change
// so a later Undo targets only that edit.
func editTextWithAttrs(
	t *testing.T,
	doc *document.Document,
	from, to int,
	content string,
	attrs map[string]string,
) {
	t.Helper()

	require.NoError(t, doc.Update(func(root *json.Object, _ *presence.Presence) error {
		root.GetText("t").Edit(from, to, content, attrs)
		return nil
	}))
}

// TestTextRedoKeepsAttributesAfterCollection is a regression for yorkie#2273.
//
// The reverse of an insert records the inserted run as a retombstoneSpan so
// the undo can re-remove it by identity. The redo flips the direction and
// restores that same span: a replica still holding the tombstone un-tombstones
// the original node, attributes intact, while a replica that collected it in
// between must recreate the node from the span alone. If the span carries no
// attributes, those two replicas disagree about the revived text.
func TestTextRedoKeepsAttributesAfterCollection(t *testing.T) {
	t.Run("redo on a collected replica revives the attributes test", func(t *testing.T) {
		doc := newTextDoc(t)
		editText(t, doc, 0, 0, "ab")
		editTextWithAttrs(t, doc, 1, 1, "X", map[string]string{"b": "1"})
		assert.Equal(t, map[string]string{"b": "1"}, attrsOfRun(t, doc, "X"))

		// Undo tombstones "X" by identity; collection then purges it, so the
		// redo cannot un-tombstone in place and has to recreate the node from
		// the span.
		require.NoError(t, doc.Undo())
		assert.Equal(t, 1, doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID())))
		assert.Equal(t, 0, doc.GarbageLen())

		require.NoError(t, doc.Redo())
		assert.Equal(t, "aXb", textOf(t, doc, "t").String())
		assert.Equal(t, map[string]string{"b": "1"}, attrsOfRun(t, doc, "X"),
			"the recreated run lost the attributes it was inserted with")
	})

	t.Run("collecting replica converges with one that kept the tombstone test", func(t *testing.T) {
		// The scenario from the issue: only one of the two replicas collects
		// between the undo and the redo, so the two take different arms of the
		// restore path for the same operation.
		y := newTextDoc(t)
		editText(t, y, 0, 0, "ab")

		x := document.New("d1")
		deliverChanges(t, y, x)

		editTextWithAttrs(t, y, 1, 1, "X", map[string]string{"b": "1"})
		require.NoError(t, y.Undo())
		deliverChanges(t, y, x)

		vector := helper.MaxVersionVector(x.ActorID(), y.ActorID())
		assert.Equal(t, 1, y.GarbageCollect(vector))
		assert.Equal(t, 1, x.GarbageLen(), "x still holds the tombstone")

		require.NoError(t, y.Redo())
		deliverChanges(t, y, x)

		assert.Equal(t, "aXb", textOf(t, y, "t").String())
		assert.Equal(t, map[string]string{"b": "1"}, attrsOfRun(t, y, "X"))
		assert.Equal(t, x.Marshal(), y.Marshal(),
			"the replica that collected the tombstone revived the text without its attributes")
	})

	t.Run("chained undo redo keeps the attributes across collections test", func(t *testing.T) {
		doc := newTextDoc(t)
		editText(t, doc, 0, 0, "ab")
		editTextWithAttrs(t, doc, 1, 1, "X", map[string]string{"b": "1", "i": "1"})

		for range 3 {
			require.NoError(t, doc.Undo())
			doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID()))
			require.NoError(t, doc.Redo())

			assert.Equal(t, "aXb", textOf(t, doc, "t").String())
			assert.Equal(t, map[string]string{"b": "1", "i": "1"}, attrsOfRun(t, doc, "X"))
		}
	})

	t.Run("plain insert redo stays unstyled test", func(t *testing.T) {
		// An edit that carried no attributes must not gain any from the span,
		// whichever arm of the restore path the redo takes.
		doc := newTextDoc(t)
		editText(t, doc, 0, 0, "ab")
		editText(t, doc, 1, 1, "X")

		require.NoError(t, doc.Undo())
		doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID()))
		require.NoError(t, doc.Redo())

		assert.Equal(t, "aXb", textOf(t, doc, "t").String())
		assert.Empty(t, attrsOfRun(t, doc, "X"))
	})
}

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
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

// styleTree applies a style over the whole "<p>...</p>" element (index range
// 0 to 4, the same range editTree(0, 4, ...) uses to touch the whole element)
// under key "t", in its own change.
func styleTree(t *testing.T, doc *document.Document, attrs map[string]string) {
	t.Helper()

	assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
		r.GetTree("t").Style(0, 4, attrs)
		return nil
	}))
}

// removeStyleTree removes the given attribute keys over the whole element
// under key "t", in its own change.
func removeStyleTree(t *testing.T, doc *document.Document, keys []string) {
	t.Helper()

	assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
		r.GetTree("t").RemoveStyle(0, 4, keys)
		return nil
	}))
}

// treePAttrs returns the live style attributes carried by the tree's "p"
// node under key "t", as a plain map -- so a test can assert on attribute
// state directly rather than inferring it from rendered XML.
func treePAttrs(t *testing.T, doc *document.Document) map[string]string {
	t.Helper()

	tree := treeCRDT(t, doc)
	children := tree.Root().Children()
	assert.NotEmpty(t, children)
	if children[0].Attrs == nil {
		return nil
	}
	return children[0].Attrs.Elements()
}

func TestTreeStyleUndo(t *testing.T) {
	t.Run("style undo restores the previous attribute value test", func(t *testing.T) {
		doc := newTreeDoc(t, "000000000000000000000001")
		styleTree(t, doc, map[string]string{"bold": "true"})
		styleTree(t, doc, map[string]string{"bold": "false"})
		assert.Equal(t, map[string]string{"bold": "false"}, treePAttrs(t, doc))

		assert.True(t, doc.CanUndo())
		assert.NoError(t, doc.Undo())
		assert.Equal(t, map[string]string{"bold": "true"}, treePAttrs(t, doc),
			"undo should restore the value the attribute held before the style call")

		assert.True(t, doc.CanRedo())
		assert.NoError(t, doc.Redo())
		assert.Equal(t, map[string]string{"bold": "false"}, treePAttrs(t, doc))
	})

	t.Run("style undo removes an attribute that did not exist before test", func(t *testing.T) {
		// The absent-key branch: undoing a style that added a key must
		// remove it, not set it to the empty string.
		doc := newTreeDoc(t, "000000000000000000000001")
		assert.Empty(t, treePAttrs(t, doc))

		styleTree(t, doc, map[string]string{"italic": "true"})
		assert.Equal(t, map[string]string{"italic": "true"}, treePAttrs(t, doc))

		assert.True(t, doc.CanUndo())
		assert.NoError(t, doc.Undo())
		attrs := treePAttrs(t, doc)
		_, exists := attrs["italic"]
		assert.False(t, exists, "undo should remove the key, not set it to \"\"")
		assert.Empty(t, attrs)

		assert.True(t, doc.CanRedo())
		assert.NoError(t, doc.Redo())
		assert.Equal(t, map[string]string{"italic": "true"}, treePAttrs(t, doc))
	})

	t.Run("style undo unions restore and removal across keys test", func(t *testing.T) {
		// One style call touching a mix of an existing key (bold) and a new
		// key (italic) produces a single reverse TreeStyle carrying both a
		// restore (bold) and a removal (italic). Executing that reverse
		// only applies the restore half, though: JS's
		// tree_style_operation.ts execute is `if (attributes.size) {...}
		// else {...}`, not two independent branches like Text's, so the
		// attributesToRemove half of a combined reverse is dropped whenever
		// attributes is also non-empty. This is a known JS defect (PR #1221
		// copied Text's combined-reverse constructor without also copying
		// Text's independent-if execute shape from PR #1174), preserved
		// here rather than fixed, per this port's rule not to fix a defect
		// the JS SDK still has. See
		// docs/tasks/active/20260816-remote-redo-replica-divergence-todo.md.
		// italic is therefore left untouched by both undo and redo below.
		doc := newTreeDoc(t, "000000000000000000000001")
		styleTree(t, doc, map[string]string{"bold": "true"})

		styleTree(t, doc, map[string]string{"bold": "false", "italic": "true"})
		assert.Equal(t, map[string]string{"bold": "false", "italic": "true"}, treePAttrs(t, doc))

		assert.NoError(t, doc.Undo())
		assert.Equal(t, map[string]string{"bold": "true", "italic": "true"}, treePAttrs(t, doc),
			"bold is restored; italic is not removed by the same reverse")

		assert.NoError(t, doc.Redo())
		assert.Equal(t, map[string]string{"bold": "false", "italic": "true"}, treePAttrs(t, doc))
	})

	t.Run("removeStyle undo restores the removed attribute test", func(t *testing.T) {
		doc := newTreeDoc(t, "000000000000000000000001")
		styleTree(t, doc, map[string]string{"bold": "true"})
		assert.Equal(t, map[string]string{"bold": "true"}, treePAttrs(t, doc))

		removeStyleTree(t, doc, []string{"bold"})
		assert.Empty(t, treePAttrs(t, doc))

		assert.True(t, doc.CanUndo())
		assert.NoError(t, doc.Undo())
		assert.Equal(t, map[string]string{"bold": "true"}, treePAttrs(t, doc),
			"undo should restore the removed attribute")

		assert.True(t, doc.CanRedo())
		assert.NoError(t, doc.Redo())
		assert.Empty(t, treePAttrs(t, doc))
	})

	t.Run("removeStyle of an absent key is not undoable test", func(t *testing.T) {
		// RemoveStyle reports no PrevAttr for a key that was already absent
		// -- there is nothing to restore, so no reverse is produced and the
		// undo stack does not grow.
		doc := newTreeDoc(t, "000000000000000000000001")
		before := doc.UndoStackLenForTest()

		removeStyleTree(t, doc, []string{"italic"})
		assert.Equal(t, before, doc.UndoStackLenForTest(),
			"removing an attribute that was never set has no reverse")
	})

	t.Run("style undo survives a snapshot round trip test", func(t *testing.T) {
		// Unlike Text (whose per-character attribute encoding does not
		// carry a tombstoned attribute through a snapshot, filed in
		// docs/tasks/active/20260816-remote-redo-replica-divergence-todo.md),
		// a tree node's attributes are encoded via the generic
		// toRHT/fromRHT pair, which does carry isRemoved -- so the
		// absent-before-key removal branch is exercised directly through a
		// snapshot round trip here, not a DeepCopy substitute.
		doc := newTreeDoc(t, "000000000000000000000001")
		styleTree(t, doc, map[string]string{"italic": "true"})
		assert.NoError(t, doc.Undo())
		assert.Empty(t, treePAttrs(t, doc))

		bytes, err := converter.SnapshotToBytes(doc.RootObject(), doc.AllPresences())
		assert.NoError(t, err)

		restored := document.New("doc")
		assert.NoError(t, restored.ApplyChangePack(change.NewPack(
			restored.Key(),
			change.InitialCheckpoint,
			nil,
			helper.MaxVersionVector(restored.ActorID()),
			bytes,
		)))

		assert.Equal(t, doc.Marshal(), restored.Marshal())
		assert.Empty(t, treePAttrs(t, restored),
			"a removed-before-restore key must not be resurrected by the snapshot round trip")
	})
}

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
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
)

// Styling a range and then undoing the style is the only route that tombstones
// an attribute: the reverse Style carries attributesToRemove, and RHT.Remove
// replaces the live node with a tombstone stamped by the removal.
//
// The tree and the text halves answer that with the same shape of content, so
// they must move the same bytes through the same ledger. They do not (#2007),
// and the tree is the one that is right:
//
//	                style      undo         gc         rebuilt
//	  tree      {24,168}   {20,144}   {20,144}   {20,144}
//	  text      {24,120}   {24,120}   {24,120}    {20,96}
//
// Two defects, visible as two different failures:
//
//  1. Double charge. TextValue.DataSize counts removed attributes, so the
//     tombstone stays inside Live while RegisterGCPair also put it in GC. Live
//     never returns to its pre-style value the way the tree's does.
//
//  2. Stranded on purge. Root.collect subtracts the child's size from GC only,
//     so once the tombstone is purged its bytes are in neither the RHT nor GC,
//     yet Live still holds them. That is permanent drift, one attribute per
//     tombstone ever purged.

// styleThenUndoText returns a text document holding one tombstoned attribute,
// and the Live size it had before the style was applied.
func styleThenUndoText(t *testing.T) (*document.Document, resourceSize) {
	t.Helper()

	doc := document.New("d")
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewText("k").Edit(0, 0, "abcdefghij")
		return nil
	}))
	before := sizeOf(doc)

	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("k").Style(0, 10, map[string]string{"b": "1"})
		return nil
	}))
	require.NoError(t, doc.Undo())

	return doc, before
}

// styleThenUndoTree is the same history on a tree, which is the control: it
// already behaves correctly, so it shows the assertions below are reachable
// rather than aspirational.
func styleThenUndoTree(t *testing.T) (*document.Document, resourceSize) {
	t.Helper()

	doc := document.New("d")
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "abcdefghij"}}},
		}})
		return nil
	}))
	before := sizeOf(doc)

	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Style(0, 1, map[string]string{"b": "1"})
		return nil
	}))
	require.NoError(t, doc.Undo())

	return doc, before
}

type resourceSize struct {
	Data int
	Meta int
}

func sizeOf(doc *document.Document) resourceSize {
	live := doc.DocSize().Live
	return resourceSize{Data: live.Data, Meta: live.Meta}
}

// A tombstoned attribute belongs to GC, not to Live. Live must therefore return
// to exactly what it was before the style that introduced the key.
func TestTombstonedTextAttrLeavesLive(t *testing.T) {
	doc, before := styleThenUndoText(t)

	require.Equal(t, 1, doc.GarbageLen(), "the tombstone is registered as garbage")
	require.Equal(t, before, sizeOf(doc),
		"the tombstone is charged to GC and to Live at once")
}

// The same assertion against the tree, which already passes. It is here so a
// change that "fixes" the text half by loosening the rule fails loudly.
func TestTombstonedTreeAttrLeavesLive(t *testing.T) {
	doc, before := styleThenUndoTree(t)

	require.Equal(t, 1, doc.GarbageLen())
	require.Equal(t, before, sizeOf(doc))
}

// Once the tombstone is purged it is in neither the RHT nor GC. Live must not
// still be holding it, and the only way to see that is to compare against a
// document rebuilt from the same content -- Live is a running accumulator, so
// it cannot detect its own drift.
func TestPurgedTextAttrIsNotStrandedInLive(t *testing.T) {
	doc, before := styleThenUndoText(t)

	doc.GarbageCollect(doc.VersionVector())
	require.Equal(t, 0, doc.GarbageLen())

	clone, err := doc.InternalDocument().DeepCopy()
	require.NoError(t, err)

	require.Equal(t, before, sizeOf(doc), "the purged tombstone never left Live")
	require.Equal(t, clone.DocSize().Live, doc.DocSize().Live,
		"size must be a function of content")
	require.Equal(t, clone.DocSize().GC, doc.DocSize().GC)
}

// Overwriting an attribute is a separate leak from the tombstone accounting
// above, and it hits the tree as well as the text.
//
// RHT.Set replaces the node under a key and drops the old one from the map, so
// the superseded value is genuinely gone -- RHT overrides immutably, there is
// no tombstone and nothing to collect. But Set only RETURNS the old node when
// it was already removed, so a superseded LIVE node is neither registered as
// garbage nor subtracted from Live. Its bytes stay charged forever.
//
// Measured on main, styling the same key three times:
//
//	        Live         rebuilt      gcLen
//	tree  {32,216}     {24,168}         0
//	text  {32,168}     {24,120}         0
//
// One attribute per overwrite, on the most common operation a rich-text editor
// performs. MaxSizeLimit reads Live + GC, so this eats a document's allowance.
func TestOverwritingATextAttrDoesNotLeakLive(t *testing.T) {
	doc := document.New("d")
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewText("k").Edit(0, 0, "abcdefghij")
		return nil
	}))

	for _, v := range []string{"1", "2", "3"} {
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("k").Style(0, 10, map[string]string{"b": v})
			return nil
		}))
	}

	clone, err := doc.InternalDocument().DeepCopy()
	require.NoError(t, err)
	require.Equal(t, clone.DocSize().Live, doc.DocSize().Live,
		"each overwrite leaves the superseded value charged to Live")
}

func TestOverwritingATreeAttrDoesNotLeakLive(t *testing.T) {
	doc := document.New("d")
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "abcdefghij"}}},
		}})
		return nil
	}))

	for _, v := range []string{"1", "2", "3"} {
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Style(0, 1, map[string]string{"b": v})
			return nil
		}))
	}

	clone, err := doc.InternalDocument().DeepCopy()
	require.NoError(t, err)
	require.Equal(t, clone.DocSize().Live, doc.DocSize().Live,
		"each overwrite leaves the superseded value charged to Live")
}

// Repeating the cycle is what makes this matter: a rich-text editor that sets
// and clears a key leaves one attribute's worth of residue per cycle.
func TestRepeatedTextStyleCyclesDoNotAccumulate(t *testing.T) {
	doc := document.New("d")
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewText("k").Edit(0, 0, "abcdefghij")
		return nil
	}))
	before := sizeOf(doc)

	for range 5 {
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("k").Style(0, 10, map[string]string{"b": "1"})
			return nil
		}))
		require.NoError(t, doc.Undo())
		doc.GarbageCollect(doc.VersionVector())
	}

	require.Equal(t, before, sizeOf(doc), "each cycle leaves one attribute of residue")
}

// A split deep-copies the value's attributes, tombstones included, so the copy
// is new garbage under a new parent with no registration of its own -- the
// original's pair names the original's parent. Without one the copy can never
// be collected, and a document rebuilt from a snapshot disagrees with the live
// one about how much garbage it holds (#2002).
//
// The tree half of this landed in #2005 by keying gcNodePairMap on
// (parent, child) so two tombstones sharing an id stop cancelling each other.
// That keying is what makes the text half expressible: the original and the
// copy share an id and differ only by parent.
func TestSplitCopyOfATextAttrTombstoneIsCollectable(t *testing.T) {
	doc, _ := styleThenUndoText(t)
	require.Equal(t, 1, doc.GarbageLen(), "one tombstone before the split")

	// Split the text node the tombstone rides on.
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("k").Edit(5, 5, "X")
		return nil
	}))

	clone, err := doc.InternalDocument().DeepCopy()
	require.NoError(t, err)

	require.Equal(t, clone.GarbageLen(), doc.GarbageLen(),
		"the copy the split made is not registered, so the rebuild disagrees")
	require.Equal(t, clone.DocSize().GC, doc.DocSize().GC)
	require.Equal(t, clone.DocSize().Live, doc.DocSize().Live)

	// And it must actually be collectable, not merely counted.
	purged := doc.GarbageCollect(doc.VersionVector())
	require.Equal(t, doc.GarbageLen(), 0, "every tombstone was collected")
	require.Positive(t, purged)
}

// A style whose range opens inside one element and runs past its end yields
// that element as an End token with no Start token. Both halves of the
// per-token accounting have to agree about such a visit, or one fires without
// the other and Live drifts by one attribute per operation -- downward, past
// zero, without bound. MaxSizeLimit reads Live + GC, so a negative Live stops
// the document being size-limited at all.
//
// The fix is to book from what the write reported rather than from the map:
// the second visit to a node already styled at its Start loses LWW and
// installs nothing, so it charges nothing, and no token-type guard is needed.
func TestStyleStraddlingAnElementBoundaryKeepsLiveExact(t *testing.T) {
	doc := document.New("d")
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "abcd"}}},
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "efgh"}}},
		}})
		return nil
	}))
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Style(0, 12, map[string]string{"b": "1"})
		return nil
	}))

	for i := range 6 {
		v := fmt.Sprintf("v%d", i)
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			// (1,6) opens inside the first <p> and runs past its end.
			root.GetTree("t").Style(1, 6, map[string]string{"b": v})
			return nil
		}))

		clone, err := doc.InternalDocument().DeepCopy()
		require.NoError(t, err)
		require.Equal(t, clone.DocSize().Live, doc.DocSize().Live,
			"overwrite %d drifted", i+1)
		require.GreaterOrEqual(t, doc.DocSize().Live.Data, 0, "Live went negative")
	}
}

// canStyle admits a tombstoned text node, so a style can land on one. But
// Text.DataSize skips removed nodes, so those attribute bytes are not in Live
// -- they went to GC inside the node's own charge when the node was removed.
// Removing such an attribute must not debit Live a second time, and must not
// charge GC a second time either.
func TestRemovingAnAttrFromATombstonedTextNodeBalances(t *testing.T) {
	doc := document.New("d")
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewText("k").Edit(0, 0, "abcdefghij")
		return nil
	}))
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("k").Style(4, 6, map[string]string{"bbbbbbbbbb": "vvvvvvvvvv"})
		return nil
	}))
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("k").Edit(4, 6, "")
		return nil
	}))
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("k").Style(0, 8, map[string]string{"bbbbbbbbbb": "vvvvvvvvvv"})
		return nil
	}))
	require.NoError(t, doc.Undo())

	clone, err := doc.InternalDocument().DeepCopy()
	require.NoError(t, err)
	require.Equal(t, clone.DocSize().Live, doc.DocSize().Live)
	require.Equal(t, clone.DocSize().GC, doc.DocSize().GC)
	require.GreaterOrEqual(t, doc.DocSize().Live.Data, 0, "Live went negative")

	doc.GarbageCollect(doc.VersionVector())
	require.Equal(t, 0, doc.GarbageLen())
	require.Equal(t, resourceSize{}, resourceSize{
		Data: doc.DocSize().GC.Data, Meta: doc.DocSize().GC.Meta,
	}, "collection left GC residue")
}

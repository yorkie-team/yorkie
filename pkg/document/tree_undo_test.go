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
	"sort"
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

// The reverse of a Tree edit re-inserts a COPY of the nodes the edit removed.
// That copy carries the removed nodes' identities, and the tombstones it was
// copied from are still in the tree -- so every assertion here pairs the
// rendered XML with a duplicate-id check and a garbage collection pass. XML
// alone passes while two nodes share an id, which is what makes a document
// permanently unloadable (docs/design/tree-content-identity.md).

// treeCRDT returns the CRDT tree under key "t" on the document's real root
// (not the clone Document.Root hands out), so identity assertions read the
// state that is actually synced.
func treeCRDT(t *testing.T, doc *document.Document) *crdt.Tree {
	t.Helper()

	tree, ok := doc.RootObject().Get("t").(*crdt.Tree)
	if !ok {
		t.Fatalf(`"t" should be a Tree, got %v`, doc.RootObject().Get("t"))
	}
	return tree
}

// treeXML returns the XML of the tree under key "t".
func treeXML(t *testing.T, doc *document.Document) string {
	t.Helper()
	return treeCRDT(t, doc).ToXML()
}

// assertNoDuplicateTreeIDs fails when any id names more than one node. A
// reverse that re-inserts content under an id a tombstone still holds is
// invisible in the XML but makes every position anchored there ambiguous.
func assertNoDuplicateTreeIDs(t *testing.T, doc *document.Document, msg string) {
	t.Helper()

	ids := treeNodeIDs(t, doc.RootObject())
	assert.Empty(t, duplicatedTreeIDs(ids), "%s: got %v", msg, ids)
}

// liveTreeNodeIDs returns the ids of every node still visible in the tree
// under key "t", sorted. Tombstones are left out: a replica collects them on
// its own schedule, so only the live set is comparable across replicas.
func liveTreeNodeIDs(t *testing.T, doc *document.Document) []string {
	t.Helper()

	var ids []string
	for _, node := range treeCRDT(t, doc).Nodes() {
		if node.RemovedAt() == nil {
			ids = append(ids, node.IDString())
		}
	}
	sort.Strings(ids)

	return ids
}

// editTree applies a single tree edit under key "t" in its own change.
func editTree(t *testing.T, doc *document.Document, from, to int, node *json.TreeNode) {
	t.Helper()

	assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
		r.GetTree("t").Edit(from, to, node, 0)
		return nil
	}))
}

// newTwoParagraphDoc returns a document holding <r><p>ab</p><p>cd</p></r>,
// the shape a merge needs: a range covering </p><p> moves the second
// paragraph's children into the first.
func newTwoParagraphDoc(t *testing.T) *document.Document {
	t.Helper()

	doc := document.New("doc")
	assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
		r.SetNewTree("t", json.TreeNode{Type: "r", Children: []json.TreeNode{
			{Type: "p", Children: []json.TreeNode{{Type: textNodeType, Value: "ab"}}},
			{Type: "p", Children: []json.TreeNode{{Type: textNodeType, Value: "cd"}}},
		}})
		return nil
	}))

	return doc
}

// textNode builds a text content node.
func textNode(value string) *json.TreeNode {
	return &json.TreeNode{Type: textNodeType, Value: value}
}

// collectGarbage runs a full GC pass and returns how many nodes it purged.
func collectGarbage(t *testing.T, doc *document.Document) int {
	t.Helper()
	return doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID()))
}

func TestTreeUndo(t *testing.T) {
	t.Run("insert undo redo survives gc test", func(t *testing.T) {
		// The reverse of an insert is a deletion of what it inserted, so undo
		// leaves a tombstone GC may collect. The redo then has to re-insert
		// the content from the copy the undo's own reverse captured.
		doc := newTreeDoc(t, "000000000000000000000001")
		editTree(t, doc, 3, 3, textNode("c"))
		assert.Equal(t, "<r><p>abc</p></r>", treeXML(t, doc))
		assert.True(t, doc.CanUndo())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, "<r><p>ab</p></r>", treeXML(t, doc))
		assert.Equal(t, 1, doc.GarbageLen(), `the inserted "c" is tombstoned`)
		assert.Equal(t, 1, collectGarbage(t, doc))
		assert.Equal(t, "<r><p>ab</p></r>", treeXML(t, doc))
		assert.Equal(t, 0, doc.GarbageLen())
		assertNoDuplicateTreeIDs(t, doc, "after undo and gc")

		assert.True(t, doc.CanRedo())
		assert.NoError(t, doc.Redo())
		assert.Equal(t, "<r><p>abc</p></r>", treeXML(t, doc))
		assert.Equal(t, 0, doc.GarbageLen())
		assertNoDuplicateTreeIDs(t, doc, "after redo")
	})

	t.Run("delete undo revives by identity and survives gc test", func(t *testing.T) {
		// The reverse of a delete revives the removed nodes under their
		// ORIGINAL identity rather than re-inserting copies. Two things must
		// hold afterwards: the tree gains no new node ids at all, and the
		// tombstone is no longer pending collection -- otherwise a GC pass
		// purges live content.
		doc := newTreeDoc(t, "000000000000000000000001")
		before := treeNodeIDs(t, doc.RootObject())

		editTree(t, doc, 1, 3, nil)
		assert.Equal(t, "<r><p></p></r>", treeXML(t, doc))
		assert.Equal(t, 1, doc.GarbageLen())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, "<r><p>ab</p></r>", treeXML(t, doc))
		assertNoDuplicateTreeIDs(t, doc, "after undo of a delete")
		assert.Equal(t, before, treeNodeIDs(t, doc.RootObject()),
			"the revived run keeps the id it was inserted under; a copy would add one")
		assert.Equal(t, 0, doc.GarbageLen(), "the revived node is no longer pending collection")
		assert.Equal(t, 0, collectGarbage(t, doc))
		assert.Equal(t, "<r><p>ab</p></r>", treeXML(t, doc))

		assert.True(t, doc.CanRedo())
		assert.NoError(t, doc.Redo())
		assert.Equal(t, "<r><p></p></r>", treeXML(t, doc))
		assert.Equal(t, 1, doc.GarbageLen())
		assert.Equal(t, before, treeNodeIDs(t, doc.RootObject()))
		assertNoDuplicateTreeIDs(t, doc, "after redo")

		// The redo re-removed by identity, so the pass collects exactly the
		// node the original delete did -- no copy accumulated across the cycle.
		assert.Equal(t, 1, collectGarbage(t, doc))
		assert.Equal(t, "<r><p></p></r>", treeXML(t, doc))
		assert.Equal(t, 0, doc.GarbageLen())
	})

	t.Run("delete element undo redo survives gc test", func(t *testing.T) {
		// Deleting an element tombstones its whole subtree, so the reverse
		// carries one span per node -- parent before child, which is the order
		// a restore needs to re-anchor a purged subtree top-down.
		doc := newTreeDoc(t, "000000000000000000000001")
		before := treeNodeIDs(t, doc.RootObject())

		editTree(t, doc, 0, 4, nil)
		assert.Equal(t, "<r></r>", treeXML(t, doc))
		assert.Equal(t, 2, doc.GarbageLen(), "the <p> and its text")

		assert.NoError(t, doc.Undo())
		assert.Equal(t, "<r><p>ab</p></r>", treeXML(t, doc))
		assertNoDuplicateTreeIDs(t, doc, "after undo of an element delete")
		assert.Equal(t, before, treeNodeIDs(t, doc.RootObject()),
			"the whole subtree comes back under the ids it had")
		assert.Equal(t, 0, doc.GarbageLen())
		assert.Equal(t, 0, collectGarbage(t, doc))
		assert.Equal(t, "<r><p>ab</p></r>", treeXML(t, doc))

		assert.NoError(t, doc.Redo())
		assert.Equal(t, "<r></r>", treeXML(t, doc))
		assert.Equal(t, 2, doc.GarbageLen())
		assertNoDuplicateTreeIDs(t, doc, "after redo")

		// Purging the subtree and undoing again is the harder direction: the
		// nodes are physically gone, so the restore has to recreate them from
		// the spans and re-anchor them under their original identities.
		assert.Equal(t, 2, collectGarbage(t, doc))
		assert.Equal(t, 0, doc.GarbageLen())
		assert.NoError(t, doc.Undo())
		assert.Equal(t, "<r><p>ab</p></r>", treeXML(t, doc))
		assert.Equal(t, before, treeNodeIDs(t, doc.RootObject()),
			"a recreated subtree keeps the identities the spans carried")
		assert.Equal(t, 0, doc.GarbageLen())
		assertNoDuplicateTreeIDs(t, doc, "after undoing a purged delete")
	})

	t.Run("replace undo redo survives gc test", func(t *testing.T) {
		// A replace both removes and inserts, so its reverse has to delete the
		// inserted range AND re-insert the removed content in one edit.
		doc := newTreeDoc(t, "000000000000000000000001")
		editTree(t, doc, 1, 3, textNode("xy"))
		assert.Equal(t, "<r><p>xy</p></r>", treeXML(t, doc))
		assert.Equal(t, 1, doc.GarbageLen())

		inserted := treeNodeIDs(t, doc.RootObject())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, "<r><p>ab</p></r>", treeXML(t, doc))
		assertNoDuplicateTreeIDs(t, doc, "after undo of a replace")
		assert.Equal(t, inserted, treeNodeIDs(t, doc.RootObject()),
			`nothing is minted: "ab" is revived and "xy" re-removed, both by identity`)
		assert.Equal(t, 1, doc.GarbageLen(), `only the "xy" the undo removed is pending`)
		assert.Equal(t, 1, collectGarbage(t, doc))
		assert.Equal(t, "<r><p>ab</p></r>", treeXML(t, doc))
		assert.Equal(t, 0, doc.GarbageLen())
		assertNoDuplicateTreeIDs(t, doc, "after gc")

		// The redo has to work against a chain the GC pass changed underneath
		// it: recreate the purged "xy" and re-remove "ab", both under the
		// identities they were born with.
		assert.NoError(t, doc.Redo())
		assert.Equal(t, "<r><p>xy</p></r>", treeXML(t, doc))
		assertNoDuplicateTreeIDs(t, doc, "after redo")
		assert.Equal(t, inserted, treeNodeIDs(t, doc.RootObject()),
			"the recreated insertion keeps its original identity")
		assert.Equal(t, 1, doc.GarbageLen())
		assert.Equal(t, 1, collectGarbage(t, doc))
		assert.Equal(t, "<r><p>xy</p></r>", treeXML(t, doc))
		assert.Equal(t, 0, doc.GarbageLen())
	})

	t.Run("replacing a whole element cycles without accumulating content test", func(t *testing.T) {
		// The replacement is inserted at the parent's leftmost position while
		// the replaced element stays as a tombstone beside it. Reversing that
		// by identity means the pending-collection count is the SAME on every
		// cycle: nothing new is minted. A copy-reinserting reverse produces
		// the same XML while that count climbs every cycle, which is why the
		// number is asserted rather than described.
		doc := newTreeDoc(t, "000000000000000000000001")
		editTree(t, doc, 0, 4, &json.TreeNode{
			Type:     "p",
			Children: []json.TreeNode{{Type: textNodeType, Value: "Z"}},
		})
		assert.Equal(t, "<r><p>Z</p></r>", treeXML(t, doc))

		for cycle := range 2 {
			assert.NoError(t, doc.Undo())
			assert.Equal(t, "<r><p>ab</p></r>", treeXML(t, doc), "cycle %d undo", cycle)
			assertNoDuplicateTreeIDs(t, doc, "after undo")
			assert.Equal(t, 2, doc.GarbageLen(), "cycle %d: pending stays constant", cycle)

			assert.NoError(t, doc.Redo())
			assert.Equal(t, "<r><p>Z</p></r>", treeXML(t, doc), "cycle %d redo", cycle)
			assertNoDuplicateTreeIDs(t, doc, "after redo")
			assert.Equal(t, 2, doc.GarbageLen(), "cycle %d: pending stays constant", cycle)
		}
	})

	t.Run("undo does not resurrect an earlier delete test", func(t *testing.T) {
		// Typing inside a node that is later deleted, with the typing undone
		// first: the block's own undo must not bring the typed text back. The
		// identity path gets this by construction -- it only names nodes the
		// edit itself tombstoned -- and the copy-reinsert fallback gets it
		// from the pre-tombstoned filter (see reverseContents' own tests).
		// Either way, resurrecting the text would accumulate it every cycle,
		// so the pending count is asserted alongside the content.
		doc := newTreeDoc(t, "000000000000000000000001")
		editTree(t, doc, 4, 4, &json.TreeNode{Type: "p"})
		assert.Equal(t, "<r><p>ab</p><p></p></r>", treeXML(t, doc))
		editTree(t, doc, 5, 5, textNode("x"))
		assert.Equal(t, "<r><p>ab</p><p>x</p></r>", treeXML(t, doc))

		assert.NoError(t, doc.Undo())
		assert.Equal(t, "<r><p>ab</p><p></p></r>", treeXML(t, doc), "undo the typing")
		assert.NoError(t, doc.Undo())
		assert.Equal(t, "<r><p>ab</p></r>", treeXML(t, doc), "undo the block insert")

		for cycle := range 2 {
			assert.NoError(t, doc.Redo())
			assert.Equal(t, "<r><p>ab</p><p></p></r>", treeXML(t, doc),
				"cycle %d: the redone block must not carry the typed text back", cycle)
			assertNoDuplicateTreeIDs(t, doc, "after redo")
			assert.Equal(t, 1, doc.GarbageLen(), "cycle %d: only the typed text stays pending", cycle)

			assert.NoError(t, doc.Undo())
			assert.Equal(t, "<r><p>ab</p></r>", treeXML(t, doc), "cycle %d: undo again", cycle)
			assertNoDuplicateTreeIDs(t, doc, "after undo")
			assert.Equal(t, 2, doc.GarbageLen(), "cycle %d: the typed text and the block", cycle)
		}
	})

	t.Run("undoing a split-range delete mints no new node test", func(t *testing.T) {
		// Deleting the middle of a run splits it, so the removed node sits in
		// a split chain. Reviving it by identity puts it back into that chain
		// in place; a copy would be spliced into a chain it never belonged to,
		// and purging the chain would unlink the real tombstone from it. The
		// id set is the direct evidence of which happened.
		doc := document.New("doc")
		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetNewTree("t", json.TreeNode{
				Type: "r",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: textNodeType, Value: "abcdef"}},
				}},
			})
			return nil
		}))

		// Deleting the middle of the run splits it, so the removed node has
		// both an InsPrevID and an InsNextID.
		editTree(t, doc, 3, 6, nil)
		assert.Equal(t, "<r><p>abf</p></r>", treeXML(t, doc))
		before := treeNodeIDs(t, doc.RootObject())
		assert.Equal(t, 1, doc.GarbageLen())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, "<r><p>abcdef</p></r>", treeXML(t, doc))
		assertNoDuplicateTreeIDs(t, doc, "after undo")
		assert.Equal(t, before, treeNodeIDs(t, doc.RootObject()),
			"the revived piece rejoins its own split chain rather than arriving as a copy")
		assert.Equal(t, 0, doc.GarbageLen())
		assert.Equal(t, 0, collectGarbage(t, doc))
		assert.Equal(t, "<r><p>abcdef</p></r>", treeXML(t, doc))
	})

	t.Run("a merging edit produces no reverse yet test", func(t *testing.T) {
		// Backspace at the start of the second paragraph: the range covers
		// </p><p>, so its children move into the first paragraph and the
		// emptied element is tombstoned. Re-inserting that tombstone would put
		// back an empty shell — its children live in the first paragraph now —
		// which is why a merge's reverse is a split, not a content
		// re-insertion. That split reverse does not exist yet, so a merging
		// edit produces no reverse at all rather than a wrong one.
		doc := newTwoParagraphDoc(t)
		editTree(t, doc, 3, 5, nil)
		assert.Equal(t, "<r><p>abcd</p></r>", treeXML(t, doc))
		assert.Equal(t, 1, doc.UndoStackLenForTest(),
			"only the SetNewTree change is undoable; a merge has no reverse yet")
	})

	t.Run("a splitting edit produces no reverse yet test", func(t *testing.T) {
		// A split's reverse is a boundary deletion rather than a content
		// re-insertion, and is not built yet. Pinned here so the gap is
		// explicit: the split stays on the document and the undo stack still
		// holds only the change that created the tree.
		doc := newTreeDoc(t, "000000000000000000000001")
		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.GetTree("t").Edit(2, 2, nil, 1)
			return nil
		}))
		assert.Equal(t, "<r><p>a</p><p>b</p></r>", treeXML(t, doc))
		assert.Equal(t, 1, doc.UndoStackLenForTest(), "only the SetNewTree change is undoable")
	})

	t.Run("an edit that changes nothing is undoable as a no-op test", func(t *testing.T) {
		// An edit that neither removes nor inserts still produces a reverse,
		// matching the JS SDK. That reverse has to be a zero-width range: it
		// has nothing to delete, and a wide one would take out whatever is
		// live where this edit ran.
		doc := newTreeDoc(t, "000000000000000000000001")
		editTree(t, doc, 2, 2, nil)
		assert.Equal(t, "<r><p>ab</p></r>", treeXML(t, doc))

		assert.True(t, doc.CanUndo())
		assert.NoError(t, doc.Undo())
		assert.Equal(t, "<r><p>ab</p></r>", treeXML(t, doc))
		assert.Equal(t, 0, doc.GarbageLen())

		assert.NoError(t, doc.Redo())
		assert.Equal(t, "<r><p>ab</p></r>", treeXML(t, doc))
		assert.Equal(t, 0, doc.GarbageLen())
	})

	t.Run("the undo change applies on a replica test", func(t *testing.T) {
		// The undo is pushed to peers as an ordinary change, so its positions
		// and the ids it mints have to mean the same thing there. A replica
		// that ends up with different node ids has diverged even while the
		// rendered XML still matches.
		doc := newTreeDoc(t, "000000000000000000000001")
		editTree(t, doc, 1, 3, nil)
		assert.NoError(t, doc.Undo())
		assert.Equal(t, "<r><p>ab</p></r>", treeXML(t, doc))

		pack := doc.CreateChangePack()
		replica := document.New("doc")
		actorB, err := time.ActorIDFromHex("000000000000000000000002")
		assert.NoError(t, err)
		replica.SetActor(actorB)
		pack.VersionVector.Set(replica.ActorID(), replica.VersionVector().VersionOf(replica.ActorID()))
		assert.NoError(t, replica.ApplyChangePack(pack))

		assert.Equal(t, treeXML(t, doc), treeXML(t, replica))
		assert.Equal(t, liveTreeNodeIDs(t, doc), liveTreeNodeIDs(t, replica),
			"the replica must give the re-inserted nodes the ids the undo issued")
		assertNoDuplicateTreeIDs(t, replica, "on the replica")
	})

	t.Run("undo survives a snapshot round trip test", func(t *testing.T) {
		// A CRDT-level identity fix is easy to lose across DeepCopy and
		// snapshot decode, which rebuild state by replaying setters rather
		// than copying it. The re-inserted copy has to come back distinct
		// from the tombstone it was copied from on the far side too.
		doc := newTreeDoc(t, "000000000000000000000001")
		editTree(t, doc, 0, 4, nil)
		assert.NoError(t, doc.Undo())
		assert.Equal(t, "<r><p>ab</p></r>", treeXML(t, doc))

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
		assert.Equal(t, "<r><p>ab</p></r>", treeXML(t, restored))
		assertNoDuplicateTreeIDs(t, restored, "after a snapshot round trip")
		assert.Equal(t, treeNodeIDs(t, doc.RootObject()), treeNodeIDs(t, restored.RootObject()))

		// The undo revived the subtree in place, so nothing is pending
		// collection on either side. A restore that left a stale registration
		// behind would show up as a purge here, taking out live content.
		assert.Equal(t, doc.GarbageLen(), restored.GarbageLen())
		assert.Equal(t, 0, restored.GarbageLen())
		assert.Equal(t, 0, restored.GarbageCollect(helper.MaxVersionVector(doc.ActorID())))
		assert.Equal(t, "<r><p>ab</p></r>", treeXML(t, restored))
	})
}

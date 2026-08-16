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

	t.Run("delete undo re-inserts without reusing the tombstone id test", func(t *testing.T) {
		// The re-inserted copy must not carry the id its tombstone still
		// holds. Two nodes under one id make a position anchored there
		// resolve differently on different replicas, and the tree refuses
		// content whose id it already holds -- so a copy that kept its id is
		// dropped outright and the undo restores nothing.
		doc := newTreeDoc(t, "000000000000000000000001")
		editTree(t, doc, 1, 3, nil)
		assert.Equal(t, "<r><p></p></r>", treeXML(t, doc))
		assert.Equal(t, 1, doc.GarbageLen())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, "<r><p>ab</p></r>", treeXML(t, doc))
		assertNoDuplicateTreeIDs(t, doc, "after undo of a delete")
		assert.Equal(t, 1, doc.GarbageLen(), "the original run is still tombstoned")

		// The tombstone the copy came from is collected; the live copy must
		// survive that pass untouched.
		assert.Equal(t, 1, collectGarbage(t, doc))
		assert.Equal(t, "<r><p>ab</p></r>", treeXML(t, doc))
		assert.Equal(t, 0, doc.GarbageLen())
		assertNoDuplicateTreeIDs(t, doc, "after gc")

		assert.True(t, doc.CanRedo())
		assert.NoError(t, doc.Redo())
		assert.Equal(t, "<r><p></p></r>", treeXML(t, doc))
		assert.Equal(t, 1, doc.GarbageLen())
		assertNoDuplicateTreeIDs(t, doc, "after redo")
	})

	t.Run("delete element undo redo survives gc test", func(t *testing.T) {
		// Deleting an element tombstones its whole subtree, so the reverse
		// carries one top-level node whose descendants come with it.
		doc := newTreeDoc(t, "000000000000000000000001")
		editTree(t, doc, 0, 4, nil)
		assert.Equal(t, "<r></r>", treeXML(t, doc))
		assert.Equal(t, 2, doc.GarbageLen(), "the <p> and its text")

		assert.NoError(t, doc.Undo())
		assert.Equal(t, "<r><p>ab</p></r>", treeXML(t, doc))
		assertNoDuplicateTreeIDs(t, doc, "after undo of an element delete")
		assert.Equal(t, 2, doc.GarbageLen())
		assert.Equal(t, 2, collectGarbage(t, doc))
		assert.Equal(t, "<r><p>ab</p></r>", treeXML(t, doc))
		assert.Equal(t, 0, doc.GarbageLen())
		assertNoDuplicateTreeIDs(t, doc, "after gc")

		assert.NoError(t, doc.Redo())
		assert.Equal(t, "<r></r>", treeXML(t, doc))
		assert.Equal(t, 2, doc.GarbageLen())
		assertNoDuplicateTreeIDs(t, doc, "after redo")
	})

	t.Run("replace undo redo survives gc test", func(t *testing.T) {
		// A replace both removes and inserts, so its reverse has to delete the
		// inserted range AND re-insert the removed content in one edit.
		doc := newTreeDoc(t, "000000000000000000000001")
		editTree(t, doc, 1, 3, textNode("xy"))
		assert.Equal(t, "<r><p>xy</p></r>", treeXML(t, doc))
		assert.Equal(t, 1, doc.GarbageLen())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, "<r><p>ab</p></r>", treeXML(t, doc))
		assertNoDuplicateTreeIDs(t, doc, "after undo of a replace")
		assert.Equal(t, 2, doc.GarbageLen(), `the original "ab" and the "xy" the undo removed`)
		assert.Equal(t, 2, collectGarbage(t, doc))
		assert.Equal(t, "<r><p>ab</p></r>", treeXML(t, doc))
		assertNoDuplicateTreeIDs(t, doc, "after gc")

		assert.NoError(t, doc.Redo())
		assert.Equal(t, "<r><p>xy</p></r>", treeXML(t, doc))
		assertNoDuplicateTreeIDs(t, doc, "after redo")
		assert.Equal(t, 1, doc.GarbageLen())
	})

	t.Run("undo does not resurrect an earlier delete test", func(t *testing.T) {
		// Typing inside a node that is later deleted, with the typing undone
		// first: the block's own undo must not bring the typed text back. Its
		// reverse copies the removed subtree, and the text is already a
		// tombstone in there -- copying it too would resurrect a delete the
		// user made independently, and accumulate it across every cycle.
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

			assert.NoError(t, doc.Undo())
			assert.Equal(t, "<r><p>ab</p></r>", treeXML(t, doc), "cycle %d: undo again", cycle)
			assertNoDuplicateTreeIDs(t, doc, "after undo")
		}
	})

	t.Run("re-inserted copy drops the split chain and merge lineage test", func(t *testing.T) {
		// The copy came from a node the deletion removed, which carries that
		// node's split chain. Left in place, the copy is spliced into a chain
		// it never belonged to, and purging that chain unlinks the real
		// tombstone from it.
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
		// both an InsPrevID and an InsNextID to carry into the copy.
		editTree(t, doc, 3, 6, nil)
		assert.Equal(t, "<r><p>abf</p></r>", treeXML(t, doc))

		before := map[string]struct{}{}
		for _, node := range treeCRDT(t, doc).Nodes() {
			before[node.IDString()] = struct{}{}
		}

		assert.NoError(t, doc.Undo())
		assert.Equal(t, "<r><p>abcdef</p></r>", treeXML(t, doc))
		assertNoDuplicateTreeIDs(t, doc, "after undo")

		var inserted []string
		for _, node := range treeCRDT(t, doc).Nodes() {
			if _, ok := before[node.IDString()]; ok {
				continue
			}
			inserted = append(inserted, node.IDString())
			assert.Nil(t, node.InsPrevID, "%s kept a split chain", node.IDString())
			assert.Nil(t, node.InsNextID, "%s kept a split chain", node.IDString())
			assert.Nil(t, node.MergedFrom, "%s kept a merge lineage", node.IDString())
			assert.Nil(t, node.MergedAt, "%s kept a merge lineage", node.IDString())
		}
		assert.NotEmpty(t, inserted, "the undo re-inserted the removed content")
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

		// The tombstones the copy was made from were all written by doc's
		// actor, so collecting them takes doc's clock, not the restored
		// document's own.
		assert.Equal(t, doc.GarbageLen(), restored.GarbageLen())
		assert.Equal(t, 2, restored.GarbageCollect(helper.MaxVersionVector(doc.ActorID())))
		assert.Equal(t, "<r><p>ab</p></r>", treeXML(t, restored))
		assert.Equal(t, 0, restored.GarbageLen())
	})
}

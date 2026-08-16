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
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

// A splitLevel >= 1 edit creates element boundaries without removing anything,
// so its reverse is a boundary deletion -- a splitLevel 0 edit over the
// 2*splitLevel tokens the split introduced -- and that boundary deletion's own
// reverse is a re-split rather than a re-insertion of the tombstoned boundary
// nodes. A merge is the mirror image: it deletes boundaries, so its reverse is
// a split. See docs/design/tree-split-undo-redo.md.
//
// Every re-split mints brand new element nodes, and the boundary deletion
// tombstones the ones the previous split minted, so each assertion here pairs
// the rendered XML with a duplicate-id check and a garbage collection pass:
// XML alone passes while two nodes share an id, which is what makes a document
// permanently unloadable (docs/design/tree-content-identity.md).

// splitTree applies a single splitting tree edit under key "t" in its own
// change: a zero-width edit at idx carrying no content, at the given level.
func splitTree(t *testing.T, doc *document.Document, idx, splitLevel int) {
	t.Helper()

	assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
		r.GetTree("t").Edit(idx, idx, nil, splitLevel)
		return nil
	}))
}

// newSplitDoc returns a document holding <r><p>abcd</p></r> under key "t", the
// shape an L1 split needs at three positions. Each number below is the index
// immediately AFTER the token above it, so 1 is the front of the paragraph's
// content, 3 its middle and 5 its back:
//
//	<p>  a  b  c  d  </p>
//	 1   2  3  4  5   6
func newSplitDoc(t *testing.T, hexActor string) *document.Document {
	t.Helper()

	doc := document.New("doc")
	actor, err := time.ActorIDFromHex(hexActor)
	assert.NoError(t, err)
	doc.SetActor(actor)

	assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
		r.SetNewTree("t", json.TreeNode{
			Type: "r",
			Children: []json.TreeNode{{
				Type:     "p",
				Children: []json.TreeNode{{Type: textNodeType, Value: "abcd"}},
			}},
		})
		return nil
	}))

	return doc
}

// newNestedSplitDoc returns a document holding <r><d><p>abcd</p></d></r>, the
// shape an L2 split needs. Same numbering as newSplitDoc, one level deeper, so
// the middle of the paragraph's content is 4:
//
//	<d>  <p>  a  b  c  d  </p>  </d>
//	 1    2   3  4  5  6   7     8
func newNestedSplitDoc(t *testing.T, hexActor string) *document.Document {
	t.Helper()

	doc := document.New("doc")
	actor, err := time.ActorIDFromHex(hexActor)
	assert.NoError(t, err)
	doc.SetActor(actor)

	assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
		r.SetNewTree("t", json.TreeNode{
			Type: "r",
			Children: []json.TreeNode{{
				Type: "d",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: textNodeType, Value: "abcd"}},
				}},
			}},
		})
		return nil
	}))

	return doc
}

// newThreeBlockDoc returns a document holding
// <r><d><p>ab</p></d><d><p>cd</p></d><d><p>ef</p></d></r>, the shape two
// successive L2 merges need — and so the shape that puts two splitLevel 2
// reverses into a single undo entry.
func newThreeBlockDoc(t *testing.T) *document.Document {
	t.Helper()

	doc := document.New("doc")
	var blocks []json.TreeNode
	for _, value := range []string{"ab", "cd", "ef"} {
		blocks = append(blocks, json.TreeNode{
			Type: "d",
			Children: []json.TreeNode{{
				Type:     "p",
				Children: []json.TreeNode{{Type: textNodeType, Value: value}},
			}},
		})
	}

	assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
		r.SetNewTree("t", json.TreeNode{Type: "r", Children: blocks})
		return nil
	}))

	return doc
}

// TestTreeEditRejectsNegativeSplitLevel covers the producer side of the same
// invariant api/converter enforces on decode. A negative split level is inert
// going forward -- the split loop does nothing for a non-positive level -- but
// it sizes the boundary-deletion reverse as 2*splitLevel, which would run
// backwards. The decoder now refuses one arriving from a peer, and that
// decoder is shared with the stored-change read path, so it is worth never
// minting one locally either.
func TestTreeEditRejectsNegativeSplitLevel(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(tree *json.Tree)
	}{
		{"Edit", func(tr *json.Tree) { tr.Edit(1, 1, nil, -1) }},
		{"EditBulk", func(tr *json.Tree) { tr.EditBulk(1, 1, nil, -1) }},
		{"EditByPath", func(tr *json.Tree) { tr.EditByPath([]int{0, 0}, []int{0, 0}, nil, -1) }},
		{"EditBulkByPath", func(tr *json.Tree) { tr.EditBulkByPath([]int{0, 0}, []int{0, 0}, nil, -1) }},
	} {
		t.Run(tc.name+" refuses a negative split level test", func(t *testing.T) {
			doc := newSplitDoc(t, "000000000000000000000001")

			err := doc.Update(func(r *json.Object, p *presence.Presence) error {
				assert.PanicsWithError(t, json.ErrInvalidSplitLevel.Error(), func() {
					tc.edit(r.GetTree("t"))
				})
				return nil
			})
			assert.NoError(t, err)

			// The refusal left the tree untouched.
			assert.Equal(t, "<r><p>abcd</p></r>", treeXML(t, doc))
		})
	}

	t.Run("a zero split level is still accepted test", func(t *testing.T) {
		doc := newSplitDoc(t, "000000000000000000000001")
		editTree(t, doc, 3, 3, textNode("X"))
		assert.Equal(t, "<r><p>abXcd</p></r>", treeXML(t, doc))
	})
}

func TestTreeSplitUndo(t *testing.T) {
	t.Run("a multi op undo entry gives each split reverse its own tickets test", func(t *testing.T) {
		// executeUndoRedo issues ONE ticket per operation, but a splitLevel N
		// edit consumes N more for the elements it mints. Left to reconstruct
		// those from its own executedAt, a level 2 split reverse walks two
		// delimiters past the one it was issued -- straight over the ticket the
		// NEXT operation in the same undo entry was issued, and over the
		// delimiter that one goes on to simulate from. Both replicas and the
		// server replay the resulting change, so the two elements that land
		// under one TreeNodeID are live everywhere, which is what makes a
		// document permanently unloadable.
		//
		// Two L2 merges in ONE change is the smallest shape that reaches it:
		// one undo entry, two splitLevel 2 reverses. A single-op undo entry
		// cannot collide with anything, which is why every other test here
		// misses this.
		doc := newThreeBlockDoc(t)
		assert.Equal(t,
			"<r><d><p>ab</p></d><d><p>cd</p></d><d><p>ef</p></d></r>",
			treeXML(t, doc))

		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.GetTree("t").Edit(4, 8, nil, 0)
			r.GetTree("t").Edit(6, 10, nil, 0)
			return nil
		}))
		assert.Equal(t, "<r><d><p>abcdef</p></d></r>", treeXML(t, doc))

		assert.NoError(t, doc.Undo())
		assert.Equal(t,
			"<r><d><p>ab</p></d><d><p>cd</p></d><d><p>ef</p></d></r>",
			treeXML(t, doc))

		ids := treeNodeIDs(t, doc.RootObject())
		assert.Empty(t, duplicatedTreeIDs(ids),
			"each element a split reverse mints needs its own id, got %v", ids)
	})

	t.Run("l1 split undo redo undo cycle test", func(t *testing.T) {
		// The full cycle the design doc names: split -> undo (boundary
		// deletion) -> redo (re-split) -> undo (boundary deletion again).
		// Each step has to land back on the exact XML the step before it
		// started from, which is what proves the redo re-splits rather than
		// re-inserting the boundary nodes the undo tombstoned.
		doc := newSplitDoc(t, "000000000000000000000001")
		splitTree(t, doc, 3, 1)
		assert.Equal(t, "<r><p>ab</p><p>cd</p></r>", treeXML(t, doc))
		assertNoDuplicateTreeIDs(t, doc, "after the split")
		assert.True(t, doc.CanUndo(), "a splitting edit has to be undoable")

		assert.NoError(t, doc.Undo())
		assert.Equal(t, "<r><p>abcd</p></r>", treeXML(t, doc))
		assertNoDuplicateTreeIDs(t, doc, "after the split undo")

		assert.True(t, doc.CanRedo(), "the boundary deletion has to push a redo")
		assert.NoError(t, doc.Redo())
		assert.Equal(t, "<r><p>ab</p><p>cd</p></r>", treeXML(t, doc))
		assertNoDuplicateTreeIDs(t, doc, "after the re-split redo")

		assert.True(t, doc.CanUndo())
		assert.NoError(t, doc.Undo())
		assert.Equal(t, "<r><p>abcd</p></r>", treeXML(t, doc))
		assertNoDuplicateTreeIDs(t, doc, "after the second split undo")
	})

	t.Run("l1 split undo survives gc test", func(t *testing.T) {
		// The boundary deletion tombstones the element the split minted. A GC
		// pass right after the undo purges it, and the redo has to still work
		// -- it mints a fresh element rather than reviving the purged one, so
		// nothing anchors at an id GC just took away.
		doc := newSplitDoc(t, "000000000000000000000001")
		splitTree(t, doc, 3, 1)
		assert.Equal(t, 0, doc.GarbageLen(), "a split removes nothing")

		assert.NoError(t, doc.Undo())
		assert.Equal(t, "<r><p>abcd</p></r>", treeXML(t, doc))
		assert.Equal(t, 1, doc.GarbageLen(), "the boundary element the split minted")
		assert.Equal(t, 1, collectGarbage(t, doc))
		assert.Equal(t, 0, doc.GarbageLen())
		assert.Equal(t, "<r><p>abcd</p></r>", treeXML(t, doc))
		assertNoDuplicateTreeIDs(t, doc, "after undo and gc")

		assert.NoError(t, doc.Redo())
		assert.Equal(t, "<r><p>ab</p><p>cd</p></r>", treeXML(t, doc))
		assert.Equal(t, 0, doc.GarbageLen())
		assertNoDuplicateTreeIDs(t, doc, "after redo past a gc pass")
	})

	t.Run("l1 front split undo test", func(t *testing.T) {
		// Splitting at the very front of the element's content leaves an empty
		// element behind; the boundary deletion has to merge it back.
		doc := newSplitDoc(t, "000000000000000000000001")
		splitTree(t, doc, 1, 1)
		assert.Equal(t, "<r><p></p><p>abcd</p></r>", treeXML(t, doc))

		assert.NoError(t, doc.Undo())
		assert.Equal(t, "<r><p>abcd</p></r>", treeXML(t, doc))
		assert.Equal(t, 1, collectGarbage(t, doc))
		assertNoDuplicateTreeIDs(t, doc, "after a front split undo")

		assert.NoError(t, doc.Redo())
		assert.Equal(t, "<r><p></p><p>abcd</p></r>", treeXML(t, doc))
		assertNoDuplicateTreeIDs(t, doc, "after a front split redo")
	})

	t.Run("l1 back split undo test", func(t *testing.T) {
		// The trailing-empty-element mirror of the front split.
		doc := newSplitDoc(t, "000000000000000000000001")
		splitTree(t, doc, 5, 1)
		assert.Equal(t, "<r><p>abcd</p><p></p></r>", treeXML(t, doc))

		assert.NoError(t, doc.Undo())
		assert.Equal(t, "<r><p>abcd</p></r>", treeXML(t, doc))
		assert.Equal(t, 1, collectGarbage(t, doc))
		assertNoDuplicateTreeIDs(t, doc, "after a back split undo")

		assert.NoError(t, doc.Redo())
		assert.Equal(t, "<r><p>abcd</p><p></p></r>", treeXML(t, doc))
		assertNoDuplicateTreeIDs(t, doc, "after a back split redo")
	})

	t.Run("l2 split undo redo undo cycle test", func(t *testing.T) {
		// Two levels means four boundary tokens, so the reverse deletes twice
		// the range an L1 undo does. Everything else is the same path.
		doc := newNestedSplitDoc(t, "000000000000000000000001")
		splitTree(t, doc, 4, 2)
		assert.Equal(t, "<r><d><p>ab</p></d><d><p>cd</p></d></r>", treeXML(t, doc))
		assertNoDuplicateTreeIDs(t, doc, "after the l2 split")

		assert.NoError(t, doc.Undo())
		assert.Equal(t, "<r><d><p>abcd</p></d></r>", treeXML(t, doc))
		assert.Equal(t, 2, doc.GarbageLen(), "the <d> and <p> the split minted")
		assertNoDuplicateTreeIDs(t, doc, "after the l2 split undo")
		assert.Equal(t, 2, collectGarbage(t, doc))
		assert.Equal(t, "<r><d><p>abcd</p></d></r>", treeXML(t, doc))

		assert.NoError(t, doc.Redo())
		assert.Equal(t, "<r><d><p>ab</p></d><d><p>cd</p></d></r>", treeXML(t, doc))
		assertNoDuplicateTreeIDs(t, doc, "after the l2 re-split redo")

		assert.NoError(t, doc.Undo())
		assert.Equal(t, "<r><d><p>abcd</p></d></r>", treeXML(t, doc))
		assertNoDuplicateTreeIDs(t, doc, "after the second l2 split undo")
	})

	t.Run("split undo applies on a replica across the wire test", func(t *testing.T) {
		// The boundary deletion and the re-split are both pushed to peers as
		// ordinary changes, so both have to survive protobuf encode/decode --
		// and this is the only test where a splitLevel > 0 operation crosses
		// it, since redoSplitLevel is local-only state that does NOT and the
		// re-split has to work without it. The pack goes through
		// ToChangePack/FromChangePack rather than being handed over in memory,
		// or the "replica" would just re-execute the originator's own operation
		// pointers and never exercise decode at all.
		doc := newSplitDoc(t, "000000000000000000000001")
		splitTree(t, doc, 3, 1)
		assert.NoError(t, doc.Undo())
		assert.NoError(t, doc.Redo())
		assert.Equal(t, "<r><p>ab</p><p>cd</p></r>", treeXML(t, doc))

		pbPack, err := converter.ToChangePack(doc.CreateChangePack())
		assert.NoError(t, err)
		pack, err := converter.FromChangePack(pbPack)
		assert.NoError(t, err)

		replica := document.New("doc")
		actorB, err := time.ActorIDFromHex("000000000000000000000002")
		assert.NoError(t, err)
		replica.SetActor(actorB)
		pack.VersionVector.Set(replica.ActorID(), replica.VersionVector().VersionOf(replica.ActorID()))
		assert.NoError(t, replica.ApplyChangePack(pack))

		assert.Equal(t, treeXML(t, doc), treeXML(t, replica))
		assert.Equal(t, liveTreeNodeIDs(t, doc), liveTreeNodeIDs(t, replica),
			"the replica must give the re-split element the id the redo issued")
		assertNoDuplicateTreeIDs(t, replica, "on the replica")
	})

	t.Run("a multi op undo entry applies on a replica across the wire test", func(t *testing.T) {
		// The identity collision two split reverses in one entry used to
		// produce was never local: the change carries both operations, so every
		// replica and the server replayed it. Pinned here across a real decode,
		// where the split tickets have to arrive on the wire rather than being
		// reconstructed.
		doc := newThreeBlockDoc(t)
		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.GetTree("t").Edit(4, 8, nil, 0)
			r.GetTree("t").Edit(6, 10, nil, 0)
			return nil
		}))
		assert.NoError(t, doc.Undo())

		pbPack, err := converter.ToChangePack(doc.CreateChangePack())
		assert.NoError(t, err)
		pack, err := converter.FromChangePack(pbPack)
		assert.NoError(t, err)

		replica := document.New("doc")
		actorB, err := time.ActorIDFromHex("000000000000000000000002")
		assert.NoError(t, err)
		replica.SetActor(actorB)
		pack.VersionVector.Set(replica.ActorID(), replica.VersionVector().VersionOf(replica.ActorID()))
		assert.NoError(t, replica.ApplyChangePack(pack))

		assert.Equal(t, treeXML(t, doc), treeXML(t, replica))
		assert.Equal(t, liveTreeNodeIDs(t, doc), liveTreeNodeIDs(t, replica))
		assertNoDuplicateTreeIDs(t, replica, "on the replica")
	})

	t.Run("split undo survives a snapshot round trip test", func(t *testing.T) {
		// DeepCopy and snapshot decode rebuild state by replaying setters, so
		// a boundary the undo merged away has to still be merged away on the
		// far side, with the same node ids and nothing left pending GC.
		doc := newSplitDoc(t, "000000000000000000000001")
		splitTree(t, doc, 3, 1)
		assert.NoError(t, doc.Undo())
		assert.Equal(t, "<r><p>abcd</p></r>", treeXML(t, doc))

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
		assert.Equal(t, "<r><p>abcd</p></r>", treeXML(t, restored))
		assertNoDuplicateTreeIDs(t, restored, "after a snapshot round trip")
		assert.Equal(t, treeNodeIDs(t, doc.RootObject()), treeNodeIDs(t, restored.RootObject()))
		assert.Equal(t, doc.GarbageLen(), restored.GarbageLen())
		assert.Equal(t, 1, restored.GarbageCollect(helper.MaxVersionVector(doc.ActorID())))
		assert.Equal(t, "<r><p>abcd</p></r>", treeXML(t, restored))
	})

	t.Run("a split that also inserts produces no reverse test", func(t *testing.T) {
		// A boundary deletion undoes the split half and nothing else, so an
		// edit that splits AND inserts would need a reverse undoing both at
		// once. Neither builder produces that, so such an edit gets no reverse
		// rather than one that undoes half of it and leaves the content behind.
		doc := newSplitDoc(t, "000000000000000000000001")
		before := doc.UndoStackLenForTest()

		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.GetTree("t").Edit(3, 3, textNode("X"), 1)
			return nil
		}))
		assert.Equal(t, "<r><p>abX</p><p>cd</p></r>", treeXML(t, doc))
		assert.Equal(t, before, doc.UndoStackLenForTest(),
			"a split that also inserts is not undoable")
	})

	t.Run("merge undo redo undo cycle test", func(t *testing.T) {
		// A merge is a split run backwards: it deletes the boundary tokens
		// between two elements and moves the second's children into the first.
		// Re-inserting the emptied element would restore a shell whose children
		// now live elsewhere, so the reverse is a split at the merge point.
		doc := newTwoParagraphDoc(t)
		editTree(t, doc, 3, 5, nil)
		assert.Equal(t, "<r><p>abcd</p></r>", treeXML(t, doc))
		assert.True(t, doc.CanUndo(), "a merging edit has to be undoable")

		assert.NoError(t, doc.Undo())
		assert.Equal(t, "<r><p>ab</p><p>cd</p></r>", treeXML(t, doc))
		assertNoDuplicateTreeIDs(t, doc, "after the merge undo")

		assert.True(t, doc.CanRedo(), "the split undo has to push a redo")
		assert.NoError(t, doc.Redo())
		assert.Equal(t, "<r><p>abcd</p></r>", treeXML(t, doc))
		assertNoDuplicateTreeIDs(t, doc, "after the merge redo")

		assert.NoError(t, doc.Undo())
		assert.Equal(t, "<r><p>ab</p><p>cd</p></r>", treeXML(t, doc))
		assertNoDuplicateTreeIDs(t, doc, "after the second merge undo")
	})

	t.Run("merge undo survives gc test", func(t *testing.T) {
		// The merge tombstoned the second paragraph's element; the undo splits
		// a brand new one back in rather than reviving that tombstone, so a GC
		// pass after the undo is free to purge the original -- and must leave
		// the split-in element alone.
		doc := newTwoParagraphDoc(t)
		editTree(t, doc, 3, 5, nil)
		assert.Equal(t, 1, doc.GarbageLen(), "the merged-away <p>")

		assert.NoError(t, doc.Undo())
		assert.Equal(t, "<r><p>ab</p><p>cd</p></r>", treeXML(t, doc))
		assert.Equal(t, 1, doc.GarbageLen())
		assert.Equal(t, 1, collectGarbage(t, doc))
		assert.Equal(t, 0, doc.GarbageLen())
		assert.Equal(t, "<r><p>ab</p><p>cd</p></r>", treeXML(t, doc),
			"gc must not touch the element the split undo minted")
		assertNoDuplicateTreeIDs(t, doc, "after a merge undo and gc")
	})
}

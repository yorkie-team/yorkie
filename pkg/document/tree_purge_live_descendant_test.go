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
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestTreePurgeWithLiveDescendant checks the size ledger when GC meets a
// removed element that still has a live descendant (yorkie#2244).
//
// d1 deletes the first paragraph whole, then applies d2's delete of its "b"
// and d2's undo of that delete, which revives "b" by identity under the
// paragraph d1 removed: the undo did not know of d1's delete. Purging the
// paragraph as it stands would detach "b" with it, bytes Live holds while a
// rebuild no longer counts them, and Live is what MaxSizePerDocument is
// checked against. Holding the paragraph for "b" would keep it for as long
// as a node no one can see stays live. The collection tombstones "b" and
// takes it out with the paragraph instead, and d2's later redo, naming the
// detached "b", changes nothing on d1.
func TestTreePurgeWithLiveDescendant(t *testing.T) {
	d1, d2, a1, a2 := newReplicas(t)
	collect := func(t *testing.T) int {
		t.Helper()
		vv := d1.VersionVector().DeepCopy()
		for _, actor := range []time.ActorID{a1, a2} {
			if v := d2.VersionVector().VersionOf(actor); v > vv.VersionOf(actor) {
				vv.Set(actor, v)
			}
		}
		return d1.GarbageCollect(vv)
	}

	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{
			Type: "doc",
			Children: []json.TreeNode{
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}}},
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "cd"}}},
			},
		})
		return nil
	}))
	crossSync(t, d1, d2)

	// <doc>0<p>1ab3</p>4<p>5cd7</p>8</doc>
	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Edit(0, 4, nil, 0)
		return nil
	}))
	require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Edit(2, 3, nil, 0)
		return nil
	}))
	require.NoError(t, d2.Undo())
	crossSync(t, d1, d2)

	paragraph := treeCRDT(t, d1).Root().Children(true)[0]
	pieces := paragraph.Children(true)
	require.Len(t, pieces, 2)
	text := pieces[1]
	require.True(t, paragraph.IsRemoved(), "the paragraph stays removed")
	require.False(t, text.IsRemoved(), `"b" is live again under it`)
	assertRebuildsSame(t, d1, "before collection")

	collect(t)
	require.Equal(t, "<doc><p>cd</p></doc>", treeXML(t, d1))
	require.Len(t, treeCRDT(t, d1).Root().Children(true), 1, "the paragraph is purged")
	assert.True(t, text.IsRemoved(), `"b" went out with it, as a tombstone`)
	assertRebuildsSame(t, d1, "after a collection that met the live descendant")
	assert.Equal(t, 0, d1.GarbageLen(), "nothing is held")
	assert.Equal(t, len(treeCRDT(t, d1).Nodes()), treeCRDT(t, d1).NodeLen(),
		"every registered node is reachable from the root")
	rebuilt, err := d1.InternalDocumentForTest().DeepCopy()
	require.NoError(t, err)
	live, rebuiltLive := d1.DocSize().Live, rebuilt.DocSize().Live
	assert.Equal(t, (&rebuiltLive).Total(), (&live).Total(), "the size gate reads what a rebuild reads")

	// d2 redoes its delete and collects too: both end with the same tree and
	// a ledger equal to a rebuild.
	require.NoError(t, d2.Redo())
	crossSync(t, d1, d2)
	collect(t)
	d2.GarbageCollect(d1.VersionVector())
	assertRebuildsSame(t, d1, "after the redo")
	assertRebuildsSame(t, d2, "after the redo")
	assert.Equal(t, treeXML(t, d1), treeXML(t, d2))
}

// TestTreeReviveUnderAKnownRemovalDoesNotDependOnGC pins that a restore
// leaves a node a tombstone under an ancestor whose removal the restoring
// change knew, so the result cannot depend on when each replica collects.
//
// d2 deletes "b"; d1 then deletes the paragraph, and d2 sees that before it
// undoes its delete. d1 collects first, taking the paragraph out; d2 undoes
// and collects at the same vector. Had the undo revived "b" under the
// removed paragraph, "b" would be live on d2 until d2 collected, and an undo
// of the paragraph delete arriving before that would bring the paragraph
// back with "b" on d2 and without it on d1.
func TestTreeReviveUnderAKnownRemovalDoesNotDependOnGC(t *testing.T) {
	d1, d2, a1, a2 := newReplicas(t)
	vector := func() time.VersionVector {
		vv := d1.VersionVector().DeepCopy()
		for _, actor := range []time.ActorID{a1, a2} {
			if v := d2.VersionVector().VersionOf(actor); v > vv.VersionOf(actor) {
				vv.Set(actor, v)
			}
		}
		return vv
	}

	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}}},
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "cd"}}},
		}})
		return nil
	}))
	crossSync(t, d1, d2)
	require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Edit(2, 3, nil, 0)
		return nil
	}))
	crossSync(t, d1, d2)
	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Edit(0, 3, nil, 0)
		return nil
	}))
	crossSync(t, d1, d2)

	d1.GarbageCollect(vector())
	require.NoError(t, d2.Undo())
	d2.GarbageCollect(vector())
	crossSync(t, d1, d2)
	assert.Equal(t, treeXML(t, d1), treeXML(t, d2))
	assert.Equal(t, d1.DocSize().Live, d2.DocSize().Live)
	assertRebuildsSame(t, d1, "after both collect")
	assertRebuildsSame(t, d2, "after both collect")

	require.NoError(t, d1.Undo())
	crossSync(t, d1, d2)
	assert.Equal(t, "<doc><p>a</p><p>cd</p></doc>", treeXML(t, d1))
	assert.Equal(t, treeXML(t, d1), treeXML(t, d2), "the paragraph comes back the same everywhere")
	assertRebuildsSame(t, d1, "after the paragraph's undo")
	assertRebuildsSame(t, d2, "after the paragraph's undo")
}

// TestTreeClocklessReviveUnderARemovalDoesNotDependOnGC is the clock-less
// case of the test above, on three replicas. d2's undo of its delete of "b"
// arrives without a version vector at d1, which purged the paragraph, and at
// d3, which had not. Read as knowing every removal, the restore leaves "b" a
// tombstone on both, so the paragraph's undo brings it back nowhere.
func TestTreeClocklessReviveUnderARemovalDoesNotDependOnGC(t *testing.T) {
	var docs []*document.Document
	for _, hex := range []string{
		"000000000000000000000001", "000000000000000000000002", "000000000000000000000003",
	} {
		actor, err := time.ActorIDFromHex(hex)
		require.NoError(t, err)
		doc := document.New("test-doc")
		doc.SetActor(actor)
		docs = append(docs, doc)
	}

	// send delivers src's pending changes to dsts, without their vectors
	// when clockless, and acks them at src.
	send := func(src int, clockless bool, dsts ...int) {
		pack := docs[src].CreateChangePack()
		changes := pack.Changes
		if clockless {
			changes = nil
			for _, c := range pack.Changes {
				id := c.ID()
				id = change.NewID(id.ClientSeq(), id.ServerSeq(), id.Lamport(), id.ActorID(), time.NewVersionVector())
				changes = append(changes, change.New(id, c.Message(), c.Operations(), c.PresenceChange()))
			}
		}
		for _, dst := range dsts {
			require.NoError(t, docs[dst].ApplyChangePack(change.NewPack(
				pack.DocumentKey, change.NewCheckpoint(0, 0), changes, time.InitialVersionVector, nil)))
		}
		var last uint32
		if len(pack.Changes) > 0 {
			last = pack.Changes[len(pack.Changes)-1].ClientSeq()
		}
		require.NoError(t, docs[src].ApplyChangePack(change.NewPack(
			pack.DocumentKey, change.NewCheckpoint(0, last), nil, time.InitialVersionVector, nil)))
	}
	syncAll := func() {
		send(0, false, 1, 2)
		send(1, false, 0, 2)
		send(2, false, 0, 1)
	}
	collect := func(i int) {
		vv := docs[0].VersionVector().DeepCopy()
		for _, doc := range docs[1:] {
			for actor, v := range doc.VersionVector() {
				if v > vv.VersionOf(actor) {
					vv.Set(actor, v)
				}
			}
		}
		docs[i].GarbageCollect(vv)
	}
	edit := func(i, from, to int) {
		require.NoError(t, docs[i].Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(from, to, nil, 0)
			return nil
		}))
	}
	assertConverged := func(msg string) {
		t.Helper()
		for _, doc := range docs {
			assert.Equal(t, treeXML(t, docs[0]), treeXML(t, doc), msg)
			assertRebuildsSame(t, doc, msg)
		}
	}

	require.NoError(t, docs[0].Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}}},
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "cd"}}},
		}})
		return nil
	}))
	syncAll()
	edit(1, 2, 3)
	syncAll()
	edit(0, 0, 3)
	syncAll()

	collect(0)
	require.NoError(t, docs[1].Undo())
	send(1, true, 0, 2)
	collect(2)
	syncAll()
	assertConverged("after the clock-less undo of b")

	require.NoError(t, docs[0].Undo())
	syncAll()
	assert.Equal(t, "<doc><p>a</p><p>cd</p></doc>", treeXML(t, docs[0]))
	assertConverged("after the paragraph's undo")
}

// TestTreeCraftedReviveCannotPinARemovedChain pins that no restore can keep
// a removed element in the tree. A crafted restore claims to know the text's
// creation but not the removal of the elements above it, so it revives the
// text under them, where no one can see or delete it. A collection at a
// vector covering that removal still takes the whole chain, the text with
// it, and leaves nothing in GC: what it purges does not depend on the
// restoring change's vector.
func TestTreeCraftedReviveCannotPinARemovedChain(t *testing.T) {
	d1, _, _, _ := newReplicas(t)
	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
			{Type: "div", Children: []json.TreeNode{
				{Type: "div", Children: []json.TreeNode{
					{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}}},
				}},
			}},
		}})
		return nil
	}))
	tree := treeCRDT(t, d1)
	p := tree.Root().Children()[0].Children()[0].Children()[0]
	text := p.Children()[0]
	require.NoError(t, d1.Update(func(root *json.Object, _ *presence.Presence) error {
		root.GetTree("t").Edit(0, 8, nil, 0)
		return nil
	}))
	require.Equal(t, "<doc></doc>", treeXML(t, d1))

	created := text.ID().CreatedAt
	crafted := time.NewVersionVector()
	crafted.Set(created.ActorID(), created.Lamport())
	at := time.NewTicket(d1.VersionVector().VersionOf(created.ActorID())+1, 0, created.ActorID())
	pos := crdt.NewTreePos(p.ID(), p.ID())
	span := &crdt.TreeRestoreSpan{
		ID: text.ID(), NodeType: "text", IsText: true, Length: 2, Value: "ab", ParentID: p.ID(),
	}
	op := operations.NewRestoreTreeEdit(tree.CreatedAt(), pos, pos, at,
		[]*crdt.TreeRestoreSpan{span}, crdt.RestoreModeRestore, nil)
	_, err := op.Execute(d1.InternalDocumentForTest().Root(), operations.OpSourceRemote, crafted)
	require.NoError(t, err)
	require.False(t, text.IsRemoved(), "the crafted restore revives the text under the chain")

	vector := d1.VersionVector().DeepCopy()
	vector.Set(at.ActorID(), at.Lamport())
	assert.Positive(t, d1.GarbageCollect(vector))
	assert.Empty(t, treeCRDT(t, d1).Root().Children(true), "the whole chain is purged")
	assert.True(t, text.IsRemoved())
	assert.Equal(t, 0, d1.GarbageLen(), "nothing is held in GC")
	assertRebuildsSame(t, d1, "after the collection")
}

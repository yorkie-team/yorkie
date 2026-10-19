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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/converter"
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
	assert.Equal(t, 0, d1.GarbageLen(), `"b" goes in the same collection: the vector has seen the paragraph whole`)
	assert.Equal(t, len(treeCRDT(t, d1).Nodes()), treeCRDT(t, d1).NodeLen(),
		"every registered node is reachable from the root")
	rebuilt, err := d1.InternalDocumentForTest().Root().Rederive()
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
	assert.Equal(t, 0, d1.GarbageLen(), "the released text goes in the same collection")
	assertRebuildsSame(t, d1, "after the collection")
}

// TestRestoreAfterDetachKeepsTheLedger pins the GC pair of a detached node a
// restore recreates. d2's delete of "b" carries a later ticket than d1's
// delete of the paragraph, so a collection that covers d1's alone purges
// the paragraph and "a" but leaves "b" detached with its own pair pending.
// d1's undo recreates "ab" under the recreated paragraph, under the same
// IDs; the pending pair of the old "b" must not stay registered under the
// recreated piece's key, or the next delete of it cancels both pairs and
// docSize.GC drifts from a rebuild.
func TestRestoreAfterDetachKeepsTheLedger(t *testing.T) {
	d1, d2, a1, a2 := newReplicas(t)
	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}}},
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "cd"}}},
		}})
		return nil
	}))
	crossSync(t, d1, d2)
	for range 5 {
		require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Style(4, 7, map[string]string{"k": "v"})
			return nil
		}))
	}
	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Edit(0, 4, nil, 0)
		return nil
	}))
	// d2 also inserts "z" after "a" in the same change, which the
	// collection below does not cover: its creation keeps the paragraph's
	// subtree after the purge, as a concurrent split's product would.
	require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Edit(2, 3, nil, 0)
		root.GetTree("t").Edit(2, 2, &json.TreeNode{Type: "text", Value: "z"}, 0)
		return nil
	}))
	crossSync(t, d1, d2)

	pieces := treeCRDT(t, d1).Root().Children(true)[0].Children(true)
	require.Len(t, pieces, 3)
	b := pieces[1]
	require.Equal(t, a2, b.RemovedAt().ActorID(), "d2's later delete wins the tombstone of b")

	// Cover d1's delete and not d2's.
	vector := d1.VersionVector().DeepCopy()
	vector.Set(a2, b.RemovedAt().Lamport()-1)
	vector.Set(a1, d1.VersionVector().VersionOf(a1))
	d1.GarbageCollect(vector)
	require.Len(t, treeCRDT(t, d1).Root().Children(true), 1, "the paragraph is purged")
	require.Equal(t, 3, d1.GarbageLen(),
		`the pairs of b and "z" are pending, and "a"'s, which "z" after it holds back`)

	require.NoError(t, d1.Undo())
	assert.Equal(t, `<doc><p>ab</p><p k="v">cd</p></doc>`, treeXML(t, d1))
	assertRebuildsSame(t, d1, "after the undo")

	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Edit(0, 4, nil, 0)
		return nil
	}))
	assertRebuildsSame(t, d1, "after deleting the recreated paragraph")
	full := d1.VersionVector().DeepCopy()
	full.Set(a2, d2.VersionVector().VersionOf(a2))
	d1.GarbageCollect(full)
	assertRebuildsSame(t, d1, "after collecting it")
	assert.Equal(t, 0, d1.GarbageLen())
}

// TestRestoreForgettingADetachedNodeDropsItsAttributePairs pins the pairs a
// detached element's removed attributes hold when a restore forgets the
// element to recreate it. d2 removes p's attribute and then p, both after
// d1's delete of the div, so a collection that covers d1's delete alone
// purges the div and leaves p detached, with its own pair and its
// attribute's pending; "z", which d2 inserts into the div in the same
// change, keeps the subtree, the collection not having seen it. d1's undo
// forgets p and recreates it; both pairs go
// with the node, or the root keeps one for a node no longer in the
// document and its GC drifts from its copy's.
func TestRestoreForgettingADetachedNodeDropsItsAttributePairs(t *testing.T) {
	d1, d2, a1, a2 := newReplicas(t)
	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
			{Type: "div", Children: []json.TreeNode{
				{Type: "p", Attributes: map[string]string{"k": "v"}, Children: []json.TreeNode{{Type: "text", Value: "ab"}}},
			}},
		}})
		return nil
	}))
	crossSync(t, d1, d2)
	for range 5 {
		require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Style(1, 2, map[string]string{"z": "z"})
			return nil
		}))
	}
	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Edit(0, 6, nil, 0)
		return nil
	}))
	// d2 also inserts "z" into the div in the same change, which the
	// collection below does not cover: its creation keeps the div's
	// subtree after the purge, as a concurrent split's product would.
	require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").RemoveStyle(1, 2, []string{"k"})
		root.GetTree("t").Edit(1, 5, nil, 0)
		root.GetTree("t").Edit(1, 1, &json.TreeNode{Type: "text", Value: "z"}, 0)
		return nil
	}))
	crossSync(t, d1, d2)

	vector := d1.VersionVector().DeepCopy()
	vector.Set(a1, d1.VersionVector().VersionOf(a1))
	vector.Set(a2, treeCRDT(t, d1).Root().Children(true)[0].RemovedAt().Lamport())
	d1.GarbageCollect(vector)
	require.Empty(t, treeCRDT(t, d1).Root().Children(true), "the div is purged")
	require.Equal(t, 4, d1.GarbageLen(), `p, its removed attribute, the styled one and "z" are pending`)
	assertRebuildsSame(t, d1, "after the collection")

	require.NoError(t, d1.Undo())
	assertRebuildsSame(t, d1, "after the undo")
}

// TestReleaseLiveEqualsASnapshotReload pins Live, the figure the server's
// size gate reads, against a replica loaded from a snapshot, which holds no
// detached node. d1 deletes the first paragraph while d2 deletes its "b"
// and undoes that, reviving "b" under the removed paragraph on d1; d1's
// collection releases "b". A reload from d1's snapshot reports the same
// Live. GC can differ by the pending pairs of detached nodes until the
// collection that covers them (see document-size-limit.md).
func TestReleaseLiveEqualsASnapshotReload(t *testing.T) {
	d1, d2, a1, a2 := newReplicas(t)
	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "abc"}}},
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "de"}}},
		}})
		return nil
	}))
	crossSync(t, d1, d2)
	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Edit(0, 5, nil, 0)
		return nil
	}))
	require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Edit(2, 3, nil, 0)
		return nil
	}))
	require.NoError(t, d2.Undo())
	crossSync(t, d1, d2)

	vector := d1.VersionVector().DeepCopy()
	for _, actor := range []time.ActorID{a1, a2} {
		if v := d2.VersionVector().VersionOf(actor); v > vector.VersionOf(actor) {
			vector.Set(actor, v)
		}
	}
	assert.Positive(t, d1.GarbageCollect(vector))
	require.Equal(t, "<doc><p>de</p></doc>", treeXML(t, d1))

	snapshot, err := converter.ObjectToBytes(d1.RootObject())
	require.NoError(t, err)
	obj, err := converter.BytesToObject(snapshot)
	require.NoError(t, err)
	reloaded := crdt.NewRoot(obj)
	live, reloadedLive := d1.DocSize().Live, reloaded.DocSize().Live
	assert.Equal(t, reloadedLive, live, "Live equals a snapshot reload")
}

// TestTreeOpsInsideARemovedTreeElement pins the ledger of content a change
// makes live inside a Tree element that is itself removed (yorkie#2251).
// d1 deletes the first paragraph and then the whole element; d2, which saw
// neither, inserts into the second paragraph, styles it, and deletes "b" of
// the first and undoes that, reviving "b" by identity under the removed
// paragraph. A rebuild counts a removed element in GC whole, so none of it
// may land in Live: Live and GC equal a ledger derived from the content and
// a reload at every step, through a collection that purges the paragraph
// and releases "b" but leaves the element, and one that collects it.
func TestTreeOpsInsideARemovedTreeElement(t *testing.T) {
	for _, collectElement := range []bool{false, true} {
		d1, d2, a1, a2 := newReplicas(t)
		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}}},
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "cd"}}},
			}})
			return nil
		}))
		crossSync(t, d1, d2)
		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(0, 4, nil, 0)
			return nil
		}))
		pRemoval := d1.VersionVector().VersionOf(a1)
		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("t")
			return nil
		}))
		require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(6, 6, &json.TreeNode{Type: "text", Value: "xy"}, 0)
			root.GetTree("t").Style(4, 5, map[string]string{"k": "v"})
			return nil
		}))
		require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 3, nil, 0)
			return nil
		}))
		require.NoError(t, d2.Undo())
		crossSync(t, d1, d2)
		check := func(when string) {
			t.Helper()
			msg := fmt.Sprintf("collect element %v, %s", collectElement, when)
			assertRebuildsSame(t, d1, msg)
			assertReloadLive(t, d1, msg)
		}
		check("before collection")

		vv := time.NewVersionVector()
		vv.Set(a2, d2.VersionVector().VersionOf(a2))
		if collectElement {
			vv.Set(a1, d1.VersionVector().VersionOf(a1))
		} else {
			vv.Set(a1, pRemoval)
		}
		d1.GarbageCollect(vv)
		check("after collection")
		full := vv.DeepCopy()
		full.Set(a1, d1.VersionVector().VersionOf(a1))
		d1.GarbageCollect(full)
		check("after full collection")
		assert.Equal(t, 0, d1.GarbageLen(), "collect element %v", collectElement)
	}
}

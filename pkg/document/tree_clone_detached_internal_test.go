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

package document

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestUndoMatchesOnTheCloneWithoutDetachedNodes pins that an undo resolves
// the same on the document's root, which keeps the detached nodes a purge
// left registered, and on a rebuilt clone. The clone, a DeepCopy, carries
// those nodes too; a replica loaded from a snapshot does not, and Restore
// resolving a detached node as purged is what makes all three agree (see
// TestRestoreLeavesADetachedNodeATombstone for the snapshot side). The
// paragraph of <doc><p>ab</p></doc> is deleted, and a remote restore that
// does not know the delete revives "ab" under it; a collection then purges
// the paragraph and releases "ab", detached but registered. Once the clone
// is rebuilt, undoing the delete must bring back <p>ab</p> on both.
func TestUndoMatchesOnTheCloneWithoutDetachedNodes(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	peer, err := time.ActorIDFromHex("0000000000000000000000ee")
	require.NoError(t, err)
	d := New("clone-detached")
	d.SetActor(actor)

	require.NoError(t, d.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}}},
		}})
		return nil
	}))
	treeOf := func(root *crdt.Root) *crdt.Tree { return root.Object().Get("t").(*crdt.Tree) }
	tree := treeOf(d.doc.root)
	p := tree.Root().Children()[0]
	text := p.Children()[0]
	require.NoError(t, d.Update(func(root *json.Object, _ *presence.Presence) error {
		root.GetTree("t").Edit(0, 4, nil, 0)
		return nil
	}))

	created := text.ID().CreatedAt
	unaware := time.NewVersionVector()
	unaware.Set(created.ActorID(), created.Lamport())
	at := time.NewTicket(d.doc.VersionVector().VersionOf(actor)+1, 0, peer)
	pos := crdt.NewTreePos(p.ID(), p.ID())
	span := &crdt.TreeRestoreSpan{
		ID: text.ID(), NodeType: "text", IsText: true, Length: 2, Value: "ab", ParentID: p.ID(),
	}
	for _, root := range []*crdt.Root{d.cloneRoot, d.doc.root} {
		op := operations.NewRestoreTreeEdit(treeOf(root).CreatedAt(), pos, pos, at,
			[]*crdt.TreeRestoreSpan{span}, crdt.RestoreModeRestore, nil)
		_, err := op.Execute(root, operations.OpSourceRemote, unaware)
		require.NoError(t, err)
	}
	require.False(t, text.IsRemoved(), "revived under the removed paragraph")

	vector := d.doc.VersionVector().DeepCopy()
	vector.Set(peer, at.Lamport())
	assert.Positive(t, d.GarbageCollect(vector))
	require.True(t, text.IsRemoved(), "released with the paragraph")

	d.invalidateClone()
	require.NoError(t, d.Undo())
	assert.Equal(t, "<doc><p>ab</p></doc>", treeOf(d.doc.root).ToXML())
	assert.Equal(t, treeOf(d.doc.root).ToXML(), treeOf(d.cloneRoot).ToXML(), "the root and the clone agree")
}

// TestRemoteOpNamingADetachedNodeAppliesOnTheClone pins that the document's
// clone, rebuilt by DeepCopy, carries the subtrees the root's purges took
// out. <doc><div><p>ab</p></div></doc> loses its div to d's delete while a
// remote restore that did not know it revives p and "ab" under it; a
// collection then releases them, detached but still resolvable. A remote
// insert after "ab", as a late op under the yorkie#2110 race can be, runs on
// the clone before the root: both must apply it, and end the same.
func TestRemoteOpNamingADetachedNodeAppliesOnTheClone(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	peer, err := time.ActorIDFromHex("0000000000000000000000ee")
	require.NoError(t, err)
	d := New("clone-detached-op")
	d.SetActor(actor)
	require.NoError(t, d.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
			{Type: "div", Children: []json.TreeNode{
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}}},
			}},
		}})
		return nil
	}))
	treeOf := func(root *crdt.Root) *crdt.Tree { return root.Object().Get("t").(*crdt.Tree) }
	tree := treeOf(d.doc.root)
	div := tree.Root().Children()[0]
	p := div.Children()[0]
	text := p.Children()[0]
	require.NoError(t, d.Update(func(root *json.Object, _ *presence.Presence) error {
		root.GetTree("t").Edit(0, 6, nil, 0)
		return nil
	}))

	unaware := time.NewVersionVector()
	unaware.Set(actor, text.ID().CreatedAt.Lamport())
	at := time.NewTicket(d.doc.VersionVector().VersionOf(actor)+1, 0, peer)
	pos := crdt.NewTreePos(div.ID(), div.ID())
	spans := []*crdt.TreeRestoreSpan{
		{ID: p.ID(), NodeType: "p", Length: 1, ParentID: div.ID()},
		{ID: text.ID(), NodeType: "text", IsText: true, Length: 2, Value: "ab", ParentID: p.ID()},
	}
	for _, root := range []*crdt.Root{d.cloneRoot, d.doc.root} {
		op := operations.NewRestoreTreeEdit(treeOf(root).CreatedAt(), pos, pos, at, spans, crdt.RestoreModeRestore, nil)
		_, err := op.Execute(root, operations.OpSourceRemote, unaware)
		require.NoError(t, err)
	}
	require.False(t, p.IsRemoved(), "p is live again under the removed div")
	// A merge into p the collection below has not seen keeps the subtree.
	stampUnseenMerge(d, peer, at.Lamport()+100, func(tree *crdt.Tree) *crdt.TreeNode {
		return tree.Root().Children(true)[0].Children(true)[0]
	})

	vector := d.doc.VersionVector().DeepCopy()
	vector.Set(peer, at.Lamport())
	assert.Positive(t, d.GarbageCollect(vector))
	require.True(t, p.IsRemoved(), "released with div")
	d.invalidateClone()
	require.NoError(t, d.ensureClone())

	insertAt := time.NewTicket(at.Lamport()+1, 0, peer)
	after := crdt.NewTreePos(p.ID(), crdt.NewTreeNodeID(text.ID().CreatedAt, 2))
	content := []*crdt.TreeNode{crdt.NewTreeNode(crdt.NewTreeNodeID(insertAt, 0), "text", nil, "X")}
	opVV := vector.DeepCopy()
	opVV.Set(peer, insertAt.Lamport())
	for _, root := range []*crdt.Root{d.cloneRoot, d.doc.root} {
		copied := make([]*crdt.TreeNode, len(content))
		for i, c := range content {
			cc, err := c.DeepCopy()
			require.NoError(t, err)
			copied[i] = cc
		}
		op := operations.NewTreeEdit(treeOf(root).CreatedAt(), after, after, copied, 0, insertAt)
		_, err := op.Execute(root, operations.OpSourceRemote, opVV)
		require.NoError(t, err, "the op resolves on the clone as on the root")
	}
	assert.Equal(t, treeOf(d.doc.root).ToXML(), treeOf(d.cloneRoot).ToXML())
	assert.Equal(t, d.doc.root.DocSize(), d.cloneRoot.DocSize(), "and the ledgers agree")
	assert.Equal(t, d.doc.root.GarbageLen(), d.cloneRoot.GarbageLen())
}

// TestValueCopyOfATreeCarriesNoDetachedNode pins that detached state stays
// with the document and its clone: a Tree's own DeepCopy, the copy a Remove's
// reverse carries as an element payload, holds the attached tree alone, and
// the converter's payload of it names no detached node, while the root's
// DeepCopy, the clone, does carry them.
func TestValueCopyOfATreeCarriesNoDetachedNode(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	peer, err := time.ActorIDFromHex("0000000000000000000000ee")
	require.NoError(t, err)
	d := New("value-copy-detached")
	d.SetActor(actor)
	require.NoError(t, d.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
			{Type: "div", Children: []json.TreeNode{
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}}},
			}},
		}})
		return nil
	}))
	treeOf := func(root *crdt.Root) *crdt.Tree { return root.Object().Get("t").(*crdt.Tree) }
	tree := treeOf(d.doc.root)
	div := tree.Root().Children()[0]
	p := div.Children()[0]
	text := p.Children()[0]
	require.NoError(t, d.Update(func(root *json.Object, _ *presence.Presence) error {
		root.GetTree("t").Edit(0, 6, nil, 0)
		return nil
	}))
	unaware := time.NewVersionVector()
	unaware.Set(actor, text.ID().CreatedAt.Lamport())
	at := time.NewTicket(d.doc.VersionVector().VersionOf(actor)+1, 0, peer)
	pos := crdt.NewTreePos(div.ID(), div.ID())
	spans := []*crdt.TreeRestoreSpan{
		{ID: p.ID(), NodeType: "p", Length: 1, ParentID: div.ID()},
		{ID: text.ID(), NodeType: "text", IsText: true, Length: 2, Value: "ab", ParentID: p.ID()},
	}
	op := operations.NewRestoreTreeEdit(tree.CreatedAt(), pos, pos, at, spans, crdt.RestoreModeRestore, nil)
	_, err = op.Execute(d.doc.root, operations.OpSourceRemote, unaware)
	require.NoError(t, err)
	stampUnseenMerge(d, peer, at.Lamport()+100, func(*crdt.Tree) *crdt.TreeNode { return p })
	vector := d.doc.VersionVector().DeepCopy()
	vector.Set(peer, at.Lamport())
	_, err = d.doc.GarbageCollect(vector)
	require.NoError(t, err)
	require.Same(t, p, tree.DetachedNode(p.ID()), "p is released, detached")

	value, err := tree.DeepCopy()
	require.NoError(t, err)
	assert.Nil(t, value.(*crdt.Tree).DetachedNode(p.ID()), "a value copy holds no detached node")
	for _, node := range converter.ToTreeNodes(value.(*crdt.Tree).Root()) {
		created := node.Id.CreatedAt
		for _, detached := range []*crdt.TreeNode{p, text} {
			id := detached.ID()
			same := created.Lamport == id.CreatedAt.Lamport() &&
				created.Delimiter == id.CreatedAt.Delimiter() && int(node.Id.Offset) == id.Offset
			assert.False(t, same, "the payload names no detached node")
		}
	}

	clone, err := d.doc.root.DeepCopy()
	require.NoError(t, err)
	assert.NotNil(t, treeOf(clone).DetachedNode(p.ID()), "the clone does carry it")
}

// TestADocumentCollectsWithItsOwnVersionVector pins that a document hands
// its own version vector to the drop, for the root and its clone. A remote
// restore that did not know the delete revives "ab" under the removed
// paragraph, and "ab" is stamped merged in by a phantom actor no change
// carried. The collection that releases it lets it go on both, though no
// vector will ever cover the forged ticket.
func TestADocumentCollectsWithItsOwnVersionVector(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	peer, err := time.ActorIDFromHex("0000000000000000000000ee")
	require.NoError(t, err)
	phantom, err := time.ActorIDFromHex("0000000000000000000000ab")
	require.NoError(t, err)
	d := New("forged-merge")
	d.SetActor(actor)
	require.NoError(t, d.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}}},
		}})
		return nil
	}))
	treeOf := func(root *crdt.Root) *crdt.Tree { return root.Object().Get("t").(*crdt.Tree) }
	p := treeOf(d.doc.root).Root().Children()[0]
	text := p.Children()[0]
	require.NoError(t, d.Update(func(root *json.Object, _ *presence.Presence) error {
		root.GetTree("t").Edit(0, 4, nil, 0)
		return nil
	}))

	created := text.ID().CreatedAt
	unaware := time.NewVersionVector()
	unaware.Set(created.ActorID(), created.Lamport())
	at := time.NewTicket(d.doc.VersionVector().VersionOf(actor)+1, 0, peer)
	pos := crdt.NewTreePos(p.ID(), p.ID())
	span := &crdt.TreeRestoreSpan{
		ID: text.ID(), NodeType: "text", IsText: true, Length: 2, Value: "ab", ParentID: p.ID(),
	}
	for _, root := range []*crdt.Root{d.cloneRoot, d.doc.root} {
		op := operations.NewRestoreTreeEdit(treeOf(root).CreatedAt(), pos, pos, at,
			[]*crdt.TreeRestoreSpan{span}, crdt.RestoreModeRestore, nil)
		_, err := op.Execute(root, operations.OpSourceRemote, unaware)
		require.NoError(t, err)
		revived := treeOf(root).Root().Children(true)[0].Children(true)[0]
		require.False(t, revived.IsRemoved())
		revived.MergedAt = time.NewTicket(time.MaxLamport-1, 0, phantom)
	}
	d.doc.changeID = d.doc.changeID.SyncClocks(change.NewID(0, 0, at.Lamport(), peer, d.doc.VersionVector()))

	vector := d.doc.VersionVector().DeepCopy()
	vector.Set(peer, at.Lamport())
	d.GarbageCollect(vector)
	require.True(t, text.IsRemoved(), "released with the paragraph")
	assert.Equal(t, 0, d.GarbageLen(), "and let go in the same collection, the forged ticket counted as seen")
	assert.Equal(t, 0, d.cloneRoot.GarbageLen())
}

// stampUnseenMerge stamps the node pick finds, on the root and on the clone
// when there is one, merged in by a change of actor at lamport that the
// document applied and a collection's vector, short of it, has not seen,
// so a subtree holding the node stays after a purge takes it out, as one
// holding a node a concurrent merge moved in stays. The stamp is the whole
// lineage a merge leaves, MergedFrom with MergedAt, naming a source that has
// since been purged.
func stampUnseenMerge(d *Document, actor time.ActorID, lamport int64, pick func(*crdt.Tree) *crdt.TreeNode) {
	at := time.NewTicket(lamport, 0, actor)
	from := crdt.NewTreeNodeID(time.NewTicket(lamport-1, 0, actor), 0)
	for _, root := range []*crdt.Root{d.doc.root, d.cloneRoot} {
		if root != nil {
			node := pick(root.Object().Get("t").(*crdt.Tree))
			node.MergedFrom = from
			node.MergedAt = at
		}
	}
	applied := d.doc.VersionVector().DeepCopy()
	applied.Set(actor, lamport)
	d.doc.changeID = d.doc.changeID.SyncClocks(change.NewID(0, 0, lamport, actor, applied))
}

// TestLocalTreeOpsOnARemovedElementKeepTheCloneWithTheRoot pins the local
// path of yorkie#2251. An Update that keeps a handle on a Tree, deletes the
// Tree, or the object holding it, and then edits or styles through the
// handle runs the json proxy on the clone, while the root replays the
// operations; both have to charge what the edit makes live to the removed
// element's GC charge, or the clone, whose size the local gate reads, and
// the root disagree for good. Each must equal a ledger derived from the
// content, through a later Update and a collection.
func TestLocalTreeOpsOnARemovedElementKeepTheCloneWithTheRoot(t *testing.T) {
	newTree := func(obj *json.Object) {
		obj.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}}},
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "cd"}}},
		}})
	}
	for _, mode := range []string{"edit", "edit in a removed object", "style", "remove style"} {
		actor, err := time.ActorIDFromHex("000000000000000000000001")
		require.NoError(t, err)
		d := New("local-removed")
		d.SetActor(actor)
		require.NoError(t, d.Update(func(root *json.Object, p *presence.Presence) error {
			newTree(root.SetNewObject("o"))
			newTree(root)
			root.GetTree("t").Style(4, 5, map[string]string{"k": "v"})
			return nil
		}))
		require.NoError(t, d.Update(func(root *json.Object, p *presence.Presence) error {
			tr := root.GetTree("t")
			if mode == "edit in a removed object" {
				tr = root.GetObject("o").GetTree("t")
				root.Delete("o")
			} else {
				root.Delete("t")
			}
			switch mode {
			case "style":
				tr.Style(4, 5, map[string]string{"k": "longer-value"})
			case "remove style":
				tr.RemoveStyle(4, 5, []string{"k"})
			default:
				tr.Edit(6, 6, &json.TreeNode{Type: "text", Value: "xyzw"}, 0)
				tr.Edit(1, 2, nil, 0)
			}
			return nil
		}))
		check := func(when string) {
			t.Helper()
			require.NoError(t, d.ensureClone())
			rederived, err := d.doc.root.Rederive()
			require.NoError(t, err)
			assert.Equal(t, rederived.DocSize(), d.doc.root.DocSize(), "%s, %s: the root", mode, when)
			assert.Equal(t, d.doc.root.DocSize(), d.cloneRoot.DocSize(), "%s, %s: the clone", mode, when)
		}
		check("after the local ops")
		require.NoError(t, d.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("x").Edit(0, 0, "q")
			return nil
		}))
		check("after a later update")
		vector := d.doc.VersionVector().DeepCopy()
		d.GarbageCollect(vector)
		check("after a collection")
	}
}

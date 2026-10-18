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

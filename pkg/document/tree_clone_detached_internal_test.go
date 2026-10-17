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
// left registered, and on a clone rebuilt from the attached tree, which has
// none. The paragraph of <doc><p>ab</p></doc> is deleted, and a remote
// restore that does not know the delete revives "ab" under it; a collection
// then purges the paragraph and releases "ab", detached but registered on
// the root. Once the clone is rebuilt, undoing the delete must bring back
// <p>ab</p> on both: the undo runs on the clone first, and a root that left
// the detached "ab" a tombstone would show <p></p> under the clone's
// <p>ab</p>.
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

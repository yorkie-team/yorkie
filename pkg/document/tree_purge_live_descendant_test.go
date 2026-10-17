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

	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestTreePurgeWithLiveDescendant checks the size ledger when GC purges a
// removed element that still has a live descendant (yorkie#2244).
//
// d1 deletes the first paragraph whole, then applies d2's delete of its "b"
// and d2's undo of that delete, which revives "b" by identity under the
// paragraph d1 removed, as a piece chained after the removed "a". Purging
// the paragraph detaches "b" with it, so a rebuild no longer counts it, and
// the running ledger must not either: Live is what MaxSizePerDocument is
// checked against. "b" leaves the tree with the paragraph, so a later redo
// that names it does not reach it and charges no ledger for it.
func TestTreePurgeWithLiveDescendant(t *testing.T) {
	d1, d2, a1, a2 := newReplicas(t)

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

	pieces := treeCRDT(t, d1).Root().Children(true)[0].Children(true)
	require.Len(t, pieces, 2)
	head, text := pieces[0], pieces[1]
	require.True(t, text.Index.Parent.Value.IsRemoved(), "the paragraph stays removed")
	require.True(t, head.IsRemoved(), `"a" stays removed`)
	require.False(t, text.IsRemoved(), `"b" is live again under it`)
	require.True(t, head.InsNextID.Equal(text.ID()), "the two pieces are chained")
	assertRebuildsSame(t, d1, "before collection")

	vv := d1.VersionVector().DeepCopy()
	for _, actor := range []time.ActorID{a1, a2} {
		if v := d2.VersionVector().VersionOf(actor); v > vv.VersionOf(actor) {
			vv.Set(actor, v)
		}
	}
	assert.Positive(t, d1.GarbageCollect(vv))
	require.Equal(t, "<doc><p>cd</p></doc>", treeXML(t, d1))

	// The detached piece leaves the tree with the paragraph, as if it were
	// purged with it: no lookup by ID and no chain reaches it any more.
	tree := treeCRDT(t, d1)
	if _, registered := tree.NodeMapByID.Floor(text.ID()); registered == text {
		assert.Fail(t, "the detached live piece is still registered")
	}
	assert.Nil(t, text.InsPrevID, "the detached live piece leaves its insertion chain")
	assert.Nil(t, head.InsNextID)

	assertRebuildsSame(t, d1, "after purging an element with a live descendant")
	rebuilt, err := d1.InternalDocumentForTest().DeepCopy()
	require.NoError(t, err)
	live, rebuiltLive := d1.DocSize().Live, rebuilt.DocSize().Live
	assert.Equal(t, (&rebuiltLive).Total(), (&live).Total(), "the size gate reads what a rebuild reads")

	// d2 redoes its delete, which reaches the detached text by identity. The
	// text left d1's ledger with the paragraph, so the removal must not take
	// it out a second time.
	require.NoError(t, d2.Redo())
	crossSync(t, d1, d2)
	assert.False(t, text.IsRemoved(), "the redo does not reach the detached piece")
	assertRebuildsSame(t, d1, "after a redo names the detached text")

	// d2 had not collected when the redo arrived, so it applied it to the
	// paragraph it still held. Once it collects too, the two agree.
	assert.Equal(t, treeXML(t, d1), treeXML(t, d2))
	vv = d1.VersionVector().DeepCopy()
	for _, actor := range []time.ActorID{a1, a2} {
		if v := d2.VersionVector().VersionOf(actor); v > vv.VersionOf(actor) {
			vv.Set(actor, v)
		}
	}
	d1.GarbageCollect(vv)
	d2.GarbageCollect(vv)
	assert.Equal(t, treeShape(t, d1), treeShape(t, d2), "both replicas end on the same tree")
	assertRebuildsSame(t, d2, "the replica that collected after the redo")
}

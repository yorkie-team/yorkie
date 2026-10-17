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

// TestTreePurgeWithLiveDescendant checks the size ledger when GC meets a
// removed element that still has a live descendant (yorkie#2244).
//
// d1 deletes the first paragraph whole, then applies d2's delete of its "b"
// and d2's undo of that delete, which revives "b" by identity under the
// paragraph d1 removed. Purging the paragraph would detach "b" with it:
// bytes Live holds while a rebuild no longer counts them, and Live is what
// MaxSizePerDocument is checked against. GC holds the paragraph instead, and
// "a" with it, until "b" is removed; d2's redo does that, and the next
// collection takes all of it.
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
	assert.Same(t, paragraph, treeCRDT(t, d1).Root().Children(true)[0],
		"the paragraph is held while a live node is under it")
	assertRebuildsSame(t, d1, "after a collection that met the live descendant")
	rebuilt, err := d1.InternalDocumentForTest().DeepCopy()
	require.NoError(t, err)
	live, rebuiltLive := d1.DocSize().Live, rebuilt.DocSize().Live
	assert.Equal(t, (&rebuiltLive).Total(), (&live).Total(), "the size gate reads what a rebuild reads")

	// d2 redoes its delete: "b" is removed on both, and the next collection
	// takes the paragraph with everything under it.
	require.NoError(t, d2.Redo())
	crossSync(t, d1, d2)
	require.True(t, text.IsRemoved())
	assert.Positive(t, collect(t))
	// The hold ends with the live node: GC and Live both equal a rebuild.
	assertRebuildsSame(t, d1, "after the hold is released")
	assert.Equal(t, 0, d1.GarbageLen(), "nothing is left held")
	assert.Equal(t, treeXML(t, d1), treeXML(t, d2))
}

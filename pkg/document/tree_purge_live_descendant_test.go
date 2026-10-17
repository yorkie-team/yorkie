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
// d1 deletes the first paragraph whole, then applies d2's delete of its text
// and d2's undo of that delete, which revives the text by identity under the
// paragraph d1 removed. Purging the paragraph detaches the text with it, so a
// rebuild no longer counts the text, and the running ledger must not either:
// Live is what MaxSizePerDocument is checked against.
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
		root.GetTree("t").Edit(1, 3, nil, 0)
		return nil
	}))
	require.NoError(t, d2.Undo())
	crossSync(t, d1, d2)

	text := treeCRDT(t, d1).Root().Children(true)[0].Children(true)[0]
	require.True(t, text.Index.Parent.Value.IsRemoved(), "the paragraph stays removed")
	require.False(t, text.IsRemoved(), "its text is live again under it")
	assertRebuildsSame(t, d1, "before collection")

	vv := d1.VersionVector().DeepCopy()
	for _, actor := range []time.ActorID{a1, a2} {
		if v := d2.VersionVector().VersionOf(actor); v > vv.VersionOf(actor) {
			vv.Set(actor, v)
		}
	}
	assert.Positive(t, d1.GarbageCollect(vv))
	require.Equal(t, "<doc><p>cd</p></doc>", treeXML(t, d1))

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
	assertRebuildsSame(t, d1, "after a redo names the detached text")
}

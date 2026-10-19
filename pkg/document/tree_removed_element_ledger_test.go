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
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// assertReloadLive checks Live against a replica loaded from doc's snapshot,
// the figure the server's size gate reads.
func assertReloadLive(t *testing.T, doc *document.Document, msg string) {
	t.Helper()
	snapshot, err := converter.ObjectToBytes(doc.RootObject())
	require.NoError(t, err)
	obj, err := converter.BytesToObject(snapshot)
	require.NoError(t, err)
	assert.Equal(t, crdt.NewRoot(obj).DocSize().Live, doc.DocSize().Live, "%s: Live equals a reload", msg)
}

// TestTreeInsertUnderARemovedParentKeepsLive pins the ledger of content an
// Edit inserts under a parent a concurrent change removed. d1 deletes the
// first paragraph while d2 types into it. On d1 the insert lands under the
// removed paragraph and is born a tombstone, which a rebuild counts in GC
// alone, so its pair books it to GC without taking it out of Live, where it
// never was.
func TestTreeInsertUnderARemovedParentKeepsLive(t *testing.T) {
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
	require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Edit(2, 2, &json.TreeNode{Type: "text", Value: "xy"}, 0)
		return nil
	}))
	crossSync(t, d1, d2)
	assert.Equal(t, treeXML(t, d1), treeXML(t, d2))
	for i, d := range []*document.Document{d1, d2} {
		msg := fmt.Sprintf("d%d", i+1)
		assertRebuildsSame(t, d, msg)
		assertReloadLive(t, d, msg)
	}

	vv := time.NewVersionVector()
	vv.Set(a1, d1.VersionVector().VersionOf(a1))
	vv.Set(a2, d2.VersionVector().VersionOf(a2))
	d1.GarbageCollect(vv)
	assertRebuildsSame(t, d1, "after collection")
	assertReloadLive(t, d1, "after collection")
}

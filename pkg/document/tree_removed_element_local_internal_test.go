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

	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

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

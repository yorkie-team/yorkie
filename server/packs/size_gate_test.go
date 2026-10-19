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

package packs

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

func TestCanGrow(t *testing.T) {
	// The document every case starts from: a key, a text and a tree.
	newDoc := func(t *testing.T) *document.Document {
		doc := document.New("d")
		require.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetString("k", "v")
			r.SetNewText("text").Edit(0, 0, "hello")
			r.SetNewTree("tree", json.TreeNode{Type: "doc", Children: []json.TreeNode{
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}}},
			}})
			r.SetNewCounter("cnt", 0)
			return nil
		}))
		return doc
	}

	cases := []struct {
		name   string
		update func(r *json.Object, p *presence.Presence)
		grows  bool
	}{
		{"set", func(r *json.Object, _ *presence.Presence) { r.SetString("k2", "v") }, true},
		{"remove", func(r *json.Object, _ *presence.Presence) { r.Delete("k") }, false},
		{"text insert", func(r *json.Object, _ *presence.Presence) { r.GetText("text").Edit(0, 0, "x") }, true},
		{"text replace", func(r *json.Object, _ *presence.Presence) { r.GetText("text").Edit(0, 1, "x") }, true},
		{"text delete", func(r *json.Object, _ *presence.Presence) { r.GetText("text").Edit(0, 2, "") }, false},
		{"text style", func(r *json.Object, _ *presence.Presence) {
			r.GetText("text").Style(0, 2, map[string]string{"b": "1"})
		}, true},
		{"tree insert", func(r *json.Object, _ *presence.Presence) {
			r.GetTree("tree").Edit(1, 1, &json.TreeNode{Type: "text", Value: "x"}, 0)
		}, true},
		{"tree delete", func(r *json.Object, _ *presence.Presence) { r.GetTree("tree").Edit(1, 2, nil, 0) }, false},
		{"counter", func(r *json.Object, _ *presence.Presence) { r.GetCounter("cnt").Increase(1) }, true},
		{"presence only", func(_ *json.Object, p *presence.Presence) { p.Set("cursor", "1") }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := newDoc(t)
			require.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
				tc.update(r, p)
				return nil
			}))
			changes := doc.CreateChangePack().Changes
			require.Len(t, changes, 2)
			assert.Equal(t, tc.grows, canGrow(changes[1]))
		})
	}
}

// TestCanGrowReadsTheSpansItsModeRevives pins that an identity-preserving
// edit grows the document when the span set its restore mode revives is not
// empty: a restore-mode edit revives its restore spans, a retombstone-mode
// one its retombstone spans (yorkie#2252), and the other set is removed.
func TestCanGrowReadsTheSpansItsModeRevives(t *testing.T) {
	at := time.NewTicket(1, 0, time.InitialActorID)
	treeSpans := []*crdt.TreeRestoreSpan{{ID: crdt.NewTreeNodeID(at, 0), NodeType: "p"}}
	textSpans := []*crdt.RestoreSpan{{}}
	changeOf := func(op operations.Operation) *change.Change {
		id := change.NewID(1, 0, 1, time.InitialActorID, time.NewVersionVector())
		return change.New(id, "", []operations.Operation{op}, nil)
	}
	pos := crdt.NewTreePos(crdt.NewTreeNodeID(at, 0), crdt.NewTreeNodeID(at, 0))
	textPos := crdt.NewRGATreeSplitNodePos(crdt.NewRGATreeSplitNodeID(at, 0), 0)
	for _, tc := range []struct {
		name            string
		mode            crdt.RestoreMode
		restore, retomb bool
		grows           bool
	}{
		{"restore mode reviving its restore spans", crdt.RestoreModeRestore, true, false, true},
		{"restore mode only removing", crdt.RestoreModeRestore, false, true, false},
		{"retombstone mode reviving its retombstone spans", crdt.RestoreModeRetombstone, false, true, true},
		{"retombstone mode only removing", crdt.RestoreModeRetombstone, true, false, false},
	} {
		var tr, tt []*crdt.TreeRestoreSpan
		var xr, xt []*crdt.RestoreSpan
		if tc.restore {
			tr, xr = treeSpans, textSpans
		}
		if tc.retomb {
			tt, xt = treeSpans, textSpans
		}
		tree := operations.NewRestoreTreeEdit(at, pos, pos, at, tr, tc.mode, tt)
		assert.Equal(t, tc.grows, canGrow(changeOf(tree)), "tree: %s", tc.name)
		text := operations.NewRestoreEdit(at, textPos, textPos, at, xr, tc.mode, xt)
		assert.Equal(t, tc.grows, canGrow(changeOf(text)), "text: %s", tc.name)
	}
}

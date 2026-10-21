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

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestDissolutionAgainstAnUnawareSplitInEveryOrder runs the §6.4 window:
// a split that never saw a merge, concurrent with the undo that revives the
// merge's source, on a node holding both the source and a child the merge
// moved into it. In <r><div><p>ab</p><p>cd</p></div></r>, d1 deletes from
// the start of the div into the first paragraph, which moves "b" up into
// the div stamped with that paragraph, and undoes it; d2 splits the div.
// d3 applies the merge, the split, then the undo, so its split reads the
// stamp §7.1 partitions on; d1 applies the split after the undo cleared it;
// d2 applies the split first. All three must agree.
//
// The same scenario, over every delete range, split position and level of
// this tree and of the flat <r><p>ab</p><p>cd</p></r>, gives the same result
// on main and with the dissolution turned off, row for row.
func TestDissolutionAgainstAnUnawareSplitInEveryOrder(t *testing.T) {
	for _, splitAt := range []int{5, 9} {
		var docs []*document.Document
		for _, hex := range []string{
			"000000000000000000000001", "000000000000000000000002", "000000000000000000000003",
		} {
			actor, err := time.ActorIDFromHex(hex)
			require.NoError(t, err)
			doc := document.New("test-doc")
			doc.SetActor(actor)
			docs = append(docs, doc)
		}
		d1, d2, d3 := docs[0], docs[1], docs[2]
		take := func(d *document.Document) *change.Pack {
			pack := d.CreateChangePack()
			var last uint32
			if len(pack.Changes) > 0 {
				last = pack.Changes[len(pack.Changes)-1].ClientSeq()
			}
			require.NoError(t, d.ApplyChangePack(change.NewPack(
				pack.DocumentKey, change.NewCheckpoint(0, last), nil, time.InitialVersionVector, nil)))
			return pack
		}
		give := func(pack *change.Pack, dsts ...*document.Document) {
			for _, d := range dsts {
				require.NoError(t, d.ApplyChangePack(change.NewPack(
					pack.DocumentKey, change.NewCheckpoint(0, 0), pack.Changes, time.InitialVersionVector, nil)))
			}
		}

		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "r", Children: []json.TreeNode{
				{Type: "div", Children: []json.TreeNode{
					{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}}},
					{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "cd"}}},
				}},
			}})
			return nil
		}))
		give(take(d1), d2, d3)

		// <r>0<div>1<p>2a3b4</p>5<p>6c7d8</p>9</div>10</r>
		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 3, nil, 0)
			return nil
		}))
		merge := take(d1)
		require.NoError(t, d1.Undo())
		undo := take(d1)
		require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(splitAt, splitAt, nil, 1)
			return nil
		}))
		split := take(d2)

		give(merge, d3)
		give(split, d3, d1)
		give(undo, d3)
		give(merge, d2)
		give(undo, d2)

		want := treeXML(t, d1)
		assert.Equal(t, want, treeXML(t, d2), "split at %d", splitAt)
		assert.Equal(t, want, treeXML(t, d3), "split at %d", splitAt)
	}
}

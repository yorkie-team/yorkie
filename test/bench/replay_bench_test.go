//go:build bench

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

package bench

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
)

// BenchmarkChangeReplay measures the server's rebuild path: replaying stored
// changes into a fresh InternalDocument through ApplyChangePack, exactly as
// BuildInternalDocForServerSeq does for every snapshot, compaction and
// push-pull that misses the cache.
//
// Undo/redo made every operation build the bookkeeping a reverse operation
// needs, which is linear work per change and so quadratic over a replay:
// `Edit.Execute` normalizes the anchor over the whole physical `prev` chain,
// and `Tree.Edit` resolves a pre-edit visible index and captures an identity
// span per touched node, each of which walks a parent's children including
// its accumulated tombstones. The replay path discards every one of those
// values, so it must not pay for them.
//
// The cases cover both data types and both directions on purpose: the cost is
// paid per removed node as well as per inserted one, and a guard that only
// covers Text insertions is how the Tree half of it went unnoticed once
// already. The numbers must stay close to linear in the edit count.
func BenchmarkChangeReplay(b *testing.B) {
	for _, tc := range []struct {
		name    string
		changes func(*testing.B, int) []*change.Change
	}{
		{"text-insert", textInsertChanges},
		{"text-delete", textDeleteChanges},
		{"tree-insert", treeInsertChanges},
		{"tree-delete", treeDeleteChanges},
	} {
		for _, cnt := range []int{400, 1600} {
			b.Run(fmt.Sprintf("%s-%d-edits", tc.name, cnt), func(b *testing.B) {
				// Replayed as a pack rather than through ApplyChanges: the
				// pack entry point is the one the server rebuild actually
				// takes, and it is what distinguishes a replay from a client
				// applying the same changes remotely.
				pack := change.NewPack("d1", change.InitialCheckpoint, tc.changes(b, cnt), nil, nil)
				b.ResetTimer()

				for range b.N {
					doc := document.NewInternalDocument("d1")
					if err := doc.ApplyChangePack(pack, true); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// textInsertChanges builds cnt appending Text edits as a client would and
// returns the resulting changes, ready to be replayed. Replaying leaves the
// operations untouched, so the same slice can be replayed repeatedly.
func textInsertChanges(b *testing.B, cnt int) []*change.Change {
	doc := document.New("d1")
	assert.NoError(b, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewText("text")
		return nil
	}))

	for i := range cnt {
		assert.NoError(b, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(i, i, "a")
			return nil
		}))
	}

	return doc.CreateChangePack().Changes
}

// textDeleteChanges seeds cnt characters in a single change, then deletes them
// one change at a time from the front. The seeding change is part of the
// replay, so the case measures cnt deletions on top of one bulk insertion.
func textDeleteChanges(b *testing.B, cnt int) []*change.Change {
	doc := document.New("d1")
	assert.NoError(b, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewText("text").Edit(0, 0, strings.Repeat("a", cnt))
		return nil
	}))

	for range cnt {
		assert.NoError(b, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(0, 1, "")
			return nil
		}))
	}

	return doc.CreateChangePack().Changes
}

// treeInsertChanges builds cnt appending Tree edits, one text node per change,
// all into the same paragraph. Sharing one parent is what makes the per-node
// sibling scans visible: every insertion lengthens the child list the next one
// walks.
func treeInsertChanges(b *testing.B, cnt int) []*change.Change {
	doc := document.New("d1")
	assert.NoError(b, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{
			Type:     "doc",
			Children: []json.TreeNode{{Type: "p", Children: []json.TreeNode{}}},
		})
		return nil
	}))

	for i := range cnt {
		assert.NoError(b, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(i+1, i+1, &json.TreeNode{Type: "text", Value: "a"}, 0)
			return nil
		}))
	}

	return doc.CreateChangePack().Changes
}

// treeDeleteChanges seeds one text node of cnt characters, then deletes them
// one change at a time from the front. Each deletion leaves a tombstone in the
// paragraph, so the child list every later change walks keeps growing even as
// the visible tree shrinks — the shape the removed-node bookkeeping is most
// expensive on.
func treeDeleteChanges(b *testing.B, cnt int) []*change.Change {
	doc := document.New("d1")
	assert.NoError(b, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{
			Type: "doc",
			Children: []json.TreeNode{{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: strings.Repeat("a", cnt)}},
			}},
		})
		return nil
	}))

	for range cnt {
		assert.NoError(b, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 2, nil, 0)
			return nil
		}))
	}

	return doc.CreateChangePack().Changes
}

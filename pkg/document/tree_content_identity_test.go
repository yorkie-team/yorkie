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
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// Every position anchors by TreeNodeID, so an ID must name a single node.
// See docs/design/tree-content-identity.md.

// treeNodeIDs returns every node id in the tree under key "t", sorted.
func treeNodeIDs(t *testing.T, root *crdt.Object) []string {
	t.Helper()

	tree, ok := root.Get("t").(*crdt.Tree)
	assert.True(t, ok)

	var ids []string
	for _, node := range tree.Nodes() {
		ids = append(ids, node.IDString())
	}
	sort.Strings(ids)

	return ids
}

// duplicatedTreeIDs returns the ids that name more than one node.
func duplicatedTreeIDs(ids []string) []string {
	seen := map[string]int{}
	var dupes []string
	for _, id := range ids {
		seen[id]++
		if seen[id] == 2 {
			dupes = append(dupes, id)
		}
	}
	return dupes
}

// newTreeDoc returns a document holding <r><p>ab</p></r> under key "t".
func newTreeDoc(t *testing.T, hexActor string) *document.Document {
	t.Helper()

	doc := document.New("doc")
	actor, err := time.ActorIDFromHex(hexActor)
	assert.NoError(t, err)
	doc.SetActor(actor)

	assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
		r.SetNewTree("t", json.TreeNode{
			Type: "r",
			Children: []json.TreeNode{{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "ab"}},
			}},
		})
		return nil
	}))

	return doc
}

// TestBulkContentsGetDistinctIDs verifies that an edit inserting several nodes
// at once gives each of them its own identity. They are separate nodes in the
// document, and a position anchored at an id that names two of them resolves
// differently on different replicas.
func TestBulkContentsGetDistinctIDs(t *testing.T) {
	doc := newTreeDoc(t, "000000000000000000000001")

	assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
		r.GetTree("t").EditBulkByPath([]int{1}, []int{1}, []*json.TreeNode{{
			Type:     "p",
			Children: []json.TreeNode{{Type: "text", Value: "x"}},
		}, {
			Type:     "p",
			Children: []json.TreeNode{{Type: "text", Value: "y"}},
		}}, 0)
		return nil
	}))

	ids := treeNodeIDs(t, doc.RootObject())
	assert.Empty(t, duplicatedTreeIDs(ids), "each inserted node needs its own id, got %v", ids)
}

// TestSingleContentEditKeepsItsIDs pins the compatibility this change rests
// on. The first content keeps the ticket the edit already issued, so an edit
// through Edit — as opposed to EditBulk — assigns the ids it always did, and a
// stored history replays to the state its snapshot holds. Issuing a fresh
// ticket for every content would shift these.
func TestSingleContentEditKeepsItsIDs(t *testing.T) {
	doc := newTreeDoc(t, "000000000000000000000001")

	assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
		r.GetTree("t").Edit(2, 2, &json.TreeNode{
			Type:     "p",
			Children: []json.TreeNode{{Type: "text", Value: "x"}},
		}, 0)
		return nil
	}))

	assert.Equal(t, []string{
		"1:1:AAAAAAAAAAAAAAAB:0",
		"1:2:AAAAAAAAAAAAAAAB:0",
		"1:3:AAAAAAAAAAAAAAAB:0",
		"1:3:AAAAAAAAAAAAAAAB:1",
		"2:1:AAAAAAAAAAAAAAAB:0",
		"2:2:AAAAAAAAAAAAAAAB:0",
	}, treeNodeIDs(t, doc.RootObject()))
}

// TestOriginatorAndReplicaAgree compares what the replica that issued the
// tickets built against what a replica applying the same change builds. The
// originator's own tree is the one Update executed the change on; its clone,
// which the json proxies mutated with the tickets actually issued, is compared
// too — an edit that splits is where the two allocations can drift apart.
func TestOriginatorAndReplicaAgree(t *testing.T) {
	origin := newTreeDoc(t, "000000000000000000000001")

	assert.NoError(t, origin.Update(func(r *json.Object, p *presence.Presence) error {
		r.GetTree("t").EditBulkByPath([]int{0, 1}, []int{0, 1}, []*json.TreeNode{{
			Type:     "p",
			Children: []json.TreeNode{{Type: "text", Value: "x"}},
		}, {
			Type:     "p",
			Children: []json.TreeNode{{Type: "text", Value: "y"}},
		}}, 0)
		return nil
	}))

	pack := origin.CreateChangePack()

	replica := document.New("doc")
	actorB, err := time.ActorIDFromHex("000000000000000000000002")
	assert.NoError(t, err)
	replica.SetActor(actorB)
	pack.VersionVector.Set(replica.ActorID(), replica.VersionVector().VersionOf(replica.ActorID()))
	assert.NoError(t, replica.ApplyChangePack(pack))

	assert.Equal(t, origin.Root().GetTree("t").ToXML(), replica.Root().GetTree("t").ToXML())
	assert.Equal(t, treeNodeIDs(t, origin.RootObject()), treeNodeIDs(t, replica.RootObject()),
		"a replica must give the inserted nodes the ids the originator issued")
}

// TestSplitTicketCollidesWithContent is the source this change does NOT
// remove, kept as its reproduction. The tickets an element split consumes are
// simulated from the operation rather than carried in it, and the simulation
// advances by the number of top-level contents while the originator also
// spends one per descendant — so a split ticket can land on an id the edit's
// own content already holds. Fixing it changes the wire; see
// docs/design/tree-content-identity.md.
func TestSplitTicketCollidesWithContent(t *testing.T) {
	t.Skip("known issue: split tickets are simulated, not carried; see docs/design/tree-content-identity.md")

	doc := newTreeDoc(t, "000000000000000000000001")

	assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
		r.GetTree("t").Edit(2, 2, &json.TreeNode{Type: "text", Value: "q"}, 1)
		r.GetTree("t").Edit(1, 1, &json.TreeNode{Type: "text", Value: "z"}, 0)
		return nil
	}))

	ids := treeNodeIDs(t, doc.RootObject())
	assert.Empty(t, duplicatedTreeIDs(ids), "a split ticket must not land on the edit's own content, got %v", ids)
}

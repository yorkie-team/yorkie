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

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestTreeOpsNamingACollectedSubtree throws every kind of Tree operation at
// the nodes of a removed paragraph that GC has taken, with a live descendant
// under it or without, on two replicas: d1, which collected, and d2, which
// did not. The ops come straight off the
// operation layer, naming the nodes by ID and position as a client could put
// them on the wire. After each, both replicas must agree on the visible tree
// and each one's size ledger must equal a rebuild; once d2 collects too, both
// must hold the same tree, tombstones included (yorkie#2244).
func TestTreeOpsNamingACollectedSubtree(t *testing.T) {
	// replicas builds <doc><p>ab</p><p>cd</p></doc>, with the first
	// paragraph removed and "b" revived under it, and runs a collection on
	// d1 only, which takes the paragraph out of the tree. With release set,
	// "b" is removed again first; without, the collection tombstones it and
	// takes it out with the paragraph.
	replicas := func(t *testing.T, release bool) (d1, d2 *document.Document, vv time.VersionVector) {
		t.Helper()
		d1, d2, _, _ = newReplicas(t)
		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}}},
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "cd"}}},
			}})
			return nil
		}))
		crossSync(t, d1, d2)
		// d1 deletes the paragraph while d2 deletes "b" and undoes that, so
		// d2's restore is concurrent with the paragraph's removal: d1, which
		// applies it after the removal, revives "b" under the removed
		// paragraph, while d2 tombstones "b" again when the removal arrives,
		// the identity-revive-versus-remove race main has too (yorkie#2247).
		// Only the visible tree can be compared until both collect.
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
		if release {
			require.NoError(t, d2.Redo())
			crossSync(t, d1, d2)
		}

		vv = time.NewVersionVector()
		for _, d := range []*document.Document{d1, d2} {
			for actor, lamport := range d.VersionVector() {
				if lamport > vv.VersionOf(actor) {
					vv.Set(actor, lamport)
				}
			}
		}
		d1.GarbageCollect(vv)
		return d1, d2, vv
	}

	// nodes returns, off d2, which never collects, the paragraph, its pieces
	// "a" and "b", and the tree.
	nodes := func(t *testing.T, d *document.Document) (*crdt.Tree, *crdt.TreeNode, *crdt.TreeNode, *crdt.TreeNode) {
		tree := treeCRDT(t, d)
		paragraph := tree.Root().Children(true)[0]
		pieces := paragraph.Children(true)
		require.Len(t, pieces, 2)
		return tree, paragraph, pieces[0], pieces[1]
	}

	type opFor func(tree *crdt.Tree, paragraph, a, b *crdt.TreeNode, at *time.Ticket) operations.Operation
	spanOf := func(node *crdt.TreeNode, parent, left, right *crdt.TreeNodeID) *crdt.TreeRestoreSpan {
		span := &crdt.TreeRestoreSpan{
			ID: node.ID(), NodeType: node.Type(), IsText: node.IsText(), Length: node.Len(),
			Value: node.Value, ParentID: parent, LeftSiblingID: left, RightSiblingID: right,
		}
		if !node.IsText() {
			span.Length = 1
		}
		return span
	}
	ops := []struct {
		name string
		op   opFor
	}{
		{"insert under the paragraph", func(tree *crdt.Tree, p, _, _ *crdt.TreeNode, at *time.Ticket) operations.Operation {
			pos := crdt.NewTreePos(p.ID(), p.ID())
			text := crdt.NewTreeNode(crdt.NewTreeNodeID(at, 0), "text", nil, "XYZ")
			return operations.NewTreeEdit(tree.CreatedAt(), pos, pos, []*crdt.TreeNode{text}, 0, at)
		}},
		{"insert after b", func(tree *crdt.Tree, p, _, b *crdt.TreeNode, at *time.Ticket) operations.Operation {
			pos := crdt.NewTreePos(p.ID(), crdt.NewTreeNodeID(b.ID().CreatedAt, b.ID().Offset+b.Len()))
			text := crdt.NewTreeNode(crdt.NewTreeNodeID(at, 0), "text", nil, "XYZ")
			return operations.NewTreeEdit(tree.CreatedAt(), pos, pos, []*crdt.TreeNode{text}, 0, at)
		}},
		{"delete over the paragraph", func(tree *crdt.Tree, p, _, _ *crdt.TreeNode, at *time.Ticket) operations.Operation {
			from := crdt.NewTreePos(tree.Root().ID(), tree.Root().ID())
			to := crdt.NewTreePos(tree.Root().ID(), p.ID())
			return operations.NewTreeEdit(tree.CreatedAt(), from, to, nil, 0, at)
		}},
		{"style the paragraph", func(tree *crdt.Tree, p, _, _ *crdt.TreeNode, at *time.Ticket) operations.Operation {
			from := crdt.NewTreePos(tree.Root().ID(), tree.Root().ID())
			to := crdt.NewTreePos(tree.Root().ID(), p.ID())
			return operations.NewTreeStyle(tree.CreatedAt(), from, to, map[string]string{"k": "v"}, at)
		}},
		{"restore b under p", func(tree *crdt.Tree, p, a, b *crdt.TreeNode, at *time.Ticket) operations.Operation {
			pos := crdt.NewTreePos(p.ID(), p.ID())
			span := spanOf(b, p.ID(), a.ID(), nil)
			return operations.NewRestoreTreeEdit(tree.CreatedAt(), pos, pos, at,
				[]*crdt.TreeRestoreSpan{span}, crdt.RestoreModeRestore, nil)
		}},
		{"restore the paragraph and b", func(tree *crdt.Tree, p, _, b *crdt.TreeNode, at *time.Ticket) operations.Operation {
			pos := crdt.NewTreePos(tree.Root().ID(), tree.Root().ID())
			spans := []*crdt.TreeRestoreSpan{
				spanOf(p, tree.Root().ID(), nil, nil),
				spanOf(b, p.ID(), nil, nil),
			}
			return operations.NewRestoreTreeEdit(tree.CreatedAt(), pos, pos, at,
				spans, crdt.RestoreModeRestore, nil)
		}},
		{"restore a span anchored on b", func(tree *crdt.Tree, p, _, b *crdt.TreeNode, at *time.Ticket) operations.Operation {
			pos := crdt.NewTreePos(p.ID(), p.ID())
			span := spanOf(b, p.ID(), b.ID(), b.ID())
			return operations.NewRestoreTreeEdit(tree.CreatedAt(), pos, pos, at,
				[]*crdt.TreeRestoreSpan{span}, crdt.RestoreModeRestore, nil)
		}},
		{"retombstone p and b", func(tree *crdt.Tree, p, _, b *crdt.TreeNode, at *time.Ticket) operations.Operation {
			pos := crdt.NewTreePos(tree.Root().ID(), tree.Root().ID())
			spans := []*crdt.TreeRestoreSpan{
				spanOf(p, tree.Root().ID(), nil, nil),
				spanOf(b, p.ID(), nil, nil),
			}
			return operations.NewRestoreTreeEdit(tree.CreatedAt(), pos, pos, at,
				nil, crdt.RestoreModeRetombstone, spans)
		}},
	}

	peer, err := time.ActorIDFromHex("0000000000000000000000ee")
	require.NoError(t, err)
	for _, release := range []bool{false, true} {
		for i, tc := range ops {
			t.Run(fmt.Sprintf("released %v: %s", release, tc.name), func(t *testing.T) {
				d1, d2, vv := replicas(t, release)
				require.Len(t, treeCRDT(t, d1).Root().Children(true), 1, "d1 took the paragraph out")

				// Nodes are named off d2, which still holds them all.
				tree, paragraph, a, b := nodes(t, d2)
				at := time.NewTicket(time.MaxLamport/2+int64(i), 0, peer)
				opVV := vv.DeepCopy()
				opVV.Set(peer, at.Lamport())
				for _, d := range []*document.Document{d1, d2} {
					op := tc.op(tree, paragraph, a, b, at)
					// An op the replica cannot resolve fails there; that is
					// what a purged node does on main, and it changes nothing.
					_, _ = op.Execute(d.InternalDocumentForTest().Root(), operations.OpSourceRemote, opVV)
					assertRebuildsSame(t, d, tc.name)
				}
				assert.Equal(t, treeXML(t, d1), treeXML(t, d2), "both replicas show the same tree")

				final := vv.DeepCopy()
				final.Set(peer, at.Lamport())
				d1.GarbageCollect(final)
				d2.GarbageCollect(final)
				assert.Equal(t, treeXML(t, d1), treeXML(t, d2))
				assert.Equal(t, treeShape(t, d1), treeShape(t, d2), "and the same tree once both collect")
				assertRebuildsSame(t, d1, "after both collect")
				assertRebuildsSame(t, d2, "after both collect")
			})
		}
	}
}

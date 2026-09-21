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

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
)

// TestTreeStyleAfterCollect is the sequence reported in issue #2008, kept
// verbatim because it is what a user actually did.
//
// It used to take the process down with a nil dereference. Two collections
// around an undo left a live node hanging under a parent the second collection
// purged, so the node stayed registered in NodeMapByID with no path to the
// root; styling through it walked off the top of the detached subtree.
//
// Two changes meet here. recreateFromSpan no longer places a restored node
// live under a tombstoned parent, so the orphan is never built -- that is why
// this now reads as an ordinary successful style rather than an error. The nil
// guard in toTreePos is the second, and it is pinned separately in
// crdt.TestToTreePosRejectsAChainEndingInAPurgedNode, because a document-level
// test can no longer reach it.
//
// Every step is load-bearing: dropping either GarbageCollect, the second Edit,
// or the Undo stopped the original panic, and a smaller seed tree never
// panicked at all.
func TestTreeStyleAfterCollect(t *testing.T) {
	doc := document.New("repro")

	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
			{Type: "p", Children: []json.TreeNode{
				{Type: "span", Children: []json.TreeNode{{Type: "text", Value: "abcdefgh"}}},
				{Type: "span", Children: []json.TreeNode{{Type: "text", Value: "ijklmn"}}},
			}},
			{Type: "p", Children: []json.TreeNode{
				{Type: "span", Children: []json.TreeNode{{Type: "text", Value: "opqrst"}}},
			}},
		}})
		return nil
	}))

	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Edit(23, 27, nil, 0)
		return nil
	}))
	doc.GarbageCollect(doc.VersionVector())

	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Edit(19, 22, nil, 1)
		return nil
	}))
	require.NoError(t, doc.Undo())
	doc.GarbageCollect(doc.VersionVector())

	// The style that used to fault. json.(*Tree).Style reports failure by
	// panicking with the error it got back, so a clean return is also the
	// assertion that nothing was refused.
	require.NotPanics(t, func() {
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Style(21, 22, map[string]string{"color": "2"})
			return nil
		}))
	})

	// Every node the document still tracks has to be reachable from the root.
	// That is the invariant the crash came from breaking, and asserting the
	// style merely succeeded would not catch a stranded node left behind:
	// NodeLen counts what is registered in NodeMapByID, Nodes traverses what
	// is reachable, and the orphan was registered but not reachable.
	tree := doc.Root().GetTree("t")
	require.Equal(t, tree.NodeLen(), len(tree.Nodes()),
		"every registered node must be reachable from the root")
}

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
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
)

// TestTreeStyleAfterCollect reproduces issue #2008. Two garbage collections
// around an undo leave the tree holding a position whose parent chain has
// been unlinked from the root. Styling through that position sends
// crdt.(*Tree).toTreePos walking up from a removed node to its least alive
// ancestor; the walk runs off the top of the detached subtree and
// dereferences a nil Index.Parent, panicking inside Document.Update.
//
// Styling a range that no longer resolves must fail as an error, not take
// the process down.
//
// Every step below is load-bearing: dropping either GarbageCollect, the
// second Edit, or the Undo makes the panic vanish, and a smaller seed tree
// does not panic at all.
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

	// json.(*Tree).Style reports a failed style by panicking with the error
	// it got back, so a panic alone is not the defect. The defect is *which*
	// panic: a runtime fault from dereferencing a nil Index.Parent, raised
	// before any error value exists. The tree layer must refuse the position
	// and hand an error up instead.
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_ = doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Style(21, 22, map[string]string{"color": "2"})
			return nil
		})
	}()

	if fault, ok := recovered.(runtime.Error); ok {
		t.Fatalf("styling a collected range faulted instead of reporting an error: %v", fault)
	}

	// Assert the guard actually fired, not merely that nothing crashed. The
	// state this test builds depends on a second defect -- an undo that
	// restores a node under an already-tombstoned parent -- so a fix for that
	// defect will make Style resolve normally and leave nothing to recover.
	// Requiring the error makes this test fail loudly when that day comes,
	// rather than passing while guarding nothing.
	err, ok := recovered.(error)
	require.Truef(t, ok, "expected the style to be refused, recovered %#v", recovered)
	require.ErrorIs(t, err, crdt.ErrNodeNotFound)
}

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

package operations

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/index"
)

// ticketer returns a function issuing a fresh ticket on every call, the way
// change.Context.IssueTimeTicket does.
func ticketer() func() *time.Ticket {
	delimiter := uint32(0)
	return func() *time.Ticket {
		delimiter++
		return time.NewTicket(1, delimiter, time.InitialActorID)
	}
}

// treeIDsOf collects the ids of the given content subtrees, in the order the
// reissue traversal visits them.
func treeIDsOf(contents []*crdt.TreeNode) []string {
	var ids []string
	for _, content := range contents {
		index.TraverseNode(content.Index, func(node *index.Node[*crdt.TreeNode], _ int) {
			ids = append(ids, node.Value.IDString())
		})
	}
	return ids
}

// TestTreeEditReissueContentIDs pins the identity rule a copy-reinserting
// reverse depends on: the copy carries the removed nodes' ids, and inserting
// it again would put two nodes under one id. A restore-mode reverse revives
// by identity instead, so it must keep its ids.
func TestTreeEditReissueContentIDs(t *testing.T) {
	issue := ticketer()
	newContent := func() *crdt.TreeNode {
		p := crdt.NewTreeNode(crdt.NewTreeNodeID(issue(), 0), "p", nil)
		assert.NoError(t, p.Append(
			crdt.NewTreeNode(crdt.NewTreeNodeID(issue(), 0), index.TextNodeType, nil, "a"),
			crdt.NewTreeNode(crdt.NewTreeNodeID(issue(), 0), index.TextNodeType, nil, "b"),
		))
		return p
	}

	t.Run("gives copied content a fresh identity test", func(t *testing.T) {
		content := newContent()
		content.InsPrevID = crdt.NewTreeNodeID(issue(), 0)
		content.InsNextID = crdt.NewTreeNodeID(issue(), 0)
		content.MergedFrom = crdt.NewTreeNodeID(issue(), 0)
		content.MergedAt = issue()

		op := NewTreeEdit(issue(), nil, nil, []*crdt.TreeNode{content}, 0, issue())
		before := treeIDsOf(op.Contents())

		assert.NoError(t, op.ReissueContentIDs(issue))

		after := treeIDsOf(op.Contents())
		// The count is stated rather than derived from the traversal the
		// reissue uses, so a traversal that skipped a node shows up here.
		assert.Len(t, after, 3, "the <p> and its two texts")
		assert.Len(t, lo(after), 3, "every node gets its own id")
		for _, id := range after {
			assert.NotContains(t, before, id, "no node keeps the id it was copied from")
		}

		// A fresh identity has to be fresh in every field naming a node: the
		// copy carries the split chain and merge lineage of the node it was
		// copied from, and left in place it is spliced into a chain it never
		// belonged to.
		assert.Nil(t, content.InsPrevID)
		assert.Nil(t, content.InsNextID)
		assert.Nil(t, content.MergedFrom)
		assert.Nil(t, content.MergedAt)
	})

	t.Run("leaves a restore-mode reverse alone test", func(t *testing.T) {
		// Deliberately over-constrained: a restore reverse is built with no
		// contents at all, so this pins the guard rather than a shape
		// production emits.
		content := newContent()
		op := NewRestoreTreeEdit(issue(), nil, nil, issue(), nil, crdt.RestoreModeRestore, nil)
		op.contents = []*crdt.TreeNode{content}
		before := treeIDsOf(op.Contents())

		assert.NoError(t, op.ReissueContentIDs(issue))

		assert.Equal(t, before, treeIDsOf(op.Contents()),
			"a restore revives nodes by identity and must keep it")
	})

	t.Run("assigns ids a later reissue does not repeat test", func(t *testing.T) {
		opA := NewTreeEdit(issue(), nil, nil, []*crdt.TreeNode{newContent()}, 0, issue())
		opB := NewTreeEdit(issue(), nil, nil, []*crdt.TreeNode{newContent()}, 0, issue())

		assert.NoError(t, opA.ReissueContentIDs(issue))
		assert.NoError(t, opB.ReissueContentIDs(issue))

		assert.NotEqual(t, treeIDsOf(opA.Contents()), treeIDsOf(opB.Contents()))
	})

	t.Run("refuses a splitting edit test", func(t *testing.T) {
		// The tickets taken here start one past executedAt's delimiter and run
		// one per node, while Execute simulates the tickets an element split
		// consumes starting past the content count. The two ranges overlap as
		// soon as content has descendants, so this only holds for splitLevel 0
		// -- which is every reverse toReverseOperation builds.
		op := NewTreeEdit(issue(), nil, nil, []*crdt.TreeNode{newContent()}, 1, issue())
		assert.ErrorIs(t, op.ReissueContentIDs(issue), ErrCannotReissueSplittingEdit)
	})
}

// TestTreeEditTopLevelRemoved pins the top-level filter a copy-reinserting
// reverse uses to pick its content. JS's own nodesToBeRemoved INCLUDES
// pre-tombstoned nodes while Go's removed EXCLUDES them, so parent membership
// has to be tested against the union of both sets: testing removed alone
// promotes a live descendant of an already-tombstoned parent to top level, and
// undo resurrects it at the wrong depth.
func TestTreeEditTopLevelRemoved(t *testing.T) {
	issue := ticketer()
	node := func(nodeType, value string) *crdt.TreeNode {
		return crdt.NewTreeNode(crdt.NewTreeNodeID(issue(), 0), nodeType, nil, value)
	}

	t.Run("keeps a node whose parent this edit did not touch test", func(t *testing.T) {
		p := node("p", "")
		text := node(index.TextNodeType, "a")
		assert.NoError(t, p.Append(text))

		assert.Equal(t, []*crdt.TreeNode{text},
			topLevelRemoved([]*crdt.TreeNode{text}, nil))
	})

	t.Run("drops a node whose parent this edit also removed test", func(t *testing.T) {
		p := node("p", "")
		text := node(index.TextNodeType, "a")
		assert.NoError(t, p.Append(text))

		assert.Equal(t, []*crdt.TreeNode{p},
			topLevelRemoved([]*crdt.TreeNode{p, text}, nil))
	})

	t.Run("drops a node whose parent was already tombstoned test", func(t *testing.T) {
		p := node("p", "")
		text := node(index.TextNodeType, "a")
		assert.NoError(t, p.Append(text))

		// p is not in removed -- this edit found it already tombstoned -- so a
		// filter reading removed alone would promote its live child.
		assert.Empty(t, topLevelRemoved(
			[]*crdt.TreeNode{text},
			map[string]struct{}{p.IDString(): {}},
		))
	})

	t.Run("keeps a parentless node test", func(t *testing.T) {
		orphan := node(index.TextNodeType, "a")
		assert.Equal(t, []*crdt.TreeNode{orphan},
			topLevelRemoved([]*crdt.TreeNode{orphan}, nil))
	})
}

// lo returns the distinct values of the given slice.
func lo(values []string) []string {
	seen := map[string]struct{}{}
	var distinct []string
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		distinct = append(distinct, value)
	}
	return distinct
}

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

// buildTree returns a fresh CRDT tree holding <r><p>ab</p></r>, built from
// crdt primitives so this package can drive Tree.Edit without a Root.
func buildTree(t *testing.T, issue func() *time.Ticket) *crdt.Tree {
	t.Helper()

	root := crdt.NewTreeNode(crdt.NewTreeNodeID(issue(), 0), "r", nil)
	p := crdt.NewTreeNode(crdt.NewTreeNodeID(issue(), 0), "p", nil)
	assert.NoError(t, p.Append(
		crdt.NewTreeNode(crdt.NewTreeNodeID(issue(), 0), index.TextNodeType, nil, "ab"),
	))
	assert.NoError(t, root.Append(p))

	tree := crdt.NewTree(root, issue())
	assert.Equal(t, "<r><p>ab</p></r>", tree.ToXML())

	return tree
}

// editAt runs a Tree.Edit over the given visible index range.
func editAt(
	t *testing.T, tree *crdt.Tree, from, to int,
	contents []*crdt.TreeNode, issue func() *time.Ticket,
) crdt.TreeEditReverseInfo {
	t.Helper()

	fromPos, err := tree.FindPos(from)
	assert.NoError(t, err)
	toPos, err := tree.FindPos(to)
	assert.NoError(t, err)

	_, _, info, err := tree.Edit(fromPos, toPos, contents, 0, issue(), issue, nil)
	assert.NoError(t, err)

	return info
}

// indexOfPos converts a TreePos back to a visible index, so a reverse
// operation's range can be asserted in the units the test states it in.
func indexOfPos(t *testing.T, tree *crdt.Tree, pos *crdt.TreePos) int {
	t.Helper()

	parent, left := tree.ToTreeNodes(pos)
	assert.NotNil(t, parent)
	idx, err := tree.ToIndex(parent, left)
	assert.NoError(t, err)

	return idx
}

// TestTreeEditCopyReinsertFallback covers the reverse built when Tree.Edit's
// identity spans do not fully describe the edit (SpansComplete false) — merge
// propagation, content born tombstoned under a removed parent, or a piece
// split off an already-tombstoned node. Ordinary single-client editing always
// produces complete spans and never reaches this path, so the spans are
// blanked here to drive it, the way the JS SDK's own copy-path test stubs
// CRDTTree.edit to return none.
func TestTreeEditCopyReinsertFallback(t *testing.T) {
	// dropSpans makes info look like an edit whose spans were incomplete.
	dropSpans := func(info crdt.TreeEditReverseInfo) crdt.TreeEditReverseInfo {
		info.SpansComplete = false
		info.RemovedSpans = nil
		info.InsertedSpans = nil
		return info
	}

	t.Run("re-inserts the removed content as a copy test", func(t *testing.T) {
		issue := ticketer()
		tree := buildTree(t, issue)
		info := editAt(t, tree, 1, 3, nil, issue)
		assert.Equal(t, "<r><p></p></r>", tree.ToXML())

		op := NewTreeEdit(issue(), nil, nil, nil, 0, issue())
		reverse, err := op.toReverseOperation(tree, nil, dropSpans(info), 1)
		assert.NoError(t, err)

		edit, ok := reverse.(*TreeEdit)
		assert.True(t, ok)
		assert.Empty(t, edit.RestoreSpans(), "the fallback copies rather than restoring by identity")
		assert.Len(t, edit.Contents(), 1)
		assert.Equal(t, "ab", edit.Contents()[0].Value)
		assert.False(t, edit.Contents()[0].IsRemoved(), "the copy is re-inserted live")
		assert.Equal(t, 1, indexOfPos(t, tree, edit.FromPos()))
		assert.Equal(t, 1, indexOfPos(t, tree, edit.ToPos()),
			"a pure delete leaves nothing to remove, so the range is zero-width")
	})

	t.Run("drops descendants tombstoned before this edit test", func(t *testing.T) {
		// "a" is deleted on its own first, so the later whole-element delete
		// finds it already tombstoned. Copying it back would resurrect a
		// delete the user made independently.
		issue := ticketer()
		tree := buildTree(t, issue)
		editAt(t, tree, 1, 2, nil, issue)
		assert.Equal(t, "<r><p>b</p></r>", tree.ToXML())

		info := editAt(t, tree, 0, 3, nil, issue)
		assert.Equal(t, "<r></r>", tree.ToXML())
		assert.Len(t, info.PreTombstoned, 1, `the "a" piece was already tombstoned`)

		op := NewTreeEdit(issue(), nil, nil, nil, 0, issue())
		reverse, err := op.toReverseOperation(tree, nil, dropSpans(info), 0)
		assert.NoError(t, err)

		contents := reverse.(*TreeEdit).Contents()
		assert.Len(t, contents, 1, "only the <p> is top-level; its text comes with it")
		assert.Equal(t, "<p>b</p>", crdt.ToXML(contents[0]))
		// The clone carried the pre-drop size, so this is where a missing
		// bottom-up rebuild shows up: 1 text token plus the element's two.
		assert.Equal(t, 3, contents[0].Index.PaddedLength())
	})

	t.Run("deletes the range it inserted test", func(t *testing.T) {
		issue := ticketer()
		tree := buildTree(t, issue)
		content := crdt.NewTreeNode(crdt.NewTreeNodeID(issue(), 0), index.TextNodeType, nil, "XY")
		info := editAt(t, tree, 3, 3, []*crdt.TreeNode{content}, issue)
		assert.Equal(t, "<r><p>abXY</p></r>", tree.ToXML())

		op := NewTreeEdit(issue(), nil, nil, nil, 0, issue())
		reverse, err := op.toReverseOperation(tree, []*crdt.TreeNode{content}, dropSpans(info), 3)
		assert.NoError(t, err)

		edit := reverse.(*TreeEdit)
		assert.Empty(t, edit.Contents(), "the edit removed nothing, so the reverse re-inserts nothing")
		assert.Equal(t, 3, indexOfPos(t, tree, edit.FromPos()))
		assert.Equal(t, 5, indexOfPos(t, tree, edit.ToPos()), "the range covers the inserted content")
	})

	t.Run("skips a merging edit test", func(t *testing.T) {
		// A merge's reverse is a split; re-inserting the emptied element would
		// restore a shell whose children now live in the merge target.
		issue := ticketer()
		root := crdt.NewTreeNode(crdt.NewTreeNodeID(issue(), 0), "r", nil)
		for _, value := range []string{"ab", "cd"} {
			p := crdt.NewTreeNode(crdt.NewTreeNodeID(issue(), 0), "p", nil)
			assert.NoError(t, p.Append(
				crdt.NewTreeNode(crdt.NewTreeNodeID(issue(), 0), index.TextNodeType, nil, value),
			))
			assert.NoError(t, root.Append(p))
		}
		tree := crdt.NewTree(root, issue())

		info := editAt(t, tree, 3, 5, nil, issue)
		assert.Equal(t, "<r><p>abcd</p></r>", tree.ToXML())
		assert.Positive(t, info.MergeLevel, "the edit merged a boundary")
		assert.False(t, info.SpansComplete, "a merge's spans do not describe it")

		op := NewTreeEdit(issue(), nil, nil, nil, 0, issue())
		reverse, err := op.toReverseOperation(tree, nil, info, 3)
		assert.NoError(t, err)
		assert.Nil(t, reverse, "no reverse at all is better than one that restores a shell")
	})
}

// TestTreeEditSpansCompleteGate pins when Tree.Edit hands out identity spans
// at all. They are the difference between an undo that revives nodes in place
// and one that re-inserts copies, so a silent regression to empty spans would
// otherwise only show as duplicated content under concurrency.
func TestTreeEditSpansCompleteGate(t *testing.T) {
	t.Run("a plain delete reports complete spans test", func(t *testing.T) {
		issue := ticketer()
		tree := buildTree(t, issue)
		info := editAt(t, tree, 1, 3, nil, issue)

		assert.True(t, info.SpansComplete)
		assert.Len(t, info.RemovedSpans, 1)
		assert.Equal(t, "ab", info.RemovedSpans[0].Value)
		assert.True(t, info.RemovedSpans[0].IsText)
		assert.Equal(t, 2, info.RemovedSpans[0].Length)
		assert.NotNil(t, info.RemovedSpans[0].ParentID, "a restore re-anchors under its parent")
		assert.Empty(t, info.InsertedSpans)
	})

	t.Run("a delete reports its subtree parent before child test", func(t *testing.T) {
		issue := ticketer()
		tree := buildTree(t, issue)
		info := editAt(t, tree, 0, 4, nil, issue)

		assert.True(t, info.SpansComplete)
		assert.Len(t, info.RemovedSpans, 2)
		assert.Equal(t, "p", info.RemovedSpans[0].NodeType,
			"parent first: recreating a purged subtree resolves a child's parent by identity")
		assert.True(t, info.RemovedSpans[1].IsText)
	})

	t.Run("an insert reports its content parent before child test", func(t *testing.T) {
		issue := ticketer()
		tree := buildTree(t, issue)
		content := crdt.NewTreeNode(crdt.NewTreeNodeID(issue(), 0), "p", nil)
		assert.NoError(t, content.Append(
			crdt.NewTreeNode(crdt.NewTreeNodeID(issue(), 0), index.TextNodeType, nil, "z"),
		))
		info := editAt(t, tree, 4, 4, []*crdt.TreeNode{content}, issue)
		assert.Equal(t, "<r><p>ab</p><p>z</p></r>", tree.ToXML())

		assert.True(t, info.SpansComplete)
		assert.Empty(t, info.RemovedSpans)
		assert.Len(t, info.InsertedSpans, 2)
		assert.Equal(t, "p", info.InsertedSpans[0].NodeType, "parent before child")
		assert.Equal(t, "z", info.InsertedSpans[1].Value)
		assert.Equal(t, 3, info.InsertedContentSize, "one text token plus the element's two")
	})

	t.Run("a merging edit reports no spans test", func(t *testing.T) {
		issue := ticketer()
		root := crdt.NewTreeNode(crdt.NewTreeNodeID(issue(), 0), "r", nil)
		for _, value := range []string{"ab", "cd"} {
			p := crdt.NewTreeNode(crdt.NewTreeNodeID(issue(), 0), "p", nil)
			assert.NoError(t, p.Append(
				crdt.NewTreeNode(crdt.NewTreeNodeID(issue(), 0), index.TextNodeType, nil, value),
			))
			assert.NoError(t, root.Append(p))
		}
		tree := crdt.NewTree(root, issue())

		info := editAt(t, tree, 3, 5, nil, issue)

		assert.False(t, info.SpansComplete)
		assert.Empty(t, info.RemovedSpans, "an incomplete account is handed out as none at all")
		assert.Empty(t, info.InsertedSpans)
		assert.NotEmpty(t, info.Removed, "the copy-reinsert fallback still gets what it needs")
	})
}

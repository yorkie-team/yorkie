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

package crdt_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// Tree.PurgeBarrierAt reports every ticket that has to be causally stable
// before a tombstone may be unlinked. Beside the document-sibling ticket it
// has always reported, it now reports the split chains the same-boundary walks
// read: those of the tombstone's ancestors, and the tombstone's own.
//
// Each leg of the chain gate stands for a different walk, and the end-to-end
// tests in pkg/document only reach the InsPrevID one -- a right-half product,
// the shape a local split produces. These assert the gate itself, so the
// InsNextID leg (emptyRunReachesActor's start node, which is found among
// document siblings and so need not carry an InsPrevID) and the tombstone's
// own membership (which §7.8 reads through its IsRemoved break) are pinned
// too.

// barrierTree builds <r><p>ab</p><q></q></r> and returns the tree with p, its
// text child and q. Nothing is in a split chain yet; each subtest links the
// chain it is about.
func barrierTree(t *testing.T) (*crdt.Tree, *crdt.TreeNode, *crdt.TreeNode, *crdt.TreeNode) {
	t.Helper()

	var s ticketer
	node := func(nodeType string, value ...string) *crdt.TreeNode {
		return crdt.NewTreeNode(crdt.NewTreeNodeID(s.issue(knownActor), 0), nodeType, nil, value...)
	}
	issue := func() *time.Ticket { return s.issue(knownActor) }

	tree := crdt.NewTree(node("r"), s.issue(knownActor))

	p := node("p")
	_, _, err := tree.EditT(0, 0, []*crdt.TreeNode{p}, 0, issue(), issue)
	require.NoError(t, err)

	text := node("text", "ab")
	_, _, err = tree.EditT(1, 1, []*crdt.TreeNode{text}, 0, issue(), issue)
	require.NoError(t, err)

	q := node("q")
	_, _, err = tree.EditT(4, 4, []*crdt.TreeNode{q}, 0, issue(), issue)
	require.NoError(t, err)

	require.Equal(t, "<r><p>ab</p><q></q></r>", tree.ToXML())

	return tree, p, text, q
}

// barrierKeys renders the reported barrier tickets so a subtest can compare
// them without depending on pointer identity.
func barrierKeys(tree *crdt.Tree, node *crdt.TreeNode) []string {
	var keys []string
	for _, ticket := range tree.PurgeBarrierAt(node) {
		keys = append(keys, ticket.Key())
	}
	return keys
}

func TestTreePurgeBarrierSplitChain(t *testing.T) {
	// The baseline the other subtests are read against: text is p's only
	// child, so there is no sibling ticket, and no node on its ancestor path
	// is in a chain.
	t.Run("reports nothing for a tombstone outside every chain", func(t *testing.T) {
		tree, _, text, _ := barrierTree(t)
		assert.Empty(t, barrierKeys(tree, text))
	})

	// The InsPrevID leg: the parent is a split product, which is what a local
	// SplitElement leaves behind and what the pkg/document tests drive.
	t.Run("reports an ancestor that is a split product", func(t *testing.T) {
		tree, p, text, q := barrierTree(t)
		p.InsPrevID = q.ID()

		assert.Equal(t, []string{p.ID().CreatedAt.Key()}, barrierKeys(tree, text))
	})

	// The InsNextID leg: the parent is the node that was split rather than
	// the product, so it carries an InsNextID and no InsPrevID. It is where
	// emptyRunReachesActor starts, and its Children(true) count decides that
	// walk, so its tombstones have to wait as well.
	t.Run("reports an ancestor that was split, carrying only an InsNextID", func(t *testing.T) {
		tree, p, text, q := barrierTree(t)
		p.InsNextID = q.ID()

		assert.Equal(t, []string{p.ID().CreatedAt.Key()}, barrierKeys(tree, text))
	})

	// The tombstone's own membership. §7.8 breaks at a chain node that is
	// removed; purging it relinks the chain across it (Tree.Purge), so the
	// walk runs on to its InsNext on the collecting replica while it still
	// stops there on the other. Both tickets are reported.
	t.Run("reports the tombstone's own chain, and its InsNext's", func(t *testing.T) {
		tree, _, text, q := barrierTree(t)
		text.InsNextID = q.ID()

		assert.ElementsMatch(t, []string{
			text.ID().CreatedAt.Key(),
			q.ID().CreatedAt.Key(),
		}, barrierKeys(tree, text))
	})

	// With no InsNext to run on to there is nothing past the purged node for
	// the walk to reach, so only the node's own ticket is reported.
	t.Run("reports only its own ticket when the tombstone ends its chain", func(t *testing.T) {
		tree, _, text, q := barrierTree(t)
		text.InsPrevID = q.ID()

		assert.Equal(t, []string{text.ID().CreatedAt.Key()}, barrierKeys(tree, text))
	})

	// The reported set is a list, not a single ticket: a tombstone can need
	// several chains covered at once.
	t.Run("reports every chain a tombstone depends on at once", func(t *testing.T) {
		tree, p, text, q := barrierTree(t)
		p.InsPrevID = q.ID()
		text.InsNextID = q.ID()

		assert.ElementsMatch(t, []string{
			text.ID().CreatedAt.Key(),
			q.ID().CreatedAt.Key(),
			p.ID().CreatedAt.Key(),
		}, barrierKeys(tree, text))
	})
}

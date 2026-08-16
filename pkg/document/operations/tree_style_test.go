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

package operations_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/index"
)

// newTreeStyleTestRoot builds a root holding a Tree under key "t" with
// content <r><p>ab</p></r>, the "p" node carrying the attribute bold="true",
// so a test can style over it.
func newTreeStyleTestRoot(t *testing.T, actor *time.ActorID) (*crdt.Root, *crdt.Tree) {
	t.Helper()

	delimiter := uint32(0)
	issue := func() *time.Ticket {
		delimiter++
		return time.NewTicket(1, delimiter, *actor)
	}

	root := crdt.NewTreeNode(crdt.NewTreeNodeID(issue(), 0), "r", nil)
	p := crdt.NewTreeNode(crdt.NewTreeNodeID(issue(), 0), "p", nil)
	assert.NoError(t, p.Append(
		crdt.NewTreeNode(crdt.NewTreeNodeID(issue(), 0), index.TextNodeType, nil, "ab"),
	))
	assert.NoError(t, root.Append(p))

	tree := crdt.NewTree(root, issue())
	assert.Equal(t, "<r><p>ab</p></r>", tree.ToXML())

	fromPos, err := tree.FindPos(0)
	assert.NoError(t, err)
	toPos, err := tree.FindPos(1)
	assert.NoError(t, err)
	_, _, _, err = tree.Style(fromPos, toPos, map[string]string{"bold": "true"}, issue(), nil)
	assert.NoError(t, err)

	obj := crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket)
	obj.Set("t", tree)
	r := crdt.NewRoot(obj)

	return r, tree
}

// pNode returns the tree's "p" element, the node newTreeStyleTestRoot styles.
func pNode(t *testing.T, tree *crdt.Tree) *crdt.TreeNode {
	t.Helper()
	return tree.Root().Children()[0]
}

func TestTreeStyle(t *testing.T) {
	actor, _ := time.ActorIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaa")

	t.Run("reverse of a remove-only forward op restores the removed value", func(t *testing.T) {
		root, tree := newTreeStyleTestRoot(t, &actor)

		fromPos, err := tree.FindPos(0)
		assert.NoError(t, err)
		toPos, err := tree.FindPos(1)
		assert.NoError(t, err)

		removeOp := operations.NewTreeStyleRemove(
			tree.CreatedAt(), fromPos, toPos, []string{"bold"}, time.NewTicket(2, 0, actor),
		)
		reverseRes, err := removeOp.Execute(root, operations.OpSourceLocal, time.NewVersionVector())
		reverse := reverseRes.Reverse
		assert.NoError(t, err)
		assert.False(t, pNode(t, tree).Attrs.Has("bold"),
			"forward RemoveStyle should have removed the attribute")

		reverseStyle, ok := reverse.(*operations.TreeStyle)
		assert.True(t, ok, "reverse of a remove-only op should be a set-attributes TreeStyle")
		assert.Equal(t, map[string]string{"bold": "true"}, reverseStyle.Attributes())
		assert.Empty(t, reverseStyle.AttributesToRemove())

		reverseStyle.SetExecutedAt(time.NewTicket(3, 0, actor))
		_, err = reverseStyle.Execute(root, operations.OpSourceUndoRedo, time.NewVersionVector())
		assert.NoError(t, err)
		assert.Equal(t, "true", pNode(t, tree).Attrs.Get("bold"),
			"executing the reverse should restore the removed value")
	})

	t.Run("reverse of a mixed set carries both branches, but only restores on execute", func(t *testing.T) {
		// bold already exists ("true"); italic does not. Styling both in one
		// call must produce a single reverse that carries both a restore
		// (bold) and a removal (italic) -- that construction step matches
		// Text's and is asserted below via Attributes()/AttributesToRemove().
		//
		// Executing that reverse is where Tree diverges from Text: JS's
		// tree_style_operation.ts execute is `if (attributes.size) {...}
		// else {...}`, not two independent branches like Text's, so a
		// combined reverse's attributesToRemove half is silently skipped
		// whenever attributes is non-empty. This is a known JS defect (PR
		// #1221 copied Text's combined-reverse constructor without also
		// copying Text's independent-if execute shape from PR #1174) --
		// preserved here rather than fixed, per this port's rule not to fix
		// a defect the JS SDK still has. See
		// docs/tasks/active/20260816-tree-style-combined-reverse-dropped-todo.md.
		root, tree := newTreeStyleTestRoot(t, &actor)

		fromPos, err := tree.FindPos(0)
		assert.NoError(t, err)
		toPos, err := tree.FindPos(1)
		assert.NoError(t, err)

		setOp := operations.NewTreeStyle(
			tree.CreatedAt(), fromPos, toPos,
			map[string]string{"bold": "false", "italic": "true"},
			time.NewTicket(2, 0, actor),
		)
		reverseRes, err := setOp.Execute(root, operations.OpSourceLocal, time.NewVersionVector())
		reverse := reverseRes.Reverse
		assert.NoError(t, err)

		attrs := pNode(t, tree).Attrs
		assert.Equal(t, "false", attrs.Get("bold"))
		assert.Equal(t, "true", attrs.Get("italic"))

		reverseStyle, ok := reverse.(*operations.TreeStyle)
		assert.True(t, ok, "reverse of a mixed set should be a TreeStyle carrying both branches")
		assert.Equal(t, map[string]string{"bold": "true"}, reverseStyle.Attributes())
		assert.Equal(t, []string{"italic"}, reverseStyle.AttributesToRemove())

		reverseStyle.SetExecutedAt(time.NewTicket(3, 0, actor))
		_, err = reverseStyle.Execute(root, operations.OpSourceUndoRedo, time.NewVersionVector())
		assert.NoError(t, err)

		attrs = pNode(t, tree).Attrs
		assert.Equal(t, "true", attrs.Get("bold"), "bold is restored via the attributes branch")
		assert.Equal(t, "true", attrs.Get("italic"),
			"italic is NOT removed: the attributesToRemove half of a combined "+
				"reverse is dropped when attributes is also non-empty")
	})

	t.Run("styling with an unchanged value still returns a restoring reverse", func(t *testing.T) {
		root, tree := newTreeStyleTestRoot(t, &actor)

		fromPos, err := tree.FindPos(0)
		assert.NoError(t, err)
		toPos, err := tree.FindPos(1)
		assert.NoError(t, err)

		// Styling with the same value that is already present changes
		// nothing observable, but Tree.Style still reports the prior value
		// (Existed: true) for the requested key, so the reverse still
		// restores it -- it is not nil merely because the value repeats.
		setOp := operations.NewTreeStyle(
			tree.CreatedAt(), fromPos, toPos,
			map[string]string{"bold": "true"},
			time.NewTicket(2, 0, actor),
		)
		reverseRes, err := setOp.Execute(root, operations.OpSourceLocal, time.NewVersionVector())
		reverse := reverseRes.Reverse
		assert.NoError(t, err)

		reverseStyle, ok := reverse.(*operations.TreeStyle)
		assert.True(t, ok)
		assert.Equal(t, map[string]string{"bold": "true"}, reverseStyle.Attributes())
		assert.Empty(t, reverseStyle.AttributesToRemove())
	})

	t.Run("reverse of a mixed set survives a wire round trip", func(t *testing.T) {
		// toTreeStyle (api/converter/to_pb.go) always encodes both
		// Attributes and AttributesToRemove, and JS's TreeStyleOperation
		// constructor always accepts both -- so a combined reverse (built
		// above) must decode both fields together too. Decoding them as
		// mutually exclusive would silently drop the restore half on every
		// replica that receives this reverse over the wire, diverging it
		// from the replica that executed it locally.
		root, tree := newTreeStyleTestRoot(t, &actor)

		fromPos, err := tree.FindPos(0)
		assert.NoError(t, err)
		toPos, err := tree.FindPos(1)
		assert.NoError(t, err)

		setOp := operations.NewTreeStyle(
			tree.CreatedAt(), fromPos, toPos,
			map[string]string{"bold": "false", "italic": "true"},
			time.NewTicket(2, 0, actor),
		)
		reverseRes, err := setOp.Execute(root, operations.OpSourceLocal, time.NewVersionVector())
		reverse := reverseRes.Reverse
		assert.NoError(t, err)

		pbOps, err := converter.ToOperations([]operations.Operation{reverse})
		assert.NoError(t, err)
		decodedOps, err := converter.FromOperations(pbOps)
		assert.NoError(t, err)
		assert.Len(t, decodedOps, 1)

		decoded, ok := decodedOps[0].(*operations.TreeStyle)
		assert.True(t, ok)
		assert.Equal(t, map[string]string{"bold": "true"}, decoded.Attributes(),
			"the restore half must survive the wire, not just the removal half")
		assert.Equal(t, []string{"italic"}, decoded.AttributesToRemove())

		decoded.SetExecutedAt(time.NewTicket(3, 0, actor))
		_, err = decoded.Execute(root, operations.OpSourceRemote, time.NewVersionVector())
		assert.NoError(t, err)

		// Both fields decoded correctly (asserted above); what a remote
		// peer's Execute then does with them is the separately-filed if/else
		// defect (see the "carries both branches, but only restores on
		// execute" test above) -- bold is restored, italic is not removed.
		attrs := pNode(t, tree).Attrs
		assert.Equal(t, "true", attrs.Get("bold"),
			"executing the decoded reverse should restore bold")
		assert.Equal(t, "true", attrs.Get("italic"),
			"italic is left in place on this replica too -- decoding is not the defect, execute is")
	})

	t.Run("reverse removal of an absent-before key survives a DeepCopy round trip", func(t *testing.T) {
		// Unlike Text (whose per-character attribute encoding does not
		// carry isRemoved through a snapshot, filed in
		// docs/tasks/active/20260816-remote-redo-replica-divergence-todo.md),
		// a tree node's attributes are encoded via the generic toRHT/fromRHT
		// pair, which does carry isRemoved -- so this branch is not affected
		// by that defect. DeepCopy is still exercised directly here as the
		// narrowest possible check; the document-level snapshot round trip
		// lives in pkg/document/tree_style_undo_test.go.
		root, tree := newTreeStyleTestRoot(t, &actor)

		fromPos, err := tree.FindPos(0)
		assert.NoError(t, err)
		toPos, err := tree.FindPos(1)
		assert.NoError(t, err)

		setOp := operations.NewTreeStyle(
			tree.CreatedAt(), fromPos, toPos,
			map[string]string{"italic": "true"},
			time.NewTicket(2, 0, actor),
		)
		reverseRes, err := setOp.Execute(root, operations.OpSourceLocal, time.NewVersionVector())
		reverse := reverseRes.Reverse
		assert.NoError(t, err)

		reverseStyle, ok := reverse.(*operations.TreeStyle)
		assert.True(t, ok)
		reverseStyle.SetExecutedAt(time.NewTicket(3, 0, actor))
		_, err = reverseStyle.Execute(root, operations.OpSourceUndoRedo, time.NewVersionVector())
		assert.NoError(t, err)
		assert.False(t, pNode(t, tree).Attrs.Has("italic"))

		copied, err := tree.DeepCopy()
		assert.NoError(t, err)
		copiedTree, ok := copied.(*crdt.Tree)
		assert.True(t, ok)
		assert.False(t, pNode(t, copiedTree).Attrs.Has("italic"),
			"a DeepCopy must not resurrect a key undo correctly removed")
	})
}

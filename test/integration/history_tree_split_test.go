//go:build integration

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

package integration

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

// This file ports history_tree_split_test.ts in full: split- and
// merge-related Tree history coverage for `Tree.Edit` with splitLevel=1 and
// splitLevel=2, built on the boundary-deletion reverse operation described in
// docs/design/tree-split-undo-redo.md (`operations.TreeEdit.toSplitReverseOperation`).
//
// One JS case stays skipped here for the same reason it is skipped in JS
// (history_tree_split_test.ts:798-802, TODO(#1235)): a split-l2 -> split-l2
// undo chain, where two consecutive splitLevel=2 splits produce tombstoned
// structure the boundary-deletion reverse does not correctly restore. See
// TestHistoryTreeSplitL2ChainedOps below.

// initSplitReproDoc seeds the given document with
// <doc><p><inline></inline></p></doc>, the fixture
// history_tree_split_test.ts's initDoc uses for the reverseOp
// pre-tombstoned-filter regression suite.
func initSplitReproDoc(t *testing.T, doc *document.Document) {
	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{{
			Type:     "p",
			Children: []json.TreeNode{{Type: "inline"}},
		}}})
		return nil
	}, "init"))
}

// insertSplitSiblingBlock inserts a second <p><inline></inline></p> block
// after the first, mirroring history_tree_split_test.ts's
// insertSiblingBlock.
func insertSplitSiblingBlock(t *testing.T, doc *document.Document) {
	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").EditByPath([]int{1}, []int{1}, &json.TreeNode{
			Type:     "p",
			Children: []json.TreeNode{{Type: "inline"}},
		}, 0)
		return nil
	}, "insert-block"))
}

// secondInlineRe locates the text content of the second block's <inline>,
// mirroring history_tree_split_test.ts's typeInSecondBlock regex. Only ever
// matched against ASCII input in this file, so byte length, rune count and
// UTF-16 code unit count agree.
var secondInlineRe = regexp.MustCompile(`<inline>([^<]*)</inline></p></doc>$`)

// typeInSplitSecondBlock inserts one character at the end of the second
// <p>'s <inline>, mirroring history_tree_split_test.ts's typeInSecondBlock:
// the insertion index is computed from the visible XML so this works
// regardless of whether the inline also carries tombstoned children.
func typeInSplitSecondBlock(t *testing.T, doc *document.Document, ch string) {
	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		xml := root.GetTree("t").ToXML()
		cur := 0
		if m := secondInlineRe.FindStringSubmatch(xml); m != nil {
			cur = len(m[1])
		}
		root.GetTree("t").EditByPath([]int{1, 0, cur}, []int{1, 0, cur}, &json.TreeNode{
			Type: "text", Value: ch,
		}, 0)
		return nil
	}, fmt.Sprintf("type-%s", ch)))
}

// treeNodeIDString renders a crdt.TreeNodeID the way CRDTTreeNodeID's
// toIDString does in the JS SDK: <actor-lamport-delimiter key>:<offset>.
// TreeNodeID.toIDString itself is unexported, so this is reconstructed from
// the two exported fields of crdt.TreeNodeID.
func treeNodeIDString(id *crdt.TreeNodeID) string {
	if id == nil {
		return "-"
	}
	return id.CreatedAt.Key() + ":" + strconv.Itoa(id.Offset)
}

// splitTestTopRedoTreeEdit returns the last *operations.TreeEdit in the top
// entry of the redo stack, mirroring history_tree_split_test.ts's
// topRedoTreeEdit.
func splitTestTopRedoTreeEdit(doc *document.Document) *operations.TreeEdit {
	top := doc.RedoStackTopForTest()
	for i := len(top) - 1; i >= 0; i-- {
		if te, ok := top[i].Op.(*operations.TreeEdit); ok {
			return te
		}
	}
	return nil
}

// splitTestReversePayload concatenates whichever span set a reverse
// TreeEdit carries -- an undo of an insert fills RetombstoneSpans, an undo
// of a delete fills RestoreSpans -- mirroring history_tree_split_test.ts's
// reversePayload.
func splitTestReversePayload(op *operations.TreeEdit) []*crdt.TreeRestoreSpan {
	if op == nil {
		return nil
	}
	spans := make([]*crdt.TreeRestoreSpan, 0, len(op.RestoreSpans())+len(op.RetombstoneSpans()))
	spans = append(spans, op.RestoreSpans()...)
	spans = append(spans, op.RetombstoneSpans()...)
	return spans
}

// splitTestFingerprint builds an ordered, identity-bearing description of a
// reverse payload, mirroring history_tree_split_test.ts's fingerprint.
// Comparing sizes alone would not notice a return to copy-reinsert, which
// can emit the same number of spans while minting a fresh identity every
// cycle -- exactly the regression identity-preserving restore exists to
// prevent.
func splitTestFingerprint(op *operations.TreeEdit) []string {
	var out []string
	for _, s := range splitTestReversePayload(op) {
		out = append(out, fmt.Sprintf("%s/%s/%s/%d/%s",
			treeNodeIDString(s.ID), treeNodeIDString(s.ParentID), s.NodeType, s.Length, s.Value))
	}
	return out
}

// TestHistoryTreeSplitSingleClientMerge ports history_tree_split_test.ts's
// "Tree History - single client split/merge" describe block (:88-198): 4
// runtime instances, the fixed (non-table-driven) split and merge cases.
func TestHistoryTreeSplitSingleClientMerge(t *testing.T) {
	t.Run("should undo editByPath split", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "ABCD"}},
			}}})
			return nil
		}, "init"))

		before := doc.Root().GetTree("t").ToXML()
		assert.Equal(t, "<doc><p>ABCD</p></doc>", before)

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 2}, []int{0, 2}, nil, 1)
			return nil
		}, "split"))
		after := doc.Root().GetTree("t").ToXML()
		assert.Equal(t, "<doc><p>AB</p><p>CD</p></doc>", after)

		assert.NoError(t, doc.Undo())
		assert.Equal(t, before, doc.Root().GetTree("t").ToXML())
	})

	t.Run("should redo editByPath split", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "ABCD"}},
			}}})
			return nil
		}, "init"))

		before := doc.Root().GetTree("t").ToXML()
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 2}, []int{0, 2}, nil, 1)
			return nil
		}, "split"))
		after := doc.Root().GetTree("t").ToXML()

		assert.NoError(t, doc.Undo())
		assert.Equal(t, before, doc.Root().GetTree("t").ToXML())

		assert.NoError(t, doc.Redo())
		assert.Equal(t, after, doc.Root().GetTree("t").ToXML())
	})

	t.Run("should undo editByPath merge", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "AB"}}},
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "CD"}}},
			}})
			return nil
		}, "init"))

		before := doc.Root().GetTree("t").ToXML()
		assert.Equal(t, "<doc><p>AB</p><p>CD</p></doc>", before)

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 2}, []int{1, 0}, nil, 0)
			return nil
		}, "merge"))
		after := doc.Root().GetTree("t").ToXML()
		assert.Equal(t, "<doc><p>ABCD</p></doc>", after)

		assert.NoError(t, doc.Undo())
		assert.Equal(t, before, doc.Root().GetTree("t").ToXML())
	})

	t.Run("should redo editByPath merge", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "AB"}}},
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "CD"}}},
			}})
			return nil
		}, "init"))

		before := doc.Root().GetTree("t").ToXML()
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 2}, []int{1, 0}, nil, 0)
			return nil
		}, "merge"))
		after := doc.Root().GetTree("t").ToXML()

		assert.NoError(t, doc.Undo())
		assert.Equal(t, before, doc.Root().GetTree("t").ToXML())

		assert.NoError(t, doc.Redo())
		assert.Equal(t, after, doc.Root().GetTree("t").ToXML())
	})
}

// TestHistoryTreeSplitL1UndoRedo ports history_tree_split_test.ts's "Tree
// History - single client split L1 undo/redo" describe block (:200-305): 9
// runtime instances, {front, middle, back} x {undo, undo-redo,
// undo-redo-undo}.
func TestHistoryTreeSplitL1UndoRedo(t *testing.T) {
	beforeXML := "<doc><p>ABCD</p></doc>"
	splitCases := []struct {
		pos      string
		splitIdx int
		afterXML string
	}{
		{"front", 1, "<doc><p></p><p>ABCD</p></doc>"},
		{"middle", 3, "<doc><p>AB</p><p>CD</p></doc>"},
		{"back", 5, "<doc><p>ABCD</p><p></p></doc>"},
	}

	newDoc := func(t *testing.T) *document.Document {
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "ABCD"}},
			}}})
			return nil
		}, "init"))
		return doc
	}

	for _, tc := range splitCases {
		tc := tc

		t.Run(fmt.Sprintf("should undo split at %s", tc.pos), func(t *testing.T) {
			doc := newDoc(t)
			assert.Equal(t, beforeXML, doc.Root().GetTree("t").ToXML())

			assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetTree("t").Edit(tc.splitIdx, tc.splitIdx, nil, 1)
				return nil
			}, fmt.Sprintf("split at %s", tc.pos)))
			assert.Equal(t, tc.afterXML, doc.Root().GetTree("t").ToXML())

			assert.NoError(t, doc.Undo())
			assert.Equal(t, beforeXML, doc.Root().GetTree("t").ToXML(), fmt.Sprintf("undo split at %s failed", tc.pos))
		})

		t.Run(fmt.Sprintf("should undo-redo split at %s", tc.pos), func(t *testing.T) {
			doc := newDoc(t)

			assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetTree("t").Edit(tc.splitIdx, tc.splitIdx, nil, 1)
				return nil
			}, fmt.Sprintf("split at %s", tc.pos)))

			assert.NoError(t, doc.Undo())
			assert.Equal(t, beforeXML, doc.Root().GetTree("t").ToXML())

			assert.NoError(t, doc.Redo())
			assert.Equal(t, tc.afterXML, doc.Root().GetTree("t").ToXML(), fmt.Sprintf("redo split at %s failed", tc.pos))
		})

		t.Run(fmt.Sprintf("should undo-redo-undo split at %s", tc.pos), func(t *testing.T) {
			doc := newDoc(t)

			assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetTree("t").Edit(tc.splitIdx, tc.splitIdx, nil, 1)
				return nil
			}, fmt.Sprintf("split at %s", tc.pos)))

			assert.NoError(t, doc.Undo())
			assert.NoError(t, doc.Redo())
			assert.NoError(t, doc.Undo())
			assert.Equal(t, beforeXML, doc.Root().GetTree("t").ToXML(),
				fmt.Sprintf("undo-redo-undo split at %s failed", tc.pos))
		})
	}
}

// TestHistoryTreeSplitL1ChainedOps ports history_tree_split_test.ts's "Tree
// History - single client split L1 chained ops" describe block (:307-368):
// 9 runtime instances, the Cartesian product of {split, insert-text,
// delete-text}^2.
func TestHistoryTreeSplitL1ChainedOps(t *testing.T) {
	chainOps := []string{"split", "insert-text", "delete-text"}

	applyChainOp := func(t *testing.T, doc *document.Document, op string) {
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			switch op {
			case "split":
				// Split first <p> at offset 2 (between 2nd and 3rd char).
				root.GetTree("t").EditByPath([]int{0, 2}, []int{0, 2}, nil, 1)
			case "insert-text":
				// Insert 'X' at start of first <p>.
				root.GetTree("t").EditByPath([]int{0, 0}, []int{0, 0}, &json.TreeNode{Type: "text", Value: "X"}, 0)
			case "delete-text":
				// Delete first char of first <p>.
				root.GetTree("t").Edit(1, 2, nil, 0)
			}
			return nil
		}, op))
	}

	for _, op1 := range chainOps {
		for _, op2 := range chainOps {
			op1, op2 := op1, op2

			t.Run(fmt.Sprintf("should undo chain: %s -> %s", op1, op2), func(t *testing.T) {
				doc := document.New(helper.TestKey(t))
				assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
					root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{{
						Type:     "p",
						Children: []json.TreeNode{{Type: "text", Value: "ABCD"}},
					}}})
					return nil
				}, "init"))

				s0 := doc.Root().GetTree("t").ToXML()
				applyChainOp(t, doc, op1)
				s1 := doc.Root().GetTree("t").ToXML()
				applyChainOp(t, doc, op2)
				s2 := doc.Root().GetTree("t").ToXML()

				// Undo: s2 -> s1 -> s0
				assert.NoError(t, doc.Undo())
				assert.Equal(t, s1, doc.Root().GetTree("t").ToXML(), fmt.Sprintf("undo %s failed", op2))
				assert.NoError(t, doc.Undo())
				assert.Equal(t, s0, doc.Root().GetTree("t").ToXML(), fmt.Sprintf("undo %s failed", op1))

				// Redo: s0 -> s1 -> s2
				assert.NoError(t, doc.Redo())
				assert.Equal(t, s1, doc.Root().GetTree("t").ToXML(), fmt.Sprintf("redo %s failed", op1))
				assert.NoError(t, doc.Redo())
				assert.Equal(t, s2, doc.Root().GetTree("t").ToXML(), fmt.Sprintf("redo %s failed", op2))
			})
		}
	}
}

// TestHistoryTreeSplitL1MultiClientConvergence ports
// history_tree_split_test.ts's "Tree History - multi client split L1
// convergence" describe block (:370-505): 9 runtime instances,
// {insert-text, delete-text, insert-element} x {before-split, after-split,
// different-element}.
func TestHistoryTreeSplitL1MultiClientConvergence(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	remoteOps := []string{"insert-text", "delete-text", "insert-element"}
	remotePositions := []string{"before-split", "after-split", "different-element"}

	applyRemoteOp := func(root *json.Object, op, pos string) {
		switch op {
		case "insert-text":
			switch pos {
			case "before-split":
				root.GetTree("t").Edit(1, 1, &json.TreeNode{Type: "text", Value: "X"}, 0)
			case "after-split":
				root.GetTree("t").Edit(5, 5, &json.TreeNode{Type: "text", Value: "X"}, 0)
			case "different-element":
				root.GetTree("t").Edit(7, 7, &json.TreeNode{Type: "text", Value: "X"}, 0)
			}
		case "delete-text":
			switch pos {
			case "before-split":
				root.GetTree("t").Edit(1, 2, nil, 0)
			case "after-split":
				root.GetTree("t").Edit(4, 5, nil, 0)
			case "different-element":
				root.GetTree("t").Edit(7, 8, nil, 0)
			}
		case "insert-element":
			node := &json.TreeNode{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "NEW"}}}
			switch pos {
			case "before-split":
				root.GetTree("t").Edit(0, 0, node, 0)
			case "after-split":
				root.GetTree("t").Edit(6, 6, node, 0)
			case "different-element":
				root.GetTree("t").Edit(12, 12, node, 0)
			}
		}
	}

	for _, remoteOp := range remoteOps {
		for _, remotePos := range remotePositions {
			remoteOp, remotePos := remoteOp, remotePos

			t.Run(fmt.Sprintf("should converge: split + remote %s at %s", remoteOp, remotePos), func(t *testing.T) {
				ctx := context.Background()

				d1 := document.New(helper.TestKey(t))
				assert.NoError(t, c1.Attach(ctx, d1))
				defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
				d2 := document.New(helper.TestKey(t))
				assert.NoError(t, c2.Attach(ctx, d2))
				defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

				assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
					root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
						{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ABCD"}}},
						{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "EFGH"}}},
					}})
					return nil
				}, "init"))
				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))

				// d1: split first <p> at middle (between B and C).
				assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
					root.GetTree("t").Edit(3, 3, nil, 1)
					return nil
				}, "split"))

				// d2: remote operation.
				assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
					applyRemoteOp(root, remoteOp, remotePos)
					return nil
				}, fmt.Sprintf("remote %s at %s", remoteOp, remotePos)))

				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))
				assert.NoError(t, c1.Sync(ctx))

				// d1: undo the split.
				assert.NoError(t, d1.Undo())

				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))
				assert.NoError(t, c1.Sync(ctx))

				assert.Equal(t, d1.Root().GetTree("t").ToXML(), d2.Root().GetTree("t").ToXML(),
					fmt.Sprintf("divergence: split + %s at %s", remoteOp, remotePos))
			})
		}
	}
}

// TestHistoryTreeSplitL1EdgeCases ports history_tree_split_test.ts's "Tree
// History - split L1 edge cases" describe block (:507-641): 4 runtime
// instances.
func TestHistoryTreeSplitL1EdgeCases(t *testing.T) {
	t.Run("should undo front split with empty paragraph", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "AB"}},
			}}})
			return nil
		}, "init"))
		before := doc.Root().GetTree("t").ToXML()

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 1, nil, 1)
			return nil
		}, "front split"))
		assert.Equal(t, "<doc><p></p><p>AB</p></doc>", doc.Root().GetTree("t").ToXML())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, before, doc.Root().GetTree("t").ToXML())

		assert.NoError(t, doc.Redo())
		assert.Equal(t, "<doc><p></p><p>AB</p></doc>", doc.Root().GetTree("t").ToXML())
	})

	t.Run("should undo back split with empty paragraph", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "AB"}},
			}}})
			return nil
		}, "init"))
		before := doc.Root().GetTree("t").ToXML()

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(3, 3, nil, 1)
			return nil
		}, "back split"))
		assert.Equal(t, "<doc><p>AB</p><p></p></doc>", doc.Root().GetTree("t").ToXML())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, before, doc.Root().GetTree("t").ToXML())

		assert.NoError(t, doc.Redo())
		assert.Equal(t, "<doc><p>AB</p><p></p></doc>", doc.Root().GetTree("t").ToXML())
	})

	t.Run("should clear redo stack when new edit is made after split undo", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "ABCD"}},
			}}})
			return nil
		}, "init"))

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(3, 3, nil, 1)
			return nil
		}, "split"))

		assert.NoError(t, doc.Undo())
		assert.True(t, doc.CanRedo())

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 1, &json.TreeNode{Type: "text", Value: "Z"}, 0)
			return nil
		}, "new edit"))
		assert.False(t, doc.CanRedo())
	})

	t.Run("should handle undo after concurrent parent deletion (L1)", func(t *testing.T) {
		clients := activeClients(t, 2)
		c1, c2 := clients[0], clients[1]
		defer deactivateAndCloseClients(t, clients)

		ctx := context.Background()

		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		d2 := document.New(helper.TestKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ABCD"}}},
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "EFGH"}}},
			}})
			return nil
		}, "init"))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))

		// d1: split first <p> at middle.
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(3, 3, nil, 1)
			return nil
		}, "split"))

		// d2: delete the first <p> entirely.
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(0, 6, nil, 0)
			return nil
		}, "delete parent"))

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))

		// d1: undo the split -- parent is deleted, should be no-op.
		assert.NoError(t, d1.Undo())

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))

		assert.Equal(t, d1.Root().GetTree("t").ToXML(), d2.Root().GetTree("t").ToXML(),
			"divergence after undo with concurrent parent deletion (L1)")
	})
}

// TestHistoryTreeSplitL2UndoRedo ports history_tree_split_test.ts's "Tree
// History - single client split L2 undo/redo" describe block (:643-766): 9
// runtime instances, {front, middle, back} x {undo, undo-redo,
// undo-redo-undo}.
func TestHistoryTreeSplitL2UndoRedo(t *testing.T) {
	beforeXML := "<doc><div><p>ABCD</p></div></doc>"
	l2SplitCases := []struct {
		pos      string
		splitIdx int
		afterXML string
	}{
		{"front", 2, "<doc><div><p></p></div><div><p>ABCD</p></div></doc>"},
		{"middle", 4, "<doc><div><p>AB</p></div><div><p>CD</p></div></doc>"},
		{"back", 6, "<doc><div><p>ABCD</p></div><div><p></p></div></doc>"},
	}

	newDoc := func(t *testing.T) *document.Document {
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{{
				Type: "div",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "ABCD"}},
				}},
			}}})
			return nil
		}, "init"))
		return doc
	}

	for _, tc := range l2SplitCases {
		tc := tc

		t.Run(fmt.Sprintf("should undo split at %s", tc.pos), func(t *testing.T) {
			doc := newDoc(t)
			assert.Equal(t, beforeXML, doc.Root().GetTree("t").ToXML())

			assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetTree("t").Edit(tc.splitIdx, tc.splitIdx, nil, 2)
				return nil
			}, fmt.Sprintf("split at %s", tc.pos)))
			assert.Equal(t, tc.afterXML, doc.Root().GetTree("t").ToXML())

			assert.NoError(t, doc.Undo())
			assert.Equal(t, beforeXML, doc.Root().GetTree("t").ToXML(), fmt.Sprintf("undo split at %s failed", tc.pos))
		})

		t.Run(fmt.Sprintf("should undo-redo split at %s", tc.pos), func(t *testing.T) {
			doc := newDoc(t)

			assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetTree("t").Edit(tc.splitIdx, tc.splitIdx, nil, 2)
				return nil
			}, fmt.Sprintf("split at %s", tc.pos)))

			assert.NoError(t, doc.Undo())
			assert.Equal(t, beforeXML, doc.Root().GetTree("t").ToXML())

			assert.NoError(t, doc.Redo())
			assert.Equal(t, tc.afterXML, doc.Root().GetTree("t").ToXML(), fmt.Sprintf("redo split at %s failed", tc.pos))
		})

		t.Run(fmt.Sprintf("should undo-redo-undo split at %s", tc.pos), func(t *testing.T) {
			doc := newDoc(t)

			assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetTree("t").Edit(tc.splitIdx, tc.splitIdx, nil, 2)
				return nil
			}, fmt.Sprintf("split at %s", tc.pos)))

			assert.NoError(t, doc.Undo())
			assert.NoError(t, doc.Redo())
			assert.NoError(t, doc.Undo())
			assert.Equal(t, beforeXML, doc.Root().GetTree("t").ToXML(),
				fmt.Sprintf("undo-redo-undo split at %s failed", tc.pos))
		})
	}
}

// TestHistoryTreeSplitL2ChainedOps ports history_tree_split_test.ts's "Tree
// History - single client split L2 chained ops" describe block (:768-842):
// 9 runtime instances, the Cartesian product of {split-l2, insert-text,
// delete-text}^2. One instance (split-l2 -> split-l2) is skipped, carrying
// over the JS suite's own skip.
func TestHistoryTreeSplitL2ChainedOps(t *testing.T) {
	chainOps := []string{"split-l2", "insert-text", "delete-text"}

	applyChainOp := func(t *testing.T, doc *document.Document, op string) {
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			switch op {
			case "split-l2":
				// Split first <p> at offset 2 with splitLevel=2.
				root.GetTree("t").EditByPath([]int{0, 0, 2}, []int{0, 0, 2}, nil, 2)
			case "insert-text":
				// Insert 'X' at start of first <p>.
				root.GetTree("t").EditByPath([]int{0, 0, 0}, []int{0, 0, 0}, &json.TreeNode{Type: "text", Value: "X"}, 0)
			case "delete-text":
				// Delete first char of first text in first <div><p>.
				root.GetTree("t").EditByPath([]int{0, 0, 0}, []int{0, 0, 1}, nil, 0)
			}
			return nil
		}, op))
	}

	for _, op1 := range chainOps {
		for _, op2 := range chainOps {
			op1, op2 := op1, op2

			t.Run(fmt.Sprintf("should undo chain: %s -> %s", op1, op2), func(t *testing.T) {
				if op1 == "split-l2" && op2 == "split-l2" {
					// Carries over history_tree_split_test.ts:798-802
					// (TODO(#1235)): two consecutive splitLevel=2 splits
					// produce a tombstoned structure the boundary-deletion
					// reverse does not correctly restore on undo. Still
					// unfixed in the JS SDK at v0.7.16, so this port skips
					// the same case rather than diverge from JS.
					t.Skip("known bug: split-l2 -> split-l2 undo chain does not restore correctly " +
						"when two consecutive splitLevel=2 splits produce tombstoned structure (still present in yorkie-js-sdk)")
				}

				doc := document.New(helper.TestKey(t))
				assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
					root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{{
						Type: "div",
						Children: []json.TreeNode{{
							Type:     "p",
							Children: []json.TreeNode{{Type: "text", Value: "ABCD"}},
						}},
					}}})
					return nil
				}, "init"))

				s0 := doc.Root().GetTree("t").ToXML()
				applyChainOp(t, doc, op1)
				s1 := doc.Root().GetTree("t").ToXML()
				applyChainOp(t, doc, op2)
				s2 := doc.Root().GetTree("t").ToXML()

				// Undo: s2 -> s1 -> s0
				assert.NoError(t, doc.Undo())
				assert.Equal(t, s1, doc.Root().GetTree("t").ToXML(), fmt.Sprintf("undo %s failed", op2))
				assert.NoError(t, doc.Undo())
				assert.Equal(t, s0, doc.Root().GetTree("t").ToXML(), fmt.Sprintf("undo %s failed", op1))

				// Redo: s0 -> s1 -> s2
				assert.NoError(t, doc.Redo())
				assert.Equal(t, s1, doc.Root().GetTree("t").ToXML(), fmt.Sprintf("redo %s failed", op1))
				assert.NoError(t, doc.Redo())
				assert.Equal(t, s2, doc.Root().GetTree("t").ToXML(), fmt.Sprintf("redo %s failed", op2))
			})
		}
	}
}

// TestHistoryTreeSplitL2MultiClientConvergence ports
// history_tree_split_test.ts's "Tree History - multi client split L2
// convergence" describe block (:844-1079): 18 runtime instances --
// {insert-text, delete-text, insert-element} x {before-split, after-split,
// different-element} x {undo, undo-then-redo}.
func TestHistoryTreeSplitL2MultiClientConvergence(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	remoteOps := []string{"insert-text", "delete-text", "insert-element"}
	remotePositions := []string{"before-split", "after-split", "different-element"}

	initSplitL2Pair := func(root *json.Object) {
		root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
			{Type: "div", Children: []json.TreeNode{
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ABCD"}}},
			}},
			{Type: "div", Children: []json.TreeNode{
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "EFGH"}}},
			}},
		}})
	}

	applyRemoteOp := func(root *json.Object, op, pos string) {
		switch op {
		case "insert-text":
			switch pos {
			case "before-split":
				root.GetTree("t").Edit(3, 3, &json.TreeNode{Type: "text", Value: "X"}, 0)
			case "after-split":
				root.GetTree("t").Edit(6, 6, &json.TreeNode{Type: "text", Value: "X"}, 0)
			case "different-element":
				root.GetTree("t").Edit(11, 11, &json.TreeNode{Type: "text", Value: "X"}, 0)
			}
		case "delete-text":
			switch pos {
			case "before-split":
				root.GetTree("t").Edit(2, 3, nil, 0)
			case "after-split":
				root.GetTree("t").Edit(5, 6, nil, 0)
			case "different-element":
				root.GetTree("t").Edit(10, 11, nil, 0)
			}
		case "insert-element":
			node := &json.TreeNode{Type: "div", Children: []json.TreeNode{
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "NEW"}}},
			}}
			switch pos {
			case "before-split":
				root.GetTree("t").Edit(0, 0, node, 0)
			case "after-split":
				root.GetTree("t").Edit(8, 8, node, 0)
			case "different-element":
				root.GetTree("t").Edit(16, 16, node, 0)
			}
		}
	}

	for _, remoteOp := range remoteOps {
		for _, remotePos := range remotePositions {
			remoteOp, remotePos := remoteOp, remotePos

			t.Run(fmt.Sprintf("should converge: split L2 + remote %s at %s", remoteOp, remotePos), func(t *testing.T) {
				ctx := context.Background()

				d1 := document.New(helper.TestKey(t))
				assert.NoError(t, c1.Attach(ctx, d1))
				defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
				d2 := document.New(helper.TestKey(t))
				assert.NoError(t, c2.Attach(ctx, d2))
				defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

				assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
					initSplitL2Pair(root)
					return nil
				}, "init"))
				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))

				// d1: split first <div><p> at middle (between B and C).
				assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
					root.GetTree("t").Edit(4, 4, nil, 2)
					return nil
				}, "split"))

				// d2: remote operation.
				assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
					applyRemoteOp(root, remoteOp, remotePos)
					return nil
				}, fmt.Sprintf("remote %s at %s", remoteOp, remotePos)))

				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))
				assert.NoError(t, c1.Sync(ctx))

				// d1: undo the split.
				assert.NoError(t, d1.Undo())

				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))
				assert.NoError(t, c1.Sync(ctx))

				assert.Equal(t, d1.Root().GetTree("t").ToXML(), d2.Root().GetTree("t").ToXML(),
					fmt.Sprintf("divergence: split L2 + %s at %s", remoteOp, remotePos))
			})
		}
	}

	for _, remoteOp := range remoteOps {
		for _, remotePos := range remotePositions {
			remoteOp, remotePos := remoteOp, remotePos

			t.Run(fmt.Sprintf("should converge after redo: split L2 + remote %s at %s", remoteOp, remotePos), func(t *testing.T) {
				ctx := context.Background()

				d1 := document.New(helper.TestKey(t))
				assert.NoError(t, c1.Attach(ctx, d1))
				defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
				d2 := document.New(helper.TestKey(t))
				assert.NoError(t, c2.Attach(ctx, d2))
				defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

				assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
					initSplitL2Pair(root)
					return nil
				}, "init"))
				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))

				// d1: split first <div><p> at middle (between B and C).
				assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
					root.GetTree("t").Edit(4, 4, nil, 2)
					return nil
				}, "split"))

				// d2: remote operation.
				assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
					applyRemoteOp(root, remoteOp, remotePos)
					return nil
				}, fmt.Sprintf("remote %s at %s", remoteOp, remotePos)))

				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))
				assert.NoError(t, c1.Sync(ctx))

				// d1: undo then redo.
				assert.NoError(t, d1.Undo())
				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))
				assert.NoError(t, c1.Sync(ctx))

				assert.NoError(t, d1.Redo())
				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))
				assert.NoError(t, c1.Sync(ctx))

				assert.Equal(t, d1.Root().GetTree("t").ToXML(), d2.Root().GetTree("t").ToXML(),
					fmt.Sprintf("redo divergence: split L2 + %s at %s", remoteOp, remotePos))
			})
		}
	}
}

// TestHistoryTreeSplitL2EdgeCases ports history_tree_split_test.ts's "Tree
// History - multi client split L2 edge cases" describe block (:1081-1251):
// 3 runtime instances.
func TestHistoryTreeSplitL2EdgeCases(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	t.Run("should converge: undo L2 front split with remote insert", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		d2 := document.New(helper.TestKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{{
				Type: "div",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "AB"}},
				}},
			}}})
			return nil
		}, "init"))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))

		// d1: front split -> <doc><div><p></p></div><div><p>AB</p></div></doc>
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 2, nil, 2)
			return nil
		}, "front split"))

		// d2: insert text in the same element.
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(3, 3, &json.TreeNode{Type: "text", Value: "X"}, 0)
			return nil
		}, "insert X"))

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))

		// d1: undo the front split.
		assert.NoError(t, d1.Undo())

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))

		assert.Equal(t, d1.Root().GetTree("t").ToXML(), d2.Root().GetTree("t").ToXML(),
			"divergence: undo front L2 split with remote insert")
	})

	t.Run("should converge: undo L2 back split with remote insert", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		d2 := document.New(helper.TestKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{{
				Type: "div",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "AB"}},
				}},
			}}})
			return nil
		}, "init"))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))

		// d1: back split -> <doc><div><p>AB</p></div><div><p></p></div></doc>
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(4, 4, nil, 2)
			return nil
		}, "back split"))

		// d2: insert text in the same element.
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 2, &json.TreeNode{Type: "text", Value: "X"}, 0)
			return nil
		}, "insert X"))

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))

		// d1: undo the back split.
		assert.NoError(t, d1.Undo())

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))

		assert.Equal(t, d1.Root().GetTree("t").ToXML(), d2.Root().GetTree("t").ToXML(),
			"divergence: undo back L2 split with remote insert")
	})

	t.Run("should handle undo after concurrent parent deletion (L2)", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		d2 := document.New(helper.TestKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
				{Type: "div", Children: []json.TreeNode{
					{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ABCD"}}},
				}},
				{Type: "div", Children: []json.TreeNode{
					{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "EFGH"}}},
				}},
			}})
			return nil
		}, "init"))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))

		// d1: split first <div><p> at middle with splitLevel=2.
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(4, 4, nil, 2)
			return nil
		}, "split"))

		// d2: delete the first <div> entirely. <div><p>ABCD</p></div> spans
		// index 0-8.
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(0, 8, nil, 0)
			return nil
		}, "delete parent"))

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))

		// d1: undo the split -- parent is deleted, should be no-op.
		assert.NoError(t, d1.Undo())

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))

		assert.Equal(t, d1.Root().GetTree("t").ToXML(), d2.Root().GetTree("t").ToXML(),
			"divergence after undo with concurrent parent deletion (L2)")
	})
}

// TestHistoryTreeSplitReverseOpPreTombstonedFilter ports
// history_tree_split_test.ts's "Tree History - single client reverseOp
// pre-tombstoned filter" describe block (:1261-1442): 5 runtime instances.
//
// A reverse op used to carry a deep copy of the affected subtree; reverse
// ops now address nodes by identity instead (RestoreSpans /
// RetombstoneSpans), so these regression cases inspect the span payload
// directly via the redo stack rather than a rendered copy.
func TestHistoryTreeSplitReverseOpPreTombstonedFilter(t *testing.T) {
	t.Run("should not accumulate reverseOp payload across redo cycles", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		initSplitReproDoc(t, doc)
		insertSplitSiblingBlock(t, doc)

		const numCycles = 4
		var fingerprints [][]string

		for cycle := 0; cycle < numCycles; cycle++ {
			// Type "asdf" in the inserted block.
			for _, ch := range "asdf" {
				typeInSplitSecondBlock(t, doc, string(ch))
			}

			// Undo each char. These four text nodes are now tombstoned, so
			// the block-insert's reverse op must not mention them.
			for i := 0; i < 4; i++ {
				assert.NoError(t, doc.Undo())
			}

			// Undo the block-insert. After this, the redo stack's top is
			// the op that the next Redo() will execute. Its span payload
			// is what goes over the wire in production.
			assert.NoError(t, doc.Undo())

			redoTop := splitTestTopRedoTreeEdit(doc)
			fingerprints = append(fingerprints, splitTestFingerprint(redoTop))

			// Now actually redo for the next cycle's setup.
			assert.NoError(t, doc.Redo())
		}

		// Non-zero matters as much as constant: an all-empty payload would
		// satisfy "identical across cycles" while carrying nothing at all.
		assert.Greater(t, len(fingerprints[0]), 0)
		for i := 1; i < numCycles; i++ {
			assert.Equal(t, fingerprints[0], fingerprints[i], fmt.Sprintf("cycle %d fingerprint diverged", i))
		}
	})

	t.Run("should produce a reverse payload that omits pre-tombstoned nodes", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		initSplitReproDoc(t, doc)
		insertSplitSiblingBlock(t, doc)

		for _, ch := range "asdf" {
			typeInSplitSecondBlock(t, doc, string(ch))
		}
		for i := 0; i < 4; i++ {
			assert.NoError(t, doc.Undo())
		}
		assert.NoError(t, doc.Undo())

		redoTop := splitTestTopRedoTreeEdit(doc)
		assert.NotNil(t, redoTop)
		spans := splitTestReversePayload(redoTop)
		assert.Greater(t, len(spans), 0)

		// The reversed edit inserted <p><inline></inline></p> and nothing
		// else. The four characters were tombstoned by their own undos
		// beforehand, so including them here would be the accumulation
		// bug this suite guards.
		var nodeTypes []string
		hasText := false
		for _, s := range spans {
			nodeTypes = append(nodeTypes, s.NodeType)
			if s.IsText {
				hasText = true
			}
		}
		assert.Equal(t, []string{"p", "inline"}, nodeTypes)
		assert.False(t, hasText)

		var violations []string
		seen := map[string]bool{}
		for i, span := range spans {
			key := treeNodeIDString(span.ID)

			// A repeated identity means the same node was collected twice.
			if seen[key] {
				violations = append(violations, fmt.Sprintf("%s: duplicate id %s", span.NodeType, key))
			}
			seen[key] = true

			// Every span needs a parent anchor; without one the server's
			// recreateFromSpan silently skips the node (the B1 rule).
			if span.ParentID == nil {
				violations = append(violations, fmt.Sprintf("%s: missing parentID", span.NodeType))
			}

			// Parent before child, which is what CRDTTree.restore requires
			// in order to rebuild a purged subtree top-down.
			parentKey := treeNodeIDString(span.ParentID)
			parentIdx := -1
			for j, s2 := range spans {
				if treeNodeIDString(s2.ID) == parentKey {
					parentIdx = j
					break
				}
			}
			if parentIdx > i {
				violations = append(violations, fmt.Sprintf("%s: parent span comes after the child", span.NodeType))
			}

			// Length is recorded in UTF-16 code units, and restore()/
			// retombstone() use it as a string index boundary: byte length
			// would disagree on anything outside the BMP.
			expected := 0
			if span.IsText {
				expected = len(utf16.Encode([]rune(span.Value)))
			}
			if span.Length != expected {
				violations = append(violations, fmt.Sprintf("%s: length=%d expected=%d", span.NodeType, span.Length, expected))
			}
		}
		assert.Empty(t, violations)
	})

	// The span length above is only exercised by text spans, and every
	// text node in the scenario above is pre-tombstoned out of the
	// payload. Undoing a surrogate-pair insert is the case where code
	// points and code units diverge, so it is the one that pins the unit.
	t.Run("should record text span length in UTF-16 code units", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		initSplitReproDoc(t, doc)
		insertSplitSiblingBlock(t, doc)

		// U+20BB7, a single code point that occupies two UTF-16 code
		// units.
		const astral = "\U00020bb7"
		assert.Equal(t, 1, utf8.RuneCountInString(astral))
		assert.Equal(t, 2, len(utf16.Encode([]rune(astral))))

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{1, 0, 0}, []int{1, 0, 0}, &json.TreeNode{Type: "text", Value: astral}, 0)
			return nil
		}, "type-astral"))
		assert.NoError(t, doc.Undo())

		spans := splitTestReversePayload(splitTestTopRedoTreeEdit(doc))
		var textSpans []*crdt.TreeRestoreSpan
		for _, s := range spans {
			if s.IsText {
				textSpans = append(textSpans, s)
			}
		}
		assert.Len(t, textSpans, 1)
		assert.Equal(t, astral, textSpans[0].Value)
		assert.Equal(t, 2, textSpans[0].Length)
	})

	t.Run("should allow typing at the correct position after redo", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		initSplitReproDoc(t, doc)
		insertSplitSiblingBlock(t, doc)

		for _, ch := range "asdf" {
			typeInSplitSecondBlock(t, doc, string(ch))
		}
		for i := 0; i < 4; i++ {
			assert.NoError(t, doc.Undo())
		}
		assert.NoError(t, doc.Undo())
		assert.Equal(t, "<doc><p><inline></inline></p></doc>", doc.Root().GetTree("t").ToXML())

		assert.NoError(t, doc.Redo())
		assert.Equal(t, "<doc><p><inline></inline></p><p><inline></inline></p></doc>", doc.Root().GetTree("t").ToXML())

		typeInSplitSecondBlock(t, doc, "x")
		assert.Equal(t, "<doc><p><inline></inline></p><p><inline>x</inline></p></doc>", doc.Root().GetTree("t").ToXML())
	})

	t.Run("should remain stable across three cycles followed by typing", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		initSplitReproDoc(t, doc)
		insertSplitSiblingBlock(t, doc)

		for cycle := 0; cycle < 3; cycle++ {
			for _, ch := range "asdf" {
				typeInSplitSecondBlock(t, doc, string(ch))
			}
			for i := 0; i < 4; i++ {
				assert.NoError(t, doc.Undo())
			}
			assert.NoError(t, doc.Undo())
			assert.NoError(t, doc.Redo())
			assert.Equal(t, "<doc><p><inline></inline></p><p><inline></inline></p></doc>", doc.Root().GetTree("t").ToXML())
		}

		typeInSplitSecondBlock(t, doc, "z")
		assert.Equal(t, "<doc><p><inline></inline></p><p><inline>z</inline></p></doc>", doc.Root().GetTree("t").ToXML())
	})
}

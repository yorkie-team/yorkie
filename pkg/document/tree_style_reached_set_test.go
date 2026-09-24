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

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// The set of nodes a tree Style reaches used to depend on which of two
// concurrent changes arrived first, because it was resolved in the receiving
// replica's visible-index space. Both replicas then rendered a different
// document, on live nodes, with no way back: neither side can retract a style
// and garbage collection does not touch live nodes.
//
// canStyle answers whether a node may be styled and is stable (#2011). These
// tests cover the other half: which nodes it is asked about.

// styleScanBase is the tree every case below starts from:
// <r><p>ab</p><p>cd</p><p>ef</p></r>, 12 wide inside the root.
func styleScanBase(t *testing.T) []*change.Change {
	t.Helper()

	seed := newActor(t, "000000000000000000000009")
	require.NoError(t, seed.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{
			Type: "r",
			Children: []json.TreeNode{
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}}},
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "cd"}}},
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ef"}}},
			},
		})
		return nil
	}))

	return grab(t, seed)
}

// styleReplayResult is everything two delivery orders have to agree on: what
// the document renders, and both halves of the size ledger. A style that
// lands on a tombstone shows up only in the second.
type styleReplayResult struct {
	xml  string
	size string
	err  string
}

// replayStyleOrder applies the batches in the given order onto a fresh
// replica of the base and reports what it ends up with.
func replayStyleOrder(t *testing.T, base []*change.Change, batches ...[]*change.Change) styleReplayResult {
	t.Helper()

	d := newActor(t, "00000000000000000000000a")
	feed(t, d, base)
	for _, batch := range batches {
		if err := d.ApplyChangePack(change.NewPack(
			"d", change.NewCheckpoint(0, 0), batch, time.InitialVersionVector, nil,
		)); err != nil {
			return styleReplayResult{err: err.Error()}
		}
	}

	return styleReplayResult{
		xml: d.Root().GetTree("t").ToXML(),
		size: fmt.Sprintf("live=%+v gc=%+v gcLen=%d",
			d.DocSize().Live, d.DocSize().GC, d.GarbageLen()),
	}
}

// concurrentTreeChanges generates one structural change and one style change
// from the same base, on fixed actors, and hands both back through the
// protobuf converter. Handing pointers over instead would let the receiver
// rewrite a version vector the other replay still has to read.
func concurrentTreeChanges(
	t *testing.T,
	base []*change.Change,
	structural, style func(tree *json.Tree),
) ([]*change.Change, []*change.Change) {
	t.Helper()

	docA := newActor(t, "000000000000000000000001")
	docB := newActor(t, "000000000000000000000002")
	feed(t, docA, base)
	feed(t, docB, base)

	require.NoError(t, docA.Update(func(root *json.Object, p *presence.Presence) error {
		structural(root.GetTree("t"))
		return nil
	}))
	require.NoError(t, docB.Update(func(root *json.Object, p *presence.Presence) error {
		style(root.GetTree("t"))
		return nil
	}))

	return grab(t, docA), grab(t, docB)
}

// A concurrent split of the paragraph a style covers. The style range ran
// past the paragraph's end before the split existed, so it styles the
// paragraph; the split then has to carry that onto both halves, whichever
// change the replica applies first.
func TestStyleAcrossConcurrentSplit(t *testing.T) {
	base := styleScanBase(t)
	pA, pB := concurrentTreeChanges(t, base,
		func(tree *json.Tree) { tree.Edit(6, 6, nil, 1) },
		func(tree *json.Tree) { tree.Style(5, 8, map[string]string{"b": "x"}) },
	)

	ab := replayStyleOrder(t, base, pA, pB)
	ba := replayStyleOrder(t, base, pB, pA)
	require.Equal(t, `<r><p>ab</p><p b="x">c</p><p b="x">d</p><p>ef</p></r>`, ba.xml)
	require.Equal(t, ba, ab, "split-then-style diverges from style-then-split")
}

// A concurrent merge of the two paragraphs a style spans. The style covered
// the first paragraph's End token and the second's Start token, so both carry
// it — the second as a tombstone, which only the GC half of the ledger shows.
func TestStyleAcrossConcurrentMerge(t *testing.T) {
	base := styleScanBase(t)
	pA, pB := concurrentTreeChanges(t, base,
		func(tree *json.Tree) { tree.Edit(1, 5, nil, 0) },
		func(tree *json.Tree) { tree.Style(1, 6, map[string]string{"b": "x"}) },
	)

	ab := replayStyleOrder(t, base, pA, pB)
	ba := replayStyleOrder(t, base, pB, pA)
	require.Equal(t, `<r><p b="x">cd</p><p>ef</p></r>`, ba.xml)
	require.Equal(t, ba, ab, "merge-then-style diverges from style-then-merge")
}

// styleScanDivergences replays every (structural change, style range) pair the
// caller generates in both orders and counts the pairs the two orders disagree
// on, split by whether the disagreement is visible in the rendered document.
func styleScanDivergences(
	t *testing.T,
	base []*change.Change,
	structural []func(tree *json.Tree),
) (rendered, tombstoneOnly int) {
	t.Helper()

	for _, edit := range structural {
		for from := 0; from <= 12; from++ {
			for to := from; to <= 12; to++ {
				pA, pB := concurrentTreeChanges(t, base, edit,
					func(tree *json.Tree) { tree.Style(from, to, map[string]string{"b": "x"}) },
				)
				ab := replayStyleOrder(t, base, pA, pB)
				ba := replayStyleOrder(t, base, pB, pA)
				switch {
				case ab.err != "" || ba.err != "":
					require.Equal(t, ba.err, ab.err)
				case ab.xml != ba.xml:
					rendered++
				case ab.size != ba.size:
					tombstoneOnly++
				}
			}
		}
	}

	return rendered, tombstoneOnly
}

// Every split position against every style range: 11 x 91 pairs. This family
// is closed — the style range's own end position says whether it ran past the
// paragraph's end, and the split lineage says which nodes that paragraph
// became.
func TestStyleAcrossEverySplit(t *testing.T) {
	base := styleScanBase(t)

	var splits []func(tree *json.Tree)
	for at := 1; at <= 11; at++ {
		splits = append(splits, func(tree *json.Tree) { tree.Edit(at, at, nil, 1) })
	}

	rendered, tombstoneOnly := styleScanDivergences(t, base, splits)
	require.Zero(t, rendered, "split x style diverges in the rendered document")
	require.Zero(t, tombstoneOnly, "split x style diverges on tombstoned attributes")
}

// Every deletion range against every style range: 78 x 91 pairs. This family
// is NOT closed: a merge moves children out of a parent, and the nodes a
// style covered strictly between its two anchors are then unreachable from
// the resolved range. Recovering those needs the range resolved in an index
// space filtered by the change's version vector, which has to land on the
// server and the JS SDK together — see docs/design/concurrent-merge-split.md.
//
// The budgets below are a ratchet, not a target. They were 297 and 2879
// before the change's own positions decided the reached set.
func TestStyleAcrossEveryMerge(t *testing.T) {
	base := styleScanBase(t)

	var merges []func(tree *json.Tree)
	for from := 0; from <= 12; from++ {
		for to := from + 1; to <= 12; to++ {
			merges = append(merges, func(tree *json.Tree) { tree.Edit(from, to, nil, 0) })
		}
	}

	rendered, tombstoneOnly := styleScanDivergences(t, base, merges)
	t.Logf("merge x style: rendered=%d tombstone-only=%d", rendered, tombstoneOnly)
	require.LessOrEqual(t, rendered, 126, "merge x style regressed in the rendered document")
	require.LessOrEqual(t, tombstoneOnly, 1292, "merge x style regressed on tombstoned attributes")
}

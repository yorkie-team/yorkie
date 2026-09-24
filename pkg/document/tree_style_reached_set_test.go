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
	"strings"
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

// The mirror of TestStyleAcrossConcurrentMerge: the merge removes the opening
// tag of the paragraph the style range STARTS inside, so that paragraph's
// children move into the one before it. The style covered the paragraph it
// named and nothing else, so after the merge it lands on a tombstone and
// nothing renders — the paragraph that absorbed the children is an element
// the styling replica never entered, and styling it would put an attribute on
// a live node the other order leaves alone.
func TestStyleAfterMergedRangeStart(t *testing.T) {
	base := styleScanBase(t)
	pA, pB := concurrentTreeChanges(t, base,
		func(tree *json.Tree) { tree.Edit(1, 5, nil, 0) },
		func(tree *json.Tree) { tree.Style(6, 8, map[string]string{"b": "x"}) },
	)

	ab := replayStyleOrder(t, base, pA, pB)
	ba := replayStyleOrder(t, base, pB, pA)
	require.Equal(t, `<r><p>cd</p><p>ef</p></r>`, ba.xml)
	require.Equal(t, ba, ab, "merge-then-style styles the merge target the other order does not")
}

// The same pair as a RemoveStyle, which is the counterexample the property
// suite reported and docs/design/concurrent-merge-split.md carried as a §9.4
// known limitation: the merge target kept its attribute in one order and lost
// it in the other, because the range start moved into it.
func TestRemoveStyleAfterMergedRangeStart(t *testing.T) {
	base := styleScanBoldBase(t)
	pA, pB := concurrentTreeChanges(t, base,
		func(tree *json.Tree) { tree.Edit(1, 5, nil, 0) },
		func(tree *json.Tree) { tree.RemoveStyle(6, 8, []string{"b"}) },
	)

	ab := replayStyleOrder(t, base, pA, pB)
	ba := replayStyleOrder(t, base, pB, pA)
	require.Equal(t, `<r><p b="x">cd</p><p b="x">ef</p></r>`, ba.xml)
	require.Equal(t, ba, ab, "merge-then-remove-style clears the merge target the other order keeps")
}

// styleScanBoldBase is styleScanBase with every paragraph already bold, so a
// RemoveStyle has something to take off.
func styleScanBoldBase(t *testing.T) []*change.Change {
	t.Helper()

	bold := func(text string) json.TreeNode {
		return json.TreeNode{
			Type:       "p",
			Attributes: map[string]string{"b": "x"},
			Children:   []json.TreeNode{{Type: "text", Value: text}},
		}
	}
	seed := newActor(t, "000000000000000000000009")
	require.NoError(t, seed.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{
			Type:     "r",
			Children: []json.TreeNode{bold("ab"), bold("cd"), bold("ef")},
		})
		return nil
	}))

	return grab(t, seed)
}

// RemoveStyle resolves its range exactly the way Style does, so it inherits
// the same order dependence. It shares the resolution now; this pins that.
func TestRemoveStyleAcrossConcurrentSplit(t *testing.T) {
	base := styleScanBoldBase(t)
	pA, pB := concurrentTreeChanges(t, base,
		func(tree *json.Tree) { tree.Edit(6, 6, nil, 1) },
		func(tree *json.Tree) { tree.RemoveStyle(5, 8, []string{"b"}) },
	)

	ab := replayStyleOrder(t, base, pA, pB)
	ba := replayStyleOrder(t, base, pB, pA)
	require.Equal(t, `<r><p b="x">ab</p><p>c</p><p>d</p><p b="x">ef</p></r>`, ba.xml)
	require.Equal(t, ba, ab, "split-then-remove-style diverges from the other order")
}

// styleScan is what a scan reports. Divergence between the two delivery
// orders is the defect this change is about, but two orders agreeing tells
// you nothing about WHICH nodes they agreed to style: an implementation that
// styles every node, or none, converges perfectly. So the scan also counts
// what was actually reached, and how many pairs it could not replay at all —
// a pair whose two orders fail identically is counted, never dropped in
// silence, because an implementation that errors everywhere would otherwise
// score zero divergence.
type styleScan struct {
	pairs int
	// rendered counts pairs whose two orders render different documents.
	rendered int
	// tombstoneOnly counts pairs that render alike but whose size ledgers
	// differ, i.e. attributes that landed on tombstones in one order only.
	tombstoneOnly int
	// errored counts pairs neither order could apply.
	errored int
	// styledNodes totals the styled elements over every converged pair, and
	// styledPairs counts the pairs that styled at least one. Together they
	// pin the reached set in absolute terms: narrowing it, widening it, or
	// emptying it moves one of them even though all three converge.
	styledNodes int
	styledPairs int
}

// styleScanDivergences replays every (structural change, style range) pair the
// caller generates in both orders and reports what the two orders did.
func styleScanDivergences(
	t *testing.T,
	base []*change.Change,
	structural []func(tree *json.Tree),
	style func(tree *json.Tree, from, to int),
	countStyled func(xml string) int,
) styleScan {
	t.Helper()

	var scan styleScan
	for _, edit := range structural {
		for from := 0; from <= 12; from++ {
			for to := from; to <= 12; to++ {
				scan.pairs++
				pA, pB := concurrentTreeChanges(t, base, edit,
					func(tree *json.Tree) { style(tree, from, to) },
				)
				ab := replayStyleOrder(t, base, pA, pB)
				ba := replayStyleOrder(t, base, pB, pA)
				switch {
				case ab.err != "" || ba.err != "":
					require.Equal(t, ba.err, ab.err)
					scan.errored++
				case ab.xml != ba.xml:
					scan.rendered++
				case ab.size != ba.size:
					scan.tombstoneOnly++
				}
				if ab.err == "" {
					styled := countStyled(ab.xml)
					scan.styledNodes += styled
					if styled > 0 {
						scan.styledPairs++
					}
				}
			}
		}
	}

	return scan
}

// styleRange is the Style the scans apply, and countBold counts what it left
// behind in a rendered document.
func styleRange(tree *json.Tree, from, to int) {
	tree.Style(from, to, map[string]string{"b": "x"})
}

func countBold(xml string) int {
	return strings.Count(xml, `b="x"`)
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

	scan := styleScanDivergences(t, base, splits, styleRange, countBold)
	require.Equal(t, 1001, scan.pairs)
	require.Zero(t, scan.rendered, "split x style diverges in the rendered document")
	require.Zero(t, scan.tombstoneOnly, "split x style diverges on tombstoned attributes")
	require.Zero(t, scan.errored, "split x style could not be replayed")
	// Absolute, not relative to the other order: converging on the wrong set
	// of nodes converges just as well as converging on the right one. 759 of
	// the 1001 (split position, style range) pairs style at least one
	// element, and 1862 elements carry the attribute across them — a range
	// that covered one paragraph reaches two once a split cut it in half.
	// Widening, narrowing or emptying the reached set moves these even
	// though all three converge, so update them deliberately, with the
	// reason.
	require.Equal(t, 759, scan.styledPairs, "split x style reached a different set of ranges")
	require.Equal(t, 1862, scan.styledNodes, "split x style reached a different set of nodes")
}

// mergeRanges is every deletion range over the scan base: 78 of them, each
// paired with every one of the 91 style ranges.
func mergeRanges() []func(tree *json.Tree) {
	var merges []func(tree *json.Tree)
	for from := 0; from <= 12; from++ {
		for to := from + 1; to <= 12; to++ {
			merges = append(merges, func(tree *json.Tree) { tree.Edit(from, to, nil, 0) })
		}
	}

	return merges
}

// Every deletion range against every style range: 78 x 91 pairs. No pair
// renders differently in the two delivery orders any more — the rendered
// family is closed for merges as it is for splits. It was the last one open
// that put attributes on LIVE nodes, which is the failure neither replica can
// retract: a merge moves the range-start anchor into the element that
// absorbed the children, and styling that element writes where the replica
// that styled first wrote nothing.
//
// What remains is attributes on TOMBSTONES, counted below and bounded as a
// ratchet: the two orders render the same document but book a different
// amount of attribute metadata onto removed nodes. Closing that needs the
// range resolved in an index space filtered by the change's version vector,
// which has to land on the server and the JS SDK together — see
// docs/design/concurrent-merge-split.md §9.5.
func TestStyleAcrossEveryMerge(t *testing.T) {
	base := styleScanBase(t)

	scan := styleScanDivergences(t, base, mergeRanges(), styleRange, countBold)
	t.Logf("merge x style: %+v", scan)
	require.Equal(t, 7098, scan.pairs)
	require.Zero(t, scan.rendered, "merge x style diverges in the rendered document")
	// The tombstone half is a ratchet, not a target: 2879 before the change's
	// own positions decided the reached set.
	require.LessOrEqual(t, scan.tombstoneOnly, 1292, "merge x style regressed on tombstoned attributes")
	// A pair neither order can apply is not a converged pair. Bounding them
	// keeps the ratchet above from being satisfied by a scan that mostly
	// failed to run.
	require.Zero(t, scan.errored, "merge x style could not be replayed")
	// The absolute half, for the same reason as the split scan: convergence
	// alone is blind to a reached set that is uniformly too small. These are
	// 54 pairs and 126 nodes below what they were while the rendered family
	// was open, which is exactly the over-reach that family consisted of —
	// the merge target the styling replica never touched.
	require.Equal(t, 4254, scan.styledPairs, "merge x style reached a different set of ranges")
	require.Equal(t, 6302, scan.styledNodes, "merge x style reached a different set of nodes")
}

// The merge scan with RemoveStyle in place of Style, over a pre-bolded base.
// Both operations share one range resolution, and the numbers are identical
// to TestStyleAcrossEveryMerge's on every count — which is what "shared"
// has to mean for a reached set.
func TestRemoveStyleAcrossEveryMerge(t *testing.T) {
	base := styleScanBoldBase(t)

	// A paragraph RemoveStyle reached renders as `<p>`; one it did not still
	// carries the attribute from the base.
	cleared := func(xml string) int { return strings.Count(xml, "<p>") }
	scan := styleScanDivergences(t, base, mergeRanges(),
		func(tree *json.Tree, from, to int) { tree.RemoveStyle(from, to, []string{"b"}) },
		cleared)
	t.Logf("merge x remove-style: %+v", scan)
	require.Equal(t, 7098, scan.pairs)
	require.Zero(t, scan.rendered, "merge x remove-style diverges in the rendered document")
	require.LessOrEqual(t, scan.tombstoneOnly, 1292, "merge x remove-style regressed on tombstoned attributes")
	require.Zero(t, scan.errored, "merge x remove-style could not be replayed")
	require.Equal(t, 4254, scan.styledPairs, "merge x remove-style reached a different set of ranges")
	require.Equal(t, 6302, scan.styledNodes, "merge x remove-style reached a different set of nodes")
}

// The same scan with RemoveStyle in place of Style, over a pre-bolded base.
// The two share one range resolution now, so the family has to be closed for
// both or the sharing is only nominal.
func TestRemoveStyleAcrossEverySplit(t *testing.T) {
	base := styleScanBoldBase(t)

	var splits []func(tree *json.Tree)
	for at := 1; at <= 11; at++ {
		splits = append(splits, func(tree *json.Tree) { tree.Edit(at, at, nil, 1) })
	}

	// A paragraph RemoveStyle reached renders as `<p>`; one it did not still
	// carries the attribute from the base.
	cleared := func(xml string) int { return strings.Count(xml, "<p>") }
	scan := styleScanDivergences(t, base, splits,
		func(tree *json.Tree, from, to int) { tree.RemoveStyle(from, to, []string{"b"}) },
		cleared)
	require.Equal(t, 1001, scan.pairs)
	require.Zero(t, scan.rendered, "split x remove-style diverges in the rendered document")
	require.Zero(t, scan.tombstoneOnly, "split x remove-style diverges on tombstoned attributes")
	require.Zero(t, scan.errored, "split x remove-style could not be replayed")
	require.Equal(t, 759, scan.styledPairs, "split x remove-style reached a different set of ranges")
	require.Equal(t, 1862, scan.styledNodes, "split x remove-style reached a different set of nodes")
}

// styleConvergence is a two-client concurrency case: one client's structural
// change against another's style, converging on one document whichever
// arrives first.
type styleConvergence struct {
	name string
	base json.TreeNode
	a    func(tree *json.Tree)
	b    func(tree *json.Tree)
	want string
}

// The §9.4 merge cases live in test/complex, which is gated behind a build
// tag and a path filter and so does not run on every change to this code.
// Their assertions are the ones that pin what a style may NOT reach — the
// interloper a merge moved next to the range end, the descendants of one —
// and the reached-set resolution they consume is what this file changes.
// Reproduced here without a server so CI runs them.
func TestStyleReachedSetMatchesComplexSuite(t *testing.T) {
	twoParagraphs := json.TreeNode{Type: "r", Children: []json.TreeNode{
		{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}}},
		{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "cd"}}},
	}}
	threeParagraphs := json.TreeNode{Type: "r", Children: []json.TreeNode{
		{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}}},
		{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "cd"}}},
		{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ef"}}},
	}}
	bold := map[string]string{"bold": "x"}

	cases := []styleConvergence{{
		// TestTreeConcurrencyStyleCoveringMergedContent
		name: "covering-merged-content",
		base: twoParagraphs,
		a:    func(tr *json.Tree) { tr.Edit(0, 4, nil, 0) },
		b:    func(tr *json.Tree) { tr.Style(4, 8, bold) },
		want: `<r><p bold="x">cd</p></r>`,
	}, {
		// TestTreeConcurrencyStyleAcrossChainedMerge
		name: "across-chained-merge",
		base: threeParagraphs,
		a:    func(tr *json.Tree) { tr.Edit(7, 9, nil, 0); tr.Edit(3, 5, nil, 0) },
		b: func(tr *json.Tree) {
			tr.Edit(12, 12, &json.TreeNode{Type: "p"}, 0)
			tr.Style(0, 9, bold)
		},
		want: `<r><p bold="x">abcdef</p><p></p></r>`,
	}, {
		// TestTreeConcurrencyStyleAfterMovedAnchor: the interloper inserted
		// at the merged anchor stays UNSTYLED.
		name: "after-moved-anchor",
		base: twoParagraphs,
		a:    func(tr *json.Tree) { tr.Edit(0, 5, nil, 0) },
		b: func(tr *json.Tree) {
			tr.Edit(8, 8, &json.TreeNode{Type: "p"}, 0)
			tr.Style(0, 6, bold)
		},
		want: `<r><p></p>cd</r>`,
	}, {
		// TestTreeConcurrencyStyleCoversOwnInsertIntoMergedRange
		name: "covers-own-insert-into-merged-range",
		base: twoParagraphs,
		a:    func(tr *json.Tree) { tr.Edit(0, 5, nil, 0) },
		b: func(tr *json.Tree) {
			tr.Edit(5, 5, &json.TreeNode{Type: "b"}, 0)
			tr.Style(0, 8, bold)
		},
		want: `<r><b bold="x"></b>cd</r>`,
	}, {
		// TestTreeConcurrencyStyleSiblingBeforeTombstone
		name: "sibling-before-tombstone",
		base: json.TreeNode{Type: "r", Children: []json.TreeNode{
			{Type: "b"},
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}}},
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "cd"}}},
		}},
		a:    func(tr *json.Tree) { tr.Edit(2, 7, nil, 0) },
		b:    func(tr *json.Tree) { tr.Style(0, 8, bold) },
		want: `<r><b bold="x"></b>cd</r>`,
	}, {
		// TestTreeConcurrencyStyleSkipsInterloperDescendants
		name: "skips-interloper-descendants",
		base: twoParagraphs,
		a:    func(tr *json.Tree) { tr.Edit(0, 5, nil, 0) },
		b: func(tr *json.Tree) {
			tr.Edit(8, 8, &json.TreeNode{Type: "p"}, 0)
			tr.Edit(9, 9, &json.TreeNode{Type: "b"}, 0)
			tr.Style(0, 6, bold)
		},
		want: `<r><p><b></b></p>cd</r>`,
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seed := newActor(t, "000000000000000000000009")
			require.NoError(t, seed.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewTree("t", tc.base)
				return nil
			}))
			base := grab(t, seed)

			pA, pB := concurrentTreeChanges(t, base, tc.a, tc.b)
			ab := replayStyleOrder(t, base, pA, pB)
			ba := replayStyleOrder(t, base, pB, pA)
			require.Empty(t, ab.err)
			require.Empty(t, ba.err)
			require.Equal(t, tc.want, ba.xml)
			// The rendered document only, which is what the complex suite
			// asserts. Five of these six still book a different number of
			// attributes onto tombstones depending on the order — the
			// tombstone-only half of the merge family's known limitation,
			// counted by TestStyleAcrossEveryMerge, not introduced here.
			require.Equal(t, ba.xml, ab.xml, "the two delivery orders render differently")
		})
	}
}

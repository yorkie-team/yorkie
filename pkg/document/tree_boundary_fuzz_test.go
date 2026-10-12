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
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// mulberry32 is the generator the JS port of this test uses, so seed N
// produces the same script in both SDKs.
type mulberry32 uint32

func (m *mulberry32) next() float64 {
	*m += 0x6D2B79F5
	z := uint32(*m)
	z = (z ^ z>>15) * (z | 1)
	z = (z + (z^z>>7)*(z|61)) ^ z
	return float64(z^z>>14) / 4294967296
}

// boundaryFuzz drives replicas through a server-ordered log: an op is pushed
// as soon as it is made, and a pull hands a replica the next entry it has not
// seen, skipping its own, so delivery is causal as Yorkie's server makes it.
type boundaryFuzz struct {
	t      *testing.T
	docs   []*document.Document
	log    []*change.Change
	origin []int
	cursor []int
	script []string
}

func (f *boundaryFuzz) push(r int) {
	pb, err := converter.ToChangePack(f.docs[r].CreateChangePack())
	require.NoError(f.t, err)
	pack, err := converter.FromChangePack(pb)
	require.NoError(f.t, err)
	for _, c := range pack.Changes {
		f.log = append(f.log, c)
		f.origin = append(f.origin, r)
	}
	if n := len(pack.Changes); n > 0 {
		require.NoError(f.t, f.docs[r].ApplyChangePack(change.NewPack(
			pack.DocumentKey, change.NewCheckpoint(0, pack.Changes[n-1].ClientSeq()), nil,
			time.InitialVersionVector, nil,
		)))
	}
}

func (f *boundaryFuzz) skipOwn(r int) {
	for f.cursor[r] < len(f.log) && f.origin[f.cursor[r]] == r {
		f.cursor[r]++
	}
}

func (f *boundaryFuzz) pull(r int) bool {
	f.skipOwn(r)
	if f.cursor[r] == len(f.log) {
		return false
	}
	c := f.log[f.cursor[r]]
	f.cursor[r]++
	require.NoError(f.t, f.docs[r].ApplyChangePack(change.NewPack(
		f.docs[r].Key(), change.NewCheckpoint(0, 0), []*change.Change{c}, time.InitialVersionVector, nil,
	)))
	return true
}

func (f *boundaryFuzz) caughtUp(r int) bool {
	f.skipOwn(r)
	return f.cursor[r] == len(f.log)
}

func (f *boundaryFuzz) collect() {
	vectors := make([]time.VersionVector, len(f.docs))
	for i, d := range f.docs {
		vectors[i] = d.VersionVector()
	}
	minVV := time.MinVersionVector(vectors...)
	for r, d := range f.docs {
		if f.caughtUp(r) {
			d.GarbageCollect(minVV)
		}
	}
}

// paragraphTexts returns the index where each live paragraph starts and its
// live text.
func (f *boundaryFuzz) paragraphTexts(r int) ([]int, []string) {
	var bases []int
	var texts []string
	base := 0
	for _, p := range paragraphs(treeCRDT(f.t, f.docs[r])) {
		if p.RemovedAt() != nil {
			continue
		}
		var b strings.Builder
		for _, c := range p.Children(true) {
			if c.RemovedAt() == nil && c.IsText() {
				b.WriteString(c.Value)
			}
		}
		bases = append(bases, base)
		texts = append(texts, b.String())
		base += b.Len() + 2
	}
	return bases, texts
}

// step makes one random op on replica r: an insert, a split, a delete inside
// a paragraph, or a pull.
func (f *boundaryFuzz) step(rng *mulberry32, r int, value string) {
	kind, x, y := rng.next(), rng.next(), rng.next()
	bases, texts := f.paragraphTexts(r)
	switch {
	case kind < 0.6:
		var slots []int
		for i, text := range texts {
			for k := 0; k <= len(text); k++ {
				slots = append(slots, bases[i]+1+k)
			}
		}
		if len(slots) == 0 {
			return
		}
		idx := slots[int(x*float64(len(slots)))]
		if kind < 0.35 {
			updateTree(f.t, f.docs[r], insertText(idx, value))
			f.script = append(f.script, fmt.Sprintf("d%d.insert(%d,%q)", r+1, idx, value))
		} else {
			updateTree(f.t, f.docs[r], splitAt(idx))
			f.script = append(f.script, fmt.Sprintf("d%d.split(%d)", r+1, idx))
		}
		f.push(r)
	case kind < 0.75:
		type span struct{ from, room int }
		var spans []span
		for i, text := range texts {
			for k := 0; k < len(text); k++ {
				spans = append(spans, span{bases[i] + 1 + k, min(3, len(text)-k)})
			}
		}
		if len(spans) == 0 {
			return
		}
		s := spans[int(x*float64(len(spans)))]
		to := s.from + 1 + int(y*float64(s.room))
		updateTree(f.t, f.docs[r], func(tree *json.Tree) { tree.Edit(s.from, to, nil, 0) })
		f.script = append(f.script, fmt.Sprintf("d%d.delete(%d,%d)", r+1, s.from, to))
		f.push(r)
	default:
		if f.pull(r) {
			f.script = append(f.script, fmt.Sprintf("d%d.pull", r+1))
		}
	}
}

// runBoundaryFuzz plays one seed and reports whether every replica ended in
// the same tree, live node identities and boundary records included.
func runBoundaryFuzz(t *testing.T, seed uint32, n, steps int) {
	t.Helper()

	f := &boundaryFuzz{t: t, docs: boundaryReplicas(t, n, "ab"), cursor: make([]int, n)}
	rng := mulberry32(seed)
	for i := 1; i <= steps; i++ {
		if i%3 == 0 {
			f.collect()
		}
		if r := i % n; i%7 == 0 && f.caughtUp(r) {
			f.docs[r] = reloadFromSnapshot(t, f.docs[r])
			f.script = append(f.script, fmt.Sprintf("d%d.reload", r+1))
		}
		r := int(rng.next() * float64(n))
		f.step(&rng, r, string(rune('c'+i%24)))
	}
	for progress := true; progress; {
		progress = false
		for r := range f.docs {
			for f.pull(r) {
				progress = true
			}
		}
	}
	f.collect()

	for r := 1; r < n; r++ {
		msg := strings.Join(f.script, "; ")
		require.Equal(t, treeXML(t, f.docs[0]), treeXML(t, f.docs[r]), msg)
		require.Equal(t, liveTreeShape(t, f.docs[0]), liveTreeShape(t, f.docs[r]), msg)
		require.Equal(t, boundaryRecords(t, f.docs[0]), boundaryRecords(t, f.docs[r]), msg)
	}
}

// TestTreeBoundaryFuzz runs concurrent inserts, splits and deletes on one
// paragraph across two and three replicas, with garbage collection and
// snapshot reloads along the way, and checks that every replica converges.
// It is the regression net for yorkie-js-sdk#1436; see
// docs/design/split-boundary-anchors.md.
func TestTreeBoundaryFuzz(t *testing.T) {
	for _, n := range []int{2, 3} {
		for seed := uint32(1); seed <= 300; seed++ {
			t.Run(fmt.Sprintf("%d replicas, seed %d", n, seed), func(t *testing.T) {
				runBoundaryFuzz(t, seed, n, 30)
			})
		}
	}
}

// replayBoundaryScript plays a script of the form the fuzz prints, such as
// "d1.split(1); d1.undo; d2.split(3)", on n fresh replicas with empty
// histories, delivers everything, collects garbage, and checks convergence.
func replayBoundaryScript(t *testing.T, n int, script string) {
	t.Helper()

	replayBoundaryScriptFrom(t, n, "ab", script)
}

// replayBoundaryScriptFrom is replayBoundaryScript on replicas that start
// with text in their paragraph.
func replayBoundaryScriptFrom(t *testing.T, n int, text, script string) {
	t.Helper()

	docs := playBoundaryScriptFrom(t, n, text, script)
	for r := 1; r < n; r++ {
		require.Equal(t, treeXML(t, docs[0]), treeXML(t, docs[r]), script)
		require.Equal(t, liveTreeShape(t, docs[0]), liveTreeShape(t, docs[r]), script)
		require.Equal(t, boundaryRecords(t, docs[0]), boundaryRecords(t, docs[r]), script)
	}
}

// playBoundaryScript plays script as replayBoundaryScript does and returns
// the replicas after everything is delivered and collected.
func playBoundaryScript(t *testing.T, n int, script string) []*document.Document {
	t.Helper()

	return playBoundaryScriptFrom(t, n, "ab", script)
}

// playBoundaryScriptFrom is playBoundaryScript on replicas that start with
// text in their paragraph.
func playBoundaryScriptFrom(t *testing.T, n int, text, script string) []*document.Document {
	t.Helper()

	f := &boundaryFuzz{t: t, docs: boundaryReplicas(t, n, text), cursor: make([]int, n)}
	for _, d := range f.docs {
		require.NoError(t, d.ClearHistory())
	}
	for _, tok := range strings.Split(script, "; ") {
		var r, a, b int
		var v string
		scan := func(format string, args ...any) {
			_, err := fmt.Sscanf(tok, format, args...)
			require.NoError(t, err, tok)
		}
		switch {
		case tok == "gc":
			f.collect()
		case strings.HasSuffix(tok, ".reload"):
			scan("d%d.reload", &r)
			f.docs[r-1] = reloadFromSnapshot(t, f.docs[r-1])
		case strings.HasSuffix(tok, ".pull"):
			scan("d%d.pull", &r)
			f.pull(r - 1)
		case strings.HasSuffix(tok, ".undo"):
			scan("d%d.undo", &r)
			require.NoError(t, f.docs[r-1].Undo(), tok)
			f.push(r - 1)
		case strings.HasSuffix(tok, ".redo"):
			scan("d%d.redo", &r)
			require.NoError(t, f.docs[r-1].Redo(), tok)
			f.push(r - 1)
		case strings.Contains(tok, ".insert("):
			scan("d%d.insert(%d,%q)", &r, &a, &v)
			updateTree(t, f.docs[r-1], insertText(a, v))
			f.push(r - 1)
		case strings.Contains(tok, ".split("):
			scan("d%d.split(%d)", &r, &a)
			updateTree(t, f.docs[r-1], splitAt(a))
			f.push(r - 1)
		case strings.Contains(tok, ".delete("):
			scan("d%d.delete(%d,%d)", &r, &a, &b)
			updateTree(t, f.docs[r-1], func(tree *json.Tree) { tree.Edit(a, b, nil, 0) })
			f.push(r - 1)
		default:
			t.Fatalf("unknown step %q", tok)
		}
	}
	for progress := true; progress; {
		progress = false
		for r := range f.docs {
			for f.pull(r) {
				progress = true
			}
		}
	}
	f.collect()
	return f.docs
}

// TestTreeBoundaryUndoSplit pins cases where undoing a split, which merges its
// product away, races a peer's split. Each converges on main, and the
// anchors must not lose that.
func TestTreeBoundaryUndoSplit(t *testing.T) {
	for _, tc := range []struct {
		name     string
		replicas int
		script   string
	}{{
		// The peer's split would open inside the product the undo removed.
		name:     "Enter and undo at the start, peer Enter at the end",
		replicas: 2,
		script:   "d1.split(1); d1.undo; d2.split(3)",
	}, {
		// The same with nothing for the merge to move.
		name:     "two Enters at the end, one undone",
		replicas: 3,
		script:   "d2.split(3); d1.split(3); d2.undo",
	}, {
		// The peer's split nests under the product the undo merged away.
		name:     "Enter and undo in the middle, peer splits the product",
		replicas: 2,
		script:   "d1.split(2); d1.undo; d2.pull; d2.split(4)",
	}, {
		// The undo's merge target is not where the content is any more: a
		// second Enter carried it on.
		name:     "Enter, undo, Enter again at the start, peer Enter at the end",
		replicas: 2,
		script:   "d2.split(3); d1.split(1); d1.undo; d1.split(1)",
	}, {
		// The undo's merge passes over a peer's newer boundary between the
		// two paragraphs: the content joins that boundary's paragraph.
		name:     "Enter and undo at the start, peer types and splits after it",
		replicas: 3,
		script:   `d3.split(1); d2.insert(1,"j"); d3.undo; d2.split(2)`,
	}, {
		// Redo must not anchor to the product the undo removed: where that
		// tombstone sits differs between replicas.
		name:     "two Enters at the end, one undone and redone",
		replicas: 3,
		script:   "d2.split(3); d1.split(3); d2.undo; d2.redo",
	}, {
		// A split made inside a product, applied where an undo has merged
		// that product away: the product's boundary is behind the position.
		name:     "Enter at the end of a product its owner undoes",
		replicas: 3,
		script:   "d2.split(1); d1.pull; d2.undo; d1.split(5); d2.split(3)",
	}, {
		// Text typed at the start of a paragraph whose Enter is then undone
		// and redone. The redo's boundary is one the typist never saw, so
		// the text stays on its left wherever the undo's merge forwards it.
		name:     "type at the start of a new paragraph, its Enter undone and redone",
		replicas: 2,
		script:   `d2.split(1); d1.pull; d1.insert(3,"l"); d2.undo; d2.redo`,
	}, {
		name:     "the same with the Enter redone by a third replica",
		replicas: 3,
		script:   `d2.split(1); d2.undo; d1.pull; d1.insert(3,"f"); d3.pull; d3.pull; d3.split(1)`,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			replayBoundaryScript(t, tc.replicas, tc.script)
		})
	}
}

// TestTreeBoundaryUndoRacesSplitAndInsert pins an undo racing a peer's Enter
// at the end of the paragraph it merges into and a third replica typing at the
// start of the paragraph it merges away, in every order, with and without the
// Enter's ticket above the undo's. The Enter did not know the undo, so its
// boundary is in front of the merged-away paragraph and the typed text joins
// its paragraph: whether text stays left of the boundary is decided by what
// the boundary was anchored to, not by comparing the two tickets.
func TestTreeBoundaryUndoRacesSplitAndInsert(t *testing.T) {
	const (
		prefix = "d1.split(1); d2.pull; d3.pull"
		bump   = `d2.insert(1,"q"); d2.delete(1,2); d2.insert(1,"q"); d2.delete(1,2)`
		undo   = "d1.undo"
		split  = "d2.split(1)"
		insert = `d3.insert(3,"x")`
	)
	for _, order := range [][]string{
		{undo, split, insert}, {undo, insert, split}, {split, undo, insert},
		{split, insert, undo}, {insert, undo, split}, {insert, split, undo},
	} {
		for _, pre := range []string{prefix, prefix + "; " + bump} {
			script := pre + "; " + strings.Join(order, "; ")
			t.Run(script, func(t *testing.T) {
				docs := playBoundaryScript(t, 3, script)
				for _, d := range docs {
					require.Equal(t, "<doc><p></p><p>xab</p></doc>", treeXML(t, d), script)
				}
				replayBoundaryScript(t, 3, script)
			})
		}
	}
}

// TestTreeBoundaryMergeTargetSurvivesReload reloads a replica from a snapshot
// between an undo's merge and a peer's split that carries the merged content
// on. A replica's later results must not depend on whether it reloaded: the
// merge forwarding it keeps at runtime has to match what a snapshot rebuilds.
func TestTreeBoundaryMergeTargetSurvivesReload(t *testing.T) {
	const prefix = "d1.split(2); d2.pull; d3.pull; d1.undo; d2.split(2); d1.pull; d1.pull"
	const suffix = `d3.insert(4,"x")`
	plain := playBoundaryScript(t, 3, prefix+"; "+suffix)
	reloaded := playBoundaryScript(t, 3, prefix+"; d1.reload; "+suffix)
	for r := range plain {
		require.Equal(t, treeXML(t, plain[r]), treeXML(t, reloaded[r]), "d%d", r+1)
	}
}

// TestTreeBoundaryRepeatedMerge has two replicas Backspace at the same join
// concurrently while a third, which saw only one Backspace, presses Enter
// there. Each replica stamps the moved text with the merge it applied first,
// so whether the Enter knew the merge read differently per replica until
// repeated merges kept the smaller ticket. When the Enter knew only the merge
// with the larger ticket, it still saw the node merged away: the merged-away
// node keeps every merge ticket for that, and a snapshot carries them.
func TestTreeBoundaryRepeatedMerge(t *testing.T) {
	for _, script := range []string{
		"d1.split(2); d2.pull; d3.pull; d1.delete(2,4); d2.delete(2,4); d3.pull; d3.split(2)",
		"d1.split(2); d2.pull; d3.pull; d2.delete(2,4); d1.delete(2,4); d3.pull; d3.split(2)",
		"d1.split(2); d2.pull; d3.pull; d2.delete(2,4); d1.delete(2,4); d3.pull; d1.pull; d1.reload; d3.split(2)",
	} {
		t.Run(script, func(t *testing.T) {
			replayBoundaryScript(t, 3, script)
		})
	}
}

// TestTreeBoundaryInsertForwardedPastTypedText has a replica type at the start
// of a paragraph whose first moved child a merge carried in, before a peer's
// insert that targets the merged-away node arrives. The typed text is newer
// than the paragraph, so the insert never knew it: it must not keep the
// moved child from opening the paragraph (preMergeHolder), or the insert
// lands after the typed text on that replica only.
func TestTreeBoundaryInsertForwardedPastTypedText(t *testing.T) {
	replayBoundaryScriptFrom(t, 3, "abcd", "d2.split(2); d3.pull; d3.delete(5,7); d1.pull; "+
		"d3.delete(1,2); d2.delete(2,6); d1.split(1); d3.pull; d3.pull; d3.delete(1,3); "+
		`d1.insert(3,"m"); d3.split(1); d3.insert(3,"r")`)
}

// TestTreeBoundaryInsertForwardedPastKnownText has the inserting replica
// know the text a peer typed at the start of the product, then insert at the
// start of the merged-away node. The text was in front of the position for
// it, so the insert stays in the product after that text.
func TestTreeBoundaryInsertForwardedPastKnownText(t *testing.T) {
	replayBoundaryScript(t, 2, `d1.split(1); d1.undo; d2.split(1); d2.insert(3,"m"); d2.pull; d2.insert(6,"n")`)
}

// TestTreeBoundaryForwardedToFixedPoint has later edits name the start of a
// merged-away node after an earlier edit was forwarded there (preMergePoint).
// The forwarding target is one fixed point, not the current end of the node
// before the product, so a delete covers text an insert put there, and an
// undo's merge removes a product a forwarded split opened there.
func TestTreeBoundaryForwardedToFixedPoint(t *testing.T) {
	for _, tc := range []struct {
		replicas int
		text     string
		script   string
	}{
		{2, "ab", `d2.split(1); d1.pull; d2.delete(1,3); d1.insert(3,"o"); d1.delete(3,5); d2.split(1); d2.insert(3,"q")`},
		{3, "ab", `d2.split(1); d1.pull; d2.delete(1,3); d1.insert(3,"o"); d1.delete(3,5); d2.split(1); d2.insert(3,"q")`},
		{2, "ab", `d1.split(2); d2.pull; d1.delete(2,4); d1.split(2); d2.split(4); d2.undo; d1.insert(4,"m")`},
		{3, "ab", `d1.split(2); d2.pull; d1.delete(2,4); d1.split(2); d2.split(4); d2.undo; d1.insert(4,"m")`},
		{2, "ab", `d2.split(1); d2.insert(3,"m"); d1.pull; d2.delete(3,4); d1.delete(1,3); d1.split(1); d1.insert(3,"p")`},
		{2, "ab", `d2.split(1); d1.pull; d2.delete(1,3); d2.undo; d1.insert(3,"x"); d1.delete(3,5); d2.insert(3,"y")`},
		// A product the change knew stops the walk unless it was split inside
		// the merged-away node; reading its anchor instead diverged here.
		{3, "abcd", `d1.split(3); d2.insert(3,"m"); d3.insert(1,"n"); d1.undo; ` +
			`d2.split(4); d3.split(4); d3.pull; d3.insert(8,"o")`},
	} {
		t.Run(tc.script, func(t *testing.T) {
			replayBoundaryScriptFrom(t, tc.replicas, tc.text, tc.script)
		})
	}
}

// TestTreeBoundarySplitAfterKnownRemoval has a split whose position is in
// front of moved children the splitting change knew were deleted. The first
// moved child opens the run even as a tombstone: the replica that applied the
// split before the merge anchored it before the merged-away node, and the
// one that applied the merge first must record the same.
func TestTreeBoundarySplitAfterKnownRemoval(t *testing.T) {
	replayBoundaryScript(t, 2, `d1.split(2); d1.insert(4,"x"); d2.pull; d2.pull; `+
		"d2.delete(2,4); d1.delete(4,5); d1.split(2)")
}

// TestTreeBoundaryMovedChildRevivedByUndo has a split whose change knew a
// moved child was deleted, while the delete's author undoes it. Whether the
// child is still a tombstone depends on whether the undo arrived first, so
// it must not decide where the split is anchored.
func TestTreeBoundaryMovedChildRevivedByUndo(t *testing.T) {
	replayBoundaryScript(t, 3, "d1.split(2); d2.pull; d1.delete(4,5); d2.pull; d3.pull; "+
		"d3.delete(2,4); d2.split(2); d1.undo")
}

// TestTreeBoundaryRemovalKnownThroughMergeTicket has a node removed by two
// merges, and a change that knew only the one whose ticket removedAt does
// not hold. It still knew the node was removed (knewRemovalOf).
func TestTreeBoundaryRemovalKnownThroughMergeTicket(t *testing.T) {
	replayBoundaryScript(t, 2, "d2.split(1); d2.split(1); d1.pull; d2.delete(3,5); d2.split(5); d1.delete(1,3)")
}

// TestTreeBoundarySeenParentFromOperation has a peer press Enter at the end of
// a paragraph whose own Enter was undone meanwhile. On a replica that applied
// the undo first, the merge has moved the text, and the end of the paragraph
// the peer saw has the same neighbours as the start of the merged-back one;
// only the parent the operation names says where the peer was.
func TestTreeBoundarySeenParentFromOperation(t *testing.T) {
	replayBoundaryScript(t, 3, "d2.split(1); d2.undo; d1.pull; d1.split(3)")
}

// TestTreeBoundaryMergedAwayNodeDeletedOnce has a change cover a node a merge
// removed. A change that knew any merge removing the node reads it as
// removed (knewRemovalOf), whichever merge's ticket removedAt holds on the
// replica, so it cannot re-delete the node and tombstone, on one replica
// only, the children the merge moved out (propagateMergeDeletes) or the
// split siblings it never saw (appendUnknownSplitSiblings).
func TestTreeBoundaryMergedAwayNodeDeletedOnce(t *testing.T) {
	for _, tc := range []struct {
		replicas int
		text     string
		script   string
	}{
		{2, "ab", `d2.split(1); d1.pull; d2.insert(3,"m"); d1.delete(1,3); d2.split(4); ` +
			"d2.delete(1,3); d1.split(1); d1.delete(1,3); d1.split(1)"},
		{2, "ab", `d2.split(1); d1.pull; d2.insert(3,"m"); d1.delete(1,3); d2.split(4); ` +
			"d2.delete(1,3); d1.split(1); d1.delete(1,3); d2.undo; d2.redo; d1.undo"},
		{2, "ab", `d2.split(1); d1.pull; d2.insert(3,"m"); d1.delete(3,4); d1.delete(1,3); ` +
			"d2.split(4); d2.delete(1,3); d1.split(1); d1.pull; d1.delete(2,4)"},
		// Diverges on 2b33dc54 as well.
		{2, "a", `d2.split(1); d1.pull; d2.insert(3,"m"); d1.delete(1,3); d2.split(4); ` +
			"d2.delete(1,3); d1.split(1); d1.pull; d1.delete(2,4)"},
	} {
		t.Run(tc.script, func(t *testing.T) {
			replayBoundaryScriptFrom(t, tc.replicas, tc.text, tc.script)
		})
	}
}

// TestTreeBoundaryMergedIntoFollowsFirstHolder has text a merge moved out of
// a node carried off its family, here to the root by a delete from a
// root-level position. The merged-away node's forwarding pointer is the
// parent of the first child that names it as MergedFrom, as a snapshot
// rebuilds it, not wherever a moved child went last, so a later range that
// names the node does not run past its family.
func TestTreeBoundaryMergedIntoFollowsFirstHolder(t *testing.T) {
	for _, tc := range []struct {
		replicas int
		text     string
		script   string
	}{
		{2, "abcd", `d1.split(1); d1.insert(3,"m"); d1.undo; d1.undo; d2.insert(1,"n"); d2.pull; ` +
			"d2.delete(2,4); d2.undo; d2.split(1); d2.split(1); d2.pull; d2.delete(4,6)"},
		// Diverges on 2b33dc54 as well.
		{3, "abcd", `d1.split(1); d1.insert(3,"m"); d2.insert(1,"n"); d2.pull; d2.delete(2,4); ` +
			"d2.undo; d2.split(1); d2.split(1); d2.pull; d2.delete(4,6); d1.undo; d1.undo"},
	} {
		t.Run(tc.script, func(t *testing.T) {
			replayBoundaryScriptFrom(t, tc.replicas, tc.text, tc.script)
		})
	}
}

// TestTreeBoundaryConcurrentMergeKeepsSplitCascade has a delete reach a
// node that a merge it did not know already emptied on one replica and not
// yet on the other. The split-sibling cascade and merge propagation must
// not read that replica-local merge state, or the replica that applied the
// merge first keeps split siblings and moved content the other one drops.
func TestTreeBoundaryConcurrentMergeKeepsSplitCascade(t *testing.T) {
	for _, tc := range []struct {
		text   string
		script string
	}{
		{"a", "d1.split(1); d2.split(1); d2.split(3); d1.split(4); d2.undo; d2.pull; " +
			"d1.delete(1,3); d2.delete(3,7)"},
		{"a", `d1.split(1); d2.split(1); d1.insert(3,"m"); d2.split(3); d1.split(4); ` +
			"d2.undo; d2.pull; d1.delete(1,3); d2.delete(3,7)"},
	} {
		t.Run(tc.script, func(t *testing.T) {
			replayBoundaryScriptFrom(t, 2, tc.text, tc.script)
		})
	}
}

// TestTreeBoundaryKnownDivergences records cases that still diverge. The
// first diverges on main too. Each is skipped with what it would take.
func TestTreeBoundaryKnownDivergences(t *testing.T) {
	for _, tc := range []struct {
		name     string
		script   string
		text     string
		needs    string
		replicas int
	}{{
		name:   "split with a peer's concurrent split and undo, then typing",
		script: `d1.split(1); d2.split(1); d3.pull; d1.undo; d3.insert(3,"x")`,
		needs: "The tree, the records and the ticket order match the case in " +
			"TestTreeBoundaryUndoRacesSplitAndInsert, but the text belongs on the " +
			"other side. Only whether the split's change knew the merge tells them " +
			"apart, and no replica keeps that.",
	}, {
		name:   "two inserts at the start of a merged-away node, then an undo",
		script: `d1.split(3); d2.pull; d3.pull; d3.insert(5,"n"); d2.insert(5,"o"); d1.delete(3,5); d1.undo`,
		text:   "abcd",
		needs: "Both inserts are forwarded to the same fixed point and stamped as moved " +
			"out of the merged-away node; their order there still follows arrival. " +
			"The order with d2's insert first diverges without preMergePoint too.",
	}, {
		name:   "split re-made inside a merged-away node, then a range from it",
		script: "d2.split(1); d1.pull; d2.delete(1,3); d2.split(1); d1.split(3); d1.delete(1,3); d2.pull; d2.delete(5,7)",
		text:   "abc",
		needs: "preMergePoint passes over a product nested in the merged-away node " +
			"that a change which did not know the merge split off; the records " +
			"already part here without it. Converges on main.",
	}, {
		name: "split arriving before and after a merge, then ranges over it",
		script: "d1.split(1); d2.split(1); d2.split(3); d1.split(4); d2.undo; d2.pull; " +
			"d1.delete(1,3); d2.delete(3,5); d2.delete(3,7)",
		text:     "abcd",
		replicas: 2,
		needs: "A change that knew its merge no longer re-deletes the merged-away " +
			"node (knewRemovalOf), which exposes how a split applied after the merge " +
			"and one applied before it differ under the toBeMergedNodes skip. " +
			"Converges on main.",
	}, {
		name: "undo and redo of a merge over text typed into a split product",
		script: `d2.split(5); d1.pull; d2.insert(7,"m"); d2.split(8); d1.delete(5,7); ` +
			"d2.delete(5,7); d1.undo; d1.redo",
		text:     "abcd",
		replicas: 2,
		needs: "The prefix before the undo already diverges on main; the redo's merge " +
			"used to mask it through the one-replica re-delete of the merged-away node.",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			if os.Getenv("YORKIE_RUN_KNOWN_DIVERGENCES") == "" {
				t.Skip(tc.needs)
			}
			text := tc.text
			if text == "" {
				text = "ab"
			}
			replicas := tc.replicas
			if replicas == 0 {
				replicas = 3
			}
			replayBoundaryScriptFrom(t, replicas, text, tc.script)
		})
	}
}

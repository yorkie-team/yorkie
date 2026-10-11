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

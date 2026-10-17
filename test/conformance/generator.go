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

package conformance

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf16"

	"github.com/yorkie-team/yorkie/pkg/document/json"
)

// Family is a fixture family: which generator family, how many replicas
// and how many steps. With Observer the last replica never edits: it only
// pulls, the way a read-only client keeps its server row moving. Runs is
// how many seeds its parity fixture pins, 0 meaning defaultRuns.
type Family struct {
	Name     string
	Fam      string
	N        int
	Steps    int
	Observer bool
	Runs     int
}

// defaultRuns is the seed count of a family that does not state one, and
// v2Runs the count of the families format v2 added.
//
// The parity fixtures are a cross-SDK check — the two runners must agree
// byte for byte on the same seed — not the net that catches a convergence
// bug: a seed that caught one is minimized into tree/corpus, which every
// run replays whatever the seed count is. So the count only has to be wide
// enough to catch a runner that drifts, and the v2 families take a quarter
// of the v1 count. That is a CPU bound, not a taste: this package has no
// build tag, so CI's one test lane runs it beside the integration suite on
// the same four cores, and the v2 families at the v1 count tripled the
// suite's share of them (70s to 219s) — which is CPU the timing-sensitive
// integration tests then miss their deadlines without.
const (
	defaultRuns = 200
	v2Runs      = 50
)

// Families are the fixture families. The first six are the original ones,
// at defaultRuns; the undo, span and observer families came later and run
// v2Runs, whatever script format they generate (only the span families
// write format v2 scripts).
var Families = []Family{
	{Name: "is", Fam: "is", N: 2, Steps: 24},
	{Name: "del", Fam: "del", N: 2, Steps: 24},
	{Name: "merge", Fam: "merge", N: 2, Steps: 24},
	{Name: "style", Fam: "style", N: 2, Steps: 24},
	{Name: "all", Fam: "all", N: 2, Steps: 24},
	{Name: "all3", Fam: "all", N: 3, Steps: 30},

	{Name: "ud", Fam: "ud", N: 2, Steps: 24, Runs: v2Runs},
	{Name: "ud3", Fam: "ud", N: 3, Steps: 30, Runs: v2Runs},
	{Name: "uds", Fam: "uds", N: 2, Steps: 24, Runs: v2Runs},
	{Name: "uds3", Fam: "uds", N: 3, Steps: 30, Runs: v2Runs},
	{Name: "udm", Fam: "udm", N: 2, Steps: 24, Runs: v2Runs},
	{Name: "udst", Fam: "udst", N: 2, Steps: 24, Runs: v2Runs},
	{Name: "uall", Fam: "uall", N: 2, Steps: 24, Runs: v2Runs},
	{Name: "uall3", Fam: "uall", N: 3, Steps: 30, Runs: v2Runs},

	{Name: "span", Fam: "span", N: 2, Steps: 24, Runs: v2Runs},
	{Name: "spand", Fam: "spand", N: 2, Steps: 24, Runs: v2Runs},
	{Name: "spand3", Fam: "spand", N: 3, Steps: 30, Runs: v2Runs},

	{Name: "del4o", Fam: "del", N: 4, Steps: 40, Observer: true, Runs: v2Runs},
	{Name: "all4o", Fam: "all", N: 4, Steps: 40, Observer: true, Runs: v2Runs},
}

// ParityRuns is how many seeds this family's parity fixture pins.
func (f Family) ParityRuns() int {
	if f.Runs == 0 {
		return defaultRuns
	}
	return f.Runs
}

// FamilyByName returns the fixture family with the given name.
func FamilyByName(name string) (Family, bool) {
	for _, f := range Families {
		if f.Name == name {
			return f, true
		}
	}
	return Family{}, false
}

// Gen emits the script of this family for the given seed.
func (f Family) Gen(seed uint32) *Script {
	return generate(seed, f.Fam, f.N, f.Steps, f.Observer)
}

type weight struct {
	k string
	w float64
}

// famWeights are the event weights of each generator family. `undo` and
// `redo` are emitted as undo and redo steps, the span kinds (`s…`) as gen
// steps on the span tree of spanInit, every other kind as a gen step.
var famWeights = map[string][]weight{
	"is":    {{"ins", 0.5}, {"split", 0.5}},
	"del":   {{"ins", 0.35}, {"split", 0.35}, {"del", 0.3}},
	"merge": {{"ins", 0.35}, {"split", 0.35}, {"merge", 0.3}},
	"style": {{"ins", 0.35}, {"split", 0.35}, {"style", 0.3}},
	"all":   {{"ins", 0.3}, {"split", 0.3}, {"del", 0.15}, {"merge", 0.15}, {"style", 0.1}},

	"ud":   {{"ins", 0.35}, {"del", 0.3}, {"undo", 0.25}, {"redo", 0.1}},
	"uds":  {{"ins", 0.3}, {"split", 0.2}, {"del", 0.2}, {"undo", 0.2}, {"redo", 0.1}},
	"udm":  {{"ins", 0.3}, {"split", 0.15}, {"del", 0.1}, {"merge", 0.15}, {"undo", 0.2}, {"redo", 0.1}},
	"udst": {{"ins", 0.3}, {"split", 0.15}, {"style", 0.25}, {"undo", 0.2}, {"redo", 0.1}},
	"uall": {{"ins", 0.25}, {"split", 0.2}, {"del", 0.1}, {"merge", 0.1}, {"style", 0.05},
		{"undo", 0.2}, {"redo", 0.1}},

	"span":  {{"sins", 0.4}, {"ssplit", 0.15}, {"senter", 0.15}, {"senter11", 0.3}},
	"spand": {{"sins", 0.4}, {"ssplit", 0.15}, {"senter", 0.15}, {"senter11", 0.15}, {"sdel", 0.15}},
}

const valuePool = "cdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"

// Mulberry32 is the PRNG both SDKs share, so seed N is the same script in
// each.
func Mulberry32(a uint32) func() float64 {
	return func() float64 {
		a += 0x6d2b79f5
		t := (a ^ (a >> 15)) * (1 | a)
		t = (t + (t^(t>>7))*(61|t)) ^ t
		return float64(t^(t>>14)) / 4294967296
	}
}

// Gen emits the script of generator family fam for the given seed, with
// no observer.
func Gen(seed uint32, fam string, n, steps int) *Script {
	return generate(seed, fam, n, steps, false)
}

func generate(seed uint32, fam string, n, steps int, observer bool) *Script {
	ws, ok := famWeights[fam]
	if !ok {
		panic(fmt.Sprintf("unknown family %q", fam))
	}
	rnd := Mulberry32(seed)
	c := 0
	var out []Step
	var authors []int
	cursor := make([]int, n)
	pending := func(r int) int {
		p := 0
		for i := cursor[r]; i < len(authors); i++ {
			if authors[i] != r {
				p++
			}
		}
		return p
	}
	pull := func(r int) {
		out = append(out, Step{R: r, Op: opPull})
		for cursor[r] < len(authors) && authors[cursor[r]] == r {
			cursor[r]++
		}
		cursor[r]++
	}
	for range steps {
		r := int(math.Floor(rnd() * float64(n)))
		if observer && r == n-1 {
			if pending(r) > 0 {
				out = append(out, Step{R: r, Op: opSync})
				cursor[r] = len(authors)
			}
			continue
		}
		if rnd() < 0.5 && pending(r) > 0 {
			pull(r)
			continue
		}
		pick := rnd()
		acc := 0.0
		k := ws[len(ws)-1].k
		for _, w := range ws {
			acc += w.w
			if pick < acc {
				k = w.k
				break
			}
		}
		f, g, h := rnd(), rnd(), rnd()
		v := string(valuePool[c%len(valuePool)])
		c++
		switch k {
		case opUndo, opRedo:
			out = append(out, Step{R: r, Op: k})
		default:
			out = append(out, Step{R: r, Op: opGen, K: k, F: f, G: g, H: h, Val: v})
		}
		authors = append(authors, r)
	}
	s := &Script{
		V:        1,
		ID:       fmt.Sprintf("gen-%s-n%d-s%d-%d", fam, n, steps, seed),
		Replicas: n,
		Init:     Init{Text: "abcd"},
		Steps:    out,
	}
	if observer {
		s.ID = fmt.Sprintf("gen-%s-n%do-s%d-%d", fam, n, steps, seed)
	}
	if spanGenKinds[ws[0].k] {
		s.V = 2
		s.Init = Init{Node: spanInit()}
	}
	return s
}

// para is a live paragraph: the index of its <p> and the length of its
// text in UTF-16 code units, the unit of Tree indexes in both SDKs.
type para struct {
	base int
	size int
}

// paras lists the live paragraphs of replica r.
func (rn *run) paras(r int) []para {
	var out []para
	base := 0
	for _, p := range tree(rn.docs[r]).Root().Children() {
		var sb strings.Builder
		for _, c := range p.Children() {
			if c.IsText() {
				sb.WriteString(c.Value)
			}
		}
		size := len(utf16.Encode([]rune(sb.String())))
		out = append(out, para{base, size})
		base += size + 2
	}
	return out
}

func pickIndex(f float64, n int) int {
	return int(math.Floor(f * float64(n)))
}

// gen resolves a generator event on replica st.R exactly as fuzz2 does and
// returns its description, or "" when there was no candidate.
func (rn *run) gen(st Step) string {
	ps := rn.paras(st.R)
	switch st.K {
	case "ins", "split":
		var slots []int
		for _, p := range ps {
			for k := 0; k <= p.size; k++ {
				slots = append(slots, p.base+1+k)
			}
		}
		if len(slots) == 0 {
			return ""
		}
		idx := slots[pickIndex(st.F, len(slots))]
		if st.K == "ins" {
			rn.edit(st.R, idx, idx, st.Val, 0)
			return describeEdit(idx, idx, st.Val, 0)
		}
		rn.edit(st.R, idx, idx, "", 1)
		return describeEdit(idx, idx, "", 1)
	case "del":
		type cand struct{ idx, room int }
		var cs []cand
		for _, p := range ps {
			for k := 0; k < p.size; k++ {
				cs = append(cs, cand{p.base + 1 + k, min(3, p.size-k)})
			}
		}
		if len(cs) == 0 {
			return ""
		}
		c := cs[pickIndex(st.F, len(cs))]
		ln := 1 + pickIndex(st.G, c.room)
		rn.edit(st.R, c.idx, c.idx+ln, "", 0)
		return describeEdit(c.idx, c.idx+ln, "", 0)
	case "merge":
		if len(ps) < 2 {
			return ""
		}
		i := pickIndex(st.F, len(ps)-1)
		base, size, next := ps[i].base, ps[i].size, ps[i+1].size
		a := pickIndex(st.G, min(2, size)+1)
		b := pickIndex(st.H, min(2, next)+1)
		from := base + 1 + size - a
		to := base + size + 3 + b
		rn.edit(st.R, from, to, "", 0)
		return describeEdit(from, to, "", 0)
	case "style":
		if len(ps) == 0 {
			return ""
		}
		p := ps[pickIndex(st.F, len(ps))]
		key := "i"
		if st.G < 0.5 {
			key = "b"
		}
		attrs := map[string]string{key: st.Val}
		to := p.base + p.size + 2
		rn.update(st.R, func(t *json.Tree) { t.Style(p.base, to, attrs) })
		return describeStyle(p.base, to, attrs)
	}
	if spanGenKinds[st.K] {
		return rn.genSpan(st)
	}
	panic(fmt.Sprintf("unknown gen kind %q", st.K))
}

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
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The span oracle enumerates every two- and three-op combination of the
// span family's edits (insert, span split, Enter as one edit, Enter as two
// edits) on <doc><p><span>abcd</span></p></doc>, runs each through the
// runner, and compares the converged XML with the set of results an
// intention-preserving list CRDT may give. That set comes from a flat
// model: the tree as the token stream its indexes count (tags and
// characters), each op as tokens inserted between the two tokens around its
// gap at the time it was made. Any final order that keeps every op between
// its two neighbours, never splits another op's tokens, and is well formed
// (text only inside spans) is allowed. A combination whose allowed set has
// one element pins the outcome; one with several leaves the choice to the
// rule a fix adopts (yorkie#2187 follow-up, P4).
//
// CONFORMANCE_ORACLE_OUT=path writes one JSON line per combination and
// prints the summary tables. Skipped otherwise.

// oTok is one token of the flat model.
type oTok struct {
	op int // the op that inserted it, -1 for the init tree
	s  string
}

// oPrim is one insertion of tokens between two existing tokens. Token ids
// are global: the init tree's are 0..7, op k's are 1000*(k+1)+n.
type oPrim struct {
	op          int
	left, right int // token ids, -1 for the start, -2 for the end
	ids         []int
	toks        []string
}

type oModel struct {
	ids  []int
	toks map[int]oTok
	seq  map[int]int // tokens inserted so far per op
}

func newOModel() *oModel {
	m := &oModel{toks: map[int]oTok{}, seq: map[int]int{}}
	for i, s := range []string{"<p>", "<span>", "a", "b", "c", "d", "</span>", "</p>"} {
		m.toks[i] = oTok{-1, s}
		m.ids = append(m.ids, i)
	}
	return m
}

func (m *oModel) clone() *oModel {
	c := &oModel{ids: append([]int{}, m.ids...), toks: map[int]oTok{}, seq: map[int]int{}}
	maps.Copy(c.toks, m.toks)
	maps.Copy(c.seq, m.seq)
	return c
}

func (m *oModel) put(id int, t oTok, at int) {
	m.toks[id] = t
	m.ids = append(m.ids[:at], append([]int{id}, m.ids[at:]...)...)
}

// stack is the open-element stack at gap i (after i tokens).
func (m *oModel) stack(i int) []string {
	var st []string
	for _, id := range m.ids[:i] {
		s := m.toks[id].s
		switch {
		case strings.HasPrefix(s, "</"):
			st = st[:len(st)-1]
		case strings.HasPrefix(s, "<"):
			st = append(st, strings.Trim(s, "<>"))
		}
	}
	return st
}

// insert applies a local insertion at gap i and returns its primitive.
func (m *oModel) insert(op, i int, toks []string) oPrim {
	p := oPrim{op: op, left: -1, right: -2, toks: toks}
	if i > 0 {
		p.left = m.ids[i-1]
	}
	if i < len(m.ids) {
		p.right = m.ids[i]
	}
	for k, s := range toks {
		id := 1000*(op+1) + m.seq[op]
		m.seq[op]++
		p.ids = append(p.ids, id)
		m.put(id, oTok{op, s}, i+k)
	}
	return p
}

// split is a split of the given level at gap i.
func (m *oModel) split(op, i, level int) oPrim {
	st := m.stack(i)
	var toks []string
	for k := range level {
		toks = append(toks, "</"+st[len(st)-1-k]+">")
	}
	for k := level - 1; k >= 0; k-- {
		toks = append(toks, "<"+st[len(st)-1-k]+">")
	}
	return m.insert(op, i, toks)
}

func (m *oModel) xml() string {
	var sb strings.Builder
	sb.WriteString("<doc>")
	for _, id := range m.ids {
		sb.WriteString(m.toks[id].s)
	}
	sb.WriteString("</doc>")
	return sb.String()
}

// spanSlots are the gaps inside a span, as the span generator offers them.
func (m *oModel) spanSlots() []int {
	var out []int
	for i := 0; i <= len(m.ids); i++ {
		st := m.stack(i)
		if len(st) == 2 && st[1] == oTagSpan {
			out = append(out, i)
		}
	}
	return out
}

// wellFormed: text only inside a span, a span only inside a p, a p only
// at the top, every tag balanced.
func (m *oModel) wellFormed() bool {
	var st []string
	for _, id := range m.ids {
		s := m.toks[id].s
		switch {
		case strings.HasPrefix(s, "</"):
			if len(st) == 0 || "</"+st[len(st)-1]+">" != s {
				return false
			}
			st = st[:len(st)-1]
		case strings.HasPrefix(s, "<"):
			t := strings.Trim(s, "<>")
			if (t == "p" && len(st) != 0) || (t == oTagSpan && (len(st) != 1 || st[0] != "p")) {
				return false
			}
			st = append(st, t)
		default:
			if len(st) != 2 || st[1] != oTagSpan {
				return false
			}
		}
	}
	return len(st) == 0
}

// oOp is one op of a combination.
type oOp struct {
	r    int
	kind string // ins, ssplit, senter, senter11
	slot int    // index into the replica's span slots
	val  string
}

// oPattern is the causal shape of a combination: which replica makes each
// op, and which earlier ops each op has seen.
type oPattern struct {
	name string
	reps []int
	// after[k] lists the ops op k has seen. A replica's own earlier ops
	// are always seen; an op of another replica it has seen is delivered
	// by a pull just before op k.
	after [][]int
}

var oPatterns = []oPattern{
	{name: "2c", reps: []int{0, 1}, after: [][]int{{}, {}}},
	{name: "3c-r0chain", reps: []int{0, 0, 1}, after: [][]int{{}, {0}, {}}},
	{name: "3c-r1chain", reps: []int{0, 1, 1}, after: [][]int{{}, {}, {1}}},
	{name: "3s", reps: []int{0, 1, 0}, after: [][]int{{}, {0}, {0}}},
}

// The oracle's op kinds and the element tag a span slot sits in.
const (
	oKindIns = "ins"
	oTagSpan = "span"
)

var oKinds = []string{oKindIns, "ssplit", "senter", "senter11"}

// apply runs op k on model m and returns its primitives and its step.
func (m *oModel) apply(k int, o oOp) ([]oPrim, Step, bool) {
	slots := m.spanSlots()
	if o.slot >= len(slots) {
		return nil, Step{}, false
	}
	i := slots[o.slot]
	switch o.kind {
	case oKindIns:
		return []oPrim{m.insert(k, i, []string{o.val})},
			Step{R: o.r, Op: opEdit, From: i, To: i, Text: o.val}, true
	case "ssplit":
		return []oPrim{m.split(k, i, 1)}, Step{R: o.r, Op: opEdit, From: i, To: i, Split: 1}, true
	case "senter":
		return []oPrim{m.split(k, i, 2)}, Step{R: o.r, Op: opEdit, From: i, To: i, Split: 2}, true
	case "senter11":
		p1 := m.split(k, i, 1)
		p2 := m.split(k, i+1, 1)
		return []oPrim{p1, p2}, Step{R: o.r, Op: opUpdate, Ops: []Step{
			{Op: opEdit, From: i, To: i, Split: 1}, {Op: opEdit, From: i + 1, To: i + 1, Split: 1},
		}}, true
	}
	panic(o.kind)
}

// allowed enumerates every final XML the flat model allows. before(j, k)
// reports whether op k causally follows op j.
func allowed(prims [][]oPrim, before func(j, k int) bool) []string {
	n := len(prims)
	done := make([]int, n)
	total := 0
	for _, ps := range prims {
		total += len(ps)
	}
	seen := map[string]bool{}
	var rec func(m *oModel, placed int)
	rec = func(m *oModel, placed int) {
		if placed == total {
			if m.wellFormed() {
				seen[m.xml()] = true
			}
			return
		}
		for k := range n {
			if done[k] == len(prims[k]) {
				continue
			}
			ready := true
			for j := range n {
				if j != k && before(j, k) && done[j] < len(prims[j]) {
					ready = false
				}
			}
			// An op's primitives are atomic with respect to other ops:
			// once an op started, it finishes first.
			for j := range n {
				if j != k && done[j] > 0 && done[j] < len(prims[j]) {
					ready = false
				}
			}
			if !ready {
				continue
			}
			p := prims[k][done[k]]
			lo, hi := 0, len(m.ids)
			for idx, id := range m.ids {
				if id == p.left {
					lo = idx + 1
				}
				if id == p.right {
					hi = idx
				}
			}
			for g := lo; g <= hi; g++ {
				// Nobody lands inside another op's tokens.
				if g > 0 && g < len(m.ids) {
					a, b := m.toks[m.ids[g-1]].op, m.toks[m.ids[g]].op
					if a == b && a >= 0 && a != k {
						continue
					}
				}
				c := m.clone()
				for t, s := range p.toks {
					c.put(p.ids[t], oTok{k, s}, g+t)
				}
				done[k]++
				rec(c, placed+1)
				done[k]--
			}
		}
	}
	rec(newOModel(), 0)
	out := make([]string, 0, len(seen))
	for x := range seen {
		out = append(out, x)
	}
	sort.Strings(out)
	return out
}

// oCase is one enumerated combination and its verdict.
type oCase struct {
	Pattern string   `json:"pattern"`
	Kinds   []string `json:"kinds"`
	Slots   []int    `json:"slots"`
	Steps   []string `json:"steps"`
	Result  string   `json:"result"`
	Checks  string   `json:"checks"`
	XML     []string `json:"xml"`
	Allowed []string `json:"allowed"`
	Verdict string   `json:"verdict"`
}

// build makes the script and the oracle of one combination, or returns
// false when a slot does not exist.
func (pt oPattern) build(kinds []string, slots []int) (*Script, []string, bool) {
	vals := []string{"x", "y", "z"}
	models := map[int]*oModel{0: newOModel(), 1: newOModel()}
	cursor := map[int]int{0: 0, 1: 0}
	var log []int // op index of each log entry
	prims := make([][]oPrim, len(kinds))
	var steps []Step
	for k := range kinds {
		r := pt.reps[k]
		for _, j := range pt.after[k] {
			if pt.reps[j] == r {
				continue
			}
			// deliver op j: replica r has seen nothing else of the other
			// replica, so its model becomes the author's model at that
			// point plus r's own ops, which there are none of in these
			// patterns.
			for cursor[r] < len(log) {
				idx := cursor[r]
				cursor[r]++
				if pt.reps[log[idx]] == r {
					continue
				}
				steps = append(steps, Step{R: r, Op: opPull})
				if log[idx] == j {
					break
				}
			}
			m := models[r]
			for _, p := range prims[j] {
				at := slices.Index(m.ids, p.left) + 1
				for t, s := range p.toks {
					m.put(p.ids[t], oTok{j, s}, at+t)
				}
				m.seq[j] = len(p.ids)
			}
		}
		ps, st, ok := models[r].apply(k, oOp{r: r, kind: kinds[k], slot: slots[k], val: vals[k]})
		if !ok {
			return nil, nil, false
		}
		prims[k] = ps
		steps = append(steps, st)
		log = append(log, k)
	}
	steps = append(steps, Step{Op: opExpect, Label: "local", XML: []string{models[0].xml(), models[1].xml()}})
	s := &Script{V: 2, ID: "oracle", Replicas: 2, Init: Init{Node: spanInit()}, Delivery: DeliveryLog, Steps: steps}
	before := func(j, k int) bool {
		if pt.reps[j] == pt.reps[k] && j < k {
			return true
		}
		return slices.Contains(pt.after[k], j)
	}
	return s, allowed(prims, before), true
}

func oVerdict(rec Record, allow []string) string {
	switch {
	case !rec.Checks.E:
		return "model-mismatch"
	case rec.Result == ResultError:
		return "error"
	case rec.Checks.I1 && slices.Contains(allow, rec.XML[0]):
		return "ok"
	case rec.Checks.I1:
		return "converged-outside"
	}
	in := 0
	for _, x := range rec.XML {
		if slices.Contains(allow, x) {
			in++
		}
	}
	if rec.XML[0] == rec.XML[1] {
		return "shape-only"
	}
	return fmt.Sprintf("diverged-%dof2-allowed", in)
}

func TestSpanOracle(t *testing.T) {
	out := os.Getenv("CONFORMANCE_ORACLE_OUT")
	if out == "" {
		t.Skip("CONFORMANCE_ORACLE_OUT is not set")
	}
	f, err := os.Create(out) // #nosec G304 G703 -- test-chosen path
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)

	type key struct{ pattern, kinds, verdict string }
	counts := map[key]int{}
	totals := map[string]map[string]int{}
	for _, pt := range oPatterns {
		totals[pt.name] = map[string]int{}
		n := len(pt.reps)
		var walk func(kinds []string, slots []int)
		walk = func(kinds []string, slots []int) {
			if len(kinds) == n {
				s, allow, ok := pt.build(kinds, slots)
				if !ok {
					return
				}
				rec := Run(s)
				v := oVerdict(rec, allow)
				c := oCase{Pattern: pt.name, Kinds: kinds, Slots: slots, Steps: rec.Steps,
					Result: rec.Result, Checks: rec.Checks.String(), XML: rec.XML, Allowed: allow, Verdict: v}
				require.NoError(t, enc.Encode(c))
				counts[key{pt.name, strings.Join(kinds, "+"), v}]++
				totals[pt.name][v]++
				totals[pt.name]["total"]++
				if len(allow) == 1 {
					totals[pt.name]["unique-oracle"]++
					if v == "ok" {
						totals[pt.name]["unique-oracle-ok"]++
					}
				}
				return
			}
			for _, k := range oKinds {
				for sl := range 12 {
					walk(append(append([]string{}, kinds...), k), append(append([]int{}, slots...), sl))
				}
			}
		}
		walk(nil, nil)
	}
	for _, pt := range oPatterns {
		fmt.Printf("TOTAL %s %v\n", pt.name, totals[pt.name])
	}
	keys := make([]key, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.pattern != b.pattern {
			return a.pattern < b.pattern
		}
		if a.kinds != b.kinds {
			return a.kinds < b.kinds
		}
		return a.verdict < b.verdict
	})
	for _, k := range keys {
		fmt.Printf("ROW %s %s %s %d\n", k.pattern, k.kinds, k.verdict, counts[k])
	}
}

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
	"unicode/utf16"

	"github.com/yorkie-team/yorkie/pkg/document/json"
)

// spanGenKinds are the generator kinds of the span families. They resolve
// on the text slots of spans, `<doc><p><span>…</span>…</p>…</doc>`, so a
// script that carries them starts from a tree init.
var spanGenKinds = map[string]bool{
	"sins": true, "ssplit": true, "senter": true, "senter11": true, "sdel": true,
}

// spanInit is the tree the span families start from:
// `<doc><p><span>abcd</span></p></doc>`.
func spanInit() *json.TreeNode {
	return &json.TreeNode{Type: "doc", Children: []json.TreeNode{{
		Type: "p",
		Children: []json.TreeNode{{
			Type:     "span",
			Children: []json.TreeNode{{Type: textType, Value: "abcd"}},
		}},
	}}}
}

// spanSlot is a text slot inside a live span: its tree index, its offset in
// the span's text and the length of that text, both in UTF-16 code units.
type spanSlot struct {
	idx   int
	inner int
	size  int
}

// spanSlots lists every text slot of every live span of replica r, in
// document order. A span's text is the concatenation of its text children.
func (rn *run) spanSlots(r int) []spanSlot {
	var out []spanSlot
	idx := 0
	for _, p := range tree(rn.docs[r]).Root().Children() {
		idx++ // <p>
		for _, sp := range p.Children() {
			if sp.IsText() {
				idx += len(utf16.Encode([]rune(sp.Value)))
				continue
			}
			idx++ // <span>
			size := 0
			for _, c := range sp.Children() {
				if c.IsText() {
					size += len(utf16.Encode([]rune(c.Value)))
				}
			}
			for k := 0; k <= size; k++ {
				out = append(out, spanSlot{idx + k, k, size})
			}
			idx += size + 1 // text, </span>
		}
		idx++ // </p>
	}
	return out
}

// genSpan resolves a span generator event on replica st.R: `sins` inserts
// text, `ssplit` splits the span, `senter` splits the span and its
// paragraph in one edit (level 2), `senter11` does the same in two edits of
// one update (the span, then the paragraph between the two halves), `sdel`
// deletes inside the span. It returns "" when there was no candidate.
func (rn *run) genSpan(st Step) string {
	slots := rn.spanSlots(st.R)
	if len(slots) == 0 {
		return ""
	}
	s := slots[pickIndex(st.F, len(slots))]
	switch st.K {
	case "sins":
		rn.edit(st.R, s.idx, s.idx, st.Val, 0)
		return describeEdit(s.idx, s.idx, st.Val, 0)
	case "ssplit":
		rn.edit(st.R, s.idx, s.idx, "", 1)
		return describeEdit(s.idx, s.idx, "", 1)
	case "senter":
		rn.edit(st.R, s.idx, s.idx, "", 2)
		return describeEdit(s.idx, s.idx, "", 2)
	case "senter11":
		rn.update(st.R, func(t *json.Tree) {
			t.Edit(s.idx, s.idx, nil, 1)
			t.Edit(s.idx+1, s.idx+1, nil, 1)
		})
		return fmt.Sprintf("update[%s;%s]",
			describeEdit(s.idx, s.idx, "", 1), describeEdit(s.idx+1, s.idx+1, "", 1))
	case "sdel":
		if s.inner >= s.size {
			return ""
		}
		ln := 1 + pickIndex(st.G, min(2, s.size-s.inner))
		rn.edit(st.R, s.idx, s.idx+ln, "", 0)
		return describeEdit(s.idx, s.idx+ln, "", 0)
	}
	panic(fmt.Sprintf("unknown span gen kind %q", st.K))
}

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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
)

// "😀x" is one non-BMP code point followed by one BMP one: 2 runes, 3 UTF-16
// code units. Index 1 is the only index that cuts the emoji in half.
const emojiText = "\U0001F600x"

// midSurrogateDoc builds <r><p>😀x</p></r>.
func midSurrogateDoc(t *testing.T) *document.Document {
	t.Helper()

	doc := document.New("mid-surrogate")
	require.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
		r.SetNewTree("t", json.TreeNode{Type: "r", Children: []json.TreeNode{{
			Type: "p", Children: []json.TreeNode{{Type: "text", Value: emojiText}},
		}}})
		r.SetNewText("s").Edit(0, 0, emojiText)
		return nil
	}))

	return doc
}

// recovered runs fn and returns whatever it panicked with, or nil. The json
// layer reports caller errors by panicking; Update does not translate those
// into its error return, so the test has to catch them itself.
func recovered(fn func()) (r any) {
	defer func() { r = recover() }()
	fn()
	return nil
}

// updatePanic applies fn to the document and returns the value it panicked
// with, or nil if it completed.
func updatePanic(t *testing.T, doc *document.Document, fn func(r *json.Object)) any {
	t.Helper()

	return recovered(func() {
		require.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			fn(r)
			return nil
		}))
	})
}

// TestTreeEditRejectsMidSurrogateIndex is the issue's repro. Index 2 of
// <r><p>😀x</p></r> -- one for the <p> open, one for the emoji's high
// surrogate -- lands between the emoji's two code units. Go used to accept it
// and write "�y�x", where the JS SDK writes the two lone halves
// unchanged: same operation, different text on each replica.
func TestTreeEditRejectsMidSurrogateIndex(t *testing.T) {
	doc := midSurrogateDoc(t)
	before := doc.Marshal()

	r := updatePanic(t, doc, func(r *json.Object) {
		r.GetTree("t").Edit(2, 2, &json.TreeNode{Type: "text", Value: "y"}, 0)
	})

	assert.Equal(t, json.ErrMidSurrogatePair, r)
	assert.Equal(t, before, doc.Marshal(), "the refused edit left no trace")
}

// TestTreeEditAcceptsSurrogateBoundaries covers the indexes the validation
// must not touch: the ones on either side of the pair, and the end of the
// text. Rejecting those would make emoji unusable rather than safe.
func TestTreeEditAcceptsSurrogateBoundaries(t *testing.T) {
	for _, idx := range []int{1, 3, 4} {
		doc := midSurrogateDoc(t)
		r := updatePanic(t, doc, func(r *json.Object) {
			r.GetTree("t").Edit(idx, idx, &json.TreeNode{Type: "text", Value: "y"}, 0)
		})
		assert.Nil(t, r, "index %d is a whole-character boundary", idx)
	}
}

// TestTreeEditByPathRejectsMidSurrogateOffset covers the path-addressed twin:
// the last component of a path into a text node is a UTF-16 offset, so it
// reaches the same split as an index.
func TestTreeEditByPathRejectsMidSurrogateOffset(t *testing.T) {
	doc := midSurrogateDoc(t)

	r := updatePanic(t, doc, func(r *json.Object) {
		path := []int{0, 1} // offset 1 of the text under the first <p>

		r.GetTree("t").EditByPath(path, path, &json.TreeNode{Type: "text", Value: "y"}, 0)
	})

	assert.Equal(t, json.ErrMidSurrogatePair, r)
}

// TestTreeStyleRejectsMidSurrogateIndex covers Style and RemoveStyle, which
// take the same indexes Edit does.
func TestTreeStyleRejectsMidSurrogateIndex(t *testing.T) {
	doc := midSurrogateDoc(t)

	assert.Equal(t, json.ErrMidSurrogatePair, updatePanic(t, doc, func(r *json.Object) {
		r.GetTree("t").Style(2, 3, map[string]string{"b": "t"})
	}))
	assert.Equal(t, json.ErrMidSurrogatePair, updatePanic(t, doc, func(r *json.Object) {
		r.GetTree("t").RemoveStyle(1, 2, []string{"b"})
	}))
}

// TestTextEditRejectsMidSurrogateIndex is the Text half: TextValue.Split cuts
// the same pair the same way, with the same divergence.
func TestTextEditRejectsMidSurrogateIndex(t *testing.T) {
	doc := midSurrogateDoc(t)
	before := doc.Marshal()

	r := updatePanic(t, doc, func(r *json.Object) {
		r.GetText("s").Edit(1, 1, "y")
	})

	assert.Equal(t, json.ErrMidSurrogatePair, r)
	assert.Equal(t, before, doc.Marshal(), "the refused edit left no trace")
}

// TestTextEditAcceptsSurrogateBoundaries is the Text twin of
// TestTreeEditAcceptsSurrogateBoundaries.
func TestTextEditAcceptsSurrogateBoundaries(t *testing.T) {
	for _, idx := range []int{0, 2, 3} {
		doc := midSurrogateDoc(t)
		r := updatePanic(t, doc, func(r *json.Object) {
			r.GetText("s").Edit(idx, idx, "y")
		})
		assert.Nil(t, r, "index %d is a whole-character boundary", idx)
	}
}

// TestTextStyleRejectsMidSurrogateIndex covers Text.Style, which splits nodes
// at its range ends just as Edit does.
func TestTextStyleRejectsMidSurrogateIndex(t *testing.T) {
	doc := midSurrogateDoc(t)

	assert.Equal(t, json.ErrMidSurrogatePair, updatePanic(t, doc, func(r *json.Object) {
		r.GetText("s").Style(0, 1, map[string]string{"b": "t"})
	}))
}

// TestBMPTextUnaffected pins the common case: a document with no surrogate
// pair has no rejected index at all.
func TestBMPTextUnaffected(t *testing.T) {
	doc := document.New("bmp")
	require.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
		r.SetNewTree("t", json.TreeNode{Type: "r", Children: []json.TreeNode{{
			Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab가"}},
		}}})
		return nil
	}))

	for idx := 0; idx <= 5; idx++ {
		r := updatePanic(t, doc, func(r *json.Object) {
			r.GetTree("t").Style(idx, idx, map[string]string{"b": "t"})
		})
		assert.Nil(t, r, "index %d of a BMP-only tree", idx)
	}
}

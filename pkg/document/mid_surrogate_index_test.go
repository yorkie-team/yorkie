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

// TestTreeEditRejectsMidSurrogateIndexAfterSplit is the multi-node case: an
// earlier edit splits the text node, so the rejected index no longer lives in
// the first node and its offset has to be resolved relative to the node that
// holds it. The seam the split created is a whole-character boundary and must
// stay editable.
func TestTreeEditRejectsMidSurrogateIndexAfterSplit(t *testing.T) {
	doc := midSurrogateDoc(t)

	// Insert between the emoji and the "x", splitting <p>'s single text node.
	require.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
		r.GetTree("t").Edit(3, 3, &json.TreeNode{Type: "text", Value: "yz"}, 0)
		return nil
	}))
	before := doc.Marshal()

	// Index 2 still cuts the emoji, now in a node that is no longer alone.
	assert.Equal(t, json.ErrMidSurrogatePair, updatePanic(t, doc, func(r *json.Object) {
		r.GetTree("t").Edit(2, 2, &json.TreeNode{Type: "text", Value: "w"}, 0)
	}))
	assert.Equal(t, before, doc.Marshal(), "the refused edit left no trace")

	// The seam (3) and the offsets inside the inserted node (4, 5) are not.
	for _, idx := range []int{3, 4, 5} {
		doc := midSurrogateDoc(t)
		require.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.GetTree("t").Edit(3, 3, &json.TreeNode{Type: "text", Value: "yz"}, 0)
			return nil
		}))
		assert.Nil(t, updatePanic(t, doc, func(r *json.Object) {
			r.GetTree("t").Edit(idx, idx, &json.TreeNode{Type: "text", Value: "w"}, 0)
		}), "index %d is a whole-character boundary", idx)
	}
}

// TestTextEditRejectsMidSurrogateIndexInLaterNode is the Text twin: the
// rejected index lives in the second node, so a validation that only ever
// looked at the first node's offsets would miss it.
func TestTextEditRejectsMidSurrogateIndexInLaterNode(t *testing.T) {
	doc := midSurrogateDoc(t)

	// Append a second emoji; "s" is now "😀x😀y" across two nodes.
	require.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
		r.GetText("s").Edit(3, 3, "\U0001F600y")
		return nil
	}))
	before := doc.Marshal()

	// Index 4 is the second emoji's seam, one code unit into the second node.
	assert.Equal(t, json.ErrMidSurrogatePair, updatePanic(t, doc, func(r *json.Object) {
		r.GetText("s").Edit(4, 4, "w")
	}))
	assert.Equal(t, before, doc.Marshal(), "the refused edit left no trace")

	// The node seam itself (3) and the end (5, 6) stay editable.
	for _, idx := range []int{3, 5, 6} {
		doc := midSurrogateDoc(t)
		require.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.GetText("s").Edit(3, 3, "\U0001F600y")
			return nil
		}))
		assert.Nil(t, updatePanic(t, doc, func(r *json.Object) {
			r.GetText("s").Edit(idx, idx, "w")
		}), "index %d is a whole-character boundary", idx)
	}
}

// TestRejectedEditDiscardsClone covers what the panic leaves behind. The
// updater mutates the clone and only then hits the rejected index: the root
// never takes those mutations, so unless Update discards the clone on its way
// out, Root -- which serves the clone -- hands back a state that exists on no
// replica, and the next Update resolves its indexes against it.
func TestRejectedEditDiscardsClone(t *testing.T) {
	doc := midSurrogateDoc(t)
	before := doc.Marshal()

	assert.Equal(t, json.ErrMidSurrogatePair, updatePanic(t, doc, func(r *json.Object) {
		r.GetTree("t").Edit(4, 4, &json.TreeNode{Type: "text", Value: "dirty"}, 0)
		r.GetText("s").Edit(3, 3, "dirty")
		r.GetTree("t").Edit(2, 2, &json.TreeNode{Type: "text", Value: "w"}, 0)
	}))
	assert.Equal(t, before, doc.Marshal(), "the refused edit left no trace")

	root := doc.Root()
	assert.Equal(t, "<r><p>"+emojiText+"</p></r>", root.GetTree("t").ToXML(),
		"the clone the panic left behind was discarded")
	assert.Equal(t, emojiText, root.GetText("s").String(),
		"the clone the panic left behind was discarded")
}

// TestRootViewMutationDiscardsClone is the same contract one entry point
// over: Root hands out the mutating proxies bound to the clone, so an edit
// made through them outside an updater reaches the clone while the root --
// whose change that throwaway context never produces -- stays where it was.
// Whether the edit completes or panics on a rejected index, the clone must
// be discarded rather than serve a state no replica holds.
func TestRootViewMutationDiscardsClone(t *testing.T) {
	t.Run("edit that completes", func(t *testing.T) {
		doc := midSurrogateDoc(t)
		before := doc.Marshal()

		doc.Root().GetText("s").Edit(3, 3, "dirty")
		assert.Equal(t, before, doc.Marshal(), "the root never took the edit")
		assert.Equal(t, emojiText, doc.Root().GetText("s").String(),
			"the clone the view dirtied was discarded")

		// The next updater has to resolve its indexes against the root's
		// state, not the one the discarded clone held.
		require.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.GetText("s").Edit(3, 3, "y")
			return nil
		}))
		assert.Equal(t, emojiText+"y", doc.Root().GetText("s").String())
	})

	t.Run("edit that panics", func(t *testing.T) {
		doc := midSurrogateDoc(t)
		before := doc.Marshal()

		assert.Equal(t, json.ErrMidSurrogatePair, recovered(func() {
			tree := doc.Root().GetTree("t")
			tree.Edit(4, 4, &json.TreeNode{Type: "text", Value: "dirty"}, 0)
			tree.Edit(2, 2, &json.TreeNode{Type: "text", Value: "w"}, 0)
		}))
		assert.Equal(t, before, doc.Marshal(), "the root never took the edit")
		assert.Equal(t, "<r><p>"+emojiText+"</p></r>", doc.Root().GetTree("t").ToXML(),
			"the clone the panic left behind was discarded")
	})
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

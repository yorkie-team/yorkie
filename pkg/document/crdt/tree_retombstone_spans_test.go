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

package crdt

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestAppendUncoveredSpans pins how Retombstone merges the spans its §4.1
// cascade captured into the input spans: a node the input already describes
// is left out, and of a text piece only what no input text span of the same
// insertion covers is kept -- a recreate after GC can bring a range back as
// one piece across a boundary an input span was recorded at.
func TestAppendUncoveredSpans(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	insertion := time.NewTicket(1, 0, actor)
	element := &TreeRestoreSpan{ID: NewTreeNodeID(time.NewTicket(2, 0, actor), 0), NodeType: "p"}
	text := func(offset int, value string) *TreeRestoreSpan {
		return &TreeRestoreSpan{
			ID: NewTreeNodeID(insertion, offset), NodeType: "text", IsText: true,
			Length: len(value), Value: value,
		}
	}
	ranges := func(spans []*TreeRestoreSpan) []string {
		var out []string
		for _, span := range spans {
			out = append(out, span.ID.toIDString()+"="+span.Value)
		}
		return out
	}

	input := []*TreeRestoreSpan{element, text(2, "cd"), text(6, "gh")}
	for _, tc := range []struct {
		name     string
		cascaded *TreeRestoreSpan
		want     []*TreeRestoreSpan
	}{
		{"an element the input names", &TreeRestoreSpan{ID: element.ID, NodeType: "p"}, nil},
		{"a piece inside an input span", text(2, "c"), nil},
		{"a piece no input span touches", text(0, "ab"), []*TreeRestoreSpan{text(0, "ab")}},
		{"a piece that overlaps one input span", text(1, "bcde"), []*TreeRestoreSpan{text(1, "b"), text(4, "e")}},
		{"a piece across two input spans", text(3, "defg"), []*TreeRestoreSpan{text(4, "ef")}},
		{"an empty piece inside an input span", text(3, ""), []*TreeRestoreSpan{text(3, "")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spans := append([]*TreeRestoreSpan{}, input...)
			got := appendUncoveredSpans(spans, []*TreeRestoreSpan{tc.cascaded})
			// Input elements, then what the cascade adds, then input text.
			want := append([]*TreeRestoreSpan{input[0]}, tc.want...)
			want = append(want, input[1:]...)
			assert.Equal(t, ranges(want), ranges(got))
			for _, span := range got[1 : 1+len(tc.want)] {
				assert.Equal(t, len(span.Value), span.Length)
			}
		})
	}
}

// TestAppendUncoveredSpansIgnoresAWrongKind pins that an input span whose
// kind disagrees, which Retombstone skipped and Restore skips too, leaves a
// cascaded span naming the same node in the list: it describes nothing.
func TestAppendUncoveredSpansIgnoresAWrongKind(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	element := NewTreeNodeID(time.NewTicket(2, 0, actor), 0)
	insertion := time.NewTicket(3, 0, actor)

	wrongElement := &TreeRestoreSpan{ID: element, NodeType: "text"}
	cascadedElement := &TreeRestoreSpan{ID: element, NodeType: "p"}
	got := appendUncoveredSpans([]*TreeRestoreSpan{wrongElement}, []*TreeRestoreSpan{cascadedElement})
	assert.Equal(t, []*TreeRestoreSpan{wrongElement, cascadedElement}, got)

	wrongText := &TreeRestoreSpan{
		ID: NewTreeNodeID(insertion, 0), NodeType: "p", IsText: true, Length: 2, Value: "ab",
	}
	cascadedText := &TreeRestoreSpan{
		ID: NewTreeNodeID(insertion, 0), NodeType: "text", IsText: true, Length: 2, Value: "ab",
	}
	got = appendUncoveredSpans([]*TreeRestoreSpan{wrongText}, []*TreeRestoreSpan{cascadedText})
	assert.Equal(t, []*TreeRestoreSpan{cascadedText, wrongText}, got)
}

// TestUncoveredTextSpansAnchorEachGap pins the anchors of a cascaded text
// span cut into two parts by an input span inside it. Each part is anchored
// on its own gap, and a Restore after GC that recreates the parts before the
// input span brings the text back in order: the second part after the
// first, not after the left neighbour the whole span had.
func TestUncoveredTextSpansAnchorEachGap(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	lamport := int64(0)
	nextTicket := func() *time.Ticket {
		lamport++
		return time.NewTicket(lamport, 0, actor)
	}
	root := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "r", nil)
	p := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "p", nil)
	insertion := nextTicket()
	require.NoError(t, p.Append(NewTreeNode(NewTreeNodeID(insertion, 0), "text", nil, "abcde")))
	require.NoError(t, root.Append(p))
	tree := NewTree(root, nextTicket())

	// <r><p>abcde</p></r>: delete "bcde" and purge it.
	_, _, err = tree.EditT(2, 6, nil, 0, nextTicket(), nextTicket)
	require.NoError(t, err)
	a := p.Children(true)[0]
	removed := p.Children(true)[1]
	require.Equal(t, "a", a.Value)
	require.NoError(t, tree.Purge(removed))
	require.Equal(t, "<r><p>a</p></r>", tree.ToXML())

	at := func(offset int) *TreeNodeID { return NewTreeNodeID(insertion, offset) }
	cascaded := &TreeRestoreSpan{
		ID: at(1), NodeType: "text", IsText: true, Length: 4, Value: "bcde",
		ParentID: p.id, LeftSiblingID: a.id,
	}
	parts := uncoveredTextSpans(cascaded, [][2]int{{2, 4}})
	require.Len(t, parts, 2)
	assert.Equal(t, "b", parts[0].Value)
	assert.Equal(t, a.id, parts[0].LeftSiblingID, "the first part keeps the span's left")
	assert.Equal(t, at(2), parts[0].RightSiblingID, "and ends before the covered c")
	assert.Equal(t, "e", parts[1].Value)
	assert.Equal(t, at(3), parts[1].LeftSiblingID, "the second starts after the covered d")
	assert.Nil(t, parts[1].RightSiblingID, "and keeps the span's right")

	input := &TreeRestoreSpan{
		ID: at(2), NodeType: "text", IsText: true, Length: 2, Value: "cd",
		ParentID: p.id, LeftSiblingID: a.id,
	}
	known := time.NewVersionVector()
	known.Set(actor, lamport)
	_, _, _, _, _, err = tree.Restore(append(parts, input), false, known)
	require.NoError(t, err)
	assert.Equal(t, "<r><p>abcde</p></r>", tree.ToXML())
}

// TestRetombstoneCascadesWhateverArrivedFirst pins that the §4.1 cascade in
// Retombstone does not depend on arrival order. A peer's Enter split <p>ab</p>
// unknown to the retombstoning change, and a concurrent delete, which knew
// the split, removed the paragraph alone. One replica applies the
// retombstone first, the other the delete; both must end with the split
// product removed.
func TestRetombstoneCascadesWhateverArrivedFirst(t *testing.T) {
	build := func(t *testing.T) (*Tree, *TreeNode, *TreeNode, func() *time.Ticket, time.VersionVector) {
		t.Helper()
		actor, err := time.ActorIDFromHex("000000000000000000000001")
		require.NoError(t, err)
		peer, err := time.ActorIDFromHex("0000000000000000000000ff")
		require.NoError(t, err)
		lamport := int64(0)
		nextTicket := func() *time.Ticket {
			lamport++
			return time.NewTicket(lamport, 0, actor)
		}
		root := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "r", nil)
		p := NewTreeNode(NewTreeNodeID(nextTicket(), 0), "p", nil)
		require.NoError(t, p.Append(NewTreeNode(NewTreeNodeID(nextTicket(), 0), "text", nil, "ab")))
		require.NoError(t, root.Append(p))
		tree := NewTree(root, nextTicket())
		known := time.NewVersionVector()
		known.Set(actor, lamport)

		peerLamport := lamport
		peerTicket := func() *time.Ticket {
			peerLamport++
			return time.NewTicket(peerLamport, 0, peer)
		}
		_, _, err = tree.EditT(2, 2, nil, 1, peerTicket(), peerTicket)
		require.NoError(t, err)
		require.Equal(t, "<r><p>a</p><p>b</p></r>", tree.ToXML())
		return tree, p, tree.Root().Children()[1], nextTicket, known
	}
	retombstone := func(t *testing.T, tree *Tree, p *TreeNode, at *time.Ticket, known time.VersionVector) {
		t.Helper()
		_, _, _, _, err := tree.Retombstone([]*TreeRestoreSpan{{
			ID: p.id, NodeType: "p", ParentID: tree.Root().id,
		}}, at, false, known)
		require.NoError(t, err)
	}

	first, p1, product1, next1, known1 := build(t)
	retombstone(t, first, p1, next1(), known1)
	p1.remove(next1())

	second, p2, product2, next2, known2 := build(t)
	retombstoneAt, deleteAt := next2(), next2()
	p2.remove(deleteAt)
	retombstone(t, second, p2, retombstoneAt, known2)

	assert.True(t, product1.IsRemoved(), "retombstone first: the cascade takes the product")
	assert.True(t, product2.IsRemoved(), "delete first: so it does here")
	assert.Equal(t, first.ToXML(), second.ToXML())
}

// TestAppendUncoveredSpansKeepsParentsFirst pins the order of the merged
// spans. The input names div and productP, a child of the split product,
// but not the product itself, which only the cascade reports; the cascade's
// own productP is left out and its text kept only where the input does not
// cover it. Every span must still follow the span of its parent, or a
// Restore after GC declines the child for want of a parent.
func TestAppendUncoveredSpansKeepsParentsFirst(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	ticket := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }
	root := NewTreeNodeID(ticket(1), 0)
	div := &TreeRestoreSpan{ID: NewTreeNodeID(ticket(2), 0), NodeType: "div", ParentID: root}
	product := &TreeRestoreSpan{ID: NewTreeNodeID(ticket(3), 0), NodeType: "div", ParentID: root}
	productP := &TreeRestoreSpan{ID: NewTreeNodeID(ticket(4), 0), NodeType: "p", ParentID: product.ID}
	text := func(offset int, value string) *TreeRestoreSpan {
		return &TreeRestoreSpan{
			ID: NewTreeNodeID(ticket(5), offset), NodeType: "text", IsText: true,
			Length: len(value), Value: value, ParentID: productP.ID,
		}
	}

	input := []*TreeRestoreSpan{div, productP, text(0, "ab")}
	cascaded := []*TreeRestoreSpan{product, {ID: productP.ID, NodeType: "p", ParentID: product.ID}, text(1, "bcd")}
	got := appendUncoveredSpans(input, cascaded)

	seen := map[string]bool{root.toIDString(): true}
	var order []string
	for _, span := range got {
		require.NotNil(t, span.ParentID)
		assert.True(t, seen[span.ParentID.toIDString()], "%s comes after its parent", span.NodeType)
		if !span.IsText {
			seen[span.ID.toIDString()] = true
		}
		order = append(order, span.NodeType+"@"+span.ID.toIDString()+"="+span.Value)
	}
	assert.Len(t, got, 5, "div, product, productP, ab and the uncovered cd: %v", order)
	assert.Equal(t, "cd", got[3].Value, "the cascade's part comes before the input text")
	assert.Equal(t, "ab", got[4].Value)
}

// TestParentsFirstIsLinear pins parentsFirst on span sets a client controls:
// many spans that all name the last one as their parent, and a chain whose
// parents loop back on themselves. Depths are memoized, so the first shape
// stays linear, and the second ends without looping.
func TestParentsFirstIsLinear(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	id := func(lamport int) *TreeNodeID { return NewTreeNodeID(time.NewTicket(int64(lamport), 0, actor), 0) }

	t.Run("every span names the last one as parent", func(t *testing.T) {
		const n = 100000
		spans := make([]*TreeRestoreSpan, n)
		for i := range spans {
			spans[i] = &TreeRestoreSpan{ID: id(i + 1), NodeType: "p", ParentID: id(n)}
		}
		spans[n-1].ParentID = id(n + 1)

		sorted := parentsFirst(spans)
		require.Len(t, sorted, n)
		assert.Same(t, spans[n-1], sorted[0], "the parent comes first")
		assert.Same(t, spans[0], sorted[1], "the rest keep their order")
	})

	t.Run("parents named in a cycle", func(t *testing.T) {
		spans := []*TreeRestoreSpan{
			{ID: id(1), NodeType: "p", ParentID: id(3)},
			{ID: id(2), NodeType: "p", ParentID: id(1)},
			{ID: id(3), NodeType: "p", ParentID: id(2)},
		}
		assert.Len(t, parentsFirst(spans), 3)
	})
}

// TestAppendUncoveredSpansMatchesTheParent pins that a cascaded text piece
// is covered only by an input span of its insertion under the same parent.
// The input span recorded "abcd" under p before a split carried "cd" into
// the product; the cascade reports "cd" under the product. Both are kept,
// and the product's "cd" comes first, so a Restore after GC recreates it
// under the product and then finds it there when the input span reaches
// that range, recreating only "ab" under p.
func TestAppendUncoveredSpansMatchesTheParent(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	ticket := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }
	root := NewTreeNodeID(ticket(1), 0)
	p := &TreeRestoreSpan{ID: NewTreeNodeID(ticket(2), 0), NodeType: "p", ParentID: root}
	product := &TreeRestoreSpan{ID: NewTreeNodeID(ticket(3), 0), NodeType: "p", ParentID: root}
	text := func(offset int, value string, parent *TreeNodeID) *TreeRestoreSpan {
		return &TreeRestoreSpan{
			ID: NewTreeNodeID(ticket(4), offset), NodeType: "text", IsText: true,
			Length: len(value), Value: value, ParentID: parent,
		}
	}

	got := appendUncoveredSpans(
		[]*TreeRestoreSpan{p, text(0, "abcd", p.ID)},
		[]*TreeRestoreSpan{product, text(2, "cd", product.ID)},
	)
	var order []string
	for _, span := range got {
		order = append(order, span.NodeType+"@"+span.ParentID.toIDString()+"="+span.Value)
	}
	assert.Equal(t, []string{
		"p@" + root.toIDString() + "=",
		"p@" + root.toIDString() + "=",
		"text@" + product.ID.toIDString() + "=cd",
		"text@" + p.ID.toIDString() + "=abcd",
	}, order)
}

// TestRetombstoneAnEmptyTextSpanRemovesNothing pins that a text span of
// Length 0 retombstones nothing. Read as one character, it removed "b" of
// <p>ab</p> while its reverse, an empty span too, could not bring it back.
func TestRetombstoneAnEmptyTextSpanRemovesNothing(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	lamport := int64(0)
	next := func() *time.Ticket {
		lamport++
		return time.NewTicket(lamport, 0, actor)
	}
	root := NewTreeNode(NewTreeNodeID(next(), 0), "r", nil)
	p := NewTreeNode(NewTreeNodeID(next(), 0), "p", nil)
	text := NewTreeNode(NewTreeNodeID(next(), 0), "text", nil, "ab")
	require.NoError(t, p.Append(text))
	require.NoError(t, root.Append(p))
	tree := NewTree(root, next())

	span := &TreeRestoreSpan{
		ID: NewTreeNodeID(text.id.CreatedAt, 1), NodeType: "text", IsText: true, ParentID: p.id,
	}
	known := time.NewVersionVector()
	known.Set(actor, lamport)
	pairs, _, edits, reverse, err := tree.Retombstone([]*TreeRestoreSpan{span}, next(), true, known)
	require.NoError(t, err)
	assert.Empty(t, pairs)
	assert.Empty(t, edits)
	assert.Equal(t, "<r><p>ab</p></r>", tree.ToXML())
	require.Len(t, reverse, 1)
	assert.Equal(t, 0, reverse[0].Length)
}

// TestRetombstoneReverseSharesNoAttributes pins that the reverse spans a
// retombstone reports copy the attribute snapshot rather than alias the
// operation's: writing to one leaves the other as it was.
func TestRetombstoneReverseSharesNoAttributes(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	lamport := int64(0)
	next := func() *time.Ticket {
		lamport++
		return time.NewTicket(lamport, 0, actor)
	}
	root := NewTreeNode(NewTreeNodeID(next(), 0), "r", nil)
	p := NewTreeNode(NewTreeNodeID(next(), 0), "p", nil)
	require.NoError(t, root.Append(p))
	tree := NewTree(root, next())

	attrs := NewRHT()
	attrs.Set("k", "v", next())
	span := &TreeRestoreSpan{ID: p.id, NodeType: "p", Attributes: attrs, ParentID: root.id}
	known := time.NewVersionVector()
	known.Set(actor, lamport)
	_, _, _, reverse, err := tree.Retombstone([]*TreeRestoreSpan{span}, next(), false, known)
	require.NoError(t, err)
	require.Len(t, reverse, 1)
	require.NotSame(t, span.Attributes, reverse[0].Attributes)

	reverse[0].Attributes.Set("k", "w", next())
	assert.Equal(t, "v", span.Attributes.Get("k"))
}

// TestDeleteCascadeReportsParentsFirst pins the order of the spans the §4.1
// cascade adds to a delete's reverse. A peer's split cut
// <r><div><p>ab</p></div></r> into two divs, the product div' holding p'
// and "b"; a delete of the first div that did not know the split cascades
// to the product. Its reverse has to name div' before p' before "b": an
// undo, which knows the delete, revives a node only once its parent is live
// again, so a child listed first would stay a tombstone.
func TestDeleteCascadeReportsParentsFirst(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	peer, err := time.ActorIDFromHex("0000000000000000000000ff")
	require.NoError(t, err)
	lamport := int64(0)
	next := func() *time.Ticket {
		lamport++
		return time.NewTicket(lamport, 0, actor)
	}
	root := NewTreeNode(NewTreeNodeID(next(), 0), "r", nil)
	div := NewTreeNode(NewTreeNodeID(next(), 0), "div", nil)
	p := NewTreeNode(NewTreeNodeID(next(), 0), "p", nil)
	require.NoError(t, p.Append(NewTreeNode(NewTreeNodeID(next(), 0), "text", nil, "ab")))
	require.NoError(t, div.Append(p))
	require.NoError(t, root.Append(div))
	tree := NewTree(root, next())
	unaware := time.NewVersionVector()
	unaware.Set(actor, lamport+10)

	peerLamport := lamport
	peerTicket := func() *time.Ticket {
		peerLamport++
		return time.NewTicket(peerLamport, 0, peer)
	}
	_, _, err = tree.EditT(3, 3, nil, 2, peerTicket(), peerTicket)
	require.NoError(t, err)
	require.Equal(t, "<r><div><p>a</p></div><div><p>b</p></div></r>", tree.ToXML())

	lamport = peerLamport
	from, err := tree.FindPos(0)
	require.NoError(t, err)
	to, err := tree.FindPos(5)
	require.NoError(t, err)
	at := next()
	unaware.Set(actor, at.Lamport())
	_, _, info, err := tree.Edit(from, to, nil, 0, at, next, unaware, true)
	require.NoError(t, err)
	require.Equal(t, "<r></r>", tree.ToXML(), "the cascade took the product too")

	seen := map[string]bool{root.id.toIDString(): true}
	for _, span := range info.RemovedSpans {
		require.NotNil(t, span.ParentID)
		assert.True(t, seen[span.ParentID.toIDString()], "%s comes after its parent", span.NodeType)
		if !span.IsText {
			seen[span.ID.toIDString()] = true
		}
	}

	known := time.NewVersionVector()
	known.Set(actor, at.Lamport())
	known.Set(peer, peerLamport)
	_, _, _, _, _, err = tree.Restore(info.RemovedSpans, false, known)
	require.NoError(t, err)
	assert.Equal(t, "<r><div><p>a</p></div><div><p>b</p></div></r>", tree.ToXML(), "the undo brings all of it back")
}

// TestRetombstoneCascadeIsLinear pins the cost of the §4.1 cascade over a
// span list that names every element of one split chain. <r> holds p0 and
// its split products p1..pN, linked through InsNextID and unknown to the
// retombstoning change; a span for each one makes each cascade start one
// link further down. The call walks the chain once in all, not once per
// span, so a crafted span list over a long chain costs linear work.
func TestRetombstoneCascadeIsLinear(t *testing.T) {
	const n = 500
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	peer, err := time.ActorIDFromHex("0000000000000000000000ff")
	require.NoError(t, err)
	root := NewTreeNode(NewTreeNodeID(time.NewTicket(1, 0, actor), 0), "r", nil)
	tree := NewTree(root, time.NewTicket(2, 0, actor))
	chain := make([]*TreeNode, n+1)
	chain[0] = NewTreeNode(NewTreeNodeID(time.NewTicket(3, 0, actor), 0), "p", nil)
	for i := 1; i <= n; i++ {
		chain[i] = NewTreeNode(NewTreeNodeID(time.NewTicket(int64(10+i), 0, peer), 0), "p", nil)
	}
	var prev *TreeNode
	for _, p := range chain {
		require.NoError(t, root.Append(p))
		tree.putNode(p)
		if prev != nil {
			prev.InsNextID, p.InsPrevID = p.id, prev.id
		}
		prev = p
	}
	known := time.NewVersionVector()
	known.Set(actor, 5)

	spans := make([]*TreeRestoreSpan, 0, len(chain))
	for _, p := range chain {
		spans = append(spans, &TreeRestoreSpan{ID: p.id, NodeType: "p", ParentID: root.id})
	}
	steps := 0
	_, _, _, _, err = tree.retombstone(spans, time.NewTicket(6, 0, actor), false, known, &steps)
	require.NoError(t, err)
	for _, p := range chain {
		require.True(t, p.IsRemoved())
	}
	assert.LessOrEqual(t, steps, 2*n, "each link is walked about once, not once per span")
}

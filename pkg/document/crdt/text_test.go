/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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

package crdt_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestText(t *testing.T) {
	t.Run("marshal test", func(t *testing.T) {
		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)
		text := crdt.NewText(crdt.NewRGATreeSplit(crdt.InitialTextNode()), ctx.IssueTimeTicket())

		fromPos, toPos, _ := text.CreateRange(0, 0)
		_, _, _, _, _, err := text.Edit(fromPos, toPos, "Hello World", nil, ctx.IssueTimeTicket(), nil)
		assert.NoError(t, err)
		assert.Equal(t, `[{"val":"Hello World"}]`, text.Marshal())

		fromPos, toPos, _ = text.CreateRange(6, 11)
		_, _, _, _, _, err = text.Edit(fromPos, toPos, "Yorkie", nil, ctx.IssueTimeTicket(), nil)
		assert.NoError(t, err)
		assert.Equal(t, `[{"val":"Hello "},{"val":"Yorkie"}]`, text.Marshal())
	})

	t.Run("UTF-16 code units test", func(t *testing.T) {
		tests := []struct {
			length int
			value  string
		}{
			{4, "abcd"},
			{2, "한글"},
			{8, "अनुच्छेद"},
			{12, "🌷🎁💩😜👍🏳"},
			{10, "Ĺo͂řȩm̅"},
		}
		for _, test := range tests {
			val := crdt.NewTextValue(test.value, crdt.NewRHT())
			assert.Equal(t, test.length, val.Len())
			assert.Equal(t, test.length-2, val.Split(2).Len())

			richVal := crdt.NewTextValue(test.value, crdt.NewRHT())
			assert.Equal(t, test.length, richVal.Len())
			assert.Equal(t, test.length-2, richVal.Split(2).Len())
		}
	})

	t.Run("marshal test", func(t *testing.T) {
		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)
		text := crdt.NewText(crdt.NewRGATreeSplit(crdt.InitialTextNode()), ctx.IssueTimeTicket())

		fromPos, toPos, _ := text.CreateRange(0, 0)
		_, _, _, _, _, err := text.Edit(fromPos, toPos, "Hello World", nil, ctx.IssueTimeTicket(), nil)
		assert.NoError(t, err)
		assert.Equal(t, `[{"val":"Hello World"}]`, text.Marshal())

		fromPos, toPos, _ = text.CreateRange(6, 11)
		_, _, _, _, _, err = text.Edit(fromPos, toPos, "Yorkie", nil, ctx.IssueTimeTicket(), nil)
		assert.NoError(t, err)
		assert.Equal(t, `[{"val":"Hello "},{"val":"Yorkie"}]`, text.Marshal())

		fromPos, toPos, _ = text.CreateRange(0, 1)
		_, _, _, err = text.Style(fromPos, toPos, map[string]string{"b": "1"}, ctx.IssueTimeTicket(), nil)
		assert.NoError(t, err)
		assert.Equal(
			t,
			`[{"attrs":{"b":"1"},"val":"H"},{"val":"ello "},{"val":"Yorkie"}]`,
			text.Marshal(),
		)
	})

	t.Run("returns a born-tombstoned split's GC pair even when the other split errors", func(t *testing.T) {
		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)
		text := crdt.NewText(crdt.NewRGATreeSplit(crdt.InitialTextNode()), ctx.IssueTimeTicket())

		fromPos, toPos, _ := text.CreateRange(0, 0)
		_, _, _, _, _, err := text.Edit(fromPos, toPos, "hello world", nil, ctx.IssueTimeTicket(), nil)
		assert.NoError(t, err)

		// Tombstone "llo wo" (index 2..8), leaving "he" and "rld" live.
		fromPos, toPos, _ = text.CreateRange(2, 8)
		_, _, _, _, _, err = text.Edit(fromPos, toPos, "", nil, ctx.IssueTimeTicket(), nil)
		assert.NoError(t, err)

		var tombstoned *crdt.RGATreeSplitNode[*crdt.TextValue]
		for _, n := range text.Nodes() {
			if n.RemovedAt() != nil {
				tombstoned = n
				break
			}
		}
		require.NotNil(t, tombstoned)

		// `to` lands inside the already-tombstoned node, so splitting it
		// buffers a born-dead GC pair for the split-off piece. `from` is a
		// position that cannot be found, so the second split call fails.
		toPos = crdt.NewRGATreeSplitNodePos(tombstoned.ID(), 3)
		fromPos = crdt.NewRGATreeSplitNodePos(crdt.NewRGATreeSplitNodeID(ctx.IssueTimeTicket(), 0), 0)

		_, pairs, _, _, _, err := text.Edit(fromPos, toPos, "", nil, ctx.IssueTimeTicket(), nil)
		assert.Error(t, err)
		assert.Len(t, pairs, 1, "the pair buffered by splitting `to` must still be returned on the `from` error path")
	})

	t.Run("returns the live diff from splitting `to` even when the other split errors", func(t *testing.T) {
		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)
		text := crdt.NewText(crdt.NewRGATreeSplit(crdt.InitialTextNode()), ctx.IssueTimeTicket())

		fromPos, toPos, _ := text.CreateRange(0, 0)
		_, _, _, _, _, err := text.Edit(fromPos, toPos, "hello world", nil, ctx.IssueTimeTicket(), nil)
		assert.NoError(t, err)

		// `to` lands in the middle of the live "hello world" node, so
		// splitting it produces a non-zero metadata diff for the new node
		// it mutates the list into. `from` is a position that cannot be
		// found, so the second split call fails.
		_, toPos, _ = text.CreateRange(3, 3)
		fromPos = crdt.NewRGATreeSplitNodePos(crdt.NewRGATreeSplitNodeID(ctx.IssueTimeTicket(), 0), 0)

		_, _, diff, _, _, err := text.Edit(fromPos, toPos, "", nil, ctx.IssueTimeTicket(), nil)
		assert.Error(t, err)
		assert.Positive(t, diff.Meta, "the diff from splitting `to` must still be returned on the `from` error path")
	})
}

func TestTextEditReturnsRemoved(t *testing.T) {
	// Deleting a range must report the removed values and the spans that
	// identify them, which is what an undo needs to revive them by identity.
	root := helper.TestRoot()
	ctx := helper.TextChangeContext(root)
	text := crdt.NewText(crdt.NewRGATreeSplit(crdt.InitialTextNode()), ctx.IssueTimeTicket())

	fromPos, toPos, err := text.CreateRange(0, 0)
	require.NoError(t, err)
	seed := ctx.IssueTimeTicket()
	_, _, _, _, _, err = text.Edit(fromPos, toPos, "0123456789", nil, seed, nil)
	require.NoError(t, err)

	fromPos, toPos, err = text.CreateRange(4, 7)
	require.NoError(t, err)
	_, _, _, removedValues, removedSpans, err := text.Edit(fromPos, toPos, "", nil, ctx.IssueTimeTicket(), nil)
	require.NoError(t, err)

	require.Equal(t, []string{"456"}, removedValues)
	require.Len(t, removedSpans, 1)
	span := removedSpans[0]
	assert.Zero(t, span.CreatedAt.Compare(seed), "span identity must name the original insertion's ticket")
	assert.Equal(t, 4, span.Start)
	assert.Equal(t, 7, span.End)
	assert.Equal(t, "456", span.Content)

	// Assert identity, not just shape: the span's (CreatedAt, Start) must
	// resolve back to the actual tombstoned node in the tree.
	tombstoned := text.RGATreeSplit().FindNode(crdt.NewRGATreeSplitNodeID(seed, span.Start))
	require.NotNil(t, tombstoned, "span must name a node that exists in the tree")
	assert.NotNil(t, tombstoned.RemovedAt(), "the node the span identifies must actually be tombstoned")
	assert.Equal(t, "456", tombstoned.String(), "the identified node must hold the removed content")
}

func TestTextStyleReturnsPrevAttr(t *testing.T) {
	// Styling a range that already carries one attribute, with a call that
	// touches that attribute plus a brand-new one, must report the prior
	// value for the existing key and Existed: false for the new one, sorted
	// by key so the result is deterministic regardless of map order.
	root := helper.TestRoot()
	ctx := helper.TextChangeContext(root)
	text := crdt.NewText(crdt.NewRGATreeSplit(crdt.InitialTextNode()), ctx.IssueTimeTicket())

	fromPos, toPos, err := text.CreateRange(0, 0)
	require.NoError(t, err)
	_, _, _, _, _, err = text.Edit(fromPos, toPos, "Hello", nil, ctx.IssueTimeTicket(), nil)
	require.NoError(t, err)

	fromPos, toPos, err = text.CreateRange(0, 5)
	require.NoError(t, err)
	_, _, _, err = text.Style(fromPos, toPos, map[string]string{"b": "1"}, ctx.IssueTimeTicket(), nil)
	require.NoError(t, err)

	fromPos, toPos, err = text.CreateRange(0, 5)
	require.NoError(t, err)
	_, _, prevAttrs, err := text.Style(
		fromPos, toPos, map[string]string{"b": "2", "i": "1"}, ctx.IssueTimeTicket(), nil,
	)
	require.NoError(t, err)

	require.Equal(t, []crdt.PrevAttr{
		{Key: "b", Value: "1", Existed: true},
		{Key: "i", Existed: false},
	}, prevAttrs)
}

func TestTextRemoveStyleReturnsPrevAttr(t *testing.T) {
	// Removing a style attribute must report its prior value so a reverse
	// operation can restore it; a key that never existed has nothing to
	// reverse and must be omitted entirely (matching JS's removeStyle),
	// not reported as Existed: false.
	root := helper.TestRoot()
	ctx := helper.TextChangeContext(root)
	text := crdt.NewText(crdt.NewRGATreeSplit(crdt.InitialTextNode()), ctx.IssueTimeTicket())

	fromPos, toPos, err := text.CreateRange(0, 0)
	require.NoError(t, err)
	_, _, _, _, _, err = text.Edit(fromPos, toPos, "Hello", nil, ctx.IssueTimeTicket(), nil)
	require.NoError(t, err)

	fromPos, toPos, err = text.CreateRange(0, 5)
	require.NoError(t, err)
	_, _, _, err = text.Style(fromPos, toPos, map[string]string{"b": "1"}, ctx.IssueTimeTicket(), nil)
	require.NoError(t, err)

	fromPos, toPos, err = text.CreateRange(0, 5)
	require.NoError(t, err)
	_, _, prevAttrs, err := text.RemoveStyle(fromPos, toPos, []string{"i", "b"}, ctx.IssueTimeTicket(), nil)
	require.NoError(t, err)

	require.Equal(t, []crdt.PrevAttr{
		{Key: "b", Value: "1", Existed: true},
	}, prevAttrs)
}

func TestTextNormalizeAndRefinePos(t *testing.T) {
	// A reverse operation anchors on a normalized position -- an absolute
	// offset from the head -- and the anchor is remapped onto whatever the
	// chain looks like when it finally runs. Both halves are exercised here
	// against a chain that gets split and partly tombstoned in between.
	root := helper.TestRoot()
	ctx := helper.TextChangeContext(root)
	text := crdt.NewText(crdt.NewRGATreeSplit(crdt.InitialTextNode()), ctx.IssueTimeTicket())

	fromPos, toPos, err := text.CreateRange(0, 0)
	require.NoError(t, err)
	seed := ctx.IssueTimeTicket()
	_, _, _, _, _, err = text.Edit(fromPos, toPos, "0123456789", nil, seed, nil)
	require.NoError(t, err)

	// Index 4 sits inside the single run, so normalizing it yields offset 4
	// counted from the head rather than a position relative to the run.
	at4, _, err := text.CreateRange(4, 4)
	require.NoError(t, err)
	normalized, err := text.NormalizePos(at4)
	require.NoError(t, err)
	assert.Equal(t, 4, normalized.RelativeOffset())
	assert.Zero(t, normalized.ID().CreatedAt().Compare(time.InitialTicket),
		"a normalized position is anchored on the head")

	// Split the run and tombstone "23": the chain is now
	// ["01"]{"23"}["456789"] and the live text reads "01456789".
	//
	// refinePos does NOT preserve the character the position originally sat
	// next to. It reinterprets the offset against the text as it now reads:
	// offset 4 walked over live characters only (removed ones have length 0
	// and are stepped over without consuming any of it) lands after "0145",
	// between "5" and "6" -- index 4 of the current live text, not the
	// "3"|"4" boundary it named before the delete. So the position moves.
	//
	// That is the JS behavior (rga_tree_split.ts refinePos), ported as-is,
	// and it is what the findRestoreAnchor from-position fallback inherits.
	delFrom, delTo, err := text.CreateRange(2, 4)
	require.NoError(t, err)
	_, _, _, _, _, err = text.Edit(delFrom, delTo, "", nil, ctx.IssueTimeTicket(), nil)
	require.NoError(t, err)
	require.Equal(t, "01456789", text.String())

	refined, err := text.RefinePos(normalized)
	require.NoError(t, err)
	assert.Zero(t, refined.ID().CreatedAt().Compare(seed))
	assert.Equal(t, 4, refined.ID().Offset(), "the refined position names the live run starting at \"4\"")
	assert.Equal(t, 2, refined.RelativeOffset(),
		"offset 4 over live text is \"01\" plus 2 more characters, i.e. between \"5\" and \"6\"")

	// Refining an offset past the end of the chain snaps to the last node.
	beyond, err := text.RefinePos(crdt.NewRGATreeSplitNodePos(normalized.ID(), 100))
	require.NoError(t, err)
	assert.Zero(t, beyond.ID().CreatedAt().Compare(seed))
	assert.Equal(t, 6, beyond.RelativeOffset(), "\"456789\" is 6 characters long")
}

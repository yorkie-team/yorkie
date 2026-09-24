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

package crdt_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/resource"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestMoveAfterReportsTheMovedAtTicket pins the diff MoveAfter reports, which
// is the term the running ledger used to skip: the element's MetaSize counts
// the movedAt ticket the move stamps, so a rebuild charges it and the
// accumulator has to as well. Only the stamp costs anything.
func TestMoveAfterReportsTheMovedAtTicket(t *testing.T) {
	ticket := resource.DataSize{Meta: time.TicketSize}

	t.Run("first move of an element charges one ticket test", func(t *testing.T) {
		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)

		tA, tB, tC := ctx.IssueTimeTicket(), ctx.IssueTimeTicket(), ctx.IssueTimeTicket()
		list := buildList(t, []string{"A", "B", "C"}, []*time.Ticket{tA, tB, tC})

		_, diff, err := list.MoveAfter(tC, tA, ctx.IssueTimeTicket())
		require.NoError(t, err)
		assert.Equal(t, ticket, diff)
	})

	t.Run("moving the same element again charges nothing test", func(t *testing.T) {
		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)

		tA, tB, tC := ctx.IssueTimeTicket(), ctx.IssueTimeTicket(), ctx.IssueTimeTicket()
		list := buildList(t, []string{"A", "B", "C"}, []*time.Ticket{tA, tB, tC})

		_, first, err := list.MoveAfter(tC, tA, ctx.IssueTimeTicket())
		require.NoError(t, err)
		assert.Equal(t, ticket, first)

		// The second and third moves overwrite a ticket the element already
		// carries, so the document is no larger for them.
		for range 2 {
			_, diff, err := list.MoveAfter(tB, tA, ctx.IssueTimeTicket())
			require.NoError(t, err)
			assert.Equal(t, resource.DataSize{}, diff)
		}
	})

	t.Run("a move discarded by LWW charges nothing test", func(t *testing.T) {
		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)

		tA, tB, tC := ctx.IssueTimeTicket(), ctx.IssueTimeTicket(), ctx.IssueTimeTicket()
		tLoser, tWinner := ctx.IssueTimeTicket(), ctx.IssueTimeTicket()

		list := buildList(t, []string{"A", "B", "C"}, []*time.Ticket{tA, tB, tC})
		_, diff, err := list.MoveAfter(tC, tA, tWinner)
		require.NoError(t, err)
		assert.Equal(t, ticket, diff)

		// The loser stamps nothing -- it only mints the dead position node
		// later operations may anchor on -- so it must charge nothing.
		dead, diff, err := list.MoveAfter(tB, tA, tLoser)
		require.NoError(t, err)
		require.NotNil(t, dead)
		assert.Equal(t, resource.DataSize{}, diff)
	})
}

// TestRHTNodeChargesTheLogicalValue pins the value RHTNode.DataSize sizes. A
// client that stores a string which itself parses as JSON keeps the quotes so
// that reading it back can tell the string '1' from the number 1, and sizes the
// string it started from. Sizing the stored form here made the server and the
// SDK disagree about one document on exactly that subset.
func TestRHTNodeChargesTheLogicalValue(t *testing.T) {
	tests := []struct {
		desc   string
		stored string
		data   int
	}{
		{desc: `1. a plain string is stored raw`, stored: `red`, data: (1 + 3) * 2},
		{desc: `2. a number is stored raw`, stored: `1`, data: (1 + 1) * 2},
		{desc: `3. the string "1" keeps its quotes`, stored: `"1"`, data: (1 + 1) * 2},
		{desc: `4. so does the string "true"`, stored: `"true"`, data: (1 + 4) * 2},
		{desc: `5. an escaped string decodes once, not twice`, stored: `"\"a\""`, data: (1 + 3) * 2},
		{desc: `6. a JSON object is not a JSON string`, stored: `{"a":1}`, data: (1 + 7) * 2},
		{desc: `7. a quote that opens nothing is not JSON`, stored: `"unterminated`, data: (1 + 13) * 2},
		{desc: `8. multibyte values count their UTF-8 bytes`, stored: `빨강`, data: (1 + 6) * 2},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			root := helper.TestRoot()
			ctx := helper.TextChangeContext(root)

			rht := crdt.NewRHT()
			write := rht.Set("b", tt.stored, ctx.IssueTimeTicket())
			require.NotNil(t, write.Installed)
			assert.Equal(t, tt.stored, write.Installed.Value(), "the stored form does not move")
			assert.Equal(t, resource.DataSize{
				Data: tt.data,
				Meta: time.TicketSize,
			}, write.Installed.DataSize())
		})
	}
}

// TestRHTRemoveMintsAValuelessTombstone pins that a tombstone's bytes are a
// function of its key alone. Copying the value it replaced made them a function
// of what had landed at that key when the removal arrived, so two replicas
// holding the same document disagreed about its size by delivery order.
func TestRHTRemoveMintsAValuelessTombstone(t *testing.T) {
	root := helper.TestRoot()
	ctx := helper.TextChangeContext(root)

	// One key that held a long value, one that was never set at all.
	held := crdt.NewRHT()
	held.Set("b", "a-long-attribute-value", ctx.IssueTimeTicket())
	heldRemoval := held.Remove("b", ctx.IssueTimeTicket())

	absent := crdt.NewRHT()
	absentRemoval := absent.Remove("b", ctx.IssueTimeTicket())

	heldTombstone := heldRemoval.GCNodes[len(heldRemoval.GCNodes)-1]
	absentTombstone := absentRemoval.GCNodes[len(absentRemoval.GCNodes)-1]

	assert.Empty(t, heldTombstone.Value())
	assert.Equal(t, absentTombstone.DataSize(), heldTombstone.DataSize(),
		"a tombstone over a live value must weigh what one over an absent key weighs")

	// What the value was charging has to leave the ledger holding it, or
	// dropping it from the tombstone would strand those bytes.
	assert.Equal(t, resource.DataSize{Data: len("a-long-attribute-value") * 2},
		heldRemoval.ValueDropped)
	assert.Equal(t, resource.DataSize{}, absentRemoval.ValueDropped,
		"an absent key was charging nothing to drop")
}

// TestRemoveStyleOnATombstonedNodeDebitsGC pins the other ledger the dropped
// value can leave. canStyle admits a node removed concurrently with the change,
// so the node holding the attribute may itself be a tombstone -- and there the
// value's bytes were being carried by that node's GC charge, taken when the
// node was removed and the attribute was still live. Dropping the value from
// the tombstone without taking them back out of GC would strand them there,
// where only collection makes them visible.
func TestRemoveStyleOnATombstonedNodeDebitsGC(t *testing.T) {
	const value = "a-long-attribute-value"

	root := helper.TestRoot()
	ctx := helper.TextChangeContext(root)
	text := crdt.NewText(crdt.NewRGATreeSplit(crdt.InitialTextNode()), ctx.IssueTimeTicket())

	from, to, err := text.CreateRange(0, 0)
	require.NoError(t, err)
	_, _, _, _, _, err = text.Edit(from, to, "Hello", nil, ctx.IssueTimeTicket(), nil)
	require.NoError(t, err)

	// The range a concurrent editor holds: resolved before the delete, so it
	// still addresses the node after it becomes a tombstone.
	from, to, err = text.CreateRange(0, 5)
	require.NoError(t, err)

	styleFrom, styleTo, err := text.CreateRange(0, 5)
	require.NoError(t, err)
	_, _, _, err = text.Style(styleFrom, styleTo, map[string]string{"b": value}, ctx.IssueTimeTicket(), nil)
	require.NoError(t, err)

	delFrom, delTo, err := text.CreateRange(0, 5)
	require.NoError(t, err)
	_, _, _, _, _, err = text.Edit(delFrom, delTo, "", nil, ctx.IssueTimeTicket(), nil)
	require.NoError(t, err)

	_, size, _, err := text.RemoveStyle(from, to, []string{"b"}, ctx.IssueTimeTicket(), nil)
	require.NoError(t, err)

	assert.Equal(t, -len(value)*2, size.GC.Data,
		"the value the tombstone no longer carries has to leave the GC charge holding it")
	assert.Equal(t, resource.DataSize{}, size.Live,
		"Live never held it: the node was already a tombstone")
}

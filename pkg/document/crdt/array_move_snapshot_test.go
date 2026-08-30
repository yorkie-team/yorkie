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
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestArrayMoveThenAddSnapshotStable is the minimal deterministic regression
// for yorkie#1948: when the last element of an array was moved into its slot,
// appending more elements and then rebuilding the array through Add (which is
// how DeepCopy and snapshot restore reconstruct the list) must preserve order.
//
// Before the fix, RGATreeList.Add anchored on the last node's ELEMENT createdAt
// instead of its POSITION createdAt. For a moved last element those differ, and
// the element createdAt resolves to the element's now-dead original position
// node, so each Add landed before the previous one — reversing the appended
// run. This surfaced as a passive replica diverging after a snapshot restore.
func TestArrayMoveThenAddSnapshotStable(t *testing.T) {
	root := helper.TestRoot()
	ctx := helper.TextChangeContext(root)

	list := crdt.NewRGATreeList()

	add := func(v int) {
		p, err := crdt.NewPrimitive(v, ctx.IssueTimeTicket())
		require.NoError(t, err)
		require.NoError(t, list.Add(p))
	}

	// Build [14,15].
	p14, err := crdt.NewPrimitive(14, ctx.IssueTimeTicket())
	require.NoError(t, err)
	require.NoError(t, list.Add(p14))
	p15, err := crdt.NewPrimitive(15, ctx.IssueTimeTicket())
	require.NoError(t, err)
	pos14, err := list.PosCreatedAt(p14.CreatedAt())
	require.NoError(t, err)
	require.NoError(t, list.InsertAfter(pos14, p15, ctx.IssueTimeTicket()))
	assert.Equal(t, "[14,15]", list.Marshal())

	// Move 14 after 15, then move 15 after 14 (net order [14,15], two moves that
	// leave two dead position nodes and a moved last element).
	pos15, err := list.PosCreatedAt(p15.CreatedAt())
	require.NoError(t, err)
	_, err = list.MoveAfter(pos15, p14.CreatedAt(), ctx.IssueTimeTicket())
	require.NoError(t, err)
	pos14b, err := list.PosCreatedAt(p14.CreatedAt())
	require.NoError(t, err)
	_, err = list.MoveAfter(pos14b, p15.CreatedAt(), ctx.IssueTimeTicket())
	require.NoError(t, err)
	assert.Equal(t, "[14,15]", list.Marshal())

	// Append 26 and 66 after the moved last element.
	add(26)
	add(66)
	arr := crdt.NewArray(list, ctx.IssueTimeTicket())
	assert.Equal(t, "[14,15,26,66]", arr.Marshal())

	// DeepCopy rebuilds the list through Add — it must not reorder.
	cp, err := arr.DeepCopy()
	require.NoError(t, err)
	assert.Equal(t, "[14,15,26,66]", cp.Marshal(),
		"DeepCopy of a moved-then-appended array must preserve order (yorkie#1948)")
}

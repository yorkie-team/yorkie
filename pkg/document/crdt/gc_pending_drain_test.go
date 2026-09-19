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

// TestRetombstoneDrainsPendingGCPairs pins the one split-creating path that
// had no reason to drain before attribute tombstones became registerable.
//
// retombstone isolates LIVE pieces, and splitting a live piece used to buffer
// nothing, so returning only its own pairs was correct. It no longer is: the
// split copies the value's RHT, tombstones included, and buffers a pair per
// copy. Left in the buffer those pairs are registered by whichever operation
// drains next -- under that operation's accounting -- or never at all.
//
// This is an internal test because retombstone is not reachable from the
// document API with a boundary that falls inside a live piece: undo/redo
// spans are split-invariant and already aligned. They are client-supplied
// though, and validateRestoreIdentities checks the identity's causality, not
// the boundary's alignment.
func TestRetombstoneDrainsPendingGCPairs(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)

	textTicket := time.NewTicket(1, 0, actor)
	text := NewText(NewRGATreeSplit(InitialTextNode()), textTicket)

	from, to, err := text.CreateRange(0, 0)
	require.NoError(t, err)
	_, _, _, _, _, err = text.Edit(
		from, to, "abcdefghij", nil, time.NewTicket(2, 0, actor), nil)
	require.NoError(t, err)

	// Leave one tombstoned attribute on the single live node.
	sf, st, err := text.CreateRange(0, 10)
	require.NoError(t, err)
	_, _, _, err = text.Style(
		sf, st, map[string]string{"b": "1"}, time.NewTicket(3, 0, actor), nil)
	require.NoError(t, err)
	rf, rt, err := text.CreateRange(0, 10)
	require.NoError(t, err)
	_, _, _, err = text.RemoveStyle(
		rf, rt, []string{"b"}, time.NewTicket(4, 0, actor), nil)
	require.NoError(t, err)

	// A span whose boundaries fall strictly inside the live node, so
	// isolateRange has to split twice and each copy buffers a pair.
	node := text.rgaTreeSplit.initialHead.next
	pairs, _ := text.rgaTreeSplit.retombstone([]restoreSpanValue[*TextValue]{{
		createdAt: node.ID().CreatedAt(),
		start:     2,
		end:       8,
		value:     node.Value(),
	}}, time.NewTicket(5, 0, actor))

	assert.Empty(t, text.rgaTreeSplit.pendingGCPairs,
		"retombstone must hand back everything its splits buffered")
	// One for the retombstoned target, one per attribute copy the two
	// splits made.
	assert.Len(t, pairs, 3)
}

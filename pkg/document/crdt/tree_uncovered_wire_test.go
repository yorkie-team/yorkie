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
	"unicode/utf16"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestUncoveredTextSpansSurviveTheWire pins that every part uncoveredTextSpans
// emits has a Length equal to the UTF-16 length of its Value, which the wire
// decoder requires of a text span, so a reverse operation carrying the parts
// round-trips through the converter. The spans cover a surrogate pair cut by
// a cover and a span whose Value holds fewer code units than its Length.
func TestUncoveredTextSpansSurviveTheWire(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	insertion := time.NewTicket(1, 0, actor)
	parent := crdt.NewTreeNodeID(time.NewTicket(2, 0, actor), 0)
	text := func(length int, value string) *crdt.TreeRestoreSpan {
		return &crdt.TreeRestoreSpan{
			ID: crdt.NewTreeNodeID(insertion, 0), NodeType: "text", IsText: true,
			Length: length, Value: value, ParentID: parent,
		}
	}

	for _, tc := range []struct {
		name   string
		span   *crdt.TreeRestoreSpan
		covers [][2]int
		parts  int
	}{
		{"a cover inside a surrogate pair", text(4, "a😀b"), [][2]int{{2, 3}}, 2},
		{"a Value shorter than its Length", text(4, "ab"), [][2]int{{1, 2}}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parts := crdt.UncoveredTextSpansForTest(tc.span, tc.covers)
			require.Len(t, parts, tc.parts)
			for _, part := range parts {
				assert.Equal(t, len(utf16.Encode([]rune(part.Value))), part.Length, part.Value)
			}

			at := time.NewTicket(10, 0, actor)
			pos := crdt.NewTreePos(parent, parent)
			op := operations.NewRestoreTreeEdit(time.NewTicket(3, 0, actor), pos, pos, at,
				parts, crdt.RestoreModeRestore, nil)
			pbOps, err := converter.ToOperations([]operations.Operation{op})
			require.NoError(t, err)
			decoded, err := converter.FromOperations(pbOps)
			require.NoError(t, err, "the decoder accepts every part")
			require.Len(t, decoded, 1)
		})
	}
}

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

// wantPart is one part uncoveredTextSpans is expected to emit: the offset and
// length it recreates, the text it carries and the anchors on either side, with
// noAnchor for an end that keeps the original span's own anchor.
type wantPart struct {
	offset, length int
	value          string
	left, right    int
}

const noAnchor = -1

// TestUncoveredTextSpansSurviveTheWire pins the parts uncoveredTextSpans emits
// one by one -- offset, length, text and both anchors -- so a part it drops or
// a text it corrupts fails here, and then that the parts round-trip through the
// converter: the wire decoder requires a text span's Length to be the UTF-16
// length of its Value, so a reverse operation carrying one that is not could
// not be decoded. The cases cover an ordinary two-part split, a cover that cuts
// a surrogate pair and a span whose Value holds fewer code units than its
// Length claims.
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
		want   []wantPart
	}{
		{
			// Both sides of the cover come back, the far side anchored on the
			// covered character in front of it.
			"a cover in the middle", text(4, "abcd"), [][2]int{{1, 2}},
			[]wantPart{
				{offset: 0, length: 1, value: "a", left: noAnchor, right: 1},
				{offset: 2, length: 2, value: "cd", left: 1, right: noAnchor},
			},
		},
		{
			// The pair goes whole to the part holding its leading half rather
			// than decoding to a replacement character on either side of the
			// cut; the second part starts past it.
			"a cover inside a surrogate pair", text(4, "a😀b"), [][2]int{{2, 3}},
			[]wantPart{
				{offset: 0, length: 3, value: "a😀", left: noAnchor, right: 3},
				{offset: 3, length: 1, value: "b", left: 2, right: noAnchor},
			},
		},
		{
			// Offsets [2,4) name content this Value does not carry -- a shape
			// the wire rejects, since it requires Length to be the UTF-16
			// length of Value -- so the gap over them describes nothing and
			// emits no part. Only the part the Value does cover comes back.
			"a Value shorter than its Length", text(4, "ab"), [][2]int{{1, 2}},
			[]wantPart{{offset: 0, length: 1, value: "a", left: noAnchor, right: 1}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parts := crdt.UncoveredTextSpansForTest(tc.span, tc.covers)
			require.Len(t, parts, len(tc.want))
			for i, want := range tc.want {
				part := parts[i]
				assert.Equal(t, want.offset, part.ID.Offset, "part %d offset", i)
				assert.Equal(t, want.length, part.Length, "part %d length", i)
				assert.Equal(t, want.value, part.Value, "part %d value", i)
				assert.Equal(t, len(utf16.Encode([]rune(part.Value))), part.Length, part.Value)

				if want.left == noAnchor {
					assert.Equal(t, tc.span.LeftSiblingID, part.LeftSiblingID, "part %d left", i)
				} else {
					require.NotNil(t, part.LeftSiblingID, "part %d left", i)
					assert.Equal(t, want.left, part.LeftSiblingID.Offset, "part %d left", i)
				}
				if want.right == noAnchor {
					assert.Equal(t, tc.span.RightSiblingID, part.RightSiblingID, "part %d right", i)
				} else {
					require.NotNil(t, part.RightSiblingID, "part %d right", i)
					assert.Equal(t, want.right, part.RightSiblingID.Offset, "part %d right", i)
				}
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

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
	} {
		t.Run(tc.name, func(t *testing.T) {
			spans := append([]*TreeRestoreSpan{}, input...)
			got := appendUncoveredSpans(spans, []*TreeRestoreSpan{tc.cascaded})
			assert.Equal(t, ranges(append(append([]*TreeRestoreSpan{}, input...), tc.want...)), ranges(got))
			for _, span := range got[len(input):] {
				assert.Equal(t, len(span.Value), span.Length)
			}
		})
	}
}

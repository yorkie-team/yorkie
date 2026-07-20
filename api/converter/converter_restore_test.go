/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
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

package converter_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestRestoreSpanRoundTrip verifies that identity-preserving restore
// payloads survive the protobuf round-trip on Edit operations.
func TestRestoreSpanRoundTrip(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000000")
	assert.NoError(t, err)
	seed := time.NewTicket(1, 0, actor)
	executedAt := time.NewTicket(4, 0, actor)
	pos := crdt.NewRGATreeSplitNodePos(crdt.NewRGATreeSplitNodeID(seed, 0), 0)

	cases := []struct {
		name  string
		mode  crdt.RestoreMode
		spans []*crdt.RestoreSpan
	}{
		{
			name: "restore with two spans",
			mode: crdt.RestoreModeRestore,
			spans: []*crdt.RestoreSpan{
				{CreatedAt: seed, Start: 4, End: 6, Content: "45"},
				{CreatedAt: seed, Start: 2, End: 8, Content: "234567",
					Attributes: map[string]string{"bold": "true"}},
			},
		},
		{
			name:  "retombstone",
			mode:  crdt.RestoreModeRetombstone,
			spans: []*crdt.RestoreSpan{{CreatedAt: seed, Start: 4, End: 6, Content: "45"}},
		},
		{
			name:  "ordinary edit carries no restore payload",
			mode:  crdt.RestoreModeNone,
			spans: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var op operations.Operation
			if tc.mode == crdt.RestoreModeNone {
				op = operations.NewEdit(seed, pos, pos, "", nil, executedAt)
			} else {
				op = operations.NewRestoreEdit(seed, pos, pos, executedAt, tc.spans, tc.mode, nil)
			}

			pbOps, err := converter.ToOperations([]operations.Operation{op})
			assert.NoError(t, err)
			ops, err := converter.FromOperations(pbOps)
			assert.NoError(t, err)
			assert.Len(t, ops, 1)

			got, ok := ops[0].(*operations.Edit)
			assert.True(t, ok)
			assert.Equal(t, tc.mode, got.RestoreMode())
			assert.Len(t, got.RestoreSpans(), len(tc.spans))
			for i, span := range tc.spans {
				assert.Equal(t, span.Start, got.RestoreSpans()[i].Start)
				assert.Equal(t, span.End, got.RestoreSpans()[i].End)
				assert.Equal(t, span.Content, got.RestoreSpans()[i].Content)
				assert.True(t, span.CreatedAt.Compare(got.RestoreSpans()[i].CreatedAt) == 0)
				assert.Equal(t, span.Attributes, got.RestoreSpans()[i].Attributes)
			}
		})
	}
}

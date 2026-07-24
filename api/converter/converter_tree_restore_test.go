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
	"math"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/converter"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestTreeRestoreSpanRoundTrip verifies that identity-preserving Tree restore
// payloads survive the protobuf round-trip on TreeEdit operations, including
// the tree-specific structure (parent/left/right anchors, per-node attributes).
func TestTreeRestoreSpanRoundTrip(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000000")
	assert.NoError(t, err)
	seed := time.NewTicket(1, 0, actor)
	executedAt := time.NewTicket(4, 0, actor)
	pos := crdt.NewTreePos(crdt.NewTreeNodeID(seed, 0), crdt.NewTreeNodeID(seed, 0))

	attrs := crdt.NewRHT()
	attrs.Set("bold", "true", seed)

	elem := &crdt.TreeRestoreSpan{
		ID:             crdt.NewTreeNodeID(seed, 2),
		NodeType:       "p",
		IsText:         false,
		Attributes:     attrs,
		ParentID:       crdt.NewTreeNodeID(seed, 0),
		LeftSiblingID:  crdt.NewTreeNodeID(seed, 1),
		RightSiblingID: crdt.NewTreeNodeID(seed, 3),
	}
	text := &crdt.TreeRestoreSpan{
		ID:       crdt.NewTreeNodeID(seed, 5),
		NodeType: "text",
		IsText:   true,
		Length:   5,
		Value:    "hello",
		ParentID: crdt.NewTreeNodeID(seed, 2),
	}

	cases := []struct {
		name             string
		mode             crdt.RestoreMode
		restoreSpans     []*crdt.TreeRestoreSpan
		retombstoneSpans []*crdt.TreeRestoreSpan
	}{
		{
			name:         "restore element + text spans",
			mode:         crdt.RestoreModeRestore,
			restoreSpans: []*crdt.TreeRestoreSpan{elem, text},
		},
		{
			name:             "restore with a retombstone companion set",
			mode:             crdt.RestoreModeRestore,
			restoreSpans:     []*crdt.TreeRestoreSpan{elem},
			retombstoneSpans: []*crdt.TreeRestoreSpan{text},
		},
		{
			name:             "pure-insert reverse carries only retombstone spans",
			mode:             crdt.RestoreModeRestore,
			retombstoneSpans: []*crdt.TreeRestoreSpan{elem, text},
		},
		{
			name:             "redo (retombstone direction)",
			mode:             crdt.RestoreModeRetombstone,
			restoreSpans:     []*crdt.TreeRestoreSpan{text},
			retombstoneSpans: []*crdt.TreeRestoreSpan{elem},
		},
	}

	assertSpan := func(t *testing.T, want, got *crdt.TreeRestoreSpan) {
		t.Helper()
		assert.True(t, want.ID.Equal(got.ID))
		assert.Equal(t, want.NodeType, got.NodeType)
		assert.Equal(t, want.IsText, got.IsText)
		assert.Equal(t, want.Length, got.Length)
		assert.Equal(t, want.Value, got.Value)
		if want.ParentID != nil {
			assert.True(t, want.ParentID.Equal(got.ParentID))
		}
		if want.LeftSiblingID != nil {
			assert.True(t, want.LeftSiblingID.Equal(got.LeftSiblingID))
		}
		if want.RightSiblingID != nil {
			assert.True(t, want.RightSiblingID.Equal(got.RightSiblingID))
		}
		if want.Attributes != nil {
			assert.NotNil(t, got.Attributes)
			assert.Equal(t, want.Attributes.Marshal(), got.Attributes.Marshal())
		} else {
			assert.Nil(t, got.Attributes)
		}
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			op := operations.NewRestoreTreeEdit(seed, pos, pos, executedAt,
				tc.restoreSpans, tc.mode, tc.retombstoneSpans)

			pbOps, err := converter.ToOperations([]operations.Operation{op})
			assert.NoError(t, err)
			ops, err := converter.FromOperations(pbOps)
			assert.NoError(t, err)
			assert.Len(t, ops, 1)

			got, ok := ops[0].(*operations.TreeEdit)
			assert.True(t, ok)
			assert.Equal(t, tc.mode, got.RestoreMode())
			assert.Len(t, got.RestoreSpans(), len(tc.restoreSpans))
			assert.Len(t, got.RetombstoneSpans(), len(tc.retombstoneSpans))
			for i := range tc.restoreSpans {
				assertSpan(t, tc.restoreSpans[i], got.RestoreSpans()[i])
			}
			for i := range tc.retombstoneSpans {
				assertSpan(t, tc.retombstoneSpans[i], got.RetombstoneSpans()[i])
			}
		})
	}
}

// TestTreeRestoreSpanRejectsNilCreatedAt guards the wire boundary: a span whose
// id (or parent id) carries no created_at is malformed and must be rejected on
// deserialization, before it reaches the restore path where a nil identity
// ticket would panic on the first comparison.
func TestTreeRestoreSpanRejectsNilCreatedAt(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000000")
	assert.NoError(t, err)
	seed := time.NewTicket(1, 0, actor)
	executedAt := time.NewTicket(4, 0, actor)
	pos := crdt.NewTreePos(crdt.NewTreeNodeID(seed, 0), crdt.NewTreeNodeID(seed, 0))

	build := func() []*api.Operation {
		op := operations.NewRestoreTreeEdit(seed, pos, pos, executedAt,
			[]*crdt.TreeRestoreSpan{{
				ID:             crdt.NewTreeNodeID(seed, 2),
				NodeType:       "text",
				IsText:         true,
				Length:         1,
				Value:          "x",
				ParentID:       crdt.NewTreeNodeID(seed, 0),
				LeftSiblingID:  crdt.NewTreeNodeID(seed, 1),
				RightSiblingID: crdt.NewTreeNodeID(seed, 3),
			}}, crdt.RestoreModeRestore, nil)
		pbOps, err := converter.ToOperations([]operations.Operation{op})
		assert.NoError(t, err)
		return pbOps
	}

	t.Run("nil span id created_at", func(t *testing.T) {
		pbOps := build()
		pbOps[0].GetTreeEdit().RestoreSpans[0].Id.CreatedAt = nil
		_, err := converter.FromOperations(pbOps)
		assert.Error(t, err)
	})

	t.Run("nil parent id created_at", func(t *testing.T) {
		pbOps := build()
		pbOps[0].GetTreeEdit().RestoreSpans[0].ParentId.CreatedAt = nil
		_, err := converter.FromOperations(pbOps)
		assert.Error(t, err)
	})

	t.Run("nil left sibling created_at", func(t *testing.T) {
		pbOps := build()
		pbOps[0].GetTreeEdit().RestoreSpans[0].LeftSiblingId.CreatedAt = nil
		_, err := converter.FromOperations(pbOps)
		assert.Error(t, err)
	})

	t.Run("nil right sibling created_at", func(t *testing.T) {
		pbOps := build()
		pbOps[0].GetTreeEdit().RestoreSpans[0].RightSiblingId.CreatedAt = nil
		_, err := converter.FromOperations(pbOps)
		assert.Error(t, err)
	})
}

// TestTreeRestoreSpanRejectsBadTextLength guards the recreate slicing path: a
// text span whose Length does not match its Value (in UTF-16 code units) would
// slice out of bounds when a purged range is rebuilt, so it is rejected on read.
func TestTreeRestoreSpanRejectsBadTextLength(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000000")
	assert.NoError(t, err)
	seed := time.NewTicket(1, 0, actor)
	executedAt := time.NewTicket(4, 0, actor)
	pos := crdt.NewTreePos(crdt.NewTreeNodeID(seed, 0), crdt.NewTreeNodeID(seed, 0))

	// Length 3 but "hello" is 5 UTF-16 code units — serialization is in range,
	// but deserialization must reject the mismatch.
	op := operations.NewRestoreTreeEdit(seed, pos, pos, executedAt,
		[]*crdt.TreeRestoreSpan{{
			ID:       crdt.NewTreeNodeID(seed, 2),
			NodeType: "text",
			IsText:   true,
			Length:   3,
			Value:    "hello",
			ParentID: crdt.NewTreeNodeID(seed, 0),
		}}, crdt.RestoreModeRestore, nil)
	pbOps, err := converter.ToOperations([]operations.Operation{op})
	assert.NoError(t, err)
	_, err = converter.FromOperations(pbOps)
	assert.Error(t, err)
}

// TestTreeRestoreSpanRejectsLengthOverflow guards serialization: a Length beyond
// int32 must error rather than silently wrap to a bogus (possibly negative)
// wire value (parity with the Text toRestoreSpans bounds check).
func TestTreeRestoreSpanRejectsLengthOverflow(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000000")
	assert.NoError(t, err)
	seed := time.NewTicket(1, 0, actor)
	executedAt := time.NewTicket(4, 0, actor)
	pos := crdt.NewTreePos(crdt.NewTreeNodeID(seed, 0), crdt.NewTreeNodeID(seed, 0))

	op := operations.NewRestoreTreeEdit(seed, pos, pos, executedAt,
		[]*crdt.TreeRestoreSpan{{
			ID:       crdt.NewTreeNodeID(seed, 2),
			NodeType: "text",
			IsText:   true,
			Length:   math.MaxInt32 + 1,
			Value:    "x",
			ParentID: crdt.NewTreeNodeID(seed, 0),
		}}, crdt.RestoreModeRestore, nil)
	_, err = converter.ToOperations([]operations.Operation{op})
	assert.Error(t, err)
}

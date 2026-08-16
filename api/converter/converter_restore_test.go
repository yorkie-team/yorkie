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
	"google.golang.org/protobuf/proto"

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

// TestRestoreSpanRoundTripsRetombstoneCompanion ports restore_converter_test.ts's
// "round-trips the companion retombstone_spans of a replace reverse" (found
// missing in the Task 21 parity re-audit). The reverse of a replace revives
// the removed content (RestoreSpans) and re-removes the inserted content
// (RetombstoneSpans) in the same operation; both sets must survive the wire
// independently or a peer/server diverges on one half of the replace.
func TestRestoreSpanRoundTripsRetombstoneCompanion(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000000")
	assert.NoError(t, err)
	seed := time.NewTicket(1, 0, actor)
	executedAt := time.NewTicket(4, 0, actor)
	pos := crdt.NewRGATreeSplitNodePos(crdt.NewRGATreeSplitNodeID(seed, 0), 0)

	restoreSpans := []*crdt.RestoreSpan{{CreatedAt: seed, Start: 2, End: 4, Content: "CD"}}
	retombstoneSpans := []*crdt.RestoreSpan{{CreatedAt: seed, Start: 0, End: 2, Content: "12"}}
	op := operations.NewRestoreEdit(seed, pos, pos, executedAt,
		restoreSpans, crdt.RestoreModeRestore, retombstoneSpans)

	pbOps, err := converter.ToOperations([]operations.Operation{op})
	assert.NoError(t, err)
	ops, err := converter.FromOperations(pbOps)
	assert.NoError(t, err)
	assert.Len(t, ops, 1)

	got, ok := ops[0].(*operations.Edit)
	assert.True(t, ok)
	assert.Equal(t, crdt.RestoreModeRestore, got.RestoreMode())
	assert.Len(t, got.RestoreSpans(), 1)
	assert.Equal(t, "CD", got.RestoreSpans()[0].Content)
	assert.Len(t, got.RetombstoneSpans(), 1)
	assert.Equal(t, "12", got.RetombstoneSpans()[0].Content)
	assert.True(t, seed.Compare(got.RetombstoneSpans()[0].CreatedAt) == 0)
}

// TestRestoreSpanBaseEditCarriesNoInlineContent ports
// restore_converter_test.ts's "decodes to a harmless no-op for peers that
// ignore restore fields" (found missing in the Task 21 parity re-audit).
//
// Mixed-version interop contract: a restore/undo op carries its content only
// in RestoreSpans; its base Edit fields are a zero-width, empty-content edit
// (From === To, Content === ""). A peer or server without restore support
// drops the unknown restore fields and applies just the base edit -- which
// inserts nothing and deletes nothing. So a restore op reaching an old node
// cannot duplicate or corrupt content; at worst the old node skips the
// restore and stays diverged until upgraded. This pins that wire contract so
// a future change can't quietly start emitting inline content on the restore
// path.
func TestRestoreSpanBaseEditCarriesNoInlineContent(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000000")
	assert.NoError(t, err)
	seed := time.NewTicket(1, 0, actor)
	executedAt := time.NewTicket(4, 0, actor)
	pos := crdt.NewRGATreeSplitNodePos(crdt.NewRGATreeSplitNodeID(seed, 0), 0)

	op := operations.NewRestoreEdit(seed, pos, pos, executedAt,
		[]*crdt.RestoreSpan{{CreatedAt: seed, Start: 4, End: 6, Content: "45"}},
		crdt.RestoreModeRestore, nil)

	pbOps, err := converter.ToOperations([]operations.Operation{op})
	assert.NoError(t, err)
	pbEdit := pbOps[0].GetEdit()
	assert.Equal(t, "", pbEdit.Content,
		"restore ops carry no inline content for an old peer to re-insert")
	assert.True(t, proto.Equal(pbEdit.From, pbEdit.To),
		"restore ops are zero-width, so an old peer deletes nothing either")

	// A new peer still receives the full identity payload.
	ops, err := converter.FromOperations(pbOps)
	assert.NoError(t, err)
	got, ok := ops[0].(*operations.Edit)
	assert.True(t, ok)
	assert.Equal(t, "", got.Content())
	assert.Equal(t, crdt.RestoreModeRestore, got.RestoreMode())
	assert.Len(t, got.RestoreSpans(), 1)
}

// TestRestoreSpanRejectsNilCreatedAt guards the wire boundary: a span with no
// created_at is malformed and must be rejected on deserialization, not passed
// to the server-side restore path where a nil identity ticket would panic on
// the first comparison.
func TestRestoreSpanRejectsNilCreatedAt(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000000")
	assert.NoError(t, err)
	seed := time.NewTicket(1, 0, actor)
	executedAt := time.NewTicket(4, 0, actor)
	pos := crdt.NewRGATreeSplitNodePos(crdt.NewRGATreeSplitNodeID(seed, 0), 0)

	op := operations.NewRestoreEdit(seed, pos, pos, executedAt,
		[]*crdt.RestoreSpan{{CreatedAt: seed, Start: 4, End: 6, Content: "45"}},
		crdt.RestoreModeRestore, nil)
	pbOps, err := converter.ToOperations([]operations.Operation{op})
	assert.NoError(t, err)

	// Simulate a malformed / forged span that omits created_at.
	pbOps[0].GetEdit().RestoreSpans[0].CreatedAt = nil

	_, err = converter.FromOperations(pbOps)
	assert.Error(t, err)
}

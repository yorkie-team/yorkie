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

package converter_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/proto"

	"github.com/yorkie-team/yorkie/api/converter"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestFromOperationsRejectsMissingTicket pins the wire refusal of an operation
// that omits a ticket it cannot be interpreted without.
//
// fromTimeTicket answers (nil, nil) for an absent ticket, so before
// fromRequiredTimeTicket a crafted change could hand a nil executedAt (or
// parentCreatedAt, or an element's createdAt) to Execute, where the first
// Ticket.Compare reads a field off the nil pointer and takes the process down
// -- nothing under server/ recovers, and a change already stored would repeat
// the crash on every replay. Each case below nulls ONE field of an otherwise
// well-formed operation, so a regression that drops the guard for a single
// field is still caught.
func TestFromOperationsRejectsMissingTicket(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000000")
	assert.NoError(t, err)
	seed := time.NewTicket(1, 0, actor)
	executedAt := time.NewTicket(2, 0, actor)

	prim, err := crdt.NewPrimitive(1, seed)
	assert.NoError(t, err)
	obj := crdt.NewObject(crdt.NewElementRHT(), seed)
	textPos := crdt.NewRGATreeSplitNodePos(crdt.NewRGATreeSplitNodeID(seed, 0), 0)
	treePos := crdt.NewTreePos(crdt.NewTreeNodeID(seed, 0), crdt.NewTreeNodeID(seed, 0))

	// nullObjectBytesCreatedAt nulls the createdAt inside an element carried as
	// serialized bytes, which is the branch of fromElement that BytesToObject
	// decodes rather than the inline one.
	nullObjectBytesCreatedAt := func(pbElem *api.JSONElementSimple) {
		var pbJSON api.JSONElement
		assert.NoError(t, proto.Unmarshal(pbElem.Value, &pbJSON))
		pbJSON.GetJsonObject().CreatedAt = nil
		bytes, err := proto.Marshal(&pbJSON)
		assert.NoError(t, err)
		pbElem.Value = bytes
	}

	// Each case names the field to null on the encoded operation; nulling is
	// done on the protobuf so the operation itself stays well-formed.
	tests := []struct {
		name string
		op   operations.Operation
		null func(pbOp *api.Operation)
	}{{
		"set.parent_created_at",
		operations.NewSet(seed, "k", prim, executedAt),
		func(op *api.Operation) { op.GetSet().ParentCreatedAt = nil },
	}, {
		"set.executed_at",
		operations.NewSet(seed, "k", prim, executedAt),
		func(op *api.Operation) { op.GetSet().ExecutedAt = nil },
	}, {
		"add.prev_created_at",
		operations.NewAdd(seed, seed, prim, executedAt),
		func(op *api.Operation) { op.GetAdd().PrevCreatedAt = nil },
	}, {
		"add.executed_at",
		operations.NewAdd(seed, seed, prim, executedAt),
		func(op *api.Operation) { op.GetAdd().ExecutedAt = nil },
	}, {
		"move.created_at",
		operations.NewMove(seed, seed, seed, executedAt),
		func(op *api.Operation) { op.GetMove().CreatedAt = nil },
	}, {
		"move.executed_at",
		operations.NewMove(seed, seed, seed, executedAt),
		func(op *api.Operation) { op.GetMove().ExecutedAt = nil },
	}, {
		"remove.created_at",
		operations.NewRemove(seed, seed, executedAt),
		func(op *api.Operation) { op.GetRemove().CreatedAt = nil },
	}, {
		"remove.executed_at",
		operations.NewRemove(seed, seed, executedAt),
		func(op *api.Operation) { op.GetRemove().ExecutedAt = nil },
	}, {
		"edit.executed_at",
		operations.NewEdit(seed, textPos, textPos, "a", nil, executedAt),
		func(op *api.Operation) { op.GetEdit().ExecutedAt = nil },
	}, {
		"style.executed_at",
		operations.NewStyle(seed, textPos, textPos, map[string]string{"b": "t"}, executedAt),
		func(op *api.Operation) { op.GetStyle().ExecutedAt = nil },
	}, {
		"increase.executed_at",
		operations.NewIncrease(seed, prim, executedAt),
		func(op *api.Operation) { op.GetIncrease().ExecutedAt = nil },
	}, {
		"tree_edit.executed_at",
		operations.NewTreeEdit(seed, treePos, treePos, nil, 0, executedAt),
		func(op *api.Operation) { op.GetTreeEdit().ExecutedAt = nil },
	}, {
		"tree_style.executed_at",
		operations.NewTreeStyle(seed, treePos, treePos, map[string]string{"b": "t"}, executedAt),
		func(op *api.Operation) { op.GetTreeStyle().ExecutedAt = nil },
	}, {
		"array_set.created_at",
		operations.NewArraySet(seed, seed, prim, executedAt),
		func(op *api.Operation) { op.GetArraySet().CreatedAt = nil },
	}, {
		"array_set.executed_at",
		operations.NewArraySet(seed, seed, prim, executedAt),
		func(op *api.Operation) { op.GetArraySet().ExecutedAt = nil },
	}, {
		// The element a Set/Add/ArraySet carries is keyed by its own createdAt
		// once it reaches ElementRHT/RGATreeList, so it is as required as the
		// operation's own tickets -- inline, and inside the serialized bytes.
		"set.value.created_at",
		operations.NewSet(seed, "k", prim, executedAt),
		func(op *api.Operation) { op.GetSet().Value.CreatedAt = nil },
	}, {
		"add.value.created_at",
		operations.NewAdd(seed, seed, prim, executedAt),
		func(op *api.Operation) { op.GetAdd().Value.CreatedAt = nil },
	}, {
		"array_set.value.created_at",
		operations.NewArraySet(seed, seed, prim, executedAt),
		func(op *api.Operation) { op.GetArraySet().Value.CreatedAt = nil },
	}, {
		"set.value.object_bytes.created_at",
		operations.NewSet(seed, "k", obj, executedAt),
		func(op *api.Operation) { nullObjectBytesCreatedAt(op.GetSet().Value) },
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pbOps, err := converter.ToOperations([]operations.Operation{tc.op})
			assert.NoError(t, err)

			// The operation decodes while it is intact...
			_, err = converter.FromOperations(pbOps)
			assert.NoError(t, err)

			// ...and is refused once the ticket is gone.
			tc.null(pbOps[0])
			_, err = converter.FromOperations(pbOps)
			assert.ErrorIs(t, err, converter.ErrMissingTimeTicket)
		})
	}
}

// TestNormalizeRepairsIncreaseValueCreatedAt pins the one ticket rejection
// that needed a stored-side repair.
//
// fromElement now requires created_at on every element, and fromIncrease
// decodes its delta through it. Unlike a Set or an Add, an Increase never keys
// its value by that ticket -- Execute reads the number and hands the pointer
// to crdt.NewPrimitive without dereferencing it -- so a change stored before
// the guard existed executed fine and must keep loading. The wire still
// refuses it; NormalizeStoredOperations fills it from the operation's own
// executed_at, which every replica derives identically from the same message.
func TestNormalizeRepairsIncreaseValueCreatedAt(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000000")
	assert.NoError(t, err)
	seed := time.NewTicket(1, 0, actor)
	executedAt := time.NewTicket(2, 0, actor)

	prim, err := crdt.NewPrimitive(1, seed)
	assert.NoError(t, err)

	pbOps, err := converter.ToOperations([]operations.Operation{
		operations.NewIncrease(seed, prim, executedAt),
	})
	assert.NoError(t, err)
	pbOps[0].GetIncrease().Value.CreatedAt = nil

	// The wire refuses it, where there is still a client to reject.
	_, err = converter.FromOperations(pbOps)
	assert.ErrorIs(t, err, converter.ErrMissingTimeTicket)

	// The stored side repairs it instead, so the document stays loadable.
	converter.NormalizeStoredOperations(pbOps)
	assert.True(t, proto.Equal(pbOps[0].GetIncrease().ExecutedAt, pbOps[0].GetIncrease().Value.CreatedAt))

	ops, err := converter.FromOperations(pbOps)
	assert.NoError(t, err)
	assert.Len(t, ops, 1)
	assert.Equal(t, executedAt, ops[0].(*operations.Increase).Value().CreatedAt())
}

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
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/yorkie-team/yorkie/api/converter"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestTreeEditRejectsAbsentSplitTicket covers the list a TreeEdit carries for
// the nodes an element split creates. Each entry becomes a split node's
// CreatedAt, which crdt.Tree keys NodeMapByID on, so an absent one is a nil
// TreeNodeID.CreatedAt rather than a tolerable gap. The stored counterpart
// discards the whole list instead of rejecting: TreeEdit.Execute falls back to
// reconstructing every ticket, and only the whole-list fallback is free of the
// delimiter collision a carried prefix plus the fallback would produce.
func TestTreeEditRejectsAbsentSplitTicket(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000000")
	require.NoError(t, err)
	seed := time.NewTicket(1, 0, actor)
	executedAt := time.NewTicket(4, 0, actor)
	pos := crdt.NewTreePos(crdt.NewTreeNodeID(seed, 0), crdt.NewTreeNodeID(seed, 0))

	op := operations.NewTreeEdit(seed, pos, pos, nil, 1, executedAt)
	op.SetSplitTickets([]*time.Ticket{executedAt, nil})
	pbOps, err := converter.ToOperations([]operations.Operation{op})
	require.NoError(t, err)

	_, err = converter.FromOperations(pbOps)
	assert.ErrorIs(t, err, converter.ErrMissingTicket)

	converter.NormalizeStoredOperations(pbOps)
	ops, err := converter.FromOperations(pbOps)
	require.NoError(t, err)
	assert.Empty(t, ops[0].(*operations.TreeEdit).SplitTickets())
}

// TestElementRejectsAbsentCreatedAt covers the payload the operation carries
// rather than the operation's own tickets. ElementRHT and RGATreeList key on
// the element's createdAt (v.CreatedAt().Key()), so an absent one is a nil
// dereference the moment the operation is applied -- server-side, in the
// background snapshot goroutine, on every later replay.
func TestElementRejectsAbsentCreatedAt(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000000")
	require.NoError(t, err)
	seed := time.NewTicket(1, 0, actor)
	executedAt := time.NewTicket(4, 0, actor)

	t.Run("inline element simple test", func(t *testing.T) {
		primitive, err := crdt.NewPrimitive("a", executedAt)
		require.NoError(t, err)
		pbOps, err := converter.ToOperations([]operations.Operation{
			operations.NewSet(seed, "k", primitive, executedAt),
		})
		require.NoError(t, err)

		pbOps[0].GetSet().Value.CreatedAt = nil
		_, err = converter.FromOperations(pbOps)
		assert.ErrorIs(t, err, converter.ErrMissingTicket)
	})

	// The object/array/tree payloads arrive as element bytes and go through the
	// same decoder that reads a server-built snapshot; unlike a snapshot they
	// are wholly client-supplied.
	t.Run("element bytes test", func(t *testing.T) {
		bytes, err := proto.Marshal(&api.JSONElement{
			Body: &api.JSONElement_JsonObject{JsonObject: &api.JSONElement_JSONObject{}},
		})
		require.NoError(t, err)

		_, err = converter.BytesToObject(bytes)
		assert.ErrorIs(t, err, converter.ErrMissingTicket)
	})
}

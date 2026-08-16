/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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

package database_test

import (
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

func TestChangeInfo(t *testing.T) {
	t.Run("comparing actorID equals after calling ToChange test", func(t *testing.T) {
		actorID := time.ActorID{}
		_, err := rand.Read(actorID.Bytes())
		assert.NoError(t, err)

		expectedID := actorID.String()
		changeInfo := database.ChangeInfo{
			ActorID: types.ID(expectedID),
		}

		change, err := changeInfo.ToChange()
		assert.NoError(t, err)
		assert.Equal(t, change.ID().ActorID().String(), expectedID)
	})
}

// TestChangeInfoDecodesOperationsRejectedOnTheWire pins the asymmetry between
// the two FromOperations callers. Validation added at the wire boundary also
// lands on ChangeInfo.ToChange, which reads changes written before that
// validation existed -- and there, rejecting one makes the whole document
// permanently unloadable. Each case asserts both halves: the wire path still
// rejects, and the stored path still loads.
func TestChangeInfoDecodesOperationsRejectedOnTheWire(t *testing.T) {
	// storedChange packs pb operations the way they sit in storage: marshalled
	// bytes, decoded back through ToChange rather than handed over directly.
	storedChange := func(t *testing.T, pbOps []*api.Operation) *database.ChangeInfo {
		t.Helper()

		actorID := time.ActorID{}
		_, err := rand.Read(actorID.Bytes())
		assert.NoError(t, err)

		var raw [][]byte
		for _, pbOp := range pbOps {
			bytesOp, err := proto.Marshal(pbOp)
			assert.NoError(t, err)
			raw = append(raw, bytesOp)
		}

		return &database.ChangeInfo{ActorID: types.ID(actorID.String()), Operations: raw}
	}

	actor, err := time.ActorIDFromHex("000000000000000000000000")
	assert.NoError(t, err)
	seed := time.NewTicket(1, 0, actor)
	executedAt := time.NewTicket(4, 0, actor)
	pos := crdt.NewTreePos(crdt.NewTreeNodeID(seed, 0), crdt.NewTreeNodeID(seed, 0))

	// pbTreeEdit builds a fresh ordinary TreeEdit each case mutates. The two
	// cases cannot share one operation: a restore mode returns before the
	// split level is ever read.
	pbTreeEdit := func(t *testing.T) ([]*api.Operation, *api.Operation_TreeEdit) {
		t.Helper()

		op := operations.NewTreeEdit(seed, pos, pos, nil, 0, executedAt)
		pbOps, err := converter.ToOperations([]operations.Operation{op})
		assert.NoError(t, err)
		return pbOps, pbOps[0].GetTreeEdit()
	}

	t.Run("stored negative split level clamps to zero test", func(t *testing.T) {
		pbOps, treeEdit := pbTreeEdit(t)
		treeEdit.SplitLevel = -1

		_, err := converter.FromOperations(pbOps)
		assert.ErrorIs(t, err, converter.ErrInvalidSplitLevel)

		// Zero is what a negative level already meant: nothing read it beyond
		// the split loop, which does nothing for a non-positive level.
		c, err := storedChange(t, pbOps).ToChange()
		require.NoError(t, err)
		assert.Equal(t, 0, c.Operations()[0].(*operations.TreeEdit).SplitLevel())
	})

	t.Run("stored restore span attribute without updatedAt is dropped test", func(t *testing.T) {
		pbOps, treeEdit := pbTreeEdit(t)
		treeEdit.RestoreMode = api.RestoreMode_RESTORE_MODE_RESTORE
		treeEdit.RestoreSpans = []*api.TreeRestoreSpan{{
			Id:       treeEdit.From.ParentId,
			NodeType: "text",
			IsText:   true,
			Length:   1,
			Value:    "a",
			Attributes: map[string]*api.NodeAttr{
				"dated":   {Value: "kept", UpdatedAt: converter.ToTimeTicket(executedAt)},
				"undated": {Value: "dropped"},
			},
		}}

		_, err := converter.FromOperations(pbOps)
		assert.ErrorIs(t, err, converter.ErrInvalidRestoreSpan)

		// Dropping the one attribute keeps the document readable; keeping it
		// would reach the RHT with a nil updatedAt and panic on the first
		// comparison. The well-formed sibling must survive.
		c, err := storedChange(t, pbOps).ToChange()
		require.NoError(t, err)

		spans := c.Operations()[0].(*operations.TreeEdit).RestoreSpans()
		assert.Len(t, spans, 1)
		assert.Equal(t, "kept", spans[0].Attributes.Get("dated"))
		assert.False(t, spans[0].Attributes.Has("undated"))
	})
}

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

package packs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

// TestServerPackSanitizesStoredOperations pins the pull path to the same
// repair-and-drop rules the server's own read path applies. ToPBChangePack
// forwards stored operation bytes without decoding them into models, so
// anything it ships raw is decoded by the client through the strict wire path
// (FromChangePack -> FromChanges -> FromOperations) -- which rejects exactly
// what ChangeInfo.ToChange tolerates. Both halves are asserted here: the raw
// bytes fail the wire decode, and what ToPBChangePack actually ships passes it.
func TestServerPackSanitizesStoredOperations(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000000")
	require.NoError(t, err)
	seed := time.NewTicket(1, 0, actor)
	executedAt := time.NewTicket(4, 0, actor)
	pos := crdt.NewTreePos(crdt.NewTreeNodeID(seed, 0), crdt.NewTreeNodeID(seed, 0))

	undated := operations.NewTreeEdit(seed, pos, pos, nil, 0, executedAt)
	kept := operations.NewTreeEdit(seed, pos, pos, nil, 0, executedAt)
	pbOps, err := converter.ToOperations([]operations.Operation{undated, kept})
	require.NoError(t, err)
	// A shape a change written before the check existed could carry, and the
	// one no repair exists for.
	pbOps[0].GetTreeEdit().ExecutedAt = nil
	// And one the stored path repairs rather than drops.
	pbOps[1].GetTreeEdit().SplitLevel = -1

	require.Error(t, func() error {
		_, err := converter.FromOperations(pbOps)
		return err
	}())

	var raw [][]byte
	for _, pbOp := range pbOps {
		bytesOp, err := proto.Marshal(pbOp)
		require.NoError(t, err)
		raw = append(raw, bytesOp)
	}

	pack := NewServerPack("d1", change.Checkpoint{}, []*database.ChangeInfo{{
		ActorID:    types.ID(actor.String()),
		Operations: raw,
	}}, nil)

	pbPack, err := pack.ToPBChangePack()
	require.NoError(t, err)
	require.Len(t, pbPack.Changes, 1)

	// What the client does with what it receives.
	changes, err := converter.FromChanges(pbPack.Changes)
	require.NoError(t, err)
	require.Len(t, changes, 1)
	require.Len(t, changes[0].Operations(), 1)
	assert.Equal(t, 0, changes[0].Operations()[0].(*operations.TreeEdit).SplitLevel())
}

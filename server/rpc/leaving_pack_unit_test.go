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

package rpc

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/rpc/auth"
)

// leavingPack builds a pack of count changes. When refuseLast is set, the
// last of them is crafted into a payload the push boundary refuses.
func leavingPack(t *testing.T, count int, refuseLast bool) *api.ChangePack {
	t.Helper()

	doc := document.New("leaving")
	doc.SetActor(time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1})
	for i := range count {
		require.NoError(t, doc.Update(func(r *json.Object, _ *presence.Presence) error {
			r.SetString(strconv.Itoa(i), "1")
			return nil
		}))
	}

	pbPack, err := converter.ToChangePack(doc.CreateChangePack())
	require.NoError(t, err)
	require.Len(t, pbPack.Changes, count)

	if refuseLast {
		// A value created after its own operation; see
		// converter.ValidatePushedOperations.
		set := pbPack.Changes[count-1].Operations[0].GetSet()
		require.NotNil(t, set)
		set.Value.CreatedAt = converter.ToTimeTicket(time.NewTicket(time.MaxLamport, 0, doc.ActorID()))
	}

	return pbPack
}

// TestLeavingChangePackAuthorizesBeforeDropping pins the order DetachDocument
// and RemoveDocument depend on: fromLeavingChangePack hands back the pack as
// the client sent it, changes included, so auth.VerifyAccess judges a refused
// leave as a write. Only after that does the caller drop the changes. Were the
// changes dropped first, a read-only token could detach with a refused pack
// and the write would never be authorized.
func TestLeavingChangePackAuthorizesBeforeDropping(t *testing.T) {
	ctx := logging.With(context.Background(), logging.DefaultLogger())

	t.Run("a pack the boundary takes", func(t *testing.T) {
		pack, refusedFrom, err := fromLeavingChangePack(ctx, "c1", leavingPack(t, 1, false))
		require.NoError(t, err)
		assert.Equal(t, -1, refusedFrom)
		assert.True(t, pack.HasChanges())
	})

	t.Run("a refused pack is still a write when it is authorized", func(t *testing.T) {
		pack, refusedFrom, err := fromLeavingChangePack(ctx, "c1", leavingPack(t, 1, true))
		require.NoError(t, err)
		require.Equal(t, 0, refusedFrom, "the crafted pack must reach the lenient path")

		// What DetachDocument and RemoveDocument pass to auth.VerifyAccess,
		// evaluated before they truncate pack.Changes.
		assert.True(t, pack.HasChanges())
		assert.Equal(t, []types.AccessAttribute{{
			Key:  "leaving",
			Verb: types.ReadWrite,
		}}, auth.AccessAttributes(pack))

		// And the truncation the callers then apply leaves nothing of it.
		pack.Changes = pack.Changes[:refusedFrom]
		assert.Equal(t, types.Read, auth.AccessAttributes(pack)[0].Verb)
	})
}

// TestLeavingChangePackKeepsChangesBeforeTheRefusedOne pins that one refused
// change does not take the rest of a leaving pack with it. The changes the
// client queued before it are legitimate, and a detach or remove is the last
// chance they have to reach the document; everything from the refused change
// on is dropped, since a gap in the middle would fail the clientSeq
// continuity packs.PushPull requires.
func TestLeavingChangePackKeepsChangesBeforeTheRefusedOne(t *testing.T) {
	ctx := logging.With(context.Background(), logging.DefaultLogger())

	pack, refusedFrom, err := fromLeavingChangePack(ctx, "c1", leavingPack(t, 3, true))
	require.NoError(t, err)
	require.Equal(t, 2, refusedFrom)

	pack.Changes = pack.Changes[:refusedFrom]
	assert.Len(t, pack.Changes, 2)
	for i, cn := range pack.Changes {
		assert.Equal(t, uint32(i+1), cn.ClientSeq(), "the kept changes stay continuous")
	}
}

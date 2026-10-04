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

// leavingPack builds a pack of one change, refused by the push boundary when
// refuse is set.
func leavingPack(t *testing.T, refuse bool) *api.ChangePack {
	t.Helper()

	doc := document.New("leaving")
	doc.SetActor(time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1})
	require.NoError(t, doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetString("a", "1")
		return nil
	}))

	pbPack, err := converter.ToChangePack(doc.CreateChangePack())
	require.NoError(t, err)
	require.Len(t, pbPack.Changes, 1)

	if refuse {
		// A value created after its own operation; see
		// converter.ValidatePushedOperations.
		set := pbPack.Changes[0].Operations[0].GetSet()
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
		pack, refused, err := fromLeavingChangePack(ctx, "c1", leavingPack(t, false))
		require.NoError(t, err)
		assert.False(t, refused)
		assert.True(t, pack.HasChanges())
	})

	t.Run("a refused pack is still a write when it is authorized", func(t *testing.T) {
		pack, refused, err := fromLeavingChangePack(ctx, "c1", leavingPack(t, true))
		require.NoError(t, err)
		require.True(t, refused, "the crafted pack must reach the lenient path")

		// What DetachDocument and RemoveDocument pass to auth.VerifyAccess,
		// evaluated before they nil out pack.Changes.
		assert.True(t, pack.HasChanges())
		assert.Equal(t, []types.AccessAttribute{{
			Key:  "leaving",
			Verb: types.ReadWrite,
		}}, auth.AccessAttributes(pack))

		// And the drop the callers then apply leaves nothing of it.
		pack.Changes = nil
		assert.Equal(t, types.Read, auth.AccessAttributes(pack)[0].Verb)
	})
}

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

package converter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func TestReissueOperations(t *testing.T) {
	to, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	from := time.InitialActorID

	t.Run("re-issue tickets of a nested value and keep the lamport-0 root ticket", func(t *testing.T) {
		objAt := time.NewTicket(1, 1, from)
		obj := crdt.NewObject(crdt.NewElementRHT(), objAt)
		inner, err := crdt.NewPrimitive("v", time.NewTicket(1, 2, from))
		require.NoError(t, err)
		obj.Set("k", inner)
		set := operations.NewSet(time.InitialTicket, "o", obj, time.NewTicket(1, 3, from))

		ops, err := ReissueOperations([]operations.Operation{set}, from, to)
		require.NoError(t, err)
		require.Len(t, ops, 1)

		got := ops[0].(*operations.Set)
		assert.Equal(t, time.InitialTicket.Key(), got.ParentCreatedAt().Key())
		assert.Equal(t, time.NewTicket(1, 3, to).Key(), got.ExecutedAt().Key())
		value := got.Value().(*crdt.Object)
		assert.Equal(t, time.NewTicket(1, 1, to).Key(), value.CreatedAt().Key())
		assert.Equal(t, time.NewTicket(1, 2, to).Key(), value.Get("k").CreatedAt().Key())

		// The input is not written into.
		assert.Equal(t, from, set.ExecutedAt().ActorID())
		assert.Equal(t, from, obj.CreatedAt().ActorID())
	})

	t.Run("keep a Text value's content", func(t *testing.T) {
		text := crdt.NewText(crdt.NewRGATreeSplit(crdt.InitialTextNode()), time.NewTicket(1, 1, from))
		fromPos, toPos, err := text.CreateRange(0, 0)
		require.NoError(t, err)
		_, _, _, _, _, err = text.Edit(fromPos, toPos, "hi", nil, time.NewTicket(1, 2, from), nil)
		require.NoError(t, err)
		set := operations.NewSet(time.InitialTicket, "t", text, time.NewTicket(1, 3, from))

		ops, err := ReissueOperations([]operations.Operation{set}, from, to)
		require.NoError(t, err)
		got := ops[0].(*operations.Set).Value().(*crdt.Text)
		assert.Equal(t, "hi", got.String())
		assert.Equal(t, to, got.CreatedAt().ActorID())
	})

	t.Run("rename an actor-keyed map entry", func(t *testing.T) {
		pbFrom := ToTimeTicket(time.NewTicket(2, 1, from))
		edit := &api.Operation_Edit{
			ExecutedAt: ToTimeTicket(time.NewTicket(2, 2, from)),
			CreatedAtMapByActor: map[string]*api.TimeTicket{
				from.String(): pbFrom,
			},
		}
		r := ticketReissuer{
			from: from.Bytes(),
			to:   to.Bytes(),
			fromKeys: map[string]string{
				from.String():       to.String(),
				from.StringBase64(): to.StringBase64(),
			},
		}
		require.NoError(t, r.walk(edit.ProtoReflect()))

		assert.NotContains(t, edit.CreatedAtMapByActor, from.String())
		require.Contains(t, edit.CreatedAtMapByActor, to.String())
		assert.Equal(t, to.Bytes(), edit.CreatedAtMapByActor[to.String()].ActorId)
		assert.Equal(t, to.Bytes(), edit.ExecutedAt.ActorId)
	})
}

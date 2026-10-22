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

	r := ticketReissuer{
		from: from.Bytes(),
		to:   to.Bytes(),
		fromKeys: map[string]string{
			from.String():       to.String(),
			from.StringBase64(): to.StringBase64(),
		},
	}

	t.Run("rename an actor-keyed map entry", func(t *testing.T) {
		// VersionVector.vector is the protocol's only actor-keyed map, and no
		// operation carries one -- created_at_map_by_actor is deprecated and
		// never written -- so this walks the message directly rather than
		// through an operation, which is what ReissueOperations hands the
		// walk. It holds both key forms at once because rekeyMap has to
		// rename whichever one it finds, not the one the field happens to use.
		vector := &api.VersionVector{Vector: map[string]int64{
			from.String():       1,
			from.StringBase64(): 2,
		}}
		require.NoError(t, r.walk(vector.ProtoReflect()))

		assert.NotContains(t, vector.Vector, from.String())
		assert.NotContains(t, vector.Vector, from.StringBase64())
		assert.Equal(t, int64(1), vector.Vector[to.String()])
		assert.Equal(t, int64(2), vector.Vector[to.StringBase64()])
	})

	t.Run("re-issue the tickets inside a message-valued map", func(t *testing.T) {
		// TreeNode.attributes is map<string, NodeAttr>, the live
		// message-valued map an operation does carry: the walk has to descend
		// into each value to reach the NodeAttr's own ticket, and to keep
		// doing so for an entry rekeyMap has just renamed.
		pbOp := &api.Operation{Body: &api.Operation_TreeEdit_{
			TreeEdit: &api.Operation_TreeEdit{
				ExecutedAt: ToTimeTicket(time.NewTicket(2, 2, from)),
				Contents: []*api.TreeNodes{{Content: []*api.TreeNode{{
					Attributes: map[string]*api.NodeAttr{
						"bold": {
							Value:     "true",
							UpdatedAt: ToTimeTicket(time.NewTicket(2, 3, from)),
						},
						from.String(): {
							Value:     "x",
							UpdatedAt: ToTimeTicket(time.NewTicket(2, 4, from)),
						},
					},
				}}}},
			},
		}}
		require.NoError(t, r.walk(pbOp.ProtoReflect()))

		treeEdit := pbOp.GetTreeEdit()
		assert.Equal(t, to.Bytes(), treeEdit.ExecutedAt.ActorId)

		attrs := treeEdit.Contents[0].Content[0].Attributes
		assert.Equal(t, to.Bytes(), attrs["bold"].UpdatedAt.ActorId)
		assert.NotContains(t, attrs, from.String())
		require.Contains(t, attrs, to.String())
		assert.Equal(t, to.Bytes(), attrs[to.String()].UpdatedAt.ActorId)
	})
}

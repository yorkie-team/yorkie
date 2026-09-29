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

// TestRefusedMemberInSnapshotStaysLoadable pins that an object member the
// ElementRHT refuses is rejected in a pushed payload but dropped everywhere
// else.
//
// All of them are decoded by fromJSONObject. A pushed payload is a change the
// client can be told about, so refusing it is the point. A snapshot or a
// stored change has no other source: failing its decode makes the document
// unloadable for the server and for every client that attaches. The refused member is a loser no
// key reaches, so dropping it leaves the document's content unchanged -- which
// is how the decoder read it before the refusal existed.
func TestRefusedMemberInSnapshotStaysLoadable(t *testing.T) {
	actor := time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	ticket := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }

	// The occupant of k was created at 5 and positioned at 12. The second
	// member of k was created at 12: it ties the occupant's positionedAt, so
	// it loses, and 12 does not follow its own createdAt, so it cannot be
	// tombstoned. The RHT refuses it.
	member := func(createdAt, movedAt int64) *api.RHTNode {
		obj := crdt.NewObject(crdt.NewElementRHT(), ticket(1))
		value, err := crdt.NewPrimitive(createdAt, ticket(createdAt))
		require.NoError(t, err)
		obj.Set("k", value)
		if movedAt != 0 {
			value.SetMovedAt(ticket(movedAt))
		}
		bytes, err := converter.ObjectToBytes(obj)
		require.NoError(t, err)
		pbElem := &api.JSONElement{}
		require.NoError(t, proto.Unmarshal(bytes, pbElem))
		return pbElem.GetJsonObject().Nodes[0]
	}
	pbObj := &api.JSONElement_JSONObject{
		Nodes:     []*api.RHTNode{member(5, 12), member(12, 0)},
		CreatedAt: converter.ToTimeTicket(time.InitialTicket),
	}

	t.Run("a snapshot drops the refused member", func(t *testing.T) {
		bytes, err := proto.Marshal(&api.Snapshot{
			Root: &api.JSONElement{Body: &api.JSONElement_JsonObject{JsonObject: pbObj}},
		})
		require.NoError(t, err)

		root, _, err := converter.BytesToSnapshot(bytes)
		require.NoError(t, err, "a stored snapshot became unloadable")
		assert.Equal(t, `{"k":5}`, root.Marshal())
	})

	t.Run("a pushed payload is rejected, a stored one is not", func(t *testing.T) {
		value, err := proto.Marshal(&api.JSONElement{
			Body: &api.JSONElement_JsonObject{JsonObject: pbObj},
		})
		require.NoError(t, err)

		pbOps, err := converter.ToOperations([]operations.Operation{
			operations.NewSet(time.InitialTicket, "o", crdt.NewObject(crdt.NewElementRHT(), ticket(20)), ticket(20)),
		})
		require.NoError(t, err)
		pbOps[0].GetSet().Value.Value = value

		assert.ErrorIs(t, converter.ValidatePushedOperations(pbOps), converter.ErrRefusedMember)

		// A stored or pulled change was already accepted: it decodes with the
		// member dropped, as it did before the refusal existed.
		ops, err := converter.FromStoredOperations(pbOps)
		require.NoError(t, err)
		assert.Len(t, ops, 1)
	})
}

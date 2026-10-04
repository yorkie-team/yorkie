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

	"github.com/yorkie-team/yorkie/api/converter"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestRejectsAcausalElementIdentity covers the push boundary for the element
// identity an operation carries. The ticket arrives verbatim off the wire and
// decides control flow once the operation runs -- whether a container refuses
// the value, and whether the operation refuses it first -- so a client must
// not be able to name one freely. A change issues all of its tickets at one
// lamport, so a payload whose createdAt outruns the operation carrying it
// names an identity no history the sender has observed could contain.
//
// As with every other wire rejection, the stored paths must stay loadable:
// rejecting a change already written would make its document permanently
// unloadable, and the server replays the same log on every rebuild.
func TestRejectsAcausalElementIdentity(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000000")
	require.NoError(t, err)

	executedAt := time.NewTicket(4, 0, actor)
	parentAt := time.NewTicket(1, 0, actor)

	// An element dated after the operation that carries it.
	forged, err := crdt.NewPrimitive("forged", time.NewTicket(9, 0, actor))
	require.NoError(t, err)
	// An element dated before it -- the undo-restore shape, the one that
	// legitimately reuses a ticket.
	restored, err := crdt.NewPrimitive("restored", time.NewTicket(2, 0, actor))
	require.NoError(t, err)

	pbOf := func(t *testing.T, op operations.Operation) []*api.Operation {
		t.Helper()

		pbOps, err := converter.ToOperations([]operations.Operation{op})
		require.NoError(t, err)
		return pbOps
	}

	for _, tc := range []struct {
		name   string
		forged operations.Operation
		sound  operations.Operation
	}{
		{
			"set",
			operations.NewSet(parentAt, "k", forged, executedAt),
			operations.NewSet(parentAt, "k", restored, executedAt),
		},
		{
			"add",
			operations.NewAdd(parentAt, parentAt, forged, executedAt),
			operations.NewAdd(parentAt, parentAt, restored, executedAt),
		},
		{
			"array set",
			operations.NewArraySet(parentAt, parentAt, forged, executedAt),
			operations.NewArraySet(parentAt, parentAt, restored, executedAt),
		},
	} {
		t.Run(tc.name+" test", func(t *testing.T) {
			// 01. the wire boundary rejects the forged identity.
			pbForged := pbOf(t, tc.forged)
			_, err := converter.FromOperations(pbForged)
			assert.ErrorIs(t, err, converter.ErrAcausalElementIdentity)

			// 02. an identity older than the operation is the restore shape
			// and passes.
			decoded, err := converter.FromOperations(pbOf(t, tc.sound))
			assert.NoError(t, err)
			assert.Len(t, decoded, 1)

			// 03. the stored path drops the operation rather than failing the
			// whole read, so a document written before the check stays
			// loadable -- and rebuildable, which is the same thing on the
			// server.
			stored, err := converter.FromStoredOperations(pbForged)
			assert.NoError(t, err)
			assert.Empty(t, stored)
			assert.Empty(t, converter.SanitizeStoredOperations(pbForged))
		})
	}
}

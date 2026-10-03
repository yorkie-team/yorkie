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
	goerrors "errors"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/converter"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// treeEditWithContentLamport builds the protobuf for a TreeEdit whose content
// node claims contentLamport while the edit itself runs at executedLamport.
func treeEditWithContentLamport(t *testing.T, contentLamport, executedLamport int64) []*api.Operation {
	t.Helper()

	actor := time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	ticket := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }

	paragraph := crdt.NewTreeNode(crdt.NewTreeNodeID(ticket(contentLamport), 0), "p", nil)
	text := crdt.NewTreeNode(crdt.NewTreeNodeID(ticket(contentLamport), 0), "text", nil, "hi")
	assert.NoError(t, paragraph.Append(text))

	pos := crdt.NewTreePos(
		crdt.NewTreeNodeID(ticket(1), 0),
		crdt.NewTreeNodeID(ticket(1), 0),
	)
	pbOps, err := converter.ToOperations([]operations.Operation{
		operations.NewTreeEdit(
			ticket(1), pos, pos,
			[]*crdt.TreeNode{paragraph},
			0, ticket(executedLamport),
		),
	})
	assert.NoError(t, err)

	return pbOps
}

// A content node id arrives verbatim from the wire and is trusted from then
// on, and crdt.Tree's GC barrier answers from it: a tombstone stays linked
// while a chain node's createdAt is outside the collecting vector. That hold
// lifts because every attached client's lamport eventually climbs past any
// lamport a real change reached — so a ticket whose lamport no change ever
// reached would pin the tombstones below it, and the storage behind them, for
// the life of the document. Every node in an edit's content is minted by that
// edit, so a lamport above the edit's own is malformed.
func TestTreeEditRejectsContentLamportAheadOfChange(t *testing.T) {
	_, err := converter.FromOperations(treeEditWithContentLamport(t, math.MaxInt64, 5))
	assert.Error(t, err)
	assert.True(t, goerrors.Is(err, converter.ErrInvalidContentTicket))

	// A content ticket at or below the edit's own lamport is what a
	// well-formed client sends, and stays accepted.
	decoded, err := converter.FromOperations(treeEditWithContentLamport(t, 5, 5))
	assert.NoError(t, err)
	assert.Len(t, decoded, 1)
}

// The stored path has the opposite job: rejecting a change already written
// makes the document holding it permanently unloadable. Clamp the inflated
// ticket to the edit's own lamport instead, which is what a well-formed edit
// would have carried.
func TestStoredTreeEditClampsContentLamport(t *testing.T) {
	pbOps := treeEditWithContentLamport(t, math.MaxInt64, 5)

	decoded, err := converter.FromStoredOperations(pbOps)
	assert.NoError(t, err)
	assert.Len(t, decoded, 1)

	edit, ok := decoded[0].(*operations.TreeEdit)
	assert.True(t, ok)
	assert.Len(t, edit.Contents(), 1)
	assert.Equal(t, int64(5), edit.Contents()[0].ID().CreatedAt.Lamport())
}

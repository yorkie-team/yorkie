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

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// The wire format carries removedAt on every tree node, but a TreeEdit's
// content is always freshly created by the editing client, so it can never
// legitimately arrive tombstoned: Tree.Edit is what tombstones content, and
// only when the parent it lands in is removed. A node born tombstoned under a
// live parent would be counted into the live data size with no GC pair ever
// registered for it, so drop the tombstone on the way in, along with the
// index length it withheld from its ancestors.
func TestTreeEditContentDropsTombstone(t *testing.T) {
	actor := time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	ticket := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }

	paragraph := crdt.NewTreeNode(crdt.NewTreeNodeID(ticket(2), 0), "p", nil)
	text := crdt.NewTreeNode(crdt.NewTreeNodeID(ticket(3), 0), "text", nil, "hello")
	assert.NoError(t, paragraph.Append(text))

	// Poison the content the way a crafted operation would: both nodes born
	// tombstoned.
	paragraph.SetRemovedAt(ticket(4))
	text.SetRemovedAt(ticket(4))

	pos := crdt.NewTreePos(
		crdt.NewTreeNodeID(ticket(1), 0),
		crdt.NewTreeNodeID(ticket(1), 0),
	)
	pbOps, err := converter.ToOperations([]operations.Operation{
		operations.NewTreeEdit(
			ticket(1), pos, pos,
			[]*crdt.TreeNode{paragraph},
			0, ticket(5),
		),
	})
	assert.NoError(t, err)

	decoded, err := converter.FromOperations(pbOps)
	assert.NoError(t, err)
	assert.Len(t, decoded, 1)

	edit, ok := decoded[0].(*operations.TreeEdit)
	assert.True(t, ok)
	assert.Len(t, edit.Contents(), 1)

	content := edit.Contents()[0]
	assert.False(t, content.IsRemoved(), "content should not arrive tombstoned")
	children := content.Index.Children(true)
	assert.Len(t, children, 1)
	assert.False(t, children[0].Value.IsRemoved(),
		"content descendant should not arrive tombstoned")

	// The revived text has to be back in its parent's visible length: the
	// decode path excludes removed children from it, so a tombstone cleared
	// without that bookkeeping would leave the content sized as if empty.
	assert.Equal(t, len("hello")+2, content.Index.PaddedLength())
}

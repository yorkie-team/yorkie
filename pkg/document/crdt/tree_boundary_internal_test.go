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

package crdt

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestDropSplitLinksClearsBoundaryIndex checks that stripping the boundary
// records from a built tree also forgets them in anchoredBefore. Otherwise a
// later purge of the anchor would relink the product and give it a record
// back that no peer decoding the same payload has.
func TestDropSplitLinksClearsBoundaryIndex(t *testing.T) {
	actor := time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	ticket := func(lamport int64) *time.Ticket { return time.NewTicket(lamport, 0, actor) }

	root := NewTreeNode(NewTreeNodeID(ticket(1), 0), "r", nil)
	first := NewTreeNode(NewTreeNodeID(ticket(2), 0), "p", nil)
	text := NewTreeNode(NewTreeNodeID(ticket(3), 0), "text", nil, "a")
	product := NewTreeNode(NewTreeNodeID(ticket(4), 0), "p", nil)
	require.NoError(t, first.Append(text))
	require.NoError(t, root.Append(first, product))
	product.BoundaryAnchor = text.ID()

	tree := NewTree(root, ticket(1))
	require.Len(t, tree.anchoredBefore, 1)

	DropSplitLinksInElement(tree)
	assert.False(t, hasBoundaryRecord(product))
	assert.Empty(t, tree.anchoredBefore)

	tree.relinkAnchors(text)
	assert.False(t, hasBoundaryRecord(product), "a purge must not bring the record back")
}

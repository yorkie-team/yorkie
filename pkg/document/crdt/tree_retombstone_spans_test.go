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

// TestRetombstoneAnEmptyTextSpanRemovesNothing pins that a text span of
// Length 0 retombstones nothing. Read as one character, it removed "b" of
// <p>ab</p> while its reverse, an empty span too, could not bring it back.
func TestRetombstoneAnEmptyTextSpanRemovesNothing(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	lamport := int64(0)
	next := func() *time.Ticket {
		lamport++
		return time.NewTicket(lamport, 0, actor)
	}
	root := NewTreeNode(NewTreeNodeID(next(), 0), "r", nil)
	p := NewTreeNode(NewTreeNodeID(next(), 0), "p", nil)
	text := NewTreeNode(NewTreeNodeID(next(), 0), "text", nil, "ab")
	require.NoError(t, p.Append(text))
	require.NoError(t, root.Append(p))
	tree := NewTree(root, next())

	span := &TreeRestoreSpan{
		ID: NewTreeNodeID(text.id.CreatedAt, 1), NodeType: "text", IsText: true, ParentID: p.id,
	}
	known := time.NewVersionVector()
	known.Set(actor, lamport)
	pairs, _, edits, reverse, err := tree.Retombstone([]*TreeRestoreSpan{span}, next(), true)
	require.NoError(t, err)
	assert.Empty(t, pairs)
	assert.Empty(t, edits)
	assert.Equal(t, "<r><p>ab</p></r>", tree.ToXML())
	require.Len(t, reverse, 1)
	assert.Equal(t, 0, reverse[0].Length)
}

// TestRetombstoneReverseSharesNoAttributes pins that the reverse spans a
// retombstone reports copy the attribute snapshot rather than alias the
// operation's: writing to one leaves the other as it was.
func TestRetombstoneReverseSharesNoAttributes(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	lamport := int64(0)
	next := func() *time.Ticket {
		lamport++
		return time.NewTicket(lamport, 0, actor)
	}
	root := NewTreeNode(NewTreeNodeID(next(), 0), "r", nil)
	p := NewTreeNode(NewTreeNodeID(next(), 0), "p", nil)
	require.NoError(t, root.Append(p))
	tree := NewTree(root, next())

	attrs := NewRHT()
	attrs.Set("k", "v", next())
	span := &TreeRestoreSpan{ID: p.id, NodeType: "p", Attributes: attrs, ParentID: root.id}
	known := time.NewVersionVector()
	known.Set(actor, lamport)
	_, _, _, reverse, err := tree.Retombstone([]*TreeRestoreSpan{span}, next(), false)
	require.NoError(t, err)
	require.Len(t, reverse, 1)
	require.NotSame(t, span.Attributes, reverse[0].Attributes)

	reverse[0].Attributes.Set("k", "w", next())
	assert.Equal(t, "v", span.Attributes.Get("k"))
}

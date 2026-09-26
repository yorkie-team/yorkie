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

// TestLeftAnchorIDEmptyText covers a text node with no characters, which a
// local edit cannot create but a remote peer's contents can carry: it has no
// last character, so the anchor must not land one code unit before its start.
func TestLeftAnchorIDEmptyText(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)

	empty := NewTreeNode(NewTreeNodeID(time.NewTicket(9, 0, actor), 0), "text", nil, "")
	anchor := leftAnchorID(empty)
	assert.Equal(t, 0, anchor.Offset)
	assert.Equal(t, 0, anchor.Compare(empty.id))

	// A text node with characters still anchors on its last one.
	text := NewTreeNode(NewTreeNodeID(time.NewTicket(10, 0, actor), 3), "text", nil, "abc")
	assert.Equal(t, 5, leftAnchorID(text).Offset)
}

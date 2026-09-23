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

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestLeftAnchorID pins the offset a restore span stores as its left-sibling
// anchor. The last-char arithmetic underflows for a text node with no
// characters, and the wire decoder rejects a negative offset -- so a span
// built beside one would be refused by the server on every retry, wedging the
// pushing client. Local edits cannot create an empty text node, but a remote
// peer's decoded contents can.
func TestLeftAnchorID(t *testing.T) {
	createdAt := time.NewTicket(1, 0, time.InitialActorID)

	t.Run("text sibling anchors on its last character test", func(t *testing.T) {
		node := NewTreeNode(NewTreeNodeID(createdAt, 3), "text", nil, "ab")
		assert.Equal(t, 4, leftAnchorID(node).Offset)
	})

	t.Run("empty text sibling anchors on its own offset test", func(t *testing.T) {
		node := NewTreeNode(NewTreeNodeID(createdAt, 0), "text", nil, "")
		assert.Equal(t, 0, leftAnchorID(node).Offset)
	})

	t.Run("element sibling anchors on its own id test", func(t *testing.T) {
		node := NewTreeNode(NewTreeNodeID(createdAt, 2), "p", nil)
		assert.Equal(t, 2, leftAnchorID(node).Offset)
	})
}

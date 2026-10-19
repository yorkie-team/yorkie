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

// TestRelinkRecreatedTextNeverPointsAtItself pins the self-link guard: when
// the surviving predecessor still points at the recreated piece's own ID,
// left by an earlier node under it, the recreated piece does not take that
// as its successor.
func TestRelinkRecreatedTextNeverPointsAtItself(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	root := NewTreeNode(NewTreeNodeID(time.NewTicket(1, 0, actor), 0), "r", nil)
	p := NewTreeNode(NewTreeNodeID(time.NewTicket(2, 0, actor), 0), "p", nil)
	require.NoError(t, root.Append(p))
	tree := NewTree(root, time.NewTicket(3, 0, actor))
	insertion := time.NewTicket(4, 0, actor)
	a := NewTreeNode(NewTreeNodeID(insertion, 0), "text", nil, "a")
	b := NewTreeNode(NewTreeNodeID(insertion, 1), "text", nil, "b")
	require.NoError(t, p.Append(a, b))
	a.InsNextID = b.id
	tree.putNode(a)
	tree.putNode(b)

	tree.relinkRecreatedText(b)
	assert.Equal(t, b.id, a.InsNextID)
	assert.Equal(t, a.id, b.InsPrevID)
	assert.Nil(t, b.InsNextID, "b does not point at itself")
}

/*
 * Copyright 2023 The Yorkie Authors. All rights reserved.
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

package crdt_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

var (
	initialTreePos = &crdt.TreePos{
		CreatedAt: time.InitialTicket,
		Offset:    0,
	}
)

func TestTreeNode(t *testing.T) {
	t.Run("inline node test", func(t *testing.T) {
		node := crdt.NewTreeNode(initialTreePos, "text", "hello")
		assert.Equal(t, initialTreePos, node.Pos)
		assert.Equal(t, "text", node.Type)
		assert.Equal(t, "hello", node.Value)
		assert.Equal(t, 5, node.Len())
		assert.Equal(t, true, node.IsInline())
		assert.Equal(t, false, node.IsRemoved())
	})

	t.Run("block node test", func(t *testing.T) {
		para := crdt.NewTreeNode(initialTreePos, "paragraph")
		assert.NoError(t, para.Append(crdt.NewTreeNode(initialTreePos, "text", "helloyorkie")))
		// assert.Equal(t, crdt.ToXML(para), "<p>helloyorkie</p>")
		assert.Equal(t, 11, para.Len())
		assert.Equal(t, false, para.IsInline())

		// left := para.Child(0)
		// right := left.Split(5)
		// assert.Equal(t, crdt.ToXML(para), "<p>helloyorkie</p>")
		// assert.Equal(t, 11, para.Len())

		// assert.Equal(t, left.Value, "hello")
		// assert.Equal(t, right.Value, "yorkie")
		// assert.Equal(t, &crdt.TreePos{CreatedAt: time.InitialTicket, Offset: 0}, left.Pos)
		// assert.Equal(t, &crdt.TreePos{CreatedAt: time.InitialTicket, Offset: 5}, right.Pos)
	})
}

func TestTree(t *testing.T) {

}

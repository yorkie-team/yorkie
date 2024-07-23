//go:build bench

/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
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

package bench

import (
	"testing"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/test/helper"
)

func BenchmarkTreeEditing(b *testing.B) {
	BenchmarkTreeConverting(b)
}

// bench mark converting from protobuf tree nodes to crdt.TreeNode
func BenchmarkTreeConverting(b *testing.B) {
	b.Run("10 vertex tree converting test", func(b *testing.B) {
		TreeConverting(10, b)
	})

	b.Run("100 vertex tree converting test", func(b *testing.B) {
		TreeConverting(100, b)
	})

	b.Run("1000 vertex tree converting test", func(b *testing.B) {
		TreeConverting(1000, b)
	})

	b.Run("10000 vertex tree converting test", func(b *testing.B) {
		TreeConverting(10000, b)
	})
}

// MakeTree is a helper function to create simple tree.
func MakeTree(vertexCnt int) []*api.TreeNode {
	var chd []json.TreeNode
	for i := 0; i < vertexCnt; i++ {
		chd = append(chd, json.TreeNode{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "a"}}})
	}

	root := helper.BuildTreeNode(&json.TreeNode{
		Type:     "r",
		Children: chd,
	})

	pbNodes := converter.ToTreeNodes(root)
	return pbNodes
}

func TreeConverting(vertexCnt int, b *testing.B) {
	b.StopTimer()
	pbNodes := MakeTree(vertexCnt)
	b.StartTimer()

	converter.FromTreeNodes(pbNodes)
}

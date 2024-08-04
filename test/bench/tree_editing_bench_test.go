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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/test/helper"
)

func BenchmarkTree(b *testing.B) {
	verticesCounts := []int{10000, 20000, 30000}

	for _, cnt := range verticesCounts {
		root := buildTree(cnt)
		b.ResetTimer()

		b.Run(fmt.Sprintf("%d vertices to protobuf", cnt), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_ = converter.ToTreeNodes(root)
			}
		})

		b.Run(fmt.Sprintf("%d vertices from protobuf", cnt), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				pbNodes := converter.ToTreeNodes(root)
				_, err := converter.FromTreeNodes(pbNodes)
				assert.NoError(b, err)
			}
		})
	}
}

// buildTree creates a tree with the given number of vertices.
func buildTree(vertexCnt int) *crdt.TreeNode {
	children := make([]json.TreeNode, vertexCnt)
	for i := 0; i < vertexCnt; i++ {
		children[i] = json.TreeNode{
			Type: "p", Children: []json.TreeNode{{Type: "text", Value: "a"}},
		}
	}

	return helper.BuildTreeNode(&json.TreeNode{
		Type:     "r",
		Children: children,
	})
}

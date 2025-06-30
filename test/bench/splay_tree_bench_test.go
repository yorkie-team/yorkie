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
	"crypto/rand"
	gojson "encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"testing"

	"github.com/yorkie-team/yorkie/pkg/splay"
)

type stringValue struct {
	content string
	removed bool
}

func newSplayNode(content string) *splay.Node[*stringValue] {
	return splay.NewNode(&stringValue{
		content: content,
	})
}

func (v *stringValue) Len() int {
	if v.removed {
		return 0
	}
	return len(v.content)
}

func (v *stringValue) String() string {
	return v.content
}

type editingTrace struct {
	Edits     [][]interface{} `json:"edits"`
	FinalText string          `json:"finalText"`
}

func BenchmarkSplayTree(b *testing.B) {
	for _, cnt := range []int{100000, 200000, 300000} {
		b.Run(fmt.Sprintf("stress test %d", cnt), func(b *testing.B) {
			// find, insert, delete
			tree := splay.NewTree[*stringValue](nil)
			treeSize := 1
			for range cnt {
				maxVal := big.NewInt(3)
				operation, err := rand.Int(rand.Reader, maxVal)
				if err != nil {
					b.Fatal(err)
				}

				if int(operation.Int64()) == 0 {
					tree.Insert(newSplayNode("A"))
					treeSize++
				} else if int(operation.Int64()) == 1 {
					maxVal = big.NewInt(int64(treeSize))
					index, err := rand.Int(rand.Reader, maxVal)
					if err != nil {
						b.Fatal(err)
					}
					_, _, _ = tree.Find(int(index.Int64()))
				} else {
					maxVal = big.NewInt(int64(treeSize))
					index, err := rand.Int(rand.Reader, maxVal)
					if err != nil {
						b.Fatal(err)
					}
					node, _, _ := tree.Find(int(index.Int64()))
					if node != nil {
						tree.Delete(node)
						treeSize--
					}
				}
			}
		})
	}

	for _, cnt := range []int{100000, 200000, 300000} {
		b.Run(fmt.Sprintf("random access %d", cnt), func(_ *testing.B) {
			// Create a skewed tree by inserting characters only at the very end.
			b.StopTimer()
			tree := splay.NewTree[*stringValue](nil)
			for range cnt {
				tree.Insert(newSplayNode("A"))
			}
			b.StartTimer()

			// 1000 times random access
			for range 1000 {
				maxVal := big.NewInt(int64(cnt))
				index, err := rand.Int(rand.Reader, maxVal)
				if err != nil {
					b.Fatal(err)
				}
				_, _, _ = tree.Find(int(index.Int64()))
			}
		})
	}

	b.Run("editing trace bench", func(b *testing.B) {
		b.StopTimer()

		var editingTrace editingTrace

		file, err := os.Open("./editing-trace.json")
		if err != nil {
			b.Fatal(err)
		}
		defer func() {
			if err = file.Close(); err != nil {
				b.Fatal(err)
			}
		}()

		byteValue, err := io.ReadAll(file)
		if err != nil {
			b.Fatal(err)
		}

		if err = gojson.Unmarshal(byteValue, &editingTrace); err != nil {
			b.Fatal(err)
		}

		b.StartTimer()
		tree := splay.NewTree[*stringValue](nil)
		for _, edit := range editingTrace.Edits {
			cursor := int(edit[0].(float64))
			mode := int(edit[1].(float64))

			if mode == 0 {
				strValue, ok := edit[2].(string)
				node, _, err := tree.Find(cursor)
				if ok && err != nil && node != nil {
					tree.InsertAfter(node, newSplayNode(strValue))
				}
			} else {
				node, _, err := tree.Find(cursor)
				if err != nil && node != nil {
					tree.Delete(node)
				}
			}
		}
	})
}

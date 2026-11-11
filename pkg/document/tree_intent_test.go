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

package document_test

import (
	"fmt"
	"math/rand"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// treeReplicas gives n replicas the same tree <doc><p>base</p></doc>.
func treeReplicas(t *testing.T, n int, base string) []*document.Document {
	t.Helper()
	docs := make([]*document.Document, n)
	for i := range docs {
		a, err := time.ActorIDFromHex(fmt.Sprintf("%024x", i+1))
		require.NoError(t, err)
		docs[i] = document.New("tree-intent")
		docs[i].SetActor(a)
	}
	require.NoError(t, docs[0].Update(func(root *json.Object, _ *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: base}}},
		}})
		return nil
	}))
	intentSyncAll(t, docs)
	return docs
}

func treeXMLOf(d *document.Document) string {
	return d.Root().GetTree("t").ToXML()
}

func treeEdit(t *testing.T, d *document.Document, from, to int, content *json.TreeNode) {
	t.Helper()
	require.NoError(t, d.Update(func(root *json.Object, _ *presence.Presence) error {
		root.GetTree("t").Edit(from, to, content, 0)
		return nil
	}))
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

// TestTreeIntentInterleaving measures interleaving of concurrent runs in a
// Tree, the Text #2309 scenario: (1) characters typed into one paragraph at
// the same (or a neighbouring) caret, by the same strategies as the Text
// property test; (2) sibling <p> elements inserted at the same place in the
// parent, each paragraph a one-character marker, typed forward or backward.
func TestTreeIntentInterleaving(t *testing.T) {
	trials := 300
	if os.Getenv("ORIGIN_TREE_TRIALS") != "" {
		fmt.Sscan(os.Getenv("ORIGIN_TREE_TRIALS"), &trials)
	}
	alphabets := []string{"가나다라마바", "abcdef", "ABCDEF"}

	for _, n := range []int{2, 3} {
		for _, strategy := range []string{"forward", "backward", "random"} {
			for _, neighbouring := range []bool{false, true} {
				name := fmt.Sprintf("text replicas=%d strategy=%s neighbouring=%v", n, strategy, neighbouring)
				t.Run(name, func(t *testing.T) {
					interleaved, diverged := 0, 0
					for seed := range trials {
						rnd := rand.New(rand.NewSource(int64(seed + 1)))
						docs := treeReplicas(t, n, "0123456789")
						runs := make([]string, n)
						for i, d := range docs {
							start := 5
							if neighbouring {
								start = 4 + rnd.Intn(3)
							}
							chars := []rune(alphabets[i])[:2+rnd.Intn(4)]
							var run []rune
							for _, c := range chars {
								off := 0
								switch strategy {
								case "forward":
									off = len(run)
								case "random":
									off = rnd.Intn(len(run) + 1)
								}
								at := 1 + start + off // +1 for the <p> open tag
								treeEdit(t, d, at, at, &json.TreeNode{Type: "text", Value: string(c)})
								run = append(run[:off], append([]rune{c}, run[off:]...)...)
							}
							runs[i] = string(run)
						}
						intentSyncAll(t, docs)
						merged := treeXMLOf(docs[0])
						div := false
						for _, d := range docs[1:] {
							if treeXMLOf(d) != merged {
								div = true
							}
						}
						if div {
							diverged++
							continue
						}
						text := tagRe.ReplaceAllString(merged, "")
						for _, r := range runs {
							if !strings.Contains(text, r) {
								interleaved++
								break
							}
						}
					}
					t.Logf("TREESTAT %s interleaved=%d/%d diverged=%d", name, interleaved, trials, diverged)
					require.Zero(t, diverged)
					if crdt.TreeOrigins {
						require.Zero(t, interleaved)
					}
				})
			}
		}

		for _, strategy := range []string{"forward", "backward"} {
			name := fmt.Sprintf("elements replicas=%d strategy=%s", n, strategy)
			t.Run(name, func(t *testing.T) {
				interleaved, diverged := 0, 0
				for seed := range trials {
					rnd := rand.New(rand.NewSource(int64(seed + 1)))
					docs := treeReplicas(t, n, "X")
					runs := make([]string, n)
					for i, d := range docs {
						chars := []rune(alphabets[i])[:2+rnd.Intn(4)]
						var run []rune
						for k, c := range chars {
							// After the first paragraph (<p>X</p> spans 0..3).
							// Each new paragraph is 3 positions wide.
							at := 3
							if strategy == "forward" {
								at = 3 + 3*k
							}
							treeEdit(t, d, at, at, &json.TreeNode{Type: "p", Children: []json.TreeNode{{Type: "text", Value: string(c)}}})
							if strategy == "forward" {
								run = append(run, c)
							} else {
								run = append([]rune{c}, run...)
							}
						}
						runs[i] = string(run)
					}
					intentSyncAll(t, docs)
					merged := treeXMLOf(docs[0])
					for _, d := range docs[1:] {
						if treeXMLOf(d) != merged {
							diverged++
							break
						}
					}
					text := tagRe.ReplaceAllString(merged, "")
					for _, r := range runs {
						if !strings.Contains(text, r) {
							interleaved++
							break
						}
					}
				}
				t.Logf("TREESTAT %s interleaved=%d/%d diverged=%d", name, interleaved, trials, diverged)
				require.Zero(t, diverged)
				if crdt.TreeOrigins {
					require.Zero(t, interleaved)
				}
			})
		}
	}
}

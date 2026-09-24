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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// boldSpanReplicas returns n replicas seeded with a bold span, so a style can
// be removed from it.
func boldSpanReplicas(t *testing.T, n int) []*document.Document {
	t.Helper()

	docs := make([]*document.Document, n)
	for i := range docs {
		actor, err := time.ActorIDFromHex(fmt.Sprintf("%024d", i+1))
		require.NoError(t, err)
		docs[i] = document.New("test-doc")
		docs[i].SetActor(actor)
	}

	require.NoError(t, docs[0].Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{
			Type: "doc",
			Children: []json.TreeNode{{
				Type: "p",
				Children: []json.TreeNode{{
					Type:       "span",
					Attributes: map[string]string{"bold": "true"},
					Children:   []json.TreeNode{{Type: "text", Value: "abcde"}},
				}},
			}},
		})
		return nil
	}))
	exchangeInOrder(t, docs, [][]int{{}, {0}}[:n])

	return docs
}

// RemoveStyle runs the §7.5 advance on both range anchors, the same as Style.
// Nothing covered that advance, so these pin the shapes that reach it: the
// anchors do move here, and the replicas have to agree whichever side of the
// concurrent split arrives first.
func TestTreeRemoveStyleOverAConcurrentlySplitBoundary(t *testing.T) {
	split := func(tree *json.Tree) {
		tree.EditByPath([]int{0, 0, 3}, []int{0, 0, 3}, nil, 1)
	}
	unbold := func(tree *json.Tree) {
		tree.RemoveStyleByPath([]int{0, 0}, []int{0, 1}, []string{"bold"})
	}

	t.Run("one replica splits, the other removes the style", func(t *testing.T) {
		docs := boldSpanReplicas(t, 2)
		require.NoError(t, docs[0].Update(func(root *json.Object, p *presence.Presence) error {
			split(root.GetTree("t"))
			return nil
		}))
		require.NoError(t, docs[1].Update(func(root *json.Object, p *presence.Presence) error {
			unbold(root.GetTree("t"))
			return nil
		}))
		exchangeInOrder(t, docs, [][]int{{1}, {0}})

		// The removal was issued against the unsplit span, so the advance has
		// to carry its right anchor past the split product the other replica
		// made: both halves come back unbolded, on both replicas. Pinning the
		// content rather than only replica-to-replica equality is what makes a
		// removal that quietly stops removing fail here.
		const want = `<doc><p><span>abc</span><span>de</span></p></doc>`
		assert.Equal(t, want, docs[0].Root().GetTree("t").ToXML())
		assert.Equal(t, want, docs[1].Root().GetTree("t").ToXML())
	})

	t.Run("both replicas split and one removes the style", func(t *testing.T) {
		docs := boldSpanReplicas(t, 2)
		require.NoError(t, docs[0].Update(func(root *json.Object, p *presence.Presence) error {
			split(root.GetTree("t"))
			unbold(root.GetTree("t"))
			return nil
		}))
		require.NoError(t, docs[1].Update(func(root *json.Object, p *presence.Presence) error {
			split(root.GetTree("t"))
			return nil
		}))
		exchangeInOrder(t, docs, [][]int{{1}, {0}})

		// Here the removal saw its own split first, so its range covers only
		// the left product: the right one keeps the style. The two concurrent
		// splits of the one boundary order by ticket and leave an empty middle
		// product, and both replicas have to land on that same shape.
		const want = `<doc><p><span>abc</span><span></span><span bold="true">de</span></p></doc>`
		assert.Equal(t, want, docs[0].Root().GetTree("t").ToXML())
		assert.Equal(t, want, docs[1].Root().GetTree("t").ToXML())
	})
}

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
	"github.com/yorkie-team/yorkie/test/helper"
)

// §7.8's marker counts tombstones, and GC unlinks them on its own schedule, so
// these run the same-boundary splits with one replica having collected a
// tombstone the other still holds. The design doc (§7.8) argues why a purge
// cannot make an edit set diverge that converged without the marker; these pin
// the shapes the two replicas have to agree on.
func TestTreeSameBoundarySplitAfterGC(t *testing.T) {
	t.Run("converges when one replica collects while a split is in flight", func(t *testing.T) {
		d1, d2, a1, a2 := newReplicas(t)
		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{
				Type: "doc",
				Children: []json.TreeNode{{
					Type: "p",
					Children: []json.TreeNode{{
						Type:     "span",
						Children: []json.TreeNode{{Type: "text", Value: "abcde"}},
					}},
				}},
			})
			return nil
		}))
		crossSync(t, d1, d2)

		// d2 removes the last character. Delivering it one way and acking at
		// d2 leaves the removal covered by the element-wise min of the two
		// vectors, which is what a server would compute.
		require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 0, 4}, []int{0, 0, 5}, nil, 0)
			return nil
		}))
		oneWayDeliver(t, d2, d1)
		minVV := time.MinVersionVector(d1.VersionVector(), d2.VersionVector())

		// d1 splits the span, which moves the tombstone into a product d2 has
		// not seen, and d2 splits the same boundary concurrently.
		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 0, 3}, []int{0, 0, 3}, nil, 1)
			return nil
		}))
		require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 0, 3}, []int{0, 0, 3}, nil, 1)
			return nil
		}))

		require.Positive(t, d1.GarbageCollect(minVV), "d1 should collect the removed character")

		crossSync(t, d1, d2)
		crossSync(t, d1, d2)

		assert.Equal(t, `<doc><p><span>abc</span><span></span><span>d</span></p></doc>`,
			d1.Root().GetTree("t").ToXML())
		assert.Equal(t, d1.Root().GetTree("t").ToXML(), d2.Root().GetTree("t").ToXML())
		assert.Equal(t, liveTreeShape(t, d1), liveTreeShape(t, d2))

		for _, doc := range []*document.Document{d1, d2} {
			doc.GarbageCollect(helper.MaxVersionVector(a1, a2))
			assert.Equal(t, 0, doc.GarbageLen(), "tree garbage must drain on both replicas")
		}
		assert.Equal(t, treeShape(t, d1), treeShape(t, d2))
	})

	// Two same-boundary splits and a follow-up split of the newer actor's
	// product (the shape of yorkie-js-sdk#1433), at a boundary with a removed
	// character behind it, which either replica -- or neither -- collected
	// before the splits.
	for _, collector := range []int{-1, 0, 1} {
		t.Run(fmt.Sprintf("a follow-up split over a removed tail, collected by replica %d", collector),
			func(t *testing.T) {
				docs := paragraphReplicas(t, 2, "abc")
				applySplitSteps(t, docs, []splitStep{{replica: 0, index: 3, to: 4}})
				exchangeInOrder(t, docs, [][]int{{1}, {0}})
				if collector >= 0 {
					minVV := time.MinVersionVector(docs[0].VersionVector(), docs[1].VersionVector())
					require.Positive(t, docs[collector].GarbageCollect(minVV))
				}

				applySplitSteps(t, docs, []splitStep{
					{replica: 0, index: 3}, {replica: 1, index: 3}, {replica: 1, index: 5},
				})
				exchangeInOrder(t, docs, [][]int{{1}, {0}})

				assert.Equal(t, "<doc><p>ab</p><p></p><p></p><p></p></doc>", docs[0].Root().GetTree("t").ToXML())
				assert.Equal(t, docs[0].Root().GetTree("t").ToXML(), docs[1].Root().GetTree("t").ToXML())
				assert.Equal(t, liveTreeShape(t, docs[0]), liveTreeShape(t, docs[1]))
			})
	}
}

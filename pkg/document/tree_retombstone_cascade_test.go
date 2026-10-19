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

	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestTreeRetombstoneCascadeRedoThenUndo runs an undo whose retombstone
// cascades §4.1 to a split product, then its redo and the redo's undo, with
// and without GC between the steps.
//
// d0 inserts <div><p>XY</p></div> and undoes it while d1, which d0 has not
// heard from, splits it at level 2, so the product is a three-level subtree
// <div><p>Y</p></div> the undo does not know. d1 applies the undo after its
// split and takes the product with the element (the cascade); d0 applies the
// split to an element its undo already removed. The redo and the undo after
// it then act on both replicas by identity, and every exchange has to leave
// the two holding the same tree, tombstones included.
func TestTreeRetombstoneCascadeRedoThenUndo(t *testing.T) {
	for _, gc := range []bool{false, true} {
		t.Run(fmt.Sprintf("gc %v", gc), func(t *testing.T) {
			docs := twoParagraphReplicas(t, 2)
			edit := func(doc int, from, to int, content *json.TreeNode, splitLevel int) {
				require.NoError(t, docs[doc].Update(func(root *json.Object, p *presence.Presence) error {
					root.GetTree("t").Edit(from, to, content, splitLevel)
					return nil
				}))
			}
			sync := func(label string) {
				exchangeInOrder(t, docs, [][]int{{1}, {0}})
				if gc {
					vv := time.MinVersionVector(docs[0].VersionVector(), docs[1].VersionVector())
					for _, doc := range docs {
						doc.GarbageCollect(vv)
					}
				}
				require.Equal(t, treeShape(t, docs[0]), treeShape(t, docs[1]), label)
			}

			// <r>0<p>1ab3</p>4<p>5cd7</p>8</r>: the div goes in at 8, and 11
			// is between X and Y inside it.
			edit(0, 8, 8, &json.TreeNode{Type: "div", Children: []json.TreeNode{{
				Type: "p", Children: []json.TreeNode{{Type: "text", Value: "XY"}},
			}}}, 0)
			sync("after the insert")
			edit(1, 11, 11, nil, 2)
			require.Equal(t, "<r><p>ab</p><p>cd</p><div><p>X</p></div><div><p>Y</p></div></r>",
				treeXML(t, docs[1]))

			require.NoError(t, docs[0].Undo())
			sync("after the cascading undo")
			assert.Equal(t, "<r><p>ab</p><p>cd</p></r>", treeXML(t, docs[0]),
				"the undo takes the product it did not know with the element")

			require.NoError(t, docs[0].Redo())
			sync("after its redo")
			assert.Contains(t, treeXML(t, docs[0]), "<div><p>X",
				"the redo brings the element back")

			require.NoError(t, docs[0].Undo())
			sync("after the redo's undo")
			assert.Equal(t, "<r><p>ab</p><p>cd</p></r>", treeXML(t, docs[0]),
				"the undo of the redo leaves nothing of the element live")
		})
	}
}

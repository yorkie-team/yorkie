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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
)

// boundaryRecords renders the boundary record of every paragraph, so that
// replicas can be compared on what later splits will read, not only on the
// shape they have now.
func boundaryRecords(t *testing.T, doc *document.Document) string {
	t.Helper()

	var b strings.Builder
	for _, p := range paragraphs(treeCRDT(t, doc)) {
		fmt.Fprintf(&b, "%s{", p.IDString())
		if p.BoundaryAnchor != nil {
			fmt.Fprintf(&b, "anchor=%v:%d ", p.BoundaryAnchor.CreatedAt.ToTestString(), p.BoundaryAnchor.Offset)
		}
		if p.BoundaryAtEnd {
			b.WriteString("end ")
		}
		if p.BoundaryNest != nil {
			fmt.Fprintf(&b, "nest=%v:%d", p.BoundaryNest.CreatedAt.ToTestString(), p.BoundaryNest.Offset)
		}
		b.WriteString("} ")
	}
	return b.String()
}

// reloadFromSnapshot rebuilds doc from a snapshot of itself, with the same
// actor and version vector, as a client does when the server sends one.
func reloadFromSnapshot(t *testing.T, doc *document.Document) *document.Document {
	t.Helper()

	bytes, err := converter.SnapshotToBytes(doc.RootObject(), doc.AllPresences())
	require.NoError(t, err)
	restored := document.New(doc.Key())
	restored.SetActor(doc.ActorID())
	require.NoError(t, restored.ApplyChangePack(change.NewPack(
		restored.Key(), change.NewCheckpoint(0, doc.Checkpoint().ClientSeq), nil, doc.VersionVector(), bytes,
	)))
	require.Equal(t, treeShape(t, doc), treeShape(t, restored))
	return restored
}

// TestTreeBoundaryAnchorSurvivesSnapshot reloads a replica from a snapshot
// between a split and a later split made inside that split's group. The
// later split reads the earlier boundary's anchor to decide its nesting, so
// a snapshot that drops anchors makes the reloaded replica nest its split
// differently from how its peer nests a concurrent one.
func TestTreeBoundaryAnchorSurvivesSnapshot(t *testing.T) {
	for _, reloaded := range []int{0, 1} {
		t.Run(fmt.Sprintf("d%d reloads", reloaded+1), func(t *testing.T) {
			docs := boundaryReplicas(t, 2, "ab")
			updateTree(t, docs[0], splitAt(3)) // S1: <p>ab</p><p></p>
			exchangeInOrder(t, docs, [][]int{nil, {0}})

			docs[reloaded] = reloadFromSnapshot(t, docs[reloaded])
			ps := paragraphs(treeCRDT(t, docs[reloaded]))
			require.Len(t, ps, 2)
			assert.True(t, ps[1].BoundaryAtEnd, "the reloaded product keeps its anchor")

			updateTree(t, docs[0], splitAt(5)) // S2, inside S1's group
			updateTree(t, docs[1], splitAt(5)) // S3, concurrent with S2
			exchangeInOrder(t, docs, [][]int{{1}, {0}})

			assert.Equal(t, treeXML(t, docs[0]), treeXML(t, docs[1]))
			assert.Equal(t, treeShape(t, docs[0]), treeShape(t, docs[1]))
			assert.Equal(t, boundaryRecords(t, docs[0]), boundaryRecords(t, docs[1]))
		})
	}
}

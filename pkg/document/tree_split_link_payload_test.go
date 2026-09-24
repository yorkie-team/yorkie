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

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// splitLinks lists every split-sibling link the tree under "t" carries, one
// line per node that has one. Empty when the tree carries none.
func splitLinks(t *testing.T, doc *document.Document) []string {
	t.Helper()

	id := func(nodeID *crdt.TreeNodeID) string {
		if nodeID == nil {
			return "none"
		}
		return fmt.Sprintf("%s:%d", nodeID.CreatedAt.Key(), nodeID.Offset)
	}

	var lines []string
	var walk func(n *crdt.TreeNode)
	walk = func(n *crdt.TreeNode) {
		if n.InsPrevID != nil || n.InsNextID != nil {
			lines = append(lines, fmt.Sprintf("%s prev=%s next=%s", n.IDString(), id(n.InsPrevID), id(n.InsNextID)))
		}
		for _, child := range n.Index.Children(true) {
			walk(child.Value)
		}
	}
	walk(treeCRDT(t, doc).Root())

	return lines
}

// replicate hands every change doc has produced to a fresh replica, through
// protobuf as on the wire.
func replicate(t *testing.T, from *document.Document) *document.Document {
	t.Helper()

	pb, err := converter.ToChangePack(from.CreateChangePack())
	require.NoError(t, err)
	pack, err := converter.FromChangePack(pb)
	require.NoError(t, err)

	actor, err := time.ActorIDFromHex("000000000000000000000002")
	require.NoError(t, err)
	to := document.New("test-doc")
	to.SetActor(actor)
	require.NoError(t, to.ApplyChangePack(change.NewPack(
		pack.DocumentKey, change.NewCheckpoint(0, 0), pack.Changes, time.InitialVersionVector, nil,
	)))

	return to
}

// withSplitTree returns a document holding a tree whose span and paragraph
// have both been split, so its nodes carry split-sibling links.
func withSplitTree(t *testing.T) *document.Document {
	t.Helper()

	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	doc := document.New("test-doc")
	doc.SetActor(actor)

	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
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
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").EditByPath([]int{0, 0, 3}, []int{0, 0, 3}, nil, 1)
		return nil
	}))

	return doc
}

// A Set/Add/ArraySet reverse captures its value with DeepCopy, and that copy
// keeps the split-sibling links of the tree it was taken from. Undo executes
// it directly, while every other replica -- and the server -- decodes the same
// operation through dropSplitLinksInElement. Unless the copy is stripped too,
// the replica that ran the undo is the only one left holding the links.
func TestTreeSplitLinksInReverseOperationPayload(t *testing.T) {
	t.Run("the tree a split leaves behind does carry them", func(t *testing.T) {
		assert.NotEmpty(t, splitLinks(t, withSplitTree(t)))
	})

	t.Run("an undone Set restores the same links here and on a replica", func(t *testing.T) {
		doc := withSplitTree(t)
		restored := doc.Root().GetTree("t").ToXML()
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{{Type: "p"}}})
			return nil
		}))
		require.NoError(t, doc.Undo())

		replica := replicate(t, doc)
		assert.Equal(t, restored, doc.Root().GetTree("t").ToXML())
		assert.Equal(t, restored, replica.Root().GetTree("t").ToXML())
		assert.Equal(t, splitLinks(t, replica), splitLinks(t, doc))
	})

	t.Run("an undone Delete restores the same links here and on a replica", func(t *testing.T) {
		doc := withSplitTree(t)
		restored := doc.Root().GetTree("t").ToXML()
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("t")
			return nil
		}))
		require.NoError(t, doc.Undo())

		replica := replicate(t, doc)
		assert.Equal(t, restored, doc.Root().GetTree("t").ToXML())
		assert.Equal(t, restored, replica.Root().GetTree("t").ToXML())
		assert.Equal(t, splitLinks(t, replica), splitLinks(t, doc))
	})
}

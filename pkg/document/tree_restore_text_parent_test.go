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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestRestoreSpanUnderTextParentStaysApplicable covers #2168. A TreeEdit
// restore span names its node's parent by ID, and the ID can name a text
// node. No honest replica produces such a span, but any client with write
// access can send one, and the server stores a pushed change before it
// applies it. The change must therefore apply, both on a replica that pulls
// it and on the server rebuilding the document from its stored changes, and
// it must leave the tree as it was: the span's node is left unplaced, as it
// is when its parent is missing.
func TestRestoreSpanUnderTextParentStaysApplicable(t *testing.T) {
	newReplica := func(t *testing.T, hexActor string) *document.Document {
		doc := document.New(helper.TestKey(t))
		actor, err := time.ActorIDFromHex(hexActor)
		require.NoError(t, err)
		doc.SetActor(actor)
		return doc
	}
	update := func(t *testing.T, doc *document.Document, fn func(root *json.Object)) {
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			fn(root)
			return nil
		}))
	}

	// craft replaces the operations of sender's last change with a restore
	// TreeEdit carrying span, and passes the whole pack through the protobuf
	// round trip a push takes.
	craft := func(
		t *testing.T, sender *document.Document,
		span func(executedAt *time.Ticket, textID *crdt.TreeNodeID) *crdt.TreeRestoreSpan,
	) *change.Pack {
		tree := sender.Root().GetTree("tree")
		textID := tree.Root().Children()[0].Children()[0].ID()

		update(t, sender, func(root *json.Object) {
			root.GetTree("tree").Edit(1, 1, &json.TreeNode{Type: "text", Value: "x"}, 0)
		})
		pack := sender.CreateChangePack()
		last := pack.Changes[len(pack.Changes)-1]
		require.Len(t, last.Operations(), 1)
		valid, ok := last.Operations()[0].(*operations.TreeEdit)
		require.True(t, ok)

		// The span's own ID reuses the replaced insertion's ticket: it is
		// causally known to the change, and no live node holds it once the
		// insertion is gone, so Restore falls through to recreateFromSpan.
		crafted := operations.NewRestoreTreeEdit(
			valid.ParentCreatedAt(), valid.FromPos(), valid.ToPos(), valid.ExecutedAt(),
			[]*crdt.TreeRestoreSpan{span(valid.ExecutedAt(), textID)},
			crdt.RestoreModeRestore, nil,
		)
		pack.Changes[len(pack.Changes)-1] = change.New(
			last.ID(), last.Message(), []operations.Operation{crafted}, last.PresenceChange(),
		)

		pbPack, err := converter.ToChangePack(pack)
		require.NoError(t, err)
		decoded, err := converter.FromChangePack(pbPack)
		require.NoError(t, err, "decoding cannot tell a text parent from an element")
		return decoded
	}

	spans := map[string]func(*time.Ticket, *crdt.TreeNodeID) *crdt.TreeRestoreSpan{
		"element span": func(at *time.Ticket, textID *crdt.TreeNodeID) *crdt.TreeRestoreSpan {
			return &crdt.TreeRestoreSpan{
				ID:       crdt.NewTreeNodeID(at, 0),
				NodeType: "p",
				ParentID: textID,
			}
		},
		"text span": func(at *time.Ticket, textID *crdt.TreeNodeID) *crdt.TreeRestoreSpan {
			return &crdt.TreeRestoreSpan{
				ID:       crdt.NewTreeNodeID(at, 0),
				NodeType: "text",
				IsText:   true,
				Length:   1,
				Value:    "x",
				ParentID: textID,
			}
		},
	}

	// The honest path: undo after GC recreates a purged text node under its
	// element parent, and the pulling replica and a server rebuild agree.
	t.Run("honest undo after GC", func(t *testing.T) {
		sender := newReplica(t, "000000000000000000000001")
		update(t, sender, func(root *json.Object) {
			root.SetNewTree("tree", json.TreeNode{Type: "r", Children: []json.TreeNode{{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "hello"}},
			}}})
		})
		update(t, sender, func(root *json.Object) {
			root.GetTree("tree").Edit(1, 6, nil, 0)
		})
		assert.Positive(t, sender.GarbageCollect(helper.MaxVersionVector(sender.ActorID())))
		require.NoError(t, sender.Undo())
		assert.Equal(t, "<r><p>hello</p></r>", sender.Root().GetTree("tree").ToXML())

		pbPack, err := converter.ToChangePack(sender.CreateChangePack())
		require.NoError(t, err)
		pack, err := converter.FromChangePack(pbPack)
		require.NoError(t, err)
		undo, ok := pack.Changes[len(pack.Changes)-1].Operations()[0].(*operations.TreeEdit)
		require.True(t, ok)
		require.NotEmpty(t, undo.RestoreSpans(), "the undo takes the restore path")

		receiver := newReplica(t, "000000000000000000000002")
		pack.VersionVector.Set(receiver.ActorID(), 0)
		require.NoError(t, receiver.ApplyChangePack(pack))
		assert.Equal(t, "<r><p>hello</p></r>", receiver.Root().GetTree("tree").ToXML())

		built := document.NewInternalDocument(sender.Key())
		_, _, err = built.ApplyChanges(pack.Changes...)
		require.NoError(t, err)
		assert.Equal(t, sender.Marshal(), built.Marshal())

		require.NoError(t, sender.Redo())
		assert.Equal(t, "<r><p></p></r>", sender.Root().GetTree("tree").ToXML())
	})

	for name, span := range spans {
		t.Run(name, func(t *testing.T) {
			sender := newReplica(t, "000000000000000000000001")
			update(t, sender, func(root *json.Object) {
				root.SetNewTree("tree", json.TreeNode{Type: "r", Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "hello"}},
				}}})
			})
			receiver := newReplica(t, "000000000000000000000002")
			pack := sender.CreateChangePack()
			pack.VersionVector.Set(receiver.ActorID(), 0)
			require.NoError(t, receiver.ApplyChangePack(pack))

			crafted := craft(t, sender, span)

			// A replica that pulls the change.
			pack = &change.Pack{
				DocumentKey:   crafted.DocumentKey,
				Checkpoint:    crafted.Checkpoint,
				Changes:       crafted.Changes[len(crafted.Changes)-1:],
				VersionVector: crafted.VersionVector.DeepCopy(),
			}
			pack.VersionVector.Set(receiver.ActorID(), 0)
			require.NoError(t, receiver.ApplyChangePack(pack))
			assert.Equal(t, "<r><p>hello</p></r>", receiver.Root().GetTree("tree").ToXML())

			// The server rebuilding the document from every stored change.
			built := document.NewInternalDocument(sender.Key())
			_, _, err := built.ApplyChanges(crafted.Changes...)
			require.NoError(t, err)
			assert.Equal(t, receiver.Marshal(), built.Marshal(), "replicas converge")

			// The tree stays editable after the skipped span.
			update(t, receiver, func(root *json.Object) {
				root.GetTree("tree").Edit(6, 6, &json.TreeNode{Type: "text", Value: "!"}, 0)
			})
			assert.Equal(t, "<r><p>hello!</p></r>", receiver.Root().GetTree("tree").ToXML())
		})
	}
}

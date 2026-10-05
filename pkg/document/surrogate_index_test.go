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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestRejectMidSurrogatePairIndexes verifies that Text and Tree operations
// reject indexes that split a UTF-16 surrogate pair.
func TestRejectMidSurrogatePairIndexes(t *testing.T) {
	t.Run("Text.Edit", func(t *testing.T) {
		doc := document.New("text-mid-surrogate")

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("text").Edit(0, 0, "😀x")
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, `{"text":[{"val":"😀x"}]}`, doc.Marshal())

		assert.PanicsWithError(
			t,
			crdt.ErrInvalidUTF16Index.Error(),
			func() {
				_ = doc.Update(func(root *json.Object, p *presence.Presence) error {
					root.GetText("text").Edit(1, 1, "y")
					return nil
				})
			},
		)

		assert.Equal(t, `{"text":[{"val":"😀x"}]}`, doc.Marshal())
	})

	t.Run("Tree.Edit", func(t *testing.T) {
		doc := document.New("tree-mid-surrogate")

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("tree", json.TreeNode{
				Type: "r",
				Children: []json.TreeNode{
					{
						Type: "p",
						Children: []json.TreeNode{
							{
								Type:  "text",
								Value: "😀x",
							},
						},
					},
				},
			})
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, "<r><p>😀x</p></r>", doc.Root().GetTree("tree").ToXML())

		assert.PanicsWithError(
			t,
			crdt.ErrInvalidUTF16Index.Error(),
			func() {
				_ = doc.Update(func(root *json.Object, p *presence.Presence) error {
					root.GetTree("tree").Edit(2, 2, &json.TreeNode{
						Type:  "text",
						Value: "y",
					}, 0)
					return nil
				})
			},
		)

		assert.Equal(t, "<r><p>😀x</p></r>", doc.Root().GetTree("tree").ToXML())
	})

	t.Run("Text.Style", func(t *testing.T) {
		doc := document.New("text-style-mid-surrogate")

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("text").Edit(0, 0, "😀x")
			return nil
		})
		require.NoError(t, err)

		expected := doc.Marshal()

		assert.PanicsWithError(
			t,
			crdt.ErrInvalidUTF16Index.Error(),
			func() {
				_ = doc.Update(func(root *json.Object, p *presence.Presence) error {
					root.GetText("text").Style(
						0,
						1,
						map[string]string{"bold": "true"},
					)
					return nil
				})
			},
		)

		assert.Equal(t, expected, doc.Marshal())
	})

	t.Run("Tree.Style", func(t *testing.T) {
		doc := document.New("tree-style-mid-surrogate")

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("tree", json.TreeNode{
				Type: "r",
				Children: []json.TreeNode{
					{
						Type: "p",
						Children: []json.TreeNode{
							{
								Type:  "text",
								Value: "😀x",
							},
						},
					},
				},
			})
			return nil
		})
		require.NoError(t, err)

		expected := doc.Marshal()

		assert.PanicsWithError(
			t,
			crdt.ErrInvalidUTF16Index.Error(),
			func() {
				_ = doc.Update(func(root *json.Object, p *presence.Presence) error {
					root.GetTree("tree").Style(
						1,
						2,
						map[string]string{"bold": "true"},
					)
					return nil
				})
			},
		)

		assert.Equal(t, expected, doc.Marshal())
	})

	t.Run("valid UTF-16 boundaries remain editable", func(t *testing.T) {
		tests := []struct {
			name     string
			index    int
			expected string
		}{
			{
				name:     "before surrogate pair",
				index:    0,
				expected: `{"text":[{"val":"y"},{"val":"😀x"}]}`,
			},
			{
				name:     "after surrogate pair",
				index:    2,
				expected: `{"text":[{"val":"😀"},{"val":"y"},{"val":"x"}]}`,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				doc := document.New(helper.TestKey(t))

				err := doc.Update(func(
					root *json.Object,
					p *presence.Presence,
				) error {
					root.SetNewText("text").Edit(0, 0, "😀x")
					return nil
				})
				require.NoError(t, err)

				assert.NotPanics(t, func() {
					err = doc.Update(func(
						root *json.Object,
						p *presence.Presence,
					) error {
						root.GetText("text").Edit(tc.index, tc.index, "y")
						return nil
					})
				})
				require.NoError(t, err)
				assert.Equal(t, tc.expected, doc.Marshal())
			})
		}
	})
}

// surrogateText is one non-BMP character followed by a BMP one: 2 runes, 3
// UTF-16 code units. Text offset 1 (Tree index 2) is the only position that
// cuts the emoji in half.
const surrogateText = "\U0001F600x"

// newSurrogateDoc returns a document holding the Tree <r><p>😀x</p></r> under
// "tree" and the Text "😀x" under "text".
func newSurrogateDoc(t *testing.T) *document.Document {
	t.Helper()

	doc := document.New(helper.TestKey(t))
	populateSurrogateDoc(t, doc)

	return doc
}

// populateSurrogateDoc writes the content newSurrogateDoc describes.
func populateSurrogateDoc(t *testing.T, doc *document.Document) {
	t.Helper()

	updateSurrogateDoc(t, doc, func(root *json.Object) {
		root.SetNewTree("tree", json.TreeNode{Type: "r", Children: []json.TreeNode{{
			Type:     "p",
			Children: []json.TreeNode{{Type: "text", Value: surrogateText}},
		}}})
		root.SetNewText("text").Edit(0, 0, surrogateText)
	})
}

// updateSurrogateDoc applies fn to the document in its own change.
func updateSurrogateDoc(t *testing.T, doc *document.Document, fn func(root *json.Object)) {
	t.Helper()

	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		fn(root)
		return nil
	}))
}

// assertRejectsMidSurrogate asserts that fn is refused with
// ErrInvalidUTF16Index and leaves the document unchanged.
func assertRejectsMidSurrogate(t *testing.T, doc *document.Document, fn func(root *json.Object)) {
	t.Helper()

	before := doc.Marshal()
	assert.PanicsWithError(t, crdt.ErrInvalidUTF16Index.Error(), func() {
		_ = doc.Update(func(root *json.Object, p *presence.Presence) error {
			fn(root)
			return nil
		})
	})
	assert.Equal(t, before, doc.Marshal(), "the refused edit left no trace")
}

// TestRejectedEditDiscardsEarlierEditsOfTheSameUpdate covers a rejection
// that comes after a valid edit in the same updater. The json proxies turn the
// rejection into a panic, and the valid edit has already reached the clone, so
// Update has to discard the clone on the way out. Otherwise Root shows an edit
// the document never took, and the next change is built on top of it.
func TestRejectedEditDiscardsEarlierEditsOfTheSameUpdate(t *testing.T) {
	z := &json.TreeNode{Type: "text", Value: "z"}

	tests := []struct {
		name string
		fn   func(root *json.Object)
	}{
		{"Text", func(root *json.Object) {
			root.GetText("text").Edit(3, 3, "z")
			root.GetText("text").Edit(1, 1, "y")
		}},
		{"Tree", func(root *json.Object) {
			root.GetTree("tree").Edit(4, 4, z, 0)
			root.GetTree("tree").Edit(2, 2, z, 0)
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := newSurrogateDoc(t)
			state := func() []string {
				return []string{
					doc.Root().GetTree("tree").ToXML(),
					doc.Root().GetText("text").String(),
				}
			}
			original := state()

			assertRejectsMidSurrogate(t, doc, tc.fn)
			assert.Equal(t, original, state(), "the valid edit before the rejection was discarded")

			updateSurrogateDoc(t, doc, func(root *json.Object) {
				root.GetTree("tree").Edit(4, 4, z, 0)
				root.GetText("text").Edit(3, 3, "z")
			})
			edited := []string{"<r><p>\U0001F600xz</p></r>", "\U0001F600xz"}
			assert.Equal(t, edited, state())

			require.NoError(t, doc.Undo())
			assert.Equal(t, original, state())
			require.NoError(t, doc.Redo())
			assert.Equal(t, edited, state())
		})
	}
}

// TestRejectMidSurrogatePairIndexesAtEveryEntryPoint covers the Tree entry
// points that resolve indexes or paths through Tree.FindPos, and the Text
// ones that resolve offsets through Text.CreateRange, beyond Edit and Style.
func TestRejectMidSurrogatePairIndexesAtEveryEntryPoint(t *testing.T) {
	y := &json.TreeNode{Type: "text", Value: "y"}

	tests := []struct {
		name string
		fn   func(root *json.Object)
	}{
		{"Tree.EditBulk", func(root *json.Object) {
			root.GetTree("tree").EditBulk(2, 2, []*json.TreeNode{y}, 0)
		}},
		{"Tree.EditByPath", func(root *json.Object) {
			// The last component of a path into a text node is a UTF-16
			// offset, so [0, 1] is offset 1 of the text under the first <p>.
			root.GetTree("tree").EditByPath([]int{0, 1}, []int{0, 1}, y, 0)
		}},
		{"Tree.EditBulkByPath", func(root *json.Object) {
			root.GetTree("tree").EditBulkByPath([]int{0, 1}, []int{0, 1}, []*json.TreeNode{y}, 0)
		}},
		{"Tree.RemoveStyle", func(root *json.Object) {
			root.GetTree("tree").RemoveStyle(1, 2, []string{"bold"})
		}},
		{"Tree.StyleByPath", func(root *json.Object) {
			root.GetTree("tree").StyleByPath([]int{0, 0}, []int{0, 1}, map[string]string{"bold": "true"})
		}},
		{"Tree.RemoveStyleByPath", func(root *json.Object) {
			root.GetTree("tree").RemoveStyleByPath([]int{0, 0}, []int{0, 1}, []string{"bold"})
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertRejectsMidSurrogate(t, newSurrogateDoc(t), tc.fn)
		})
	}

	t.Run("valid Tree boundaries remain editable", func(t *testing.T) {
		for idx, expected := range map[int]string{
			1: "<r><p>y\U0001F600x</p></r>",
			3: "<r><p>\U0001F600yx</p></r>",
			4: "<r><p>\U0001F600xy</p></r>",
		} {
			doc := newSurrogateDoc(t)
			updateSurrogateDoc(t, doc, func(root *json.Object) {
				root.GetTree("tree").Edit(idx, idx, y, 0)
			})
			assert.Equal(t, expected, doc.Root().GetTree("tree").ToXML(), "index %d", idx)
		}
	})

	t.Run("Tree.Edit after a split", func(t *testing.T) {
		// An earlier edit splits <p>'s text node, so the emoji's node is no
		// longer alone and every offset has to be resolved relative to the
		// node that holds it.
		splitDoc := func(t *testing.T) *document.Document {
			doc := newSurrogateDoc(t)
			updateSurrogateDoc(t, doc, func(root *json.Object) {
				root.GetTree("tree").Edit(3, 3, &json.TreeNode{Type: "text", Value: "yz"}, 0)
			})
			return doc
		}

		w := &json.TreeNode{Type: "text", Value: "w"}
		assertRejectsMidSurrogate(t, splitDoc(t), func(root *json.Object) {
			root.GetTree("tree").Edit(2, 2, w, 0)
		})

		// The seam the split created (3) and the offsets inside the inserted
		// node (4, 5) are whole-character boundaries.
		for idx, expected := range map[int]string{
			3: "<r><p>\U0001F600wyzx</p></r>",
			4: "<r><p>\U0001F600ywzx</p></r>",
			5: "<r><p>\U0001F600yzwx</p></r>",
		} {
			doc := splitDoc(t)
			updateSurrogateDoc(t, doc, func(root *json.Object) {
				root.GetTree("tree").Edit(idx, idx, w, 0)
			})
			assert.Equal(t, expected, doc.Root().GetTree("tree").ToXML(), "index %d", idx)
		}
	})

	t.Run("Text.Edit in a later node", func(t *testing.T) {
		// Appending a second emoji leaves "😀x😀y" across two nodes, so the
		// rejected offset lives in a node that is not the first one.
		twoNodeDoc := func(t *testing.T) *document.Document {
			doc := newSurrogateDoc(t)
			updateSurrogateDoc(t, doc, func(root *json.Object) {
				root.GetText("text").Edit(3, 3, "\U0001F600y")
			})
			return doc
		}

		assertRejectsMidSurrogate(t, twoNodeDoc(t), func(root *json.Object) {
			root.GetText("text").Edit(4, 4, "w")
		})

		// The node seam (3) and the offsets after the second emoji (5, 6)
		// stay editable.
		for idx, expected := range map[int]string{
			3: "\U0001F600xw\U0001F600y",
			5: "\U0001F600x\U0001F600wy",
			6: "\U0001F600x\U0001F600yw",
		} {
			doc := twoNodeDoc(t)
			updateSurrogateDoc(t, doc, func(root *json.Object) {
				root.GetText("text").Edit(idx, idx, "w")
			})
			assert.Equal(t, expected, doc.Root().GetText("text").String(), "index %d", idx)
		}
	})
}

// TestRemoteMidSurrogateOperationStillApplies pins the invariant that makes
// it safe to reject mid-pair indexes in Tree.FindPos and Text.CreateRange:
// remote operations never go through them. An operation minted by an older
// client already carries CRDT positions, so a mid-pair offset in it must
// still apply on a replica that has the check, and must not break that
// replica's own undo/redo afterwards.
func TestRemoteMidSurrogateOperationStillApplies(t *testing.T) {
	newReplica := func(t *testing.T, hexActor string) *document.Document {
		doc := document.New(helper.TestKey(t))
		actor, err := time.ActorIDFromHex(hexActor)
		require.NoError(t, err)
		doc.SetActor(actor)
		return doc
	}

	// sync applies the given changes of sender to receiver.
	sync := func(t *testing.T, sender, receiver *document.Document, changes ...*change.Change) {
		pack := sender.CreateChangePack()
		if len(changes) > 0 {
			pack.Changes = changes
		}
		pack.VersionVector.Set(
			receiver.ActorID(),
			receiver.VersionVector().VersionOf(receiver.ActorID()),
		)
		require.NoError(t, receiver.ApplyChangePack(pack))
	}

	sender := newReplica(t, "000000000000000000000001")
	populateSurrogateDoc(t, sender)
	receiver := newReplica(t, "000000000000000000000002")
	sync(t, sender, receiver)

	// The receiver makes a local edit it will later undo and redo.
	updateSurrogateDoc(t, receiver, func(root *json.Object) {
		root.GetTree("tree").Edit(4, 4, &json.TreeNode{Type: "text", Value: "z"}, 0)
		root.GetText("text").Edit(3, 3, "z")
	})

	// The sender inserts right after the emoji, which is a valid index, and
	// the operations are then moved one code unit to the left, into the
	// middle of the pair. That is what an older client, which did not reject
	// the index, would have sent.
	updateSurrogateDoc(t, sender, func(root *json.Object) {
		root.GetTree("tree").Edit(3, 3, &json.TreeNode{Type: "text", Value: "y"}, 0)
		root.GetText("text").Edit(2, 2, "y")
	})
	pack := sender.CreateChangePack()
	valid := pack.Changes[len(pack.Changes)-1]

	var ops []operations.Operation
	for _, op := range valid.Operations() {
		switch op := op.(type) {
		case *operations.TreeEdit:
			from := op.FromPos()
			pos := crdt.NewTreePos(from.ParentID, crdt.NewTreeNodeID(
				from.LeftSiblingID.CreatedAt,
				from.LeftSiblingID.Offset-1,
			))
			ops = append(ops, operations.NewTreeEdit(
				op.ParentCreatedAt(), pos, pos, op.Contents(), op.SplitLevel(), op.ExecutedAt(),
			))
		case *operations.Edit:
			from := op.From()
			pos := crdt.NewRGATreeSplitNodePos(from.ID(), from.RelativeOffset()-1)
			ops = append(ops, operations.NewEdit(
				op.ParentCreatedAt(), pos, pos, op.Content(), op.Attributes(), op.ExecutedAt(),
			))
		default:
			t.Fatalf("unexpected operation %T", op)
		}
	}
	require.Len(t, ops, 2)
	sync(t, sender, receiver, change.New(valid.ID(), valid.Message(), ops, valid.PresenceChange()))

	tree := func() string { return receiver.Root().GetTree("tree").ToXML() }
	text := func() string { return receiver.Root().GetText("text").String() }
	// The edit landed inside the pair, so the emoji no longer survives. What
	// the lone halves become is the divergence #2065 describes and is not
	// pinned here.
	applied := []string{tree(), text()}
	assert.NotContains(t, applied[0], "\U0001F600", "the remote edit split the pair")
	assert.NotContains(t, applied[1], "\U0001F600", "the remote edit split the pair")

	// Undo and redo of the receiver's earlier edit still work after the
	// remote insert shifted them.
	require.NoError(t, receiver.Undo())
	assert.Equal(t, strings.Replace(applied[0], "z", "", 1), tree())
	assert.Equal(t, strings.Replace(applied[1], "z", "", 1), text())

	require.NoError(t, receiver.Redo())
	assert.Equal(t, applied, []string{tree(), text()})
}

// TestUndoRedoAroundSurrogatePair covers the local undo/redo path next to and
// over an intact pair, where undo and redo must never be rejected.
func TestUndoRedoAroundSurrogatePair(t *testing.T) {
	tests := []struct {
		name string
		fn   func(root *json.Object)
	}{
		{"insert after the pair", func(root *json.Object) {
			root.GetTree("tree").Edit(3, 3, &json.TreeNode{Type: "text", Value: "y"}, 0)
			root.GetText("text").Edit(2, 2, "y")
		}},
		{"delete the pair", func(root *json.Object) {
			root.GetTree("tree").Edit(1, 3, nil, 0)
			root.GetText("text").Edit(0, 2, "")
		}},
		{"replace the pair", func(root *json.Object) {
			root.GetTree("tree").Edit(1, 3, &json.TreeNode{Type: "text", Value: "y"}, 0)
			root.GetText("text").Edit(0, 2, "y")
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := newSurrogateDoc(t)
			state := func() []string {
				return []string{
					doc.Root().GetTree("tree").ToXML(),
					doc.Root().GetText("text").String(),
				}
			}
			original := state()

			updateSurrogateDoc(t, doc, tc.fn)
			edited := state()

			require.NoError(t, doc.Undo())
			assert.Equal(t, original, state())

			require.NoError(t, doc.Redo())
			assert.Equal(t, edited, state())
		})
	}
}

// TestConcurrentTreeEditsWithSurrogatePair covers actual replica changes,
// including reverse construction and propagation of undo/redo. These cases
// also pass with checked reverse builders: they are compatibility coverage,
// not a reproduction of the review's proposed reverse-builder rejection.
func TestConcurrentTreeEditsWithSurrogatePair(t *testing.T) {
	type edit struct {
		from, to, splitLevel int
		content              string
	}
	tests := []struct {
		name                    string
		local, remote           edit
		applied, undone, redone string
	}{
		{
			name:    "parent deletion and concurrent emoji insertion",
			local:   edit{from: 0, to: 4},
			remote:  edit{from: 1, to: 1, content: "😀"},
			applied: "<r><p>cd</p></r>",
			undone:  "<r><p>ab</p><p>cd</p></r>",
			redone:  "<r><p>cd</p></r>",
		},
		{
			name:    "split and overlapping emoji replacement",
			local:   edit{from: 2, to: 2, splitLevel: 1},
			remote:  edit{from: 1, to: 3, content: "😀"},
			applied: "<r><p>😀</p><p></p><p>cd</p></r>",
			// Existing reconciliation arithmetic splits the pair on undo.
			// Pin that limitation explicitly rather than claiming restoration.
			undone: "<r><p>\uFFFD</p><p></p><p>cd</p></r>",
			redone: "<r><p></p><p>\uFFFD</p><p></p><p>cd</p></r>",
		},
		{
			name:    "merge and overlapping emoji replacement",
			local:   edit{from: 3, to: 5},
			remote:  edit{from: 2, to: 6, content: "😀"},
			applied: "<r><p>a😀d</p></r>",
			undone:  "<r><p>a</p><p>😀d</p></r>",
			redone:  "<r><p>a😀d</p></r>",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			newReplica := func(hexActor string) *document.Document {
				doc := document.New(helper.TestKey(t))
				actor, err := time.ActorIDFromHex(hexActor)
				require.NoError(t, err)
				doc.SetActor(actor)
				return doc
			}
			a := newReplica("000000000000000000000001")
			b := newReplica("000000000000000000000002")
			// Only send the newest change; older local changes remain pending
			// without a server acknowledgement in this in-memory setup.
			latestPack := func(sender *document.Document) *change.Pack {
				pack := sender.CreateChangePack()
				require.NotEmpty(t, pack.Changes)
				pack.Changes = pack.Changes[len(pack.Changes)-1:]
				return pack
			}
			apply := func(receiver *document.Document, pack *change.Pack) {
				pack.Checkpoint = receiver.Checkpoint()
				pack.VersionVector.Set(receiver.ActorID(),
					receiver.VersionVector().VersionOf(receiver.ActorID()))
				require.NoError(t, receiver.ApplyChangePack(pack))
			}
			require.NoError(t, a.Update(func(root *json.Object, _ *presence.Presence) error {
				root.SetNewTree("tree", json.TreeNode{Type: "r", Children: []json.TreeNode{
					{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}}},
					{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "cd"}}},
				}})
				return nil
			}))
			apply(b, latestPack(a))
			runEdit := func(doc *document.Document, e edit) {
				require.NoError(t, doc.Update(func(root *json.Object, _ *presence.Presence) error {
					var content *json.TreeNode
					if e.content != "" {
						content = &json.TreeNode{Type: "text", Value: e.content}
					}
					root.GetTree("tree").Edit(e.from, e.to, content, e.splitLevel)
					return nil
				}))
			}
			// Neither replica has seen the other's edit when it creates its own.
			runEdit(a, tc.local)
			runEdit(b, tc.remote)
			packA, packB := latestPack(a), latestPack(b)
			apply(a, packB)
			apply(b, packA)
			assertState := func(expected string) {
				t.Helper()
				assert.Equal(t, expected, a.Root().GetTree("tree").ToXML())
				assert.Equal(t, expected, b.Root().GetTree("tree").ToXML())
			}
			assertState(tc.applied)
			for range 2 {
				require.NoError(t, a.Undo())
				apply(b, latestPack(a))
				assertState(tc.undone)
				require.NoError(t, a.Redo())
				apply(b, latestPack(a))
				assertState(tc.redone)
			}
		})
	}
}

// TestTreeSplitReverseAfterRemoteSplitHistory reproduces a checked reverse
// builder rejecting an SDK-computed index after the forward edit mutated the
// root. A prior split's lineage changes where later split products land; the
// reverse's preFromIdx + splitSize can therefore be inside live emoji text.
func TestTreeSplitReverseAfterRemoteSplitHistory(t *testing.T) {
	newReplica := func(hexActor string) *document.Document {
		doc := document.New(helper.TestKey(t))
		actor, err := time.ActorIDFromHex(hexActor)
		require.NoError(t, err)
		doc.SetActor(actor)
		return doc
	}
	a := newReplica("000000000000000000000002")
	b := newReplica("000000000000000000000001")
	var sentA, sentB uint32
	exchange := func() {
		packA, packB := a.CreateChangePack(), b.CreateChangePack()
		unsent := func(pack *change.Pack, sent *uint32) {
			first := 0
			for first < len(pack.Changes) && pack.Changes[first].ClientSeq() <= *sent {
				first++
			}
			pack.Changes = pack.Changes[first:]
			if len(pack.Changes) > 0 {
				*sent = pack.Changes[len(pack.Changes)-1].ClientSeq()
			}
		}
		unsent(packA, &sentA)
		unsent(packB, &sentB)
		// Peer changes do not acknowledge the receiver's pending local changes.
		packA.Checkpoint, packB.Checkpoint = b.Checkpoint(), a.Checkpoint()
		packA.VersionVector.Set(b.ActorID(), b.VersionVector().VersionOf(b.ActorID()))
		packB.VersionVector.Set(a.ActorID(), a.VersionVector().VersionOf(a.ActorID()))
		require.NoError(t, a.ApplyChangePack(packB))
		require.NoError(t, b.ApplyChangePack(packA))
	}
	require.NoError(t, a.Update(func(root *json.Object, _ *presence.Presence) error {
		root.SetNewTree("tree", json.TreeNode{Type: "r", Children: []json.TreeNode{
			{Type: "section", Children: []json.TreeNode{
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "a😀b"}}},
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "c😀d"}}},
			}},
		}})
		return nil
	}))
	exchange()
	runEdit := func(doc *document.Document, from, to, splitLevel int, value string) {
		require.NoError(t, doc.Update(func(root *json.Object, _ *presence.Presence) error {
			var content *json.TreeNode
			if value != "" {
				content = &json.TreeNode{Type: "text", Value: value}
			}
			root.GetTree("tree").Edit(from, to, content, splitLevel)
			return nil
		}))
	}
	// B creates split lineage, which A then edits through remote positions.
	runEdit(b, 3, 3, 2, "")
	exchange()
	runEdit(a, 2, 4, 0, "😀")
	runEdit(a, 5, 7, 0, "😀")
	exchange()
	before := "<r><section><p>😀</p>😀</section><section><p>😀b</p><p>c😀d</p></section></r>"
	require.Equal(t, before, a.Root().GetTree("tree").ToXML())
	require.Equal(t, before, b.Root().GetTree("tree").ToXML())
	// Index 4 is a valid caller boundary. The reverse, however, computes
	// [4,8), and its endpoint 8 is inside the direct section text after split.
	runEdit(b, 4, 4, 2, "")
	after := "<r><section><p>😀</p></section><section>😀</section><section><p></p><p>😀b</p><p>c😀d</p></section></r>"
	require.Equal(t, after, b.Root().GetTree("tree").ToXML())
	_, err := b.Root().GetTree("tree").Tree.FindPos(8)
	require.ErrorIs(t, err, crdt.ErrInvalidUTF16Index)
	exchange()
	require.Equal(t, after, a.Root().GetTree("tree").ToXML())
	// A subsequent update must retain the same successful tree edit.
	require.NoError(t, b.Update(func(root *json.Object, _ *presence.Presence) error {
		root.SetString("status", "ok")
		return nil
	}))
	exchange()
	require.Equal(t, after, b.Root().GetTree("tree").ToXML())
	require.Equal(t, a.Marshal(), b.Marshal())
}

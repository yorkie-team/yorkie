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

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/resource"
	"github.com/yorkie-team/yorkie/test/helper"
)

// assertRebuildsSame checks the invariant the whole of docSize rests on: a
// document's sizes and garbage count are a function of its content, so a
// document rebuilt from that content reports the same numbers. A rebuilt
// document is what every client joining an existing document holds, and what
// the server replays from a snapshot.
func assertRebuildsSame(t *testing.T, doc *document.Document, msg string) {
	t.Helper()

	rebuilt, err := doc.InternalDocument().DeepCopy()
	require.NoError(t, err)
	assert.Equal(t, doc.DocSize().GC, rebuilt.DocSize().GC, "%s: gc size", msg)
	assert.Equal(t, doc.GarbageLen(), rebuilt.GarbageLen(), "%s: garbage count", msg)
	assert.Equal(t, doc.DocSize().Live, rebuilt.DocSize().Live, "%s: live size", msg)
}

// TestTreeAttrSplitGC covers the attribute tombstones an element split copies.
//
// SplitElement deep-copies the node's RHT, tombstones included -- it has to,
// or the two halves of what was one node would resolve a concurrent style
// differently and never reconverge. RHT.DeepCopy preserves updatedAt and key,
// which are exactly what RHTNode.IDString is made of, so the copy is
// indistinguishable by id from the original. Registering it was missing, and
// the id it shares with the original made the two collide in the GC map.
func TestTreeAttrSplitGC(t *testing.T) {
	styledAndRemoved := func(t *testing.T) *document.Document {
		t.Helper()

		doc := document.New("test-doc")
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{{
				Type: "p", Children: []json.TreeNode{{
					Type:     "span",
					Children: []json.TreeNode{{Type: "text", Value: "abcdefghij"}},
				}},
			}}})
			return nil
		}))
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Style(1, 13, map[string]string{"color": "red"})
			return nil
		}))
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").RemoveStyle(1, 13, []string{"color"})
			return nil
		}))
		return doc
	}

	t.Run("counts and collects the tombstone a split copied", func(t *testing.T) {
		doc := styledAndRemoved(t)
		assert.Equal(t, 1, doc.GarbageLen())
		assertRebuildsSame(t, doc, "before the split")

		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 0, 1}, []int{0, 0, 1}, nil, 1)
			return nil
		}))

		// Two spans now, each holding a tombstone for "color".
		assert.Equal(t, "<doc><p><span>a</span><span>bcdefghij</span></p></doc>",
			doc.Root().GetTree("t").ToXML())
		assert.Equal(t, 2, doc.GarbageLen())
		assertRebuildsSame(t, doc, "after the split")

		assert.Equal(t, 2, doc.GarbageCollect(doc.VersionVector()))
		assert.Equal(t, 0, doc.GarbageLen())
		assert.Equal(t, resource.DataSize{}, doc.DocSize().GC)
		assertRebuildsSame(t, doc, "after collecting")
	})

	t.Run("counts one tombstone per element a multi level split creates", func(t *testing.T) {
		doc := styledAndRemoved(t)

		// splitLevel 2 splits the <span> and the <p> above it, so the
		// tombstone is copied twice: once per element the split opens.
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 0, 1}, []int{0, 0, 1}, nil, 2)
			return nil
		}))

		// The <p> never carried the attribute, so only the two spans do.
		assert.Equal(t, 2, doc.GarbageLen())
		assertRebuildsSame(t, doc, "after a two level split")

		assert.Equal(t, 2, doc.GarbageCollect(doc.VersionVector()))
		assert.Equal(t, resource.DataSize{}, doc.DocSize().GC)
	})

	t.Run("counts a tombstone the second split copied from the first copy", func(t *testing.T) {
		doc := styledAndRemoved(t)

		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 0, 1}, []int{0, 0, 1}, nil, 1)
			return nil
		}))
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 1, 4}, []int{0, 1, 4}, nil, 1)
			return nil
		}))

		assert.Equal(t, 3, doc.GarbageLen())
		assertRebuildsSame(t, doc, "after splitting a split")

		assert.Equal(t, 3, doc.GarbageCollect(doc.VersionVector()))
		assert.Equal(t, resource.DataSize{}, doc.DocSize().GC)
	})

	t.Run("counts every removed attribute a split copies", func(t *testing.T) {
		doc := document.New("test-doc")
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{{
				Type: "p", Children: []json.TreeNode{{
					Type:     "span",
					Children: []json.TreeNode{{Type: "text", Value: "abcdefghij"}},
				}},
			}}})
			return nil
		}))
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Style(1, 13,
				map[string]string{"color": "red", "size": "9", "b": "1"})
			return nil
		}))
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").RemoveStyle(1, 13, []string{"color", "size", "b"})
			return nil
		}))
		assert.Equal(t, 3, doc.GarbageLen())

		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 0, 1}, []int{0, 0, 1}, nil, 1)
			return nil
		}))

		// One pair per key per half. On main all three copies cancelled
		// their originals and the rebuilt document reported zero.
		assert.Equal(t, 6, doc.GarbageLen())
		assertRebuildsSame(t, doc, "after splitting three tombstones")

		assert.Equal(t, 6, doc.GarbageCollect(doc.VersionVector()))
		assert.Equal(t, resource.DataSize{}, doc.DocSize().GC)
	})

	t.Run("counts the tombstones copied into a piece born tombstoned", func(t *testing.T) {
		d1, d2, a1, a2 := newReplicas(t)

		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{{
				Type: "p", Children: []json.TreeNode{{
					Type:     "span",
					Children: []json.TreeNode{{Type: "text", Value: "abcdefghij"}},
				}},
			}}})
			return nil
		}))
		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Style(1, 13, map[string]string{"color": "red"})
			return nil
		}))
		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").RemoveStyle(1, 13, []string{"color"})
			return nil
		}))
		crossSync(t, d1, d2)

		// d1 splits the <span>; d2 concurrently deletes the whole <p>. When
		// d1's split arrives at d2 it splits a node already tombstoned, so
		// the copied attribute tombstone is born inside a born-dead piece --
		// the branch where the new registration meets GCOnlySize.
		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 0, 1}, []int{0, 0, 1}, nil, 1)
			return nil
		}))
		require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(0, 14, nil, 0)
			return nil
		}))
		crossSync(t, d1, d2)

		assert.Equal(t, "<doc></doc>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, d1.Root().GetTree("t").ToXML(), d2.Root().GetTree("t").ToXML())
		// main reported 6 live against 5 rebuilt here.
		assert.Equal(t, 7, d1.GarbageLen())
		assert.Equal(t, 7, d2.GarbageLen())
		assertRebuildsSame(t, d1, "the replica that split")
		assertRebuildsSame(t, d2, "the replica that deleted")

		assert.Equal(t, 7, d1.GarbageCollect(helper.MaxVersionVector(a1, a2)))
		assert.Equal(t, 7, d2.GarbageCollect(helper.MaxVersionVector(a1, a2)))
		assert.Equal(t, resource.DataSize{}, d1.DocSize().GC)
		assert.Equal(t, resource.DataSize{}, d2.DocSize().GC)
	})

	t.Run("drains when a later style revives the key on both halves", func(t *testing.T) {
		doc := styledAndRemoved(t)
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 0, 1}, []int{0, 0, 1}, nil, 1)
			return nil
		}))
		assert.Equal(t, 2, doc.GarbageLen())

		// Re-setting the key supersedes both tombstones. Each un-registers
		// against its own parent; keyed on the child alone, the second
		// registration would have re-added the entry the first removed.
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Style(1, 14, map[string]string{"color": "blue"})
			return nil
		}))
		assert.Equal(t, 0, doc.GarbageLen())
		assert.Equal(t, resource.DataSize{}, doc.DocSize().GC)
		assertRebuildsSame(t, doc, "after reviving the key")
	})

	t.Run("purges the same tombstones on both replicas when the split is remote", func(t *testing.T) {
		d1, d2, a1, a2 := newReplicas(t)

		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{{
				Type: "p", Children: []json.TreeNode{{
					Type:     "span",
					Children: []json.TreeNode{{Type: "text", Value: "abcdefghij"}},
				}},
			}}})
			return nil
		}))
		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Style(1, 13, map[string]string{"color": "red"})
			return nil
		}))
		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").RemoveStyle(1, 13, []string{"color"})
			return nil
		}))
		crossSync(t, d1, d2)

		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditByPath([]int{0, 0, 1}, []int{0, 0, 1}, nil, 1)
			return nil
		}))
		crossSync(t, d1, d2)

		assert.Equal(t, d1.Root().GetTree("t").ToXML(), d2.Root().GetTree("t").ToXML())
		// Both replicas leaked the copy before the fix, so agreeing with
		// each other is not enough -- name the count they must agree on.
		assert.Equal(t, 2, d1.GarbageLen())
		assert.Equal(t, 2, d2.GarbageLen())
		assert.Equal(t, d1.DocSize(), d2.DocSize())
		assertRebuildsSame(t, d1, "the replica that split")
		assertRebuildsSame(t, d2, "the replica the split arrived at")

		purged1 := d1.GarbageCollect(helper.MaxVersionVector(a1, a2))
		purged2 := d2.GarbageCollect(helper.MaxVersionVector(a1, a2))
		assert.Equal(t, purged1, purged2, "asymmetric purge: d1=%d d2=%d", purged1, purged2)
		assert.Equal(t, resource.DataSize{}, d1.DocSize().GC)
		assert.Equal(t, resource.DataSize{}, d2.DocSize().GC)
	})
}

// textStyledAndRemoved returns a document whose only text node carries one
// tombstoned attribute. Undoing a Style that introduced a key issues a reverse
// Style carrying attributesToRemove, which is the only route that tombstones a
// text attribute today.
func textStyledAndRemoved(t *testing.T) *document.Document {
	t.Helper()

	doc := document.New("test-doc")
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewText("k").Edit(0, 0, "abcdefghij")
		return nil
	}))
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetText("k").Style(0, 10, map[string]string{"b": "1"})
		return nil
	}))
	require.NoError(t, doc.Undo())

	return doc
}

// TestTextAttrSplitGC covers the same defect in the Text CRDT, which the
// tracking issue did not name: TextValue.Split deep-copies the value's
// attributes the same way, and the copies collided the same way.
func TestTextAttrSplitGC(t *testing.T) {

	t.Run("counts and collects the tombstone a split copied", func(t *testing.T) {
		doc := textStyledAndRemoved(t)
		assert.Equal(t, 1, doc.GarbageLen())
		assertRebuildsSame(t, doc, "before the split")

		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("k").Edit(5, 5, "X")
			return nil
		}))

		assert.Equal(t, 2, doc.GarbageLen())
		assertRebuildsSame(t, doc, "after the split")

		assert.Equal(t, 2, doc.GarbageCollect(doc.VersionVector()))
		assert.Equal(t, 0, doc.GarbageLen())
		assert.Equal(t, resource.DataSize{}, doc.DocSize().GC)

		// The tree case asserts assertRebuildsSame here. This one cannot,
		// and the difference is yorkie#2007 rather than anything this change
		// does: TextValue.DataSize counts removed attributes, so Live is
		// holding each tombstone's bytes, and collect only ever gives back
		// to GC -- so purging one strands its size in Live forever. Pinned
		// rather than skipped, so the day #2007 lands this says so.
		rebuilt, err := doc.InternalDocument().DeepCopy()
		require.NoError(t, err)
		stranded := doc.DocSize().Live
		stranded.Sub(rebuilt.DocSize().Live)
		assert.Equal(t, resource.DataSize{Data: 8, Meta: 48}, stranded,
			"two purged text attribute tombstones, each stranding its own "+
				"size in Live (yorkie#2007)")
		assert.Equal(t, doc.DocSize().GC, rebuilt.DocSize().GC, "gc still agrees")
		assert.Equal(t, doc.GarbageLen(), rebuilt.GarbageLen(), "count still agrees")
	})

	t.Run("drains gc whichever order collection reaches the pairs in", func(t *testing.T) {
		// A text node's DataSize counts its removed attributes, so charging
		// the node for them as well as charging each on its own booked the
		// same bytes twice. Purging the attribute shrinks the node, so the
		// double charge cancelled only when collection happened to reach the
		// node first -- and Go randomises map iteration order. Repeat enough
		// that a re-regression cannot pass by luck.
		for i := 0; i < 50; i++ {
			doc := textStyledAndRemoved(t)
			require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetText("k").Edit(5, 5, "X")
				return nil
			}))
			// Delete the half holding the copied tombstone, so both its pair
			// and its owner's pair are in the map at the same time.
			require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetText("k").Edit(6, 11, "")
				return nil
			}))

			doc.GarbageCollect(doc.VersionVector())
			require.Equal(t, 0, doc.GarbageLen(), "run %d", i)
			require.Equal(t, resource.DataSize{}, doc.DocSize().GC, "run %d", i)
		}
	})

	t.Run("purges the same tombstones on both replicas when the split is remote", func(t *testing.T) {
		d1, d2, a1, a2 := newReplicas(t)

		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("k").Edit(0, 0, "abcdefghij")
			return nil
		}))
		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("k").Style(0, 10, map[string]string{"b": "1"})
			return nil
		}))
		require.NoError(t, d1.Undo())
		crossSync(t, d1, d2)

		require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("k").Edit(5, 5, "X")
			return nil
		}))
		crossSync(t, d1, d2)

		assert.Equal(t, d1.Root().GetText("k").Marshal(), d2.Root().GetText("k").Marshal())
		assert.Equal(t, 2, d1.GarbageLen())
		assert.Equal(t, 2, d2.GarbageLen())
		assert.Equal(t, d1.DocSize().GC, d2.DocSize().GC)
		assertRebuildsSame(t, d1, "the replica that split")
		assertRebuildsSame(t, d2, "the replica the split arrived at")

		purged1 := d1.GarbageCollect(helper.MaxVersionVector(a1, a2))
		purged2 := d2.GarbageCollect(helper.MaxVersionVector(a1, a2))
		assert.Equal(t, purged1, purged2, "asymmetric purge: d1=%d d2=%d", purged1, purged2)
		assert.Equal(t, resource.DataSize{}, d1.DocSize().GC)
		assert.Equal(t, resource.DataSize{}, d2.DocSize().GC)
	})
}

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
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// moveToFrontByValue moves the element holding the given string to the front.
// Addressing by value rather than by index means a reorder cannot silently
// retarget a different element as the test goes on.
func moveToFrontByValue(t *testing.T, doc *document.Document, value string) {
	t.Helper()
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		arr := root.GetArray("a")
		for i := range arr.Len() {
			if arr.Get(i).Marshal() == `"`+value+`"` {
				arr.MoveFront(arr.Get(i).CreatedAt())
				return nil
			}
		}
		require.Fail(t, "element not found", value)
		return nil
	}))
}

// TestArrayMoveChargesTheMovedAtTicket is the document-level half of the
// MoveAfter fix. The running ledger skipped the movedAt ticket a move stamps,
// so it under-reported by 24 bytes per moved element -- bounded, but
// proportional to the array, and in the permissive direction: the document
// quietly exceeded its quota and the first load through a path that recomputes
// from content made the size jump with nothing in the content to explain it.
func TestArrayMoveChargesTheMovedAtTicket(t *testing.T) {
	newArray := func(t *testing.T) *document.Document {
		t.Helper()
		doc := document.New("move-ledger")
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("a").AddString("e0").AddString("e1").AddString("e2").AddString("e3")
			return nil
		}))
		return doc
	}

	t.Run("one move costs one ticket test", func(t *testing.T) {
		doc := newArray(t)
		before := doc.DocSize().Live

		moveToFrontByValue(t, doc, "e2")
		assert.Equal(t, before.Meta+time.TicketSize, doc.DocSize().Live.Meta)
		assert.Equal(t, before.Data, doc.DocSize().Live.Data)
		assertRebuildsSame(t, doc, "after one move")
	})

	t.Run("moving the same element again costs nothing test", func(t *testing.T) {
		doc := newArray(t)
		before := doc.DocSize().Live

		for range 3 {
			moveToFrontByValue(t, doc, "e2")
		}
		assert.Equal(t, before.Meta+time.TicketSize, doc.DocSize().Live.Meta,
			"the drift is per moved element, not per move")
		assertRebuildsSame(t, doc, "after three moves of one element")
	})

	t.Run("each distinct element costs one ticket test", func(t *testing.T) {
		for moved, values := range map[int][]string{
			2: {"e1", "e2"},
			3: {"e1", "e2", "e3"},
		} {
			doc := newArray(t)
			before := doc.DocSize().Live

			for _, v := range values {
				moveToFrontByValue(t, doc, v)
			}
			assert.Equal(t, before.Meta+moved*time.TicketSize, doc.DocSize().Live.Meta)
			assertRebuildsSame(t, doc, "after moving distinct elements")
		}
	})

	t.Run("the limit sees the same size a rebuild reports test", func(t *testing.T) {
		// The whole point of charging the ticket: MaxSizeLimit is enforced
		// against the accumulator, so anything the accumulator hides is quota
		// the document gets for free and loses on its next rebuild.
		doc := newArray(t)
		for _, v := range []string{"e1", "e2", "e3"} {
			moveToFrontByValue(t, doc, v)
		}

		rebuilt, err := doc.InternalDocument().DeepCopy()
		require.NoError(t, err)
		live, rebuiltLive := doc.DocSize().Live, rebuilt.DocSize().Live
		require.Equal(t, (&rebuiltLive).Total(), (&live).Total())

		doc.MaxSizeLimit = (&live).Total()
		assert.Error(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetArray("a").AddString("e4")
			return nil
		}), "an edit past a limit the rebuild already reaches must be refused")
	})
}

// TestArrayMoveOnATombstonedElement pins WHICH ledger the movedAt ticket lands
// in. MoveAfter stamps the ticket whether or not the element is already a
// tombstone -- a remove and a move issued concurrently is the ordinary way to
// reach that -- and a rebuild charges a tombstone's whole DataSize, movedAt
// included, to GC. Booking the ticket to Live there left 24 bytes in Live that
// no rebuild agrees with and that collection never takes back out.
func TestArrayMoveOnATombstonedElement(t *testing.T) {
	d1, d2, a1, a2 := newReplicas(t)

	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewArray("a").AddString("e0").AddString("e1").AddString("e2")
		return nil
	}))
	crossSync(t, d1, d2)

	// Concurrent: d1 deletes "e2", d2 moves it to the front. d1 therefore
	// applies the move to an element it has already tombstoned, and d2
	// tombstones an element it has already moved.
	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetArray("a").Delete(2)
		return nil
	}))
	require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
		arr := root.GetArray("a")
		arr.MoveFront(arr.Get(2).CreatedAt())
		return nil
	}))
	crossSync(t, d1, d2)

	require.Equal(t, d1.Marshal(), d2.Marshal(), "sanity: the replicas converged")
	assertRebuildsSame(t, d1, "move applied to a tombstone")
	assertRebuildsSame(t, d2, "tombstone set on a moved element")
	assert.Equal(t, d1.DocSize(), d2.DocSize(),
		"identical documents must report identical sizes whatever order they arrived in")

	// And the ticket must leave the ledger with the element it was stamped on.
	vv := d1.VersionVector().DeepCopy()
	for _, actor := range []time.ActorID{a1, a2} {
		if v := d2.VersionVector().VersionOf(actor); v > vv.VersionOf(actor) {
			vv.Set(actor, v)
		}
	}
	d1.GarbageCollect(vv)
	d2.GarbageCollect(vv)
	assert.Equal(t, d1.DocSize(), d2.DocSize(), "collection must leave both replicas equal")
	gc := d1.DocSize().GC
	assert.Zero(t, (&gc).Total(), "nothing left charged to GC")
	assertRebuildsSame(t, d1, "after collection")
}

// TestAttributeTombstoneDoesNotDependOnDeliveryOrder is the document-level half
// of the RHT.Remove fix. A tombstone that copied the value it replaced made its
// stored bytes a function of what had landed at that key when the removal
// arrived: a concurrent same-key style and removeStyle rendered identically in
// either order and charged differently.
func TestAttributeTombstoneDoesNotDependOnDeliveryOrder(t *testing.T) {
	d1, d2, a1, a2 := newReplicas(t)

	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{
			Type: "doc",
			Children: []json.TreeNode{{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "hello"}},
			}},
		})
		return nil
	}))
	crossSync(t, d1, d2)

	// Concurrent, on a key neither side has set: d1 styles it with a long
	// value, d2 removes it. d2's actor id is the higher one, so its removal
	// wins LWW on both replicas -- but d1 reaches the tombstone by replacing a
	// live value it applied first, and d2 by minting one over an absent key.
	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Style(0, 7, map[string]string{"b": "a-long-attribute-value"})
		return nil
	}))
	require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").RemoveStyle(0, 7, []string{"b"})
		return nil
	}))
	crossSync(t, d1, d2)

	require.Equal(t, d1.Marshal(), d2.Marshal(), "sanity: the two orders render the same")
	assert.Equal(t, d1.DocSize(), d2.DocSize(),
		"identical documents must report identical sizes whatever order they arrived in")
	assertRebuildsSame(t, d1, "style then remove")
	assertRebuildsSame(t, d2, "remove then style")

	// And the divergence must not merely be deferred to collection.
	vv := d1.VersionVector().DeepCopy()
	for _, actor := range []time.ActorID{a1, a2} {
		if v := d2.VersionVector().VersionOf(actor); v > vv.VersionOf(actor) {
			vv.Set(actor, v)
		}
	}
	d1.GarbageCollect(vv)
	d2.GarbageCollect(vv)
	assert.Equal(t, d1.DocSize(), d2.DocSize(), "collection must leave both replicas equal")
	gc := d1.DocSize().GC
	assert.Zero(t, (&gc).Total(), "nothing left charged to GC")
}

// TestAttributeTombstoneRacesTheNodesRemoval is the same invariant where the
// attribute's removal races the removal of the node carrying it, which is how
// the value the tombstone used to copy could end up charged to GC rather than
// to Live.
//
// The GC half of that compensation is pinned directly, at the CRDT layer, by
// TestRemoveStyleOnATombstonedNodeDebitsGC: the Tree's range traversal does not
// reach a node its own replica has already tombstoned, so this document-level
// shape exercises convergence and the rebuild, not that branch.
func TestAttributeTombstoneRacesTheNodesRemoval(t *testing.T) {
	d1, d2, a1, a2 := newReplicas(t)

	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{
			Type: "doc",
			Children: []json.TreeNode{
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "hello"}}},
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "world"}}},
			},
		})
		return nil
	}))
	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Style(0, 7, map[string]string{"b": "a-long-attribute-value"})
		return nil
	}))
	crossSync(t, d1, d2)

	// Concurrent: d1 deletes the styled paragraph, d2 strips the attribute
	// from it. d2's removal reaches d1 after the node is already a tombstone.
	require.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Edit(0, 7, nil, 0)
		return nil
	}))
	require.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").RemoveStyle(0, 7, []string{"b"})
		return nil
	}))
	crossSync(t, d1, d2)

	require.Equal(t, d1.Marshal(), d2.Marshal(), "sanity: the replicas converged")
	assertRebuildsSame(t, d1, "the attribute was stripped from a tombstone")
	assertRebuildsSame(t, d2, "the node was tombstoned under a stripped attribute")

	vv := d1.VersionVector().DeepCopy()
	for _, actor := range []time.ActorID{a1, a2} {
		if v := d2.VersionVector().VersionOf(actor); v > vv.VersionOf(actor) {
			vv.Set(actor, v)
		}
	}
	d1.GarbageCollect(vv)
	d2.GarbageCollect(vv)
	assert.Equal(t, 0, d1.GarbageLen())
	gc := d1.DocSize().GC
	assert.Zero(t, (&gc).Total(), "collection must not leave the dropped value behind")
}

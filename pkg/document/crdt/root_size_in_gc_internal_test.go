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

package crdt

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// sizeInGCFixture is a root holding {"o": {"a": "b"}} and a ticket source.
//
// It is an internal test because what it pins, the number of records
// sizeInGC holds, is not visible through any exported surface: a leaked
// record changes no size and no count, it only keeps memory.
type sizeInGCFixture struct {
	root    *Root
	actor   time.ActorID
	lamport int64
}

func newSizeInGCFixture(t *testing.T) *sizeInGCFixture {
	t.Helper()

	f := &sizeInGCFixture{
		root:  NewRoot(NewObject(NewElementRHT(), time.InitialTicket)),
		actor: time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
	}

	o := NewObject(NewElementRHT(), f.tick())
	a, err := NewPrimitive("b", f.tick())
	require.NoError(t, err)
	o.Set("a", a)
	f.root.Object().Set("o", o)
	f.root.RegisterElement(o, f.root.Object())

	return f
}

func (f *sizeInGCFixture) tick() *time.Ticket {
	f.lamport++
	return time.NewTicket(f.lamport, 0, f.actor)
}

// removeAndRestore removes key k of the given parent and restores it the way
// undoing that removal does: Set.Execute re-sets the copy the reverse took
// before the removal, under the element's original createdAt, and retires
// the tombstone's collection entry before registering the copy.
func (f *sizeInGCFixture) removeAndRestore(t *testing.T, parent *Object, k string) {
	t.Helper()

	restored, err := parent.Get(k).DeepCopy()
	require.NoError(t, err)

	removed, err := parent.DeleteByCreatedAt(restored.CreatedAt(), f.tick())
	require.NoError(t, err)
	f.root.RegisterRemovedElementPair(parent, removed)

	parent.SetWithExecutedAt(k, restored, f.tick())
	f.root.UnregisterRemovedElementPair(restored.CreatedAt())
	f.root.RegisterElement(restored, parent)
}

func (f *sizeInGCFixture) collect(t *testing.T) {
	t.Helper()

	vector := time.NewVersionVector()
	vector.Set(f.actor, time.MaxLamport)
	_, err := f.root.GarbageCollect(vector)
	require.NoError(t, err)
}

// assertRebuildsSame checks that the root's sizes are a function of its
// content: a root rebuilt from it reports the same numbers.
func (f *sizeInGCFixture) assertRebuildsSame(t *testing.T, msg string) {
	t.Helper()

	rebuilt, err := f.root.DeepCopy()
	require.NoError(t, err)
	assert.Equal(t, rebuilt.DocSize(), f.root.DocSize(), msg)
	assert.Equal(t, rebuilt.GarbageLen(), f.root.GarbageLen(), msg)
}

// TestReleasedSizeInGCRecordIsRetired pins that a remove/undo cycle does not
// leave a sizeInGC record behind.
//
// Undoing a removal orphans the tombstone's subtree, and release charges it
// to neither side by writing a zero record for each of its elements. Only
// deregisterElement ever deleted a record, and nothing deregisters an
// orphan, so every cycle added one record per element of the subtree -- and,
// because the map is keyed by the element, kept the whole dead subtree
// reachable for the life of the document. The JS SDK holds the same records
// in a WeakMap (yorkie-js-sdk#1395).
func TestReleasedSizeInGCRecordIsRetired(t *testing.T) {
	f := newSizeInGCFixture(t)

	f.removeAndRestore(t, f.root.Object(), "o")
	f.collect(t)
	assert.Empty(t, f.root.sizeInGC,
		"the orphaned subtree's records outlived the restore")

	for range 50 {
		f.removeAndRestore(t, f.root.Object(), "o")
		f.collect(t)
	}

	assert.Empty(t, f.root.sizeInGC,
		"each remove/undo cycle leaked the records of the orphaned subtree")
	assert.Zero(t, f.root.GarbageLen())
	assert.Equal(t, `{"o":{"a":"b"}}`, f.root.Object().Marshal())
	f.assertRebuildsSame(t, "after 51 remove/undo cycles")
}

// TestReleasedRecordStaysWhileAddressable pins what the zero record is for,
// so retiring it does not reach too far.
//
// A peer can add a member into a container after the undoing replica took
// its copy. The restore orphans that member along with the tombstone, but
// the copy does not carry it, so nothing takes over its elementMap slot and
// the peer can still edit inside it. A removal there moves the removed
// element's size to GC; without the release record it would take that size
// out of Live a second time.
func TestReleasedRecordStaysWhileAddressable(t *testing.T) {
	f := newSizeInGCFixture(t)
	o := f.root.Object().Get("o").(*Object)

	// The undoing replica's copy, taken before the peer's member lands.
	restored, err := o.DeepCopy()
	require.NoError(t, err)

	n := NewObject(NewElementRHT(), f.tick())
	y, err := NewPrimitive(1, f.tick())
	require.NoError(t, err)
	n.Set("y", y)
	o.Set("n", n)
	f.root.RegisterElement(n, o)

	removed, err := f.root.Object().DeleteByCreatedAt(o.CreatedAt(), f.tick())
	require.NoError(t, err)
	f.root.RegisterRemovedElementPair(f.root.Object(), removed)

	f.root.Object().SetWithExecutedAt("o", restored, f.tick())
	f.root.UnregisterRemovedElementPair(restored.CreatedAt())
	f.root.RegisterElement(restored, f.root.Object())

	assert.Same(t, n, f.root.FindByCreatedAt(n.CreatedAt()),
		"the peer's member is no longer addressable")
	assert.Contains(t, f.root.sizeInGC, Element(n))
	assert.Contains(t, f.root.sizeInGC, Element(y))
	assert.NotContains(t, f.root.sizeInGC, Element(o),
		"the tombstone the copy took over is still recorded")

	// The peer, which has not seen the removal, removes y inside n.
	live := f.root.DocSize().Live
	removedY, err := n.DeleteByCreatedAt(y.CreatedAt(), f.tick())
	require.NoError(t, err)
	f.root.RegisterRemovedElementPair(n, removedY)
	assert.Equal(t, live, f.root.DocSize().Live,
		"a released element's size was taken out of Live again")

	f.collect(t)
	assert.Zero(t, f.root.GarbageLen())
	f.assertRebuildsSame(t, "after collecting inside the orphan")
}

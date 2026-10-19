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

	"github.com/yorkie-team/yorkie/pkg/document/resource"
)

// TestChargeCancellingToZeroIsNotARelease pins that a GC charge whose sum
// lands on exactly zero is still a charge.
//
// A release records that neither side of the ledger holds an element; it used
// to say so by writing a zero into sizeInGC, which chargeToElement read back
// as a release marker and index retired as one. A negative diff that cancels
// the charge it is booked against -- a move ticket taken off a tombstone, say
// -- sums to the same zero without the element having been released, so from
// then on every charge on that still-collectable tombstone was dropped and its
// record could be retired under a restored copy. The mark lives in a set of
// its own now, so only release can set it.
func TestChargeCancellingToZeroIsNotARelease(t *testing.T) {
	f := newSizeInGCFixture(t)
	o, ok := f.root.Object().Get("o").(*Object)
	require.True(t, ok)

	removed, err := f.root.Object().DeleteByCreatedAt(o.CreatedAt(), f.tick())
	require.NoError(t, err)
	f.root.RegisterRemovedElementPair(f.root.Object(), removed)

	charged, held := f.root.sizeInGC[o]
	require.True(t, held, "the tombstone is charged to GC")
	require.NotEqual(t, resource.DataSize{}, charged)

	before := f.root.DocSize()
	gcBefore := before.GC
	f.root.AccMovedElement(o, resource.DataSize{Data: -charged.Data, Meta: -charged.Meta})
	require.Equal(t, resource.DataSize{}, f.root.sizeInGC[o], "the charge cancelled out")
	assert.NotContains(t, f.root.released, Element(o),
		"a cancelled charge was read back as a release")

	// The next charge still belongs to GC, and the record has to rise with it
	// or collecting the tombstone leaves the bytes in GC forever.
	f.root.AccMovedElement(o, resource.DataSize{Meta: charged.Meta})
	assert.Equal(t, resource.DataSize{Meta: charged.Meta}, f.root.sizeInGC[o])
	after := f.root.DocSize()
	assert.Equal(t, gcBefore.Total()-charged.Data, after.GC.Total())

	f.collect(t)
	assert.Empty(t, f.root.sizeInGC, "collecting the tombstone retires its record")
	collected := f.root.DocSize()
	assert.Zero(t, collected.GC.Total(), "GC keeps nothing of a collected tombstone")
}

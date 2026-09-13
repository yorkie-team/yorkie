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
)

// The successor barrier delays a purge, so its cost is retention. Retention is
// the axis that disqualified two other candidate fixes for the same defect:
// one retained a dead position node per moved element forever, which grew
// DocSize by 148% on a drag-reorder and — because DocSize.Total feeds
// MaxSizeLimit — made a 20-element array under a 1000-byte limit refuse the
// 8th of 19 drags.
//
// These tests pin the properties that distinguish an acceptable delay from an
// unacceptable leak, so a future change to the barrier cannot quietly acquire
// the cost profile that ruled those candidates out:
//
//   - retention is bounded by how far behind the collection vector is, not by
//     the size of the array;
//   - it drains completely once the vector catches up, back to no garbage and
//     no GC charge at all;
//   - an array under a byte limit still accepts every move.
//
// They are written as invariants rather than as golden numbers because the
// point is the shape of the cost, not one measurement of it.

// dragToFront moves the element at idx to the front of the array.
func dragToFront(t *testing.T, d *document.Document, idx int) {
	t.Helper()
	require.NoError(t, d.Update(func(root *json.Object, p *presence.Presence) error {
		arr := root.GetArray("arr")
		arr.MoveFront(arr.Get(idx).CreatedAt())
		return nil
	}))
}

// newStringArray seeds a document with an array of n distinct strings.
func newStringArray(t *testing.T, d *document.Document, n int) {
	t.Helper()
	require.NoError(t, d.Update(func(root *json.Object, p *presence.Presence) error {
		arr := root.SetNewArray("arr")
		for i := 0; i < n; i++ {
			arr.AddString(fmt.Sprintf("e%03d", i))
		}
		return nil
	}))
}

// TestBarrierRetentionIsBoundedByLagNotByArraySize drags every element of a
// 200-element array to the front, collecting after each move with a vector
// that lags by a fixed number of tickets. Retention must track the lag.
func TestBarrierRetentionIsBoundedByLagNotByArraySize(t *testing.T) {
	const n = 200

	for _, lag := range []int64{1, 5, 50} {
		t.Run(fmt.Sprintf("lag=%d", lag), func(t *testing.T) {
			doc := document.New("barrier-cost-lag")
			newStringArray(t, doc, n)

			actor := doc.ActorID()
			lagging := func() time.VersionVector {
				v := doc.VersionVector().DeepCopy()
				if cur := v.VersionOf(actor); cur > lag {
					v.Set(actor, cur-lag)
				}
				return v
			}

			doc.GarbageCollect(lagging())
			baseline := doc.DocSize()
			baselineTotal := (&baseline).Total()

			peak := 0
			for i := 1; i < n; i++ {
				dragToFront(t, doc, i)
				doc.GarbageCollect(lagging())
				if l := doc.GarbageLen(); l > peak {
					peak = l
				}
			}

			// Bounded by the lag. The array is 200 elements; a per-element
			// leak would show up here as a peak in the hundreds.
			assert.LessOrEqual(t, peak, int(lag),
				"retention must be bounded by the lag, not by the array size")

			// Drains completely once everyone has caught up.
			doc.GarbageCollect(doc.VersionVector())
			drained := doc.DocSize()
			assert.Equal(t, 0, doc.GarbageLen(), "retention must drain")
			assert.Zero(t, drained.GC.Data, "no data charged to GC after draining")
			assert.Zero(t, drained.GC.Meta, "no metadata charged to GC after draining")
			assert.Equal(t, baselineTotal, (&drained).Total(),
				"a fully synced document must cost exactly what it cost before the moves")
		})
	}
}

// TestBarrierKeepsAnArrayUnderItsSizeLimitMovable is the user-visible half.
// A candidate fix that retained a position node per move was refused at the
// 8th of 19 drags under a 1000-byte limit.
func TestBarrierKeepsAnArrayUnderItsSizeLimitMovable(t *testing.T) {
	doc := document.New("barrier-cost-limit")
	doc.MaxSizeLimit = 1000
	newStringArray(t, doc, 20)
	doc.GarbageCollect(doc.VersionVector())

	for i := 1; i < 20; i++ {
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.GetArray("arr")
			arr.MoveFront(arr.Get(i).CreatedAt())
			return nil
		}), "move %d of 19 must be accepted under the size limit", i)
		doc.GarbageCollect(doc.VersionVector())
	}

	assert.Equal(t, 0, doc.GarbageLen())
}

// TestBarrierCostsNothingOnConcurrentMoves exercises the case that can
// actually engage the barrier: two actors moving elements concurrently, synced
// through the faithful push/pull model with a server-computed minVV. A
// single-actor fixture cannot engage it, because lagging one actor's vector
// lags every ticket uniformly.
func TestBarrierCostsNothingOnConcurrentMoves(t *testing.T) {
	const n = 60
	const rounds = 25

	d1, d2, _, _ := newReplicas(t)
	rows := map[string]time.VersionVector{}

	newStringArray(t, d1, n)
	vpull(t, d2, vpush(t, d1, rows))
	rows[d2.ActorID().String()] = d2.VersionVector().DeepCopy()

	synced := d1.DocSize()
	syncedTotal := (&synced).Total()

	peak := 0
	for r := 0; r < rounds; r++ {
		// Concurrent: both move before either exchanges.
		dragToFront(t, d1, 1+(r*7)%(n-1))
		dragToFront(t, d2, 1+(r*13+3)%(n-1))

		c1 := vpush(t, d1, rows)
		c2 := vpush(t, d2, rows)
		vpull(t, d2, c1)
		vpull(t, d1, c2)

		minVV := serverMinVV(rows)
		d1.GarbageCollect(minVV)
		d2.GarbageCollect(minVV)

		for _, d := range []*document.Document{d1, d2} {
			if l := d.GarbageLen(); l > peak {
				peak = l
			}
		}
	}

	// Two concurrent movers can leave at most one in-flight slot each.
	assert.LessOrEqual(t, peak, 2,
		"concurrent moves must not accumulate retention across rounds")

	rows[d1.ActorID().String()] = d1.VersionVector().DeepCopy()
	rows[d2.ActorID().String()] = d2.VersionVector().DeepCopy()
	final := serverMinVV(rows)
	d1.GarbageCollect(final)
	d2.GarbageCollect(final)

	assert.Equal(t, d1.Marshal(), d2.Marshal(), "replicas must agree")
	assert.Equal(t, 0, d1.GarbageLen())
	assert.Equal(t, 0, d2.GarbageLen())

	drained := d1.DocSize()
	assert.Equal(t, syncedTotal, (&drained).Total(),
		"moving elements around and syncing must not change what the document costs")
}

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

package crdt_test

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func TestHLL(t *testing.T) {
	t.Run("new HLL has zero count", func(t *testing.T) {
		hll := crdt.NewHLL()
		assert.Equal(t, uint64(0), hll.Count())
	})

	t.Run("add single element returns true", func(t *testing.T) {
		hll := crdt.NewHLL()
		added := hll.Add("user-1")
		assert.True(t, added)
		assert.Equal(t, uint64(1), hll.Count())
	})

	t.Run("add duplicate element returns false", func(t *testing.T) {
		hll := crdt.NewHLL()
		hll.Add("user-1")
		added := hll.Add("user-1")
		assert.False(t, added)
		assert.Equal(t, uint64(1), hll.Count())
	})

	t.Run("count many unique elements within error margin", func(t *testing.T) {
		hll := crdt.NewHLL()
		n := 100000
		for i := range n {
			hll.Add(fmt.Sprintf("user-%d", i))
		}
		count := hll.Count()
		errorRate := math.Abs(float64(count)-float64(n)) / float64(n)
		assert.Less(t, errorRate, 0.05) // within 5%
	})

	t.Run("merge two HLLs", func(t *testing.T) {
		hll1 := crdt.NewHLL()
		hll2 := crdt.NewHLL()

		hll1.Add("user-1")
		hll1.Add("user-2")

		hll2.Add("user-2")
		hll2.Add("user-3")

		hll1.Merge(hll2)
		assert.Equal(t, uint64(3), hll1.Count())
	})

	t.Run("merge is idempotent", func(t *testing.T) {
		hll1 := crdt.NewHLL()
		hll2 := crdt.NewHLL()

		hll1.Add("user-1")
		hll2.Add("user-1")

		hll1.Merge(hll2)
		assert.Equal(t, uint64(1), hll1.Count())
	})

	t.Run("serialize and restore", func(t *testing.T) {
		hll := crdt.NewHLL()
		hll.Add("user-1")
		hll.Add("user-2")
		hll.Add("user-3")
		originalCount := hll.Count()

		bytes := hll.Bytes()
		assert.Equal(t, 1<<14, len(bytes)) // precision 14 = 16384 registers

		restored := crdt.NewHLL()
		err := restored.Restore(bytes)
		assert.NoError(t, err)
		assert.Equal(t, originalCount, restored.Count())
	})

	t.Run("restore rejects a payload of the wrong length", func(t *testing.T) {
		assert.Error(t, crdt.NewHLL().Restore(make([]byte, (1<<14)-1)))
		assert.Error(t, crdt.NewHLL().Restore(make([]byte, (1<<14)+1)))
	})

	t.Run("restore accepts the largest value add can write", func(t *testing.T) {
		data := make([]byte, 1<<14)
		data[0] = 51 // 64 - precision + 1
		data[(1<<14)-1] = 51

		hll := crdt.NewHLL()
		assert.NoError(t, hll.Restore(data))
		assert.Equal(t, uint64(2), hll.Count())
	})

	t.Run("restore clamps a register above the largest value add can write", func(t *testing.T) {
		// Such a payload cannot come from Add, but it may already sit in
		// storage, so Restore repairs it instead of making the document
		// unloadable.
		for _, val := range []byte{52, 200, 255} {
			data := make([]byte, 1<<14)
			data[0] = val

			hll := crdt.NewHLL()
			assert.NoError(t, hll.Restore(data), "register %d", val)
			assert.Equal(t, byte(51), hll.Bytes()[0], "register %d", val)
			assert.Equal(t, uint64(1), hll.Count(), "register %d", val)
		}
	})

	t.Run("restore of a clamped payload is idempotent", func(t *testing.T) {
		data := make([]byte, 1<<14)
		data[7] = 200

		first := crdt.NewHLL()
		assert.NoError(t, first.Restore(data))
		second := crdt.NewHLL()
		assert.NoError(t, second.Restore(first.Bytes()))
		assert.Equal(t, first.Bytes(), second.Bytes())
	})

	t.Run("count saturates instead of wrapping on an all-max payload", func(t *testing.T) {
		data := make([]byte, 1<<14)
		for i := range data {
			data[i] = 51
		}

		hll := crdt.NewHLL()
		assert.NoError(t, hll.Restore(data))
		// The raw estimate here is ~2.7e19, past both int32 and uint64. The
		// count must stay inside the int32 the dedup counter stores it in, so
		// neither the float conversion nor the counter wraps it negative.
		count := hll.Count()
		assert.Equal(t, uint64(math.MaxInt32), count)
		assert.Positive(t, int32(count))
	})

	t.Run("dedup counter stays positive on an all-max payload", func(t *testing.T) {
		data := make([]byte, 1<<14)
		for i := range data {
			data[i] = 51
		}

		counter, err := crdt.NewCounter(crdt.IntegerDedupCnt, int32(0), time.InitialTicket)
		assert.NoError(t, err)
		assert.NoError(t, counter.RestoreHLL(data))
		assert.Equal(t, int32(math.MaxInt32), counter.Value())
	})
}
